package httpCollapseProxy

import (
	"context"
	"errors"
	"io"
	"net/http"
	"sync"
	"time"
)

// HTTP Proxy that takes request and returns response
type HttpProxy interface {
	Do(req *http.Request) (*http.Response, error)
}

// Errors
var (
	ErrReadingCommenced = errors.New("ReadingCommenced")
)

// Internal : Entry for collapsing requests
type responseState struct {
	lock    sync.Mutex
	reader  *MultiTeeReaderWithFullRead
	waiters []chan http.Response
}

// constructor
func newResponseState() *responseState {
	return &responseState{
		lock:    sync.Mutex{},
		reader:  nil,
		waiters: []chan http.Response{},
	}
}

// process the response once received
// sets up the CopyReader for all waiters
// Builds Response object with duplicate readers for each waiter
// unblocks the waiters by sending the copy Response
func (r *responseState) handleResponse(ctx context.Context, resp http.Response) (*http.Response, error) {
	r.lock.Lock()
	defer r.lock.Unlock()
	// waiters are present
	var writers []io.WriteCloser
	if resp.Body != nil {
		// list of upstream writers
		writers = make([]io.WriteCloser, 0, len(r.waiters))
	}
	// for each waiter
	for _, waiter := range r.waiters {
		// copy the response object
		copyResp := resp
		if resp.Body != nil {
			// create a pipe
			rH, wH := io.Pipe()
			// add writer to the upstream write list
			writers = append(writers, wH)
			// add reader to the downstream read as resp.Body
			copyResp.Body = rH
		}
		// unblock waiter with
		select {
		case <-ctx.Done():
			return nil, ctx.Err()
		case waiter <- copyResp:
		}
	}
	if resp.Body != nil {
		// link the upstream reader with list of writers to copy to
		r.reader = NewMultiTeeReaderWithFullRead(resp.Body, writers)
		// original response include the MultiReader for copy
		resp.Body = r.reader
	}
	return &resp, nil
}

// adds a waiter to existing request
func (r *responseState) addWaiter(ch chan http.Response) error {
	r.lock.Lock()
	defer r.lock.Unlock()
	if r.reader != nil {
		return ErrReadingCommenced
	}
	r.waiters = append(r.waiters, ch)
	return nil
}

type HttpCollapseProxy struct {
	ctx      context.Context
	proxy    HttpProxy
	requests map[string]*responseState
	lock     sync.Mutex
}

// Create Map key from request
func makeKey(req *http.Request) string {
	return req.URL.String()
}

// Constructor
func NewHttpCollapseProxy(ctx context.Context, proxy HttpProxy) HttpCollapseProxy {
	return HttpCollapseProxy{
		ctx:      ctx,
		proxy:    proxy,
		requests: make(map[string]*responseState),
		lock:     sync.Mutex{},
	}
}

// Checks if already an same request exists
// if present adds this to the same if possible
// if not possible, will create a new request
// Returns a channel to wait for Response
func (p *HttpCollapseProxy) lookupAndAddDependent(ctx context.Context, req *http.Request) (chan http.Response, error) {
	// create key
	key := makeKey(req)
	// channel to accept response
	var entry *responseState
	var ok bool
	var newEntry bool
	// create channel to be responded
	ch := make(chan http.Response)
	toCloseCh := false
	if toCloseCh {
		close(ch)
	}
	for {
		newEntry = false
		entry, ok = p.requests[key]
		if !ok {
			// create the entry
			func() {
				p.lock.Lock()
				defer p.lock.Unlock()
				// check again
				entry, ok = p.requests[key]
				if !ok {
					// not present
					// create new
					entry = newResponseState()
					// DONOT Add the channel for the first entry
					// entry.waiters = append(entry.waiters, ch)
					p.requests[key] = entry
					newEntry = true
				}
			}()
		}
		// entry must be valid
		if !newEntry {
			//second entry
			err := entry.addWaiter(ch)
			if err != nil {
				switch err {
				case ErrReadingCommenced:
					// unable to add due to Channel Reading already commenced
					func() {
						p.lock.Lock()
						defer p.lock.Unlock()
						// swap the old entry to new
						keyOld := key + time.Now().String()
						p.requests[keyOld] = p.requests[key]
					}()
					continue
				}
				toCloseCh = true
				// other error
				return nil, err
			}
		}
		break //loop
	}
	if newEntry {
		// first request ... do Proxy Do in background
		go func(ctx context.Context) {
			resp, err := p.proxy.Do(req)
			if err != nil {
				// if err build a response with error
				resp = &http.Response{
					Status:     err.Error(),
					StatusCode: http.StatusInternalServerError,
					Request:    req,
				}
			}
			if resp == nil {
				// if err build a response with error
				resp = &http.Response{
					Status:     "upstream response is nil",
					StatusCode: http.StatusInternalServerError,
					Request:    req,
				}
			}
			// setup the resp for distribution to all other waiters
			resp, err = entry.handleResponse(p.ctx, *resp)
			if err != nil {
				return
			}
			// write to first channel
			// who will need to do the READ for other distributions
			select {
			case <-ctx.Done():
				return
			case ch <- *resp:
			}
		}(ctx)
	}
	return ch, nil
}

func (p *HttpCollapseProxy) Do(req *http.Request) (*http.Response, error) {
	// check and create backend request
	ch, err := p.lookupAndAddDependent(p.ctx, req)
	if err != nil {
		return nil, err
	}
	// if request context is not set
	if req.Context() == nil {
		resp := <-ch
		// get response from channel and respond
		return &resp, nil
	}
	// request context is set
	select {
	case <-req.Context().Done():
		//start a go routine to drain channel
		go func() {
			resp := <-ch
			if resp.Body != nil {
				// close the body
				resp.Body.Close()
			}
		}()
		// return error
		return nil, req.Context().Err()
	case resp := <-ch:
		// get response from channel and respond
		return &resp, nil
	}
}

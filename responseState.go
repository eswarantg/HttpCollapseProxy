package httpCollapseProxy

import (
	"context"
	"io"
	"net/http"
	"sync"
)

// Internal : Entry for collapsing requests
type responseState struct {
	lock     sync.Mutex
	reader   *MultiTeeReaderWithFullRead
	waiters  []chan http.Response
	respRecd *http.Response
}

// constructor
func newResponseState() *responseState {
	return &responseState{
		lock:     sync.Mutex{},
		reader:   nil,
		waiters:  []chan http.Response{},
		respRecd: nil,
	}
}

// process the response once received
// sets up the CopyReader for all waiters
// Builds Response object with duplicate readers for each waiter
// unblocks the waiters by sending the copy Response
func (r *responseState) handleResponse(ctx context.Context, resp http.Response) (*http.Response, error) {
	r.lock.Lock()
	defer r.lock.Unlock()

	// Save the resp object
	copyRespRecd := resp
	copyRespRecd.Body = nil
	r.respRecd = &copyRespRecd

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
func (r *responseState) addWaiter(ctx context.Context, ch chan http.Response) error {
	r.lock.Lock()
	defer r.lock.Unlock()
	if r.reader != nil {
		copyResp := *r.respRecd
		if len(r.reader.writers) > 0 {
			// create a pipe
			rH, wH := io.Pipe()
			err := r.reader.AddWriter(wH)
			if err != nil {
				wH.Close()
				rH.Close()
				return err
			}
			// add reader to the downstream read as resp.Body
			copyResp.Body = rH
			select {
			case <-ctx.Done():
				return ctx.Err()
			case ch <- copyResp:
			}
		}
		return nil
	}
	r.waiters = append(r.waiters, ch)
	return nil
}

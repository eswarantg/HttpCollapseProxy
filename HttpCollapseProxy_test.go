package httpCollapseProxy_test

import (
	"bytes"
	"context"
	"fmt"
	"github.com/eswarantg/httpCollapseProxy"
	"io"
	"net/http"
	"strings"
	"sync"
	"sync/atomic"
	"time"
)

type TestProxy struct {
	counter  int32
	response string
}

func (t *TestProxy) Do(req *http.Request) (*http.Response, error) {
	atomic.AddInt32(&t.counter, 1)
	time.Sleep(500 * time.Millisecond)
	rdr := io.NopCloser(bytes.NewReader([]byte(t.response)))
	return &http.Response{StatusCode: http.StatusOK, Body: rdr}, nil
}

func ExampleHttpCollapseProxy() {
	client := &TestProxy{
		counter:  0,
		response: "Response",
	}
	// Can be a HTTP Client
	//client := &http.Client{}
	NClients := 10
	wg := sync.WaitGroup{}

	hp := httpCollapseProxy.NewHttpCollapseProxy(context.Background(), client)
	var responseCount int32
	for i := 0; i < NClients; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			req, _ := http.NewRequest(http.MethodGet, "google.com", nil)
			req = req.WithContext(req.Context())
			resp, err := hp.Do(req)
			if err != nil {
				fmt.Printf("Error:%v", err)
				return
			}
			defer resp.Body.Close()
			body, _ := io.ReadAll(resp.Body)
			cmpRes := strings.Compare(string(body), client.response)
			if cmpRes == 0 {
				atomic.AddInt32(&responseCount, 1)
			}
		}(i)
	}
	wg.Wait()
	fmt.Printf("Req:%v Resp:%v", client.counter, responseCount)
	//Output:Req:1 Resp:10
}

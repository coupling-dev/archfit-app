package anthropic

import (
	"errors"
	"io"
	"net/http"
	"strings"
	"sync"
)

// step answers one request of the fake vendor.
type step func(req *http.Request) (*http.Response, error)

type recordedRequest struct {
	Method string
	URL    string
	Host   string
	Header http.Header
	Body   []byte
}

// fakeVendor is the http.RoundTripper standing in for the Anthropic Messages API.
// Steps are consumed in order; the last one repeats.
type fakeVendor struct {
	mu       sync.Mutex
	steps    []step
	requests []recordedRequest
	returned int
}

func newFakeVendor(steps ...step) *fakeVendor {
	return &fakeVendor{steps: steps}
}

func (f *fakeVendor) RoundTrip(req *http.Request) (*http.Response, error) {
	var body []byte
	if req.Body != nil {
		body, _ = io.ReadAll(req.Body)
		_ = req.Body.Close()
	}
	f.mu.Lock()
	f.requests = append(f.requests, recordedRequest{
		Method: req.Method,
		URL:    req.URL.String(),
		Host:   req.URL.Host,
		Header: req.Header.Clone(),
		Body:   body,
	})
	s := f.steps[min(len(f.requests), len(f.steps))-1]
	f.mu.Unlock()

	resp, err := s(req)
	if resp != nil {
		resp.Request = req
	}
	f.mu.Lock()
	f.returned++
	f.mu.Unlock()
	return resp, err
}

func (f *fakeVendor) setSteps(steps ...step) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.steps = steps
	f.requests = nil
	f.returned = 0
}

func (f *fakeVendor) recorded() []recordedRequest {
	f.mu.Lock()
	defer f.mu.Unlock()
	return append([]recordedRequest(nil), f.requests...)
}

// inFlight is the number of RoundTrip calls that have not returned.
func (f *fakeVendor) inFlight() int {
	f.mu.Lock()
	defer f.mu.Unlock()
	return len(f.requests) - f.returned
}

func respond(status int, body string, headers ...string) step {
	return func(*http.Request) (*http.Response, error) {
		h := http.Header{}
		for i := 0; i+1 < len(headers); i += 2 {
			h.Set(headers[i], headers[i+1])
		}
		return &http.Response{
			StatusCode: status,
			Header:     h,
			Body:       io.NopCloser(strings.NewReader(body)),
		}, nil
	}
}

// hang blocks until the request context ends, like a vendor that never answers.
func hang(req *http.Request) (*http.Response, error) {
	<-req.Context().Done()
	return nil, req.Context().Err()
}

func transportError(*http.Request) (*http.Response, error) {
	return nil, errors.New("connection reset by peer")
}

// countingBody serves size bytes and counts how many the client read.
type countingBody struct {
	mu     sync.Mutex
	size   int
	served int
}

func (b *countingBody) Read(p []byte) (int, error) {
	b.mu.Lock()
	defer b.mu.Unlock()
	if b.served >= b.size {
		return 0, io.EOF
	}
	n := min(len(p), b.size-b.served)
	for i := range p[:n] {
		p[i] = 'a'
	}
	b.served += n
	return n, nil
}

func (b *countingBody) Close() error { return nil }

func (b *countingBody) read() int {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.served
}

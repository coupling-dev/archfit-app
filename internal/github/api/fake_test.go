package api

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"sync"
	"testing"
	"time"
)

const blobHost = "blob.example"

// recorded is one request as the fake GitHub saw it.
type recorded struct {
	Method      string
	Host        string
	Path        string // escaped
	Query       url.Values
	Auth        string
	IfNoneMatch string
	Header      http.Header
	Body        []byte
}

func (r recorded) String() string { return r.Method + " " + r.Path }

// fakeGitHub is an httptest server reached through the client's transport: every host is routed to it and the
// handler sees the original host in r.Host. Nothing leaves the process.
type fakeGitHub struct {
	srv *httptest.Server

	mu   sync.Mutex
	reqs []recorded
	fail error // when set, RoundTrip fails without reaching the server
}

func newFake(t *testing.T, h http.Handler) *fakeGitHub {
	t.Helper()
	f := &fakeGitHub{}
	f.srv = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, _ := io.ReadAll(r.Body)
		r.Body = io.NopCloser(bytes.NewReader(body))
		f.mu.Lock()
		f.reqs = append(f.reqs, recorded{
			Method: r.Method, Host: r.Host, Path: r.URL.EscapedPath(), Query: r.URL.Query(),
			Auth: r.Header.Get("Authorization"), IfNoneMatch: r.Header.Get("If-None-Match"),
			Header: r.Header.Clone(), Body: body,
		})
		f.mu.Unlock()
		h.ServeHTTP(w, r)
	}))
	t.Cleanup(f.srv.Close)
	return f
}

func (f *fakeGitHub) RoundTrip(req *http.Request) (*http.Response, error) {
	f.mu.Lock()
	fail := f.fail
	f.mu.Unlock()
	if fail != nil {
		return nil, fail
	}
	target, err := url.Parse(f.srv.URL)
	if err != nil {
		return nil, err
	}
	out := req.Clone(req.Context())
	out.Host = req.URL.Host
	out.URL.Scheme, out.URL.Host = target.Scheme, target.Host
	return http.DefaultTransport.RoundTrip(out)
}

func (f *fakeGitHub) requests() []recorded {
	f.mu.Lock()
	defer f.mu.Unlock()
	return append([]recorded(nil), f.reqs...)
}

func (f *fakeGitHub) reset() {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.reqs = nil
}

func (f *fakeGitHub) count(method, path string) int {
	n := 0
	for _, r := range f.requests() {
		if r.Method == method && r.Path == path {
			n++
		}
	}
	return n
}

// fakeClock backs the client's now and sleep hooks: sleeping advances the clock and never blocks.
type fakeClock struct {
	mu      sync.Mutex
	t       time.Time
	sleeps  []time.Duration
	onSleep func(n int) // called before the n-th sleep (1-based) returns
}

func (c *fakeClock) now() time.Time {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.t
}

func (c *fakeClock) sleep(ctx context.Context, d time.Duration) error {
	c.mu.Lock()
	c.sleeps = append(c.sleeps, d)
	c.t = c.t.Add(d)
	n, hook := len(c.sleeps), c.onSleep
	c.mu.Unlock()
	if hook != nil {
		hook(n)
	}
	return ctx.Err()
}

func (c *fakeClock) slept() []time.Duration {
	c.mu.Lock()
	defer c.mu.Unlock()
	return append([]time.Duration{}, c.sleeps...)
}

func (c *fakeClock) total() time.Duration {
	var sum time.Duration
	for _, d := range c.slept() {
		sum += d
	}
	return sum
}

// stubTokens is a recording TokenSource. Installation tokens change after Invalidate.
type stubTokens struct {
	mu          sync.Mutex
	prefix      string
	gen         map[int64]int
	minted      []int64
	invalidated []int64
	jwtCalls    int
	instErr     error
}

func newStubTokens() *stubTokens { return &stubTokens{gen: map[int64]int{}} }

func (s *stubTokens) AppJWT() (string, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.jwtCalls++
	return s.prefix + "app-jwt", nil
}

func (s *stubTokens) InstallationToken(_ context.Context, id int64) (string, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.instErr != nil {
		return "", s.instErr
	}
	s.minted = append(s.minted, id)
	return fmt.Sprintf("%sinst-%d-v%d", s.prefix, id, s.gen[id]), nil
}

func (s *stubTokens) Invalidate(id int64) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.invalidated = append(s.invalidated, id)
	s.gen[id]++
}

func (s *stubTokens) RepoReadToken(context.Context, int64, string) (string, error) {
	return "", errors.New("RepoReadToken must never be called by api")
}

func (s *stubTokens) mintedFor(id int64) int {
	s.mu.Lock()
	defer s.mu.Unlock()
	n := 0
	for _, m := range s.minted {
		if m == id {
			n++
		}
	}
	return n
}

type testEnv struct {
	c     *Client
	gh    *fakeGitHub
	ts    *stubTokens
	clock *fakeClock
}

func newEnv(t *testing.T, h http.Handler) *testEnv {
	t.Helper()
	gh := newFake(t, h)
	ts := newStubTokens()
	clock := &fakeClock{t: time.Unix(1_800_000_000, 0)}
	c := New(ts, &http.Client{Transport: gh})
	c.now, c.sleep = clock.now, clock.sleep
	return &testEnv{c: c, gh: gh, ts: ts, clock: clock}
}

// reply is one scripted response.
type reply struct {
	status int
	header map[string]string
	body   string
}

func (r reply) write(w http.ResponseWriter) {
	for k, v := range r.header {
		w.Header().Set(k, v)
	}
	w.Header().Set("X-GitHub-Request-Id", "REQ-1")
	w.WriteHeader(r.status)
	_, _ = io.WriteString(w, r.body)
}

// script answers requests in order, whatever they are; an exhausted script answers 599.
func script(replies ...reply) http.HandlerFunc {
	var mu sync.Mutex
	return func(w http.ResponseWriter, _ *http.Request) {
		mu.Lock()
		if len(replies) == 0 {
			mu.Unlock()
			w.WriteHeader(599)
			return
		}
		next := replies[0]
		replies = replies[1:]
		mu.Unlock()
		next.write(w)
	}
}

func writeJSON(w http.ResponseWriter, status int, v any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(v)
}

// etagJSON answers 304 when the request carries the current ETag of v, else 200 with it.
func etagJSON(w http.ResponseWriter, r *http.Request, etag string, v any) {
	if r.Header.Get("If-None-Match") == etag {
		w.WriteHeader(http.StatusNotModified)
		return
	}
	w.Header().Set("ETag", etag)
	writeJSON(w, http.StatusOK, v)
}

func decodeBody(t *testing.T, r recorded) map[string]any {
	t.Helper()
	var m map[string]any
	if err := json.Unmarshal(r.Body, &m); err != nil {
		t.Fatalf("%s: request body is not JSON: %v", r, err)
	}
	return m
}

func findRequest(t *testing.T, reqs []recorded, method, path string) recorded {
	t.Helper()
	for _, r := range reqs {
		if r.Method == method && r.Path == path {
			return r
		}
	}
	t.Fatalf("no %s %s among %v", method, path, reqs)
	return recorded{}
}

func bearer(token string) string { return "Bearer " + token }

// appHandler answers GET /app for tests that need the App's identity.
func appHandler(w http.ResponseWriter, _ *http.Request) {
	writeJSON(w, http.StatusOK, map[string]any{"id": 4242, "slug": "archfit"})
}

func linkHeader(path string, page int) string {
	sep := "?"
	if strings.Contains(path, "?") {
		sep = "&"
	}
	return fmt.Sprintf(`<https://api.github.com%s%sper_page=100&page=%d>; rel="next", <https://api.github.com%s%spage=99>; rel="last"`,
		path, sep, page, path, sep)
}

var repoA = RepoRef{InstallationID: 1, FullName: "o/r"}

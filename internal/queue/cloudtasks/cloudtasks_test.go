package cloudtasks_test

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"

	"github.com/alexei-led/archfit-app/internal/queue"
	"github.com/alexei-led/archfit-app/internal/queue/cloudtasks"
	"github.com/alexei-led/archfit-app/internal/queue/queuetest"
)

const (
	testQueue    = "projects/coupling-test/locations/europe-west1/queues/webhooks"
	testURL      = "https://worker.example/internal/webhooks"
	testAudience = "https://worker.example"
	testInvoker  = "invoker@coupling-test.iam.gserviceaccount.com"
	testToken    = "MARKER-access-token"
)

type staticTokens struct {
	token string
	err   error
}

func (s staticTokens) Token(context.Context) (string, error) { return s.token, s.err }

// created is one tasks.create request as the fake API decoded it.
type created struct {
	path, auth string
	Task       struct {
		Name        string `json:"name"`
		HTTPRequest struct {
			URL        string            `json:"url"`
			HTTPMethod string            `json:"httpMethod"`
			Headers    map[string]string `json:"headers"`
			Body       []byte            `json:"body"`
			OIDCToken  struct {
				ServiceAccountEmail string `json:"serviceAccountEmail"`
				Audience            string `json:"audience"`
			} `json:"oidcToken"`
		} `json:"httpRequest"`
	} `json:"task"`
}

// fakeTasks is the Cloud Tasks API: it refuses a repeated name with ALREADY_EXISTS, as the real service does
// for the deduplication window, and dispatches each new task to deliver at once.
type fakeTasks struct {
	mu       sync.Mutex
	names    map[string]bool
	requests []created
	status   int    // when set, every create answers this status
	reply    string // with this body
	deliver  queue.Handler
}

func (f *fakeTasks) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	var c created
	raw, _ := io.ReadAll(r.Body)
	if err := json.Unmarshal(raw, &c); err != nil {
		http.Error(w, "bad json", http.StatusBadRequest)
		return
	}
	c.path, c.auth = r.URL.Path, r.Header.Get("Authorization")
	f.mu.Lock()
	f.requests = append(f.requests, c)
	if f.status != 0 {
		f.mu.Unlock()
		w.WriteHeader(f.status)
		_, _ = io.WriteString(w, f.reply)
		return
	}
	if f.names[c.Task.Name] {
		f.mu.Unlock()
		w.WriteHeader(http.StatusConflict)
		_, _ = io.WriteString(w, `{"error":{"code":409,"message":"Requested entity already exists","status":"ALREADY_EXISTS"}}`)
		return
	}
	f.names[c.Task.Name] = true
	f.mu.Unlock()
	if f.deliver != nil {
		h := http.Header{}
		for k, v := range c.Task.HTTPRequest.Headers {
			h.Set(k, v)
		}
		f.deliver(r.Context(), queue.Task{Name: c.Task.Name[strings.LastIndex(c.Task.Name, "/")+1:], Header: h, Body: c.Task.HTTPRequest.Body})
	}
	_, _ = io.WriteString(w, `{"name":"`+c.Task.Name+`"}`)
}

func (f *fakeTasks) sent() []created {
	f.mu.Lock()
	defer f.mu.Unlock()
	return append([]created(nil), f.requests...)
}

func newDriver(t *testing.T, f *fakeTasks, ts cloudtasks.TokenSource) queue.Queue {
	t.Helper()
	if f.names == nil {
		f.names = map[string]bool{}
	}
	srv := httptest.NewServer(f)
	t.Cleanup(srv.Close)
	q, err := cloudtasks.New(cloudtasks.Config{
		Queue: testQueue, URL: testURL, Audience: testAudience, Invoker: testInvoker, Endpoint: srv.URL,
	}, ts, srv.Client())
	if err != nil {
		t.Fatal(err)
	}
	return q
}

func TestCloudTasksConformance(t *testing.T) {
	queuetest.Run(t, func(t *testing.T, h queue.Handler) queue.Queue {
		return newDriver(t, &fakeTasks{deliver: h}, staticTokens{token: testToken})
	})
}

func TestCreateRequestShape(t *testing.T) {
	f := &fakeTasks{}
	q := newDriver(t, f, staticTokens{token: testToken})
	h := http.Header{}
	h.Set("X-GitHub-Delivery", "d-1")
	h.Set("x-hub-signature-256", "sha256=ab")
	if err := q.Enqueue(t.Context(), queue.Task{Name: "d-1_gate", Header: h, Body: []byte(`{"a":1}`)}); err != nil {
		t.Fatal(err)
	}
	reqs := f.sent()
	if len(reqs) != 1 {
		t.Fatalf("requests = %d, want 1", len(reqs))
	}
	c := reqs[0]
	hr := c.Task.HTTPRequest
	checks := []struct{ field, got, want string }{
		{"path", c.path, "/v2/" + testQueue + "/tasks"},
		{"authorization", c.auth, "Bearer " + testToken},
		{"task name", c.Task.Name, testQueue + "/tasks/d-1_gate"},
		{"url", hr.URL, testURL},
		{"method", hr.HTTPMethod, http.MethodPost},
		{"body", string(hr.Body), `{"a":1}`},
		{"oidc e-mail", hr.OIDCToken.ServiceAccountEmail, testInvoker},
		{"oidc audience", hr.OIDCToken.Audience, testAudience},
		{"delivery header", hr.Headers["X-Github-Delivery"], "d-1"},
		{"signature header", hr.Headers["X-Hub-Signature-256"], "sha256=ab"},
	}
	for _, ch := range checks {
		if ch.got != ch.want {
			t.Errorf("%s = %q, want %q", ch.field, ch.got, ch.want)
		}
	}
}

func TestDuplicateNameIsDeduplicated(t *testing.T) {
	var delivered int
	f := &fakeTasks{deliver: func(context.Context, queue.Task) { delivered++ }}
	q := newDriver(t, f, staticTokens{token: testToken})
	for i := range 3 {
		if err := q.Enqueue(t.Context(), queue.Task{Name: "d-7_control", Body: []byte("{}")}); err != nil {
			t.Fatalf("Enqueue %d = %v; a repeated name is success, never a reason to process inline", i+1, err)
		}
	}
	if delivered != 1 {
		t.Errorf("delivered %d times; want 1", delivered)
	}
}

func TestEnqueueFailures(t *testing.T) {
	tests := []struct {
		name        string
		status      int
		reply       string
		tokens      staticTokens
		task        queue.Task
		wantInvalid bool
		wantSent    int
	}{
		{"409 ABORTED is not a duplicate", http.StatusConflict, `{"error":{"code":409,"status":"ABORTED"}}`, staticTokens{token: testToken}, queue.Task{Name: "n"}, false, 1},
		{"409 without a body is not a duplicate", http.StatusConflict, ``, staticTokens{token: testToken}, queue.Task{Name: "n"}, false, 1},
		{"server error", http.StatusServiceUnavailable, `{"error":{"status":"UNAVAILABLE"}}`, staticTokens{token: testToken}, queue.Task{Name: "n"}, false, 1},
		{"rate limited", http.StatusTooManyRequests, `{"error":{"status":"RESOURCE_EXHAUSTED"}}`, staticTokens{token: testToken}, queue.Task{Name: "n"}, false, 1},
		{"permission denied", http.StatusForbidden, `{"error":{"status":"PERMISSION_DENIED","message":"MARKER-message"}}`, staticTokens{token: testToken}, queue.Task{Name: "n"}, false, 1},
		{"queue paused or missing", http.StatusBadRequest, `{"error":{"status":"FAILED_PRECONDITION"}}`, staticTokens{token: testToken}, queue.Task{Name: "n"}, false, 1},
		{"token source fails", 0, "", staticTokens{err: errors.New("metadata server down")}, queue.Task{Name: "n"}, false, 0},
		{"invalid name", 0, "", staticTokens{token: testToken}, queue.Task{Name: "a/b"}, true, 0},
		{"empty name", 0, "", staticTokens{token: testToken}, queue.Task{}, true, 0},
		{"body over the task limit", 0, "", staticTokens{token: testToken}, queue.Task{Name: "n", Body: make([]byte, 900<<10+1)}, true, 0},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			f := &fakeTasks{status: tt.status, reply: tt.reply}
			q := newDriver(t, f, tt.tokens)
			err := q.Enqueue(t.Context(), tt.task)
			if err == nil {
				t.Fatal("Enqueue = nil, want an error")
			}
			if got := errors.Is(err, queue.ErrInvalidTask); got != tt.wantInvalid {
				t.Errorf("errors.Is(%v, ErrInvalidTask) = %v, want %v", err, got, tt.wantInvalid)
			}
			if n := len(f.sent()); n != tt.wantSent {
				t.Errorf("API requests = %d, want %d", n, tt.wantSent)
			}
			if strings.Contains(err.Error(), "MARKER") {
				t.Errorf("error %q leaks the token or the API message", err)
			}
		})
	}

	t.Run("a body at the task limit is sent", func(t *testing.T) {
		f := &fakeTasks{}
		if err := newDriver(t, f, staticTokens{token: testToken}).Enqueue(t.Context(), queue.Task{Name: "n", Body: make([]byte, 900<<10)}); err != nil {
			t.Errorf("Enqueue = %v, want nil", err)
		}
	})

	t.Run("an unreachable API is an error", func(t *testing.T) {
		srv := httptest.NewServer(http.NotFoundHandler())
		srv.Close()
		q, err := cloudtasks.New(cloudtasks.Config{Queue: testQueue, URL: testURL, Audience: testAudience, Invoker: testInvoker, Endpoint: srv.URL},
			staticTokens{token: testToken}, nil)
		if err != nil {
			t.Fatal(err)
		}
		if err := q.Enqueue(t.Context(), queue.Task{Name: "n"}); err == nil || errors.Is(err, queue.ErrInvalidTask) {
			t.Errorf("Enqueue = %v, want a transport error", err)
		}
	})
}

func TestNewValidatesConfig(t *testing.T) {
	valid := cloudtasks.Config{Queue: testQueue, URL: testURL, Audience: testAudience, Invoker: testInvoker}
	tests := []struct {
		name   string
		change func(c *cloudtasks.Config)
		tokens cloudtasks.TokenSource
	}{
		{"queue without a location", func(c *cloudtasks.Config) { c.Queue = "projects/coupling-test/queues/webhooks" }, staticTokens{}},
		{"queue with a trailing slash", func(c *cloudtasks.Config) { c.Queue = testQueue + "/" }, staticTokens{}},
		{"empty queue", func(c *cloudtasks.Config) { c.Queue = "" }, staticTokens{}},
		{"plain http URL", func(c *cloudtasks.Config) { c.URL = "http://worker.example/internal/webhooks" }, staticTokens{}},
		{"relative URL", func(c *cloudtasks.Config) { c.URL = "/internal/webhooks" }, staticTokens{}},
		{"empty audience", func(c *cloudtasks.Config) { c.Audience = "" }, staticTokens{}},
		{"invoker without a domain", func(c *cloudtasks.Config) { c.Invoker = "invoker" }, staticTokens{}},
		{"nil token source", func(*cloudtasks.Config) {}, nil},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			c := valid
			tt.change(&c)
			if _, err := cloudtasks.New(c, tt.tokens, nil); err == nil {
				t.Error("New = nil error")
			}
		})
	}
	if _, err := cloudtasks.New(valid, staticTokens{}, nil); err != nil {
		t.Errorf("New(valid) = %v", err)
	}
}

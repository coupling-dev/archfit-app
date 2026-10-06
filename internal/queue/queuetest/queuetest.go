// Package queuetest is the conformance suite every queue.Queue implementation must pass.
package queuetest

import (
	"bytes"
	"context"
	"fmt"
	"net/http"
	"sync"
	"testing"
	"time"

	"github.com/alexei-led/archfit-app/internal/queue"
)

// deliveryWait bounds how long a case waits for an asynchronous driver to deliver.
const deliveryWait = 5 * time.Second

// New builds the queue under test. Every task the queue delivers must reach h.
type New func(t *testing.T, h queue.Handler) queue.Queue

// Run runs every conformance case against a fresh queue per case.
func Run(t *testing.T, newQueue New) {
	cases := []struct {
		name string
		run  func(*testing.T, New)
	}{
		{"DeliversHeadersAndBody", testDeliversHeadersAndBody},
		{"DuplicateNameIsAccepted", testDuplicateNameIsAccepted},
		{"ConcurrentUse", testConcurrentUse},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) { tc.run(t, newQueue) })
	}
}

// recorder collects delivered tasks by the value of their X-Test-Id header.
type recorder struct {
	mu        sync.Mutex
	delivered map[string][]queue.Task
	arrived   chan struct{}
}

func newRecorder() *recorder {
	return &recorder{delivered: map[string][]queue.Task{}, arrived: make(chan struct{}, 1024)}
}

func (r *recorder) handle(_ context.Context, t queue.Task) {
	r.mu.Lock()
	id := t.Header.Get("X-Test-Id")
	r.delivered[id] = append(r.delivered[id], t)
	r.mu.Unlock()
	r.arrived <- struct{}{}
}

// await waits until every id has at least one delivery.
func (r *recorder) await(t *testing.T, ids ...string) map[string][]queue.Task {
	t.Helper()
	deadline := time.After(deliveryWait)
	for {
		r.mu.Lock()
		missing := ""
		for _, id := range ids {
			if len(r.delivered[id]) == 0 {
				missing = id
				break
			}
		}
		snapshot := make(map[string][]queue.Task, len(r.delivered))
		for k, v := range r.delivered {
			snapshot[k] = append([]queue.Task(nil), v...)
		}
		r.mu.Unlock()
		if missing == "" {
			return snapshot
		}
		select {
		case <-r.arrived:
		case <-deadline:
			t.Fatalf("task %q was not delivered within %s", missing, deliveryWait)
		}
	}
}

func task(name, id string, body []byte) queue.Task {
	h := http.Header{}
	h.Set("X-Test-Id", id)
	h.Set("X-GitHub-Event", "pull_request")
	h.Set("Content-Type", "application/json")
	return queue.Task{Name: name, Header: h, Body: body}
}

func testDeliversHeadersAndBody(t *testing.T, newQueue New) {
	rec := newRecorder()
	q := newQueue(t, rec.handle)
	body := []byte("{\"a\":1}\x00\xff binary-safe")
	if err := q.Enqueue(t.Context(), task("delivery-1_gate", "one", body)); err != nil {
		t.Fatalf("Enqueue: %v", err)
	}
	got := rec.await(t, "one")["one"][0]
	if !bytes.Equal(got.Body, body) {
		t.Errorf("body = %q, want %q", got.Body, body)
	}
	for _, h := range []string{"X-Test-Id", "X-GitHub-Event", "Content-Type"} {
		if got.Header.Get(h) != task("", "one", nil).Header.Get(h) {
			t.Errorf("header %s = %q, not delivered intact", h, got.Header.Get(h))
		}
	}
}

func testDuplicateNameIsAccepted(t *testing.T, newQueue New) {
	rec := newRecorder()
	q := newQueue(t, rec.handle)
	for i := range 2 {
		if err := q.Enqueue(t.Context(), task("delivery-2_gate", "dup", []byte("{}"))); err != nil {
			t.Fatalf("Enqueue %d of a repeated name: %v; want nil", i+1, err)
		}
	}
	if n := len(rec.await(t, "dup")["dup"]); n < 1 || n > 2 {
		t.Errorf("a name enqueued twice was delivered %d times; want once or twice", n)
	}
}

func testConcurrentUse(t *testing.T, newQueue New) {
	rec := newRecorder()
	q := newQueue(t, rec.handle)
	const n = 16
	ids := make([]string, n)
	errs := make(chan error, n)
	var wg sync.WaitGroup
	for i := range n {
		ids[i] = fmt.Sprintf("c%d", i)
		wg.Go(func() { errs <- q.Enqueue(t.Context(), task("delivery-c"+fmt.Sprint(i), ids[i], []byte("{}"))) })
	}
	wg.Wait()
	close(errs)
	for err := range errs {
		if err != nil {
			t.Errorf("concurrent Enqueue: %v", err)
		}
	}
	rec.await(t, ids...)
}

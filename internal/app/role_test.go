package app

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"slices"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/alexei-led/archfit-app/internal/github/api"
	"github.com/alexei-led/archfit-app/internal/github/webhook"
	"github.com/alexei-led/archfit-app/internal/queue"
)

// idToken builds a Google ID token for the invoker with the signature removed, as Cloud Run passes it on.
func idToken(change func(claims map[string]any)) string {
	claims := map[string]any{
		"iss": "https://accounts.google.com", "aud": testWorkerURL, "email": testInvoker, "email_verified": true,
		"exp": testNow.Add(time.Hour).Unix(), "iat": testNow.Unix(), "sub": "1234567890",
	}
	if change != nil {
		change(claims)
	}
	payload, _ := json.Marshal(claims)
	enc := base64.RawURLEncoding.EncodeToString
	return enc([]byte(`{"alg":"RS256","kid":"k1","typ":"JWT"}`)) + "." + enc(payload) + ".SIGNATURE_REMOVED_BY_GOOGLE"
}

func asInvoker(r *http.Request) *http.Request {
	r.Header.Set("Authorization", bearerPrefix+idToken(nil))
	return r
}

// taskRequest is a webhook task as Cloud Tasks dispatches it to the worker.
func taskRequest(half, event, delivery string, body []byte, signature string) *http.Request {
	r := webhookRequest(taskRoute, event, delivery, body, signature)
	r.Header.Set(headerHalf, half)
	return asInvoker(r)
}

func newRoleFixture(role string) *fixture {
	f := newFixture()
	f.role = role
	if role == rolePublic {
		f.queue = &recordingQueue{}
	}
	f.rebuild()
	return f
}

func TestRoleRouteTables(t *testing.T) {
	event := []byte(pullOpenedBody)
	signed := func(target string) *http.Request {
		return webhookRequest(target, "pull_request", "d-1", event, sign(testWebhookSecret, event))
	}
	operator := func(target string) *http.Request { return operatorRequest(target, testOperatorToken, "") }
	type route struct {
		name string
		req  func() *http.Request
	}
	routes := []route{
		{"healthz", func() *http.Request { return httptest.NewRequest(http.MethodGet, "/healthz", nil) }},
		{"upload", func() *http.Request { return httptest.NewRequest(http.MethodPost, "/v1/reports", nil) }},
		{"discovery", func() *http.Request { return httptest.NewRequest(http.MethodPost, "/v1/discoveries", nil) }},
		{"baseline", func() *http.Request { return httptest.NewRequest(http.MethodPost, "/v1/baselines", nil) }},
		{"intake", func() *http.Request { return signed("/webhooks/github") }},
		{"ui", func() *http.Request { return httptest.NewRequest(http.MethodGet, "/", nil) }},
		{"ui page", func() *http.Request { return httptest.NewRequest(http.MethodGet, "/r/o/n", nil) }},
		{"sweep with the operator token", func() *http.Request { return operator("/internal/sweep") }},
		{"grant with the operator token", func() *http.Request {
			return operatorRequest("/internal/grants", testOperatorToken, `{"installation_id":42}`)
		}},
		{"task as the invoker", func() *http.Request {
			return taskRequest(halfGate, "pull_request", "d-1", event, sign(testWebhookSecret, event))
		}},
		{"sweep as the invoker", func() *http.Request { return asInvoker(httptest.NewRequest(http.MethodPost, "/internal/sweep", nil)) }},
		{"unknown internal path", func() *http.Request { return asInvoker(operator("/internal/other")) }},
	}
	// want: the first call the request causes, or the status when it reaches no fake.
	type want struct {
		call   string
		status int
	}
	tables := map[string]map[string]want{
		roleAll: {
			"healthz": {status: 200}, "upload": {call: "gate.Upload"}, "discovery": {call: "control.Discovery"},
			"baseline": {call: "control.Baseline"}, "intake": {call: "gate.HandleEvent"}, "ui": {call: "control.UI"},
			"ui page": {call: "control.UI"}, "sweep with the operator token": {call: "control.Sweep"},
			"grant with the operator token": {call: "control.Grant"}, "task as the invoker": {status: 404},
			"sweep as the invoker": {status: 401}, "unknown internal path": {status: 404},
		},
		rolePublic: {
			"healthz": {status: 200}, "upload": {call: "gate.Upload"}, "discovery": {call: "control.Discovery"},
			"baseline": {call: "control.Baseline"}, "intake": {status: http.StatusAccepted}, "ui": {call: "control.UI"},
			"ui page": {call: "control.UI"}, "sweep with the operator token": {status: 404},
			"grant with the operator token": {status: 404}, "task as the invoker": {status: 404},
			"sweep as the invoker": {status: 404}, "unknown internal path": {status: 404},
		},
		roleWorker: {
			"healthz": {status: 200}, "upload": {status: 404}, "discovery": {status: 404}, "baseline": {status: 404},
			"intake": {status: 404}, "ui": {status: 404}, "ui page": {status: 404},
			"sweep with the operator token": {status: 401}, "grant with the operator token": {status: 401},
			"task as the invoker": {call: "gate.HandleEvent"}, "sweep as the invoker": {call: "control.Sweep"},
			"unknown internal path": {status: 404},
		},
	}
	for role, table := range tables {
		for _, rt := range routes {
			t.Run(role+"/"+rt.name, func(t *testing.T) {
				w, ok := table[rt.name]
				if !ok {
					t.Fatalf("no expectation for %s", rt.name)
				}
				f := newRoleFixture(role)
				rec := f.do(rt.req())
				calls := f.calls.get()
				if w.call != "" {
					if len(calls) == 0 || calls[0] != w.call {
						t.Errorf("calls = %v (status %d), want first %s", calls, rec.Code, w.call)
					}
					return
				}
				if rec.Code != w.status || len(calls) != 0 {
					t.Errorf("status = %d, calls = %v; want %d and no call", rec.Code, calls, w.status)
				}
			})
		}
	}
}

func TestWorkerRequiresTheInvokerIdentity(t *testing.T) {
	tests := []struct {
		name string
		set  func(r *http.Request)
		ok   bool
	}{
		{"no token", func(*http.Request) {}, false},
		{"operator token instead", func(r *http.Request) { r.Header.Set("Authorization", bearerPrefix+testOperatorToken) }, false},
		{"another service account", func(r *http.Request) {
			r.Header.Set("Authorization", bearerPrefix+idToken(func(c map[string]any) { c["email"] = "public@coupling-test.iam.gserviceaccount.com" }))
		}, false},
		{"another audience", func(r *http.Request) {
			r.Header.Set("Authorization", bearerPrefix+idToken(func(c map[string]any) { c["aud"] = "https://archfit.example" }))
		}, false},
		{"another issuer", func(r *http.Request) {
			r.Header.Set("Authorization", bearerPrefix+idToken(func(c map[string]any) { c["iss"] = "https://token.actions.githubusercontent.com" }))
		}, false},
		{"unverified e-mail", func(r *http.Request) {
			r.Header.Set("Authorization", bearerPrefix+idToken(func(c map[string]any) { c["email_verified"] = false }))
		}, false},
		{"expired", func(r *http.Request) {
			r.Header.Set("Authorization", bearerPrefix+idToken(func(c map[string]any) { c["exp"] = testNow.Unix() }))
		}, false},
		{"audience list", func(r *http.Request) {
			r.Header.Set("Authorization", bearerPrefix+idToken(func(c map[string]any) { c["aud"] = []string{testWorkerURL} }))
		}, false},
		{"no bearer scheme", func(r *http.Request) { r.Header.Set("Authorization", idToken(nil)) }, false},
		{"two segments", func(r *http.Request) {
			r.Header.Set("Authorization", bearerPrefix+strings.Join(strings.Split(idToken(nil), ".")[:2], "."))
		}, false},
		{"payload not base64", func(r *http.Request) { r.Header.Set("Authorization", bearerPrefix+"a.%%%.c") }, false},
		{"payload not JSON", func(r *http.Request) {
			r.Header.Set("Authorization", bearerPrefix+"a."+base64.RawURLEncoding.EncodeToString([]byte("MARKER"))+".c")
		}, false},
		{"invoker in Authorization", func(r *http.Request) { r.Header.Set("Authorization", bearerPrefix+idToken(nil)) }, true},
		{"invoker in X-Serverless-Authorization", func(r *http.Request) {
			r.Header.Set(headerServerlessAuth, bearerPrefix+idToken(nil))
			r.Header.Set("Authorization", bearerPrefix+testOperatorToken)
		}, true},
		{"issuer without scheme", func(r *http.Request) {
			r.Header.Set("Authorization", bearerPrefix+idToken(func(c map[string]any) { c["iss"] = "accounts.google.com" }))
		}, true},
	}
	event := []byte(pullOpenedBody)
	for _, path := range []string{taskRoute, "/internal/sweep", "/internal/grants"} {
		for _, tt := range tests {
			t.Run(path+"/"+tt.name, func(t *testing.T) {
				f := newRoleFixture(roleWorker)
				r := webhookRequest(path, "pull_request", "d-1", event, sign(testWebhookSecret, event))
				r.Header.Set(headerHalf, halfGate)
				if path == "/internal/grants" {
					r = operatorRequest(path, "", `{"installation_id":42}`)
					r.Header.Set(headerOperator, bearerPrefix+testOperatorToken)
				}
				tt.set(r)
				rec := f.do(r)
				reached := len(f.calls.get()) > 0
				if reached != tt.ok {
					t.Fatalf("status %d, reached = %v; want reached = %v", rec.Code, reached, tt.ok)
				}
				if !tt.ok && rec.Code != http.StatusUnauthorized {
					t.Errorf("status = %d, want 401", rec.Code)
				}
			})
		}
	}

	t.Run("a worker without a configured invoker admits nobody", func(t *testing.T) {
		f := newRoleFixture(roleWorker)
		h := newHandler(handlerDeps{role: roleWorker, gate: f.gate, control: f.control, now: func() time.Time { return testNow }, log: slog.New(slog.DiscardHandler)})
		rec := httptest.NewRecorder()
		h.ServeHTTP(rec, asInvoker(httptest.NewRequest(http.MethodPost, "/internal/sweep", nil)))
		if rec.Code != http.StatusUnauthorized || len(f.calls.get()) != 0 {
			t.Errorf("status = %d, calls = %v; want 401 and no call", rec.Code, f.calls.get())
		}
	})
}

func TestWorkerGrantNeedsTheOperatorToken(t *testing.T) {
	tests := []struct {
		name     string
		operator string // value of X-Archfit-Operator
		want     int
	}{
		{"operator token in its header", bearerPrefix + testOperatorToken, http.StatusNoContent},
		{"no operator token", "", http.StatusUnauthorized},
		{"wrong operator token", bearerPrefix + "wrong", http.StatusUnauthorized},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			f := newRoleFixture(roleWorker)
			r := asInvoker(operatorRequest("/internal/grants", "", `{"installation_id":42,"until":"2999-01-01T00:00:00Z"}`))
			if tt.operator != "" {
				r.Header.Set(headerOperator, tt.operator)
			}
			if got := f.do(r).Code; got != tt.want {
				t.Errorf("status = %d, want %d", got, tt.want)
			}
			if granted := len(f.control.grants) == 1; granted != (tt.want == http.StatusNoContent) {
				t.Errorf("grants = %v", f.control.grants)
			}
		})
	}
}

func TestWorkerTaskErrorClasses(t *testing.T) {
	event := []byte(pullOpenedBody)
	good := sign(testWebhookSecret, event)
	tests := []struct {
		name      string
		half      string
		event     string
		body      []byte
		signature string
		gateErr   error
		ctlErr    error
		ctlPanic  bool
		want      int
		calls     []string
		outcome   string
	}{
		{name: "gate done", half: halfGate, want: 204, calls: []string{"gate.HandleEvent"}, outcome: outcomeDone},
		{name: "control done", half: halfControl, want: 204, calls: []string{"control.HandleEvent"}, outcome: outcomeDone},
		{name: "signed with the previous secret during a rotation", half: halfGate, signature: sign(testWebhookPrevious, event),
			want: 204, calls: []string{"gate.HandleEvent"}, outcome: outcomeDone},
		{name: "transient gate error is retried", half: halfGate, gateErr: errors.New("store: unavailable"), want: 503,
			calls: []string{"gate.HandleEvent"}, outcome: outcomeTransient},
		{name: "rate limit is retried", half: halfGate, gateErr: fmt.Errorf("gate: %w", api.ErrRateLimited), want: 503,
			calls: []string{"gate.HandleEvent"}, outcome: outcomeTransient},
		{name: "deadline is retried", half: halfControl, ctlErr: context.DeadlineExceeded, want: 503,
			calls: []string{"control.HandleEvent"}, outcome: outcomeTransient},
		{name: "control panic is retried", half: halfControl, ctlPanic: true, want: 503,
			calls: []string{"control.HandleEvent"}, outcome: outcomeTransient},
		{name: "permanent gate error is dropped", half: halfGate, gateErr: fmt.Errorf("gate: resolve inputs: %w", api.ErrNotFound),
			want: 204, calls: []string{"gate.HandleEvent"}, outcome: outcomePermanent},
		{name: "permanent control error is dropped", half: halfControl, ctlErr: fmt.Errorf("control: %w", api.ErrNotFound),
			want: 204, calls: []string{"control.HandleEvent"}, outcome: outcomePermanent},
		{name: "bad signature is dropped", half: halfGate, signature: sign("MARKER-wrong", event), want: 204, outcome: "bad_signature"},
		{name: "unparsable body is dropped", half: halfGate, body: []byte(`{"action":`), want: 204, outcome: "unparsable"},
		{name: "unknown half is dropped", half: "MARKER-billing", want: 204, outcome: "unknown_half"},
		{name: "missing half is dropped", half: "", want: 204, outcome: "unknown_half"},
		{name: "ignored event", half: halfGate, event: "star", want: 204, outcome: outcomeIgnored},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			f := newRoleFixture(roleWorker)
			f.gate.eventErr, f.control.eventErr, f.control.eventPanic = tt.gateErr, tt.ctlErr, tt.ctlPanic
			body := event
			if tt.body != nil {
				body = tt.body
			}
			signature := good
			if tt.body != nil {
				signature = sign(testWebhookSecret, body)
			}
			if tt.signature != "" {
				signature = tt.signature
			}
			name := "pull_request"
			if tt.event != "" {
				name = tt.event
			}
			rec := f.do(taskRequest(tt.half, name, "d-9", body, signature))
			if rec.Code != tt.want {
				t.Errorf("status = %d, want %d", rec.Code, tt.want)
			}
			if got := f.calls.get(); !slices.Equal(got, tt.calls) {
				t.Errorf("calls = %v, want %v", got, tt.calls)
			}
			if logs := f.logs.String(); !strings.Contains(logs, `"outcome":"`+tt.outcome+`"`) {
				t.Errorf("logs lack outcome %s: %s", tt.outcome, logs)
			}
			if strings.Contains(f.logs.String(), "MARKER") {
				t.Error("logs echo a header value or a secret")
			}
		})
	}

	t.Run("the half runs under its own deadline, attached to the dispatch", func(t *testing.T) {
		f := newRoleFixture(roleWorker)
		f.do(taskRequest(halfGate, "pull_request", "d-9", event, good))
		if d := f.calls.deadlines(); len(d) != 1 || d[0] <= uploadTimeout-time.Second || d[0] > uploadTimeout {
			t.Errorf("deadlines = %v, want about %s", d, uploadTimeout)
		}
		ctx, cancel := context.WithCancel(context.Background())
		cancel()
		f.gate.eventErr = nil
		r := taskRequest(halfGate, "pull_request", "d-9", event, good).WithContext(ctx)
		f.gate.ctxErr = true
		if got := f.do(r).Code; got != http.StatusServiceUnavailable {
			t.Errorf("a cancelled dispatch answered %d; the worker must not detach from it", got)
		}
	})
}

// recordingQueue is a queue.Queue that keeps every task and fails the names listed in fail.
type recordingQueue struct {
	mu    sync.Mutex
	tasks []queue.Task
	fail  map[string]error
}

func (q *recordingQueue) Enqueue(ctx context.Context, t queue.Task) error {
	q.mu.Lock()
	defer q.mu.Unlock()
	if _, ok := ctx.Deadline(); !ok {
		return errors.New("test: enqueue without a deadline")
	}
	if err := q.fail[t.Name]; err != nil {
		return err
	}
	q.tasks = append(q.tasks, t)
	return nil
}

func (q *recordingQueue) names() []string {
	q.mu.Lock()
	defer q.mu.Unlock()
	var names []string
	for _, t := range q.tasks {
		names = append(names, t.Name)
	}
	return names
}

func TestIntakeQueuesOneTaskPerHalf(t *testing.T) {
	event := []byte(pullOpenedBody)
	const delivery = "72d3162e-cc78-11e3-81ab-4c9367dc0958"
	tests := []struct {
		name      string
		event     string
		delivery  string
		fail      map[string]error
		wantNames []string
		status    int
		outcome   string
	}{
		{name: "both halves queued", event: "pull_request", delivery: delivery,
			wantNames: []string{delivery + "_gate", delivery + "_control"}, status: http.StatusAccepted, outcome: "accepted"},
		{name: "a control enqueue failure is not acknowledged", event: "pull_request", delivery: delivery,
			fail:      map[string]error{delivery + "_control": errors.New("cloudtasks: create task: status 503")},
			wantNames: []string{delivery + "_gate"}, status: http.StatusServiceUnavailable, outcome: "enqueue_failed"},
		{name: "a gate enqueue failure still attempts control", event: "pull_request", delivery: delivery,
			fail:      map[string]error{delivery + "_gate": errors.New("cloudtasks: create task: status 503")},
			wantNames: []string{delivery + "_control"}, status: http.StatusServiceUnavailable, outcome: "enqueue_failed"},
		{name: "a body too large for a task is rejected without inline work", event: "pull_request", delivery: delivery,
			fail: map[string]error{
				delivery + "_gate":    fmt.Errorf("cloudtasks: body: %w", queue.ErrInvalidTask),
				delivery + "_control": fmt.Errorf("cloudtasks: body: %w", queue.ErrInvalidTask),
			},
			status: http.StatusServiceUnavailable, outcome: "enqueue_failed"},
		{name: "an invalid task name is rejected without inline work", event: "pull_request", delivery: "d/1",
			fail: map[string]error{
				"d/1_gate":    fmt.Errorf("cloudtasks: task name: %w", queue.ErrInvalidTask),
				"d/1_control": fmt.Errorf("cloudtasks: task name: %w", queue.ErrInvalidTask),
			},
			status: http.StatusServiceUnavailable, outcome: "enqueue_failed"},
		{name: "an ignored event queues nothing", event: "star", delivery: delivery,
			status: http.StatusAccepted, outcome: "accepted"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			f := newFixture()
			q := &recordingQueue{fail: tt.fail}
			f.role, f.queue = rolePublic, q
			f.rebuild()
			rec := f.do(webhookRequest("/webhooks/github", tt.event, tt.delivery, event, sign(testWebhookSecret, event)))
			if rec.Code != tt.status {
				t.Errorf("status = %d, want %d", rec.Code, tt.status)
			}
			if got := q.names(); !slices.Equal(got, tt.wantNames) {
				t.Errorf("queued %v, want %v", got, tt.wantNames)
			}
			if got := f.calls.get(); len(got) != 0 {
				t.Errorf("public intake ran handlers inline: %v", got)
			}
			if !strings.Contains(f.logs.String(), "\"outcome\":\""+tt.outcome+"\"") {
				t.Errorf("request line lacks outcome %s: %s", tt.outcome, f.logs.String())
			}
		})
	}

	t.Run("a public intake without a queue rejects the delivery", func(t *testing.T) {
		f := newFixture()
		f.role = rolePublic
		f.rebuild()
		rec := f.do(webhookRequest("/webhooks/github", "pull_request", delivery, event, sign(testWebhookSecret, event)))
		if rec.Code != http.StatusServiceUnavailable {
			t.Errorf("status = %d, want 503", rec.Code)
		}
		if got := f.calls.get(); len(got) != 0 {
			t.Errorf("public intake ran handlers inline: %v", got)
		}
	})

	t.Run("a task carries GitHub's own headers, body and the half", func(t *testing.T) {
		f := newFixture()
		q := &recordingQueue{}
		f.role, f.queue = rolePublic, q
		f.rebuild()
		r := webhookRequest("/webhooks/github", "pull_request", delivery, event, sign(testWebhookSecret, event))
		r.Header.Set("Cookie", "MARKER-cookie")
		r.Header.Set("X-Forwarded-For", "203.0.113.7")
		f.do(r)
		if len(q.tasks) != 2 {
			t.Fatalf("tasks = %d, want 2", len(q.tasks))
		}
		for i, half := range []string{halfGate, halfControl} {
			task := q.tasks[i]
			want := http.Header{}
			want.Set(webhook.HeaderEvent, "pull_request")
			want.Set(webhook.HeaderDelivery, delivery)
			want.Set(webhook.HeaderSignature, sign(testWebhookSecret, event))
			want.Set("Content-Type", "application/json")
			want.Set(headerHalf, half)
			if fmt.Sprint(task.Header) != fmt.Sprint(want) {
				t.Errorf("task %d headers = %v, want exactly %v", i, task.Header, want)
			}
			if !bytes.Equal(task.Body, event) {
				t.Errorf("task %d body differs from the delivery", i)
			}
		}
	})
}

// TestQueuedTaskReachesTheWorker runs intake and worker as two handlers, joined by a queue that dispatches
// each task to the worker route the way Cloud Tasks does.
func TestQueuedTaskReachesTheWorker(t *testing.T) {
	worker := newRoleFixture(roleWorker)
	dispatch := queue.NewInline(func(ctx context.Context, task queue.Task) {
		r := httptest.NewRequestWithContext(ctx, http.MethodPost, taskRoute, bytes.NewReader(task.Body))
		r.Header = task.Header.Clone()
		worker.do(asInvoker(r))
	})
	intake := newFixture()
	intake.role, intake.queue = rolePublic, dispatch
	intake.rebuild()

	event := []byte(pullOpenedBody)
	if rec := intake.do(webhookRequest("/webhooks/github", "pull_request", "d-5", event, sign(testWebhookSecret, event))); rec.Code != http.StatusAccepted {
		t.Fatalf("intake = %d, want 202", rec.Code)
	}
	if got := intake.calls.get(); len(got) != 0 {
		t.Errorf("intake ran %v; the worker owns the work", got)
	}
	if got, want := worker.calls.get(), []string{"gate.HandleEvent", "control.HandleEvent"}; !slices.Equal(got, want) {
		t.Errorf("worker calls = %v, want %v", got, want)
	}
	if len(worker.gate.events) != 1 || worker.gate.events[0].DeliveryID != "d-5" || worker.gate.events[0].InstallationID != 42 {
		t.Errorf("worker gate events = %+v", worker.gate.events)
	}
}

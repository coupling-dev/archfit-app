package app

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"log/slog"
	"net"
	"net/http"
	"net/http/httptest"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/alexei-led/archfit-app/internal/gate"
	"github.com/alexei-led/archfit-app/internal/github/webhook"
)

func TestOperatorTokenComparison(t *testing.T) {
	tests := []struct {
		name          string
		authorization string
		want          int
	}{
		{"missing header", "", http.StatusUnauthorized},
		{"wrong scheme", "Basic " + testOperatorToken, http.StatusUnauthorized},
		{"scheme only", "Bearer ", http.StatusUnauthorized},
		{"wrong token, same length", "Bearer " + strings.Repeat("x", len(testOperatorToken)), http.StatusUnauthorized},
		{"shorter token", "Bearer " + testOperatorToken[:5], http.StatusUnauthorized},
		{"longer token", "Bearer " + testOperatorToken + "x", http.StatusUnauthorized},
		{"correct token", "Bearer " + testOperatorToken, http.StatusOK},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			f := newFixture()
			r := httptest.NewRequest(http.MethodPost, "/internal/sweep", nil)
			if tt.authorization != "" {
				r.Header.Set("Authorization", tt.authorization)
			}
			if got := f.do(r).Code; got != tt.want {
				t.Errorf("status = %d, want %d", got, tt.want)
			}
			if swept := len(f.calls.get()) > 0; swept != (tt.want == http.StatusOK) {
				t.Errorf("sweep ran = %v", swept)
			}
		})
	}

	t.Run("an empty configured token authorizes nobody", func(t *testing.T) {
		l := &callLog{}
		h := newHandler(handlerDeps{role: roleAll, gate: newFakeGate(l), control: newFakeControl(l), now: time.Now, log: slog.Default()})
		r := httptest.NewRequest(http.MethodPost, "/internal/sweep", nil)
		r.Header.Set("Authorization", "Bearer ")
		rec := httptest.NewRecorder()
		h.ServeHTTP(rec, r)
		if rec.Code != http.StatusUnauthorized {
			t.Errorf("status = %d, want 401", rec.Code)
		}
	})
}

func TestRouteTable(t *testing.T) {
	f := newFixture()
	base, _ := background(t, func(ctx context.Context, ready func(net.Addr)) error {
		return serve(ctx, "127.0.0.1:0", f.handler, slog.Default(), ready)
	})

	signed := func() *http.Request {
		body := []byte(pullOpenedBody)
		return webhookRequest(base+"/webhooks/github", "pull_request", "d-1", body, sign(testWebhookSecret, body))
	}
	grant := `{"installation_id": 42, "until": "2026-10-01T00:00:00Z"}`
	tests := []struct {
		name       string
		request    *http.Request
		wantStatus int
		wantCalls  []string
	}{
		{"health", httptest.NewRequest(http.MethodGet, base+"/health", nil), http.StatusOK, nil},
		{"reports", httptest.NewRequest(http.MethodPost, base+"/v1/reports", nil), http.StatusCreated, []string{"gate.Upload"}},
		{"discoveries", httptest.NewRequest(http.MethodPost, base+"/v1/discoveries", nil), http.StatusAccepted, []string{"control.Discovery"}},
		{"baselines", httptest.NewRequest(http.MethodPost, base+"/v1/baselines", nil), http.StatusAccepted, []string{"control.Baseline"}},
		{"GET on the baseline path", httptest.NewRequest(http.MethodGet, base+"/v1/baselines", nil), http.StatusOK, []string{"control.UI"}},
		{"webhook", signed(), http.StatusAccepted, []string{"gate.HandleEvent", "control.HandleEvent"}},
		{"sweep", operatorRequest(base+"/internal/sweep", testOperatorToken, ""), http.StatusOK, []string{"control.Sweep", "gate.Sweep"}},
		{"grants", operatorRequest(base+"/internal/grants", testOperatorToken, grant), http.StatusNoContent, []string{"control.Grant"}},
		{"root", httptest.NewRequest(http.MethodGet, base+"/", nil), http.StatusOK, []string{"control.UI"}},
		{"unknown path", httptest.NewRequest(http.MethodGet, base+"/octo/repo/policy", nil), http.StatusOK, []string{"control.UI"}},
		{"GET on the upload path", httptest.NewRequest(http.MethodGet, base+"/v1/reports", nil), http.StatusOK, []string{"control.UI"}},
		{"GET on an operator path", httptest.NewRequest(http.MethodGet, base+"/internal/sweep", nil), http.StatusNotFound, nil},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			before := len(f.calls.get())
			resp := send(t, tt.request)
			if resp.StatusCode != tt.wantStatus {
				t.Errorf("status = %d, want %d", resp.StatusCode, tt.wantStatus)
			}
			if got := f.calls.get()[before:]; !reflect.DeepEqual(got, tt.wantCalls) && len(got)+len(tt.wantCalls) > 0 {
				t.Errorf("reached %v, want %v", got, tt.wantCalls)
			}
		})
	}

	t.Run("health body", func(t *testing.T) {
		rec := f.do(httptest.NewRequest(http.MethodGet, "/health", nil))
		if got := rec.Body.String(); got != `{"status":"ok"}` {
			t.Errorf("body = %q", got)
		}
		if got := rec.Header().Get("Content-Type"); got != "application/json" {
			t.Errorf("Content-Type = %q", got)
		}
	})
}

func TestRouteDeadlines(t *testing.T) {
	const short, long = 55 * time.Second, 300 * time.Second
	body := []byte(pullOpenedBody)
	post := func(target string) *http.Request { return httptest.NewRequest(http.MethodPost, target, nil) }
	tests := []struct {
		name    string
		request *http.Request
		want    []time.Duration // one per fake reached, in order; 0 means no deadline
	}{
		{"gate upload", post("/v1/reports"), []time.Duration{short}},
		{"discovery upload", post("/v1/discoveries"), []time.Duration{short}},
		{"baseline upload", post("/v1/baselines"), []time.Duration{short}},
		{"UI page", httptest.NewRequest(http.MethodGet, "/", nil), []time.Duration{short}},
		{"UI save", post("/r/octo/repo/policy"), []time.Duration{short}},
		{"GET on the assist path", httptest.NewRequest(http.MethodGet, "/r/octo/repo/assist", nil), []time.Duration{short}},
		{"below the assist path", post("/r/octo/repo/assist/x"), []time.Duration{short}},
		{"another path that ends in assist", post("/r/octo/assist"), []time.Duration{short}},
		{"managed assist", post("/r/octo/repo/assist"), []time.Duration{long}},
		{"webhook halves", webhookRequest("/webhooks/github", "pull_request", "d-1", body, sign(testWebhookSecret, body)), []time.Duration{short, short}},
		{"sweep", operatorRequest("/internal/sweep", testOperatorToken, ""), []time.Duration{0, 0}},
		{"grant", operatorRequest("/internal/grants", testOperatorToken, `{"installation_id": 42}`), []time.Duration{0}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			f := newFixture()
			if rec := f.do(tt.request); rec.Code >= http.StatusBadRequest {
				t.Fatalf("status = %d", rec.Code)
			}
			got := f.calls.deadlines()
			if len(got) != len(tt.want) {
				t.Fatalf("deadlines = %v, want %v", got, tt.want)
			}
			for i, want := range tt.want {
				// The fake is reached well within a second of the deadline being set.
				if got[i] > want || got[i] < want-time.Second || (want == 0) != (got[i] == 0) {
					t.Errorf("fake %d saw %v left, want %v", i+1, got[i], want)
				}
			}
			for _, d := range tt.want {
				if writeTimeout <= d {
					t.Errorf("server write timeout %v does not exceed the route deadline %v", writeTimeout, d)
				}
			}
		})
	}
}

func TestWebhookFanOut(t *testing.T) {
	body := []byte(pullOpenedBody)
	want, err := webhook.Parse("pull_request", "d-1", body)
	if err != nil || want.Kind != webhook.PullOpened {
		t.Fatalf("fixture does not parse as pull_opened: %+v, %v", want, err)
	}

	t.Run("signed pull_request reaches gate then control with the same event", func(t *testing.T) {
		f := newFixture()
		rec := f.do(webhookRequest("/webhooks/github", "pull_request", "d-1", body, sign(testWebhookSecret, body)))
		if rec.Code != http.StatusAccepted {
			t.Fatalf("status = %d, want 202", rec.Code)
		}
		if got, wantCalls := f.calls.get(), []string{"gate.HandleEvent", "control.HandleEvent"}; !reflect.DeepEqual(got, wantCalls) {
			t.Errorf("order = %v, want %v", got, wantCalls)
		}
		if !reflect.DeepEqual(f.gate.events, []webhook.Event{want}) {
			t.Errorf("gate saw %+v, want %+v", f.gate.events, want)
		}
		if !reflect.DeepEqual(f.control.events, []webhook.Event{want}) {
			t.Errorf("control saw %+v, want %+v", f.control.events, want)
		}
		if want.ChangedPaths != nil || f.gate.events[0].ChangedPaths != nil {
			t.Errorf("ChangedPaths must stay nil (unknown) for a pull_request event")
		}
	})

	t.Run("the previous secret is accepted during a rotation", func(t *testing.T) {
		f := newFixture()
		rec := f.do(webhookRequest("/webhooks/github", "pull_request", "d-6", body, sign(testWebhookPrevious, body)))
		if rec.Code != http.StatusAccepted {
			t.Fatalf("status = %d, want 202", rec.Code)
		}
		if got := f.calls.get(); !reflect.DeepEqual(got, []string{"gate.HandleEvent", "control.HandleEvent"}) {
			t.Errorf("calls = %v, want both halves", got)
		}
	})

	rejected := []struct {
		name      string
		signature string
	}{
		{"bad signature", sign("another secret", body)},
		{"missing signature", ""},
		{"signature over another body", sign(testWebhookSecret, []byte("{}"))},
	}
	for _, tt := range rejected {
		t.Run(tt.name, func(t *testing.T) {
			f := newFixture()
			rec := f.do(webhookRequest("/webhooks/github", "pull_request", "d-2", body, tt.signature))
			if rec.Code != http.StatusUnauthorized {
				t.Errorf("status = %d, want 401", rec.Code)
			}
			if got := f.calls.get(); len(got) != 0 {
				t.Errorf("handlers called: %v", got)
			}
		})
	}

	t.Run("unknown event type answers 202 and does nothing", func(t *testing.T) {
		f := newFixture()
		unknown := []byte(`{"zen": "Keep it logically awesome."}`)
		rec := f.do(webhookRequest("/webhooks/github", "ping", "d-3", unknown, sign(testWebhookSecret, unknown)))
		if rec.Code != http.StatusAccepted {
			t.Errorf("status = %d, want 202", rec.Code)
		}
		if got := f.calls.get(); len(got) != 0 {
			t.Errorf("handlers called: %v", got)
		}
	})

	t.Run("signed but unparsable payload answers 400", func(t *testing.T) {
		f := newFixture()
		broken := []byte(`{"action": `)
		rec := f.do(webhookRequest("/webhooks/github", "pull_request", "d-4", broken, sign(testWebhookSecret, broken)))
		if rec.Code != http.StatusBadRequest {
			t.Errorf("status = %d, want 400", rec.Code)
		}
		if got := f.calls.get(); len(got) != 0 {
			t.Errorf("handlers called: %v", got)
		}
	})

	t.Run("a gate error is logged by delivery ID and does not change the answer", func(t *testing.T) {
		f := newFixture()
		f.gate.eventErr = errors.New("github unavailable")
		rec := f.do(webhookRequest("/webhooks/github", "pull_request", "d-5", body, sign(testWebhookSecret, body)))
		if rec.Code != http.StatusAccepted {
			t.Errorf("status = %d, want 202", rec.Code)
		}
		if got := f.calls.get(); !reflect.DeepEqual(got, []string{"gate.HandleEvent", "control.HandleEvent"}) {
			t.Errorf("order = %v", got)
		}
		if logs := f.logs.String(); !strings.Contains(logs, `"delivery_id":"d-5"`) || !strings.Contains(logs, `"handler":"gate"`) {
			t.Errorf("gate error not logged by delivery ID: %s", logs)
		}
	})
}

func TestSweepEndpointReturnsSummary(t *testing.T) {
	want := gate.Summary{Repos: 3, Pulls: 9, Pended: 2, TimedOut: 1, Rerun: 1, Warned: 4, Suspended: 1, Errors: 1, RateLimited: true}

	t.Run("counts as JSON, control first", func(t *testing.T) {
		f := newFixture()
		f.gate.summary = want
		rec := f.do(operatorRequest("/internal/sweep", testOperatorToken, ""))
		if rec.Code != http.StatusOK {
			t.Fatalf("status = %d, want 200", rec.Code)
		}
		var got gate.Summary
		if err := json.Unmarshal(rec.Body.Bytes(), &got); err != nil {
			t.Fatalf("body %q: %v", rec.Body, err)
		}
		if got != want {
			t.Errorf("summary = %+v, want %+v", got, want)
		}
		if calls := f.calls.get(); !reflect.DeepEqual(calls, []string{"control.Sweep", "gate.Sweep"}) {
			t.Errorf("order = %v", calls)
		}
	})

	t.Run("the request line counts a rate-limited pass", func(t *testing.T) {
		for _, tt := range []struct {
			limited bool
			outcome string
		}{{false, "swept"}, {true, "sweep_rate_limited"}} {
			f := newFixture()
			f.gate.summary = gate.Summary{RateLimited: tt.limited}
			if rec := f.do(operatorRequest("/internal/sweep", testOperatorToken, "")); rec.Code != http.StatusOK {
				t.Fatalf("status = %d, want 200: the next pass continues the work", rec.Code)
			}
			if logs := f.logs.String(); !strings.Contains(logs, `"outcome":"`+tt.outcome+`"`) {
				t.Errorf("RateLimited %v: logs lack outcome %s: %s", tt.limited, tt.outcome, logs)
			}
		}
	})

	t.Run("a gate sweep error answers 500 with the partial counts", func(t *testing.T) {
		f := newFixture()
		f.gate.summary = want
		f.gate.sweepErr = errors.New("rate limited")
		rec := f.do(operatorRequest("/internal/sweep", testOperatorToken, ""))
		if rec.Code != http.StatusInternalServerError {
			t.Fatalf("status = %d, want 500", rec.Code)
		}
		var got gate.Summary
		if err := json.Unmarshal(rec.Body.Bytes(), &got); err != nil || got != want {
			t.Errorf("summary = %+v, %v", got, err)
		}
	})

	t.Run("unauthorized", func(t *testing.T) {
		f := newFixture()
		if rec := f.do(operatorRequest("/internal/sweep", "wrong", "")); rec.Code != http.StatusUnauthorized {
			t.Errorf("status = %d, want 401", rec.Code)
		}
		if got := f.calls.get(); len(got) != 0 {
			t.Errorf("sweep ran: %v", got)
		}
	})
}

func TestGrantEndpoint(t *testing.T) {
	until := testNow.Add(30 * 24 * time.Hour)
	valid := `{"installation_id": 42, "until": "` + until.Format(time.RFC3339) + `"}`
	tests := []struct {
		name string
		body string
		want int
	}{
		{"valid", valid, http.StatusNoContent},
		{"until is null clears the grant", `{"installation_id": 42, "until": null}`, http.StatusNoContent},
		{"until omitted clears the grant", `{"installation_id": 42}`, http.StatusNoContent},
		{"malformed JSON", `{"installation_id": 42, "until": `, http.StatusBadRequest},
		{"until in the past", `{"installation_id": 42, "until": "2026-09-20T00:00:00Z"}`, http.StatusBadRequest},
		{"until is now", `{"installation_id": 42, "until": "` + testNow.Format(time.RFC3339) + `"}`, http.StatusBadRequest},
		{"until is not RFC 3339", `{"installation_id": 42, "until": "next month"}`, http.StatusBadRequest},
		{"until is the zero time", `{"installation_id": 42, "until": "0001-01-01T00:00:00Z"}`, http.StatusBadRequest},
		{"until is empty", `{"installation_id": 42, "until": ""}`, http.StatusBadRequest},
		{"missing installation ID", `{"until": "` + until.Format(time.RFC3339) + `"}`, http.StatusBadRequest},
		{"negative installation ID", `{"installation_id": -1, "until": "` + until.Format(time.RFC3339) + `"}`, http.StatusBadRequest},
		{"unknown field", `{"installation_id": 42, "until": "` + until.Format(time.RFC3339) + `", "plan": "pro"}`, http.StatusBadRequest},
		{"oversized body", `{"installation_id": 42, "until": "` + strings.Repeat(" ", maxGrantBody) + `"}`, http.StatusBadRequest},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			f := newFixture()
			rec := f.do(operatorRequest("/internal/grants", testOperatorToken, tt.body))
			if rec.Code != tt.want {
				t.Fatalf("status = %d, want %d", rec.Code, tt.want)
			}
			wantGrants := []grantCall(nil)
			if tt.want == http.StatusNoContent {
				wantGrants = []grantCall{{42, until}}
				if tt.body != valid {
					wantGrants = []grantCall{{42, time.Time{}}}
				}
			}
			if len(f.control.grants) != len(wantGrants) || (len(wantGrants) == 1 &&
				(f.control.grants[0].installationID != 42 || !f.control.grants[0].until.Equal(wantGrants[0].until))) {
				t.Errorf("grants = %+v, want %+v", f.control.grants, wantGrants)
			}
		})
	}

	t.Run("a session cookie is not the operator token", func(t *testing.T) {
		f := newFixture()
		r := operatorRequest("/internal/grants", "", valid)
		r.AddCookie(&http.Cookie{Name: "session", Value: testOperatorToken})
		if rec := f.do(r); rec.Code != http.StatusUnauthorized {
			t.Errorf("status = %d, want 401", rec.Code)
		}
		if len(f.control.grants) != 0 {
			t.Errorf("grants = %+v, want none", f.control.grants)
		}
	})

	t.Run("the grant is logged with the installation ID", func(t *testing.T) {
		f := newFixture()
		f.do(operatorRequest("/internal/grants", testOperatorToken, valid))
		if logs := f.logs.String(); !strings.Contains(logs, `"installation_id":42`) || !strings.Contains(logs, `"outcome":"granted"`) {
			t.Errorf("operator action not logged: %s", logs)
		}
		f.do(operatorRequest("/internal/grants", testOperatorToken, `{"installation_id": 42}`))
		if logs := f.logs.String(); !strings.Contains(logs, `"outcome":"grant_cleared"`) {
			t.Errorf("cleared grant not logged: %s", logs)
		}
	})
}

func TestWebhookBodyCap(t *testing.T) {
	over := bytes.Repeat([]byte("A"), maxWebhookBody+1)
	copy(over, bodyMarker)

	tests := []struct {
		name string
		body io.Reader // a plain io.Reader has no known length, as in a chunked delivery
	}{
		{"declared length", bytes.NewReader(over)},
		{"unknown length", struct{ io.Reader }{bytes.NewReader(over)}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			f := newFixture()
			r := httptest.NewRequest(http.MethodPost, "/webhooks/github", tt.body)
			r.Header.Set(webhook.HeaderEvent, "pull_request")
			r.Header.Set(webhook.HeaderDelivery, "d-big")
			r.Header.Set(webhook.HeaderSignature, sign(testWebhookSecret, over))
			if rec := f.do(r); rec.Code != http.StatusRequestEntityTooLarge {
				t.Fatalf("status = %d, want 413", rec.Code)
			}
			if got := f.calls.get(); len(got) != 0 {
				t.Errorf("handlers called: %v", got)
			}
			logs := f.logs.String()
			if !strings.Contains(logs, `"delivery_id":"d-big"`) || !strings.Contains(logs, `"outcome":"body_too_large"`) {
				t.Errorf("rejection not logged by delivery ID: %s", logs)
			}
			if strings.Contains(logs, "MARKER") || len(logs) > 1024 {
				t.Errorf("log holds more than size and delivery ID (%d bytes)", len(logs))
			}
		})
	}

	t.Run("a body of exactly the cap is read", func(t *testing.T) {
		f := newFixture()
		atCap := over[:maxWebhookBody]
		rec := f.do(webhookRequest("/webhooks/github", "ping", "d-cap", atCap, sign(testWebhookSecret, atCap)))
		if rec.Code != http.StatusAccepted {
			t.Errorf("status = %d, want 202", rec.Code)
		}
	})
}

func TestGateServesWhileControlFails(t *testing.T) {
	f := newFixture()
	f.control.ui = http.HandlerFunc(func(http.ResponseWriter, *http.Request) { panic("ui panic") })
	f.control.eventErr = errors.New("control event failure")
	f.control.sweepErr = errors.New("control sweep failure")
	f.gate.summary = gate.Summary{Repos: 1, Pended: 1}
	f.rebuild()
	body := []byte(pullOpenedBody)

	steps := []struct {
		name    string
		prepare func()
		request func() *http.Request
		want    int
	}{
		{"UI panics", nil, func() *http.Request { return httptest.NewRequest(http.MethodGet, "/", nil) }, http.StatusInternalServerError},
		{"upload still serves", nil, func() *http.Request { return httptest.NewRequest(http.MethodPost, "/v1/reports", nil) }, http.StatusCreated},
		{"control event error", nil, func() *http.Request {
			return webhookRequest("/webhooks/github", "pull_request", "d-1", body, sign(testWebhookSecret, body))
		}, http.StatusAccepted},
		{"control event panic", func() { f.control.eventPanic = true }, func() *http.Request {
			return webhookRequest("/webhooks/github", "pull_request", "d-2", body, sign(testWebhookSecret, body))
		}, http.StatusAccepted},
		{"control sweep error", nil, func() *http.Request { return operatorRequest("/internal/sweep", testOperatorToken, "") }, http.StatusOK},
		{"control sweep panic", func() { f.control.sweepPanic = true }, func() *http.Request {
			return operatorRequest("/internal/sweep", testOperatorToken, "")
		}, http.StatusOK},
		{"UI still panics, process still up", nil, func() *http.Request { return httptest.NewRequest(http.MethodGet, "/ui", nil) }, http.StatusInternalServerError},
		{"health", nil, func() *http.Request { return httptest.NewRequest(http.MethodGet, "/health", nil) }, http.StatusOK},
	}
	for _, st := range steps {
		if st.prepare != nil {
			st.prepare()
		}
		rec := f.do(st.request())
		if rec.Code != st.want {
			t.Errorf("%s: status = %d, want %d", st.name, rec.Code, st.want)
		}
		if strings.HasPrefix(st.name, "control sweep") {
			var got gate.Summary
			if err := json.Unmarshal(rec.Body.Bytes(), &got); err != nil || got != f.gate.summary {
				t.Errorf("%s: summary = %+v, %v", st.name, got, err)
			}
		}
	}

	want := []string{
		"gate.Upload",
		"gate.HandleEvent", "control.HandleEvent",
		"gate.HandleEvent", "control.HandleEvent",
		"control.Sweep", "gate.Sweep",
		"control.Sweep", "gate.Sweep",
	}
	if got := f.calls.get(); !reflect.DeepEqual(got, want) {
		t.Errorf("calls = %v, want %v", got, want)
	}
	if len(f.gate.events) != 2 {
		t.Errorf("gate handled %d events, want 2", len(f.gate.events))
	}
}

func TestDuplicateWebhookDelivery(t *testing.T) {
	f := newFixture()
	body := []byte(pullOpenedBody)
	for i := range 2 {
		rec := f.do(webhookRequest("/webhooks/github", "pull_request", "d-dup", body, sign(testWebhookSecret, body)))
		if rec.Code != http.StatusAccepted {
			t.Errorf("delivery %d: status = %d, want 202", i+1, rec.Code)
		}
	}
	want := []string{"gate.HandleEvent", "control.HandleEvent", "gate.HandleEvent", "control.HandleEvent"}
	if got := f.calls.get(); !reflect.DeepEqual(got, want) {
		t.Errorf("calls = %v, want %v", got, want)
	}
	if len(f.gate.pending) != 1 {
		t.Errorf("pending checks = %d, want 1", len(f.gate.pending))
	}
}

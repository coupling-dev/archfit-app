package anthropic

import (
	"context"
	"errors"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/alexei-led/archfit-app/internal/control/assist"
)

func TestRetryIsBounded(t *testing.T) {
	rateLimited := respond(http.StatusTooManyRequests, errorBody("rate_limit_error"))
	overloaded := respond(529, errorBody("overloaded_error"), "retry-after", "3")
	tests := []struct {
		name      string
		steps     func(t *testing.T) []step
		wantErr   bool
		wantCalls int
		wantWaits []time.Duration
	}{
		{"429 then 200", func(t *testing.T) []step {
			return []step{rateLimited, okStep(t, testUsage, text("ok"))}
		}, false, 2, []time.Duration{2 * time.Second}},
		{"529 then 200", func(t *testing.T) []step {
			return []step{overloaded, okStep(t, testUsage, text("ok"))}
		}, false, 2, []time.Duration{3 * time.Second}},
		{"429 twice", func(*testing.T) []step {
			return []step{rateLimited}
		}, true, 2, []time.Duration{2 * time.Second}},
		{"500 once", func(t *testing.T) []step {
			return []step{respond(http.StatusInternalServerError, errorBody("api_error")), okStep(t, testUsage, text("ok"))}
		}, true, 1, nil},
		{"429 with retry-after 600", func(t *testing.T) []step {
			return []step{
				respond(http.StatusTooManyRequests, errorBody("rate_limit_error"), "retry-after", "600"),
				okStep(t, testUsage, text("ok")),
			}
		}, false, 2, []time.Duration{10 * time.Second}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			h := newHarness(t, "", tt.steps(t)...)
			got, err := h.provider.Complete(context.Background(), testPrompt)
			if (err != nil) != tt.wantErr {
				t.Fatalf("error = %v, wantErr %v", err, tt.wantErr)
			}
			if tt.wantErr && (got.Text != "" || got.CostMicroUSD != 0) {
				t.Errorf("completion = %+v, want empty text and zero cost", got)
			}
			if !tt.wantErr && got.Text != "ok" {
				t.Errorf("Text = %q, want ok", got.Text)
			}
			if n := len(h.vendor.recorded()); n != tt.wantCalls {
				t.Errorf("requests = %d, want %d", n, tt.wantCalls)
			}
			waits := h.slept()
			if len(waits) != len(tt.wantWaits) {
				t.Fatalf("waits = %v, want %v", waits, tt.wantWaits)
			}
			for i, w := range waits {
				if w != tt.wantWaits[i] || w > 10*time.Second {
					t.Errorf("wait %d = %v, want %v", i, w, tt.wantWaits[i])
				}
			}
		})
	}

	t.Run("cancellation stops the wait", func(t *testing.T) {
		ctx, cancel := context.WithCancel(context.Background())
		h := newHarness(t, "", func(req *http.Request) (*http.Response, error) {
			defer cancel()
			return rateLimited(req)
		})
		h.provider.sleep = sleepContext // the real wait: it must return at once
		got, err := h.provider.Complete(ctx, testPrompt)
		if !errors.Is(err, context.Canceled) {
			t.Fatalf("error = %v, want context.Canceled", err)
		}
		if got.Text != "" || got.CostMicroUSD != 0 {
			t.Errorf("completion = %+v, want empty text and zero cost", got)
		}
		if n := len(h.vendor.recorded()); n != 1 {
			t.Errorf("requests = %d, want 1", n)
		}
	})
}

func TestNewRejectsBadConfig(t *testing.T) {
	bad := []struct {
		name  string
		cfg   Config
		field string
	}{
		{"empty key", Config{APIKey: "", Model: "claude-sonnet-5"}, "APIKey"},
		{"model gpt-x", Config{APIKey: secretKey, Model: "gpt-x"}, "Model"},
		{"dated model", Config{APIKey: secretKey, Model: "claude-sonnet-5-20260101"}, "Model"},
	}
	for _, tt := range bad {
		t.Run(tt.name, func(t *testing.T) {
			p, err := New(tt.cfg, nil)
			if err == nil || p != nil {
				t.Fatalf("New = %v, %v; want nil provider and an error", p, err)
			}
			if !strings.Contains(err.Error(), tt.field) {
				t.Errorf("error %q does not name the field %s", err, tt.field)
			}
			if strings.Contains(err.Error(), secretKey) {
				t.Errorf("error %q contains the key", err)
			}
		})
	}
	for _, model := range []string{"claude-sonnet-5", "claude-opus-5"} {
		t.Run("valid "+model, func(t *testing.T) {
			p, err := New(Config{APIKey: secretKey, Model: model}, nil)
			if err != nil || p == nil {
				t.Fatalf("New = %v, %v; want a provider and nil error", p, err)
			}
		})
	}
}

func TestOversizedResponseIsAnError(t *testing.T) {
	body := &countingBody{size: 2 << 20}
	h := newHarness(t, "", func(*http.Request) (*http.Response, error) {
		return &http.Response{StatusCode: http.StatusOK, Header: http.Header{}, Body: body}, nil
	})
	got, err := h.provider.Complete(context.Background(), testPrompt)
	if err == nil {
		t.Fatal("error = nil, want an error")
	}
	if got.Text != "" {
		t.Errorf("Text is not empty (%d bytes)", len(got.Text))
	}
	if want := h.provider.MaxCostMicroUSD(testPrompt); got.CostMicroUSD != want {
		t.Errorf("CostMicroUSD = %d, want %d", got.CostMicroUSD, want)
	}
	if read := body.read(); read > 1<<20+1 {
		t.Errorf("read %d bytes, want at most %d", read, 1<<20+1)
	}
}

func TestNothingSensitiveInLogsOrErrors(t *testing.T) {
	logs := captureLogs(t)
	var errs []error

	h := newHarness(t, "", okStep(t, testUsage, text(secretCompletion)))
	if _, err := h.provider.Complete(context.Background(), testPrompt); err != nil {
		t.Fatalf("Complete: %v", err)
	}
	for _, row := range failureRows() {
		_, _, err := row.run(t)
		errs = append(errs, err)
	}
	retried := newHarness(t, "", respond(http.StatusTooManyRequests, errorBody(secretVendorMsg)))
	_, err := retried.provider.Complete(context.Background(), testPrompt)
	errs = append(errs, err)
	_, err = New(Config{APIKey: secretKey, Model: secretPrompt}, nil)
	errs = append(errs, err)

	if logs.Len() == 0 {
		t.Fatal("no log lines captured")
	}
	haystacks := map[string]string{"logs": logs.String()}
	for _, err := range errs {
		if err == nil {
			t.Fatal("a failure row returned a nil error")
		}
		haystacks["error "+err.Error()] = err.Error()
	}
	for where, s := range haystacks {
		for _, secret := range []string{secretKey, secretPrompt, secretCompletion, secretVendorMsg} {
			if strings.Contains(s, secret) {
				t.Errorf("%s contains %s", where, secret)
			}
		}
	}
}

func TestInvalidPromptMakesNoCall(t *testing.T) {
	tests := []struct {
		name   string
		prompt assist.Prompt
	}{
		{"MaxTokens 0", assist.Prompt{System: "s", User: "u", MaxTokens: 0}},
		{"MaxTokens negative", assist.Prompt{System: "s", User: "u", MaxTokens: -1}},
		{"empty User", assist.Prompt{System: "s", User: "", MaxTokens: 100}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			h := newHarness(t, "", okStep(t, testUsage, text("ok")))
			got, err := h.provider.Complete(context.Background(), tt.prompt)
			if err == nil {
				t.Fatal("error = nil, want an error")
			}
			if got.Text != "" || got.CostMicroUSD != 0 {
				t.Errorf("completion = %+v, want empty text and zero cost", got)
			}
			if n := len(h.vendor.recorded()); n != 0 {
				t.Errorf("requests = %d, want 0", n)
			}
		})
	}
}

func TestRedirectIsNotFollowed(t *testing.T) {
	h := newHarness(t, "",
		respond(http.StatusTemporaryRedirect, "", "location", "https://elsewhere.example/v1/messages"),
		okStep(t, testUsage, text("ok")),
	)
	got, err := h.provider.Complete(context.Background(), testPrompt)
	if err == nil {
		t.Fatal("error = nil, want an error")
	}
	if got.Text != "" || got.CostMicroUSD != 0 {
		t.Errorf("completion = %+v, want empty text and zero cost", got)
	}
	reqs := h.vendor.recorded()
	if len(reqs) != 1 {
		t.Errorf("requests = %d, want 1", len(reqs))
	}
	for _, req := range reqs {
		if req.Host != "api.anthropic.com" {
			t.Errorf("request sent to %s carrying x-api-key=%t", req.Host, req.Header.Get("x-api-key") != "")
		}
	}
}

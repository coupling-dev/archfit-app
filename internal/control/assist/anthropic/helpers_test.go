package anthropic

import (
	"bytes"
	"context"
	"encoding/json"
	"log/slog"
	"net/http"
	"sync"
	"testing"
	"time"

	"github.com/alexei-led/archfit-app/internal/control/assist"
)

// Recognizable strings that must never reach a log line or an error.
const (
	secretKey        = "sk-ant-SECRET-KEY-VALUE"
	secretPrompt     = "PROMPT-TEXT-MARKER"
	secretCompletion = "COMPLETION-TEXT-MARKER"
	secretVendorMsg  = "VENDOR-ERROR-MESSAGE-MARKER"
)

type harness struct {
	provider *provider
	vendor   *fakeVendor

	mu       sync.Mutex
	sleeps   []time.Duration
	timeouts []time.Duration
}

// newHarness builds a real provider over the fake vendor. Retry waits are recorded, never slept.
func newHarness(t *testing.T, model string, steps ...step) *harness {
	t.Helper()
	h := &harness{vendor: newFakeVendor(steps...)}
	p, err := New(Config{APIKey: secretKey, Model: model}, &http.Client{Transport: h.vendor})
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	h.provider = p.(*provider)
	h.provider.sleep = func(ctx context.Context, d time.Duration) error {
		h.mu.Lock()
		defer h.mu.Unlock()
		h.sleeps = append(h.sleeps, d)
		return ctx.Err()
	}
	return h
}

// expireAttempts makes every attempt's deadline pass at once and records the timeout it was given,
// so a hanging vendor costs fake time only.
func (h *harness) expireAttempts() {
	h.provider.attemptContext = func(ctx context.Context, d time.Duration) (context.Context, context.CancelFunc) {
		h.mu.Lock()
		defer h.mu.Unlock()
		h.timeouts = append(h.timeouts, d)
		return context.WithDeadline(ctx, time.Unix(0, 0))
	}
}

func (h *harness) slept() []time.Duration {
	h.mu.Lock()
	defer h.mu.Unlock()
	return append([]time.Duration(nil), h.sleeps...)
}

func (h *harness) fakeElapsed() time.Duration {
	h.mu.Lock()
	defer h.mu.Unlock()
	var total time.Duration
	for _, d := range h.sleeps {
		total += d
	}
	for _, d := range h.timeouts {
		total += d
	}
	return total
}

type usageJSON struct {
	InputTokens              int64 `json:"input_tokens"`
	OutputTokens             int64 `json:"output_tokens"`
	CacheCreationInputTokens int64 `json:"cache_creation_input_tokens,omitempty"`
	CacheReadInputTokens     int64 `json:"cache_read_input_tokens,omitempty"`
}

type blockJSON struct {
	Type string `json:"type"`
	Text string `json:"text,omitempty"`
}

func text(s string) blockJSON { return blockJSON{Type: "text", Text: s} }

func messageBody(t *testing.T, stopReason string, u usageJSON, blocks ...blockJSON) string {
	t.Helper()
	if blocks == nil {
		blocks = []blockJSON{}
	}
	raw, err := json.Marshal(map[string]any{
		"id":          "msg_01",
		"type":        "message",
		"role":        "assistant",
		"model":       "claude-sonnet-5",
		"content":     blocks,
		"stop_reason": stopReason,
		"usage":       u,
	})
	if err != nil {
		t.Fatalf("marshal message: %v", err)
	}
	return string(raw)
}

func errorBody(errType string) string {
	return `{"type":"error","error":{"type":"` + errType + `","message":"` + secretVendorMsg + `"}}`
}

var testPrompt = assist.Prompt{
	System:    "You edit policy files. " + secretPrompt,
	User:      "Tighten the rule. " + secretPrompt,
	MaxTokens: 1000,
}

var testUsage = usageJSON{InputTokens: 120, OutputTokens: 40}

type wantCost int

const (
	costZero wantCost = iota
	costUsage
	costMax
)

// failureRow is one failing Complete call, shared by the cost and the leak tests.
type failureRow struct {
	name   string
	step   func(t *testing.T, cancel context.CancelFunc) step
	expire bool // the attempt times out
	cost   wantCost
}

func fixed(s func(t *testing.T) step) func(*testing.T, context.CancelFunc) step {
	return func(t *testing.T, _ context.CancelFunc) step { return s(t) }
}

func failureRows() []failureRow {
	return []failureRow{
		{name: "400", cost: costZero, step: fixed(func(*testing.T) step {
			return respond(http.StatusBadRequest, errorBody("invalid_request_error"))
		})},
		{name: "401", cost: costZero, step: fixed(func(*testing.T) step {
			return respond(http.StatusUnauthorized, errorBody("authentication_error"))
		})},
		{name: "500", cost: costZero, step: fixed(func(*testing.T) step {
			return respond(http.StatusInternalServerError, errorBody("api_error"))
		})},
		{name: "200 max_tokens", cost: costUsage, step: fixed(func(t *testing.T) step {
			return respond(http.StatusOK, messageBody(t, "max_tokens", testUsage, text(secretCompletion)))
		})},
		{name: "200 refusal", cost: costUsage, step: fixed(func(t *testing.T) step {
			return respond(http.StatusOK, messageBody(t, "refusal", testUsage, text(secretCompletion)))
		})},
		{name: "200 no text block", cost: costUsage, step: fixed(func(t *testing.T) step {
			return respond(http.StatusOK, messageBody(t, "end_turn", testUsage, blockJSON{Type: "tool_use"}))
		})},
		{name: "200 invalid JSON", cost: costMax, step: fixed(func(*testing.T) step {
			return respond(http.StatusOK, `{"content":[{"type":"text","text":"`+secretCompletion)
		})},
		{name: "transport error after send", cost: costMax, step: fixed(func(*testing.T) step {
			return transportError
		})},
		{name: "attempt timeout", cost: costMax, expire: true, step: fixed(func(*testing.T) step {
			return hang
		})},
		{name: "caller cancels mid-flight", cost: costMax, step: func(_ *testing.T, cancel context.CancelFunc) step {
			return func(req *http.Request) (*http.Response, error) {
				cancel()
				return hang(req)
			}
		}},
	}
}

// run executes the row against a fresh harness.
func (r failureRow) run(t *testing.T) (*harness, assist.Completion, error) {
	t.Helper()
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	h := newHarness(t, "", r.step(t, cancel))
	if r.expire {
		h.expireAttempts()
	}
	got, err := h.provider.Complete(ctx, testPrompt)
	return h, got, err
}

func (r failureRow) wantCostValue(h *harness) int64 {
	switch r.cost {
	case costUsage:
		return 2*testUsage.InputTokens + 10*testUsage.OutputTokens
	case costMax:
		return h.provider.MaxCostMicroUSD(testPrompt)
	default:
		return 0
	}
}

// captureLogs swaps the default logger; tests using it must not run in parallel.
func captureLogs(t *testing.T) *bytes.Buffer {
	t.Helper()
	var buf bytes.Buffer
	prev := slog.Default()
	slog.SetDefault(slog.New(slog.NewJSONHandler(&buf, &slog.HandlerOptions{Level: slog.LevelDebug})))
	t.Cleanup(func() { slog.SetDefault(prev) })
	return &buf
}

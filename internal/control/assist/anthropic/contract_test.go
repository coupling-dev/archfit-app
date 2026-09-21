package anthropic

import (
	"context"
	"encoding/json"
	"net/http"
	"sort"
	"strings"
	"testing"

	"github.com/alexei-led/archfit-app/internal/control/assist"
)

func okStep(t *testing.T, u usageJSON, blocks ...blockJSON) step {
	t.Helper()
	return respond(http.StatusOK, messageBody(t, "end_turn", u, blocks...))
}

func TestCompleteSendsPinnedRequest(t *testing.T) {
	h := newHarness(t, "claude-opus-5", okStep(t, testUsage, text("ok")))
	prompt := assist.Prompt{System: "be brief", User: "say ok", MaxTokens: 64}
	if _, err := h.provider.Complete(context.Background(), prompt); err != nil {
		t.Fatalf("Complete: %v", err)
	}

	reqs := h.vendor.recorded()
	if len(reqs) != 1 {
		t.Fatalf("requests = %d, want 1", len(reqs))
	}
	req := reqs[0]
	if req.Method != http.MethodPost || req.URL != "https://api.anthropic.com/v1/messages" {
		t.Errorf("request = %s %s, want POST https://api.anthropic.com/v1/messages", req.Method, req.URL)
	}
	for name, want := range map[string]string{
		"x-api-key":         secretKey,
		"anthropic-version": "2023-06-01",
		"content-type":      "application/json",
	} {
		if got := req.Header.Get(name); got != want {
			t.Errorf("header %s = %q, want %q", name, got, want)
		}
	}

	var body map[string]json.RawMessage
	if err := json.Unmarshal(req.Body, &body); err != nil {
		t.Fatalf("request body is not a JSON object: %v", err)
	}
	keys := make([]string, 0, len(body))
	for k := range body {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	if got, want := strings.Join(keys, ","), "max_tokens,messages,model,system,thinking"; got != want {
		t.Errorf("body fields = %s, want %s", got, want)
	}
	for field, want := range map[string]string{
		"model":      `"claude-opus-5"`,
		"max_tokens": `64`,
		"system":     `"be brief"`,
		"messages":   `[{"role":"user","content":"say ok"}]`,
		"thinking":   `{"type":"disabled"}`,
	} {
		if got := string(body[field]); got != want {
			t.Errorf("body %s = %s, want %s", field, got, want)
		}
	}
}

func TestCompleteReturnsTextAndCost(t *testing.T) {
	h := newHarness(t, "claude-sonnet-5", okStep(t,
		usageJSON{InputTokens: 1000, OutputTokens: 500},
		text("first "), blockJSON{Type: "thinking"}, text("second"),
	))
	got, err := h.provider.Complete(context.Background(), testPrompt)
	if err != nil {
		t.Fatalf("Complete: %v", err)
	}
	if got.Text != "first second" {
		t.Errorf("Text = %q, want %q", got.Text, "first second")
	}
	if got.CostMicroUSD != 7000 {
		t.Errorf("CostMicroUSD = %d, want 7000", got.CostMicroUSD)
	}
}

func TestCompleteFailureCost(t *testing.T) {
	for _, row := range failureRows() {
		t.Run(row.name, func(t *testing.T) {
			h, got, err := row.run(t)
			if err == nil {
				t.Fatal("error = nil, want an error")
			}
			if got.Text != "" {
				t.Errorf("Text = %q, want empty", got.Text)
			}
			if want := row.wantCostValue(h); got.CostMicroUSD != want {
				t.Errorf("CostMicroUSD = %d, want %d", got.CostMicroUSD, want)
			}
			if n := len(h.vendor.recorded()); n != 1 {
				t.Errorf("requests = %d, want 1", n)
			}
		})
	}
}

func TestDefaultModel(t *testing.T) {
	h := newHarness(t, "", okStep(t, testUsage, text("ok")))
	if _, err := h.provider.Complete(context.Background(), testPrompt); err != nil {
		t.Fatalf("Complete: %v", err)
	}
	var body struct {
		Model string `json:"model"`
	}
	if err := json.Unmarshal(h.vendor.recorded()[0].Body, &body); err != nil {
		t.Fatalf("request body: %v", err)
	}
	if body.Model != "claude-sonnet-5" {
		t.Errorf("model = %q, want claude-sonnet-5", body.Model)
	}
}

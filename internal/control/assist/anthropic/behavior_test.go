package anthropic

import (
	"context"
	"errors"
	"net/http"
	"testing"
	"time"

	"github.com/alexei-led/archfit-app/internal/control/assist"
)

const draftPolicy = `version: 1
rules:
  - id: no-cycles
    severity: warn
`

const editedPolicy = `version: 1
rules:
  - id: no-cycles
    severity: error
`

func TestProposalRoundTrip(t *testing.T) {
	usage := usageJSON{InputTokens: 310, CacheReadInputTokens: 90, OutputTokens: 45}
	h := newHarness(t, "", okStep(t, usage, text(editedPolicy)))
	prompt := assist.Prompt{
		System:    "Return the full edited policy file and nothing else.",
		User:      "Policy:\n" + draftPolicy + "\nInstruction: make cycles an error.",
		MaxTokens: 2048,
	}
	got, err := h.provider.Complete(context.Background(), prompt)
	if err != nil {
		t.Fatalf("Complete: %v", err)
	}
	if got.Text != editedPolicy {
		t.Errorf("Text = %q, want %q", got.Text, editedPolicy)
	}
	if want := int64(2*(310+90) + 10*45); got.CostMicroUSD != want {
		t.Errorf("CostMicroUSD = %d, want %d", got.CostMicroUSD, want)
	}
	if reserved := h.provider.MaxCostMicroUSD(prompt); got.CostMicroUSD > reserved {
		t.Errorf("CostMicroUSD = %d exceeds the reserved %d", got.CostMicroUSD, reserved)
	}
}

func TestVendorOutageIsContained(t *testing.T) {
	h := newHarness(t, "", hang)
	h.expireAttempts()

	got, err := h.provider.Complete(context.Background(), testPrompt)
	if !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("error = %v, want context.DeadlineExceeded", err)
	}
	if got.Text != "" {
		t.Errorf("Text = %q, want empty", got.Text)
	}
	if want := h.provider.MaxCostMicroUSD(testPrompt); got.CostMicroUSD != want {
		t.Errorf("CostMicroUSD = %d, want the worst case %d", got.CostMicroUSD, want)
	}
	if elapsed := h.fakeElapsed(); elapsed > 60*time.Second {
		t.Errorf("Complete took %v of fake time, want at most 60s", elapsed)
	}
	if n := h.vendor.inFlight(); n != 0 {
		t.Errorf("%d requests still in flight after Complete returned", n)
	}

	h.vendor.setSteps(okStep(t, testUsage, text("recovered")))
	got, err = h.provider.Complete(context.Background(), testPrompt)
	if err != nil {
		t.Fatalf("Complete after recovery: %v", err)
	}
	if got.Text != "recovered" {
		t.Errorf("Text after recovery = %q, want recovered", got.Text)
	}
}

func TestTruncatedAnswerNeverBecomesText(t *testing.T) {
	half := editedPolicy[:len(editedPolicy)/2]
	usage := usageJSON{InputTokens: 300, OutputTokens: 20}
	h := newHarness(t, "", respond(http.StatusOK, messageBody(t, "max_tokens", usage, text(half))))

	got, err := h.provider.Complete(context.Background(), assist.Prompt{User: draftPolicy, MaxTokens: 20})
	if err == nil {
		t.Fatal("error = nil, want an error for a truncated answer")
	}
	if got.Text != "" {
		t.Errorf("Text = %q, want empty", got.Text)
	}
	if want := int64(2*300 + 10*20); got.CostMicroUSD != want {
		t.Errorf("CostMicroUSD = %d, want %d", got.CostMicroUSD, want)
	}
}

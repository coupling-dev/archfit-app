package feedback

import (
	"context"
	"errors"
	"testing"

	"github.com/alexei-led/archfit-app/internal/wire/result"
)

func TestWithdraw4UsesTheLiveReceiptInsideTheLease(t *testing.T) {
	for _, current := range []bool{true, false} {
		t.Run(map[bool]string{true: "current authority", false: "inputs moved"}[current], func(t *testing.T) {
			h := newHarness(headB)
			h.publish4(t, pub4(prTarget(headB), 9, 2, engineResult(t, "pass")))
			p := pub4(prTarget(headB), 0, 0, result.Result{Status: result.StatusUnknown, Summary: "archfit: unknown"})
			p.Current = func(_ context.Context) (bool, error) { return current, nil }
			err := h.pub4().Withdraw4(t.Context(), p)
			c := h.checks.latest(t, headB, gateCheck)
			if current {
				if err != nil || c.Conclusion != conclusionActionRequired || c.ExternalID != "v2:9:2:"+digest4 {
					t.Fatalf("withdrawal = %v, check %+v", err, c)
				}
			} else if !errors.Is(err, ErrStaleInputs) || c.Conclusion != conclusionSuccess {
				t.Fatalf("stale withdrawal = %v, check %+v", err, c)
			}
		})
	}
}

func TestWithdraw4CannotPublishASuccess(t *testing.T) {
	h := newHarness(headB)
	if err := h.pub4().Withdraw4(t.Context(), pub4(prTarget(headB), 0, 0, engineResult(t, "pass"))); err == nil {
		t.Fatal("withdrawal accepted a success")
	}
	if len(h.checks.named(headB, gateCheck)) != 0 {
		t.Fatal("refused withdrawal wrote a check")
	}
}

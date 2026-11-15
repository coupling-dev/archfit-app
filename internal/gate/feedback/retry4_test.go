package feedback

import (
	"context"
	"errors"
	"testing"
)

func TestPublish4RetriesGitHubFailures(t *testing.T) {
	for _, method := range []string{"CheckRuns", "CreateCheckRun", "AppComments", "CreateComment"} {
		t.Run(method, func(t *testing.T) {
			h := newHarness(headB)
			failed := errors.New("github unavailable")
			switch method {
			case "CheckRuns", "CreateCheckRun":
				h.checks.failOnce(method, failed)
			default:
				h.comments.failOnce(method, failed)
			}
			p := pub4(prTarget(headB), 7, 1, engineResult(t, "graph"))
			if err := h.pub.Publish4(context.Background(), p); !errors.Is(err, failed) {
				t.Fatalf("Publish4 = %v, want GitHub error", err)
			}
			h.clock.set(t0.Add(leaseTTL))
			if err := h.pub.Publish4(context.Background(), p); err != nil {
				t.Fatalf("retry: %v", err)
			}
			if runs := h.checks.named(headB, gateCheck); len(runs) != 1 || runs[0].Conclusion != conclusionFailure {
				t.Fatalf("checks after retry: %+v", runs)
			}
			if comments := h.comments.all(); len(comments) != 1 {
				t.Fatalf("comments after retry: %+v", comments)
			}
		})
	}
}

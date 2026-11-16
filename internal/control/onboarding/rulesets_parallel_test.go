package onboarding

import (
	"context"
	"slices"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/alexei-led/archfit-app/internal/github/api"
)

type parallelRules struct {
	*setupHub
	started      chan struct{}
	release      chan struct{}
	active, peak atomic.Int64
	fail         bool
}

func (h *parallelRules) GetRuleset(ctx context.Context, r api.RepoRef, id int64) (api.Ruleset, error) {
	active := h.active.Add(1)
	defer h.active.Add(-1)
	for old := h.peak.Load(); active > old; old = h.peak.Load() {
		if h.peak.CompareAndSwap(old, active) {
			break
		}
	}
	h.started <- struct{}{}
	select {
	case <-h.release:
	case <-ctx.Done():
		return api.Ruleset{}, ctx.Err()
	}
	if h.fail && id == 1 {
		return api.Ruleset{}, api.ErrRateLimited
	}
	return h.setupHub.GetRuleset(ctx, r, id)
}
func TestRulesetReadsOverlapAndUnreadableRulesStayUnknown(t *testing.T) {
	for _, fail := range []bool{false, true} {
		t.Run(map[bool]string{false: "success", true: "rate limit"}[fail], func(t *testing.T) {
			ctx, cancel := context.WithTimeout(t.Context(), 2*time.Second)
			defer cancel()
			base := newSetupHub()
			for id := int64(1); id <= 6; id++ {
				base.branch = append(base.branch, api.RepositoryRule{RulesetID: id, Type: "pull_request"})
				base.sets = append(base.sets, api.Ruleset{ID: id, Target: "branch", Enforcement: "active", CanBypass: "always"})
			}
			h := &parallelRules{setupHub: base, started: make(chan struct{}, 6), release: make(chan struct{}), fail: fail}
			s := &setups{gh: h}
			var out RulesetReport
			var wg sync.WaitGroup
			wg.Go(func() { out = s.rules(ctx, setupRepo) })
			for range 4 {
				select {
				case <-h.started:
				case <-ctx.Done():
					close(h.release)
					wg.Wait()
					t.Fatal("ruleset reads did not overlap")
				}
			}
			close(h.release)
			wg.Wait()
			if h.peak.Load() != 4 {
				t.Fatalf("concurrency = %d", h.peak.Load())
			}
			if fail && !slices.ContainsFunc(out.Findings, func(f RulesetFinding) bool { return f.Code == "policy_push" && f.State == "unknown" }) {
				t.Fatalf("unreadable rules became clean: %+v", out)
			}
			bypasses := 0
			for _, f := range out.Findings {
				if f.Code == "app_bypass" {
					bypasses++
				}
			}
			want := 6
			if fail {
				want = 5
			}
			if bypasses != want {
				t.Fatalf("bypass findings = %d; want %d", bypasses, want)
			}
		})
	}
}

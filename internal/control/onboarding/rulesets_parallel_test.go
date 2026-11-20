package onboarding

import (
	"context"
	"slices"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/coupling-dev/archfit-app/internal/github/api"
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

// detailCounts records which rulesets were read in full.
type detailCounts struct {
	*setupHub
	mu   sync.Mutex
	read []int64
	fail map[int64]bool
}

func (h *detailCounts) GetRuleset(ctx context.Context, r api.RepoRef, id int64) (api.Ruleset, error) {
	h.mu.Lock()
	h.read = append(h.read, id)
	h.mu.Unlock()
	if h.fail[id] {
		return api.Ruleset{}, api.ErrRateLimited
	}
	return h.setupHub.GetRuleset(ctx, r, id)
}

func TestRulesetDetailsAreReadOnlyWhereTheListItemCanMatter(t *testing.T) {
	branch := func(id int64, enforcement string) api.Ruleset {
		return api.Ruleset{ID: id, Target: "branch", Enforcement: enforcement, CanBypass: "never", BypassActors: &[]api.BypassActor{}}
	}
	org := setupRepo
	org.OwnerType, org.Visibility = "Organization", "private"
	personal := setupRepo
	personal.OwnerType, personal.Visibility = "User", "private"
	tests := []struct {
		name string
		repo api.Repo
		sets []api.Ruleset
		fail []int64
		want []int64
		// unknown names the finding that must be unknown after the reads
		unknown string
	}{
		{"inactive rulesets are not read", org,
			[]api.Ruleset{branch(1, "active"), branch(2, "disabled"), branch(3, "evaluate"), {ID: 8, Target: "push", Enforcement: "disabled"}}, nil, []int64{1}, ""},
		{"an unrelated active tag ruleset is not read", org,
			[]api.Ruleset{branch(1, "active"), {ID: 4, Target: "tag", Enforcement: "active"}}, nil, []int64{1}, ""},
		{"an unrelated branch ruleset is not read", org,
			[]api.Ruleset{branch(1, "active"), branch(5, "active")}, nil, []int64{1}, ""},
		{"an absent enforcement is read", org,
			[]api.Ruleset{{ID: 1, Target: "branch"}}, nil, []int64{1}, ""},
		{"an absent target is read", org,
			[]api.Ruleset{branch(1, "active"), {ID: 6, Enforcement: "active"}}, nil, []int64{1, 6}, ""},
		{"a push ruleset is read where it is supported", org,
			[]api.Ruleset{{ID: 7, Target: "push", Enforcement: "active"}, {ID: 9, Target: "push"}}, nil, []int64{7, 9}, ""},
		{"a push ruleset is not read where it is not supported", personal,
			[]api.Ruleset{{ID: 7, Target: "push", Enforcement: "active"}}, nil, nil, ""},
		{"an unreadable applicable ruleset stays unknown", org,
			[]api.Ruleset{branch(1, "active"), branch(2, "disabled")}, []int64{1}, []int64{1}, "administrator_bypass"},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			base := newSetupHub()
			base.branch = []api.RepositoryRule{{RulesetID: 1, Type: "pull_request"}}
			base.sets = tc.sets
			h := &detailCounts{setupHub: base, fail: map[int64]bool{}}
			for _, id := range tc.fail {
				h.fail[id] = true
			}
			report := (&setups{gh: h}).rules(t.Context(), tc.repo)
			slices.Sort(h.read)
			if !slices.Equal(h.read, tc.want) {
				t.Fatalf("details read = %v; want %v", h.read, tc.want)
			}
			if tc.unknown != "" && !slices.ContainsFunc(report.Findings, func(f RulesetFinding) bool { return f.Code == tc.unknown && f.State == "unknown" }) {
				t.Fatalf("%s is not unknown: %+v", tc.unknown, report.Findings)
			}
		})
	}
}

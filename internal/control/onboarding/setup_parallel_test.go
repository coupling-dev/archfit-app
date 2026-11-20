package onboarding

import (
	"context"
	"errors"
	"fmt"
	"maps"
	"slices"
	"sync"
	"testing"
	"testing/synctest"
	"time"

	"github.com/coupling-dev/archfit-app/internal/gate"
	"github.com/coupling-dev/archfit-app/internal/github/api"
	"github.com/coupling-dev/archfit-app/internal/store"
	"github.com/coupling-dev/archfit-app/internal/wire/policy3"
)

type parallelSetupHub struct {
	*setupHub
	started                chan string
	release                chan struct{}
	policyErr, workflowErr error
}

func (h *parallelSetupHub) File(ctx context.Context, r api.RepoRef, ref, path string) ([]byte, string, error) {
	h.started <- path
	select {
	case <-ctx.Done():
		return nil, "", ctx.Err()
	case <-h.release:
	}
	if path == ".archfit.yaml" && h.policyErr != nil {
		return nil, "", h.policyErr
	}
	if path == workflowPath && h.workflowErr != nil {
		return nil, "", h.workflowErr
	}
	return h.setupHub.File(ctx, r, ref, path)
}

func TestSetupReadsPolicyAndWorkflowConcurrentlyAndPreservesErrors(t *testing.T) {
	failure := errors.New("storage unavailable")
	for _, tc := range []struct {
		name                         string
		policyErr, workflowErr, want error
	}{
		{"success", nil, nil, nil},
		{"policy rate limit wins", api.ErrRateLimited, failure, api.ErrRateLimited},
		{"workflow rate limit", nil, api.ErrRateLimited, api.ErrRateLimited},
		{"policy timeout", context.DeadlineExceeded, nil, context.DeadlineExceeded},
		{"workflow failure", nil, failure, failure},
		{"missing policy", api.ErrNotFound, nil, nil},
		{"missing workflow", nil, api.ErrNotFound, nil},
	} {
		t.Run(tc.name, func(t *testing.T) {
			ctx, cancel := context.WithTimeout(t.Context(), 2*time.Second)
			defer cancel()
			base := newSetupHub()
			base.files["tip"] = map[string]string{".archfit.yaml": setupPolicy, workflowPath: setupWorkflow(setupCommit)}
			h := &parallelSetupHub{setupHub: base, started: make(chan string, 2), release: make(chan struct{}), policyErr: tc.policyErr, workflowErr: tc.workflowErr}
			s := &setups{gh: h, gate: setupView{}, actionApproved: func(ref string) bool { return ref == setupCommit }}
			var out Setup
			var err error
			var wg sync.WaitGroup
			wg.Go(func() { out, err = s.Setup(ctx, setupRepo) })
			for range 2 {
				select {
				case <-h.started:
				case <-ctx.Done():
					close(h.release)
					wg.Wait()
					t.Fatal("policy and workflow did not overlap")
				}
			}
			close(h.release)
			wg.Wait()
			if !errors.Is(err, tc.want) {
				t.Fatalf("error = %v; want %v", err, tc.want)
			}
			if err == nil && (out.Policy != (tc.policyErr == nil) || out.Workflow != (tc.workflowErr == nil)) {
				t.Fatalf("Setup = %+v", out)
			}
		})
	}
}

func TestSetupAndHomeReuseProtectedInputsButReadFreshApproval(t *testing.T) {
	h := newSetupHub()
	h.files["tip"] = map[string]string{".archfit.yaml": setupPolicy, workflowPath: setupWorkflow(setupCommit)}
	digest := policy3.Digest([]byte(setupPolicy))
	s := &setups{gh: h, gate: setupView{state: gate.SetupState{ApprovedDigest: digest}}, actionApproved: func(ref string) bool { return ref == setupCommit }}
	ctx, snapshot := gate.WithHomeSnapshot(t.Context(), setupRepo)
	out, err := s.Setup(ctx, setupRepo)
	if err != nil || out.State != "approved" {
		t.Fatalf("Setup = %+v, %v", out, err)
	}
	mem := store.NewMemory()
	key := "v4/repo/" + setupRepo.FullName
	if err := mem.Put(ctx, 7, key, []byte(`{"schema":"archfit-app.repo.v1","approved_digest":"`+digest+`"}`)); err != nil {
		t.Fatal(err)
	}
	home := gate.NewHome(h, mem, gate.NewAdmissions(mem, []string{"acme"}))
	view, err := home.Home(ctx, setupRepo)
	if err != nil || !view.PolicyAvailable || view.OutsideFlow {
		t.Fatalf("Home = %+v, %v", view, err)
	}
	if len(h.fileReads) != 2 || snapshot.CacheHits() != 3 {
		t.Fatalf("reads = %v, hits = %d", h.fileReads, snapshot.CacheHits())
	}
	if err := mem.Put(ctx, 7, key, []byte(`{"schema":"archfit-app.repo.v1","approved_digest":"sha256:changed"}`)); err != nil {
		t.Fatal(err)
	}
	view, err = home.Home(ctx, setupRepo)
	if err != nil || !view.OutsideFlow {
		t.Fatalf("fresh approval mismatch hidden: %+v, %v", view, err)
	}
}

// rulesFirstHub holds the tip read until the rules read has started: Setup must start the rules beside BranchHead,
// not after the tip, policy and workflow reads.
type rulesFirstHub struct {
	*setupHub
	rulesStarted chan struct{}
	once         sync.Once
}

func (h *rulesFirstHub) BranchPolicy(ctx context.Context, r api.RepoRef, branch string) ([]api.RepositoryRule, error) {
	h.once.Do(func() { close(h.rulesStarted) })
	return h.setupHub.BranchPolicy(ctx, r, branch)
}

func (h *rulesFirstHub) BranchHead(ctx context.Context, _ api.RepoRef, _ string) (string, error) {
	select {
	case <-h.rulesStarted:
		return "tip", nil
	case <-ctx.Done():
		return "", ctx.Err()
	}
}

func TestSetupStartsRulesBesideTheTipRead(t *testing.T) {
	ctx, cancel := context.WithTimeout(t.Context(), 2*time.Second)
	defer cancel()
	base := newSetupHub()
	base.files["tip"] = map[string]string{".archfit.yaml": setupPolicy, workflowPath: setupWorkflow(setupCommit)}
	base.branch = []api.RepositoryRule{{RulesetID: 1, Type: "pull_request"}}
	h := &rulesFirstHub{setupHub: base, rulesStarted: make(chan struct{})}
	s := &setups{gh: h, gate: setupView{}, actionApproved: func(ref string) bool { return ref == setupCommit }}
	out, err := s.Setup(ctx, setupRepo)
	if err != nil {
		t.Fatalf("Setup = %v; the rules read did not overlap the tip read", err)
	}
	if !slices.ContainsFunc(out.Rules.Findings, func(f RulesetFinding) bool { return f.Code == "pull_requests" && f.State == "ok" }) {
		t.Fatalf("rules findings = %+v", out.Rules.Findings)
	}
}

// slowHub is a GitHub that takes latency of virtual time per call and counts the calls by name.
type slowHub struct {
	*setupHub
	latency time.Duration
	mu      sync.Mutex
	calls   map[string]int
}

func (h *slowHub) call(name string) {
	h.mu.Lock()
	h.calls[name]++
	h.mu.Unlock()
	time.Sleep(h.latency)
}

func (h *slowHub) BranchHead(ctx context.Context, r api.RepoRef, b string) (string, error) {
	h.call("BranchHead")
	return h.setupHub.BranchHead(ctx, r, b)
}

func (h *slowHub) File(ctx context.Context, r api.RepoRef, ref, path string) ([]byte, string, error) {
	h.call("File")
	return h.setupHub.File(ctx, r, ref, path)
}

func (h *slowHub) BranchPolicy(ctx context.Context, r api.RepoRef, b string) ([]api.RepositoryRule, error) {
	h.call("BranchPolicy")
	return h.setupHub.BranchPolicy(ctx, r, b)
}

func (h *slowHub) ListRulesets(ctx context.Context, r api.RepoRef) ([]api.Ruleset, error) {
	h.call("ListRulesets")
	return h.setupHub.ListRulesets(ctx, r)
}

func (h *slowHub) GetRuleset(ctx context.Context, r api.RepoRef, id int64) (api.Ruleset, error) {
	h.call("GetRuleset")
	return h.setupHub.GetRuleset(ctx, r, id)
}

// Setup's longest chain is two GitHub round trips: the tip, then the policy and workflow; the rules (the branch rules
// and the list, then the one ruleset that matters) run beside them. Before the rules started first it was three
// for a repository with no rulesets, and four with one.
func TestSetupTakesTwoGitHubRoundTrips(t *testing.T) {
	const latency = 300 * time.Millisecond
	branchRuleset := api.Ruleset{ID: 1, Target: "branch", Enforcement: "active", CanBypass: "never", BypassActors: &[]api.BypassActor{}}
	for _, tc := range []struct {
		name string
		sets []api.Ruleset
		want map[string]int
	}{
		{"no rulesets", nil, map[string]int{"BranchHead": 1, "File": 2, "BranchPolicy": 1, "ListRulesets": 1}},
		{"one that matters, two that do not", []api.Ruleset{branchRuleset, {ID: 2, Target: "branch", Enforcement: "disabled"}, {ID: 3, Target: "tag", Enforcement: "active"}},
			map[string]int{"BranchHead": 1, "File": 2, "BranchPolicy": 1, "ListRulesets": 1, "GetRuleset": 1}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			synctest.Test(t, func(t *testing.T) {
				base := newSetupHub()
				base.files["tip"] = map[string]string{".archfit.yaml": setupPolicy, workflowPath: setupWorkflow(setupCommit)}
				base.branch = []api.RepositoryRule{{RulesetID: 1, Type: "pull_request"}}
				base.sets = tc.sets
				h := &slowHub{setupHub: base, latency: latency, calls: map[string]int{}}
				s := &setups{gh: h, gate: setupView{}, actionApproved: func(ref string) bool { return ref == setupCommit }}
				start := time.Now()
				if _, err := s.Setup(t.Context(), setupRepo); err != nil {
					t.Fatal(err)
				}
				elapsed := time.Since(start)
				t.Logf("Setup took %v and made %v", elapsed, h.calls)
				if elapsed != 2*latency {
					t.Errorf("Setup took %v; want two round trips (%v)", elapsed, 2*latency)
				}
				if !maps.Equal(h.calls, tc.want) {
					t.Errorf("GitHub calls = %v; want %v", h.calls, tc.want)
				}
			})
		})
	}
}

// headFailHub fails reads of one pull's head only, so the default branch reads are unaffected.
type headFailHub struct {
	*setupHub
	policyErr, workflowErr error
}

func (h *headFailHub) File(ctx context.Context, r api.RepoRef, ref, path string) ([]byte, string, error) {
	if ref == "head" && path == ".archfit.yaml" && h.policyErr != nil {
		return nil, "", h.policyErr
	}
	if ref == "head" && path == workflowPath && h.workflowErr != nil {
		return nil, "", h.workflowErr
	}
	return h.setupHub.File(ctx, r, ref, path)
}

// A pull's workflow read counts only when its policy is there, and a policy read error ends the scan, as the serial order
// did before the two reads of one head overlapped.
func TestOpenPullPolicyAndWorkflowKeepTheSerialAnswer(t *testing.T) {
	failure := errors.New("storage unavailable")
	for _, tc := range []struct {
		name                   string
		policy                 bool
		policyErr, workflowErr error
		wantErr                error
		wantFirst              int
	}{
		{"policy and workflow", true, nil, nil, nil, 9},
		{"workflow error without a policy is ignored", false, nil, failure, nil, 0},
		{"policy error wins over workflow error", false, failure, failure, failure, 0},
		{"workflow error with a policy", true, nil, failure, failure, 0},
	} {
		t.Run(tc.name, func(t *testing.T) {
			base := newSetupHub()
			base.files["tip"] = map[string]string{}
			base.pulls = []api.Pull{{Number: 9, State: "open", BaseRef: setupRepo.DefaultBranch, HeadRepo: setupRepo.FullName, HeadSHA: "head"}}
			base.files["head"] = map[string]string{workflowPath: setupWorkflow(setupCommit)}
			if tc.policy {
				base.files["head"][".archfit.yaml"] = setupPolicy
			}
			h := &headFailHub{setupHub: base, policyErr: tc.policyErr, workflowErr: tc.workflowErr}
			s := &setups{gh: h, gate: setupView{}, actionApproved: func(ref string) bool { return ref == setupCommit }}
			out, err := s.Setup(t.Context(), setupRepo)
			if !errors.Is(err, tc.wantErr) || (err == nil && out.FirstPull != tc.wantFirst) {
				t.Fatalf("Setup = %+v, %v; want error %v and first pull %d", out, err, tc.wantErr, tc.wantFirst)
			}
		})
	}
}

// Five open pull requests with no policy and no workflow take two windows of one round each, not five serial rounds.
func TestOpenPullScanTakesOneRoundPerWindow(t *testing.T) {
	const latency = 290 * time.Millisecond
	synctest.Test(t, func(t *testing.T) {
		base := newSetupHub()
		base.files["tip"] = map[string]string{".archfit.yaml": setupPolicy}
		for i := range 5 {
			base.pulls = append(base.pulls, api.Pull{Number: 400 + i, State: "open", BaseRef: "main", HeadRepo: setupRepo.FullName, HeadSHA: fmt.Sprintf("p%d", i)})
		}
		h := &slowHub{setupHub: base, latency: latency, calls: map[string]int{}}
		s := &setups{gh: h, gate: setupView{}, actionApproved: func(string) bool { return false }}
		start := time.Now()
		if _, err := s.Setup(t.Context(), setupRepo); err != nil {
			t.Fatal(err)
		}
		elapsed := time.Since(start)
		t.Logf("Setup with 5 pulls took %v, calls %v", elapsed, h.calls)
		// tip and policy/workflow at the tip take two rounds; the pulls take two windows of one round each.
		if want := 4 * latency; elapsed != want {
			t.Errorf("Setup took %v; want %v", elapsed, want)
		}
	})
}

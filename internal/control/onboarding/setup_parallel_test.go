package onboarding

import (
	"context"
	"errors"
	"slices"
	"sync"
	"testing"
	"time"

	"github.com/alexei-led/archfit-app/internal/gate"
	"github.com/alexei-led/archfit-app/internal/github/api"
	"github.com/alexei-led/archfit-app/internal/store"
	"github.com/alexei-led/archfit-app/internal/wire/policy3"
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

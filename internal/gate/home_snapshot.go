package gate

import (
	"context"
	"sync"
	"sync/atomic"

	"github.com/alexei-led/archfit-app/internal/gate/approvals"
	"github.com/alexei-led/archfit-app/internal/github/api"
	"github.com/alexei-led/archfit-app/internal/wire/policy3"
)

type snapshotKey struct{}

// HomeSnapshot holds only immutable default-branch inputs for a single Home request.
// Approval records, PR heads, permission checks and action eligibility are never kept across requests;
// WithRequestMemo covers the reads that repeat inside one.
type HomeSnapshot struct {
	repo         api.Repo
	tipOnce      sync.Once
	tip          string
	tipErr       error
	policyOnce   sync.Once
	policy       HomePolicy
	policyRaw    []byte
	policyBlob   string
	workflowOnce sync.Once
	workflow     []byte
	workflowErr  error
	hits         atomic.Int64
}

// HomePolicy retains read and decode errors separately: missing/invalid policy has
// different meanings during onboarding and when checking an already approved repository.
type HomePolicy struct {
	File               policy3.File
	Digest             string
	ReadErr, DecodeErr error
}

type snapshotGitHub interface {
	BranchHead(context.Context, api.RepoRef, string) (string, error)
	File(context.Context, api.RepoRef, string, string) ([]byte, string, error)
}

// WithHomeSnapshot scopes protected-tip reuse to one authorized repository and request.
func WithHomeSnapshot(ctx context.Context, r api.Repo) (context.Context, *HomeSnapshot) {
	s := &HomeSnapshot{repo: r}
	return context.WithValue(ctx, snapshotKey{}, s), s
}

// ReadHomeSnapshot resolves the protected tip once. Without a Home scope it reads afresh.
func ReadHomeSnapshot(ctx context.Context, gh snapshotGitHub, r api.Repo) (*HomeSnapshot, error) {
	s, ok := ctx.Value(snapshotKey{}).(*HomeSnapshot)
	if !ok || s.repo != r {
		s = &HomeSnapshot{repo: r}
	}
	loaded := false
	s.tipOnce.Do(func() {
		loaded = true
		s.tip, s.tipErr = gh.BranchHead(ctx, r.RepoRef, r.DefaultBranch)
	})
	if !loaded {
		s.hits.Add(1)
	}
	return s, s.tipErr
}

// Tip is the resolved immutable default-branch commit.
func (s *HomeSnapshot) Tip() string { return s.tip }

// CacheHits counts reused tip, policy and workflow reads in this request only.
func (s *HomeSnapshot) CacheHits() int64 { return s.hits.Load() }

// Policy reads and decodes the protected policy once. Callers must not mutate File.
func (s *HomeSnapshot) Policy(ctx context.Context, gh snapshotGitHub) HomePolicy {
	loaded := false
	s.policyOnce.Do(func() {
		loaded = true
		raw, blob, err := gh.File(ctx, s.repo.RepoRef, s.tip, homePolicyPath)
		s.policyRaw, s.policyBlob = raw, blob
		s.policy.ReadErr = err
		s.policy.File, s.policy.DecodeErr = policy3.Decode(raw)
		s.policy.Digest = policy3.Digest(raw)
	})
	if !loaded {
		s.hits.Add(1)
	}
	return s.policy
}

// Workflow reads the workflow at the same protected tip, never at a proposed PR head.
func (s *HomeSnapshot) Workflow(ctx context.Context, gh snapshotGitHub) ([]byte, error) {
	loaded := false
	s.workflowOnce.Do(func() {
		loaded = true
		s.workflow, _, s.workflowErr = gh.File(ctx, s.repo.RepoRef, s.tip, approvals.WorkflowPath)
	})
	if !loaded {
		s.hits.Add(1)
	}
	return s.workflow, s.workflowErr
}

// summaryInputs reuses only the Home request's protected files. Candidate heads
// and approval records are still read by the summary's existing eligibility path.
type summaryInputs struct {
	api.Repos
	snapshot *HomeSnapshot
}

func (g summaryInputs) BranchHead(ctx context.Context, r api.RepoRef, branch string) (string, error) {
	if r == g.snapshot.repo.RepoRef && branch == g.snapshot.repo.DefaultBranch {
		snapshot, err := ReadHomeSnapshot(ctx, g.Repos, g.snapshot.repo)
		return snapshot.Tip(), err
	}
	return g.Repos.BranchHead(ctx, r, branch)
}

func (g summaryInputs) File(ctx context.Context, r api.RepoRef, ref, path string) ([]byte, string, error) {
	if r == g.snapshot.repo.RepoRef && ref == g.snapshot.Tip() {
		switch path {
		case homePolicyPath:
			policy := g.snapshot.Policy(ctx, g.Repos)
			return g.snapshot.policyRaw, g.snapshot.policyBlob, policy.ReadErr
		case approvals.WorkflowPath:
			raw, err := g.snapshot.Workflow(ctx, g.Repos)
			return raw, "", err
		}
	}
	return g.Repos.File(ctx, r, ref, path)
}

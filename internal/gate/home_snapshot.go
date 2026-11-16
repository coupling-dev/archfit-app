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
// Approval records, PR heads, permission checks and action eligibility are never cached.
type HomeSnapshot struct {
	repo         api.Repo
	tipOnce      sync.Once
	tip          string
	tipErr       error
	policyOnce   sync.Once
	policy       HomePolicy
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
		raw, _, err := gh.File(ctx, s.repo.RepoRef, s.tip, homePolicyPath)
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

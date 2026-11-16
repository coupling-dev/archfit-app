package approvals

import (
	"context"

	"github.com/alexei-led/archfit-app/internal/github/api"
)

// SummaryInputs supplies immutable file inputs to the read-only setup summary.
// It must not cache approval records or replace the fresh inputs of Review or Act.
type SummaryInputs interface {
	BranchHead(context.Context, api.RepoRef, string) (string, error)
	File(context.Context, api.RepoRef, string, string) ([]byte, string, error)
}

// SummaryWithInputs reuses a request's protected inputs for navigation evidence
// only. The original service and every approval-authorizing path remain unchanged.
func (s *Service) SummaryWithInputs(ctx context.Context, r api.Repo, a Action, inputs SummaryInputs) (Summary, error) {
	view := *s
	view.gh = summaryGitHub{GitHub: s.gh, inputs: inputs}
	return view.Summary(ctx, r, a)
}

type summaryGitHub struct {
	GitHub
	inputs SummaryInputs
}

func (g summaryGitHub) BranchHead(ctx context.Context, r api.RepoRef, branch string) (string, error) {
	return g.inputs.BranchHead(ctx, r, branch)
}

func (g summaryGitHub) File(ctx context.Context, r api.RepoRef, ref, path string) ([]byte, string, error) {
	return g.inputs.File(ctx, r, ref, path)
}

package ui

import (
	"context"
	"net/http"
	"strings"

	"github.com/alexei-led/archfit-app/internal/gate"
	"github.com/alexei-led/archfit-app/internal/github/api"
)

const (
	approvalGranted = "granted"
	approvalMissing = "missing"
)

// errApprovalNotRun is an approval that was recorded while the re-run it asks for failed: the hourly sweep re-runs it.
var errApprovalNotRun = &userError{http.StatusBadGateway, "approved_not_rerun",
	"The approval was recorded, but the workflow could not be re-run now. Archfit retries within the hour; you can also re-run it on GitHub."}

// approvalJSON is the owner approval of a pull request head that changes a protected file; absent when it changes none.
type approvalJSON struct {
	HeadSHA string   `json:"head_sha"` // the full head the approval is for; Approve sends it back
	Paths   []string `json:"paths"`
	// Digests is each path's digest at the head, "" when deleted, null for a fork; Approve sends it back.
	Digests      map[string]string `json:"digests"`
	ShortDigests map[string]string `json:"short_digests"` // the same, cut for display
	State        string            `json:"state"`         // granted or missing
	By           string            `json:"by,omitempty"`
	CanApprove   bool              `json:"can_approve"`
	// Reason is why the signed-in user cannot approve while the approval is missing, in the words of a refused Approve.
	Reason        string `json:"reason,omitempty"`
	AuthorIsAgent bool   `json:"author_is_agent"` // the pull request was opened by an agent identity of the settings
}

// pullTarget is the pull request as the gate addresses it. The repository is the authorized one; the head and base
// come from GitHub. A head in another repository, or in a deleted one, is a fork.
func pullTarget(sc scope, p api.Pull) gate.Target {
	return gate.Target{
		Repo: sc.repo, PullRequest: p.Number, HeadSHA: p.HeadSHA, BaseRef: p.BaseRef,
		Fork: !strings.EqualFold(p.HeadRepo, sc.repo.FullName),
	}
}

// approval reads the owner approval of p's head for the signed-in user; nil when p changes no protected file.
func (s *server) approval(ctx context.Context, sc scope, p api.Pull) (*approvalJSON, error) {
	a, err := s.approvals.PullApproval(ctx, pullTarget(sc, p), sc.user.Login, p.Author)
	if err != nil || len(a.Paths) == 0 {
		return nil, err
	}
	out := &approvalJSON{
		HeadSHA: p.HeadSHA, Paths: a.Paths, Digests: a.Digests, State: approvalMissing,
		CanApprove: a.Refusal == nil, AuthorIsAgent: a.AuthorIsAgent,
	}
	if a.Digests != nil {
		out.ShortDigests = make(map[string]string, len(a.Digests))
		for path, d := range a.Digests {
			out.ShortDigests[path] = d[:min(len(d), shortSHA)]
		}
	}
	switch {
	case a.Granted:
		out.State, out.By, out.CanApprove = approvalGranted, a.ApprovedBy, false
	case a.Refusal != nil:
		_, _, out.Reason = explain(a.Refusal)
	}
	return out, nil
}

type approveJSON struct {
	HeadSHA string            `json:"head_sha"`
	Digests map[string]string `json:"digests"`
}

type approvedJSON struct {
	Approval *approvalJSON `json:"approval"`
	Notice   string        `json:"notice"`
}

// apiApprovePull is the Approve button: the signed-in owner approves the exact head and protected files the page
// showed, then the gate re-decides the pull request as it does for a review on GitHub.
func (s *server) apiApprovePull(r *http.Request, sc scope) (any, error) {
	number, ok := pullNumber(r)
	if !ok {
		return nil, api.ErrNotFound
	}
	var in approveJSON
	if err := decodeJSON(r, &in); err != nil {
		return nil, err
	}
	ctx := r.Context()
	p, err := s.gh.pulls.PullSummary(ctx, sc.repo.RepoRef, number)
	if err != nil {
		return nil, err
	}
	if in.HeadSHA != p.HeadSHA {
		return nil, conflict("The pull request has a new commit since this page was loaded. Nothing was approved. Reload the page and review it.")
	}
	t := pullTarget(sc, p)
	if err := s.approvals.Approve(ctx, t, sc.user.Login, in.Digests); err != nil {
		return nil, err
	}
	if err := s.approvals.Reevaluate(ctx, t); err != nil {
		return nil, errApprovalNotRun
	}
	a, err := s.approval(ctx, sc, p)
	if err != nil {
		return nil, errSavedUnread
	}
	return approvedJSON{Approval: a, Notice: notices["approved"]}, nil
}

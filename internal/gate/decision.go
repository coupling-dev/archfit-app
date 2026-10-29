package gate

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/alexei-led/archfit-app/internal/gate/decisions"
	"github.com/alexei-led/archfit-app/internal/gate/inputs"
	"github.com/alexei-led/archfit-app/internal/github/api"
	"github.com/alexei-led/archfit-app/internal/github/webhook"
	"github.com/alexei-led/archfit-app/internal/wire/decision"
	"github.com/alexei-led/archfit-app/internal/wire/policy"
	"github.com/alexei-led/archfit-app/internal/wire/settings"
)

// Reactions to a comment that carries a decision request, as logged.
const (
	reactionDecisionFiled    = "decision_filed"
	reactionDecisionAnswered = "decision_answered" // a status comment without a record
	reactionDecisionKept     = "decision_kept"     // a duplicate, a sticky decision, or a full queue: nothing written
)

const authorTypeBot = "Bot"

// requests is what the gate asks of gate/decisions. *decisions.Requests implements it.
type requests interface {
	File(ctx context.Context, t inputs.Target, f decisions.Filing, now time.Time) (decisions.Outcome, error)
	Answer(ctx context.Context, t inputs.Target, findingID, digest string, status decision.Status, reason string) (bool, error)
	List(ctx context.Context, t inputs.Target) ([]decisions.Record, error)
	Get(ctx context.Context, t inputs.Target, findingID, digest string) (decisions.Record, error)
	Policy(ctx context.Context, t inputs.Target, findingID, digest string) (string, error)
	SettleAt(ctx context.Context, t inputs.Target, findingID, digest, expectedHead string, status decision.Status, reason string, proposalPR int, now time.Time) error
	HeadMoved(ctx context.Context, t inputs.Target, now time.Time) error
	Closed(ctx context.Context, t inputs.Target, now time.Time) error
}

// commented takes in a decision request an unattended agent posted as a pull request comment. Nothing in the block is
// trusted: the pull request, its head, the settings and the policy digests come from GitHub and the protected ref. A
// comment on a pull request the gate does not decide (a fork, a closed pull request, another base, a repository that
// is not onboarded or not in required mode) is ignored without a word. A request that fails a check gets no record
// and one status comment that says why.
func (g *Gate) commented(ctx context.Context, e webhook.Event) (string, error) {
	if !decision.HasRequest(e.Body) {
		return reactionNone, nil
	}
	ref := api.RepoRef{InstallationID: e.InstallationID, FullName: e.Repo}
	if e.AuthorType == authorTypeBot {
		// The App's own comments never hold a request block; this keeps its answers from ever being read as input.
		own, err := g.comments.AppComments(ctx, ref, e.PullRequest)
		if err != nil {
			return reactionNone, fmt.Errorf("gate: list comments: %w", err)
		}
		for _, c := range own {
			if c.ID == e.CommentID {
				return reactionNone, nil
			}
		}
	}
	pl, err := g.pulls.Pull(ctx, ref, e.PullRequest)
	if errors.Is(err, api.ErrNotFound) {
		return reactionDecisionIgnored, nil
	}
	if err != nil {
		return reactionNone, fmt.Errorf("gate: pull request: %w", err)
	}
	t := eventTarget(e)
	t.HeadSHA, t.BaseRef, t.Fork = pl.HeadSHA, pl.BaseRef, pl.HeadRepo != e.Repo
	if pl.State != "open" || t.Fork || e.DefaultBranch == "" || pl.BaseRef != e.DefaultBranch {
		return reactionDecisionIgnored, nil
	}
	trusted, err := g.resolver.Resolve(ctx, t, g.now())
	if err != nil {
		return reactionNone, fmt.Errorf("gate: resolve inputs: %w", err)
	}
	if !trusted.SettingsFound || trusted.Settings.Gate != settings.GateRequired {
		return reactionDecisionIgnored, nil
	}

	raw, err := decision.Extract(e.Body)
	if err != nil {
		return g.refuse(ctx, t, decision.Unknown, decision.Unknown, decision.StatusDeclined, reason(err))
	}
	req, err := decision.Decode(raw)
	if err != nil {
		findingID, digest := decision.Identify(raw)
		return g.refuse(ctx, t, findingID, digest, decision.StatusDeclined, reason(err))
	}
	switch {
	case req.HeadSHA != t.HeadSHA:
		return g.refuse(ctx, t, req.FindingID, req.PolicyDigest, decision.StatusStale, decision.ReasonHeadMoved)
	case !inputs.Accepts(trusted.PolicyDigests, req.PolicyDigest):
		return g.refuse(ctx, t, req.FindingID, req.PolicyDigest, decision.StatusStale, decision.ReasonPolicyChanged)
	}
	if diags := policy.Diagnose([]byte(req.Proposal.Policy)); len(diags) > 0 {
		why := "proposal.policy: not a valid policy file"
		if diags[0].Line > 0 {
			why += fmt.Sprintf(", line %d", diags[0].Line)
		}
		return g.refuse(ctx, t, req.FindingID, req.PolicyDigest, decision.StatusDeclined, why)
	}

	outcome, err := g.requests.File(ctx, t, decisions.Filing{Request: req, Requester: e.Author, CommentID: e.CommentID}, g.now())
	switch {
	case errors.Is(err, decisions.ErrTooLarge):
		return g.refuse(ctx, t, req.FindingID, req.PolicyDigest, decision.StatusDeclined, "request: too large to store")
	case err != nil:
		// The record is kept when only the status comment failed; the retry finds it a duplicate and writes the comment.
		return reactionDecisionFiled, fmt.Errorf("gate: file decision request: %w", err)
	case outcome == decisions.Filed || outcome == decisions.Refiled:
		return reactionDecisionFiled, nil
	}
	return reactionDecisionKept, nil
}

func (g *Gate) refuse(ctx context.Context, t inputs.Target, findingID, digest string, status decision.Status, why string) (string, error) {
	written, err := g.requests.Answer(ctx, t, findingID, digest, status, why)
	switch {
	case err != nil:
		return reactionDecisionAnswered, fmt.Errorf("gate: answer decision request: %w", err)
	case !written:
		return reactionDecisionKept, nil
	}
	return reactionDecisionAnswered, nil
}

// reason is a decode error without its package prefix. Decode and Extract errors name a field and a rule, never a value.
func reason(err error) string { return strings.TrimPrefix(err.Error(), "decision: ") }

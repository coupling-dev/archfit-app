package gate

import (
	"context"
	"errors"
	"fmt"
	"strings"

	"github.com/alexei-led/archfit-app/internal/gate/decisions"
	"github.com/alexei-led/archfit-app/internal/gate/inputs"
	"github.com/alexei-led/archfit-app/internal/github/api"
	"github.com/alexei-led/archfit-app/internal/wire/decision"
)

// The refusals of a decision. Any other error is a failed read or write. The owner rule is that of an approval:
// ErrAgentIdentity and ErrNotOwner.
var (
	// ErrDecisionRequester refuses the login that filed the request: whoever asks never decides.
	ErrDecisionRequester = errors.New("gate: decision: the requester cannot decide its own request")
	// ErrDecisionSettled means the request is not pending: approved, declined or stale, it never moves again.
	ErrDecisionSettled = errors.New("gate: decision: the request is not pending")
	// ErrDecisionStale means the request no longer matches the pull request or the policy; it is now marked stale.
	ErrDecisionStale = errors.New("gate: decision: the request is stale")
	// ErrDecisionNotFound means there is no such request, or no such pull request.
	ErrDecisionNotFound = errors.New("gate: decision: no such request")
)

// ErrStatusComment is gate/decisions' error for a status comment that could not be written. After a decision it
// means the request did move.
var ErrStatusComment = decisions.ErrStatusComment

// DecisionRecord is gate/decisions' record of a request, owned by that child.
type DecisionRecord = decisions.Record

// Decisions is what the App's pull request page may do about the decision requests of a pull request: list them,
// decline one, and approve one in two steps (ApproveDecision, then the page opens the proposal, then
// ApprovedDecision). *Gate implements it. Nothing here grants anything to the requester.
type Decisions interface {
	DecisionRequests(ctx context.Context, t Target, login string) ([]DecisionRequest, error)
	ApproveDecision(ctx context.Context, t Target, login, findingID, digest string) (DecisionCase, error)
	ApprovedDecision(ctx context.Context, t Target, findingID, digest, head string, proposalPR int) error
	DeclineDecision(ctx context.Context, t Target, login, findingID, digest string) error
}

// DecisionRequest is a request as the pull request page lists it.
type DecisionRequest struct {
	DecisionRecord
	CanDecide bool // login is an owner, is no agent, did not file the request, and the request is pending
}

// DecisionCase is a request an owner may approve now: it is pending, on the pull request's current head, under a
// policy the protected ref still accepts.
type DecisionCase struct {
	Record DecisionRecord
	Policy string // the candidate policy the agent proposed, verbatim
}

// DecisionRequests lists the requests of t's pull request. t is the page's target: the repository and the pull
// request as GitHub reports them. CanDecide is computed for login, and only while a request is pending.
func (g *Gate) DecisionRequests(ctx context.Context, t Target, login string) ([]DecisionRequest, error) {
	if err := g.admitted(ctx, t); err != nil {
		return nil, err
	}
	recs, err := g.requests.List(ctx, t)
	if err != nil {
		return nil, err
	}
	owner := false
	for _, rec := range recs {
		if rec.Status == decision.StatusPending {
			if owner, err = g.mayDecide(ctx, t, login); err != nil {
				return nil, err
			}
			break
		}
	}
	out := make([]DecisionRequest, 0, len(recs))
	for _, rec := range recs {
		out = append(out, DecisionRequest{
			DecisionRecord: rec, CanDecide: owner && rec.Status == decision.StatusPending && !filedBy(rec, login),
		})
	}
	return out, nil
}

// mayDecide reports whether login passes the owner rule; any other failure is an error.
func (g *Gate) mayDecide(ctx context.Context, t Target, login string) (bool, error) {
	switch err := g.resolver.CanDecide(ctx, t, login); {
	case err == nil:
		return true, nil
	case errors.Is(err, ErrAgentIdentity) || errors.Is(err, ErrNotOwner):
		return false, nil
	default:
		return false, err
	}
}

func filedBy(rec decisions.Record, login string) bool { return strings.EqualFold(rec.Requester, login) }

// ApproveDecision checks that login may approve the request and that it still holds, from GitHub and the protected
// ref, not from the record: the pull request is open on the head the record names, and the policy digest it was
// measured under is still accepted. A request that does not hold is marked stale and ErrDecisionStale is returned.
// t names the repository and the pull request; the rest of it is read from GitHub here.
func (g *Gate) ApproveDecision(ctx context.Context, t Target, login, findingID, digest string) (DecisionCase, error) {
	d, err := g.authorizeDecision(ctx, t, login, findingID, digest)
	if err != nil {
		return DecisionCase{}, err
	}
	reason, err := g.staleReason(ctx, d)
	if err != nil {
		return DecisionCase{}, err
	}
	if reason != "" {
		if err := g.settle(ctx, d.target, findingID, digest, d.rec.HeadSHA, decision.StatusStale, reason, 0); err != nil {
			return DecisionCase{}, err
		}
		return DecisionCase{}, ErrDecisionStale
	}
	text, err := g.requests.Policy(ctx, d.target, findingID, digest)
	if err != nil {
		return DecisionCase{}, notFound(err)
	}
	return DecisionCase{Record: d.rec, Policy: text}, nil
}

// staleReason is why the request no longer holds, "" when it does.
func (g *Gate) staleReason(ctx context.Context, d authorized) (string, error) {
	switch {
	case !d.open:
		return decision.ReasonPullClosed, nil
	case d.rec.HeadSHA != d.target.HeadSHA:
		return decision.ReasonHeadMoved, nil
	}
	trusted, err := g.resolver.Resolve(ctx, d.target, g.now())
	if err != nil {
		return "", fmt.Errorf("gate: resolve inputs: %w", err)
	}
	if !inputs.Accepts(trusted.PolicyDigests, d.rec.PolicyDigest) {
		return decision.ReasonPolicyChanged, nil
	}
	return "", nil
}

// ApprovedDecision records that the proposal pull request exists. The caller has opened it after ApproveDecision.
func (g *Gate) ApprovedDecision(ctx context.Context, t Target, findingID, digest, head string, proposalPR int) error {
	if err := g.admitted(ctx, t); err != nil {
		return err
	}
	return g.settle(ctx, t, findingID, digest, head, decision.StatusApproved, "", proposalPR)
}

// DeclineDecision declines the request for login. It stays declined until the policy digest changes.
func (g *Gate) DeclineDecision(ctx context.Context, t Target, login, findingID, digest string) error {
	d, err := g.authorizeDecision(ctx, t, login, findingID, digest)
	if err != nil {
		return err
	}
	return g.settle(ctx, d.target, findingID, digest, d.rec.HeadSHA, decision.StatusDeclined, decision.ReasonOwnerDeclined, 0)
}

// authorized is a request login may decide, with the pull request as GitHub reports it.
type authorized struct {
	target Target // completed from GitHub: head, base, fork
	open   bool
	rec    DecisionRecord
}

// authorizeDecision runs the checks both decisions share, in this order so the first failure is the one worded: the
// installation is admitted, the pull request exists, login passes the owner rule of the protected ref, the request
// exists, login did not file it, and it is pending.
func (g *Gate) authorizeDecision(ctx context.Context, t Target, login, findingID, digest string) (authorized, error) {
	if err := g.admitted(ctx, t); err != nil {
		return authorized{}, err
	}
	pl, err := g.pulls.Pull(ctx, t.Repo.RepoRef, t.PullRequest)
	if err != nil {
		return authorized{}, notFound(err)
	}
	t.HeadSHA, t.BaseRef, t.Fork = pl.HeadSHA, pl.BaseRef, !strings.EqualFold(pl.HeadRepo, t.Repo.FullName)
	if err := g.resolver.CanDecide(ctx, t, login); err != nil {
		return authorized{}, err
	}
	rec, err := g.requests.Get(ctx, t, findingID, digest)
	switch {
	case err != nil:
		return authorized{}, notFound(err)
	case filedBy(rec, login):
		return authorized{}, ErrDecisionRequester
	case rec.Status != decision.StatusPending:
		return authorized{}, ErrDecisionSettled
	}
	return authorized{target: t, open: pl.State == "open", rec: rec}, nil
}

// settle moves the request only while its head is still head, through the same publish path as every status write.
func (g *Gate) settle(ctx context.Context, t Target, findingID, digest, head string, status decision.Status, reason string, proposalPR int) error {
	err := g.requests.SettleAt(ctx, t, findingID, digest, head, status, reason, proposalPR, g.now())
	switch {
	case errors.Is(err, decisions.ErrNotPending), errors.Is(err, decisions.ErrChanged):
		return ErrDecisionSettled
	case errors.Is(err, decisions.ErrNoRecord):
		return ErrDecisionNotFound
	}
	return err
}

// notFound turns a missing request or pull request into ErrDecisionNotFound; other errors pass.
func notFound(err error) error {
	if errors.Is(err, decisions.ErrNoRecord) || errors.Is(err, api.ErrNotFound) {
		return ErrDecisionNotFound
	}
	return err
}

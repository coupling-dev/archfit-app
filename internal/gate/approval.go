package gate

import (
	"context"
	"errors"
	"fmt"
	"log/slog"

	"github.com/alexei-led/archfit-app/internal/gate/inputs"
)

// Approvals is what the App's pull request page may do about an owner approval of a head: read it, record one and
// re-decide the pull request. *Gate implements it; no other part of the gate path is reachable through it.
type Approvals interface {
	PullApproval(ctx context.Context, t Target, login, author string) (PullApproval, error)
	Approve(ctx context.Context, t Target, login string, seen map[string]string) error
	Reevaluate(ctx context.Context, t Target) error
}

// PullApproval is the owner approval of a pull request head as the App's pull request page shows it.
type PullApproval struct {
	Candidate            // no Paths: the pull request needs no owner approval, and nothing below is set
	Granted       bool   // an owner approved this exact head, on GitHub or in the App
	ApprovedBy    string // the approving login when Granted
	Refusal       error  // while not Granted: why login may not approve in the App (an Err* refusal); nil when it may
	AuthorIsAgent bool   // the pull request was opened by an agent identity of the protected settings
}

// errNotAdmitted refuses an installation no operator approved. The App's pages answer it before they get here.
var errNotAdmitted = errors.New("gate: installation not admitted")

// PullApproval reads the owner approval of t's head for the signed-in login; author is the pull request's.
func (g *Gate) PullApproval(ctx context.Context, t Target, login, author string) (PullApproval, error) {
	if err := g.admitted(ctx, t); err != nil {
		return PullApproval{}, err
	}
	c, err := g.resolver.Candidate(ctx, t)
	if err != nil || len(c.Paths) == 0 {
		return PullApproval{}, err
	}
	trusted, err := g.resolver.Resolve(ctx, t, g.now())
	if err != nil {
		return PullApproval{}, fmt.Errorf("gate: resolve inputs: %w", err)
	}
	out := PullApproval{
		Candidate: c, Granted: trusted.Approval == inputs.ApprovalGranted, ApprovedBy: trusted.ApprovedBy,
		AuthorIsAgent: trusted.Settings.IsAgent(author),
	}
	if out.Granted {
		return out, nil
	}
	switch err := g.resolver.CanApprove(ctx, t, login); {
	case refusal(err):
		out.Refusal = err
	case err != nil:
		return PullApproval{}, err
	}
	return out, nil
}

// refusal reports whether err is one of the refusals of an approval, as opposed to a failed read.
func refusal(err error) bool {
	for _, r := range []error{ErrNothingToApprove, ErrForkPull, ErrAgentIdentity, ErrNotOwner, ErrDigestsChanged} {
		if errors.Is(err, r) {
			return true
		}
	}
	return false
}

// Approve records login's approval of t's head in the App: the way a sole owner approves, because GitHub does not let
// an author approve their own pull request. seen is the Candidate digests the owner was shown. It does not re-decide
// the pull request; Reevaluate does.
func (g *Gate) Approve(ctx context.Context, t Target, login string, seen map[string]string) error {
	if err := g.admitted(ctx, t); err != nil {
		return err
	}
	return g.resolver.Approve(ctx, t, login, seen, g.now())
}

// Reevaluate re-decides the pull request t after its owner approval changed, as a review event does: an
// owner_approval_missing check whose approval is now granted re-runs the workflow, and a result whose approval is gone
// is withdrawn. Nothing here turns a check green; the re-run's upload does.
func (g *Gate) Reevaluate(ctx context.Context, t Target) error {
	if err := g.admitted(ctx, t); err != nil {
		return err
	}
	reaction, err := g.approvalChanged(ctx, t)
	slog.Default().InfoContext(ctx, "gate reevaluate",
		"installation_id", t.Repo.InstallationID, "pr", t.PullRequest, "head_sha", t.HeadSHA,
		"reaction", reaction, "failed", err != nil)
	return err
}

// admitted is the gate's own admission guard for the entry points the App's pages call.
func (g *Gate) admitted(ctx context.Context, t Target) error {
	ok, err := g.admissions.Admitted(ctx, t.Repo.InstallationID, inputs.Owner(t.Repo.FullName))
	if err != nil {
		return fmt.Errorf("gate: read admission: %w", err)
	}
	if !ok {
		return errNotAdmitted
	}
	return nil
}

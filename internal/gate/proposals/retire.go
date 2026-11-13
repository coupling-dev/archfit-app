package proposals

import (
	"context"
	"fmt"
	"strconv"
	"strings"
	"time"

	"github.com/alexei-led/archfit-app/internal/github/api"
)

// Why a proposal's head no longer carries its verdict.
const (
	RetiredUsed       = "used"       // its merge push used its approvals
	RetiredVoid       = "void"       // it was closed, merged another policy, or its approvals no longer apply
	RetiredSuperseded = "superseded" // the App wrote a newer head for it
	RetiredForeign    = "foreign"    // another pull request has this commit as its head
)

// GateCheck is the name of the required check the App sets on a proposal's head.
const GateCheck = gateCheck

// externalPrefix starts the external id of every archfit/gate run the App sets on a proposal's head.
const externalPrefix = "proposal:"

// conclusionNoVerdict is what a retired head shows: no verdict, as for an unknown result (design 11.6). It never
// satisfies a required check.
const conclusionNoVerdict = "action_required"

// CheckWriter is the role Retire needs of GitHub.
type CheckWriter interface {
	CreateCheckRun(ctx context.Context, r api.RepoRef, c api.CheckRun) (api.CheckRun, error)
}

// Retire takes back the verdict a proposal's head carries (M2 review H2): a newer, completed archfit/gate run on head
// with no success, which is the run the ruleset reads. GitHub ties a check to the commit, not to the pull request, and
// the App squash-merges, so without it another pull request at the same commit would merge on the old success after
// the approval was used. pull is the proposal's pull request, for the link; why is one of the Retired values.
func Retire(ctx context.Context, gh CheckWriter, r api.Repo, head string, pull int, proposalID, why string, now time.Time) error {
	if head == "" {
		return nil
	}
	_, err := gh.CreateCheckRun(ctx, r.RepoRef, api.CheckRun{
		Name:       gateCheck,
		HeadSHA:    head,
		ExternalID: externalPrefix + proposalID + ":retired",
		DetailsURL: "https://github.com/" + r.FullName + "/pull/" + strconv.Itoa(pull),
		Status:     statusCompleted,
		Conclusion: conclusionNoVerdict,
		Title:      retiredTitle,
		Summary:    retiredSummary(pull, why),
		StartedAt:  now.UTC(),
	})
	if err != nil {
		return fmt.Errorf("proposals: retire the check of %s: %w", head, err)
	}
	return nil
}

// retire is Retire as a step of an answer that stands: a failure is logged, and the head of a pull request opened at
// that commit is retired when it opens (gate, owner.go).
func (s *Service) retire(ctx context.Context, r api.Repo, head string, pull int, proposalID, why string) {
	if err := Retire(ctx, s.gh, r, head, pull, proposalID, why, s.now()); err != nil {
		logFailure(ctx, r, "retire a proposal head's check", err)
	}
}

// ProposalOf returns the proposal id of an archfit/gate run the App set on a proposal's head, and whether it is one.
func ProposalOf(c api.CheckRun) (string, bool) {
	id, ok := strings.CutPrefix(c.ExternalID, externalPrefix)
	if !ok || id == "" || strings.Contains(id, ":") {
		return "", false
	}
	return id, true
}

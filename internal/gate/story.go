package gate

import (
	"context"
	"errors"
	"regexp"
	"strings"

	"github.com/coupling-dev/archfit-app/internal/gate/feedback"
	"github.com/coupling-dev/archfit-app/internal/gate/records"
	"github.com/coupling-dev/archfit-app/internal/github/api"
	"github.com/coupling-dev/archfit-app/internal/store"
	"github.com/coupling-dev/archfit-app/internal/wire/policy3"
)

// personLogin is a GitHub user login: an organization's team ("acme/arch") or a placeholder is never mentioned.
var personLogin = regexp.MustCompile(`^[A-Za-z0-9](?:[A-Za-z0-9]|-[A-Za-z0-9]){0,38}$`)

// storyGitHub is what the story reader reads on GitHub.
type storyGitHub interface {
	File(ctx context.Context, r api.RepoRef, ref, path string) (content []byte, blobSHA string, err error)
	AccountType(ctx context.Context, r api.RepoRef, login string) (string, error)
}

// storyReader reads the facts of a request for the story line of a pull request's check: the request's state, whether
// it waits on the first approval, the App's policy pull request that carries its answer, and the person who answers. The policy declares owners for the
// whole repository only, so that person is its first owner who is a person: a login GitHub says is a User, never an
// organization; "" when it names none, or when GitHub cannot say.
func storyReader(files storyGitHub, s store.Store) feedback.StoryReader {
	return func(ctx context.Context, r api.Repo, request int) (feedback.StoryFacts, error) {
		t := store.Tenant(r.InstallationID)
		var facts feedback.StoryFacts
		var req records.Request
		if err := records.Load(ctx, s, t, records.RequestKey(r.FullName, request), &req); err != nil {
			return facts, err
		}
		facts.State = req.State
		// No answer can be written before an administrator approves the first policy, which the default branch holds.
		var repo records.Repo
		if req.State == records.StateOpen {
			err := records.Load(ctx, s, t, records.RepoKey(r.FullName), &repo)
			if err != nil && !errors.Is(err, records.ErrNoRecord) {
				return facts, err
			}
		}
		if req.Approval != "" {
			var a records.Approval
			if err := records.Load(ctx, s, t, records.ApprovalKey(r.FullName, req.Approval), &a); err != nil {
				return facts, err
			}
			if a.Proposal != "" {
				var p records.Proposal
				if err := records.Load(ctx, s, t, records.ProposalKey(r.FullName, a.Proposal), &p); err != nil {
					return facts, err
				}
				facts.PolicyPull, facts.PolicyMerged = p.PullRequest, p.State == records.StateMerged
			}
		}
		raw, _, err := files.File(ctx, r.RepoRef, r.DefaultBranch, policyPath)
		switch {
		case errors.Is(err, api.ErrNotFound):
			return facts, nil
		case err != nil:
			return facts, err
		}
		pol, err := policy3.Decode(raw)
		if err != nil {
			return facts, nil
		}
		facts.FirstApproval = req.State == records.StateOpen && repo.ApprovedDigest == ""
		if login := firstPerson(pol.Owners); login != "" {
			if kind, err := files.AccountType(ctx, r.RepoRef, login); err == nil && kind == "User" {
				facts.Owner = login
			}
		}
		return facts, nil
	}
}

// firstPerson is the first owner that is a person's login, without a leading "@"; "" when none is.
func firstPerson(owners []string) string {
	for _, o := range owners {
		if login := strings.TrimPrefix(o, "@"); personLogin.MatchString(login) {
			return login
		}
	}
	return ""
}

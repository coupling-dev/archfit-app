package approvals

import (
	"context"
	"regexp"
	"strconv"
	"strings"

	"github.com/coupling-dev/archfit-app/internal/gate/feedback"
	"github.com/coupling-dev/archfit-app/internal/gate/records"
	"github.com/coupling-dev/archfit-app/internal/gate/requests"
	"github.com/coupling-dev/archfit-app/internal/github/api"
	"github.com/coupling-dev/archfit-app/internal/wire/policy3"
)

// The words of the issue of a policy changed outside the flow and of the notes (design 5). They hold only the App's
// own words, a branch name in a code span, issue numbers and commit SHAs: no worker or owner text.
const outsideTitle = "archfit: the policy changed outside the flow"

// ownerPages are the links of an issue about a policy changed outside the flow; "" without an App URL.
type ownerPages struct {
	confirm, revert string
	revertOnly      bool // the policy on the default branch is not valid, so it cannot be confirmed
}

// outsideLinks are the confirm and revert pages of the repository.
// A policy at commit that cannot be read or is not valid leaves only the revert.
func (s *Service) outsideLinks(ctx context.Context, r api.Repo, commit string) ownerPages {
	raw, err := s.policy(ctx, r.RepoRef, commit)
	_, derr := policy3.Decode(raw)
	return ownerPages{
		confirm:    feedback.OwnerLink(s.appURL, r.FullName, "/owner/confirm"),
		revert:     feedback.OwnerLink(s.appURL, r.FullName, "/owner/revert"),
		revertOnly: err == nil && (raw == nil || derr != nil),
	}
}

// next is the first line of the issue and of its notes: confirm, with revert as the other way.
func (l ownerPages) next() string {
	if l.confirm == "" {
		return "**Next:** an owner confirms or reverts the policy in the App (owner, passkey)\n"
	}
	if l.revertOnly {
		return feedback.NextLine("Revert the policy", l.revert, "owner") +
			"The policy on the default branch is not valid, so it cannot be confirmed.\n"
	}
	return feedback.NextLine("Confirm the policy", l.confirm, "owner") +
		"Or [revert it](" + l.revert + ") to restore the approved policy.\n"
}

// loginName is a GitHub login: at most 39 letters, digits and single hyphens inside.
var loginName = regexp.MustCompile(`^[A-Za-z0-9](?:[A-Za-z0-9-]{0,37}[A-Za-z0-9])?$`)

// cell makes s safe inside a code span in a table cell.
func cell(s string) string {
	return strings.NewReplacer("|", "/", "`", "'", "\r", " ", "\n", " ").Replace(s)
}

func outsideBody(l ownerPages, branch, commit string, owners []string) string {
	who := "an owner named in the approved policy"
	var names []string
	for _, o := range owners {
		if loginName.MatchString(o) { // policy text is untrusted: only a real login goes in the table
			names = append(names, o)
		}
	}
	if len(names) > 0 {
		who += ": `" + strings.Join(names, "`, `") + "`"
	}
	branch = cell(branch)
	return l.next() + "\n" +
		"Every pull request is `unknown` until then.\n\n" +
		"| | |\n| --- | --- |\n" +
		"| Policy | `" + PolicyPath + "` on `" + branch + "` |\n" +
		"| Changed in | `" + commit + "` |\n" +
		"| Approval | none recorded |\n" +
		"| Who can act | " + who + " |\n"
}

func changedAgainNote(l ownerPages, commit string) string {
	return l.next() + "\nThe policy changed again in `" + commit + "` with no recorded approval. Every pull request stays `unknown` until then.\n"
}

func duplicateNote(issue int) string {
	return "archfit: the policy changed outside the flow is followed in #" + strconv.Itoa(issue) + ".\n"
}

func mergedOtherNote(pull int) string {
	return "archfit: #" + strconv.Itoa(pull) + " merged a policy other than the one the answers wrote, so the answers " +
		"were not used. This request is open again for an owner to answer.\n"
}

// trimLogin is the login of an owner entry: "@alice" and "alice" are alice; a team ("org/team") is no login.
func trimLogin(owner string) string {
	login := strings.TrimPrefix(strings.TrimSpace(owner), "@")
	if strings.Contains(login, "/") {
		return ""
	}
	return login
}

func revertedNote(pull int, commit string) string {
	return "✅ **Policy restored.** #" + strconv.Itoa(pull) + " restored the approved policy in `" + commit + "`. It is under " +
		"change control again, and pull requests get a verdict again.\n"
}

// The words of the GitHub record of an owner action outside requests (design 19). Logins are GitHub logins and
// passkey ids base64url, so a code span around them cannot be closed early, and no @ in them mentions anyone.
const firstTitle = "archfit: the first policy is approved"

func byOwner(login, passkey string) string {
	return " by `" + login + "` with the passkey `" + requests.PasskeyShown(passkey) + "`"
}

func approvedPullNote(c Change, login, passkey string) string {
	what, ends := "`"+WorkflowPath+"`", "A new push needs a new approval."
	if c.Kind == records.KindFirst {
		what = "the first policy"
		ends = "When this pull request merges, its policy is the approved policy and change control starts. " + ends
	}
	heading := "**Workflow change approved.** "
	if c.OwnerVerdict() { // only then is the approval the head's verdict
		heading = "**Owner approved this head.** "
	}
	return "✅ " + heading + what + " was approved" + byOwner(login, passkey) + ".\n\n" +
		"| | |\n| --- | --- |\n| Head | `" + c.HeadSHA + "` |\n\n" +
		"The check runs again on this head. " + ends + "\n\n" +
		"<details><summary>Fingerprints</summary>\n\nPolicy: `" + c.Policy + "`\n\nWorkflow: `" + c.Workflow + "`\n\n</details>\n"
}

func firstBody(branch string, c Change, login, passkey string) string {
	return "✅ **First policy approved**" + byOwner(login, passkey) + ".\n\n" +
		"| | |\n| --- | --- |\n| Policy | `" + PolicyPath + "` on `" + branch + "` |\n| Commit | `" + c.Commit + "` |\n\n" +
		"Change control starts here. Owners change the policy through requests in the App. A change made any other way " +
		"stops every verdict until an owner confirms or reverts it.\n\n" +
		"<details><summary>Fingerprint</summary>\n\n`" + c.Policy + "`\n\n</details>\n"
}

func confirmedNote(c Change, login, passkey string) string {
	return "✅ **Policy confirmed**" + byOwner(login, passkey) + ". It is the approved policy now, and pull requests get a verdict again.\n\n" +
		"| | |\n| --- | --- |\n| Commit | `" + c.Commit + "` |\n\n" +
		"<details><summary>Fingerprint</summary>\n\n`" + c.Policy + "`\n\n</details>\n"
}

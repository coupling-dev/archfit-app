package approvals

import (
	"strconv"
	"strings"

	"github.com/coupling-dev/archfit-app/internal/gate/feedback"
	"github.com/coupling-dev/archfit-app/internal/gate/records"
	"github.com/coupling-dev/archfit-app/internal/gate/requests"
	"github.com/coupling-dev/archfit-app/internal/github/api"
)

// The words of the issue of a policy changed outside the flow and of the notes (design 5). They hold only the App's
// own words, a branch name in a code span, issue numbers and commit SHAs: no worker or owner text.
const outsideTitle = "archfit: the policy changed outside the flow"

// ownerPages are the links of an issue about a policy changed outside the flow; "" without an App URL.
type ownerPages struct{ confirm, revert string }

// outsideLinks are the confirm and revert pages of the repository.
func (s *Service) outsideLinks(r api.Repo) ownerPages {
	return ownerPages{
		confirm: feedback.OwnerLink(s.appURL, r.FullName, "/owner/confirm"),
		revert:  feedback.OwnerLink(s.appURL, r.FullName, "/owner/revert"),
	}
}

// next is the first line of the issue and of its notes: confirm, with revert as the other way.
func (l ownerPages) next() string {
	if l.confirm == "" {
		return "**Next:** an owner confirms or reverts the policy in the App (owner, passkey)\n"
	}
	return feedback.NextLine("Confirm the policy", l.confirm, "owner") +
		"Or [revert it](" + l.revert + ") to restore the approved policy.\n"
}

func outsideBody(l ownerPages, branch, commit string, owners []string) string {
	who := "an owner named in the approved policy"
	if len(owners) > 0 {
		who += ": `" + strings.Join(owners, "`, `") + "`"
	}
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
	return "✅ **Owner approved this head.** " + what + " was approved" + byOwner(login, passkey) + ".\n\n" +
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

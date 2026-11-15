package approvals

import (
	"strconv"
	"strings"

	"github.com/alexei-led/archfit-app/internal/gate/records"
	"github.com/alexei-led/archfit-app/internal/gate/requests"
)

// The words of the issue of a policy changed outside the flow and of the notes (design 5). They hold only the App's
// own words, a branch name in a code span, issue numbers and commit SHAs: no worker or owner text.
const outsideTitle = "archfit: the policy changed outside the flow"

func outsideBody(branch, commit string) string {
	return "The policy `" + PolicyPath + "` on `" + branch + "` changed in " + commit + " with no recorded approval.\n\n" +
		"Every pull request is `unknown` until an owner confirms or reverts the policy in the App.\n"
}

func changedAgainNote(commit string) string {
	return "archfit: the policy changed again in " + commit + " with no recorded approval. Every pull request stays " +
		"`unknown` until an owner confirms or reverts the policy in the App.\n"
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
	return "archfit: #" + strconv.Itoa(pull) + " restored the approved policy in " + commit + ". The policy is under " +
		"change control again, and pull requests get a verdict again.\n"
}

// The words of the GitHub record of an owner action outside requests (design 19). Logins are GitHub logins and
// passkey ids base64url, so a code span around them cannot be closed early, and no @ in them mentions anyone.
const firstTitle = "archfit: the first policy is approved"

func byOwner(login, passkey string) string {
	return " by `" + login + "` with the passkey `" + requests.PasskeyShown(passkey) + "`"
}

func approvedPullNote(c Change, login, passkey string) string {
	if c.Kind == records.KindFirst {
		return "archfit: the first policy of this pull request was approved" + byOwner(login, passkey) + ": head " +
			c.HeadSHA + ", policy `" + c.Policy + "`, workflow `" + c.Workflow + "`. The check runs again on this head. " +
			"When this pull request merges, its policy is the approved policy and change control starts. A new push to " +
			"it needs a new approval.\n"
	}
	return "archfit: the change of `" + WorkflowPath + "` in this pull request was approved" + byOwner(login, passkey) +
		": head " + c.HeadSHA + ", workflow `" + c.Workflow + "`. The check runs again on this head. A new push to it " +
		"needs a new approval.\n"
}

func firstBody(branch string, c Change, login, passkey string) string {
	return "The policy `" + PolicyPath + "` on `" + branch + "` at " + c.Commit + " (`" + c.Policy + "`) was approved" +
		byOwner(login, passkey) + ".\n\nChange control starts here: from now on owners change the policy through " +
		"requests in the App, and a change made any other way stops every verdict until an owner confirms or reverts it.\n"
}

func confirmedNote(c Change, login, passkey string) string {
	return "archfit: the policy at " + c.Commit + " (`" + c.Policy + "`) was confirmed" + byOwner(login, passkey) +
		". It is the approved policy now, and pull requests get a verdict again.\n"
}

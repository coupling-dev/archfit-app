package approvals

import (
	"strconv"
	"strings"
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

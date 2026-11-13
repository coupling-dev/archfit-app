package proposals

import (
	"slices"
	"strconv"
	"strings"

	"github.com/alexei-led/archfit-app/internal/gate/records"
	"github.com/alexei-led/archfit-app/internal/gate/requests"
	"github.com/alexei-led/archfit-app/internal/wire/policy3"
)

// The words of the policy pull request and its check. Logins are GitHub logins (letters, digits, hyphens) and passkey
// ids base64url, so a code span around them cannot be closed early, and no @ in them mentions anyone.
const (
	pullTitle  = "archfit: owners' answers to requests"
	checkTitle = "archfit: pass"
)

// requestList is "request #431" or "requests #431, #432": the requests of the answers listed; "" when none.
func requestList(listed []approved) string {
	var refs []string
	for _, a := range listed {
		if a.revert == nil {
			refs = append(refs, "#"+strconv.Itoa(a.rec.Request))
		}
	}
	switch len(refs) {
	case 0:
		return ""
	case 1:
		return "request " + refs[0]
	}
	return "requests " + strings.Join(refs, ", ")
}

// reverts reports whether listed holds a revert.
func reverts(listed []approved) bool {
	return slices.ContainsFunc(listed, func(a approved) bool { return a.revert != nil })
}

// carried is what the pull request carries: the owners' answers to its requests, the revert of the policy changed
// outside the flow, or both.
func carried(listed []approved) string {
	var parts []string
	if reverts(listed) {
		parts = append(parts, "the revert of the policy changed outside the flow")
	}
	if list := requestList(listed); list != "" {
		parts = append(parts, "the owners' answers to "+list)
	}
	return strings.Join(parts, " and ")
}

// commitMessage is the message of the App's commit on the policy branch.
func commitMessage(listed []approved) string {
	if !reverts(listed) {
		return "archfit: answers to " + requestList(listed)
	}
	return "archfit: " + carried(listed)
}

// answerWords is an answer as the pull request lists it.
func answerWords(c requests.Change) string {
	switch c.Kind {
	case policy3.ApproveUntil:
		return "approve until " + c.Until
	case policy3.Reject:
		return "reject"
	}
	return "approve"
}

// pullBody lists every answer the pull request carries, with its owner and passkey (design 19).
func pullBody(base string, listed []approved) string {
	var b strings.Builder
	b.WriteString("The App writes owners' answers to requests into `" + policyPath + "` (design 9.2). Each answer was " +
		"made in the App; an answer that loosens the policy was made with the owner's passkey.\n\n")
	b.WriteString("| Request | Answer | Owner | Passkey |\n| --- | --- | --- | --- |\n")
	for _, a := range listed {
		passkey := "none"
		if a.rec.Credential != "" {
			passkey = "`" + requests.PasskeyShown(a.rec.Credential) + "`"
		}
		request, answer := "#"+strconv.Itoa(a.rec.Request), answerWords(a.change)
		if a.revert != nil {
			request, answer = "none", "revert to the policy approved at "+a.revert.Commit
		}
		b.WriteString("| " + request + " | " + answer + " | `" + a.rec.Login + "` | " + passkey + " |\n")
	}
	b.WriteString("\nThe check `archfit/gate` passes on this pull request only while it holds exactly these answers on `" +
		base + "`. Change the policy in the App, not on this branch.\n")
	return b.String()
}

// checkSummary is the summary of archfit/gate on the policy pull request.
func checkSummary(base string, pin records.Pin, listed []approved) string {
	return "This is the App's policy pull request. It writes " + carried(listed) + " into `" + policyPath + "` on `" +
		base + "`, and nothing else. The policy it makes has the digest `" + pin.Digest + "`.\n"
}

// revertNote is the comment on the issue of the policy changed outside the flow when an owner reverts it (design 19).
func revertNote(login, passkey string, out Outcome) string {
	pull := "#" + strconv.Itoa(out.PullRequest)
	text := "archfit: reverted by `" + login + "` with the passkey `" + requests.PasskeyShown(passkey) + "`. " + pull +
		" restores the approved policy; "
	switch out.State {
	case requests.PullMerged:
		text += "it merged, and this issue closes when its push is judged.\n"
	case requests.PullLater:
		text += "it takes effect when it merges.\n"
	default:
		text += "GitHub did not merge it, so it stays open until it merges.\n"
	}
	return text
}

// The words of a proposal head's check once its verdict is taken back (M2 review H2).
const retiredTitle = "archfit: no verdict, this approval is spent"

func retiredSummary(pull int, why string) string {
	what := map[string]string{
		RetiredUsed:       "its approvals were used",
		RetiredVoid:       "its approvals were not used, and no longer apply",
		RetiredSuperseded: "the App wrote a newer head for it",
		RetiredForeign:    "another pull request has this commit as its head",
	}[why]
	of := "an approved change"
	if pull > 0 {
		of = "the approved change of pull request #" + strconv.Itoa(pull)
	}
	return "This commit carried the verdict of " + of + ", and " + what + ". It carries no verdict now: an approval is " +
		"valid once, for its own pull request. A pull request at this commit gets its verdict from its own run.\n"
}

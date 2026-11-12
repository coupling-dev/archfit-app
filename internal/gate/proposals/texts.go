package proposals

import (
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

// requestList is "request #431" or "requests #431, #432".
func requestList(listed []approved) string {
	refs := make([]string, len(listed))
	for i, a := range listed {
		refs[i] = "#" + strconv.Itoa(a.rec.Request)
	}
	if len(refs) == 1 {
		return "request " + refs[0]
	}
	return "requests " + strings.Join(refs, ", ")
}

// commitMessage is the message of the App's commit on the policy branch.
func commitMessage(listed []approved) string {
	return "archfit: answers to " + requestList(listed)
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
		b.WriteString("| #" + strconv.Itoa(a.rec.Request) + " | " + answerWords(a.change) + " | `" + a.rec.Login + "` | " +
			passkey + " |\n")
	}
	b.WriteString("\nThe check `archfit/gate` passes on this pull request only while it holds exactly these answers on `" +
		base + "`. Change the policy in the App, not on this branch.\n")
	return b.String()
}

// checkSummary is the summary of archfit/gate on the policy pull request.
func checkSummary(base string, pin records.Pin, listed []approved) string {
	return "This is the App's policy pull request. It writes the owners' answers to " + requestList(listed) + " into `" +
		policyPath + "` on `" + base + "`, and nothing else. The policy it makes has the digest `" + pin.Digest + "`.\n"
}

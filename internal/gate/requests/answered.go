package requests

import (
	"strconv"
	"strings"

	"github.com/alexei-led/archfit-app/internal/wire/policy3"
)

// What became of the pull request an answer's change went into (M2 task 19).
const (
	PullMerged  = "merged"  // the App merged it
	PullLater   = "later"   // the owner chose to merge it later
	PullRefused = "refused" // GitHub refused the merge; the pull request stays open
)

// passkeyShown is how many characters of a passkey id a note shows: enough to tell an owner's passkeys apart.
const passkeyShown = 8

// AnswerNote is the comment on a request issue when an owner answers it, before the App closes the issue (design 19,
// M2 task 19): the answer, the owner and the passkey it was made with, and the pull request that carries the change.
type AnswerNote struct {
	Kind    policy3.Answer
	Until   string // approve-until: the date
	Instead string // reject: what to do instead, the owner's text
	Owner   string // the owner's login
	Passkey string // the id of the passkey the answer was made with; "" for a reject, which needs none
	Pull    int    // the pull request with the policy change; 0 when the answer writes no line
	State   string // PullMerged, PullLater or PullRefused, when Pull is set
}

// Text is the comment. The owner's login and the passkey are in code spans and the owner's text in a `text` block, so
// nothing in them mentions, links or reads as a request.
func (n AnswerNote) Text() string {
	var b strings.Builder
	b.WriteString("archfit: ")
	switch n.Kind {
	case policy3.ApproveUntil:
		b.WriteString("approved until " + n.Until)
	case policy3.Reject:
		b.WriteString("rejected")
	default:
		b.WriteString("approved")
	}
	b.WriteString(" by " + codeSpan(n.Owner))
	if n.Passkey != "" {
		b.WriteString(" with the passkey " + codeSpan(PasskeyShown(n.Passkey)))
	}
	b.WriteString(".")
	if n.Kind == policy3.Reject {
		b.WriteString(" Instead:\n\n" + fenced(n.Instead) + "\n")
	} else {
		b.WriteString(" ")
	}
	pull := "#" + strconv.Itoa(n.Pull)
	switch {
	case n.Pull == 0 && n.Kind == policy3.Reject:
		b.WriteString("The policy has no line for this answer; this issue keeps it.\n")
	case n.Pull == 0:
		b.WriteString("The policy already allows it; nothing changes.\n")
	case n.State == PullMerged:
		b.WriteString("The policy change merged in " + pull + ".\n")
	case n.State == PullLater:
		b.WriteString("The policy change is in " + pull + "; it takes effect when " + pull + " merges.\n")
	default:
		b.WriteString("The policy change is in " + pull + ". GitHub did not merge it, so it stays open until it merges.\n")
	}
	return inert(b.String())
}

// PasskeyShown is the start of a passkey id that the App's texts show: enough to tell an owner's passkeys apart.
func PasskeyShown(id string) string { return id[:min(len(id), passkeyShown)] }

// ReopenNote is the comment on a request issue the App reopens because its answer no longer fits the policy on the
// default branch: it changed before pull, which carried the answer, merged. closed says pull was closed without a merge.
func ReopenNote(pull int, closed bool) string {
	why := "the policy on the default branch changed before #" + strconv.Itoa(pull) + " merged, and the answer no longer fits it"
	if closed {
		why = "#" + strconv.Itoa(pull) + " was closed without a merge"
	}
	return inert("archfit: " + why + ". This request is open again for an owner to answer.\n")
}

package policy3

import "fmt"

// Answer is the kind of an owner's answer to a request, or of a confirmation of a policy
// changed outside the flow (design 10.4).
type Answer string

// The answers the App records in milestone 2.
const (
	// Approve adds the dependency to depends_on, or changes the rule.
	Approve Answer = "approve"
	// ApproveUntil adds an exception with an expiry date; the rule stays.
	ApproveUntil Answer = "approve-until"
	// Reject adds a deny rule with instead, which answers later requests for the same thing.
	Reject Answer = "reject"
	// Confirm accepts the policy as it is on the default branch after a change outside the flow.
	Confirm Answer = "confirm"
)

// Loosens reports whether an answer of this kind loosens the policy, so it needs a passkey
// (design 9.2). Milestone 2 decides by the kind alone (D10): approve, approve-until and
// confirm loosen; reject tightens. The App compares two policies only with the YAML editor
// of milestone 3. An unknown kind is an error, never a guess.
func Loosens(kind Answer) (bool, error) {
	switch kind {
	case Approve, ApproveUntil, Confirm:
		return true, nil
	case Reject:
		return false, nil
	}
	return false, fmt.Errorf("unknown answer %q", kind)
}

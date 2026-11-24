package gate

import (
	"cmp"
	"slices"
	"strconv"
	"time"

	"github.com/coupling-dev/archfit-app/internal/gate/feedback"
)

// LabelFirst is the label of the owner page that approves the first policy.
const LabelFirst = feedback.LabelFirst

// PendingOwnerAction is one step of an owner that waits on a repository: the App page that resolves it, with the same
// label and path the check and the comment link to.
type PendingOwnerAction struct {
	feedback.OwnerPage
	Kind  string    // OwnerConfirm, OwnerRevert or OwnerWorkflow
	Pull  int       // 0 when the action belongs to the repository
	Since time.Time // the last change that is known to wait; zero when no time is recorded
}

// OwnerActions are the owner steps that wait on an approved repository, in the order they are shown: a policy changed
// outside the flow first, then the workflow changes, the longest wait first; one with no recorded time comes last.
func (h HomeState) OwnerActions() []PendingOwnerAction {
	var out []PendingOwnerAction
	if h.OutsideFlow {
		if h.PolicyAvailable {
			out = append(out, PendingOwnerAction{OwnerPage: feedback.OwnerPage{Label: labelConfirm, Path: "/owner/confirm", Role: "owner"}, Kind: OwnerConfirm})
		}
		out = append(out, PendingOwnerAction{OwnerPage: feedback.OwnerPage{Label: labelRevert, Path: "/owner/revert", Role: "owner"}, Kind: OwnerRevert})
	}
	pulls := slices.Clone(h.WorkflowPulls)
	slices.SortStableFunc(pulls, func(a, b int) int {
		ta, tb := h.WorkflowUpdated[a], h.WorkflowUpdated[b]
		if ta.IsZero() != tb.IsZero() {
			return cmp.Compare(btoi(ta.IsZero()), btoi(tb.IsZero()))
		}
		return cmp.Or(ta.Compare(tb), cmp.Compare(a, b))
	})
	for _, pull := range pulls {
		path := "/owner/workflow?pull=" + strconv.Itoa(pull)
		out = append(out, PendingOwnerAction{OwnerPage: feedback.OwnerPage{Label: labelWorkflow, Path: path, Role: "owner"},
			Kind: OwnerWorkflow, Pull: pull, Since: h.WorkflowUpdated[pull]})
	}
	return out
}

func btoi(b bool) int {
	if b {
		return 1
	}
	return 0
}

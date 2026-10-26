package feedback

import "github.com/alexei-led/archfit-app/internal/gate/verdict"

// reportReason is a source-admissible assessment reason, not a gate-conclusion rule. Unknown reasons fail closed.
func reportReason(reason verdict.Reason) bool {
	reportDerived, _ := verdict.ClassifyDisplayReason(reason.Code, reason.Category)
	return reportDerived
}

// presentation returns a display-only copy. A decoded pointer is not proof of approved producer/input authority.
// It leaves the authoritative Outcome, Receipt, conclusions and report admission untouched.
func presentation(p Publication) Publication {
	usable := p.Report != nil && len(p.Outcome.Reasons) > 0
	for _, reason := range p.Outcome.Reasons {
		if !reportReason(reason) {
			usable = false
		}
	}
	if usable {
		return p
	}
	p.Report = nil
	reasons := make([]verdict.Reason, 0, len(p.Outcome.Reasons))
	for _, reason := range p.Outcome.Reasons {
		if !reportReason(reason) {
			reasons = append(reasons, reason)
		}
	}
	p.Outcome.Reasons = reasons
	return p
}

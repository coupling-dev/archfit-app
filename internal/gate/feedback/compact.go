package feedback

import (
	"fmt"
	"strings"

	"github.com/alexei-led/archfit-app/internal/gate/verdict"
	"github.com/alexei-led/archfit-app/internal/wire/report"
)

const commentFindingCap = 3

// compactFindings leaves accepted debt and historical details in the exact check.
func compactFindings(p Publication) string {
	if p.Report == nil || suspended(p.Outcome) {
		return ""
	}
	g := group(p.Report)
	fs := append(append([]report.Finding{}, g.violations...), g.advisories...)
	var lines []string
	for _, f := range fs[:min(len(fs), commentFindingCap)] {
		kind := "advisory"
		if f.Active() {
			kind = "active finding"
		}
		line := fmt.Sprintf("- **%s** %s: %s", kind, code(f.RuleID), subject(f))
		if len(f.Locations) > 0 {
			line += " at " + location(f.Locations[0])
		}
		if f.Constraint != "" {
			line += ". Constraint: " + text(f.Constraint)
		}
		lines = append(lines, line)
	}
	if len(fs) > commentFindingCap {
		lines = append(lines, fmt.Sprintf("- %d more active findings/advisories: read the exact archfit/state check.", len(fs)-commentFindingCap))
	}
	if len(g.fixed) > 0 {
		lines = append(lines, report.FixedNotice)
	}
	return strings.Join(lines, "\n")
}

func referenceCoversRecovery(p Publication, code verdict.ReasonCode) bool {
	if code != verdict.ReasonMissingCoverage || p.Report == nil || !report.ReferenceCoversAllGaps(*p.Report) {
		return false
	}
	for _, reason := range p.Outcome.Reasons {
		if reason.Code == verdict.ReasonBaselineNotComparable {
			return true
		}
	}
	return false
}

func displayReasons(p Publication) []verdict.Reason {
	out := append([]verdict.Reason(nil), p.Outcome.Reasons...)
	for i := range out {
		if referenceCoversRecovery(p, out[i].Code) {
			out[i].Next = ""
		}
	}
	return out
}

func recoveryRole(code verdict.ReasonCode) string {
	switch code {
	case verdict.ReasonBaselineNotComparable, verdict.ReasonOwnerApprovalMissing, verdict.ReasonPolicyDefect, verdict.ReasonWaiverExpired:
		return "Architecture owner: "
	case verdict.ReasonHealthy, verdict.ReasonAttentionOnly, verdict.ReasonBlockingFindings:
		return ""
	default:
		return "CI maintainer: "
	}
}

func compactEvidence(p Publication) string {
	lines := []string{headGaps, "", text(report.EvidenceSummary(*p.Report))}
	var rules []report.UnevaluatedRule
	for _, u := range p.Report.Decision.UnevaluatedRequiredRules {
		if u.RuleID != "metric_ratchets" {
			rules = append(rules, u)
		}
	}
	for i, u := range rules {
		if i == commentFindingCap {
			lines = append(lines, fmt.Sprintf("%d more required-rule gaps: read the exact check.", len(rules)-i))
			break
		}
		lines = append(lines, "- Required rule "+code(u.RuleID)+" not evaluated: "+text(u.Reason))
	}
	return strings.Join(lines, "\n")
}

func commentAssessment(p Publication) string {
	if p.Report == nil {
		return "No usable report. This is not a clean result. Source-repair tasks are withheld pending approved measurement."
	}
	if required(p) && p.Outcome.Conclusion == verdict.Success && report.NoRequiredChange(*p.Report) && !verdict.ReferenceConflicts(p.Report.GateReference, len(p.Trusted.BaselineDigests) > 0) {
		s := "No required architecture change requested in the evaluated scope."
		if len(group(p.Report).debt) > 0 {
			s += " Accepted debt remains."
		}
		return s
	}
	g := group(p.Report)
	return fmt.Sprintf("Assessment: %d active %s, %d %s, %d accepted %s.", len(g.violations), plural(len(g.violations), "finding", "findings"), len(g.advisories), plural(len(g.advisories), "advisory", "advisories"), len(g.debt), plural(len(g.debt), "finding", "findings"))
}

func commentIdentity(p Publication) string {
	root := "https://github.com/" + p.Target.Repo.FullName
	line := "HEAD " + code(p.Target.HeadSHA)
	if p.Receipt.RunID > 0 {
		url := fmt.Sprintf("%s/actions/runs/%d", root, p.Receipt.RunID)
		if p.Receipt.RunAttempt > 0 {
			url += fmt.Sprintf("/attempts/%d", p.Receipt.RunAttempt)
		}
		line += fmt.Sprintf(" · [Run %d · attempt %d](%s)", p.Receipt.RunID, p.Receipt.RunAttempt, url)
	}
	if p.stateCheckID > 0 {
		line += fmt.Sprintf(" · [archfit/state check %d](%s/runs/%d)", p.stateCheckID, root, p.stateCheckID)
	} else {
		line += ". Detailed findings/evidence: `archfit/state` for this HEAD and run."
	}
	return line
}

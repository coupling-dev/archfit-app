package feedback

import (
	"fmt"
	"strings"

	"github.com/alexei-led/archfit-app/internal/gate/verdict"
	"github.com/alexei-led/archfit-app/internal/wire/report"
)

const (
	commentFindingCap = 3
	// commentReasonRunes cuts a finding's why and fix in the comment; the archfit/state check keeps them whole.
	commentReasonRunes = 300
	// attentionCap and attentionRunes bound the repository attention fold, which is budgeted before any finding.
	attentionCap   = 10
	attentionRunes = 200
	shortSHALen    = 7
)

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
		// The place first: a reviewer on a phone reads where, then which rule, then why and what to do.
		line := fmt.Sprintf("- **%s** %s: %s", kind, code(f.RuleID), subject(f))
		if len(f.Locations) > 0 {
			line = fmt.Sprintf("- **%s** %s %s: %s", kind, location(f.Locations[0]), code(f.RuleID), subject(f))
		}
		// An advisory's why is the coupling arithmetic (strength, distance, volatility): the archfit/state check and the
		// App page keep it; the first screen does not.
		if f.Why != "" && f.Active() {
			line += ". Why: " + text(cutRunes(f.Why, commentReasonRunes))
		}
		if f.Constraint != "" {
			line += ". Fix: " + text(cutRunes(f.Constraint, commentReasonRunes))
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
	return fmt.Sprintf("Assessment: %d active %s, %d %s, %d accepted %s.", len(g.violations), plural(len(g.violations), "finding", "findings"), len(g.advisories), plural(len(g.advisories), "advisory", "advisories"), len(g.debt), plural(len(g.debt), "finding", "findings")) + measuredWith(p)
}

// measuredWith names the engine that measured and the policy it measured against, so the first screen says what the
// result is of. The policy is the ref and commit the engine states when it states one, else the digest the App checked.
func measuredWith(p Publication) string {
	var parts []string
	if v := p.Identity.EngineVersion; v != "" {
		parts = append(parts, "engine "+code(cutRunes(v, 40)))
	}
	switch id := p.Report.Identity; {
	case id != nil && id.PolicyRef != nil && id.PolicyRef.Ref != "":
		sha := id.PolicyRef.SHA
		if len(sha) > shortSHALen {
			sha = sha[:shortSHALen] // a short commit SHA, as GitHub shows one
		}
		parts = append(parts, "policy "+code(cutRunes(id.PolicyRef.Ref, 60)+"@"+sha))
	case p.Receipt.PolicyDigest != "":
		parts = append(parts, "policy "+code(cutRunes(p.Receipt.PolicyDigest, 12)))
	}
	if len(parts) == 0 {
		return ""
	}
	return " Measured by " + strings.Join(parts, " against ") + "."
}

// attentionBlock is the engine's repository attention: the coupling gate's own reasons, for the owner, not the
// author. Bounded so the fold costs little: it is budgeted before any finding.
func attentionBlock(p Publication) string {
	if p.Report == nil || len(p.Report.GateReasons) == 0 || suspended(p.Outcome) {
		return ""
	}
	var lines []string
	for i, r := range p.Report.GateReasons {
		if i == attentionCap {
			lines = append(lines, fmt.Sprintf("- and %d more: read the archfit/state check", len(p.Report.GateReasons)-i))
			break
		}
		lines = append(lines, "- "+text(cutRunes(r, attentionRunes)))
	}
	return "The engine's coupling gate reports these for an architecture owner. They are not this pull request's tasks.\n\n" + strings.Join(lines, "\n")
}

// agentIdentity names what the engine measured, for an agent that re-runs the check and compares; "" without one.
func agentIdentity(p Publication) string {
	if p.Report == nil || p.Report.Identity == nil || suspended(p.Outcome) {
		return ""
	}
	id := p.Report.Identity
	var parts []string
	for _, f := range []struct{ name, value string }{
		{"engine", id.EngineVersion}, {"policy digest", id.PolicyDigest}, {"baseline digest", id.BaselineDigest},
		{"head tree", id.HeadTreeDigest}, {"base", id.BaseSHA},
	} {
		if f.value != "" {
			parts = append(parts, f.name+" "+code(cutRunes(f.value, 64)))
		}
	}
	return "Result identity: " + strings.Join(parts, ", ") + "."
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

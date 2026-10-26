package report

import (
	"fmt"
	"strings"
)

const (
	FixedNotice  = "Previously accepted finding no longer observed; resolution not established."
	NoTaskNotice = "No automated source-repair task supplied."
)

// NoRequiredChange describes the engine's evaluated scope, not report eligibility or the App gate.
func NoRequiredChange(r Report) bool {
	return r.Decision.HardGates == "pass" && r.Decision.ActiveBlockers == 0 && len(r.Decision.UnevaluatedRequiredRules) == 0 && len(Group(r, 0).Violations) == 0
}

// PRImpact counts finding identities, never new module pairs or newly added source sites.
func PRImpact(r Report) string {
	if r.Comparison.OriginStatus != "comparable" {
		return "PR impact: unknown — no comparable Git-base finding-origin evidence."
	}
	introduced, existing, unknown := 0, 0, 0
	for _, f := range r.Findings {
		if f.Status == "fixed" {
			continue
		}
		switch f.Origin {
		case "introduced":
			introduced++
		case "pre_existing":
			existing++
		default:
			unknown++
		}
	}
	return fmt.Sprintf("PR impact: %d introduced %s, %d pre-existing, %d unknown. Finding origin does not establish that each module pair or listed source site was added.", introduced, plural(introduced, "finding", "findings"), existing, unknown)
}

// EvidenceSummary keeps declared rules separate from implicit accepted-reference metric checks.
func EvidenceSummary(r Report) string {
	declared := "Declared rule checks: not reported."
	if d, ok := r.Dimensions["intent"]; ok && d.Coverage.Basis == "declared rules evaluated" {
		declared = fmt.Sprintf("Declared rule checks: %d/%d evaluated.", d.Coverage.Observed, d.Coverage.Total)
	}
	metric := "Implicit accepted-reference metric checks: inventory not reported."
	for _, u := range r.Decision.UnevaluatedRequiredRules {
		if u.RuleID == "metric_ratchets" {
			metric = "Implicit accepted-reference metric checks: unavailable — " + u.Reason + "."
			break
		}
	}
	ref := "Accepted reference: not reported."
	if r.GateReference != nil {
		ref = "Accepted reference: " + strings.ReplaceAll(r.GateReference.Status, "_", " ") + "."
	}
	return declared + " " + metric + " " + ref
}

package feedback

import (
	"fmt"
	"strings"

	"github.com/alexei-led/archfit-app/internal/wire/report"
	"github.com/alexei-led/archfit-app/internal/wire/settings"
)

const (
	headPolicyChange = "### Policy change"
	// policyCardItems caps the listed changes: the card sits in a block that is never cut, so it must stay small.
	policyCardItems = 20
)

// policyCard is the policy change card: what a change to the protected policy does to what blocks and to what the
// analysis reads, as the engine classified it from the two policies' declarations, and who approves it. "" when the
// publication carries no classification. Every engine and repository string is sanitized; nothing here decides.
func policyCard(pc *report.PolicyChange, s settings.Settings) string {
	if pc == nil {
		return ""
	}
	var lines []string
	add := func(label string, items ...string) {
		for _, it := range items {
			lines = append(lines, "- "+label+": "+it)
		}
	}
	for _, r := range pc.RulesRemoved {
		add("Rule removed", code(r.ID)+" ("+text(r.Gate)+")")
	}
	for _, r := range pc.RulesLoosened {
		add("Rule loosened", code(r.ID)+" "+text(r.Field)+" "+code(r.From)+" → "+code(r.To))
	}
	for _, w := range pc.WaiversAdded {
		add("Waiver added", code(w.Rule)+" until "+text(w.Expires))
	}
	for _, a := range pc.AllowlistsWidened {
		add("Allowlist widened", code(a.Module)+" "+text(a.Field)+" "+code(a.From)+" → "+code(a.To))
	}
	for _, c := range pc.CoverageLost {
		add("Coverage lost", text(c.Kind)+" "+code(c.Detail))
	}
	for _, d := range pc.DeclarationsChanged {
		add("Declaration changed", code(d.Key))
	}
	for _, r := range pc.RulesAdded {
		add("Rule added", code(r.ID)+" ("+text(r.Gate)+")")
	}
	for _, r := range pc.RulesTightened {
		add("Rule tightened", code(r.ID)+" "+text(r.Field)+" "+code(r.From)+" → "+code(r.To))
	}
	for _, w := range pc.WaiversRemoved {
		add("Waiver removed", code(w.Rule))
	}
	for _, a := range pc.AllowlistsNarrowed {
		add("Allowlist narrowed", code(a.Module)+" "+text(a.Field)+" "+code(a.From)+" → "+code(a.To))
	}
	for _, c := range pc.CoverageGained {
		add("Coverage gained", text(c.Kind)+" "+code(c.Detail))
	}
	if more := len(lines) - policyCardItems; more > 0 {
		lines = append(lines[:policyCardItems], fmt.Sprintf("- and %d more", more))
	}

	b := []string{headPolicyChange, "", policySummary(pc)}
	if len(lines) > 0 {
		b = append(b, "", strings.Join(lines, "\n"))
	}
	rule := "A change to the protected policy needs an architecture owner's approval of this exact commit."
	if pc.Direction == report.DirectionLoosening || pc.Direction == report.DirectionMixed {
		rule += " A loosening is never approved by an agent."
	}
	if len(pc.CoverageLost) > 0 {
		rule += " Coverage lost is code the analysis no longer reads."
	}
	b = append(b, "", rule)
	if _, held := s.Effective(); held != "" {
		b = append(b, text(held)+".")
	}
	return strings.Join(b, "\n")
}

// policySummary is one line: the direction and the counts.
func policySummary(pc *report.PolicyChange) string {
	switch pc.Direction {
	case report.DirectionNone:
		return "This pull request changes the policy file without changing what blocks or what the analysis reads."
	case report.DirectionTightening:
		return fmt.Sprintf("This pull request tightens the policy: %d tightening.", pc.Tightens())
	case report.DirectionLoosening:
		return fmt.Sprintf("This pull request loosens the policy: %d loosening.", pc.Loosens())
	}
	return fmt.Sprintf("This pull request both loosens and tightens the policy: %d loosening, %d tightening.", pc.Loosens(), pc.Tightens())
}

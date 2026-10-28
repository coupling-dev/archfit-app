package feedback

import (
	"fmt"
	"strings"
	"unicode/utf8"

	"github.com/alexei-led/archfit-app/internal/wire/report"
	"github.com/alexei-led/archfit-app/internal/wire/settings"
)

const (
	headPolicyChange = "### Policy change"
	// policyCardBytes bounds the listed changes. The card sits in a block that is never cut, and a pull request's author
	// controls the policy values it shows, so what it lists is bounded by bytes, not by a count of lines.
	policyCardBytes = 3000
	// policyValueRunes cuts one engine value: the engine writes whole canonical declarations into from and to.
	policyValueRunes = 80
)

// policyCard is the policy change card: what a change to the protected policy does to what blocks and to what the
// analysis reads, as the engine classified it from the two policies' declarations, and who approves it. "" when the
// publication carries no classification. Every engine and repository string is cut and sanitized; nothing here decides.
func policyCard(pc *report.PolicyChange, s settings.Settings) string {
	if pc == nil {
		return ""
	}
	var lines []string
	add := func(line string) { lines = append(lines, "- "+line) }
	for _, r := range pc.RulesRemoved {
		add("Rule removed: " + code(short(r.ID)) + " (" + gateWords(r.Gate) + ")")
	}
	for _, r := range pc.RulesLoosened {
		add("Rule loosened: " + ruleDelta(r))
	}
	for _, w := range pc.WaiversAdded {
		add("Waiver added: " + code(short(w.Rule)) + " until " + text(short(w.Expires)))
	}
	for _, a := range pc.AllowlistsWidened {
		add("Allowlist widened: " + allowlist(a))
	}
	for _, c := range pc.CoverageLost {
		add("Coverage lost: " + coverageWords(c))
	}
	for _, d := range pc.DeclarationsChanged {
		add("Declaration changed: " + code(short(d.Key)))
	}
	for _, r := range pc.RulesAdded {
		add("Rule added: " + code(short(r.ID)) + " (" + gateWords(r.Gate) + ")")
	}
	for _, r := range pc.RulesTightened {
		add("Rule tightened: " + ruleDelta(r))
	}
	for _, w := range pc.WaiversRemoved {
		add("Waiver removed: " + code(short(w.Rule)))
	}
	for _, a := range pc.AllowlistsNarrowed {
		add("Allowlist narrowed: " + allowlist(a))
	}
	for _, c := range pc.CoverageGained {
		add("Coverage gained: " + coverageWords(c))
	}

	b := []string{headPolicyChange, "", policySummary(pc)}
	if listed := within(lines, policyCardBytes); len(listed) > 0 {
		if more := len(lines) - len(listed); more > 0 {
			listed = append(listed, fmt.Sprintf("- and %d more", more))
		}
		b = append(b, "", strings.Join(listed, "\n"))
	}
	rule := "A change to the protected policy needs an architecture owner's approval of this exact commit."
	if pc.Direction == report.DirectionLoosening || pc.Direction == report.DirectionMixed {
		rule += " A loosening is never approved by an agent."
	}
	b = append(b, "", rule)
	if _, held := s.Effective(); held != "" {
		b = append(b, text(held)+".")
	}
	return strings.Join(b, "\n")
}

// within keeps the leading lines that fit in limit bytes, one line at least.
func within(lines []string, limit int) []string {
	size := 0
	for i, l := range lines {
		size += len(l) + 1
		if size > limit && i > 0 {
			return lines[:i]
		}
	}
	return lines
}

// short cuts a policy value to policyValueRunes runes.
func short(s string) string { return cutRunes(s, policyValueRunes) }

// cutRunes cuts s to n runes and marks the cut. Callers cut before sanitizing, which can lengthen a value.
func cutRunes(s string, n int) string {
	if utf8.RuneCountInString(s) <= n {
		return s
	}
	return string([]rune(s)[:n]) + "…"
}

func ruleDelta(r report.PolicyRuleDelta) string {
	return code(short(r.ID)) + " " + text(short(r.Field)) + ": " + code(short(r.From)) + " → " + code(short(r.To))
}

func allowlist(a report.PolicyAllowlist) string {
	return code(short(a.Module)) + " " + text(short(a.Field)) + ": " + code(short(a.From)) + " → " + code(short(a.To))
}

// gateWords says what a rule's gate does, in the words of the check.
func gateWords(gate string) string {
	switch gate {
	case "fail":
		return "blocks"
	case "warn":
		return "warns"
	case "off":
		return "off"
	}
	return text(short(gate))
}

// coverageWords says in plain words what a coverage change does to the code the analysis reads. An unknown kind is shown
// as the engine wrote it.
func coverageWords(c report.PolicyCoverage) string {
	d := code(short(c.Detail))
	switch c.Kind {
	case "exclude_added":
		return d + " is now excluded from the analysis"
	case "exclude_removed":
		return d + " is no longer excluded from the analysis"
	case "language_disabled":
		return "language " + d + " is turned off"
	case "language_enabled":
		return "language " + d + " is turned on"
	case "language_mode_lowered":
		return "language " + d + " is read less strictly"
	case "language_mode_raised":
		return "language " + d + " is read more strictly"
	case "analyzer_disabled":
		return "analyzer " + d + " is turned off"
	case "analyzer_enabled":
		return "analyzer " + d + " is turned on"
	case "module_removed":
		return "module " + d + " is removed"
	case "module_paths_removed":
		return "paths are removed from module " + d
	case "module_review_gate_lowered":
		return "the module review gate is lowered: " + d
	case "module_review_gate_raised":
		return "the module review gate is raised: " + d
	}
	return text(short(c.Kind)) + " " + d
}

// policySummary is one line: the direction and how many changes it holds.
func policySummary(pc *report.PolicyChange) string {
	switch pc.Direction {
	case report.DirectionNone:
		return "This pull request changes the policy file without changing what blocks or what the analysis reads."
	case report.DirectionTightening:
		return "This pull request tightens the policy (" + changes(pc.Tightens()) + ")."
	case report.DirectionLoosening:
		return "This pull request loosens the policy (" + changes(pc.Loosens()) + ")."
	}
	return fmt.Sprintf("This pull request both loosens the policy (%s) and tightens it (%s).", changes(pc.Loosens()), changes(pc.Tightens()))
}

func changes(n int) string {
	if n == 1 {
		return "1 change"
	}
	return fmt.Sprintf("%d changes", n)
}

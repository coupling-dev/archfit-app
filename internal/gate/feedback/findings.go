package feedback

import (
	"fmt"
	"slices"
	"strconv"
	"strings"

	"github.com/alexei-led/archfit-app/internal/wire/report"
)

// tableLocationCap is how many locations a table row names before "and N more": a row stays one line.
const tableLocationCap = 3

// Mermaid edge widths by location count, so the heavy direction of a cycle reads at a glance.
const (
	thinEdgeBelow   = 3
	mediumEdgeBelow = 10
	cycleEdgeColor  = "#cf222e" // GitHub's danger red; readable on the light and the dark theme
)

// findingsBlock is block 5. It labels Finding.Active findings as violations, a display heuristic only: the conclusion
// always comes from the Outcome. diagrams adds a Mermaid graph per dependency cycle; only the comment asks for it,
// because a check summary is not known to render Mermaid.
func findingsBlock(r *report.Report, diagrams bool) string {
	if r == nil {
		return ""
	}
	var violations, advisories, debt, fixed []report.Finding
	for _, f := range r.Findings {
		switch {
		case f.Active():
			violations = append(violations, f)
		case f.Status == statusBaseline || f.Status == statusWaived:
			debt = append(debt, f)
		case f.Status == statusFixed:
			fixed = append(fixed, f)
		case f.Kind == kindAdvisory && (f.Status == statusNew || f.Status == statusExpired):
			advisories = append(advisories, f)
		default: // never hide a finding whose status this version does not know
			violations = append(violations, f)
		}
	}
	slices.SortStableFunc(violations, func(a, b report.Finding) int { // Active first, as before the unknown statuses
		return boolOrder(b.Active()) - boolOrder(a.Active())
	})
	seams := seamIndex(r.Seams)

	var b strings.Builder
	b.WriteString(headFindings)
	if len(r.Findings) == 0 {
		b.WriteString("\n\nNo findings reported.")
		return b.String()
	}
	if t := tally(r, len(violations), len(advisories), len(debt), len(fixed), cycleCount(violations)); t != "" {
		b.WriteString("\n\n" + t)
	}
	if len(violations) > 0 { // the tally line counts them; no group title repeats it
		writeViolations(&b, violations, seams, diagrams)
	}
	foldTable(&b, advisories, seams, "advisory", "advisories", "never block")
	foldTable(&b, debt, seams, "accepted", "accepted", "baseline or waiver · display only")
	foldTable(&b, fixed, seams, "fixed", "fixed", "display only")
	return b.String()
}

func boolOrder(v bool) int {
	if v {
		return 1
	}
	return 0
}

// tally is the one line that counts every non-empty group, so the size of the problem reads before any detail. It
// never says "no violations": a hard gate can fail without a finding.
func tally(r *report.Report, violations, advisories, debt, fixed, cycles int) string {
	var parts []string
	if violations > 0 {
		v := fmt.Sprintf("**%d rule %s**", violations, plural(violations, "violation", "violations"))
		if cycles > 0 {
			v += fmt.Sprintf(" in %d dependency %s", cycles, plural(cycles, "cycle", "cycles"))
		}
		parts = append(parts, v)
	}
	for _, g := range []struct {
		n    int
		noun string
	}{{advisories, plural(advisories, "advisory", "advisories")}, {debt, "accepted"}, {fixed, "fixed"}} {
		if g.n > 0 {
			parts = append(parts, fmt.Sprintf("%d %s", g.n, g.noun))
		}
	}
	if r.Comparison.OriginStatus == "comparable" { // origin is presentation only, and only with a PR comparison
		n := 0
		for _, f := range r.Findings {
			if f.Origin == originIntroduced {
				n++
			}
		}
		parts = append(parts, fmt.Sprintf("%d introduced by this pull request", n))
	}
	return strings.Join(parts, " · ")
}

func plural(n int, one, many string) string {
	if n == 1 {
		return one
	}
	return many
}

// writeViolations writes the first groupCap violations: each dependency cycle as one item with its directions in a
// table (and a diagram), every other finding as a list item. Display only: the count is still of findings.
func writeViolations(b *strings.Builder, fs []report.Finding, seams map[[2]string]report.Seam, diagrams bool) {
	shown := fs[:min(len(fs), groupCap)] // a cycle partner past the cap would show past it
	comp := cycles(shown)
	done := map[int]bool{}
	listOpen := false
	for i, f := range shown {
		c := comp[i]
		switch {
		case c < 0:
			if !listOpen {
				b.WriteString("\n")
				listOpen = true
			}
			writeFinding(b, f)
		case !done[c]:
			done[c] = true
			listOpen = false
			var members []report.Finding
			for j, g := range shown {
				if comp[j] == c {
					members = append(members, g)
				}
			}
			writeCycle(b, members, seams, diagrams)
		}
	}
	if len(fs) > groupCap {
		fmt.Fprintf(b, "\n\n- and %d more", len(fs)-groupCap)
	}
}

// cycleCount is the number of dependency cycles among the shown violations.
func cycleCount(fs []report.Finding) int {
	n := -1
	for _, c := range cycles(fs[:min(len(fs), groupCap)]) {
		n = max(n, c)
	}
	return n + 1
}

// cycles returns, per finding, the index of the dependency cycle it lies on, or -1. A finding lies on a cycle when
// its target module reaches its source through findings of the same rule, kind, status and severity (the fields
// report.Finding.Reverses pairs on). Cycle edges of one such class that share a module are one strongly connected
// component, so they are one cycle item. Indices follow the first finding of each cycle. Display only.
func cycles(fs []report.Finding) []int {
	class := func(f report.Finding) [4]string { return [4]string{f.RuleID, f.Kind, f.Status, f.Severity} }
	edge := func(f report.Finding) (string, string, bool) {
		from, to := f.Edge.From.Module, f.Edge.To.Module
		return from, to, from != "" && to != "" && from != to
	}
	reaches := func(c [4]string, start, goal string) bool {
		seen, queue := map[string]bool{start: true}, []string{start}
		for len(queue) > 0 {
			at := queue[0]
			queue = queue[1:]
			for _, f := range fs {
				from, to, ok := edge(f)
				if !ok || from != at || class(f) != c || seen[to] {
					continue
				}
				if to == goal {
					return true
				}
				seen[to] = true
				queue = append(queue, to)
			}
		}
		return false
	}

	parent := make([]int, len(fs))
	onCycle := make([]bool, len(fs))
	for i, f := range fs {
		parent[i] = i
		from, to, ok := edge(f)
		onCycle[i] = ok && reaches(class(f), to, from)
	}
	root := func(i int) int {
		for parent[i] != i {
			i = parent[i]
		}
		return i
	}
	for i := range fs {
		for j := i + 1; j < len(fs) && onCycle[i]; j++ {
			if !onCycle[j] || class(fs[i]) != class(fs[j]) {
				continue
			}
			a1, b1, _ := edge(fs[i])
			a2, b2, _ := edge(fs[j])
			if a1 == a2 || a1 == b2 || b1 == a2 || b1 == b2 {
				parent[root(j)] = root(i)
			}
		}
	}
	out := make([]int, len(fs))
	index := map[int]int{}
	for i := range fs {
		out[i] = -1
		if !onCycle[i] {
			continue
		}
		r := root(i)
		if _, ok := index[r]; !ok {
			index[r] = len(index)
		}
		out[i] = index[r]
	}
	return out
}

// writeCycle writes one dependency cycle: the modules on it, the rule, a diagram, a row per direction, then the
// distinct repairs. feedback adds no repair of its own: the constraint and alternatives are the engine's.
func writeCycle(b *strings.Builder, fs []report.Finding, seams map[[2]string]report.Seam, diagrams bool) {
	var modules []string
	for _, f := range fs {
		for _, m := range []string{f.Edge.From.Module, f.Edge.To.Module} {
			if !slices.Contains(modules, m) {
				modules = append(modules, m)
			}
		}
	}
	names := make([]string, len(modules))
	for i, m := range modules {
		names[i] = code(m)
	}
	where := "among " + strings.Join(names, ", ")
	if len(names) == 2 {
		where = "between " + names[0] + " and " + names[1]
	}
	label := ""
	if fs[0].Active() { // one class per cycle, so one label for all
		label = "**violation** · "
	}
	fmt.Fprintf(b, "\n\n#### Dependency cycle %s\n\n%s%s · %s", where, label, code(fs[0].RuleID), meta(fs[0], fs))
	if diagrams {
		b.WriteString("\n\n" + cycleDiagram(modules, fs))
	}
	b.WriteString("\n\n" + dependencyTable(fs, seams, false))
	// The engine writes one Why per direction that only swaps the module names; the first one says it. Constraints
	// and alternatives stay distinct per finding.
	writeReasons(b, fs, "", true)
}

// meta is severity, status and origin; origin only when every finding of the item shares it.
func meta(f report.Finding, all []report.Finding) string {
	parts := []string{text(f.Severity), text(f.Status)}
	if f.Origin != "" && !slices.ContainsFunc(all, func(g report.Finding) bool { return g.Origin != f.Origin }) {
		parts = append(parts, originText(f.Origin))
	}
	return strings.Join(parts, ", ")
}

const originIntroduced = "introduced"

// originText reads the engine's origin token as a phrase. Display only: the App never decides on origin.
func originText(o string) string {
	switch o {
	case originIntroduced:
		return "introduced"
	case "pre_existing":
		return "pre-existing"
	}
	return "origin " + text(o)
}

// cycleDiagram is a Mermaid flowchart: one node per module, one edge per direction labeled with its location count,
// heavier for more locations. Node IDs are generated and labels escaped, so no report string reaches Mermaid syntax.
func cycleDiagram(modules []string, fs []report.Finding) string {
	id := map[string]string{}
	lines := []string{"```mermaid", "flowchart LR"}
	for i, m := range modules {
		id[m] = "m" + strconv.Itoa(i)
		lines = append(lines, fmt.Sprintf("  %s[\"%s\"]", id[m], mermaidText(m)))
	}
	var styles []string
	for i, f := range fs {
		n := len(f.Locations)
		lines = append(lines, fmt.Sprintf("  %s -->|\"%d %s\"| %s", id[f.Edge.From.Module], n, plural(n, "location", "locations"), id[f.Edge.To.Module]))
		width := 4
		switch {
		case n < thinEdgeBelow:
			width = 2
		case n < mediumEdgeBelow:
			width = 3
		}
		styles = append(styles, fmt.Sprintf("  linkStyle %d stroke:%s,stroke-width:%dpx", i, cycleEdgeColor, width))
	}
	lines = append(lines, styles...)
	return strings.Join(append(lines, "```"), "\n")
}

// mermaidText keeps letters, digits and a few path characters; every other rune becomes a Mermaid entity code
// (#<decimal>;), so a quote, a bracket, a pipe or a backtick can neither end the label nor the code fence.
func mermaidText(s string) string {
	var b strings.Builder
	for _, r := range s {
		switch {
		case r >= 'a' && r <= 'z', r >= 'A' && r <= 'Z', r >= '0' && r <= '9', strings.ContainsRune("_-./:@+", r):
			b.WriteRune(r)
		default:
			fmt.Fprintf(&b, "#%d;", r)
		}
	}
	return b.String()
}

// seamIndex keys the report's seams by module pair. A seam carries the coupling dimensions of Balanced Coupling
// (integration strength, distance, volatility) that a finding does not.
func seamIndex(seams []report.Seam) map[[2]string]report.Seam {
	out := make(map[[2]string]report.Seam, len(seams))
	for _, s := range seams {
		out[[2]string{s.FromModule, s.ToModule}] = s
	}
	return out
}

// dependencyTable is one row per finding: the dependency, where it occurs, and, when the report has a seam for any
// row, its integration strength, distance and volatility. withRule adds the rule and its meta for mixed groups.
func dependencyTable(fs []report.Finding, seams map[[2]string]report.Seam, withRule bool) string {
	coupling := slices.ContainsFunc(fs, func(f report.Finding) bool {
		_, ok := seams[[2]string{f.Edge.From.Module, f.Edge.To.Module}]
		return ok
	})
	var head []string
	if withRule {
		head = append(head, "Rule")
	}
	head = append(head, "Dependency", "Where")
	if coupling {
		head = append(head, "Strength", "Distance", "Volatility")
	}
	rows := []string{"| " + strings.Join(head, " | ") + " |", "|" + strings.Repeat(" --- |", len(head))}
	for _, f := range fs {
		var row []string
		if withRule {
			row = append(row, code(f.RuleID)+" "+meta(f, nil))
		}
		row = append(row, endpoint(f.Edge.From)+" → "+endpoint(f.Edge.To), capped(f.Locations, tableLocationCap))
		if coupling {
			s, ok := seams[[2]string{f.Edge.From.Module, f.Edge.To.Module}]
			if !ok {
				row = append(row, "", "", "")
			} else {
				row = append(row, text(s.Strength), text(s.Distance), text(s.Volatility))
			}
		}
		for i := range row {
			row[i] = cell(row[i])
		}
		rows = append(rows, "| "+strings.Join(row, " | ")+" |")
	}
	return strings.Join(rows, "\n")
}

// cell escapes the table delimiter; GitHub reads "\|" as a pipe, inside code spans too.
func cell(s string) string { return strings.ReplaceAll(s, "|", `\|`) }

// foldTable writes a group that never blocks as one fold: the count in the summary, a table, then its reasons.
func foldTable(b *strings.Builder, fs []report.Finding, seams map[[2]string]report.Seam, one, many, effect string) {
	if len(fs) == 0 {
		return
	}
	shown := fs[:min(len(fs), groupCap)]
	fmt.Fprintf(b, "\n\n<details>\n<summary>%d %s · %s</summary>\n\n%s", len(fs), plural(len(fs), one, many), effect, dependencyTable(shown, seams, true))
	if len(fs) > groupCap {
		fmt.Fprintf(b, "\n\n- and %d more", len(fs)-groupCap)
	}
	writeReasons(b, shown, "Rule ", false)
	b.WriteString("\n\n" + foldClose)
}

// writeReasons writes each distinct Why, Constraint and set of allowed alternatives once, in first-seen order: the
// engine repeats one explanation for every finding of a rule, and repeating it hides the rest.
func writeReasons(b *strings.Builder, fs []report.Finding, rulePrefix string, firstWhy bool) {
	seen := map[string]bool{}
	var lines []string
	add := func(rule, label, s string) {
		if s == "" || seen[label+"\x00"+s] {
			return
		}
		seen[label+"\x00"+s] = true
		prefix := ""
		if rulePrefix != "" {
			prefix = code(rule) + " · "
		}
		lines = append(lines, "- "+prefix+"**"+label+":** "+s)
	}
	for i, f := range fs {
		if i == 0 || !firstWhy {
			add(f.RuleID, "Why", text(f.Why))
		}
		if f.Constraint != "" {
			add(f.RuleID, "Constraint", text(f.Constraint))
		}
		add(f.RuleID, "Allowed alternatives", alternatives(f.AllowedAlternatives))
	}
	if len(lines) > 0 {
		b.WriteString("\n\n" + strings.Join(lines, "\n"))
	}
}

func alternatives(as []string) string {
	out := make([]string, len(as))
	for i, a := range as {
		out[i] = text(a)
	}
	return strings.Join(out, "; ")
}

// writeFinding writes one finding outside a cycle as a list item.
func writeFinding(b *strings.Builder, f report.Finding) {
	b.WriteString("\n- ")
	if f.Active() {
		b.WriteString("**violation** ")
	}
	fmt.Fprintf(b, "%s (%s): %s → %s", code(f.RuleID), meta(f, nil), endpoint(f.Edge.From), endpoint(f.Edge.To))
	b.WriteString("\n  - Locations: " + capped(f.Locations, locationCap))
	fmt.Fprintf(b, "\n  - Why: %s", text(f.Why))
	if f.Constraint != "" {
		fmt.Fprintf(b, "\n  - Constraint: %s", text(f.Constraint))
	}
	if len(f.AllowedAlternatives) > 0 {
		fmt.Fprintf(b, "\n  - Allowed alternatives: %s", alternatives(f.AllowedAlternatives))
	}
}

// capped is up to limit locations as file:line, then "and N more"; "none reported" when empty.
func capped(locs []report.Location, limit int) string {
	if len(locs) == 0 {
		return "none reported"
	}
	var b strings.Builder
	for i, loc := range locs {
		if i == limit {
			fmt.Fprintf(&b, " and %d more", len(locs)-limit)
			break
		}
		if i > 0 {
			b.WriteString(", ")
		}
		b.WriteString(location(loc))
	}
	return b.String()
}

func endpoint(e report.Endpoint) string {
	if e.Path == "" {
		return module(e)
	}
	return module(e) + " (" + code(e.Path) + ")"
}

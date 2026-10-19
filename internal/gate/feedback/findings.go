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

// Mermaid edge widths by location count, so the heavy direction of a cycle reads at a glance. A direction the report
// shows as added by the pull request is drawn red and wide, one that existed before grey and thin.
const (
	thinEdgeBelow     = 3
	mediumEdgeBelow   = 10
	cycleEdgeColor    = "#cf222e" // GitHub's danger red; readable on the light and the dark theme
	existingEdgeColor = "#8c959f" // GitHub's neutral grey, the same on both themes
	addedEdgeWidth    = 4
	existingEdgeWidth = 1
)

// What a row says about the pull request base (the "This PR" column). "added", not "new": without a baseline every
// finding has status new, and new does not mean added by this pull request.
const (
	prAdded    = report.PairAdded
	prExisting = report.PairExisting
)

// groups is the report's findings sorted into the groups block 5 shows. all holds every finding, with its origin
// cleared when the report was not compared with the pull request base: origin is presentation only, and means nothing
// without that comparison.
type groups struct {
	compared                                 bool
	all, violations, advisories, debt, fixed []report.Finding
	onCycle                                  map[string]bool // IDs of the shown violations on a dependency cycle, of any rule
}

func group(r *report.Report) groups {
	rg := report.Group(*r, groupCap)
	return groups{compared: rg.Compared, all: rg.All, violations: rg.Violations, advisories: rg.Advisories,
		debt: rg.Accepted, fixed: rg.Fixed, onCycle: rg.OnCycle}
}

// findingsBlock is block 5. It labels Finding.Active findings as violations, a display heuristic only: the conclusion
// always comes from the Outcome. diagrams adds a Mermaid graph per dependency cycle; only the comment asks for it,
// because a check summary is not known to render Mermaid.
func findingsBlock(r *report.Report, diagrams bool) string {
	if r == nil {
		return ""
	}
	g := group(r)
	seams := seamIndex(r.Seams)

	var b strings.Builder
	b.WriteString(headFindings)
	if len(r.Findings) == 0 {
		b.WriteString("\n\nNo findings reported.")
		return b.String()
	}
	if t := tally(r, g.compared, len(g.violations), len(g.advisories), len(g.debt), len(g.fixed), cycleCount(g.violations)); t != "" {
		b.WriteString("\n\n" + t)
	}
	if len(g.violations) > 0 { // the tally line counts them; no group title repeats it
		writeViolations(&b, g, seams, diagrams)
	}
	foldTable(&b, g.advisories, seams, "advisory", "advisories", "never block")
	foldTable(&b, g.debt, seams, "accepted", "accepted", "baseline or waiver · display only")
	foldTable(&b, g.fixed, seams, "fixed", "fixed", "display only")
	return b.String()
}

// leadSentence says in one sentence what the pull request did to the rule violations, from origins and locations
// only, before the next step. It is about the first dependency cycle when that one is introduced, else about the
// introduced violations; "" without a comparison with the pull request base or without a violation to speak of.
// It picks no repair: which direction to remove stays the engine's constraint.
func leadSentence(r *report.Report) string {
	if r == nil {
		return ""
	}
	g := group(r)
	if !g.compared || len(g.violations) == 0 {
		return ""
	}
	shown := g.violations[:min(len(g.violations), groupCap)]
	var cycle []report.Finding
	for i, c := range report.Cycles(shown) {
		if c == 0 {
			cycle = append(cycle, shown[i])
		}
	}
	if len(cycle) > 0 && !slices.ContainsFunc(cycle, func(f report.Finding) bool { return f.Origin != originIntroduced }) {
		var added, existing []string
		for _, f := range cycle {
			switch n := len(f.Locations); report.PairOrigin(f, g.all, g.onCycle) {
			case prAdded:
				added = append(added, dependency(f)+" ("+capped(f.Locations, 1)+")")
			case prExisting:
				existing = append(existing, fmt.Sprintf("%s (%d %s)", dependency(f), n, plural(n, "location", "locations")))
			}
		}
		verb := "introduces"
		if len(existing) > 0 {
			verb = "closes"
		}
		s := "This pull request " + verb + " a dependency cycle " + among(cycleModules(cycle))
		if len(added) > 0 {
			s += ": it adds " + series(added)
		}
		if len(existing) > 0 {
			sep := ": "
			if len(added) > 0 {
				sep = "; "
			}
			s += sep + series(existing) + " existed before"
		}
		return s + "."
	}
	var introduced []string
	for _, f := range g.violations {
		if f.Origin == originIntroduced {
			introduced = append(introduced, dependency(f))
		}
	}
	if len(introduced) == 0 {
		if !slices.ContainsFunc(g.violations, func(f report.Finding) bool { return f.Origin != originPreExisting }) {
			return "The rule violations below existed before this pull request."
		}
		return ""
	}
	n := len(introduced)
	return fmt.Sprintf("This pull request introduces %d rule %s: %s.", n, plural(n, "violation", "violations"), series(introduced))
}

// seriesCap is how many items a sentence names before "and N more".
const seriesCap = 3

// series joins items as prose: "a", "a and b", "a, b and c", then "a, b, c and N more".
func series(items []string) string {
	if len(items) > seriesCap {
		items = append(items[:seriesCap:seriesCap], fmt.Sprintf("%d more", len(items)-seriesCap))
	}
	if len(items) == 1 {
		return items[0]
	}
	return strings.Join(items[:len(items)-1], ", ") + " and " + items[len(items)-1]
}

// dependency is the module pair of a finding, without paths.
func dependency(f report.Finding) string { return module(f.Edge.From) + " → " + module(f.Edge.To) }

// tally is the one line that counts every non-empty group, so the size of the problem reads before any detail. It
// never says "no violations": a hard gate can fail without a finding. cycles counts the dependency cycles among the
// shown violations, as its own part: not every violation lies on one.
func tally(r *report.Report, compared bool, violations, advisories, debt, fixed, cycles int) string {
	var parts []string
	if violations > 0 {
		parts = append(parts, fmt.Sprintf("**%d rule %s**", violations, plural(violations, "violation", "violations")))
	}
	for _, g := range []struct {
		n    int
		noun string
	}{
		{cycles, plural(cycles, "dependency cycle", "dependency cycles")},
		{advisories, plural(advisories, "advisory", "advisories")}, {debt, "accepted"}, {fixed, "fixed"},
	} {
		if g.n > 0 {
			parts = append(parts, fmt.Sprintf("%d %s", g.n, g.noun))
		}
	}
	if compared {
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

// writeViolations writes the first groupCap violations: each dependency cycle first, as one item with its directions
// in a table (and a diagram), then every other finding as a list item, under its own heading when a cycle came before,
// so no list item reads as part of a cycle. Display only: the count is still of findings.
func writeViolations(b *strings.Builder, g groups, seams map[[2]string]report.Seam, diagrams bool) {
	fs := g.violations
	shown := fs[:min(len(fs), groupCap)] // a cycle partner past the cap would show past it
	comp := report.Cycles(shown)
	n := 0
	for _, c := range comp {
		n = max(n, c+1)
	}
	members := make([][]report.Finding, n)
	var plain []report.Finding
	for i, f := range shown {
		if c := comp[i]; c >= 0 {
			members[c] = append(members[c], f)
		} else {
			plain = append(plain, f)
		}
	}
	for _, m := range members {
		writeCycle(b, m, g, seams, diagrams)
	}
	if len(plain) > 0 {
		if n > 0 {
			b.WriteString("\n\n#### Other rule violations")
		}
		b.WriteString("\n")
		for _, f := range plain {
			writeFinding(b, f)
		}
	}
	if len(fs) > groupCap {
		fmt.Fprintf(b, "\n\n- and %d more", len(fs)-groupCap)
	}
}

// cycleCount is the number of dependency cycles among the shown violations.
func cycleCount(fs []report.Finding) int {
	n := -1
	for _, c := range report.Cycles(fs[:min(len(fs), groupCap)]) {
		n = max(n, c)
	}
	return n + 1
}

func cycleModules(fs []report.Finding) []string {
	var modules []string
	for _, f := range fs {
		for _, m := range []string{f.Edge.From.Module, f.Edge.To.Module} {
			if !slices.Contains(modules, m) {
				modules = append(modules, m)
			}
		}
	}
	return modules
}

// among names the modules of a cycle: "between `a` and `b`", or "among `a`, `b`, `c`".
func among(modules []string) string {
	names := make([]string, len(modules))
	for i, m := range modules {
		names[i] = code(m)
	}
	if len(names) == 2 {
		return "between " + names[0] + " and " + names[1]
	}
	return "among " + strings.Join(names, ", ")
}

// writeCycle writes one dependency cycle: the modules on it, the rule, a diagram, a row per direction, then the
// distinct repairs. feedback adds no repair of its own: the constraint and alternatives are the engine's. g holds
// every finding of the report, which tells for each direction whether the base already had it.
func writeCycle(b *strings.Builder, fs []report.Finding, g groups, seams map[[2]string]report.Seam, diagrams bool) {
	modules := cycleModules(fs)
	label := ""
	if fs[0].Active() { // one class per cycle, so one label for all
		label = "**violation** · "
	}
	fmt.Fprintf(b, "\n\n#### Dependency cycle %s\n\n%s%s · %s", among(modules), label, ruleName(fs[0].RuleID), meta(fs[0]))
	pr := make([]string, len(fs))
	for i, f := range fs {
		pr[i] = report.PairOrigin(f, g.all, g.onCycle)
	}
	if diagrams {
		b.WriteString("\n\n" + cycleDiagram(modules, fs, pr))
	}
	b.WriteString("\n\n" + dependencyTable(fs, pr, seams, false))
	// The engine writes one Why per direction that only swaps the module names; the first one says it. Constraints
	// and alternatives stay distinct per finding.
	reasons := slices.Clone(fs)
	for i := 1; i < len(reasons); i++ {
		reasons[i].Why = ""
	}
	writeReasons(b, reasons, false)
}

// meta is the severity, then the status as a word when the group does not already say it: a violation or an advisory
// is new unless its waiver expired, and an accepted finding is a baseline entry or a waiver.
func meta(f report.Finding) string {
	parts := []string{text(f.Severity)}
	switch f.Status {
	case statusNew, statusFixed:
	case statusExpired:
		parts = append(parts, "waiver expired")
	case statusWaived:
		parts = append(parts, "waiver")
	default:
		parts = append(parts, text(f.Status))
	}
	return strings.Join(parts, ", ")
}

const (
	originIntroduced  = "introduced"
	originPreExisting = "pre_existing"
	originComparable  = "comparable" // Comparison.OriginStatus when the engine compared against the PR base
)

// prWord is a finding's origin in the "This PR" column. Display only: the App never decides on origin.
func prWord(o string) string {
	switch o {
	case "":
		return ""
	case originIntroduced:
		return prAdded
	case originPreExisting:
		return prExisting
	}
	return text(o)
}

func ruleName(id string) string {
	if n, ok := report.EngineRuleNames[id]; ok {
		return n
	}
	return code(id)
}

func distanceText(token string) string {
	if w, ok := report.DistanceWords[token]; ok {
		return w
	}
	return text(token)
}

// cycleDiagram is a Mermaid flowchart: one node per module, one edge per direction labeled with its location count.
// A direction added by the pull request is red and wide, one that existed before grey and thin, and one the report
// cannot place red and heavier for more locations. Node IDs are generated and labels escaped, so no report string
// reaches Mermaid syntax.
func cycleDiagram(modules []string, fs []report.Finding, pr []string) string {
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
		color, width := cycleEdgeColor, 4
		switch {
		case pr[i] == prAdded:
			width = addedEdgeWidth
		case pr[i] == prExisting:
			color, width = existingEdgeColor, existingEdgeWidth
		case n < thinEdgeBelow:
			width = 2
		case n < mediumEdgeBelow:
			width = 3
		}
		styles = append(styles, fmt.Sprintf("  linkStyle %d stroke:%s,stroke-width:%dpx", i, color, width))
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

// dependencyTable is one row per finding: the dependency, what it is on the pull request base when pr says so for any
// row ("This PR"), where it occurs, and, when the report has a seam for any row, its integration strength, distance
// and volatility. withRule adds the rule and its meta for mixed groups.
func dependencyTable(fs []report.Finding, pr []string, seams map[[2]string]report.Seam, withRule bool) string {
	coupling := slices.ContainsFunc(fs, func(f report.Finding) bool {
		_, ok := seams[[2]string{f.Edge.From.Module, f.Edge.To.Module}]
		return ok
	})
	origins := slices.ContainsFunc(pr, func(s string) bool { return s != "" })
	var head []string
	if withRule {
		head = append(head, "Rule")
	}
	head = append(head, "Dependency")
	if origins {
		head = append(head, "This PR")
	}
	head = append(head, "Where")
	if coupling {
		head = append(head, "Strength", "Distance", "Volatility")
	}
	rows := []string{"| " + strings.Join(head, " | ") + " |", "|" + strings.Repeat(" --- |", len(head))}
	for i, f := range fs {
		var row []string
		if withRule {
			row = append(row, ruleName(f.RuleID)+" · "+meta(f))
		}
		row = append(row, endpoint(f.Edge.From)+" → "+endpoint(f.Edge.To))
		if origins {
			row = append(row, pr[i])
		}
		row = append(row, capped(f.Locations, tableLocationCap))
		if coupling {
			s, ok := seams[[2]string{f.Edge.From.Module, f.Edge.To.Module}]
			if !ok {
				row = append(row, "", "", "")
			} else {
				row = append(row, text(s.Strength), distanceText(s.Distance), text(s.Volatility))
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
	pr := make([]string, len(shown))
	for i, f := range shown {
		pr[i] = prWord(f.Origin)
	}
	fmt.Fprintf(b, "\n\n<details>\n<summary>%d %s · %s</summary>\n\n%s", len(fs), plural(len(fs), one, many), effect, dependencyTable(shown, pr, seams, true))
	if len(fs) > groupCap {
		fmt.Fprintf(b, "\n\n- and %d more", len(fs)-groupCap)
	}
	writeReasons(b, shown, true)
	b.WriteString("\n\n" + foldClose)
}

// writeReasons writes each distinct Why, Constraint and set of allowed alternatives once, in first-seen order: the
// engine repeats one explanation for every finding of a rule, and repeating it hides the rest.
func writeReasons(b *strings.Builder, fs []report.Finding, withRule bool) {
	seen := map[string]bool{}
	var lines []string
	add := func(rule, label, s string) {
		key := rule + "\x00" + label + "\x00" + s // per rule: another rule's same text is its own explanation
		if s == "" || seen[key] {
			return
		}
		seen[key] = true
		prefix := ""
		if withRule {
			prefix = ruleName(rule) + " · "
		}
		lines = append(lines, prefix+"**"+label+":** "+s)
	}
	for _, f := range fs {
		if f.Why != "" {
			add(f.RuleID, "Why", text(f.Why))
		}
		if f.Constraint != "" {
			add(f.RuleID, "Constraint", text(f.Constraint))
		}
		add(f.RuleID, "Allowed alternatives", alternatives(f.AllowedAlternatives))
	}
	// Paragraphs, not a list: a list here would swallow a list that follows it.
	if len(lines) > 0 {
		b.WriteString("\n\n" + strings.Join(lines, "\n\n"))
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
	m := meta(f)
	switch prWord(f.Origin) {
	case "":
	case prAdded:
		m += ", added by this pull request"
	case prExisting:
		m += ", existed before this pull request"
	default:
		m += ", origin " + text(f.Origin)
	}
	fmt.Fprintf(b, "%s (%s): %s → %s", ruleName(f.RuleID), m, endpoint(f.Edge.From), endpoint(f.Edge.To))
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

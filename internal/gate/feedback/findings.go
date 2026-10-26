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

// groups holds detailed-check display groups. report.Group clears origins without comparable Git-base evidence.
type groups struct {
	violations, advisories, debt, fixed []report.Finding
	// covered maps the module pair of a violation to what the report also says about that pair as an advisory: the
	// advisory is the same dependency under another rule, so it joins the violation's row instead of a section of its own.
	covered map[[2]string][]string
}

func pairOf(f report.Finding) [2]string { return [2]string{f.Edge.From.Module, f.Edge.To.Module} }

func group(r *report.Report) groups {
	rg := report.Group(*r, groupCap)
	g := groups{violations: rg.Violations, debt: rg.Accepted, fixed: rg.Fixed, covered: map[[2]string][]string{}}
	var covered map[[2]string][]report.Finding
	g.advisories, covered = report.SplitCovered(rg.Violations, rg.Advisories)
	for p, fs := range covered {
		for _, f := range fs {
			if note := ruleName(f.RuleID) + " · " + meta(f); !slices.Contains(g.covered[p], note) {
				g.covered[p] = append(g.covered[p], note)
			}
		}
	}
	return g
}

// findingsBlock renders detailed groups; the conclusion always comes from Outcome. Publication uses diagrams=false.
// The compact comment uses compactFindings instead and never emits Mermaid.
func findingsBlock(r *report.Report, diagrams bool) string {
	open, folds := findingParts(r, diagrams)
	return join(open, folds)
}

// findingParts is findingsBlock in two parts: the violations, and the folds that never block. The comment puts the
// next step between them and cuts the folds first.
func findingParts(r *report.Report, diagrams bool) (open, folds string) {
	if r == nil {
		return "", ""
	}
	g := group(r)
	seams := seamIndex(r.Seams)

	var b strings.Builder
	if !diagrams {
		b.WriteString(headFindings)
	}
	if len(r.Findings) == 0 {
		b.WriteString("\n\nNo findings reported.")
		return strings.TrimLeft(b.String(), "\n"), ""
	}
	if t := tally(g); t != "" && !diagrams {
		b.WriteString("\n\n" + t)
	}
	if len(g.violations) > 0 {
		writeViolations(&b, g, seams, diagrams)
	}
	var f strings.Builder
	otherAdvisories := len(g.covered) > 0
	foldTable(&f, g.advisories, seams, "advisory", "advisories", advisoryEffect(otherAdvisories), true)
	foldTable(&f, g.debt, seams, "known issue accepted earlier", "known issues accepted earlier", "", false)
	if len(g.fixed) > 0 {
		f.WriteString("\n\n" + report.FixedNotice)
		for _, finding := range g.fixed[:min(len(g.fixed), groupCap)] {
			fmt.Fprintf(&f, "\n- Finding %s, rule %s. Historical severity, endpoints and resolution are not established.", code(shortID(finding.ID)), code(finding.RuleID))
		}
	}
	return strings.TrimLeft(b.String(), "\n"), strings.TrimLeft(f.String(), "\n")
}

// advisoryEffect is what the advisory fold says beside its count. When an advisory sits on a dependency a violation
// above already lists, it joined that violation's row, so the fold counts only the advisories on other dependencies.
func advisoryEffect(others bool) string {
	if others {
		return "on other dependencies · never block"
	}
	return "never block"
}

// leadSentence uses the shared finding-identity summary, never inferred dependency/site novelty.
func leadSentence(r *report.Report) string {
	if r == nil {
		return ""
	}
	return report.PRImpact(*r)
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
func dependency(f report.Finding) string { return node(f.Edge.From) + " → " + node(f.Edge.To) }

// node names one end of a dependency by its module; an end no module owns is named by its path, so it is not lost.
func node(e report.Endpoint) string {
	if e.Module == "" && e.Path != "" {
		return code(e.Path)
	}
	return module(e)
}

func isMetric(f report.Finding) bool { return strings.HasPrefix(f.RuleID, report.MetricRulePrefix) }

// subject is what a finding is about: its module pair, or for a metric ratchet, which has no edge, the metric with
// the numbers the engine reported.
func subject(f report.Finding) string {
	if !isMetric(f) {
		return dependency(f)
	}
	name := f.MatchedBy[report.MatchedByMetric]
	if name == "" {
		name = strings.TrimPrefix(f.RuleID, report.MetricRulePrefix)
	}
	s := "metric " + code(name)
	before, after, threshold := f.MatchedBy[report.MatchedByBefore], f.MatchedBy[report.MatchedByAfter], f.MatchedBy[report.MatchedByThreshold]
	if before == "" || after == "" {
		return s
	}
	s += ": " + text(before) + " → " + text(after)
	if threshold != "" {
		s += " (" + text(threshold) + ")"
	}
	return s
}

func advisoryTail(others bool) string {
	if others {
		return " on other dependencies"
	}
	return ""
}

// tally is the one line of the check summary that counts every non-empty group, so the size of the problem reads
// before any detail. A dependency cycle is counted once, with its dependencies; the other violations are counted
// apart, never again. It never says "no violations": a hard gate can fail without a finding.
func tally(g groups) string {
	cycles, onCycle := cycleCount(g.violations), 0
	for _, c := range report.Cycles(g.violations[:min(len(g.violations), groupCap)]) {
		if c >= 0 {
			onCycle++
		}
	}
	var parts []string
	if cycles > 0 {
		parts = append(parts, fmt.Sprintf("**%d %s** (%d %s)", cycles, plural(cycles, "dependency cycle", "dependency cycles"), onCycle, plural(onCycle, "dependency", "dependencies")))
	}
	if other := len(g.violations) - onCycle; other > 0 {
		noun := plural(other, "rule violation", "rule violations")
		if cycles > 0 {
			noun = plural(other, "other rule violation", "other rule violations")
		}
		parts = append(parts, fmt.Sprintf("**%d %s**", other, noun))
	}
	for _, c := range []struct {
		n    int
		noun string
	}{
		{len(g.advisories), plural(len(g.advisories), "advisory", "advisories") + advisoryTail(len(g.covered) > 0)},
		{len(g.debt), plural(len(g.debt), "known issue accepted earlier", "known issues accepted earlier")},
		{len(g.fixed), plural(len(g.fixed), "known issue fixed", "known issues fixed")},
	} {
		if c.n > 0 {
			parts = append(parts, fmt.Sprintf("%d %s", c.n, c.noun))
		}
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
	for i, m := range members {
		writeCycle(b, m, g, seams, diagrams, i == 0)
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

// among names the modules of a cycle: "between `a` and `b`", or "among `a`, `b` and `c`".
func among(modules []string) string {
	names := make([]string, len(modules))
	for i, m := range modules {
		names[i] = code(m)
	}
	if len(names) == 2 {
		return "between " + names[0] + " and " + names[1]
	}
	return "among " + series(names)
}

// writeCycle writes one dependency cycle: its picture (comment only), what it breaks, a row per direction and the
// distinct fixes. feedback adds no fix of its own: the constraint and alternatives are the engine's. g holds every
// finding of the report, which tells for each direction whether the base already had it. In the comment the lead
// cycle has no heading, so the picture follows the headline's sentence at once; the check summary always has one.
func writeCycle(b *strings.Builder, fs []report.Finding, g groups, seams map[[2]string]report.Seam, diagrams, lead bool) {
	modules := cycleModules(fs)
	pr := make([]string, len(fs))
	for i, f := range fs {
		pr[i] = prWord(f.Origin)
	}
	if !diagrams || !lead {
		kind := "Dependency cycle"
		if diagrams {
			kind = "Another dependency cycle"
		}
		fmt.Fprintf(b, "\n\n#### %s %s", kind, among(modules))
	}
	if diagrams {
		b.WriteString("\n\n" + cycleDiagram(modules, fs, pr) + "\n\n" + diagramLegend(pr))
	}
	verb := "break"
	if !fs[0].Active() {
		verb = "are reported under"
	}
	n := len(fs)
	fmt.Fprintf(b, "\n\n%d %s of this cycle %s %s (%s).", n, plural(n, "dependency", "dependencies"), verb, ruleName(fs[0].RuleID), severityMeta(fs[0]))
	also := make([]string, len(fs))
	for i, f := range fs {
		also[i] = strings.Join(g.covered[pairOf(f)], "; ")
	}
	table, shared := dependencyTable(fs, pr, seams, tableOpts{also: also})
	b.WriteString("\n\n" + table)
	if shared != "" {
		b.WriteString("\n\n" + shared)
	}
	// The engine writes one Why per direction that only swaps the module names, and names the first pair. The cycle
	// is described from what the directions show: each one leads back to its own source.
	b.WriteString("\n\n**Why:** " + report.CycleWhy(n, len(modules)))
	reasons := slices.Clone(fs)
	for i := range reasons {
		reasons[i].Why = ""
	}
	writeReasons(b, reasons, false)
}

// diagramLegend is the line under a cycle diagram: what the colours and labels mean. A direction the report cannot
// place is drawn red too, so with a mix the legend says so.
func diagramLegend(pr []string) string {
	known := slices.ContainsFunc(pr, func(s string) bool { return s != "" })
	if !known {
		return "_Label and width: locations in code that use the dependency_"
	}
	red := "introduced finding"
	if slices.Contains(pr, "") {
		red += ", or finding origin unknown"
	}
	return "_Red: " + red + " · grey: pre-existing finding · label: reported locations; source-site origin is unknown_"
}

// severityMeta is meta with the severity named as one: "high severity, waiver expired".
func severityMeta(f report.Finding) string {
	severity, rest, found := strings.Cut(meta(f), ", ")
	if !found {
		return severity + " severity"
	}
	return severity + " severity, " + rest
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
		parts = append(parts, "accepted by waiver")
	case statusBaseline:
		parts = append(parts, "accepted in the baseline")
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
	var nodeIDs []string
	for i, m := range modules {
		id[m] = "m" + strconv.Itoa(i)
		nodeIDs = append(nodeIDs, id[m])
		lines = append(lines, fmt.Sprintf("  %s(\"%s\")", id[m], mermaidText(m)))
	}
	var styles []string
	for i, f := range fs {
		n := report.LocationsTotal(f)
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
	// A transparent fill keeps the text colour of the viewer's theme readable, light or dark.
	lines = append(lines, "  classDef module fill:transparent,stroke:"+existingEdgeColor+",stroke-width:1px", "  class "+strings.Join(nodeIDs, ",")+" module")
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

// tableOpts are the optional columns of a dependency table.
type tableOpts struct {
	rule, moves bool
	also        []string // per row; nil for none
}

// column is one column of a dependency table. phrase turns a value that is the same on every row into words for the
// line under the table; a column without phrase is never folded away.
type column struct {
	head   string
	cells  []string
	phrase func(value string) string
}

func same(s []string) bool {
	return len(s) > 0 && s[0] != "" && !slices.ContainsFunc(s, func(v string) bool { return v != s[0] })
}

// dependencyTable is one row per finding: the dependency, what it is on the pull request base when pr says so for any
// row ("This PR"), where it occurs, and, when the report has a seam for any row, its integration strength, distance
// and volatility. opts.rule adds the rule and its meta for mixed groups; opts.moves adds the balancing move of the seam;
// opts.also is, per row, the advisory the report also has on the same dependency. With two rows or more, a column that holds the same value on every row is not a column: its value goes into the
// second result, the line under the table, together with a move that is the same on every row.
func dependencyTable(fs []report.Finding, pr []string, seams map[[2]string]report.Seam, opts tableOpts) (table, shared string) {
	seamOf := func(f report.Finding) (report.Seam, bool) {
		s, ok := seams[[2]string{f.Edge.From.Module, f.Edge.To.Module}]
		return s, ok
	}
	coupling := slices.ContainsFunc(fs, func(f report.Finding) bool { _, ok := seamOf(f); return ok })
	cellsOf := func(each func(i int, f report.Finding) string) []string {
		out := make([]string, len(fs))
		for i, f := range fs {
			out[i] = each(i, f)
		}
		return out
	}
	seamCells := func(value func(report.Seam) string) []string {
		return cellsOf(func(_ int, f report.Finding) string {
			if s, ok := seamOf(f); ok {
				return value(s)
			}
			return ""
		})
	}
	identity := func(v string) string { return v }

	var cols []column
	if opts.rule {
		cols = append(cols, column{"Rule", cellsOf(func(_ int, f report.Finding) string { return ruleName(f.RuleID) + " · " + meta(f) }), identity})
	}
	cols = append(cols, column{head: "Dependency", cells: cellsOf(func(_ int, f report.Finding) string { return subject(f) })})
	if slices.ContainsFunc(pr, func(s string) bool { return s != "" }) {
		origins := make([]string, len(pr))
		for i, value := range pr {
			switch value {
			case prAdded:
				origins[i] = "introduced finding"
			case prExisting:
				origins[i] = "pre-existing finding"
			default:
				origins[i] = value
			}
		}
		cols = append(cols, column{"Finding origin", origins, identity})
	}
	cols = append(cols, column{head: "Where", cells: cellsOf(func(_ int, f report.Finding) string { return capped(f, tableLocationCap) })})
	if coupling {
		cols = append(cols,
			column{"Strength", seamCells(func(s report.Seam) string { return text(s.Strength) }), func(v string) string { return v + " coupling" }},
			column{"Distance", seamCells(func(s report.Seam) string { return distanceText(s.Distance) }), identity},
			column{"Volatility", seamCells(func(s report.Seam) string { return text(s.Volatility) }), func(v string) string { return v + " volatility" }})
	}
	if opts.also != nil && slices.ContainsFunc(opts.also, func(v string) bool { return v != "" }) {
		cols = append(cols, column{"Also reported as", opts.also, func(v string) string { return "also reported as " + v }})
	}
	var move []string
	if opts.moves {
		move = seamCells(func(s report.Seam) string { return moveWords(s.Hypothesis) })
		if !slices.ContainsFunc(move, func(v string) bool { return v != "" }) {
			move = nil
		}
	}
	if move != nil && len(fs) > 1 && same(move) {
		shared = "**Suggested move:** " + move[0] + "."
	} else if move != nil {
		cols = append(cols, column{head: "Suggested move", cells: move})
	}

	var kept []column
	var words []string
	for _, c := range cols {
		if len(fs) > 1 && c.phrase != nil && same(c.cells) {
			words = append(words, c.phrase(c.cells[0]))
			continue
		}
		kept = append(kept, c)
	}
	if len(words) > 0 {
		line := "On every row: " + strings.Join(words, " · ") + "."
		if shared != "" {
			line += "\n\n" + shared
		}
		shared = line
	}

	heads := make([]string, len(kept))
	for i, c := range kept {
		heads[i] = c.head
	}
	rows := []string{"| " + strings.Join(heads, " | ") + " |", "|" + strings.Repeat(" --- |", len(kept))}
	for i := range fs {
		row := make([]string, len(kept))
		for j, c := range kept {
			row[j] = cell(c.cells[i])
		}
		rows = append(rows, "| "+strings.Join(row, " | ")+" |")
	}
	return strings.Join(rows, "\n"), shared
}

// moveWords reads a seam hypothesis in book words; a token this version does not know is shown as written.
func moveWords(h string) string {
	if h == "" {
		return ""
	}
	if w, ok := report.BalancingMoves[h]; ok {
		return w
	}
	return text(h)
}

// cell escapes the table delimiter; GitHub reads "\|" as a pipe, inside code spans too.
func cell(s string) string { return strings.ReplaceAll(s, "|", `\|`) }

// foldTable writes a group that never blocks as one fold: the count and the effect in the summary, a table, then its
// reasons. effect "" is left out when the count already says it.
func foldTable(b *strings.Builder, fs []report.Finding, seams map[[2]string]report.Seam, one, many, effect string, moves bool) {
	if len(fs) == 0 {
		return
	}
	shown := fs[:min(len(fs), groupCap)]
	pr := make([]string, len(shown))
	for i, f := range shown {
		pr[i] = prWord(f.Origin)
	}
	summary := fmt.Sprintf("%d %s", len(fs), plural(len(fs), one, many))
	if effect != "" {
		summary += " · " + effect
	}
	table, shared := dependencyTable(shown, pr, seams, tableOpts{rule: true, moves: moves})
	fmt.Fprintf(b, "\n\n<details>\n<summary>%s</summary>\n\n%s", summary, table)
	if shared != "" {
		b.WriteString("\n\n" + shared)
	}
	if len(fs) > groupCap {
		fmt.Fprintf(b, "\n\n- and %d more", len(fs)-groupCap)
	}
	writeReasons(b, shown, distinctRules(shown) > 1)
	b.WriteString("\n\n" + foldClose)
}

// distinctRules is how many rules the findings come from: one rule is already named in the line under the table.
func distinctRules(fs []report.Finding) int {
	seen := map[string]bool{}
	for _, f := range fs {
		seen[f.RuleID] = true
	}
	return len(seen)
}

// writeReasons writes each distinct Why, Fix (the engine's constraint) and set of allowed alternatives once, in first-seen order: the
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
			add(f.RuleID, "Why", text(report.ReadableWhy(f.Why)))
		}
		if f.Constraint != "" {
			add(f.RuleID, "Fix", text(f.Constraint))
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
		m += ", introduced finding; source-site origin unknown"
	case prExisting:
		m += ", pre-existing finding; source-site origin unknown"
	default:
		m += ", origin " + text(f.Origin)
	}
	fmt.Fprintf(b, "%s (%s): %s", ruleName(f.RuleID), m, subject(f))
	if len(f.Locations) > 0 || !isMetric(f) { // a metric ratchet has no place in the code
		b.WriteString("\n  - Locations: " + capped(f, locationCap))
	}
	fmt.Fprintf(b, "\n  - Why: %s", text(report.ReadableWhy(f.Why)))
	if f.Constraint != "" {
		fmt.Fprintf(b, "\n  - Fix: %s", text(f.Constraint))
	}
	if len(f.AllowedAlternatives) > 0 {
		fmt.Fprintf(b, "\n  - Allowed alternatives: %s", alternatives(f.AllowedAlternatives))
	}
}

// capped is up to limit locations as file:line, then "and N more" up to the engine's full count (report.LocationsTotal:
// the engine lists at most 50); "none reported" when empty.
func capped(f report.Finding, limit int) string {
	locs := f.Locations
	if len(locs) == 0 {
		return "none reported"
	}
	shown := locs[:min(len(locs), limit)]
	parts := make([]string, len(shown))
	for i, loc := range shown {
		parts[i] = location(loc)
	}
	out := strings.Join(parts, ", ")
	if more := report.LocationsTotal(f) - len(shown); more > 0 {
		out += fmt.Sprintf(" and %d more", more)
	}
	return out
}

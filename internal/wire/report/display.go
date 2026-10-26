package report

import (
	"fmt"
	"regexp"
	"slices"
	"strconv"
	"strings"
)

// Display rules that the PR comment, the App's pull request page and its graph share, so all three tell the same story
// about cycles and about what a pull request added. None of them is a gate decision.

// ShownPerGroup is how many findings of a group the detailed check and App list; the rest are counted. Group searches the
// first ShownPerGroup violations for cycles, so every reader that wants the same cycles passes this.
const ShownPerGroup = 50

// Groups is a report's findings in the groups the comment and the App show. Display only: the gate decides from the
// decision, never from these groups.
type Groups struct {
	Compared   bool      // the engine compared against the pull request base, so origins mean something
	All        []Finding // every finding, Origin cleared when not compared
	Violations []Finding // active gate findings first, then any status this version does not know: never hidden
	Advisories []Finding
	Accepted   []Finding // baseline entries and waivers
	Fixed      []Finding
	OnCycle    map[string]bool // IDs of the first shown violations that lie on a dependency cycle
}

// Group sorts a report's findings into Groups. shown is how many violations the caller shows: only those are searched
// for cycles, because Cycles is quadratic or worse in the findings and a report may hold 10 000.
func Group(r Report, shown int) Groups {
	g := Groups{Compared: r.Comparison.OriginStatus == "comparable"}
	for _, f := range r.Findings {
		if !g.Compared {
			f.Origin = ""
		}
		g.All = append(g.All, f)
		switch {
		case f.Active():
			g.Violations = append(g.Violations, f)
		case f.Status == "baseline" || f.Status == "waived":
			g.Accepted = append(g.Accepted, f)
		case f.Status == "fixed":
			g.Fixed = append(g.Fixed, f)
		case f.Kind == "advisory" && (f.Status == "new" || f.Status == "expired_waiver"):
			g.Advisories = append(g.Advisories, f)
		default:
			g.Violations = append(g.Violations, f)
		}
	}
	slices.SortStableFunc(g.Violations, func(a, b Finding) int { // active first, as before the unknown statuses
		return boolOrder(b.Active()) - boolOrder(a.Active())
	})
	g.OnCycle = map[string]bool{}
	for i, c := range Cycles(g.Violations[:min(len(g.Violations), shown)]) {
		if c >= 0 {
			g.OnCycle[g.Violations[i].ID] = true
		}
	}
	return g
}

func boolOrder(v bool) int {
	if v {
		return 1
	}
	return 0
}

// Check-run headlines the App writes when the engine found violations. In advisory mode such a check is neutral, so
// the App's pages read the headline, never the conclusion alone, to say "violations found" wherever the check is shown.
const (
	HeadlineViolations  = "Architecture violations found" // advisory pull request, or a push: the engine's verdict is blocked
	HeadlineGateFailing = "Architecture gate failing"     // required gate: archfit/gate fails
)

// SplitCovered splits advisories by the violations' module pairs. An advisory on the pair of a violation is the same
// dependency under another rule, so it joins that violation's row ("also reported as") instead of a section of its
// own: covered holds those per pair, in report order; rest is every other advisory. The comment and the App's pages
// both split this way, so their advisory counts agree.
func SplitCovered(violations, advisories []Finding) (rest []Finding, covered map[[2]string][]Finding) {
	pairs := map[[2]string]bool{}
	for _, f := range violations {
		if p := modulePair(f); p[0] != "" && p[1] != "" {
			pairs[p] = true
		}
	}
	covered = map[[2]string][]Finding{}
	for _, f := range advisories {
		if p := modulePair(f); pairs[p] {
			covered[p] = append(covered[p], f)
			continue
		}
		rest = append(rest, f)
	}
	return rest, covered
}

func modulePair(f Finding) [2]string { return [2]string{f.Edge.From.Module, f.Edge.To.Module} }

// Pair origin tokens describe finding evidence on a pair, never pair or source-site novelty.
const (
	PairAdded    = "added"
	PairExisting = "existing"
)

// Cycles returns, per finding, the index of the dependency cycle it lies on, or -1. A finding lies on a cycle when
// its target module reaches its source through findings of the same rule, kind, status and severity (the fields
// Finding.Reverses pairs on). Cycle edges of one such class that share a module are one strongly connected
// component, so they are one cycle item. Indices follow the first finding of each cycle. Display only.
func Cycles(fs []Finding) []int {
	class := func(f Finding) [4]string { return [4]string{f.RuleID, f.Kind, f.Status, f.Severity} }
	edge := func(f Finding) (string, string, bool) {
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

// PairOrigin aggregates non-fixed finding origins after Group has cleared incomparable origins. PairAdded means
// contains an introduced finding, even on a cycle or mixed pair; PairExisting means contains a pre-existing finding.
// Neither value dates the relationship or every source site. No finding evidence means no origin claim.
func PairOrigin(f Finding, all []Finding) string {
	same := func(o Finding) bool {
		return o.Status != "fixed" && o.Edge.From.Module == f.Edge.From.Module && o.Edge.To.Module == f.Edge.To.Module
	}
	if slices.ContainsFunc(all, func(o Finding) bool { return same(o) && o.Origin == "introduced" }) {
		return PairAdded
	}
	if slices.ContainsFunc(all, func(o Finding) bool { return same(o) && o.Origin == "pre_existing" }) {
		return PairExisting
	}
	return ""
}

// EngineRuleNames are the readable names of the engine's own rules; a policy rule's ID is the name its owner chose.
var EngineRuleNames = map[string]string{
	"bc/imbalanced_coupling": "Imbalanced coupling",
	"bc/coupling_gate":       "Coupling gate",
}

// RuleName is a rule's readable name: an engine rule's from EngineRuleNames, a policy rule's ID as its owner chose it.
func RuleName(id string) string {
	if n, ok := EngineRuleNames[id]; ok {
		return n
	}
	return id
}

// FindingMeta is a finding's severity, then its status as a word when the group it is listed in does not already say
// it, in the comment's words: a new or fixed finding is its severity alone, an accepted one is "accepted in the
// baseline" or "accepted by waiver". The comment keeps its own copy, which escapes Markdown.
func FindingMeta(f Finding) string {
	switch f.Status {
	case "new", "fixed":
		return f.Severity
	case "expired_waiver":
		return f.Severity + ", waiver expired"
	case "waived":
		return f.Severity + ", accepted by waiver"
	case "baseline":
		return f.Severity + ", accepted in the baseline"
	}
	return f.Severity + ", " + f.Status
}

// EngineVerdict is the engine's decision as the comment states it, whatever the gate mode does with it: the verdict
// ("blocked") and the rest of the line ("hard gates fail, 2 active blockers, every required rule evaluated").
func EngineVerdict(r Report) (verdict, detail string) {
	gates := map[string]string{"pass": "hard gates pass", "fail": "hard gates fail", "unmeasured": "hard gates not measured"}
	gate, ok := gates[r.Decision.HardGates]
	if !ok {
		gate = "hard gates " + r.Decision.HardGates
	}
	n := r.Decision.ActiveBlockers
	detail = fmt.Sprintf("%s, %d active %s", gate, n, plural(n, "blocker", "blockers"))
	if u := len(r.Decision.UnevaluatedRequiredRules); u > 0 {
		detail += fmt.Sprintf(", %d required %s not evaluated", u, plural(u, "rule", "rules"))
	} else {
		detail += ", every required rule evaluated"
	}
	return strings.ReplaceAll(r.Verdict, "_", " "), detail
}

func plural(n int, one, many string) string {
	if n == 1 {
		return one
	}
	return many
}

// DistanceWords reads the engine's distance tokens in the words of Balanced Coupling; an unknown token is shown as
// written.
var DistanceWords = map[string]string{
	"same_module":                  "same module",
	"cross_module":                 "across modules",
	"cross_module_same_owner":      "across modules, same owner", // engine v2
	"cross_module_different_owner": "across modules, different owners",
	"cross_deploy_unit":            "across services",
	"declared_external":            "external",
}

var distanceToken = regexp.MustCompile(`\b([a-z_]+) distance\b`)

// balancedWhy is the engine's imbalanced-coupling Why: "balanced coupling: <formula> → <band> severity (<explanation>)".
var balancedWhy = regexp.MustCompile(`^balanced coupling: .* → (\w+) severity \((.+)\)$`)

// ReadableWhy is a finding's Why as the App shows it, on the comment and on every page. The engine's "<token> distance"
// reads "distance <words>", so the Why says what the tables beside it say. The imbalanced-coupling Why is said in one
// sentence, "<explanation, → as a colon> (Balanced Coupling: <band> severity)": its formula is the strength, distance
// and volatility the tables already show. Any other text, and a token DistanceWords does not know, stays as written.
func ReadableWhy(why string) string {
	why = distanceToken.ReplaceAllStringFunc(why, func(m string) string {
		token := distanceToken.FindStringSubmatch(m)[1]
		if w, ok := DistanceWords[token]; ok {
			return "distance " + w
		}
		return m
	})
	if m := balancedWhy.FindStringSubmatch(why); m != nil {
		return strings.Replace(m[2], " → ", ": ", 1) + " (Balanced Coupling: " + m[1] + " severity)"
	}
	return why
}

// CycleWhy says why the directions of one dependency cycle are a cycle, from what its table shows. The engine writes one
// Why per direction that only swaps two module names and names the first pair, so no one of them describes the cycle.
func CycleWhy(directions, modules int) string {
	return fmt.Sprintf("Each of the %d dependencies lies on a cycle: from its target, dependencies lead back to its source, so the %d modules depend on each other.", directions, modules)
}

// LocationsTotal is how many locations a finding has: the engine's full count when it reported one, else the locations
// it listed. The engine lists at most 50; a count it reports below the listed ones is ignored.
func LocationsTotal(f Finding) int {
	n, err := strconv.Atoi(f.MatchedBy[MatchedByLocationsTotal])
	if err != nil {
		return len(f.Locations)
	}
	return max(n, len(f.Locations))
}

// BalancingMoves reads the engine's seam hypothesis in the words of Balanced Coupling (integration strength, distance,
// volatility). A hypothesis it does not know has no entry; callers show the token as written.
var BalancingMoves = map[string]string{
	"introduce_contract":    "Introduce a contract: lower the integration strength, so the target can change without breaking this module",
	"move_functionality":    "Move the functionality: lower the distance, so the coupled code lives in one module",
	"accept_low_volatility": "Accept it: the coupled code rarely changes, so the strength costs little",
	"declare_volatility":    "Declare the volatility of the module in the policy: undeclared volatility counts as high",
	"expected_by_role":      "Expected for the role of the module: no change needed",
	"follow_rule":           "A rule violation covers this pair: follow its Fix",
	"balanced":              "Balanced: no change needed",
}

// BalancingMove is the words of BalancingMoves for a hypothesis, the token as written when it has none, "" for none.
func BalancingMove(h string) string {
	if w, ok := BalancingMoves[h]; ok {
		return w
	}
	return h
}

// MetricRulePrefix starts the rule ID of a metric ratchet finding, which has no module edge.
const MetricRulePrefix = "metric/"

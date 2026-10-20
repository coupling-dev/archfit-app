package report

import (
	"regexp"
	"slices"
)

// Display rules that the PR comment, the App's pull request page and its graph share, so all three tell the same story
// about cycles and about what a pull request added. None of them is a gate decision.

// ShownPerGroup is how many findings of a group the comment and the App list; the rest are counted. Group searches the
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

// Pair origins: what the report shows about one direction of a dependency on the pull request base.
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

// PairOrigin is what the report shows about one direction of a cycle on the pull request base. PairExisting: a
// finding on the same module pair is pre_existing, so the base had that dependency. PairAdded: none is, and a finding
// on the pair that lies on no cycle is introduced. "" otherwise: cycle findings cannot tell, because a cycle that is
// new is introduced in every direction, also in those that existed before, and so is the same cycle under another
// rule. all is every finding of the report; onCycle holds the IDs of the findings on a dependency cycle.
func PairOrigin(f Finding, all []Finding, onCycle map[string]bool) string {
	same := func(o Finding) bool {
		return o.Edge.From.Module == f.Edge.From.Module && o.Edge.To.Module == f.Edge.To.Module
	}
	if slices.ContainsFunc(all, func(o Finding) bool { return same(o) && o.Origin == "pre_existing" }) {
		return PairExisting
	}
	if slices.ContainsFunc(all, func(o Finding) bool { return same(o) && o.Origin == "introduced" && !onCycle[o.ID] }) {
		return PairAdded
	}
	return ""
}

// EngineRuleNames are the readable names of the engine's own rules; a policy rule's ID is the name its owner chose.
var EngineRuleNames = map[string]string{
	"bc/imbalanced_coupling": "Imbalanced coupling",
	"bc/coupling_gate":       "Coupling gate",
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

// ReadableWhy rewrites the engine's "<token> distance" in a finding's Why as "distance <words>", so the Why says what
// the tables beside it say. Any other text, and a token DistanceWords does not know, stays as written.
func ReadableWhy(why string) string {
	return distanceToken.ReplaceAllStringFunc(why, func(m string) string {
		token := distanceToken.FindStringSubmatch(m)[1]
		if w, ok := DistanceWords[token]; ok {
			return "distance " + w
		}
		return m
	})
}

// BalancingMoves reads the engine's seam hypothesis in the words of Balanced Coupling (integration strength, distance,
// volatility). A hypothesis it does not know has no entry; callers show the token as written.
var BalancingMoves = map[string]string{
	"introduce_contract":    "Introduce a contract: lower the integration strength, so the target can change without breaking this module",
	"move_functionality":    "Move the functionality: lower the distance, so the coupled code lives in one module",
	"accept_low_volatility": "Accept it: the target rarely changes, so the strength costs little",
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

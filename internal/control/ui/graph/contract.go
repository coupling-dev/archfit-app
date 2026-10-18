// Package graph builds the module graph view model from a report and the policy text.
package graph

import (
	"cmp"
	"slices"
	"unicode/utf8"

	"github.com/alexei-led/archfit-app/internal/wire/policy"
	"github.com/alexei-led/archfit-app/internal/wire/report"
)

type Node struct {
	Name       string
	Layer      string
	PolicyLine int // 1-based line of the module in the policy YAML; 0 if not found
}

// LinkStatus is what the report says about a module pair: the engine's seam policy status when the report carries
// one (every engine release the App reads carries it), else what the findings on the pair say. It is a display fact, never a check input.
type LinkStatus string

const (
	StatusViolation LinkStatus = "violation" // an active gate finding (wire/report Finding.Active), or a gate finding of unknown status, names the pair
	StatusAccepted  LinkStatus = "accepted"  // a gate finding accepted by the baseline or a waiver names the pair
	StatusAdvisory  LinkStatus = "advisory"  // another finding that is not fixed names the pair
	StatusAllowed   LinkStatus = "allowed"   // engine seam policy only: a fail-gated allowlist or layer rule permits the pair
	StatusObserved  LinkStatus = "observed"  // engine seam policy: no active finding names the pair and no rule permits it; without one: no current finding names the pair
)

type Link struct {
	From, To   string
	Status     LinkStatus
	Count      int  // observed dependencies of the pair's seams; 0 when only a finding names the pair
	Introduced bool // a finding that names the pair is one the engine says this change introduced; display only
}

// View is a read-only picture. Stale is true when the policy text no longer matches the report's config_hash.
// Incomplete is true when the report's hard gates did not pass or a required rule was not evaluated: an observed
// link may then still break a rule.
type View struct {
	Nodes      []Node
	Links      []Link
	Findings   []LinkFinding // what names each link, for the page's detail panel; display only
	Stale      bool
	Incomplete bool
}

// LinkFinding is one finding that names a drawn link's module pair, as the engine reported it. It explains a link and
// never decides its Status. Why is engine text: the page must treat it as untrusted.
type LinkFinding struct {
	From, To string
	RuleID   string
	Kind     string
	Status   string
	Origin   string // "introduced" | "pre_existing" | "unknown"; "" unless the report compared with the base branch
	Why      string
	More     int // on the last listed finding of a pair: how many more name the pair and are not listed
}

// Display caps of View.Findings.
const (
	findingsPerLink = 5   // the first ones in report order
	whyRunes        = 400 // longer engine text ends with "…"
)

// Finding vocabulary of wire/report that the status reads. Finding.Active decides an active violation; a gate
// finding of a status outside this vocabulary is a violation too (fail safe).
const (
	kindGate       = "gate"
	statusBaseline = "baseline"
	statusWaived   = "waived"
	statusFixed    = "fixed"

	originComparable = "comparable" // Comparison.OriginStatus: the origin of each finding is meaningful
	originIntroduced = "introduced" // Finding.Origin: the base branch does not have the finding

	hardGatesPass = "pass" // Decision.HardGates; any other value, an unknown one included, is incomplete
)

type pair struct{ from, to string }

// Build is pure. Nodes come from the policy's modules, links from the report's seams and findings. A link's Status
// is the engine's seams[].policy when a seam of the pair carries it; otherwise it comes from the findings that name
// the pair. Graph never computes a status from the policy text.
func Build(rep report.Report, policyText []byte) View {
	defs, err := policy.Modules(policyText)
	if err != nil {
		defs = nil
	}
	stale := err != nil || len(policyText) == 0 || policy.Digest(policyText) != rep.Comparison.ConfigHash

	links := map[pair]*Link{}
	link := func(from, to string) *Link {
		l, ok := links[pair{from, to}]
		if !ok {
			l = &Link{From: from, To: to, Status: StatusObserved}
			links[pair{from, to}] = l
		}
		return l
	}
	// decided holds the pairs whose status the engine gave in seams[].policy: findings never change those.
	decided := map[pair]bool{}
	for _, s := range rep.Seams {
		if s.FromModule == "" || s.ToModule == "" {
			continue
		}
		l := link(s.FromModule, s.ToModule)
		l.Count += s.Edges
		if s.Policy != "" {
			if p := LinkStatus(s.Policy); rank(p) > rank(l.Status) {
				l.Status = p
			}
			decided[pair{s.FromModule, s.ToModule}] = true
		}
	}
	for _, f := range rep.Findings {
		from, to := f.Edge.From.Module, f.Edge.To.Module
		if decided[pair{from, to}] {
			continue
		}
		if s := findingStatus(f); s != StatusObserved && from != "" && to != "" {
			if l := link(from, to); rank(s) > rank(l.Status) {
				l.Status = s
			}
		}
	}

	nodes := make(map[string]Node, len(defs))
	for _, d := range defs {
		if _, dup := nodes[d.Name]; !dup {
			nodes[d.Name] = Node{Name: d.Name, Layer: d.Layer, PolicyLine: d.Line}
		}
	}
	for p := range links {
		for _, name := range [2]string{p.from, p.to} {
			if _, ok := nodes[name]; !ok {
				nodes[name] = Node{Name: name}
			}
		}
	}

	// Findings that name a drawn pair, fixed ones excluded: they say nothing about the current tree.
	// The strongest status first, so the cap never drops the finding that set the link's status; the last listed
	// one of a capped pair counts the rest.
	// A link is introduced when a finding behind it is, and only when the report compared with the base branch.
	compared := rep.Comparison.OriginStatus == originComparable
	byPair := map[pair][]report.Finding{}
	for _, f := range rep.Findings {
		p := pair{f.Edge.From.Module, f.Edge.To.Module}
		if links[p] != nil && f.Status != statusFixed {
			byPair[p] = append(byPair[p], f)
			if compared && f.Origin == originIntroduced {
				links[p].Introduced = true
			}
		}
	}
	findings := []LinkFinding{}
	for p, fs := range byPair {
		slices.SortStableFunc(fs, func(a, b report.Finding) int { return rank(findingStatus(b)) - rank(findingStatus(a)) })
		for i, f := range fs[:min(len(fs), findingsPerLink)] {
			lf := LinkFinding{From: p.from, To: p.to, RuleID: f.RuleID, Kind: f.Kind, Status: f.Status, Why: clip(f.Why)}
			if compared {
				lf.Origin = f.Origin
			}
			if i == findingsPerLink-1 {
				lf.More = len(fs) - findingsPerLink
			}
			findings = append(findings, lf)
		}
	}

	// Non-nil slices: the parent serializes the view to JSON, where an empty graph is [] and not null.
	v := View{
		Nodes:      make([]Node, 0, len(nodes)),
		Links:      make([]Link, 0, len(links)),
		Findings:   findings,
		Stale:      stale,
		Incomplete: rep.Decision.HardGates != hardGatesPass || len(rep.Decision.UnevaluatedRequiredRules) > 0,
	}
	for _, n := range nodes {
		v.Nodes = append(v.Nodes, n)
	}
	for _, l := range links {
		v.Links = append(v.Links, *l)
	}
	slices.SortFunc(v.Nodes, func(a, b Node) int { return cmp.Compare(a.Name, b.Name) })
	slices.SortFunc(v.Links, func(a, b Link) int {
		return cmp.Or(cmp.Compare(a.From, b.From), cmp.Compare(a.To, b.To))
	})
	// Stable: within a pair the report order stays.
	slices.SortStableFunc(v.Findings, func(a, b LinkFinding) int {
		return cmp.Or(cmp.Compare(a.From, b.From), cmp.Compare(a.To, b.To))
	})
	return v
}

// clip shortens engine text to whyRunes runes.
func clip(s string) string {
	if utf8.RuneCountInString(s) <= whyRunes {
		return s
	}
	return string([]rune(s)[:whyRunes-1]) + "…"
}

// findingStatus is the status one finding gives its pair. A fixed finding says nothing about the current tree:
// it gives StatusObserved, which never creates a link. A gate finding whose status this version does not know is a
// violation, fail safe like feedback, which treats an unknown status as live. A kind this version does not know is
// advisory, so a finding is never drawn as a plain observation.
func findingStatus(f report.Finding) LinkStatus {
	switch {
	case f.Active():
		return StatusViolation
	case f.Status == statusFixed:
		return StatusObserved
	case f.Kind == kindGate && (f.Status == statusBaseline || f.Status == statusWaived):
		return StatusAccepted
	case f.Kind == kindGate:
		return StatusViolation
	default:
		return StatusAdvisory
	}
}

// rank orders the statuses, the engine's own order: the strongest status on a pair wins. Findings never give
// StatusAllowed.
func rank(s LinkStatus) int {
	return slices.Index([]LinkStatus{StatusObserved, StatusAllowed, StatusAdvisory, StatusAccepted, StatusViolation}, s)
}

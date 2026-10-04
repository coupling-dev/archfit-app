// Package graph builds the module graph view model from a report and the policy text.
package graph

import (
	"cmp"
	"slices"

	"github.com/alexei-led/archfit-app/internal/wire/policy"
	"github.com/alexei-led/archfit-app/internal/wire/report"
)

type Node struct {
	Name       string
	Layer      string
	PolicyLine int // 1-based line of the module in the policy YAML; 0 if not found
}

// LinkStatus is what the report's findings say about a module pair. It is a display fact, never a check input.
type LinkStatus string

const (
	StatusViolation LinkStatus = "violation" // an active gate finding (wire/report Finding.Active) names the pair
	StatusAccepted  LinkStatus = "accepted"  // a gate finding accepted by the baseline or a waiver names the pair
	StatusAdvisory  LinkStatus = "advisory"  // another finding that is not fixed names the pair
	StatusObserved  LinkStatus = "observed"  // a seam that no current finding names
)

type Link struct {
	From, To string
	Status   LinkStatus
	Count    int // observed dependencies of the pair's seams; 0 when only a finding names the pair
}

// View is a read-only picture. Stale is true when the policy text no longer matches the report's config_hash.
type View struct {
	Nodes []Node
	Links []Link
	Stale bool
}

// Finding vocabulary of wire/report that the status reads; Finding.Active stays the one rule for a violation.
const (
	kindGate       = "gate"
	statusBaseline = "baseline"
	statusWaived   = "waived"
	statusFixed    = "fixed"
)

type pair struct{ from, to string }

// Build is pure. Nodes come from the policy's modules, links from the report's seams and findings; a link's
// Status comes from the findings that name its module pair, never from the policy text.
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
	for _, s := range rep.Seams {
		if s.FromModule != "" && s.ToModule != "" {
			link(s.FromModule, s.ToModule).Count += s.Edges
		}
	}
	for _, f := range rep.Findings {
		from, to := f.Edge.From.Module, f.Edge.To.Module
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

	// Non-nil slices: the parent serializes the view to JSON, where an empty graph is [] and not null.
	v := View{Nodes: make([]Node, 0, len(nodes)), Links: make([]Link, 0, len(links)), Stale: stale}
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
	return v
}

// findingStatus is the status one finding gives its pair. A fixed finding says nothing about the current tree:
// it gives StatusObserved, which never creates a link. A kind or status this version does not know is advisory,
// so a finding is never drawn as a plain observation.
func findingStatus(f report.Finding) LinkStatus {
	switch {
	case f.Active():
		return StatusViolation
	case f.Status == statusFixed:
		return StatusObserved
	case f.Kind == kindGate && (f.Status == statusBaseline || f.Status == statusWaived):
		return StatusAccepted
	default:
		return StatusAdvisory
	}
}

// rank orders the statuses: the strongest finding on a pair sets its status.
func rank(s LinkStatus) int {
	return slices.Index([]LinkStatus{StatusObserved, StatusAdvisory, StatusAccepted, StatusViolation}, s)
}

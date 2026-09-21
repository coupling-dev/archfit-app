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

type Link struct {
	From, To  string
	Observed  bool // seen in the report
	Permitted bool // allowed by policy
	Count     int
}

// View is a read-only picture. Stale is true when the policy text no longer matches the report's config_hash.
type View struct {
	Nodes []Node
	Links []Link
	Stale bool
}

type pair struct{ from, to string }

// Build is pure. Nodes come from the policy's modules, links from the report's seams; a link is not
// Permitted when an Active finding (wire/report `Finding.Active`) names that module pair.
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
			l = &Link{From: from, To: to, Observed: true, Permitted: true}
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
		if from, to := f.Edge.From.Module, f.Edge.To.Module; f.Active() && from != "" && to != "" {
			link(from, to).Permitted = false
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

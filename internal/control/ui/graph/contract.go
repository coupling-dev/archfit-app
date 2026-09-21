// Package graph builds the module graph view model from a report and the policy text.
package graph

import "github.com/alexei-led/archfit-app/internal/wire/report"

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

// Build is pure. Nodes come from the policy's modules, links from the report's seams; a link is not
// Permitted when a gate finding with status "new" or "expired_waiver" names that module pair.
func Build(rep report.Report, policy []byte) View {
	return View{}
}

package graph

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/alexei-led/archfit-app/internal/wire/policy"
	"github.com/alexei-led/archfit-app/internal/wire/report"
)

// fixturePolicy is the policy text the wire fixtures in testdata/ were computed from. It is inline because
// its exact bytes are the fixtures' config_hash.
const fixturePolicy = `version: 2
modules:
  a:
    paths:
      - pkg/a/**
  b:
    paths:
      - pkg/b/**
    public:
      - pkg/b/b.go
    internal:
      - pkg/b/internal/**
rules:
  - id: no_direct_b_dependency
    type: forbidden_dependency
    from: pkg/a/**
    to: pkg/b
    gate: fail
`

const (
	blockedFixture = "real-blocked-no-baseline.json"
	healthyFixture = "healthy.json"
)

const threeModules = `version: 2
modules:
  api:
    layer: app
    paths: [api/**]
  db:
    layer: infra
    paths: [db/**]
  ui:
    layer: app
    paths: [ui/**]
`

func loadReport(t *testing.T, name string) report.Report {
	t.Helper()
	raw, err := os.ReadFile(filepath.Join("testdata", name))
	if err != nil {
		t.Fatalf("read fixture: %v", err)
	}
	rep, err := report.Decode(raw)
	if err != nil {
		t.Fatalf("decode fixture %s: %v", name, err)
	}
	return rep
}

func seam(from, to string, edges int) report.Seam {
	return report.Seam{FromModule: from, ToModule: to, Edges: edges}
}

func finding(kind, status, from, to string) report.Finding {
	return report.Finding{
		Kind:   kind,
		Status: status,
		Edge:   report.Edge{From: report.Endpoint{Module: from}, To: report.Endpoint{Module: to}},
	}
}

// reportFor returns a report computed from policyText: its config_hash is the text's digest.
func reportFor(policyText string, seams []report.Seam, findings []report.Finding) report.Report {
	return report.Report{
		Comparison: report.Comparison{ConfigHash: policy.Digest([]byte(policyText))},
		Seams:      seams,
		Findings:   findings,
	}
}

func observed(from, to string, count int, permitted bool) Link {
	return Link{From: from, To: to, Observed: true, Permitted: permitted, Count: count}
}

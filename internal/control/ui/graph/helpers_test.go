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
	rep, err := report.Decode(raw, "v2.3.1")
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

func link(from, to string, count int, status LinkStatus) Link {
	return Link{From: from, To: to, Status: status, Count: count}
}

// corpusEngine is the engine release of the shared wire corpus's synthetic fixtures and older captures.
const corpusEngine = "v2.3.1"

// corpusFile reads a file of the shared wire corpus; internal/wire/module.md owns its names.
func corpusFile(t *testing.T, name string) []byte {
	t.Helper()
	raw, err := os.ReadFile(filepath.Join("..", "..", "..", "wire", "testdata", "report", name))
	if err != nil {
		t.Fatalf("read corpus file: %v", err)
	}
	return raw
}

// corpusReport decodes a report of the shared wire corpus as a document of release.
func corpusReport(t *testing.T, name, release string) report.Report {
	t.Helper()
	rep, err := report.Decode(corpusFile(t, name+".json"), release)
	if err != nil {
		t.Fatalf("decode corpus fixture %s: %v", name, err)
	}
	return rep
}

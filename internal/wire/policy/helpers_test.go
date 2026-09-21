package policy

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
	"time"
)

// The shared corpus; its layout, sibling suffixes and expectation format are defined in internal/wire/module.md.
const (
	fixtureDir      = "../testdata"
	sharedPolicyDir = fixtureDir + "/policy"
	sharedReportDir = fixtureDir + "/report"

	fullPolicy            = "policy/full.yaml"
	fullExpectation       = "policy/full.expect.json"
	malformedExpiryPolicy = "policy/malformed-expiry.yaml"
	realCapturePolicy     = "report/real-blocked-no-baseline.policy.yaml"
)

// The one private fixture: the shared corpus has no engine report for the malformed-expiry policy.
const malformedExpiryReport = "testdata/policy/malformed-expiry.report.json"

func fixture(t testing.TB, name string) []byte {
	t.Helper()
	raw, err := os.ReadFile(filepath.Join(fixtureDir, name))
	if err != nil {
		t.Fatalf("read fixture: %v", err)
	}
	return raw
}

func fixtureJSON(t testing.TB, name string, into any) {
	t.Helper()
	if err := json.Unmarshal(fixture(t, name), into); err != nil {
		t.Fatalf("decode fixture %s: %v", name, err)
	}
}

// policyPaths lists every policy file of the shared corpus: the policy fixtures and the report fixtures' siblings.
func policyPaths(t testing.TB) []string {
	t.Helper()
	var paths []string
	for _, pattern := range []string{
		filepath.Join(sharedPolicyDir, "*.yaml"),
		filepath.Join(sharedReportDir, "*.policy.yaml"),
	} {
		found, err := filepath.Glob(pattern)
		if err != nil || len(found) == 0 {
			t.Fatalf("no fixtures for %s: %v", pattern, err)
		}
		paths = append(paths, found...)
	}
	return paths
}

func policyFixtures(t testing.TB) [][]byte {
	t.Helper()
	var out [][]byte
	for _, path := range policyPaths(t) {
		raw, err := os.ReadFile(path)
		if err != nil {
			t.Fatalf("read fixture: %v", err)
		}
		out = append(out, raw)
	}
	return out
}

func endOfDay(year int, month time.Month, day int) time.Time {
	return time.Date(year, month, day, 23, 59, 59, 0, time.UTC)
}

func mustWaivers(t *testing.T, policy []byte) []Waiver {
	t.Helper()
	got, err := Waivers(policy)
	if err != nil {
		t.Fatalf("Waivers: %v", err)
	}
	return got
}

func mustModules(t *testing.T, policy []byte) []ModuleDef {
	t.Helper()
	got, err := Modules(policy)
	if err != nil {
		t.Fatalf("Modules: %v", err)
	}
	return got
}

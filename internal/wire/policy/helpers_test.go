package policy

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
	"time"
)

// Fixtures live inside the module until the shared internal/wire/testdata/policy/ set exists.
const fixtureDir = "testdata/policy"

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

func policyFixtures(t testing.TB) [][]byte {
	t.Helper()
	names, err := filepath.Glob(filepath.Join(fixtureDir, "*.archfit.yaml"))
	if err != nil || len(names) == 0 {
		t.Fatalf("no policy fixtures: %v", err)
	}
	var out [][]byte
	for _, name := range names {
		out = append(out, fixture(t, filepath.Base(name)))
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

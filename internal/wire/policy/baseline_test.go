package policy

import (
	"bytes"
	"strings"
	"testing"
)

const (
	comparableBaseline = "report/real-baselined-comparable.baseline.json"
	comparablePolicy   = "report/real-baselined-comparable.policy.yaml"
	emptyBaseline      = "report/real-attention-baseline.baseline.json"
)

// minimalBaseline is the smallest file ReadBaseline accepts, with fields replaced by edit.
func minimalBaseline(edit func(string) string) []byte {
	doc := `{"schema_version":"archfit.baseline.v3",` +
		`"accepted":[{"fingerprint":"ba3803eca947bf3c1b7efa8f37854d5e","rule_id":"no_direct_b_dependency","kind":"gate"}],` +
		`"state":{"config_hash":"` + strings.Repeat("4a", 32) + `","measurement_profile":{"version":"archfit.measurement.v2"}}}`
	if edit != nil {
		doc = edit(doc)
	}
	return []byte(doc)
}

func TestReadBaselineRealCaptures(t *testing.T) {
	got, err := ReadBaseline(fixture(t, comparableBaseline))
	if err != nil {
		t.Fatalf("ReadBaseline: %v", err)
	}
	want := []Accepted{
		{Fingerprint: "ba3803eca947bf3c1b7efa8f37854d5e", RuleID: "no_direct_b_dependency"},
		{Fingerprint: "07af466df8bfd6e77ee485ac01c50a17", RuleID: "bc/imbalanced_coupling"},
	}
	if len(got.Accepted) != len(want) {
		t.Fatalf("accepted = %+v, want %+v", got.Accepted, want)
	}
	for i := range want {
		if got.Accepted[i] != want[i] {
			t.Errorf("accepted[%d] = %+v, want %+v", i, got.Accepted[i], want[i])
		}
	}
	// The capture's config hash is the engine's digest of the policy it ran under.
	if want := Digest(fixture(t, comparablePolicy)); got.ConfigHash != want {
		t.Errorf("ConfigHash = %s, want the policy digest %s", got.ConfigHash, want)
	}
	if got.ProfileVersion != "archfit.measurement.v2" {
		t.Errorf("ProfileVersion = %q", got.ProfileVersion)
	}

	empty, err := ReadBaseline(fixture(t, emptyBaseline))
	if err != nil || len(empty.Accepted) != 0 || empty.ConfigHash != got.ConfigHash {
		t.Errorf("empty capture = %+v, %v", empty, err)
	}
}

func TestReadBaselineRejects(t *testing.T) {
	replace := func(old, new string) func(string) string {
		return func(s string) string { return strings.Replace(s, old, new, 1) }
	}
	tests := []struct {
		name string
		raw  []byte
	}{
		{"empty", nil},
		{"not JSON", []byte("version: 1\n")},
		{"array", []byte(`[]`)},
		{"trailing data", append(minimalBaseline(nil), []byte(` {}`)...)},
		{"invalid UTF-8", minimalBaseline(replace("no_direct_b_dependency", "no_\xff"))},
		{"over the cap", append(minimalBaseline(nil), bytes.Repeat([]byte(" "), MaxBaselineBytes)...)},
		{"schema v1", minimalBaseline(replace("archfit.baseline.v3", "archfit.baseline.v1"))},
		{"schema missing", minimalBaseline(replace(`"schema_version":"archfit.baseline.v3",`, ""))},
		{"accepted missing", minimalBaseline(replace(`"accepted":[`, `"other":[`))},
		{"accepted null", []byte(`{"schema_version":"archfit.baseline.v3","accepted":null,"state":{"config_hash":"` +
			strings.Repeat("4a", 32) + `","measurement_profile":{"version":"archfit.measurement.v2"}}}`)},
		{"state missing", minimalBaseline(replace(`"state":`, `"other":`))},
		{"config hash prefixed", minimalBaseline(replace(`"config_hash":"`, `"config_hash":"sha256:`))},
		{"config hash empty", minimalBaseline(replace(strings.Repeat("4a", 32), ""))},
		{"config hash uppercase", minimalBaseline(replace(strings.Repeat("4a", 32), strings.Repeat("4A", 32)))},
		{"profile missing", minimalBaseline(replace(`"measurement_profile"`, `"other_profile"`))},
		{"profile version empty", minimalBaseline(replace("archfit.measurement.v2", ""))},
		{"profile version with space", minimalBaseline(replace("archfit.measurement.v2", "archfit measurement"))},
		{"fingerprint 31 hex", minimalBaseline(replace("ba3803eca947bf3c1b7efa8f37854d5e", "ba3803eca947bf3c1b7efa8f37854d5"))},
		{"fingerprint uppercase", minimalBaseline(replace("ba3803eca947bf3c1b7efa8f37854d5e", "BA3803ECA947BF3C1B7EFA8F37854D5E"))},
		{"fingerprint missing", minimalBaseline(replace(`"fingerprint":"ba3803eca947bf3c1b7efa8f37854d5e",`, ""))},
		{"rule id with space", minimalBaseline(replace("no_direct_b_dependency", "no direct"))},
		{"rule id with markdown", minimalBaseline(replace("no_direct_b_dependency", "x`](https://evil)"))},
		{"rule id empty", minimalBaseline(replace("no_direct_b_dependency", ""))},
		{"rule id too long", minimalBaseline(replace("no_direct_b_dependency", strings.Repeat("r", 129)))},
		{"duplicate fingerprint", minimalBaseline(replace(`"kind":"gate"}`,
			`"kind":"gate"},{"fingerprint":"ba3803eca947bf3c1b7efa8f37854d5e","rule_id":"other"}`))},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if _, err := ReadBaseline(tt.raw); err == nil {
				t.Error("ReadBaseline accepted the file")
			}
		})
	}
	if _, err := ReadBaseline(minimalBaseline(nil)); err != nil {
		t.Fatalf("the unedited minimal baseline is refused: %v", err)
	}
}

// The engine adds baseline keys over time; the App reads only what it needs (critiques #387).
func TestReadBaselineIgnoresUnknownKeys(t *testing.T) {
	raw := minimalBaseline(func(s string) string {
		s = strings.Replace(s, `"accepted":`, `"module_digests":{"a":"b"},"accepted":`, 1)
		s = strings.Replace(s, `"kind":"gate"`, `"kind":"gate","subject":"x"`, 1)
		return strings.Replace(s, `"state":{`, `"state":{"seams":[1,2],`, 1)
	})
	got, err := ReadBaseline(raw)
	if err != nil || len(got.Accepted) != 1 {
		t.Fatalf("ReadBaseline = %+v, %v", got, err)
	}
}

func TestReadBaselineErrorsNeverEchoValues(t *testing.T) {
	const marker = "LEAK-BASELINE-7f3a"
	for _, raw := range [][]byte{
		minimalBaseline(func(s string) string { return strings.Replace(s, "no_direct_b_dependency", marker+" x", 1) }),
		minimalBaseline(func(s string) string { return strings.Replace(s, "archfit.baseline.v3", marker, 1) }),
		[]byte(`{"schema_version":"` + marker + `"`),
	} {
		_, err := ReadBaseline(raw)
		if err == nil || strings.Contains(err.Error(), marker) {
			t.Errorf("error %v echoes the input", err)
		}
	}
}

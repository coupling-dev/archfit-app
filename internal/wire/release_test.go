package wire_test

import (
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"github.com/alexei-led/archfit-app/internal/wire/envelope"
	"github.com/alexei-led/archfit-app/internal/wire/policy"
	"github.com/alexei-led/archfit-app/internal/wire/report"
)

func TestV323NativeCapturesPreserveMetricApplicability(t *testing.T) {
	for _, arch := range []string{"amd64", "arm64"} {
		t.Run(arch, func(t *testing.T) {
			name := "real-v323-" + arch + "-blocked-no-baseline"
			raw := readFile(t, filepath.Join(corpusRoot, folderReport, name+".json"))
			r, err := report.Decode(raw, "v3.2.3")
			if err != nil {
				t.Fatal(err)
			}
			env, err := envelope.Decode(readFile(t, filepath.Join(corpusRoot, folderReport, name+".envelope.json")))
			if err != nil {
				t.Fatal(err)
			}
			if env.EngineVersion != "v3.2.3" || env.Platform != "linux/"+arch || env.PayloadDigest != envelope.PayloadDigest(raw) {
				t.Fatalf("actual capture identity does not bind: %+v", env)
			}
			for _, d := range r.Dimensions {
				for _, m := range d.Metrics {
					if m.Name == "encapsulation" {
						t.Error("N/A encapsulation acquired a zero value")
					}
				}
			}
			if len(r.Dimensions["drift"].Metrics) != 0 {
				t.Error("unmeasured drift acquired metrics")
			}
			cycle := false
			for _, m := range r.Dimensions["structure"].Metrics {
				if m.Name == "cycle" {
					cycle = true
					if m.Value != 0 {
						t.Errorf("measured cycle=%v", m.Value)
					}
				}
			}
			if !cycle {
				t.Error("measured cycle zero disappeared")
			}
			for _, tt := range []struct {
				file     string
				accepted int
			}{{"captured-baseline.json", 2}, {"reanchored-baseline.json", 0}} {
				b, err := policy.ReadBaseline(readFile(t, filepath.Join(corpusRoot, "release-v323", arch, tt.file)))
				if err != nil {
					t.Fatal(err)
				}
				if len(b.Accepted) != tt.accepted || b.ConfigHash != r.Comparison.ConfigHash || b.ProfileVersion != r.Comparison.MeasurementProfile.Version {
					t.Fatalf("baseline does not match native capture: %+v", b)
				}
				if _, ok := b.Metrics["encapsulation"]; ok {
					t.Error("N/A baseline metric fabricated")
				}
				if got, ok := b.Metrics["cycle"]; !ok || got != (policy.MetricValue{Value: 0, Version: "cycle.v1"}) {
					t.Errorf("measured baseline cycle=%+v present=%v", got, ok)
				}
				for i, a := range b.Accepted {
					if a.Fingerprint != r.Findings[i].ID || a.RuleID != r.Findings[i].RuleID {
						t.Errorf("accepted identity differs: %+v", a)
					}
				}
			}
			log := string(readFile(t, filepath.Join(corpusRoot, "release-v323", arch, "reanchor-report.txt")))
			for _, want := range []string{"kept 0 accepted entries, dropped 1", "metric reference not carried: encapsulation (current metric is unmeasured)"} {
				if !strings.Contains(log, want) {
					t.Errorf("actual reanchor missing %q", want)
				}
			}
		})
	}
}

func TestV323CaptureBytesAreUnmodified(t *testing.T) {
	for path, want := range map[string]string{
		"report/real-v323-amd64-blocked-no-baseline.json":          "f2d2aaa419a304b6ca92d83ad1a16d75cca43caabbb97706ec7408162e6fbb01",
		"report/real-v323-amd64-blocked-no-baseline.envelope.json": "5fb5f3e6ac694e70fb7376a8596b76ceb96f9b09fdf4ccff035a809608c7507e",
		"release-v323/amd64/captured-baseline.json":                "69bf7bd1c7107cd63b61a6cad9f3c5d559be9e0c3cb056b30d3410632411e339",
		"release-v323/amd64/reanchored-baseline.json":              "44de87108c683538876b88dbfbd7aaf8e18ad4a37ae64016db9fb3fd788c318b",
		"release-v323/amd64/reanchor-report.txt":                   "9150a14a515ac840b0b6cd851a89dee49e65e4d80bcb8395c6e484f7d0fd6f38",
		"report/real-v323-arm64-blocked-no-baseline.json":          "c472d77d04cf03d912d1a3f8b41e7494c9a688da60d957a71d789e05f07c5127",
		"report/real-v323-arm64-blocked-no-baseline.envelope.json": "00dfe2d810115e0b325ea447fd26f4e9d5fd873e985511f764bfc219e7bcd184",
		"release-v323/arm64/captured-baseline.json":                "8dd2d79aefa9852cbf6bfbcd20b6b38e666d83986d66f464f9e142a2a260418e",
		"release-v323/arm64/reanchored-baseline.json":              "254496115e437d0f7d32f7273626a808596152556d40b0edfb24bb9ee848a88f",
		"release-v323/arm64/reanchor-report.txt":                   "9150a14a515ac840b0b6cd851a89dee49e65e4d80bcb8395c6e484f7d0fd6f38",
	} {
		if got := policy.Digest(readFile(t, filepath.Join(corpusRoot, path))); got != want {
			t.Errorf("%s SHA256=%s want %s", path, got, want)
		}
	}
}

func TestV322CapturePreservesMetricApplicability(t *testing.T) {
	raw := readFile(t, filepath.Join(corpusRoot, folderReport, "real-v322-blocked-no-baseline.json"))
	r, err := report.Decode(raw, "v3.2.2")
	if err != nil {
		t.Fatal(err)
	}
	for _, d := range r.Dimensions {
		for _, m := range d.Metrics {
			if m.Name == "encapsulation" {
				t.Error("N/A encapsulation must remain absent, not measured zero")
			}
		}
	}
	if len(r.Dimensions["drift"].Metrics) != 0 {
		t.Error("unmeasured drift must not acquire metrics")
	}
	cycleFound := false
	for _, m := range r.Dimensions["structure"].Metrics {
		if m.Name == "cycle" {
			cycleFound = true
			if m.Value != 0 {
				t.Errorf("measured cycle = %v, want 0", m.Value)
			}
		}
	}
	if !cycleFound {
		t.Error("measured cycle zero must not be omitted")
	}
	for _, tt := range []struct {
		file     string
		accepted int
	}{
		{"captured-baseline.json", 2},
		{"reanchored-baseline.json", 0},
	} {
		t.Run(tt.file, func(t *testing.T) {
			b, err := policy.ReadBaseline(readFile(t, filepath.Join(corpusRoot, "release-v322", tt.file)))
			if err != nil {
				t.Fatal(err)
			}
			if len(b.Accepted) != tt.accepted || b.ConfigHash != r.Comparison.ConfigHash || b.ProfileVersion != r.Comparison.MeasurementProfile.Version {
				t.Fatalf("baseline does not match capture: %+v", b)
			}
			if _, exists := b.Metrics["encapsulation"]; exists {
				t.Error("N/A encapsulation must not become a baseline metric")
			}
			if got, ok := b.Metrics["cycle"]; !ok || got != (policy.MetricValue{Value: 0, Version: "cycle.v1"}) {
				t.Errorf("baseline measured cycle = %+v, present=%v", got, ok)
			}
			if tt.accepted != 0 {
				for i, a := range b.Accepted {
					if a.Fingerprint != r.Findings[i].ID || a.RuleID != r.Findings[i].RuleID {
						t.Errorf("accepted finding %+v does not match report", a)
					}
				}
			}
		})
	}
	log := string(readFile(t, filepath.Join(corpusRoot, "release-v322", "reanchor-report.txt")))
	for _, want := range []string{"kept 0 accepted entries, dropped 1", "metric reference not carried: encapsulation (current metric is unmeasured)"} {
		if !strings.Contains(log, want) {
			t.Errorf("reanchor capture missing %q", want)
		}
	}
}

func TestV322CaptureBytesAreUnmodified(t *testing.T) {
	for path, want := range map[string]string{
		"report/real-v322-blocked-no-baseline.json":          "c472d77d04cf03d912d1a3f8b41e7494c9a688da60d957a71d789e05f07c5127",
		"report/real-v322-blocked-no-baseline.envelope.json": "1263ecc36c052ccd11bd2d73d452dbc2b9e2a73f55620cc350c03fef8b974e8e",
		"release-v322/captured-baseline.json":                "8dd2d79aefa9852cbf6bfbcd20b6b38e666d83986d66f464f9e142a2a260418e",
		"release-v322/reanchored-baseline.json":              "254496115e437d0f7d32f7273626a808596152556d40b0edfb24bb9ee848a88f",
		"release-v322/reanchor-report.txt":                   "9150a14a515ac840b0b6cd851a89dee49e65e4d80bcb8395c6e484f7d0fd6f38",
	} {
		if got := policy.Digest(readFile(t, filepath.Join(corpusRoot, path))); got != want {
			t.Errorf("%s SHA-256 = %s, want %s", path, got, want)
		}
	}
	captured, err := policy.ReadBaseline(readFile(t, filepath.Join(corpusRoot, "release-v322", "captured-baseline.json")))
	if err != nil {
		t.Fatal(err)
	}
	reanchored, err := policy.ReadBaseline(readFile(t, filepath.Join(corpusRoot, "release-v322", "reanchored-baseline.json")))
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(captured.Metrics, reanchored.Metrics) {
		t.Errorf("reanchor changed current measured references: %v -> %v", captured.Metrics, reanchored.Metrics)
	}
}

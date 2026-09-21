package policy

import (
	"bytes"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
)

var bareHex = regexp.MustCompile(`^[0-9a-f]{64}$`)

func TestDigestKnownVectors(t *testing.T) {
	base := fixture(t, "full.archfit.yaml")
	if !bytes.HasSuffix(base, []byte("\n")) {
		t.Fatal("fixture must end with a newline")
	}
	tests := []struct {
		name string
		in   []byte
		want string
	}{
		{"empty", nil, "e3b0c44298fc1c149afbf4c8996fb92427ae41e4649b934ca495991b7852b855"},
		{"abc", []byte("abc"), "ba7816bf8f01cfea414140de5dae2223b00361a396177a9cb410ff61f20015ad"},
		{"fixture", base, "b8dcc8eaf47d1aeffc6843172722286719ce9189ad4eae93564f1efbab6a9e87"},
		{"fixture CRLF", bytes.ReplaceAll(base, []byte("\n"), []byte("\r\n")), ""},
		{"fixture BOM", append([]byte("\xef\xbb\xbf"), base...), ""},
		{"fixture no trailing newline", bytes.TrimSuffix(base, []byte("\n")), ""},
	}
	seen := map[string]string{}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := Digest(tt.in)
			if tt.want != "" && got != tt.want {
				t.Errorf("Digest = %s, want %s", got, tt.want)
			}
			if !bareHex.MatchString(got) || strings.HasPrefix(got, "sha256:") {
				t.Errorf("Digest = %q, want bare lowercase hex", got)
			}
			if other, ok := seen[got]; ok {
				t.Errorf("same digest as %q", other)
			}
			seen[got] = tt.name
		})
	}
}

func TestDigestMatchesEngineConfigHash(t *testing.T) {
	reports, err := filepath.Glob(filepath.Join(fixtureDir, "*.report.json"))
	if err != nil || len(reports) == 0 {
		t.Fatalf("no report fixtures: %v", err)
	}
	for _, path := range reports {
		name := strings.TrimSuffix(filepath.Base(path), ".report.json")
		t.Run(name, func(t *testing.T) {
			var report struct {
				Comparison struct {
					ConfigHash string `json:"config_hash"`
				} `json:"comparison"`
			}
			fixtureJSON(t, name+".report.json", &report)
			if got := Digest(fixture(t, name+".archfit.yaml")); got != report.Comparison.ConfigHash {
				t.Errorf("Digest = %s, want config_hash %s", got, report.Comparison.ConfigHash)
			}
		})
	}

	// The real engine v2.3.1 capture, pinned independently of its report fixture.
	const realCapture = "4a18013bab44d922fa67d9e8e699463b2b5284f1c2908b62d2b7c5f607c55574"
	if got := Digest(fixture(t, "real-blocked-no-baseline.archfit.yaml")); got != realCapture {
		t.Errorf("real capture Digest = %s, want %s", got, realCapture)
	}
}

func TestDigestMatchesEnvelopeInputDigests(t *testing.T) {
	for _, upload := range []string{"upload-full", "upload-no-inputs"} {
		t.Run(upload, func(t *testing.T) {
			var envelope struct {
				BaselineDigest string `json:"baseline_digest"`
				LabelsDigest   string `json:"labels_digest"`
			}
			fixtureJSON(t, upload+"/envelope.json", &envelope)
			files := []struct{ file, stated string }{
				{"baseline.json", envelope.BaselineDigest},
				{"labels.yaml", envelope.LabelsDigest},
			}
			for _, f := range files {
				raw, err := os.ReadFile(filepath.Join(fixtureDir, upload, f.file))
				if os.IsNotExist(err) {
					if f.stated != "" {
						t.Errorf("%s: no file, envelope states %q, want \"\"", f.file, f.stated)
					}
					continue
				}
				if err != nil {
					t.Fatal(err)
				}
				if got := Digest(raw); got != f.stated {
					t.Errorf("%s: Digest = %s, envelope states %s", f.file, got, f.stated)
				}
			}
		})
	}
}

func TestPathConstants(t *testing.T) {
	tests := []struct{ name, got, want string }{
		{"PolicyPath", PolicyPath, ".archfit.yaml"},
		{"BaselinePath", BaselinePath, ".archfit-baseline.json"},
		{"LabelsPath", LabelsPath, ".archfit-labels.yaml"},
	}
	for _, tt := range tests {
		if tt.got != tt.want {
			t.Errorf("%s = %q, want %q", tt.name, tt.got, tt.want)
		}
	}
}

func TestPolicyEditChangesDigest(t *testing.T) {
	base := fixture(t, "full.archfit.yaml")
	tests := []struct {
		name string
		head []byte
	}{
		{"one waiver line", bytes.Replace(base, []byte("expires: 2026-12-31"), []byte("expires: 2027-12-31"), 1)},
		{"whitespace only", bytes.Replace(base, []byte("version: 2\n"), []byte("version: 2 \n"), 1)},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if bytes.Equal(tt.head, base) {
				t.Fatal("head does not differ from base")
			}
			if Digest(tt.head) == Digest(base) {
				t.Error("edited policy has the digest of the base policy")
			}
		})
	}
}

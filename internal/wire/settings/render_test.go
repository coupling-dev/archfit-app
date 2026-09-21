package settings

import (
	"bytes"
	"math/rand/v2"
	"os"
	"path/filepath"
	"reflect"
	"regexp"
	"strings"
	"testing"
)

const wantHeader = "# Archfit App settings. Changes need architecture-owner approval.\n"

// The rules are restated here on purpose: the checks must not share code with the parser.
var (
	ruleLogin    = regexp.MustCompile(`^[A-Za-z0-9-]+$`)
	ruleTeam     = regexp.MustCompile(`^[A-Za-z0-9-]+/[A-Za-z0-9._-]+$`)
	ruleWorkflow = regexp.MustCompile(`^\.github/workflows/[A-Za-z0-9._-]+\.ya?ml$`)
)

// checkFieldRules fails the test if s breaks any version 1 field rule.
func checkFieldRules(t *testing.T, s Settings) {
	t.Helper()
	if s.Version != 1 {
		t.Errorf("accepted version %d", s.Version)
	}
	if len(s.Owners) < 1 || len(s.Owners) > 100 {
		t.Errorf("accepted %d owners", len(s.Owners))
	}
	seen := map[string]bool{}
	for _, o := range s.Owners {
		isLogin := ruleLogin.MatchString(o) && len(o) <= 39
		if !isLogin && !ruleTeam.MatchString(o) {
			t.Errorf("accepted owner %q", o)
		}
		if seen[strings.ToLower(o)] {
			t.Errorf("accepted duplicate owner %q", o)
		}
		seen[strings.ToLower(o)] = true
	}
	if s.Gate != GateAdvisory && s.Gate != GateRequired {
		t.Errorf("accepted gate %q", s.Gate)
	}
	if s.DataMode != DataCustomer && s.DataMode != DataManaged {
		t.Errorf("accepted data_mode %q", s.DataMode)
	}
	if !ruleWorkflow.MatchString(s.Workflow) || len(s.Workflow) > 255 {
		t.Errorf("accepted workflow %q", s.Workflow)
	}
}

// checkRoundTrip asserts the round-trip law for a valid s with both modes set.
func checkRoundTrip(t *testing.T, s Settings) {
	t.Helper()
	out := Render(s)
	back, err := Parse(out)
	if err != nil {
		t.Fatalf("Parse(Render(s)) failed: %v\n%s", err, out)
	}
	if !reflect.DeepEqual(back, s) {
		t.Fatalf("Parse(Render(s)) = %+v, want %+v", back, s)
	}
	if again := Render(back); !bytes.Equal(again, out) {
		t.Fatalf("Render(Parse(Render(s))) differs:\n%s\nwant:\n%s", again, out)
	}
}

func validFixtures(t testing.TB) map[string][]byte {
	t.Helper()
	paths, err := filepath.Glob(filepath.Join("testdata", "valid", "*.yaml"))
	if err != nil || len(paths) == 0 {
		t.Fatalf("no valid fixtures: %v", err)
	}
	fixtures := make(map[string][]byte, len(paths))
	for _, p := range paths {
		raw, err := os.ReadFile(p)
		if err != nil {
			t.Fatal(err)
		}
		fixtures[filepath.Base(p)] = raw
	}
	return fixtures
}

func TestRenderIsCanonical(t *testing.T) {
	s := Settings{
		Version:  1,
		Owners:   []string{"alice", "acme/architects"},
		Gate:     GateRequired,
		Workflow: ".github/workflows/archfit.yaml",
		DataMode: DataManaged,
	}
	golden, err := os.ReadFile(filepath.Join("testdata", "golden", "canonical.yaml"))
	if err != nil {
		t.Fatal(err)
	}
	got := Render(s)
	if !bytes.Equal(got, Render(s)) {
		t.Error("two renders of one value differ")
	}
	if !bytes.Equal(got, golden) {
		t.Errorf("Render =\n%s\nwant golden:\n%s", got, golden)
	}

	text := string(got)
	if !strings.HasPrefix(text, wantHeader) {
		t.Errorf("output does not start with the header comment:\n%s", text)
	}
	if strings.Contains(text, "\r") {
		t.Error("output contains a carriage return")
	}
	if !strings.HasSuffix(text, "\n") || strings.HasSuffix(text, "\n\n") {
		t.Error("output does not end with exactly one newline")
	}
	last := -1
	for _, key := range []string{"version:", "owners:", "gate:", "workflow:", "data_mode:"} {
		at := strings.Index(text, "\n"+key)
		if at <= last {
			t.Errorf("key %q is missing or out of order", key)
		}
		last = at
	}
	if !strings.Contains(text, "owners:\n  - alice\n  - acme/architects\n") {
		t.Errorf("owners is not a block sequence with two-space indentation:\n%s", text)
	}
}

// awkwardOwners are valid owners that a YAML plain scalar would read as another type or as syntax.
var awkwardOwners = []string{
	"123", "0", "-1", "0x1f", "0o17", "0b1", "1e3", "true", "False", "NULL",
	"yes", "No", "on", "OFF", "y", "n", "-", "--", "---", "-a", "a-",
	"1/2", "null/null", "true/false", "acme/.", "acme/..", "acme/-", "acme/...", "acme/1.0", "-/-",
	strings.Repeat("a", 39), strings.Repeat("-", 39), "acme/" + strings.Repeat("t", 200),
}

const nameAlphabet = "abcdefghijklmnopqrstuvwxyzABCDEFGHIJKLMNOPQRSTUVWXYZ0123456789-"

func randomName(r *rand.Rand, alphabet string, maxLen int) string {
	b := make([]byte, 1+r.IntN(maxLen))
	for i := range b {
		b[i] = alphabet[r.IntN(len(alphabet))]
	}
	return string(b)
}

func randomSettings(r *rand.Rand) Settings {
	s := Settings{Version: 1, Gate: GateAdvisory, DataMode: DataCustomer}
	if r.IntN(2) == 0 {
		s.Gate = GateRequired
	}
	if r.IntN(2) == 0 {
		s.DataMode = DataManaged
	}

	count := 1 + r.IntN(5)
	if r.IntN(20) == 0 {
		count = 100
	}
	seen := map[string]bool{}
	for len(s.Owners) < count {
		var o string
		switch r.IntN(3) {
		case 0:
			o = awkwardOwners[r.IntN(len(awkwardOwners))]
		case 1:
			o = randomName(r, nameAlphabet, 39)
		default:
			o = randomName(r, nameAlphabet, 39) + "/" + randomName(r, nameAlphabet+"._", 30)
		}
		if seen[strings.ToLower(o)] {
			continue
		}
		seen[strings.ToLower(o)] = true
		s.Owners = append(s.Owners, o)
	}

	ext := []string{".yml", ".yaml"}[r.IntN(2)]
	maxName := 255 - len(".github/workflows/") - len(ext)
	nameLen := 40
	if r.IntN(10) == 0 {
		nameLen = maxName
	}
	name := randomName(r, nameAlphabet+"._", nameLen)
	if nameLen == maxName {
		name += strings.Repeat("w", maxName-len(name))
	}
	s.Workflow = ".github/workflows/" + name + ext
	return s
}

func TestParseRenderRoundTrip(t *testing.T) {
	t.Run("generated", func(t *testing.T) {
		r := rand.New(rand.NewPCG(7, 3))
		for range 2000 {
			s := randomSettings(r)
			checkFieldRules(t, s)
			checkRoundTrip(t, s)
		}
	})
	t.Run("every awkward owner", func(t *testing.T) {
		checkRoundTrip(t, Settings{
			Version:  1,
			Owners:   awkwardOwners,
			Gate:     GateRequired,
			Workflow: ".github/workflows/null.yml",
			DataMode: DataManaged,
		})
	})
	for name, raw := range validFixtures(t) {
		t.Run(name, func(t *testing.T) {
			s, err := Parse(raw)
			if err != nil {
				t.Fatalf("valid fixture rejected: %v", err)
			}
			checkFieldRules(t, s)
			checkRoundTrip(t, s)
		})
	}
}

func TestRenderFillsDefaults(t *testing.T) {
	s := Settings{Version: 1, Owners: []string{"alice"}, Workflow: ".github/workflows/archfit.yaml"}
	out := string(Render(s))
	for _, line := range []string{"\ngate: advisory\n", "\ndata_mode: customer\n"} {
		if !strings.Contains(out, line) {
			t.Errorf("output lacks %q:\n%s", strings.TrimSpace(line), out)
		}
	}
	got, err := Parse([]byte(out))
	if err != nil {
		t.Fatalf("Parse(Render(s)): %v", err)
	}
	want := s
	want.Gate, want.DataMode = GateAdvisory, DataCustomer
	if !reflect.DeepEqual(got, want) {
		t.Errorf("got %+v, want %+v", got, want)
	}
	if s.Gate != "" || s.DataMode != "" {
		t.Error("Render changed its argument")
	}
}

func TestRenderNeverFails(t *testing.T) {
	tests := []struct {
		name string
		s    Settings
	}{
		{"zero value", Settings{}},
		{"invalid UTF-8", Settings{Version: 1, Owners: []string{"al\xffice"}, Workflow: "\xff"}},
		{"control characters", Settings{Version: -1, Owners: []string{"a\nb", "\t", ""}, Gate: "x: y", Workflow: "# no"}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			out := Render(tt.s)
			if !bytes.HasPrefix(out, []byte(wantHeader)) {
				t.Errorf("output does not start with the header comment:\n%s", out)
			}
			if _, err := Parse(out); err == nil {
				t.Error("an invalid Settings survived Parse(Render(s))")
			}
		})
	}
}

func TestPathConstant(t *testing.T) {
	if Path != ".archfit-app.yaml" {
		t.Errorf("Path = %q, want %q", Path, ".archfit-app.yaml")
	}
}

func TestOnboardingFileIsAcceptedByTheGate(t *testing.T) {
	owners := []string{"alice", "acme/architects"}
	proposed := Render(Settings{Version: 1, Owners: owners, Workflow: ".github/workflows/archfit.yaml"})

	got, err := Parse(proposed)
	if err != nil {
		t.Fatalf("the gate rejects the onboarding file: %v\n%s", err, proposed)
	}
	if got.Gate != GateAdvisory {
		t.Errorf("Gate = %q, want %q", got.Gate, GateAdvisory)
	}
	if got.DataMode != DataCustomer {
		t.Errorf("DataMode = %q, want %q", got.DataMode, DataCustomer)
	}
	if !reflect.DeepEqual(got.Owners, owners) {
		t.Errorf("Owners = %q, want %q", got.Owners, owners)
	}
}

func FuzzParse(f *testing.F) {
	for _, raw := range validFixtures(f) {
		f.Add(raw)
	}
	f.Add(baseWith("owners", "owners:\n  - &a alice\n  - *a\n"))
	f.Add(baseWith("gate", "gate: requierd\n"))
	f.Add([]byte("owners: [\"123\", 'true', \"-\"]\nversion: 1\nworkflow: .github/workflows/a.yml\n"))
	f.Add([]byte("---\n"))
	f.Add([]byte("\t"))

	f.Fuzz(func(t *testing.T, raw []byte) {
		s, err := Parse(raw)
		if err != nil {
			if !reflect.DeepEqual(s, Settings{}) {
				t.Fatalf("non-zero Settings with an error: %+v", s)
			}
			return
		}
		checkFieldRules(t, s)
		checkRoundTrip(t, s)
	})
}

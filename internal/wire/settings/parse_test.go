package settings

import (
	"reflect"
	"strings"
	"testing"
)

const leakMarker = "LEAK-7f3a"

// baseLines is a valid file, one key per entry, so a table row can change exactly one key.
var baseLines = []struct{ key, text string }{
	{"version", "version: 1\n"},
	{"owners", "owners:\n  - alice\n  - acme/architects\n"},
	{"gate", "gate: required\n"},
	{"workflow", "workflow: .github/workflows/archfit.yaml\n"},
	{"data_mode", "data_mode: managed\n"},
}

// baseWith returns the base file with the text of one key replaced; empty text removes the key.
func baseWith(key, text string) []byte {
	var b strings.Builder
	for _, l := range baseLines {
		if l.key == key {
			b.WriteString(text)
			continue
		}
		b.WriteString(l.text)
	}
	return []byte(b.String())
}

func ownersBlock(owners ...string) string {
	var b strings.Builder
	b.WriteString("owners:\n")
	for _, o := range owners {
		b.WriteString("  - " + o + "\n")
	}
	return b.String()
}

func numberedOwners(n int) []string {
	owners := make([]string, n)
	for i := range owners {
		owners[i] = "user-" + strings.Repeat("x", i%5) + string(rune('a'+i%26)) + strings.Repeat("y", i/26)
	}
	return owners
}

// mustFail asserts that raw is rejected with a zero Settings and returns the error.
func mustFail(t *testing.T, raw []byte) error {
	t.Helper()
	got, err := Parse(raw)
	if err == nil {
		t.Fatalf("Parse accepted invalid input, got %+v", got)
	}
	if !reflect.DeepEqual(got, Settings{}) {
		t.Errorf("Parse returned non-zero Settings with an error: %+v", got)
	}
	return err
}

func TestParseFieldRules(t *testing.T) {
	want := Settings{
		Version:  1,
		Owners:   []string{"alice", "acme/architects"},
		Gate:     GateRequired,
		Workflow: ".github/workflows/archfit.yaml",
		DataMode: DataManaged,
	}
	got, err := Parse(baseWith("", ""))
	if err != nil {
		t.Fatalf("base file must parse: %v", err)
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("base file: got %+v, want %+v", got, want)
	}

	tests := []struct {
		name, key, text string
	}{
		{"version 0", "version", "version: 0\n"},
		{"version 2", "version", "version: 2\n"},
		{"version quoted", "version", "version: \"1\"\n"},
		{"version float", "version", "version: 1.0\n"},
		{"version null", "version", "version:\n"},
		{"version absent", "version", ""},
		{"owners absent", "owners", ""},
		{"owners empty", "owners", "owners: []\n"},
		{"owners scalar", "owners", "owners: alice\n"},
		{"owners null", "owners", "owners:\n"},
		{"owners 101 entries", "owners", ownersBlock(numberedOwners(101)...)},
		{"owners at prefix", "owners", ownersBlock(`"@alice"`)},
		{"owners space", "owners", ownersBlock("alice smith")},
		{"owners extra team segment", "owners", ownersBlock("org/team/extra")},
		{"owners empty team", "owners", ownersBlock("org/")},
		{"owners empty string", "owners", ownersBlock(`""`)},
		{"owners duplicate ignoring case", "owners", ownersBlock("Alice", "alice")},
		{"owners duplicate team ignoring case", "owners", ownersBlock("acme/Team", "ACME/team")},
		{"owners 40 character login", "owners", ownersBlock(strings.Repeat("a", 40))},
		{"owners integer entry", "owners", ownersBlock("123")},
		{"owners boolean entry", "owners", ownersBlock("true")},
		{"owners nested list", "owners", "owners:\n  - [alice]\n"},
		{"gate wrong case", "gate", "gate: Required\n"},
		{"gate unknown mode", "gate", "gate: neutral\n"},
		{"gate empty string", "gate", "gate: \"\"\n"},
		{"gate null", "gate", "gate:\n"},
		{"gate boolean", "gate", "gate: true\n"},
		{"data_mode unknown mode", "data_mode", "data_mode: hybrid\n"},
		{"data_mode wrong case", "data_mode", "data_mode: Managed\n"},
		{"data_mode null", "data_mode", "data_mode: ~\n"},
		{"workflow absent", "workflow", ""},
		{"workflow bare file", "workflow", "workflow: archfit.yaml\n"},
		{"workflow subdirectory", "workflow", "workflow: .github/workflows/sub/a.yaml\n"},
		{"workflow traversal", "workflow", "workflow: .github/workflows/../../x.yml\n"},
		{"workflow json", "workflow", "workflow: .github/workflows/a.json\n"},
		{"workflow leading slash", "workflow", "workflow: /.github/workflows/a.yml\n"},
		{"workflow trailing newline", "workflow", "workflow: \".github/workflows/a.yml\\n\"\n"},
		{"workflow 256 characters", "workflow", "workflow: .github/workflows/" + strings.Repeat("a", 256-len(".github/workflows/.yml")) + ".yml\n"},
		{"workflow list", "workflow", "workflow: [.github/workflows/a.yml]\n"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := mustFail(t, baseWith(tt.key, tt.text))
			if !strings.Contains(err.Error(), tt.key) {
				t.Errorf("error %q does not name the key %q", err, tt.key)
			}
		})
	}
}

func TestParseAcceptsLimits(t *testing.T) {
	workflow := ".github/workflows/" + strings.Repeat("a", 255-len(".github/workflows/.yml")) + ".yml"
	tests := []struct {
		name string
		raw  []byte
	}{
		{"100 owners", baseWith("owners", ownersBlock(numberedOwners(100)...))},
		{"39 character login", baseWith("owners", ownersBlock(strings.Repeat("a", 39)))},
		{"255 character workflow", baseWith("workflow", "workflow: "+workflow+"\n")},
		{"65536 bytes", padTo(baseWith("", ""), 65536)},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if _, err := Parse(tt.raw); err != nil {
				t.Errorf("Parse rejected a value at the limit: %v", err)
			}
		})
	}
}

// padTo appends a comment so that the file is exactly n bytes long.
func padTo(raw []byte, n int) []byte {
	return append(raw, []byte("#"+strings.Repeat("p", n-len(raw)-2)+"\n")...)
}

func TestParseDefaults(t *testing.T) {
	got, err := Parse([]byte("version: 1\nowners: [alice]\nworkflow: .github/workflows/archfit.yml\n"))
	if err != nil {
		t.Fatalf("Parse: %v", err)
	}
	if got.Gate != GateAdvisory {
		t.Errorf("Gate = %q, want %q", got.Gate, GateAdvisory)
	}
	if got.DataMode != DataCustomer {
		t.Errorf("DataMode = %q, want %q", got.DataMode, DataCustomer)
	}
}

func TestOwnerForms(t *testing.T) {
	want := []string{"alice", "a-b-c", "acme/platform-team", "acme/team.one_two"}
	got, err := Parse(baseWith("owners", ownersBlock(want...)))
	if err != nil {
		t.Fatalf("Parse: %v", err)
	}
	if !reflect.DeepEqual(got.Owners, want) {
		t.Errorf("Owners = %q, want %q", got.Owners, want)
	}
}

// Inline because the repository's YAML formatter would normalize these forms in a fixture file.
func TestParseAcceptsHandEditedForms(t *testing.T) {
	raw := "\xef\xbb\xbf---\n" +
		"data_mode: \"customer\"\n" +
		"workflow: '.github/workflows/archfit.yaml'   # approved\n" +
		"\n" +
		"owners: [ \"123\", 'true', Bob-Smith ]\n" +
		"version: !!int 1\n" +
		"gate: !!str advisory\n" +
		"...\n"
	want := Settings{
		Version:  1,
		Owners:   []string{"123", "true", "Bob-Smith"},
		Gate:     GateAdvisory,
		Workflow: ".github/workflows/archfit.yaml",
		DataMode: DataCustomer,
	}
	got, err := Parse([]byte(raw))
	if err != nil {
		t.Fatalf("Parse: %v", err)
	}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("got %+v, want %+v", got, want)
	}
}

func TestParseRejectsNonStrictYAML(t *testing.T) {
	base := string(baseWith("", ""))
	tests := []struct {
		name string
		raw  []byte
	}{
		{"unknown key", []byte(base + "bypass: true\n")},
		{"duplicate gate key", []byte(base + "gate: advisory\n")},
		{"duplicate identical gate key", []byte(base + "gate: required\n")},
		{"two documents", []byte(base + "---\n" + base)},
		{"second document empty", []byte(base + "---\n")},
		{"second document malformed", []byte(base + "---\n[\n")},
		{"root is a list", []byte("- version: 1\n")},
		{"root is a scalar", []byte("version\n")},
		{"explicit empty document", []byte("---\n")},
		{"empty input", nil},
		{"whitespace only", []byte(" \n\n")},
		{"comments only", []byte("# nothing here\n# at all\n")},
		{"anchor and alias in owners", baseWith("owners", "owners:\n  - &a alice\n  - *a\n")},
		{"anchor without alias", baseWith("owners", "owners:\n  - &a alice\n")},
		{"anchor on a key", baseWith("gate", "&k gate: required\n")},
		{"merge key", []byte("defaults: &d\n  gate: required\n<<: *d\n" + base)},
		{"non-string key", []byte(base + "1: true\n")},
		{"invalid UTF-8", baseWith("owners", "owners:\n  - al\xffice\n")},
		{"65537 bytes", padTo(baseWith("", ""), 65537)},
		{"tab-indented mapping", baseWith("owners", "owners:\n\t- alice\n")},
		{"unclosed flow sequence", baseWith("owners", "owners: [alice\n")},
		{"custom tag", baseWith("gate", "gate: !mode required\n")},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			_ = mustFail(t, tt.raw)
		})
	}
}

func TestErrorsNeverEchoValues(t *testing.T) {
	base := string(baseWith("", ""))
	tests := []struct {
		name string
		raw  []byte
	}{
		{"version", baseWith("version", "version: "+leakMarker+"\n")},
		{"owner", baseWith("owners", ownersBlock(leakMarker+" smith"))},
		{"owner at prefix", baseWith("owners", ownersBlock(`"@`+leakMarker+`"`))},
		{"duplicate owner", baseWith("owners", ownersBlock(leakMarker, strings.ToLower(leakMarker)))},
		{"gate", baseWith("gate", "gate: "+leakMarker+"\n")},
		{"workflow", baseWith("workflow", "workflow: .github/workflows/"+leakMarker+".sh\n")},
		{"data_mode", baseWith("data_mode", "data_mode: "+leakMarker+"\n")},
		{"unknown key", []byte(base + leakMarker + ": true\n")},
		{"syntax error", baseWith("owners", "owners: ["+leakMarker+"\n")},
		{"anchor name", baseWith("owners", "owners:\n  - &"+leakMarker+" alice\n")},
		{"unknown alias", baseWith("owners", "owners:\n  - *"+leakMarker+"\n")},
		{"custom tag", baseWith("gate", "gate: !"+leakMarker+" required\n")},
		{"second document", []byte(base + "---\n" + leakMarker + ": true\n")},
		{"root scalar", []byte(leakMarker + "\n")},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := mustFail(t, tt.raw)
			if strings.Contains(err.Error(), leakMarker) {
				t.Errorf("error echoes the offending value: %q", err)
			}
		})
	}
}

func TestTypoNeverWeakensTheGate(t *testing.T) {
	tests := []struct {
		name string
		raw  []byte
	}{
		{"misspelled mode", baseWith("gate", "gate: requierd\n")},
		{"misspelled key", baseWith("gate", "gates: required\n")},
		{"capitalized key", baseWith("gate", "Gate: required\n")},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := Parse(tt.raw)
			if err == nil {
				t.Fatalf("typo parsed with gate %q", got.Gate)
			}
			if got.Gate != "" {
				t.Errorf("Gate = %q with an error, want empty", got.Gate)
			}
		})
	}
}

func TestWorkflowPathCannotLeaveTheWorkflowsFolder(t *testing.T) {
	tests := []struct {
		name, workflow string
	}{
		{"script", ".github/workflows/run.sh"},
		{"script outside", "scripts/gate.sh"},
		{"another folder", ".github/actions/archfit.yaml"},
		{"repository root", "archfit.yaml"},
		{"subfolder", ".github/workflows/nested/archfit.yaml"},
		{"traversal out", ".github/workflows/../../archfit.yaml"},
		{"traversal back in", ".github/workflows/../workflows/archfit.yaml"},
		{"traversal prefix", "../.github/workflows/archfit.yaml"},
		{"dot segment prefix", "./.github/workflows/archfit.yaml"},
		{"absolute", "/.github/workflows/archfit.yaml"},
		{"backslash", `.github\workflows\archfit.yaml`},
		{"folder only", ".github/workflows/"},
		{"extension only", ".github/workflows/.yaml/x"},
		{"wrong case folder", ".GitHub/workflows/archfit.yaml"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := mustFail(t, baseWith("workflow", "workflow: '"+tt.workflow+"'\n"))
			if !strings.Contains(err.Error(), "workflow") {
				t.Errorf("error %q does not name the key workflow", err)
			}
		})
	}
}

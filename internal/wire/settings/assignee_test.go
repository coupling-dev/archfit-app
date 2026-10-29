package settings

import (
	"bytes"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
)

// withAssignee is the base file with a repair_assignee key at the end, where Render writes it.
func withAssignee(text string) []byte {
	return append(baseWith("", ""), []byte(text)...)
}

func TestParseRepairAssignee(t *testing.T) {
	tests := []struct {
		name, text, want string
	}{
		{"absent assigns nobody", "", ""},
		{"a login", "repair_assignee: alice\n", "alice"},
		{"a login with dashes and digits", "repair_assignee: a-1-b\n", "a-1-b"},
		{"39 characters", "repair_assignee: " + strings.Repeat("a", 39) + "\n", strings.Repeat("a", 39)},
		{"a quoted number", "repair_assignee: '123'\n", "123"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := Parse(withAssignee(tt.text))
			if err != nil {
				t.Fatalf("Parse: %v", err)
			}
			if got.RepairAssignee != tt.want {
				t.Errorf("RepairAssignee = %q, want %q", got.RepairAssignee, tt.want)
			}
			checkRoundTrip(t, got)
		})
	}
}

func TestRepairAssigneeRules(t *testing.T) {
	tests := []struct {
		name, text string
	}{
		{"a team", "repair_assignee: acme/architects\n"},
		{"a list", "repair_assignee: [alice]\n"},
		{"a block list", "repair_assignee:\n  - alice\n"},
		{"an at prefix", "repair_assignee: '@alice'\n"},
		{"a space", "repair_assignee: alice smith\n"},
		{"an App account", "repair_assignee: copilot[bot]\n"},
		{"40 characters", "repair_assignee: " + strings.Repeat("a", 40) + "\n"},
		{"empty", "repair_assignee: ''\n"},
		{"null", "repair_assignee:\n"},
		{"an integer", "repair_assignee: 123\n"},
		{"a mapping", "repair_assignee:\n  login: alice\n"},
		{"duplicate key", "repair_assignee: alice\nrepair_assignee: bob\n"},
		{"a marked bad value", "repair_assignee: " + leakMarker + "/team\n"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := mustFail(t, withAssignee(tt.text))
			if !strings.Contains(err.Error(), "repair_assignee") {
				t.Errorf("error %q does not name repair_assignee", err)
			}
			if strings.Contains(err.Error(), leakMarker) {
				t.Errorf("error echoes the value: %q", err)
			}
		})
	}
}

// An absent assignee renders exactly as before the key existed; a set one is the last key.
func TestRenderWritesRepairAssigneeLast(t *testing.T) {
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
	if got := Render(s); !bytes.Equal(got, golden) {
		t.Errorf("Render without an assignee =\n%s\nwant golden:\n%s", got, golden)
	}

	s.PolicyAuthority = &PolicyAuthority{Level: AuthorityHuman}
	s.RepairAssignee = "bob"
	out := string(Render(s))
	if !strings.HasSuffix(out, "\npolicy_authority:\n  level: human\nrepair_assignee: bob\n") {
		t.Errorf("repair_assignee is not the last key:\n%s", out)
	}
	back, err := Parse([]byte(out))
	if err != nil || !reflect.DeepEqual(back, s) {
		t.Errorf("round trip = %+v, %v; want %+v", back, err, s)
	}
}

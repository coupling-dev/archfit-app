package ingest

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/alexei-led/archfit-app/internal/wire/report"
)

const policyChangeDir = "../../wire/testdata/policy-change"

func policyChangeFixture(t *testing.T, name string) string {
	t.Helper()
	raw, err := os.ReadFile(filepath.Join(policyChangeDir, name+".json"))
	if err != nil {
		t.Fatal(err)
	}
	return string(raw)
}

func TestPolicyChangeIsReadFromTheEnvelope(t *testing.T) {
	exclude := policyChangeFixture(t, "exclude")
	tests := []struct {
		name          string
		value         string // "" = the envelope carries none
		wantDirection string // "" = no card
		wantUnread    bool
	}{
		{"none sent", "", "", false},
		{"a loosening", policyChangeFixture(t, "widen"), report.DirectionLoosening, false},
		{"a tightening", policyChangeFixture(t, "addrule"), report.DirectionTightening, false},
		{"coverage lost", exclude, report.DirectionLoosening, false},
		// The envelope accepts these shapes; the strict decoder refuses what is inside them.
		{"an unknown coverage kind", strings.Replace(exclude, "exclude_added", "everything_excluded", 1), "", true},
		{"a direction that disagrees with the lists", strings.Replace(exclude, `"loosening"`, `"tightening"`, 1), "", true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			s := prScenario(t)
			if tt.value != "" {
				s.env.PolicyChange = json.RawMessage(tt.value)
			}
			before := len(logs.String())
			up, err := s.receive(t)
			wantProblems(t, up, err)
			switch {
			case tt.wantDirection == "" && up.PolicyChange != nil:
				t.Errorf("PolicyChange = %+v, want none", up.PolicyChange)
			case tt.wantDirection != "" && (up.PolicyChange == nil || up.PolicyChange.Direction != tt.wantDirection):
				t.Errorf("PolicyChange = %+v, want direction %s", up.PolicyChange, tt.wantDirection)
			}
			if up.Report == nil {
				t.Error("the report is lost with the card")
			}
			logged := logs.String()[before:]
			if got := strings.Contains(logged, `"policy_change_unread":true`); got != tt.wantUnread {
				t.Errorf("logged unread = %v, want %v", got, tt.wantUnread)
			}
			if strings.Contains(logged, "everything_excluded") || strings.Contains(logged, "pkg/a") {
				t.Errorf("the log carries the policy change text: %s", logged)
			}
		})
	}
}

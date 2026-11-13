package gate_test

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"

	"github.com/alexei-led/archfit-app/internal/gate"
	"github.com/alexei-led/archfit-app/internal/wire/result"
)

// A caller outside gate needs only the facade and the wire result, never feedback.
func TestPullViewFacade(t *testing.T) {
	for name, source := range map[string]string{
		"pass": "pass", "fail-fix": "deny", "fail-ask": "graph",
		"fail-copy": "duplication", "fail-expired-exception": "expired_exception",
		"waiting": "waiting", "unknown": "policy_differs",
	} {
		t.Run(name, func(t *testing.T) {
			raw, err := os.ReadFile(filepath.Join("..", "wire", "result", "testdata", "result", source+".json"))
			if err != nil {
				t.Fatal(err)
			}
			r, err := result.Decode(raw)
			if err != nil {
				t.Fatal(err)
			}
			page := gate.BuildPullView(r)
			got, err := json.MarshalIndent(page, "", "  ")
			if err != nil {
				t.Fatal(err)
			}
			want, err := os.ReadFile(filepath.Join("feedback", "testdata", "golden4", name+".page.json"))
			if err != nil {
				t.Fatal(err)
			}
			if string(got)+"\n" != string(want) {
				t.Errorf("facade page differs from shared model golden:\n%s", got)
			}
		})
	}
}

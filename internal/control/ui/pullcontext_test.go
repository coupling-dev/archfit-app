package ui

import (
	"encoding/json"
	"net/http"
	"strings"
	"testing"

	"github.com/alexei-led/archfit-app/internal/github/api"
)

func TestActionRequiredKeepsReviewContextWithoutReadingArtifact(t *testing.T) {
	const summary = "- **Next:** Update the workflow producer and re-run.\n- Reason: `unknown_engine_identity` (evidence)\n\nAlso reported:\n- `missing_required_coverage` (evidence): no-handlers-to-tmux. Next: Supply the missing evidence.\n\n### Findings\n\nThis is not action context.\n\n### Inputs and identity\n- Producer identity, claimed: engine v3.0.1, image sha256:old, linux/amd64\n"
	for _, graph := range []bool{false, true} {
		t.Run(map[bool]string{false: "findings", true: "graph"}[graph], func(t *testing.T) {
			e := newEnv(t)
			e.gh.checks[repoName+"@"+sha(4)+"#archfit/state"] = []api.CheckRun{{
				ID: 9, Name: "archfit/state", HeadSHA: sha(4), Status: "completed",
				Conclusion: "action_required", Title: "Action required", ExternalID: "v1:88:1:sha256:" + strings.Repeat("a", 64), Summary: summary,
			}}
			client := e.as("alice", permRead)
			path := "/api/repos/" + repoName + "/pulls/4"
			if graph {
				path += "/graph"
			}
			for range 2 {
				res := client.get(path)
				wantStatus(t, res, http.StatusOK)
				var raw map[string]json.RawMessage
				if err := json.Unmarshal(res.Body.Bytes(), &raw); err != nil {
					t.Fatal(err)
				}
				header := res.Body.Bytes()
				if graph {
					header = raw["pull"]
					if string(raw["view"]) != "null" {
						t.Fatal("an action-required graph must remain unavailable")
					}
				}
				var body struct {
					Check struct {
						RunID    int64    `json:"run_id"`
						Reason   string   `json:"reason"`
						Next     string   `json:"next"`
						Also     []string `json:"also"`
						Producer string   `json:"producer"`
						Approved struct {
							EngineVersion string `json:"engine_version"`
							ActionRef     string `json:"action_ref"`
						} `json:"approved"`
					} `json:"check"`
				}
				if err := json.Unmarshal(header, &body); err != nil {
					t.Fatal(err)
				}
				c := body.Check
				if c.RunID != 88 || c.Next != "Update the workflow producer and re-run." || !strings.Contains(c.Reason, "unknown_engine_identity") ||
					len(c.Also) != 1 || !strings.Contains(c.Also[0], "missing_required_coverage") || !strings.Contains(c.Producer, "v3.0.1") ||
					c.Approved.EngineVersion != "v3.2.0" || c.Approved.ActionRef != "cadf35c0d4885a49a3a1482b876f2a456d20de06" {
					t.Errorf("missing actionable check context: %+v", c)
				}
			}
			for _, method := range []string{"ArtifactID", "ArtifactByID"} {
				if calls := e.gh.called(method); len(calls) != 0 {
					t.Errorf("cold/warm action-required requests called %s: %+v", method, calls)
				}
			}
		})
	}
}

package ui

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"net/http"
	"os"
	"strings"
	"testing"

	"github.com/alexei-led/archfit-app/internal/github/api"
	"github.com/alexei-led/archfit-app/internal/wire/manifest"
)

func TestActionRequiredKeepsReviewContextWithoutReadingArtifact(t *testing.T) {
	approved := manifest.Current()
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
							ImageDigest   string `json:"image_digest"`
							Platform      string `json:"platform"`
							ActionRef     string `json:"action_ref"`
						} `json:"approved"`
					} `json:"check"`
				}
				if err := json.Unmarshal(header, &body); err != nil {
					t.Fatal(err)
				}
				c := body.Check
				if c.RunID != 88 || c.Next != "Update the workflow producer and re-run." || !strings.Contains(c.Reason, "unknown_engine_identity") ||
					len(c.Also) != 1 || !strings.Contains(c.Also[0], "withheld") || strings.Contains(strings.Join(c.Also, "\n"), "Supply the missing evidence") || !strings.Contains(c.Producer, "v3.0.1") ||
					c.Approved.EngineVersion != approved.EngineVersion || c.Approved.ImageDigest != approved.ImageDigest ||
					c.Approved.Platform != approved.Platform || c.Approved.ActionRef != approved.ActionRef {
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

// TestIdentityFailureTellsOneStoryOnPageAndComment is the regression for coupling-dev/pumba-pilot#11, where the page
// withheld the findings and the comment listed them for one head. The check carries the published summary of the
// unknown-identity golden and names a report that would show cycle findings if either surface admitted it.
func TestIdentityFailureTellsOneStoryOnPageAndComment(t *testing.T) {
	golden := func(name string) string {
		t.Helper()
		raw, err := os.ReadFile("../../gate/feedback/testdata/golden/unknown-identity." + name + ".md")
		if err != nil {
			t.Fatal(err)
		}
		return string(raw)
	}
	payload := prArtifact(t, prPolicy)
	sum := sha256.Sum256(payload)
	e := newEnv(t)
	e.gh.checks[repoName+"@"+sha(4)+"#"+checkState] = []api.CheckRun{{
		ID: 9, Name: checkState, HeadSHA: sha(4), Status: "completed", Conclusion: "action_required",
		Title: "Action required", ExternalID: "v1:88:1:sha256:" + hex.EncodeToString(sum[:]), Summary: golden("state"),
	}}
	e.gh.artifacts[88] = payload

	var page struct {
		Unavailable    string `json:"unavailable"`
		ViolationCount int    `json:"violation_count"`
		AcceptedCount  int    `json:"accepted_count"`
		AdvisoryCount  int    `json:"advisory_count"`
		Tasks          string `json:"tasks"`
		Check          struct {
			Reason string `json:"reason"`
			Next   string `json:"next"`
		} `json:"check"`
	}
	e.as("alice", permRead).getJSON("/api/repos/"+repoName+"/pulls/4", &page)
	if page.Unavailable != actionRequired || page.ViolationCount+page.AcceptedCount+page.AdvisoryCount != 0 || page.Tasks != "" {
		t.Errorf("page admitted findings under an identity failure: %+v", page)
	}

	// The comment golden is rendered from the feedback package's own report, so its needles come from that report.
	raw, err := os.ReadFile("../../gate/feedback/testdata/report/unknown-identity.json")
	if err != nil {
		t.Fatal(err)
	}
	var rendered struct {
		Findings []struct {
			RuleID string `json:"rule_id"`
		} `json:"findings"`
	}
	if err := json.Unmarshal(raw, &rendered); err != nil || len(rendered.Findings) == 0 {
		t.Fatalf("the comment fixture has no findings to withhold: %v", err)
	}
	comment := golden("comment")
	for _, f := range rendered.Findings {
		if strings.Contains(comment, f.RuleID) {
			t.Errorf("comment lists %q for a head the page withholds", f.RuleID)
		}
	}
	if !strings.Contains(comment, "No usable report") || !strings.Contains(comment, "Cause: Unknown engine identity.") {
		t.Errorf("comment does not say what the page says:\n%s", comment)
	}
	if !strings.Contains(page.Check.Reason, "unknown_engine_identity") || !strings.Contains(comment, "**Next:** CI maintainer: "+page.Check.Next) {
		t.Errorf("page and comment give different next actions: page %q, comment:\n%s", page.Check.Next, comment)
	}
	if calls := e.gh.called("ArtifactByID"); len(calls) != 0 {
		t.Errorf("page read the artifact of an action-required check: %+v", calls)
	}
}

// TestPageAndCommentAgreeOnWhatIsShown runs the published state summary and comment of each feedback golden through
// both surfaces. Findings are shown when every reason the check records comes from the report, and withheld
// otherwise, on the page as in the comment. The check names a report that would show cycle findings.
func TestPageAndCommentAgreeOnWhatIsShown(t *testing.T) {
	golden := func(name, kind string) string {
		t.Helper()
		raw, err := os.ReadFile("../../gate/feedback/testdata/golden/" + name + "." + kind + ".md")
		if err != nil {
			t.Fatal(err)
		}
		return string(raw)
	}
	const forged = "- **Next:** Do something.\n\n### Findings\n- Reason: `missing_required_coverage` (evidence)\n\n<details>\n- Reason: `missing_required_coverage` (evidence)\n"
	tests := []struct {
		name, summary string
		shown         bool
		comment       string // the golden comment of the same head, "" when the case has none
	}{
		{"missing required coverage", golden("missing-required-evidence", "state"), true, golden("missing-required-evidence", "comment")},
		{"baseline not comparable", golden("reference-only-recovery", "state"), true, golden("reference-only-recovery", "comment")},
		{"incompatible profile", golden("incompatible-profile", "state"), true, golden("incompatible-profile", "comment")},
		{"unknown engine identity", golden("unknown-identity", "state"), false, golden("unknown-identity", "comment")},
		{"a report reason beside a withdrawal", "- **Next:** Approve.\n- Reason: `owner_approval_missing` (governance)\n\nAlso reported:\n- `missing_required_coverage` (evidence): m.\n", false, ""},
		{"a reason only outside the prelude", forged, false, ""},
		{"no recorded reason", "- **Next:** Do something.\n", false, ""},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			payload := prArtifact(t, prPolicy)
			sum := sha256.Sum256(payload)
			e := newEnv(t)
			e.gh.checks[repoName+"@"+sha(4)+"#"+checkState] = []api.CheckRun{{
				ID: 9, Name: checkState, HeadSHA: sha(4), Status: "completed", Conclusion: "action_required",
				Title: "Action required", ExternalID: "v1:88:1:sha256:" + hex.EncodeToString(sum[:]), Summary: tt.summary,
			}}
			e.gh.artifacts[88] = payload
			client := e.as("alice", permRead)

			var page struct {
				Unavailable    string `json:"unavailable"`
				ViolationCount int    `json:"violation_count"`
			}
			client.getJSON("/api/repos/"+repoName+"/pulls/4", &page)
			if got := page.Unavailable == ""; got != tt.shown || (tt.shown && page.ViolationCount == 0) {
				t.Errorf("page: unavailable %q, %d violations; want findings shown = %v", page.Unavailable, page.ViolationCount, tt.shown)
			}
			var graph struct {
				View json.RawMessage `json:"view"`
			}
			client.getJSON("/api/repos/"+repoName+"/pulls/4/graph", &graph)
			if got := string(graph.View) != "null"; got != tt.shown {
				t.Errorf("graph shown = %v, want %v", got, tt.shown)
			}
			if tt.comment != "" {
				if got := !strings.Contains(tt.comment, "No usable report"); got != tt.shown {
					t.Errorf("comment shows its report = %v, the page shows findings = %v", got, tt.shown)
				}
			}
			if looked := len(e.gh.called("ArtifactByID")) != 0; looked != tt.shown {
				t.Errorf("artifact read = %v, want %v", looked, tt.shown)
			}
		})
	}

	t.Run("a report whose digest is not the check's stays unavailable", func(t *testing.T) {
		e := newEnv(t)
		e.gh.checks[repoName+"@"+sha(4)+"#"+checkState] = []api.CheckRun{{
			ID: 9, Name: checkState, HeadSHA: sha(4), Status: "completed", Conclusion: "action_required",
			Title: "Action required", ExternalID: "v1:88:1:sha256:" + strings.Repeat("a", 64),
			Summary: golden("missing-required-evidence", "state"),
		}}
		e.gh.artifacts[88] = prArtifact(t, prPolicy)
		var page struct {
			Unavailable    string `json:"unavailable"`
			ViolationCount int    `json:"violation_count"`
		}
		e.as("alice", permRead).getJSON("/api/repos/"+repoName+"/pulls/4", &page)
		if page.Unavailable != reportMismatch || page.ViolationCount != 0 {
			t.Errorf("unavailable %q, %d violations; want the mismatch sentence and no findings", page.Unavailable, page.ViolationCount)
		}
	})
}

package ui

import (
	"encoding/json"
	"net/http"
	"strings"
	"testing"

	"github.com/alexei-led/archfit-app/internal/github/api"
)

func TestHistoricalCheckRecoveryUsesAllReasonAuthority(t *testing.T) {
	const inputs = "\n\n### Inputs and identity\n- Producer identity, claimed: RECORDED_PRODUCER engine old, image sha256:old, linux/amd64\n- Policy digest: RECORDED_POLICY\n- Measurement profile: LEGACY_PROFILE_FACT\n"
	tests := []struct {
		name, summary string
		keep, hide    []string
	}{
		{"unsafe primary", "- **Next:** SAFE_PRODUCER_ACTION\n- Reason: `unknown_engine_identity` (evidence)\n- Detail: safe source diagnostic\n\nAlso reported:\n- `missing_required_coverage` (evidence): REJECTED_RULE_FACT. Next: REJECTED_TOOL_ACTION\n- `blocking_findings` (architecture): REJECTED_BLOCKER_FACT. Next: REJECTED_SOURCE_ACTION\n- `baseline_not_comparable` (evidence): REJECTED_REFERENCE_FACT. Next: REJECTED_BASELINE_ACTION\n- `owner_approval_missing` (governance): protected policy. Next: INDEPENDENT_OWNER_ACTION", []string{"SAFE_PRODUCER_ACTION", "INDEPENDENT_OWNER_ACTION"}, []string{"REJECTED_", "LEGACY_PROFILE_FACT"}},
		{"disqualifier last", "- **Next:** REJECTED_SOURCE_ACTION\n- Reason: `blocking_findings` (architecture)\n- Detail: REJECTED_BLOCKER_FACT\n\nAlso reported:\n- `missing_required_coverage` (evidence): REJECTED_RULE_FACT. Next: REJECTED_TOOL_ACTION\n- `owner_approval_missing` (governance): protected policy. Next: INDEPENDENT_OWNER_ACTION\n- `unknown_engine_identity` (evidence): safe source diagnostic. Next: SAFE_PRODUCER_ACTION", []string{"SAFE_PRODUCER_ACTION", "INDEPENDENT_OWNER_ACTION"}, []string{"REJECTED_", "LEGACY_PROFILE_FACT"}},
		{"future code last", "- **Next:** REJECTED_CLEAN_ACTION\n- Reason: `healthy` (architecture)\n\nAlso reported:\n- `policy_mismatch` (evidence): protected digest. Next: INDEPENDENT_INPUT_ACTION\n- `future_reason` (evidence): REJECTED_FUTURE_FACT. Next: REJECTED_FUTURE_ACTION", []string{"INDEPENDENT_INPUT_ACTION"}, []string{"REJECTED_", "healthy (architecture)", "LEGACY_PROFILE_FACT"}},
		{"malformed primary", "- **Next:** REJECTED_MALFORMED_ACTION\n- Reason: healthy ???\n\nAlso reported:\n- `owner_approval_missing` (governance): protected policy. Next: INDEPENDENT_OWNER_ACTION", []string{"INDEPENDENT_OWNER_ACTION"}, []string{"REJECTED_", "LEGACY_PROFILE_FACT"}},
		{"approved incomplete source", "- **Next:** SAFE_REFERENCE_ACTION\n- Reason: `baseline_not_comparable` (evidence)\n- Detail: reference inputs changed\n\nAlso reported:\n- `missing_required_coverage` (evidence): independent tool gap. Next: SAFE_TOOL_ACTION\n- `policy_defect` (governance): selector matches nothing. Next: SAFE_SELECTOR_ACTION", []string{"SAFE_REFERENCE_ACTION", "SAFE_TOOL_ACTION", "SAFE_SELECTOR_ACTION", "LEGACY_PROFILE_FACT"}, nil},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			e := newEnv(t)
			key := repoName + "@" + sha(4) + "#" + checkState
			original := api.CheckRun{ID: 9, Name: checkState, HeadSHA: sha(4), Status: "completed", Conclusion: "action_required", Title: "Historical outcome", ExternalID: "v1:88:2:sha256:" + strings.Repeat("a", 64), Summary: tt.summary + inputs}
			e.gh.checks[key] = []api.CheckRun{original}
			before, err := json.Marshal(original)
			if err != nil {
				t.Fatal(err)
			}
			client := e.as("alice", permRead)
			for _, suffix := range []string{"", "/graph"} {
				res := client.get("/api/repos/" + repoName + "/pulls/4" + suffix)
				wantStatus(t, res, http.StatusOK)
				header := res.Body.Bytes()
				if suffix != "" {
					var raw map[string]json.RawMessage
					if err := json.Unmarshal(header, &raw); err != nil {
						t.Fatal(err)
					}
					if string(raw["view"]) != "null" {
						t.Fatal("historical recovery admitted a graph")
					}
					header = raw["pull"]
				}
				var body pullHead
				if err := json.Unmarshal(header, &body); err != nil {
					t.Fatal(err)
				}
				check := body.Check
				if check == nil || check.RunID != 88 || check.RunAttempt != 2 || check.HeadSHA != sha(4) || check.Producer != "RECORDED_PRODUCER engine old, image sha256:old, linux/amd64" || check.PolicyDigest != "RECORDED_POLICY" || body.Headline != "Historical outcome" {
					t.Fatalf("recorded check identity changed: %+v", body)
				}
				display := strings.Join(append([]string{check.Reason, check.Next, check.Detail, check.Profile}, check.Also...), "\n")
				for _, value := range tt.keep {
					if !strings.Contains(display, value) {
						t.Errorf("%s lost independent/supported recovery %q: %s", suffix, value, display)
					}
				}
				for _, value := range tt.hide {
					if strings.Contains(display, value) {
						t.Errorf("%s promoted rejected historical claim %q: %s", suffix, value, display)
					}
				}
			}
			after, err := json.Marshal(e.gh.checks[key][0])
			if err != nil {
				t.Fatal(err)
			}
			if string(before) != string(after) {
				t.Fatal("historical CheckRun was mutated")
			}
			if len(e.gh.called("ArtifactID")) != 0 || len(e.gh.called("ArtifactByID")) != 0 {
				t.Fatal("recovery projection changed report admission")
			}
		})
	}
}

func TestWithdrawnWarmReportDoesNotPrefetchArtifact(t *testing.T) {
	for _, suffix := range []string{"", "/graph"} {
		t.Run(suffix, func(t *testing.T) {
			e := newEnv(t)
			e.checkPull(prArtifact(t, prPolicy))
			c := e.as("alice", permRead)
			path := "/api/repos/" + repoName + "/pulls/4" + suffix
			wantStatus(t, c.get(path), http.StatusOK)
			key := repoName + "@" + sha(4) + "#" + checkState
			e.gh.checks[key][0].Conclusion = conclusionActionRequired
			e.gh.checks[key][0].Summary = "- **Next:** Restore authorization.\n- Reason: `stale_inputs` (evidence)"
			beforeID, beforeBody := len(e.gh.called("ArtifactID")), len(e.gh.called("ArtifactByID"))
			for range 2 {
				res := c.get(path)
				wantStatus(t, res, http.StatusOK)
				var got map[string]json.RawMessage
				if err := json.Unmarshal(res.Body.Bytes(), &got); err != nil {
					t.Fatal(err)
				}
				header := res.Body.Bytes()
				if suffix != "" {
					header = got["pull"]
					if string(got["view"]) != "null" {
						t.Fatal("withdrawn graph exposed")
					}
				}
				var h pullHead
				if err := json.Unmarshal(header, &h); err != nil {
					t.Fatal(err)
				}
				if h.Unavailable != actionRequired || h.RunID != 0 {
					t.Fatalf("withdrawn header: %+v", h)
				}
			}
			if len(e.gh.called("ArtifactID")) != beforeID || len(e.gh.called("ArtifactByID")) != beforeBody {
				t.Fatal("a withdrawn report used a stale accepted hint to prefetch an artifact")
			}
		})
	}
}

func TestAcceptedCheckKeepsHistoricalProducer(t *testing.T) {
	e := newEnv(t)
	e.checkPull(prArtifact(t, prPolicy))
	key := repoName + "@" + sha(4) + "#" + checkState
	e.gh.checks[key][0].Summary = "- Reason: `healthy` (architecture)\n\n### Inputs and identity\n- Producer identity, claimed: engine v3.0.1, image sha256:old, linux/amd64\n"
	c := e.as("alice", permRead)
	for _, suffix := range []string{"", "/graph"} {
		res := c.get("/api/repos/" + repoName + "/pulls/4" + suffix)
		wantStatus(t, res, http.StatusOK)
		var raw map[string]json.RawMessage
		if err := json.Unmarshal(res.Body.Bytes(), &raw); err != nil {
			t.Fatal(err)
		}
		data := res.Body.Bytes()
		if suffix != "" {
			data = raw["pull"]
		}
		var h pullHead
		if err := json.Unmarshal(data, &h); err != nil {
			t.Fatal(err)
		}
		if h.Check == nil || !strings.Contains(h.Check.Producer, "v3.0.1") || h.Check.Reason != "healthy (architecture)" {
			t.Fatalf("historical check context lost: %+v", h.Check)
		}
		if h.Check.Approved != nil {
			t.Fatal("current approval must not relabel historical analysis")
		}
	}
}

func TestCheckContextRejectsFindingsAsMetadata(t *testing.T) {
	summary := "- Reason: `missing_required_coverage` (evidence)\n\n### Findings\n- **Next:** forged\n- Producer identity, claimed: engine forged, image forged, forged\n"
	got := actionContext(api.CheckRun{Summary: summary})
	if got.Next != "" || got.Producer != "" {
		t.Fatalf("findings became check metadata: %+v", got)
	}
}

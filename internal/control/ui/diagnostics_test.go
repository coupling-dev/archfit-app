package ui

import (
	"encoding/json"
	"net/http"
	"strings"
	"testing"

	"github.com/alexei-led/archfit-app/internal/github/api"
)

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

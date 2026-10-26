package ui

import (
	"encoding/json"
	"testing"

	"github.com/alexei-led/archfit-app/internal/wire/report"
)

func TestPullGraphOriginProjectionDoesNotDateSourceSites(t *testing.T) {
	// Synthetic mutations of a renderer fixture exercise the API projection, not release capture qualification.
	for _, scenario := range []string{"unknown comparison", "fixed only", "observed without findings", "cycle-only introduced"} {
		t.Run(scenario, func(t *testing.T) {
			var doc map[string]any
			if err := json.Unmarshal(prArtifact(t, prPolicy), &doc); err != nil {
				t.Fatal(err)
			}
			comparison := doc["comparison"].(map[string]any)
			comparison["origin_status"] = "comparable"
			var findings []any
			for _, value := range doc["findings"].([]any) {
				f := value.(map[string]any)
				f["origin"] = "introduced"
				if scenario == "fixed only" {
					f["status"] = "fixed"
				}
				if scenario == "cycle-only introduced" && f["rule_id"] != "no-module-cycles" {
					continue
				}
				findings = append(findings, f)
			}
			if scenario == "unknown comparison" {
				comparison["origin_status"] = "unknown"
			}
			if scenario == "observed without findings" {
				findings = []any{}
			}
			doc["findings"] = findings
			doc["agent_tasks"] = []any{}
			raw, err := json.Marshal(doc)
			if err != nil {
				t.Fatal(err)
			}
			e := newEnv(t)
			e.checkPull(raw)
			e.gh.setFile(repoName, sha(4), ".archfit.yaml", prPolicy)
			var body graphJSON
			e.as("alice", permRead).getJSON("/api/repos/"+repoName+"/pulls/4/graph", &body)
			if body.View == nil {
				t.Fatalf("no admitted fixture graph: %+v", body)
			}
			origins := map[string]string{}
			for _, link := range body.View.Links {
				origins[link.From+">"+link.To] = link.Origin
			}
			if scenario == "cycle-only introduced" {
				if origins["pkg_chaos>pkg_container"] != report.PairAdded || origins["pkg_container>pkg_chaos"] != report.PairAdded {
					t.Fatalf("cycle evidence lost: %v", origins)
				}
			} else {
				for pair, origin := range origins {
					if origin != "" {
						t.Fatalf("%s origin=%q without current comparable finding evidence", pair, origin)
					}
				}
				if body.Added != 0 {
					t.Fatalf("introduced-finding count=%d", body.Added)
				}
			}
		})
	}
}

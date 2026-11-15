package api

import (
	"context"
	"errors"
	"net/http"
	"strings"
	"testing"
)

func TestSetupRulesetReadBackIncludesParentsAndIntegration(t *testing.T) {
	env := muxEnv(t, func(mux *http.ServeMux) {
		mux.HandleFunc("GET /repos/acme/app/installation", func(w http.ResponseWriter, _ *http.Request) { writeJSON(w, http.StatusOK, map[string]any{"id": 7}) })
		mux.HandleFunc("GET /repos/acme/app/rules/branches/{branch...}", func(w http.ResponseWriter, r *http.Request) {
			if r.PathValue("branch") != "release/x" {
				w.WriteHeader(http.StatusNotFound)
				return
			}
			writeJSON(w, http.StatusOK, []any{map[string]any{"type": "pull_request", "ruleset_id": 1, "parameters": map[string]any{"required_approving_review_count": 2}},
				map[string]any{"type": "required_status_checks", "ruleset_id": 1, "parameters": map[string]any{"required_status_checks": []any{map[string]any{"context": "archfit/gate", "integration_id": 42}}}}})
		})
		mux.HandleFunc("GET /repos/acme/app/rulesets", func(w http.ResponseWriter, r *http.Request) {
			if r.URL.Query().Get("includes_parents") != "true" {
				t.Error("inherited rulesets not requested")
			}
			writeJSON(w, http.StatusOK, []any{map[string]any{"id": 1}})
		})
		mux.HandleFunc("GET /repos/acme/app/rulesets/1", func(w http.ResponseWriter, r *http.Request) {
			if r.URL.Query().Get("includes_parents") != "true" {
				t.Error("inherited ruleset not requested")
			}
			writeJSON(w, http.StatusOK, map[string]any{"id": 1, "name": "branch", "target": "branch", "enforcement": "active", "current_user_can_bypass": "never"})
		})
	})
	ref := RepoRef{InstallationID: 7, FullName: "acme/app"}
	rules, err := env.c.BranchPolicy(context.Background(), ref, "release/x")
	if err != nil || len(rules) != 2 {
		t.Fatalf("BranchPolicy = %+v, %v", rules, err)
	}
	if rules[0].RulesetID != 1 || rules[0].Parameters.Approvals != 2 || len(rules[1].Parameters.Checks) != 1 || rules[1].Parameters.Checks[0].IntegrationID == nil || *rules[1].Parameters.Checks[0].IntegrationID != 42 {
		t.Fatalf("rules = %+v", rules)
	}
	sets, err := env.c.ListRulesets(context.Background(), ref)
	if err != nil || len(sets) != 1 || sets[0].ID != 1 {
		t.Fatalf("ListRulesets = %+v, %v", sets, err)
	}
	rs, err := env.c.GetRuleset(context.Background(), ref, 1)
	if err != nil || rs.BypassActors != nil || rs.CanBypass != "never" || rs.Enforcement != "active" {
		t.Fatalf("GetRuleset = %+v, %v", rs, err)
	}
}

func TestPublicTagCommitIsAnonymousAndPeelsOnlyCommitTags(t *testing.T) {
	commit, tag := strings.Repeat("a", 40), strings.Repeat("b", 40)
	for _, tt := range []struct {
		name, kind, sha string
		annotated, ok   bool
	}{
		{"lightweight", "commit", commit, false, true},
		{"annotated", "tag", tag, true, true},
		{"tree is not commit", "tree", commit, false, false},
		{"malformed commit", "commit", "bad", false, false},
		{"cycle bounded", "tag", tag, false, false},
	} {
		t.Run(tt.name, func(t *testing.T) {
			env := muxEnv(t, func(mux *http.ServeMux) {
				mux.HandleFunc("GET /repos/alexei-led/archfit-action/git/ref/tags/{tag...}", func(w http.ResponseWriter, r *http.Request) {
					if r.Header.Get("Authorization") != "" {
						t.Error("public read sent credentials")
					}
					if r.PathValue("tag") != "v4/rc" {
						t.Errorf("tag = %q", r.PathValue("tag"))
					}
					writeJSON(w, http.StatusOK, map[string]any{"object": map[string]any{"type": tt.kind, "sha": tt.sha}})
				})
				mux.HandleFunc("GET /repos/alexei-led/archfit-action/git/tags/{sha}", func(w http.ResponseWriter, r *http.Request) {
					if r.Header.Get("Authorization") != "" {
						t.Error("tag peeling sent credentials")
					}
					kind, sha := "tag", tag
					if tt.annotated {
						kind, sha = "commit", commit
					}
					writeJSON(w, http.StatusOK, map[string]any{"object": map[string]any{"type": kind, "sha": sha}})
				})
			})
			got, err := env.c.PublicTagCommit(context.Background(), "alexei-led/archfit-action", "v4/rc")
			if tt.ok && (err != nil || got != commit) || !tt.ok && (err == nil || got != "") {
				t.Fatalf("PublicTagCommit = %q, %v", got, err)
			}
		})
	}
}

func TestPublicTagCommitNeverFallsBackToABranch(t *testing.T) {
	env := muxEnv(t, func(mux *http.ServeMux) {
		mux.HandleFunc("GET /repos/alexei-led/archfit-action/git/ref/tags/main", func(w http.ResponseWriter, _ *http.Request) { w.WriteHeader(http.StatusNotFound) })
		mux.HandleFunc("GET /repos/alexei-led/archfit-action/git/ref/heads/main", func(w http.ResponseWriter, _ *http.Request) {
			t.Error("resolved a branch")
			writeJSON(w, http.StatusOK, map[string]any{"object": map[string]any{"type": "commit", "sha": strings.Repeat("a", 40)}})
		})
	})
	got, err := env.c.PublicTagCommit(context.Background(), "alexei-led/archfit-action", "main")
	if got != "" || !errors.Is(err, ErrNotFound) {
		t.Fatalf("PublicTagCommit = %q, %v", got, err)
	}
}

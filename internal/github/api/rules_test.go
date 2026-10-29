package api

import (
	"context"
	"maps"
	"net/http"
	"reflect"
	"testing"
)

func TestRequiredChecksReadsRulesetsAndTheProtectedFlag(t *testing.T) {
	ref := RepoRef{InstallationID: 7, FullName: "acme/app"}
	required := func(contexts ...string) map[string]any {
		checks := make([]map[string]any, len(contexts))
		for i, c := range contexts {
			checks[i] = map[string]any{"context": c, "integration_id": 1}
		}
		return map[string]any{"type": "required_status_checks", "parameters": map[string]any{"required_status_checks": checks}}
	}
	tests := []struct {
		name      string
		rules     any
		protected bool
		classic   []string // the checks the branch endpoint lists; nil: it lists none
		want      BranchRules
	}{
		{"ruleset requires checks", []any{map[string]any{"type": "deletion"}, required("archfit/gate", "Test")}, false, nil,
			BranchRules{Contexts: []string{"archfit/gate", "Test"}}},
		{"two rulesets", []any{required("a"), required("b")}, false, nil, BranchRules{Contexts: []string{"a", "b"}}},
		{"no rules", []any{}, false, nil, BranchRules{}},
		{"protected branch that shows no checks", []any{}, true, nil, BranchRules{Protected: true}},
		{"readable classic protection with the gate", []any{}, true, []string{"Test", "archfit/gate"}, BranchRules{Contexts: []string{"Test", "archfit/gate"}}},
		{"readable classic protection without the gate", []any{}, true, []string{"Test"}, BranchRules{Contexts: []string{"Test"}}},
		{"ruleset and readable protection", []any{required("a")}, true, []string{"b"}, BranchRules{Contexts: []string{"a", "b"}}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			env := muxEnv(t, func(mux *http.ServeMux) {
				mux.HandleFunc("GET /repos/acme/app/installation", func(w http.ResponseWriter, _ *http.Request) {
					writeJSON(w, http.StatusOK, map[string]any{"id": 7})
				})
				mux.HandleFunc("GET /repos/acme/app/rules/branches/{branch...}", func(w http.ResponseWriter, r *http.Request) {
					if r.PathValue("branch") != "release/x" {
						w.WriteHeader(http.StatusNotFound)
						return
					}
					writeJSON(w, http.StatusOK, tt.rules)
				})
				mux.HandleFunc("GET /repos/acme/app/branches/{branch...}", func(w http.ResponseWriter, _ *http.Request) {
					body := map[string]any{"name": "release/x", "protected": tt.protected}
					if tt.classic != nil {
						body["protection"] = map[string]any{"enabled": true, "required_status_checks": map[string]any{"contexts": tt.classic}}
					}
					writeJSON(w, http.StatusOK, body)
				})
			})
			got, err := env.c.RequiredChecks(context.Background(), ref, "release/x")
			if err != nil {
				t.Fatal(err)
			}
			if !reflect.DeepEqual(got, tt.want) {
				t.Errorf("RequiredChecks = %+v, want %+v", got, tt.want)
			}
		})
	}
}

func TestRequiredChecksFailsWhenEitherReadFails(t *testing.T) {
	ref := RepoRef{InstallationID: 7, FullName: "acme/app"}
	for _, failing := range []string{"rules", "branch"} {
		t.Run(failing, func(t *testing.T) {
			env := muxEnv(t, func(mux *http.ServeMux) {
				mux.HandleFunc("GET /repos/acme/app/installation", func(w http.ResponseWriter, _ *http.Request) {
					writeJSON(w, http.StatusOK, map[string]any{"id": 7})
				})
				mux.HandleFunc("GET /repos/acme/app/rules/branches/{branch...}", func(w http.ResponseWriter, _ *http.Request) {
					if failing == "rules" {
						w.WriteHeader(http.StatusForbidden)
						return
					}
					writeJSON(w, http.StatusOK, []any{})
				})
				mux.HandleFunc("GET /repos/acme/app/branches/{branch...}", func(w http.ResponseWriter, _ *http.Request) {
					if failing == "branch" {
						w.WriteHeader(http.StatusForbidden)
						return
					}
					writeJSON(w, http.StatusOK, map[string]any{"protected": false})
				})
			})
			if got, err := env.c.RequiredChecks(context.Background(), ref, "main"); err == nil {
				t.Errorf("RequiredChecks = %+v, want an error: the caller must show unknown, not not-enforced", got)
			}
		})
	}
}

// TestPullReportsGitHubsMergeState: the test merge commit and the mergeable verdict are what an upload's envelope is
// held to. GitHub leaves both null until it has computed the merge, and a conflict has no merge commit.
func TestPullReportsGitHubsMergeState(t *testing.T) {
	yes, no := true, false
	tests := []struct {
		number    string
		fields    map[string]any
		wantSHA   string
		wantMerge *bool
	}{
		{"1", map[string]any{"merge_commit_sha": "mc1", "mergeable": true}, "mc1", &yes},
		{"2", map[string]any{"merge_commit_sha": nil, "mergeable": nil}, "", nil},
		{"3", map[string]any{"merge_commit_sha": nil, "mergeable": false}, "", &no},
		{"4", map[string]any{}, "", nil},
	}
	env := muxEnv(t, func(mux *http.ServeMux) {
		mux.HandleFunc("GET /repos/acme/app/installation", func(w http.ResponseWriter, _ *http.Request) {
			writeJSON(w, http.StatusOK, map[string]any{"id": 7})
		})
		mux.HandleFunc("GET /repos/acme/app/pulls/{n}", func(w http.ResponseWriter, r *http.Request) {
			for _, tt := range tests {
				if tt.number == r.PathValue("n") {
					body := pullFixture(1, "open", "feature", "acme/app", nil)
					maps.Copy(body, tt.fields)
					writeJSON(w, http.StatusOK, body)
					return
				}
			}
			w.WriteHeader(http.StatusNotFound)
		})
	})
	ref := RepoRef{InstallationID: 7, FullName: "acme/app"}
	for _, tt := range tests {
		t.Run("pull "+tt.number, func(t *testing.T) {
			n := int(tt.number[0] - '0')
			p, err := env.c.PullSummary(context.Background(), ref, n)
			if err != nil {
				t.Fatal(err)
			}
			if p.MergeCommitSHA != tt.wantSHA || !reflect.DeepEqual(p.Mergeable, tt.wantMerge) {
				t.Errorf("MergeCommitSHA = %q, Mergeable = %v; want %q, %v", p.MergeCommitSHA, p.Mergeable, tt.wantSHA, tt.wantMerge)
			}
		})
	}
}

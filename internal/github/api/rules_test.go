package api

import (
	"context"
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
		want      BranchRules
	}{
		{"ruleset requires checks", []any{map[string]any{"type": "deletion"}, required("archfit/gate", "Test")}, false,
			BranchRules{Contexts: []string{"archfit/gate", "Test"}}},
		{"two rulesets", []any{required("a"), required("b")}, false, BranchRules{Contexts: []string{"a", "b"}}},
		{"no rules", []any{}, false, BranchRules{}},
		{"protected branch without ruleset checks", []any{}, true, BranchRules{Protected: true}},
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
					writeJSON(w, http.StatusOK, map[string]any{"name": "release/x", "protected": tt.protected})
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

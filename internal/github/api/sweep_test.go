package api

import (
	"net/http"
	"reflect"
	"testing"
	"time"
)

func TestCompareCarriesBoundedCommitParentProof(t *testing.T) {
	env := muxEnv(t, func(mux *http.ServeMux) {
		mux.HandleFunc("GET /repos/o/r/compare/{spec}", func(w http.ResponseWriter, r *http.Request) {
			if r.URL.Query().Get("per_page") != "100" {
				t.Errorf("query = %s", r.URL.RawQuery)
			}
			writeJSON(w, http.StatusOK, map[string]any{"status": "ahead", "files": []any{}, "total_commits": 101,
				"commits": []any{map[string]any{"sha": testSHA2, "parents": []any{map[string]string{"sha": testSHA1}, map[string]string{"sha": testSHA3}}}}})
		})
	})
	c, err := env.c.Compare(t.Context(), repoA, testSHA1, testSHA2)
	if err != nil || c.TotalCommits != 101 || !reflect.DeepEqual(c.Commits, []Commit{{SHA: testSHA2, Parents: []string{testSHA1, testSHA3}}}) {
		t.Fatalf("Compare = %+v,%v", c, err)
	}
}

func TestWorkflowRunCarriesItsLastStatusUpdate(t *testing.T) {
	updated := time.Date(2026, 11, 20, 3, 0, 0, 0, time.UTC)
	env := muxEnv(t, func(mux *http.ServeMux) {
		mux.HandleFunc("GET /repos/o/r/actions/runs/7", func(w http.ResponseWriter, _ *http.Request) {
			writeJSON(w, http.StatusOK, map[string]any{"id": 7, "run_attempt": 2, "status": "completed", "created_at": updated.Add(-30 * 24 * time.Hour), "updated_at": updated})
		})
	})
	run, err := env.c.Run(t.Context(), repoA, 7)
	if err != nil || !run.UpdatedAt.Equal(updated) || !run.CreatedAt.Before(run.UpdatedAt) {
		t.Fatalf("Run = %+v,%v", run, err)
	}
}

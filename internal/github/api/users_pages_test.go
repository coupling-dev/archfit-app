package api

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"sync"
	"testing"
	"time"
)

// Each installation's repository page waits for the others. Read one after another, the first would wait out the
// deadline and the list would fail, so the test proves the pages are in flight together.
func TestInstallationPagesAreReadTogether(t *testing.T) {
	ids := []int64{1, 2, 3}
	var mu sync.Mutex
	seen := map[string]bool{}
	all := make(chan struct{})
	env := muxEnv(t, func(mux *http.ServeMux) {
		mux.HandleFunc("GET /user/installations", func(w http.ResponseWriter, _ *http.Request) {
			items := []map[string]any{}
			for _, id := range ids {
				items = append(items, map[string]any{"id": id})
			}
			writeTestJSON(w, map[string]any{"installations": items})
		})
		mux.HandleFunc("GET /user/installations/{id}/repositories", func(w http.ResponseWriter, r *http.Request) {
			mu.Lock()
			seen[r.PathValue("id")] = true
			if len(seen) == len(ids) {
				close(all)
			}
			mu.Unlock()
			select {
			case <-all:
			case <-time.After(5 * time.Second):
				// Not a status the client retries: a sequential read would fail here rather than wait it out.
				w.WriteHeader(http.StatusNotFound)
				return
			}
			writeTestJSON(w, map[string]any{"repositories": []map[string]any{
				{"id": 10, "full_name": fmt.Sprintf("org%s/app", r.PathValue("id")), "default_branch": "main"},
			}})
		})
	})
	repos, err := env.c.UserRepos(context.Background(), "alice-token")
	if err != nil {
		t.Fatalf("the pages were not read together: %v", err)
	}
	var got []string
	for _, r := range repos {
		got = append(got, r.FullName)
	}
	want := []string{"org1/app", "org2/app", "org3/app"}
	if fmt.Sprint(got) != fmt.Sprint(want) {
		t.Errorf("repositories = %v, want the installations' order %v", got, want)
	}
}

// One page that fails fails the whole list: a partial list would hide repositories the user can read.
func TestOneFailingPageFailsTheWholeList(t *testing.T) {
	env := muxEnv(t, func(mux *http.ServeMux) {
		mux.HandleFunc("GET /user/installations", func(w http.ResponseWriter, _ *http.Request) {
			writeTestJSON(w, map[string]any{"installations": []map[string]any{{"id": 1}, {"id": 2}}})
		})
		mux.HandleFunc("GET /user/installations/{id}/repositories", func(w http.ResponseWriter, r *http.Request) {
			if r.PathValue("id") == "2" {
				w.WriteHeader(http.StatusInternalServerError)
				return
			}
			writeTestJSON(w, map[string]any{"repositories": []map[string]any{
				{"id": 10, "full_name": "org1/app", "default_branch": "main"},
			}})
		})
	})
	repos, err := env.c.UserRepos(context.Background(), "alice-token")
	if err == nil {
		t.Fatalf("a failed page returned %d repositories, want an error", len(repos))
	}
	if repos != nil {
		t.Errorf("a failed page returned a partial list: %v", repos)
	}
}

func writeTestJSON(w http.ResponseWriter, body any) {
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(body)
}

// The cap applies to the total over all installations: a list one repository past it fails, as before.
func TestTheTotalCapCoversAllInstallations(t *testing.T) {
	full := make([]map[string]any, maxItems)
	for i := range full {
		full[i] = map[string]any{"id": i + 1, "full_name": fmt.Sprintf("org/r%d", i), "default_branch": "main"}
	}
	env := muxEnv(t, func(mux *http.ServeMux) {
		mux.HandleFunc("GET /user/installations", func(w http.ResponseWriter, _ *http.Request) {
			writeTestJSON(w, map[string]any{"installations": []map[string]any{{"id": 1}, {"id": 2}}})
		})
		mux.HandleFunc("GET /user/installations/{id}/repositories", func(w http.ResponseWriter, r *http.Request) {
			if r.PathValue("id") == "1" {
				writeTestJSON(w, map[string]any{"repositories": full})
				return
			}
			writeTestJSON(w, map[string]any{"repositories": []map[string]any{
				{"id": 99999, "full_name": "other/one", "default_branch": "main"},
			}})
		})
	})
	if _, err := env.c.UserRepos(context.Background(), "alice-token"); err == nil {
		t.Fatal("a list one past the total cap was returned, want an error")
	}
}

package ui

import (
	"bytes"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"

	"github.com/alexei-led/archfit-app/internal/wire/wording"
)

func TestReposGoldens(t *testing.T) {
	tests := []struct {
		name   string
		record map[string]any
		listed bool
	}{
		{"clean", map[string]any{"approved_digest": "approved", "drift": map[string]any{"head": "stored-head", "state": "clean"}}, true},
		{"drift", map[string]any{"approved_digest": "approved", "drift": map[string]any{"head": "stored-head", "state": "drift"}}, true},
		{"expired", map[string]any{"approved_digest": "approved", "drift": map[string]any{"head": "stored-head", "state": "expired"}}, true},
		{"outside-flow", map[string]any{"approved_digest": "approved", "outside_flow": map[string]any{"digest": "other"}, "drift": map[string]any{"head": "stored-head", "state": "expired"}}, true},
		{"not-set-up", nil, true},
		{"no-approval", map[string]any{"outside_flow": map[string]any{"digest": "other"}, "drift": map[string]any{"head": "stored-head", "state": "clean"}}, true},
		{"missing-evidence", map[string]any{"approved_digest": "approved"}, true},
		{"partial-evidence", map[string]any{"approved_digest": "approved", "drift": map[string]any{"head": "stored-head", "state": "clean", "unread": true}}, true},
		{"no-repositories", nil, false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			e := newEnv(t)
			if tt.record != nil {
				tt.record["schema"] = "archfit-app.repo.v1"
				homePut(t, e, "repo", tt.record)
			}
			if tt.listed {
				e.grant("alice", repoName, permRead)
			}
			response := e.signIn("alice").get("/api/repos")
			wantStatus(t, response, http.StatusOK)
			checkReposGolden(t, tt.name, response)
			for _, method := range []string{"BranchHead", "CheckRuns", "File", "OpenPulls"} {
				if calls := e.gh.called(method); len(calls) != 0 {
					t.Errorf("repositories read %s: %v", method, calls)
				}
			}
		})
	}
}

func TestReposSortAndTruncateBeforeReadingState(t *testing.T) {
	e := newEnv(t)
	for i := maxOverviewRepos; i >= 0; i-- {
		name := fmt.Sprintf("acme/repo-%03d", i)
		e.addRepo(name, installA, int64(100+i))
		e.grant("alice", name, permRead)
	}
	if err := e.store.Put(t.Context(), 1, "v4/repo/acme/repo-100", []byte("unreadable")); err != nil {
		t.Fatal(err)
	}
	response := e.signIn("alice").get("/api/repos")
	wantStatus(t, response, http.StatusOK)
	body := decodeAs[overviewBody](t, response)
	if !body.Truncated || len(body.Repos) != maxOverviewRepos {
		t.Fatalf("repositories = %+v", body)
	}
	for i, row := range body.Repos {
		if want := fmt.Sprintf("acme/repo-%03d", i); row.FullName != want {
			t.Errorf("repository %d = %s, want %s", i, row.FullName, want)
		}
	}
}

func TestReposUnreadableRecordIsAnError(t *testing.T) {
	e := newEnv(t)
	homePut(t, e, "repo", map[string]any{"schema": "unreadable"})
	wantStatus(t, e.as("alice", permRead).get("/api/repos"), http.StatusBadGateway)
}

func checkReposGolden(t *testing.T, name string, response *httptest.ResponseRecorder) {
	t.Helper()
	if words := wording.NotUsedIn(response.Body.String()); len(words) != 0 {
		t.Fatalf("repositories use %v: %s", words, response.Body.String())
	}
	var pretty bytes.Buffer
	if err := json.Indent(&pretty, response.Body.Bytes(), "", "  "); err != nil {
		t.Fatal(err)
	}
	pretty.WriteByte('\n')
	path := filepath.Join("testdata", "repos", name+".json")
	if os.Getenv("UPDATE_GOLDEN") == "1" {
		if err := os.MkdirAll(filepath.Dir(path), 0755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(path, pretty.Bytes(), 0644); err != nil {
			t.Fatal(err)
		}
	}
	want, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(pretty.Bytes(), want) {
		t.Errorf("repositories differ from %s:\ngot %s\nwant %s", path, pretty.Bytes(), want)
	}
}

func TestReposAllStatesGolden(t *testing.T) {
	e := strangerEnv(t)
	for _, state := range []string{"clean", "drift", "expired", "outside-flow", "not-set-up", "unknown"} {
		name := "acme/" + state
		e.addRepo(name, installA, 50)
		e.grant("alice", name, permRead)
		rec := map[string]any{"schema": "archfit-app.repo.v1", "approved_digest": "approved"}
		switch state {
		case "outside-flow":
			rec["outside_flow"] = map[string]any{"digest": "other"}
		case "not-set-up":
			delete(rec, "approved_digest")
		case "unknown":
		default:
			rec["drift"] = map[string]any{"head": "stored-head", "state": state}
		}
		raw, err := json.Marshal(rec)
		if err != nil {
			t.Fatal(err)
		}
		if err := e.store.Put(t.Context(), 1, "v4/repo/"+name, raw); err != nil {
			t.Fatal(err)
		}
	}
	e.addRepo("octo/tool", installC, 51)
	e.grant("alice", "octo/tool", permRead)
	// Unadmitted data must never be read, even when it would fail decoding.
	if err := e.store.Put(t.Context(), 3, "v4/repo/octo/tool", []byte("unreadable")); err != nil {
		t.Fatal(err)
	}
	res := e.signIn("alice").get("/api/repos")
	wantStatus(t, res, http.StatusOK)
	checkReposGolden(t, "all-states", res)
}

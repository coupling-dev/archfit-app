package ui

import (
	"bytes"
	"encoding/json"
	"errors"
	"net/http"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/alexei-led/archfit-app/internal/github/api"
	"github.com/alexei-led/archfit-app/internal/store"
	"github.com/alexei-led/archfit-app/internal/wire/envelope"
	"github.com/alexei-led/archfit-app/internal/wire/result"
	"github.com/alexei-led/archfit-app/internal/wire/wording"
)

func TestPullAPIGoldens(t *testing.T) {
	cases := []struct{ name, source, status string }{
		{"pass", "pass", "pass"}, {"fail-advisory", "deny", "fail"}, {"fail-fix", "deny", "fail"}, {"fail-ask", "graph", "fail"},
		{"fail-copy", "duplication", "fail"}, {"fail-expired-exception", "expired_exception", "fail"},
		{"unknown", "policy_differs", "unknown"}, {"waiting", "graph", "waiting"},
		{"unknown-workflow", "pass", "unknown"}, {"unknown-outside-flow", "pass", "unknown"}, {"unknown-missing-evidence", "pass", "unknown"}, {"no-result", "", ""}, {"missing-artifact", "pass", "pass"}, {"mismatch", "pass", "pass"}, {"pending", "pass", "pass"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			e := newEnv(t)
			e.now = time.Date(2026, 11, 10, 12, 0, 0, 0, time.UTC)
			if tc.source != "" {
				raw, err := os.ReadFile(filepath.Join("..", "..", "wire", "result", "testdata", "result", tc.source+".json"))
				if err != nil {
					t.Fatal(err)
				}
				digest := envelope.PayloadDigest(raw)
				if tc.name == "mismatch" {
					digest = envelope.PayloadDigest([]byte("different"))
				}
				homePut(t, e, "pull/4", map[string]any{"schema": "archfit-app.pull.v1", "head_sha": sha(4), "run_id": int64(42), "run_attempt": 1, "payload_digest": digest, "policy_digest": "accepted", "status": tc.status})
				e.gh.artifacts[42] = raw
			}
			if tc.name == "missing-artifact" {
				delete(e.gh.artifacts, 42)
			}
			if tc.name == "waiting" {
				homePut(t, e, "request-index", map[string]any{"schema": "archfit-app.request-index.v1", "entries": []any{map[string]any{"key": "graph pkg_tc pkg_util", "id": "431"}}})
				homePut(t, e, "request/431", map[string]any{"schema": "archfit-app.request.v1", "key": "graph pkg_tc pkg_util", "state": "open", "waiting": []int{4}, "reason": "reason", "numbers": map[string]any{}})
			}
			var unknown result.Unknown
			switch tc.name {
			case "unknown-workflow":
				unknown = result.Unknown{Path: ".github/workflows/archfit.yaml", Cause: "tool", What: "differs from main", Why: "a pull request runs its own workflow file", Next: result.Next{Kind: "fix", Text: "update the branch; if the pull request changes the workflow, an owner approves it in the App"}}
			case "unknown-outside-flow":
				unknown = result.Unknown{Path: ".archfit.yaml", Cause: "policy", What: "changed on main with no recorded approval", Why: "only owners change the policy", Next: result.Next{Kind: "wait", Text: "an owner confirms or reverts it in the App: #433"}}
			case "unknown-missing-evidence":
				unknown = result.Unknown{Cause: "tool", What: "the check finished without an uploaded result", Next: result.Next{Kind: "fix", Text: "run the Archfit check again"}}
			}
			if unknown.Cause != "" {
				body, err := json.Marshal(result.Result{Status: result.StatusUnknown, Summary: "archfit: unknown, no verdict", Unknown: []result.Unknown{unknown}})
				if err != nil {
					t.Fatal(err)
				}
				homePut(t, e, "publication/4/"+sha(4), map[string]any{"schema": "archfit-app.publication.v1", "head_sha": sha(4), "run_id": 42, "run_attempt": 1, "result": body, "digest": envelope.PayloadDigest(body), "source": "sweep"})
			}
			if tc.name == "unknown-missing-evidence" {
				if err := e.store.Delete(t.Context(), store.Tenant(installA), "v4/pull/"+repoName+"/4"); err != nil {
					t.Fatal(err)
				}
			}
			if tc.name == "pending" {
				homePut(t, e, "publication/4/"+sha(4), map[string]any{"schema": "archfit-app.publication.v1", "head_sha": sha(4), "run_id": 42, "run_attempt": 1, "pending": true})
			}
			if tc.status != "" {
				check := api.CheckRun{ID: 7, Name: "archfit/gate", HeadSHA: sha(4), Status: "completed"}
				switch tc.status {
				case "pass":
					check.Conclusion = "success"
				case "fail":
					check.Conclusion = "failure"
				case "waiting":
					check.Status = "in_progress"
				case "unknown":
					check.Conclusion = "action_required"
				}
				if tc.name == "fail-advisory" {
					check.Conclusion = "neutral"
				}
				e.gh.checks[repoName+"@"+sha(4)+"#archfit/gate"] = []api.CheckRun{check}
			}
			res := e.as("alice", permRead).get("/api/repos/" + repoName + "/pulls/4")
			wantStatus(t, res, http.StatusOK)
			if words := wording.NotUsedIn(res.Body.String()); len(words) > 0 {
				t.Fatalf("pull uses %v", words)
			}
			var pretty bytes.Buffer
			if err := json.Indent(&pretty, res.Body.Bytes(), "", "  "); err != nil {
				t.Fatal(err)
			}
			pretty.WriteByte('\n')
			path := filepath.Join("testdata", "pull", tc.name+".json")
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
				t.Fatalf("%s differs:\n%s", path, pretty.String())
			}
		})
	}
}

func TestPullAPIArtifactFailure(t *testing.T) {
	e := newEnv(t)
	homePut(t, e, "pull/4", map[string]any{"schema": "archfit-app.pull.v1", "head_sha": sha(4), "run_id": 42, "run_attempt": 1, "status": "fail", "policy_digest": "accepted"})
	e.gh.fail["Artifact"] = errors.New("unavailable")
	res := e.as("alice", permRead).get("/api/repos/" + repoName + "/pulls/4")
	if res.Code < 500 {
		t.Fatalf("status = %d, want error", res.Code)
	}
	path := filepath.Join("testdata", "pull", "infrastructure-error.json")
	var pretty bytes.Buffer
	if err := json.Indent(&pretty, res.Body.Bytes(), "", "  "); err != nil {
		t.Fatal(err)
	}
	pretty.WriteByte('\n')
	if os.Getenv("UPDATE_GOLDEN") == "1" {
		if err := os.WriteFile(path, pretty.Bytes(), 0644); err != nil {
			t.Fatal(err)
		}
	}
	want, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(pretty.Bytes(), want) {
		t.Fatalf("%s differs:\n%s", path, pretty.String())
	}
}

package ui

import (
	"encoding/json"
	"net/http"
	"testing"
	"testing/synctest"

	"github.com/alexei-led/archfit-app/internal/control/answers"
	"github.com/alexei-led/archfit-app/internal/gate"
	"github.com/alexei-led/archfit-app/internal/wire/envelope"
	"github.com/alexei-led/archfit-app/internal/wire/result"
)

// The pull page of an unknown result reads the user's permission while the owner views are read, not before them.
func TestPullReviewReadsPermissionBesideTheViews(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		e := newEnv(t)
		body, err := json.Marshal(result.Result{Status: result.StatusUnknown, Summary: "archfit: unknown, no verdict", Unknown: []result.Unknown{
			{Path: ".archfit.yaml", Cause: "policy", What: "the first policy needs approval", Next: result.Next{Kind: "wait", Text: "an administrator approves the policy in the App"}}}})
		if err != nil {
			t.Fatal(err)
		}
		homePut(t, e, "pull/4", map[string]any{"schema": "archfit-app.pull.v1", "head_sha": sha(4), "run_id": 42, "run_attempt": 1, "status": "unknown", "policy_digest": "accepted"})
		homePut(t, e, "publication/4/"+sha(4), map[string]any{"schema": "archfit-app.publication.v1", "head_sha": sha(4), "run_id": 42, "run_attempt": 1, "result": body, "digest": envelope.PayloadDigest(body), "source": "sweep"})
		var admin bool
		e.owners.review = func(a answers.OwnerAction, u gate.OwnerUser) error {
			if a.Kind == gate.OwnerFirst && a.Pull == 4 {
				admin = u.Admin
				return nil
			}
			return gate.ErrOwnerNotDue
		}
		client := e.as("alice", permAdmin)
		const pull = "/api/repos/" + repoName + "/pulls/4"
		// The first request warms the authorization cache, which reads the same permission on its own.
		wantStatus(t, client.get(pull), http.StatusOK)
		e.gh.onPermission, e.owners.onView = meet(t)
		res := client.get(pull)
		wantStatus(t, res, http.StatusOK)
		var got pullJSON
		if err := json.Unmarshal(res.Body.Bytes(), &got); err != nil {
			t.Fatal(err)
		}
		if got.Review == nil || got.Review.Href != "/r/"+repoName+"/owner/first?pull=4" {
			t.Fatalf("review = %+v, want the first policy of pull request 4", got.Review)
		}
		if !admin {
			t.Fatal("the review did not get the permission it waited for")
		}
	})
}

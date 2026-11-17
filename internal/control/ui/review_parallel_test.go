package ui

import (
	"encoding/json"
	"fmt"
	"net/http"
	"strings"
	"sync/atomic"
	"testing"
	"testing/synctest"
	"time"

	"github.com/alexei-led/archfit-app/internal/control/answers"
	"github.com/alexei-led/archfit-app/internal/gate"
	"github.com/alexei-led/archfit-app/internal/github/api"
)

// addWorkflowPull adds an open pull request on the same repository whose head changes the workflow.
func addWorkflowPull(e *env, number int) {
	e.gh.pulls[fmt.Sprintf("%s#%d", repoName, number)] = api.Pull{Number: number, State: "open", BaseRef: mainBranch,
		HeadSHA: sha(number), HeadRepo: repoName}
	e.gh.setFile(repoName, sha(number), workflow, "changed workflow")
	e.gh.setFile(repoName, sha(number), ".archfit.yaml", homePolicy)
}

// Each owner probe takes probe of virtual time. The critical path of the whole response is then one probe, where the
// sequential code took one probe per probe per need: three needs of three probes each were nine probes in a row.
func TestHomeOwnerReviewsOfWorkflowChangesRunTogether(t *testing.T) {
	const probe = 250 * time.Millisecond
	synctest.Test(t, func(t *testing.T) {
		e := newHomeEnv(t, homePolicy)
		for _, n := range []int{4, 5, 6} {
			addWorkflowPull(e, n)
		}
		var probes atomic.Int32
		e.owners.review = func(a answers.OwnerAction, _ gate.OwnerUser) error {
			time.Sleep(probe)
			probes.Add(1)
			if a.Kind == gate.OwnerWorkflow {
				return nil
			}
			return gate.ErrOwnerNotDue
		}
		client := e.as("alice", permAdmin)
		// The first request warms the authorization cache, which reads the same permission on its own.
		wantStatus(t, client.get(homeAPI), http.StatusOK)
		probes.Store(0)
		permissions := len(e.gh.called("Permission"))
		start := time.Now()
		response := client.get(homeAPI)
		elapsed := time.Since(start)
		wantStatus(t, response, http.StatusOK)
		reads := len(e.gh.called("Permission")) - permissions
		t.Logf("owner probes = %d, permission reads = %d, response time = %v", probes.Load(), reads, elapsed)
		var body homeJSON
		if err := json.Unmarshal(response.Body.Bytes(), &body); err != nil {
			t.Fatal(err)
		}
		if len(body.NeedsYou) != 3 {
			t.Fatalf("needs you = %+v", body.NeedsYou)
		}
		for i, need := range body.NeedsYou {
			if want := fmt.Sprintf("Review workflow change · pull request #%d", need.Pull); need.Text != want || need.Href != "/r/acme/app/owner/workflow?pull="+fmt.Sprint(need.Pull) {
				t.Fatalf("need %d = %+v, want %q", i, need, want)
			}
		}
		if got := probes.Load(); got != 9 {
			t.Fatalf("owner probes = %d, want 9", got)
		}
		if reads != 1 {
			t.Fatalf("permission reads = %d, want one per request", reads)
		}
		if elapsed != probe {
			t.Fatalf("response took %v of virtual time, want one probe (%v)", elapsed, probe)
		}
	})
}

// The answer is the first decisive probe in order, even when a later probe finishes first.
func TestHomeOwnerReviewAnswerFollowsProbeOrderNotFinishTime(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		e := newHomeEnv(t, homePolicy)
		addWorkflowPull(e, 4)
		e.owners.review = func(a answers.OwnerAction, _ gate.OwnerUser) error {
			switch {
			case a.Kind == gate.OwnerFirst && a.Pull == 4:
				time.Sleep(500 * time.Millisecond)
				return gate.ErrOwnerNotOwner
			case a.Kind == gate.OwnerFirst:
				time.Sleep(100 * time.Millisecond)
				return gate.ErrOwnerNotDue
			default:
				time.Sleep(100 * time.Millisecond)
				return nil
			}
		}
		response := e.as("alice", permAdmin).get(homeAPI)
		wantStatus(t, response, http.StatusOK)
		var body homeJSON
		if err := json.Unmarshal(response.Body.Bytes(), &body); err != nil {
			t.Fatal(err)
		}
		if len(body.NeedsYou) != 1 || !strings.HasPrefix(body.NeedsYou[0].Text, "An architecture owner named in the approved policy can review this.") || body.NeedsYou[0].Href != "" {
			t.Fatalf("needs you = %+v", body.NeedsYou)
		}
	})
}

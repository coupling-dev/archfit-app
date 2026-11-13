package gate

import (
	"context"
	"errors"
	"slices"
	"strings"
	"testing"

	"github.com/alexei-led/archfit-app/internal/gate/drift"
	"github.com/alexei-led/archfit-app/internal/gate/ingest"
	"github.com/alexei-led/archfit-app/internal/gate/records"
	"github.com/alexei-led/archfit-app/internal/github/api"
	"github.com/alexei-led/archfit-app/internal/store"
	"github.com/alexei-led/archfit-app/internal/wire/envelope"
	"github.com/alexei-led/archfit-app/internal/wire/result"
)

// pushSpec is an envelope 2 upload of a push run of the default branch's tip with the result golden.
func (w *world4) pushSpec(p *pull, golden string) *spec4 {
	w.t.Helper()
	w.editRun(p, func(r *api.WorkflowRun) { r.Event, r.PullRequests, r.HeadSHA = "push", nil, w.tip })
	s := w.spec4(p, golden)
	s.claims.EventName, s.claims.Ref, s.claims.SHA = "push", "refs/heads/"+testBranch, w.tip
	s.claims.JobWorkflowRef = shopName + "/" + testWorkflow + "@refs/heads/" + testBranch
	s.env.Event, s.env.PullRequest, s.env.HeadSHA, s.env.MergeBaseSHA = "push", 0, w.tip, w.tip
	s.result["head"] = w.tip
	return s
}

func (w *world4) driftIssues() []fakeIssue {
	w.t.Helper()
	var out []fakeIssue
	for _, i := range w.gh.issues[shopName] {
		if slices.Contains(i.Labels, drift.Label) {
			out = append(out, i)
		}
	}
	return out
}

func TestAPushRunWithDriftOpensTheDriftIssue(t *testing.T) {
	w := newWorld4(t)
	p := w.openPull(w.shop, 7)
	w.wantStatus(w.send4(w.pushSpec(p, "repository_drift")), "pass", "")

	issues := w.driftIssues()
	if len(issues) != 1 || issues[0].Title != "Archfit: drift on "+testBranch || !issues[0].Open ||
		!strings.Contains(issues[0].Body, "pkg/tc/netem.go:12: graph: pkg_tc -> pkg_util is not an allowed dependency") {
		t.Fatalf("drift issues = %+v", issues)
	}
	var rec records.Repo
	if err := records.Load(context.Background(), w.mem, store.Tenant(shopInstallation), records.RepoKey(shopName), &rec); err != nil {
		t.Fatal(err)
	}
	if rec.DriftIssue != issues[0].Number || !strings.HasPrefix(rec.DriftDigest, "sha256:") || rec.ApprovedDigest == "" {
		t.Errorf("repository record = %+v", rec)
	}
	// The same upload again (the Action's retry) changes nothing.
	edits := w.gh.issueEdits
	w.wantStatus(w.send4(w.pushSpec(p, "repository_drift")), "pass", "")
	if len(w.driftIssues()) != 1 || w.gh.issueEdits != edits {
		t.Errorf("the retry wrote again: %d issues, %d edits", len(w.driftIssues()), w.gh.issueEdits-edits)
	}
}

// TestOnlyAnAcceptedPushRunAtTheTipCountsForDrift: everything else carries no repository state the App may use.
func TestOnlyAnAcceptedPushRunAtTheTipCountsForDrift(t *testing.T) {
	block := &result.Repository{State: result.RepositoryDrift, Drift: []result.Error{{Path: "a.go", Line: 1, Rule: "graph",
		What: "a -> b is not an allowed dependency", Next: result.Next{Kind: "fix", Text: "fix the code"}, Key: "graph a b"}}}
	for _, tc := range []struct {
		name  string
		event string
		pull  int
		head  string
		d     decision4
	}{
		{"an older push that arrives late", envelope.EventPush, 0, "old", decision4{Status: result.StatusPass, Tip: "tip"}},
		{"a result with the App's records", envelope.EventPush, 0, "tip",
			decision4{Status: result.StatusUnknown, Tip: "tip", Reasons: []reason4{{Code: reasonUnknownEngine}}}},
		{"a pull request", envelope.EventPullRequest, 7, "tip", decision4{Status: result.StatusPass, Tip: "tip"}},
		{"a merge group", envelope.EventMergeGroup, 0, "tip", decision4{Status: result.StatusPass, Tip: "tip"}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			w := newWorld4(t)
			u := ingest.Upload4{
				Binding: ingest.Binding4{Repo: w.shop, Event: tc.event, PullRequest: tc.pull, HeadSHA: tc.head},
				// The envelope's own word is not trusted: GitHub's event decides.
				Envelope: envelope.EnvelopeV2{Event: envelope.EventPush},
				Result:   &result.Result{Status: result.StatusPass, Repository: block},
			}
			if err := w.gate.driftPushed(context.Background(), u, tc.d); err != nil {
				t.Fatal(err)
			}
			if issues := w.driftIssues(); len(issues) != 0 || len(w.gh.issues[shopName]) != 0 {
				t.Errorf("issues = %+v", w.gh.issues[shopName])
			}
		})
	}
}

// TestAFailedDriftIssueFailsTheUpload: the Action retries it, and the retry opens the issue.
func TestAFailedDriftIssueFailsTheUpload(t *testing.T) {
	w := newWorld4(t)
	p := w.openPull(w.shop, 7)
	w.gh.fail = map[string]error{"CreateIssue": errors.New("GitHub is down")}
	if a := w.send4(w.pushSpec(p, "repository_drift")); a.status != 503 {
		t.Fatalf("answer = %+v, want 503", a)
	}
	w.gh.fail = nil
	w.wantStatus(w.send4(w.pushSpec(p, "repository_drift")), "pass", "")
	if len(w.driftIssues()) != 1 {
		t.Errorf("drift issues = %+v", w.driftIssues())
	}
}

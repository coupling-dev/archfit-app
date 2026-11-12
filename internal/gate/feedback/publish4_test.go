package feedback

import (
	"context"
	"slices"
	"strings"
	"testing"

	"github.com/alexei-led/archfit-app/internal/gate/inputs"
	"github.com/alexei-led/archfit-app/internal/github/api"
	"github.com/alexei-led/archfit-app/internal/wire/result"
)

const digest4 = "sha256:1111111111111111111111111111111111111111111111111111111111111111"

func (h *harness) pub4() Publisher4 {
	return New4(h.checks, h.comments, h.pulls, h.store, appURL, h.clock.now)
}

func pub4(t inputs.Target, runID int64, attempt int, r result.Result) Publication4 {
	return Publication4{Target: t, RunID: runID, RunAttempt: attempt, Digest: digest4, Result: r}
}

func (h *harness) publish4(t *testing.T, p Publication4) {
	t.Helper()
	if err := h.pub4().Publish4(context.Background(), p); err != nil {
		t.Fatalf("Publish4: %v", err)
	}
}

func TestPublish4WritesTheGateCheckAndTheComment(t *testing.T) {
	h := newHarness(headB)
	h.publish4(t, pub4(prTarget(headB), 7, 1, engineResult(t, "graph")))

	run := h.checks.latest(t, headB, gateCheck)
	if run.Status != statusCompleted || run.Conclusion != conclusionFailure || run.Title != "fail, 1 error" {
		t.Errorf("check = %s %s %q", run.Status, run.Conclusion, run.Title)
	}
	if run.ExternalID != "v2:7:1:"+digest4 || run.DetailsURL != appURL+"/r/acme/shop/pull/7" || len(run.Annotations) != 1 {
		t.Errorf("check external_id %q, details %q, %d annotations", run.ExternalID, run.DetailsURL, len(run.Annotations))
	}
	if runs := h.checks.named(headB, stateCheck); len(runs) != 0 {
		t.Errorf("Archfit 4 wrote archfit/state: %+v", runs)
	}
	comments := h.comments.all()
	if len(comments) != 1 {
		t.Fatalf("%d comments, want 1", len(comments))
	}
	want := commentMarker + "\n" + headMarker(headB, Receipt{RunID: 7, RunAttempt: 1}) + "\n" + newView(engineResult(t, "graph")).comment(maxBody)
	if comments[0].Body != want {
		t.Errorf("comment:\n%s\nwant:\n%s", comments[0].Body, want)
	}
}

func TestPublish4WritesNoCommentForAPushOrAMergeGroup(t *testing.T) {
	for name, target := range map[string]inputs.Target{"push": pushTarget(headB), "merge group": groupTarget(headB)} {
		t.Run(name, func(t *testing.T) {
			h := newHarness(headB)
			h.publish4(t, pub4(target, 7, 1, engineResult(t, "pass")))
			if run := h.checks.latest(t, headB, gateCheck); run.Conclusion != conclusionSuccess {
				t.Errorf("conclusion %q", run.Conclusion)
			}
			if n := len(h.comments.all()); n != 0 || h.pulls.listed != 0 {
				t.Errorf("%d comments, %d changed-file reads", n, h.pulls.listed)
			}
		})
	}
}

// A check or a comment of a newer run, or a newer attempt of the same run, is never overwritten, a waiting check
// included.
func TestPublish4NeverOverwritesANewerRun(t *testing.T) {
	for name, newer := range map[string]api.CheckRun{
		"a newer run":                       {Status: statusCompleted, Conclusion: conclusionSuccess, ExternalID: "v2:8:1:" + digest4},
		"a newer attempt":                   {Status: statusCompleted, Conclusion: conclusionSuccess, ExternalID: "v2:7:2:" + digest4},
		"a newer run that waits":            {Status: statusInProgress, ExternalID: "v2:8:1:" + digest4},
		"a newer run with many annotations": {Status: statusCompleted, Conclusion: conclusionFailure, ExternalID: "v2:8:1:" + digest4, Annotations: make([]api.Annotation, 3)},
	} {
		t.Run(name, func(t *testing.T) {
			h := newHarness(headB)
			newer.Name, newer.HeadSHA, newer.Title = gateCheck, headB, "newer"
			h.checks.add(newer)
			newerBody := commentMarker + "\n" + headMarker(headB, Receipt{RunID: 8, RunAttempt: 1}) + "\nnewer"
			if _, err := h.comments.CreateComment(context.Background(), api.RepoRef{}, 7, newerBody); err != nil {
				t.Fatal(err)
			}
			h.publish4(t, pub4(prTarget(headB), 7, 1, engineResult(t, "deny")))

			if runs := h.checks.named(headB, gateCheck); len(runs) != 1 || runs[0].Title != "newer" {
				t.Errorf("the older run wrote the check: %+v", runs)
			}
			if c := h.comments.all(); len(c) != 1 || c[0].Body != newerBody {
				t.Errorf("the older run wrote the comment: %+v", c)
			}
		})
	}
}

func TestPublish4BatchesAnnotations(t *testing.T) {
	h := newHarness(headB)
	h.publish4(t, pub4(prTarget(headB), 7, 1, manyErrors(120)))
	if got := h.checks.annotationRequests; !slices.Equal(got, []int{50, 50, 20}) {
		t.Errorf("annotation requests %v, want [50 50 20]", got)
	}
	if run := h.checks.latest(t, headB, gateCheck); len(run.Annotations) != 120 {
		t.Errorf("%d annotations, want 120", len(run.Annotations))
	}
}

// The existing re-read between annotation batches stops an older publication once a newer one took the check.
func TestPublish4StopsBetweenBatchesWhenANewerRunWrote(t *testing.T) {
	h := newHarness(headB)
	h.checks.afterWrite = func() {
		h.checks.add(api.CheckRun{Name: gateCheck, HeadSHA: headB, Status: statusCompleted, Conclusion: conclusionSuccess, ExternalID: "v2:9:1:" + digest4, Title: "newer"})
	}
	h.publish4(t, pub4(prTarget(headB), 7, 1, manyErrors(120)))
	if got := h.checks.annotationRequests; !slices.Equal(got, []int{50}) {
		t.Errorf("annotation requests %v, want [50]: the older run went on after the newer one wrote", got)
	}
	if run := h.checks.latest(t, headB, gateCheck); run.Title != "newer" {
		t.Errorf("latest check %q", run.Title)
	}
}

func TestPublish4RepublishUpdatesInPlace(t *testing.T) {
	h := newHarness(headB)
	p := pub4(prTarget(headB), 7, 1, engineResult(t, "graph"))
	h.publish4(t, p)
	h.publish4(t, p)
	if runs := h.checks.named(headB, gateCheck); len(runs) != 1 || len(runs[0].Annotations) != 1 {
		t.Fatalf("runs %+v, want one run with one annotation", runs)
	}
	if got := h.checks.annotationRequests; !slices.Equal(got, []int{1, 0}) {
		t.Errorf("annotation requests %v, want [1 0]", got)
	}
	if n := h.comments.count("UpdateComment"); n != 0 {
		t.Errorf("an unchanged comment was written %d times", n)
	}
}

// GitHub keeps a completed run completed: a waiting result for the same head gets a new run, which is the latest.
func TestPublish4WaitingAfterACompletedRunIsANewRun(t *testing.T) {
	h := newHarness(headB)
	h.publish4(t, pub4(prTarget(headB), 7, 1, engineResult(t, "graph")))
	h.publish4(t, pub4(prTarget(headB), 7, 1, engineResult(t, "waiting")))
	runs := h.checks.named(headB, gateCheck)
	if len(runs) != 2 {
		t.Fatalf("%d runs, want 2", len(runs))
	}
	if latest := h.checks.latest(t, headB, gateCheck); latest.Status != statusInProgress || latest.Conclusion != "" || latest.Title != "waiting, request #431" {
		t.Errorf("latest = %s %q %q", latest.Status, latest.Conclusion, latest.Title)
	}
	mustContain(t, h.comments.all()[0].Body, "## Archfit: waiting, request #431\n")
}

// A pending check (in_progress, no receipt, no annotation) is completed in place.
func TestPublish4CompletesAPendingCheckInPlace(t *testing.T) {
	h := newHarness(headB)
	h.checks.add(api.CheckRun{Name: gateCheck, HeadSHA: headB, Status: statusInProgress, Title: pendingTitle})
	h.publish4(t, pub4(prTarget(headB), 7, 1, engineResult(t, "pass")))
	runs := h.checks.named(headB, gateCheck)
	if len(runs) != 1 || runs[0].Status != statusCompleted || runs[0].Conclusion != conclusionSuccess {
		t.Errorf("runs %+v, want the pending run completed as a success", runs)
	}
}

func TestPublish4AdvisoryFailIsNeutralWithTheSameRecords(t *testing.T) {
	required, advisory := newHarness(headB), newHarness(headB)
	p := pub4(prTarget(headB), 7, 1, engineResult(t, "deny"))
	required.publish4(t, p)
	p.Advisory = true
	advisory.publish4(t, p)
	r, a := required.checks.latest(t, headB, gateCheck), advisory.checks.latest(t, headB, gateCheck)
	if r.Conclusion != conclusionFailure || a.Conclusion != conclusionNeutral {
		t.Errorf("conclusions %q and %q, want failure and neutral", r.Conclusion, a.Conclusion)
	}
	if r.Title != a.Title || r.Summary != a.Summary || len(a.Annotations) != len(r.Annotations) ||
		required.comments.all()[0].Body != advisory.comments.all()[0].Body {
		t.Error("advisory mode changed the records")
	}
}

func TestPublish4SkipsTheCommentOfAnOldHead(t *testing.T) {
	h := newHarness(headA) // the pull request has moved on to headA
	h.publish4(t, pub4(prTarget(headB), 7, 1, engineResult(t, "deny")))
	if run := h.checks.latest(t, headB, gateCheck); run.Conclusion != conclusionFailure {
		t.Errorf("the old head's own check was not written: %+v", run)
	}
	if n := len(h.comments.all()); n != 0 {
		t.Errorf("%d comments for an old head", n)
	}
}

func TestPublish4UpdatesTheOneCommentOfThePullRequest(t *testing.T) {
	h := newHarness(headB)
	old, err := h.comments.CreateComment(context.Background(), api.RepoRef{}, 7, commentMarker+"\n"+headMarker(headA, Receipt{RunID: 9, RunAttempt: 1})+"\n## Archfit 3 comment")
	if err != nil {
		t.Fatal(err)
	}
	h.publish4(t, pub4(prTarget(headB), 7, 1, engineResult(t, "pass")))
	c := h.comments.all()
	if len(c) != 1 || c[0].ID != old.ID || !strings.HasSuffix(c[0].Body, "\n## Archfit: pass\n") {
		t.Errorf("comments %+v, want the one comment edited", c)
	}
}

// manyErrors is a fail with n errors, each on its own line.
func manyErrors(n int) result.Result {
	r := result.Result{Status: result.StatusFail, Summary: "archfit: fail, many errors"}
	for i := range n {
		r.Errors = append(r.Errors, result.Error{Path: "a.go", Line: i + 1, Rule: "graph", What: "a -> b is not an allowed dependency",
			Next: result.Next{Kind: "fix", Text: "fix the code"}})
	}
	return r
}

package feedback

import (
	"context"
	"errors"
	"reflect"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/alexei-led/archfit-app/internal/gate/inputs"
	"github.com/alexei-led/archfit-app/internal/gate/verdict"
	"github.com/alexei-led/archfit-app/internal/github/api"
	"github.com/alexei-led/archfit-app/internal/wire/manifest"
	"github.com/alexei-led/archfit-app/internal/wire/report"
)

func TestRepeatedDeliveryDoesNotSpam(t *testing.T) {
	ctx := context.Background()
	h := newHarness(headB)
	h.pulls.changed = []string{"pkg/a/a.go"} // the fixture's blocker and advisory are both located there
	const located = 2
	for range 5 {
		if err := h.pub.Publish(ctx, blockedPublication(t, headB, 7, 1)); err != nil {
			t.Fatalf("Publish: %v", err)
		}
	}
	counts := func() [3]int {
		return [3]int{len(h.checks.named(headB, gateCheck)), len(h.checks.named(headB, stateCheck)), len(h.comments.all())}
	}
	if counts() != [3]int{1, 1, 1} {
		t.Fatalf("gate, state, comments = %v, want one each", counts())
	}
	if got := len(h.checks.latest(t, headB, stateCheck).Annotations); got != located {
		t.Errorf("state annotations after five deliveries = %d, want %d: GitHub appends on every update", got, located)
	}
	if h.comments.count("CreateComment") != 1 || h.comments.count("UpdateComment") != 0 {
		t.Errorf("identical bodies caused comment writes: %v", h.comments.calls)
	}

	if err := h.pub.Publish(ctx, blockedPublication(t, headB, 7, 2)); err != nil {
		t.Fatalf("Publish: %v", err)
	}
	// A new attempt is a new archfit/state run: an update in place would add its annotations to the old ones.
	if counts() != [3]int{1, 2, 1} || h.comments.count("UpdateComment") != 1 {
		t.Errorf("new attempt: counts %v, comment updates %d; want gate 1, state 2, comment 1 and 1 update", counts(), h.comments.count("UpdateComment"))
	}
	state := h.checks.latest(t, headB, stateCheck)
	if state.ExternalID != "v1:7:2:"+reportDigest || len(state.Annotations) != located {
		t.Errorf("latest state run = %q with %d annotations, want the new attempt with %d", state.ExternalID, len(state.Annotations), located)
	}
	if got := h.checks.named(headB, gateCheck)[0].ExternalID; got != "v1:7:2:"+reportDigest {
		t.Errorf("gate external_id = %q", got)
	}
	mustContain(t, h.comments.all()[0].Body, "run 7.2 -->")
}

// A sweep publication keeps the run identity of the last receipt but has no report: the failure annotations of the
// report it replaces must not stay on the run that now says action_required.
func TestRepublishWithoutReportDropsAnnotations(t *testing.T) {
	ctx := context.Background()
	h := newHarness(headB)
	h.pulls.changed = []string{"pkg/a/a.go"}
	if err := h.pub.Publish(ctx, blockedPublication(t, headB, 7, 1)); err != nil {
		t.Fatalf("Publish: %v", err)
	}
	stale := blockedPublication(t, headB, 7, 1)
	stale.Identity, stale.Report = manifest.Identity{}, nil
	stale.Outcome = outcome(verdict.ActionRequired, verdict.Reason{Code: verdict.ReasonStaleInputs, Category: verdict.CategoryEvidence})
	stale.Receipt = receiptFor(stale.Outcome, 7, 1)
	for range 2 {
		if err := h.pub.Publish(ctx, stale); err != nil {
			t.Fatalf("Publish: %v", err)
		}
	}
	runs := h.checks.named(headB, stateCheck)
	state := h.checks.latest(t, headB, stateCheck)
	if len(runs) != 2 || len(state.Annotations) != 0 || state.Conclusion != string(verdict.ActionRequired) {
		t.Errorf("state runs = %d, latest %q with %d annotations; want a second run, action_required, none", len(runs), state.Conclusion, len(state.Annotations))
	}
	if gate := h.checks.named(headB, gateCheck); len(gate) != 1 || gate[0].Conclusion != string(verdict.ActionRequired) {
		t.Errorf("gate runs = %+v, want the one run updated in place", gate)
	}
}

// A newer publication that lands between two annotation batches of an older one keeps its summary: the older
// one stops sending batches.
func TestNewerPublicationStopsAnnotationBatches(t *testing.T) {
	ctx := context.Background()
	h := newHarness(headB)
	p := blockedPublication(t, headB, 7, 1)
	p.Target = pushTarget(headB) // every location is annotated; no gate check, no comment
	located := make([]report.Location, 120)
	for i := range located {
		located[i] = report.Location{File: "pkg/a/a.go", Line: i + 1}
	}
	p.Report.Findings[0].Locations = located

	newer := blockedPublication(t, headB, 8, 1)
	h.checks.afterWrite = func() {
		h.checks.add(api.CheckRun{Name: stateCheck, HeadSHA: headB, ExternalID: externalID(newer.Receipt), Status: statusCompleted,
			Conclusion: stateNeutral, Summary: "newer\n" + receiptMarker(newer.Receipt)})
	}
	if err := h.pub.Publish(ctx, p); err != nil {
		t.Fatalf("Publish: %v", err)
	}
	if got := h.checks.latest(t, headB, stateCheck); got.Summary != "newer\n"+receiptMarker(newer.Receipt) || len(got.Annotations) != 0 {
		t.Errorf("the newer run changed: %d annotations, summary %q", len(got.Annotations), got.Summary)
	}
	if h.checks.count("UpdateCheckRun") != 0 || !slices.Equal(h.checks.annotationRequests, []int{50}) {
		t.Errorf("batches after the newer publication: updates %d, requests %v", h.checks.count("UpdateCheckRun"), h.checks.annotationRequests)
	}
}

// The file list only filters annotations. Its failure never keeps the required check from being written.
func TestChangedFilesErrorStillPublishes(t *testing.T) {
	ctx := context.Background()
	for _, tt := range []struct {
		name    string
		err     error
		wantErr bool
	}{
		{"rate limited", api.ErrRateLimited, true},
		{"other error", errors.New("boom"), false},
	} {
		t.Run(tt.name, func(t *testing.T) {
			h := newHarness(headB)
			h.pulls.changed = []string{"pkg/a/a.go"}
			if err := h.pub.Pending(ctx, prTarget(headB)); err != nil {
				t.Fatalf("Pending: %v", err)
			}
			h.pulls.failOnce("ChangedFiles", tt.err)
			err := h.pub.Publish(ctx, blockedPublication(t, headB, 7, 1))
			if got := errors.Is(err, tt.err); got != tt.wantErr {
				t.Fatalf("Publish error = %v, want returned: %v", err, tt.wantErr)
			}
			gate := h.checks.named(headB, gateCheck)
			if len(gate) != 1 || gate[0].Status != statusCompleted || gate[0].Conclusion != string(verdict.Failure) {
				t.Errorf("gate = %+v, want the pending run completed as failure", gate)
			}
			if state := h.checks.named(headB, stateCheck); len(state) != 1 || len(state[0].Annotations) != 0 {
				t.Errorf("state runs = %+v, want one without annotations", state)
			}
			if len(h.comments.all()) != 1 {
				t.Errorf("comments = %d, want 1", len(h.comments.all()))
			}

			// The retry the Action makes after ErrRateLimited sends the annotations on a new state run.
			if err := h.pub.Publish(ctx, blockedPublication(t, headB, 7, 1)); err != nil {
				t.Fatalf("retry: %v", err)
			}
			if got := len(h.checks.latest(t, headB, stateCheck).Annotations); got != 2 {
				t.Errorf("annotations after retry = %d, want 2", got)
			}
			if len(h.checks.named(headB, gateCheck)) != 1 || len(h.comments.all()) != 1 {
				t.Errorf("the retry duplicated the gate check or the comment")
			}
		})
	}
}

func TestOldRunCannotOverwriteNewerHead(t *testing.T) {
	ctx := context.Background()
	h := newHarness(headB)
	if err := h.pub.Publish(ctx, blockedPublication(t, headB, 7, 1)); err != nil {
		t.Fatalf("Publish: %v", err)
	}
	gateB, stateB := h.checks.named(headB, gateCheck), h.checks.named(headB, stateCheck)
	comment := h.comments.all()

	older := blockedPublication(t, headB, 6, 3)
	older.Outcome = outcome(verdict.Success, reasonHealthy)
	older.Receipt = receiptFor(older.Outcome, 6, 3)
	if err := h.pub.Publish(ctx, older); err != nil {
		t.Fatalf("Publish: %v", err)
	}
	oldHead := blockedPublication(t, headA, 5, 1)
	if err := h.pub.Publish(ctx, oldHead); err != nil {
		t.Fatalf("Publish: %v", err)
	}

	if !reflect.DeepEqual(h.checks.named(headB, gateCheck), gateB) || !reflect.DeepEqual(h.checks.named(headB, stateCheck), stateB) {
		t.Errorf("head B checks changed")
	}
	if h.checks.count("UpdateCheckRun") != 0 {
		t.Errorf("an older run updated a check: %v", h.checks.calls)
	}
	if !reflect.DeepEqual(h.comments.all(), comment) {
		t.Errorf("the comment changed")
	}
	mustContain(t, comment[0].Body, "<!-- archfit:head "+headB+" run 7.1 -->")
	gateA, stateA := h.checks.named(headA, gateCheck), h.checks.named(headA, stateCheck)
	if len(gateA) != 1 || len(stateA) != 1 || gateA[0].ExternalID != "v1:5:1:"+reportDigest {
		t.Errorf("head A did not get its own checks: %+v %+v", gateA, stateA)
	}
}

func TestUsefulWithoutLLM(t *testing.T) {
	cases := map[string]fixtureCase{}
	for _, c := range fixtureCases() {
		cases[c.name] = c
	}
	tests := []struct {
		fixture string
		wants   []string
	}{
		{"healthy", []string{"## Archfit: Passed", reasonHealthy.Next, "No findings reported", "dependency-cruiser", "absent"}},
		{"both-repair-kinds", []string{
			"## Archfit: Blocked by architecture findings", reasonBlocking.Next,
			"no_direct_b_dependency", "`a` (`pkg/a/a.go`) → `b` (`pkg/b`)", "`pkg/a/a.go:3`",
			"Import from pkg/a/** to pkg/b is explicitly forbidden", "Remove the dependency or move the code",
			"archfit check -c .archfit.yaml\n", "archfit check -c .archfit.yaml --base main --lang go --require-tools\n",
		}},
		{"missing-required-evidence", []string{
			"## Archfit: Action required", reasonCoverage.Next,
			"no_direct_b_dependency", "required tool go/packages was absent", "bc/imbalanced_coupling",
		}},
	}
	for _, tt := range tests {
		t.Run(tt.fixture, func(t *testing.T) {
			p := fixturePublication(t, cases[tt.fixture], headB)
			comment := renderComment(p, t0, "")
			mustContain(t, comment, tt.wants...)
			mustContain(t, renderState(p), tt.wants[0], tt.wants[1])
			if gaps := section(comment, headGaps); strings.Contains(gaps, "None reported") {
				t.Errorf("missing data is hidden: %q", gaps)
			}
		})
	}
}

func TestBlockerKeepsMissingRuleReasons(t *testing.T) {
	var c fixtureCase
	for _, fc := range fixtureCases() {
		if fc.name == "blocker-plus-unevaluated" {
			c = fc
		}
	}
	h := newHarness(headB)
	if err := h.pub.Publish(context.Background(), fixturePublication(t, c, headB)); err != nil {
		t.Fatalf("Publish: %v", err)
	}
	gate := h.checks.named(headB, gateCheck)[0]
	if gate.Conclusion != "failure" {
		t.Errorf("gate conclusion = %q, want failure", gate.Conclusion)
	}
	comment := h.comments.all()[0].Body
	state := h.checks.named(headB, stateCheck)[0].Summary
	for _, body := range []string{comment, state, gate.Summary} {
		verdictBlock, _, _ := strings.Cut(body, "\n### ")
		mustContain(t, verdictBlock, "blocking_findings", "missing_required_coverage", "layers/no_upward_imports", reasonCoverage.Next)
	}
	for _, body := range []string{comment, state} {
		mustContain(t, section(body, headGaps), "layers/no_upward_imports", "required tool dependency-cruiser was absent")
	}
}

func TestAcceptedDebtIsDisplayOnly(t *testing.T) {
	var c fixtureCase
	for _, fc := range fixtureCases() {
		if fc.name == "blocked" {
			c = fc
		}
	}
	h := newHarness(headB)
	h.pulls.changed = []string{"pkg/a/a.go"}
	p := fixturePublication(t, c, headB)
	if err := h.pub.Publish(context.Background(), p); err != nil {
		t.Fatalf("Publish: %v", err)
	}
	gate := h.checks.named(headB, gateCheck)[0]
	if gate.Conclusion != "failure" {
		t.Errorf("gate conclusion = %q, want failure", gate.Conclusion)
	}
	mustContain(t, section(gate.Summary, headBlocking), "No active gate finding is listed")
	comment := h.comments.all()[0].Body
	verdictBlock, _, _ := strings.Cut(comment, "\n### ")
	mustContain(t, verdictBlock, "Blocked by architecture findings", "blocking_findings")

	findings := section(comment, headFindings)
	_, debt, ok := strings.Cut(findings, "**Accepted debt — display only**")
	if !ok {
		t.Fatalf("no accepted debt group in %q", findings)
	}
	mustContain(t, debt, "no_direct_b_dependency")
	lower := strings.ToLower(comment)
	mustNotContain(t, lower, "exempt", "clean", "not block", "non-blocking", "ignored", "safe to merge")
	annotations := h.checks.named(headB, stateCheck)[0].Annotations
	if len(annotations) != 1 || annotations[0].Level != "warning" || strings.Contains(annotations[0].Message, "no_direct_b_dependency") {
		t.Errorf("annotations = %+v, want only the live advisory as a warning: accepted debt is not annotated", annotations)
	}
}

func TestSuspensionFeedback(t *testing.T) {
	ctx := context.Background()
	notice := "Trial ends 2026-10-20"

	t.Run("suspended", func(t *testing.T) {
		h := newHarness(headB)
		p := blockedPublication(t, headB, 7, 1)
		p.Outcome = outcome(verdict.ActionRequired, reasonSuspend)
		p.Receipt = receiptFor(p.Outcome, 7, 1)
		p.Trusted.Suspended = true
		p.Trusted.Entitlement = inputs.Entitlement{SuspendAfter: t0.Add(-time.Hour), Reason: reasonSuspend.Next}
		if err := h.pub.Publish(ctx, p); err != nil {
			t.Fatalf("Publish: %v", err)
		}
		state := h.checks.named(headB, stateCheck)[0]
		for _, body := range []string{h.comments.all()[0].Body, state.Summary, h.checks.named(headB, gateCheck)[0].Summary} {
			mustContain(t, body, "subscription_suspended", "subscription", reasonSuspend.Next, headInputs)
			mustNotContain(t, body, headFindings, headDimensions, headGaps, headCommands, headTasks, tasksOpen, "no_direct_b_dependency")
		}
		if len(state.Annotations) != 0 {
			t.Errorf("suspended publication carries %d annotations", len(state.Annotations))
		}
	})

	tests := []struct {
		name     string
		warnFrom time.Time
		want     bool
	}{
		{"warn from in the past", t0.Add(-time.Hour), true},
		{"warn from now", t0, true},
		{"warn from in the future", t0.Add(time.Hour), false},
		{"no warn from", time.Time{}, false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			p := blockedPublication(t, headB, 7, 1)
			p.Trusted.Entitlement = inputs.Entitlement{WarnFrom: tt.warnFrom, Notice: notice}
			comment := renderComment(p, t0, "")
			if got := strings.Contains(comment, notice); got != tt.want {
				t.Errorf("notice shown = %v, want %v", got, tt.want)
			}
			mustContain(t, comment, headFindings)
		})
	}
}

func TestPendingThenFinal(t *testing.T) {
	ctx := context.Background()
	h := newHarness(headB)
	if err := h.pub.Pending(ctx, prTarget(headB)); err != nil {
		t.Fatalf("Pending: %v", err)
	}
	pending := h.checks.named(headB, gateCheck)[0]
	if err := h.pub.Publish(ctx, blockedPublication(t, headB, 7, 1)); err != nil {
		t.Fatalf("Publish: %v", err)
	}
	gate := h.checks.named(headB, gateCheck)
	if len(gate) != 1 {
		t.Fatalf("gate runs = %d, want 1", len(gate))
	}
	if g := gate[0]; g.ID != pending.ID || g.Status != "completed" || g.Conclusion != "failure" || g.ExternalID == "" {
		t.Errorf("gate run = %+v, want the pending run completed as failure", g)
	}
}

package feedback

import (
	"context"
	"strings"
	"testing"

	"github.com/alexei-led/archfit-app/internal/gate/inputs"
	"github.com/alexei-led/archfit-app/internal/gate/verdict"
	"github.com/alexei-led/archfit-app/internal/wire/policy"
	"github.com/alexei-led/archfit-app/internal/wire/settings"
)

func TestDecisionBannerAppearsBeforeReportsAndIsIdempotent(t *testing.T) {
	h := newHarness(headA)
	target := prTarget(headA)
	target.BaseSHA = headB
	trusted := trusted(settings.GateRequired)
	trusted.ProtectedPaths = []string{policy.PolicyPath, settings.Path}
	bannerer := h.pub.(interface {
		DecisionBanner(context.Context, inputs.Target, inputs.Trusted) error
	})
	if err := bannerer.DecisionBanner(context.Background(), target, trusted); err != nil {
		t.Fatal(err)
	}
	comments := h.comments.all()
	if len(comments) != 1 {
		t.Fatalf("comments = %d, want one banner", len(comments))
	}
	body := comments[0].Body
	for _, want := range []string{
		"Architecture decision needed", "Why:", "`.archfit.yaml`", "`.archfit-app.yaml`", "alice",
		"any one current eligible", "not everyone listed", headB, "Gate mode: required", "GitHub's branch rules",
		"approves this exact commit with a GitHub review", "See this pull request in Archfit", "/r/acme/shop/pull/7",
	} {
		if !strings.Contains(body, want) {
			t.Errorf("decision banner lacks %q: %s", want, body)
		}
	}
	// The App page has no owner action since M2 task 24: the banner must not send an owner there to decide.
	for _, gone := range []string{"decide in Archfit App", "decide in this exact Archfit PR page"} {
		if strings.Contains(body, gone) {
			t.Errorf("decision banner still says %q: %s", gone, body)
		}
	}
	if len(h.checks.runs) != 1 || h.checks.runs[0].Name != gateCheck || h.checks.runs[0].Status != statusInProgress ||
		!strings.Contains(h.checks.runs[0].Summary, "alice") || !strings.Contains(h.checks.runs[0].Summary, headB) {
		t.Fatalf("pending gate check = %+v, want owner banner while report is absent", h.checks.runs)
	}
	if err := bannerer.DecisionBanner(context.Background(), target, trusted); err != nil {
		t.Fatal(err)
	}
	if len(h.comments.all()) != 1 || len(h.checks.runs) != 1 {
		t.Fatalf("retry duplicated the decision banner/check: comments=%d checks=%d", len(h.comments.all()), len(h.checks.runs))
	}
}

func TestFirstResultReusesTheNoReportDecisionComment(t *testing.T) {
	h := newHarness(headA)
	target := prTarget(headA)
	target.BaseSHA = headB
	tr := trusted(settings.GateRequired)
	tr.ProtectedPaths = []string{policy.BaselinePath}
	bannerer := h.pub.(interface {
		DecisionBanner(context.Context, inputs.Target, inputs.Trusted) error
	})
	if err := bannerer.DecisionBanner(context.Background(), target, tr); err != nil {
		t.Fatal(err)
	}
	before := h.comments.all()
	if len(before) != 1 {
		t.Fatalf("banner comments = %d", len(before))
	}
	readsBefore := h.comments.count("AppComments")
	out := verdict.Without(verdict.ReasonMissingUpload)
	pub := Publication{Target: target, Outcome: out, Trusted: tr, Receipt: Receipt{
		RunID: 1, RunAttempt: 1, ReportDigest: reportDigest, Conclusion: out.Conclusion, Reason: verdict.ReasonMissingUpload,
	}}
	if err := h.pub.Publish(context.Background(), pub); err != nil {
		t.Fatal(err)
	}
	comments := h.comments.all()
	if len(comments) != 1 || !strings.Contains(comments[0].Body, "Architecture decision needed") || !strings.Contains(comments[0].Body, commentMarker) {
		t.Fatalf("first result duplicated/lost the banner comment: %+v", comments)
	}
	if got := h.comments.count("AppComments"); got != readsBefore+1 {
		t.Fatalf("comment reads after publication = %d, want one additional read", got-readsBefore)
	}
}

func TestAdvisoryDecisionBannerSaysItDoesNotBlock(t *testing.T) {
	h := newHarness(headA)
	target := prTarget(headA)
	target.BaseSHA = headB
	tr := trusted(settings.GateAdvisory)
	tr.ProtectedPaths = []string{policy.BaselinePath}
	bannerer := h.pub.(interface {
		DecisionBanner(context.Context, inputs.Target, inputs.Trusted) error
	})
	if err := bannerer.DecisionBanner(context.Background(), target, tr); err != nil {
		t.Fatal(err)
	}
	if got := h.comments.all(); len(got) != 1 || !strings.Contains(got[0].Body, "does not prevent merging") {
		t.Fatalf("advisory banner = %+v", got)
	}
	if len(h.checks.runs) != 0 {
		t.Fatalf("advisory banner created a required check: %+v", h.checks.runs)
	}
}

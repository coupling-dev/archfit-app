package feedback

import (
	"reflect"
	"strings"
	"testing"
	"time"
	"unicode/utf8"

	"github.com/alexei-led/archfit-app/internal/gate/inputs"
	"github.com/alexei-led/archfit-app/internal/wire/policy"
	"github.com/alexei-led/archfit-app/internal/wire/settings"
)

func TestOwnerBannerPreservesCommentOrderingWaiversAndIdempotency(t *testing.T) {
	h := newHarness(headB)
	p := blockedPublication(t, headB, 7, 1)
	p.Trusted.ProtectedPaths = []string{policy.PolicyPath}
	p.Trusted.EarliestWaiverExpiry = t0.Add(24 * time.Hour)
	if err := h.pub.Publish(t.Context(), p); err != nil {
		t.Fatal(err)
	}
	if err := h.pub.Warn(t.Context(), p.Target, p.Trusted.EarliestWaiverExpiry); err != nil {
		t.Fatal(err)
	}
	before := h.comments.all()
	if len(before) != 1 {
		t.Fatalf("waiver duplicated a protected-path comment: %d", len(before))
	}
	if err := h.pub.Publish(t.Context(), p); err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(h.comments.all(), before) {
		t.Fatal("identical publication lost waiver or changed comment")
	}
	p.Receipt.RunID--
	if err := h.pub.Publish(t.Context(), p); err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(h.comments.all(), before) {
		t.Fatal("older publication overwrote newer owner-banner result")
	}
}

func TestOwnerBannerSharesTheGitHubBodyBudget(t *testing.T) {
	h := newHarness(headB)
	p := blockedPublication(t, headB, 7, 1)
	p.Report = stressReport()
	p.Trusted.ProtectedPaths = []string{policy.PolicyPath}
	if err := h.pub.Publish(t.Context(), p); err != nil {
		t.Fatal(err)
	}
	for _, run := range h.checks.runs {
		if len(run.Summary) > maxBody || !utf8.ValidString(run.Summary) {
			t.Fatalf("check body = %d bytes", len(run.Summary))
		}
	}
	if err := h.pub.Warn(t.Context(), p.Target, t0.Add(24*time.Hour)); err != nil {
		t.Fatal(err)
	}
	for _, comment := range h.comments.all() {
		if len(comment.Body) > maxBody || !utf8.ValidString(comment.Body) {
			t.Fatalf("comment body = %d bytes", len(comment.Body))
		}
	}
}

func TestOwnerBannerReusesALegacyResultComment(t *testing.T) {
	h := newHarness(headB)
	p := blockedPublication(t, headB, 7, 1)
	if err := h.pub.Publish(t.Context(), p); err != nil {
		t.Fatal(err)
	}
	p.Trusted.ProtectedPaths = []string{policy.PolicyPath}
	if err := h.pub.DecisionBanner(t.Context(), p.Target, p.Trusted); err != nil {
		t.Fatal(err)
	}
	if len(h.comments.all()) != 1 {
		t.Fatal("upgrading a legacy result created a second comment")
	}
	if err := h.pub.Publish(t.Context(), p); err != nil {
		t.Fatal(err)
	}
	if len(h.comments.all()) != 1 {
		t.Fatal("publication after upgrade duplicated the banner")
	}
}

func TestOwnerBannerNamesTheEffectiveDecision(t *testing.T) {
	for _, state := range []string{"approved", "changes requested"} {
		t.Run(state, func(t *testing.T) {
			tr := trusted(settings.GateRequired)
			tr.ProtectedPaths = []string{policy.PolicyPath}
			if state == "approved" {
				tr.Approval, tr.ApprovedBy = inputs.ApprovalGranted, "alice"
			} else {
				tr.Objections = []inputs.Objection{{ID: "app:1", Login: "alice", Reason: "Separate the modules."}}
			}
			body := renderDecisionBanner(prTarget(headB), tr, appURL)
			if strings.Contains(body, "## Architecture decision needed") || !strings.Contains(strings.ToLower(body), state) || !strings.Contains(body, "alice") {
				t.Fatalf("wrong owner decision banner: %s", body)
			}
		})
	}
}

func TestPreReportBannerOnALargeLegacyCommentStaysBounded(t *testing.T) {
	h := newHarness(headB)
	p := blockedPublication(t, headB, 7, 1)
	p.Report = stressReport()
	if err := h.pub.Publish(t.Context(), p); err != nil {
		t.Fatal(err)
	}
	p.Trusted.ProtectedPaths = []string{policy.PolicyPath}
	if err := h.pub.DecisionBanner(t.Context(), p.Target, p.Trusted); err != nil {
		t.Fatal(err)
	}
	if err := h.pub.Warn(t.Context(), p.Target, t0.Add(24*time.Hour)); err != nil {
		t.Fatal(err)
	}
	comments := h.comments.all()
	if len(comments) != 1 || len(comments[0].Body) > maxBody || !utf8.ValidString(comments[0].Body) {
		t.Fatal("legacy upgrade lost the single bounded comment")
	}
	if head, run, _, ok := parseHead(comments[0].Body); !ok || head != headB || run != 7 {
		t.Fatal("legacy upgrade lost publication ordering")
	}
}

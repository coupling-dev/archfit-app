package feedback

import (
	"bytes"
	"context"
	"errors"
	"log/slog"
	"reflect"
	"regexp"
	"strings"
	"testing"

	"github.com/alexei-led/archfit-app/internal/gate/verdict"
	"github.com/alexei-led/archfit-app/internal/github/api"
	"github.com/alexei-led/archfit-app/internal/wire/settings"
)

var banned = regexp.MustCompile(`(?i)verified|score|grade|%`)

// scrub masks the banned words inside engine-authored strings, so that a scan finds App-authored text only.
func scrub(v reflect.Value) {
	switch v.Kind() {
	case reflect.Pointer:
		if !v.IsNil() {
			scrub(v.Elem())
		}
	case reflect.Struct:
		for i := range v.NumField() {
			scrub(v.Field(i))
		}
	case reflect.Slice:
		for i := range v.Len() {
			scrub(v.Index(i))
		}
	case reflect.Map:
		for _, k := range v.MapKeys() {
			e := reflect.New(v.Type().Elem()).Elem()
			e.Set(v.MapIndex(k))
			scrub(e)
			v.SetMapIndex(k, e)
		}
	case reflect.String:
		v.SetString(banned.ReplaceAllString(v.String(), "~"))
	}
}

func TestNoVerifiedNoScore(t *testing.T) {
	for _, c := range fixtureCases() {
		t.Run(c.name, func(t *testing.T) {
			p := fixturePublication(t, c, headB)
			scrub(reflect.ValueOf(p.Report))
			texts := map[string]string{
				"comment":     renderComment(p, t0, "2026-10-01"),
				"state":       renderState(p),
				"gate":        renderGate(p),
				"gate title":  gateTitle(p),
				"state title": stateTitle(p),
				"pending":     pendingTitle + " " + pendingSummary,
			}
			for name, text := range texts {
				if m := banned.FindString(text); m != "" {
					t.Errorf("%s contains %q", name, m)
				}
			}
			for _, name := range []string{"comment", "state", "gate"} {
				mustContain(t, texts[name], "customer-attested")
			}
		})
	}
}

func TestGateCheckNeverNeutral(t *testing.T) {
	conclusions := map[verdict.ReasonCode]verdict.Conclusion{
		verdict.ReasonHealthy:               verdict.Success,
		verdict.ReasonAttentionOnly:         verdict.Success,
		verdict.ReasonBlockingFindings:      verdict.Failure,
		verdict.ReasonEngineError:           verdict.ActionRequired,
		verdict.ReasonMissingCoverage:       verdict.ActionRequired,
		verdict.ReasonBaselineNotComparable: verdict.ActionRequired,
		verdict.ReasonBaselineMismatch:      verdict.ActionRequired,
		verdict.ReasonLabelsMismatch:        verdict.ActionRequired,
		verdict.ReasonPolicyMismatch:        verdict.ActionRequired,
		verdict.ReasonPolicyMissing:         verdict.ActionRequired,
		verdict.ReasonPolicyDefect:          verdict.ActionRequired,
		verdict.ReasonUnknownIdentity:       verdict.ActionRequired,
		verdict.ReasonProfileMissing:        verdict.ActionRequired,
		verdict.ReasonWorkflowNotApproved:   verdict.ActionRequired,
		verdict.ReasonOwnerApprovalMissing:  verdict.ActionRequired,
		verdict.ReasonSettingsMissing:       verdict.ActionRequired,
		verdict.ReasonForkUnsupported:       verdict.ActionRequired,
		verdict.ReasonUnsupportedEvent:      verdict.ActionRequired,
		verdict.ReasonEnvelopeMismatch:      verdict.ActionRequired,
		verdict.ReasonReportInvalid:         verdict.ActionRequired,
		verdict.ReasonMissingUpload:         verdict.ActionRequired,
		verdict.ReasonStaleInputs:           verdict.ActionRequired,
		verdict.ReasonWaiverExpired:         verdict.Failure,
		verdict.ReasonSuspended:             verdict.ActionRequired,
	}
	allowed := map[string]bool{"success": true, "failure": true, "action_required": true}
	for code, conclusion := range conclusions {
		for _, withReport := range []bool{true, false} {
			t.Run(string(code), func(t *testing.T) {
				h := newHarness(headB)
				category := verdict.CategoryEvidence
				if code == verdict.ReasonSuspended {
					category = verdict.CategorySubscription
				}
				o := outcome(conclusion, verdict.Reason{Code: code, Category: category, Detail: "id 1", Next: "Do the next thing."})
				p := Publication{Target: prTarget(headB), Outcome: o, Receipt: receiptFor(o, 7, 1), Trusted: trusted(settings.GateRequired)}
				if withReport {
					p.Report = loadReport(t, "attention-warning-only")
				}
				if err := h.pub.Publish(context.Background(), p); err != nil {
					t.Fatalf("Publish: %v", err)
				}
				gate := h.checks.named(headB, gateCheck)
				if len(gate) != 1 || !allowed[gate[0].Conclusion] || gate[0].Conclusion != string(conclusion) {
					t.Errorf("gate runs = %+v, want one with conclusion %q", gate, conclusion)
				}
				// Fail closed on the advisory check too: an outcome that asks for action is never neutral there.
				if state := h.checks.named(headB, stateCheck); conclusion == verdict.ActionRequired && state[0].Conclusion != string(verdict.ActionRequired) {
					t.Errorf("state conclusion = %q for an outcome that asks for action", state[0].Conclusion)
				}
			})
		}
	}
}

func TestRateLimitLeavesPendingAlone(t *testing.T) {
	ctx := context.Background()
	tests := []struct {
		name        string
		fail        func(h *harness)
		pendingKept bool
	}{
		{"CheckRuns", func(h *harness) { h.checks.failOnce("CheckRuns", api.ErrRateLimited) }, true},
		{"ChangedFiles", func(h *harness) { h.pulls.failOnce("ChangedFiles", api.ErrRateLimited) }, true},
		{"UpdateCheckRun", func(h *harness) { h.checks.failOnce("UpdateCheckRun", api.ErrRateLimited) }, true},
		{"CreateComment", func(h *harness) { h.comments.failOnce("CreateComment", api.ErrRateLimited) }, false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			h := newHarness(headB)
			if err := h.pub.Pending(ctx, prTarget(headB)); err != nil {
				t.Fatalf("Pending: %v", err)
			}
			pending := h.checks.named(headB, gateCheck)[0]
			p := blockedPublication(t, headB, 7, 1)

			tt.fail(h)
			if err := h.pub.Publish(ctx, p); !errors.Is(err, api.ErrRateLimited) {
				t.Fatalf("Publish error = %v, want ErrRateLimited", err)
			}
			if got := h.checks.named(headB, gateCheck); tt.pendingKept && !reflect.DeepEqual(got, []api.CheckRun{pending}) {
				t.Errorf("the pending check changed: %+v", got)
			}
			for _, r := range h.checks.runs {
				if r.Conclusion == "success" {
					t.Errorf("a failed publication produced green: %+v", r)
				}
			}

			if err := h.pub.Publish(ctx, p); err != nil {
				t.Fatalf("retry: %v", err)
			}
			gate, state := h.checks.named(headB, gateCheck), h.checks.named(headB, stateCheck)
			if len(gate) != 1 || len(state) != 1 || len(h.comments.all()) != 1 {
				t.Fatalf("after retry: %d gate, %d state, %d comments; want one each", len(gate), len(state), len(h.comments.all()))
			}
			if gate[0].ID != pending.ID || gate[0].Status != "completed" || gate[0].Conclusion != "failure" {
				t.Errorf("gate after retry = %+v", gate[0])
			}
		})
	}
}

func TestSummaryLimitIsRespected(t *testing.T) {
	ctx := context.Background()
	var pubs []Publication
	for _, c := range fixtureCases() {
		pubs = append(pubs, fixturePublication(t, c, headB))
	}
	stress := blockedPublication(t, headB, 7, 1)
	stress.Report = stressReport()
	forged := blockedPublication(t, headB, 7, 1)
	forged.Report = evilReport()
	pubs = append(pubs, stress, forged)

	for _, p := range pubs {
		h := newHarness(headB)
		if err := h.pub.Publish(ctx, p); err != nil {
			t.Errorf("Publish rejected: %v", err)
		}
		if err := h.pub.Warn(ctx, p.Target, t0.AddDate(0, 0, 10)); err != nil {
			t.Errorf("Warn: %v", err)
		}
		for _, c := range h.comments.all() {
			if len(c.Body) > maxBody {
				t.Errorf("comment is %d bytes", len(c.Body))
			}
		}
	}
}

func TestLogsCarryOnlyIdentifiers(t *testing.T) {
	var buf bytes.Buffer
	old := slog.Default()
	slog.SetDefault(slog.New(slog.NewJSONHandler(&buf, &slog.HandlerOptions{Level: slog.LevelDebug})))
	t.Cleanup(func() { slog.SetDefault(old) })

	ctx := context.Background()
	h := newHarness(headB)
	o := outcome(verdict.Failure, verdict.Reason{Code: verdict.ReasonBlockingFindings, Category: verdict.CategoryArchitecture, Detail: evil, Next: evil})
	p := blockedPublication(t, headB, 7, 1)
	p.Outcome = o
	p.Report = evilReport()
	p.Report.Findings[0].Locations[0].File = "secret/path.go"
	p.Report.Findings[0].Why += " secret message"

	if err := h.pub.Pending(ctx, p.Target); err != nil {
		t.Fatalf("Pending: %v", err)
	}
	if err := h.pub.Publish(ctx, p); err != nil {
		t.Fatalf("Publish: %v", err)
	}
	if err := h.pub.Warn(ctx, p.Target, t0.AddDate(0, 0, 10)); err != nil {
		t.Fatalf("Warn: %v", err)
	}
	h.comments.failOnce("AppComments", api.ErrRateLimited)
	_ = h.pub.Publish(ctx, p)

	logs := buf.String()
	mustNotContain(t, logs, "archfit:", "<!--", "tick", "bold", "secret", "acme/shop", "Waiting")
	mustContain(t, logs, headB, reportDigest, policyDigest, `"repo_id":42`, `"run_id":7`, "blocking_findings", "failure", "created", "updated")
}

func TestForeignCommentsAreIgnored(t *testing.T) {
	ctx := context.Background()
	h := newHarness(headB)
	welcome := "Thanks for installing Archfit."
	quoting := "> " + commentMarker + "\nquoted by the App in a reply"
	h.comments.comments = []api.Comment{{ID: 100, Body: welcome}, {ID: 101, Body: quoting}}
	h.comments.lastID = 101

	for attempt := 1; attempt <= 2; attempt++ {
		if err := h.pub.Publish(ctx, blockedPublication(t, headB, 7, attempt)); err != nil {
			t.Fatalf("Publish: %v", err)
		}
	}
	if err := h.pub.Warn(ctx, prTarget(headB), t0.AddDate(0, 0, 10)); err != nil {
		t.Fatalf("Warn: %v", err)
	}
	all := h.comments.all()
	if len(all) != 3 || all[0].Body != welcome || all[1].Body != quoting {
		t.Fatalf("foreign comments were touched: %d comments", len(all))
	}
	if !strings.HasPrefix(all[2].Body, commentMarker+"\n") || !strings.Contains(all[2].Body, "run 7.2") || !strings.Contains(all[2].Body, waiverOpen) {
		t.Errorf("the marked comment was not created and maintained: %.120q", all[2].Body)
	}
	if h.comments.count("CreateComment") != 1 {
		t.Errorf("creates = %d, want 1", h.comments.count("CreateComment"))
	}
}

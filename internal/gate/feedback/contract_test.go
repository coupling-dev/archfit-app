package feedback

import (
	"context"
	"flag"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/alexei-led/archfit-app/internal/gate/inputs"
	"github.com/alexei-led/archfit-app/internal/gate/verdict"
	"github.com/alexei-led/archfit-app/internal/wire/settings"
)

var update = flag.Bool("update", false, "rewrite the golden files")

func TestPublishMatrix(t *testing.T) {
	noSettings := trusted(settings.GateRequired)
	noSettings.SettingsFound = false
	tests := []struct {
		name        string
		target      inputs.Target
		trusted     inputs.Trusted
		wantGate    int
		wantComment int
	}{
		{"pull request, required", prTarget(headB), trusted(settings.GateRequired), 1, 1},
		{"pull request, advisory", prTarget(headB), trusted(settings.GateAdvisory), 0, 1},
		{"pull request, settings missing", prTarget(headB), noSettings, 0, 0},
		{"push", pushTarget(headB), trusted(settings.GateRequired), 0, 0},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			h := newHarness(headB)
			p := blockedPublication(t, headB, 7, 1)
			p.Target, p.Trusted = tt.target, tt.trusted
			if err := h.pub.Publish(context.Background(), p); err != nil {
				t.Fatalf("Publish: %v", err)
			}
			state := h.checks.named(headB, "archfit/state")
			if len(state) != 1 || state[0].Status != "completed" || state[0].Conclusion != "neutral" {
				t.Errorf("archfit/state runs = %+v, want one completed neutral run", state)
			}
			gate := h.checks.named(headB, "archfit/gate")
			if len(gate) != tt.wantGate {
				t.Fatalf("archfit/gate runs = %d, want %d", len(gate), tt.wantGate)
			}
			if tt.wantGate == 1 && (gate[0].Conclusion != "failure" || gate[0].ExternalID != "v1:7:1:"+reportDigest || !gate[0].StartedAt.Equal(t0)) {
				t.Errorf("gate run = %+v", gate[0])
			}
			comments := h.comments.all()
			if len(comments) != tt.wantComment {
				t.Fatalf("comments = %d, want %d", len(comments), tt.wantComment)
			}
			if tt.wantComment == 1 && !strings.HasPrefix(comments[0].Body, "<!-- archfit:comment -->\n<!-- archfit:head "+headB+" run 7.1 -->\n") {
				t.Errorf("comment does not start with the markers: %.120q", comments[0].Body)
			}
			if got := len(h.checks.runs); got != 1+tt.wantGate {
				t.Errorf("check runs = %d, want %d", got, 1+tt.wantGate)
			}
		})
	}
}

func TestPendingIsIdempotent(t *testing.T) {
	ctx := context.Background()
	h := newHarness(headB)
	for range 2 {
		if err := h.pub.Pending(ctx, prTarget(headB)); err != nil {
			t.Fatalf("Pending: %v", err)
		}
	}
	runs := h.checks.named(headB, gateCheck)
	if len(runs) != 1 || h.checks.count("CreateCheckRun") != 1 {
		t.Fatalf("got %d runs and %d creates, want 1 and 1", len(runs), h.checks.count("CreateCheckRun"))
	}
	if r := runs[0]; r.Status != "in_progress" || r.ExternalID != "" || r.Conclusion != "" || !r.StartedAt.Equal(t0) || !strings.Contains(r.Summary, "Waiting for the Archfit report.") {
		t.Errorf("pending run = %+v", r)
	}

	done := newHarness(headB)
	if err := done.pub.Publish(ctx, blockedPublication(t, headB, 7, 1)); err != nil {
		t.Fatalf("Publish: %v", err)
	}
	before := done.checks.named(headB, gateCheck)[0]
	if err := done.pub.Pending(ctx, prTarget(headB)); err != nil {
		t.Fatalf("Pending: %v", err)
	}
	after := done.checks.named(headB, gateCheck)
	if len(after) != 1 || after[0].Summary != before.Summary || after[0].Status != "completed" {
		t.Errorf("Pending wrote over a completed check: %+v", after)
	}
	if done.checks.count("CreateCheckRun") != 2 || done.checks.count("UpdateCheckRun") != 0 {
		t.Errorf("Pending wrote: calls = %v", done.checks.calls)
	}
}

func TestStateReadsBack(t *testing.T) {
	ctx := context.Background()
	h := newHarness(headB)
	target := prTarget(headB)

	got, err := h.pub.State(ctx, target)
	if err != nil || got != (CheckState{}) {
		t.Fatalf("State with no check = %+v, %v; want zero", got, err)
	}

	if err := h.pub.Pending(ctx, target); err != nil {
		t.Fatalf("Pending: %v", err)
	}
	got, err = h.pub.State(ctx, target)
	if err != nil || !got.Exists || !got.Pending || !got.StartedAt.Equal(t0) || got.Receipt != (Receipt{}) {
		t.Fatalf("State of a pending check = %+v, %v", got, err)
	}

	published := t0.Add(5 * time.Minute)
	h.clock.set(published)
	p := blockedPublication(t, headB, 7, 1)
	p.Receipt.EarliestWaiverExpiry = time.Date(2026, 10, 1, 0, 0, 0, 0, time.UTC)
	if err := h.pub.Publish(ctx, p); err != nil {
		t.Fatalf("Publish: %v", err)
	}
	got, err = h.pub.State(ctx, target)
	if err != nil || !got.Exists || got.Pending || !got.StartedAt.Equal(published) || got.Receipt != p.Receipt {
		t.Fatalf("State of a published check = %+v, %v; want receipt %+v", got, err, p.Receipt)
	}
}

func TestAdvisoryStateReadsTheStateCheck(t *testing.T) {
	ctx := context.Background()
	h := newHarness(headB)
	target := prTarget(headB)
	p := blockedPublication(t, headB, 7, 1)
	p.Trusted.Settings.Gate = settings.GateAdvisory
	if err := h.pub.Publish(ctx, p); err != nil {
		t.Fatalf("Publish: %v", err)
	}
	if got, err := h.pub.State(ctx, target); err != nil || got.Exists {
		t.Fatalf("State of an advisory head = %+v, %v; want no gate check", got, err)
	}
	got, err := h.pub.AdvisoryState(ctx, target)
	if err != nil || !got.Exists || got.Pending || got.Receipt != p.Receipt {
		t.Fatalf("AdvisoryState = %+v, %v; want receipt %+v", got, err, p.Receipt)
	}
}

func TestWarnIsIdempotent(t *testing.T) {
	ctx := context.Background()
	h := newHarness(headB)
	target := prTarget(headB)
	first := time.Date(2026, 10, 1, 15, 0, 0, 0, time.UTC)
	second := time.Date(2026, 11, 5, 0, 0, 0, 0, time.UTC)
	writes := func() int { return h.comments.count("CreateComment") + h.comments.count("UpdateComment") }

	if err := h.pub.Warn(ctx, target, first); err != nil {
		t.Fatalf("Warn: %v", err)
	}
	if c := h.comments.all(); len(c) != 1 || !strings.HasPrefix(c[0].Body, commentMarker+"\n") || !strings.Contains(c[0].Body, "<!-- archfit:waiver 2026-10-01 -->") || !strings.Contains(c[0].Body, "<!-- /archfit:waiver -->") {
		t.Fatalf("after first Warn: %+v", c)
	}

	if err := h.pub.Warn(ctx, target, first.Add(time.Hour)); err != nil {
		t.Fatalf("Warn: %v", err)
	}
	if writes() != 1 {
		t.Errorf("Warn with the same date wrote again: %v", h.comments.calls)
	}

	if err := h.pub.Warn(ctx, target, second); err != nil {
		t.Fatalf("Warn: %v", err)
	}
	body := h.comments.all()[0].Body
	if writes() != 2 || !strings.Contains(body, "archfit:waiver 2026-11-05") || strings.Contains(body, "2026-10-01") || strings.Count(body, waiverClose) != 1 {
		t.Errorf("section was not replaced: %q", body)
	}

	p := blockedPublication(t, headB, 7, 1)
	p.Trusted.EarliestWaiverExpiry = second
	if err := h.pub.Publish(ctx, p); err != nil {
		t.Fatalf("Publish: %v", err)
	}
	body = h.comments.all()[0].Body
	if !strings.Contains(body, waiverSection("2026-11-05")) || !strings.Contains(body, "archfit:head") {
		t.Errorf("matching expiry: the section was not kept: %.300q", body)
	}
	if i, j := strings.Index(body, waiverOpen), strings.Index(body, "<details>"); i < 0 || j < 0 || i > j {
		t.Errorf("the section is not before the folds")
	}

	// Warn on a published comment inserts at the same place Publish uses.
	if err := h.pub.Warn(ctx, target, first); err != nil {
		t.Fatalf("Warn: %v", err)
	}
	p.Trusted.EarliestWaiverExpiry = first
	before := writes()
	if err := h.pub.Publish(ctx, p); err != nil {
		t.Fatalf("Publish: %v", err)
	}
	if writes() != before {
		t.Errorf("Publish after Warn rewrote an identical comment")
	}

	p.Trusted.EarliestWaiverExpiry = first.AddDate(0, 1, 0)
	if err := h.pub.Publish(ctx, p); err != nil {
		t.Fatalf("Publish: %v", err)
	}
	if body = h.comments.all()[0].Body; strings.Contains(body, "archfit:waiver") {
		t.Errorf("non-matching expiry: the section was not dropped")
	}
	if len(h.comments.all()) != 1 {
		t.Errorf("comments = %d, want 1", len(h.comments.all()))
	}

	push := newHarness(headB)
	if err := push.pub.Warn(ctx, pushTarget(headB), first); err != nil {
		t.Fatalf("Warn on push: %v", err)
	}
	if len(push.comments.calls) != 0 {
		t.Errorf("Warn on a push target called %v", push.comments.calls)
	}
}

func golden(t *testing.T, name, got string) {
	t.Helper()
	path := filepath.Join("testdata", "golden", name+".md")
	if *update {
		if err := os.WriteFile(path, []byte(got), 0o644); err != nil {
			t.Fatalf("write golden: %v", err)
		}
		return
	}
	want, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read golden (run with -update to create): %v", err)
	}
	if got != string(want) {
		t.Errorf("%s differs from its golden file; run with -update and review the diff", name)
	}
}

func TestGoldenFeedback(t *testing.T) {
	dimensionOrder := []string{"intent", "structure", "modularity", "coupling", "change_locality", "complexity", "testability", "operations", "drift"}
	for _, c := range fixtureCases() {
		t.Run(c.name, func(t *testing.T) {
			// Expected display excludes decoded claims whose structured reasons reject their source authority.
			p := presentation(fixturePublication(t, c, headB))
			waiver := ""
			if !c.waiver.IsZero() {
				waiver = c.waiver.Format(time.DateOnly)
			}
			comment := renderComment(p, t0, waiver, "")
			state := renderState(p)
			gate := renderGate(p)
			golden(t, c.name+".comment", comment)
			golden(t, c.name+".state", state)
			golden(t, c.name+".gate", gate)

			lines := strings.Split(comment, "\n")
			if lines[0] != commentMarker || !strings.HasPrefix(lines[1], headOpen) || !strings.HasPrefix(lines[2], "## Archfit: ") {
				t.Errorf("comment does not open with markers and verdict: %q", lines[:3])
			}
			first := p.Outcome.Reasons[0]
			verdictEnd := strings.Index(comment, "\n### ")
			if first.Code == verdict.ReasonBlockingFindings {
				mustContain(t, comment[:verdictEnd], "PR author/coding agent:", "supplied validation commands")
			} else {
				mustContain(t, comment[:verdictEnd], first.Next)
			}
			mustNotContain(t, comment[:verdictEnd], "`"+string(first.Code)+"`")
			mustContain(t, section(state, headDecision), string(first.Code), string(first.Category), first.Detail)
			mustContain(t, state[:strings.Index(state+"\n### ", "\n### ")], string(first.Code), first.Detail, first.Next)
			for _, body := range []string{comment, state} {
				if section(body, headGaps) == "" {
					t.Errorf("no evidence-gap section")
				}
			}
			mustNotContain(t, comment, "--refresh")
			if p.Report == nil {
				mustContain(t, comment, "No usable report")
				return
			}

			mustNotContain(t, comment, headDimensions, "```mermaid")
			dims := section(state, headDimensions)
			at := -1
			for _, key := range dimensionOrder {
				i := strings.Index(dims, "| `"+key+"` |")
				if i <= at {
					t.Errorf("dimension %s is missing or out of order", key)
				}
				at = i
			}

			for _, body := range []string{comment, state} {
				tasks := decodeTasks(t, body)
				if len(tasks) != len(p.Report.AgentTasks) {
					t.Fatalf("task block holds %d tasks, want %d", len(tasks), len(p.Report.AgentTasks))
				}
				commands := section(body, headCommands)
				for i, task := range tasks {
					src := p.Report.AgentTasks[i]
					if len(task) != 8 || task["repair_kind"] != src.RepairKind || task["origin"] != src.Origin {
						t.Errorf("task %d = %v, want eight keys with repair_kind %q", i, task, src.RepairKind)
					}
					if task["goal"] != src.Goal || task["finding_id"] != src.FindingID || task["rule_id"] != src.RuleID {
						t.Errorf("task identity or goal changed: %v", task)
					}
					for key, expected := range map[string][]string{"constraints": src.Constraints, "files": src.Files, "validation": src.Validation} {
						values, ok := task[key].([]any)
						if !ok || len(values) != len(expected) {
							t.Fatalf("task %s = %v, want %v", key, task[key], expected)
						}
						for j, value := range expected {
							if values[j] != value {
								t.Errorf("task %s[%d] changed", key, j)
							}
						}
					}
					if body == state {
						for _, cmd := range src.Validation {
							if strings.Count(commands, "\n"+cmd+"\n") != 1 {
								t.Errorf("validation command %q is not printed exactly once", cmd)
							}
						}
					}
				}
				if len(tasks) == 0 && commands != "" {
					t.Errorf("validation block without tasks")
				}
			}
		})
	}

	both := renderComment(fixturePublication(t, fixtureCases()[11], headB), t0, "", "")
	mustContain(t, both, `"repair_kind":"code_change"`, `"repair_kind":"needs_owner_decision"`, "--base main --lang go --require-tools")
}

func TestCommentLinksToTheAppPullPage(t *testing.T) {
	ctx := context.Background()
	h := newHarness(headB)
	if err := h.pub.Publish(ctx, blockedPublication(t, headB, 7, 1)); err != nil {
		t.Fatalf("Publish: %v", err)
	}
	body := h.comments.all()[0].Body
	mustContain(t, body, "[Details in Archfit](https://app.example/r/acme/shop/pull/7)")
	if i, j := strings.Index(body, "Details in Archfit"), strings.Index(body, "<details>"); i < 0 || i > j {
		t.Errorf("the link is not in the visible part, before the folds")
	}

	noticed := blockedPublication(t, headB, 7, 1)
	noticed.Trusted.Entitlement = inputs.Entitlement{WarnFrom: t0.Add(-time.Hour), Notice: "Trial ends 2026-10-20"}
	head := renderComment(noticed, t0, "", "https://app.example/r/acme/shop/pull/7")
	if i, j := strings.Index(head, "Trial ends"), strings.Index(head, "Details in Archfit"); i < 0 || j < i {
		t.Errorf("the link is not after the entitlement notice:\n%s", head)
	}

	details := func(name, when string, h *harness) {
		t.Helper()
		if got := h.checks.latest(t, headB, name).DetailsURL; got != "https://app.example/r/acme/shop/pull/7" {
			t.Errorf("%s %s: DetailsURL = %q, want the App pull request page", when, name, got)
		}
	}
	h = newHarness(headB)
	p := blockedPublication(t, headB, 7, 1)
	if err := h.pub.Pending(ctx, p.Target); err != nil {
		t.Fatalf("Pending: %v", err)
	}
	details(gateCheck, "pending", h)
	if err := h.pub.Publish(ctx, p); err != nil {
		t.Fatalf("Publish: %v", err)
	}
	details(gateCheck, "published", h)
	details(stateCheck, "published", h)

	tests := []struct {
		name, appURL, fullName, want string
		pull                         int
	}{
		{"no App URL", "", "acme/shop", "", 7},
		{"a push has no pull request page", "https://app.example", "acme/shop", "", 0},
		{"names are path-escaped", "https://app.example", "acme/a b?", "https://app.example/r/acme/a%20b%3F/pull/7", 7},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			p := &publisher{appURL: tt.appURL}
			target := prTarget(headB)
			target.Repo.FullName, target.PullRequest = tt.fullName, tt.pull
			if got := p.pullLink(target); got != tt.want {
				t.Errorf("pullLink = %q, want %q", got, tt.want)
			}
		})
	}
}

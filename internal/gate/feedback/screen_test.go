package feedback

import (
	"strings"
	"testing"
	"time"

	"github.com/alexei-led/archfit-app/internal/gate/verdict"
)

// inputOf is what the pull request page reads of a published result: the state check's title, reasons and summary.
func inputOf(p Publication) ScreenInput {
	in := ScreenInput{
		Repo: p.Target.Repo.FullName, HeadSHA: p.Target.HeadSHA, PullRequest: p.Target.PullRequest,
		Title: stateTitle(p), CheckID: p.stateCheckID, Required: required(p),
		EngineVersion: p.Identity.EngineVersion, PolicyDigest: p.Receipt.PolicyDigest, BaselineDigest: baselineWord(p),
		RunID: p.Receipt.RunID, RunAttempt: p.Receipt.RunAttempt, PolicyCard: cardOf(renderState(presentation(p))),
		Report: p.Report,
	}
	for _, r := range p.Outcome.Reasons {
		in.Reasons = append(in.Reasons, ScreenReason{string(r.Code), string(r.Category), r.Detail, r.Next})
	}
	return in
}

// baselineWord is the baseline digest as the inputs section of the state check words it.
func baselineWord(p Publication) string {
	if p.Receipt.BaselineDigest == "" {
		return noBaseline
	}
	return p.Receipt.BaselineDigest + " (stated by the producer)"
}

// cardOf is the policy change section of a check summary.
func cardOf(summary string) string {
	_, rest, ok := strings.Cut(summary, headPolicyChange)
	if !ok {
		return ""
	}
	card, _, _ := strings.Cut(rest, "\n\n<details>")
	if i := strings.Index(card, "\n\n### "); i >= 0 {
		card = card[:i]
	}
	return headPolicyChange + card
}

// commentScreen is the comment before its first fold, as lines of text: its markers and the link to the page are not
// the first screen.
func commentScreen(comment string) []Line {
	head, _, _ := strings.Cut(comment, "\n\n<details>")
	var out []Line
	for _, raw := range strings.Split(head, "\n") {
		raw = strings.TrimSpace(raw)
		kind := LineText
		switch {
		case raw == "" || strings.HasPrefix(raw, "<!--") || strings.HasPrefix(raw, "[Details in Archfit]"):
			continue
		case strings.HasPrefix(raw, "## Archfit: "):
			kind, raw = LineHeadline, strings.TrimPrefix(raw, "## Archfit: ")
		case strings.HasPrefix(raw, "### "):
			kind, raw = LineHeading, strings.TrimPrefix(raw, "### ")
		case strings.HasPrefix(raw, "- "):
			kind, raw = LineItem, strings.TrimPrefix(raw, "- ")
		}
		raw = markdownLink.ReplaceAllString(raw, "$1")
		raw = strings.NewReplacer("**", "", "`", "", "&lt;", "<", "&gt;", ">").Replace(raw)
		out = append(out, Line{kind, raw})
	}
	return out
}

func render(lines []Line) string {
	var b strings.Builder
	for _, l := range lines {
		b.WriteString(l.Kind + ": " + l.Text + "\n")
	}
	return b.String()
}

// The page's first screen is the comment's: for every fixture the lines are the same text, in the same order.
func TestFirstScreenIsTheCommentsFirstScreen(t *testing.T) {
	for _, c := range fixtureCases() {
		t.Run(c.name, func(t *testing.T) {
			p := fixturePublication(t, c, headB)
			p.stateCheckID = 99
			got := FirstScreen(inputOf(p))
			if presentation(p).Report == nil {
				if got != nil {
					t.Fatalf("the comment shows no report, the page screen is:\n%s", render(got))
				}
				return
			}
			want := commentScreen(renderComment(p, time.Time{}, "", ""))
			if render(got) != render(want) {
				t.Errorf("page first screen:\n%s\ncomment first screen:\n%s", render(got), render(want))
			}
			if len(got) < 3 || got[0].Kind != LineHeadline {
				t.Errorf("first screen has no headline and sentences:\n%s", render(got))
			}
		})
	}
}

// The assessment depends on whether a baseline was trusted: the page reads that from the check, and says what the comment says.
func TestFirstScreenWithoutBaseline(t *testing.T) {
	for _, c := range fixtureCases() {
		if c.name != "healthy" && c.name != "advisory-balancing-move" {
			continue
		}
		p := fixturePublication(t, c, headB)
		p.stateCheckID = 99
		p.Trusted.BaselineDigests, p.Receipt.BaselineDigest = nil, ""
		got, want := render(FirstScreen(inputOf(p))), render(commentScreen(renderComment(p, time.Time{}, "", "")))
		if got != want || !strings.Contains(got, "no baseline") {
			t.Errorf("%s: page first screen:\n%s\ncomment first screen:\n%s", c.name, got, want)
		}
	}
}

func TestFirstScreenWithPolicyChangeCard(t *testing.T) {
	p := fixturePublication(t, healthyCase(t), headB)
	p.stateCheckID = 99
	p.PolicyChange = enginePolicyChange(t, "widen")
	got := FirstScreen(inputOf(p))
	want := commentScreen(renderComment(p, time.Time{}, "", ""))
	if render(got) != render(want) || !strings.Contains(render(got), "heading: Policy change") {
		t.Errorf("page first screen:\n%s\ncomment first screen:\n%s", render(got), render(want))
	}
	golden(t, "policy-change-widen.screen", render(got))
}

func TestFirstScreenGolden(t *testing.T) {
	for _, name := range []string{"blocked", "healthy"} {
		for _, c := range fixtureCases() {
			if c.name != name {
				continue
			}
			p := fixturePublication(t, c, headB)
			p.stateCheckID = 99
			golden(t, name+".screen", render(FirstScreen(inputOf(p))))
		}
	}
}

// What cannot be shown as the comment shows it is not shown at all.
func TestFirstScreenWithheld(t *testing.T) {
	base := inputOf(func() Publication {
		p := fixturePublication(t, healthyCase(t), headB)
		p.stateCheckID = 99
		return p
	}())
	tests := []struct {
		name   string
		change func(in *ScreenInput)
	}{
		{"no report", func(in *ScreenInput) { in.Report = nil }},
		{"no recorded reason", func(in *ScreenInput) { in.Reasons = nil }},
		{"a reason that is not report-derived", func(in *ScreenInput) {
			in.Reasons = append(in.Reasons, ScreenReason{string(verdict.ReasonTargetMoved), string(verdict.CategoryEvidence), "", ""})
		}},
		{"a reason with another category", func(in *ScreenInput) { in.Reasons[0].Category = string(verdict.CategoryEvidence) }},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			in := base
			in.Reasons = append([]ScreenReason(nil), base.Reasons...)
			tt.change(&in)
			if got := FirstScreen(in); got != nil {
				t.Errorf("first screen:\n%s", render(got))
			}
		})
	}
}

// Text from the repository stays text: no marker, tag or link survives into a line.
func TestFirstScreenIsPlainText(t *testing.T) {
	p := fixturePublication(t, healthyCase(t), headB)
	in := inputOf(p)
	in.Title = "Gate <script>alert(1)</script> `x` <!-- y -->"
	for _, l := range FirstScreen(in) {
		if strings.ContainsAny(l.Text, "`") || strings.Contains(l.Text, "**") || strings.Contains(l.Text, "<!--") || strings.Contains(l.Text, "](") {
			t.Errorf("markdown left in %q", l.Text)
		}
	}
	if got, want := FirstScreen(in)[0], (Line{LineHeadline, "Gate <script>alert(1)</script> x  y"}); got != want {
		t.Errorf("headline = %+v; want %+v: text for a reader that shows text, without the escapes of Markdown", got, want)
	}
}

func TestRecoveryStepNamesTheRole(t *testing.T) {
	tests := []struct{ code, next, want string }{
		{"target_moved", "The target branch moved, and with it this pull request closes a module cycle: a, b. Update the branch.", "Pull request author: The target branch moved, and with it this pull request closes a module cycle: a, b. Update the branch."},
		{"owner_approval_missing", "An architecture owner must approve.", "Architecture owner: An architecture owner must approve."},
		{"missing_upload", "Re-run <it>.", "CI maintainer: Re-run <it>."},
	}
	for _, tt := range tests {
		if got := RecoveryStep(tt.code, tt.next); got != tt.want {
			t.Errorf("RecoveryStep(%q) = %q; want %q", tt.code, got, tt.want)
		}
	}
}

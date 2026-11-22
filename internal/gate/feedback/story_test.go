package feedback

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"slices"
	"strings"
	"testing"
	"unicode/utf8"

	"github.com/coupling-dev/archfit-app/internal/gate/records"
	"github.com/coupling-dev/archfit-app/internal/github/api"
	"github.com/coupling-dev/archfit-app/internal/wire/result"
)

// parseStory reads the line as the engine's `archfit status` does (internal/application/request/status.go).
func parseStory(t *testing.T, line string) story {
	t.Helper()
	if !strings.HasPrefix(line, storyPrefix) || !strings.HasSuffix(line, storySuffix) || strings.Contains(line, "\n") {
		t.Fatalf("not one story line: %q", line)
	}
	var s story
	if err := json.Unmarshal([]byte(strings.TrimSuffix(strings.TrimPrefix(line, storyPrefix), storySuffix)), &s); err != nil {
		t.Fatal(err)
	}
	return s
}

func TestStoryLineFollowsTheStoryOfAChange(t *testing.T) {
	fix := result.Next{Kind: nextFix, Text: "fix the code"}
	ask := result.Next{Kind: nextCommand, Text: `archfit request -m "<why you need it>"`}
	wait := func(n int) result.Next {
		return result.Next{Kind: nextWait, Text: fmt.Sprintf("wait for an owner: #%d", n)}
	}
	errs := func(steps ...result.Next) []result.Error {
		out := make([]result.Error, len(steps))
		for i, n := range steps {
			out[i] = result.Error{Path: "a.go", Line: i + 1, Rule: "graph", What: "x", Next: n}
		}
		return out
	}
	open := &StoryFacts{State: records.StateOpen, Owner: "alexei-led"}
	for _, tt := range []struct {
		name       string
		r          result.Result
		facts      *StoryFacts
		steps      storySteps
		request    int
		waitingFor string
		next       string
	}{
		{"pass", result.Result{Status: result.StatusPass}, nil,
			storySteps{"done", "done", "not-needed", "not-needed", "not-needed", "not-needed"}, 0, "", "nothing to do: the check passes."},
		{"fix", result.Result{Status: result.StatusFail, Errors: errs(fix)}, nil,
			storySteps{"done", "failed", "not-needed", "not-needed", "not-needed", "not-needed"}, 0, "", authorFix},
		{"ask", result.Result{Status: result.StatusFail, Errors: errs(ask)}, nil,
			storySteps{"done", "failed", "current", "later", "later", "later"}, 0, "", authorAsk},
		{"waiting for the owner", result.Result{Status: result.StatusWaiting, Errors: errs(wait(40))}, open,
			storySteps{"done", "failed", "current", "later", "later", "later"}, 40, "alexei-led",
			"nothing to do. This check runs again when #40 is answered."},
		{"no person to name", result.Result{Status: result.StatusWaiting, Errors: errs(wait(40))},
			&StoryFacts{State: records.StateOpen},
			storySteps{"done", "failed", "current", "later", "later", "later"}, 40, "",
			"nothing to do. This check runs again when #40 is answered."},
		{"the App merges the policy", result.Result{Status: result.StatusWaiting, Errors: errs(wait(40))},
			&StoryFacts{State: records.StateAnswered, PolicyPull: 44},
			storySteps{"done", "failed", "done", "done", "current", "later"}, 40, "",
			"nothing to do. #40 is answered; the App is merging policy pull request #44, then this check runs again by itself."},
		{"policy updated", result.Result{Status: result.StatusWaiting, Errors: errs(wait(40))},
			&StoryFacts{State: records.StateAnswered, PolicyPull: 44, PolicyMerged: true},
			storySteps{"done", "failed", "done", "done", "done", "current"}, 40, "",
			"nothing to do. #40 is answered and the policy has it; this check runs again by itself."},
		{"unknown", result.Result{Status: result.StatusUnknown, Unknown: []result.Unknown{{Cause: "base", What: "x",
			Next: result.Next{Kind: nextWait, Text: "the check runs again"}}}}, nil,
			storySteps{"done", "current", "not-needed", "not-needed", "not-needed", "not-needed"}, 0, "", "the check runs again"},
	} {
		t.Run(tt.name, func(t *testing.T) {
			line := storyLine(newView(tt.r), tt.facts)
			s := parseStory(t, line)
			if s.Steps != tt.steps || s.Request != tt.request || s.WaitingFor != tt.waitingFor || s.Next != tt.next {
				t.Errorf("story = %+v\nwant steps %+v, request %d, waiting for %q, next %q", s, tt.steps, tt.request, tt.waitingFor, tt.next)
			}
		})
	}
}

func TestStoryLineIsTheContractsShape(t *testing.T) {
	r := result.Result{Status: result.StatusWaiting, Errors: []result.Error{{Path: "a.go", Line: 1, Rule: "graph", What: "x",
		Next: result.Next{Kind: nextWait, Text: "wait for an owner: #40"}}}}
	want := `<!-- archfit-story v1 {"steps":{"change":"done","check":"failed","ask":"current","answer":"later","policy":"later","recheck":"later"},"request":40,"waiting_for":"alexei-led","next":"nothing to do. This check runs again when #40 is answered."} -->`
	if got := storyLine(newView(r), &StoryFacts{State: records.StateOpen, Owner: "alexei-led"}); got != want {
		t.Errorf("line\n got %s\nwant %s", got, want)
	}
}

func TestStoryLineCannotBeClosedOrGrowPastOneKiB(t *testing.T) {
	for _, next := range []string{
		"--> <!-- " + strings.Repeat("é", 2000),
		strings.Repeat("<&", 300) + strings.Repeat("\x01\x1f", 200),
	} {
		r := result.Result{Status: result.StatusUnknown, Unknown: []result.Unknown{{Cause: "tool", What: "x",
			Next: result.Next{Kind: nextWait, Text: next}}}}
		line := storyLine(newView(r), nil)
		if len(line) >= 1024 || !utf8.ValidString(line) {
			t.Errorf("story line is %d bytes", len(line))
		}
		inner := strings.TrimSuffix(strings.TrimPrefix(line, storyPrefix), storySuffix)
		if strings.Contains(inner, "-->") || strings.Contains(inner, "<!--") {
			t.Errorf("the text can close the comment: %s", inner)
		}
		parseStory(t, line)
	}
}

func TestStoryLineEscapedSizeIsStillBounded(t *testing.T) {
	r := result.Result{Status: result.StatusUnknown, Unknown: []result.Unknown{{Cause: "tool", What: "x",
		Next: result.Next{Kind: nextWait, Text: "--> <!-- " + strings.Repeat("é", 2000)}}}}
	line := storyLine(newView(r), nil)
	if len(line) >= 1024 || !utf8.ValidString(line) || strings.Contains(line, "\ufffd") {
		t.Errorf("story line is %d bytes", len(line))
	}
	inner := strings.TrimSuffix(strings.TrimPrefix(line, storyPrefix), storySuffix)
	if strings.Contains(inner, "-->") || strings.Contains(inner, "<!--") {
		t.Errorf("the text can close the comment: %s", inner)
	}
	parseStory(t, line)
}

func TestThePullRequestCheckCarriesTheStoryLine(t *testing.T) {
	h := newHarness(headB)
	var asked []int
	h.pub = New4(h.checks, h.comments, h.pulls, h.store, appURL, h.clock.now, nil,
		func(_ context.Context, r api.Repo, request int) (StoryFacts, error) {
			asked = append(asked, request)
			return StoryFacts{State: records.StateOpen, Owner: "alexei-led"}, nil
		})
	waiting := result.Result{Status: result.StatusWaiting, Summary: "archfit: waiting, request #40", Errors: []result.Error{{
		Path: "a.go", Line: 1, Rule: "graph", What: "x", Next: result.Next{Kind: nextWait, Text: "wait for an owner: #40"}}}}
	if err := h.pub.Publish4(context.Background(), pub4(prTarget(headB), 7, 1, waiting)); err != nil {
		t.Fatal(err)
	}
	body := h.comments.all()[0].Body
	if !strings.Contains(body, "✓ change → ✗ check → ● **ask #40**") || !strings.Contains(body, "@alexei-led answers request #40") {
		t.Fatalf("comment missing story: %s", body)
	}
	s := parseStory(t, h.checks.latest(t, headB, gateCheck).Text)
	if s.Request != 40 || s.WaitingFor != "alexei-led" || s.Steps.Ask != stepCurrent || !slices.Equal(asked, []int{40}) {
		t.Fatalf("story %+v, asked %v", s, asked)
	}

	failing := errors.New("store down")
	h = newHarness(headB)
	h.pub = New4(h.checks, h.comments, h.pulls, h.store, appURL, h.clock.now, nil,
		func(context.Context, api.Repo, int) (StoryFacts, error) { return StoryFacts{}, failing })
	if err := h.pub.Publish4(context.Background(), pub4(prTarget(headB), 7, 1, waiting)); err != nil {
		t.Fatalf("an unreadable story stopped the check: %v", err)
	}
	if s := parseStory(t, h.checks.latest(t, headB, gateCheck).Text); s.WaitingFor != "" || s.Request != 40 {
		t.Fatalf("story without facts %+v", s)
	}
}

func TestAPushCheckHasNoStoryLine(t *testing.T) {
	h := newHarness(headB)
	if err := h.pub.Publish4(context.Background(), pub4(pushTarget(headA), 7, 1, engineResult(t, "pass"))); err != nil {
		t.Fatal(err)
	}
	if text := h.checks.latest(t, headA, gateCheck).Text; text != "" {
		t.Fatalf("push check text %q", text)
	}
}

func TestCommentTopLineFollowsTheStory(t *testing.T) {
	r := result.Result{Status: result.StatusWaiting, Errors: []result.Error{{Path: "a.go", Line: 1, Rule: "graph", What: "x", Next: result.Next{Kind: nextWait, Text: "wait for an owner: #40"}}}}
	v := newView(r)
	s := storyOf(v, &StoryFacts{State: records.StateOpen, Owner: "alexei-led"})
	v.story = &s
	v.requestURL = "https://example.com/r/acme/repo/requests/40"
	got := v.comment(65536)
	want := "✓ change → ✗ check → ● **ask #40** → ○ answer → ○ policy → ○ re-check\n\n**Next: @alexei-led answers request #40** (1 minute, passkey): https://example.com/r/acme/repo/requests/40\nAfter the answer, the App updates the policy and this check runs again. Nothing for the author to do."
	if !strings.HasPrefix(got, want) {
		t.Errorf("comment starts with %q, want %q", got, want)
	}
}

func TestCommentOtherStoryRows(t *testing.T) {
	wait := result.Next{Kind: nextWait, Text: "wait for an owner: #40"}
	for _, tc := range []struct {
		name          string
		status        result.Status
		next          result.Next
		facts         *StoryFacts
		page          bool
		row, nextText string
	}{
		{"no owner", result.StatusWaiting, wait, &StoryFacts{State: records.StateOpen}, false, "● **ask #40**", "an owner answers request #40"},
		{"ask", result.StatusFail, result.Next{Kind: nextCommand, Text: "archfit request -m why"}, nil, false, "● **ask**", "the author asks an owner"},
		{"fix", result.StatusFail, result.Next{Kind: nextFix, Text: "fix the code"}, nil, false, "✗ check", "the author fixes the code"},
		{"policy", result.StatusWaiting, wait, &StoryFacts{State: records.StateAnswered, PolicyPull: 44}, false, "● **policy**", "policy pull request #44"},
		{"recheck", result.StatusWaiting, wait, &StoryFacts{State: records.StateAnswered, PolicyMerged: true}, false, "● **re-check**", "updated policy"},
		{"pass", result.StatusPass, result.Next{}, nil, false, "✓ check", ""},
		{"unknown", result.StatusUnknown, result.Next{}, nil, false, "● **check**", ""},
		{"owner page", result.StatusWaiting, wait, &StoryFacts{State: records.StateOpen}, true, "● **ask #40**", ""},
	} {
		t.Run(tc.name, func(t *testing.T) {
			r := result.Result{Status: tc.status}
			if tc.next.Kind != "" {
				r.Errors = []result.Error{{Next: tc.next}}
			}
			v := newView(r)
			s := storyOf(v, tc.facts)
			v.story = &s
			if tc.facts != nil {
				v.policyPull = tc.facts.PolicyPull
			}
			if tc.page {
				v.next = &nextLink{label: "answer", role: "owner", url: "https://example.com"}
			}
			body := v.comment(maxBody)
			if !strings.Contains(body, tc.row) || (tc.nextText != "" && !strings.Contains(body, tc.nextText)) {
				t.Errorf("unexpected comment: %s", body)
			}
			if strings.Count(body, "**Next:") > 1 {
				t.Errorf("multiple next lines: %s", body)
			}
		})
	}
}

func TestCommentHeaderMentionsAtMostOnePerson(t *testing.T) {
	v := newView(result.Result{Status: result.StatusWaiting, Errors: []result.Error{{Next: result.Next{Kind: nextWait, Text: "wait for an owner: #40"}}}})
	s := storyOf(v, &StoryFacts{State: records.StateOpen, Owner: "alexei-led"})
	v.story = &s
	if body := v.comment(maxBody); strings.Count(body, "@") > 1 {
		t.Errorf("too many mentions: %s", body)
	}
}

func TestStoryMentionOnRepublishAndNewRun(t *testing.T) {
	h := newHarness(headB)
	h.pub = New4(h.checks, h.comments, h.pulls, h.store, appURL, h.clock.now, nil, func(context.Context, api.Repo, int) (StoryFacts, error) {
		return StoryFacts{State: records.StateOpen, Owner: "alexei-led"}, nil
	})
	r := result.Result{Status: result.StatusWaiting, Errors: []result.Error{{Next: result.Next{Kind: nextWait, Text: "wait for an owner: #40"}}}}
	p := pub4(prTarget(headB), 7, 1, r)
	if err := h.pub.Publish4(context.Background(), p); err != nil {
		t.Fatal(err)
	}
	if err := h.pub.Publish4(context.Background(), p); err != nil {
		t.Fatal(err)
	}
	if n := h.comments.count("UpdateComment"); n != 0 {
		t.Fatalf("identical publication updated comment %d times", n)
	}
	p.RunID = 8
	if err := h.pub.Publish4(context.Background(), p); err != nil {
		t.Fatal(err)
	}
	if n := h.comments.count("UpdateComment"); n != 1 {
		t.Fatalf("new run updated comment %d times", n)
	}
	if comments := h.comments.all(); len(comments) != 1 || strings.Count(comments[0].Body, "@alexei-led") != 1 {
		t.Fatalf("comments: %+v", comments)
	}
}

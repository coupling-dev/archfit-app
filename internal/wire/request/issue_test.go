package request_test

import (
	"errors"
	"reflect"
	"strings"
	"testing"

	"github.com/coupling-dev/archfit-app/internal/wire/request"
)

func TestTitle(t *testing.T) {
	for _, tc := range []struct {
		issue request.Issue
		want  string
	}{
		{request.Issue{Rule: "graph", From: "pkg_tc", To: "pkg_util"}, "Request: graph pkg_tc -> pkg_util"},
		{request.Issue{Rule: "cycle", From: "b", To: "a"}, "Request: cycle b -> a"},
		{request.Issue{Rule: "duplication", From: "pkg_util", To: "pkg_tc"}, "Request: duplication pkg_tc <-> pkg_util"},
		{request.Issue{Rule: "unowned", Path: "tools/gen/main.go"}, "Request: unowned tools/gen/main.go"},
	} {
		if got := tc.issue.Title(); got != tc.want {
			t.Errorf("Title() = %q, want %q", got, tc.want)
		}
	}
}

// TestBodyFencesWhatTheWorkerWrote: a reason or a reported record cannot close its block, whatever backticks it holds.
func TestBodyFencesWhatTheWorkerWrote(t *testing.T) {
	for _, tc := range []struct {
		name, reason, record, fence string
	}{
		{"no backticks", "why", "rec\n", "```text\n"},
		{"three backticks", "a ``` b", "rec\n", "````text\n"},
		{"four backticks", "````", "rec\n", "`````text\n"},
		{"backticks in the record", "why", "```\n@team\n```\n", "````text\n"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			body := request.Issue{Rule: "graph", From: "a", To: "b", Reason: tc.reason, Record: tc.record}.Body()
			if !strings.Contains(body, tc.fence) {
				t.Errorf("no %q fence in:\n%s", tc.fence, body)
			}
			if got := blockContents(body); len(got) != 2 || got[0] != tc.reason+"\n" || got[1] != tc.record {
				t.Errorf("blocks %q, want the reason and the record", got)
			}
		})
	}
}

// blockContents reads the fenced blocks of a Markdown body as CommonMark does: a block ends at a line of at least
// as many backticks as opened it.
func blockContents(body string) []string {
	var blocks []string
	open, content := 0, ""
	for line := range strings.Lines(body) {
		line = strings.TrimSuffix(line, "\n")
		run := len(line) - len(strings.TrimLeft(line, "`"))
		switch {
		case open == 0 && run >= 3:
			open, content = run, ""
		case open > 0 && run >= open && strings.Trim(line, "`") == "":
			blocks, open = append(blocks, content), 0
		case open > 0:
			content += line + "\n"
		}
	}
	return blocks
}

func TestParseIssue(t *testing.T) {
	const line = "/archfit request why\n\n"
	can := "pkg_tc -> pkg_util: graph: not an allowed dependency\n  balance: 9/10 (min 7)\n  next: archfit request -m \"<why you need it>\"\n"
	graph := &request.Answer{Rule: "graph", From: "pkg_tc", To: "pkg_util", Balance: &request.Balance{Value: 9, Min: 7}}
	noneDeclared := "a -> b: coupling: b/x is past the surface\n  balance: 2/10 (min 7): intrusive, b is high\n  surface: none declared\n"
	can09 := "analysis-application -> assessment-repair: coupling: internal/assessment/score is past the surface\n" +
		"  balance: 2/10 (min 7): intrusive, assessment-repair is high\n" +
		"  surface: internal/assessment/finding (public), internal/assessment/result (model)\n" +
		"  next: use the surface, or: archfit request -m \"<why score belongs to the surface>\"\n"
	coupling := &request.Answer{
		Rule: "coupling", From: "analysis-application", To: "assessment-repair",
		Balance: &request.Balance{Value: 2, Min: 7, Strength: "intrusive", Volatility: "high"},
		Surface: &request.Surface{Public: []string{"internal/assessment/finding"}, Model: []string{"internal/assessment/result"}},
	}
	for _, tc := range []struct {
		name   string
		body   string
		record string
		answer *request.Answer
	}{
		{"leading blank lines", "\n \n" + line + "```text\n" + can + "```\n", can, graph},
		{"unprotected policy", line + "```text\n" + can + "policy: not protected\n```\n", can + "policy: not protected\n", graph},
		{"unknown trailing line", line + "```text\n" + can + "policy: unknown\n```\n", can + "policy: unknown\n", nil},
		{"policy prefix", line + "```text\n" + can + "policy: not protected extra\n```\n", can + "policy: not protected extra\n", nil},
		{"policy before fields", line + "```text\na -> b: graph: x\npolicy: not protected\n  next: x\n```\n", "a -> b: graph: x\npolicy: not protected\n  next: x\n", nil},
		{"duplicate policy", line + "```text\n" + can + "policy: not protected\npolicy: not protected\n```\n", can + "policy: not protected\npolicy: not protected\n", nil},
		{"a longer fence", line + "````text\n" + can + "````\n", can, graph},
		{"text before the block", line + "Asked from a laptop.\n\n```text\n" + can + "```\n", can, graph},
		{"the first text block", line + "```text\n" + can + "```\n```text\nother\n```\n", can, graph},
		{"an answer without balance", line + "```text\na -> b: layers: goes against the layer order\n  next: x\n```\n", "a -> b: layers: goes against the layer order\n  next: x\n", &request.Answer{Rule: "layers", From: "a", To: "b"}},
		{"no block", line, "", nil},
		{"a block that is not text", line + "```json\n" + can + "```\n", "", nil},
		{"a block not closed", line + "```text\n" + can, "", nil},
		{"a shorter closing fence", line + "````text\n" + can + "```\n", "", nil},
		{"a check record", line + "```text\npkg/tc/netem.go:12: graph: pkg_tc -> pkg_util is not an allowed dependency\n```\n", "pkg/tc/netem.go:12: graph: pkg_tc -> pkg_util is not an allowed dependency\n", nil},
		{"an answer yes", line + "```text\na -> b: yes\n```\n", "a -> b: yes\n", nil},
		{"an answer no without a rule", line + "```text\na -> b: no: not allowed\n```\n", "a -> b: no: not allowed\n", nil},
		{"two answers", line + "```text\na -> b: graph: x\nc -> d: graph: y\n```\n", "a -> b: graph: x\nc -> d: graph: y\n", nil},
		{"a balance out of scale", line + "```text\na -> b: graph: x\n  balance: 11/10 (min 7)\n```\n", "a -> b: graph: x\n  balance: 11/10 (min 7)\n", nil},
		{"an empty module", line + "```text\n -> b: graph: x\n```\n", " -> b: graph: x\n", nil},
		{"a 0.9.0 balance with its inputs", line + "```text\n" + can09 + "```\n", can09, coupling},
		{"inputs about another module", line + "```text\na -> b: coupling: x\n  balance: 2/10 (min 7): intrusive, c is high\n```\n", "a -> b: coupling: x\n  balance: 2/10 (min 7): intrusive, c is high\n", nil},
		{"an unknown strength", line + "```text\na -> b: coupling: x\n  balance: 2/10 (min 7): functional, b is high\n```\n", "a -> b: coupling: x\n  balance: 2/10 (min 7): functional, b is high\n", nil},
		{"a surface after a v4.0.0 balance", line + "```text\na -> b: coupling: x\n  balance: 2/10 (min 7)\n  surface: b/api (public)\n```\n", "a -> b: coupling: x\n  balance: 2/10 (min 7)\n  surface: b/api (public)\n", nil},
		{"a surface without a balance", line + "```text\na -> b: coupling: x\n  surface: b/api (public)\n```\n", "a -> b: coupling: x\n  surface: b/api (public)\n", nil},
		{"a surface with no declared part", line + "```text\n" + noneDeclared + "```\n", noneDeclared, &request.Answer{Rule: "coupling", From: "a", To: "b", Balance: &request.Balance{Value: 2, Min: 7, Strength: "intrusive", Volatility: "high"}, Surface: &request.Surface{Public: []string{}, Model: []string{}}}},
		{"a surface with an unknown role", line + "```text\na -> b: coupling: x\n  balance: 2/10 (min 7): intrusive, b is high\n  surface: b/api (private)\n```\n", "a -> b: coupling: x\n  balance: 2/10 (min 7): intrusive, b is high\n  surface: b/api (private)\n", nil},
	} {
		t.Run(tc.name, func(t *testing.T) {
			got, ok, err := request.ParseIssue(tc.body, today)
			if !ok || err != nil {
				t.Fatalf("ok %v, err %v", ok, err)
			}
			if got.Reason != "why" || got.Record != tc.record {
				t.Errorf("reason %q, record %q; want record %q", got.Reason, got.Record, tc.record)
			}
			switch {
			case tc.answer == nil && got.Answer != nil:
				t.Errorf("answer %+v, want none", got.Answer)
			case tc.answer != nil && (got.Answer == nil || got.Answer.Rule != tc.answer.Rule || got.Answer.From != tc.answer.From ||
				got.Answer.To != tc.answer.To || (got.Answer.Balance == nil) != (tc.answer.Balance == nil) ||
				got.Answer.Balance != nil && *got.Answer.Balance != *tc.answer.Balance ||
				!reflect.DeepEqual(got.Answer.Surface, tc.answer.Surface)):
				t.Errorf("answer %+v, want %+v", got.Answer, tc.answer)
			}
		})
	}
}

func TestParseIssueIsNotARequest(t *testing.T) {
	for _, body := range []string{
		"",
		"\n\n",
		"Hi\n/archfit request why\n",
		"**Reason**\n\n```text\n/archfit request why\n```\n",
	} {
		if got, ok, err := request.ParseIssue(body, today); ok || err != nil {
			t.Errorf("ParseIssue(%q) = %+v, %v, %v; want not a request", body, got, ok, err)
		}
	}
}

func TestParseIssueRejects(t *testing.T) {
	_, ok, err := request.ParseIssue("/archfit request why --expires 2026-11-10\n\n```text\na -> b: graph: x\n```\n", today)
	var r *request.Rejection
	if !ok || !errors.As(err, &r) || r.Kind != request.KindDate {
		t.Errorf("ok %v, err %v; want the date rejection", ok, err)
	}
}

// TestBodyOfAReasonThatIsARequestLine: the App's body is never read as a request, even when the reason is a line.
func TestBodyOfAReasonThatIsARequestLine(t *testing.T) {
	body := request.Issue{Rule: "graph", From: "a", To: "b", Reason: "/archfit request again", Record: "a -> b: graph: x\n"}.Body()
	if _, ok, _ := request.ParseIssue(body, today); ok {
		t.Errorf("the body reads as a request:\n%s", body)
	}
}

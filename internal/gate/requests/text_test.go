package requests

import (
	"strings"
	"testing"

	"github.com/alexei-led/archfit-app/internal/gate/records"
	"github.com/alexei-led/archfit-app/internal/wire/request"
	"github.com/alexei-led/archfit-app/internal/wire/result"
	"github.com/alexei-led/archfit-app/internal/wire/wording"
)

// githubBodyLimit is GitHub's limit of an issue or comment body, in characters.
const githubBodyLimit = 65_536

func TestEveryTextUsesNoNotUsedWordAndIsNoRequest(t *testing.T) {
	texts := map[string]string{
		"no result":   textNoResult,
		"no policy":   textNoPolicy,
		"answered":    answeredReply(answered{what: "a -> b", rule: "no-a-to-b", instead: "use c"}),
		"duplicate":   duplicateReply(431),
		"joined note": joinedNote(7),
		"reply": outcome{opened: []int{1}, joined: []int{2}, left: 3,
			answered: []answered{{what: "a -> b", rule: "no-a-to-b", instead: "use c"}}}.reply(true),
		"reply of one": outcome{opened: []int{1}, left: 1}.reply(false),
	}
	for name, text := range texts {
		t.Run(name, func(t *testing.T) {
			text = inert(text)
			if words := wording.NotUsedIn(text); len(words) > 0 {
				t.Errorf("uses %v:\n%s", words, text)
			}
			for line := range strings.SplitSeq(text, "\n") {
				if strings.HasPrefix(strings.TrimSpace(line), request.Command) {
					t.Errorf("a line reads as a request: %q", line)
				}
			}
			if _, ok, _ := request.ParseComment(text, testToday); ok {
				t.Error("the App would read its own text as a request")
			}
		})
	}
}

func TestOwnerAndWorkerTextCannotMentionOrLink(t *testing.T) {
	hostile := "@team see [x](https://evil.example) ``` ` <b>"
	reply := outcome{answered: []answered{{what: "@a -> b`", rule: "r`", instead: hostile}}}.reply(true)
	for _, want := range []string{"````text\n  " + hostile + "\n  ````\n", "`` @a -> b` ``", "`` r` ``"} {
		if !strings.Contains(reply, want) {
			t.Errorf("reply lacks %q:\n%s", want, reply)
		}
	}
	if !strings.Contains(answeredReply(answered{rule: "r", instead: hostile}), "````text\n"+hostile+"\n````\n") {
		t.Error("the instead of an answered issue is not fenced")
	}
}

func TestTheIssueBodyStaysUnderGitHubsLimit(t *testing.T) {
	var errs []result.Error
	for range 2000 {
		e := ask("a", "b")
		e.Why = strings.Repeat("w", 200)
		errs = append(errs, e)
	}
	rec := recordsOf(errs)
	if len(rec) > recordCap+100 || !strings.HasSuffix(rec, " more not shown)\n") {
		t.Fatalf("record of %d bytes ends %q", len(rec), rec[len(rec)-40:])
	}
	req := records.Request{Rule: "graph", From: "a", To: "b", Reason: strings.Repeat("é", request.MaxReason), Record: rec,
		Numbers: records.Numbers{Verified: true}, Waiting: []int{1, 2, 3}, Expires: "2027-01-01"}
	if n := len([]rune(issueOf(req).Body())); n >= githubBodyLimit {
		t.Errorf("body of %d characters", n)
	}
	if raw, err := records.Encode(&req); err != nil {
		t.Errorf("the stored request does not fit: %v (%d bytes)", err, len(raw))
	}
	reported := capRecord(strings.Repeat("line of a reported record\n", 3000))
	if len(reported) > recordCap+20 || !strings.HasSuffix(reported, "(more not shown)\n") {
		t.Errorf("reported record of %d bytes", len(reported))
	}
	if short := "a\n"; capRecord(short) != short {
		t.Error("a short record was cut")
	}
}

func TestDeniedMatchesModulesOnly(t *testing.T) {
	pol := mustPolicy(t, "version: 3\nowners: [o]\nmodules:\n  a:\n    paths: [a/**]\nrules:\n"+
		"  - id: no-a-to-b\n    deny:\n      from: [a, x]\n      to: [b]\n    why: w\n    instead: use c\n"+
		"  - id: no-globs\n    deny:\n      from: [\"pkg/a/**\"]\n      to: [\"pkg/d/**\"]\n    why: w\n    instead: i\n")
	tests := []struct {
		from, to string
		want     string
	}{
		{"a", "b", "no-a-to-b"},
		{"x", "b", "no-a-to-b"},
		{"b", "a", ""},
		{"a", "d", ""}, // a rule written with path globs is not matched
		{"", "b", ""},
	}
	for _, tt := range tests {
		got, ok := denied(pol, result.Error{From: tt.from, To: tt.to})
		if ok != (tt.want != "") || got.rule != tt.want {
			t.Errorf("denied(%s -> %s) = %+v %v, want %q", tt.from, tt.to, got, ok, tt.want)
		}
	}
}

func TestGroupsKeepRecordOrderAndSkipErrorsWithoutAKey(t *testing.T) {
	gs := groups([]result.Error{ask("a", "c"), ask("a", "b"), ask("a", "c"), {Rule: "exception"}})
	if len(gs) != 2 || gs[0].key != "graph a c" || len(gs[0].errs) != 2 || gs[1].key != "graph a b" {
		t.Errorf("groups = %+v", gs)
	}
}

func TestNoCommentOfTheAppIsARequest(t *testing.T) {
	quoted := []string{
		"use x\r/archfit request y",
		"use x\n/archfit request y",
		"use x\r\n/archfit request y",
		"/archfit request y",
	}
	for _, q := range quoted {
		texts := []string{
			outcome{answered: []answered{{what: "a -> b", rule: "r", instead: q}}}.reply(true),
			answeredReply(answered{rule: "r", instead: q}),
			request.NothingToAsk(recordsOf([]result.Error{{Path: "p.go", Line: 1, Rule: "r", What: q,
				Next: result.Next{Kind: "fix", Text: "fix the code"}}})).Reply(),
		}
		for _, text := range texts {
			if _, ok, _ := request.ParseComment(inert(text), testToday); ok {
				t.Errorf("the App would read its own comment as a request:\n%q", inert(text))
			}
		}
	}
	if got := inert("a\r\nb\rc"); got != "a\nb\nc" {
		t.Errorf("inert line ends = %q", got)
	}
}

func TestTheReplySaysWaitingOnlyWhenTheCheckCanWait(t *testing.T) {
	o := outcome{opened: []int{1}}
	if !strings.Contains(o.reply(true), "`waiting`") {
		t.Error("a required policy's reply does not say the check waits")
	}
	if strings.Contains(o.reply(false), "waiting") {
		t.Error("an advisory policy's reply says the check waits; it never does")
	}
	if strings.Contains(outcome{answered: []answered{{rule: "r"}}}.reply(true), "waiting") {
		t.Error("a reply that opened and joined nothing says the check waits")
	}
}

func TestSameRecordComparesTheStatus(t *testing.T) {
	rec := records.Pull{HeadSHA: "h", RunID: 1, RunAttempt: 1, PayloadDigest: "d", Status: "fail"}
	changes := map[string]func(*records.Pull){
		"status": func(p *records.Pull) { p.Status = "unknown" }, "run": func(p *records.Pull) { p.RunID = 2 },
		"attempt": func(p *records.Pull) { p.RunAttempt = 2 }, "digest": func(p *records.Pull) { p.PayloadDigest = "e" },
		"head": func(p *records.Pull) { p.HeadSHA = "g" },
	}
	if !sameRecord(rec, rec) {
		t.Fatal("a record differs from itself")
	}
	for name, change := range changes {
		other := rec
		change(&other)
		if sameRecord(rec, other) {
			t.Errorf("a record with another %s is the same", name)
		}
	}
}

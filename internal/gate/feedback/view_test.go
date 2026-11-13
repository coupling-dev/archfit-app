package feedback

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/alexei-led/archfit-app/internal/github/api"
	"github.com/alexei-led/archfit-app/internal/wire/result"
	"github.com/alexei-led/archfit-app/internal/wire/wording"
)

// The three comments of design 12.5, as the design prints them.
const (
	designRule = "## Archfit: fail, 1 error\n\n" +
		"`pkg/chaos/netem/netem.go:8` **no-chaos-to-runtime** pkg_chaos must not depend on pkg_runtime\n" +
		"- why: A direct import ties a chaos action to one runtime.\n" +
		"- instead: Add a method to container.Client.\n\n" +
		"**Author:** fix the code and push.\n"
	designAsk = "## Archfit: fail, 1 error\n\n" +
		"`pkg/tc/netem.go:12` **graph** pkg_tc -> pkg_util is not an allowed dependency\n" +
		"- balance: 9/10 (min 7)\n\n" +
		"**Author:** reply `/archfit request <why you need it>`. Do not copy the code.\n" +
		"**Owner:** nothing to do until a request exists.\n"
	designWaiting = "## Archfit: waiting, request #431\n\n" +
		"`pkg/tc/netem.go:12` **graph** pkg_tc -> pkg_util is not an allowed dependency\n" +
		"- balance: 9/10 (min 7)\n\n" +
		"**Author:** nothing to do. This check runs again when #431 is answered.\n" +
		"**Owner:** answer #431.\n"
)

// engineResult reads a vendored engine result golden of wire/result.
func engineResult(t *testing.T, name string) result.Result {
	t.Helper()
	raw, err := os.ReadFile(filepath.Join("..", "..", "wire", "result", "testdata", "result", name+".json"))
	if err != nil {
		t.Fatal(err)
	}
	r, err := result.Decode(raw)
	if err != nil {
		t.Fatalf("%s: %v", name, err)
	}
	return r
}

// The design's graph examples leave out the engine's why; everything else is the vendored result.
func TestCommentsOfDesign12_5(t *testing.T) {
	noWhy := func(r result.Result) result.Result {
		r.Errors[0].Why = ""
		return r
	}
	for name, tc := range map[string]struct {
		result result.Result
		want   string
	}{
		"a rule the code must follow":       {engineResult(t, "deny"), designRule},
		"a dependency only an owner allows": {noWhy(engineResult(t, "graph")), designAsk},
		"after the request":                 {noWhy(engineResult(t, "waiting")), designWaiting},
	} {
		t.Run(name, func(t *testing.T) {
			if got := newView(tc.result).comment(maxBody); got != tc.want {
				t.Errorf("comment:\n--- got\n%s--- want\n%s", got, tc.want)
			}
		})
	}
}

// Every form uses the same fixture, including the 8.1 examples and the three
// comments printed in 12.5. Existing combined check goldens also keep annotations.
func TestViewGoldens(t *testing.T) {
	for _, tc := range viewFixtures(t) {
		t.Run(tc.name, func(t *testing.T) {
			v := newView(tc.result)
			golden4(t, tc.name+".comment.md", v.comment(maxBody))
			switch tc.name {
			case "pass", "fail-fix", "fail-advisory", "fail-ask", "fail-copy", "fail-expired-exception", "waiting", "unknown":
				golden4(t, tc.name+".check.md", checkText(v, tc.advisory))
			}
			golden4(t, tc.name+".title.txt", v.title()+"\n")
			golden4(t, tc.name+".summary.md", v.summary()+"\n")
			raw, err := json.MarshalIndent(BuildPage(tc.result), "", "  ")
			if err != nil {
				t.Fatal(err)
			}
			golden4(t, tc.name+".page.json", string(raw)+"\n")
		})
	}
}

type viewFixture struct {
	name     string
	result   result.Result
	advisory bool
}

func viewFixtures(t *testing.T) []viewFixture {
	t.Helper()
	fixtures := []viewFixture{
		{"pass", engineResult(t, "pass"), false},
		{"fail-fix", engineResult(t, "deny"), false},
		{"fail-advisory", engineResult(t, "deny"), true},
		{"fail-ask", engineResult(t, "graph"), false},
		{"fail-copy", engineResult(t, "duplication"), false},
		{"fail-expired-exception", engineResult(t, "expired_exception"), false},
		{"waiting", engineResult(t, "waiting"), false},
		{"unknown", engineResult(t, "policy_differs"), false},
		{"design-12-5-rule", engineResult(t, "deny"), false},
	}
	for _, tc := range []struct{ name, source string }{
		{"design-12-5-ask", "graph"},
		{"design-12-5-waiting", "waiting"},
	} {
		r := engineResult(t, tc.source)
		r.Errors[0].Why = ""
		fixtures = append(fixtures, viewFixture{tc.name, r, false})
	}
	// A can answer is not an uploadable check result. Carry its record text
	// into a fail fixture without broadening the result wire contract.
	can := result.Result{Status: result.StatusFail, Summary: "archfit: fail, 1 error", Errors: []result.Error{{
		Path: "pkg_tc -> pkg_util", Rule: "graph", What: "not an allowed dependency",
		Balance: &result.Balance{Value: 9, Min: 7},
		Next:    result.Next{Kind: nextCommand, Text: `archfit request -m "<why you need it>"`},
	}}}
	fixtures = append(fixtures, viewFixture{"can-record", can, false})
	waiting := engineResult(t, "waiting")
	waiting.Errors = append(waiting.Errors, waiting.Errors[0], waiting.Errors[0])
	waiting.Errors[1].Path = "pkg/tc/second.go"
	waiting.Errors[1].Next.Text = "wait for an owner: #432"
	waiting.Summary = "archfit: waiting, requests #431 and #432"
	fixtures = append(fixtures, viewFixture{"waiting-many", waiting, false})
	for _, tc := range []struct {
		name string
		u    result.Unknown
	}{
		{"unknown-workflow", result.Unknown{Path: ".github/workflows/archfit.yaml", Cause: "tool",
			What: "differs from main", Why: "a pull request runs its own workflow file",
			Next: result.Next{Kind: nextFix, Text: "update the branch; if the pull request changes the workflow, an owner approves it in the App"}}},
		{"unknown-outside-flow", result.Unknown{Path: ".archfit.yaml", Cause: "policy",
			What: "changed on main with no recorded approval", Why: "only owners change the policy",
			Next: result.Next{Kind: nextWait, Text: "an owner confirms or reverts it in the App: #433"}}},
		{"unknown-missing-evidence", result.Unknown{Cause: "tool", What: "the check finished without an uploaded result",
			Next: result.Next{Kind: nextFix, Text: "run the Archfit check again"}}},
	} {
		fixtures = append(fixtures, viewFixture{tc.name, result.Result{
			Status: result.StatusUnknown, Summary: "archfit: unknown, no verdict", Unknown: []result.Unknown{tc.u},
		}, false})
	}
	fixtures = append(fixtures, viewFixture{"waiting-twelve", twelveWaitingRequests(t), false})
	return fixtures
}

func twelveWaitingRequests(t *testing.T) result.Result {
	t.Helper()
	r := engineResult(t, "waiting")
	first := r.Errors[0]
	r.Errors = nil
	for i := range 12 {
		e := first
		e.Path = fmt.Sprintf("pkg/tc/netem%d.go", i+1)
		e.Next.Text = fmt.Sprintf("wait for an owner: #%d", 431+i)
		r.Errors = append(r.Errors, e)
	}
	r.Summary = "archfit: waiting, requests #431, #432, #433, #434, #435, #436, #437, #438, #439, #440 and 2 more"
	return r
}

// checkText is a check run as a reader sees it: its state, title, summary and annotations.
func checkText(v view, advisory bool) string {
	status, conclusion := checkState(v.result.Status, advisory)
	var b strings.Builder
	fmt.Fprintf(&b, "status: %s\nconclusion: %s\ntitle: %s\n\n%s\n", status, conclusion, v.title(), v.summary())
	for _, a := range v.annotations() {
		fmt.Fprintf(&b, "\nannotation %s:%d %s\n%s\n", a.Path, a.Line, a.Level, a.Message)
	}
	return b.String()
}

func golden4(t *testing.T, name, got string) {
	t.Helper()
	if found := wording.NotUsedIn(got); len(found) > 0 {
		t.Errorf("%s uses %q", name, found)
	}
	path := filepath.Join("testdata", "golden4", name)
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
		t.Errorf("%s differs from its golden file; run with -update and review the diff:\n--- got\n%s", name, got)
	}
}

func TestGoldensOfArchfit4UseNoNotUsedWord(t *testing.T) {
	entries, err := os.ReadDir(filepath.Join("testdata", "golden4"))
	if err != nil {
		t.Fatal(err)
	}
	if len(entries) == 0 {
		t.Fatal("no golden to check")
	}
	for _, e := range entries {
		raw, err := os.ReadFile(filepath.Join("testdata", "golden4", e.Name()))
		if err != nil {
			t.Fatal(err)
		}
		if found := wording.NotUsedIn(string(raw)); len(found) > 0 {
			t.Errorf("%s uses %q", e.Name(), found)
		}
	}
	for _, line := range []string{authorFix, authorAsk, authorEach, ownerAsk, designRule, designAsk, designWaiting} {
		if found := wording.NotUsedIn(line); len(found) > 0 {
			t.Errorf("%q uses %q", line, found)
		}
	}
}

func TestCheckStateOfEachStatus(t *testing.T) {
	for _, tc := range []struct {
		status                 result.Status
		advisory               bool
		wantStatus, wantResult string
	}{
		{result.StatusPass, false, "completed", "success"},
		{result.StatusPass, true, "completed", "success"},
		{result.StatusFail, false, "completed", "failure"},
		{result.StatusFail, true, "completed", "neutral"},
		{result.StatusWaiting, false, "in_progress", ""},
		{result.StatusWaiting, true, "in_progress", ""},
		{result.StatusUnknown, false, "completed", "action_required"},
		{result.StatusUnknown, true, "completed", "action_required"},
		{result.Status("other"), true, "completed", "action_required"},
		{result.Status(""), false, "completed", "action_required"},
	} {
		status, conclusion := checkState(tc.status, tc.advisory)
		if status != tc.wantStatus || conclusion != tc.wantResult {
			t.Errorf("checkState(%q, advisory %v) = %s %s, want %s %s", tc.status, tc.advisory, status, conclusion, tc.wantStatus, tc.wantResult)
		}
	}
}

func TestAuthorAndOwnerLines(t *testing.T) {
	fix := result.Next{Kind: "fix", Text: "fix the code"}
	ask := result.Next{Kind: "command", Text: `archfit request -m "<why you need it>"`}
	wait := func(n int) result.Next {
		return result.Next{Kind: "wait", Text: fmt.Sprintf("wait for an owner: #%d", n)}
	}
	errs := func(steps ...result.Next) []result.Error {
		out := make([]result.Error, len(steps))
		for i, n := range steps {
			out[i] = result.Error{Path: "a.go", Line: i + 1, Rule: "graph", What: "x", Next: n}
		}
		return out
	}
	for name, tc := range map[string]struct {
		r                   result.Result
		author, owner, show string
	}{
		"pass": {result.Result{Status: "pass"}, "", "", ""},
		"a fix and a request": {result.Result{Status: "fail", Errors: errs(fix, ask)},
			authorAsk, ownerAsk, "- next: fix the code"},
		"a fix and an expired exception": {result.Result{Status: "fail", Errors: errs(fix, wait(431))},
			authorFix, "answer #431.", "- next: wait for an owner: #431"},
		"two requests": {result.Result{Status: "waiting", Errors: errs(wait(431), wait(432), wait(431))},
			"nothing to do. This check runs again when #431 and #432 are answered.", "answer #431 and #432.", ""},
		"three requests": {result.Result{Status: "waiting", Errors: errs(wait(3), wait(2), wait(1))},
			"nothing to do. This check runs again when #3, #2 and #1 are answered.", "answer #3, #2 and #1.", ""},
		"a copy keeps its own step": {result.Result{Status: "fail", Errors: errs(result.Next{Kind: "command", Text: `use pkg_util, or run: archfit request -m "<why>"`})},
			authorAsk, ownerAsk, "- next: `use pkg_util, or run: archfit request -m \"<why>\"`"},
		"unknown keeps every step": {result.Result{Status: "unknown", Unknown: []result.Unknown{{Cause: "tool", What: "x", Next: result.Next{Kind: "wait", Text: "the check runs again"}}}},
			authorEach, "", "- next: the check runs again"},
		"a step no line names": {result.Result{Status: "fail", Errors: errs(result.Next{Kind: "restore", Text: "git checkout origin/main -- .archfit.yaml"})},
			authorEach, "", "- next: `git checkout origin/main -- .archfit.yaml`"},
	} {
		t.Run(name, func(t *testing.T) {
			v := newView(tc.r)
			if v.author != tc.author || v.owner != tc.owner {
				t.Errorf("author %q owner %q, want %q %q", v.author, v.owner, tc.author, tc.owner)
			}
			body := v.comment(maxBody)
			if tc.show != "" && !strings.Contains(body, tc.show+"\n") {
				t.Errorf("comment does not show %q:\n%s", tc.show, body)
			}
			if tc.r.Status == "pass" && (strings.Contains(body, "**Author:**") || strings.Contains(body, "**Owner:**")) {
				t.Errorf("a pass names a step:\n%s", body)
			}
		})
	}
}

// Producer text never escapes its place: no HTML, no comment marker, no code span or fence it can close, one line.
func TestProducerTextStaysInert(t *testing.T) {
	r := result.Result{Status: "fail", Summary: "archfit: fail, 1 error", Errors: []result.Error{{
		Path: "a`b/<!--x-->.go", Line: 3, Rule: "no<b>bold</b>", What: "two\nlines <!-- archfit:comment -->",
		Why: "```\n# heading", Next: result.Next{Kind: "command", Text: "run `x` and ```y```"},
	}}}
	v := newView(r)
	body := v.comment(maxBody)
	for _, bad := range []string{"<!-- archfit", "<b>", "\n# heading", "two\nlines"} {
		if strings.Contains(body, bad) {
			t.Errorf("comment carries %q:\n%s", bad, body)
		}
	}
	mustContain(t, body, "``a`b/<!--x-->.go:3``", "**no&lt;b>bold&lt;/b>**", "- next: ```` run `x` and ```y``` ````")
	summary := v.summary()
	if !strings.HasPrefix(summary, "````text\n") || !strings.HasSuffix(summary, "\n````") {
		t.Errorf("summary fence does not outrun the record's backticks:\n%s", summary)
	}
}

func TestOverlongOutputKeepsTheStatusLine(t *testing.T) {
	long := strings.Repeat("x", 2000)
	r := result.Result{Status: "fail", Summary: "archfit: fail, 200 errors"}
	for i := range 200 {
		r.Errors = append(r.Errors, result.Error{Path: "a.go", Line: i + 1, Rule: "graph", What: long, Next: result.Next{Kind: "fix", Text: "fix the code"}})
	}
	v := newView(r)
	for name, out := range map[string]string{"summary": v.summary(), "comment": v.comment(maxBody)} {
		if len(out) > maxBody {
			t.Errorf("%s is %d bytes, over %d", name, len(out), maxBody)
		}
		if !strings.Contains(out, " more not shown)") {
			t.Errorf("%s does not say records were left out", name)
		}
	}
	mustContain(t, v.summary(), "archfit: fail, 200 errors\n```")
	mustContain(t, v.comment(maxBody), "**Author:** fix the code and push.\n")
	if n := len(v.annotations()); n != annotationCap {
		t.Errorf("%d annotations, want the cap %d", n, annotationCap)
	}
}

func TestAnnotationsOnlyForErrorsWithALine(t *testing.T) {
	v := newView(engineResult(t, "expired_exception"))
	if a := v.annotations(); len(a) != 0 {
		t.Errorf("an error of the policy file without a line has annotations: %+v", a)
	}
	v = newView(engineResult(t, "graph"))
	want := []api.Annotation{{Path: "pkg/tc/netem.go", Line: 12, Level: "failure",
		Message: "pkg/tc/netem.go:12: graph: pkg_tc -> pkg_util is not an allowed dependency\n  why: a new dependency between modules needs an owner\n  balance: 9/10 (min 7)\n  next: archfit request -m \"<why you need it>\"     (do not copy the code)"}}
	if got := v.annotations(); len(got) != 1 || got[0] != want[0] {
		t.Errorf("annotations = %+v, want %+v", got, want)
	}
}

// Producer text of any size, and any number of requests, still fits GitHub's limits: each field is cut, never the
// status line's place.
func TestHugeProducerTextFitsGitHubLimits(t *testing.T) {
	huge := strings.Repeat("x", 100_000)
	ticks := strings.Repeat("`", 40_000)
	r := result.Result{Status: "waiting", Summary: "archfit: waiting, " + huge}
	for i := range 50 {
		r.Errors = append(r.Errors, result.Error{Path: ticks + "a.go", Line: i + 1, Rule: huge, What: ticks, Why: huge, Instead: huge,
			Next: result.Next{Kind: "wait", Text: fmt.Sprintf("wait for an owner: #%d", 100+i), Note: huge}})
	}
	v := newView(r)
	for name, out := range map[string]string{"title": v.title(), "summary": v.summary(), "comment": v.comment(maxBody)} {
		if len(out) > maxBody {
			t.Errorf("%s is %d bytes, over %d", name, len(out), maxBody)
		}
	}
	for _, a := range v.annotations() {
		if len(a.Message) > maxBody {
			t.Fatalf("an annotation message is %d bytes", len(a.Message))
		}
	}
	mustContain(t, v.comment(maxBody), "answer #100, #101, #102, #103, #104, #105, #106, #107, #108, #109 and 40 more.\n")
	if !strings.Contains(v.summary(), "…\n```") {
		t.Error("the cut status line does not close the summary")
	}
}

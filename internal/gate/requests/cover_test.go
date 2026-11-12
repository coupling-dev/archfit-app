package requests

import (
	"slices"
	"strconv"
	"strings"
	"testing"

	"github.com/alexei-led/archfit-app/internal/gate/records"
	"github.com/alexei-led/archfit-app/internal/wire/result"
)

func ask(from, to string) result.Error {
	return result.Error{Path: "pkg/" + from + "/x.go", Line: 3, Rule: "graph", What: from + " -> " + to + " is not an allowed dependency",
		From: from, To: to, Key: "graph " + from + " " + to + " example.com/" + to,
		Next: result.Next{Kind: nextCommand, Text: `archfit request -m "<why you need it>"`, Note: "do not copy the code"}}
}

func failed(errs ...result.Error) result.Result {
	return result.Result{Status: result.StatusFail, Summary: "archfit: fail, " + strconv.Itoa(len(errs)) + " errors", Errors: errs}
}

func openIndex(t *testing.T, keys map[string]int) *records.Index {
	t.Helper()
	x := &records.Index{Schema: records.SchemaRequestIndex}
	for k, n := range keys {
		if err := x.Add(k, strconv.Itoa(n)); err != nil {
			t.Fatal(err)
		}
	}
	return x
}

func TestCover(t *testing.T) {
	// A fix-the-code error whose key is an open request: only an error that asks is covered.
	fix := ask("a", "b")
	fix.Next = result.Next{Kind: "fix", Text: "fix the code"}
	open := map[string]int{"graph a b": 431, "graph a c": 432}
	tests := []struct {
		name    string
		in      result.Result
		ids     []int
		summary string
	}{
		{"one covered error", failed(ask("a", "b")), []int{431}, "archfit: waiting, request #431"},
		{"two errors of one request", failed(ask("a", "b"), ask("a", "b")), []int{431}, "archfit: waiting, request #431"},
		{"two requests", failed(ask("a", "c"), ask("a", "b")), []int{432, 431}, "archfit: waiting, requests #432 and #431"},
		{"an error without a request", failed(ask("a", "b"), ask("a", "d")), nil, ""},
		{"a fix-the-code error beside a covered one", failed(ask("a", "c"), fix), nil, ""},
		{"a pass", result.Result{Status: result.StatusPass, Summary: "archfit: pass"}, nil, ""},
		{"an unknown", result.Result{Status: result.StatusUnknown, Errors: []result.Error{ask("a", "b")}}, nil, ""},
		{"a fail without errors", result.Result{Status: result.StatusFail}, nil, ""},
		{"an expired exception asks nothing", failed(result.Error{Rule: "exception", From: "a", To: "b",
			Next: result.Next{Kind: nextCommand, Text: "archfit request"}}), nil, ""},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			shown, ids := Cover(tt.in, openIndex(t, open))
			if !slices.Equal(ids, tt.ids) {
				t.Fatalf("ids = %v, want %v", ids, tt.ids)
			}
			if tt.ids == nil {
				if shown.Status != tt.in.Status || shown.Summary != tt.in.Summary {
					t.Errorf("an uncovered result changed: %+v", shown)
				}
				return
			}
			if shown.Status != result.StatusWaiting || shown.Summary != tt.summary {
				t.Errorf("shown = %s %q, want waiting %q", shown.Status, shown.Summary, tt.summary)
			}
			for _, e := range shown.Errors {
				if e.Next.Kind != nextWait || !strings.HasPrefix(e.Next.Text, waitPrefix) || e.Next.Note != "" {
					t.Errorf("next = %+v", e.Next)
				}
			}
			if tt.in.Errors[0].Next.Kind != nextCommand {
				t.Error("Cover changed its input")
			}
		})
	}
	if _, ids := Cover(failed(ask("a", "b")), nil); ids != nil {
		t.Error("no index covered an error")
	}
}

func TestCoverMatchesTheWaitingGolden(t *testing.T) {
	r := result.Result{Status: result.StatusFail, Summary: "archfit: fail, 1 error", Errors: []result.Error{{
		Path: "pkg/tc/netem.go", Line: 12, Rule: "graph", What: "pkg_tc -> pkg_util is not an allowed dependency",
		Why: "a new dependency between modules needs an owner", Balance: &result.Balance{Value: 9, Min: 7},
		Next: result.Next{Kind: nextCommand, Text: `archfit request -m "<why you need it>"`, Note: "do not copy the code"},
		From: "pkg_tc", To: "pkg_util", Key: "graph pkg_tc pkg_util github.com/alexei-led/pumba/pkg/util"}}}
	shown, _ := Cover(r, openIndex(t, map[string]int{"graph pkg_tc pkg_util": 431}))
	want := "pkg/tc/netem.go:12: graph: pkg_tc -> pkg_util is not an allowed dependency\n" +
		"  why: a new dependency between modules needs an owner\n  balance: 9/10 (min 7)\n" +
		"  next: wait for an owner: #431\narchfit: waiting, request #431\n"
	if got := result.Record(shown); got != want {
		t.Errorf("record:\n%s\nwant (wire/result testdata/waiting.txt):\n%s", got, want)
	}
}

func TestRequestNames(t *testing.T) {
	var twelve []int
	for i := range 12 {
		twelve = append(twelve, 1000+i)
	}
	tests := []struct {
		ids  []int
		want string
	}{
		{[]int{431}, "request #431"},
		{[]int{431, 432}, "requests #431 and #432"},
		{[]int{1, 2, 3}, "requests #1, #2 and #3"},
		{twelve, "requests #1000, #1001, #1002, #1003, #1004, #1005, #1006, #1007, #1008, #1009 and 2 more"},
	}
	for _, tt := range tests {
		if got := requestNames(tt.ids); got != tt.want {
			t.Errorf("requestNames(%v) = %q, want %q", tt.ids, got, tt.want)
		}
	}
}

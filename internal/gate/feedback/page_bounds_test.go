package feedback

import (
	"fmt"
	"strings"
	"testing"
	"unicode/utf8"

	"github.com/alexei-led/archfit-app/internal/wire/result"
)

func TestPageBindsOnlyCompleteWaitSteps(t *testing.T) {
	for _, tc := range []struct {
		kind, text string
		want       int
	}{
		{"wait", "wait for an owner: #431", 431},
		{"command", "wait for an owner: #431", 0},
		{"wait", "wait for an owner: #0", 0},
		{"wait", "wait for an owner: #-1", 0},
		{"wait", "wait for an owner: #431\n", 0},
		{"wait", "wait for an owner: #999999999999999999999999999999", 0},
		{"wait", "the check runs again", 0},
	} {
		t.Run(tc.kind+"/"+tc.text, func(t *testing.T) {
			r := engineResult(t, "waiting")
			r.Errors[0].Next = result.Next{Kind: tc.kind, Text: tc.text}
			page := BuildPage(r)
			if page.Records[0].Request != tc.want || page.Records[0].ShowNext != (tc.want == 0) {
				t.Errorf("record = %+v", page.Records[0])
			}
		})
	}
}

func TestPageBoundsProducerTextWithoutChangingTheSource(t *testing.T) {
	r := engineResult(t, "graph")
	long := strings.Repeat("界", fieldCap+1)
	r.Errors[0].What = long
	page := BuildPage(r)
	if got := page.Records[0].What; utf8.RuneCountInString(got) != fieldCap+1 || !strings.HasSuffix(got, "…") {
		t.Errorf("page did not bound the field: %d runes", utf8.RuneCountInString(got))
	}
	if r.Errors[0].What != long {
		t.Error("bounding changed the source")
	}
}

func TestWaitStepsBeyondTheNamedRequestsStayVisible(t *testing.T) {
	r := twelveWaitingRequests(t)
	r.Errors = append(r.Errors, r.Errors[0], r.Errors[10])
	v := newView(r)
	page := v.page()
	for i, record := range page.Records {
		want := i >= 10 && i != 12
		if record.ShowNext != want {
			t.Errorf("request #%d at record %d: ShowNext = %v, want %v", record.Request, i, record.ShowNext, want)
		}
	}
	body := v.comment(maxBody)
	for _, id := range []int{441, 442} {
		step := fmt.Sprintf("- next: wait for an owner: #%d\n", id)
		if !strings.Contains(body, step) {
			t.Errorf("comment hides %q", step)
		}
	}
	if !strings.Contains(page.Author, "#440 and 2 more") {
		t.Errorf("author line = %q", page.Author)
	}
}

func TestFixStepIsHiddenOnlyWhenTheAuthorLineNamesIt(t *testing.T) {
	for _, tc := range []struct {
		text, note string
		show       bool
	}{
		{"fix the code", "", false},
		{"fix the code, or ask an owner to extend the exception", "", true},
		{"fix the code", "then check again", true},
		{"repair the code", "", true},
	} {
		t.Run(tc.text+"/"+tc.note, func(t *testing.T) {
			r := engineResult(t, "deny")
			r.Errors[0].Next = result.Next{Kind: "fix", Text: tc.text, Note: tc.note}
			v := newView(r)
			if got := v.page().Records[0].ShowNext; got != tc.show {
				t.Errorf("ShowNext = %v, want %v", got, tc.show)
			}
			step := "- next: " + tc.text
			if tc.note != "" {
				step += " (" + tc.note + ")"
			}
			if got := strings.Contains(v.comment(maxBody), step+"\n"); got != tc.show {
				t.Errorf("comment includes %q = %v, want %v", step, got, tc.show)
			}
		})
	}
}

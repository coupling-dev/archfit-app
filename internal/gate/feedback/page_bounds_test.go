package feedback

import (
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

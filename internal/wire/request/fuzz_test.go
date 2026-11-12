package request_test

import (
	"errors"
	"strings"
	"testing"
	"time"
	"unicode/utf8"

	"github.com/alexei-led/archfit-app/internal/wire/request"
)

// FuzzParseComment: a parsed reason is one trimmed line of 1 to 500 characters and a parsed date is in range; every
// other request line is a rejection.
func FuzzParseComment(f *testing.F) {
	for _, seed := range []string{
		"/archfit request why",
		"/archfit request why --expires 2026-12-10",
		"/archfit request --expires",
		"x\r\n/archfit request a\rb --expires 2027-11-10 ",
		"/archfit request   ``` @team --expires 2026-02-30",
	} {
		f.Add(seed)
	}
	f.Fuzz(func(t *testing.T, body string) {
		got, ok, err := request.ParseComment(body, today)
		if !ok {
			return
		}
		if err != nil {
			var r *request.Rejection
			if !errors.As(err, &r) || r.Reply() == "" {
				t.Fatalf("err %v, want a rejection with a reply", err)
			}
			return
		}
		n := utf8.RuneCountInString(got.Reason)
		if n < 1 || n > request.MaxReason || strings.TrimSpace(got.Reason) != got.Reason || strings.ContainsAny(got.Reason, "\r\n") {
			t.Fatalf("reason %q is not one trimmed line of 1 to 500 characters", got.Reason)
		}
		if got.Expires == "" {
			return
		}
		day, err := time.Parse(time.DateOnly, got.Expires)
		if err != nil || !day.After(date(2026, 11, 10)) || day.After(date(2027, 11, 10)) {
			t.Fatalf("expires %q is out of range", got.Expires)
		}
	})
}

// FuzzBody: whatever the worker wrote, the body has exactly two blocks, the reason and the record, unchanged.
func FuzzBody(f *testing.F) {
	for _, seed := range [][2]string{{"why", "rec"}, {"```", "````\n@team"}, {"`` ` ````", "~~~\n```text"}} {
		f.Add(seed[0], seed[1])
	}
	f.Fuzz(func(t *testing.T, reason, record string) {
		if strings.ContainsAny(reason, "\r\n") || strings.Contains(record, "\r") {
			t.Skip("a reason is one line; records have LF line ends")
		}
		if !strings.HasSuffix(record, "\n") {
			record += "\n"
		}
		body := request.Issue{Rule: "graph", From: "a", To: "b", Reason: reason, Record: record}.Body()
		if got := blockContents(body); len(got) != 2 || got[0] != reason+"\n" || got[1] != record {
			t.Fatalf("blocks %q of\n%s", got, body)
		}
	})
}

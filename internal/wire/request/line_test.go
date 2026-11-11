package request_test

import (
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/alexei-led/archfit-app/internal/wire/request"
)

func TestParseComment(t *testing.T) {
	for _, tc := range []struct {
		name   string
		body   string
		today  time.Time
		want   request.Line
		reject request.Kind
	}{
		{name: "CRLF line ends", body: "hi\r\n/archfit request why\r\nbye", want: request.Line{Reason: "why"}},
		{name: "CR ends a line", body: "/archfit request why\r@team", want: request.Line{Reason: "why"}},
		{name: "a reason of 500 characters", body: "/archfit request " + strings.Repeat("é", 500), want: request.Line{Reason: strings.Repeat("é", 500)}},
		{name: "a reason of 501 characters", body: "/archfit request " + strings.Repeat("é", 501), reject: request.KindReasonTooLong},
		{name: "no reason", body: "/archfit request   ", reject: request.KindNoReason},
		{name: "only the flag", body: "/archfit request --expires 2026-12-10", reject: request.KindNoReason},
		{name: "the day after today", body: "/archfit request why --expires 2026-11-11", want: request.Line{Reason: "why", Expires: "2026-11-11"}},
		{name: "today", body: "/archfit request why --expires 2026-11-10", reject: request.KindDate},
		{name: "365 days ahead", body: "/archfit request why --expires 2027-11-10", want: request.Line{Reason: "why", Expires: "2027-11-10"}},
		{name: "366 days ahead", body: "/archfit request why --expires 2027-11-11", reject: request.KindDate},
		{name: "365 days ahead across a leap day", body: "/archfit request why --expires 2028-03-01", today: date(2027, 3, 2), want: request.Line{Reason: "why", Expires: "2028-03-01"}},
		{name: "a leap day", body: "/archfit request why --expires 2028-02-29", today: date(2028, 1, 1), want: request.Line{Reason: "why", Expires: "2028-02-29"}},
		{name: "no leap day", body: "/archfit request why --expires 2027-02-29", reject: request.KindDate},
		{name: "a date without zeros", body: "/archfit request why --expires 2026-12-1", reject: request.KindDate},
		{name: "a word after the flag", body: "/archfit request why --expires tomorrow", reject: request.KindDate},
		{name: "a lone flag at the end", body: "/archfit request why --expires", reject: request.KindDate},
		{name: "the flag inside the reason", body: "/archfit request why --expires 2026-12-10 not", want: request.Line{Reason: "why --expires 2026-12-10 not"}},
		{name: "the flag with an equals sign is text", body: "/archfit request why --expires=2026-12-10", want: request.Line{Reason: "why --expires=2026-12-10"}},
		{name: "the reason is checked before the date", body: "/archfit request --expires 2020-01-01", reject: request.KindNoReason},
		{name: "the date is that of today in its own zone", body: "/archfit request why --expires 2026-11-11", today: time.Date(2026, 11, 10, 23, 30, 0, 0, time.FixedZone("UTC-5", -5*3600)), want: request.Line{Reason: "why", Expires: "2026-11-11"}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			now := today
			if !tc.today.IsZero() {
				now = tc.today
			}
			got, ok, err := request.ParseComment(tc.body, now)
			if !ok {
				t.Fatal("not a request")
			}
			var r *request.Rejection
			switch {
			case tc.reject != "":
				if !errors.As(err, &r) || r.Kind != tc.reject {
					t.Fatalf("err %v, want rejection %s", err, tc.reject)
				}
			case err != nil:
				t.Fatalf("err %v", err)
			case got != tc.want:
				t.Errorf("got %+v, want %+v", got, tc.want)
			}
		})
	}
}

func TestParseCommentIsNotARequest(t *testing.T) {
	for _, body := range []string{
		"",
		"/archfit requests why",
		"/archfit request-why",
		"/Archfit request why",
		" /archfit request why",
		"    /archfit request why",
		"> /archfit request why",
		"reply `/archfit request <why you need it>`",
		"/archfit can pkg_tc pkg_util",
	} {
		if got, ok, err := request.ParseComment(body, today); ok || err != nil {
			t.Errorf("ParseComment(%q) = %+v, %v, %v; want not a request", body, got, ok, err)
		}
	}
}

func TestRejectionErrorNamesTheKindOnly(t *testing.T) {
	const marker = "SEEDED-MARKER"
	for _, body := range []string{
		"/archfit request " + strings.Repeat(marker, 50),
		"/archfit request " + marker + " --expires " + marker,
	} {
		_, _, err := request.ParseComment(body, today)
		var r *request.Rejection
		if !errors.As(err, &r) {
			t.Fatalf("%q: err %v, want a rejection", body, err)
		}
		if strings.Contains(err.Error(), marker) || strings.Contains(r.Reply(), marker) {
			t.Errorf("%q: the error or the reply repeats the comment", body)
		}
	}
}

func date(y int, m time.Month, d int) time.Time { return time.Date(y, m, d, 0, 0, 0, 0, time.UTC) }

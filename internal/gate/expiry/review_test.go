package expiry

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/alexei-led/archfit-app/internal/gate/feedback"
	"github.com/alexei-led/archfit-app/internal/gate/markdown"
	"github.com/alexei-led/archfit-app/internal/gate/records"
	"github.com/alexei-led/archfit-app/internal/github/api"
	"github.com/alexei-led/archfit-app/internal/store"
)

func TestExceptionLiteralsUseTheirPublicDate(t *testing.T) {
	for _, tc := range []struct {
		date    string
		now     time.Time
		expired bool
	}{
		{"2026-11-19", days(-1), false},
		{"2026-11-19", days(0), false},
		{"2026-11-19", days(1), true},
		{"2026-11-19", time.Date(2026, 11, 19, 20, 0, 0, 0, time.FixedZone("west", -5*3600)), true},
		{"2027-01-31", days(1), false},
		{"", days(1), false},
		{"invalid", days(1), false},
	} {
		x := Exception{Expires: tc.date}
		if got := x.Expired(tc.now); got != tc.expired {
			t.Errorf("Expired(%q, %s) = %v", tc.date, tc.now, got)
		}
	}
}

func TestNotificationsDoNotRemoveApprovedExceptions(t *testing.T) {
	for _, failure := range []error{errFake, api.ErrNotFound} {
		t.Run(failure.Error(), func(t *testing.T) {
			w := newWorld(t, policyWith("2026-11-19"))
			w.gh.failOn, w.gh.failErr = "CreateIssue", failure
			a, err := w.svc.Approved(t.Context(), repo, tip)
			if err != nil || a == nil || a.Tip != tip || len(w.gh.issues) != 0 {
				t.Fatalf("Approved = %+v, %v", a, err)
			}
			err = w.svc.Warn(t.Context(), repo, a, days(1))
			if failure == errFake && !errors.Is(err, errFake) || failure == api.ErrNotFound && err != nil {
				t.Errorf("Warn = %v", err)
			}
			if got := Due(a.Exceptions, records.Pull{Status: "pass", Date: "2026-11-10"}, days(1)); len(got) != 1 {
				t.Errorf("Due after a failed notification = %+v", got)
			}
			if w.repoRecord().ExpiryIssues != nil {
				t.Errorf("recorded a failed create: %+v", w.repoRecord())
			}
		})
	}
}

func TestAGoneExpiryIssueIsForgotten(t *testing.T) {
	w := newWorld(t, policyWith("2026-11-19"))
	w.check(days(-1))
	w.newPolicy(noDated)
	w.gh.failOn, w.gh.failErr = "CloseIssue", api.ErrNotFound
	w.check(days(1))
	if w.repoRecord().ExpiryIssues != nil {
		t.Errorf("gone issue left recorded: %+v", w.repoRecord())
	}
}

type failingRecordStore struct{ store.Store }

func (s failingRecordStore) CompareAndSwapAll(context.Context, store.Tenant, []store.Swap) (bool, error) {
	return false, errFake
}

func TestAFailedRecordClosesTheUnrecordedIssue(t *testing.T) {
	w := newWorld(t, policyWith("2026-11-19"))
	a, err := w.svc.Approved(t.Context(), repo, tip)
	if err != nil {
		t.Fatal(err)
	}
	w.svc = New(w.gh, failingRecordStore{w.store})
	err = w.svc.Warn(t.Context(), repo, a, days(-1))
	if !errors.Is(err, errFake) || w.gh.issues[21].open || w.repoRecord().ExpiryIssues != nil {
		t.Fatalf("Warn = %v, issue = %+v, record = %+v", err, w.gh.issues[21], w.repoRecord())
	}
	w.svc = New(w.gh, w.store)
	w.check(days(-1))
	if w.repoRecord().ExpiryIssues["exception duplication pkg_tc pkg_util 2026-11-19"] != 22 {
		t.Errorf("retry record = %+v", w.repoRecord())
	}
}

func TestFailRechecksTheApprovedTipInsideTheLease(t *testing.T) {
	for _, tc := range []struct {
		name, head, failOn string
		want               error
	}{
		{"unchanged", tip, "", nil},
		{"extension merged", "new-tip", "", feedback.ErrStaleInputs},
		{"cannot read", tip, "BranchHead", errFake},
	} {
		t.Run(tc.name, func(t *testing.T) {
			w := newWorld(t, policyWith("2026-11-19"))
			a := w.check(days(1))
			rec := records.Pull{HeadSHA: "h1", RunID: 900, RunAttempt: 1, PayloadDigest: "sha256:pp", Status: "pass", Date: "2026-11-10"}
			raw, err := records.Encode(&rec)
			if err != nil {
				t.Fatal(err)
			}
			if err := w.store.Put(t.Context(), store.Tenant(installation), records.PullKey(repoName, 7), raw); err != nil {
				t.Fatal(err)
			}
			w.gh.head, w.gh.failOn = tc.head, tc.failOn
			p := &fakePublisher{}
			err = w.svc.Fail(t.Context(), p, Failure{Repo: repo, Pull: 7, Tip: a.Tip, Record: rec, Expired: Due(a.Exceptions, rec, days(1))})
			if !errors.Is(err, tc.want) || (len(p.got) == 1) != (tc.want == nil) {
				t.Errorf("Fail = %v, publications = %d", err, len(p.got))
			}
		})
	}
}

func TestPolicyTextStaysLiteralAndBounded(t *testing.T) {
	x := Exception{Rule: "graph", From: "a`b", To: "c", Why: "@owner\n[x](https://example.com) ```", Expires: "2026-11-19"}
	text := body(x, branch, days(-1))
	for _, want := range []string{"``a`b``", markdown.CodeSpan(x.Why)} {
		if !strings.Contains(text, want) {
			t.Errorf("body has no %q: %s", want, text)
		}
	}
	x.Rule, x.From, x.To, x.Why = strings.Repeat("`", 70000), strings.Repeat("x", 70000), strings.Repeat("`", 70000), strings.Repeat("é", 70000)
	if text := body(x, strings.Repeat("`", 70000), days(-1)); len(text) >= 65536 || len(title(x)) > maxTitleBytes {
		t.Errorf("body = %d bytes, title = %d bytes", len(text), len(title(x)))
	}
}

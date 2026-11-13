package drift

import (
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/alexei-led/archfit-app/internal/github/api"
	"github.com/alexei-led/archfit-app/internal/store"
	"github.com/alexei-led/archfit-app/internal/wire/result"
)

func TestARerunOnTheSameHeadCanChangeTheDrift(t *testing.T) {
	for _, clean := range []bool{false, true} {
		t.Run(map[bool]string{false: "more drift", true: "all places read"}[clean], func(t *testing.T) {
			w := newWorld(t)
			w.push("h1", block(t, "repository_drift"))
			w.push("h2", block(t, "repository_clean"))
			state, want := block(t, "repository_drift"), Updated
			if clean {
				state, want = noUnread(t, "repository_clean"), Closed
			}
			if got := w.push("h2", state); got != want {
				t.Fatalf("same-head rerun = %q, want %q", got, want)
			}
			if w.gh.issues[11].open == clean {
				t.Errorf("issue open = %v", w.gh.issues[11].open)
			}
		})
	}
}

func TestUnreadPlacesDoNotReopenAClosedIssue(t *testing.T) {
	w := newWorld(t)
	w.push("h1", block(t, "repository_drift"))
	w.gh.issues[11].open = false
	if got := w.push("h2", block(t, "repository_clean")); got != Updated || w.gh.issues[11].open {
		t.Errorf("Pushed = %q, issue open = %v", got, w.gh.issues[11].open)
	}
}

func TestPermanentIssueFailuresDoNotFailPushes(t *testing.T) {
	for _, method := range []string{"CreateIssue", "UpdateIssueBody", "ReopenIssue", "CloseIssue"} {
		t.Run(method, func(t *testing.T) {
			w := newWorld(t)
			if method != "CreateIssue" {
				w.push("h1", block(t, "repository_drift"))
			}
			w.gh.failOn, w.gh.failErr = method, api.ErrNotFound
			state := block(t, "repository_drift")
			if method == "CloseIssue" {
				state = noUnread(t, "repository_clean")
			}
			got := w.push("h2", state)
			want, issue := None, 0
			if method == "UpdateIssueBody" || method == "ReopenIssue" {
				want, issue = Opened, 12
			}
			if got != want || w.repoRecord().DriftIssue != issue {
				t.Errorf("Pushed = %q, record = %+v", got, w.repoRecord())
			}
		})
	}
}

type failingRecordStore struct{ store.Store }

func (s failingRecordStore) CompareAndSwapAll(context.Context, store.Tenant, []store.Swap) (bool, error) {
	return false, errFake
}

func TestAFailedRecordClosesTheUnrecordedIssue(t *testing.T) {
	w := newWorld(t)
	w.svc = New(w.gh, failingRecordStore{w.store})
	_, err := w.svc.Pushed(t.Context(), Run{Repo: repo, Head: "h1", State: block(t, "repository_drift")})
	if !errors.Is(err, errFake) || w.gh.issues[11].open || w.repoRecord().DriftIssue != 0 {
		t.Fatalf("Pushed = %v, issue = %+v, record = %+v", err, w.gh.issues[11], w.repoRecord())
	}
	w.svc = New(w.gh, w.store)
	if got := w.push("h1", block(t, "repository_drift")); got != Opened || w.repoRecord().DriftIssue != 12 {
		t.Errorf("retry = %q, record = %+v", got, w.repoRecord())
	}
}

func TestLargeUntrustedTextCannotOverflowOrEscapeTheIssue(t *testing.T) {
	for _, payload := range []string{strings.Repeat("x", 70000), strings.Repeat("`", 25000)} {
		w := newWorld(t)
		state := block(t, "repository_drift")
		state.Drift[0].What = payload + "\n```\n@owner [link](https://example.com)"
		for range 80 {
			state.Unread = append(state.Unread, result.UnreadPlace{Path: payload, Reason: payload, Fix: payload})
		}
		w.push("h1", state)
		body := w.gh.issues[11].body
		if len(body) >= 65536 || !strings.Contains(body, "more places") {
			t.Errorf("issue body has %d bytes; omitted places counted = %v", len(body), strings.Contains(body, "more places"))
		}
	}
	state := block(t, "repository_drift")
	state.Drift[0].What = "error\n```\n@owner [link](https://example.com)"
	text := body(Run{Repo: repo, Head: "h1", State: state})
	if !strings.Contains(text, "````text\n") || !strings.Contains(text, "\n````\n") {
		t.Errorf("untrusted record has no safe fence: %s", text)
	}
}

func TestTransientIssueEditsKeepTheRecordForRetry(t *testing.T) {
	for _, method := range []string{"UpdateIssueBody", "ReopenIssue"} {
		t.Run(method, func(t *testing.T) {
			w := newWorld(t)
			w.push("h1", block(t, "repository_drift"))
			before := w.repoRecord()
			w.gh.failOn = method
			_, err := w.svc.Pushed(t.Context(), Run{Repo: repo, Head: "h2", State: block(t, "repository_drift")})
			if !errors.Is(err, errFake) || w.repoRecord().DriftDigest != before.DriftDigest {
				t.Fatalf("error = %v, record = %+v", err, w.repoRecord())
			}
			w.gh.failOn = ""
			if got := w.push("h2", block(t, "repository_drift")); got != Updated {
				t.Errorf("retry = %q", got)
			}
		})
	}
}

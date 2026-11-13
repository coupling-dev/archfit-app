package requests

import (
	"context"
	"encoding/json"
	"slices"
	"testing"

	"github.com/alexei-led/archfit-app/internal/gate/records"
	"github.com/alexei-led/archfit-app/internal/github/api"
	"github.com/alexei-led/archfit-app/internal/store"
)

// TestWaitJoinsARequestWhileItCoversItsKey: an answered request whose proposal is not consumed yet still takes the
// pull requests that fail on its key; once consumed it takes none, and neither does a closed one.
func TestWaitJoinsARequestWhileItCoversItsKey(t *testing.T) {
	ctx := context.Background()
	r := api.Repo{RepoRef: api.RepoRef{InstallationID: 7, FullName: "acme/shop"}}
	s := store.NewMemory()
	open := func(issue int, key string) {
		t.Helper()
		if _, err := put(ctx, s, r, issue, records.Request{Key: key, Reason: "why"}); err != nil {
			t.Fatal(err)
		}
	}
	waiting := func(issue int) []int {
		t.Helper()
		var req records.Request
		if err := records.Load(ctx, s, tenant(r), records.RequestKey(r.FullName, issue), &req); err != nil {
			t.Fatal(err)
		}
		return req.Waiting
	}
	open(431, "graph a b")
	answer := records.Approval{Kind: records.KindApprove, Request: 431, Change: json.RawMessage(`{}`), Owner: 1}
	pin := &records.Pin{Proposal: "p1", PullRequest: 50, HeadSHA: "h", Digest: "d"}
	if err := records.Answer(ctx, s, tenant(r), r.FullName, "a1", answer, pin); err != nil {
		t.Fatal(err)
	}
	open(432, "graph c d")
	if err := records.Answer(ctx, s, tenant(r), r.FullName, "a2",
		records.Approval{Kind: records.KindApprove, Request: 432, Change: json.RawMessage(`{}`), Owner: 1}, nil); err != nil {
		t.Fatal(err)
	}

	if err := Wait(ctx, s, r, 7, []int{431, 432}); err != nil {
		t.Fatal(err)
	}
	if got := waiting(431); !slices.Equal(got, []int{7}) {
		t.Errorf("request 431 (answered, its proposal open) waiting = %v, want [7]", got)
	}
	if got := waiting(432); len(got) != 0 {
		t.Errorf("request 432 (answered, no proposal) waiting = %v, want none", got)
	}

	if err := records.Consume(ctx, s, tenant(r), r.FullName,
		records.Merge{Proposal: "p1", Commit: "c1", Digest: "d", Push: records.Push{After: "c1"}, AtTip: true}); err != nil {
		t.Fatal(err)
	}
	if err := Wait(ctx, s, r, 8, []int{431}); err != nil {
		t.Fatal(err)
	}
	if got := waiting(431); !slices.Equal(got, []int{7}) {
		t.Errorf("request 431 after its consume waiting = %v, want [7] only", got)
	}
}

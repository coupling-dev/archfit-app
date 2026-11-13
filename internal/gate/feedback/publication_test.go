package feedback

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"testing"

	"github.com/alexei-led/archfit-app/internal/gate/records"
	"github.com/alexei-led/archfit-app/internal/wire/envelope"
)

func publication(t *testing.T, h *harness) records.Publication {
	t.Helper()
	var got records.Publication
	if err := records.Load(context.Background(), h.store, 1, records.PublicationKey("acme/shop", 7, headB), &got); err != nil {
		t.Fatal(err)
	}
	return got
}

func TestPublicationReceiptFollowsGitHub(t *testing.T) {
	h := newHarness(headB)
	p := pub4(prTarget(headB), 7, 1, engineResult(t, "graph"))
	p.Source = "upload"
	p.Generated = true
	h.publish4(t, p)
	got := publication(t, h)
	raw, err := json.Marshal(p.Result)
	if err != nil {
		t.Fatal(err)
	}
	if got.Pending || got.Omitted || string(got.Result) != string(raw) || got.Digest != envelope.PayloadDigest(raw) || got.Source != p.Source || got.PublishedAt != t0 {
		t.Fatalf("receipt: %+v", got)
	}
}

func TestPublicationFailureLeavesNoReceipt(t *testing.T) {
	h := newHarness(headB)
	h.comments.failOnce("CreateComment", errors.New("down"))
	if err := h.pub4().Publish4(context.Background(), pub4(prTarget(headB), 7, 1, engineResult(t, "pass"))); err == nil {
		t.Fatal("expected failure")
	}
	got := publication(t, h)
	if !got.Pending || len(got.Result) != 0 {
		t.Fatalf("expected pending marker, got %+v", got)
	}
}

func TestPublicationOmitsOversizedResult(t *testing.T) {
	h := newHarness(headB)
	p := pub4(prTarget(headB), 7, 1, engineResult(t, "pass"))
	p.Generated = true
	p.Result.Engine = strings.Repeat("x", 70000)
	h.publish4(t, p)
	got := publication(t, h)
	if !got.Omitted || got.Pending || len(got.Result) != 0 {
		t.Fatalf("expected omitted body, got %+v", got)
	}
}

func TestOlderPublicationCannotInvalidateNewerReceipt(t *testing.T) {
	h := newHarness(headB)
	newer := pub4(prTarget(headB), 9, 1, engineResult(t, "pass"))
	newer.Generated, newer.Source = true, "owner"
	h.publish4(t, newer)
	before := publication(t, h)
	older := pub4(prTarget(headB), 8, 1, engineResult(t, "deny"))
	h.publish4(t, older)
	after := publication(t, h)
	if after.Pending || after.RunID != before.RunID || after.Digest != before.Digest {
		t.Fatalf("newer receipt changed: %+v", after)
	}
}

func TestOrdinaryPublicationRemovesPending(t *testing.T) {
	h := newHarness(headB)
	p := pub4(prTarget(headB), 7, 1, engineResult(t, "pass"))
	h.publish4(t, p)
	var rec records.Publication
	err := records.Load(context.Background(), h.store, 1, records.PublicationKey("acme/shop", 7, headB), &rec)
	if !errors.Is(err, records.ErrNoRecord) {
		t.Fatalf("ordinary result stored a receipt: %+v, %v", rec, err)
	}
}

func TestPublicationLeaseOrdersReceipts(t *testing.T) {
	h := newHarness(headB)
	reached, release, contended, retry := make(chan struct{}), make(chan struct{}), make(chan struct{}), make(chan struct{})
	h.checks.afterWrite = func() { close(reached); <-release }
	h.pub.(*serialized).wait = func(context.Context) error { close(contended); <-retry; return nil }
	first := pub4(prTarget(headB), 7, 1, engineResult(t, "pass"))
	first.Generated = true
	first.Source = "upload"
	second := pub4(prTarget(headB), 8, 1, engineResult(t, "deny"))
	second.Generated = true
	second.Source = "sweep"
	a, b := make(chan error, 1), make(chan error, 1)
	go func() { a <- h.pub.Publish4(context.Background(), first) }()
	<-reached
	go func() { b <- h.pub.Publish4(context.Background(), second) }()
	<-contended
	close(release)
	if err := <-a; err != nil {
		t.Fatal(err)
	}
	close(retry)
	if err := <-b; err != nil {
		t.Fatal(err)
	}
	got := publication(t, h)
	if got.RunID != 8 || got.Pending || got.Source != "sweep" {
		t.Fatalf("receipt %+v", got)
	}
}

func TestNewPendingMarkerCannotBeConfirmedByOldWriter(t *testing.T) {
	h := newHarness(headB)
	p := pub4(prTarget(headB), 7, 1, engineResult(t, "pass"))
	p.Generated = true
	p.Source = "upload"
	h.checks.afterWrite = func() {
		marker := records.Publication{HeadSHA: headB, RunID: 7, RunAttempt: 1, Pending: true, Source: "upload"}
		raw, err := records.Encode(&marker)
		if err != nil {
			panic(err)
		}
		if err = h.store.Put(context.Background(), 1, records.PublicationKey("acme/shop", 7, headB), raw); err != nil {
			panic(err)
		}
	}
	h.publish4(t, p)
	got := publication(t, h)
	if !got.Pending || len(got.Result) != 0 {
		t.Fatalf("newer pending marker overwritten: %+v", got)
	}
}

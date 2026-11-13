package feedback

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"testing"

	"github.com/alexei-led/archfit-app/internal/gate/records"
	"github.com/alexei-led/archfit-app/internal/store"
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

type receiptWriteFailure struct{ store.Store }

func (s receiptWriteFailure) CompareAndSwapAll(ctx context.Context, tenant store.Tenant, swaps []store.Swap) (bool, error) {
	for _, swap := range swaps {
		if !strings.HasPrefix(swap.Key, "v4/publication/") || swap.Value == nil {
			continue
		}
		var pub records.Publication
		if err := records.Decode(swap.Value, &pub); err != nil {
			return false, err
		}
		if !pub.Pending {
			return false, errors.New("receipt write unavailable")
		}
	}
	return s.Store.CompareAndSwapAll(ctx, tenant, swaps)
}

func TestReceiptWriteFailureDoesNotRetryGitHub(t *testing.T) {
	h := newHarness(headB)
	h.pub = New4(h.checks, h.comments, h.pulls, receiptWriteFailure{h.store}, appURL, h.clock.now)
	p := pub4(prTarget(headB), 7, 1, engineResult(t, "policy_differs"))
	p.Generated = true
	p.Source = "upload"
	h.publish4(t, p)
	got := publication(t, h)
	if !got.Pending || len(got.Result) != 0 {
		t.Fatalf("receipt %+v", got)
	}
	if len(h.checks.named(headB, gateCheck)) != 1 || len(h.comments.all()) != 1 {
		t.Fatal("GitHub write was retried or missing")
	}
}

func TestSupersededUploadCannotClaimAnotherPendingMarker(t *testing.T) {
	h := newHarness(headB)
	key := records.PublicationKey("acme/shop", 7, headB)
	newer := records.Publication{HeadSHA: headB, RunID: 7, RunAttempt: 1, Pending: true, Staged: true, Source: "upload", Token: "newer"}
	raw, err := records.Encode(&newer)
	if err != nil {
		t.Fatal(err)
	}
	if err = h.store.Put(t.Context(), 1, key, raw); err != nil {
		t.Fatal(err)
	}
	p := pub4(prTarget(headB), 7, 1, engineResult(t, "pass"))
	p.StagedToken = "older"
	p.Source = "upload"
	if err = h.pub.Publish4(t.Context(), p); err == nil {
		t.Fatal("superseded upload was published")
	}
	got := publication(t, h)
	if !got.Pending || got.Token != "newer" || len(h.checks.named(headB, gateCheck)) != 0 {
		t.Fatalf("newer marker changed: %+v", got)
	}
}

func TestRepublishCannotClaimStagedUpload(t *testing.T) {
	h := newHarness(headB)
	key := records.PublicationKey("acme/shop", 7, headB)
	marker := records.Publication{HeadSHA: headB, RunID: 7, RunAttempt: 1, Pending: true, Staged: true, Source: "upload", Token: "new-upload"}
	raw, err := records.Encode(&marker)
	if err != nil {
		t.Fatal(err)
	}
	if err = h.store.Put(t.Context(), 1, key, raw); err != nil {
		t.Fatal(err)
	}
	p := pub4(prTarget(headB), 7, 1, engineResult(t, "waiting"))
	p.Source = "upload"
	if err = h.pub.Publish4(t.Context(), p); !errors.Is(err, ErrStaleInputs) {
		t.Fatalf("publication error %v", err)
	}
	got := publication(t, h)
	if !got.Staged || got.Token != "new-upload" || len(h.checks.named(headB, gateCheck)) != 0 {
		t.Fatalf("staged upload changed: %+v", got)
	}
}

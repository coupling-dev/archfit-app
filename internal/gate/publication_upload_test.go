package gate

import (
	"context"
	"encoding/json"
	"errors"
	"testing"

	"github.com/alexei-led/archfit-app/internal/gate/ingest"
	"github.com/alexei-led/archfit-app/internal/gate/records"
	"github.com/alexei-led/archfit-app/internal/store"
	"github.com/alexei-led/archfit-app/internal/wire/envelope"
	"github.com/alexei-led/archfit-app/internal/wire/result"
)

func TestUploadPublishesAppResultReceipt(t *testing.T) {
	w := newWorld4(t)
	p := w.openPull(w.shop, 7)
	spec := w.spec4(p, "pass")
	w.setFile(w.shop, spec.claims.SHA, ingest.WorkflowPath, []byte("different workflow"))
	w.wantStatus(w.send4(spec), "unknown", reasonWorkflowChanged)
	var receipt records.Publication
	if err := records.Load(context.Background(), w.mem, store.Tenant(shopInstallation), records.PublicationKey(shopName, p.number, p.head), &receipt); err != nil {
		t.Fatal(err)
	}
	var got result.Result
	if receipt.Pending || receipt.Source != "upload" || envelope.PayloadDigest(receipt.Result) != receipt.Digest {
		t.Fatalf("receipt %+v", receipt)
	}
	if err := json.Unmarshal(receipt.Result, &got); err != nil {
		t.Fatal(err)
	}
	if got.Status != result.StatusUnknown || len(got.Unknown) == 0 || got.Unknown[0].What == "" {
		t.Fatalf("result %+v", got)
	}
}

func TestUploadFailedPublicationLeavesPending(t *testing.T) {
	w := newWorld4(t)
	p := w.openPull(w.shop, 7)
	spec := w.spec4(p, "pass")
	w.gh.fail["CreateCheckRun"] = errors.New("down")
	if answer := w.send4(spec); answer.status < 500 {
		t.Fatalf("status %d, want failure", answer.status)
	}
	var receipt records.Publication
	if err := records.Load(context.Background(), w.mem, store.Tenant(shopInstallation), records.PublicationKey(shopName, p.number, p.head), &receipt); err != nil {
		t.Fatal(err)
	}
	if !receipt.Pending || len(receipt.Result) != 0 {
		t.Fatalf("receipt %+v", receipt)
	}
}

func TestOlderUploadCannotInvalidateNewerPublication(t *testing.T) {
	w := newWorld4(t)
	p := w.openPull(w.shop, 7)
	ctx := context.Background()
	key := records.PublicationKey(shopName, p.number, p.head)
	confirmed := records.Publication{HeadSHA: p.head, RunID: p.runID + 1, RunAttempt: 1, Source: "sweep", Result: []byte(`{"status":"unknown"}`)}
	raw, err := records.Encode(&confirmed)
	if err != nil {
		t.Fatal(err)
	}
	if err = w.mem.Put(ctx, store.Tenant(shopInstallation), key, raw); err != nil {
		t.Fatal(err)
	}
	u := ingest.Upload4{Binding: ingest.Binding4{Repo: w.shop, PullRequest: p.number, HeadSHA: p.head, RunID: p.runID, RunAttempt: 1}}
	if err = w.gate.v4.storePull(ctx, u, decision4{Status: result.StatusPass}); err != nil {
		t.Fatal(err)
	}
	got, err := w.mem.Get(ctx, store.Tenant(shopInstallation), key)
	if err != nil {
		t.Fatal(err)
	}
	if string(got) != string(raw) {
		t.Fatalf("newer publication changed: %s", got)
	}
}

func (w *world4) wantPublication(p *pull, source string, status result.Status) {
	w.t.Helper()
	var pub records.Publication
	if err := records.Load(w.t.Context(), w.mem, store.Tenant(shopInstallation), records.PublicationKey(shopName, p.number, p.head), &pub); err != nil {
		w.t.Fatal(err)
	}
	var got result.Result
	if err := json.Unmarshal(pub.Result, &got); err != nil {
		w.t.Fatal(err)
	}
	if pub.Pending || pub.Omitted || pub.HeadSHA != p.head || pub.Source != source || pub.Digest != envelope.PayloadDigest(pub.Result) || got.Status != status {
		w.t.Fatalf("publication %+v; status %s", pub, got.Status)
	}
}

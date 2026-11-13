package gate

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"strings"
	"testing"

	"github.com/alexei-led/archfit-app/internal/gate/feedback"
	"github.com/alexei-led/archfit-app/internal/gate/ingest"
	"github.com/alexei-led/archfit-app/internal/gate/inputs"
	"github.com/alexei-led/archfit-app/internal/gate/records"
	"github.com/alexei-led/archfit-app/internal/github/api"
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
	if err = w.gate.v4.storePull(ctx, u, decision4{Status: result.StatusPass}, true); err != nil {
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

type pullSummaryGitHub struct{ *fakeGitHub }

func (g pullSummaryGitHub) PullSummary(ctx context.Context, r api.RepoRef, n int) (api.Pull, error) {
	return g.Pull(ctx, r, n)
}

func TestUnpublishedPolicyProposalDoesNotStayPending(t *testing.T) {
	w := newWorld4(t)
	p := w.openPull(w.shop, 7)
	w.put4(records.ProposalKey(shopName, "p1"), &records.Proposal{PullRequest: p.number, HeadSHA: p.head, ExpectedDigest: "d", State: records.StateOpen})
	index := records.Index{Schema: records.SchemaProposalIndex}
	if err := index.Add(records.PullEntry(p.number), "p1"); err != nil {
		t.Fatal(err)
	}
	w.put4(records.ProposalIndexKey(shopName), &index)
	w.wantStatus(w.send4(w.spec4(p, "pass")), "pass", "")
	got, err := NewPullViews(pullSummaryGitHub{w.gh}, w.mem).PullPage(t.Context(), w.shop, p.number)
	if err != nil {
		t.Fatal(err)
	}
	if got.View != nil || got.Message != "No result yet." {
		t.Fatalf("unpublished proposal page %+v", got)
	}
}

type failPublicationLease struct{ store.Store }

func (s failPublicationLease) Get(ctx context.Context, t store.Tenant, key string) ([]byte, error) {
	if strings.HasPrefix(key, "feedback/lease/") {
		return nil, errors.New("lease unavailable")
	}
	return s.Store.Get(ctx, t, key)
}

func TestUploadStagesPendingBeforeLeaseFailure(t *testing.T) {
	w := newWorld4(t)
	p := w.openPull(w.shop, 7)
	body, err := os.ReadFile("../wire/result/testdata/result/pass.json")
	if err != nil {
		t.Fatal(err)
	}
	res, err := result.Decode(body)
	if err != nil {
		t.Fatal(err)
	}
	u := ingest.Upload4{Binding: ingest.Binding4{Repo: w.shop, PullRequest: p.number, HeadSHA: p.head, RunID: p.runID, RunAttempt: 1}, Envelope: envelope.EnvelopeV2{PayloadDigest: envelope.PayloadDigest(body), PolicyDigest: "approved"}, Result: &res}
	if err = w.gate.v4.storePull(t.Context(), u, decision4{Status: result.StatusPass}, true); err != nil {
		t.Fatal(err)
	}
	w.gh.artifacts[p.runID] = body
	publisher := feedback.New4(w.gh, w.gh, w.gh, failPublicationLease{w.mem}, testAudience, w.clock)
	err = publisher.Publish4(t.Context(), feedback.Publication4{Target: inputs.Target{Repo: w.shop, PullRequest: p.number, HeadSHA: p.head}, RunID: p.runID, RunAttempt: 1, Result: res})
	if err == nil {
		t.Fatal("expected lease failure")
	}
	page, err := NewPullViews(pullSummaryGitHub{w.gh}, w.mem).PullPage(t.Context(), w.shop, p.number)
	if err != nil {
		t.Fatal(err)
	}
	if page.View != nil || page.Message != "The App has not published this result yet." {
		t.Fatalf("page %+v", page)
	}
}

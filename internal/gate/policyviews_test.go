package gate

import (
	"context"
	"errors"

	"testing"

	"github.com/alexei-led/archfit-app/internal/gate/records"
	"github.com/alexei-led/archfit-app/internal/store"
	"github.com/alexei-led/archfit-app/internal/wire/policy3"
)

func TestPolicyStateReadsRepositoryRecord(t *testing.T) {
	w := newWorld4(t)
	ctx := context.Background()
	digest := policy3.Digest([]byte(policy4))
	got, err := w.gate.PolicyState(ctx, w.shop)
	if err != nil || got.ApprovedDigest != digest || got.OutsideFlow {
		t.Fatalf("approved: %+v %v", got, err)
	}
	data, err := records.Encode(&records.Repo{ApprovedDigest: digest, OutsideFlow: &records.OutsideFlow{Digest: "other"}})
	if err != nil {
		t.Fatal(err)
	}
	if err = w.mem.Put(ctx, store.Tenant(w.shop.InstallationID), records.RepoKey(w.shop.FullName), data); err != nil {
		t.Fatal(err)
	}
	got, err = w.gate.PolicyState(ctx, w.shop)
	if err != nil || got.ApprovedDigest != digest || !got.OutsideFlow {
		t.Fatalf("outside: %+v %v", got, err)
	}
	w.unrecorded()
	got, err = w.gate.PolicyState(ctx, w.shop)
	if err != nil || got != (PolicyState{}) {
		t.Fatalf("missing: %+v %v", got, err)
	}
}

func TestPolicyDigestIsConsumedDigest(t *testing.T) {
	ctx := context.Background()
	s := store.NewMemory()
	tenant := store.Tenant(9)
	name := "acme/app"
	raw := []byte(policy4)
	digest := policy3.Digest(raw)
	p := &records.Proposal{Kind: records.ProposalFirst, PullRequest: 21, HeadSHA: "head", ExpectedDigest: digest, State: records.StateOpen}
	data, err := records.Encode(p)
	if err != nil {
		t.Fatal(err)
	}
	if err = s.Put(ctx, tenant, records.ProposalKey(name, "p"), data); err != nil {
		t.Fatal(err)
	}
	m := records.Merge{Proposal: "p", Commit: "merged", Digest: policy3.Digest(append(raw, 'x')), Push: records.Push{Before: "before", After: "merged"}, AtTip: true}
	if err = records.Consume(ctx, s, tenant, name, m); !errors.Is(err, records.ErrMoved) {
		t.Fatalf("modified digest: %v", err)
	}
	m.Digest = digest
	if err = records.Consume(ctx, s, tenant, name, m); err != nil {
		t.Fatalf("matching digest: %v", err)
	}
	var saved records.Repo
	if err = records.Load(ctx, s, tenant, records.RepoKey(name), &saved); err != nil || saved.ApprovedDigest != digest {
		t.Fatalf("record: %+v %v", saved, err)
	}
}

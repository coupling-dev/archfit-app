package records_test

import (
	"context"
	"errors"
	"testing"

	"github.com/coupling-dev/archfit-app/internal/gate/records"
	"github.com/coupling-dev/archfit-app/internal/store"
)

func editSetup(t *testing.T) (store.Store, records.Merge) {
	t.Helper()
	s := store.NewMemory()
	put(t, s, records.RepoKey(repoName), &records.Repo{ApprovedDigest: "old", ApprovedCommit: "target"})
	edit := records.Edit{File: []byte("new"), Base: "old", Target: "target"}
	pin := records.Pin{Proposal: "p1", PullRequest: 12, HeadSHA: "head", Digest: "new"}
	if err := records.EditPolicy(context.Background(), s, tenant, repoName, "e1", ownerApproval(records.KindEdit), edit, pin); err != nil {
		t.Fatal(err)
	}
	return s, records.Merge{Proposal: "p1", Commit: "merge", Parent: "target", Digest: "new", Push: records.Push{Before: "target", After: "merge"}, AtTip: true, Over: "target"}
}

func TestEditConsumedOnlyOnceOnItsExactTarget(t *testing.T) {
	for _, parent := range []string{"", "another", "target"} {
		t.Run(parent, func(t *testing.T) {
			s, m := editSetup(t)
			m.Parent = parent
			err := records.Consume(context.Background(), s, tenant, repoName, m)
			if parent != "target" {
				if !errors.Is(err, records.ErrMoved) {
					t.Fatal("wrong parent accepted", err)
				}
				if p := load[records.Proposal](t, s, records.ProposalKey(repoName, "p1")); p.State != records.StateOpen {
					t.Fatal(p)
				}
				return
			}
			if err != nil {
				t.Fatal(err)
			}
			if err := records.Consume(context.Background(), s, tenant, repoName, m); err != nil {
				t.Fatal("delivery again", err)
			}
			m.Commit = "replay"
			m.Push.After = "replay"
			if err := records.Consume(context.Background(), s, tenant, repoName, m); !errors.Is(err, records.ErrMoved) {
				t.Fatal("replayed approval", err)
			}
			if r := load[records.Repo](t, s, records.RepoKey(repoName)); r.ApprovedDigest != "new" {
				t.Fatal(r)
			}
		})
	}
}

func TestRepinFromBeforeEditVoidIsRefused(t *testing.T) {
	s, _ := editSetup(t)
	if _, _, err := records.Void(context.Background(), s, tenant, repoName, "e1"); err != nil {
		t.Fatal(err)
	}
	put(t, s, records.RequestKey(repoName, 431), &records.Request{Key: "graph a b", State: records.StateOpen})
	pin := &records.Pin{Proposal: "p1", PullRequest: 12, HeadSHA: "stale", Digest: "new"}
	a := ownerApproval(records.KindApprove)
	a.Request = 431
	if err := records.Answer(context.Background(), s, tenant, repoName, "a2", a, pin); !errors.Is(err, records.ErrMoved) {
		t.Fatal("stale answer repinned", err)
	}
	pin.EditEpoch = 1
	if err := records.Answer(context.Background(), s, tenant, repoName, "a2", a, pin); err != nil {
		t.Fatal("fresh recomputation refused", err)
	}
	if err := records.Join(context.Background(), s, tenant, repoName, "a2", "p1", "stale", "new", 0); !errors.Is(err, records.ErrMoved) {
		t.Fatal("stale resync repinned", err)
	}
	p := load[records.Proposal](t, s, records.ProposalKey(repoName, "p1"))
	if p.EditVoided || p.EditEpoch != 1 {
		t.Fatal("fresh pin lost its generation", p)
	}
}

func TestVoidingEditInvalidatesTheOldPinAndNeverReopensZero(t *testing.T) {
	s, m := editSetup(t)
	request, reopened, err := records.Void(context.Background(), s, tenant, repoName, "e1")
	if err != nil || request != 0 || reopened {
		t.Fatalf("void %d %v %v", request, reopened, err)
	}
	p := load[records.Proposal](t, s, records.ProposalKey(repoName, "p1"))
	if !p.EditVoided || len(p.Approvals) != 0 {
		t.Fatal(p)
	}
	if err := records.Consume(context.Background(), s, tenant, repoName, m); !errors.Is(err, records.ErrMoved) {
		t.Fatal("old pin consumed", err)
	}
	var zero records.Request
	if err := records.Load(context.Background(), s, tenant, records.RequestKey(repoName, 0), &zero); !errors.Is(err, records.ErrNoRecord) {
		t.Fatal("request zero exists", err)
	}
}

func TestRebaseMovesTheEditTargetWithThePin(t *testing.T) {
	s, m := editSetup(t)
	if err := records.Rebase(context.Background(), s, tenant, repoName, "p1", "head2", "new", "target2", 0, []string{"e1"}); err != nil {
		t.Fatal(err)
	}
	if p := load[records.Proposal](t, s, records.ProposalKey(repoName, "p1")); p.HeadSHA != "head2" || p.ExpectedDigest != "new" {
		t.Fatalf("pin %+v", p)
	}
	if err := records.Consume(context.Background(), s, tenant, repoName, m); !errors.Is(err, records.ErrMoved) {
		t.Fatal("the old target still consumes", err)
	}
	m.Parent, m.Over, m.Push.Before = "target2", "target", "target2"
	if err := records.Consume(context.Background(), s, tenant, repoName, m); err != nil {
		t.Fatal("the new target does not consume", err)
	}
}

func TestRebaseRefusesAMovedProposal(t *testing.T) {
	for _, name := range []string{"other digest", "old epoch", "void edit", "unknown edit"} {
		t.Run(name, func(t *testing.T) {
			s, _ := editSetup(t)
			digest, epoch, edits := "new", uint64(0), []string{"e1"}
			switch name {
			case "other digest":
				digest = "other"
			case "old epoch":
				epoch = 7
			case "void edit":
				if _, _, err := records.Void(context.Background(), s, tenant, repoName, "e1"); err != nil {
					t.Fatal(err)
				}
				epoch = 1
			case "unknown edit":
				edits = []string{"e9"}
			}
			err := records.Rebase(context.Background(), s, tenant, repoName, "p1", "head2", digest, "target2", epoch, edits)
			if !errors.Is(err, records.ErrMoved) && !errors.Is(err, records.ErrNoRecord) {
				t.Fatalf("err = %v", err)
			}
			if p := load[records.Proposal](t, s, records.ProposalKey(repoName, "p1")); p.HeadSHA != "head" {
				t.Fatalf("pin moved: %+v", p)
			}
		})
	}
}

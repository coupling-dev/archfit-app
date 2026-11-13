package records_test

import (
	"context"
	"encoding/json"
	"errors"
	"reflect"
	"slices"
	"testing"

	"github.com/alexei-led/archfit-app/internal/gate/records"
	"github.com/alexei-led/archfit-app/internal/store"
)

func ownerApproval(kind string) records.Approval {
	return records.Approval{Kind: kind, Change: json.RawMessage(`{}`), Owner: 7, Login: "admin", Credential: "cred-1", At: at}
}

func bound(proposal, head string) records.Bound {
	return records.Bound{Proposal: proposal, PullRequest: 12, HeadSHA: head, Digest: "sha256:p", WorkflowDigest: "sha256:w"}
}

func reposIndex(t *testing.T, s store.Store) []records.Entry {
	t.Helper()
	return loadIndex(t, s, records.ReposKey, records.SchemaRepos).Entries
}

func TestFirstOpensTheProposalOfItsPullRequestAndListsTheRepository(t *testing.T) {
	ctx := context.Background()
	s := store.NewMemory()
	if err := records.First(ctx, s, tenant, repoName, "a1", ownerApproval(records.KindFirst), bound("p1", "h1")); err != nil {
		t.Fatal(err)
	}

	p := load[records.Proposal](t, s, records.ProposalKey(repoName, "p1"))
	want := records.Proposal{Schema: records.SchemaProposal, Kind: records.ProposalFirst, Approvals: []string{"a1"},
		PullRequest: 12, HeadSHA: "h1", ExpectedDigest: "sha256:p", WorkflowDigest: "sha256:w", State: records.StateOpen}
	if !reflect.DeepEqual(p, want) {
		t.Errorf("proposal = %+v, want %+v", p, want)
	}
	if a := load[records.Approval](t, s, records.ApprovalKey(repoName, "a1")); a.Proposal != "p1" || a.State != records.StateOpen {
		t.Errorf("approval joined %q in state %q, want p1 open", a.Proposal, a.State)
	}
	if x := loadIndex(t, s, records.ProposalIndexKey(repoName), records.SchemaProposalIndex); !reflect.DeepEqual(x.Entries,
		[]records.Entry{{Key: "12", ID: "p1"}}) {
		t.Errorf("proposal index = %v", x.Entries)
	}
	if got := reposIndex(t, s); !reflect.DeepEqual(got, []records.Entry{{Key: "acme/shop"}}) {
		t.Errorf("repositories = %v, want acme/shop", got)
	}

	// The owner approves the pull request again after a new push: the same proposal, pinned to the new head.
	if err := records.First(ctx, s, tenant, repoName, "a2", ownerApproval(records.KindFirst), bound("p1", "h2")); err != nil {
		t.Fatal(err)
	}
	if p := load[records.Proposal](t, s, records.ProposalKey(repoName, "p1")); p.HeadSHA != "h2" ||
		!slices.Equal(p.Approvals, []string{"a1", "a2"}) {
		t.Errorf("proposal = %+v, want both approvals at h2", p)
	}
}

func TestAFirstProposalIsConsumedAsTheFirstApproval(t *testing.T) {
	ctx := context.Background()
	s := store.NewMemory()
	if err := records.First(ctx, s, tenant, repoName, "a1", ownerApproval(records.KindFirst), bound("p1", "h1")); err != nil {
		t.Fatal(err)
	}
	m := records.Merge{Proposal: "p1", Commit: "m1", Digest: "sha256:p", Push: records.Push{Before: "b", After: "m1"}, AtTip: true}
	if err := records.Consume(ctx, s, tenant, repoName, m); err != nil {
		t.Fatal(err)
	}
	r := load[records.Repo](t, s, records.RepoKey(repoName))
	if r.ApprovedDigest != "sha256:p" || r.ApprovedCommit != "m1" || r.GuardedSince != "m1" {
		t.Errorf("repository = %+v, want sha256:p approved at m1, guarded since m1", r)
	}
	if x := loadIndex(t, s, records.ProposalIndexKey(repoName), records.SchemaProposalIndex); len(x.Entries) != 0 {
		t.Errorf("proposal index = %v, want empty", x.Entries)
	}
}

func TestOwnerApprovalsRefuseAndWriteNothing(t *testing.T) {
	approved := &records.Repo{ApprovedDigest: "sha256:p", ApprovedCommit: "c0"}
	outside := &records.Repo{ApprovedDigest: "sha256:p", ApprovedCommit: "c0", OutsideFlow: &records.OutsideFlow{Digest: "sha256:x", Issue: 70}}
	workflowAt12 := &records.Proposal{Kind: records.ProposalWorkflow, PullRequest: 12, HeadSHA: "h0", ExpectedDigest: "sha256:p",
		State: records.StateOpen}
	cases := []struct {
		name     string
		repo     *records.Repo
		proposal *records.Proposal // at p1, indexed on pull request 12
		usedID   bool
		act      func(context.Context, store.Store) error
		want     error
	}{
		{"a first approval of an approved repository", approved, nil, false, first, records.ErrMoved},
		{"a first approval outside the flow", outside, nil, false, first, records.ErrMoved},
		{"a first approval of a pull request a workflow proposal holds", nil, workflowAt12, false, first, records.ErrMoved},
		{"a first approval with an approval id in use", nil, nil, true, first, records.ErrExists},
		{"a workflow approval without an approved policy", nil, nil, false, workflow, records.ErrMoved},
		{"a workflow approval outside the flow", outside, nil, false, workflow, records.ErrMoved},
		{"the current policy of an approved repository", approved, nil, false, current, records.ErrMoved},
		{"the current policy outside the flow", outside, nil, false, current, records.ErrMoved},
		{"a confirm inside the flow", approved, nil, false, confirm("sha256:x"), records.ErrMoved},
		{"a confirm of another digest", outside, nil, false, confirm("sha256:y"), records.ErrMoved},
		{"a revert inside the flow", approved, nil, false, revert("sha256:x"), records.ErrMoved},
		{"a revert of another digest", outside, nil, false, revert("sha256:y"), records.ErrMoved},
		{"a revert into a workflow proposal", outside, workflowAt12, false, revert("sha256:x"), records.ErrMoved},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			s := store.NewMemory()
			if tc.repo != nil {
				put(t, s, records.RepoKey(repoName), tc.repo)
			}
			if tc.proposal != nil {
				put(t, s, records.ProposalKey(repoName, "p1"), tc.proposal)
				put(t, s, records.ProposalIndexKey(repoName), &records.Index{Schema: records.SchemaProposalIndex,
					Entries: []records.Entry{{Key: "12", ID: "p1"}}})
			}
			if tc.usedID {
				put(t, s, records.ApprovalKey(repoName, "a1"), approval(records.StateOpen))
			}
			keys := []string{records.RepoKey(repoName), records.ReposKey, records.ApprovalKey(repoName, "a1"),
				records.ProposalKey(repoName, "p1"), records.ProposalIndexKey(repoName)}
			before := snapshot(t, s, keys...)
			if err := tc.act(context.Background(), s); !errors.Is(err, tc.want) {
				t.Errorf("err = %v, want %v", err, tc.want)
			}
			if !reflect.DeepEqual(before, snapshot(t, s, keys...)) {
				t.Error("a refused approval wrote a key")
			}
		})
	}
}

func first(ctx context.Context, s store.Store) error {
	return records.First(ctx, s, tenant, repoName, "a1", ownerApproval(records.KindFirst), bound("p1", "h1"))
}

func workflow(ctx context.Context, s store.Store) error {
	return records.Workflow(ctx, s, tenant, repoName, "a1", ownerApproval(records.KindWorkflow), bound("p1", "h1"))
}

func current(ctx context.Context, s store.Store) error {
	return records.ApproveCurrent(ctx, s, tenant, repoName, "a1", ownerApproval(records.KindFirst), "c1", "sha256:p")
}

func confirm(digest string) func(context.Context, store.Store) error {
	return func(ctx context.Context, s store.Store) error {
		_, err := records.Confirm(ctx, s, tenant, repoName, "a1", ownerApproval(records.KindConfirm), "c1", digest)
		return err
	}
}

func revert(outside string) func(context.Context, store.Store) error {
	return func(ctx context.Context, s store.Store) error {
		return records.RevertTo(ctx, s, tenant, repoName, "a1", ownerApproval(records.KindRevert),
			records.Revert{Commit: "c0", Digest: "sha256:p", Outside: outside, Issue: 70},
			records.Pin{Proposal: "p1", PullRequest: 12, HeadSHA: "h1", Digest: "sha256:p"})
	}
}

func TestApproveCurrentStartsChangeControlAtItsCommit(t *testing.T) {
	ctx := context.Background()
	s := store.NewMemory()
	if err := current(ctx, s); err != nil {
		t.Fatal(err)
	}
	r := load[records.Repo](t, s, records.RepoKey(repoName))
	if r.ApprovedDigest != "sha256:p" || r.ApprovedCommit != "c1" || r.GuardedSince != "c1" {
		t.Errorf("repository = %+v, want sha256:p approved at c1, guarded since c1", r)
	}
	if a := load[records.Approval](t, s, records.ApprovalKey(repoName, "a1")); a.State != records.StateMerged {
		t.Errorf("approval state = %q, want merged: it took effect at once", a.State)
	}
	if got := reposIndex(t, s); !reflect.DeepEqual(got, []records.Entry{{Key: "acme/shop"}}) {
		t.Errorf("repositories = %v, want acme/shop", got)
	}
}

func TestConfirmEndsTheStateAtItsCommit(t *testing.T) {
	ctx := context.Background()
	s := store.NewMemory()
	put(t, s, records.RepoKey(repoName), &records.Repo{ApprovedDigest: "sha256:p", ApprovedCommit: "c0", GuardedSince: "c0",
		OutsideFlow: &records.OutsideFlow{Digest: "sha256:x", Issue: 70}})
	issue, err := records.Confirm(ctx, s, tenant, repoName, "a1", ownerApproval(records.KindConfirm), "c5", "sha256:x")
	if err != nil || issue != 70 {
		t.Fatalf("Confirm = %d, %v; want issue 70", issue, err)
	}
	r := load[records.Repo](t, s, records.RepoKey(repoName))
	want := records.Repo{Schema: records.SchemaRepo, ApprovedDigest: "sha256:x", ApprovedCommit: "c5", GuardedSince: "c0"}
	if !reflect.DeepEqual(r, want) {
		t.Errorf("repository = %+v, want %+v", r, want)
	}
}

func TestARevertConsumeEndsTheState(t *testing.T) {
	ctx := context.Background()
	s := store.NewMemory()
	put(t, s, records.RepoKey(repoName), &records.Repo{ApprovedDigest: "sha256:p", ApprovedCommit: "c0", GuardedSince: "c0",
		OutsideFlow: &records.OutsideFlow{Digest: "sha256:x", Issue: 70}})
	if err := revert("sha256:x")(ctx, s); err != nil {
		t.Fatal(err)
	}
	m := records.Merge{Proposal: "p1", Commit: "m2", Digest: "sha256:p", Push: records.Push{Before: "c1", After: "m2"},
		AtTip: true, Over: "c0"}
	if err := records.Consume(ctx, s, tenant, repoName, m); err != nil {
		t.Fatal(err)
	}
	r := load[records.Repo](t, s, records.RepoKey(repoName))
	want := records.Repo{Schema: records.SchemaRepo, ApprovedDigest: "sha256:p", ApprovedCommit: "m2", GuardedSince: "c0"}
	if !reflect.DeepEqual(r, want) {
		t.Errorf("repository = %+v, want %+v", r, want)
	}
}

func TestAWorkflowProposalIsConsumedByItsMergeOnce(t *testing.T) {
	ctx := context.Background()
	s := store.NewMemory()
	put(t, s, records.RepoKey(repoName), &records.Repo{ApprovedDigest: "sha256:p", ApprovedCommit: "c0"})
	if err := workflow(ctx, s); err != nil {
		t.Fatal(err)
	}
	if got := reposIndex(t, s); len(got) != 0 {
		t.Errorf("repositories = %v, want none: only a first approval lists one", got)
	}
	if err := records.ConsumeWorkflow(ctx, s, tenant, repoName, "p1", "m1"); err != nil {
		t.Fatal(err)
	}
	if p := load[records.Proposal](t, s, records.ProposalKey(repoName, "p1")); p.State != records.StateMerged || p.Commit != "m1" {
		t.Errorf("proposal %q by %q, want merged by m1", p.State, p.Commit)
	}
	if x := loadIndex(t, s, records.ProposalIndexKey(repoName), records.SchemaProposalIndex); len(x.Entries) != 0 {
		t.Errorf("proposal index = %v, want empty", x.Entries)
	}
	if err := records.ConsumeWorkflow(ctx, s, tenant, repoName, "p1", "m1"); err != nil {
		t.Errorf("the same merge again = %v, want nil", err)
	}
	if err := records.ConsumeWorkflow(ctx, s, tenant, repoName, "p1", "m2"); !errors.Is(err, records.ErrMoved) {
		t.Errorf("another merge = %v, want ErrMoved", err)
	}

	openProposal(t, s, "p2", 50, "sha256:p")
	if err := records.ConsumeWorkflow(ctx, s, tenant, repoName, "p2", "m3"); !errors.Is(err, records.ErrMoved) {
		t.Errorf("the policy proposal consumed as a workflow = %v, want ErrMoved", err)
	}
}

// TestAProposalApprovedInAStateThatEndedIsNotConsumed: a first approval once change control started, and a revert once
// the state it was made in ended, admit nothing; nothing is written.
func TestAProposalApprovedInAStateThatEndedIsNotConsumed(t *testing.T) {
	ctx := context.Background()
	m := records.Merge{Proposal: "p1", Commit: "m1", Digest: "sha256:p", Push: records.Push{Before: "b", After: "m1"}, AtTip: true}
	cases := []struct {
		name    string
		outside bool // the repository is outside the flow with sha256:x when the approval is recorded
		record  func(context.Context, store.Store) error
		later   records.Repo
	}{
		{"a first approval after change control started", false, first,
			records.Repo{ApprovedDigest: "sha256:q", ApprovedCommit: "c9"}},
		{"a revert after a confirm", true, revert("sha256:x"), records.Repo{ApprovedDigest: "sha256:x", ApprovedCommit: "c9"}},
		{"a revert after the policy changed again", true, revert("sha256:x"),
			records.Repo{ApprovedDigest: "sha256:p", ApprovedCommit: "c0", OutsideFlow: &records.OutsideFlow{Digest: "sha256:y"}}},
		{"a revert after a confirm and the same change pushed again", true, revert("sha256:x"),
			records.Repo{ApprovedDigest: "sha256:x", ApprovedCommit: "c9", OutsideFlow: &records.OutsideFlow{Digest: "sha256:x"}}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			s := store.NewMemory()
			if tc.outside {
				put(t, s, records.RepoKey(repoName), &records.Repo{ApprovedDigest: "sha256:p", ApprovedCommit: "c0",
					OutsideFlow: &records.OutsideFlow{Digest: "sha256:x", Issue: 70}})
			}
			if err := tc.record(ctx, s); err != nil {
				t.Fatal(err)
			}
			put(t, s, records.RepoKey(repoName), &tc.later)
			keys := []string{records.RepoKey(repoName), records.ProposalKey(repoName, "p1"), records.ProposalIndexKey(repoName),
				records.PolicyPushKey(repoName, m.Push)}
			before := snapshot(t, s, keys...)
			if err := records.Consume(ctx, s, tenant, repoName, m); !errors.Is(err, records.ErrMoved) {
				t.Errorf("Consume = %v, want ErrMoved", err)
			}
			if !reflect.DeepEqual(before, snapshot(t, s, keys...)) {
				t.Error("a refused consume wrote a key")
			}
		})
	}
}

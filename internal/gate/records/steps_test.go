package records_test

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"reflect"
	"strconv"
	"sync"
	"testing"

	"github.com/alexei-led/archfit-app/internal/gate/records"
	"github.com/alexei-led/archfit-app/internal/store"
)

func openRequest(ctx context.Context, s store.Store, issue int, key string) error {
	return records.Indexed(ctx, s, tenant, records.RequestKey(repoName, issue), records.RequestIndexKey(repoName),
		records.SchemaRequestIndex, func(cur []byte, x *records.Index) ([]byte, error) {
			if cur != nil {
				return nil, errors.New("request exists")
			}
			if err := x.Add(key, strconv.Itoa(issue)); err != nil {
				return nil, err
			}
			return records.Encode(&records.Request{Key: key, Reason: "why", State: records.StateOpen})
		})
}

func answerRequest(ctx context.Context, s store.Store, issue int) error {
	return records.Indexed(ctx, s, tenant, records.RequestKey(repoName, issue), records.RequestIndexKey(repoName),
		records.SchemaRequestIndex, func(cur []byte, x *records.Index) ([]byte, error) {
			var r records.Request
			if err := records.Decode(cur, &r); err != nil {
				return nil, err
			}
			x.Remove(r.Key)
			r.State = records.StateAnswered
			return records.Encode(&r)
		})
}

func TestIndexedMovesARecordAndItsEntryTogether(t *testing.T) {
	ctx := context.Background()
	s := store.NewMemory()
	if err := openRequest(ctx, s, 431, "graph a b"); err != nil {
		t.Fatalf("open: %v", err)
	}
	if got := loadIndex(t, s, records.RequestIndexKey(repoName), records.SchemaRequestIndex); !reflect.DeepEqual(got.Entries,
		[]records.Entry{{Key: "graph a b", ID: "431"}}) {
		t.Errorf("index after open: %v", got.Entries)
	}
	if err := openRequest(ctx, s, 432, "graph a b"); !errors.Is(err, records.ErrExists) {
		t.Errorf("a second open request with the same key = %v, want ErrExists", err)
	}
	if _, err := s.Get(ctx, tenant, records.RequestKey(repoName, 432)); !errors.Is(err, store.ErrNotFound) {
		t.Errorf("the refused request was written: %v", err)
	}
	if err := answerRequest(ctx, s, 431); err != nil {
		t.Fatalf("answer: %v", err)
	}
	if got := load[records.Request](t, s, records.RequestKey(repoName, 431)); got.State != records.StateAnswered {
		t.Errorf("state %q, want answered", got.State)
	}
	if got := loadIndex(t, s, records.RequestIndexKey(repoName), records.SchemaRequestIndex); len(got.Entries) != 0 {
		t.Errorf("index after answer: %v", got.Entries)
	}
}

func TestIndexedLeavesAnUntouchedIndexMissing(t *testing.T) {
	ctx := context.Background()
	s := store.NewMemory()
	err := records.Indexed(ctx, s, tenant, records.PullKey(repoName, 5), records.PullIndexKey(repoName),
		records.SchemaPullIndex, func([]byte, *records.Index) ([]byte, error) {
			return records.Encode(&records.Pull{HeadSHA: "abc", Status: "pass"})
		})
	if err != nil {
		t.Fatalf("Indexed: %v", err)
	}
	if _, err := s.Get(ctx, tenant, records.PullIndexKey(repoName)); !errors.Is(err, store.ErrNotFound) {
		t.Errorf("an index with no change was created: %v", err)
	}
}

func TestJoin(t *testing.T) {
	ctx := context.Background()
	tests := []struct {
		name     string
		approval string // state of a1; "" means it joined p2 already
		proposal string // state of p1
		before   int    // approvals already in p1
		want     error
	}{
		{"an open approval joins", records.StateOpen, records.StateOpen, 0, nil},
		{"the approval already joined another proposal", "", records.StateOpen, 0, records.ErrJoined},
		{"a void approval", records.StateVoid, records.StateOpen, 0, records.ErrMoved},
		{"a merged proposal", records.StateOpen, records.StateMerged, 0, records.ErrMoved},
		{"a full proposal", records.StateOpen, records.StateOpen, records.MaxApprovals, records.ErrFull},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			s := store.NewMemory()
			a := approval(records.StateOpen)
			if tt.approval == "" {
				a.Proposal = "p2"
			} else {
				a.State = tt.approval
			}
			put(t, s, records.ApprovalKey(repoName, "a1"), a)
			p := &records.Proposal{ExpectedDigest: "d", State: tt.proposal}
			for i := range tt.before {
				p.Approvals = append(p.Approvals, "x"+strconv.Itoa(i))
			}
			put(t, s, records.ProposalKey(repoName, "p1"), p)
			beforeA, _ := s.Get(ctx, tenant, records.ApprovalKey(repoName, "a1"))
			beforeP, _ := s.Get(ctx, tenant, records.ProposalKey(repoName, "p1"))

			err := records.Join(ctx, s, tenant, repoName, "a1", "p1", "h2", "d2")
			if !errors.Is(err, tt.want) {
				t.Fatalf("Join = %v, want %v", err, tt.want)
			}
			afterA, _ := s.Get(ctx, tenant, records.ApprovalKey(repoName, "a1"))
			afterP, _ := s.Get(ctx, tenant, records.ProposalKey(repoName, "p1"))
			if tt.want != nil {
				if !bytes.Equal(beforeA, afterA) || !bytes.Equal(beforeP, afterP) {
					t.Error("a refused join changed a record")
				}
				return
			}
			if got := load[records.Approval](t, s, records.ApprovalKey(repoName, "a1")); got.Proposal != "p1" {
				t.Errorf("approval proposal %q, want p1", got.Proposal)
			}
			if got := load[records.Proposal](t, s, records.ProposalKey(repoName, "p1")); !reflect.DeepEqual(got.Approvals, []string{"a1"}) ||
				got.HeadSHA != "h2" || got.ExpectedDigest != "d2" {
				t.Errorf("proposal %+v, want approvals [a1] pinned to h2 and d2", got)
			}
			if err := records.Join(ctx, s, tenant, repoName, "a1", "p1", "h2", "d2"); err != nil {
				t.Errorf("the same join again = %v, want nil", err)
			}
			if err := records.Join(ctx, s, tenant, repoName, "a1", "p1", "h3", "d3"); err != nil {
				t.Errorf("the same join after a rewrite = %v, want nil", err)
			}
			if got := load[records.Proposal](t, s, records.ProposalKey(repoName, "p1")); len(got.Approvals) != 1 ||
				got.HeadSHA != "h3" || got.ExpectedDigest != "d3" {
				t.Errorf("the same join again: %+v, want one approval pinned to h3 and d3", got)
			}
		})
	}
}

func TestAnApprovalJoinsOneProposalOnly(t *testing.T) {
	forEachBackend(t, 2, func(t *testing.T, clients []store.Store) {
		ctx := context.Background()
		put(t, clients[0], records.ApprovalKey(repoName, "a1"), approval(records.StateOpen))
		for _, id := range []string{"p1", "p2"} {
			put(t, clients[0], records.ProposalKey(repoName, id), &records.Proposal{ExpectedDigest: "d", State: records.StateOpen})
		}
		errs := make([]error, 2)
		var wg sync.WaitGroup
		for i, id := range []string{"p1", "p2"} {
			wg.Go(func() { errs[i] = records.Join(ctx, clients[i], tenant, repoName, "a1", id, "h2", "d2") })
		}
		wg.Wait()
		won := -1
		for i, err := range errs {
			switch {
			case err == nil && won < 0:
				won = i
			case err == nil:
				t.Fatal("the approval joined both proposals")
			case !errors.Is(err, records.ErrJoined):
				t.Fatalf("the losing join = %v, want ErrJoined", err)
			}
		}
		if won < 0 {
			t.Fatal("no join won")
		}
		winner, loser := []string{"p1", "p2"}[won], []string{"p1", "p2"}[1-won]
		if got := load[records.Approval](t, clients[0], records.ApprovalKey(repoName, "a1")); got.Proposal != winner {
			t.Errorf("approval proposal %q, want %q", got.Proposal, winner)
		}
		if got := load[records.Proposal](t, clients[0], records.ProposalKey(repoName, loser)); len(got.Approvals) != 0 {
			t.Errorf("the losing proposal holds %v", got.Approvals)
		}
		if err := records.Join(ctx, clients[0], tenant, repoName, "a1", loser, "h2", "d2"); !errors.Is(err, records.ErrJoined) {
			t.Errorf("a later join of the other proposal = %v, want ErrJoined", err)
		}
	})
}

// merge is the consume of proposal by its merge commit, landed by a push to commit while the tip has digest.
func merge(proposal, commit, digest string) records.Merge {
	return records.Merge{Proposal: proposal, Commit: commit, Digest: digest, Push: records.Push{After: commit}, AtTip: true}
}

// answeredIn opens request issue with key and answers it with approval id, joining proposal at digest.
func answeredIn(t *testing.T, s store.Store, proposal, id string, issue int, key, digest string) {
	t.Helper()
	ctx := context.Background()
	if err := openRequest(ctx, s, issue, key); err != nil {
		t.Fatal(err)
	}
	a := records.Approval{Kind: records.KindApprove, Request: issue, Change: json.RawMessage(`{}`), Owner: 99, At: at}
	if err := records.Answer(ctx, s, tenant, repoName, id, a, &records.Pin{Proposal: proposal, HeadSHA: "h", Digest: digest}); err != nil {
		t.Fatalf("answer %d: %v", issue, err)
	}
}

func TestConsume(t *testing.T) {
	ctx := context.Background()
	s := store.NewMemory()
	put(t, s, records.RepoKey(repoName), &records.Repo{ApprovedDigest: "old", DriftIssue: 3})
	openProposal(t, s, "p1", 50, "new")
	openProposal(t, s, "p2", 51, "other")
	answeredIn(t, s, "p1", "a1", 431, "graph a b", "new")
	answeredIn(t, s, "p1", "a2", 432, "graph c d", "new")
	answeredIn(t, s, "p2", "a3", 433, "graph e f", "other")
	if err := openRequest(ctx, s, 434, "graph g h"); err != nil {
		t.Fatal(err)
	}

	if err := records.Consume(ctx, s, tenant, repoName, merge("p1", "c1", "wrong")); !errors.Is(err, records.ErrMoved) {
		t.Errorf("Consume with another digest = %v, want ErrMoved", err)
	}
	if err := records.Consume(ctx, s, tenant, repoName, merge("p1", "c1", "new")); err != nil {
		t.Fatalf("Consume: %v", err)
	}
	p := load[records.Proposal](t, s, records.ProposalKey(repoName, "p1"))
	if p.State != records.StateMerged || p.Commit != "c1" {
		t.Errorf("proposal %q by %q, want merged by c1", p.State, p.Commit)
	}
	if r := load[records.Repo](t, s, records.RepoKey(repoName)); r.ApprovedDigest != "new" || r.DriftIssue != 3 {
		t.Errorf("repo %+v, want approved digest new and the drift issue kept", r)
	}
	if x := loadIndex(t, s, records.ProposalIndexKey(repoName), records.SchemaProposalIndex); !reflect.DeepEqual(x.Entries,
		[]records.Entry{{Key: "51", ID: "p2"}}) {
		t.Errorf("proposal index %v, want only p2", x.Entries)
	}
	// The requests of the consumed approvals stop covering their keys; another proposal's and an open one's stay.
	if x := loadIndex(t, s, records.RequestIndexKey(repoName), records.SchemaRequestIndex); !reflect.DeepEqual(x.Entries,
		[]records.Entry{{Key: "graph e f", ID: "433"}, {Key: "graph g h", ID: "434"}}) {
		t.Errorf("request index %v, want #433 and #434", x.Entries)
	}
	if push := load[records.PolicyPush](t, s, records.PolicyPushKey(repoName, records.Push{After: "c1"})); push.Digest != "new" || push.Proposal != "p1" {
		t.Errorf("policy push = %+v, want new by p1", push)
	}

	keys := []string{records.ProposalKey(repoName, "p1"), records.ProposalIndexKey(repoName), records.RepoKey(repoName),
		records.RequestIndexKey(repoName), records.PolicyPushKey(repoName, records.Push{After: "c1"})}
	before := snapshot(t, s, keys...)
	if err := records.Consume(ctx, s, tenant, repoName, merge("p1", "c1", "new")); err != nil {
		t.Errorf("the same commit again = %v, want nil", err)
	}
	if !reflect.DeepEqual(before, snapshot(t, s, keys...)) {
		t.Error("the same commit again changed a key")
	}
	if err := records.Consume(ctx, s, tenant, repoName, merge("p1", "c2", "new")); !errors.Is(err, records.ErrMoved) {
		t.Errorf("another commit = %v, want ErrMoved", err)
	}
	if err := records.Consume(ctx, s, tenant, repoName, merge("p1", "c1", "other")); !errors.Is(err, records.ErrMoved) {
		t.Errorf("the same commit with another digest = %v, want ErrMoved", err)
	}
	if !reflect.DeepEqual(before, snapshot(t, s, keys...)) {
		t.Error("a refused consume changed a key")
	}
}

func TestAConsumeOutsideTheFlowKeepsTheApprovedDigest(t *testing.T) {
	ctx := context.Background()
	s := store.NewMemory()
	put(t, s, records.RepoKey(repoName), &records.Repo{ApprovedDigest: "old",
		OutsideFlow: &records.OutsideFlow{Digest: "unapproved", Issue: 9}})
	openProposal(t, s, "p1", 50, "new")
	if err := records.Consume(ctx, s, tenant, repoName, merge("p1", "c1", "new")); err != nil {
		t.Fatal(err)
	}
	r := load[records.Repo](t, s, records.RepoKey(repoName))
	if r.ApprovedDigest != "old" || r.OutsideFlow == nil || *r.OutsideFlow != (records.OutsideFlow{Digest: "unapproved", Issue: 9}) {
		t.Errorf("repo = %+v, want the approved digest and the state kept", r)
	}
	if p := load[records.Proposal](t, s, records.ProposalKey(repoName, "p1")); p.State != records.StateMerged {
		t.Errorf("proposal %q, want merged: its approvals are used", p.State)
	}
}

// joinOnSwap is a Store double whose first swap is preceded by an answer joining p1 at the same digest: the
// approvals Consume read are then not all of the proposal's.
type joinOnSwap struct {
	store.Store
	t      *testing.T
	joined bool
}

func (j *joinOnSwap) CompareAndSwapAll(ctx context.Context, t store.Tenant, swaps []store.Swap) (bool, error) {
	if !j.joined {
		j.joined = true
		answeredIn(j.t, j.Store, "p1", "a2", 432, "graph c d", "d")
	}
	return j.Store.CompareAndSwapAll(ctx, t, swaps)
}

func TestAConsumeReadsAnApprovalThatJoinedMeanwhile(t *testing.T) {
	ctx := context.Background()
	mem := store.NewMemory()
	openProposal(t, mem, "p1", 50, "d")
	answeredIn(t, mem, "p1", "a1", 431, "graph a b", "d")
	if err := records.Consume(ctx, &joinOnSwap{Store: mem, t: t}, tenant, repoName, merge("p1", "c1", "d")); err != nil {
		t.Fatal(err)
	}
	if x := loadIndex(t, mem, records.RequestIndexKey(repoName), records.SchemaRequestIndex); len(x.Entries) != 0 {
		t.Errorf("request index %v, want both answered requests out", x.Entries)
	}
}

func TestAMergeOfAnOlderHeadCannotConsumeAJoinedApproval(t *testing.T) {
	ctx := context.Background()
	s := store.NewMemory()
	openProposal(t, s, "p1", 50, "d1")
	put(t, s, records.ApprovalKey(repoName, "a2"), approval(records.StateOpen))
	if err := records.Join(ctx, s, tenant, repoName, "a2", "p1", "h2", "d2"); err != nil {
		t.Fatalf("Join: %v", err)
	}
	if err := records.Consume(ctx, s, tenant, repoName, merge("p1", "c-old", "d1")); !errors.Is(err, records.ErrMoved) {
		t.Errorf("a merge of the head before the join = %v, want ErrMoved", err)
	}
	if p := load[records.Proposal](t, s, records.ProposalKey(repoName, "p1")); p.State != records.StateOpen {
		t.Errorf("proposal %q, want still open", p.State)
	}
	if err := records.Consume(ctx, s, tenant, repoName, merge("p1", "c-new", "d2")); err != nil {
		t.Errorf("a merge of the pinned head = %v, want nil", err)
	}
}

func TestConsumeRefuses(t *testing.T) {
	ctx := context.Background()
	tests := []struct {
		name     string
		proposal *records.Proposal
		want     error
	}{
		{"a void proposal", &records.Proposal{PullRequest: 50, ExpectedDigest: "d", State: records.StateVoid}, records.ErrMoved},
		{"a proposal without a pull request", &records.Proposal{ExpectedDigest: "d", State: records.StateOpen}, records.ErrMoved},
		{"no proposal", nil, records.ErrNoRecord},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			s := store.NewMemory()
			if tt.proposal != nil {
				put(t, s, records.ProposalKey(repoName, "p1"), tt.proposal)
			}
			if err := records.Consume(ctx, s, tenant, repoName, merge("p1", "c1", "d")); !errors.Is(err, tt.want) {
				t.Errorf("Consume = %v, want %v", err, tt.want)
			}
			if _, err := s.Get(ctx, tenant, records.RepoKey(repoName)); !errors.Is(err, store.ErrNotFound) {
				t.Errorf("a refused consume wrote the repository: %v", err)
			}
		})
	}
	if err := records.Consume(ctx, store.NewMemory(), tenant, repoName, merge("p1", "", "d")); err == nil {
		t.Error("Consume with an empty commit = nil, want an error")
	}

	t.Run("a push judged outside the flow", func(t *testing.T) {
		s := store.NewMemory()
		openProposal(t, s, "p1", 50, "d")
		if _, _, err := records.Outside(ctx, s, tenant, repoName, records.Push{After: "c1"}, "d"); err != nil {
			t.Fatal(err)
		}
		if err := records.Consume(ctx, s, tenant, repoName, merge("p1", "c1", "d")); !errors.Is(err, records.ErrMoved) {
			t.Errorf("Consume = %v, want ErrMoved", err)
		}
		if p := load[records.Proposal](t, s, records.ProposalKey(repoName, "p1")); p.State != records.StateOpen {
			t.Errorf("proposal %q, want open", p.State)
		}
	})
}

func TestOutside(t *testing.T) {
	ctx := context.Background()
	s := store.NewMemory()
	put(t, s, records.RepoKey(repoName), &records.Repo{ApprovedDigest: "a", DriftIssue: 3})

	r, marked, err := records.Outside(ctx, s, tenant, repoName, records.Push{After: "c1"}, "x")
	if err != nil || !marked || r.OutsideFlow == nil || *r.OutsideFlow != (records.OutsideFlow{Digest: "x"}) {
		t.Fatalf("Outside = %+v, %v, %v", r, marked, err)
	}
	if got := load[records.Repo](t, s, records.RepoKey(repoName)); got.ApprovedDigest != "a" || got.DriftIssue != 3 ||
		*got.OutsideFlow != (records.OutsideFlow{Digest: "x"}) {
		t.Errorf("repo = %+v", got)
	}
	if push := load[records.PolicyPush](t, s, records.PolicyPushKey(repoName, records.Push{After: "c1"})); push != (records.PolicyPush{
		Schema: records.SchemaPolicyPush, Digest: "x"}) {
		t.Errorf("policy push = %+v", push)
	}

	if issue, err := records.OutsideIssue(ctx, s, tenant, repoName, 70); err != nil || issue != 70 {
		t.Errorf("OutsideIssue = %d, %v; want 70", issue, err)
	}
	if issue, err := records.OutsideIssue(ctx, s, tenant, repoName, 71); err != nil || issue != 70 {
		t.Errorf("a second issue = %d, %v; want the first, 70", issue, err)
	}

	keys := []string{records.RepoKey(repoName), records.PolicyPushKey(repoName, records.Push{After: "c1"})}
	before := snapshot(t, s, keys...)
	if r, marked, err := records.Outside(ctx, s, tenant, repoName, records.Push{After: "c1"}, "x"); err != nil || marked || r.OutsideFlow.Issue != 70 {
		t.Errorf("the same push again = %+v, %v, %v; want unmarked, issue 70", r, marked, err)
	}
	if !reflect.DeepEqual(before, snapshot(t, s, keys...)) {
		t.Error("the same push again changed a key")
	}

	r, marked, err = records.Outside(ctx, s, tenant, repoName, records.Push{After: "c2"}, "y")
	if err != nil || !marked || *r.OutsideFlow != (records.OutsideFlow{Digest: "y", Issue: 70}) {
		t.Errorf("a later push = %+v, %v, %v; want digest y and the issue kept", r, marked, err)
	}

	openProposal(t, s, "p1", 50, "d")
	if err := records.Consume(ctx, s, tenant, repoName, merge("p1", "c3", "d")); err != nil {
		t.Fatal(err)
	}
	if _, _, err := records.Outside(ctx, s, tenant, repoName, records.Push{After: "c3"}, "d"); !errors.Is(err, records.ErrMoved) {
		t.Errorf("Outside of a consumed push = %v, want ErrMoved", err)
	}

	fresh := store.NewMemory()
	if _, err := records.OutsideIssue(ctx, fresh, tenant, repoName, 70); !errors.Is(err, records.ErrMoved) {
		t.Errorf("OutsideIssue of a repository inside the flow = %v, want ErrMoved", err)
	}
}

func TestAConsumeWinsOnce(t *testing.T) {
	forEachBackend(t, 2, func(t *testing.T, clients []store.Store) {
		ctx := context.Background()
		openProposal(t, clients[0], "p1", 50, "d")
		errs := make([]error, 2)
		var wg sync.WaitGroup
		for i := range clients {
			wg.Go(func() {
				errs[i] = records.Consume(ctx, clients[i], tenant, repoName, merge("p1", "c"+strconv.Itoa(i), "d"))
			})
		}
		wg.Wait()
		won := -1
		for i, err := range errs {
			switch {
			case err == nil && won < 0:
				won = i
			case err == nil:
				t.Fatal("both commits consumed the proposal")
			case !errors.Is(err, records.ErrMoved):
				t.Fatalf("the losing consume = %v, want ErrMoved", err)
			}
		}
		if won < 0 {
			t.Fatal("no consume won")
		}
		if p := load[records.Proposal](t, clients[0], records.ProposalKey(repoName, "p1")); p.Commit != "c"+strconv.Itoa(won) {
			t.Errorf("consumed by %q, want the winner c%d", p.Commit, won)
		}
	})
}

// landsBefore is a Store whose first read of key first runs land on the store beneath: another writer whose swap
// lands between two reads of one Update.
type landsBefore struct {
	store.Store
	key  string
	once sync.Once
	land func()
}

func (l *landsBefore) Get(ctx context.Context, t store.Tenant, key string) ([]byte, error) {
	if key == l.key {
		l.once.Do(l.land)
	}
	return l.Store.Get(ctx, t, key)
}

func TestAConsumeThatLandsBetweenTwoReadsMakesTheOtherErrMoved(t *testing.T) {
	ctx := context.Background()
	s := store.NewMemory()
	openProposal(t, s, "p1", 50, "d")
	torn := &landsBefore{Store: s, key: records.RepoKey(repoName), land: func() {
		if err := records.Consume(ctx, s, tenant, repoName, merge("p1", "c0", "d")); err != nil {
			t.Errorf("the winning consume: %v", err)
		}
	}}

	if err := records.Consume(ctx, torn, tenant, repoName, merge("p1", "c1", "d")); !errors.Is(err, records.ErrMoved) {
		t.Errorf("the losing consume = %v, want ErrMoved", err)
	}
	if p := load[records.Proposal](t, s, records.ProposalKey(repoName, "p1")); p.Commit != "c0" {
		t.Errorf("consumed by %q, want c0", p.Commit)
	}
}

func TestTheSameCommitConsumesConcurrentlyWithoutError(t *testing.T) {
	forEachBackend(t, 2, func(t *testing.T, clients []store.Store) {
		ctx := context.Background()
		openProposal(t, clients[0], "p1", 50, "d")
		errs := make([]error, 2)
		var wg sync.WaitGroup
		for i := range clients {
			wg.Go(func() { errs[i] = records.Consume(ctx, clients[i], tenant, repoName, merge("p1", "c1", "d")) })
		}
		wg.Wait()
		for i, err := range errs {
			if err != nil {
				t.Errorf("caller %d = %v, want nil", i, err)
			}
		}
		if p := load[records.Proposal](t, clients[0], records.ProposalKey(repoName, "p1")); p.State != records.StateMerged || p.Commit != "c1" {
			t.Errorf("proposal %q by %q, want merged by c1", p.State, p.Commit)
		}
	})
}

// retryConflict is the caller pattern of store.Update: on ErrConflict, read again and try again.
func retryConflict(f func() error) error {
	for {
		if err := f(); !errors.Is(err, store.ErrConflict) {
			return err
		}
	}
}

func TestAnIndexStaysInStepUnderConcurrentWriters(t *testing.T) {
	const writers = 20
	forEachBackend(t, 4, func(t *testing.T, clients []store.Store) {
		ctx := context.Background()
		var wg sync.WaitGroup
		errs := make([]error, writers)
		for i := range writers {
			wg.Go(func() {
				s := clients[i%len(clients)]
				issue, key := 100+i, "graph m"+strconv.Itoa(i)+" n"
				if errs[i] = retryConflict(func() error { return openRequest(ctx, s, issue, key) }); errs[i] != nil {
					return
				}
				if i%2 == 0 {
					errs[i] = retryConflict(func() error { return answerRequest(ctx, s, issue) })
				}
			})
		}
		wg.Wait()
		for i, err := range errs {
			if err != nil {
				t.Fatalf("writer %d: %v", i, err)
			}
		}
		x := loadIndex(t, clients[0], records.RequestIndexKey(repoName), records.SchemaRequestIndex)
		var want []records.Entry
		for i := range writers {
			r := load[records.Request](t, clients[0], records.RequestKey(repoName, 100+i))
			id, inIndex := x.Lookup(r.Key)
			if open := r.State == records.StateOpen; open != inIndex || (inIndex && id != strconv.Itoa(100+i)) {
				t.Errorf("request %d is %s, index entry %q (present %v)", 100+i, r.State, id, inIndex)
			}
			if i%2 == 1 {
				want = append(want, records.Entry{Key: r.Key, ID: strconv.Itoa(100 + i)})
			}
		}
		if len(x.Entries) != len(want) {
			t.Errorf("index has %d entries, want %d", len(x.Entries), len(want))
		}
	})
}

func TestArchfit3KeysAreNeitherReadNorWritten(t *testing.T) {
	ctx := context.Background()
	mem := store.NewMemory()
	v3 := map[string]string{
		"entitlement":                              "e",
		"account":                                  "a",
		"consent/source_processing":                "c",
		"feedback/lease/abc":                       "l",
		"decision/acme/shop/pending":               `{"pending":[]}`,
		"decision/acme/shop/5/f1/sha256:aa":        "{}",
		"decision/acme/shop/lease/5":               "l",
		"decision/acme/shop/5/f1/sha256:aa/policy": "p",
		"admission":                                "{}",
		"pusher/acme/shop/abc":                     "u",
		"approval/acme/shop/5/abc":                 "{}",
		"owner-decision/acme/shop/5":               "{}",
		"review-request/acme/shop/5/abc/def":       "{}",
	}
	for k, v := range v3 {
		if err := mem.Put(ctx, tenant, k, []byte(v)); err != nil {
			t.Fatal(err)
		}
	}
	s := &recording{Store: mem}
	put(t, s, records.ApprovalKey(repoName, "a1"), approval(records.StateOpen))
	openProposal(t, s, "p1", 50, "d")
	steps := []error{
		openRequest(ctx, s, 431, "graph a b"),
		answerRequest(ctx, s, 431),
		records.Join(ctx, s, tenant, repoName, "a1", "p1", "h2", "d2"),
		records.Consume(ctx, s, tenant, repoName, merge("p1", "c1", "d2")),
		records.Consume(ctx, s, tenant, repoName, merge("p1", "c1", "d2")),
		func() error {
			_, _, err := records.Outside(ctx, s, tenant, repoName, records.Push{After: "c9"}, "d9")
			return err
		}(),
		func() error { _, err := records.OutsideIssue(ctx, s, tenant, repoName, 9); return err }(),
		records.Load(ctx, s, tenant, records.PullKey(repoName, 5), &records.Pull{}),
		openRequest(ctx, s, 432, "graph c d"),
		records.Answer(ctx, s, tenant, repoName, "a2", records.Approval{Kind: records.KindApprove, Request: 432},
			&records.Pin{Proposal: "p2", PullRequest: 51, HeadSHA: "h3", Digest: "d3"}),
		func() error { _, _, err := records.Void(ctx, s, tenant, repoName, "a2"); return err }(),
		records.VoidProposal(ctx, s, tenant, repoName, "p2"),
		records.RevertTo(ctx, s, tenant, repoName, "a3", ownerApproval(records.KindRevert),
			records.Revert{Commit: "c1", Digest: "d2", Outside: "d9", Issue: 9},
			records.Pin{Proposal: "p3", PullRequest: 52, HeadSHA: "h4", Digest: "d2"}),
		func() error {
			_, err := records.Confirm(ctx, s, tenant, repoName, "a4", ownerApproval(records.KindConfirm), "c10", "d9")
			return err
		}(),
		records.First(ctx, s, tenant, "Acme/Other", "f1", ownerApproval(records.KindFirst), bound("pf", "h1")),
		records.ApproveCurrent(ctx, s, tenant, "Acme/Third", "f2", ownerApproval(records.KindFirst), "c1", "dp"),
		records.Workflow(ctx, s, tenant, "Acme/Third", "w1", ownerApproval(records.KindWorkflow), bound("pw", "h1")),
		records.ConsumeWorkflow(ctx, s, tenant, "Acme/Third", "pw", "m1"),
	}
	for i, err := range steps {
		if err != nil && !errors.Is(err, records.ErrNoRecord) {
			t.Errorf("step %d: %v", i, err)
		}
	}
	if got := s.notV4(); len(got) != 0 {
		t.Errorf("keys outside v4/ were touched: %v", got)
	}
	for k, v := range v3 {
		if got, err := mem.Get(ctx, tenant, k); err != nil || string(got) != v {
			t.Errorf("%s = %q, %v; want %q unchanged", k, got, err, v)
		}
	}
}

// TestAConsumeApprovesOnlyWhatTheTipHolds: a consume handled after a later policy push uses its approvals but leaves
// the approved digest, so an older digest never comes back and a tip no approval made is never approved.
func TestAConsumeApprovesOnlyWhatTheTipHolds(t *testing.T) {
	ctx := context.Background()
	s := store.NewMemory()
	put(t, s, records.RepoKey(repoName), &records.Repo{ApprovedDigest: "newer"})
	openProposal(t, s, "p1", 50, "older")
	answeredIn(t, s, "p1", "a1", 431, "graph a b", "older")
	m := merge("p1", "c1", "older")
	m.AtTip = false
	if err := records.Consume(ctx, s, tenant, repoName, m); err != nil {
		t.Fatal(err)
	}
	if r := load[records.Repo](t, s, records.RepoKey(repoName)); r.ApprovedDigest != "newer" {
		t.Errorf("approved digest %q, want newer kept", r.ApprovedDigest)
	}
	if p := load[records.Proposal](t, s, records.ProposalKey(repoName, "p1")); p.State != records.StateMerged {
		t.Errorf("proposal %q, want merged: its approvals are used", p.State)
	}
	if x := loadIndex(t, s, records.RequestIndexKey(repoName), records.SchemaRequestIndex); len(x.Entries) != 0 {
		t.Errorf("request index %v, want the consumed request out", x.Entries)
	}
}

// TestAConsumeRecordsTheMergeCommitAndThePush: a merge queue lands the pull request's merge commit inside a push to a
// later commit.
func TestAConsumeRecordsTheMergeCommitAndThePush(t *testing.T) {
	ctx := context.Background()
	s := store.NewMemory()
	openProposal(t, s, "p1", 50, "d")
	push := records.Push{Before: "c0", After: "c3"}
	if err := records.Consume(ctx, s, tenant, repoName, records.Merge{Proposal: "p1", Commit: "c2", Digest: "d", Push: push, AtTip: true}); err != nil {
		t.Fatal(err)
	}
	if p := load[records.Proposal](t, s, records.ProposalKey(repoName, "p1")); p.Commit != "c2" {
		t.Errorf("proposal commit %q, want the merge commit c2", p.Commit)
	}
	if got := load[records.PolicyPush](t, s, records.PolicyPushKey(repoName, push)); got.Proposal != "p1" {
		t.Errorf("policy push = %+v", got)
	}
}

func TestUnguarded(t *testing.T) {
	ctx := context.Background()
	s := store.NewMemory()
	push := records.Push{Before: "c0", After: "c1"}
	if err := records.Unguarded(ctx, s, tenant, repoName, push, "d"); err != nil {
		t.Fatal(err)
	}
	if got := load[records.PolicyPush](t, s, records.PolicyPushKey(repoName, push)); got != (records.PolicyPush{
		Schema: records.SchemaPolicyPush, Digest: "d", Unguarded: true}) {
		t.Errorf("policy push = %+v", got)
	}
	before := snapshot(t, s, records.PolicyPushKey(repoName, push))
	if err := records.Unguarded(ctx, s, tenant, repoName, push, "other"); err != nil {
		t.Errorf("again = %v", err)
	}
	if !reflect.DeepEqual(before, snapshot(t, s, records.PolicyPushKey(repoName, push))) {
		t.Error("a second Unguarded changed the judgement")
	}
	if _, _, err := records.Outside(ctx, s, tenant, repoName, push, "d"); !errors.Is(err, records.ErrMoved) {
		t.Errorf("Outside of an unguarded push = %v, want ErrMoved", err)
	}
	openProposal(t, s, "p1", 50, "d")
	m := merge("p1", "c1", "d")
	m.Push = push
	if err := records.Consume(ctx, s, tenant, repoName, m); !errors.Is(err, records.ErrMoved) {
		t.Errorf("Consume of an unguarded push = %v, want ErrMoved", err)
	}
}

// TestAPushBackToAJudgedCommitIsAnotherPush: a push is named by both of its commits, so moving the branch back to a
// commit judged before is judged again.
func TestAPushBackToAJudgedCommitIsAnotherPush(t *testing.T) {
	ctx := context.Background()
	s := store.NewMemory()
	openProposal(t, s, "p1", 50, "d1")
	if err := records.Consume(ctx, s, tenant, repoName, merge("p1", "c1", "d1")); err != nil {
		t.Fatal(err)
	}
	r, marked, err := records.Outside(ctx, s, tenant, repoName, records.Push{Before: "c2", After: "c1"}, "d1")
	if err != nil || !marked || r.OutsideFlow == nil {
		t.Errorf("a push back to c1 = %+v, %v, %v; want judged outside the flow", r, marked, err)
	}
}

// TestAConsumeApprovesOverTheApprovedCommitItRead: an approval judged against an approved commit that moved meanwhile
// writes nothing; the first approval marks where change control started.
func TestAConsumeApprovesOverTheApprovedCommitItRead(t *testing.T) {
	ctx := context.Background()
	s := store.NewMemory()
	openProposal(t, s, "p1", 50, "d1")
	first := merge("p1", "c1", "d1")
	if err := records.Consume(ctx, s, tenant, repoName, first); err != nil {
		t.Fatal(err)
	}
	if r := load[records.Repo](t, s, records.RepoKey(repoName)); r.ApprovedCommit != "c1" || r.GuardedSince != "c1" {
		t.Errorf("repository %+v, want approved at c1 and guarded since c1", r)
	}

	openProposal(t, s, "p2", 51, "d2")
	stale := merge("p2", "c2", "d2") // judged when nothing was approved yet
	keys := []string{records.ProposalKey(repoName, "p2"), records.RepoKey(repoName), records.ProposalIndexKey(repoName)}
	before := snapshot(t, s, keys...)
	if err := records.Consume(ctx, s, tenant, repoName, stale); !errors.Is(err, records.ErrApprovedMoved) {
		t.Fatalf("Consume over a moved approved commit = %v, want ErrApprovedMoved", err)
	}
	if !reflect.DeepEqual(before, snapshot(t, s, keys...)) {
		t.Error("a refused consume changed a key")
	}
	stale.Over = "c1"
	if err := records.Consume(ctx, s, tenant, repoName, stale); err != nil {
		t.Fatal(err)
	}
	if r := load[records.Repo](t, s, records.RepoKey(repoName)); r.ApprovedDigest != "d2" || r.ApprovedCommit != "c2" ||
		r.GuardedSince != "c1" {
		t.Errorf("repository %+v, want d2 approved at c2, still guarded since c1", r)
	}
}

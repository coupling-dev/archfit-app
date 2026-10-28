package inputs

import (
	"context"
	"encoding/json"
	"errors"
	"maps"
	"testing"
	"time"

	"github.com/alexei-led/archfit-app/internal/github/api"
	"github.com/alexei-led/archfit-app/internal/store"
	"github.com/alexei-led/archfit-app/internal/wire/policy"
	"github.com/alexei-led/archfit-app/internal/wire/settings"
)

// setBaseSettings replaces the settings of the protected ref.
func (f *fixture) setBaseSettings(owners []string, agents ...string) {
	f.repos.files[fileCall{baseRef, settings.Path}] = settings.Render(settings.Settings{
		Version: 1, Owners: owners, Agents: agents, Gate: settings.GateRequired, Workflow: workflowPath,
	})
}

func (f *fixture) resolver() Resolver { return New(f.repos, f.pulls, f.store) }

// shown is what the App shows an owner for t: the Candidate digests.
func (f *fixture) shown(tb testing.TB, t Target) map[string]string {
	tb.Helper()
	c, err := f.resolver().Candidate(context.Background(), t)
	if err != nil {
		tb.Fatalf("Candidate: %v", err)
	}
	return c.Digests
}

func (f *fixture) approve(t Target, login string, seen map[string]string) error {
	return f.resolver().Approve(context.Background(), t, login, seen, now)
}

func (f *fixture) record(tb testing.TB, t Target) (approvalRecord, bool) {
	tb.Helper()
	raw, err := f.store.Get(context.Background(), store.Tenant(installation), approvalKey(t))
	if errors.Is(err, store.ErrNotFound) {
		return approvalRecord{}, false
	}
	if err != nil {
		tb.Fatal(err)
	}
	var rec approvalRecord
	if err := json.Unmarshal(raw, &rec); err != nil {
		tb.Fatal(err)
	}
	return rec, true
}

func TestCandidate(t *testing.T) {
	tests := []struct {
		name      string
		changed   []string
		truncated bool
		fork      bool
		edit      func(f *fixture)
		want      Candidate
	}{
		{name: "nothing protected changed", changed: []string{"main.go"}},
		{
			name:    "baseline and settings",
			changed: []string{settings.Path, "main.go", policy.BaselinePath},
			want: Candidate{
				Paths:   []string{policy.BaselinePath, settings.Path},
				Digests: map[string]string{policy.BaselinePath: policy.Digest(headBaseline), settings.Path: policy.Digest(headSettings)},
			},
		},
		{
			name:    "a deleted file is part of what is approved",
			changed: []string{policy.LabelsPath},
			edit:    func(f *fixture) { delete(f.repos.files, fileCall{headSHA, policy.LabelsPath}) },
			want:    Candidate{Paths: []string{policy.LabelsPath}, Digests: map[string]string{policy.LabelsPath: ""}},
		},
		{
			name:      "a truncated list counts every path",
			changed:   []string{"main.go"},
			truncated: true,
			want: Candidate{Paths: allProtected, Digests: map[string]string{
				policy.PolicyPath: policy.Digest(headPolicy), policy.BaselinePath: policy.Digest(headBaseline),
				policy.LabelsPath: policy.Digest(headLabels), settings.Path: policy.Digest(headSettings), workflowPath: "",
			}},
		},
		{name: "a fork reads no head file", changed: []string{policy.PolicyPath}, fork: true, want: Candidate{Paths: []string{policy.PolicyPath}}},
		{
			name:    "no settings on the protected ref",
			changed: []string{policy.PolicyPath},
			edit:    func(f *fixture) { delete(f.repos.files, fileCall{baseRef, settings.Path}) },
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			f := newFixture()
			f.pulls.changed, f.pulls.truncated = tt.changed, tt.truncated
			if tt.edit != nil {
				tt.edit(f)
			}
			target := pullTarget()
			target.Fork = tt.fork

			got, err := f.resolver().Candidate(context.Background(), target)
			if err != nil {
				t.Fatalf("Candidate: %v", err)
			}
			wantStrings(t, "Paths", got.Paths, tt.want.Paths)
			if !maps.Equal(got.Digests, tt.want.Digests) || (got.Digests == nil) != (tt.want.Digests == nil) {
				t.Errorf("Digests = %v, want %v", got.Digests, tt.want.Digests)
			}
			wantStrings(t, "head reads", f.repos.headReads(), tt.want.Paths[:len(tt.want.Digests)])
		})
	}
}

func TestAppApprovalGrantsTheExactHead(t *testing.T) {
	f := newFixture()
	f.pulls.changed = []string{policy.BaselinePath}
	if err := f.approve(pullTarget(), "alice", f.shown(t, pullTarget())); err != nil {
		t.Fatalf("Approve: %v", err)
	}

	got := f.mustResolve(t, pullTarget())
	wantApproval(t, got, ApprovalGranted, "alice")
	wantStrings(t, "BaselineDigests", got.BaselineDigests, digests(baseBaseline, headBaseline))
	wantStrings(t, "PolicyDigests", got.PolicyDigests, digests(basePolicy))
	rec, ok := f.record(t, pullTarget())
	if !ok || rec.Login != "alice" || rec.HeadSHA != headSHA || !rec.At.Equal(now) ||
		!maps.Equal(rec.Digests, map[string]string{policy.BaselinePath: policy.Digest(headBaseline)}) {
		t.Errorf("record = %+v, %t", rec, ok)
	}

	t.Run("a new commit needs a new approval", func(t *testing.T) {
		next := pullTarget()
		next.HeadSHA = "sha-head-3"
		wantApproval(t, f.mustResolve(t, next), ApprovalMissing, "")
	})
	t.Run("another pull request of the same head needs its own approval", func(t *testing.T) {
		other := pullTarget()
		other.PullRequest++
		wantApproval(t, f.mustResolve(t, other), ApprovalMissing, "")
		wantApproval(t, f.mustResolve(t, pullTarget()), ApprovalGranted, "alice")
	})
	t.Run("an owner team member approves too", func(t *testing.T) {
		f := newFixture()
		f.pulls.changed = []string{policy.BaselinePath}
		if err := f.approve(pullTarget(), "carol", f.shown(t, pullTarget())); err != nil {
			t.Fatalf("Approve: %v", err)
		}
		wantApproval(t, f.mustResolve(t, pullTarget()), ApprovalGranted, "carol")
	})
}

func TestAppApprovalRefusals(t *testing.T) {
	tests := []struct {
		name    string
		login   string
		changed []string
		fork    bool
		edit    func(f *fixture)
		seen    func(map[string]string) map[string]string
		want    error
	}{
		{name: "the head changed after the owner looked", login: "alice", seen: func(m map[string]string) map[string]string {
			m[policy.BaselinePath] = policy.Digest(baseBaseline)
			return m
		}, want: ErrDigestsChanged},
		{name: "a protected path the owner was not shown", login: "alice", seen: func(m map[string]string) map[string]string {
			delete(m, policy.BaselinePath)
			return m
		}, want: ErrDigestsChanged},
		{name: "a non-owner", login: "bob", want: ErrNotOwner},
		{name: "an owner of the head settings only", login: "mallory", want: ErrNotOwner},
		{name: "no login", login: "", want: ErrNotOwner},
		{name: "an owner listed as an agent", login: "alice", edit: func(f *fixture) {
			f.setBaseSettings([]string{"alice", teamOwner}, "Alice")
		}, want: ErrAgentIdentity},
		{name: "the app account of an owner listed as an agent", login: "alice[bot]", edit: func(f *fixture) {
			f.setBaseSettings([]string{"alice", teamOwner}, "alice")
		}, want: ErrAgentIdentity},
		{name: "an agent in an owner team", login: "carol", edit: func(f *fixture) {
			f.setBaseSettings([]string{"alice", teamOwner}, "carol")
		}, want: ErrAgentIdentity},
		{name: "a fork", login: "alice", fork: true, want: ErrForkPull},
		{name: "nothing protected changed", login: "alice", changed: []string{"main.go"}, want: ErrNothingToApprove},
		{name: "no settings on the protected ref", login: "alice", edit: func(f *fixture) {
			delete(f.repos.files, fileCall{baseRef, settings.Path})
		}, want: ErrNothingToApprove},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			f := newFixture()
			f.pulls.changed = []string{policy.BaselinePath, settings.Path}
			if tt.changed != nil {
				f.pulls.changed = tt.changed
			}
			if tt.edit != nil {
				tt.edit(f)
			}
			target := pullTarget()
			target.Fork = tt.fork
			seen := f.shown(t, target)
			if tt.seen != nil {
				seen = tt.seen(maps.Clone(seen))
			}

			if err := f.approve(target, tt.login, seen); !errors.Is(err, tt.want) {
				t.Fatalf("Approve = %v, want %v", err, tt.want)
			}
			if err := f.resolver().CanApprove(context.Background(), target, tt.login); tt.want != ErrDigestsChanged && !errors.Is(err, tt.want) {
				t.Errorf("CanApprove = %v, want %v", err, tt.want)
			}
			if _, ok := f.record(t, target); ok {
				t.Error("a refused approval was recorded")
			}
			wantApproval(t, f.mustResolve(t, target), map[bool]Approval{true: ApprovalNotRequired, false: ApprovalMissing}[tt.want == ErrNothingToApprove], "")
		})
	}
}

// An agent identity named among the owners, or held by an owner team, approves neither on GitHub nor in the App.
func TestAgentNeverApproves(t *testing.T) {
	tests := []struct {
		name   string
		owners []string
		agent  string
	}{
		{"listed as an owner and an agent", []string{"claude", "alice"}, "claude"},
		{"a member of an owner team", []string{"alice", teamOwner}, "carol"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			f := newFixture()
			f.setBaseSettings(tt.owners, tt.agent)
			f.pulls.changed = []string{policy.BaselinePath}
			f.pulls.reviews = []api.Review{approved(tt.agent, headSHA)}

			wantApproval(t, f.mustResolve(t, pullTarget()), ApprovalMissing, "")
			if err := f.approve(pullTarget(), tt.agent, f.shown(t, pullTarget())); !errors.Is(err, ErrAgentIdentity) {
				t.Errorf("Approve = %v, want ErrAgentIdentity", err)
			}
			wantApproval(t, f.mustResolve(t, pullTarget()), ApprovalMissing, "")
			wantStrings(t, "team lookups", f.pulls.teamCalls, nil)
		})
	}
}

func TestAppApprovalVoidedByTheProtectedRef(t *testing.T) {
	tests := []struct {
		name  string
		after func(f *fixture)
		by    string // an owner who may approve once after has run
	}{
		{"the approver left the owners", func(f *fixture) { f.setBaseSettings([]string{"bob"}) }, "bob"},
		{"the approver is now an agent", func(f *fixture) { f.setBaseSettings([]string{"alice", teamOwner}, "carol") }, "alice"},
		{"the approver left the owner team", func(f *fixture) { f.pulls.teams[teamOwner] = nil }, "alice"},
		{"the approver requested changes afterwards", func(f *fixture) {
			f.pulls.reviews = []api.Review{{Reviewer: "carol", State: "CHANGES_REQUESTED", CommitSHA: headSHA, SubmittedAt: now.Add(time.Minute)}}
		}, "alice"},
		{"the base moved: the pull request changes another protected path", func(f *fixture) {
			f.pulls.changed = []string{policy.BaselinePath, policy.PolicyPath}
		}, "alice"},
		{"the base moved: the pull request no longer changes a path the owner approved", func(f *fixture) {
			f.pulls.changed = []string{policy.BaselinePath}
		}, "alice"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			f := newFixture()
			f.pulls.changed = []string{policy.BaselinePath, policy.LabelsPath}
			if err := f.approve(pullTarget(), "carol", f.shown(t, pullTarget())); err != nil {
				t.Fatalf("Approve: %v", err)
			}
			wantApproval(t, f.mustResolve(t, pullTarget()), ApprovalGranted, "carol")

			tt.after(f)
			got := f.mustResolve(t, pullTarget())
			wantApproval(t, got, ApprovalMissing, "")
			wantStrings(t, "BaselineDigests", got.BaselineDigests, digests(baseBaseline))

			// The record that no longer counts must not shadow an owner who may approve now.
			if err := f.approve(pullTarget(), tt.by, f.shown(t, pullTarget())); err != nil {
				t.Fatalf("Approve by %s: %v", tt.by, err)
			}
			wantApproval(t, f.mustResolve(t, pullTarget()), ApprovalGranted, tt.by)
		})
	}
}

// TestAppApprovalWithdrawnByTheApproversReview: an owner who can review ends the approval as on GitHub, by requesting
// changes or by a review that was dismissed, after approving in the App.
func TestAppApprovalWithdrawnByTheApproversReview(t *testing.T) {
	review := func(login, state string, at time.Duration) api.Review {
		return api.Review{Reviewer: login, State: state, CommitSHA: headSHA, SubmittedAt: now.Add(at)}
	}
	tests := []struct {
		name    string
		reviews []api.Review
		want    Approval
	}{
		{"no review", nil, ApprovalGranted},
		{"changes requested later", []api.Review{review("alice", "CHANGES_REQUESTED", time.Minute)}, ApprovalMissing},
		{"changes requested later, login in other case", []api.Review{review("ALICE", "CHANGES_REQUESTED", time.Minute)}, ApprovalMissing},
		{"changes requested in the same second", []api.Review{review("alice", "CHANGES_REQUESTED", 0)}, ApprovalMissing},
		{"a review dismissed later", []api.Review{review("alice", "DISMISSED", time.Minute)}, ApprovalMissing},
		{"changes requested earlier", []api.Review{review("alice", "CHANGES_REQUESTED", -time.Minute)}, ApprovalGranted},
		{"changes requested by someone else", []api.Review{review("bob", "CHANGES_REQUESTED", time.Minute)}, ApprovalGranted},
		{"a comment later", []api.Review{review("alice", "COMMENTED", time.Minute)}, ApprovalGranted},
		{"no submission time", []api.Review{{Reviewer: "alice", State: "CHANGES_REQUESTED", CommitSHA: headSHA}}, ApprovalGranted},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			f := newFixture()
			f.pulls.changed = []string{policy.BaselinePath}
			if err := f.approve(pullTarget(), "alice", f.shown(t, pullTarget())); err != nil {
				t.Fatalf("Approve: %v", err)
			}
			f.pulls.reviews = tt.reviews
			by := ""
			if tt.want == ApprovalGranted {
				by = "alice"
			}
			wantApproval(t, f.mustResolve(t, pullTarget()), tt.want, by)
		})
	}
}

func TestAppApprovalIsCreateOnly(t *testing.T) {
	f := newFixture()
	f.pulls.changed = []string{policy.BaselinePath}
	seen := f.shown(t, pullTarget())
	if err := f.approve(pullTarget(), "alice", seen); err != nil {
		t.Fatalf("Approve: %v", err)
	}
	first, _ := f.record(t, pullTarget())

	for _, login := range []string{"alice", "carol"} {
		if err := f.resolver().Approve(context.Background(), pullTarget(), login, seen, now.Add(time.Hour)); err != nil {
			t.Errorf("a second Approve by %s = %v, want success", login, err)
		}
	}
	if got, _ := f.record(t, pullTarget()); got.Login != first.Login || !got.At.Equal(first.At) {
		t.Errorf("record = %+v, want the first one %+v", got, first)
	}
	wantApproval(t, f.mustResolve(t, pullTarget()), ApprovalGranted, "alice")
}

func TestAppApprovalRecordFailures(t *testing.T) {
	t.Run("a failed read of the record fails closed", func(t *testing.T) {
		f := newFixture()
		f.pulls.changed = []string{policy.BaselinePath}
		f.store = &stubStore{get: func(context.Context) ([]byte, error) { return nil, errBoom }}
		if _, err := f.resolve(pullTarget()); !errors.Is(err, errBoom) {
			t.Fatalf("Resolve error = %v, want the store error: a failed read must not decide the approval", err)
		}
	})
	t.Run("a record that does not decode approves nothing", func(t *testing.T) {
		f := newFixture()
		f.pulls.changed = []string{policy.BaselinePath}
		if err := f.store.Put(context.Background(), store.Tenant(installation), approvalKey(pullTarget()), []byte("{")); err != nil {
			t.Fatal(err)
		}
		wantApproval(t, f.mustResolve(t, pullTarget()), ApprovalMissing, "")
	})
	t.Run("a record of another installation approves nothing", func(t *testing.T) {
		f := newFixture()
		f.pulls.changed = []string{policy.BaselinePath}
		other := pullTarget()
		other.Repo.InstallationID = 202
		if err := f.approve(other, "alice", f.shown(t, other)); err != nil {
			t.Fatal(err)
		}
		wantApproval(t, f.mustResolve(t, pullTarget()), ApprovalMissing, "")
	})
	t.Run("a failed write is an error", func(t *testing.T) {
		f := newFixture()
		f.pulls.changed = []string{policy.BaselinePath}
		seen := f.shown(t, pullTarget())
		f.store = failingWrites{f.store}
		if err := f.approve(pullTarget(), "alice", seen); !errors.Is(err, errBoom) {
			t.Fatalf("Approve = %v, want the store error", err)
		}
	})
}

// failingWrites is a Store whose compare-and-swap fails.
type failingWrites struct{ store.Store }

func (failingWrites) CompareAndSwap(context.Context, store.Tenant, string, []byte, []byte) (bool, error) {
	return false, errBoom
}

// TestReplacingAVoidRecordNeverOverwritesALaterApproval: bob's approval lands between alice reading the void record
// and alice replacing it. alice's replacement is against the bytes she judged void, so it loses and bob's stays.
func TestReplacingAVoidRecordNeverOverwritesALaterApproval(t *testing.T) {
	f := newFixture()
	f.pulls.changed = []string{policy.BaselinePath}
	f.setBaseSettings([]string{"alice", "bob"})
	seen := f.shown(t, pullTarget())
	void, err := json.Marshal(approvalRecord{Login: "gone", HeadSHA: headSHA, Digests: seen, At: now})
	if err != nil {
		t.Fatal(err)
	}
	tenant, key := store.Tenant(installation), approvalKey(pullTarget())
	if err := f.store.Put(context.Background(), tenant, key, void); err != nil {
		t.Fatal(err)
	}
	bob, err := json.Marshal(approvalRecord{Login: "bob", HeadSHA: headSHA, Digests: seen, At: now})
	if err != nil {
		t.Fatal(err)
	}
	f.store = &approvesBetween{Store: f.store, key: key, bob: bob}

	if err := f.approve(pullTarget(), "alice", seen); err != nil {
		t.Fatalf("Approve: %v", err)
	}
	if rec, _ := f.record(t, pullTarget()); rec.Login != "bob" {
		t.Errorf("record = %+v, want bob's, which landed after alice read the void record", rec)
	}
}

// approvesBetween makes bob approve right after the first read of key, as a concurrent request would.
type approvesBetween struct {
	store.Store
	key  string
	bob  []byte
	done bool
}

func (s *approvesBetween) Get(ctx context.Context, tn store.Tenant, key string) ([]byte, error) {
	raw, err := s.Store.Get(ctx, tn, key)
	if key == s.key && err == nil && !s.done {
		s.done = true
		_ = s.Put(ctx, tn, key, s.bob)
	}
	return raw, err
}

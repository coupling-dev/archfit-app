package inputs

import (
	"context"
	"encoding/json"
	"errors"
	"reflect"
	"slices"
	"strings"
	"testing"

	"github.com/alexei-led/archfit-app/internal/github/api"
	"github.com/alexei-led/archfit-app/internal/store"
	"github.com/alexei-led/archfit-app/internal/wire/policy"
	"github.com/alexei-led/archfit-app/internal/wire/settings"
)

const (
	tipSHA   = "sha-main-tip"
	groupSHA = "sha-group-2"
	pr1Head  = "sha-pr1-head"
	pr2Head  = "sha-pr2-head"
	repoFull = "acme/shop"
)

var (
	groupSettings = []byte("version: 1\nowners:\n  - alice\n  - " + teamOwner + "\nagents:\n  - bot-agent\ngate: required\nworkflow: " + workflowPath + "\n")
	policyA       = []byte("version: 1\nrules: []\n# change A\n")
	policyAB      = []byte("version: 1\nrules: []\n# change A\n# change B\n")
)

// groupWorld is GitHub as a merge group of two queued pull requests sees it: #1 ahead of #2, and the group commit is
// #2's entry. Files are keyed by commit SHA only: the resolver must never read a branch name.
type groupWorld struct {
	files   map[fileCall][]byte
	reads   []fileCall
	entries []api.QueueEntry
	pulls   map[int]api.Pull
	changed map[int][]string
	reviews map[int][]api.Review
	teams   map[string][]string
	store   store.Store

	queueErr, headErr error
}

func newGroupWorld() *groupWorld {
	w := &groupWorld{
		files: map[fileCall][]byte{},
		entries: []api.QueueEntry{
			{Position: 1, HeadSHA: "sha-group-1", BaseSHA: tipSHA, PullRequest: 1, PullHeadSHA: pr1Head},
			{Position: 2, HeadSHA: groupSHA, BaseSHA: tipSHA, PullRequest: 2, PullHeadSHA: pr2Head},
			{Position: 3, HeadSHA: "sha-group-3", BaseSHA: tipSHA, PullRequest: 3, PullHeadSHA: "sha-pr3-head"},
		},
		pulls: map[int]api.Pull{
			1: {Number: 1, State: "open", HeadSHA: pr1Head, HeadRepo: repoFull, BaseRef: baseRef},
			2: {Number: 2, State: "open", HeadSHA: pr2Head, HeadRepo: repoFull, BaseRef: baseRef},
			3: {Number: 3, State: "open", HeadSHA: "sha-pr3-head", HeadRepo: repoFull, BaseRef: baseRef},
		},
		changed: map[int][]string{1: {"a.go"}, 2: {"b.go"}, 3: {policy.PolicyPath}},
		reviews: map[int][]api.Review{},
		teams:   map[string][]string{teamOwner: {"carol"}},
		store:   store.NewMemory(),
	}
	for _, sha := range []string{tipSHA, groupSHA, pr1Head, pr2Head} {
		w.files[fileCall{sha, settings.Path}] = groupSettings
		w.files[fileCall{sha, policy.PolicyPath}] = basePolicy
		w.files[fileCall{sha, policy.BaselinePath}] = baseBaseline
	}
	return w
}

// changePolicy makes pull request n change the policy to raw at its head.
func (w *groupWorld) changePolicy(n int, head string, raw []byte) {
	w.changed[n] = append(w.changed[n], policy.PolicyPath)
	w.files[fileCall{head, policy.PolicyPath}] = raw
}

func (w *groupWorld) approve(n int, login, head string) {
	w.reviews[n] = append(w.reviews[n], api.Review{Reviewer: login, State: reviewApproved, CommitSHA: head})
}

func (w *groupWorld) resolve(t *testing.T) Trusted {
	t.Helper()
	got, err := w.try()
	if err != nil {
		t.Fatalf("Resolve: %v", err)
	}
	return got
}

func (w *groupWorld) try() (Trusted, error) {
	r := New(groupRepos{w}, groupPulls{w}, groupQueues{w}, w.store)
	return r.Resolve(context.Background(), groupTarget(), now)
}

func groupTarget() Target {
	return Target{Repo: api.Repo{RepoRef: api.RepoRef{InstallationID: installation, FullName: repoFull}, DefaultBranch: baseRef},
		HeadSHA: groupSHA, BaseRef: baseRef, MergeGroup: true}
}

type groupRepos struct {
	*groupWorld
}

func (w groupRepos) ListInstallations(context.Context) ([]api.Installation, error) { panic("unused") }
func (w groupRepos) ListRepos(context.Context, int64) ([]api.Repo, error)          { panic("unused") }
func (w groupRepos) ResolveRepo(context.Context, string) (api.Repo, error)         { panic("unused") }

func (w groupRepos) File(_ context.Context, _ api.RepoRef, ref, path string) ([]byte, string, error) {
	c := fileCall{ref, path}
	w.reads = append(w.reads, c)
	raw, ok := w.files[c]
	if !ok {
		return nil, "", api.ErrNotFound
	}
	return raw, "blob", nil
}

func (w groupRepos) BranchHead(_ context.Context, _ api.RepoRef, branch string) (string, error) {
	if branch != baseRef {
		return "", api.ErrNotFound
	}
	return tipSHA, w.headErr
}

type groupPulls struct {
	*groupWorld
}

func (w groupPulls) Pull(_ context.Context, _ api.RepoRef, n int) (api.Pull, error) {
	p, ok := w.pulls[n]
	if !ok {
		return api.Pull{}, api.ErrNotFound
	}
	return p, nil
}
func (w groupPulls) OpenPulls(context.Context, api.RepoRef) ([]api.Pull, error) { panic("unused") }
func (w groupPulls) PullByHead(context.Context, api.RepoRef, string) (api.Pull, error) {
	panic("unused")
}

func (w groupPulls) ChangedFiles(_ context.Context, _ api.RepoRef, n int) ([]string, bool, error) {
	return w.changed[n], false, nil
}

func (w groupPulls) Reviews(_ context.Context, _ api.RepoRef, n int) ([]api.Review, error) {
	return w.reviews[n], nil
}

func (w groupPulls) TeamMember(_ context.Context, _ api.RepoRef, team, login string) (bool, error) {
	return slices.Contains(w.teams[team], login), nil
}

type groupQueues struct {
	*groupWorld
}

func (w groupQueues) MergeQueue(_ context.Context, _ api.RepoRef, branch string) ([]api.QueueEntry, error) {
	if branch != baseRef {
		return nil, nil
	}
	return w.entries, w.queueErr
}

func TestMergeGroupInputs(t *testing.T) {
	digest := policy.Digest
	tests := []struct {
		name         string
		setup        func(w *groupWorld)
		approval     Approval
		problem      GroupProblem
		policy       []string
		protected    []string
		unapproved   string
		approvedBy   string
		detailSubstr string
	}{
		{"code-only group uses the snapshot's inputs", func(*groupWorld) {},
			ApprovalNotRequired, "", []string{digest(basePolicy)}, nil, "", "", ""},
		{"an approved policy change is the exact combined blob", func(w *groupWorld) {
			w.changePolicy(1, pr1Head, policyA)
			w.approve(1, "alice", pr1Head)
			w.files[fileCall{groupSHA, policy.PolicyPath}] = policyA
		}, ApprovalGranted, "", []string{digest(policyA)}, []string{policy.PolicyPath}, "", "alice (#1)", ""},
		{"an owner team member approves", func(w *groupWorld) {
			w.changePolicy(2, pr2Head, policyA)
			w.approve(2, "carol", pr2Head)
			w.files[fileCall{groupSHA, policy.PolicyPath}] = policyA
		}, ApprovalGranted, "", []string{digest(policyA)}, []string{policy.PolicyPath}, "", "carol (#2)", ""},
		{"an unapproved member change is refused", func(w *groupWorld) {
			w.changePolicy(1, pr1Head, policyA)
			w.files[fileCall{groupSHA, policy.PolicyPath}] = policyA
		}, ApprovalMissing, "", []string{digest(basePolicy)}, []string{policy.PolicyPath}, "#1 .archfit.yaml", "", ""},
		{"an unapproved change the group reverts is still refused", func(w *groupWorld) {
			w.changePolicy(1, pr1Head, policyA)
		}, ApprovalMissing, "", []string{digest(basePolicy)}, []string{policy.PolicyPath}, "#1 .archfit.yaml", "", ""},
		{"an agent's approval approves nothing", func(w *groupWorld) {
			w.changePolicy(1, pr1Head, policyA)
			w.approve(1, "bot-agent", pr1Head)
			w.files[fileCall{groupSHA, policy.PolicyPath}] = policyA
		}, ApprovalMissing, "", []string{digest(basePolicy)}, []string{policy.PolicyPath}, "#1 .archfit.yaml", "", ""},
		{"an approval of an older head approves nothing", func(w *groupWorld) {
			w.changePolicy(1, pr1Head, policyA)
			w.approve(1, "alice", oldSHA)
			w.files[fileCall{groupSHA, policy.PolicyPath}] = policyA
		}, ApprovalMissing, "", []string{digest(basePolicy)}, []string{policy.PolicyPath}, "#1 .archfit.yaml", "", ""},
		{"a withdrawn approval approves nothing", func(w *groupWorld) {
			w.changePolicy(1, pr1Head, policyA)
			w.approve(1, "alice", pr1Head)
			w.reviews[1] = append(w.reviews[1], api.Review{Reviewer: "alice", State: reviewChangesRequested, CommitSHA: pr1Head})
			w.files[fileCall{groupSHA, policy.PolicyPath}] = policyA
		}, ApprovalMissing, "", []string{digest(basePolicy)}, []string{policy.PolicyPath}, "#1 .archfit.yaml", "", ""},
		{"two approved members change one protected file", func(w *groupWorld) {
			w.changePolicy(1, pr1Head, policyA)
			w.changePolicy(2, pr2Head, policyAB)
			w.approve(1, "alice", pr1Head)
			w.approve(2, "carol", pr2Head)
			w.files[fileCall{groupSHA, policy.PolicyPath}] = policyAB
		}, ApprovalMissing, GroupConflict, []string{digest(basePolicy)}, []string{policy.PolicyPath}, "", "", "changed by #1 and #2"},
		{"the combined file is not the approved head's", func(w *groupWorld) {
			w.changePolicy(1, pr1Head, policyA)
			w.approve(1, "alice", pr1Head)
			w.files[fileCall{groupSHA, policy.PolicyPath}] = policyAB
		}, ApprovalMissing, GroupConflict, []string{digest(basePolicy)}, []string{policy.PolicyPath}, "", "", "not the file an owner approved on #1"},
		{"a change no member makes", func(w *groupWorld) {
			w.files[fileCall{groupSHA, policy.LabelsPath}] = headLabels
		}, ApprovalMissing, GroupConflict, []string{digest(basePolicy)}, []string{policy.LabelsPath}, "", "", "changed by no queued pull request"},
		{"a member deletes the baseline with approval", func(w *groupWorld) {
			w.changed[1] = append(w.changed[1], policy.BaselinePath)
			delete(w.files, fileCall{pr1Head, policy.BaselinePath})
			delete(w.files, fileCall{groupSHA, policy.BaselinePath})
			w.approve(1, "alice", pr1Head)
		}, ApprovalGranted, "", []string{digest(basePolicy)}, []string{policy.BaselinePath}, "", "alice (#1)", ""},
		{"a member behind the group does not count", func(w *groupWorld) {
			// #3 changes the policy without approval, but it is behind #2: the group commit does not hold it.
		}, ApprovalNotRequired, "", []string{digest(basePolicy)}, nil, "", "", ""},
		{"a fork member", func(w *groupWorld) { w.entries[0].CrossRepo = true },
			ApprovalNotRequired, GroupFork, []string{digest(basePolicy)}, nil, "", "", "#1"},
		{"a fork member GraphQL does not flag", func(w *groupWorld) {
			p := w.pulls[1]
			p.HeadRepo = "mallory/shop"
			w.pulls[1] = p
		}, ApprovalNotRequired, GroupFork, []string{digest(basePolicy)}, nil, "", "", "#1"},
		{"a member whose head moved", func(w *groupWorld) {
			p := w.pulls[1]
			p.HeadSHA = "sha-pr1-pushed"
			w.pulls[1] = p
		}, ApprovalNotRequired, GroupUnverified, []string{digest(basePolicy)}, nil, "", "", "#1"},
		{"a member into another base", func(w *groupWorld) {
			p := w.pulls[2]
			p.BaseRef = "release"
			w.pulls[2] = p
		}, ApprovalNotRequired, GroupUnverified, []string{digest(basePolicy)}, nil, "", "", "#2"},
		{"a closed member", func(w *groupWorld) {
			p := w.pulls[1]
			p.State = "closed"
			w.pulls[1] = p
		}, ApprovalNotRequired, GroupUnverified, []string{digest(basePolicy)}, nil, "", "", "#1"},
		{"the group commit is no longer queued", func(w *groupWorld) { w.entries = w.entries[:1] },
			ApprovalNotRequired, GroupUnverified, []string{digest(basePolicy)}, nil, "", "", "no entry"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			w := newGroupWorld()
			tt.setup(w)
			got := w.resolve(t)
			if got.Group == nil {
				t.Fatal("Group is nil for a merge-group target")
			}
			if got.Approval != tt.approval || got.Group.Problem != tt.problem {
				t.Errorf("approval %q problem %q (%s); want %q %q", got.Approval, got.Group.Problem, got.Group.Detail, tt.approval, tt.problem)
			}
			if !reflect.DeepEqual(got.PolicyDigests, tt.policy) {
				t.Errorf("policy digests %v, want %v", got.PolicyDigests, tt.policy)
			}
			if !reflect.DeepEqual(got.ProtectedPaths, tt.protected) {
				t.Errorf("protected paths %v, want %v", got.ProtectedPaths, tt.protected)
			}
			if got.Group.Unapproved != tt.unapproved || got.ApprovedBy != tt.approvedBy {
				t.Errorf("unapproved %q approved by %q; want %q %q", got.Group.Unapproved, got.ApprovedBy, tt.unapproved, tt.approvedBy)
			}
			if !strings.Contains(got.Group.Detail, tt.detailSubstr) {
				t.Errorf("detail %q does not name %q", got.Group.Detail, tt.detailSubstr)
			}
			if !got.SettingsFound || got.Settings.Gate != settings.GateRequired {
				t.Errorf("settings not read from the snapshot: %+v", got.Settings)
			}
			for _, c := range w.reads {
				if c.ref == baseRef {
					t.Fatalf("read %s by the branch name, not a commit", c.path)
				}
			}
		})
	}
}

func TestMergeGroupWaiversComeFromTheAcceptedPolicy(t *testing.T) {
	w := newGroupWorld()
	w.changePolicy(1, pr1Head, headPolicy)
	w.approve(1, "alice", pr1Head)
	w.files[fileCall{groupSHA, policy.PolicyPath}] = headPolicy
	got := w.resolve(t)
	if got.Approval != ApprovalGranted || got.EarliestWaiverExpiry.IsZero() {
		t.Fatalf("approval %q expiry %v; want the approved policy's waiver", got.Approval, got.EarliestWaiverExpiry)
	}
}

func TestMergeGroupSoleOwnerApprovesInTheApp(t *testing.T) {
	w := newGroupWorld()
	w.changePolicy(1, pr1Head, policyA)
	w.files[fileCall{groupSHA, policy.PolicyPath}] = policyA
	rec, err := json.Marshal(approvalRecord{Login: "alice", HeadSHA: pr1Head,
		Digests: map[string]string{policy.PolicyPath: policy.Digest(policyA)}, At: now})
	if err != nil {
		t.Fatal(err)
	}
	pr1 := Target{Repo: groupTarget().Repo, PullRequest: 1, HeadSHA: pr1Head}
	if _, err := w.store.CompareAndSwap(context.Background(), store.Tenant(installation), approvalKey(pr1), nil, rec); err != nil {
		t.Fatal(err)
	}
	got := w.resolve(t)
	if got.Approval != ApprovalGranted || got.ApprovedBy != "alice (#1)" {
		t.Fatalf("approval %q by %q; want the App-recorded approval of #1's own head", got.Approval, got.ApprovedBy)
	}
	// A record of the group commit is not an approval: only a member's own head is.
	w2 := newGroupWorld()
	w2.changePolicy(1, pr1Head, policyA)
	w2.files[fileCall{groupSHA, policy.PolicyPath}] = policyA
	groupRec := Target{Repo: groupTarget().Repo, PullRequest: 1, HeadSHA: groupSHA}
	rec2, _ := json.Marshal(approvalRecord{Login: "alice", HeadSHA: groupSHA,
		Digests: map[string]string{policy.PolicyPath: policy.Digest(policyA)}, At: now})
	if _, err := w2.store.CompareAndSwap(context.Background(), store.Tenant(installation), approvalKey(groupRec), nil, rec2); err != nil {
		t.Fatal(err)
	}
	if got := w2.resolve(t); got.Approval != ApprovalMissing {
		t.Fatalf("approval %q; a record of the group commit must approve nothing", got.Approval)
	}
}

func TestMergeGroupReadFailuresAreErrors(t *testing.T) {
	boom := errors.New("boom")
	for name, set := range map[string]func(*groupWorld){
		"queue unreadable":       func(w *groupWorld) { w.queueErr = boom },
		"default branch unknown": func(w *groupWorld) { w.headErr = boom },
	} {
		t.Run(name, func(t *testing.T) {
			w := newGroupWorld()
			set(w)
			if got, err := w.try(); err == nil {
				t.Fatalf("got %+v; want an error, never inputs", got)
			}
		})
	}
}

func TestMergeGroupWithoutSettingsIsNotOnboarded(t *testing.T) {
	w := newGroupWorld()
	delete(w.files, fileCall{tipSHA, settings.Path})
	if got := w.resolve(t); got.SettingsFound {
		t.Fatalf("settings found without a settings file at the snapshot: %+v", got)
	}
}

package inputs

import (
	"context"
	"errors"
	"reflect"
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
	store   store.Store

	queueErr, headErr, pullErr, changedErr error
	fileErr                                map[fileCall]error
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
	if err := w.fileErr[c]; err != nil {
		return nil, "", err
	}
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
	if w.pullErr != nil {
		return api.Pull{}, w.pullErr
	}
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
	if w.changedErr != nil {
		return nil, false, w.changedErr
	}
	return w.changed[n], false, nil
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

// Nothing approves a protected change in a merge group (M2 task 26): every member that changes a protected path is
// named, also when an owner approved its head, and the group keeps the snapshot's inputs.
func TestMergeGroupInputs(t *testing.T) {
	digest := policy.Digest
	base := []string{digest(basePolicy)}
	tests := []struct {
		name         string
		setup        func(w *groupWorld)
		problem      GroupProblem
		protected    []string
		unapproved   string
		detailSubstr string
	}{
		{"code-only group uses the snapshot's inputs", func(*groupWorld) {}, "", nil, "", ""},
		{"a member's policy change is refused", func(w *groupWorld) {
			w.changePolicy(1, pr1Head, policyA)
			w.files[fileCall{groupSHA, policy.PolicyPath}] = policyA
		}, "", []string{policy.PolicyPath}, "#1 .archfit.yaml", ""},
		{"a member change is refused", func(w *groupWorld) {
			w.changePolicy(2, pr2Head, policyA)
			w.files[fileCall{groupSHA, policy.PolicyPath}] = policyA
		}, "", []string{policy.PolicyPath}, "#2 .archfit.yaml", ""},
		{"a member change the group reverts is still refused", func(w *groupWorld) {
			w.changePolicy(1, pr1Head, policyA)
		}, "", []string{policy.PolicyPath}, "#1 .archfit.yaml", ""},
		{"two members change one protected file", func(w *groupWorld) {
			w.changePolicy(1, pr1Head, policyA)
			w.changePolicy(2, pr2Head, policyAB)
			w.files[fileCall{groupSHA, policy.PolicyPath}] = policyAB
		}, "", []string{policy.PolicyPath}, "#1 .archfit.yaml; #2 .archfit.yaml", ""},
		{"a change no member makes", func(w *groupWorld) {
			w.files[fileCall{groupSHA, policy.LabelsPath}] = headLabels
		}, GroupConflict, []string{policy.LabelsPath}, "", "changed by no queued pull request"},
		{"a member deletes the baseline", func(w *groupWorld) {
			w.changed[1] = append(w.changed[1], policy.BaselinePath)
			delete(w.files, fileCall{pr1Head, policy.BaselinePath})
			delete(w.files, fileCall{groupSHA, policy.BaselinePath})
		}, "", []string{policy.BaselinePath}, "#1 " + policy.BaselinePath, ""},
		{"a member behind the group does not count", func(w *groupWorld) {
			// #3 changes the policy, but it is behind #2: the group commit does not hold it.
		}, "", nil, "", ""},
		{"a fork member", func(w *groupWorld) { w.entries[0].CrossRepo = true }, GroupFork, nil, "", "#1"},
		{"a fork member GraphQL does not flag", func(w *groupWorld) {
			p := w.pulls[1]
			p.HeadRepo = "mallory/shop"
			w.pulls[1] = p
		}, GroupFork, nil, "", "#1"},
		{"a member whose head moved", func(w *groupWorld) {
			p := w.pulls[1]
			p.HeadSHA = "sha-pr1-pushed"
			w.pulls[1] = p
		}, GroupUnverified, nil, "", "#1"},
		{"a member into another base", func(w *groupWorld) {
			p := w.pulls[2]
			p.BaseRef = "release"
			w.pulls[2] = p
		}, GroupUnverified, nil, "", "#2"},
		{"a member that merged since the queue read still counts", func(w *groupWorld) {
			p := w.pulls[1]
			p.State, p.Merged = "closed", true
			w.pulls[1] = p
			w.changePolicy(1, pr1Head, policyA)
			w.files[fileCall{groupSHA, policy.PolicyPath}] = policyA
		}, "", []string{policy.PolicyPath}, "#1 .archfit.yaml", ""},
		{"a closed member", func(w *groupWorld) {
			p := w.pulls[1]
			p.State = "closed"
			w.pulls[1] = p
		}, GroupUnverified, nil, "", "#1"},
		{"the group commit is no longer queued", func(w *groupWorld) { w.entries = w.entries[:1] },
			GroupUnverified, nil, "", "no entry"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			w := newGroupWorld()
			tt.setup(w)
			got := w.resolve(t)
			if got.Group == nil {
				t.Fatal("Group is nil for a merge-group target")
			}
			if got.Group.Problem != tt.problem {
				t.Errorf("problem %q (%s); want %q", got.Group.Problem, got.Group.Detail, tt.problem)
			}
			if !reflect.DeepEqual(got.PolicyDigests, base) {
				t.Errorf("policy digests %v, want the snapshot's %v", got.PolicyDigests, base)
			}
			if !reflect.DeepEqual(got.ProtectedPaths, tt.protected) {
				t.Errorf("protected paths %v, want %v", got.ProtectedPaths, tt.protected)
			}
			if got.Group.Unapproved != tt.unapproved {
				t.Errorf("unapproved %q; want %q", got.Group.Unapproved, tt.unapproved)
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
				if c.ref == pr1Head || c.ref == pr2Head {
					t.Errorf("read %s at a member head: nothing of a member's head is trusted", c.path)
				}
			}
		})
	}
}

func TestMergeGroupReadFailuresAreErrors(t *testing.T) {
	boom := errors.New("boom")
	for name, set := range map[string]func(*groupWorld){
		"queue unreadable":                    func(w *groupWorld) { w.queueErr = boom },
		"default branch unknown":              func(w *groupWorld) { w.headErr = boom },
		"pull request unreadable":             func(w *groupWorld) { w.pullErr = boom },
		"pull request rate limited":           func(w *groupWorld) { w.pullErr = api.ErrRateLimited },
		"settings unreadable at the snapshot": func(w *groupWorld) { w.fileErr = map[fileCall]error{{tipSHA, settings.Path}: boom} },
		"policy unreadable at the group":      func(w *groupWorld) { w.fileErr = map[fileCall]error{{groupSHA, policy.PolicyPath}: boom} },
		"member changes unreadable":           func(w *groupWorld) { w.changedErr = boom },
	} {
		t.Run(name, func(t *testing.T) {
			w := newGroupWorld()
			set(w)
			got, err := w.try()
			if err == nil {
				t.Fatalf("got %+v; want an error, never inputs", got)
			}
			if !reflect.DeepEqual(got, Trusted{}) {
				t.Fatalf("got %+v with the error; want the zero Trusted", got)
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

package onboarding

import (
	"context"
	"errors"
	"fmt"
	"reflect"
	"slices"
	"strings"
	"sync"
	"testing"

	"github.com/coupling-dev/archfit-app/internal/gate"
	"github.com/coupling-dev/archfit-app/internal/github/api"
	"github.com/coupling-dev/archfit-app/internal/wire/manifest"
	"github.com/coupling-dev/archfit-app/internal/wire/policy3"
)

const setupPolicy = "version: 1\nowners: [alice]\nmodules:\n  app:\n    paths: [cmd/**]\n"
const setupCommit = "0123456789abcdef0123456789abcdef01234567"

var setupRepo = api.Repo{RepoRef: api.RepoRef{InstallationID: 7, FullName: "acme/app"}, DefaultBranch: "main", OwnerType: "Organization", Visibility: "private"}

// setupHub exposes only reads: setup cannot write repository files or approval records.
type setupHub struct {
	mu        sync.Mutex
	files     map[string]map[string]string
	pulls     []api.Pull
	tags      map[string]string
	tagErr    error
	tagReads  []string
	fileReads []string
	branch    []api.RepositoryRule
	sets      []api.Ruleset
	ruleErr   error
}

func newSetupHub() *setupHub {
	return &setupHub{files: map[string]map[string]string{}, tags: map[string]string{}}
}

func (h *setupHub) File(_ context.Context, _ api.RepoRef, ref, path string) ([]byte, string, error) {
	h.mu.Lock()
	defer h.mu.Unlock()
	h.fileReads = append(h.fileReads, ref+":"+path)
	raw, ok := h.files[ref][path]
	if !ok {
		return nil, "", api.ErrNotFound
	}
	return []byte(raw), "blob", nil
}
func (h *setupHub) BranchHead(context.Context, api.RepoRef, string) (string, error) {
	return "tip", nil
}
func (h *setupHub) OpenPulls(context.Context, api.RepoRef) ([]api.Pull, error) { return h.pulls, nil }
func (h *setupHub) PublicTagCommit(_ context.Context, repo, tag string) (string, error) {
	h.mu.Lock()
	defer h.mu.Unlock()
	h.tagReads = append(h.tagReads, repo+"@"+tag)
	if h.tagErr != nil {
		return "", h.tagErr
	}
	if sha, ok := h.tags[tag]; ok {
		return sha, nil
	}
	return "", api.ErrNotFound
}
func (h *setupHub) BranchPolicy(context.Context, api.RepoRef, string) ([]api.RepositoryRule, error) {
	return h.branch, h.ruleErr
}
func (h *setupHub) AppIntegrationID(context.Context) (int64, error) { return 42, nil }
func (h *setupHub) ListRulesets(context.Context, api.RepoRef) ([]api.Ruleset, error) {
	return h.sets, nil
}
func (h *setupHub) GetRuleset(_ context.Context, _ api.RepoRef, id int64) (api.Ruleset, error) {
	for _, rs := range h.sets {
		if rs.ID == id {
			return rs, nil
		}
	}
	return api.Ruleset{}, api.ErrNotFound
}

type setupView struct{ state gate.SetupState }

func (v setupView) SetupState(context.Context, api.Repo) (gate.SetupState, error) {
	return v.state, nil
}

func (v setupView) SetupApproval(_ context.Context, _ api.Repo, a gate.OwnerAction) gate.ApprovalSummary {
	return gate.ApprovalSummary{Status: gate.ApprovalNeeded, Kind: a.Kind, Pull: a.Pull}
}

func setupWorkflow(refs ...string) string {
	raw := "name: Archfit\non: pull_request\njobs:\n  check:\n    runs-on: ubuntu-latest\n    steps:\n"
	for _, ref := range refs {
		raw += "      - uses: " + manifest.ActionRepository + "@" + ref + "\n"
	}
	return raw
}

func TestSetupAdmissionFromImmutableDefaultBranchFiles(t *testing.T) {
	tests := []struct {
		name, policy, workflow string
		tags                   map[string]string
		admitted               bool
		missing                int
	}{
		{"no files", "", "", nil, false, 2},
		{"old policy", "version: 2\n", setupWorkflow(setupCommit), nil, false, 1},
		{"no workflow", setupPolicy, "", nil, false, 1},
		{"unlisted action", setupPolicy, setupWorkflow(strings.Repeat("b", 40)), nil, false, 1},
		{"branch not tag", setupPolicy, setupWorkflow("main"), nil, false, 1},
		{"unlisted tag target", setupPolicy, setupWorkflow("v4"), map[string]string{"v4": strings.Repeat("c", 40)}, false, 1},
		{"listed commit", setupPolicy, setupWorkflow(setupCommit), nil, true, 0},
		{"tag to listed commit", setupPolicy, setupWorkflow("v4"), map[string]string{"v4": setupCommit}, true, 0},
		{"every action must be approved", setupPolicy, setupWorkflow(setupCommit, "bad"), nil, false, 1},
		{"shell text is not a call", setupPolicy, "jobs:\n  check:\n    steps:\n      - run: echo uses: " + manifest.ActionRepository + "@" + setupCommit + "\n", nil, false, 1},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			h := newSetupHub()
			h.files["tip"] = map[string]string{}
			if tt.policy != "" {
				h.files["tip"][".archfit.yaml"] = tt.policy
			}
			if tt.workflow != "" {
				h.files["tip"][workflowPath] = tt.workflow
			}
			if tt.tags != nil {
				h.tags = tt.tags
			}
			s := &setups{gh: h, gate: setupView{}, actionApproved: func(ref string) bool { return ref == setupCommit }}
			out, err := s.Setup(context.Background(), setupRepo)
			if err != nil || out.Admitted != tt.admitted || out.Policy != (tt.policy == setupPolicy) || len(out.Missing) != tt.missing {
				t.Fatalf("Setup = %+v, %v", out, err)
			}
			want := "not_admitted"
			if tt.policy == setupPolicy {
				want = "first_approval"
			}
			if out.State != want {
				t.Errorf("state %q, want %q", out.State, want)
			}
			for _, read := range h.fileReads {
				if !strings.HasPrefix(read, "tip:") {
					t.Errorf("read mutable ref: %s", read)
				}
			}
		})
	}
}

// TestSetupAcceptsOnlyTheActionRepository: a tag is looked up in the Action's repository. The Action's old home and any
// other repository are not the Action.
func TestSetupAcceptsOnlyTheActionRepository(t *testing.T) {
	for _, tc := range []struct {
		name, repo, ref string
		admitted        bool
		tagReads        []string
	}{
		{"new repository, listed commit", manifest.ActionRepository, setupCommit, true, nil},
		{"new repository, tag", manifest.ActionRepository, "v0.9.0", true, []string{manifest.ActionRepository + "@v0.9.0"}},
		{"old home, listed commit", "alexei-led/archfit-action", setupCommit, false, nil},
		{"old home, tag", "alexei-led/archfit-action", "v0.9.0", false, nil},
		{"another repository, listed commit", "someone/archfit-action", setupCommit, false, nil},
	} {
		t.Run(tc.name, func(t *testing.T) {
			h := newSetupHub()
			h.tags = map[string]string{"v0.9.0": setupCommit}
			h.files["tip"] = map[string]string{
				".archfit.yaml": setupPolicy,
				workflowPath:    "jobs:\n  check:\n    steps:\n      - uses: " + tc.repo + "@" + tc.ref + " # v0.9.0\n",
			}
			s := &setups{gh: h, gate: setupView{}, actionApproved: func(ref string) bool { return ref == setupCommit }}
			out, err := s.Setup(context.Background(), setupRepo)
			if err != nil || out.Admitted != tc.admitted || out.Workflow != tc.admitted {
				t.Fatalf("Setup = %+v, %v; want admitted %v", out, err, tc.admitted)
			}
			if !slices.Equal(h.tagReads, tc.tagReads) {
				t.Errorf("tag reads %v, want %v", h.tagReads, tc.tagReads)
			}
		})
	}
}

func TestSetupFindsAcceptedWorkflowPullOnConvertedRepository(t *testing.T) {
	h := newSetupHub()
	h.files["tip"] = map[string]string{".archfit.yaml": setupPolicy}
	h.pulls = []api.Pull{
		{Number: 201, State: "closed", BaseRef: "main", HeadRepo: setupRepo.FullName, HeadSHA: "closed"},
		{Number: 202, State: "open", BaseRef: "main", HeadRepo: "fork/app", HeadSHA: "fork"},
		{Number: 203, State: "open", BaseRef: "main", HeadRepo: setupRepo.FullName, HeadSHA: "workflow-head"},
		{Number: 204, State: "open", BaseRef: "main", HeadRepo: setupRepo.FullName, HeadSHA: "later-head"},
	}
	h.files["workflow-head"] = map[string]string{workflowPath: setupWorkflow(setupCommit)}
	h.files["later-head"] = map[string]string{workflowPath: setupWorkflow(setupCommit)}
	s := &setups{gh: h, gate: setupView{}, actionApproved: func(ref string) bool { return ref == setupCommit }}
	out, err := s.Setup(context.Background(), setupRepo)
	if err != nil || out.OpenPull != 203 || out.FirstPull != 0 || !out.Policy || out.Workflow || out.Admitted {
		t.Fatalf("Setup = %+v, %v", out, err)
	}
	// The window reads up to pullWindow-1 heads past the match; the match is the answer, so these reads change nothing.
	for _, read := range h.fileReads {
		if !strings.HasPrefix(read, "tip:") && !strings.HasPrefix(read, "workflow-head:") && !strings.HasPrefix(read, "later-head:") {
			t.Errorf("read unexpected pull head: %s", read)
		}
	}
}

// The scan stops reading after the window that holds the first match: pulls beyond it are never read.
func TestSetupScanReadsNoPullBeyondTheFirstMatchsWindow(t *testing.T) {
	h := newSetupHub()
	h.files["tip"] = map[string]string{".archfit.yaml": setupPolicy}
	for i := range 8 {
		head := fmt.Sprintf("head%d", i)
		h.pulls = append(h.pulls, api.Pull{Number: 300 + i, State: "open", BaseRef: "main", HeadRepo: setupRepo.FullName, HeadSHA: head})
		if i == 0 {
			h.files[head] = map[string]string{workflowPath: setupWorkflow(setupCommit)}
		}
	}
	s := &setups{gh: h, gate: setupView{}, actionApproved: func(ref string) bool { return ref == setupCommit }}
	out, err := s.Setup(t.Context(), setupRepo)
	if err != nil || out.OpenPull != 300 {
		t.Fatalf("Setup = %+v, %v; want the first pull", out, err)
	}
	for i := pullWindow; i < 8; i++ {
		if slices.Contains(h.fileReads, fmt.Sprintf("head%d:%s", i, workflowPath)) {
			t.Errorf("read head%d beyond the window", i)
		}
	}
}

func TestSetupTagReadFailureDoesNotAdmit(t *testing.T) {
	h := newSetupHub()
	h.files["tip"] = map[string]string{".archfit.yaml": setupPolicy, workflowPath: setupWorkflow("v4")}
	h.tagErr = api.ErrRateLimited
	s := &setups{gh: h, gate: setupView{}, actionApproved: func(ref string) bool { return ref == setupCommit }}
	out, err := s.Setup(context.Background(), setupRepo)
	if !errors.Is(err, api.ErrRateLimited) || out.Admitted {
		t.Fatalf("Setup = %+v, %v", out, err)
	}
}

func TestSetupApprovalStatesAndOnboardingPull(t *testing.T) {
	digest := policy3.Digest([]byte(setupPolicy))
	tests := []struct {
		name         string
		converted    bool
		state        gate.SetupState
		pull         api.Pull
		want         string
		first, issue int
	}{
		{"new onboarding", false, gate.SetupState{}, api.Pull{Number: 12, State: "open", BaseRef: "main", HeadRepo: setupRepo.FullName, HeadSHA: "onboarding-head"}, "first_approval", 12, 0},
		{"already converted", true, gate.SetupState{}, api.Pull{}, "first_approval", 0, 0},
		{"converted with unrelated pull", true, gate.SetupState{}, api.Pull{Number: 12, State: "open", BaseRef: "main", HeadRepo: setupRepo.FullName, HeadSHA: "onboarding-head"}, "first_approval", 0, 0},
		{"approved", true, gate.SetupState{ApprovedDigest: digest}, api.Pull{}, "approved", 0, 0},
		{"outside flow", true, gate.SetupState{ApprovedDigest: digest, OutsideFlow: true, Issue: 70}, api.Pull{}, "outside_flow", 0, 70},
		{"unjudged direct push", true, gate.SetupState{ApprovedDigest: "sha256:old"}, api.Pull{}, "outside_flow", 0, 0},
		{"fork not onboarding", false, gate.SetupState{}, api.Pull{Number: 12, State: "open", BaseRef: "main", HeadRepo: "mallory/app", HeadSHA: "onboarding-head"}, "not_admitted", 0, 0},
		{"wrong base", false, gate.SetupState{}, api.Pull{Number: 12, State: "open", BaseRef: "feature", HeadRepo: setupRepo.FullName, HeadSHA: "onboarding-head"}, "not_admitted", 0, 0},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			h := newSetupHub()
			files := map[string]string{".archfit.yaml": setupPolicy, workflowPath: setupWorkflow(setupCommit)}
			h.files["onboarding-head"] = files
			if tt.converted {
				h.files["tip"] = files
			}
			if tt.pull.Number > 0 {
				h.pulls = []api.Pull{tt.pull}
			}
			before := map[string]map[string]string{}
			for ref, files := range h.files {
				before[ref] = map[string]string{}
				for path, raw := range files {
					before[ref][path] = raw
				}
			}
			s := &setups{gh: h, gate: setupView{tt.state}, actionApproved: func(ref string) bool { return ref == setupCommit }}
			out, err := s.Setup(context.Background(), setupRepo)
			if err != nil || out.State != tt.want || out.FirstPull != tt.first || out.Issue != tt.issue || out.Admitted != tt.converted {
				t.Fatalf("Setup = %+v, %v", out, err)
			}
			if !reflect.DeepEqual(before, h.files) {
				t.Error("setup changed repository files")
			}
		})
	}
}

func TestUnlistedCommitCannotBeAdmittedThroughASameNamedTag(t *testing.T) {
	h := newSetupHub()
	unlisted := strings.Repeat("b", 40)
	h.files["tip"] = map[string]string{".archfit.yaml": setupPolicy, workflowPath: setupWorkflow(unlisted)}
	h.tags[unlisted] = setupCommit
	s := &setups{gh: h, gate: setupView{}, actionApproved: func(ref string) bool { return ref == setupCommit }}
	out, err := s.Setup(context.Background(), setupRepo)
	if err != nil || out.Admitted || len(h.tagReads) != 0 {
		t.Fatalf("Setup = %+v, %v; tag reads %v", out, err, h.tagReads)
	}
}

func TestSetupRecognizesInitialAdvisoryPull(t *testing.T) {
	h := newSetupHub()
	h.pulls = []api.Pull{{Number: 12, State: "open", BaseRef: "main", HeadRepo: setupRepo.FullName, HeadRef: "archfit/setup", HeadSHA: "head"}}
	h.files["head"] = map[string]string{".archfit.yaml": setupPolicy, workflowPath: setupWorkflow(setupCommit)}
	s := &setups{gh: h, gate: setupView{}, actionApproved: func(ref string) bool { return ref == setupCommit }}
	out, err := s.Setup(t.Context(), setupRepo)
	if err != nil {
		t.Fatal(err)
	}
	field := reflect.ValueOf(out).FieldByName("SetupPull")
	if !field.IsValid() || field.Int() != 12 {
		t.Fatalf("initial setup pull not recognized: %+v", out)
	}
}

func TestSetupPullRejectsLegacyReviews(t *testing.T) {
	for _, tc := range []struct{ name, branch, policy string }{
		{"wrong branch", "feature", setupPolicy},
		{"required policy", "archfit/setup", setupPolicy + "gate: required\n"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			h := newSetupHub()
			h.pulls = []api.Pull{{Number: 12, State: "open", BaseRef: "main", HeadRepo: setupRepo.FullName, HeadRef: tc.branch, HeadSHA: "head"}}
			h.files["head"] = map[string]string{".archfit.yaml": tc.policy, workflowPath: setupWorkflow(setupCommit)}
			s := &setups{gh: h, gate: setupView{}, actionApproved: func(ref string) bool { return ref == setupCommit }}
			out, err := s.Setup(t.Context(), setupRepo)
			if err != nil || out.SetupPull != 0 || out.FirstPull != 12 {
				t.Fatalf("Setup = %+v, %v", out, err)
			}
		})
	}
}

func TestSetupPullRequiresInitialRepositoryState(t *testing.T) {
	for _, tc := range []struct {
		name   string
		pull   api.Pull
		policy string
		state  gate.SetupState
	}{
		{name: "fork", pull: api.Pull{State: "open", BaseRef: "main", HeadRepo: "fork/app", HeadRef: "archfit/setup"}, policy: setupPolicy},
		{name: "wrong base", pull: api.Pull{State: "open", BaseRef: "other", HeadRepo: setupRepo.FullName, HeadRef: "archfit/setup"}, policy: setupPolicy},
		{name: "invalid policy", pull: api.Pull{State: "open", BaseRef: "main", HeadRepo: setupRepo.FullName, HeadRef: "archfit/setup"}, policy: "invalid"},
		{name: "prior approval", pull: api.Pull{State: "open", BaseRef: "main", HeadRepo: setupRepo.FullName, HeadRef: "archfit/setup"}, policy: setupPolicy, state: gate.SetupState{ApprovedDigest: "old"}},
		{name: "outside flow", pull: api.Pull{State: "open", BaseRef: "main", HeadRepo: setupRepo.FullName, HeadRef: "archfit/setup"}, policy: setupPolicy, state: gate.SetupState{OutsideFlow: true}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			h := newSetupHub()
			tc.pull.Number = 12
			tc.pull.HeadSHA = "head"
			h.pulls = []api.Pull{tc.pull}
			h.files["head"] = map[string]string{".archfit.yaml": tc.policy, workflowPath: setupWorkflow(setupCommit)}
			s := &setups{gh: h, gate: setupView{tc.state}, actionApproved: func(ref string) bool { return ref == setupCommit }}
			out, err := s.Setup(t.Context(), setupRepo)
			if err != nil || out.SetupPull != 0 {
				t.Fatalf("Setup = %+v, %v", out, err)
			}
		})
	}
}

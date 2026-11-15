package onboarding

import (
	"context"
	"errors"
	"reflect"
	"strings"
	"testing"

	"github.com/alexei-led/archfit-app/internal/gate"
	"github.com/alexei-led/archfit-app/internal/github/api"
	"github.com/alexei-led/archfit-app/internal/wire/manifest"
	"github.com/alexei-led/archfit-app/internal/wire/policy3"
)

const setupPolicy = "version: 3\nowners: [alice]\nmodules:\n  app:\n    paths: [cmd/**]\n"
const setupCommit = "0123456789abcdef0123456789abcdef01234567"

var setupRepo = api.Repo{RepoRef: api.RepoRef{InstallationID: 7, FullName: "acme/app"}, DefaultBranch: "main", OwnerType: "Organization", Visibility: "private"}

// setupHub exposes only reads: setup cannot write repository files or approval records.
type setupHub struct {
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
			if err != nil || out.Admitted != tt.admitted || len(out.Missing) != tt.missing {
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

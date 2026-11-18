package onboarding

import (
	"context"
	"errors"
	"strings"
	"sync"

	"github.com/alexei-led/archfit-app/internal/gate"
	"github.com/alexei-led/archfit-app/internal/github/api"
	"github.com/alexei-led/archfit-app/internal/wire/manifest"
	"github.com/alexei-led/archfit-app/internal/wire/policy3"
	"github.com/alexei-led/archfit-app/internal/wire/workflow"
)

// Setup is derived from default-branch files, GitHub's rules and recorded approvals.
type Setup struct {
	Approval  gate.ApprovalSummary
	Admitted  bool
	Policy    bool   // valid schema 3 policy on the default branch
	Workflow  bool   // accepted Archfit workflow on the default branch
	OpenPull  int    // open pull request adding an accepted workflow; 0 when none
	State     string // not_admitted, first_approval, approved, outside_flow
	Missing   []string
	FirstPull int // onboarding PR needing approval; 0 for an already-converted repository
	Issue     int
	Rules     RulesetReport
}

// Setups is the v4 onboarding read side. It generates no files and writes no state.
type Setups interface {
	Setup(context.Context, api.Repo) (Setup, error)
}

type setupGitHub interface {
	File(context.Context, api.RepoRef, string, string) ([]byte, string, error)
	BranchHead(context.Context, api.RepoRef, string) (string, error)
	OpenPulls(context.Context, api.RepoRef) ([]api.Pull, error)
	PublicTagCommit(context.Context, string, string) (string, error)
	api.Rulesets
}

type setups struct {
	gh             setupGitHub
	gate           gate.SetupViews
	actionApproved func(string) bool
}

// NewSetup returns the v4 onboarding read side.
func NewSetup(gh *api.Client, g gate.SetupViews) Setups {
	return &setups{gh: tracedGitHub{gh}, gate: g, actionApproved: manifest.ActionApproved}
}

func (s *setups) Setup(ctx context.Context, r api.Repo) (Setup, error) {
	out := Setup{State: "not_admitted", Missing: []string{}}
	// The rules need only the repository and its default branch name, so they overlap the tip read. Early returns
	// wait for them below, so the read never outlives Setup and its result is joined before it is returned.
	var rules RulesetReport
	var ruleReads sync.WaitGroup
	ruleReads.Go(func() { rules = s.rules(ctx, r) })
	defer ruleReads.Wait()
	tip := api.StartStage(ctx, "tip")
	snapshot, err := gate.ReadHomeSnapshot(ctx, s.gh, r)
	tip(1, 0)
	if err != nil {
		return out, err
	}
	var policy gate.HomePolicy
	var accepted bool
	var workflowErr error
	var reads sync.WaitGroup
	files := api.StartStage(ctx, "policy_workflow")
	reads.Go(func() { policy = snapshot.Policy(ctx, s.gh) })
	reads.Go(func() {
		raw, err := snapshot.Workflow(ctx, s.gh)
		accepted, workflowErr = s.acceptWorkflow(ctx, raw, err)
	})
	reads.Wait()
	files(2, 0)
	if policy.ReadErr != nil && !errors.Is(policy.ReadErr, api.ErrNotFound) {
		return out, policy.ReadErr
	}
	digest, valid := policy.Digest, policy.ReadErr == nil && policy.DecodeErr == nil
	if !valid {
		out.Missing = append(out.Missing, "The default branch has no version 3 policy.")
	}
	if workflowErr != nil {
		return out, workflowErr
	}
	if !accepted {
		out.Missing = append(out.Missing, "The default branch has no Archfit workflow (or it uses an Action commit the App does not accept).")
	}
	state, err := s.gate.SetupState(ctx, r)
	if err != nil {
		return out, err
	}
	out.Policy = valid
	out.Workflow = accepted
	out.Admitted = valid && accepted
	if valid && !accepted {
		pulls, err := s.gh.OpenPulls(ctx, r.RepoRef)
		if err != nil {
			return out, err
		}
		for _, p := range pulls {
			if p.State != "open" || p.BaseRef != r.DefaultBranch || !strings.EqualFold(p.HeadRepo, r.FullName) {
				continue
			}
			accepted, err := s.workflowAt(ctx, r, p.HeadSHA)
			if err != nil {
				return out, err
			}
			if accepted {
				out.OpenPull = p.Number
				break
			}
		}
	}
	switch {
	case state.OutsideFlow || valid && state.ApprovedDigest != "" && state.ApprovedDigest != digest:
		out.State = "outside_flow"
		out.Issue = state.Issue
	case out.Admitted && state.ApprovedDigest != "":
		out.State = "approved"
	case valid && state.ApprovedDigest == "":
		out.State = "first_approval"
	}
	// A repository not converted yet can have its first approval on an onboarding PR. Validate its immutable head,
	// not its branch name; the gate binds the approval to that same head and both file digests.
	if !valid && state.ApprovedDigest == "" && !state.OutsideFlow {
		pulls, err := s.gh.OpenPulls(ctx, r.RepoRef)
		if err != nil {
			return out, err
		}
		for _, p := range pulls {
			if p.State != "open" || p.BaseRef != r.DefaultBranch || !strings.EqualFold(p.HeadRepo, r.FullName) {
				continue
			}
			// The policy and the workflow of one head are read together. A workflow read that fails or is missing counts
			// only when the policy is there, as in the serial order, so the answer is unchanged.
			var policyOK, workflowOK bool
			var policyErr, workflowErr error
			var both sync.WaitGroup
			both.Go(func() { _, policyOK, policyErr = s.policyAt(ctx, r, p.HeadSHA) })
			both.Go(func() { workflowOK, workflowErr = s.workflowAt(ctx, r, p.HeadSHA) })
			both.Wait()
			if policyErr != nil {
				return out, policyErr
			}
			if !policyOK {
				continue
			}
			if workflowErr != nil {
				return out, workflowErr
			}
			if !workflowOK {
				continue
			}
			out.State = "first_approval"
			out.FirstPull = p.Number
			break
		}
	}
	action := gate.OwnerAction{Kind: gate.OwnerFirst, Pull: out.FirstPull}
	if state.ApprovedDigest != "" && out.State != "approved" {
		action = gate.OwnerAction{Kind: gate.OwnerWorkflow, Pull: out.OpenPull}
	}
	if out.State == "outside_flow" {
		action = gate.OwnerAction{Kind: gate.OwnerConfirm}
	}
	reads.Go(func() {
		done := api.StartStage(ctx, "approval_summary")
		out.Approval = s.gate.SetupApproval(ctx, r, action)
		done(1, 0)
	})
	reads.Wait()
	ruleReads.Wait()
	out.Rules = rules
	if out.State == "approved" && out.Approval.Status == gate.ApprovalNeeded {
		out.Approval.Status = gate.ApprovalActive
	}
	return out, nil
}

func (s *setups) policyAt(ctx context.Context, r api.Repo, ref string) (digest string, ok bool, err error) {
	raw, _, err := s.gh.File(ctx, r.RepoRef, ref, ".archfit.yaml")
	if errors.Is(err, api.ErrNotFound) {
		return "", false, nil
	}
	if err != nil {
		return "", false, err
	}
	if _, err := policy3.Decode(raw); err != nil {
		return "", false, nil
	}
	return policy3.Digest(raw), true, nil
}

func (s *setups) workflowAt(ctx context.Context, r api.Repo, ref string) (bool, error) {
	raw, _, err := s.gh.File(ctx, r.RepoRef, ref, workflowPath)
	return s.acceptWorkflow(ctx, raw, err)
}

func (s *setups) acceptWorkflow(ctx context.Context, raw []byte, err error) (bool, error) {
	if errors.Is(err, api.ErrNotFound) {
		return false, nil
	}
	if err != nil {
		return false, err
	}
	found := false
	for _, use := range workflow.UsesRefs(raw) {
		ref, ok := workflow.ActionRef(use, manifest.ActionRepository)
		if !ok {
			continue
		}
		found = true
		if s.actionApproved(ref) {
			continue
		}
		// GitHub prefers a full commit SHA over a same-named tag. An unlisted SHA must not be admitted by resolving that tag.
		if len(ref) == 40 && strings.Trim(strings.ToLower(ref), "0123456789abcdef") == "" {
			return false, nil
		}
		commit, err := s.gh.PublicTagCommit(ctx, manifest.ActionRepository, ref)
		if err != nil {
			if errors.Is(err, api.ErrNotFound) {
				return false, nil
			}
			return false, err
		}
		if !s.actionApproved(commit) {
			return false, nil
		}
	}
	return found, nil
}

const (
	workflowPath  = ".github/workflows/archfit.yaml"
	gateCheckName = "archfit/gate"
)

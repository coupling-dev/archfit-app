package gate

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strconv"
	"strings"
	"sync"
	"unicode/utf8"

	"github.com/alexei-led/archfit-app/internal/gate/approvals"
	"github.com/alexei-led/archfit-app/internal/gate/proposals"
	"github.com/alexei-led/archfit-app/internal/gate/records"
	"github.com/alexei-led/archfit-app/internal/gate/requests"
	"github.com/alexei-led/archfit-app/internal/github/api"
	"github.com/alexei-led/archfit-app/internal/store"
	"github.com/alexei-led/archfit-app/internal/wire/policy3"
)

// ErrOwnerFileUnreadable refuses a file whose exact bytes cannot be shown as text.
var ErrOwnerFileUnreadable = errors.New("gate: owner file is not readable text")

// ErrOwnerMerged is ErrOwnerNotPossible for a pull request that is merged, so the page can say there is nothing to
// approve instead of showing a refusal. It is only a reason: the action is refused either way.
var ErrOwnerMerged = errors.New("gate: the pull request is merged")

// OwnerView is the complete, read-only artifact an owner's page shows.
// ActionDigest remains the M2 action binding; Digest binds all displayed inputs and rendering.
type OwnerView struct {
	Kind                      string
	Pull                      int
	Status, Note, MainAction  string
	ActionDigest, Digest      string
	Head, Base, Tip, Approved string
	Files                     []OwnerFile
	Diffs                     []OwnerDiff
	Proposals                 []proposals.RevertProposal
}

// OwnerProposal and OwnerApproval bind the open proposal inputs of a revert.
type OwnerProposal = proposals.RevertProposal
type OwnerApproval = proposals.RevertApproval

// OwnerFile is a file read at an immutable commit, or the exact proposed result.
// Missing distinguishes an absent file from an empty one.
type OwnerFile struct {
	Role, Ref, Path, Digest, Text string
	Missing                       bool
}

// OwnerDiff is the App's rendering of actual file bytes, never a worker's summary.
type OwnerDiff struct{ Title, Path, Text string }

// OwnerView reads the action binding and its actual policy/workflow blobs without writing.
func (g *Gate) OwnerView(ctx context.Context, t Target, a OwnerAction) (OwnerView, error) {
	if err := g.admitted(ctx, t); err != nil {
		return OwnerView{}, err
	}
	v := OwnerView{Kind: a.Kind, Pull: a.Pull, Files: []OwnerFile{}, Diffs: []OwnerDiff{}, Proposals: []proposals.RevertProposal{}}
	add := func(role, ref, path string, raw []byte) error {
		if !utf8.Valid(raw) {
			return fmt.Errorf("%w: %s", ErrOwnerFileUnreadable, path)
		}
		v.Files = append(v.Files, OwnerFile{Role: role, Ref: ref, Path: path, Digest: policy3.Digest(raw), Text: string(raw), Missing: raw == nil})
		return nil
	}
	fetch := func(role, ref, path string, optional bool) ([]byte, error) {
		if ref == "" {
			return nil, ErrOwnerMoved
		}
		raw, _, err := g.repos.File(ctx, t.Repo.RepoRef, ref, path)
		if errors.Is(err, api.ErrNotFound) && optional {
			raw, err = nil, nil
		}
		if err != nil {
			return nil, fmt.Errorf("gate: read %s at %s: %w", path, ref, err)
		}
		return raw, nil
	}
	read := func(role, ref, path string, optional bool) ([]byte, error) {
		raw, err := fetch(role, ref, path, optional)
		if err != nil {
			return nil, err
		}
		return raw, add(role, ref, path, raw)
	}
	diff := func(title, path string, old, next []byte) {
		v.Diffs = append(v.Diffs, OwnerDiff{Title: title, Path: path, Text: requests.Diff(path, old, next)})
	}
	if a.Kind == OwnerRevert {
		rev, err := g.proposals.RevertPreview(ctx, t.Repo)
		if err != nil {
			return OwnerView{}, err
		}
		v.ActionDigest, v.Tip, v.Approved, v.Proposals = rev.Digest, rev.Tip, rev.ApprovedCommit, rev.Proposals
		v.Status, v.MainAction = statusOutsideFlow, "Revert the policy"
		v.Note = "The policy on the default branch changed without an owner's approval. Revert restores the approved policy."
		for _, p := range rev.Proposals {
			v.Note += " It keeps the approved answers in pull request #" + strconv.Itoa(p.Pull) + "."
		}
		for _, f := range []struct {
			role, ref string
			raw       []byte
		}{
			{"approved", rev.ApprovedCommit, rev.Approved}, {"outside", rev.Tip, rev.Outside}, {"result", "", rev.Result},
		} {
			if err := add(f.role, f.ref, approvals.PolicyPath, f.raw); err != nil {
				return OwnerView{}, err
			}
		}
		diff("The policy to restore", approvals.PolicyPath, rev.Outside, rev.Result)
		if len(rev.Proposals) > 0 {
			diff("Approved answers kept on the approved policy", approvals.PolicyPath, rev.Approved, rev.Result)
		}
		return finishOwnerView(t.Repo, v), nil
	}
	c, err := g.approvals.Bind(ctx, t.Repo, a)
	if err != nil {
		return OwnerView{}, g.ownerMergedReason(ctx, t, a, err)
	}
	v.ActionDigest = c.Digest()
	var repo records.Repo
	if err := records.Load(ctx, g.store, store.Tenant(t.Repo.InstallationID), records.RepoKey(t.Repo.FullName), &repo); err != nil && !errors.Is(err, records.ErrNoRecord) {
		return OwnerView{}, err
	}
	var approved, base, head []byte
	if a.Pull > 0 {
		// The approved policy, the pull request and the default branch head are independent. The files at the pull
		// request's commits need the pull request, so they are read in a second round. Errors are taken in the order the
		// reads used to run, so the same failure is reported whichever call finished first.
		var (
			pl                                api.Pull
			oldWorkflow, nextWorkflow         []byte
			approvedErr, pullErr, tipErr      error
			baseErr, headErr, oldErr, nextErr error
			tipSHA                            string
		)
		var first sync.WaitGroup
		if repo.ApprovedDigest != "" {
			first.Add(1)
			go func() {
				defer first.Done()
				approved, approvedErr = fetch("approved", repo.ApprovedCommit, approvals.PolicyPath, false)
			}()
		}
		first.Add(2)
		go func() { defer first.Done(); pl, pullErr = g.pulls.Pull(ctx, t.Repo.RepoRef, a.Pull) }()
		go func() {
			defer first.Done()
			tipSHA, tipErr = g.repos.BranchHead(ctx, t.Repo.RepoRef, t.Repo.DefaultBranch)
		}()
		first.Wait()
		if repo.ApprovedDigest != "" {
			v.Approved = repo.ApprovedCommit
			if approvedErr != nil {
				return OwnerView{}, approvedErr
			}
			if err := add("approved", repo.ApprovedCommit, approvals.PolicyPath, approved); err != nil {
				return OwnerView{}, err
			}
			if policy3.Digest(approved) != repo.ApprovedDigest {
				return OwnerView{}, ErrOwnerMoved
			}
		}
		if pullErr != nil {
			return OwnerView{}, pullErr
		}
		if pl.HeadSHA != c.HeadSHA || pl.BaseRef != t.Repo.DefaultBranch || pl.State != "open" || !strings.EqualFold(pl.HeadRepo, t.Repo.FullName) {
			return OwnerView{}, ErrOwnerMoved
		}
		v.Head, v.Base = pl.HeadSHA, pl.BaseSHA
		v.Tip = tipSHA

		var second sync.WaitGroup
		second.Add(4)
		go func() {
			defer second.Done()
			base, baseErr = fetch("base", v.Base, approvals.PolicyPath, a.Kind == OwnerFirst)
		}()
		go func() { defer second.Done(); head, headErr = fetch("head", v.Head, approvals.PolicyPath, false) }()
		go func() {
			defer second.Done()
			oldWorkflow, oldErr = fetch("workflow-base", v.Tip, approvals.WorkflowPath, true)
		}()
		go func() {
			defer second.Done()
			nextWorkflow, nextErr = fetch("workflow-head", v.Head, approvals.WorkflowPath, false)
		}()
		second.Wait()
		if baseErr != nil {
			return OwnerView{}, baseErr
		}
		if err := add("base", v.Base, approvals.PolicyPath, base); err != nil {
			return OwnerView{}, err
		}
		if headErr != nil {
			return OwnerView{}, headErr
		}
		if err := add("head", v.Head, approvals.PolicyPath, head); err != nil {
			return OwnerView{}, err
		}
		if tipErr != nil {
			return OwnerView{}, tipErr
		}
		if oldErr != nil {
			return OwnerView{}, oldErr
		}
		if err := add("workflow-base", v.Tip, approvals.WorkflowPath, oldWorkflow); err != nil {
			return OwnerView{}, err
		}
		if nextErr != nil {
			return OwnerView{}, nextErr
		}
		if err := add("workflow-head", v.Head, approvals.WorkflowPath, nextWorkflow); err != nil {
			return OwnerView{}, err
		}
		if records.FileDigest(nextWorkflow) != c.Workflow {
			return OwnerView{}, ErrOwnerMoved
		}
		if a.Kind == OwnerWorkflow {
			diff("The workflow change", approvals.WorkflowPath, oldWorkflow, nextWorkflow)
		}
	} else {
		if repo.ApprovedDigest != "" {
			v.Approved = repo.ApprovedCommit
			var err error
			approved, err = read("approved", v.Approved, approvals.PolicyPath, false)
			if err != nil {
				return OwnerView{}, err
			}
			if policy3.Digest(approved) != repo.ApprovedDigest {
				return OwnerView{}, ErrOwnerMoved
			}
		}
		v.Tip = c.Commit
		var err error
		head, err = read("current", v.Tip, approvals.PolicyPath, false)
		if err != nil {
			return OwnerView{}, err
		}
		base = approved
	}
	if policy3.Digest(head) != c.Policy {
		return OwnerView{}, ErrOwnerMoved
	}
	switch a.Kind {
	case OwnerFirst:
		v.Status, v.MainAction = "unknown — the first policy needs approval.", "Approve the first policy"
		v.Note = "No policy is approved yet. If you approve, the policy below becomes the approved policy."
		diff("The first policy", approvals.PolicyPath, nil, head)
		if a.Pull > 0 {
			// The first approval admits both files at the head.
			var old, next []byte
			for _, f := range v.Files {
				if f.Role == "workflow-base" {
					old = []byte(f.Text)
				}
				if f.Role == "workflow-head" {
					next = []byte(f.Text)
				}
			}
			diff("The workflow change", approvals.WorkflowPath, old, next)
		}
	case OwnerWorkflow:
		v.Status, v.MainAction = "unknown — no verdict until this workflow change is approved.", "Approve the workflow change"
		v.Note = "This approves the policy and workflow at this pull request's head."
		diff("The policy change", approvals.PolicyPath, base, head)
	case OwnerConfirm:
		if repo.OutsideFlow == nil || repo.OutsideFlow.Digest != c.Policy {
			return OwnerView{}, ErrOwnerMoved
		}
		v.Status, v.MainAction = statusOutsideFlow, "Confirm the policy"
		v.Note = "The policy on the default branch changed without an owner's approval. Confirm makes the current policy the approved policy."
		diff("The policy change", approvals.PolicyPath, base, head)
	default:
		return OwnerView{}, ErrOwnerNotPossible
	}
	return finishOwnerView(t.Repo, v), nil
}

const statusOutsideFlow = "unknown — the policy changed outside the flow."

// ownerMergedReason says why a workflow approval cannot be made on a merged pull request. Any other error, and a
// failed read of the pull request, is returned as it was.
func (g *Gate) ownerMergedReason(ctx context.Context, t Target, a OwnerAction, err error) error {
	// A merged first approval is not "nothing to approve": the repository may still lack its first policy.
	if a.Kind != OwnerWorkflow || a.Pull == 0 || !errors.Is(err, ErrOwnerNotPossible) {
		return err
	}
	if pl, perr := g.pulls.Pull(ctx, t.Repo.RepoRef, a.Pull); perr == nil && pl.Merged {
		return fmt.Errorf("%w: %w", ErrOwnerMerged, err)
	}
	return err
}

func finishOwnerView(repo api.Repo, v OwnerView) OwnerView {
	// Include repository identity, raw bytes, immutable refs, proposal inputs and the rendered text.
	raw, _ := json.Marshal(struct {
		Repo api.RepoRef
		View OwnerView
	}{repo.RepoRef, v})
	v.Digest = policy3.Digest(raw)
	return v
}

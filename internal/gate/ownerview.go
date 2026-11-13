package gate

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strconv"
	"strings"
	"unicode/utf8"

	"github.com/alexei-led/archfit-app/internal/gate/approvals"
	"github.com/alexei-led/archfit-app/internal/gate/proposals"
	"github.com/alexei-led/archfit-app/internal/gate/records"
	"github.com/alexei-led/archfit-app/internal/gate/requests"
	"github.com/alexei-led/archfit-app/internal/github/api"
	"github.com/alexei-led/archfit-app/internal/store"
	"github.com/alexei-led/archfit-app/internal/wire/policy3"
)

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
			return fmt.Errorf("gate: %s is not readable text", path)
		}
		v.Files = append(v.Files, OwnerFile{Role: role, Ref: ref, Path: path, Digest: policy3.Digest(raw), Text: string(raw), Missing: raw == nil})
		return nil
	}
	read := func(role, ref, path string, optional bool) ([]byte, error) {
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
		v.Status, v.MainAction = "unknown: the policy changed outside the flow.", "Revert the policy"
		v.Note = "This restores the approved policy."
		for _, p := range rev.Proposals {
			v.Note += " It keeps the approved answers in #" + strconv.Itoa(p.Pull) + "."
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
		return OwnerView{}, err
	}
	v.ActionDigest = c.Digest()
	var repo records.Repo
	if err := records.Load(ctx, g.store, store.Tenant(t.Repo.InstallationID), records.RepoKey(t.Repo.FullName), &repo); err != nil && !errors.Is(err, records.ErrNoRecord) {
		return OwnerView{}, err
	}
	var approved []byte
	if repo.ApprovedDigest != "" {
		v.Approved = repo.ApprovedCommit
		approved, err = read("approved", v.Approved, approvals.PolicyPath, false)
		if err != nil {
			return OwnerView{}, err
		}
		if policy3.Digest(approved) != repo.ApprovedDigest {
			return OwnerView{}, ErrOwnerMoved
		}
	}
	var base, head []byte
	if a.Pull > 0 {
		pl, err := g.pulls.Pull(ctx, t.Repo.RepoRef, a.Pull)
		if err != nil {
			return OwnerView{}, err
		}
		if pl.HeadSHA != c.HeadSHA || pl.BaseRef != t.Repo.DefaultBranch || pl.State != "open" || !strings.EqualFold(pl.HeadRepo, t.Repo.FullName) {
			return OwnerView{}, ErrOwnerMoved
		}
		v.Head, v.Base = pl.HeadSHA, pl.BaseSHA
		base, err = read("base", v.Base, approvals.PolicyPath, a.Kind == OwnerFirst)
		if err != nil {
			return OwnerView{}, err
		}
		head, err = read("head", v.Head, approvals.PolicyPath, false)
		if err != nil {
			return OwnerView{}, err
		}
		v.Tip, err = g.repos.BranchHead(ctx, t.Repo.RepoRef, t.Repo.DefaultBranch)
		if err != nil {
			return OwnerView{}, err
		}
		old, err := read("workflow-base", v.Tip, approvals.WorkflowPath, true)
		if err != nil {
			return OwnerView{}, err
		}
		next, err := read("workflow-head", v.Head, approvals.WorkflowPath, false)
		if err != nil {
			return OwnerView{}, err
		}
		if records.FileDigest(next) != c.Workflow {
			return OwnerView{}, ErrOwnerMoved
		}
		if a.Kind == OwnerWorkflow {
			diff("The workflow change", approvals.WorkflowPath, old, next)
		}
	} else {
		v.Tip = c.Commit
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
		v.Status, v.MainAction = "unknown: the first policy needs approval.", "Approve the first policy"
		v.Note = "There is no approved policy yet. Approving makes every line below the approved policy."
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
		v.Status, v.MainAction = "unknown: the workflow changed.", "Approve the workflow change"
		v.Note = "This approves the policy and workflow at this pull request's head."
		diff("The policy change", approvals.PolicyPath, base, head)
	case OwnerConfirm:
		if repo.OutsideFlow == nil || repo.OutsideFlow.Digest != c.Policy {
			return OwnerView{}, ErrOwnerMoved
		}
		v.Status, v.MainAction = "unknown: the policy changed outside the flow.", "Confirm the policy"
		v.Note = "This makes the current policy the approved policy."
		diff("The policy change", approvals.PolicyPath, base, head)
	default:
		return OwnerView{}, ErrOwnerNotPossible
	}
	return finishOwnerView(t.Repo, v), nil
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

package gate

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"slices"
	"strings"

	"github.com/coupling-dev/archfit-app/internal/gate/records"
	"github.com/coupling-dev/archfit-app/internal/gate/sweep"
	"github.com/coupling-dev/archfit-app/internal/github/api"
	"github.com/coupling-dev/archfit-app/internal/store"
	"github.com/coupling-dev/archfit-app/internal/wire/policy3"
)

// RequestView contains the request page data and the recorded answer, when available.
type RequestView struct {
	Number                                               int
	State, Reason, Rule, Path, From, To, Expires, Record string
	IssueHref                                            string
	Numbers                                              RequestNumbers
	Policy                                               bool
	Layers                                               []string
	Modules                                              []RequestModule
	Allowed                                              bool
	Earlier                                              []RequestEarlier
	Answer                                               *RequestAnswer
	Dependencies                                         []RequestDependency
	Surface                                              *records.Surface // a surface request (engine 0.9.0)
	Waiting                                              []int            // pull requests waiting on this request
	AskedBy, AskedAt                                     string           // who filed the request and when
	// FirstApproval is true for an open request that no answer can reach yet: the default branch has a policy, but no
	// repository administrator approved the first policy.
	FirstApproval bool
	// PolicyPull is the App's policy pull request that carries the answer, 0 when none; PolicyMerged says the policy
	// has the answer.
	PolicyPull   int
	PolicyMerged bool
	// Rechecks are the open waiting pull requests and their check after the answer reached the policy, in Waiting
	// order.
	Rechecks []RequestRecheck
}

// RequestRecheck is one waiting pull request after the answer reached the policy: its check status under that
// policy (pass, fail, waiting or unknown), RecheckRunning while no result with that policy landed yet (the App runs the
// check again; a refused or missing run also stays here), or RecheckPush when the check runs again only after a new
// push.
type RequestRecheck struct {
	Number int
	Status string
}

// The re-check states of a RequestRecheck before its result lands.
const (
	RecheckRunning = "running"
	RecheckPush    = "push"
)

// RequestView reads a request. Layers, modules and dependencies share one target-policy snapshot.
func (g *Gate) RequestView(ctx context.Context, t Target, number int) (RequestView, error) {
	if err := g.admitted(ctx, t); err != nil {
		return RequestView{}, err
	}
	v, err := g.asks.View(ctx, t.Repo, number)
	if err != nil {
		return RequestView{}, err
	}
	out := RequestView{Number: v.Number, State: v.State, Reason: v.Reason, Rule: v.Rule, Path: v.Path, From: v.From, To: v.To,
		Expires: v.Expires, Record: v.Record, Numbers: v.Numbers, Policy: v.Policy, Layers: v.Layers, Modules: v.Modules,
		Allowed: v.Allowed, Earlier: v.Earlier, Dependencies: v.Dependencies, Surface: v.Surface, Waiting: v.Waiting, AskedBy: v.AskedBy, AskedAt: v.AskedAt, IssueHref: fmt.Sprintf("https://github.com/%s/issues/%d", t.Repo.FullName, number)}
	if v.State == records.StateOpen && v.Policy {
		repo, err := g.repoRecord(ctx, t)
		if err != nil {
			return RequestView{}, err
		}
		out.FirstApproval = repo.ApprovedDigest == ""
	}
	if v.State != records.StateAnswered {
		return out, nil
	}
	tenant := store.Tenant(t.Repo.InstallationID)
	var req records.Request
	if err := records.Load(ctx, g.store, tenant, records.RequestKey(t.Repo.FullName, number), &req); err != nil {
		return RequestView{}, err
	}
	if req.State != records.StateAnswered || req.Approval == "" {
		return out, nil
	}
	if strings.Contains(req.Approval, "/") {
		return RequestView{}, fmt.Errorf("request view: invalid approval id")
	}
	var approval records.Approval
	if err := records.Load(ctx, g.store, tenant, records.ApprovalKey(t.Repo.FullName, req.Approval), &approval); err != nil {
		return RequestView{}, err
	}
	if approval.State == records.StateVoid {
		return out, nil
	}
	var change AnswerChange
	if err := json.Unmarshal(approval.Change, &change); err != nil {
		return RequestView{}, fmt.Errorf("request view: unreadable answer: %w", err)
	}
	if approval.Request != number || change.Request != number || string(change.Kind) != approval.Kind {
		return RequestView{}, fmt.Errorf("request view: answer does not match request")
	}
	switch change.Kind {
	case policy3.Approve, policy3.ApproveUntil, policy3.Reject, policy3.DeclarePublic, policy3.DeclareModel:
		out.Answer = &RequestAnswer{Kind: change.Kind, Until: change.Until, Instead: change.Instead}
	default:
		return RequestView{}, fmt.Errorf("request view: unknown answer")
	}
	out.PolicyMerged = approval.State == records.StateMerged
	if approval.Proposal != "" {
		var p records.Proposal
		if err := records.Load(ctx, g.store, tenant, records.ProposalKey(t.Repo.FullName, approval.Proposal), &p); err != nil {
			return RequestView{}, err
		}
		out.PolicyPull, out.PolicyMerged = p.PullRequest, out.PolicyMerged || p.State == records.StateMerged
	}
	if !out.PolicyMerged || change.Kind == policy3.Reject {
		return out, nil
	}
	repo, err := g.repoRecord(ctx, t)
	if err != nil {
		return RequestView{}, err
	}
	for _, n := range out.Waiting {
		var pl records.Pull
		err := records.Load(ctx, g.store, tenant, records.PullKey(t.Repo.FullName, n), &pl)
		if err != nil && !errors.Is(err, records.ErrNoRecord) {
			return RequestView{}, err
		}
		status := recheckStatus(pl, repo.ApprovedDigest, approval.Proposal)
		if status == RecheckRunning || status == RecheckPush {
			// A pull request closed since it asked runs no check again: it leaves the re-check. A failed read keeps it, so
			// GitHub being down never hides the request.
			gp, err := g.pullRead(ctx, t.Repo.RepoRef, n)
			if errors.Is(err, api.ErrNotFound) || err == nil && gp.State != "open" {
				continue
			}
		}
		out.Rechecks = append(out.Rechecks, RequestRecheck{Number: n, Status: status})
	}
	return out, nil
}

// repoRecord reads the repository record of t; none is the zero record.
func (g *Gate) repoRecord(ctx context.Context, t Target) (records.Repo, error) {
	repo, err := loadRepoRecord(ctx, g.store, t.Repo.RepoRef)
	if err != nil && !errors.Is(err, records.ErrNoRecord) {
		return records.Repo{}, fmt.Errorf("request view: read the repository: %w", err)
	}
	return repo, nil
}

// recheckStatus is a waiting pull request's check after the answer of proposal reached the approved policy: the
// status of a result under that policy once it lands, else whether the App runs the check again or a new push must.
func recheckStatus(pl records.Pull, approved, proposal string) string {
	if approved != "" && pl.PolicyDigest == approved && slices.Contains([]string{"pass", "fail", "waiting", "unknown"}, pl.Status) {
		return pl.Status
	}
	push := slices.ContainsFunc(pl.Rechecks, func(rc records.Recheck) bool {
		return rc.Proposal == proposal && (rc.Path == sweep.PathNotUpdated || rc.Path == sweep.PathFork)
	})
	if proposal != "" && push {
		return RecheckPush
	}
	return RecheckRunning
}

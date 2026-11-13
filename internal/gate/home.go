package gate

import (
	"bytes"
	"cmp"
	"context"
	"errors"
	"fmt"
	"slices"
	"strconv"
	"strings"

	"github.com/alexei-led/archfit-app/internal/gate/approvals"
	"github.com/alexei-led/archfit-app/internal/gate/records"
	"github.com/alexei-led/archfit-app/internal/github/api"
	"github.com/alexei-led/archfit-app/internal/store"
	"github.com/alexei-led/archfit-app/internal/wire/policy3"
)

const homePolicyPath = ".archfit.yaml"

// HomeViews supplies the read-only repository data of Home through the gate façade.
type HomeViews interface {
	Home(context.Context, api.Repo) (HomeState, error)
}

// HomeState is a target policy and its requests, repository evidence and owner actions.
// Policy is read at Tip; OutsideFlow also covers a digest that has not yet been judged by a push delivery.
type HomeState struct {
	Policy                  policy3.File
	PolicyAvailable         bool
	Tip                     string
	OutsideFlow             bool
	Issue                   int
	Requests                []HomeRequest
	Drift                   []HomeError
	DriftKnown, DriftUnread bool
	RepositoryState         string
	DriftIssue              int
	ExceptionIssues         map[HomeException]int
	WorkflowPulls           []int
	Touched                 []HomeError
}

// HomeRequest is an open request with the record shown on its issue.
type HomeRequest struct {
	Number                 int
	Rule, From, To, Record string
}

// HomeException identifies a dated exception without publishing the storage key format.
type HomeException struct{ Rule, From, To, Expires string }

// HomeError is the rule and modules of an error, never source text.
type HomeError struct{ Rule, From, To string }

type homeGitHub interface {
	File(context.Context, api.RepoRef, string, string) ([]byte, string, error)
	BranchHead(context.Context, api.RepoRef, string) (string, error)
	OpenPulls(context.Context, api.RepoRef) ([]api.Pull, error)
}

type homeViews struct {
	gh         homeGitHub
	store      store.Store
	admissions *Admissions
}

// NewHome reads the target policy at the protected tip; workflow eligibility also reads proposed pull heads.
func NewHome(gh homeGitHub, s store.Store, admissions *Admissions) HomeViews {
	return &homeViews{gh: gh, store: s, admissions: admissions}
}

func (h *homeViews) Home(ctx context.Context, r api.Repo) (HomeState, error) {
	var out HomeState
	admitted, err := h.admissions.Admitted(ctx, r.InstallationID, strings.SplitN(r.FullName, "/", 2)[0])
	if err != nil {
		return out, err
	}
	if !admitted {
		return out, api.ErrNotFound
	}
	t := store.Tenant(r.InstallationID)
	var rec records.Repo
	if err := records.Load(ctx, h.store, t, records.RepoKey(r.FullName), &rec); err != nil && !errors.Is(err, records.ErrNoRecord) {
		return out, err
	}
	tip, err := h.gh.BranchHead(ctx, r.RepoRef, r.DefaultBranch)
	if err != nil {
		return out, err
	}
	raw, _, err := h.gh.File(ctx, r.RepoRef, tip, homePolicyPath)
	if err != nil && !errors.Is(err, api.ErrNotFound) {
		return out, err
	}
	pol, decodeErr := policy3.Decode(raw)
	out.Tip = tip
	out.PolicyAvailable = err == nil && decodeErr == nil
	out.OutsideFlow = rec.OutsideFlow != nil || rec.ApprovedDigest != "" && (!out.PolicyAvailable || rec.ApprovedDigest != policy3.Digest(raw))
	if !out.PolicyAvailable && !out.OutsideFlow {
		return out, fmt.Errorf("home: read the target policy: %w", cmp.Or(err, decodeErr))
	}
	if rec.OutsideFlow != nil {
		out.Issue = rec.OutsideFlow.Issue
	}
	out.Policy = pol
	out.DriftIssue = rec.DriftIssue
	out.ExceptionIssues = map[HomeException]int{}
	for _, x := range pol.Exceptions {
		key := strings.Join(slices.DeleteFunc([]string{"exception", x.Rule, x.From, x.To}, func(s string) bool { return s == "" }), " ") + " " + x.Expires
		out.ExceptionIssues[HomeException{x.Rule, x.From, x.To, x.Expires}] = rec.ExpiryIssues[key]
	}
	if rec.Drift != nil {
		out.DriftKnown = rec.Drift.Head == tip
		out.DriftUnread = rec.Drift.Unread
		out.RepositoryState = rec.Drift.State
		for _, e := range rec.Drift.Errors {
			out.Drift = append(out.Drift, HomeError{e.Rule, e.From, e.To})
		}
	}
	requests, err := h.index(ctx, t, records.RequestIndexKey(r.FullName), records.SchemaRequestIndex)
	if err != nil {
		return out, err
	}
	for _, entry := range requests.Entries {
		number, err := strconv.Atoi(entry.ID)
		if err != nil || number <= 0 {
			return out, errors.New("home: invalid request number")
		}
		var req records.Request
		if err := records.Load(ctx, h.store, t, records.RequestKey(r.FullName, number), &req); err != nil {
			return out, err
		}
		if req.State == records.StateOpen {
			out.Requests = append(out.Requests, HomeRequest{Number: number, Rule: req.Rule, From: req.From, To: req.To, Record: req.Record})
		}
	}
	slices.SortFunc(out.Requests, func(a, b HomeRequest) int { return cmp.Compare(a.Number, b.Number) })
	pulls, err := h.gh.OpenPulls(ctx, r.RepoRef)
	if err != nil {
		return out, err
	}
	for _, p := range pulls {
		if p.State != "open" || p.BaseRef != r.DefaultBranch || !strings.EqualFold(p.HeadRepo, r.FullName) {
			continue
		}
		var pull records.Pull
		err := records.Load(ctx, h.store, t, records.PullKey(r.FullName, p.Number), &pull)
		if err != nil && !errors.Is(err, records.ErrNoRecord) {
			return out, err
		}
		if pull.HeadSHA != p.HeadSHA {
			continue
		}
		// Error keys contain rule, actual module names and a symbol/path, not deny selectors.
		for _, key := range pull.Errors {
			fields := strings.Fields(key)
			if len(fields) < 3 {
				continue
			}
			if _, ok := pol.Modules[fields[1]]; !ok {
				continue
			}
			if _, ok := pol.Modules[fields[2]]; !ok {
				continue
			}
			if slices.ContainsFunc(pol.Rules, func(rule policy3.Rule) bool { return rule.Deny != nil && rule.ID == fields[0] }) {
				out.Touched = append(out.Touched, HomeError{fields[0], fields[1], fields[2]})
			}
		}

	}
	if rec.ApprovedDigest != "" && !out.OutsideFlow {
		out.WorkflowPulls, err = h.workflows(ctx, r, tip, pulls)
		if err != nil {
			return out, err
		}
	}
	return out, nil
}

func (h *homeViews) index(ctx context.Context, t store.Tenant, key, schema string) (records.Index, error) {
	index := records.Index{Schema: schema}
	err := records.Load(ctx, h.store, t, key, &index)
	if errors.Is(err, records.ErrNoRecord) {
		err = nil
	}
	return index, err
}

func (h *homeViews) file(ctx context.Context, r api.RepoRef, ref, path string) ([]byte, error) {
	raw, _, err := h.gh.File(ctx, r, ref, path)
	if errors.Is(err, api.ErrNotFound) {
		return nil, nil
	}
	return raw, err
}

// workflows lists changes the M2 workflow action can approve. Forks and other targets never enter this list.
func (h *homeViews) workflows(ctx context.Context, r api.Repo, tip string, pulls []api.Pull) ([]int, error) {
	current, err := h.file(ctx, r.RepoRef, tip, approvals.WorkflowPath)
	if err != nil {
		return nil, err
	}
	proposals, err := h.index(ctx, store.Tenant(r.InstallationID), records.ProposalIndexKey(r.FullName), records.SchemaProposalIndex)
	if err != nil {
		return nil, err
	}
	approved := map[int]string{}
	for _, entry := range proposals.Entries {
		var p records.Proposal
		if err := records.Load(ctx, h.store, store.Tenant(r.InstallationID), records.ProposalKey(r.FullName, entry.ID), &p); err != nil {
			return nil, err
		}
		if p.State == records.StateOpen && (p.Kind == records.ProposalWorkflow || p.Kind == records.ProposalFirst) {
			approved[p.PullRequest] = p.HeadSHA
		}
	}
	var out []int
	for _, p := range pulls {
		if p.State != "open" || p.HeadSHA == "" || p.BaseRef != r.DefaultBranch || !strings.EqualFold(p.HeadRepo, r.FullName) || approved[p.Number] == p.HeadSHA {
			continue
		}
		run, err := h.file(ctx, r.RepoRef, p.HeadSHA, approvals.WorkflowPath)
		if err != nil {
			return nil, err
		}
		if run == nil || bytes.Equal(run, current) {
			continue
		}
		raw, err := h.file(ctx, r.RepoRef, p.HeadSHA, homePolicyPath)
		if err != nil {
			return nil, err
		}
		if _, err := policy3.Decode(raw); err != nil {
			continue
		}
		out = append(out, p.Number)
	}
	slices.Sort(out)
	return out, nil
}

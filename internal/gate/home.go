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
	"sync"

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
	admitted, err := h.admissions.Admitted(ctx, r.InstallationID, strings.SplitN(r.FullName, "/", 2)[0])
	if err != nil {
		return HomeState{}, err
	}
	if !admitted {
		return HomeState{}, api.ErrNotFound
	}
	return h.bounded().read(ctx, r)
}

// homeLimit holds one slot per read in flight. A read takes its slot only while it runs, so stages that overlap
// share the bound instead of multiplying it.
type homeLimit chan struct{}

func (l homeLimit) run(read func() error) error {
	l <- struct{}{}
	defer func() { <-l }()
	return read()
}

type boundedGitHub struct {
	next  homeGitHub
	limit homeLimit
}

func (g boundedGitHub) BranchHead(ctx context.Context, r api.RepoRef, branch string) (string, error) {
	var out string
	err := g.limit.run(func() (err error) {
		out, err = g.next.BranchHead(ctx, r, branch)
		return err
	})
	return out, err
}

func (g boundedGitHub) File(ctx context.Context, r api.RepoRef, ref, path string) ([]byte, string, error) {
	var raw []byte
	var blob string
	err := g.limit.run(func() (err error) {
		raw, blob, err = g.next.File(ctx, r, ref, path)
		return err
	})
	return raw, blob, err
}

func (g boundedGitHub) OpenPulls(ctx context.Context, r api.RepoRef) ([]api.Pull, error) {
	var out []api.Pull
	err := g.limit.run(func() (err error) {
		out, err = g.next.OpenPulls(ctx, r)
		return err
	})
	return out, err
}

type boundedStore struct {
	store.Store
	limit homeLimit
}

func (s boundedStore) Get(ctx context.Context, t store.Tenant, key string) ([]byte, error) {
	var raw []byte
	err := s.limit.run(func() (err error) {
		raw, err = s.Store.Get(ctx, t, key)
		return err
	})
	return raw, err
}

// bounded is one Home request's view of the store and GitHub, sharing a single bound of homeReadConcurrency reads.
func (h *homeViews) bounded() *homeViews {
	limit := make(homeLimit, homeReadConcurrency)
	return &homeViews{gh: boundedGitHub{h.gh, limit}, store: boundedStore{h.store, limit}, admissions: h.admissions}
}

func policyAvailable(p HomePolicy) bool { return p.ReadErr == nil && p.DecodeErr == nil }

func outsideFlow(rec records.Repo, p HomePolicy) bool {
	return rec.OutsideFlow != nil || rec.ApprovedDigest != "" && (!policyAvailable(p) || rec.ApprovedDigest != p.Digest)
}

// read runs Home's independent reads together, then reports the first failure in the order the serial code reached
// it. A read the serial code would skip is still made when it is independent, but its error only counts when the
// serial code would have made it.
func (h *homeViews) read(ctx context.Context, r api.Repo) (HomeState, error) {
	var out HomeState
	t := store.Tenant(r.InstallationID)
	var (
		rec                        records.Repo
		recErr                     error
		snapshot                   *HomeSnapshot
		tipErr                     error
		policy                     HomePolicy
		requests                   []HomeRequest
		reqErr                     error
		pulls                      []api.Pull
		loadedPulls                []records.Pull
		pullsErr                   error
		current                    []byte
		currentErr                 error
		approved                   map[int]string
		approvedErr                error
		workflowPulls              []int
		workflowErr                error
		repoDone, tipDone          = make(chan struct{}), make(chan struct{})
		workflowDone, proposalDone = make(chan struct{}), make(chan struct{})
		pullsDone                  = make(chan struct{})
	)
	var stages sync.WaitGroup
	stages.Go(func() {
		rec, recErr = h.repoRecord(ctx, r.RepoRef)
		close(repoDone)
	})
	stages.Go(func() {
		snapshot, tipErr = ReadHomeSnapshot(ctx, h.gh, r)
		close(tipDone)
		if tipErr == nil {
			policy = snapshot.Policy(ctx, h.gh)
		}
	})
	stages.Go(func() {
		requests, reqErr = h.requestRecords(ctx, t, r)
	})
	stages.Go(func() {
		<-repoDone
		<-tipDone
		if recErr == nil && tipErr == nil && rec.ApprovedDigest != "" {
			current, currentErr = snapshot.Workflow(ctx, h.gh)
			if errors.Is(currentErr, api.ErrNotFound) {
				current, currentErr = nil, nil
			}
		}
		close(workflowDone)
	})
	stages.Go(func() {
		<-repoDone
		if recErr == nil && rec.ApprovedDigest != "" {
			approved, approvedErr = h.approvedProposals(ctx, t, r)
		}
		close(proposalDone)
	})
	stages.Go(func() {
		pulls, loadedPulls, pullsErr = h.pullRecords(ctx, t, r)
		close(pullsDone)
	})
	stages.Go(func() {
		<-repoDone
		<-pullsDone
		<-proposalDone
		if recErr != nil || rec.ApprovedDigest == "" || pullsErr != nil || approvedErr != nil {
			return
		}
		candidates := workflowCandidates(r, pulls, approved)
		heads := h.workflowHeads(ctx, r, candidates)
		<-workflowDone
		if currentErr != nil {
			return
		}
		workflowPulls, workflowErr = h.workflowListed(ctx, r, current, candidates, heads)
	})
	stages.Wait()

	if recErr != nil {
		return out, recErr
	}
	if tipErr != nil {
		return out, tipErr
	}
	if policy.ReadErr != nil && !errors.Is(policy.ReadErr, api.ErrNotFound) {
		return out, policy.ReadErr
	}
	tip, pol := snapshot.Tip(), policy.File
	out.Tip = tip
	out.PolicyAvailable = policyAvailable(policy)
	out.OutsideFlow = outsideFlow(rec, policy)
	if !out.PolicyAvailable && !out.OutsideFlow {
		return out, fmt.Errorf("home: read the target policy: %w", cmp.Or(policy.ReadErr, policy.DecodeErr))
	}
	if reqErr != nil {
		return out, reqErr
	}
	if pullsErr != nil {
		return out, pullsErr
	}
	if rec.ApprovedDigest != "" && !out.OutsideFlow {
		for _, err := range []error{currentErr, approvedErr, workflowErr} {
			if err != nil {
				return out, err
			}
		}
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
	out.Requests = requests
	for i, p := range pulls {
		pull := loadedPulls[i]
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
		out.WorkflowPulls = workflowPulls
	}
	return out, nil
}

func (h *homeViews) repoRecord(ctx context.Context, r api.RepoRef) (records.Repo, error) {
	rec, err := loadRepoRecord(ctx, h.store, r)
	if errors.Is(err, records.ErrNoRecord) {
		return rec, nil
	}
	return rec, err
}

func (h *homeViews) requestRecords(ctx context.Context, t store.Tenant, r api.Repo) ([]HomeRequest, error) {
	requests, err := h.index(ctx, t, records.RequestIndexKey(r.FullName), records.SchemaRequestIndex)
	if err != nil {
		return nil, err
	}
	loaded := make([]records.Request, len(requests.Entries))
	numbers := make([]int, len(requests.Entries))
	if err := homeReads(len(requests.Entries), func(i int) error {
		number, err := strconv.Atoi(requests.Entries[i].ID)
		if err != nil || number <= 0 {
			return errors.New("home: invalid request number")
		}
		numbers[i] = number
		return records.Load(ctx, h.store, t, records.RequestKey(r.FullName, number), &loaded[i])
	}); err != nil {
		return nil, err
	}
	var out []HomeRequest
	for i, req := range loaded {
		if req.State == records.StateOpen {
			out = append(out, HomeRequest{Number: numbers[i], Rule: req.Rule, From: req.From, To: req.To, Record: req.Record})
		}
	}
	slices.SortFunc(out, func(a, b HomeRequest) int { return cmp.Compare(a.Number, b.Number) })
	return out, nil
}

// pullRecords returns the open pull requests into the default branch of this repository and their records.
func (h *homeViews) pullRecords(ctx context.Context, t store.Tenant, r api.Repo) ([]api.Pull, []records.Pull, error) {
	listed, err := h.gh.OpenPulls(ctx, r.RepoRef)
	if err != nil {
		return nil, nil, err
	}
	// The owner reviews of this request take these pull requests from the listing, not from a GET each.
	SeedOpenPulls(ctx, r.RepoRef, listed)
	pulls := slices.DeleteFunc(slices.Clone(listed), func(p api.Pull) bool {
		return p.State != "open" || p.BaseRef != r.DefaultBranch || !strings.EqualFold(p.HeadRepo, r.FullName)
	})
	loaded := make([]records.Pull, len(pulls))
	err = homeReads(len(pulls), func(i int) error {
		err := records.Load(ctx, h.store, t, records.PullKey(r.FullName, pulls[i].Number), &loaded[i])
		if errors.Is(err, records.ErrNoRecord) {
			return nil
		}
		return err
	})
	return pulls, loaded, err
}

// approvedProposals maps each pull request with an open workflow or first-change proposal to the head it was approved at.
func (h *homeViews) approvedProposals(ctx context.Context, t store.Tenant, r api.Repo) (map[int]string, error) {
	proposals, err := h.index(ctx, t, records.ProposalIndexKey(r.FullName), records.SchemaProposalIndex)
	if err != nil {
		return nil, err
	}
	loaded := make([]records.Proposal, len(proposals.Entries))
	if err := homeReads(len(proposals.Entries), func(i int) error {
		return records.Load(ctx, h.store, t, records.ProposalKey(r.FullName, proposals.Entries[i].ID), &loaded[i])
	}); err != nil {
		return nil, err
	}
	approved := map[int]string{}
	for _, p := range loaded {
		if p.State == records.StateOpen && (p.Kind == records.ProposalWorkflow || p.Kind == records.ProposalFirst) {
			approved[p.PullRequest] = p.HeadSHA
		}
	}
	return approved, nil
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

// workflowCandidates are the open pull requests into the default branch whose workflow may be a change.
func workflowCandidates(r api.Repo, pulls []api.Pull, approved map[int]string) []api.Pull {
	var out []api.Pull
	for _, p := range pulls {
		if p.State != "open" || p.HeadSHA == "" || p.BaseRef != r.DefaultBranch || !strings.EqualFold(p.HeadRepo, r.FullName) || approved[p.Number] == p.HeadSHA {
			continue
		}
		out = append(out, p)
	}
	return out
}

// workflowHead is one read of a candidate's head: the file, or the error that stopped it.
type workflowHead struct {
	raw []byte
	err error
}

// workflowHeads reads each candidate's workflow at its head. These reads need only the listing, not the protected
// workflow they are compared with, so they start before it is known.
func (h *homeViews) workflowHeads(ctx context.Context, r api.Repo, candidates []api.Pull) []workflowHead {
	heads := make([]workflowHead, len(candidates))
	_ = homeReads(len(candidates), func(i int) error {
		heads[i].raw, heads[i].err = h.file(ctx, r.RepoRef, candidates[i].HeadSHA, approvals.WorkflowPath)
		return nil
	})
	return heads
}

// workflowListed lists the candidates whose workflow changes to one the M2 action can approve. The policy is read
// only for a changed workflow. The first failure in list order is the answer, as the serial code read each
// candidate's files in turn.
func (h *homeViews) workflowListed(ctx context.Context, r api.Repo, current []byte, candidates []api.Pull, heads []workflowHead) ([]int, error) {
	changed := func(i int) bool {
		return heads[i].err == nil && heads[i].raw != nil && !bytes.Equal(heads[i].raw, current)
	}
	policies := make([]workflowHead, len(candidates))
	var read []int
	for i := range candidates {
		if changed(i) {
			read = append(read, i)
		}
	}
	_ = homeReads(len(read), func(j int) error {
		i := read[j]
		policies[i].raw, policies[i].err = h.file(ctx, r.RepoRef, candidates[i].HeadSHA, homePolicyPath)
		return nil
	})
	var out []int
	for i, p := range candidates {
		if heads[i].err != nil {
			return nil, heads[i].err
		}
		if !changed(i) {
			continue
		}
		if policies[i].err != nil {
			return nil, policies[i].err
		}
		if _, err := policy3.Decode(policies[i].raw); err == nil {
			out = append(out, p.Number)
		}
	}
	slices.Sort(out)
	return out, nil
}

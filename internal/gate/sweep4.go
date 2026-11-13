package gate

import (
	"cmp"
	"context"
	"errors"
	"fmt"
	"log/slog"
	"slices"
	"strconv"
	"time"

	"github.com/alexei-led/archfit-app/internal/gate/approvals"
	"github.com/alexei-led/archfit-app/internal/gate/expiry"
	"github.com/alexei-led/archfit-app/internal/gate/feedback"
	"github.com/alexei-led/archfit-app/internal/gate/ingest"
	"github.com/alexei-led/archfit-app/internal/gate/inputs"
	"github.com/alexei-led/archfit-app/internal/gate/proposals"
	"github.com/alexei-led/archfit-app/internal/gate/records"
	asks "github.com/alexei-led/archfit-app/internal/gate/requests"
	"github.com/alexei-led/archfit-app/internal/gate/sweep"
	"github.com/alexei-led/archfit-app/internal/github/api"
	"github.com/alexei-led/archfit-app/internal/store"
	"github.com/alexei-led/archfit-app/internal/wire/envelope"
	"github.com/alexei-led/archfit-app/internal/wire/policy3"
	"github.com/alexei-led/archfit-app/internal/wire/result"
)

const (
	gateCheck4          = "archfit/gate"
	waitingRefreshAfter = 10 * 24 * time.Hour
	missingUpload4After = time.Hour
	maxSweepResultBytes = 5 << 20
)

// sweep4 reconciles Archfit 4 records and checks without legacy receipt or module-graph rules.
func (g *Gate) sweep4(ctx context.Context, sum Summary) (Summary, error) {
	if err := ctx.Err(); err != nil {
		return sum, err
	}
	installations, err := g.repos.ListInstallations(ctx)
	if err != nil {
		return finishSweep4(sum, err)
	}
	slices.SortFunc(installations, func(a, b api.Installation) int { return cmp.Compare(a.ID, b.ID) })
	if len(installations) == 0 {
		return sum, nil
	}
	n := int64(len(installations))
	start := int((g.now().Unix()/3600%n + n) % n)
	for i := range installations {
		inst := installations[(start+i)%len(installations)]
		ok, err := g.admissions.Admitted(ctx, inst.ID, inst.Account)
		if err != nil {
			if stop := sweep4Error(ctx, &sum, err); stop != nil {
				return finishSweep4(sum, stop)
			}
			continue
		}
		if !ok {
			continue
		}
		repos, err := g.repos.ListRepos(ctx, inst.ID)
		if err != nil {
			if stop := sweep4Error(ctx, &sum, err); stop != nil {
				return finishSweep4(sum, stop)
			}
			continue
		}
		for _, r := range repos {
			if err := g.sweepRepo4(ctx, r, &sum); err != nil {
				if stop := sweep4Error(ctx, &sum, err); stop != nil {
					return finishSweep4(sum, stop)
				}
			}
		}
	}
	return sum, nil
}

func finishSweep4(sum Summary, err error) (Summary, error) {
	if errors.Is(err, api.ErrRateLimited) {
		sum.RateLimited = true
		return sum, nil
	}
	return sum, err
}

func sweep4Error(ctx context.Context, sum *Summary, err error) error {
	if ctx.Err() != nil {
		return ctx.Err()
	}
	if errors.Is(err, api.ErrRateLimited) {
		return err
	}
	if errors.Is(err, feedback.ErrStaleInputs) {
		return nil
	}
	sum.Errors++
	slog.WarnContext(ctx, "sweep: v4 unit skipped")
	return nil
}

func (g *Gate) sweepRepo4(ctx context.Context, r api.Repo, sum *Summary) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	tip, err := g.repos.BranchHead(ctx, r.RepoRef, r.DefaultBranch)
	if err != nil {
		return err
	}
	raw, _, err := g.repos.File(ctx, r.RepoRef, tip, policyPath)
	if err != nil && !errors.Is(err, api.ErrNotFound) {
		return err
	}
	state, err := g.v4.repoState(ctx, r)
	if err != nil {
		return err
	}
	pol, decodeErr := policy3.Decode(raw)
	if decodeErr != nil && state.ApprovedDigest == "" {
		return nil
	}
	sum.Repos++
	out, err := g.approvals.Reconcile(ctx, r, tip)
	if err != nil {
		if stop := sweep4Error(ctx, sum, err); stop != nil {
			return stop
		}
	}
	if out.Judged == approvals.JudgedConsumed {
		err := sweep.AfterAnswer(ctx, g.recheckGH, g.store, r, sweep.Consumed{Proposal: out.Proposal, Requests: out.Requests, Pulls: out.Waiting}, g.now())
		if err == nil {
			err = records.SweepRechecked(ctx, g.store, store.Tenant(r.InstallationID), r.FullName, out.Proposal)
		}
		if err != nil {
			if stop := sweep4Error(ctx, sum, err); stop != nil {
				return stop
			}
		}
	}
	state, err = g.v4.repoState(ctx, r)
	if err != nil {
		return err
	}
	ambiguous := out.Judged == approvals.JudgedAmbiguous
	if ambiguous {
		slog.WarnContext(ctx, "sweep: ambiguous policy range", "installation_id", r.InstallationID, "repo_id", r.ID,
			"before", state.ApprovedCommit, "after", tip)
	}
	a, err := g.expiry.Approved(ctx, r, tip)
	if err != nil {
		return err
	}
	if a != nil {
		if err := g.expiry.Warn(ctx, r, a, g.now()); err != nil {
			if stop := sweep4Error(ctx, sum, err); stop != nil {
				return stop
			}
		}
	}
	open, err := g.pulls.OpenPulls(ctx, r.RepoRef)
	if err != nil {
		return err
	}
	if err := g.cleanPullIndex4(ctx, r, open); err != nil {
		return err
	}
	for _, pl := range open {
		if pl.BaseRef != r.DefaultBranch {
			continue
		}
		own, err := proposals.IsProposalPull(ctx, g.store, r, pl.Number)
		if err != nil {
			return err
		}
		if own {
			continue
		} // the App alone owns a policy proposal's verdict
		sum.Pulls++
		if err := g.sweepPull4(ctx, r, pl, tip, state, pol.Gate != gateRequired, ambiguous, a, sum); err != nil {
			if stop := sweep4Error(ctx, sum, err); stop != nil {
				return stop
			}
		}
	}
	return nil
}

// cleanPullIndex4 confirms absent entries against GitHub rather than treating a failed or changing listing as closure.
func (g *Gate) cleanPullIndex4(ctx context.Context, r api.Repo, open []api.Pull) error {
	index := records.Index{Schema: records.SchemaPullIndex}
	err := records.Load(ctx, g.store, store.Tenant(r.InstallationID), records.PullIndexKey(r.FullName), &index)
	if errors.Is(err, records.ErrNoRecord) {
		return nil
	}
	if err != nil {
		return err
	}
	for _, entry := range index.Entries {
		n, err := strconv.Atoi(entry.Key)
		if err != nil {
			return fmt.Errorf("gate: invalid pull index entry")
		}
		if slices.ContainsFunc(open, func(pl api.Pull) bool { return pl.Number == n }) {
			continue
		}
		pl, err := g.pulls.Pull(ctx, r.RepoRef, n)
		if err != nil && !errors.Is(err, api.ErrNotFound) {
			return err
		}
		if errors.Is(err, api.ErrNotFound) || pl.State != "open" {
			if err := asks.PullClosed(ctx, g.store, r, n); err != nil {
				return err
			}
		}
	}
	return nil
}

func (g *Gate) sweepPull4(ctx context.Context, r api.Repo, pl api.Pull, tip string, state records.Repo, advisory, ambiguous bool, a *expiry.Approved, sum *Summary) error {
	var rec records.Pull
	err := records.Load(ctx, g.store, store.Tenant(r.InstallationID), records.PullKey(r.FullName, pl.Number), &rec)
	if err != nil && !errors.Is(err, records.ErrNoRecord) {
		return err
	}
	if rec.HeadSHA != pl.HeadSHA {
		rec = records.Pull{}
	}
	current := g.currentSweep4(r, pl, tip, state, rec)
	pub := feedback.Publication4{Target: inputs.Target{Repo: r, PullRequest: pl.Number, HeadSHA: pl.HeadSHA, BaseRef: r.DefaultBranch},
		RunID: rec.RunID, RunAttempt: rec.RunAttempt, Digest: rec.PayloadDigest, Advisory: advisory, Current: current}
	var reasons []reason4
	add := func(code string, u result.Unknown) { reasons = append(reasons, reason4{Code: code, Record: u}) }
	checkState(state, r, add)
	if ambiguous || a == nil && len(reasons) == 0 {
		add(reasonPolicyUnapproved, result.Unknown{Path: policyPath, Cause: "policy", What: "the policy range " + state.ApprovedCommit + ".." + tip + " cannot be reconciled safely",
			Why:  "the history or proposal does not prove a single approved policy change",
			Next: result.Next{Kind: "wait", Text: "an owner reconciles the policy through the approval flow"}})
	}
	if len(reasons) > 0 {
		if ambiguous && rec.RunID != 0 {
			if err := g.sweepNote4(ctx, r, pl.Number, rec, state.ApprovedCommit+".."+tip); err != nil {
				return err
			}
		}
		pub.Result = (decision4{Reasons: reasons}).shown(nil)
		if rec.RunID == 0 {
			return g.v4.publisher.Withdraw4(ctx, pub)
		}
		return g.v4.publisher.Publish4(ctx, pub)
	}
	accepted := rec.PolicyDigest != "" && slices.Contains([]string{"pass", "fail", "waiting"}, rec.Status)
	running := rec.Reason == reasonPolicyChanged && slices.ContainsFunc(rec.Rechecks, func(rc records.Recheck) bool {
		return rc.HeadSHA == pl.HeadSHA && rc.Path == sweep.PathRunning
	})
	stale := accepted && rec.PolicyDigest != state.ApprovedDigest || running
	var due []expiry.Exception
	if accepted && a != nil {
		due = expiry.Due(a.Exceptions, rec, g.now())
	}
	if stale || len(due) > 0 {
		what, next := "an exception passed its date since this run", "wait for the check running with the current date"
		if stale {
			what, next = "the policy changed since this run", "wait for the check running with the current policy"
		}
		pub.Result = unknownSweep4(what, next)
		if err := g.v4.publisher.Publish4(ctx, pub); err != nil {
			return err
		}
	}
	// A failed recovered handoff remains owed; this pass withdraws stale authority but does not retry the same read.
	if state.SweepRecheck != "" && (stale || len(due) > 0) {
		return nil
	}
	missing, err := g.missingUpload4(ctx, r, pl, rec, pub, sum)
	if err != nil || missing || rec.RunID == 0 {
		return err
	}
	if stale {
		res, err := g.rerun4(ctx, r, pl, tip, rec, accepted || running)
		if err != nil {
			return err
		}
		if res == sweep.RerunRequested {
			sum.Rerun++
		}
		if res != sweep.RerunImpossible {
			return nil
		}
		pub.Result = unknownSweep4("the policy changed since this run", "update the branch so its check reads the current policy")
		return g.v4.publisher.Publish4(ctx, pub)
	}
	if len(due) > 0 {
		res, err := g.rerun4(ctx, r, pl, tip, rec, true)
		if err != nil {
			return err
		}
		if res == sweep.RerunRequested {
			sum.Rerun++
		}
		if res != sweep.RerunImpossible {
			return nil
		}
		return g.expiry.Fail(ctx, g.v4.publisher, expiry.Failure{Repo: r, Pull: pl.Number, Tip: tip, Record: rec, Expired: due, Advisory: advisory, Current: current})
	}
	if rec.Status != "waiting" || !accepted {
		return nil
	}
	checks, err := g.checks.CheckRuns(ctx, r.RepoRef, pl.HeadSHA, gateCheck4)
	if err != nil {
		return err
	}
	var latest api.CheckRun
	for _, c := range checks {
		if c.ID > latest.ID {
			latest = c
		}
	}
	if latest.ID == 0 || latest.Status != "in_progress" || latest.StartedAt.IsZero() || g.now().Sub(latest.StartedAt) < waitingRefreshAfter {
		return nil
	}
	res, err := g.waitingResult4(ctx, r, pl, rec)
	if err != nil {
		if errors.Is(err, errSweepArtifact) {
			pub.Result = unknownSweep4("the waiting result cannot be read back", "run the Archfit check again")
			return g.v4.publisher.Publish4(ctx, pub)
		}
		return err
	}
	pub.Result, pub.Fresh = res, true
	pub.Current = func(ctx context.Context) (bool, error) {
		ok, err := current(ctx)
		if err != nil || !ok {
			return ok, err
		}
		cs, err := g.checks.CheckRuns(ctx, r.RepoRef, pl.HeadSHA, gateCheck4)
		if err != nil {
			return false, err
		}
		for _, c := range cs {
			if c.ID > latest.ID || c.ID == latest.ID && c.Status != "in_progress" {
				return false, nil
			}
		}
		// Requests may have been answered while this pass waited for the lease.
		again, err := g.waitingResult4(ctx, r, pl, rec)
		return result.Record(again) == result.Record(res), err
	}
	return g.v4.publisher.Publish4(ctx, pub)
}

func (g *Gate) rerun4(ctx context.Context, r api.Repo, pl api.Pull, tip string, rec records.Pull, readsTip bool) (sweep.RerunResult, error) {
	if pl.HeadRepo != r.FullName {
		return sweep.RerunImpossible, nil
	}
	res, _, err := sweep.Rerun(ctx, g.recheckGH, r.RepoRef, sweep.RerunInput{Receipt: feedback.Receipt{RunID: rec.RunID, RunAttempt: rec.RunAttempt},
		Head: pl.HeadSHA, EventBase: pl.BaseSHA, Tip: tip, ReadsTip: readsTip}, g.now())
	return res, err
}

var errSweepArtifact = errors.New("gate: no matching result artifact")

func (g *Gate) waitingResult4(ctx context.Context, r api.Repo, pl api.Pull, rec records.Pull) (result.Result, error) {
	raw, err := g.recheckGH.Artifact(ctx, r.RepoRef, rec.RunID, asks.ArtifactName, maxSweepResultBytes)
	if errors.Is(err, api.ErrNotFound) || errors.Is(err, api.ErrArtifactInvalid) {
		return result.Result{}, errSweepArtifact
	}
	if err != nil {
		return result.Result{}, err
	}
	if envelope.PayloadDigest(raw) != rec.PayloadDigest {
		return result.Result{}, errSweepArtifact
	}
	res, err := result.Decode(raw)
	if err != nil || res.Head != pl.HeadSHA || res.Policy.Digest != rec.PolicyDigest || res.Status != result.StatusFail && res.Status != result.StatusWaiting {
		return result.Result{}, errSweepArtifact
	}
	index, err := asks.OpenIndex(ctx, g.store, r)
	if err != nil {
		return result.Result{}, err
	}
	shown, ids := asks.Cover(res, index)
	if err := asks.Wait(ctx, g.store, r, pl.Number, ids); err != nil {
		return result.Result{}, err
	}
	return shown, nil
}

func unknownSweep4(what, next string) result.Result {
	return result.Result{Status: result.StatusUnknown, Summary: summaryUnknown, Unknown: []result.Unknown{{
		Cause: "tool", What: what, Next: result.Next{Kind: "fix", Text: next},
	}}}
}

// currentSweep4 checks all authority inside the head lease: live pull, tip, approved state and the uploaded record.
func (g *Gate) currentSweep4(r api.Repo, pl api.Pull, tip string, state records.Repo, rec records.Pull) func(context.Context) (bool, error) {
	return func(ctx context.Context) (bool, error) {
		head, err := g.repos.BranchHead(ctx, r.RepoRef, r.DefaultBranch)
		if err != nil || head != tip {
			return false, err
		}
		live, err := g.pulls.Pull(ctx, r.RepoRef, pl.Number)
		if err != nil {
			return false, err
		}
		if live.State != "open" || live.HeadSHA != pl.HeadSHA || live.BaseRef != r.DefaultBranch {
			return false, nil
		}
		now, err := g.v4.repoState(ctx, r)
		if err != nil {
			return false, err
		}
		if now.ApprovedDigest != state.ApprovedDigest || (now.OutsideFlow == nil) != (state.OutsideFlow == nil) {
			return false, nil
		}
		if now.OutsideFlow != nil && *now.OutsideFlow != *state.OutsideFlow {
			return false, nil
		}
		var pull records.Pull
		err = records.Load(ctx, g.store, store.Tenant(r.InstallationID), records.PullKey(r.FullName, pl.Number), &pull)
		if errors.Is(err, records.ErrNoRecord) {
			return rec.RunID == 0, nil
		}
		if err != nil {
			return false, err
		}
		if rec.RunID == 0 {
			return pull.HeadSHA != pl.HeadSHA, nil
		}
		return pull.HeadSHA == rec.HeadSHA && pull.RunID == rec.RunID && pull.RunAttempt == rec.RunAttempt &&
			pull.PayloadDigest == rec.PayloadDigest && pull.Status == rec.Status && pull.PolicyDigest == rec.PolicyDigest && pull.Reason == rec.Reason, nil
	}
}

func (g *Gate) sweepNote4(ctx context.Context, r api.Repo, n int, rec records.Pull, note string) error {
	return store.Update(ctx, g.store, store.Tenant(r.InstallationID), []string{records.PullKey(r.FullName, n)}, func(cur [][]byte) ([][]byte, error) {
		if cur[0] == nil {
			return cur, nil
		}
		var now records.Pull
		if err := records.Decode(cur[0], &now); err != nil {
			return nil, err
		}
		if now.RunID != rec.RunID || now.RunAttempt != rec.RunAttempt || now.HeadSHA != rec.HeadSHA || now.SweepNote == note {
			return cur, nil
		}
		now.SweepNote = note
		raw, err := records.Encode(&now)
		return [][]byte{raw}, err
	})
}

func (g *Gate) missingUpload4(ctx context.Context, r api.Repo, pl api.Pull, rec records.Pull, pub feedback.Publication4, sum *Summary) (bool, error) {
	runs, err := g.recheckGH.HeadRuns(ctx, r.RepoRef, ingest.WorkflowPath, pl.HeadSHA)
	if err != nil {
		return false, err
	}
	var latest api.WorkflowRun
	for _, run := range runs {
		if run.Event != envelope.EventPullRequest || !slices.Contains(run.PullRequests, pl.Number) {
			continue
		}
		if latest.ID == 0 || run.CreatedAt.After(latest.CreatedAt) || run.CreatedAt.Equal(latest.CreatedAt) && run.ID > latest.ID {
			latest = run
		}
	}
	if rec.RunID != 0 && (latest.ID < rec.RunID || latest.ID == rec.RunID && latest.Attempt <= rec.RunAttempt) {
		return false, nil
	}
	since := pl.UpdatedAt
	if latest.ID != 0 {
		if latest.Status != "completed" {
			return false, nil
		}
		since = latest.UpdatedAt
		pub.RunID, pub.RunAttempt = latest.ID, latest.Attempt
	}
	if since.IsZero() || g.now().Sub(since) <= missingUpload4After {
		return false, nil
	}
	pub.Result = unknownSweep4("the check finished without an uploaded result", "run the Archfit check again")
	if pub.RunID == 0 {
		err = g.v4.publisher.Withdraw4(ctx, pub)
	} else {
		err = g.v4.publisher.Publish4(ctx, pub)
	}
	if err != nil {
		return false, err
	}
	sum.TimedOut++
	return true, nil
}

package decisions

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strconv"
	"strings"
	"time"

	"github.com/alexei-led/archfit-app/internal/gate/inputs"
	"github.com/alexei-led/archfit-app/internal/github/api"
	"github.com/alexei-led/archfit-app/internal/store"
	"github.com/alexei-led/archfit-app/internal/wire/decision"
	"github.com/alexei-led/archfit-app/internal/wire/policy"
)

const (
	recordAttempts = 5
	// maxValueBytes is the Store's value limit (store/module.md).
	maxValueBytes = 64 << 10
)

// pending lists the requests the rules walk, in filing order: the pending ones, and the ones that left pending but
// whose new status is not yet in the thread. The Store cannot list keys, so this list is how the stale rules and the
// sweep find records. An entry leaves it when its record is neither pending nor unpublished; only Open entries count
// toward the caps.
type pending struct {
	Entries []entry `json:"pending"`
}

type entry struct {
	PullRequest  int    `json:"pr"`
	FindingID    string `json:"finding_id"`
	PolicyDigest string `json:"policy_digest"`
	Open         bool   `json:"open"` // the record is pending
}

func (e entry) same(o entry) bool {
	return e.PullRequest == o.PullRequest && e.FindingID == o.FindingID && e.PolicyDigest == o.PolicyDigest
}

func (p pending) openInPull(pull int) (n int) {
	for _, e := range p.Entries {
		if e.Open && e.PullRequest == pull {
			n++
		}
	}
	return n
}

func (p pending) open() (n int) {
	for _, e := range p.Entries {
		if e.Open {
			n++
		}
	}
	return n
}

func (p pending) index(e entry) int {
	for i, x := range p.Entries {
		if x.same(e) {
			return i
		}
	}
	return -1
}

// with returns p with e set (added, or replaced in place).
func (p pending) with(e entry) pending {
	out := pending{Entries: append([]entry(nil), p.Entries...)}
	if i := out.index(e); i >= 0 {
		out.Entries[i] = e
	} else {
		out.Entries = append(out.Entries, e)
	}
	return out
}

func (p pending) without(e entry) pending {
	out := pending{}
	for _, x := range p.Entries {
		if !x.same(e) {
			out.Entries = append(out.Entries, x)
		}
	}
	return out
}

// Keys, under the installation tenant. Repository names compare case-insensitively, as in gate/inputs.
func repoPrefix(t inputs.Target) string { return "decision/" + strings.ToLower(t.Repo.FullName) + "/" }
func pendingKey(t inputs.Target) string { return repoPrefix(t) + "pending" }
func recordKey(t inputs.Target, pull int, findingID, digest string) string {
	return repoPrefix(t) + strconv.Itoa(pull) + "/" + findingID + "/" + digest
}
func policyKey(recKey string) string { return recKey + "/policy" }

func tenant(t inputs.Target) store.Tenant { return store.Tenant(t.Repo.InstallationID) }

// load returns the bytes at key, nil when there are none.
func (r *Requests) load(ctx context.Context, t inputs.Target, key string) ([]byte, error) {
	raw, err := r.store.Get(ctx, tenant(t), key)
	if errors.Is(err, store.ErrNotFound) {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("decisions: read: %w", err)
	}
	return raw, nil
}

func marshal(v any) ([]byte, error) {
	var b bytes.Buffer
	enc := json.NewEncoder(&b)
	enc.SetEscapeHTML(false)
	if err := enc.Encode(v); err != nil {
		return nil, err
	}
	raw := bytes.TrimSuffix(b.Bytes(), []byte("\n"))
	if len(raw) > maxValueBytes {
		return nil, ErrTooLarge
	}
	return raw, nil
}

func parseRecord(raw []byte) (*Record, error) {
	if raw == nil {
		return nil, nil
	}
	var rec Record
	if err := json.Unmarshal(raw, &rec); err != nil {
		return nil, errors.New("decisions: unreadable record")
	}
	return &rec, nil
}

func parsePending(raw []byte) (pending, error) {
	var p pending
	if raw == nil {
		return p, nil
	}
	if err := json.Unmarshal(raw, &p); err != nil {
		return pending{}, errors.New("decisions: unreadable pending list")
	}
	return p, nil
}

// Get returns the record of a request, or ErrNoRecord.
func (r *Requests) Get(ctx context.Context, t inputs.Target, findingID, digest string) (Record, error) {
	raw, err := r.load(ctx, t, recordKey(t, t.PullRequest, findingID, digest))
	if err != nil {
		return Record{}, err
	}
	rec, err := parseRecord(raw)
	if err != nil {
		return Record{}, err
	}
	if rec == nil {
		return Record{}, ErrNoRecord
	}
	return *rec, nil
}

// Policy returns the proposed policy text of a request, or ErrNoRecord.
func (r *Requests) Policy(ctx context.Context, t inputs.Target, findingID, digest string) (string, error) {
	raw, err := r.load(ctx, t, policyKey(recordKey(t, t.PullRequest, findingID, digest)))
	if err != nil {
		return "", err
	}
	if raw == nil {
		return "", ErrNoRecord
	}
	return string(raw), nil
}

// File stores a request that the caller has checked against GitHub and the protected ref. The same request on the
// same head writes nothing. A new head replaces the record and makes it pending again, unless the owners declined or
// approved it: that stays until the policy digest changes, which is a new key.
func (r *Requests) File(ctx context.Context, t inputs.Target, f Filing, now time.Time) (Outcome, error) {
	req := f.Request
	recKey, pendKey := recordKey(t, t.PullRequest, req.FindingID, req.PolicyDigest), pendingKey(t)
	polKey := policyKey(recKey)
	for range recordAttempts {
		oldRec, err := r.load(ctx, t, recKey)
		if err != nil {
			return "", err
		}
		oldPol, err := r.load(ctx, t, polKey)
		if err != nil {
			return "", err
		}
		oldPend, err := r.load(ctx, t, pendKey)
		if err != nil {
			return "", err
		}
		old, err := parseRecord(oldRec)
		if err != nil {
			return "", err
		}
		list, err := parsePending(oldPend)
		if err != nil {
			return "", err
		}
		wasPending := false
		created := now
		var commentID int64
		if old != nil {
			switch {
			case old.Status == decision.StatusDeclined || old.Status == decision.StatusApproved:
				return Sticky, nil
			case old.Status == decision.StatusPending && old.HeadSHA == req.HeadSHA:
				if old.Published != lineOf(*old) {
					return Duplicate, r.publish(ctx, t, old.FindingID, old.PolicyDigest)
				}
				return Duplicate, nil
			}
			wasPending, created, commentID = old.Status == decision.StatusPending, old.CreatedAt, old.StatusCommentID
		}
		if !wasPending {
			if list.openInPull(t.PullRequest) >= MaxPendingPerPull || list.open() >= MaxPendingPerRepo {
				return Capped, nil
			}
			list = list.with(entry{t.PullRequest, req.FindingID, req.PolicyDigest, true})
		}
		rec := Record{
			PullRequest: t.PullRequest, FindingID: req.FindingID, RuleID: req.RuleID,
			EdgeFrom: req.Edge.From, EdgeTo: req.Edge.To, HeadSHA: req.HeadSHA, PolicyDigest: req.PolicyDigest,
			Why: req.Why, AlternativesTried: req.AlternativesTried, ProposalDigest: policy.Digest([]byte(req.Proposal.Policy)),
			Requester: f.Requester, CommentID: f.CommentID, Status: decision.StatusPending,
			StatusCommentID: commentID, CreatedAt: created, UpdatedAt: now,
		}
		newRec, err := marshal(rec)
		if err != nil {
			return "", err
		}
		newPend, err := marshal(list)
		if err != nil {
			return "", err
		}
		swaps := []store.Swap{{Key: recKey, Old: oldRec, Value: newRec}, {Key: polKey, Old: oldPol, Value: []byte(req.Proposal.Policy)}}
		if !wasPending {
			swaps = append(swaps, store.Swap{Key: pendKey, Old: oldPend, Value: newPend})
		}
		swapped, err := r.store.CompareAndSwapAll(ctx, tenant(t), swaps)
		if err != nil {
			return "", fmt.Errorf("decisions: write request: %w", err)
		}
		if !swapped {
			continue
		}
		outcome := Filed
		if old != nil {
			outcome = Refiled
		}
		return outcome, r.publish(ctx, t, rec.FindingID, rec.PolicyDigest)
	}
	return "", errContention
}

// Settle moves a pending request to status, with the reason shown beside it, and updates its status comment. The
// owner's decision will call it. A request that is not pending never moves.
func (r *Requests) Settle(ctx context.Context, t inputs.Target, findingID, digest string, status decision.Status, reason string, proposalPR int, now time.Time) error {
	return r.SettleAt(ctx, t, findingID, digest, "", status, reason, proposalPR, now)
}

// SettleAt is Settle for a caller that decided on a record it read: it moves the record only while its head is still
// expectedHead (else ErrChanged), in the same transaction that checks it is pending. "" accepts any head.
//
// The request stays in the pending list, no longer open, until its new status is in the thread. A failed comment write
// is returned and leaves the entry: the stale rules, the events and the sweep write the comment later.
func (r *Requests) SettleAt(ctx context.Context, t inputs.Target, findingID, digest, expectedHead string, status decision.Status, reason string, proposalPR int, now time.Time) error {
	recKey, pendKey := recordKey(t, t.PullRequest, findingID, digest), pendingKey(t)
	for range recordAttempts {
		oldRec, err := r.load(ctx, t, recKey)
		if err != nil {
			return err
		}
		oldPend, err := r.load(ctx, t, pendKey)
		if err != nil {
			return err
		}
		rec, err := parseRecord(oldRec)
		if err != nil {
			return err
		}
		list, err := parsePending(oldPend)
		if err != nil {
			return err
		}
		switch {
		case rec == nil:
			return ErrNoRecord
		case rec.Status != decision.StatusPending:
			return ErrNotPending
		case expectedHead != "" && rec.HeadSHA != expectedHead:
			return ErrChanged
		}
		rec.Status, rec.Reason, rec.ProposalPR, rec.UpdatedAt = status, reason, proposalPR, now
		newRec, err := marshal(rec)
		if err != nil {
			return err
		}
		newPend, err := marshal(list.with(entry{t.PullRequest, findingID, digest, false}))
		if err != nil {
			return err
		}
		swapped, err := r.store.CompareAndSwapAll(ctx, tenant(t), []store.Swap{
			{Key: recKey, Old: oldRec, Value: newRec}, {Key: pendKey, Old: oldPend, Value: newPend},
		})
		if err != nil {
			return fmt.Errorf("decisions: write request: %w", err)
		}
		if swapped {
			return r.publish(ctx, t, findingID, digest)
		}
	}
	return errContention
}

// HeadMoved marks the pending requests of t's pull request stale (head moved) when the pull request is open and its
// head, read now, is not the request's. The webhook's head is history: a late delivery must not stale a request that
// already names the live head. t.HeadSHA is not used.
func (r *Requests) HeadMoved(ctx context.Context, t inputs.Target, now time.Time) error {
	return r.walkLive(ctx, t, func(rec Record, live api.Pull) (bool, error) {
		if live.State != "open" || rec.HeadSHA == live.HeadSHA {
			return false, nil
		}
		return true, r.SettleAt(ctx, t, rec.FindingID, rec.PolicyDigest, rec.HeadSHA, decision.StatusStale, decision.ReasonHeadMoved, 0, now)
	})
}

// Closed marks the pending requests of t's pull request stale (pull request closed) unless the pull request is open
// now: a delayed close delivery after a reopen must not stale a new request.
func (r *Requests) Closed(ctx context.Context, t inputs.Target, now time.Time) error {
	return r.walkLive(ctx, t, func(rec Record, live api.Pull) (bool, error) {
		if live.State == "open" {
			return false, nil
		}
		return true, r.SettleAt(ctx, t, rec.FindingID, rec.PolicyDigest, rec.HeadSHA, decision.StatusStale, decision.ReasonPullClosed, 0, now)
	})
}

// walkLive is walk with the pull request as GitHub has it now, read once and only when there is something to walk.
// A pull request GitHub no longer knows is closed.
func (r *Requests) walkLive(ctx context.Context, t inputs.Target, rule func(Record, api.Pull) (bool, error)) error {
	var live api.Pull
	read := false
	return r.walk(ctx, t, func(rec Record) (bool, error) {
		if !read {
			pl, err := r.pulls.Pull(ctx, t.Repo.RepoRef, t.PullRequest)
			if errors.Is(err, api.ErrNotFound) {
				pl, err = api.Pull{State: "closed"}, nil
			}
			if err != nil {
				return false, fmt.Errorf("decisions: pull request: %w", err)
			}
			live, read = pl, true
		}
		return rule(rec, live)
	})
}

// walk visits every request of t's pull request in the pending list. A pending one goes to rule, which reports
// whether it settled it. Every other one, and a pending one the rule left alone, gets its status comment written when
// the thread is not up to date: this is where a failed write is repaired. A failure does not stop the others; they are
// joined. A record that moved meanwhile is skipped.
func (r *Requests) walk(ctx context.Context, t inputs.Target, rule func(Record) (bool, error)) error {
	raw, err := r.load(ctx, t, pendingKey(t))
	if err != nil {
		return err
	}
	list, err := parsePending(raw)
	if err != nil {
		return err
	}
	var errs []error
	for _, e := range list.Entries {
		if e.PullRequest != t.PullRequest {
			continue
		}
		rec, err := r.Get(ctx, t, e.FindingID, e.PolicyDigest)
		if errors.Is(err, ErrNoRecord) {
			continue
		}
		if err == nil && rec.Status == decision.StatusPending {
			var settled bool
			if settled, err = rule(rec); settled {
				if err != nil && !errors.Is(err, ErrNotPending) && !errors.Is(err, ErrChanged) {
					errs = append(errs, err)
				}
				continue
			}
		}
		if err == nil && (rec.Published != lineOf(rec) || rec.Status != decision.StatusPending) {
			err = r.publish(ctx, t, rec.FindingID, rec.PolicyDigest)
		}
		if err != nil {
			errs = append(errs, err)
		}
	}
	return errors.Join(errs...)
}

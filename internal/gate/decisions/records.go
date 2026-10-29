package decisions

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"strconv"
	"strings"
	"time"

	"github.com/alexei-led/archfit-app/internal/gate/inputs"
	"github.com/alexei-led/archfit-app/internal/store"
	"github.com/alexei-led/archfit-app/internal/wire/decision"
	"github.com/alexei-led/archfit-app/internal/wire/policy"
)

const (
	recordAttempts = 5
	// maxValueBytes is the Store's value limit (store/module.md).
	maxValueBytes = 64 << 10
)

// pending lists the pending records of a repository, in filing order. Only pending records are listed: the Store
// cannot list keys, and every rule that walks records walks the pending ones.
type pending struct {
	Entries []entry `json:"pending"`
}

type entry struct {
	PullRequest  int    `json:"pr"`
	FindingID    string `json:"finding_id"`
	PolicyDigest string `json:"policy_digest"`
}

func (p pending) count(pull int) (inPull int) {
	for _, e := range p.Entries {
		if e.PullRequest == pull {
			inPull++
		}
	}
	return inPull
}

func (p pending) without(e entry) pending {
	out := pending{}
	for _, x := range p.Entries {
		if x != e {
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
				if old.StatusCommentID == 0 {
					return Duplicate, r.publish(ctx, t, *old)
				}
				return Duplicate, nil
			}
			wasPending, created, commentID = old.Status == decision.StatusPending, old.CreatedAt, old.StatusCommentID
		}
		e := entry{t.PullRequest, req.FindingID, req.PolicyDigest}
		if !wasPending {
			if list.count(t.PullRequest) >= MaxPendingPerPull || len(list.Entries) >= MaxPendingPerRepo {
				return Capped, nil
			}
			list.Entries = append(list.Entries, e)
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
		return outcome, r.publish(ctx, t, rec)
	}
	return "", errContention
}

// Settle moves a pending request to status, with the reason shown beside it, and updates its status comment. The
// stale rules call it; so will the owner's decision. A request that is not pending never moves.
func (r *Requests) Settle(ctx context.Context, t inputs.Target, findingID, digest string, status decision.Status, reason string, proposalPR int, now time.Time) error {
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
		}
		rec.Status, rec.Reason, rec.ProposalPR, rec.UpdatedAt = status, reason, proposalPR, now
		newRec, err := marshal(rec)
		if err != nil {
			return err
		}
		newPend, err := marshal(list.without(entry{t.PullRequest, findingID, digest}))
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
			return r.publish(ctx, t, *rec)
		}
	}
	return errContention
}

// HeadMoved marks the pending requests of t's pull request whose head is not t.HeadSHA stale.
func (r *Requests) HeadMoved(ctx context.Context, t inputs.Target, now time.Time) error {
	return r.eachPending(ctx, t, func(rec Record) error {
		if rec.HeadSHA == t.HeadSHA {
			return nil
		}
		return r.Settle(ctx, t, rec.FindingID, rec.PolicyDigest, decision.StatusStale, decision.ReasonHeadMoved, 0, now)
	})
}

// Closed marks every pending request of t's pull request stale.
func (r *Requests) Closed(ctx context.Context, t inputs.Target, now time.Time) error {
	return r.eachPending(ctx, t, func(rec Record) error {
		return r.Settle(ctx, t, rec.FindingID, rec.PolicyDigest, decision.StatusStale, decision.ReasonPullClosed, 0, now)
	})
}

// eachPending runs do on every pending record of t's pull request. A failure does not stop the others; they are
// joined. A record that settled meanwhile is skipped.
func (r *Requests) eachPending(ctx context.Context, t inputs.Target, do func(Record) error) error {
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
		if errors.Is(err, ErrNoRecord) || err == nil && rec.Status != decision.StatusPending {
			continue
		}
		if err == nil {
			err = do(rec)
		}
		if err != nil && !errors.Is(err, ErrNotPending) {
			errs = append(errs, err)
		}
	}
	return errors.Join(errs...)
}

// saveCommentID remembers the status comment of a record. It is a hint: the comment is found by its marker, so a lost
// write costs nothing but the next lookup.
func (r *Requests) saveCommentID(ctx context.Context, t inputs.Target, rec Record, id int64) {
	key := recordKey(t, rec.PullRequest, rec.FindingID, rec.PolicyDigest)
	old, err := r.load(ctx, t, key)
	cur, perr := parseRecord(old)
	if err != nil || perr != nil || cur == nil {
		return
	}
	cur.StatusCommentID = id
	raw, err := marshal(cur)
	if err == nil {
		_, err = r.store.CompareAndSwap(ctx, tenant(t), key, old, raw)
	}
	if err != nil {
		slog.Default().WarnContext(ctx, "decisions: status comment ID not saved", "installation_id", t.Repo.InstallationID, "pr", rec.PullRequest)
	}
}

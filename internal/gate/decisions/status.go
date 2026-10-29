package decisions

import (
	"context"
	"fmt"
	"log/slog"
	"strings"

	"github.com/alexei-led/archfit-app/internal/gate/inputs"
	"github.com/alexei-led/archfit-app/internal/store"
	"github.com/alexei-led/archfit-app/internal/wire/decision"
)

// statusDetail is the part of the status line in parentheses.
func statusDetail(rec Record) string {
	if rec.Status == decision.StatusApproved {
		return decision.ApprovedDetail(rec.ProposalPR)
	}
	return rec.Reason
}

// lineOf is the status line a record's stored status calls for. A comment is always derived from the stored record,
// never from what a caller meant to publish.
func lineOf(rec Record) string { return decision.StatusLine(rec.Status, statusDetail(rec)) }

// publish brings the status comment of a stored request up to date, under the pull request's status lease. It reads
// the record inside the lease, writes the comment its stored status calls for, and then records what it wrote
// (Published, StatusCommentID) in one transaction with the removal of the request from the pending list when it is no
// longer pending. A record that changed meanwhile is read again and written again.
func (r *Requests) publish(ctx context.Context, t inputs.Target, findingID, digest string) error {
	recKey, pendKey := recordKey(t, t.PullRequest, findingID, digest), pendingKey(t)
	err := r.hold(ctx, t, func(ctx context.Context) error {
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
			if err != nil || rec == nil {
				return err
			}
			list, err := parsePending(oldPend)
			if err != nil {
				return err
			}
			line := lineOf(*rec)
			if rec.Published != line {
				id, err := r.writeComment(ctx, t, decision.StatusMarker(findingID, digest), line)
				if err != nil {
					return fmt.Errorf("%w: %w", ErrStatusComment, err)
				}
				if id != 0 {
					rec.StatusCommentID = id
				}
				rec.Published = line
			}
			swaps, err := r.settledSwaps(recKey, pendKey, oldRec, oldPend, rec, list, entry{t.PullRequest, findingID, digest, false})
			if err != nil || len(swaps) == 0 {
				return err
			}
			swapped, err := r.store.CompareAndSwapAll(ctx, tenant(t), swaps)
			if err != nil {
				return fmt.Errorf("decisions: write request: %w", err)
			}
			if swapped {
				return nil
			}
		}
		return errContention
	})
	return err
}

// settledSwaps are the writes that record a publication: the record, when it changed, and the pending list without
// the request when it is no longer pending.
func (r *Requests) settledSwaps(recKey, pendKey string, oldRec, oldPend []byte, rec *Record, list pending, e entry) ([]store.Swap, error) {
	newRec, err := marshal(rec)
	if err != nil {
		return nil, err
	}
	var swaps []store.Swap
	if string(newRec) != string(oldRec) {
		swaps = append(swaps, store.Swap{Key: recKey, Old: oldRec, Value: newRec})
	}
	if rec.Status != decision.StatusPending && list.index(e) >= 0 {
		newPend, err := marshal(list.without(e))
		if err != nil {
			return nil, err
		}
		swaps = append(swaps, store.Swap{Key: pendKey, Old: oldPend, Value: newPend})
	}
	if len(swaps) == 1 && swaps[0].Key == pendKey {
		// Check the record too, so that a request refiled in between is not dropped from the list.
		swaps = append(swaps, store.Swap{Key: recKey, Old: oldRec, Value: oldRec})
	}
	return swaps, nil
}

// Answer writes a status comment for a request that failed a check, and reports whether it wrote one. Inside the
// status lease it checks again what is stored for the same IDs: a pending, approved or declined request is never
// overwritten, even one stored a moment ago. A stale request may be answered (the agent posted again and the new block
// is invalid): the record stays stale and records the line written, so that record and thread agree.
func (r *Requests) Answer(ctx context.Context, t inputs.Target, findingID, digest string, status decision.Status, reason string) (bool, error) {
	written := false
	err := r.hold(ctx, t, func(ctx context.Context) error {
		line := decision.StatusLine(status, reason)
		marker := decision.StatusMarker(findingID, digest)
		if findingID == decision.Unknown || digest == decision.Unknown {
			_, err := r.writeComment(ctx, t, marker, line)
			written = err == nil
			return wrapStatus(err)
		}
		recKey, pendKey := recordKey(t, t.PullRequest, findingID, digest), pendingKey(t)
		for range recordAttempts {
			oldRec, err := r.load(ctx, t, recKey)
			if err != nil {
				return err
			}
			rec, err := parseRecord(oldRec)
			if err != nil {
				return err
			}
			if rec != nil && rec.Status != decision.StatusStale {
				return nil
			}
			if _, err := r.writeComment(ctx, t, marker, line); err != nil {
				return wrapStatus(err)
			}
			written = true
			if rec == nil {
				return nil
			}
			oldPend, err := r.load(ctx, t, pendKey)
			if err != nil {
				return err
			}
			list, err := parsePending(oldPend)
			if err != nil {
				return err
			}
			rec.Published = line
			swaps, err := r.settledSwaps(recKey, pendKey, oldRec, oldPend, rec, list, entry{t.PullRequest, findingID, digest, false})
			if err != nil || len(swaps) == 0 {
				return err
			}
			swapped, err := r.store.CompareAndSwapAll(ctx, tenant(t), swaps)
			if err != nil {
				return fmt.Errorf("decisions: write request: %w", err)
			}
			if swapped {
				return nil
			}
		}
		return errContention
	})
	return written && err == nil, err
}

func wrapStatus(err error) error {
	if err != nil {
		return fmt.Errorf("%w: %w", ErrStatusComment, err)
	}
	return nil
}

// writeComment creates the App's comment that starts with marker, or edits it in place when its text differs. The
// caller holds the status lease, so the list and the write cannot interleave with another writer. It returns the
// comment's ID, 0 when the thread is full of status comments and none was written.
func (r *Requests) writeComment(ctx context.Context, t inputs.Target, marker, line string) (int64, error) {
	body := marker + "\n" + line + "\n" + ownerSentence
	all, err := r.comments.AppComments(ctx, t.Repo.RepoRef, t.PullRequest)
	if err != nil {
		return 0, err
	}
	var found struct {
		id   int64
		body string
	}
	statuses := 0
	for _, c := range all {
		if !strings.HasPrefix(c.Body, decision.StatusMarkerPrefix) {
			continue
		}
		statuses++
		if strings.HasPrefix(c.Body, marker+"\n") && (found.id == 0 || c.ID < found.id) {
			found.id, found.body = c.ID, c.Body
		}
	}
	switch {
	case found.id != 0 && found.body == body:
		return found.id, nil
	case found.id != 0:
		return found.id, inFlight(r.comments.UpdateComment(ctx, t.Repo.RepoRef, found.id, body))
	case statuses >= maxStatusComments:
		slog.Default().WarnContext(ctx, "decisions: status comment limit reached",
			"installation_id", t.Repo.InstallationID, "pr", t.PullRequest)
		return 0, nil
	}
	c, err := r.comments.CreateComment(ctx, t.Repo.RepoRef, t.PullRequest, body)
	return c.ID, inFlight(err)
}

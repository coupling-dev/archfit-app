package feedback

import (
	"context"
	"crypto/rand"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"reflect"
	"time"

	"github.com/alexei-led/archfit-app/internal/gate/inputs"
	"github.com/alexei-led/archfit-app/internal/store"
)

// A publication lease serializes every write to one head SHA across all instances: GitHub has no conditional
// check-run update, so read, supersedes check and write are only safe while no other publisher runs in between.
const (
	leasePrefix = "feedback/lease/"
	// leaseTTL is how long a lease stays held without release. A holder's work stops leaseMargin before that, by
	// its own elapsed time, so a takeover after expiry never meets a running holder; the margin absorbs clock skew
	// between instances and a request already in flight when the work deadline fires.
	leaseTTL    = 60 * time.Second
	leaseMargin = 10 * time.Second
	leasePoll   = 200 * time.Millisecond
	// releaseTimeout lets a holder whose request context ended still release its lease.
	releaseTimeout = 5 * time.Second
)

// errUnwritten marks a failure before the first GitHub request. Like success, it releases the lease at once.
var errUnwritten = errors.New("nothing written")

// lease is the stored value. Token tells holders apart, so a release never deletes a lease another holder took over.
type lease struct {
	Token   string    `json:"token"`
	Expires time.Time `json:"expires"`
}

// serialized is the Publisher New returns: every write method runs inside the head's lease. State only reads.
type serialized struct {
	next  *publisher
	store store.Store
	now   func() time.Time
	// wait pauses between polls of a held lease. Tests replace it to observe contention.
	wait func(context.Context) error
}

func (s *serialized) Publish(ctx context.Context, p Publication) error {
	return s.hold(ctx, p.Target, func(ctx context.Context) error {
		if p.Resolve != nil {
			fresh, err := p.Resolve(ctx)
			if err != nil {
				return fmt.Errorf("feedback: resolve inputs again: %w: %w", errUnwritten, err)
			}
			if !decidedFrom(p, fresh) {
				return ErrStaleInputs
			}
		}
		return s.next.Publish(ctx, p)
	})
}

// decidedFrom reports whether p was decided from the trusted inputs t read now. Every field of t must equal
// p.Trusted: the decision, the receipt and the rendering all read it, so a change of any field, digests or not (an
// owner approval dismissed, settings edited, a subscription suspended), makes p stale. Whole-value equality cannot fall
// behind a field that verdict.Decide starts to read. The digest checks are the sweep's stale_inputs condition, negated,
// so that no publication the sweep would at once call stale is ever written, whatever p.Trusted holds.
func decidedFrom(p Publication, t inputs.Trusted) bool {
	return reflect.DeepEqual(p.Trusted, t) &&
		inputs.Accepts(t.PolicyDigests, p.Receipt.PolicyDigest) &&
		inputs.Accepts(t.BaselineDigests, p.Receipt.BaselineDigest) &&
		inputs.Accepts(t.LabelsDigests, p.Receipt.LabelsDigest)
}

func (s *serialized) Pending(ctx context.Context, t inputs.Target) error {
	return s.hold(ctx, t, func(ctx context.Context) error { return s.next.Pending(ctx, t) })
}

func (s *serialized) State(ctx context.Context, t inputs.Target) (CheckState, error) {
	return s.next.State(ctx, t)
}

func (s *serialized) Warn(ctx context.Context, t inputs.Target, expiry time.Time) error {
	return s.hold(ctx, t, func(ctx context.Context) error { return s.next.Warn(ctx, t, expiry) })
}

// hold runs fn while holding the lease of t's head. Any Store error fails the call before GitHub is written.
func (s *serialized) hold(ctx context.Context, t inputs.Target, fn func(context.Context) error) error {
	tenant, key, err := leaseSlot(t)
	if err != nil {
		return err
	}
	mine, err := s.acquire(ctx, tenant, key)
	if err != nil {
		return fmt.Errorf("feedback: publication lease: %w", err)
	}
	work, cancel := context.WithTimeout(ctx, leaseTTL-leaseMargin)
	defer cancel()
	err = fn(work)
	// A failed call may have sent a write that GitHub still applies: a client timeout, a 5xx, or the work deadline.
	// The lease then stays until it expires, so that write lands inside the margin before a waiting holder reads.
	if err == nil || errors.Is(err, errUnwritten) {
		s.release(ctx, t, tenant, key, mine)
	}
	return err
}

// leaseSlot is the tenant and key of a head's lease: the installation and the head SHA. The repository is left out on
// purpose. Webhook targets carry no repository ID and a full name changes on rename, so a repository part could give
// two publishers of one head two leases. Without it, two repositories of one installation that share a commit (a fork
// in the same organization) also share a lease: they wait for each other, which costs time and never correctness.
func leaseSlot(t inputs.Target) (store.Tenant, string, error) {
	if t.Repo.InstallationID <= 0 || t.HeadSHA == "" {
		return 0, "", errors.New("feedback: publication lease: target without installation or head")
	}
	return store.Tenant(t.Repo.InstallationID), leasePrefix + t.HeadSHA, nil
}

// acquire takes the lease when it is missing, expired or unreadable, and otherwise waits for it. Every step is a
// compare-and-swap on the exact bytes read, so of two contenders only one wins.
func (s *serialized) acquire(ctx context.Context, tenant store.Tenant, key string) ([]byte, error) {
	token := rand.Text()
	for {
		cur, err := s.store.Get(ctx, tenant, key)
		switch {
		case errors.Is(err, store.ErrNotFound):
			cur = nil
		case err != nil:
			return nil, err
		case held(cur, s.now()):
			if err := s.wait(ctx); err != nil {
				return nil, err
			}
			continue
		}
		mine, err := json.Marshal(lease{Token: token, Expires: s.now().Add(leaseTTL)})
		if err != nil {
			return nil, err
		}
		ok, err := s.store.CompareAndSwap(ctx, tenant, key, cur, mine)
		if err != nil {
			return nil, err
		}
		if ok {
			return mine, nil
		}
	}
}

// held reports whether a stored lease is still in force. An unreadable value is not: only this package writes the
// key, and waiting on it forever would leave the head unpublishable.
func held(raw []byte, now time.Time) bool {
	var l lease
	return json.Unmarshal(raw, &l) == nil && now.Before(l.Expires)
}

// release deletes the lease only while it is still this holder's. It runs even when the request context has ended,
// after a call that succeeded or wrote nothing; a failed release is logged and the lease then expires on its own.
func (s *serialized) release(ctx context.Context, t inputs.Target, tenant store.Tenant, key string, mine []byte) {
	ctx, cancel := context.WithTimeout(context.WithoutCancel(ctx), releaseTimeout)
	defer cancel()
	ok, err := s.store.CompareAndSwap(ctx, tenant, key, mine, nil)
	if err != nil || !ok {
		slog.Default().WarnContext(ctx, "feedback lease not released",
			"repo_id", t.Repo.ID, "head_sha", t.HeadSHA, "taken_over", err == nil, "failed", err != nil)
	}
}

func pollWait(ctx context.Context) error {
	timer := time.NewTimer(leasePoll)
	defer timer.Stop()
	select {
	case <-ctx.Done():
		return ctx.Err()
	case <-timer.C:
		return nil
	}
}

package decisions

import (
	"context"
	"crypto/rand"
	"encoding/json"
	"errors"
	"log/slog"
	"strconv"
	"time"

	"github.com/alexei-led/archfit-app/internal/gate/inputs"
	"github.com/alexei-led/archfit-app/internal/github/api"
	"github.com/alexei-led/archfit-app/internal/store"
)

// A status lease serializes every write of status comments on one pull request across all instances: GitHub has no
// conditional comment write, so the list, the cap check and the create or edit are only safe while no other writer
// runs in between. It is the lease of gate/feedback, keyed by pull request instead of head.
const (
	leaseTTL       = 60 * time.Second
	leaseMargin    = 10 * time.Second
	leasePoll      = 200 * time.Millisecond
	releaseTimeout = 5 * time.Second
)

// errInFlight marks a failed comment write that GitHub may still apply (a timeout, a 5xx): the lease then stays
// until it expires, so that the write lands before another writer lists the thread.
var errInFlight = errors.New("comment write outcome unknown")

type lease struct {
	Token   string    `json:"token"`
	Expires time.Time `json:"expires"`
}

// leaseKey is the lease of a pull request's status comments, beside its records.
func leaseKey(t inputs.Target) string { return repoPrefix(t) + "lease/" + strconv.Itoa(t.PullRequest) }

// hold runs fn while holding the status lease of t's pull request.
func (r *Requests) hold(ctx context.Context, t inputs.Target, fn func(context.Context) error) error {
	ten, key := tenant(t), leaseKey(t)
	mine, err := r.acquire(ctx, ten, key)
	if err != nil {
		return err
	}
	work, cancel := context.WithTimeout(ctx, leaseTTL-leaseMargin)
	defer cancel()
	err = fn(work)
	if !errors.Is(err, errInFlight) {
		r.release(ctx, t, ten, key, mine)
	}
	return err
}

func (r *Requests) acquire(ctx context.Context, ten store.Tenant, key string) ([]byte, error) {
	token := rand.Text()
	for {
		cur, err := r.store.Get(ctx, ten, key)
		switch {
		case errors.Is(err, store.ErrNotFound):
			cur = nil
		case err != nil:
			return nil, err
		case held(cur, r.now()):
			if err := r.wait(ctx); err != nil {
				return nil, err
			}
			continue
		}
		mine, err := json.Marshal(lease{Token: token, Expires: r.now().Add(leaseTTL)})
		if err != nil {
			return nil, err
		}
		ok, err := r.store.CompareAndSwap(ctx, ten, key, cur, mine)
		if err != nil {
			return nil, err
		}
		if ok {
			return mine, nil
		}
	}
}

// held reports whether a stored lease is in force. An unreadable value is not: waiting on it forever would leave the
// pull request unwritable.
func held(raw []byte, now time.Time) bool {
	var l lease
	return json.Unmarshal(raw, &l) == nil && now.Before(l.Expires)
}

// release deletes the lease only while it is still this holder's.
func (r *Requests) release(ctx context.Context, t inputs.Target, ten store.Tenant, key string, mine []byte) {
	ctx, cancel := context.WithTimeout(context.WithoutCancel(ctx), releaseTimeout)
	defer cancel()
	ok, err := r.store.CompareAndSwap(ctx, ten, key, mine, nil)
	if err != nil || !ok {
		slog.Default().WarnContext(ctx, "decisions: status lease not released",
			"installation_id", t.Repo.InstallationID, "pr", t.PullRequest, "taken_over", err == nil)
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

// inFlight wraps a comment write error that may have been applied. A rate limit and a permanent refusal were not.
func inFlight(err error) error {
	if err == nil || errors.Is(err, api.ErrRateLimited) || api.Permanent(err) {
		return err
	}
	return errors.Join(err, errInFlight)
}

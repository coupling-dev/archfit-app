package proposals

import (
	"context"
	"crypto/rand"
	"encoding/json"
	"errors"
	"time"

	"github.com/alexei-led/archfit-app/internal/gate/records"
	"github.com/alexei-led/archfit-app/internal/github/api"
	"github.com/alexei-led/archfit-app/internal/store"
)

// One answer at a time writes a repository's proposal: computing the policy, writing the branch and pinning the
// proposal are three steps, and two answers that interleave them would pin a head that holds only one of them. The
// lease is a Store key; across App instances its swap is the lock.
const (
	leaseSchema = "archfit-app.proposal-lease.v1"
	leaseTTL    = 60 * time.Second
	// leaseWait is how long an answer waits for the lease another answer holds (see recordTimeout).
	leaseWait  = 10 * time.Second
	leasePoll  = 250 * time.Millisecond
	releaseTTL = 5 * time.Second
)

// ErrBusy is an answer that did not get the lease within leaseWait: another answer of the repository is being written.
var ErrBusy = errors.New("proposals: another answer of this repository is being written")

var errHeld = errors.New("proposals: the lease is held")

// lease is the value at records.ProposalLeaseKey. Token tells holders apart, so a release never deletes a lease
// another answer took over after it expired.
type lease struct {
	Schema  string    `json:"schema"`
	Token   string    `json:"token"`
	Expires time.Time `json:"expires"`
}

// Hold takes the same repository lease used by answer/check publication.
// Sibling lifecycle workers must hold it before retiring or voiding an edit.
// Read their proposal and target state only after acquisition.
func Hold(ctx context.Context, r api.Repo, s store.Store, now func() time.Time) (func(), error) {
	if now == nil {
		now = time.Now
	}
	return (&Service{store: s, now: now, sleep: sleepCtx}).hold(ctx, r)
}

// hold takes the repository's lease, waiting at most leaseWait, and returns its release. A value that cannot be read
// is no lease: it is replaced.
func (s *Service) hold(ctx context.Context, r api.Repo) (release func(), err error) {
	key, t, token := records.ProposalLeaseKey(r.FullName), tenant(r), rand.Text()
	wait, cancel := context.WithTimeout(ctx, leaseWait)
	defer cancel()
	for {
		err := store.Update(wait, s.store, t, []string{key}, func(cur [][]byte) ([][]byte, error) {
			var l lease
			if cur[0] != nil && json.Unmarshal(cur[0], &l) == nil && l.Schema == leaseSchema && s.now().Before(l.Expires) {
				return nil, errHeld
			}
			raw, err := json.Marshal(lease{Schema: leaseSchema, Token: token, Expires: s.now().Add(leaseTTL)})
			return [][]byte{raw}, err
		})
		switch {
		case err == nil:
			return func() { s.release(ctx, r, key, t, token) }, nil
		case !errors.Is(err, errHeld) && wait.Err() == nil:
			return nil, err
		}
		if s.sleep(wait, leasePoll) != nil {
			return nil, ErrBusy
		}
	}
}

// release deletes the lease when it is still this holder's. It runs even when the answer's context ended.
func (s *Service) release(ctx context.Context, r api.Repo, key string, t store.Tenant, token string) {
	ctx, cancel := context.WithTimeout(context.WithoutCancel(ctx), releaseTTL)
	defer cancel()
	err := store.Update(ctx, s.store, t, []string{key}, func(cur [][]byte) ([][]byte, error) {
		var l lease
		if cur[0] != nil && json.Unmarshal(cur[0], &l) == nil && l.Token == token {
			return [][]byte{nil}, nil
		}
		return cur, nil
	})
	if err != nil {
		logFailure(ctx, r, "release the proposal lease", err)
	}
}

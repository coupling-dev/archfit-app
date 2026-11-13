package records

import (
	"context"
	"errors"

	"github.com/alexei-led/archfit-app/internal/store"
)

// The repository's owner issues of M2 task 22: one drift issue, and one expiry issue per dated exception. Each issue
// is opened on GitHub first and recorded after, so a delivery that lost the race finds the recorded one and closes its
// own. A missing repository record is created: the issue belongs to the repository whatever its approval state.

// DriftOpened records issue as the repository's drift issue, describing head, when none is recorded, and returns the
// recorded issue: another delivery's when it was first.
func DriftOpened(ctx context.Context, s store.Store, t store.Tenant, fullName string, issue int, head string) (int, error) {
	if issue <= 0 {
		return 0, errors.New("records: drift issue: no issue")
	}
	recorded := 0
	err := editRepo(ctx, s, t, fullName, func(r *Repo) bool {
		if r.DriftIssue != 0 {
			recorded = r.DriftIssue
			return false
		}
		r.DriftIssue, r.DriftHead, recorded = issue, head, issue
		return true
	})
	return recorded, err
}

// DriftUpdated records head as the commit the drift issue describes, while issue is the recorded one.
func DriftUpdated(ctx context.Context, s store.Store, t store.Tenant, fullName string, issue int, head string) error {
	return editRepo(ctx, s, t, fullName, func(r *Repo) bool {
		if r.DriftIssue != issue || r.DriftHead == head {
			return false
		}
		r.DriftHead = head
		return true
	})
}

// DriftClosed forgets the drift issue while issue is the recorded one.
func DriftClosed(ctx context.Context, s store.Store, t store.Tenant, fullName string, issue int) error {
	return editRepo(ctx, s, t, fullName, func(r *Repo) bool {
		if r.DriftIssue != issue {
			return false
		}
		r.DriftIssue, r.DriftHead = 0, ""
		return true
	})
}

// ExpiryOpened records issue as the expiry issue of key when key has none, and returns the recorded issue: another
// delivery's when it was first.
func ExpiryOpened(ctx context.Context, s store.Store, t store.Tenant, fullName, key string, issue int) (int, error) {
	if issue <= 0 || key == "" {
		return 0, errors.New("records: expiry issue: no issue or no key")
	}
	recorded := 0
	err := editRepo(ctx, s, t, fullName, func(r *Repo) bool {
		if n := r.ExpiryIssues[key]; n != 0 {
			recorded = n
			return false
		}
		if r.ExpiryIssues == nil {
			r.ExpiryIssues = map[string]int{}
		}
		r.ExpiryIssues[key], recorded = issue, issue
		return true
	})
	return recorded, err
}

// ExpiryClosed forgets the expiry issue of key while issue is the recorded one.
func ExpiryClosed(ctx context.Context, s store.Store, t store.Tenant, fullName, key string, issue int) error {
	return editRepo(ctx, s, t, fullName, func(r *Repo) bool {
		if r.ExpiryIssues[key] != issue {
			return false
		}
		delete(r.ExpiryIssues, key)
		if len(r.ExpiryIssues) == 0 {
			r.ExpiryIssues = nil
		}
		return true
	})
}

// editRepo changes the repository record in one swap; a missing record starts empty. edit reports whether it changed
// anything: an unchanged record is only checked, never rewritten.
func editRepo(ctx context.Context, s store.Store, t store.Tenant, fullName string, edit func(*Repo) bool) error {
	return store.Update(ctx, s, t, []string{RepoKey(fullName)}, func(cur [][]byte) ([][]byte, error) {
		var r Repo
		if cur[0] != nil {
			if err := Decode(cur[0], &r); err != nil {
				return nil, err
			}
		}
		if !edit(&r) {
			return cur, nil
		}
		raw, err := Encode(&r)
		return [][]byte{raw}, err
	})
}

package requests

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"time"

	"github.com/coupling-dev/archfit-app/internal/gate/records"
	"github.com/coupling-dev/archfit-app/internal/github/api"
	"github.com/coupling-dev/archfit-app/internal/store"
	"github.com/coupling-dev/archfit-app/internal/wire/request"
	"github.com/coupling-dev/archfit-app/internal/wire/result"
)

// restoreTimeout bounds putting a held line back after its filing failed, even when the caller's deadline is gone.
const restoreTimeout = 5 * time.Second

// hold keeps a request line posted before the pull request's head had a checked result, and says it will be filed.
// Every other check already passed: the pull request, the endpoint, the author's permission and the line. A later
// early line replaces the held one. An upload of the head may have landed between the read and the hold, so the held
// line is released once at the end; claim stops a second filing when that upload releases it too.
func (s *Service) hold(ctx context.Context, c Comment, head string, line request.Line) (string, error) {
	h := records.HeldRequest{HeadSHA: head, Author: c.Author, Reason: line.Reason, Expires: line.Expires,
		Today: c.Today.UTC().Format(time.DateOnly), CreatedAt: asker{c.Author, c.CreatedAt}.at()}
	raw, err := records.Encode(&h)
	if err != nil {
		return ReactionNone, fmt.Errorf("requests: hold the request: %w", err)
	}
	key := records.HeldRequestKey(c.Repo.FullName, c.Pull)
	err = store.Update(ctx, s.store, tenant(c.Repo), []string{key}, func([][]byte) ([][]byte, error) {
		return [][]byte{raw}, nil
	})
	if err != nil {
		return ReactionNone, fmt.Errorf("requests: hold the request: %w", err)
	}
	if err := s.post(ctx, c.Repo.RepoRef, c.Pull, textHeld); err != nil {
		return ReactionHeld, fmt.Errorf("requests: reply: %w", err)
	}
	reaction, err := s.Release(ctx, c.Repo, c.Pull)
	if err != nil || reaction == ReactionNone {
		return ReactionHeld, err
	}
	return reaction, nil
}

// Release files the request line held for pull once a result for its head is in, as if it was posted then: the same
// checks and replies as a comment (take), with the App's date of the post. A pull request with a new head discards it
// with a reply, a pass discards it with a reply, and a closed pull request discards it without one. A head whose result
// has no verdict yet, or whose result has not landed, keeps it. It returns the reaction of the line it filed or
// discarded, ReactionNone when it did nothing. When filing fails the line is held again, so the next upload of the head
// files it.
func (s *Service) Release(ctx context.Context, r api.Repo, pull int) (string, error) {
	key := records.HeldRequestKey(r.FullName, pull)
	var h records.HeldRequest
	err := records.Load(ctx, s.store, tenant(r), key, &h)
	if errors.Is(err, records.ErrNoRecord) {
		return ReactionNone, nil
	}
	if err != nil {
		return ReactionNone, fmt.Errorf("requests: read the held request: %w", err)
	}
	pl, err := s.gh.Pull(ctx, r.RepoRef, pull)
	if err != nil && !errors.Is(err, api.ErrNotFound) {
		return ReactionNone, fmt.Errorf("requests: read the pull request: %w", err)
	}
	if err == nil && pl.State == stateOpen && pl.HeadSHA == h.HeadSHA {
		decided, err := s.decided(ctx, r, pull, h.HeadSHA)
		if err != nil || !decided {
			return ReactionNone, err
		}
	}
	taken, err := s.claim(ctx, r, key, h)
	if err != nil || !taken {
		return ReactionNone, err
	}
	created, _ := time.Parse(time.RFC3339, h.CreatedAt)
	today, _ := time.Parse(time.DateOnly, h.Today)
	c := Comment{Repo: r, Pull: pull, Author: h.Author, Today: today, CreatedAt: created}
	reaction, err := s.take(ctx, c, request.Line{Reason: h.Reason, Expires: h.Expires}, nil, &h)
	if err != nil {
		s.restore(ctx, r, key, h)
	}
	return reaction, err
}

// decided reports whether the pull record holds a result with a verdict for head (checked).
func (s *Service) decided(ctx context.Context, r api.Repo, pull int, head string) (bool, error) {
	var rec records.Pull
	err := records.Load(ctx, s.store, tenant(r), records.PullKey(r.FullName, pull), &rec)
	if errors.Is(err, records.ErrNoRecord) {
		return false, nil
	}
	if err != nil {
		return false, fmt.Errorf("requests: read the pull record: %w", err)
	}
	return checked(rec, head), nil
}

// checked reports whether rec is a result with a verdict for head: pass, fail or waiting. A line posted then is not
// held: its result is read now, and one that cannot be read back is refused.
func checked(rec records.Pull, head string) bool {
	switch result.Status(rec.Status) {
	case result.StatusPass, result.StatusFail, result.StatusWaiting:
		return rec.HeadSHA == head
	}
	return false
}

// claim deletes the held line h in one swap, while it is still h: only the caller that deleted it files it.
func (s *Service) claim(ctx context.Context, r api.Repo, key string, h records.HeldRequest) (bool, error) {
	taken := false
	err := store.Update(ctx, s.store, tenant(r), []string{key}, func(cur [][]byte) ([][]byte, error) {
		taken = false
		if cur[0] == nil {
			return cur, nil
		}
		var now records.HeldRequest
		if err := records.Decode(cur[0], &now); err != nil {
			return nil, err
		}
		if now != h {
			return cur, nil
		}
		taken = true
		return [][]byte{nil}, nil
	})
	if err != nil {
		return false, fmt.Errorf("requests: take the held request: %w", err)
	}
	return taken, nil
}

// dropHeld deletes the line held for pull, if any.
func (s *Service) dropHeld(ctx context.Context, r api.Repo, pull int) error {
	err := store.Update(ctx, s.store, tenant(r), []string{records.HeldRequestKey(r.FullName, pull)}, func([][]byte) ([][]byte, error) {
		return [][]byte{nil}, nil
	})
	if err != nil {
		return fmt.Errorf("requests: drop the held request: %w", err)
	}
	return nil
}

// restore holds h again after its filing failed, unless a newer early line took its place. It is logged, with
// identifiers only, when that fails too.
func (s *Service) restore(ctx context.Context, r api.Repo, key string, h records.HeldRequest) {
	ctx, cancel := context.WithTimeout(context.WithoutCancel(ctx), restoreTimeout)
	defer cancel()
	raw, err := records.Encode(&h)
	if err == nil {
		err = store.Update(ctx, s.store, tenant(r), []string{key}, func(cur [][]byte) ([][]byte, error) {
			if cur[0] != nil {
				return cur, nil
			}
			return [][]byte{raw}, nil
		})
	}
	if err != nil {
		slog.Default().WarnContext(ctx, "requests: a held request was lost", "installation_id", r.InstallationID,
			"repository", r.FullName, "error", err.Error())
	}
}

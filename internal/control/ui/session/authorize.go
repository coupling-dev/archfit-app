package session

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"regexp"
	"strings"
	"time"

	"github.com/alexei-led/archfit-app/internal/github/api"
)

const (
	cacheTTL        = 60 * time.Second
	maxCacheEntries = 10_000
	githubTimeout   = 10 * time.Second

	permNone = "none"
)

// GitHub's own limits: owner up to 39 characters, repository up to 100.
var fullNameRE = regexp.MustCompile(`^[A-Za-z0-9-]{1,39}/[A-Za-z0-9._-]{1,100}$`)

type cacheKey struct {
	userID   int64
	fullName string // lower case
}

// cacheEntry is one complete GitHub answer. repo is zero when perm is "none".
type cacheEntry struct {
	perm    string
	repo    api.Repo
	expires time.Time
}

// permissionRank orders none < read < write < admin. Anything unknown is none.
func permissionRank(p string) int {
	switch p {
	case "read":
		return 1
	case "write":
		return 2
	case "admin":
		return 3
	}
	return 0
}

func allowed(have, minPermission string) bool {
	need := permissionRank(minPermission)
	return need > 0 && permissionRank(have) >= need
}

func validFullName(fullName string) bool {
	if !fullNameRE.MatchString(fullName) {
		return false
	}
	_, name, _ := strings.Cut(fullName, "/")
	return name != "." && name != ".."
}

func (s *sessions) Authorize(ctx context.Context, u User, fullName, minPermission string) (api.Repo, error) {
	if permissionRank(minPermission) == 0 || !validFullName(fullName) || u.Token == "" {
		return api.Repo{}, ErrForbidden
	}
	key := cacheKey{userID: u.UserID, fullName: strings.ToLower(fullName)}
	e, ok := s.cacheGet(key)
	if !ok {
		var err error
		if e, err = s.ask(ctx, u, fullName); err != nil {
			return api.Repo{}, err
		}
		s.cachePut(key, e)
	}
	if !allowed(e.perm, minPermission) {
		return api.Repo{}, ErrForbidden
	}
	return e.repo, nil
}

// ask gets one complete answer from GitHub: the user's permission with the user's own token,
// then, only for a user who has one, the covering installation with the App JWT.
func (s *sessions) ask(ctx context.Context, u User, fullName string) (cacheEntry, error) {
	start := s.now()
	ctx, cancel := context.WithTimeout(ctx, githubTimeout)
	defer cancel()

	perm, err := s.users.Permission(ctx, u.Token, fullName)
	if err != nil {
		return cacheEntry{}, s.askFailed(ctx, u, start, "permission", err)
	}
	if permissionRank(perm) == 0 {
		s.logAnswer(ctx, slog.LevelInfo, u, api.Repo{}, permNone, start)
		return cacheEntry{perm: permNone, expires: s.now().Add(cacheTTL)}, nil
	}
	repo, err := s.repos.ResolveRepo(ctx, fullName)
	if err != nil {
		return cacheEntry{}, s.askFailed(ctx, u, start, "resolve repository", err)
	}
	s.logAnswer(ctx, slog.LevelInfo, u, repo, perm, start)
	return cacheEntry{perm: perm, repo: repo, expires: s.now().Add(cacheTTL)}, nil
}

func (s *sessions) askFailed(ctx context.Context, u User, start time.Time, step string, err error) error {
	switch {
	case errors.Is(err, api.ErrNotFound):
		s.logAnswer(ctx, slog.LevelInfo, u, api.Repo{}, "not_found", start)
		return ErrForbidden
	case errors.Is(err, api.ErrRateLimited):
		s.logAnswer(ctx, slog.LevelWarn, u, api.Repo{}, "rate_limited", start)
	default:
		s.logAnswer(ctx, slog.LevelWarn, u, api.Repo{}, "github_error", start)
	}
	return fmt.Errorf("session: %s: %w", step, err)
}

func (s *sessions) logAnswer(ctx context.Context, level slog.Level, u User, repo api.Repo, outcome string, start time.Time) {
	slog.Default().Log(ctx, level, "session: authorize",
		"user_id", u.UserID,
		"repo_id", repo.ID,
		"installation_id", repo.InstallationID,
		"outcome", outcome,
		"duration_ms", s.now().Sub(start).Milliseconds(),
	)
}

func (s *sessions) cacheGet(key cacheKey) (cacheEntry, bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	e, ok := s.cache[key]
	if !ok || !s.now().Before(e.expires) {
		return cacheEntry{}, false
	}
	return e, true
}

// cachePut keeps the cache within maxCacheEntries: when full it drops the expired entries,
// and clears everything if that frees nothing. Ceiling: a clear costs one burst of GitHub
// calls; move to an LRU if the bound is ever reached in normal traffic.
func (s *sessions) cachePut(key cacheKey, e cacheEntry) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if _, replace := s.cache[key]; !replace && len(s.cache) >= maxCacheEntries {
		now := s.now()
		for k, old := range s.cache {
			if !now.Before(old.expires) {
				delete(s.cache, k)
			}
		}
		if len(s.cache) >= maxCacheEntries {
			clear(s.cache)
		}
	}
	s.cache[key] = e
}

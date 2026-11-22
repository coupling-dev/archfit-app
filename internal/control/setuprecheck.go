package control

import (
	"context"
	"crypto/rand"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"time"

	"github.com/coupling-dev/archfit-app/internal/github/api"
	"github.com/coupling-dev/archfit-app/internal/store"
)

type setupGitHub interface {
	ActionsSuite(context.Context, api.Repo, string) (int64, bool, error)
	RerequestSuite(context.Context, api.Repo, int64) (int, error)
	RunURL(context.Context, api.Repo, int64) (string, error)
	HasMarkerComment(context.Context, api.Repo, int) (bool, error)
	Comment(context.Context, api.Repo, int, string) error
}

type setupRecheck struct {
	store store.Store
	gh    setupGitHub
	now   func() time.Time
}
type setupLease struct {
	Token   string    `json:"token,omitempty"`
	Expires time.Time `json:"expires,omitempty"`
	Outcome string    `json:"outcome,omitempty"`
}

const setupMarker = "<!-- archfit:setup-recheck -->"
const setupLeaseTTL = 10 * time.Minute

func (s setupRecheck) Run(ctx context.Context, repo api.Repo, pr int, head string) (string, error) {
	if repo.InstallationID <= 0 || repo.ID <= 0 || pr <= 0 || head == "" {
		return "failed", errors.New("setup recheck: invalid target")
	}
	tenant := store.Tenant(repo.InstallationID)
	key := fmt.Sprintf("setup-recheck/%d/%d", repo.ID, pr)
	var mine []byte
	for {
		cur, err := s.store.Get(ctx, tenant, key)
		if errors.Is(err, store.ErrNotFound) {
			cur = nil
		} else if err != nil {
			return "failed", err
		}
		if cur != nil {
			var old setupLease
			if err := json.Unmarshal(cur, &old); err != nil {
				return "failed", fmt.Errorf("setup recheck: invalid record: %w", err)
			}
			if old.Outcome != "" {
				return "done", nil
			}
			if s.now().Before(old.Expires) {
				return "busy", errors.New("setup recheck: lease busy")
			}
		}
		mine, err = json.Marshal(setupLease{Token: rand.Text(), Expires: s.now().Add(setupLeaseTTL)})
		if err != nil {
			return "failed", err
		}
		ok, err := s.store.CompareAndSwap(ctx, tenant, key, cur, mine)
		if err != nil {
			return "failed", err
		}
		if ok {
			break
		}
	}
	work, cancel := context.WithTimeout(ctx, setupLeaseTTL-10*time.Second)
	defer cancel()
	outcome, err := s.check(work, repo, pr, head)
	// An uncertain write holds the lease until expiry; a retry checks the marker first.
	if err != nil {
		return "failed", err
	}
	final, err := json.Marshal(setupLease{Outcome: outcome})
	if err != nil {
		return "failed", err
	}
	ok, err := s.store.CompareAndSwap(work, tenant, key, mine, final)
	if err != nil {
		return "failed", err
	}
	if !ok {
		return "failed", errors.New("setup recheck: lease lost")
	}
	return outcome, nil
}

func (s setupRecheck) check(ctx context.Context, repo api.Repo, pr int, head string) (string, error) {
	id, found, err := s.gh.ActionsSuite(ctx, repo, head)
	if err != nil {
		return "failed", fmt.Errorf("setup recheck: suite lookup: %v", err)
	}
	if found {
		status, callErr := s.gh.RerequestSuite(ctx, repo, id)
		slog.InfoContext(ctx, "setup suite rerequest", "repository", repo.FullName, "pr", pr, "status", status)
		if callErr == nil && status == 201 {
			return "rerequested", nil
		}
		if status < 400 || status >= 500 {
			return "failed", fmt.Errorf("setup recheck: rerequest status %d: %v", status, callErr)
		}
	}
	marked, err := s.gh.HasMarkerComment(ctx, repo, pr)
	if err != nil {
		return "failed", err
	}
	if marked {
		return "commented", nil
	}
	var body string
	if found {
		url, err := s.gh.RunURL(ctx, repo, id)
		if errors.Is(err, api.ErrNotFound) {
			found = false
		} else if err != nil {
			return "failed", err
		}
		if found {
			body = setupMarker + "\n## Archfit is installed. The check has not started.\n\nGitHub did not let the App start the check on this pull request. Re-run it: " + url + "\nAfter it runs, the check shows \"merge to start\"."
		}
	}
	if !found {
		body = setupMarker + "\n## Archfit is installed. The check has not started.\n\nRe-run the archfit workflow, or push a new commit: https://github.com/" + repo.FullName + "/actions/workflows/archfit.yaml"
	}
	if err := s.gh.Comment(ctx, repo, pr, body); err != nil {
		return "failed", err
	}
	return "commented", nil
}

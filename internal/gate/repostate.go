package gate

import (
	"context"
	"errors"
	"strings"

	"github.com/coupling-dev/archfit-app/internal/gate/records"
	"github.com/coupling-dev/archfit-app/internal/github/api"
	"github.com/coupling-dev/archfit-app/internal/store"
)

const (
	RepoNotSetUp    = "not set up"
	RepoOutsideFlow = "policy changed outside the flow"
	RepoExpired     = "expired"
	RepoDrift       = "drift"
	RepoClean       = "clean"
	RepoUnknown     = "unknown"
)

// RepoStates supplies only the stored repository state needed by the repositories page.
type RepoStates interface {
	RepoState(context.Context, api.Repo) (string, error)
	// OpenRequests are the repository's open requests, from stored records only.
	OpenRequests(context.Context, api.Repo) ([]HomeRequest, error)
}

type repoStates struct {
	store      store.Store
	admissions *Admissions
}

// NewRepoStates reads v4/repo without consulting checks, policies, labels or issues.
func NewRepoStates(s store.Store, admissions *Admissions) RepoStates {
	return &repoStates{store: s, admissions: admissions}
}

func (s *repoStates) RepoState(ctx context.Context, repo api.Repo) (string, error) {
	admitted, err := s.admissions.Admitted(ctx, repo.InstallationID, strings.SplitN(repo.FullName, "/", 2)[0])
	if err != nil {
		return "", err
	}
	if !admitted {
		return "", api.ErrNotFound
	}
	// Under WithRequestMemo the record is shared with the Setup state of the same request. It is only read here.
	rec, err := loadRepoRecord(ctx, s.store, repo.RepoRef)
	if err != nil && !errors.Is(err, records.ErrNoRecord) {
		return "", err
	}
	switch {
	case rec.ApprovedDigest == "":
		return RepoNotSetUp, nil
	case rec.OutsideFlow != nil:
		return RepoOutsideFlow, nil
	case rec.Drift == nil || rec.Drift.Unread || rec.Drift.Head == "":
		return RepoUnknown, nil
	}
	switch rec.Drift.State {
	case RepoExpired, RepoDrift, RepoClean:
		return rec.Drift.State, nil
	default:
		return RepoUnknown, nil
	}
}

// OpenRequests reads the open requests of an admitted repository from its stored records; it reads no GitHub.
func (s *repoStates) OpenRequests(ctx context.Context, repo api.Repo) ([]HomeRequest, error) {
	admitted, err := s.admissions.Admitted(ctx, repo.InstallationID, strings.SplitN(repo.FullName, "/", 2)[0])
	if err != nil {
		return nil, err
	}
	if !admitted {
		return nil, api.ErrNotFound
	}
	return openRequests(ctx, s.store, store.Tenant(repo.InstallationID), repo)
}

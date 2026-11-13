package gate

import (
	"context"
	"errors"
	"strings"

	"github.com/alexei-led/archfit-app/internal/gate/records"
	"github.com/alexei-led/archfit-app/internal/github/api"
	"github.com/alexei-led/archfit-app/internal/store"
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
	var rec records.Repo
	err = records.Load(ctx, s.store, store.Tenant(repo.InstallationID), records.RepoKey(repo.FullName), &rec)
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

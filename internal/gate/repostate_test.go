package gate

import (
	"context"
	"errors"
	"slices"
	"testing"

	"github.com/alexei-led/archfit-app/internal/gate/records"
	"github.com/alexei-led/archfit-app/internal/github/api"
	"github.com/alexei-led/archfit-app/internal/store"
)

type repoStateStore struct {
	store.Store
	reads []string
	fail  error
}

func (s *repoStateStore) Get(ctx context.Context, tenant store.Tenant, key string) ([]byte, error) {
	if tenant != 1 {
		return nil, errors.New("wrong tenant")
	}
	s.reads = append(s.reads, key)
	if s.fail != nil {
		return nil, s.fail
	}
	return s.Store.Get(ctx, tenant, key)
}

func TestRepoStateReadsOnlyTheRepositoryRecord(t *testing.T) {
	tests := []struct {
		name   string
		record *records.Repo
		want   string
	}{
		{"absent", nil, RepoNotSetUp},
		{"not approved before outside flow", &records.Repo{OutsideFlow: &records.OutsideFlow{}, Drift: &records.DriftState{Head: "head", State: RepoExpired}}, RepoNotSetUp},
		{"outside flow before expired", &records.Repo{ApprovedDigest: "approved", OutsideFlow: &records.OutsideFlow{}, Drift: &records.DriftState{Head: "head", State: RepoExpired}}, RepoOutsideFlow},
		{"expired", &records.Repo{ApprovedDigest: "approved", Drift: &records.DriftState{Head: "head", State: RepoExpired}}, RepoExpired},
		{"drift", &records.Repo{ApprovedDigest: "approved", Drift: &records.DriftState{Head: "head", State: RepoDrift}}, RepoDrift},
		{"clean", &records.Repo{ApprovedDigest: "approved", Drift: &records.DriftState{Head: "head", State: RepoClean}}, RepoClean},
		{"missing evidence", &records.Repo{ApprovedDigest: "approved"}, RepoUnknown},
		{"missing head", &records.Repo{ApprovedDigest: "approved", Drift: &records.DriftState{State: RepoClean}}, RepoUnknown},
		{"partial clean", &records.Repo{ApprovedDigest: "approved", Drift: &records.DriftState{Head: "head", State: RepoClean, Unread: true}}, RepoUnknown},
		{"partial expired", &records.Repo{ApprovedDigest: "approved", Drift: &records.DriftState{Head: "head", State: RepoExpired, Unread: true}}, RepoUnknown},
		{"unknown state", &records.Repo{ApprovedDigest: "approved", Drift: &records.DriftState{Head: "head", State: "other"}}, RepoUnknown},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			s := &repoStateStore{Store: store.NewMemory()}
			repo := api.Repo{RepoRef: api.RepoRef{InstallationID: 1, FullName: "acme/app"}}
			if tt.record != nil {
				raw, err := records.Encode(tt.record)
				if err != nil {
					t.Fatal(err)
				}
				if err := s.Put(t.Context(), 1, records.RepoKey(repo.FullName), raw); err != nil {
					t.Fatal(err)
				}
			}
			got, err := NewRepoStates(s, NewAdmissions(s, []string{"acme"})).RepoState(t.Context(), repo)
			if err != nil || got != tt.want {
				t.Fatalf("state = %q, %v; want %q", got, err, tt.want)
			}
			if !slices.Equal(s.reads, []string{records.RepoKey(repo.FullName)}) {
				t.Errorf("reads = %v; want only the repository record", s.reads)
			}
		})
	}
}

func TestRepoStateRefusesUnadmittedRepositoryBeforeReading(t *testing.T) {
	s := &repoStateStore{Store: store.NewMemory()}
	repo := api.Repo{RepoRef: api.RepoRef{InstallationID: 1, FullName: "outsider/app"}}
	got, err := NewRepoStates(s, NewAdmissions(s, nil)).RepoState(t.Context(), repo)
	if !errors.Is(err, api.ErrNotFound) || got != "" {
		t.Fatalf("state = %q, %v", got, err)
	}
	if slices.Contains(s.reads, records.RepoKey(repo.FullName)) {
		t.Fatal("read an unadmitted repository")
	}
}

func TestRepoStateStorageFailureIsNotAState(t *testing.T) {
	down := errors.New("storage unavailable")
	s := &repoStateStore{Store: store.NewMemory(), fail: down}
	repo := api.Repo{RepoRef: api.RepoRef{InstallationID: 1, FullName: "acme/app"}}
	got, err := NewRepoStates(s, NewAdmissions(s, []string{"acme"})).RepoState(t.Context(), repo)
	if !errors.Is(err, down) || got != "" {
		t.Fatalf("state = %q, %v", got, err)
	}
}

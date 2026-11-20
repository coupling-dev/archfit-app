package session

import (
	"context"
	"net/url"
	"sync"
	"testing"

	"github.com/coupling-dev/archfit-app/internal/github/api"
	"github.com/coupling-dev/archfit-app/internal/github/auth"
)

type fakeOAuth struct {
	mu    sync.Mutex
	tok   auth.UserToken
	err   error
	codes []string
}

func (f *fakeOAuth) AuthorizeURL(state, redirectURL string) string {
	return "https://github.example/login/oauth/authorize?state=" + state +
		"&redirect_uri=" + url.QueryEscape(redirectURL)
}

func (f *fakeOAuth) Exchange(_ context.Context, code string) (auth.UserToken, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.codes = append(f.codes, code)
	if f.err != nil {
		return auth.UserToken{}, f.err
	}
	return f.tok, nil
}

func (f *fakeOAuth) exchanges() []string {
	f.mu.Lock()
	defer f.mu.Unlock()
	return append([]string(nil), f.codes...)
}

type permissionCall struct{ token, fullName string }

type fakeUsers struct {
	t     testing.TB
	mu    sync.Mutex
	perm  string
	err   error
	calls []permissionCall
}

func (f *fakeUsers) Permission(ctx context.Context, userToken, fullName string) (string, error) {
	perm, _, err := f.Access(ctx, userToken, fullName)
	return perm, err
}

func (f *fakeUsers) Access(_ context.Context, userToken, fullName string) (string, api.Repo, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.calls = append(f.calls, permissionCall{userToken, fullName})
	return f.perm, resolvedRepo(0), f.err
}

func (f *fakeUsers) UserRepos(context.Context, string) ([]api.Repo, error) {
	f.t.Errorf("session must not call Users.UserRepos")
	return nil, nil
}

func (f *fakeUsers) answer(perm string, err error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.perm, f.err = perm, err
}

func (f *fakeUsers) callCount() int {
	f.mu.Lock()
	defer f.mu.Unlock()
	return len(f.calls)
}

func (f *fakeUsers) lastCall() permissionCall {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.calls[len(f.calls)-1]
}

// fakeRepos answers InstallationID only; session calls no other method of Repos.
type fakeRepos struct {
	t              testing.TB
	mu             sync.Mutex
	installationID int64
	err            error
	calls          int
}

func (f *fakeRepos) InstallationID(context.Context, string) (int64, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.calls++
	if f.err != nil {
		return 0, f.err
	}
	return f.installationID, nil
}

func (f *fakeRepos) answer(installationID int64, err error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.installationID, f.err = installationID, err
}

func (f *fakeRepos) callCount() int {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.calls
}

func (f *fakeRepos) ResolveRepo(context.Context, string) (api.Repo, error) {
	f.t.Errorf("session must not call Repos.ResolveRepo")
	return api.Repo{}, nil
}

func (f *fakeRepos) ListInstallations(context.Context) ([]api.Installation, error) {
	f.t.Errorf("session must not call Repos.ListInstallations")
	return nil, nil
}

func (f *fakeRepos) ListRepos(context.Context, int64) ([]api.Repo, error) {
	f.t.Errorf("session must not call Repos.ListRepos")
	return nil, nil
}

func (f *fakeRepos) File(context.Context, api.RepoRef, string, string) ([]byte, string, error) {
	f.t.Errorf("session must not call Repos.File")
	return nil, "", nil
}

func (f *fakeRepos) BranchHead(context.Context, api.RepoRef, string) (string, error) {
	f.t.Errorf("session must not call Repos.BranchHead")
	return "", nil
}

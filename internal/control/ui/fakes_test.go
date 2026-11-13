package ui

import (
	"cmp"
	"context"
	"errors"
	"fmt"
	"slices"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/alexei-led/archfit-app/internal/control/account"
	"github.com/alexei-led/archfit-app/internal/github/api"
	"github.com/alexei-led/archfit-app/internal/github/auth"
	"github.com/alexei-led/archfit-app/internal/store"
)

func sha(n int) string { return fmt.Sprintf("%040x", n) }

// ghCall is one call that reached the fake GitHub.
type ghCall struct {
	method         string
	installationID int64
	fullName       string
	detail         string
}

// fakeGH is GitHub as ui and session see it: the role-interface methods they call, over maps.
type fakeGH struct {
	mu        sync.Mutex
	repos     map[string]api.Repo       // installed repositories by full name
	perms     map[string]string         // token + " " + full name → permission
	listed    map[string][]string       // token → full names UserRepos returns
	files     map[string][]byte         // full name + "@" + ref + ":" + path
	heads     map[string]string         // full name + "@" + branch
	checks    map[string][]api.CheckRun // full name + "@" + sha + "#" + check name
	artifacts map[int64][]byte
	pulls     map[string]api.Pull // full name + "#" + number
	fail      map[string]error    // method, method + " " + full name, or method + " " + the call's detail
	calls     []ghCall
}

func newFakeGH() *fakeGH {
	return &fakeGH{
		repos: map[string]api.Repo{}, perms: map[string]string{}, listed: map[string][]string{},
		files: map[string][]byte{}, heads: map[string]string{}, checks: map[string][]api.CheckRun{},
		pulls: map[string]api.Pull{}, fail: map[string]error{}, artifacts: map[int64][]byte{},
	}
}

func (g *fakeGH) record(method string, r api.RepoRef, detail string) error {
	g.calls = append(g.calls, ghCall{method, r.InstallationID, r.FullName, detail})
	if err := g.fail[method+" "+r.FullName]; err != nil {
		return err
	}
	if err := g.fail[method+" "+detail]; detail != "" && err != nil {
		return err
	}
	return g.fail[method]
}

func (g *fakeGH) called(method string) []ghCall {
	g.mu.Lock()
	defer g.mu.Unlock()
	var out []ghCall
	for _, c := range g.calls {
		if c.method == method {
			out = append(out, c)
		}
	}
	return out
}

func (g *fakeGH) setFile(fullName, ref, path, content string) {
	g.mu.Lock()
	defer g.mu.Unlock()
	g.files[fullName+"@"+ref+":"+path] = []byte(content)
}

func (g *fakeGH) Permission(_ context.Context, userToken, fullName string) (string, error) {
	g.mu.Lock()
	defer g.mu.Unlock()
	if err := g.record("Permission", api.RepoRef{FullName: fullName}, ""); err != nil {
		return "", err
	}
	if p, ok := g.perms[userToken+" "+fullName]; ok {
		return p, nil
	}
	return "none", nil
}

func (g *fakeGH) UserRepos(_ context.Context, userToken string) ([]api.Repo, error) {
	g.mu.Lock()
	defer g.mu.Unlock()
	if err := g.record("UserRepos", api.RepoRef{}, ""); err != nil {
		return nil, err
	}
	var out []api.Repo
	for _, name := range g.listed[userToken] {
		if r, ok := g.repos[name]; ok && g.perms[userToken+" "+name] != "" && g.perms[userToken+" "+name] != "none" {
			out = append(out, r)
		}
	}
	return out, nil
}

// ListInstallations answers one installation per installation ID of the repositories, its account the owner.
func (g *fakeGH) ListInstallations(context.Context) ([]api.Installation, error) {
	g.mu.Lock()
	defer g.mu.Unlock()
	if err := g.record("ListInstallations", api.RepoRef{}, ""); err != nil {
		return nil, err
	}
	seen := map[int64]bool{}
	out := []api.Installation{}
	for _, r := range g.repos {
		if !seen[r.InstallationID] {
			seen[r.InstallationID] = true
			out = append(out, api.Installation{ID: r.InstallationID, Account: ownerOf(r.FullName), AccountType: "Organization",
				CreatedAt: time.Date(2026, 9, 1, 0, 0, 0, 0, time.UTC).Add(time.Duration(r.InstallationID) * time.Hour)})
		}
	}
	slices.SortFunc(out, func(a, b api.Installation) int { return cmp.Compare(a.ID, b.ID) })
	return out, nil
}

func (g *fakeGH) ListRepos(_ context.Context, installationID int64) ([]api.Repo, error) {
	g.mu.Lock()
	defer g.mu.Unlock()
	if err := g.record("ListRepos", api.RepoRef{InstallationID: installationID}, ""); err != nil {
		return nil, err
	}
	var out []api.Repo
	for _, r := range g.repos {
		if r.InstallationID == installationID {
			out = append(out, r)
		}
	}
	return out, nil
}

func (g *fakeGH) ResolveRepo(_ context.Context, fullName string) (api.Repo, error) {
	g.mu.Lock()
	defer g.mu.Unlock()
	if r, ok := g.repos[fullName]; ok {
		return r, nil
	}
	return api.Repo{}, api.ErrNotFound
}

func (g *fakeGH) File(_ context.Context, r api.RepoRef, ref, path string) ([]byte, string, error) {
	g.mu.Lock()
	defer g.mu.Unlock()
	if err := g.record("File", r, ref+":"+path); err != nil {
		return nil, "", err
	}
	if sha, ok := g.heads[r.FullName+"@"+ref]; ok {
		if c, ok := g.files[r.FullName+"@"+sha+":"+path]; ok {
			return c, "blob", nil
		}
	}
	c, ok := g.files[r.FullName+"@"+ref+":"+path]
	if !ok {
		for branchKey, head := range g.heads {
			if head == ref && strings.HasPrefix(branchKey, r.FullName+"@") {
				branch := strings.TrimPrefix(branchKey, r.FullName+"@")
				c, ok = g.files[r.FullName+"@"+branch+":"+path]
				if ok {
					break
				}
			}
		}
	}
	if !ok {
		return nil, "", api.ErrNotFound
	}
	return c, "blob", nil
}

func (g *fakeGH) BranchHead(_ context.Context, r api.RepoRef, branch string) (string, error) {
	g.mu.Lock()
	defer g.mu.Unlock()
	if err := g.record("BranchHead", r, branch); err != nil {
		return "", err
	}
	head, ok := g.heads[r.FullName+"@"+branch]
	if ok {
		return head, nil
	}
	for key, pull := range g.pulls {
		if strings.HasPrefix(key, r.FullName+"#") && pull.BaseRef == branch {
			return branch, nil // the fixture has a pull targeting this branch but no commit identity
		}
	}
	prefix := r.FullName + "@" + branch + ":"
	for key := range g.files {
		if strings.HasPrefix(key, prefix) {
			return branch, nil
		}
	}
	return "", api.ErrNotFound
}

func (g *fakeGH) CheckRuns(_ context.Context, r api.RepoRef, headSHA, name string) ([]api.CheckRun, error) {
	g.mu.Lock()
	defer g.mu.Unlock()
	if err := g.record("CheckRuns", r, headSHA+"#"+name); err != nil {
		return nil, err
	}
	return g.checks[r.FullName+"@"+headSHA+"#"+name], nil
}

func (g *fakeGH) PullSummary(_ context.Context, r api.RepoRef, number int) (api.Pull, error) {
	g.mu.Lock()
	defer g.mu.Unlock()
	if err := g.record("PullSummary", r, strconv.Itoa(number)); err != nil {
		return api.Pull{}, err
	}
	p, ok := g.pulls[r.FullName+"#"+strconv.Itoa(number)]
	if !ok {
		return api.Pull{}, api.ErrNotFound
	}
	return p, nil
}

// OpenPulls lists the open pull requests of a repository by number.
func (g *fakeGH) OpenPulls(_ context.Context, r api.RepoRef) ([]api.Pull, error) {
	g.mu.Lock()
	defer g.mu.Unlock()
	if err := g.record("OpenPulls", r, ""); err != nil {
		return nil, err
	}
	var open []api.Pull
	for key, p := range g.pulls {
		if strings.HasPrefix(key, r.FullName+"#") && p.State == "open" {
			open = append(open, p)
		}
	}
	slices.SortFunc(open, func(a, b api.Pull) int { return b.Number - a.Number })
	return open, nil
}

type fakeAccount struct {
	account.Account // the real account over the env's Store: ui calls Status here, and Admit and Unadmit there
	status          account.Status
	err             error
	installations   []int64
}

func (a *fakeAccount) Status(_ context.Context, installationID int64, _ time.Time) (account.Status, error) {
	a.installations = append(a.installations, installationID)
	return a.status, a.err
}

type fakeConsents struct {
	recorded map[int64]map[account.ConsentKind]account.Consent
	touched  []int64 // installation of every call
	writes   int
	readErr  error // the Store is down: Get and Record fail, Has is false
	// readErrAfterWrite fails every Get once something was written: a change saved but not read back.
	readErrAfterWrite error
}

func newFakeConsents() *fakeConsents {
	return &fakeConsents{recorded: map[int64]map[account.ConsentKind]account.Consent{}}
}

func (c *fakeConsents) Record(_ context.Context, installationID int64, consent account.Consent) error {
	c.touched = append(c.touched, installationID)
	if c.readErr != nil {
		return c.readErr
	}
	if _, ok := c.recorded[installationID][consent.Kind]; ok {
		return account.ErrConsentExists
	}
	c.writes++
	if c.recorded[installationID] == nil {
		c.recorded[installationID] = map[account.ConsentKind]account.Consent{}
	}
	c.recorded[installationID][consent.Kind] = consent
	return nil
}

func (c *fakeConsents) Revoke(_ context.Context, installationID int64, k account.ConsentKind) error {
	c.touched = append(c.touched, installationID)
	c.writes++
	delete(c.recorded[installationID], k)
	return nil
}

func (c *fakeConsents) Has(ctx context.Context, installationID int64, k account.ConsentKind) bool {
	_, ok, err := c.Get(ctx, installationID, k)
	return ok && err == nil
}

func (c *fakeConsents) Get(_ context.Context, installationID int64, k account.ConsentKind) (account.Consent, bool, error) {
	c.touched = append(c.touched, installationID)
	if c.readErr != nil {
		return account.Consent{}, false, c.readErr
	}
	if c.readErrAfterWrite != nil && c.writes > 0 {
		return account.Consent{}, false, c.readErrAfterWrite
	}
	got, ok := c.recorded[installationID][k]
	return got, ok, nil
}

// fakeOAuth exchanges the code "code-<login>" for that login's token.
type fakeOAuth struct {
	tokens map[string]auth.UserToken
}

func (o *fakeOAuth) AuthorizeURL(state, redirectURL string) string {
	return "https://github.example/login/oauth/authorize?state=" + state + "&redirect_uri=" + redirectURL
}

func (o *fakeOAuth) Exchange(_ context.Context, code string) (auth.UserToken, error) {
	t, ok := o.tokens[code]
	if !ok {
		return auth.UserToken{}, errors.New("bad code")
	}
	return t, nil
}

// downable fails every read while down is set; the approvals are read through it.
type downable struct {
	store.Store
	down atomic.Bool
}

func (d *downable) Get(ctx context.Context, t store.Tenant, key string) ([]byte, error) {
	if d.down.Load() {
		return nil, errors.New("fake: store down")
	}
	return d.Store.Get(ctx, t, key)
}

func (g *fakeGH) Artifact(_ context.Context, r api.RepoRef, run int64, _ string, _ int64) ([]byte, error) {
	g.mu.Lock()
	defer g.mu.Unlock()
	if err := g.record("Artifact", r, ""); err != nil {
		return nil, err
	}
	if raw, ok := g.artifacts[run]; ok {
		return raw, nil
	}
	return nil, api.ErrNotFound
}

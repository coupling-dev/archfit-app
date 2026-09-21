package proposal

import (
	"context"
	"errors"
	"fmt"
	"sync"

	"github.com/alexei-led/archfit-app/internal/github/api"
)

const (
	fakeBase    = "main"
	fakeBaseSHA = "base0"
	fakeURLBase = "https://ghe.example/acme/shop/pull/"
)

var errUnexpectedCall = errors.New("fake: method must not be called")

type bodyCall struct {
	number int
	body   string
}

// fakeGit is a small in-memory git host: branches, commits and pull requests.
// It implements api.Changes and api.Pulls and records every call.
type fakeGit struct {
	mu         sync.Mutex
	calls      []string
	errs       map[string]error    // method name -> error returned instead of acting
	branches   map[string][]string // branch -> commit SHAs, oldest first
	pulls      []api.Pull
	nextNumber int
	seq        int

	commitTargets []string
	commitFiles   [][]api.FileChange
	newPulls      []api.NewPull
	bodies        []bodyCall
	ready         []int
}

var (
	_ api.Changes = (*fakeGit)(nil)
	_ api.Pulls   = (*fakeGit)(nil)
)

func newFakeGit() *fakeGit {
	return &fakeGit{
		errs:       map[string]error{},
		branches:   map[string][]string{fakeBase: {fakeBaseSHA}},
		nextNumber: 1,
	}
}

// enter records the call and returns the error forced for it, if any. The caller holds f.mu.
func (f *fakeGit) enter(method string) error {
	f.calls = append(f.calls, method)
	return f.errs[method]
}

func (f *fakeGit) EnsureBranch(_ context.Context, _ api.RepoRef, branch, fromRef string) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	if err := f.enter("EnsureBranch"); err != nil {
		return err
	}
	if _, ok := f.branches[branch]; ok {
		return nil
	}
	from, ok := f.branches[fromRef]
	if !ok {
		return fmt.Errorf("fake: base %s: %w", fromRef, api.ErrNotFound)
	}
	f.branches[branch] = append([]string(nil), from...)
	return nil
}

func (f *fakeGit) Commit(_ context.Context, _ api.RepoRef, branch, _ string, files []api.FileChange) (string, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if err := f.enter("Commit"); err != nil {
		return "", err
	}
	if _, ok := f.branches[branch]; !ok {
		return "", fmt.Errorf("fake: branch %s: %w", branch, api.ErrNotFound)
	}
	f.seq++
	sha := fmt.Sprintf("sha%d", f.seq)
	f.branches[branch] = append(f.branches[branch], sha)
	f.commitTargets = append(f.commitTargets, branch)
	f.commitFiles = append(f.commitFiles, files)
	for i := range f.pulls {
		if f.pulls[i].HeadRef == branch && f.pulls[i].State == "open" {
			f.pulls[i].HeadSHA = sha
		}
	}
	return sha, nil
}

func (f *fakeGit) EnsurePull(_ context.Context, r api.RepoRef, p api.NewPull) (api.Pull, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if err := f.enter("EnsurePull"); err != nil {
		return api.Pull{}, err
	}
	f.newPulls = append(f.newPulls, p)
	for _, existing := range f.pulls {
		if existing.HeadRef == p.Head && existing.State == "open" {
			return existing, nil
		}
	}
	history, ok := f.branches[p.Head]
	if !ok {
		return api.Pull{}, fmt.Errorf("fake: head %s: %w", p.Head, api.ErrNotFound)
	}
	created := api.Pull{
		Number:   f.nextNumber,
		State:    "open",
		Draft:    p.Draft,
		Body:     p.Body,
		URL:      fmt.Sprintf("%s%d", fakeURLBase, f.nextNumber),
		HeadSHA:  history[len(history)-1],
		HeadRef:  p.Head,
		HeadRepo: r.FullName,
		BaseRef:  p.Base,
	}
	f.nextNumber++
	f.pulls = append(f.pulls, created)
	return created, nil
}

func (f *fakeGit) UpdatePullBody(_ context.Context, _ api.RepoRef, number int, body string) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	if err := f.enter("UpdatePullBody"); err != nil {
		return err
	}
	f.bodies = append(f.bodies, bodyCall{number, body})
	for i := range f.pulls {
		if f.pulls[i].Number == number {
			f.pulls[i].Body = body
			return nil
		}
	}
	return api.ErrNotFound
}

func (f *fakeGit) MarkReady(_ context.Context, _ api.RepoRef, number int) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	if err := f.enter("MarkReady"); err != nil {
		return err
	}
	f.ready = append(f.ready, number)
	for i := range f.pulls {
		if f.pulls[i].Number == number {
			f.pulls[i].Draft = false
			return nil
		}
	}
	return api.ErrNotFound
}

func (f *fakeGit) PullByHead(_ context.Context, _ api.RepoRef, branch string) (api.Pull, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if err := f.enter("PullByHead"); err != nil {
		return api.Pull{}, err
	}
	var newest *api.Pull
	for i := range f.pulls {
		if f.pulls[i].HeadRef == branch && (newest == nil || f.pulls[i].Number > newest.Number) {
			newest = &f.pulls[i]
		}
	}
	if newest == nil {
		return api.Pull{}, api.ErrNotFound
	}
	return *newest, nil
}

func (f *fakeGit) Pull(context.Context, api.RepoRef, int) (api.Pull, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.calls = append(f.calls, "Pull")
	return api.Pull{}, errUnexpectedCall
}

func (f *fakeGit) OpenPulls(context.Context, api.RepoRef) ([]api.Pull, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.calls = append(f.calls, "OpenPulls")
	return nil, errUnexpectedCall
}

func (f *fakeGit) ChangedFiles(context.Context, api.RepoRef, int) ([]string, bool, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.calls = append(f.calls, "ChangedFiles")
	return nil, false, errUnexpectedCall
}

func (f *fakeGit) Reviews(context.Context, api.RepoRef, int) ([]api.Review, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.calls = append(f.calls, "Reviews")
	return nil, errUnexpectedCall
}

func (f *fakeGit) TeamMember(context.Context, api.RepoRef, string, string) (bool, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.calls = append(f.calls, "TeamMember")
	return false, errUnexpectedCall
}

// merge plays the customer merging the pull request.
func (f *fakeGit) merge(number int) {
	f.mu.Lock()
	defer f.mu.Unlock()
	for i := range f.pulls {
		if f.pulls[i].Number == number {
			f.pulls[i].State = "closed"
			f.pulls[i].Merged = true
		}
	}
}

func (f *fakeGit) resetCalls() {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.calls = nil
}

func (f *fakeGit) openPulls(branch string) []api.Pull {
	f.mu.Lock()
	defer f.mu.Unlock()
	var open []api.Pull
	for _, p := range f.pulls {
		if p.HeadRef == branch && p.State == "open" {
			open = append(open, p)
		}
	}
	return open
}

func (f *fakeGit) history(branch string) []string {
	f.mu.Lock()
	defer f.mu.Unlock()
	return append([]string(nil), f.branches[branch]...)
}

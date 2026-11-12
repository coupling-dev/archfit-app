package proposals

import (
	"context"
	"fmt"
	"maps"
	"slices"
	"sync"

	"github.com/alexei-led/archfit-app/internal/github/api"
)

// fakeHub is a small GitHub: commits that hold whole file sets, branches, pull requests that follow their branch,
// check runs, merges and issues. It implements GitHub.
type fakeHub struct {
	mu       sync.Mutex
	commits  map[string]fakeCommit
	branches map[string]string
	pulls    map[int]*api.Pull
	issues   map[int]bool // number → open
	comments map[int][]string
	checks   []api.CheckRun
	merges   []string // "number@head" of each MergePull call
	calls    []string
	seq      int
	nextPull int

	errs      map[string]error // method → the error it returns instead of acting
	mergeErr  error            // MergePull refuses with it
	mergedBy  string           // when set, a refused MergePull still merged: someone else's merge commit
	headLag   int              // Pull shows the previous head this many times after a branch moved
	lagging   map[int]string   // pull → the head it still shows
	onCommit  func()           // runs inside CommitOn before the branch moves: someone else acts in between
	moveOnPut bool             // someone else pushes to archfit/policy right before the App's next CommitOn
}

type fakeCommit struct {
	parent  string
	message string
	files   map[string][]byte
}

const fakeBase = "main"

func newFakeHub(policy string) *fakeHub {
	h := &fakeHub{commits: map[string]fakeCommit{}, branches: map[string]string{}, pulls: map[int]*api.Pull{},
		issues: map[int]bool{}, comments: map[int][]string{}, errs: map[string]error{}, lagging: map[int]string{},
		nextPull: 20}
	h.branches[fakeBase] = h.put("", "start", map[string][]byte{policyPath: []byte(policy), "README.md": []byte("readme")})
	return h
}

// put stores a commit and returns its SHA, 40 hex characters as GitHub's are.
func (h *fakeHub) put(parent, message string, files map[string][]byte) string {
	h.seq++
	sha := fmt.Sprintf("%040x", h.seq)
	h.commits[sha] = fakeCommit{parent: parent, message: message, files: files}
	return sha
}

// push moves branch to a new commit of the given files over its head, as a person's push does.
func (h *fakeHub) push(branch, path, content string) string {
	h.mu.Lock()
	defer h.mu.Unlock()
	head := h.branches[branch]
	files := maps.Clone(h.commits[head].files)
	files[path] = []byte(content)
	h.branches[branch] = h.put(head, "push", files)
	h.follow(branch)
	return h.branches[branch]
}

// follow moves the open pull requests of branch to its head; with headLag they show the old head for a while.
func (h *fakeHub) follow(branch string) {
	for n, p := range h.pulls {
		if p.HeadRef == branch && p.State == "open" && p.HeadSHA != h.branches[branch] {
			if h.headLag > 0 {
				h.lagging[n] = p.HeadSHA
			}
			p.HeadSHA = h.branches[branch]
		}
	}
}

func (h *fakeHub) enter(method string) error {
	h.calls = append(h.calls, method)
	return h.errs[method]
}

func (h *fakeHub) resolve(ref string) (string, bool) {
	if sha, ok := h.branches[ref]; ok {
		return sha, true
	}
	_, ok := h.commits[ref]
	return ref, ok
}

func (h *fakeHub) File(_ context.Context, _ api.RepoRef, ref, path string) ([]byte, string, error) {
	h.mu.Lock()
	defer h.mu.Unlock()
	if err := h.enter("File"); err != nil {
		return nil, "", err
	}
	sha, ok := h.resolve(ref)
	content, found := h.commits[sha].files[path]
	if !ok || !found {
		return nil, "", api.ErrNotFound
	}
	return slices.Clone(content), "blob", nil
}

func (h *fakeHub) BranchHead(_ context.Context, _ api.RepoRef, branch string) (string, error) {
	h.mu.Lock()
	defer h.mu.Unlock()
	if err := h.enter("BranchHead"); err != nil {
		return "", err
	}
	sha, ok := h.branches[branch]
	if !ok {
		return "", api.ErrNotFound
	}
	return sha, nil
}

func (h *fakeHub) Pull(_ context.Context, _ api.RepoRef, number int) (api.Pull, error) {
	h.mu.Lock()
	defer h.mu.Unlock()
	if err := h.enter("Pull"); err != nil {
		return api.Pull{}, err
	}
	p, ok := h.pulls[number]
	if !ok {
		return api.Pull{}, api.ErrNotFound
	}
	out := *p
	if old, ok := h.lagging[number]; ok && h.headLag > 0 {
		h.headLag--
		out.HeadSHA = old
		if h.headLag == 0 {
			delete(h.lagging, number)
		}
	}
	return out, nil
}

// ancestors lists sha and its parents, nearest first.
func (h *fakeHub) ancestors(sha string) []string {
	var out []string
	for ; sha != ""; sha = h.commits[sha].parent {
		out = append(out, sha)
	}
	return out
}

// Compare is GitHub's three-dot compare: the paths changed from the merge base of base and head to head.
func (h *fakeHub) Compare(_ context.Context, _ api.RepoRef, base, head string) (api.Comparison, error) {
	h.mu.Lock()
	defer h.mu.Unlock()
	if err := h.enter("Compare"); err != nil {
		return api.Comparison{}, err
	}
	if _, ok := h.commits[base]; !ok {
		return api.Comparison{}, api.ErrNotFound
	}
	if _, ok := h.commits[head]; !ok {
		return api.Comparison{}, api.ErrNotFound
	}
	baseLine := h.ancestors(base)
	mergeBase := ""
	for _, c := range h.ancestors(head) {
		if slices.Contains(baseLine, c) {
			mergeBase = c
			break
		}
	}
	from, to := h.commits[mergeBase].files, h.commits[head].files
	var paths []string
	for p, v := range to {
		if old, ok := from[p]; !ok || string(old) != string(v) {
			paths = append(paths, p)
		}
	}
	for p := range from {
		if _, ok := to[p]; !ok {
			paths = append(paths, p)
		}
	}
	slices.Sort(paths)
	status := "diverged"
	switch {
	case base == head:
		status = "identical"
	case mergeBase == base:
		status = "ahead"
	case mergeBase == head:
		status = "behind"
	}
	return api.Comparison{Status: status, Paths: paths}, nil
}

func (h *fakeHub) CommitOn(_ context.Context, _ api.RepoRef, branch, parent, message string, files []api.FileChange, force bool) (string, error) {
	h.mu.Lock()
	defer h.mu.Unlock()
	if err := h.enter("CommitOn"); err != nil {
		return "", err
	}
	if h.moveOnPut {
		h.moveOnPut = false
		if head, ok := h.branches[branch]; ok {
			h.branches[branch] = h.put(head, "someone else", h.commits[head].files)
			h.follow(branch)
		}
	}
	p, ok := h.commits[parent]
	if !ok {
		return "", api.ErrNotFound
	}
	next := maps.Clone(p.files)
	for _, f := range files {
		next[f.Path] = slices.Clone(f.Content)
	}
	sha := parent
	if !maps.EqualFunc(next, p.files, func(a, b []byte) bool { return string(a) == string(b) }) {
		sha = h.put(parent, message, next)
	}
	if h.onCommit != nil {
		h.onCommit()
	}
	cur, exists := h.branches[branch]
	if exists && !force && cur != parent && cur != sha {
		return "", fmt.Errorf("%w: not a fast-forward", api.ErrUnprocessable)
	}
	h.branches[branch] = sha
	h.follow(branch)
	return sha, nil
}

func (h *fakeHub) EnsurePull(_ context.Context, _ api.RepoRef, np api.NewPull) (api.Pull, error) {
	h.mu.Lock()
	defer h.mu.Unlock()
	if err := h.enter("EnsurePull"); err != nil {
		return api.Pull{}, err
	}
	for _, p := range h.pulls {
		if p.HeadRef == np.Head && p.BaseRef == np.Base && p.State == "open" {
			return *p, nil
		}
	}
	h.nextPull++
	p := &api.Pull{Number: h.nextPull, Title: np.Title, Body: np.Body, State: "open", Draft: np.Draft,
		HeadRef: np.Head, HeadRepo: "acme/shop", BaseRef: np.Base, HeadSHA: h.branches[np.Head]}
	h.pulls[p.Number] = p
	return *p, nil
}

func (h *fakeHub) UpdatePullBody(_ context.Context, _ api.RepoRef, number int, body string) error {
	h.mu.Lock()
	defer h.mu.Unlock()
	if err := h.enter("UpdatePullBody"); err != nil {
		return err
	}
	h.pulls[number].Body = body
	return nil
}

func (h *fakeHub) MarkReady(_ context.Context, _ api.RepoRef, number int) error {
	h.mu.Lock()
	defer h.mu.Unlock()
	if err := h.enter("MarkReady"); err != nil {
		return err
	}
	h.pulls[number].Draft = false
	return nil
}

// MergePull squash-merges as GitHub does under a ruleset that requires archfit/gate from the App with no bypass actor
// (design appendix D.2): the latest archfit/gate run on the head must be a success.
func (h *fakeHub) MergePull(_ context.Context, _ api.RepoRef, number int, headSHA string) (string, error) {
	h.mu.Lock()
	defer h.mu.Unlock()
	if err := h.enter("MergePull"); err != nil {
		return "", err
	}
	h.merges = append(h.merges, fmt.Sprintf("%d@%s", number, headSHA))
	p := h.pulls[number]
	if h.mergeErr != nil {
		if h.mergedBy != "" {
			p.State, p.Merged, p.MergeCommitSHA = "closed", true, h.mergedBy
		}
		return "", h.mergeErr
	}
	if p.HeadSHA != headSHA {
		return "", fmt.Errorf("%w: head moved", api.ErrHeadChanged)
	}
	if !h.passed(headSHA) {
		return "", fmt.Errorf("%w: archfit/gate is not a success", api.ErrNotMergeable)
	}
	tip := h.branches[p.BaseRef]
	files := maps.Clone(h.commits[tip].files)
	maps.Copy(files, h.commits[headSHA].files)
	sha := h.put(tip, p.Title, files)
	h.branches[p.BaseRef] = sha
	p.State, p.Merged, p.MergeCommitSHA = "closed", true, sha
	return sha, nil
}

// passed reports whether the latest archfit/gate run on head is a success.
func (h *fakeHub) passed(head string) bool {
	for i := len(h.checks) - 1; i >= 0; i-- {
		if c := h.checks[i]; c.HeadSHA == head && c.Name == gateCheck {
			return c.Status == statusCompleted && c.Conclusion == conclusionSuccess
		}
	}
	return false
}

func (h *fakeHub) CreateCheckRun(_ context.Context, _ api.RepoRef, c api.CheckRun) (api.CheckRun, error) {
	h.mu.Lock()
	defer h.mu.Unlock()
	if err := h.enter("CreateCheckRun"); err != nil {
		return api.CheckRun{}, err
	}
	c.ID = int64(len(h.checks) + 1)
	h.checks = append(h.checks, c)
	return c, nil
}

func (h *fakeHub) CreateComment(_ context.Context, _ api.RepoRef, number int, body string) (api.Comment, error) {
	h.mu.Lock()
	defer h.mu.Unlock()
	if err := h.enter("CreateComment"); err != nil {
		return api.Comment{}, err
	}
	h.comments[number] = append(h.comments[number], body)
	return api.Comment{ID: int64(len(h.comments[number])), Body: body}, nil
}

func (h *fakeHub) CloseIssue(_ context.Context, _ api.RepoRef, number int) error {
	h.mu.Lock()
	defer h.mu.Unlock()
	if err := h.enter("CloseIssue"); err != nil {
		return err
	}
	h.issues[number] = false
	return nil
}

func (h *fakeHub) ReopenIssue(_ context.Context, _ api.RepoRef, number int) error {
	h.mu.Lock()
	defer h.mu.Unlock()
	if err := h.enter("ReopenIssue"); err != nil {
		return err
	}
	h.issues[number] = true
	return nil
}

// file is the content of path at ref, for assertions.
func (h *fakeHub) file(ref, path string) string {
	h.mu.Lock()
	defer h.mu.Unlock()
	sha, _ := h.resolve(ref)
	return string(h.commits[sha].files[path])
}

// count is how often method was called.
func (h *fakeHub) count(method string) int {
	h.mu.Lock()
	defer h.mu.Unlock()
	return len(slices.DeleteFunc(slices.Clone(h.calls), func(c string) bool { return c != method }))
}

// lastComment is the latest comment on issue number.
func (h *fakeHub) lastComment(number int) string {
	h.mu.Lock()
	defer h.mu.Unlock()
	cs := h.comments[number]
	if len(cs) == 0 {
		return ""
	}
	return cs[len(cs)-1]
}

// writes lists the calls that change something on GitHub.
func (h *fakeHub) writes() []string {
	h.mu.Lock()
	defer h.mu.Unlock()
	reads := []string{"File", "BranchHead", "Pull", "Compare"}
	return slices.DeleteFunc(slices.Clone(h.calls), func(c string) bool { return slices.Contains(reads, c) })
}

var _ GitHub = (*fakeHub)(nil)

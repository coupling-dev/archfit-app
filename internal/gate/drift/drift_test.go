package drift

import (
	"context"
	"errors"
	"os"
	"slices"
	"strconv"
	"strings"
	"testing"

	"github.com/alexei-led/archfit-app/internal/gate/records"
	"github.com/alexei-led/archfit-app/internal/github/api"
	"github.com/alexei-led/archfit-app/internal/store"
	"github.com/alexei-led/archfit-app/internal/wire/result"
	"github.com/alexei-led/archfit-app/internal/wire/wording"
)

const (
	installation = 7
	repoName     = "acme/shop"
	branch       = "main"
	policy       = "version: 3\nowners: [\"@alice\", bob, \"@acme/architects\"]\nmodules:\n  app:\n    paths: [cmd/**]\n"
)

var repo = api.Repo{RepoRef: api.RepoRef{InstallationID: installation, FullName: repoName}, ID: 42, DefaultBranch: branch}

type issue struct {
	title, body string
	open        bool
	comments    []string
	assignees   []string
	labels      []string
}

// fakeGitHub holds issues by number and the policy at every commit.
type fakeGitHub struct {
	issues    map[int]*issue
	next      int
	failOn    string           // a method that fails
	onCreate  func(number int) // runs after an issue is created, before the caller sees it
	assignErr func(logins []string) error
}

func newFake() *fakeGitHub { return &fakeGitHub{issues: map[int]*issue{}, next: 11} }

var errFake = errors.New("fake GitHub failure")

func (f *fakeGitHub) fail(method string) error {
	if f.failOn == method {
		return errFake
	}
	return nil
}

func (f *fakeGitHub) File(_ context.Context, _ api.RepoRef, _, path string) ([]byte, string, error) {
	if path != policyPath {
		return nil, "", api.ErrNotFound
	}
	return []byte(policy), "blob", f.fail("File")
}

func (f *fakeGitHub) CreateIssue(_ context.Context, _ api.RepoRef, i api.NewIssue) (api.Issue, error) {
	if err := f.fail("CreateIssue"); err != nil {
		return api.Issue{}, err
	}
	n := f.next
	f.next++
	f.issues[n] = &issue{title: i.Title, body: i.Body, open: true}
	if f.onCreate != nil {
		f.onCreate(n)
	}
	return api.Issue{Number: n, Body: i.Body}, nil
}

func (f *fakeGitHub) UpdateIssueBody(_ context.Context, _ api.RepoRef, n int, body string) error {
	if err := f.fail("UpdateIssueBody"); err != nil {
		return err
	}
	f.issues[n].body = body
	return nil
}

func (f *fakeGitHub) CloseIssue(_ context.Context, _ api.RepoRef, n int) error {
	if err := f.fail("CloseIssue"); err != nil {
		return err
	}
	f.issues[n].open = false
	return nil
}

func (f *fakeGitHub) ReopenIssue(_ context.Context, _ api.RepoRef, n int) error {
	f.issues[n].open = true
	return nil
}

func (f *fakeGitHub) CreateComment(_ context.Context, _ api.RepoRef, n int, body string) (api.Comment, error) {
	f.issues[n].comments = append(f.issues[n].comments, body)
	return api.Comment{}, nil
}

func (f *fakeGitHub) SetAssigneesAndLabels(_ context.Context, _ api.RepoRef, n int, assignees, labels []string) ([]string, []string, error) {
	if f.assignErr != nil {
		if err := f.assignErr(assignees); err != nil {
			return nil, nil, err
		}
	}
	f.issues[n].assignees, f.issues[n].labels = assignees, labels
	return assignees, labels, nil
}

// block decodes the repository block of a vendored engine golden.
func block(t *testing.T, golden string) *result.Repository {
	t.Helper()
	raw, err := os.ReadFile("../../wire/result/testdata/result/" + golden + ".json")
	if err != nil {
		t.Fatal(err)
	}
	r, err := result.Decode(raw)
	if err != nil {
		t.Fatal(err)
	}
	return r.Repository
}

// noUnread is the block of golden without its unread places.
func noUnread(t *testing.T, golden string) *result.Repository {
	b := block(t, golden)
	b.Unread = []result.UnreadPlace{}
	return b
}

type world struct {
	t     *testing.T
	gh    *fakeGitHub
	store store.Store
	svc   *Service
}

func newWorld(t *testing.T) *world {
	gh, s := newFake(), store.NewMemory()
	return &world{t: t, gh: gh, store: s, svc: New(gh, s)}
}

func (w *world) push(head string, state *result.Repository) string {
	w.t.Helper()
	got, err := w.svc.Pushed(context.Background(), Run{Repo: repo, Head: head, State: state})
	if err != nil {
		w.t.Fatalf("Pushed(%s): %v", head, err)
	}
	return got
}

func (w *world) repoRecord() records.Repo {
	w.t.Helper()
	var r records.Repo
	err := records.Load(context.Background(), w.store, store.Tenant(installation), records.RepoKey(repoName), &r)
	if err != nil && !errors.Is(err, records.ErrNoRecord) {
		w.t.Fatal(err)
	}
	return r
}

const driftRecord = "pkg/tc/netem.go:12: graph: pkg_tc -> pkg_util is not an allowed dependency\n" +
	"  why: a new dependency between modules needs an owner\n" +
	"  balance: 9/10 (min 7)\n" +
	"  next: fix the code, or ask an owner to add an exception\n"

func TestDriftOpensOneIssueForTheOwners(t *testing.T) {
	w := newWorld(t)
	if got := w.push("h1", block(t, "repository_drift")); got != Opened {
		t.Fatalf("Pushed = %q, want %q", got, Opened)
	}
	if len(w.gh.issues) != 1 {
		t.Fatalf("issues = %d, want 1", len(w.gh.issues))
	}
	is := w.gh.issues[11]
	if is.title != "Archfit: drift on main" || !is.open {
		t.Errorf("issue = %+v", is)
	}
	for _, want := range []string{"At commit h1, 1 error:", "```text\n" + driftRecord + "```", "Pull requests are not affected."} {
		if !strings.Contains(is.body, want) {
			t.Errorf("body has no %q:\n%s", want, is.body)
		}
	}
	if !slices.Equal(is.assignees, []string{"alice", "bob"}) || !slices.Equal(is.labels, []string{Label}) {
		t.Errorf("assignees %v, labels %v", is.assignees, is.labels)
	}
	if r := w.repoRecord(); r.DriftIssue != 11 || r.DriftHead != "h1" {
		t.Errorf("record = %+v", r)
	}
}

func TestDriftIsEditedInPlace(t *testing.T) {
	w := newWorld(t)
	w.push("h1", block(t, "repository_drift"))
	w.gh.issues[11].open = false // an owner closed it, but the drift is still there

	two := block(t, "repository_drift")
	second := two.Drift[0]
	second.Path, second.Line = "pkg/tc/qdisc.go", 4
	two.Drift = append(two.Drift, second)
	if got := w.push("h2", two); got != Updated {
		t.Fatalf("Pushed = %q, want %q", got, Updated)
	}
	if len(w.gh.issues) != 1 {
		t.Fatalf("a second issue was opened: %d", len(w.gh.issues))
	}
	is := w.gh.issues[11]
	if !is.open || !strings.Contains(is.body, "At commit h2, 2 errors:") || !strings.Contains(is.body, "pkg/tc/qdisc.go:4: graph") {
		t.Errorf("issue = %+v", is)
	}
	if r := w.repoRecord(); r.DriftIssue != 11 || r.DriftHead != "h2" {
		t.Errorf("record = %+v", r)
	}
}

func TestTheSameHeadAgainChangesNothing(t *testing.T) {
	w := newWorld(t)
	w.push("h1", block(t, "repository_drift"))
	w.gh.failOn = "UpdateIssueBody" // a write would fail the test
	if got := w.push("h1", block(t, "repository_drift")); got != None {
		t.Errorf("Pushed = %q, want %q", got, None)
	}
}

func TestDriftClosesWhenNoErrorIsLeftAndEveryPlaceWasRead(t *testing.T) {
	for _, golden := range []string{"repository_clean", "repository_expired"} {
		t.Run(golden, func(t *testing.T) {
			w := newWorld(t)
			w.push("h1", block(t, "repository_drift"))
			if got := w.push("h2", noUnread(t, golden)); got != Closed {
				t.Fatalf("Pushed = %q, want %q", got, Closed)
			}
			is := w.gh.issues[11]
			if is.open || len(is.comments) != 1 || !strings.Contains(is.comments[0], "At commit h2, `main` has no error outside the exceptions") {
				t.Errorf("issue = %+v", is)
			}
			if r := w.repoRecord(); r.DriftIssue != 0 || r.DriftHead != "" {
				t.Errorf("record = %+v", r)
			}
			// The next drift opens a new issue.
			if got := w.push("h3", block(t, "repository_drift")); got != Opened || w.repoRecord().DriftIssue != 12 {
				t.Errorf("after the close: Pushed = %q, record %+v", got, w.repoRecord())
			}
		})
	}
}

// TestACleanStateWithUnreadPlacesKeepsTheIssueOpen: clean covers only what was read, so the issue says what was not.
func TestACleanStateWithUnreadPlacesKeepsTheIssueOpen(t *testing.T) {
	w := newWorld(t)
	w.push("h1", block(t, "repository_drift"))
	if got := w.push("h2", block(t, "repository_clean")); got != Updated {
		t.Fatalf("Pushed = %q, want %q", got, Updated)
	}
	is := w.gh.issues[11]
	if !is.open {
		t.Error("the issue was closed")
	}
	for _, want := range []string{"has no error outside the exceptions in what the check read",
		"- `web/app.ts:3`: import ./x does not resolve. To read it: add web/x.ts"} {
		if !strings.Contains(is.body, want) {
			t.Errorf("body has no %q:\n%s", want, is.body)
		}
	}
	if strings.Contains(is.body, "```text") {
		t.Errorf("the body still lists drift:\n%s", is.body)
	}
}

func TestNoBlockMeansNoState(t *testing.T) {
	w := newWorld(t)
	w.push("h1", block(t, "repository_drift"))
	w.gh.failOn = "CloseIssue"
	if got := w.push("h2", nil); got != None {
		t.Errorf("Pushed = %q, want %q", got, None)
	}
	if is := w.gh.issues[11]; !is.open {
		t.Error("a run without a block closed the issue")
	}
}

func TestNoDriftOpensNothing(t *testing.T) {
	w := newWorld(t)
	for i, b := range []*result.Repository{block(t, "repository_clean"), noUnread(t, "repository_clean"), block(t, "repository_expired")} {
		if got := w.push("h"+string(rune('1'+i)), b); got != None {
			t.Errorf("block %d: Pushed = %q, want %q", i, got, None)
		}
	}
	if len(w.gh.issues) != 0 {
		t.Errorf("issues = %+v", w.gh.issues)
	}
}

// TestASecondDeliveryClosesItsOwnIssue: two deliveries of one push open an issue each; the first recorded one stays.
func TestASecondDeliveryClosesItsOwnIssue(t *testing.T) {
	w := newWorld(t)
	w.gh.onCreate = func(n int) {
		if n != 11 {
			return
		}
		w.gh.onCreate = nil
		// The other delivery records its issue between this one's read and its record.
		if _, err := w.gh.CreateIssue(context.Background(), repo.RepoRef, api.NewIssue{Title: "first"}); err != nil {
			t.Fatal(err)
		}
		if _, err := records.DriftOpened(context.Background(), w.store, store.Tenant(installation), repoName, 12, "h1"); err != nil {
			t.Fatal(err)
		}
	}
	if got := w.push("h1", block(t, "repository_drift")); got != None {
		t.Errorf("Pushed = %q, want %q", got, None)
	}
	if is := w.gh.issues[11]; is.open || len(is.comments) != 1 || !strings.Contains(is.comments[0], "opened #12 first") {
		t.Errorf("second issue = %+v", is)
	}
	if r := w.repoRecord(); r.DriftIssue != 12 {
		t.Errorf("record = %+v", r)
	}
}

func TestAFailedWriteIsReturnedAndRecordsNothing(t *testing.T) {
	for _, method := range []string{"File", "CreateIssue"} {
		t.Run(method, func(t *testing.T) {
			w := newWorld(t)
			w.gh.failOn = method
			_, err := w.svc.Pushed(context.Background(), Run{Repo: repo, Head: "h1", State: block(t, "repository_drift")})
			if !errors.Is(err, errFake) {
				t.Errorf("error = %v", err)
			}
			if r := w.repoRecord(); r.DriftIssue != 0 {
				t.Errorf("record = %+v", r)
			}
		})
	}
	t.Run("a retried close finishes", func(t *testing.T) {
		w := newWorld(t)
		w.push("h1", block(t, "repository_drift"))
		w.gh.failOn = "CloseIssue"
		if _, err := w.svc.Pushed(context.Background(), Run{Repo: repo, Head: "h2", State: noUnread(t, "repository_clean")}); err == nil {
			t.Fatal("a failed close was not returned")
		}
		w.gh.failOn = ""
		if got := w.push("h2", noUnread(t, "repository_clean")); got != Closed {
			t.Errorf("retry: Pushed = %q, want %q", got, Closed)
		}
	})
}

// TestOwnersGitHubCannotAssignKeepTheLabel: an assignee without access makes GitHub refuse the whole edit.
func TestOwnersGitHubCannotAssignKeepTheLabel(t *testing.T) {
	w := newWorld(t)
	w.gh.assignErr = func(logins []string) error {
		if len(logins) > 0 {
			return api.ErrUnprocessable
		}
		return nil
	}
	if got := w.push("h1", block(t, "repository_drift")); got != Opened {
		t.Fatalf("Pushed = %q", got)
	}
	if is := w.gh.issues[11]; len(is.assignees) != 0 || !slices.Equal(is.labels, []string{Label}) {
		t.Errorf("issue = %+v", is)
	}
}

func TestTheBodyStaysWithinItsBounds(t *testing.T) {
	b := block(t, "repository_drift")
	e := b.Drift[0]
	b.Drift = nil
	for range 1000 {
		b.Drift = append(b.Drift, e)
	}
	for range 80 {
		b.Unread = append(b.Unread, result.UnreadPlace{Reason: "go is not installed", Fix: "install go"})
	}
	text := body(Run{Repo: repo, Head: "h1", State: b})
	if len(text) > 40000 {
		t.Errorf("body is %d bytes", len(text))
	}
	shown := strings.Count(text, "  next: ")
	if !strings.Contains(text, "\nand "+strconv.Itoa(1000-shown)+" more\n```") || shown == 0 || shown == 1000 {
		t.Errorf("%d records shown, and no line for the other %d", shown, 1000-shown)
	}
	for _, want := range []string{"- and 30 more places\n", "- the whole tree: go is not installed. To read it: install go\n"} {
		if !strings.Contains(text, want) {
			t.Errorf("body has no %q", want)
		}
	}
}

func TestTextsUseNoNotUsedWord(t *testing.T) {
	for name, text := range map[string]string{
		"title":     title(branch),
		"drift":     body(Run{Repo: repo, Head: "h1", State: block(t, "repository_drift")}),
		"unread":    body(Run{Repo: repo, Head: "h1", State: block(t, "repository_clean")}),
		"closed":    closedNote(branch, "h1"),
		"duplicate": duplicateNote(12),
	} {
		if words := wording.NotUsedIn(text); len(words) > 0 {
			t.Errorf("%s uses %v:\n%s", name, words, text)
		}
	}
}

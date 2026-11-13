package expiry

import (
	"context"
	"errors"
	"os"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/alexei-led/archfit-app/internal/gate/feedback"
	"github.com/alexei-led/archfit-app/internal/gate/records"
	"github.com/alexei-led/archfit-app/internal/github/api"
	"github.com/alexei-led/archfit-app/internal/store"
	"github.com/alexei-led/archfit-app/internal/wire/policy3"
	"github.com/alexei-led/archfit-app/internal/wire/result"
	"github.com/alexei-led/archfit-app/internal/wire/wording"
)

const (
	installation = 7
	repoName     = "acme/shop"
	branch       = "main"
	tip          = "tip1"
)

var repo = api.Repo{RepoRef: api.RepoRef{InstallationID: installation, FullName: repoName}, ID: 42, DefaultBranch: branch}

// policyWith is a valid schema 3 policy with one dated duplication exception and an undated graph exception.
func policyWith(expires string) string {
	return `version: 3
owners: ["@alice", bob, "@acme/architects"]
gate: required
modules:
  pkg_tc:
    paths: [pkg/tc/**]
  pkg_util:
    paths: [pkg/util/**]
exceptions:
  - rule: graph
    from: pkg_tc
    to: pkg_util
    why: Migration in progress.
  - rule: duplication
    from: pkg_tc
    to: pkg_util
    expires: ` + expires + `
    why: "Request #431 is open."
`
}

const noDated = `version: 3
owners: [alice]
modules:
  pkg_tc:
    paths: [pkg/tc/**]
`

// days is 2026-11-19 plus n days at 10:00 UTC: the exception of policyWith("2026-11-19") has its last day at n = 0.
func days(n int) time.Time { return time.Date(2026, 11, 19, 10, 0, 0, 0, time.UTC).AddDate(0, 0, n) }

type issue struct {
	title, body string
	open        bool
	comments    []string
	assignees   []string
	labels      []string
}

type fakeGitHub struct {
	policy   map[string]string // by ref
	head     string
	failErr  error
	issues   map[int]*issue
	next     int
	failOn   string
	onCreate func(number int)
}

var errFake = errors.New("fake GitHub failure")

func (f *fakeGitHub) File(_ context.Context, _ api.RepoRef, ref, path string) ([]byte, string, error) {
	if f.failOn == "File" {
		return nil, "", errFake
	}
	p, ok := f.policy[ref]
	if !ok || path != policyPath {
		return nil, "", api.ErrNotFound
	}
	return []byte(p), "blob", nil
}

func (f *fakeGitHub) BranchHead(_ context.Context, _ api.RepoRef, _ string) (string, error) {
	if f.failOn == "BranchHead" {
		return "", errFake
	}
	if f.head != "" {
		return f.head, nil
	}
	return tip, nil
}

func (f *fakeGitHub) CreateIssue(_ context.Context, _ api.RepoRef, i api.NewIssue) (api.Issue, error) {
	if f.failOn == "CreateIssue" {
		if f.failErr != nil {
			return api.Issue{}, f.failErr
		}
		return api.Issue{}, errFake
	}
	n := f.next
	f.next++
	f.issues[n] = &issue{title: i.Title, body: i.Body, open: true}
	if f.onCreate != nil {
		f.onCreate(n)
	}
	return api.Issue{Number: n, Body: i.Body}, nil
}

func (f *fakeGitHub) CloseIssue(_ context.Context, _ api.RepoRef, n int) error {
	if f.failOn == "CloseIssue" {
		if f.failErr != nil {
			return f.failErr
		}
		return errFake
	}
	f.issues[n].open = false
	return nil
}

func (f *fakeGitHub) CreateComment(_ context.Context, _ api.RepoRef, n int, body string) (api.Comment, error) {
	f.issues[n].comments = append(f.issues[n].comments, body)
	return api.Comment{}, nil
}

func (f *fakeGitHub) SetAssigneesAndLabels(_ context.Context, _ api.RepoRef, n int, assignees, labels []string) ([]string, []string, error) {
	f.issues[n].assignees, f.issues[n].labels = assignees, labels
	return assignees, labels, nil
}

type world struct {
	t     *testing.T
	gh    *fakeGitHub
	store store.Store
	svc   *Service
}

// newWorld is a repository whose tip holds policy, approved unless approved is false.
func newWorld(t *testing.T, policy string) *world {
	gh := &fakeGitHub{policy: map[string]string{tip: policy}, issues: map[int]*issue{}, next: 21}
	w := &world{t: t, gh: gh, store: store.NewMemory()}
	w.svc = New(gh, w.store)
	w.setRepo(records.Repo{ApprovedDigest: policy3.Digest([]byte(policy))})
	return w
}

func (w *world) setRepo(r records.Repo) {
	w.t.Helper()
	raw, err := records.Encode(&r)
	if err != nil {
		w.t.Fatal(err)
	}
	if err := w.store.Put(context.Background(), store.Tenant(installation), records.RepoKey(repoName), raw); err != nil {
		w.t.Fatal(err)
	}
}

func (w *world) repoRecord() records.Repo {
	w.t.Helper()
	var r records.Repo
	if err := records.Load(context.Background(), w.store, store.Tenant(installation), records.RepoKey(repoName), &r); err != nil {
		w.t.Fatal(err)
	}
	return r
}

// newPolicy puts policy at the tip as the approved one, keeping the recorded issues.
func (w *world) newPolicy(policy string) {
	w.gh.policy[tip] = policy
	r := w.repoRecord()
	r.ApprovedDigest = policy3.Digest([]byte(policy))
	w.setRepo(r)
}

func (w *world) check(now time.Time) *Approved {
	w.t.Helper()
	a, err := w.svc.Approved(context.Background(), repo, tip)
	if err != nil {
		w.t.Fatalf("Approved: %v", err)
	}
	if a != nil {
		if err := w.svc.Warn(context.Background(), repo, a, now); err != nil {
			w.t.Fatalf("Warn: %v", err)
		}
	}
	return a
}

func TestTheIssueOpensSevenDaysBeforeTheDate(t *testing.T) {
	w := newWorld(t, policyWith("2026-11-19"))
	if a := w.check(days(-8)); a == nil || len(a.Exceptions) != 1 || a.Advisory {
		t.Fatalf("Check = %+v", a)
	}
	if len(w.gh.issues) != 0 {
		t.Fatalf("an issue 8 days before: %+v", w.gh.issues)
	}
	w.check(days(-7))
	if len(w.gh.issues) != 1 {
		t.Fatalf("issues 7 days before = %d, want 1", len(w.gh.issues))
	}
	is := w.gh.issues[21]
	if is.title != "Archfit: exception duplication pkg_tc -> pkg_util expires on 2026-11-19" || !is.open {
		t.Errorf("issue = %+v", is)
	}
	for _, want := range []string{"holds until 2026-11-19 (7 days).", "Its reason: `Request #431 is open.`",
		"every pull request into `main` fails"} {
		if !strings.Contains(is.body, want) {
			t.Errorf("body has no %q:\n%s", want, is.body)
		}
	}
	if !slices.Equal(is.assignees, []string{"alice", "bob"}) || !slices.Equal(is.labels, []string{Label}) {
		t.Errorf("assignees %v, labels %v", is.assignees, is.labels)
	}
	if got := w.repoRecord().ExpiryIssues; len(got) != 1 || got["exception duplication pkg_tc pkg_util 2026-11-19"] != 21 {
		t.Errorf("expiry issues = %v", got)
	}
	for _, n := range []int{-6, 0, 1, 30} {
		w.check(days(n))
	}
	if len(w.gh.issues) != 1 || !w.gh.issues[21].open {
		t.Errorf("later passes changed the issues: %+v", w.gh.issues)
	}
}

func TestAnExceptionAlreadyPastItsDateGetsItsIssue(t *testing.T) {
	w := newWorld(t, policyWith("2026-11-19"))
	w.check(days(3))
	if is := w.gh.issues[21]; is == nil || !strings.Contains(is.body, "passed its date: 2026-11-19 (expired).") {
		t.Errorf("issues = %+v", w.gh.issues)
	}
}

func TestAnExtendedExceptionClosesItsIssue(t *testing.T) {
	w := newWorld(t, policyWith("2026-11-19"))
	w.check(days(-2))
	w.newPolicy(policyWith("2026-12-31"))
	w.check(days(-1))
	is := w.gh.issues[21]
	if is.open || len(is.comments) != 1 || is.comments[0] != "The policy now holds this exception until 2026-12-31. Closed by the App.\n" {
		t.Errorf("old issue = %+v", is)
	}
	if len(w.gh.issues) != 1 || w.repoRecord().ExpiryIssues != nil {
		t.Errorf("issues %+v, record %v", w.gh.issues, w.repoRecord().ExpiryIssues)
	}
	// The new date gets its own issue 7 days before it.
	w.check(time.Date(2026, 12, 24, 0, 0, 0, 0, time.UTC))
	if got := w.repoRecord().ExpiryIssues; got["exception duplication pkg_tc pkg_util 2026-12-31"] != 22 {
		t.Errorf("expiry issues = %v", got)
	}
}

func TestARemovedExceptionClosesItsIssue(t *testing.T) {
	w := newWorld(t, policyWith("2026-11-19"))
	w.check(days(1))
	w.newPolicy(noDated)
	if a := w.check(days(2)); a == nil || len(a.Exceptions) != 0 {
		t.Errorf("Check = %+v", a)
	}
	if is := w.gh.issues[21]; is.open || len(is.comments) != 1 || !strings.Contains(is.comments[0], "no longer holds this exception") {
		t.Errorf("issue = %+v", is)
	}
}

// TestCheckActsOnlyOnTheApprovedPolicy: a repository without an approved policy, outside the flow, or with a tip no
// judgement reached yet is unknown; nothing of it may pass or run again, and its issues stay as they are.
func TestCheckActsOnlyOnTheApprovedPolicy(t *testing.T) {
	recorded := map[string]int{"exception duplication pkg_tc pkg_util 2026-11-19": 21}
	for _, tc := range []struct {
		name string
		repo *records.Repo
		tip  string
	}{
		{"no repository record", nil, policyWith("2026-11-19")},
		{"no approved policy", &records.Repo{ExpiryIssues: recorded}, policyWith("2026-11-19")},
		{"outside the flow", &records.Repo{ApprovedDigest: policy3.Digest([]byte(policyWith("2026-11-19"))),
			OutsideFlow: &records.OutsideFlow{Digest: "d", Issue: 9}, ExpiryIssues: recorded}, policyWith("2026-11-19")},
		{"a tip that is not the approved policy", &records.Repo{ApprovedDigest: policy3.Digest([]byte(policyWith("2026-11-19"))),
			ExpiryIssues: recorded}, noDated},
		{"no policy at the tip", &records.Repo{ApprovedDigest: "sha256:aa", ExpiryIssues: recorded}, ""},
	} {
		t.Run(tc.name, func(t *testing.T) {
			gh := &fakeGitHub{policy: map[string]string{}, issues: map[int]*issue{21: {open: true}}, next: 22}
			if tc.tip != "" {
				gh.policy[tip] = tc.tip
			}
			w := &world{t: t, gh: gh, store: store.NewMemory()}
			w.svc = New(gh, w.store)
			if tc.repo != nil {
				w.setRepo(*tc.repo)
			}
			if a := w.check(days(3)); a != nil {
				t.Errorf("Check = %+v, want nil", a)
			}
			if len(gh.issues) != 1 || !gh.issues[21].open || len(gh.issues[21].comments) != 0 {
				t.Errorf("issues = %+v", gh.issues)
			}
		})
	}
}

func TestDue(t *testing.T) {
	w := newWorld(t, policyWith("2026-11-19"))
	xs := w.check(days(-30)).Exceptions
	judged := func(status, date string) records.Pull { return records.Pull{Status: status, Date: date} }
	for _, tc := range []struct {
		name  string
		rec   records.Pull
		today time.Time
		due   bool
	}{
		{"before the date", judged("pass", "2026-11-10"), days(-1), false},
		{"on the last day", judged("pass", "2026-11-10"), days(0), false},
		{"the day after", judged("pass", "2026-11-10"), days(1), true},
		{"a result judged on the last day", judged("fail", "2026-11-19"), days(1), true},
		{"a waiting result", judged("waiting", "2026-11-10"), days(5), true},
		{"a result judged after the date", judged("pass", "2026-11-20"), days(5), false},
		{"an unknown result", judged("unknown", "2026-11-10"), days(5), false},
		{"a result without a date", judged("pass", ""), days(5), false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			got := Due(xs, tc.rec, tc.today)
			if (len(got) == 1) != tc.due || len(got) > 1 {
				t.Errorf("Due = %+v, want due %v", got, tc.due)
			}
		})
	}
	if got := Due(Dated(mustDecode(t, noDated)), judged("pass", "2026-01-01"), days(5)); len(got) != 0 {
		t.Errorf("Due without dated exceptions = %+v", got)
	}
}

func mustDecode(t *testing.T, policy string) policy3.File {
	t.Helper()
	f, err := policy3.Decode([]byte(policy))
	if err != nil {
		t.Fatal(err)
	}
	return f
}

// TestResultIsTheEngineRecord: the App's record of an expired exception is the engine's golden, byte for byte.
func TestResultIsTheEngineRecord(t *testing.T) {
	xs := Dated(mustDecode(t, policyWith("2026-11-19")))
	got := Result(xs)
	golden, err := os.ReadFile("../../wire/result/testdata/record/expired_exception.txt")
	if err != nil {
		t.Fatal(err)
	}
	if text := result.Record(got); text != string(golden) {
		t.Errorf("record:\n%s\nwant:\n%s", text, golden)
	}
	raw, err := os.ReadFile("../../wire/result/testdata/result/expired_exception.json")
	if err != nil {
		t.Fatal(err)
	}
	engine, err := result.Decode(raw)
	if err != nil {
		t.Fatal(err)
	}
	if got.Status != engine.Status || got.Summary != engine.Summary || len(got.Errors) != 1 {
		t.Fatalf("result = %+v", got)
	}
	e, want := got.Errors[0], engine.Errors[0]
	if e.Path != want.Path || e.Rule != want.Rule || e.What != want.What || e.Why != want.Why || e.Next != want.Next ||
		e.From != want.From || e.To != want.To || e.Key != want.Key || e.FromChange != want.FromChange ||
		*e.Exception != *want.Exception || e.Line != want.Line {
		t.Errorf("error = %+v\nwant  %+v", e, want)
	}
}

func TestResultOfAnExceptionWithoutARequest(t *testing.T) {
	x := Exception{Rule: "unowned", From: "scripts/**", Expires: "2027-01-31", Why: "Scripts get a module next quarter."}
	r := Result([]Exception{x, x})
	want := "" +
		".archfit.yaml: exception: unowned scripts/** expired on 2027-01-31\n" +
		"  why: an exception with a date fails every change after that date\n" +
		"  next: fix the code, or ask an owner to extend the exception\n"
	if text := result.Record(r); text != want+want+"archfit: fail, 2 errors (not from this change)\n" {
		t.Errorf("record:\n%s", text)
	}
	if r.Errors[0].Key != "exception unowned scripts/**" {
		t.Errorf("key = %q", r.Errors[0].Key)
	}
}

// TestASecondPassClosesItsOwnIssue: two passes open an issue each; the first recorded one stays.
func TestASecondPassClosesItsOwnIssue(t *testing.T) {
	w := newWorld(t, policyWith("2026-11-19"))
	w.gh.onCreate = func(n int) {
		w.gh.onCreate = nil
		other, err := w.gh.CreateIssue(context.Background(), repo.RepoRef, api.NewIssue{Title: "first"})
		if err != nil {
			t.Fatal(err)
		}
		if _, err := records.ExpiryOpened(context.Background(), w.store, store.Tenant(installation), repoName,
			"exception duplication pkg_tc pkg_util 2026-11-19", other.Number); err != nil {
			t.Fatal(err)
		}
	}
	w.check(days(-1))
	if is := w.gh.issues[21]; is.open || len(is.comments) != 1 || !strings.Contains(is.comments[0], "opened #22") {
		t.Errorf("second issue = %+v", is)
	}
	if got := w.repoRecord().ExpiryIssues["exception duplication pkg_tc pkg_util 2026-11-19"]; got != 22 {
		t.Errorf("recorded issue = %d, want 22", got)
	}
}

func TestAFailedIssueWriteIsReturned(t *testing.T) {
	for _, method := range []string{"File", "CreateIssue"} {
		t.Run(method, func(t *testing.T) {
			w := newWorld(t, policyWith("2026-11-19"))
			w.gh.failOn = method
			a, err := w.svc.Approved(context.Background(), repo, tip)
			if err == nil {
				err = w.svc.Warn(context.Background(), repo, a, days(-1))
			}
			if !errors.Is(err, errFake) {
				t.Errorf("error = %v", err)
			}
			if r := w.repoRecord(); r.ExpiryIssues != nil {
				t.Errorf("recorded %v", r.ExpiryIssues)
			}
		})
	}
	t.Run("a failed close is tried again", func(t *testing.T) {
		w := newWorld(t, policyWith("2026-11-19"))
		w.check(days(-1))
		w.newPolicy(noDated)
		w.gh.failOn = "CloseIssue"
		a, err := w.svc.Approved(context.Background(), repo, tip)
		if err != nil {
			t.Fatal(err)
		}
		if err := w.svc.Warn(context.Background(), repo, a, days(0)); !errors.Is(err, errFake) {
			t.Fatalf("error = %v", err)
		}
		w.gh.failOn = ""
		w.check(days(0))
		if w.gh.issues[21].open || w.repoRecord().ExpiryIssues != nil {
			t.Errorf("issue %+v, record %v", w.gh.issues[21], w.repoRecord().ExpiryIssues)
		}
	})
}

// fakePublisher keeps the publication and runs its Current check, as the head's lease does.
type fakePublisher struct {
	feedback.Publisher4
	got []feedback.Publication4
}

func (p *fakePublisher) Publish4(ctx context.Context, pub feedback.Publication4) error {
	current, err := pub.Current(ctx)
	if err != nil {
		return err
	}
	if !current {
		return feedback.ErrStaleInputs
	}
	p.got = append(p.got, pub)
	return nil
}

func TestFailPublishesOverThePullRecordsRun(t *testing.T) {
	w := newWorld(t, policyWith("2026-11-19"))
	xs := w.check(days(1)).Exceptions
	rec := records.Pull{HeadSHA: "head7", RunID: 900, RunAttempt: 2, PayloadDigest: "sha256:pp", Status: "pass", Date: "2026-11-10"}
	raw, err := records.Encode(&rec)
	if err != nil {
		t.Fatal(err)
	}
	ctx, key := context.Background(), records.PullKey(repoName, 7)
	if err := w.store.Put(ctx, store.Tenant(installation), key, raw); err != nil {
		t.Fatal(err)
	}
	p := &fakePublisher{}
	f := Failure{Repo: repo, Pull: 7, Tip: tip, Record: rec, Expired: Due(xs, rec, days(1))}
	if err := w.svc.Fail(ctx, p, f); err != nil {
		t.Fatal(err)
	}
	if len(p.got) != 1 {
		t.Fatalf("publications = %d", len(p.got))
	}
	pub := p.got[0]
	if pub.Target.PullRequest != 7 || pub.Target.HeadSHA != "head7" || pub.RunID != 900 || pub.RunAttempt != 2 ||
		pub.Digest != "sha256:pp" || pub.Advisory || pub.Result.Status != result.StatusFail || len(pub.Result.Errors) != 1 {
		t.Errorf("publication = %+v", pub)
	}

	// An upload of a newer run lands first: nothing is written over it.
	newer := rec
	newer.RunID, newer.Date = 901, "2026-11-20"
	if raw, err = records.Encode(&newer); err != nil {
		t.Fatal(err)
	}
	if err := w.store.Put(ctx, store.Tenant(installation), key, raw); err != nil {
		t.Fatal(err)
	}
	if err := w.svc.Fail(ctx, p, f); !errors.Is(err, feedback.ErrStaleInputs) || len(p.got) != 1 {
		t.Errorf("Fail over a newer record = %v, %d publications", err, len(p.got))
	}
}

func TestTextsUseNoNotUsedWord(t *testing.T) {
	x := Dated(mustDecode(t, policyWith("2026-11-19")))[0]
	for name, text := range map[string]string{
		"title": title(x), "body": body(x, branch, days(-7)), "expired body": body(x, branch, days(2)),
		"extended": extendedNote("2026-12-31"), "removed": removedNote(branch), "duplicate": duplicateNote(22),
		"record": result.Record(Result([]Exception{x})),
	} {
		if words := wording.NotUsedIn(text); len(words) > 0 {
			t.Errorf("%s uses %v:\n%s", name, words, text)
		}
	}
}

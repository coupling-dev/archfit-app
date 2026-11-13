// Package expiry gives an exception's date its teeth (design 6.4, M2 D19, task 22): an issue to the owners from 7 days
// before the date, and, from the day after it, the expired-exception record on every open pull request whose result
// did not judge it. The sweep runs it; the re-run under the guard of design 10.5 comes first, and Fail only where the
// guard refuses. Reading the approved policy (Approved) and the issues (Warn) are apart, so a failed notification
// never stops the date from reaching the pull requests.
package expiry

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"maps"
	"regexp"
	"slices"
	"strings"
	"time"

	"github.com/alexei-led/archfit-app/internal/gate/feedback"
	"github.com/alexei-led/archfit-app/internal/gate/inputs"
	"github.com/alexei-led/archfit-app/internal/gate/records"
	"github.com/alexei-led/archfit-app/internal/github/api"
	"github.com/alexei-led/archfit-app/internal/store"
	"github.com/alexei-led/archfit-app/internal/wire/policy3"
	"github.com/alexei-led/archfit-app/internal/wire/result"
)

// policyPath is the policy file on the default branch.
const policyPath = ".archfit.yaml"

// gateRequired is the policy's gate value that makes a fail block; any other shows it as neutral.
const gateRequired = "required"

// Label marks an expiry issue.
const Label = "archfit-expiry"

// WarnDays is how many days before an exception's date its issue opens (M2 D19).
const WarnDays = 7

// maxAssignees is GitHub's limit of assignees per issue.
const maxAssignees = 10

// The engine's words for an expired exception (archfit internal/assessment/count), so the record the App writes is the
// record the check prints (M2 D19).
const (
	ruleException  = "exception"
	whyExpired     = "an exception with a date fails every change after that date"
	nextExtendText = "fix the code, or ask an owner to extend the exception"
	nextWaitText   = "wait for an owner: #"
)

// requestNumber finds the request an exception's why names, such as "#431", as the engine does.
var requestNumber = regexp.MustCompile(`#([0-9]+)`)

// acceptedStatuses are the statuses of a result the App accepted as it is: only those judged a date.
var acceptedStatuses = []string{string(result.StatusPass), string(result.StatusFail), string(result.StatusWaiting)}

// Exception is a dated exception of the approved policy.
type Exception struct {
	Rule, From, To, Why string
	Expires             string // the last day the exception holds, YYYY-MM-DD
}

// Key is the key of the engine's error for the exception past its date: "exception <rule> <from> [<to>]".
func (x Exception) Key() string {
	parts := slices.DeleteFunc([]string{ruleException, x.Rule, x.From, x.To}, func(p string) bool { return p == "" })
	return strings.Join(parts, " ")
}

// issueKey names the exception and its date in Repo.ExpiryIssues: an extended date is another issue.
func (x Exception) issueKey() string { return x.Key() + " " + x.Expires }

// cutDate splits an issue key into the exception's key and its date.
func cutDate(issueKey string) (key, date string) {
	i := strings.LastIndexByte(issueKey, ' ')
	if i < 0 {
		return issueKey, ""
	}
	return issueKey[:i], issueKey[i+1:]
}

// daysLeft is how many UTC days are left from today to the last day: 0 on that day, negative after it. A date that does
// not parse has no last day (ok false): it never expires, as an exception without a date.
func (x Exception) daysLeft(today time.Time) (left int, ok bool) {
	last, err := time.Parse(time.DateOnly, x.Expires)
	if err != nil {
		return 0, false
	}
	return int(last.Sub(day(today)).Hours() / 24), true
}

// Expired reports whether today is past the exception's last day, in UTC calendar days, as the engine judges it.
func (x Exception) Expired(today time.Time) bool {
	left, ok := x.daysLeft(today)
	return ok && left < 0
}

// day is 00:00 UTC of the calendar day t falls on in UTC.
func day(t time.Time) time.Time {
	y, m, d := t.UTC().Date()
	return time.Date(y, m, d, 0, 0, 0, 0, time.UTC)
}

// Dated returns the exceptions of pol that have a date, in policy order. A date that does not parse is left out: the
// policy decoder refuses it, so it never reaches the default branch through the flow.
func Dated(pol policy3.File) []Exception {
	var out []Exception
	for _, e := range pol.Exceptions {
		x := Exception{Rule: e.Rule, From: e.From, To: e.To, Why: e.Why, Expires: e.Expires}
		if _, ok := x.daysLeft(time.Time{}); ok {
			out = append(out, x)
		}
	}
	return out
}

// Due returns the exceptions of xs past their date on today that the result of the pull record rec did not judge: a
// result the App accepted as it is (pass, fail or waiting), judged on or before the exception's last day. A result
// judged after it is the engine's own judgement of the date and stands; an unknown result keeps the record that says
// what to restore first.
func Due(xs []Exception, rec records.Pull, today time.Time) []Exception {
	if !slices.Contains(acceptedStatuses, rec.Status) || rec.Date == "" {
		return nil
	}
	var out []Exception
	for _, x := range xs {
		if x.Expired(today) && rec.Date <= x.Expires {
			out = append(out, x)
		}
	}
	return out
}

// Result is what a pull request shows when an exception passed its date and its result cannot be judged again: the
// engine's record of each expired exception (golden expired_exception), status fail.
func Result(xs []Exception) result.Result {
	errs := make([]result.Error, 0, len(xs))
	for _, x := range xs {
		place := x.From
		if x.To != "" {
			place += " -> " + x.To
		}
		errs = append(errs, result.Error{
			Path: policyPath, Rule: ruleException, What: x.Rule + " " + place + " expired on " + x.Expires,
			Why: whyExpired, Next: next(x), From: x.From, To: x.To, Key: x.Key(),
			Exception: &result.Exception{Rule: x.Rule, Expires: x.Expires},
		})
	}
	noun := "errors"
	if len(errs) == 1 {
		noun = "error"
	}
	return result.Result{Schema: result.SchemaV1, Status: result.StatusFail, Errors: errs,
		Summary: fmt.Sprintf("archfit: fail, %d %s (not from this change)", len(errs), noun)}
}

// next is the step after an exception's date: wait for the request its why names, else fix the code or have it
// extended (the engine's nextExpired).
func next(x Exception) result.Next {
	if m := requestNumber.FindStringSubmatch(x.Why); m != nil {
		return result.Next{Kind: "wait", Text: nextWaitText + m[1]}
	}
	return result.Next{Kind: "fix", Text: nextExtendText}
}

// GitHub is what the expiry issues need.
type GitHub interface {
	File(ctx context.Context, r api.RepoRef, ref, path string) ([]byte, string, error)
	BranchHead(ctx context.Context, r api.RepoRef, branch string) (string, error)
	CreateIssue(ctx context.Context, r api.RepoRef, i api.NewIssue) (api.Issue, error)
	CloseIssue(ctx context.Context, r api.RepoRef, number int) error
	CreateComment(ctx context.Context, r api.RepoRef, number int, body string) (api.Comment, error)
	SetAssigneesAndLabels(ctx context.Context, r api.RepoRef, number int, assignees, labels []string) ([]string, []string, error)
}

// Service keeps the expiry issues and publishes the expired-exception record.
type Service struct {
	gh    GitHub
	store store.Store
}

// New returns a Service over gh and s.
func New(gh GitHub, s store.Store) *Service { return &Service{gh: gh, store: s} }

// Approved is the approved policy at the default branch's tip, as far as expiry reads it.
type Approved struct {
	Tip        string      // the commit read: the date's record is published only while it is the tip
	Exceptions []Exception // the dated exceptions, in policy order
	Advisory   bool        // the policy has no gate: required, so a fail shows as neutral
	owners     []string
	issues     map[string]int // the recorded expiry issues when read
}

// Approved reads the policy at tip and returns it only when it is the approved policy (Repo.ApprovedDigest, not
// outside the flow). It returns nil otherwise: a repository without an approved policy, or outside the flow, is
// unknown, and nothing of it may pass or run again. It writes nothing, so a failed notification never stops the date
// from reaching the pull requests. An error is a failed read; the caller tries again on its next pass.
func (s *Service) Approved(ctx context.Context, r api.Repo, tip string) (*Approved, error) {
	var rec records.Repo
	err := records.Load(ctx, s.store, store.Tenant(r.InstallationID), records.RepoKey(r.FullName), &rec)
	switch {
	case errors.Is(err, records.ErrNoRecord):
		return nil, nil
	case err != nil:
		return nil, fmt.Errorf("expiry: read the repository record: %w", err)
	case rec.ApprovedDigest == "" || rec.OutsideFlow != nil:
		return nil, nil
	}
	raw, _, err := s.gh.File(ctx, r.RepoRef, tip, policyPath)
	switch {
	case errors.Is(err, api.ErrNotFound):
		return nil, nil
	case err != nil:
		return nil, fmt.Errorf("expiry: read the policy: %w", err)
	}
	if policy3.Digest(raw) != rec.ApprovedDigest {
		return nil, nil // a change no judgement has reached yet: the push judgement decides first
	}
	pol, err := policy3.Decode(raw)
	if err != nil {
		return nil, nil
	}
	return &Approved{Tip: tip, Exceptions: Dated(pol), Advisory: pol.Gate != gateRequired, owners: pol.Owners,
		issues: rec.ExpiryIssues}, nil
}

// Warn keeps the expiry issues of a: one issue for each dated exception from WarnDays before its date, assigned to the
// owners, and each recorded issue whose exception or date a no longer holds (extended or removed) closed with a note.
// A GitHub refusal that will not change (issues turned off, an issue deleted or transferred) is logged, and a recorded
// issue that is gone is forgotten. Any other failed write is returned and the rest wait for the next pass; enforcement
// never depends on it.
func (s *Service) Warn(ctx context.Context, r api.Repo, a *Approved, now time.Time) error {
	held := map[string]bool{}
	for _, x := range a.Exceptions {
		held[x.issueKey()] = true
		if left, _ := x.daysLeft(now); left > WarnDays || a.issues[x.issueKey()] != 0 {
			continue
		}
		if err := s.open(ctx, r, x, a.owners, now); err != nil {
			return err
		}
	}
	for _, key := range slices.Sorted(maps.Keys(a.issues)) {
		if !held[key] {
			if err := s.close(ctx, r, key, a.issues[key], a.Exceptions); err != nil {
				return err
			}
		}
	}
	return nil
}

// open opens the issue of x on GitHub, then records it; a second issue of a concurrent pass is closed with a note.
// GitHub's refusal for good (issues turned off) is logged: a notification never stops the pass.
func (s *Service) open(ctx context.Context, r api.Repo, x Exception, owners []string, now time.Time) error {
	created, err := s.gh.CreateIssue(ctx, r.RepoRef, api.NewIssue{Title: title(x), Body: body(x, r.DefaultBranch, now)})
	switch {
	case api.Permanent(err):
		logFailure(ctx, r, "open the issue", err)
		return nil
	case err != nil:
		return fmt.Errorf("expiry: open the issue: %w", err)
	}
	recorded, err := records.ExpiryOpened(ctx, s.store, store.Tenant(r.InstallationID), r.FullName, x.issueKey(), created.Number)
	if err != nil {
		// The next pass opens the issue again, so this one, which nothing records, closes.
		if cerr := s.gh.CloseIssue(ctx, r.RepoRef, created.Number); cerr != nil {
			logFailure(ctx, r, "close an issue not recorded", cerr)
		}
		return fmt.Errorf("expiry: record the issue: %w", err)
	}
	if recorded != created.Number {
		s.note(ctx, r, created.Number, duplicateNote(recorded))
		if err := s.gh.CloseIssue(ctx, r.RepoRef, created.Number); err != nil {
			logFailure(ctx, r, "close a second issue", err)
		}
		return nil
	}
	s.triage(ctx, r, created.Number, owners)
	return nil
}

// close closes the recorded issue of key, whose exception the policy no longer holds with that date, then forgets it.
// An issue GitHub no longer has is only forgotten.
func (s *Service) close(ctx context.Context, r api.Repo, key string, issue int, held []Exception) error {
	err := s.gh.CloseIssue(ctx, r.RepoRef, issue)
	if err != nil && !api.Permanent(err) {
		return fmt.Errorf("expiry: close the issue: %w", err)
	}
	if err := records.ExpiryClosed(ctx, s.store, store.Tenant(r.InstallationID), r.FullName, key, issue); err != nil {
		return fmt.Errorf("expiry: record the closed issue: %w", err)
	}
	if err != nil {
		logFailure(ctx, r, "close the issue", err) // GitHub no longer has it: forgotten
		return nil
	}
	note := removedNote(r.DefaultBranch)
	exception, _ := cutDate(key)
	for _, x := range held {
		if x.Key() == exception {
			note = extendedNote(x.Expires)
		}
	}
	s.note(ctx, r, issue, note)
	return nil
}

// triage assigns the owners and sets the label. It is a notification, never authority: a failure is logged. Owners
// GitHub cannot assign leave the label alone, as for requests.
func (s *Service) triage(ctx context.Context, r api.Repo, issue int, owners []string) {
	var logins []string
	for _, o := range owners {
		login := strings.TrimPrefix(strings.TrimSpace(o), "@")
		if login != "" && !strings.Contains(login, "/") && !slices.Contains(logins, login) && len(logins) < maxAssignees {
			logins = append(logins, login)
		}
	}
	_, _, err := s.gh.SetAssigneesAndLabels(ctx, r.RepoRef, issue, logins, []string{Label})
	if errors.Is(err, api.ErrUnprocessable) {
		_, _, err = s.gh.SetAssigneesAndLabels(ctx, r.RepoRef, issue, nil, []string{Label})
	}
	if err != nil {
		logFailure(ctx, r, "assign the owners", err)
	}
}

func (s *Service) note(ctx context.Context, r api.Repo, issue int, text string) {
	if _, err := s.gh.CreateComment(ctx, r.RepoRef, issue, text); err != nil {
		logFailure(ctx, r, "note on the issue", err)
	}
}

// Failure is the expired-exception record of one open pull request whose re-run the guard of design 10.5 refused.
type Failure struct {
	Repo     api.Repo
	Pull     int
	Tip      string       // Approved.Tip: the policy the dates were read from
	Record   records.Pull // the pull record read; the publication keeps its run identity
	Expired  []Exception  // Due for Record
	Advisory bool
	// Current adds caller-owned freshness checks inside the same publication lease.
	Current func(context.Context) (bool, error)
}

// Fail publishes Result(f.Expired) on the pull request's head over the run of its pull record, so the next upload of a
// newer run replaces it. Inside the head's lease the default branch's head and the pull record are read again: when a
// push moved the tip (an extension may have merged) or another upload changed the record, nothing is written
// (feedback.ErrStaleInputs) and the next pass decides from what is current.
func (s *Service) Fail(ctx context.Context, p feedback.Publisher4, f Failure) error {
	r, rec := f.Repo, f.Record
	return p.Publish4(ctx, feedback.Publication4{
		Target: inputs.Target{Repo: r, PullRequest: f.Pull, HeadSHA: rec.HeadSHA, BaseRef: r.DefaultBranch},
		Source: "sweep", Generated: true,
		RunID:      rec.RunID,
		RunAttempt: rec.RunAttempt,
		Digest:     rec.PayloadDigest,
		Advisory:   f.Advisory,
		Result:     Result(f.Expired),
		Current: func(ctx context.Context) (bool, error) {
			if f.Current != nil {
				ok, err := f.Current(ctx)
				if err != nil || !ok {
					return ok, err
				}
			}
			tip, err := s.gh.BranchHead(ctx, r.RepoRef, r.DefaultBranch)
			if err != nil || tip != f.Tip {
				return false, err
			}
			var now records.Pull
			err = records.Load(ctx, s.store, store.Tenant(r.InstallationID), records.PullKey(r.FullName, f.Pull), &now)
			if err != nil {
				return false, err
			}
			return now.HeadSHA == rec.HeadSHA && now.RunID == rec.RunID && now.RunAttempt == rec.RunAttempt &&
				now.PayloadDigest == rec.PayloadDigest && now.Status == rec.Status, nil
		},
	})
}

// logFailure logs a failed notification step: ids and the HTTP status, never a name.
func logFailure(ctx context.Context, r api.Repo, step string, err error) {
	slog.Default().WarnContext(ctx, "expiry step failed", "installation_id", r.InstallationID,
		"repository_id", r.ID, "step", step, "status", api.Status(err), "rate_limited", errors.Is(err, api.ErrRateLimited))
}

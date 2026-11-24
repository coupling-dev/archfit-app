// Package requests turns a request line into a request issue and derives the status waiting (design 7.1, 10.3; M2
// task 16). A comment `/archfit request <why>` on a pull request asks for the errors of the pull request's latest
// verified result; an issue the CLI opened from a `can` answer asks before a pull request exists. A request is a
// GitHub issue, its number is the request id, and the App keeps it under gate/records' keys.
package requests

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"slices"
	"strconv"
	"strings"
	"time"

	"github.com/coupling-dev/archfit-app/internal/gate/feedback"
	"github.com/coupling-dev/archfit-app/internal/gate/inputs"
	"github.com/coupling-dev/archfit-app/internal/gate/records"
	"github.com/coupling-dev/archfit-app/internal/github/api"
	"github.com/coupling-dev/archfit-app/internal/store"
	"github.com/coupling-dev/archfit-app/internal/wire/envelope"
	"github.com/coupling-dev/archfit-app/internal/wire/manifest"
	"github.com/coupling-dev/archfit-app/internal/wire/policy3"
	"github.com/coupling-dev/archfit-app/internal/wire/request"
	"github.com/coupling-dev/archfit-app/internal/wire/result"
	"github.com/coupling-dev/archfit-app/internal/wire/workflow"
)

const (
	// ArtifactName is the artifact of a check run that holds the result bytes the upload carried (M2 D6).
	ArtifactName = "archfit-result"
	// MaxNewPerComment is the most requests one request line opens; the reply asks again for the rest.
	MaxNewPerComment = 10

	// maxResultBytes is the upload's size cap: no result the App accepted is larger.
	maxResultBytes = 5 << 20
	policyPath     = ".archfit.yaml"
	workflowPath   = ".github/workflows/archfit.yaml"
	gateRequired   = "required"
	stateOpen      = "open"
	// maxAssignees is GitHub's limit of assignees per issue.
	maxAssignees = 10
	// republishTries bounds the re-reads when a newer upload of the pull request lands meanwhile.
	republishTries = 3
)

// Reactions, as the gate logs them.
const (
	ReactionNone     = "none"             // not a request
	ReactionIgnored  = "request_ignored"  // a pull request the App does not judge: closed, or into another base
	ReactionRefused  = "request_refused"  // one reply says why
	ReactionAsked    = "request_asked"    // requests opened or joined
	ReactionAnswered = "request_answered" // a deny rule already answers it, or the same request was asked
	ReactionFiled    = "request_filed"    // a CLI issue became a request
	ReactionHeld     = "request_held"     // posted before the head had a checked result: filed when it lands
)

// ReactionOtherEndpoint is the reaction to a request line when the default branch's workflow does not send its
// results to this App: no reply, the log says why.
const ReactionOtherEndpoint = "request_other_endpoint"

// The permissions that may ask without having written the pull request (M2 D21).
var mayAsk = []string{"admin", "write"}

// GitHub is what the requests read and write on GitHub.
type GitHub interface {
	File(ctx context.Context, r api.RepoRef, ref, path string) (content []byte, blobSHA string, err error)
	BranchHead(ctx context.Context, r api.RepoRef, branch string) (string, error)
	Pull(ctx context.Context, r api.RepoRef, number int) (api.Pull, error)
	Artifact(ctx context.Context, r api.RepoRef, runID int64, name string, maxBytes int64) ([]byte, error)
	CreateComment(ctx context.Context, r api.RepoRef, number int, body string) (api.Comment, error)
	CreateIssue(ctx context.Context, r api.RepoRef, i api.NewIssue) (api.Issue, error)
	UpdateIssueBody(ctx context.Context, r api.RepoRef, number int, body string) error
	CloseIssue(ctx context.Context, r api.RepoRef, number int) error
	SetAssigneesAndLabels(ctx context.Context, r api.RepoRef, number int, assignees, labels []string) ([]string, []string, error)
	CollaboratorPermission(ctx context.Context, r api.RepoRef, login string) (string, error)
}

// Service takes in request lines.
type Service struct {
	gh        GitHub
	store     store.Store
	publisher feedback.Publisher4
	appURL    string
}

// New returns the service. publisher is the gate's Archfit 4 publisher, so a waiting check shares the head's lease
// with uploads. appURL is the App's base URL: a request line is answered only when the workflow sends its results
// there.
func New(gh GitHub, s store.Store, p feedback.Publisher4, appURL string) *Service {
	return &Service{gh: gh, store: s, publisher: p, appURL: appURL}
}

// Comment is a comment created on a pull request.
type Comment struct {
	Repo      api.Repo // RepoRef and DefaultBranch, from the event
	Pull      int
	Author    string
	Body      string
	Today     time.Time // the App's date
	CreatedAt time.Time
}

// asker is who filed a request and when.
type asker struct {
	login string
	when  time.Time
}

func (a asker) at() string {
	if a.when.IsZero() {
		return ""
	}
	return a.when.UTC().Format(time.RFC3339)
}

// IsRequest reports whether a comment holds a request line, usable or not.
func IsRequest(body string) bool {
	_, ok, _ := request.ParseComment(body, time.Time{})
	return ok
}

// Comment takes in the request line of a pull-request comment. A comment without one is ReactionNone. On a pull
// request the App does not judge it does nothing. Else it posts one reply: why the line is refused, or which
// requests it opened or joined and which a deny rule already answers. An error is a failed read or write; the caller
// retries the delivery, and a request already open is joined again without a change.
func (s *Service) Comment(ctx context.Context, c Comment) (string, error) {
	line, ok, lineErr := request.ParseComment(c.Body, c.Today)
	if !ok {
		return ReactionNone, nil
	}
	return s.take(ctx, c, line, lineErr, nil)
}

// take judges a parsed request line. held is the held line it files (Release), nil for a comment just posted: a line
// without a checked result for the head is then held, and a held one is not held again.
func (s *Service) take(ctx context.Context, c Comment, line request.Line, lineErr error, held *records.HeldRequest) (string, error) {
	ref := c.Repo.RepoRef
	pl, err := s.gh.Pull(ctx, ref, c.Pull)
	if errors.Is(err, api.ErrNotFound) {
		return ReactionIgnored, nil
	}
	if err != nil {
		return ReactionNone, fmt.Errorf("requests: read the pull request: %w", err)
	}
	if pl.State != stateOpen || c.Repo.DefaultBranch == "" || pl.BaseRef != c.Repo.DefaultBranch {
		return ReactionIgnored, nil
	}
	here, err := s.reportsHere(ctx, c.Repo, c.Pull)
	if err != nil {
		return ReactionNone, err
	}
	if !here {
		return ReactionOtherEndpoint, nil
	}
	allowed := strings.EqualFold(c.Author, pl.Author)
	if !allowed {
		if allowed, err = s.writer(ctx, ref, c.Author); err != nil {
			return ReactionNone, err
		}
	}
	if !allowed {
		return s.refuse(ctx, ref, c.Pull, request.NoPermission().Reply())
	}
	if rejection := (*request.Rejection)(nil); errors.As(lineErr, &rejection) {
		return s.refuse(ctx, ref, c.Pull, rejection.Reply())
	}

	if held != nil && held.HeadSHA != pl.HeadSHA {
		return s.refuse(ctx, ref, c.Pull, textHeldNewHead)
	}

	rec, res, err := s.latest(ctx, c.Repo, c.Pull)
	noResult := errors.Is(err, errNoResult) || err == nil && rec.HeadSHA != pl.HeadSHA
	switch {
	case noResult && held == nil && !checked(rec, pl.HeadSHA):
		return s.hold(ctx, c, pl.HeadSHA, line)
	case noResult:
		return s.refuse(ctx, ref, c.Pull, textNoResult)
	case err != nil:
		return ReactionNone, err
	case held != nil && res.Status == result.StatusPass:
		return s.refuse(ctx, ref, c.Pull, textHeldPass)
	}
	var asks, others []result.Error
	for _, e := range res.Errors {
		if Asks(e) {
			asks = append(asks, e)
		} else {
			others = append(others, e)
		}
	}
	if len(asks) == 0 {
		return s.refuse(ctx, ref, c.Pull, request.NothingToAsk(recordsOf(others)).Reply())
	}
	pol, found, err := s.policy(ctx, c.Repo)
	if err != nil {
		return ReactionNone, err
	}
	if !found {
		return s.refuse(ctx, ref, c.Pull, textNoPolicy)
	}

	var o outcome
	for _, g := range groups(asks) {
		if a, ok := denied(pol, g.errs[0]); ok {
			o.answered = append(o.answered, a)
			continue
		}
		open, err := OpenIndex(ctx, s.store, c.Repo)
		if err != nil {
			return ReactionNone, err
		}
		if id, ok := openIssue(open, g.key); ok {
			if err := s.joinPull(ctx, c.Repo, id, c.Pull, g); err != nil {
				return ReactionNone, err
			}
			o.joined = append(o.joined, id)
			continue
		}
		if len(o.opened) == MaxNewPerComment {
			o.left++
			continue
		}
		id, joined, err := s.open(ctx, c.Repo, pol, g, line, c.Pull, asker{c.Author, c.CreatedAt})
		if err != nil {
			return ReactionNone, err
		}
		if joined {
			o.joined = append(o.joined, id)
		} else {
			o.opened = append(o.opened, id)
		}
	}
	if err := s.republish(ctx, c.Repo, c.Pull, pol.Gate != gateRequired); err != nil {
		return ReactionNone, err
	}
	if err := s.post(ctx, ref, c.Pull, o.reply(pol.Gate == gateRequired)); err != nil {
		return ReactionNone, fmt.Errorf("requests: reply: %w", err)
	}
	if len(o.opened)+len(o.joined) == 0 {
		return ReactionAnswered, nil
	}
	return ReactionAsked, nil
}

// reportsHere reports whether the workflow at the default branch's tip sends its results to this App: its Action
// step's `endpoint` is the App's base URL, byte for byte, as the upload's audience is. The default branch is read, as
// for the policy, so a pull request cannot silence the App by changing its own workflow. With two Apps on one
// repository only the one that gets the results answers; the other stays silent and logs why. A workflow without an
// endpoint sends nothing (the Action's default is check only), so no App answers. An expression cannot be compared,
// so the App answers as it did before it read the workflow.
func (s *Service) reportsHere(ctx context.Context, r api.Repo, pull int) (bool, error) {
	tip, err := s.gh.BranchHead(ctx, r.RepoRef, r.DefaultBranch)
	if err != nil {
		return false, fmt.Errorf("requests: read the default branch head: %w", err)
	}
	raw, _, err := s.gh.File(ctx, r.RepoRef, tip, workflowPath)
	if err != nil && !errors.Is(err, api.ErrNotFound) {
		return false, fmt.Errorf("requests: read the workflow: %w", err)
	}
	endpoint, ok := workflow.ActionEndpoint(raw, manifest.ActionRepository)
	log := func(msg string) {
		slog.Default().InfoContext(ctx, msg, "installation_id", r.InstallationID, "repository", r.FullName,
			"pull", pull, "workflow_endpoint", endpoint, "app_url", s.appURL)
	}
	switch {
	case ok && endpoint == s.appURL:
		return true, nil
	case ok && strings.Contains(endpoint, "${{"):
		log("request answered: the workflow endpoint is an expression")
		return true, nil
	}
	log("request not answered: the workflow does not send its results to this App")
	return false, nil
}

// group is the asked errors of one request key, in record order.
type group struct {
	key  string
	errs []result.Error
}

// groups splits errors by request key, in the order each key first appears; an error without a key asks nothing.
func groups(errs []result.Error) []group {
	var out []group
	for _, e := range errs {
		key, err := Key(e)
		if err != nil {
			continue
		}
		i := slices.IndexFunc(out, func(g group) bool { return g.key == key })
		if i < 0 {
			out = append(out, group{key: key})
			i = len(out) - 1
		}
		out[i].errs = append(out[i].errs, e)
	}
	return out
}

// openIssue is the issue of the open request with key.
func openIssue(open *records.Index, key string) (int, bool) {
	id, ok := open.Lookup(key)
	if !ok {
		return 0, false
	}
	n, err := strconv.Atoi(id)
	return n, err == nil && n > 0
}

// denied reports whether a deny rule of the policy names both modules of the error's dependency: an owner already
// answered it (design 6.3). Rules are matched by module name; a rule written with path globs is not matched.
func denied(pol policy3.File, e result.Error) (answered, bool) {
	if e.From == "" || e.To == "" {
		return answered{}, false
	}
	return deniedPair(pol, e.From, e.To)
}

func deniedPair(pol policy3.File, from, to string) (answered, bool) {
	for _, r := range pol.Rules {
		if r.Deny != nil && slices.Contains(r.Deny.From, from) && slices.Contains(r.Deny.To, to) {
			return answered{what: from + " -> " + to, rule: r.ID, instead: r.Instead}, true
		}
	}
	return answered{}, false
}

// open opens the request issue of g, assigns the owners and labels it, then stores the request with its index entry.
// When another request took the key meanwhile, the new issue is closed with a note and pull joins the other: joined is
// true and id is the other's.
func (s *Service) open(ctx context.Context, r api.Repo, pol policy3.File, g group, line request.Line, pull int, by asker) (id int, joined bool, err error) {
	e := g.errs[0]
	req := records.Request{Key: g.key, Reason: line.Reason, Expires: line.Expires, Rule: e.Rule, Path: e.Path,
		From: e.From, To: e.To, Record: recordsOf(g.errs), Waiting: []int{pull}, Listed: []int{pull},
		Numbers: records.Numbers{Verified: true, Balance: balance(e.Balance)}, Surface: groupSurface(g.errs),
		AskedBy: by.login, AskedAt: by.at()}
	issue := issueOf(req)
	created, err := s.gh.CreateIssue(ctx, r.RepoRef, api.NewIssue{Title: issue.Title(), Body: issue.Body()})
	if err != nil {
		return 0, false, fmt.Errorf("requests: open the request issue: %w", err)
	}
	s.triage(ctx, r.RepoRef, created.Number, pol.Owners)
	other, err := put(ctx, s.store, r, created.Number, req)
	if errors.Is(err, records.ErrExists) && other > 0 {
		return other, true, s.duplicate(ctx, r, created.Number, other, pull, g)
	}
	if err != nil {
		return 0, false, fmt.Errorf("requests: store request %d: %w", created.Number, err)
	}
	return created.Number, false, nil
}

// duplicate closes issue, opened for a key another request holds, and lets pull join the other.
func (s *Service) duplicate(ctx context.Context, r api.Repo, issue, other, pull int, g group) error {
	if err := s.post(ctx, r.RepoRef, issue, duplicateReply(other)); err != nil {
		return fmt.Errorf("requests: note a duplicate request: %w", err)
	}
	if err := s.gh.CloseIssue(ctx, r.RepoRef, issue); err != nil {
		return fmt.Errorf("requests: close a duplicate request: %w", err)
	}
	return s.joinPull(ctx, r, other, pull, g)
}

// joinPull lets pull join the open request id: it waits on it, and its verified numbers replace numbers "as
// reported". Until the issue body lists pull, the body is rendered again and the issue gets one note; then the request
// records what the body lists. An upload may have joined pull in the store alone (Wait), and a failed edit is done
// again on a retry, because only a done edit records the list.
func (s *Service) joinPull(ctx context.Context, r api.Repo, id, pull int, g group) error {
	req, _, _, err := join(ctx, s.store, r, id, pull, &confirmation{record: recordsOf(g.errs), balance: balance(g.errs[0].Balance),
		surface: groupSurface(g.errs)})
	if errors.Is(err, errNotOpen) {
		return nil
	}
	if err != nil || slices.Contains(req.Listed, pull) {
		return err
	}
	issue := issueOf(req)
	if err := s.gh.UpdateIssueBody(ctx, r.RepoRef, id, issue.Body()); err != nil {
		return fmt.Errorf("requests: edit request %d: %w", id, err)
	}
	if err := s.post(ctx, r.RepoRef, id, joinedNote(pull)); err != nil {
		return fmt.Errorf("requests: note on request %d: %w", id, err)
	}
	return listed(ctx, s.store, r, id, issue.Pulls)
}

// triage assigns the policy's owners to an issue and labels it. It is a notification, never authority: a failure is
// logged with identifiers only, and the request still counts. A 422 (an owner GitHub cannot assign) is tried again
// with the label alone.
func (s *Service) triage(ctx context.Context, ref api.RepoRef, issue int, owners []string) {
	var logins []string
	for _, o := range owners {
		if login := strings.TrimPrefix(strings.TrimSpace(o), "@"); login != "" && !strings.Contains(login, "/") &&
			!slices.Contains(logins, login) && len(logins) < maxAssignees {
			logins = append(logins, login)
		}
	}
	_, _, err := s.gh.SetAssigneesAndLabels(ctx, ref, issue, logins, []string{request.Label})
	if errors.Is(err, api.ErrUnprocessable) {
		_, _, err = s.gh.SetAssigneesAndLabels(ctx, ref, issue, nil, []string{request.Label})
	}
	if err != nil {
		slog.Default().WarnContext(ctx, "requests triage failed", "installation_id", ref.InstallationID,
			"repository", ref.FullName, "issue", issue, "status", api.Status(err), "rate_limited", errors.Is(err, api.ErrRateLimited))
	}
}

// refuse posts the one reply of a request that is not taken.
func (s *Service) refuse(ctx context.Context, ref api.RepoRef, number int, reply string) (string, error) {
	if err := s.post(ctx, ref, number, reply); err != nil {
		return ReactionRefused, fmt.Errorf("requests: reply: %w", err)
	}
	return ReactionRefused, nil
}

// post writes a comment of the App on a pull request or an issue. Every comment goes through inert, so no line of it
// is a request line, whatever engine or owner text it quotes.
func (s *Service) post(ctx context.Context, ref api.RepoRef, number int, body string) error {
	_, err := s.gh.CreateComment(ctx, ref, number, inert(body))
	return err
}

// writer reports whether login has write permission on the repository (M2 D21).
func (s *Service) writer(ctx context.Context, ref api.RepoRef, login string) (bool, error) {
	if login == "" {
		return false, nil
	}
	p, err := s.gh.CollaboratorPermission(ctx, ref, login)
	if err != nil {
		return false, fmt.Errorf("requests: read the author's permission: %w", err)
	}
	return slices.Contains(mayAsk, p), nil
}

// policy reads the policy at the default branch's tip; found is false when there is no valid one.
func (s *Service) policy(ctx context.Context, r api.Repo) (policy3.File, bool, error) {
	pol, _, found, err := s.policyAt(ctx, r)
	return pol, found, err
}

// policyAt reads the policy at the default branch's tip, decoded and as bytes; found is false when there is no valid
// one (version 1).
func (s *Service) policyAt(ctx context.Context, r api.Repo) (pol policy3.File, raw []byte, found bool, err error) {
	tip, err := s.gh.BranchHead(ctx, r.RepoRef, r.DefaultBranch)
	if err != nil {
		return policy3.File{}, nil, false, fmt.Errorf("requests: read the default branch head: %w", err)
	}
	raw, _, err = s.gh.File(ctx, r.RepoRef, tip, policyPath)
	if errors.Is(err, api.ErrNotFound) {
		return policy3.File{}, nil, false, nil
	}
	if err != nil {
		return policy3.File{}, nil, false, fmt.Errorf("requests: read the policy: %w", err)
	}
	pol, err = policy3.Decode(raw)
	if err != nil {
		return policy3.File{}, nil, false, nil
	}
	return pol, raw, true, nil
}

// errNoResult means the pull request has no verified result the App can read back: no record, a status without
// errors the App accepted, or an artifact that is gone or is not the uploaded bytes.
var errNoResult = errors.New("requests: no verified result")

// latest reads the pull record of M2 D6 and, for a fail or waiting status, the result back from the run's artifact,
// checked against the recorded payload digest. A pass has no errors and no read.
func (s *Service) latest(ctx context.Context, r api.Repo, pull int) (records.Pull, result.Result, error) {
	var rec records.Pull
	err := records.Load(ctx, s.store, tenant(r), records.PullKey(r.FullName, pull), &rec)
	if errors.Is(err, records.ErrNoRecord) {
		return rec, result.Result{}, errNoResult
	}
	if err != nil {
		return rec, result.Result{}, fmt.Errorf("requests: read the pull record: %w", err)
	}
	switch result.Status(rec.Status) {
	case result.StatusPass:
		return rec, result.Result{Status: result.StatusPass}, nil
	case result.StatusFail, result.StatusWaiting:
	default:
		return rec, result.Result{}, errNoResult
	}
	raw, err := s.gh.Artifact(ctx, r.RepoRef, rec.RunID, ArtifactName, maxResultBytes)
	switch {
	case errors.Is(err, api.ErrNotFound) || errors.Is(err, api.ErrArtifactInvalid):
		return rec, result.Result{}, errNoResult
	case err != nil:
		return rec, result.Result{}, fmt.Errorf("requests: read the result artifact: %w", err)
	}
	if envelope.PayloadDigest(raw) != rec.PayloadDigest {
		return rec, result.Result{}, errNoResult
	}
	res, err := result.Decode(raw)
	if err != nil {
		return rec, result.Result{}, errNoResult
	}
	return rec, res, nil
}

var errWithdrawn = errors.New("requests: publication withdrawn")

// republish derives the status of the pull request's latest result again and, when open requests now cover it,
// publishes it as waiting and records that status. Under an advisory policy the check never waits: the result stays
// a fail whose next steps name the requests (Name), published as advisory, and the recorded status stays. A newer
// upload that lands meanwhile makes the publication stale; the newest record is then read again.
func (s *Service) republish(ctx context.Context, r api.Repo, pull int, advisory bool) error {
	for range republishTries {
		rec, res, err := s.latest(ctx, r, pull)
		if errors.Is(err, errNoResult) {
			return nil
		}
		if err != nil {
			return err
		}
		open, err := OpenIndex(ctx, s.store, r)
		if err != nil {
			return err
		}
		cover := Cover
		if advisory {
			cover = Name
		}
		shown, ids := cover(res, open)
		if len(ids) == 0 {
			return nil
		}
		if err := Wait(ctx, s.store, r, pull, ids); err != nil {
			return err
		}
		err = s.publisher.Publish4(ctx, feedback.Publication4{
			Target:     inputs.Target{Repo: r, PullRequest: pull, HeadSHA: rec.HeadSHA, BaseRef: r.DefaultBranch},
			Source:     "request",
			RunID:      rec.RunID,
			RunAttempt: rec.RunAttempt,
			Digest:     rec.PayloadDigest,
			Advisory:   advisory,
			Result:     shown,
			Current:    func(ctx context.Context) (bool, error) { return s.same(ctx, r, pull, rec) },
		})
		if errors.Is(err, errWithdrawn) {
			return nil
		}
		if errors.Is(err, feedback.ErrStaleInputs) {
			continue
		}
		if err != nil {
			return fmt.Errorf("requests: publish waiting: %w", err)
		}
		if advisory {
			return nil
		}
		return s.setStatus(ctx, r, pull, rec, result.StatusWaiting)
	}
	return errors.New("requests: the pull record kept moving")
}

// same reports whether the pull record is still rec: the same run, attempt and payload, and the same status. An upload
// of the same run that the App decided again (unknown after a policy changed, say) changes the status, so a waiting
// read from the earlier decision never replaces it.
func (s *Service) same(ctx context.Context, r api.Repo, pull int, rec records.Pull) (bool, error) {
	var now records.Pull
	if err := records.Load(ctx, s.store, tenant(r), records.PullKey(r.FullName, pull), &now); err != nil {
		return false, err
	}
	if !sameRecord(now, rec) {
		return false, nil
	}
	var published records.Publication
	err := records.Load(ctx, s.store, tenant(r), records.PublicationKey(r.FullName, pull, rec.HeadSHA), &published)
	if errors.Is(err, records.ErrNoRecord) {
		return true, nil
	}
	if err != nil {
		return false, err
	}
	if published.RunID == rec.RunID && published.RunAttempt == rec.RunAttempt && published.Pending && !published.Staged && published.Source == "request" && published.Digest == rec.PayloadDigest {
		return true, nil
	}
	// A withdrawal or another publication for this or a newer run owns the view.
	if published.RunID > rec.RunID || published.RunID == rec.RunID && published.RunAttempt >= rec.RunAttempt {
		return false, errWithdrawn
	}
	return true, nil
}

func sameRecord(a, b records.Pull) bool {
	return a.HeadSHA == b.HeadSHA && a.RunID == b.RunID && a.RunAttempt == b.RunAttempt &&
		a.PayloadDigest == b.PayloadDigest && a.Status == b.Status
}

// setStatus records status in the pull record while it still names the run of rec.
func (s *Service) setStatus(ctx context.Context, r api.Repo, pull int, rec records.Pull, status result.Status) error {
	key := records.PullKey(r.FullName, pull)
	err := store.Update(ctx, s.store, tenant(r), []string{key}, func(cur [][]byte) ([][]byte, error) {
		var now records.Pull
		if cur[0] == nil {
			return cur, nil
		}
		if err := records.Decode(cur[0], &now); err != nil {
			return nil, err
		}
		if !sameRecord(now, rec) || now.Status == string(status) {
			return cur, nil
		}
		now.Status = string(status)
		raw, err := records.Encode(&now)
		return [][]byte{raw}, err
	})
	if err != nil {
		return fmt.Errorf("requests: record the status: %w", err)
	}
	return nil
}

// groupSurface is the surface a request keeps from the first of its errors that is a coupling error past the
// target's declared surface, or nil when none is.
func groupSurface(errs []result.Error) *records.Surface {
	for _, e := range errs {
		if s := surfaceOf(e); s != nil {
			return s
		}
	}
	return nil
}

// surfaceOf is the surface a request keeps from a coupling error past the target's declared surface, or nil. The
// stored lists are bounded: they are producer text, shown on the request page and kept in one record.
func surfaceOf(e result.Error) *records.Surface {
	if e.Surface == nil || e.Rule != ruleCoupling {
		return nil
	}
	return boundedSurface(e.Surface.Public, e.Surface.Model, pastSurface(e))
}

// The bounds of a stored surface. A package past them is dropped rather than cut, so a declaration never writes a
// glob other than the one the engine named.
const (
	maxSurfaceGlobs = 64
	maxSurfaceGlob  = 256
)

func boundedSurface(public, model []string, pkg string) *records.Surface {
	globs := func(list []string) []string {
		out := []string{}
		for _, g := range list[:min(len(list), maxSurfaceGlobs)] {
			out = append(out, g[:min(len(g), maxSurfaceGlob)])
		}
		return out
	}
	if len(pkg) > maxSurfaceGlob {
		pkg = ""
	}
	return &records.Surface{Public: globs(public), Model: globs(model), Package: pkg}
}

// mergeSurface is what a request keeps when a verified result brings surface s; verified says whether the request's
// numbers were verified before. A request never stops being a surface request, so a use past the surface is never
// approved as such. The package it declares is only ever one a verified result names:
//   - a verified surface replaces one the CLI reported, package included;
//   - a verified result without a surface keeps the reported one's globs but drops its unverified package;
//   - two verified results that name different packages under one request make it contested: it has no package from
//     then on, so no declaration is offered (the request key is the module pair, and one declaration covers one
//     package).
func mergeSurface(kept, s *records.Surface, verified bool) (*records.Surface, bool) {
	unnamed := func(k *records.Surface, contested bool) (*records.Surface, bool) {
		if k.Package == "" && k.Contested == contested {
			return k, false
		}
		c := *k
		c.Package, c.Contested = "", contested
		return &c, true
	}
	switch {
	case kept == nil && s == nil:
		return nil, false
	case kept != nil && kept.Contested:
		return kept, false
	case s == nil && verified:
		return kept, false
	case s == nil:
		return unnamed(kept, false)
	case kept == nil, !verified, kept.Package == "" && s.Package != "":
		return s, true
	case s.Package != "" && kept.Package != s.Package:
		return unnamed(kept, true)
	}
	return kept, false
}

// pastSurface is the package past the surface that a surface request asks about, as the verified result names it
// (engine 0.9.0, `package`). Without it the request gets no declaration (fail closed).
func pastSurface(e result.Error) string { return e.Package }

func balance(b *result.Balance) *records.Balance {
	if b == nil {
		return nil
	}
	return &records.Balance{Score: b.Value, Max: 10, Min: b.Min, Strength: string(b.Strength), Volatility: string(b.Volatility)}
}

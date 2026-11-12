package gate

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"regexp"
	"slices"
	"strconv"
	"time"

	"github.com/alexei-led/archfit-app/internal/gate/feedback"
	"github.com/alexei-led/archfit-app/internal/gate/ingest"
	"github.com/alexei-led/archfit-app/internal/gate/inputs"
	"github.com/alexei-led/archfit-app/internal/gate/records"
	asks "github.com/alexei-led/archfit-app/internal/gate/requests"
	"github.com/alexei-led/archfit-app/internal/github/api"
	"github.com/alexei-led/archfit-app/internal/store"
	"github.com/alexei-led/archfit-app/internal/wire/envelope"
	"github.com/alexei-led/archfit-app/internal/wire/manifest"
	"github.com/alexei-led/archfit-app/internal/wire/policy3"
	"github.com/alexei-led/archfit-app/internal/wire/result"
)

// policyPath is the policy file; the one on the default branch is the approved policy (design 9.1).
const policyPath = ".archfit.yaml"

// gateRequired is the policy's gate value that makes a fail block; absent or "advisory" shows a fail as neutral.
const gateRequired = "required"

// summaryUnknown is the engine's status line of an unknown result, used for the App's own records.
const summaryUnknown = "archfit: unknown, no verdict"

// The reason codes of the records the App adds to an envelope 2 upload. Each makes the status unknown. The first
// one is the reason of the upload's answer.
const (
	reasonEnvelopeMismatch = "envelope_mismatch"
	reasonUnsupportedEvent = "unsupported_event"
	reasonFork             = "fork"
	reasonResultInvalid    = "result_invalid"
	reasonUnknownEngine    = "unknown_engine"
	reasonFirstPolicy      = "first_policy"
	reasonTargetOffBranch  = "target_not_on_branch"
	reasonPolicyNotTarget  = "policy_not_target"
	reasonPolicyChanged    = "policy_changed"
	reasonWorkflowChanged  = "workflow_changed"
)

// The compare statuses that put target_sha on the default branch: the tip itself, or behind it.
const (
	compareIdentical = "identical"
	compareAhead     = "ahead"
)

var commitSHA = regexp.MustCompile(`^[0-9a-f]{40}$`)

// actionUse is one whole `uses:` line of the Action: its ref (group 2) and a version comment such as `# v4.0.1`, which
// Dependabot rewrites with the ref. Only these two parts are ignored when files are compared. A ref, a comment or a
// line end outside these narrow forms (any other character, a quote that does not close, a CR, NEL or U+2028) does
// not match, so its line stays in the comparison as written.
var actionUse = regexp.MustCompile(`(?m)^([ \t]*(?:-[ \t]+)?uses:[ \t]*(["']?)` + regexp.QuoteMeta(manifest.ActionRepository+"@") +
	`)([0-9A-Za-z._/-]+)(["']?)(?:[ \t]+#[ \t]*[0-9A-Za-z._+-]*)?[ \t]*$`)

// upload4 is the envelope 2 path of POST /v1/reports: the trusted inputs, the status the App gives the result, the
// pull record of M2 D6, and the check and comment that show it (M2 task 15).
type upload4 struct {
	receiver  ingest.Receiver4
	repos     api.Repos
	commits   api.Commits
	store     store.Store
	publisher feedback.Publisher4
	// The manifest 4 approvals; tests replace them, because the shipped manifest approves nothing yet.
	engineApproved func(manifest.Identity) bool
	actionApproved func(commit string) bool
}

func newUpload4(rc ingest.Receiver4, repos api.Repos, commits api.Commits, s store.Store, p feedback.Publisher4) *upload4 {
	return &upload4{receiver: rc, repos: repos, commits: commits, store: s, publisher: p,
		engineApproved: manifest.EngineApproved, actionApproved: manifest.ActionApproved}
}

// decision4 is the status the App gives one envelope 2 upload: the result's own status, or unknown with the App's
// records when any rule failed.
type decision4 struct {
	Status   result.Status
	Reasons  []reason4 // in rule order
	Advisory bool      // the default branch's policy has no gate: required, so a fail does not block
	Tip      string    // the default branch's head every rule read; the decision holds while it is the head
	// Coverable is a fail of a pull request in required mode that the App accepted as it is: open requests decide
	// whether it is waiting. Covered are those requests; Waiting is the result shown then.
	Coverable bool
	Covered   []int
	Waiting   *result.Result
}

// reason4 is one record the App adds, with its stable code.
type reason4 struct {
	Code   string
	Record result.Unknown
}

// answer4 is the 200 body of an envelope 2 upload: "accepted", not "passed".
type answer4 struct {
	Status result.Status `json:"status"`
	Reason string        `json:"reason,omitempty"`
}

// serveReports dispatches POST /v1/reports by the envelope's schema version: envelope 2 to the Archfit 4 path,
// anything else to the envelope 1 path, which refuses what it cannot read.
func (g *Gate) serveReports(w http.ResponseWriter, req *http.Request) {
	var head struct {
		SchemaVersion string `json:"schema_version"`
	}
	if json.Unmarshal([]byte(req.Header.Get("X-Archfit-Envelope")), &head) == nil && head.SchemaVersion == envelope.SchemaV2 {
		g.serveUpload4(w, req)
		return
	}
	g.serveUpload(w, req)
}

func (g *Gate) serveUpload4(w http.ResponseWriter, req *http.Request) {
	start := g.now()
	ctx, cancel := context.WithTimeout(req.Context(), uploadDeadline)
	defer cancel()

	u, err := g.v4.receiver.ReceiveV2(ctx, req)
	var d decision4
	if err == nil {
		d, err = g.v4.decide(ctx, u)
	}
	if err == nil {
		d, err = g.v4.cover(ctx, u, d)
	}
	if err == nil {
		err = g.v4.storePull(ctx, u, d)
	}
	if err == nil && d.publishes() {
		err = g.v4.publish(ctx, u, d)
	}

	status, body := http.StatusOK, any(nil)
	var rejection *Rejection
	switch {
	case err == nil:
		a := answer4{Status: d.Status}
		if len(d.Reasons) > 0 {
			a.Reason = d.Reasons[0].Code
		}
		body = a
	case errors.As(err, &rejection):
		status, body = rejection.Status, refused{Error: rejection.Code}
	case errors.Is(err, api.ErrRateLimited):
		status, body = http.StatusServiceUnavailable, refused{Error: codeRateLimited}
		w.Header().Set("Retry-After", strconv.Itoa(retryAfterSeconds))
	default:
		status, body = http.StatusServiceUnavailable, refused{Error: codeUnavailable}
	}
	logUpload4(ctx, u, d, status, g.now().Sub(start))

	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	// The status is out; a client that went away is not worth a log line.
	_ = json.NewEncoder(w).Encode(body)
}

// decide applies the rules of design 9.1 and 9.2 to a bound upload. Every input is read from GitHub at the default
// branch's tip now, never taken from the envelope alone. An error is a failed read: nothing is decided.
func (v *upload4) decide(ctx context.Context, u ingest.Upload4) (decision4, error) {
	b, env := u.Binding, u.Envelope
	ref, branch := b.Repo.RepoRef, b.Repo.DefaultBranch
	var reasons []reason4
	add := func(code string, rec result.Unknown) { reasons = append(reasons, reason4{Code: code, Record: rec}) }

	for _, p := range u.Problems {
		code, rec := problemRecord(p, branch)
		add(code, rec)
	}
	if !v.engineApproved(manifest.Identity{EngineVersion: env.EngineVersion, ImageDigest: env.ImageDigest, Platform: env.Platform}) {
		add(reasonUnknownEngine, result.Unknown{Cause: "tool", What: "the engine image is not one the App lists",
			Next: result.Next{Kind: "fix", Text: "pin archfit-action to a commit the App lists"}})
	}

	tip, err := v.repos.BranchHead(ctx, ref, branch)
	if err != nil {
		return decision4{}, fmt.Errorf("gate: read the default branch head: %w", err)
	}
	tipPolicy, found, err := v.file(ctx, ref, tip, policyPath)
	if err != nil {
		return decision4{}, err
	}
	policy, err := policy3.Decode(tipPolicy)
	advisory := policy.Gate != gateRequired
	if !found || err != nil {
		// No approved policy yet: this pull request brings the first one, which an administrator approves (task 23).
		add(reasonFirstPolicy, result.Unknown{Path: policyPath, Cause: "policy",
			What: branch + " has no policy of version 3 yet",
			Why:  "an administrator approves the first policy",
			Next: result.Next{Kind: "wait", Text: "an administrator approves the first policy in the App"}})
	} else if err := v.checkPolicy(ctx, ref, branch, tip, tipPolicy, env, add); err != nil {
		return decision4{}, err
	}
	if err := v.checkWorkflow(ctx, ref, branch, tip, b.RunSHA, add); err != nil {
		return decision4{}, err
	}

	if len(reasons) > 0 || u.Result == nil {
		return decision4{Status: result.StatusUnknown, Reasons: reasons, Advisory: advisory, Tip: tip}, nil
	}
	return decision4{Status: u.Result.Status, Advisory: advisory, Tip: tip}, nil
}

// cover derives the status waiting (design 7.1, M2 task 16): a fail whose errors all ask an owner and are all in open
// requests is waiting, and the pull request joins those requests in the store. Only a pull request into the default
// branch in required mode is covered: waiting is in progress, which blocks a merge, and advisory mode never blocks.
func (v *upload4) cover(ctx context.Context, u ingest.Upload4, d decision4) (decision4, error) {
	b := u.Binding
	d.Coverable = b.PullRequest != 0 && !d.Advisory && len(d.Reasons) == 0 && u.Result != nil && d.Status == result.StatusFail
	if !d.Coverable {
		return d, nil
	}
	ids, shown, err := v.covered(ctx, u)
	if err != nil || len(ids) == 0 {
		return d, err
	}
	if err := asks.Wait(ctx, v.store, b.Repo, b.PullRequest, ids); err != nil {
		return decision4{}, err
	}
	d.Status, d.Covered, d.Waiting = result.StatusWaiting, ids, &shown
	return d, nil
}

// covered reads the open requests and derives the result's status from them.
func (v *upload4) covered(ctx context.Context, u ingest.Upload4) ([]int, result.Result, error) {
	open, err := asks.OpenIndex(ctx, v.store, u.Binding.Repo)
	if err != nil {
		return nil, result.Result{}, err
	}
	shown, ids := asks.Cover(*u.Result, open)
	return ids, shown, nil
}

// checkPolicy requires target_sha on the default branch and the policy digest of the run to be the digest at
// target_sha and at the tip.
func (v *upload4) checkPolicy(ctx context.Context, ref api.RepoRef, branch, tip string, tipPolicy []byte,
	env envelope.EnvelopeV2, add func(string, result.Unknown),
) error {
	target := tipPolicy
	if env.TargetSHA != tip {
		onBranch, err := v.onBranch(ctx, ref, env.TargetSHA, tip)
		if err != nil {
			return err
		}
		if !onBranch {
			add(reasonTargetOffBranch, result.Unknown{Cause: "base",
				What: "the run read the policy from a commit that is not on " + branch,
				Next: result.Next{Kind: "fix", Text: "run the Archfit check again"}})
			return nil
		}
		if target, _, err = v.file(ctx, ref, env.TargetSHA, policyPath); err != nil {
			return err
		}
	}
	switch {
	case target == nil || env.PolicyDigest != policy3.Digest(target):
		add(reasonPolicyNotTarget, result.Unknown{Path: policyPath, Cause: "policy",
			What: "the run did not use the policy on " + branch,
			Why:  "only owners change the policy",
			Next: result.Next{Kind: "restore", Text: "git checkout origin/" + branch + " -- " + policyPath}})
	case policy3.Digest(target) != policy3.Digest(tipPolicy):
		add(reasonPolicyChanged, result.Unknown{Path: policyPath, Cause: "policy",
			What: "changed on " + branch + " since this run",
			Next: result.Next{Kind: "wait", Text: "the check runs again"}})
	}
	return nil
}

// onBranch reports whether commit is tip or behind it. A commit GitHub does not know, or one it cannot compare, is
// not on the branch.
func (v *upload4) onBranch(ctx context.Context, ref api.RepoRef, commit, tip string) (bool, error) {
	c, err := v.commits.Compare(ctx, ref, commit, tip)
	switch {
	case api.Permanent(err):
		return false, nil
	case err != nil:
		return false, fmt.Errorf("gate: compare target with the default branch: %w", err)
	}
	return c.Status == compareIdentical || c.Status == compareAhead, nil
}

// checkWorkflow requires the workflow file the run executed to be the default branch's, or to differ only in the
// Action's ref, moved to commits manifest 4 lists. A pull request runs its own workflow file, so a changed one could
// upload a forged pass (design 9.2).
func (v *upload4) checkWorkflow(ctx context.Context, ref api.RepoRef, branch, tip, runSHA string, add func(string, result.Unknown)) error {
	same := false
	// An empty ref would read the default branch: only a commit SHA names the file the run executed.
	if commitSHA.MatchString(runSHA) {
		run, runBlob, runFound, err := v.fileBlob(ctx, ref, runSHA, ingest.WorkflowPath)
		if err != nil {
			return err
		}
		target, tipBlob, tipFound, err := v.fileBlob(ctx, ref, tip, ingest.WorkflowPath)
		if err != nil {
			return err
		}
		same = runFound && tipFound && (runBlob == tipBlob || v.refOnly(run, target))
	}
	if !same {
		add(reasonWorkflowChanged, result.Unknown{Path: ingest.WorkflowPath, Cause: "tool",
			What: "differs from " + branch,
			Why:  "a pull request runs its own workflow file",
			Next: result.Next{Kind: "fix", Text: "update the branch; if the pull request changes the workflow, an owner approves it in the App"}})
	}
	return nil
}

// refOnly reports whether run differs from target only in the Action's refs and their version comments, and every
// ref of run is a commit manifest 4 lists: a pin bump, which needs no owner.
func (v *upload4) refOnly(run, target []byte) bool {
	uses := actionUse.FindAllSubmatch(run, -1)
	if len(uses) == 0 {
		return false
	}
	for _, m := range uses {
		if !bytes.Equal(m[2], m[4]) || !v.actionApproved(string(m[3])) {
			return false
		}
	}
	strip := func(b []byte) []byte { return actionUse.ReplaceAll(b, []byte("${1}${4}")) }
	return bytes.Equal(strip(run), strip(target))
}

func (v *upload4) file(ctx context.Context, ref api.RepoRef, at, path string) ([]byte, bool, error) {
	content, _, found, err := v.fileBlob(ctx, ref, at, path)
	return content, found, err
}

// fileBlob reads path at a commit; a missing file is found == false, not an error.
func (v *upload4) fileBlob(ctx context.Context, ref api.RepoRef, at, path string) ([]byte, string, bool, error) {
	content, blob, err := v.repos.File(ctx, ref, at, path)
	switch {
	case errors.Is(err, api.ErrNotFound):
		return nil, "", false, nil
	case err != nil:
		return nil, "", false, fmt.Errorf("gate: read %s: %w", path, err)
	}
	return content, blob, true, nil
}

// problemRecord is the App's record of an ingest Problem.
func problemRecord(p ingest.Problem, branch string) (string, result.Unknown) {
	switch p {
	case ingest.ProblemUnsupportedEvent:
		return reasonUnsupportedEvent, result.Unknown{Cause: "base",
			What: "only pull requests into " + branch + ", pushes to it and its merge queue are checked",
			Next: result.Next{Kind: "fix", Text: "open the pull request against " + branch}}
	case ingest.ProblemFork:
		return reasonFork, result.Unknown{Cause: "tool", What: "a run from a fork is not trusted",
			Next: result.Next{Kind: "fix", Text: "push the branch to this repository"}}
	case ingest.ProblemReportInvalid:
		return reasonResultInvalid, result.Unknown{Cause: "tool", What: "the result cannot be read",
			Next: result.Next{Kind: "fix", Text: "pin archfit-action to a commit the App lists"}}
	}
	return reasonEnvelopeMismatch, result.Unknown{Cause: "tool", What: "the upload does not match the run GitHub reports",
		Next: result.Next{Kind: "fix", Text: "run the Archfit check again"}}
}

// storePull keeps the pull record of a pull request upload (M2 D6) with its index entry, in one swap. A record of a
// newer run or attempt stays: GitHub's run ids grow, so a late upload of an older head never replaces a newer one.
// The error keys are kept only for a result the App accepted as it is.
func (v *upload4) storePull(ctx context.Context, u ingest.Upload4, d decision4) error {
	b := u.Binding
	if b.PullRequest == 0 {
		return nil
	}
	rec := records.Pull{HeadSHA: b.HeadSHA, RunID: b.RunID, RunAttempt: b.RunAttempt,
		PayloadDigest: u.Envelope.PayloadDigest, Status: string(d.Status)}
	if len(d.Reasons) == 0 && u.Result != nil {
		for _, e := range u.Result.Errors {
			key, err := asks.Key(e)
			if err != nil {
				key = ""
			}
			rec.Errors, rec.RequestKeys = append(rec.Errors, e.Key), append(rec.RequestKeys, key)
		}
	}
	name, entry := b.Repo.FullName, records.PullEntry(b.PullRequest)
	return records.Indexed(ctx, v.store, store.Tenant(b.Repo.InstallationID), records.PullKey(name, b.PullRequest),
		records.PullIndexKey(name), records.SchemaPullIndex, func(cur []byte, x *records.Index) ([]byte, error) {
			if cur != nil {
				var old records.Pull
				if err := records.Decode(cur, &old); err != nil {
					return nil, err
				}
				if old.RunID > rec.RunID || old.RunID == rec.RunID && old.RunAttempt > rec.RunAttempt {
					return cur, nil
				}
			}
			x.Remove(entry)
			if err := x.Add(entry, b.HeadSHA); err != nil {
				return nil, err
			}
			return records.Encode(&rec)
		})
}

// publishes reports whether the upload writes the head's check and comment. Only an upload bound to the default
// branch does: an open pull request into it, a push to it, or its merge group. A pull request into another base, a
// push to another branch, or a run the App cannot tie to its pull request writes nothing, so the head keeps the check
// it has: none or a pending one, which blocks a merge, or the result of the newest judged run of the same commit. A
// run of another base has a higher run id than the pull request into the default branch on the same commit, and a
// re-run keeps the lower id: were it to write, that pull request could not get its own result back until a new push.
func (d decision4) publishes() bool {
	for _, r := range d.Reasons {
		if r.Code == reasonUnsupportedEvent || r.Code == reasonEnvelopeMismatch {
			return false
		}
	}
	return true
}

// shown is the result the check and the comment show: the App's own records when a rule failed, so nothing of a result
// the App did not accept is shown; else the engine's result with the status the App gave it.
func (d decision4) shown(r *result.Result) result.Result {
	if d.Waiting != nil {
		return *d.Waiting
	}
	if len(d.Reasons) > 0 || r == nil {
		s := result.Result{Status: result.StatusUnknown, Summary: summaryUnknown}
		for _, x := range d.Reasons {
			s.Unknown = append(s.Unknown, x.Record)
		}
		return s
	}
	s := *r
	s.Status = d.Status
	return s
}

// publish writes the check of the upload's head and, for a pull request, its comment. Inside the head's lease it reads
// the default branch's head again: when it moved, the decision may be stale, nothing is written and the upload answers
// 503, so the Action's retry decides from the new head. An upload of the same run decided earlier then never replaces
// one decided later. A coverable fail also reads the open requests again: a request line that opened or joined one
// since the decision makes it stale the same way, so a fail never lands after the waiting that request set.
func (v *upload4) publish(ctx context.Context, u ingest.Upload4, d decision4) error {
	b := u.Binding
	return v.publisher.Publish4(ctx, feedback.Publication4{
		Target:     inputs.Target{Repo: b.Repo, PullRequest: b.PullRequest, HeadSHA: b.HeadSHA, BaseRef: b.Repo.DefaultBranch},
		RunID:      b.RunID,
		RunAttempt: b.RunAttempt,
		Digest:     u.Envelope.PayloadDigest,
		Advisory:   d.Advisory,
		Result:     d.shown(u.Result),
		Current: func(ctx context.Context) (bool, error) {
			tip, err := v.repos.BranchHead(ctx, b.Repo.RepoRef, b.Repo.DefaultBranch)
			if err != nil {
				return false, fmt.Errorf("gate: read the default branch head: %w", err)
			}
			if tip != d.Tip || !d.Coverable {
				return tip == d.Tip, nil
			}
			ids, _, err := v.covered(ctx, u)
			return slices.Equal(ids, d.Covered), err
		},
	})
}

func logUpload4(ctx context.Context, u ingest.Upload4, d decision4, status int, elapsed time.Duration) {
	codes := make([]string, len(d.Reasons))
	for i, r := range d.Reasons {
		codes[i] = r.Code
	}
	b := u.Binding
	slog.Default().InfoContext(ctx, "gate upload",
		"envelope", 2, "installation_id", b.Repo.InstallationID, "repository_id", b.Repo.ID,
		"pr", b.PullRequest, "head_sha", b.HeadSHA, "run_id", b.RunID, "run_attempt", b.RunAttempt,
		"digest", u.Envelope.PayloadDigest, "status", string(d.Status), "reasons", codes,
		"http_status", status, "duration_ms", elapsed.Milliseconds())
}

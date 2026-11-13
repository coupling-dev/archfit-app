# Gate

**Path**: internal/gate/
**Parent**: internal/ (Archfit App root)
**Submodules**: ingest, inputs, verdict, feedback, sweep, records, requests, proposals, approvals

## Purpose

`gate` is the fail-closed gate path: from an uploaded engine report to a concluded `archfit/gate` check.
It is the façade the rest of the App sees: one upload handler, one webhook reaction, one sweep. It
composes five children so that authentication, trusted inputs, the decision and the publication are
separate, testable steps. Without this module a report is a self-reported file and a required check is a bypass waiting to happen.

## Functional Responsibilities

- Serve `POST /v1/reports`: authenticate and bind the upload, resolve trusted inputs, check the engine
  identity against the locked manifest and the workflow path against settings, decide, publish, answer. After a
  published push to the default branch, keep the branch's repair issue (`gate/feedback`'s `Repairer`): a notification
  that never changes the answer.
- Serve envelope 2 (Archfit 4) on the same route: `UploadHandler` sends an upload whose envelope header says
  `archfit.upload-envelope.v2` to the envelope 2 path (`upload4.go`), everything else to the path above. That path
  decides the status, stores the pull record, publishes `archfit/gate` and the pull request's comment through
  `gate/feedback`'s `Publisher4` (M2 task 15), and answers. See [Envelope 2 upload](#envelope-2-upload). A `fail` of a
  pull request in required mode whose errors all ask an owner and are all in open requests is `waiting`, naming the
  requests (`gate/requests`' `Cover`, M2 task 16); the open requests are read again inside the head's lease.
- Take in requests (M2 task 16, `gate/requests`): a `/archfit request <why>` comment on a pull request opens or joins
  request issues and sets the check to `waiting`; an issue the CLI opened from a `can` answer becomes a request "as
  reported". A closed pull request leaves the Archfit 4 pull index. A comment with a request line goes this way; any
  other comment is ignored.
- React to verified webhook events: a pending check on a new head, a fork refusal, a request line or a request issue, a
  closed pull request, the checks of a merge group, and a default-branch push. No event approves a protected change: a
  push, a review or a comment changes no result.
- Gate the merge queue of the default branch: serve the upload of a merge group report on the group commit, create its
  pending required check on `merge_group` `checks_requested`, correct that check when the group is destroyed. The members are read from GitHub's queue (`gate/inputs`), never from the
  upload or the event.
- For the App's request page only (`RequestViews`, M2 task 17): read a request (reason, record, balance verified or "as
  reported", the two modules of the policy at the default branch's tip, earlier answers) and preview an answer (the
  policy diff, whether it needs the passkey, the open pull requests it touches by their stored request keys). It writes
  nothing and runs no analysis. `PolicyEdit` writes an answer's change (`AnswerChange`) into a policy: the edit the
  preview shows is the edit the answer writes (task 19).
- For the App's request page only (`Answers`, M2 task 19): take in an owner's answer to a request (approve, approve until
  a date, reject with `instead`) and carry it to the default branch through the App's policy pull request on
  `archfit/policy` (`gate/proposals`): the approval recorded, the branch written with the other open answers,
  `archfit/gate` set on that pull request when M2 D13 holds, and the merge unless the owner chose to merge later.
  `AnswerDigest` is the change digest the owner's passkey step-up is bound to.
- Judge each default-branch push that changes the policy (M2 task 20, `gate/approvals`, façade file `approvals.go`): the
  merge of a proposal with the policy its approvals wrote consumes them, once; in a repository with an approved policy,
  any other change puts it "outside the flow" (design 7.1): one issue opens to the owners, and every envelope 2 upload
  is `unknown` with a record that names it until an owner confirms or reverts (task 23). A repository without an
  approved policy is not under change control yet: the push is recorded, and its uploads say `first_approval`. The pull requests waiting on a
  consumed proposal's requests get a fresh result without their authors (design 10.5, M2 task 21,
  `sweep.AfterAnswer`): a re-run when the four conditions hold, else a branch update with the App token; the path is
  recorded in the pull record and on each request issue. Its failure is returned, so the delivery is retried. The same push is then judged for the
  workflow approval it uses (`approvals.WorkflowPushed`, façade file `owner.go`).
- When a pull request into the default branch opens or moves to a head whose latest `archfit/gate` is the success of a
  proposal that ended (used, void or gone; external id `proposal:<id>`), take that success back first
  (`proposals.Retire`, M2 review H2, façade file `owner.go`): an approval is valid once, for its own pull request. A
  proposal still open keeps its success: its own pull request relies on it, and a pull request at the same head carries
  the same approved change (if that merges first, the push is outside the flow). A failed read or write is returned,
  so the delivery is retried.
- Take the owner actions outside requests (M2 task 23, façade file `owner.go`, `OwnerActions`): the first approval of an
  onboarding pull request or of the policy already on the default branch, a workflow approval (`gate/approvals`), and
  the confirm or revert of a policy changed outside the flow (confirm `gate/approvals`, revert `gate/proposals`).
  Each is bound to the digest of what the owner saw (`OwnerDigest`) and made with a passkey. A first or workflow
  approval of a pull request then gets the approved head its verdict: the re-run guard (`sweep.Rerun`, design 10.5)
  re-runs the head's stored run (pull record of M2 D6) when its four conditions hold, and the run's upload passes the
  approval (manager, 2026-11-13). Otherwise the check stays `unknown` with the App's record naming the condition not
  met (`no_run`, `not_latest`, `too_old`, `base_changed`) and the way out; a failed re-run says so too. A first
  approval of the default branch's policy and a confirm re-run, through the same guard, every open pull request of
  this repository (no fork) whose stored status is `unknown`; one that cannot run again keeps its check, whose unknown
  may have a reason of its own. A `first` approval counts only while the repository has no approved policy and is not outside the
  flow: once change control started, another onboarding pull request's approval admits nothing, and a default branch
  that lost its policy is `outside_flow` or `policy_unapproved`, never `first_policy`.
- For the App's pull request page only (`FirstScreen`, `RecoveryStep`): word a published result with the code that
  words the comment, so the page's first screen is the comment's. Pure functions of what the check recorded.
- Run one reconciliation pass on request.
- Publish, for the control plane, the `Entitlement` record contract, the `Receiver` for discovery uploads,
  and the sweep types.
- Stay operational when the control plane, billing, UI or an LLM provider fails.

## Archfit 4 hourly reconciliation (M2 task 22)

After the legacy pass, `Gate.Sweep` runs `sweep4.go` over admitted installations and repositories with a schema 3
policy or approved v4 state. It never applies legacy approval rules or rule 5c to a v4 result: result v1 has no module
graph. A rate limit stops the pass with `Summary.RateLimited`; other failed units are counted and skipped.

- `approvals.Reconcile` repairs only a proven single policy change on a complete linear range from the approved
  commit to the tip. A matching proposal is consumed with a retryable `Repo.SweepRecheck` handoff to `AfterAnswer`.
  A proven direct change with no proposal goes outside the flow. An ambiguous range consumes and voids nothing:
  its endpoints are logged and stored in `Pull.SweepNote`, and the check stays unknown. Follow-up 3 is only partially
  satisfied: multiple policy changes, non-linear/cut/unreadable history, or unmatched proposals require owner action.
- Reconciliation notification or handoff errors are counted and retried independently of enforcement: the pass
  reloads authority and withdraws affected checks even when an outside-flow issue cannot be created.
- `expiry.Approved` reads authority independently of `Warn`: a warning failure never prevents expiry enforcement.
  From the UTC day after `expires`, a result judged on or before that date is re-run under the 10.5 guard, or receives
  the expired-exception failure when refused. The old success is withdrawn before any re-run read/request, so a failed re-run call also leaves unknown,
  never a stale pass. The expired-exception failure is published only when the guard refuses.
- A stale accepted policy digest, or an unambiguous `policy_changed` upload with a matching `running` after-answer
  marker, is re-run under the same guard once completed. A refusal asks for a branch update. No new webhook is used.
- A missing result for the live head becomes unknown after the latest relevant completed run's last status update
  is more than 60 minutes old; this includes a newer run or attempt missing its upload after an older result.
  Without a run, the pull request's update time is used. In-flight runs are left alone.
- A ten-day waiting check is replaced with a fresh check, after reading the artifact, matching its payload digest,
  strict decoding and head/policy binding, and covering its errors again against current requests. Unverifiable
  artifacts become unknown; transient reads are retried, never treated as clean. The head lease rechecks the live
  pull, default tip, approval state, pull record, latest check and request coverage. A force-pushed-back head with
  no matching pull record is withdrawn using its live check receipt inside the lease (`Withdraw4`), never a zero
  identity that an old success could outrank.
- Indexed pull requests absent from the open listing are read individually; closed or gone ones leave the index
  while their records remain. The App's policy proposal verdict is left to the owner flow.

Regression coverage is in `sweep4_test.go`, `sweep4_race_test.go`, `sweep_record_test.go`, the records handoff test,
the feedback Fresh test, and the GitHub history/update-time contract tests.

## Subdomain Classification

Core: the part that makes the product worth paying for. Volatility: high; trust rules, outcome mapping and
feedback are tuned with every pilot. The façade contract is kept small and stable so that the volatility stays inside.

## Encapsulated Knowledge

- The pipeline order and which child owns which step.
- `IdentityApproved` and `WorkflowApproved`: how manifest, measurement profile and settings combine.
- How a `Receipt` is built from an upload and trusted inputs.
- The HTTP mapping of rejections, outcomes and transient failures.
- Which webhook events matter to the gate and what each triggers.
- That the children exist at all: nothing outside imports `gate/*`.

## Public Contract

```go
// Deps are the only things the gate path may touch: GitHub, OIDC, the Store and a clock. The Store holds entitlements,
// admissions, the Archfit 4 records and publication leases.
type Deps struct {
    GitHub     *Client
    OIDC       Verifier
    Store      Store
    Admissions *Admissions // which installations the App serves; nothing runs for any other
    Audience   string      // the App URL; OIDC audience
    Sweep      Config
    Now        func() time.Time
}

type Gate struct{ /* unexported */ }

func New(d Deps) *Gate

// ClassifyDisplayReason republishes pure verdict-owned metadata classification for outside consumers.
// It does not decide a gate outcome or change report admission.
func ClassifyDisplayReason(code, category string) (reportDerived, known bool)

// UploadHandler serves POST /v1/reports.
func (g *Gate) UploadHandler() http.Handler

// HandleEvent reacts to a verified webhook event: a pending check on a new head, a fork refusal, a request line or a
// request issue, a closed pull request, the checks of a merge group, and a default-branch push, whose policy is judged
// before a re-check or a one-repository sweep. No event approves a protected change. An event for a repository whose
// owner is not admitted gets the reaction "not_admitted" and nothing else: no read, no write. An admission that cannot
// be read is returned as an error, so the delivery is retried.
func (g *Gate) HandleEvent(ctx context.Context, e Event) error

// Sweep runs one reconciliation pass.
func (g *Gate) Sweep(ctx context.Context) (Summary, error)
```

```go
// FirstScreen is the comment's first screen for a published result, as plain-text lines; nil when it would show no report.
func FirstScreen(in ScreenInput) []ScreenLine
// RecoveryStep is the next step of a recorded reason, worded for its role as the comment words it.
func RecoveryStep(code, next string) string

type ScreenInput = feedback.ScreenInput; type ScreenReason = feedback.ScreenReason; type ScreenLine = feedback.Line
```

```go
// RequestViews is what the App's request page reads. *Gate implements it.
type RequestViews interface {
    RequestView(ctx context.Context, t Target, number int) (RequestView, error)
    RequestPreview(ctx context.Context, t Target, number int, a RequestAnswer) (RequestPreview, error)
}
// Owned by gate/requests (gate/records for the numbers).
type RequestView = asks.View; type RequestModule = asks.Module; type RequestEarlier = asks.Earlier
type RequestPreview = asks.Preview; type RequestPullEffect = asks.PullEffect; type RequestAnswer = asks.Answer
type RequestNumbers = records.Numbers; type RequestBalance = records.Balance; type AnswerChange = asks.Change
const EarlierDeny, EarlierException
var ErrNoRequest, ErrClosedRequest, ErrAnswer, ErrNoPolicy, ErrByHand // gate/requests' errors
func PolicyEdit(file []byte, c AnswerChange, comment string) ([]byte, error)
func RequestComment(number int) string // "request #431": with it, PolicyEdit writes the edit the preview shows
```

```go
// Answers takes in an owner's answer to a request (M2 task 19). *Gate implements it.
type Answers interface {
    AnswerDigest(ctx context.Context, t Target, number int, a RequestAnswer) (string, error)
    Answer(ctx context.Context, t Target, in AnswerInput) (AnswerOutcome, error)
}
// Owned by gate/proposals.
type AnswerInput = proposals.Input; type AnswerOwner = proposals.Owner; type AnswerOutcome = proposals.Outcome
var ErrNotPolicyOwner, ErrNeedsPasskey, ErrAnswerBusy, ErrProposalFull // gate/proposals' errors
```

`AnswerDigest` and `Answer` ask `Admitted` first, then run `gate/proposals` (`t.Repo` names the repository); the façade
file is `answers.go`. `Answer` takes the passkey as `AnswerInput.Redeem`, which `control/answers` sets, so the gate
never imports `control`. An error from `Answer` means the answer was not recorded; what it found on its way (a
policy pull request closed without a merge, an approval that no longer applies) may have been recorded void.

```go
// OwnerActions takes in an owner action outside requests (M2 task 23). *Gate implements it.
type OwnerActions interface {
    OwnerDigest(ctx context.Context, t Target, a OwnerAction) (string, error)
    OwnerAct(ctx context.Context, t Target, in OwnerInput) (OwnerOutcome, error)
}
const OwnerFirst, OwnerWorkflow, OwnerConfirm, OwnerRevert // records' approval kinds
type OwnerAction = approvals.Action; type OwnerUser = approvals.Owner
type OwnerInput struct { Action OwnerAction; User OwnerUser; Digest string; Later bool; Redeem func(ctx, digest) (string, error) }
type OwnerOutcome struct { Approval string; PullRequest int; State, Merge string; Issue int; Check string }
const CheckRerun, CheckPending, CheckBlocked // how an approved head gets its verdict
var ErrOwnerNotAdmin, ErrOwnerNotOwner, ErrOwnerNotDue, ErrOwnerNotPossible, ErrOwnerMoved, ErrOwnerNeedsPasskey,
    ErrNotOutsideFlow, ErrNothingToRevert, ErrRevertMoved, ErrRevertNotOwner
```

`OwnerDigest` and `OwnerAct` ask `Admitted` first. A revert runs `gate/proposals` (`RevertDigest`, `Revert`); the
others `gate/approvals` (`Bind`, `Act`). `OwnerInput.Redeem` comes from `control/answers`, as for an answer. An error
means nothing was recorded; a token Redeem used stays used. `Answer` refuses with `ErrOutsideFlow` while the policy
changed outside the flow.

`RequestView` and `RequestPreview` ask `Admitted` first, then read the request and pull records from the Store and the
policy at the default branch's tip from GitHub (`t.Repo` names the repository). `RequestPreview` judges an
approve-until date on the gate's clock. The façade file is `preview.go`.

`FirstScreen` and `RecoveryStep` read nothing and need no admission.

```go
// NewReceiver exposes the upload authentication and binding step so that the discovery
// upload endpoint reuses exactly the same provenance checks as the report endpoint.
func NewReceiver(gh *Client, v Verifier, audience string, adm *Admissions) Receiver
```

### Re-published child contracts

Modules outside `gate` cite this file for the blocks below. They are type aliases of the child
types in Go. `Repo` is the repository type of `github/api`, `Identity` is the claimed identity type of
`wire/manifest`, `Report` is the transport type of `wire/report`, and `Kind` is the payload kind of
`wire/envelope`; all four are restated under Integrations.

<!-- contract: Admission, AdmissionKey, Admissions, NewAdmissions — restated from internal/gate/inputs/module.md -->
```go
// AdmissionKey is the Store key, under the installation tenant, of the JSON-encoded Admission. control/account writes
// it when an operator approves the installation, and deletes it on revoke and on uninstall.
const AdmissionKey = "admission"

// Admission records who let the App serve an installation: an operator's approval. Its presence is the approval.
type Admission struct {
    Account string    `json:"account"` // the installation's account login when approved
    By      string    `json:"by"`      // the operator's login when approved
    ByID    int64     `json:"by_id"`   // the operator's GitHub user ID
    At      time.Time `json:"at"`
}

// NewAdmissions admits the accounts named in auto by configuration, and every other one by its Store record.
// Logins are trimmed and compared case-insensitively; an empty login admits nobody.
func NewAdmissions(s Store, auto []string) *Admissions

// Admitted reports whether the App serves the installation. owner is its account login, the owner of every
// repository it holds. A configured account needs no Store read. An error means the record could not be read: the
// caller does nothing for the installation and retries, so an approved account loses no event to a Store outage.
func (a *Admissions) Admitted(ctx context.Context, installationID int64, owner string) (bool, error)
```

`Admissions` also has `Auto(account)` and `Record(ctx, installationID)`, which `control` uses.

`Target` is re-published too; it is restated under Integrations, gate/inputs.

<!-- contract: Entitlement, EntitlementKey — restated from internal/gate/inputs/module.md -->
```go
// EntitlementKey is the Store key, under the installation tenant, of the JSON-encoded Entitlement.
const EntitlementKey = "entitlement"

// Entitlement is the cached subscription state the gate reads. control/account writes it;
// the gate never calls billing code. A missing or unreadable record means "not suspended".
type Entitlement struct {
    SuspendAfter time.Time `json:"suspend_after"` // zero = never suspends
    WarnFrom     time.Time `json:"warn_from"`     // zero = no notice; feedback shows Notice from this instant
    Notice       string    `json:"notice"`        // e.g. "Trial ends 2026-10-20"
    Reason       string    `json:"reason"`        // shown when suspended, with the recovery action
}
```

<!-- contract: Upload, Binding, Problem — restated from internal/gate/ingest/module.md -->
```go
// Binding is the authoritative identity of an upload, resolved from OIDC claims and the GitHub API.
type Binding struct {
    Repo         Repo   // resolved by the App, never from the envelope
    Event        string // "pull_request" | "push"; discovery and baseline uploads also "workflow_dispatch"
    PullRequest  int    // 0 unless pull_request
    HeadSHA      string
    BaseRef      string // PR base branch; for other events the branch the run executed on
    BaseSHA      string
    MergeBaseSHA string
    Fork         bool
    RunID        int64
    RunAttempt   int
    WorkflowPath string
}

// Problem is an eligibility defect found after the tenant was authenticated.
// Problems never reject the request; they force the gate to action_required.
type Problem string

const (
    ProblemEnvelopeMismatch Problem = "envelope_mismatch" // envelope disagrees with authoritative data
    ProblemUnsupportedEvent Problem = "unsupported_event" // e.g. workflow_run
    ProblemFork             Problem = "fork"
    ProblemReportInvalid    Problem = "report_invalid" // payload failed no-source decoding
)

type Upload struct {
    Binding  Binding
    Identity Identity // claimed engine/image identity from the envelope
    Digest   string   // payload digest, verified against the envelope
    BaselineDigest string // claimed by the envelope; "" = the producer used no baseline
    LabelsDigest   string // claimed by the envelope; "" = the producer used no labels file
    Payload  []byte   // the exact verified bytes; the only content for KindDiscovery and KindBaseline
    Report   *Report  // nil for KindDiscovery, KindBaseline and when ProblemReportInvalid
    Problems []Problem
}
```

<!-- contract: Receiver, Rejection — restated from internal/gate/ingest/module.md -->
```go
// Rejection is returned when no tenant could be authenticated or the upload is stale.
// Nothing is published for a Rejection; the HTTP status is the whole answer.
type Rejection struct {
    Status int    // 400, 401, 403, 409, 413, 429, 503
    Code   string // stable machine-readable reason
}

func (r *Rejection) Error() string

type Receiver interface {
    // Receive authenticates and binds one upload of the given kind. A stale upload
    // (head is no longer the PR head, or an older attempt of the same run) is a 409 Rejection.
    Receive(ctx context.Context, req *http.Request, kind Kind) (Upload, error)
}
```

<!-- contract: Sweeper, Summary, Config — restated from internal/gate/sweep/module.md -->
```go
type Config struct {
    MissingUploadAfter time.Duration // 60 minutes
    WaiverWarnBefore   time.Duration // 7 days
}

// Summary holds counts only; it is safe to log.
type Summary struct {
    Repos, Pulls                         int
    Pended, TimedOut, Rerun, Warned      int
    Suspended                            int
    Errors                               int  // repositories or pulls skipped after an error; details are in the log
    RateLimited                          bool // the pass stopped early on a GitHub rate limit
}

type Sweeper interface {
    // Run re-derives the expected check state of every open PR from GitHub and repairs differences.
    Run(ctx context.Context, now time.Time) (Summary, error)
    // RunRepo is the same pass limited to one repository (used when the protected ref changes).
    // A rate limit stops it with Summary.RateLimited and an error that wraps api.ErrRateLimited.
    RunRepo(ctx context.Context, r Repo, now time.Time) (Summary, error)
    // Recheck is rule 5c alone for r, after a push moved its default branch to tip: a passed pull request goes stale
    // when the moved target closes a module cycle with it. It does nothing when the branch is no longer at tip.
    Recheck(ctx context.Context, r Repo, tip string, now time.Time) (Summary, error)
}
```

### Upload answers (`POST /v1/reports`)

| Situation                                                       | Status                     | Body                                     |
| --------------------------------------------------------------- | -------------------------- | ---------------------------------------- |
| Outcome published (any conclusion, including `action_required`) | 200                        | `{"conclusion": "...", "reason": "..."}` |
| Publication skipped (newer run reported, or PR head moved)      | 200                        | same, with this upload's outcome         |
| `*Rejection` from `Receive`                                     | `Rejection.Status`         | `{"error": "<Rejection.Code>"}`          |
| Installation not admitted (a `*Rejection` of `Receive`)         | 403                        | `{"error": "installation_not_approved"}` |
| Admission unreadable (a `*Rejection` of `Receive`)              | 503                        | `{"error": "admission_unavailable"}`     |
| `api.ErrRateLimited` while resolving inputs or publishing       | 503 with `Retry-After: 60` | `{"error": "rate_limited"}`              |
| Any other error after authentication                            | 503 **[default]**          | `{"error": "unavailable"}`               |
| `Pulls.OpenPulls` fails for an upload into the default branch   | 503 (`rate_limited` for a rate limit) | `{"error": "unavailable"}`    |
| Merge group upload: the queue lists no entry with the run's commit (`*Rejection` of `Receive`) | 409                        | `{"error": "stale_group"}`               |
| Merge group upload: the queue, the members or the inputs cannot be read, or changed before the write | 503 (`rate_limited` for a rate limit; a missing Merge queues permission is `unavailable`) | `{"error": "unavailable"}` |

A pull request upload into the default branch lists the repository's open pull requests (`sweep.SharedWith`) before it
decides. Another open pull request with the same head commit and a base that is not the default branch is passed to
`verdict.Decide` as `Facts.SharedHead`, so the answer is `action_required` with `shared_head`, never `success`: a check
is written per head commit and that pull request would show it. The App accepts no protected base other than the
default branch. A failed listing is an error the Action retries, never a success. A merge group upload does the same for the
group commit, before deciding and again inside the lease: a group commit is a branch head a pull request into another
base can share. A `PullOpened` or `PullSynchronized` of a pull request into another base whose head no open pull request into the
default branch has also runs `sweep.SharedHeadPublication` for that head as a merge group target, inside its lease:
a success there is a merge group's, queued or merged (the default branch fast-forwards to a merged group commit, so a
pull request from it into another base is routine), and it becomes `shared_head`. Without a gate check on the head
nothing more is read; `ErrQueueForbidden` means no group result exists and nothing is written.

A merge group upload (`Binding.MergeGroup`) is decided on the group commit. Its `Resolve` hook inside the lease resolves the group again (queue, members, protected
files) and lists the open pull requests again; inputs or shared heads that differ from those of the decision write nothing and answer 503 (`feedback.ErrStaleInputs`).

`reason` is `Outcome.Reasons[0].Code`. A 2xx means "accepted and published", not "passed". The producer
retries on every non-2xx except 400, 401, 403, 409 and 413. On 503 the pending check stays pending.
The `Retry-After` value of 60 seconds is **[default]**.

Rate-limit rule, one for the whole gate: `api.ErrRateLimited` during `Receive` (before a decision exists) is the
429 `rate_limited` rejection from `gate/ingest`, passed through unchanged. `api.ErrRateLimited` after `Receive`
(resolving inputs or publishing) is 503 with `Retry-After`. The Action retries both, and the sweep repairs what the
retries do not. A rate limit before the gate check is written leaves the pending check pending and writes nothing
green. The one rate limit that `feedback` returns after the gate check is written, from the changed-file list that
only filters annotations, leaves the gate check with the decided conclusion; the retry adds the annotations. In the sweep the same error
stops the pass with `Summary.RateLimited`. In `HandleEvent` it is returned, also from `RunRepo` and `Recheck`, so the webhook queue
retries the delivery.
The handler works under a 50 second deadline **[default]**, inside the 60 second feedback target.

### Envelope 2 upload

Action 4 posts envelope 2 (`wire/envelope`, `archfit.upload-envelope.v2`) with the result JSON v1 to `POST /v1/reports`.
`UploadHandler` reads only the `schema_version` of the `X-Archfit-Envelope` header to choose the path; the envelope 1
path is unchanged and refuses any other header as before. Envelope 1 and envelope 2 are never accepted side by side
after the release: task 25 removes envelope 1.

Steps, in order:

1. `ingest.ReceiveV2` (`gate/ingest`, "Envelope 2"): authentication, the fixed workflow file
   `.github/workflows/archfit.yaml` (M2 D15), the latest attempt of the latest run of the head, the binding, and the
   result tied to the envelope. A `*Rejection` is the whole answer; nothing is stored.
2. Rules, each read from GitHub at the default branch's tip `T` now (`Repos.BranchHead`, `Repos.File`,
   `Commits.Compare`), never from the envelope alone. A failed rule adds a record (a `result.Unknown`: cause, what,
   why, next) and a reason code; any record makes the status `unknown`. Else the status is the result's (`pass`,
   `fail`, `unknown`).

| Rule | Reason code | Next step in the record |
| --- | --- | --- |
| an ingest `Problem` | `envelope_mismatch`, `unsupported_event`, `fork`, `result_invalid` | run the check again; open the pull request against the default branch; push the branch to this repository; pin archfit-action to a commit the App lists |
| the engine identity (version, image digest, platform) is a manifest 4 row (`manifest.EngineApproved`) | `unknown_engine` | pin archfit-action to a commit the App lists |
| `T` has a schema 3 policy (`.archfit.yaml` that `policy3.Decode` reads), or an administrator approved this pull request's head as its first policy (an open `first` proposal pinned to the upload's head with the run's policy digest, M2 task 23) | `first_policy` | an administrator approves the first policy in the App (task 23) |
| the repository is not outside the flow (`records.Repo.OutsideFlow`, set by a policy push no approval made, M2 task 20) | `outside_flow` | an owner confirms or reverts it in the App: `#<issue>` |
| the repository has an approved policy (`records.Repo.ApprovedDigest`, set by a consume or, in task 23, the first approval) | `first_approval` | an administrator approves the policy in the App |
| the policy at `T` is the approved one (`policy3.Digest` of `T`'s policy equals `ApprovedDigest`; not checked outside the flow or without an approved policy): a policy push no judgement has reached yet gives no verdict | `policy_unapproved` | the check runs again |
| `target_sha` is `T` or behind it (`Compare(target_sha, T)` is `identical` or `ahead`; a commit GitHub does not know is not) | `target_not_on_branch` | run the check again |
| `policy_digest` is the digest of `.archfit.yaml` at `target_sha` (a policy from the head fails here) | `policy_not_target` | restore the policy from the default branch |
| the digest at `target_sha` is the digest at `T` | `policy_changed` | the check runs again |
| `.github/workflows/archfit.yaml` at the run's commit (`Binding4.RunSHA`, the test merge commit of a pull request) is `T`'s (same blob, both ids present: an empty id proves nothing, review M1), or differs only in the ref and the version comment (` # v4.0.1`) of whole `uses: alexei-led/archfit-action@<ref>` lines (LF line ends, optional matching quotes), each new ref a commit manifest 4 lists (`manifest.ActionApproved`); any other text, a reference inside a script, a CR, NEL or U+2028 stays compared; or the file has the digest an owner approved for this pull request's head (an open `first` or `workflow` proposal pinned to the upload's head, `records.FileDigest`, M2 task 23) | `workflow_changed` | update the branch; if the pull request changes the workflow, an owner approves it in the App (task 23) |

A later push to a pull request needs a new approval: a proposal pinned to another head lifts nothing. The policy rules are skipped on a first policy, and the `target_sha` digest rules after `target_not_on_branch`. The two
state rules come first among the policy rules; `outside_flow` is checked before `first_approval`, and
`policy_unapproved` after the `target_sha` rules. Without an approved policy no upload passes: change control starts
with the first approval (manager, 2026-11-12). A lost push event leaves `policy_unapproved` until the sweep (task 22)
judges the tip. The
workflow rule always runs: a pull request runs its own workflow file, so a changed one could upload a forged `pass`
(design 9.2). A tag ref is never a pin bump here. A pull request whose test merge commit is older than a non-ref
change of the workflow on `T` reads `unknown` until its branch is updated.

3. The status `waiting` (M2 task 16, `gate/requests`): a pull request upload into the default branch, in required
   mode, whose result the App accepted as a `fail`, is `waiting` when every error asks an owner and its request key is
   an open request (`OpenIndex`, `Cover`). The result shown names the requests (`archfit: waiting, request #431`, each
   next step `wait for an owner: #431`), and the pull request joins them in the store (`Wait`). An advisory policy
   never waits: `waiting` is `in_progress`, which blocks a merge.
4. The pull record (M2 D6, `records.Pull` at `PullKey` with its `PullIndexKey` entry, one `records.Indexed` swap):
   pull request uploads only. It holds the run id and attempt, the payload digest, the status the App gave, and the
   result's date and error keys only when no rule failed. A record of a newer run, or a newer attempt of the same run, is never
   replaced: run ids grow, so a late upload of an older head cannot win. A newer record keeps the old one's `rechecks`
   markers (the re-check after an answer, M2 task 21). Push and merge group uploads store nothing
   here; a push's repository block goes to the drift issue (step 6).
5. The check and the comment (`feedback.Publisher4`, M2 task 15; see `gate/feedback`, "Archfit 4 publication"):
   `archfit/gate` on the head and, for a pull request, its one comment. The result shown is the engine's with the
   App's status, or, when a rule failed, only the App's records under `archfit: unknown, no verdict`, so a forged
   `pass` is never shown. `gate: required` in the default branch's policy makes a `fail` a failure; without it the
   `fail` is neutral. An upload for the pull request of an open proposal (`proposals.IsProposalPull`: its proposal
   index entry) publishes nothing either (M2 D13): the App sets `archfit/gate` on its own policy pull request, and the
   Action's result there is `unknown`, because the pull request changes the policy. Its pull record is kept.
   An upload with `unsupported_event` or `envelope_mismatch` publishes nothing: the App does not
   judge it for the default branch, and a run of another base has a higher run id than the pull request into the
   default branch on the same commit, which a re-run of that pull request keeps below. Inside the head's lease the
   default branch's head is read again, and for a coverable `fail` the open requests too; when either moved, nothing is
   written and the upload answers 503, so the Action's retry decides again. The commit keeps the check it had when nothing is published:

   | Before, on the commit | After a pull request into another base, or an upload that does not match its run |
   | --- | --- |
   | No `archfit/gate` | none: a ruleset that requires the check blocks the merge ("expected") |
   | A pending check (`in_progress`) | the same pending check: it blocks |
   | The result of the pull request into the default branch | the same result: it judged that commit |

   Two pull requests into the default branch on one commit share the check: it shows the newest run, and each keeps
   its own comment. No `archfit/state` check is written.
6. The drift issue (M2 task 22, `gate/drift`, façade file `drift.go`): a `push` run of the default branch (the event
   GitHub states) with no App record, at the tip the decision read, hands its `repository` block to `drift.Pushed`
   after re-reading the tip. No block is no state: nothing changes. A transient write failure answers 503 for the
   Action to retry; permanent GitHub refusals are logged without failing the upload.
7. The answer.

| Situation | Status | Body |
| --- | --- | --- |
| Decided, stored and published | 200 | `{"status": "pass" \| "fail" \| "waiting" \| "unknown", "reason": "<first reason code>"}`; no `reason` without a record |
| `*Rejection` of `ReceiveV2` | `Rejection.Status` | `{"error": "<Rejection.Code>"}` |
| `api.ErrRateLimited` after `ReceiveV2` | 503 with `Retry-After: 60` | `{"error": "rate_limited"}` |
| Any other error (a GitHub read or write, the Store) | 503 | `{"error": "unavailable"}`; the pull record is kept per run and attempt, so the Action's retry stores and publishes again |

A 200 means "decided", not "passed". Until the first engine 4 release is pinned in manifest 4, every envelope 2 upload
is `unknown` with `unknown_engine`. The log line `gate upload` carries `envelope: 2`, identifiers, the status and the
reason codes; no path, policy, result or token.

## Integrations

### gate/ingest (child)

- **Counterpart**: internal/gate/ingest
- **Direction**: gate depends on ingest
- **Strength**: functional (composition)
- **LCA / Rank / Distance**: gate / 1 / 1
- **Volatility**: high
- **Balanced?**: yes (functional strength at distance 1)
- **Shared knowledge**: `Upload`, `Binding`, `Problem`, `Receiver`, `Rejection` (restated in Public Contract
  above). `Receive` returns `*Rejection` (find it with `errors.As`) or a plain error for transient failures.
  `Binding.BaseRef` is the PR base branch, the default branch for a merge group, or for other events the branch the run executed on.
  `Binding.MergeGroup` is true when GitHub's merge queue of the default branch lists the run's commit as an entry's. A JWKS outage is
  the 503 `oidc_unavailable` rejection, which the Action retries. `Actions` and `Verifier` are restated below.
  `Repos` is a role interface of `github/api` that `*Client` implements; the façade only passes it to the children.
  `Pulls` is a role interface of `github/api` that `*Client` implements; the façade only passes it to the children.

<!-- contract: New — restated from internal/gate/ingest/module.md -->
```go
func New(repos Repos, pulls Pulls, actions Actions, queues MergeQueues, v Verifier, audience string, admit Admitter) Receiver
```

After it resolves the repository and checks the repository ID, `Receive` asks `Admitter.Admitted` for the installation
and the owner of that repository. Not admitted is the 403 `installation_not_approved` rejection, before the run is read.
An admission that cannot be read is the 503 `admission_unavailable` rejection, which the Action retries.

### gate/inputs (child)

- **Counterpart**: internal/gate/inputs
- **Direction**: gate depends on inputs
- **Strength**: functional (composition)
- **LCA / Rank / Distance**: gate / 1 / 1
- **Volatility**: high
- **Balanced?**: yes
- **Shared knowledge**: `Resolve` reads only the protected ref, returns an
  error on GitHub failures (fail closed), never on a failed read of the
  entitlement. Nothing approves a protected change: `Resolver` has only `Resolve`. `SettingsFound == false` means "not onboarded". `Admissions` is built by `app` and passed in
  `Deps.Admissions`; the façade, `ingest` and `sweep` ask it `Admitted`, `Resolve` does not.
  `PolicyDigests[0]` and `BaselineDigests[0]` are the first accepted digests: the base digest when the file exists on
  the protected ref; the lists are empty when it does not. The inputs constructor is `New(repos Repos, pulls Pulls, queues MergeQueues, s Store) Resolver`. A `Target` with `MergeGroup` takes the merge group path of `inputs`, and `Trusted.Group` carries what the queue said.
  `inputs` owns the digest predicate and the protected path set. The façade compares no digest and names no
  protected path: a receipt digest is the claim when `Accepts` takes it and `First` of the list otherwise, and the
  `DefaultBranchPush` trigger asks `MayBeProtected` for every changed path. `ProtectedPaths` is not called here
  (a push event carries no settings); the tests iterate it.

<!-- contract: Target, Trusted — restated from internal/gate/inputs/module.md -->
```go
// Target is the pull request (or default-branch push, or merge group) whose trusted inputs are resolved.
type Target struct {
    Repo        Repo
    PullRequest int // 0 for a default-branch push and a merge group
    HeadSHA     string
    BaseRef     string // protected ref; the default branch for every upload. Events and the sweep still set the pull request's base
    BaseSHA     string // exact protected-settings snapshot; Resolve pins BranchHead when empty
    Fork        bool
    MergeGroup  bool // HeadSHA is a merge group commit of BaseRef's merge queue
}

// Trusted is everything the gate believes, all read from the protected ref or GitHub itself.
type Trusted struct {
    Settings             Settings
    SettingsFound        bool
    SettingsSHA          string // exact protected-settings commit
    PolicyDigests        []string // the protected ref's digest; empty = no policy on the protected ref
    BaselineDigests      []string // same rule; empty = no baseline on the protected ref
    LabelsDigests        []string // same rule; empty = no labels file on the protected ref
    ProtectedPaths       []string // protected paths this PR changes; nothing approves such a change (owner_approval_missing)
    EarliestWaiverExpiry time.Time // zero = no waivers
    Suspended            bool
    Entitlement          Entitlement
    Group                *Group // set for a merge-group Target only: Problem "fork" | "unverified" | "conflict", Detail, Unapproved (a member that changes a protected path)
}
```

<!-- contract: Resolver — restated from internal/gate/inputs/module.md -->
```go
type Resolver interface {
    // Resolve reads the protected ref. It never reads policy, settings, baseline, owners, or CODEOWNERS from the head
    // tree.
    Resolve(ctx context.Context, t Target, now time.Time) (Trusted, error)
}
```

<!-- contract: Accepts, First — restated from internal/gate/inputs/module.md -->
```go
// Accepts is the digest predicate of the gate: whether claim is acceptable for one digest list of Trusted.
// An empty list accepts only the empty claim ("nothing trusted, nothing claimed"). A list with entries
// accepts only a claim that is not empty and is in the list, by exact string equality.
func Accepts(trusted []string, claim string) bool

// First returns the first digest of a list of Trusted, or "" for an empty list.
// For every list that Resolve builds, Accepts(trusted, First(trusted)) is true.
func First(trusted []string) string
```

<!-- contract: ProtectedPaths, MayBeProtected — restated from internal/gate/inputs/module.md -->
```go
// ProtectedPaths returns the protected path set for the settings of the protected ref, in fixed order:
// PolicyPath, BaselinePath, LabelsPath, Path (the settings file), s.Workflow. An empty s.Workflow is left out.
func ProtectedPaths(s Settings) []string

// MayBeProtected is the same rule for a caller that has not read the settings file (a push event):
// true for every path of ProtectedPaths(Settings{}) and for every path under ".github/workflows/",
// the only directory wire/settings accepts a workflow in. It is a superset of ProtectedPaths(s) for every parsed s.
func MayBeProtected(path string) bool
```

### gate/verdict (child)

- **Counterpart**: internal/gate/verdict
- **Direction**: gate depends on verdict
- **Strength**: functional (composition)
- **LCA / Rank / Distance**: gate / 1 / 1
- **Volatility**: high
- **Balanced?**: yes
- **Shared knowledge**: `Decide` is pure and total; `Reasons[0]` sets the conclusion; `Without` always returns `ActionRequired` (the façade does not call it: the fork
  refusal goes through `sweep.Without`). `Decide` never returns an empty `Reasons`; the façade still checks it, see "Report upload flow" step 6.
  The envelope 2 path does not call `Decide`: its status is the result's or `unknown` (see "Envelope 2 upload").
  A `ReasonCode` appears at most once. `owner_approval_missing` precedes `policy_mismatch` and `baseline_mismatch`, so an
  code-and-policy PR carries the receipt reason `owner_approval_missing`, which the sweep keys its re-run on (rule 7b). `engine_error` precedes `missing_required_coverage`.

<!-- contract: Conclusion, Category, ReasonCode, Reason, Outcome — restated from internal/gate/verdict/module.md -->
```go
// Conclusion is the only vocabulary the gate can publish. neutral and skipped are not representable.
type Conclusion string

const (
    Success        Conclusion = "success"
    Failure        Conclusion = "failure"
    ActionRequired Conclusion = "action_required"
)

// Category keeps architecture, evidence, governance and subscription outcomes from masquerading as each other.
type Category string

const (
    CategoryArchitecture Category = "architecture"
    CategoryEvidence     Category = "evidence"
    CategoryGovernance   Category = "governance"
    CategorySubscription Category = "subscription"
)

type ReasonCode string

const (
    ReasonHealthy             ReasonCode = "healthy"
    ReasonAttentionOnly       ReasonCode = "attention_only" // needs_attention, and nothing blocks
    ReasonBlockingFindings    ReasonCode = "blocking_findings"
    ReasonEngineError         ReasonCode = "engine_error"
    ReasonMissingCoverage     ReasonCode = "missing_required_coverage"
    ReasonBaselineNotComparable ReasonCode = "baseline_not_comparable"
    ReasonBaselineMismatch    ReasonCode = "baseline_mismatch"
    ReasonLabelsMismatch      ReasonCode = "labels_mismatch"
    ReasonPolicyMismatch      ReasonCode = "policy_mismatch"
    ReasonPolicyMissing       ReasonCode = "policy_missing" // settings merged, no policy on the protected ref yet
    ReasonPolicyDefect        ReasonCode = "policy_defect"  // a required rule's selector matches nothing
    ReasonUnknownIdentity     ReasonCode = "unknown_engine_identity"
    ReasonProfileMissing      ReasonCode = "measurement_profile_missing"
    ReasonWorkflowNotApproved ReasonCode = "workflow_not_approved"
    ReasonOwnerApprovalMissing ReasonCode = "owner_approval_missing"
    ReasonSettingsMissing     ReasonCode = "settings_missing"
    ReasonForkUnsupported     ReasonCode = "fork_unsupported"
    ReasonUnsupportedEvent    ReasonCode = "unsupported_event"
    ReasonEnvelopeMismatch    ReasonCode = "envelope_mismatch"
    ReasonReportInvalid       ReasonCode = "report_invalid"
    ReasonMissingUpload       ReasonCode = "missing_upload"
    ReasonStaleInputs         ReasonCode = "stale_inputs"
    ReasonWaiverExpired       ReasonCode = "waiver_expired"
    ReasonSuspended           ReasonCode = "subscription_suspended"
    ReasonTargetMoved         ReasonCode = "target_moved" // the default branch moved and, with this change, closes a module cycle
    ReasonSharedHead          ReasonCode = "shared_head"  // another open pull request into a non-default branch has this head commit
)

type Reason struct {
    Code     ReasonCode
    Category Category
    Detail   string // IDs and digests only
    Next     string // the recovery action
}

// Outcome is the gate decision. Reasons[0] determines Conclusion; the rest are preserved for feedback.
type Outcome struct {
    Conclusion Conclusion
    Reasons    []Reason
}
```

<!-- contract: Facts, Decide, Without — restated from internal/gate/verdict/module.md -->
```go
// Facts is the closed input of the decision. Report is nil when no usable report exists.
type Facts struct {
    Problems         []Problem
    BaselineDigest   string // claimed by the envelope; "" = none used
    LabelsDigest     string // claimed by the envelope; "" = none used
    Report           *Report
    IdentityApproved bool
    WorkflowApproved bool
    Trusted          Trusted
}

// Decide is a pure function. It checks, in order: subscription, settings, source and producer eligibility,
// governance (a protected change, which nothing approves), input integrity, evidence completeness, then the engine decision.
// The step table of gate/verdict is the normative order.
func Decide(f Facts) Outcome

// Without returns the fixed outcome for a target that has no report: code is one of ReasonMissingUpload,
// ReasonStaleInputs, ReasonWaiverExpired, ReasonForkUnsupported, ReasonSuspended, ReasonOwnerApprovalMissing,
// ReasonTargetMoved.
func Without(code ReasonCode) Outcome

// TargetMoved is Without(ReasonTargetMoved) for a caller that knows what moved: cycle names the modules of the module
// cycle the target and the pull request close together (none when the pull request could not be compared), base and
// tip the commits it was measured on and the default branch is on now. Detail holds the first five modules (then
// "and N more", each cut to 80 runes) and "<base12>..<tip12>"; Next names the cycle and the recovery.
func TargetMoved(cycle []string, base, tip string) Outcome
```

### gate/feedback (child)

- **Counterpart**: internal/gate/feedback
- **Direction**: gate depends on feedback
- **Strength**: functional (composition)
- **LCA / Rank / Distance**: gate / 1 / 1
- **Volatility**: high
- **Balanced?**: yes
- **Shared knowledge**: `Publish` is idempotent per receipt, writes `archfit/gate` only in required mode and only
  for pull requests, writes no comment for push targets or when settings are missing, and silently skips two kinds
  of write: a check whose existing receipt has a higher `(RunID, RunAttempt)` (run-based skip), and the comment when
  the PR head is no longer `Target.HeadSHA` (old-head skip; the old head's own checks are still written). Both
  return nil, so the upload still answers 200. `Pending` is idempotent. `State` reads the `archfit/gate` check of a head SHA.
  `Publish4` (constructor `New4`) writes the Archfit 4 check and comment of an envelope 2 upload, under the same
  per-head lease, never over a newer run or attempt of either path, and writes no `archfit/state`; with
  `Publication4.Current` set it writes nothing (`ErrStaleInputs`) when the default branch's head moved after the decision.
  The feedback constructor is `New(checks Checks, comments Comments, pulls Pulls, s Store, appURL string, now func() time.Time) Publisher`;
  `Checks` and `Comments` are role interfaces of `github/api` that `*Client` implements. `NewRepairer(checks, comments,
  issues, repos, s, now) Repairer` keeps the default branch's repair issue; `Repair` returns nothing, so no issue
  failure can reach the upload answer.

<!-- contract: Receipt — restated from internal/gate/feedback/module.md -->
```go
// Receipt is what a published gate check says about itself. It is stored only in GitHub:
// the check run external_id is "v1:<run_id>:<attempt>:<report_digest>", the rest is a hidden
// JSON marker in the check summary. The check is its own receipt.
type Receipt struct {
    RunID                int64      `json:"run_id"`
    RunAttempt           int        `json:"run_attempt"`
    ReportDigest         string     `json:"report_digest"`
    PolicyDigest         string     `json:"policy_digest"`
    BaselineDigest       string     `json:"baseline_digest"`
    LabelsDigest         string     `json:"labels_digest"`
    IdentityKey          string     `json:"identity_key,omitempty"` // the report's result identity, hashed; "" without one
    BaseSHA              string     `json:"base_sha,omitempty"`     // the default-branch commit the report was measured against; "" for a push or an old receipt
    EarliestWaiverExpiry time.Time  `json:"earliest_waiver_expiry"`
    Conclusion           Conclusion `json:"conclusion"`
    Reason               ReasonCode `json:"reason"`
}
```

<!-- contract: Publication, ErrStaleInputs, Publisher, CheckState, Decision — restated from internal/gate/feedback/module.md -->
```go
// Publication is one complete piece of feedback for one head SHA.
type Publication struct {
    Target   Target
    Identity Identity // claimed engine and image identity; zero when there was no upload
    Outcome  Outcome
    Receipt  Receipt
    Report   *Report // nil when there is no usable report
    PolicyChange *PolicyChange // the Action's classification, set only for a pull request that changes the policy file; nil on a sweep
    Trusted  Trusted
    // Resolve, when set, reads the trusted inputs again inside the head's publication lease, before anything is
    // written. When the result differs from Trusted in any field (a digest, the protected paths, the settings) or no
    // longer accepts a Receipt digest, the decision was computed from inputs that are no longer current: Publish
    // writes nothing and returns ErrStaleInputs, so it never replaces a publication computed from newer inputs. nil
    // skips the check; sweep corrections, which are never a success, leave it nil.
    Resolve func(context.Context) (Trusted, error)
}

// ErrStaleInputs is returned by Publish when Publication.Resolve shows that the trusted inputs changed after the
// decision. Nothing was written; the caller decides again from the new inputs.
var ErrStaleInputs error

// CheckState is the gate check (or, from AdvisoryState, the state check) currently on a head SHA.
type CheckState struct {
    Exists    bool
    Pending   bool      // in_progress, no upload yet
    StartedAt time.Time
    Receipt   Receipt   // zero when Pending
}

type Publisher interface {
    // Publish writes archfit/state, archfit/gate (only when Settings.Gate is required) and the
    // single PR comment. It is idempotent per Receipt and never overwrites feedback of a newer head or run. A
    // re-run on unchanged inputs updates the checks and the one comment in place; Receipt.IdentityKey never suppresses
    // a publication (the identity is a claim inside a customer-attested report).
    Publish(ctx context.Context, p Publication) error
    // Pending creates the in_progress gate check if none exists on the head SHA.
    Pending(ctx context.Context, t Target) error
    State(ctx context.Context, t Target) (CheckState, error)
    // AdvisoryState is State for archfit/state: an advisory pull request has no archfit/gate, and its receipt is the
    // one on the state check. It is never Pending: only archfit/gate is created ahead of an upload.
    AdvisoryState(ctx context.Context, t Target) (CheckState, error)
    // Warn adds or refreshes the waiver-expiry notice in the PR comment. Idempotent.
    Warn(ctx context.Context, t Target, expiry time.Time) error
    // Reconcile reads the gate check and calls decide with it, then publishes what decide returns, all inside the
    // head's publication lease: no other publication for the head can land between the read, the decision and the
    // write. A nil publication writes nothing. decide reads everything else it needs itself, inside the lease.
    Reconcile(ctx context.Context, t Target, decide Decision) error
}

// Decision decides, from the gate check as it is inside the publication lease, what Reconcile publishes; nil
// publishes nothing. An error writes nothing and is returned by Reconcile.
type Decision func(ctx context.Context, current CheckState) (*Publication, error)
```

<!-- contract: Repairer — restated from internal/gate/feedback/module.md -->
```go
// Repairer keeps the repair issue of the default branch: one open issue per branch that says what a push broke and
// closes when a push passes. It is a notification and never a gate.
type Repairer interface {
    // Repair opens, updates or closes the repair issue for p, the publication of a push to the default branch, once p
    // is published. It never fails: GitHub's answer (the Issues permission missing, a rate limit) is logged, and what
    // was published stays as it is.
    Repair(ctx context.Context, p Publication)
}
```

### gate/sweep (child)

- **Counterpart**: internal/gate/sweep
- **Direction**: gate depends on sweep
- **Strength**: functional (composition)
- **LCA / Rank / Distance**: gate / 1 / 1
- **Volatility**: high
- **Balanced?**: yes
- **Shared knowledge**: `Sweeper`, `Summary`, `Config` (restated in Public Contract above). `Run` returns a nil
  error with `RateLimited` set when GitHub's rate limit stopped the pass. `RunRepo` is the same pass for one
  repository, and it returns the rate limit as an error that wraps `api.ErrRateLimited`; it needs only `RepoRef` and `Private` of the `Repo` it is given. `Errors` counts skipped units.
  The sweep owns the rules the façade also needs, and exports them: the re-check after an answer (`AfterAnswer`,
  called by `approvals.go` when a push consumes a proposal), the publication for a target without a report
  (`Without`, called for the fork refusal of a new head), and the correction of a merge group's check
  (`GroupWithdrawal`, called for `MergeGroupDestroyed`). The re-run guard (`Rerun`) and the protected-change rule 5a
  (`Withdrawn` and `Withdrawal`) are the sweep's own. The façade has no copy of any of them.

<!-- contract: New — restated from internal/gate/sweep/module.md -->
```go
func New(repos Repos, pulls Pulls, actions Runs, r Resolver, p Publisher, admit Admitter, c Config) Sweeper
```

<!-- contract: RerunResult, Unmet, Runs, RerunInput, Rerun, AfterAnswer — restated from internal/gate/sweep/module.md -->
```go
type RerunResult int

const (
    RerunInFlight   RerunResult = iota // the run, or a newer run of the head, is queued or in progress
    RerunRequested                     // Actions.Rerun accepted the request
    RerunImpossible                    // a condition of design 10.5 does not hold (Unmet names it)
)

// Unmet: "" | "no_run" | "not_latest" | "policy_not_from_tip" | "too_old" | "base_changed".
type Unmet string

type Runs interface { Actions; WorkflowRuns; Commits }

type RerunInput struct {
    Receipt   Receipt // RunID and RunAttempt name the run to re-run
    Head      string  // the head now
    EventBase string  // the base of the event the run measured; "" when unknown
    Tip       string  // the default branch's head now; "" when unknown
    ReadsTip  bool    // the run's workflow reads the policy from the target tip
}

// Rerun is the re-run guard, the only caller of Actions.Rerun on the gate path: it requests a re-run only when the
// four conditions of design 10.5 hold, and names the first one that fails.
func Rerun(ctx context.Context, gh Runs, r RepoRef, in RerunInput, now time.Time) (RerunResult, Unmet, error)

// AfterAnswer re-checks the pull requests waiting on the requests of a consumed proposal: a re-run, else a branch update.
func AfterAnswer(ctx context.Context, gh AnswerGitHub, s Store, r Repo, c Consumed, now time.Time) error
```

<!-- contract: Without, Withdrawn, Withdrawal — restated from internal/gate/sweep/module.md -->
```go
// Without builds the publication for a target that has no report: verdict.Without(code),
// verdict.Suspended(tr.Entitlement) for ReasonSuspended, or verdict.ApprovalMissing(tr) for
// ReasonOwnerApprovalMissing, with the run identity of rc (zero when there is no previous receipt) and the
// current digests of tr.
func Without(code ReasonCode, t Target, tr Trusted, rc Receipt) Publication

// Withdrawn reports whether rc is Decided although tr now changes a protected path, which nothing approves: the
// protected settings came to protect a path the pull request changes. Success, failure and an action_required whose reason is report-derived and itself asks for action (missing required coverage, a baseline that is not comparable, a policy defect: see `Decided`) match, because the report is shown under them; any other receipt never does.
func Withdrawn(rc Receipt, tr Trusted) bool

// Withdrawal is rule 5a, decided inside the head's publication lease (Publisher.Reconcile) from the gate check
// current there and inputs tr resolved there: Without(ReasonOwnerApprovalMissing) over the current receipt when
// a check exists and its receipt is Withdrawn, in either gate mode, else nil.
func Withdrawal(t Target, current CheckState, tr Trusted) *Publication
```

### gate/records (child)

- **Counterpart**: internal/gate/records
- **Direction**: gate depends on records (the envelope 2 upload writes the pull record; milestone 2 tasks 16, 19, 20
  and 22 add more callers)
- **Strength**: contract
- **LCA / Rank / Distance**: gate / 1 / 1
- **Volatility**: high
- **Balanced?**: yes
- **Shared knowledge**: the `v4/` keys and their values (`Repo`, `Request`, `Approval`, `Proposal`, `Pull`, `Index`),
  `Encode`, `Decode`, `Load`, `Indexed`, `Join`, `Consume`, and the errors `ErrNoRecord`, `ErrMoved`, `ErrJoined`,
  `ErrFull`, `ErrExists`, `ErrTooLarge`. The child owns the Archfit 4 state and its swap rules; the façade will decide
  when a record changes. It reads and writes no Archfit 3 key.

### gate/requests (child)

- **Counterpart**: internal/gate/requests
- **Direction**: gate depends on requests
- **Strength**: functional (composition)
- **LCA / Rank / Distance**: gate / 1 / 1
- **Volatility**: high
- **Balanced?**: yes
- **Shared knowledge**: `Service` (`Comment`, `Filed`), built with the same `Publisher4` as the envelope 2 upload so
  both take one head lease; `IsRequest`; `OpenIndex`, `Cover`, `Wait` for the upload's status step; `PullClosed`.
  `View`, `Preview`, `ChangeOf`, `Edit` and the request page's types for `RequestViews` and `PolicyEdit` (façade file
  `preview.go`). The façade file of the intake is `requests.go`; the package name is imported as `asks`.

### gate/proposals (child)

- **Counterpart**: internal/gate/proposals
- **Direction**: gate depends on proposals
- **Strength**: functional (composition)
- **LCA / Rank / Distance**: gate / 1 / 1
- **Volatility**: high
- **Balanced?**: yes
- **Shared knowledge**: `Service` (`Digest`, `Answer`), built with the gate's GitHub client (it adds the roles
  `api.Changes` and `api.Merges`) and Store; `IsProposalPull` for the envelope 2 upload's publish step; the answer
  types and errors re-exported for `Answers` (façade file `answers.go`).

### gate/approvals (child)

- **Counterpart**: internal/gate/approvals
- **Direction**: gate depends on approvals
- **Strength**: functional (composition)
- **LCA / Rank / Distance**: gate / 1 / 1
- **Volatility**: high
- **Balanced?**: yes
- **Shared knowledge**: `Service` (`Pushed`, `WorkflowPushed`, `Bind`, `Act`), built with the gate's GitHub client and
  Store; `Push`, `Outcome`, the judgements, the owner action types and errors. The façade file `approvals.go` calls
  `Pushed` on `DefaultBranchPush`, logs the outcome with identifiers only (`gate policy push`) and hands a consume to
  `sweep.AfterAnswer`; `owner.go` calls `WorkflowPushed` after it, and `Bind` and `Act` for `OwnerActions`.
  `upload4.go` reads the state it records (`records.Repo`) and the open first and workflow proposals.

### gate/drift (child)

- **Counterpart**: internal/gate/drift
- **Direction**: gate depends on drift
- **Strength**: functional (composition)
- **LCA / Rank / Distance**: gate / 1 / 1
- **Volatility**: low
- **Balanced?**: yes
- **Shared knowledge**: `Service` (`Pushed`), built with the gate's GitHub client and Store; `Run`. The façade file
  `drift.go` calls it from the envelope 2 upload and logs the outcome with identifiers only (`gate drift issue`).

### wire/manifest

- **Counterpart**: internal/wire/manifest
- **Direction**: gate depends on wire/manifest
- **Strength**: contract
- **LCA / Rank / Distance**: root / 2 / 2
- **Volatility**: high (gate side); the manifest is moderate
- **Balanced?**: yes
- **Shared knowledge**: the manifest is locked in the binary (engine v3.2.0, `linux/amd64` and `linux/arm64`). `Lookup`
  is an exact match on all three fields; any empty field is a miss, so unknown never equals unknown. `Current` is not
  used here.

<!-- contract: Identity, Entry, Lookup, Current — restated from internal/wire/manifest/module.md -->
```go
// Identity is a producer's claimed engine and image identity.
type Identity struct {
    EngineVersion string
    ImageDigest   string // "sha256:<hex>"
    Platform      string // "linux/amd64" | "linux/arm64"
}

// Entry is one approved row of the locked manifest shipped in the binary.
type Entry struct {
    Identity
    ProfileVersion string // approved measurement profile version for this image
    ActionRef      string // pinned archfit-action commit SHA used in generated workflows
}

// Lookup reports whether id is approved. Unknown never compares as equal: any empty field is a miss.
func Lookup(id Identity) (Entry, bool)

// Current returns the entry new workflows are generated against (linux/amd64).
func Current() Entry
```

The envelope 2 upload uses manifest 4 instead: `EngineApproved(Identity)` (exact match, any empty field is a miss),
`ActionApproved(commit)` (a full commit SHA only) and `ActionRepository`. Manifest 4 ships empty, so every envelope 2
upload is `unknown_engine` until the release tasks pin the first engine 4 row.

<!-- contract: ActionRepository, EngineApproved, ActionApproved — restated from internal/wire/manifest/module.md -->
```go
const ActionRepository = "alexei-led/archfit-action"
func EngineApproved(id Identity) bool
func ActionApproved(commit string) bool
```

### wire/policy3 and wire/result

- **Counterpart**: internal/wire/policy3, internal/wire/result
- **Direction**: gate depends on both (the envelope 2 upload)
- **Strength**: contract
- **LCA / Rank / Distance**: root / 2 / 2
- **Volatility**: high (the engine owns both contracts)
- **Balanced?**: yes
- **Shared knowledge**: `policy3.Decode` says whether the default branch has a schema 3 policy; `policy3.Digest` is the
  digest compared with the envelope. The App's records are `result.Unknown` values, so they print with
  `result.Record`; `result.Status` names the status. The gate never parses the policy for a verdict.

### wire/report

- **Counterpart**: internal/wire/report
- **Direction**: gate depends on wire/report (field reads through `ingest.Upload.Report`; the façade does not import the package)
- **Strength**: contract (published language)
- **LCA / Rank / Distance**: root / 2 / 2
- **Volatility**: high (gate side); the schema is moderate
- **Balanced?**: yes
- **Shared knowledge**: the façade reads exactly two values, `Comparison.ConfigHash` (bare hex) and
  `Comparison.MeasurementProfile.Version`, and tests `Upload.Report` and `MeasurementProfile` for nil. It reaches them
  through `ingest.Upload.Report` and has no import of `wire/report`: a renamed field breaks the build of `gate`, which
  is why the integration is listed. The rest of the report passes through to verdict and feedback unread. A report has
  no exit code and no baseline or labels digest; `GateReference` is a top-level status that only verdict and feedback read.
  `Decision` is an opaque part of `Report` for this module. `Finding` is an opaque part of `Report` for this module.
  `Dimension` is an opaque part of `Report` for this module. `AgentTask` is an opaque part of `Report` for this module.
  `Coverage` is an opaque part of `Report` for this module. `Seam` is an opaque part of `Report` for this module.
  `Reference` is an opaque part of `Report` for this module. `StateSchemaV1` is the schema constant the decoder enforced.

<!-- contract: Report, StateSchemaV1, MaxBytes — restated from internal/wire/report/module.md -->
```go
// StateSchemaV1 is the engine state contract accepted (archfit.state.schema.json, engine v3.0.0, v3.0.1 and v3.2.0: one file).
const StateSchemaV1 = "archfit.architecture-state.v1"

// MaxBytes caps an uploaded payload: 5 MiB (the "5 MB" cap of decisions.md).
const MaxBytes = 5 << 20

// Report is the no-source transport form of an engine state report. The decoder knows every key of the engine
// state schema of v3.0.0, v3.0.1 and v3.2.0: keys listed here are kept (length-capped), the other known keys are validated and dropped
// (measurement, findings[].matched_by except MatchedByKeys, agent_tasks[].declarations, metric provenance, seam scores), and any key
// the schema does not define is a decode error.
type Report struct {
    SchemaVersion string               `json:"schema_version"` // must equal StateSchemaV1
    Verdict       string               `json:"verdict"`        // "healthy" | "needs_attention" | "blocked"
    Decision      Decision             `json:"decision"`
    Comparison    Comparison           `json:"comparison"`
    GateReference *Reference           `json:"gate_reference,omitempty"` // persisted-baseline comparison; nil = engine emitted none
    Dimensions    map[string]Dimension `json:"dimensions"`               // exactly the nine engine dimension keys
    Coverage      Coverage             `json:"coverage"`
    Findings      []Finding            `json:"findings"`
    AgentTasks    []AgentTask          `json:"agent_tasks"`
    Seams         []Seam               `json:"seams"`
}
```

<!-- contract: Comparison, Reference, MeasurementProfile, Producer — restated from internal/wire/report/module.md -->
```go
// Comparison binds the report to the inputs the engine actually used (report-only --base comparison).
type Comparison struct {
    Status             string              `json:"status"` // "not_requested" | "comparable" | "non_comparable"
    BaseRef            string              `json:"base_ref,omitempty"`
    ConfigHash         string              `json:"config_hash,omitempty"`         // bare lowercase hex SHA-256 of the raw policy bytes
    ClassificationHash string              `json:"classification_hash,omitempty"` // bare lowercase hex SHA-256 of the policy leaves that change measured facts
    ModelHash          string              `json:"model_hash,omitempty"`
    LabelsHash         string              `json:"labels_hash,omitempty"` // canonical hash of approved labels; not reproducible from raw bytes
    RubricVersion      string              `json:"rubric_version,omitempty"`
    MeasurementProfile *MeasurementProfile `json:"measurement_profile,omitempty"` // nil = unknown identity
    Reasons            []string            `json:"reasons"`
    OriginStatus       string              `json:"origin_status,omitempty"` // "comparable" | "unknown"; presentation only
    OriginReasons      []string            `json:"origin_reasons,omitempty"`
}

// Reference is the engine gate's comparison with the persisted baseline (top-level gate_reference).
// The engine exposes no baseline digest: only whether it loaded a baseline, whether that one was comparable, and
// the classes of input that made it not comparable. Reasons are engine text for display; decisions read Status,
// BaselinePresent and Drift.
type Reference struct {
    Status          string   `json:"status"`             // "comparable" | "non_comparable" | "not_requested"
    BaseRef         string   `json:"base_ref,omitempty"` // "baseline"
    Reasons         []string `json:"reasons"`            // e.g. "no baseline file was loaded"
    Drift           []string `json:"drift,omitempty"`    // closed set DriftClasses; empty when comparable
    BaselinePresent bool     `json:"baseline_present"`   // the engine loaded a baseline file
}

type MeasurementProfile struct {
    Version      string     `json:"version"`
    SettingsHash string     `json:"settings_hash"`
    Producers    []Producer `json:"producers"`
    Unknowns     []string   `json:"unknowns"`
}

type Producer struct {
    Tool             string `json:"tool"`
    SemanticsVersion string `json:"semantics_version"`
    Status           string `json:"status"` // "ok" | "partial" | "absent" | "disabled" | "timed out"
    ToolVersion      string `json:"tool_version,omitempty"`
    PartialBasis     string `json:"partial_basis,omitempty"` // "unresolved_specifiers" | "degraded_precision"
}
```

### wire/envelope

- **Counterpart**: internal/wire/envelope
- **Direction**: gate depends on wire/envelope (the `Kind` type and `KindReport`)
- **Strength**: contract
- **LCA / Rank / Distance**: root / 2 / 2
- **Volatility**: high (gate side); the schema is moderate
- **Balanced?**: yes
- **Shared knowledge**: the report route passes `KindReport` to `Receive`. The envelope itself is handled inside `gate/ingest`.

<!-- contract: Envelope, Kind, SchemaV1, KindReport, KindDiscovery, KindBaseline — restated from internal/wire/envelope/module.md -->
```go
// SchemaV1 is the only envelope schema version accepted.
const SchemaV1 = "archfit.report-envelope.v1"

// Kind names the payload that accompanies the envelope.
type Kind string

const (
    KindReport    Kind = "report"    // engine state report, POST /v1/reports
    KindDiscovery Kind = "discovery" // discovery draft policy, POST /v1/discoveries
    KindBaseline  Kind = "baseline"  // captured engine baseline file, POST /v1/baselines
    KindReanchor  Kind = "reanchor"  // re-anchored baseline, POST /v1/reanchors
)

// Envelope is what the producer states about a payload. Every field is self-reported:
// it must match authoritative GitHub data and is never trusted alone.
type Envelope struct {
    SchemaVersion string `json:"schema_version"` // must equal SchemaV1
    Kind          Kind   `json:"kind"`
    Repository    string `json:"repository"`     // "owner/name"
    Event         string `json:"event"`          // "pull_request" | "push" | "workflow_dispatch"
    PullRequest   int    `json:"pull_request"`   // 0 unless Event == "pull_request"
    HeadSHA       string `json:"head_sha"`       // 40 lowercase hex
    BaseSHA       string `json:"base_sha"`       // "" unless pull_request
    MergeBaseSHA  string `json:"merge_base_sha"` // "" unless pull_request
    AnalyzedSHA   string `json:"analyzed_sha"`   // the commit the engine measured; the test merge commit when Integration is "merged"
    Integration   string `json:"integration"`    // "merged" | "conflicts" for a pull_request, "" otherwise
    RunID         int64  `json:"run_id"`
    RunAttempt    int    `json:"run_attempt"`
    EngineVersion string `json:"engine_version"` // e.g. "v3.0.1"
    ImageDigest   string `json:"image_digest"`   // "sha256:<64 hex>"
    Platform      string `json:"platform"`       // e.g. "linux/amd64"
    PayloadDigest  string `json:"payload_digest"`  // "sha256:<64 hex>" of the exact payload bytes
    BaselineDigest string `json:"baseline_digest"` // bare hex SHA-256 of the baseline file the engine loaded; "" = none; always "" for discovery and baseline
    LabelsDigest   string `json:"labels_digest"`   // bare hex SHA-256 of the labels file the engine loaded; "" = none
}
```

### wire/settings

- **Counterpart**: internal/wire/settings
- **Direction**: gate depends on wire/settings (types only)
- **Strength**: contract
- **LCA / Rank / Distance**: root / 2 / 2
- **Volatility**: high (gate side); the file format is moderate
- **Balanced?**: yes
- **Shared knowledge**: the façade reads `Trusted.Settings.Workflow` and `Trusted.Settings.Gate`, and calls
  `Trusted.Settings.IsAgent` for the author of a pull request. It does not use `Path`: the protected path set, the
  settings file included, belongs to `gate/inputs`.

<!-- contract: Settings, GateMode, DataMode, Path — restated from internal/wire/settings/module.md -->
```go
// Path is the App settings file on the protected ref, beside the engine policy.
const Path = ".archfit-app.yaml"

type GateMode string

const (
    GateAdvisory GateMode = "advisory" // publish archfit/state and the comment only
    GateRequired GateMode = "required" // also publish archfit/gate, fail closed
)

type DataMode string

const (
    DataCustomer DataMode = "customer" // source never reaches Archfit
    DataManaged  DataMode = "managed"  // managed discovery and managed LLM allowed after consent
)

// Settings is the App-owned file. The engine policy schema is untouched by it.
type Settings struct {
    Version  int      `yaml:"version"`          // must be 1
    Owners   []string `yaml:"owners"`           // GitHub logins, or "org/team-slug"; at least one
    Agents   []string `yaml:"agents,omitempty"` // logins or App slugs of coding agents, which never approve; nil when none
    Gate     GateMode `yaml:"gate"`             // default advisory
    Workflow string   `yaml:"workflow"`         // e.g. ".github/workflows/archfit.yaml"
    DataMode DataMode `yaml:"data_mode"`        // default customer
}

// IsAgent reports whether login is one of s.Agents. Case is ignored, and "name" and "name[bot]" are one identity:
// GitHub names an App's account "slug[bot]" and a file may name either form, so a missed suffix never lets an agent
// through. "" is never an agent.
func (s Settings) IsAgent(login string) bool
```

### github/webhook

- **Counterpart**: internal/github/webhook
- **Direction**: gate depends on github/webhook (the `Event` type)
- **Strength**: contract
- **LCA / Rank / Distance**: root / 2 / 2
- **Volatility**: high (gate side); the webhook model is low
- **Balanced?**: yes
- **Shared knowledge**: `app` verifies the signature and parses before calling `HandleEvent`; identity fields come
  from the signed payload, so the façade may build a `RepoRef` from them. `HeadRepo != Repo` is a fork.

<!-- contract: Event, EventKind — restated from internal/github/webhook/module.md -->
```go
type EventKind string

const (
    PullOpened         EventKind = "pull_opened"         // opened, reopened, ready_for_review
    PullSynchronized   EventKind = "pull_synchronized"   // new head SHA
    PullClosed         EventKind = "pull_closed"
    DefaultBranchPush  EventKind = "default_branch_push"
    InstallationAdded  EventKind = "installation_added"  // created, or repositories added
    InstallationRemoved EventKind = "installation_removed" // deleted
    ReposRemoved       EventKind = "repos_removed"
    MergeGroupRequested EventKind = "merge_group_requested" // checks_requested: GitHub built a merge group commit
    MergeGroupDestroyed EventKind = "merge_group_destroyed" // destroyed: merged, dequeued or invalidated
    Ignored            EventKind = "ignored"
)

// Event is the normalized form of a verified delivery. Identity fields come from the signed payload only.
type Event struct {
    Kind           EventKind
    DeliveryID     string
    InstallationID int64
    Repo           string // "owner/name", "" for installation events
    Account        string // installation kinds only: the account login from the payload; "" when it has none
    RepoPrivate    bool
    PullRequest    int
    HeadSHA        string
    HeadRepo       string // differs from Repo for forks
    BaseRef        string
    DefaultBranch  string   // the repository's default branch; "" when the payload does not name one
    Branch         string   // default_branch_push only: the branch, without "refs/heads/"
    ChangedPaths   []string // default_branch_push only
    Before         string   // default_branch_push only: the commit before the push; "" when the push created the branch
    GroupReason    string   // merge_group_destroyed only: "merged", "dequeued", "invalidated" or what GitHub sends
}
```

### github/api

- **Counterpart**: internal/github/api
- **Direction**: gate depends on github/api
- **Strength**: contract
- **LCA / Rank / Distance**: root / 2 / 2
- **Volatility**: high (gate side); the client is low
- **Balanced?**: yes
- **Shared knowledge**: `*Client` implements every role interface; the façade hands the same client to each child
  under the role it needs and itself calls only `Pulls.OpenPulls`. `ErrQueueForbidden` (the Merge queues permission is
  missing) and `ErrQueueTooLong` are the sentinels of `MergeQueue`. `ErrRateLimited` is the sentinel
  returned after bounded backoff; `ErrNotFound` is the sentinel for a missing object. `TokenSource` is the
  credential interface of `github/auth` that `app` passes to the client constructor; the gate never sees it.
  The `New` in the last block below is the client constructor of `github/api`, called by `app`, not by the gate.

<!-- contract: RepoRef, Repo, ErrNotFound, ErrRateLimited — restated from internal/github/api/module.md -->
```go
// RepoRef addresses a repository inside an authenticated installation.
// Callers build it only from a verified webhook, verified OIDC claims, or an authorized session.
type RepoRef struct {
    InstallationID int64
    FullName       string // "owner/name"
}

type Repo struct {
    RepoRef
    ID            int64
    Private       bool
    DefaultBranch string
}

var (
    ErrNotFound    = errors.New("github: not found")
    ErrRateLimited = errors.New("github: rate limit exhausted") // returned after bounded backoff
)
```

<!-- contract: Actions, WorkflowRun — restated from internal/github/api/module.md -->
```go
type WorkflowRun struct {
    ID           int64
    Attempt      int
    Event        string
    HeadSHA      string
    HeadRepo     string
    WorkflowPath string // e.g. ".github/workflows/archfit.yaml"
    Status       string
    Conclusion   string
    PullRequests []int // PR numbers GitHub associates with the run
}

// RunFilter narrows LatestRun. Each non-empty field is sent as the GitHub query parameter of the same name.
type RunFilter struct {
    Branch string
    Event  string // e.g. "push", "workflow_dispatch"
    Status string // a run status or conclusion, e.g. "completed", "success"
}

type Actions interface {
    Run(ctx context.Context, r RepoRef, runID int64) (WorkflowRun, error)
    Rerun(ctx context.Context, r RepoRef, runID int64) error
    Dispatch(ctx context.Context, r RepoRef, workflowPath, ref string, inputs map[string]string) error
    // LatestRun is the newest run of the workflow that matches every non-empty field of f.
    LatestRun(ctx context.Context, r RepoRef, workflowPath string, f RunFilter) (WorkflowRun, error)
    // Artifact downloads the named artifact of a run, capped at maxBytes.
    Artifact(ctx context.Context, r RepoRef, runID int64, name string, maxBytes int64) ([]byte, error)
}
```

<!-- contract: Client, New — restated from internal/github/api/module.md -->
```go
// Client implements every role interface. Consumers depend on the role interfaces they need.
type Client struct{ /* unexported */ }

func New(ts TokenSource, hc *http.Client) *Client
```

### github/oidc

- **Counterpart**: internal/github/oidc
- **Direction**: gate depends on github/oidc (the `Verifier` it passes to ingest)
- **Strength**: contract
- **LCA / Rank / Distance**: root / 2 / 2
- **Volatility**: high (gate side); OIDC verification is low
- **Balanced?**: yes
- **Shared knowledge**: a verified token authenticates the producer; it never proves the payload is true.
  `Deps.Audience` is the App base URL.

<!-- contract: Claims, Verifier, ErrUnavailable — restated from internal/github/oidc/module.md -->
```go
// Claims are the verified GitHub Actions OIDC claims the App uses.
type Claims struct {
    Repository        string // "owner/name"
    RepositoryID      int64
    RunID             int64
    RunAttempt        int
    SHA               string
    Ref               string
    EventName         string
    JobWorkflowRef    string // "owner/name/<path>@<ref>"
    RunnerEnvironment string // "github-hosted" | "self-hosted"
}

// ErrUnavailable means the signing keys could not be fetched; the caller answers 503 so the producer retries.
var ErrUnavailable = errors.New("oidc: signing keys unavailable")

// Verifier validates signature (cached JWKS), issuer, audience, and time claims.
// A verified token authenticates the producer; it never proves the payload is true.
type Verifier interface {
    Verify(ctx context.Context, token, audience string) (Claims, error)
}
```

### store

- **Counterpart**: internal/store
- **Direction**: gate depends on store (passes `Deps.Store` to `gate/inputs`, which reads the entitlement and the
  admission, to `gate/feedback`, which holds the publication leases, and to the Archfit 4 children, which keep the
  `v4/` records)
- **Strength**: contract
- **LCA / Rank / Distance**: root / 1 / 1
- **Volatility**: high (gate side); store is low
- **Balanced?**: yes
- **Shared knowledge**: the gate path calls `Get(Tenant(installationID), EntitlementKey)` and
  `Get(Tenant(installationID), AdmissionKey)` inside `gate/inputs`, `Get` and
  `CompareAndSwap` on `feedback/lease/<head_sha>` under the same tenant inside `gate/feedback`, and `Get` and
  `CompareAndSwapAll` (through `store.Update`) on `v4/...` keys under the same tenant inside `gate/records`. It calls
  no other Store operation. `ErrNotFound` is the store sentinel for a missing key; for the entitlement it means "not
  suspended", for a lease that the lease is free. `SystemTenant` is never used by the gate, and the gate never
  calls `Tenants`.

<!-- contract: Tenant, SystemTenant — restated from internal/store/module.md -->
```go
// Tenant is the authenticated GitHub installation ID that owns a key.
// Every key is stored under its tenant prefix; no operation crosses tenants.
type Tenant int64

// SystemTenant holds App-wide keys (the managed-LLM budget counter). It is never a GitHub installation ID.
const SystemTenant Tenant = 0
```

<!-- contract: Store, ErrNotFound — restated from internal/store/module.md -->
```go
// ErrNotFound is returned by Get when the key does not exist for the tenant.
var ErrNotFound = errors.New("store: key not found")

// Store is the only persistence seam of the App. Values are opaque bytes.
// Implementations must be safe for concurrent use.
type Store interface {
    Get(ctx context.Context, t Tenant, key string) ([]byte, error)
    Put(ctx context.Context, t Tenant, key string, value []byte) error
    Delete(ctx context.Context, t Tenant, key string) error // deleting a missing key is not an error
    // Add atomically adds delta to the integer at key (missing = 0) and returns the new value.
    Add(ctx context.Context, t Tenant, key string, delta int64) (int64, error)
    // CompareAndSwap atomically sets key to value when it currently holds exactly old, and reports whether it did.
    // A nil old matches only a missing key; a nil value deletes the key. A false result changes nothing.
    CompareAndSwap(ctx context.Context, t Tenant, key string, old, value []byte) (bool, error)
    // CompareAndSwapAll applies every swap in one atomic step when every key currently holds exactly its Old,
    // and reports whether it did. Each swap follows the rules of CompareAndSwap. A false result changes nothing.
    CompareAndSwapAll(ctx context.Context, t Tenant, swaps []Swap) (bool, error)
    // DeleteTenant removes every key of the tenant. Idempotent.
    DeleteTenant(ctx context.Context, t Tenant) error
    // Tenants lists every tenant that owns at least one key, SystemTenant excluded.
    // Used only by the sweep to forget installations whose uninstall webhook was missed.
    Tenants(ctx context.Context) ([]Tenant, error)
}

// Swap is one key of CompareAndSwapAll. A nil Old matches only a missing key; a nil Value deletes the key.
// A swap whose Value equals its Old only checks the key.
type Swap struct {
    Key        string
    Old, Value []byte
}
```

### Consumers (inbound)

- **internal/app**: contract (constructor, handlers), root / 1 / 1, low, balanced. Calls `New`, mounts
  `UploadHandler`, calls `HandleEvent` before `control.HandleEvent`, calls `Sweep` from `/internal/sweep`,
  passes `NewReceiver(...)` to `control`, builds the `Admissions` for `Deps.Admissions` and `NewReceiver`, and passes
  the `*Gate` to `control` as its `RequestViews` and its `Answers`.
- **internal/control/account**: contract, root / 2 / 2, moderate, balanced. Writes the JSON of `Entitlement`
  under `EntitlementKey`, and writes and deletes the JSON of `Admission` under `AdmissionKey`. The gate owns the
  records so that it never imports billing or operator code.
- **internal/control/onboarding**: contract, root / 2 / 2, high, balanced. Uses `Receiver`, `Rejection`, `Upload`,
  `Binding`, `Problem` for `POST /v1/discoveries`. A discovery upload never reaches `verdict` or `feedback`.
- **internal/control/ui**: contract, root / 2 / 2, high, balanced. Calls `FirstScreen` and `RecoveryStep` for the pull
  request page and the overview's re-run rows. Through `control.Deps.RequestViews` (the same `*Gate`) it calls
  `RequestView` and `RequestPreview` for the request page, read access only. Through `control.Deps.Answers` and
  `control/answers` it calls `AnswerDigest` and `Answer`, with the passkey's `Redeem`. Nothing on the upload (OIDC)
  path holds `RequestViews` or `Answers`.

## Internal Design

### Composition

`New(d)` builds, once: `ingest.New(d.GitHub, d.GitHub, d.GitHub, d.GitHub, d.OIDC, d.Audience, d.Admissions)`,
`inputs.New(d.GitHub, d.GitHub, d.GitHub, d.Store)`, `feedback.New(d.GitHub, d.GitHub, d.GitHub, d.Store, d.Audience, d.Now)` (the audience is the App's base URL, so
the comment links to the App's pull request page), `feedback.NewRepairer(d.GitHub, d.GitHub, d.GitHub, d.GitHub, d.Store, d.Now)`, and
`sweep.New(d.GitHub, d.GitHub, d.GitHub, resolver, publisher, d.Admissions, d.Sweep)`. `verdict` has no constructor.
`NewReceiver` is `ingest.New` with the same argument pattern. A nil `Deps.Now` means `time.Now` **[default]**.
`compose` also builds the envelope 2 path: `ingest.NewV2` over the same roles plus `WorkflowRuns`, then
`Repos`, `Commits` and the Store for its rules and its pull record.
`New` delegates to the unexported `compose`, which takes the nine `github/api` roles (`Repos`, `Pulls`, `Actions`,
`Checks`, `Comments`, `Issues`, `MergeQueues`, `WorkflowRuns`, `Commits`) as one interface, and
`compose` to `newGate`, which takes the children as interfaces; both exist so that tests can replace GitHub or a child.

| Child      | Contributes                                                                                              | Depends on siblings                                           |
| ---------- | -------------------------------------------------------------------------------------------------------- | ------------------------------------------------------------- |
| `ingest`   | authenticated, bound `Upload` or a `Rejection`                                                           | none                                                          |
| `inputs`   | `Trusted` for a `Target`; owns the `Entitlement` and `Admission` records, the digest predicate and the protected path set | none                                                        |
| `verdict`  | `Outcome` from `Facts`; `Without`, `Suspended`                                                           | `ingest` (`Problem`), `inputs` (`Trusted`) — model            |
| `feedback` | checks, comment, receipt, repair issue; `State`, `Pending`, `Warn`, `Repair`                             | `verdict` (`Outcome`), `inputs` (`Target`, `Trusted`) — model |
| `sweep`    | reconciliation of open PRs and the re-check after an answer; owns `Rerun`, `Without`, `AfterAnswer`                                                   | `inputs`, `feedback`, `verdict`, `records` — contract                    |

### Report upload flow

1. `u, err := receiver.Receive(ctx, req, KindReport)`. `*Rejection` → answer and stop. Nothing is published.
2. `target = Target{Repo: u.Binding.Repo, PullRequest, HeadSHA, BaseRef, Fork, MergeGroup}` from the binding. For a non-PR
   upload (`PullRequest == 0`) `Target.BaseRef` is `Repo.DefaultBranch`, not `Binding.BaseRef`: the binding names the
   branch the run executed on, which is a protected ref only when it is the default branch. `MergeGroup` is
   `Binding.MergeGroup`: `Resolve` then reads the queue and the members.
3. `trusted, err := resolver.Resolve(ctx, target, now)`. Error → 503; the pending check stays.
4. `IdentityApproved` = `manifest.Lookup(u.Identity)` hits **and** `entry.ProfileVersion != ""` **and**
   `u.Report.Comparison.MeasurementProfile != nil` **and** its `Version == entry.ProfileVersion`. A manifest row
   without a profile version approves nothing, so two empty versions never compare equal (for engine v3.2.0 the manifest holds `archfit.measurement.v2`). False when `u.Report == nil`.
5. `WorkflowApproved` = `trusted.SettingsFound` **and** `u.Binding.WorkflowPath != ""` **and** it equals
   `trusted.Settings.Workflow`. A workflow file changed by the PR is a protected path; nothing approves it, so the check stays `owner_approval_missing`.
6. `outcome = verdict.Decide(Facts{Problems: u.Problems, BaselineDigest: u.BaselineDigest, LabelsDigest: u.LabelsDigest,
   Report: u.Report, IdentityApproved, WorkflowApproved, Trusted: trusted})`. The two digests are the envelope's claims,
   copied unchanged; they are customer-attested like the report. A decision without a reason cannot come from
   `Decide`; if one ever does, the façade treats it as a failed step: 503 `unavailable`, nothing published, the
   pending check stays. It never reads `Reasons[0]` of an empty list and never invents a reason.
7. Receipt (table below); `publisher.Publish(ctx, Publication{target, u.Identity, outcome, receipt, u.Report, trusted,
   Resolve})`, with `PolicyChange: u.PolicyChange` only when `trusted.ProtectedPaths` names the policy file (the App's
   own diff backs the card; the Action's word alone never shows one, and a sweep publication has none).
   `Identity` is the envelope's claim; feedback prints it as claimed. `Resolve` calls
   `resolver.Resolve(ctx, target, now)` again, inside the publication lease: a sweep may have corrected the check
   from newer inputs between step 3 and the lease, under the same run identity. When the new inputs differ from `trusted` in any field or no longer accept a receipt
   digest, feedback writes nothing and returns `ErrStaleInputs`; the upload answers 503 `unavailable` and
   the Action's retry decides from the new inputs. The correction stays until then.
8. For a run on a push to the default branch (`Binding.Event == "push"`, `PullRequest == 0`, `Binding.BaseRef ==
   Repo.DefaultBranch`) and only after step 7 returned `nil`: `repairer.Repair(ctx, publication)`. It opens, updates or
   closes the branch's repair issue and returns nothing: an issue failure (no Issues permission, a rate limit) is
   logged and the answer stays the published decision. A push to another branch or an unbound pull request run
   (`PullRequest == 0` too) never reaches it.
9. Answer 200 with conclusion and reason.

| `Receipt` field        | Value                                                                                            |
| ---------------------- | ------------------------------------------------------------------------------------------------ |
| `RunID`, `RunAttempt`  | from `Binding`                                                                                   |
| `ReportDigest`         | `Upload.Digest`                                                                                  |
| `BaseSHA`              | `Binding.BaseSHA` when `Binding.Integration` is `merged`; `Binding.MergeBaseSHA` when it is `conflicts`; else `""` (sweep rule 5c compares it with the default-branch tip) |
| `PolicyDigest`         | the report's `ConfigHash` when `Accepts(PolicyDigests, ConfigHash)`; else `First(PolicyDigests)` |
| `BaselineDigest`       | `Upload.BaselineDigest` when `Accepts(BaselineDigests, it)`; else `First(BaselineDigests)`       |
| `LabelsDigest`         | `Upload.LabelsDigest` when `Accepts(LabelsDigests, it)`; else `First(LabelsDigests)`             |
| `EarliestWaiverExpiry` | `Trusted.EarliestWaiverExpiry`                                                                   |
| `Conclusion`, `Reason` | `outcome.Conclusion`, `outcome.Reasons[0].Code`                                                  |

`Accepts` and `First` are the digest predicate of `gate/inputs`. A receipt therefore only names digests that
`Accepts` takes for the current lists, so the sweep (which tests a receipt with the same function) never finds a
fresh receipt stale. A nil report has the claim `""` for the policy.

Special targets, all through the same flow:

- Default-branch push (`PullRequest == 0`): the "eligible default-branch run". feedback writes `archfit/state` on
  the commit only; no gate check, no comment. Then the repair issue (step 8): opened or updated when the decision is
  not a success with a report the App shows and the repository is `gate: required`; closed when it is a success, in any
  mode.
- No settings file on the protected ref (not onboarded): `Decide` returns `settings_missing`; feedback writes `archfit/state` only.
- Settings merged but no policy on the protected ref yet (between onboarding PR 1 and PR 2): `Decide` returns
  `policy_missing`, not `policy_mismatch`. The receipt's `PolicyDigest` is `""`, so once the policy PR merges the sweep
  (or the `DefaultBranchPush` repository pass) finds the receipt stale and re-runs.
- An upload that could not be bound to a pull request (`unsupported_event`, ambiguous association): `PullRequest == 0`,
  so it is published on the run's head SHA as `archfit/state` only. The PR's own gate check stays pending and the sweep times it out.
- Fork: `ProblemFork` → `fork_unsupported`; `inputs` reads nothing from the head.
- Engine error: `archfit check` exits 3 and writes no report, so nothing is uploaded. The pending gate check stays
  and the sweep ends it as `missing_upload`. The upload path never sees an exit code.
- No baseline anywhere (none on the protected ref, none claimed): allowed **[default]**. The engine's `non_comparable`
  reference ("no baseline file was loaded") is ignored by verdict, and feedback discloses that every finding counts as new.

### HandleEvent

`Target.Repo` is built from the verified event: `Repo{RepoRef{e.InstallationID, e.Repo}, Private: e.RepoPrivate}`;
`ID` and `DefaultBranch` stay zero and are not needed for pull request targets **[default: saves one API call]**.

| `Event.Kind`                     | Reaction                                                                                                                                                                                                                                                         |
| -------------------------------- | ---------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------- |
| any kind with a repository       | Checked before the rows below: `Admissions.Admitted(e.InstallationID, Owner(e.Repo))` false → reaction `not_admitted`, nothing read or written, `nil` returned. An error → reaction `not_admitted` logged, nothing read or written, the error returned, so the webhook queue retries the delivery. Installation kinds carry no repository and skip this check. |
| `PullOpened`, `PullSynchronized` | A payload base that is not the payload's default branch (`otherBase`): nothing is decided or published for it, but the head may be shared with a pull request into the default branch, whose checks it would show. One `OpenPulls` listing finds the open pull requests into the default branch whose head is `HeadSHA`; for each, `Reconcile` runs `sweep.SharedHeadPublication` inside the head's lease (reaction `shared_head`): when the check is Pending or a success and a fresh listing still shows the shared head, `shared_head` is published over it (over a pending check with the zero receipt, which any later upload replaces). Because it waits for the lease, it runs either before an in-flight upload, which then lists again inside its lease and answers 503, or after it, and withdraws the success. Only failures of these units (the listing, the check read, the lease) are returned, so the queue retries; no other pull request of the repository is read. A base changed after opening sends no event (`edited` is not mapped): the hourly sweep, rule 5d, is the net. Otherwise `Resolve`. `!SettingsFound` → nothing. Fork → `Publish(sweep.Without(ReasonForkUnsupported, target, trusted, Receipt{}))`: a zero-run receipt carrying the base digests. Else `Settings.Gate == GateRequired` → `publisher.Pending(target)`. Advisory → nothing. |
| `MergeGroupRequested`            | `otherBase` (the event's `BaseRef` is not its `DefaultBranch`) or an empty `DefaultBranch` → nothing (reaction `other_base`): only the default branch's queue is gated. Otherwise the target is the group commit (`MergeGroup`, `PullRequest` 0, `BaseRef` = the default branch) and only the settings are resolved (`MergeGroup` unset for this `Resolve`): `!SettingsFound` → nothing (`not_onboarded`); `Settings.Gate == GateRequired` → `publisher.Pending(target)` on the group commit (reaction `pending`); advisory → nothing. A group whose upload never arrives stays pending until GitHub's status check timeout fails it; the hourly sweep does not scan merge groups. |
| `MergeGroupDestroyed`            | `otherBase`, an empty `DefaultBranch`, or `GroupReason == "merged"` (the result stands) → nothing. Otherwise `Reconcile(groupTarget, …)` (reaction `group_checked`): inside the lease, no gate check → nothing; else `Resolve` of the group, then `sweep.GroupWithdrawal(target, current, trusted)`. A group no longer in the queue resolves `unverified`, so a pending or decided check becomes `action_required` `merge_group_unverified`. Never a success. |
| `PullCommented`                  | A comment with a request line (`asks.IsRequest`) → `gate/requests`' `Comment` (M2 task 16; reactions `request_*`). Any other comment → nothing (reaction `none`), no API call. |
| `IssueOpened`                    | `gate/requests`' `Filed`: an issue the CLI opened with a request line becomes a request, is closed with a note, or gets one reply. |
| `DefaultBranchPush`              | The policy judgement (`approvals.Pushed` with `Before`, `HeadSHA` and `ChangedPaths`, M2 task 20): a consume, outside the flow, or nothing; its error is returned and the queue retries, and a retry finishes the same judgement. Then `ChangedPaths` empty (= unknown), or `inputs.MayBeProtected(path)` for any changed path → `sweeper.RunRepo(ctx, repo, now)` in the request. Otherwise `sweeper.Recheck(ctx, repo, HeadSHA, now)` in the request: sweep rule 5c alone, which the full pass of `RunRepo` includes.                                                                                                   |
| `PullClosed`                     | Reaction `pull_closed`: `asks.PullClosed` drops the pull request from the Archfit 4 pull index (its pull record stays).                                                                                                                                                            |
| installation kinds, `Ignored`    | nothing; `control` handles installations.                                                                                                                                                                                                                        |

Protected default-branch push: `gate/inputs` owns the protected path set, and `MayBeProtected` is its form for a
caller without settings. The exact fifth protected path is `Settings.Workflow`, which lives on the protected ref, so
`MayBeProtected` takes the whole `.github/workflows/` directory instead **[default]**: a superset that needs no file
read; an extra `RunRepo` is harmless because the pass is idempotent. The façade names no path and no directory. `RunRepo` re-resolves every open PR of the repository, so a changed base policy or baseline shows
as `stale_inputs` (plus a re-run) within the webhook request instead of within the hour, in advisory mode too
(sweep rule 0b). The hourly sweep stays the
recovery path when the push event is lost. A rate limit in `RunRepo` is returned from `HandleEvent`, so the webhook
queue retries the push.

Fork refusal publication: `sweep.Without(ReasonForkUnsupported, target, trusted, Receipt{})`. The sweep owns the
construction (its "Without publications" table); the zero previous receipt gives a zero run identity, because no
workflow run reported for this head, and the digests are `First` of each trusted list. The sweep's rule 1 builds the
same publication for a fork it finds unmarked, so the two paths cannot differ. The `external_id` is then `v1:0:0:`.
Any later upload for the head has a higher run pair and replaces it.

Nothing approves a protected change, so no event re-evaluates one: a review, a push to a pull request branch or a
comment from an owner leaves the result as it is (`pull_request_review` and a push to a branch other than the default
branch are `Ignored` by `github/webhook`). A pull request that changes a protected path stays `owner_approval_missing`.
The sweep ends it when the change goes away (rule 7b, one re-run) and withdraws a decided result when the settings come
to protect a path the pull request changes (rule 5a, `Withdrawn` and `Withdrawal`). The re-run guard is `sweep.Rerun`;
the façade has none.

The handler is idempotent. An error is returned to `app` (which logs it); the sweep repairs what a failed or lost
event left undone.

### Sweep

`Gate.Sweep(ctx)` = `sweeper.Run(ctx, d.Now())`. It fails independently of `control.Sweep`.

## Change Vectors

- A new pipeline step (for example an approved self-hosted runner group) as a new child or a new `Facts` field.
- Supporting `workflow_run` or forks: inside `ingest` and `verdict`; the façade contract stays.
- A different webhook reaction, or more triggers for the one-repository sweep.
- Splitting the gate into its own deployment: the import rule already guarantees it builds alone.

## Constraints and Invariants

- The gate never imports `internal/control`, `internal/app`, any LLM provider package, or any UI package, directly or
  transitively. Allowed: standard library, `wire/*`, `github/*`, `store`, gate siblings. No runtime call reaches
  billing, LLM or UI code either. A test enforces it.
- Nothing outside `gate` imports `gate/*`. Children never import the parent.
- Fail closed: every path ends in `success`, `failure`, `action_required`, a rejection status, or a 503 that leaves
  the check pending. On the envelope 1 path, never `neutral` or `skipped` on `archfit/gate`, and `success` comes only
  from `verdict.Decide`. On the envelope 2 path, `success` comes only from a `pass` result with no failed App rule,
  `neutral` only from a `fail` in an advisory policy (design 11.6), and an upload the App does not judge for the
  default branch writes nothing.
- Never trust head-tree config, labels, baselines, owners or CODEOWNERS. Trusted inputs come from the protected ref
  and are verified by digest; there is no exception, no approval makes a head file trusted.
- Provenance is necessary, not sufficient. Feedback says "customer-attested"; nothing says "verified". The
  fabricated-report gap (a trusted workflow running a PR script that forges a clean report) is not closed and not claimed closed.
- Unknown identity never compares equal: a manifest miss, a missing profile or a profile version mismatch is never approved.
- The tenant comes from verified OIDC claims (`ResolveRepo`) or the verified webhook; never from the envelope or a client-supplied ID.
- Nothing runs for an installation no operator approved. The façade (`HandleEvent`, `RequestView`, `RequestPreview`,
  `AnswerDigest`, `Answer`), `ingest` (upload) and `sweep` (pass) each ask `Admitted` before they read or write for it. An admission that cannot be read (a Store error, or a
  record that does not decode) does nothing and is retried: `HandleEvent` returns the error, the upload answers 503
  `admission_unavailable`, the sweep counts the installation in `Summary.Errors`. It is never a refusal and never
  "admitted", unlike the entitlement. The gate only reads the admission; `control/account` writes it.
- The gate path reads the `Entitlement` and the `Admission`. It writes only publication leases and the Archfit 4 records
  of `gate/records`. The stored Archfit 3 records (`owner-decision/`, `approval/`, `pusher/`, `review-request/`,
  `decision/`) stay untouched and unread until a post-release cleanup.
- Nothing approves a protected change: no push, no review, no App answer, no agent identity. A pull request that changes
  a protected path is `owner_approval_missing`; the policy changes only through the App's policy pull request, which an
  owner answers in the App (`gate/proposals`).
  A missing or unreadable `Entitlement` means "not suspended" **[default]**; it never establishes non-payment and
  never blocks a merge by itself (2 second read timeout in `inputs` **[default]**). Recovery of a missing record is
  not the gate's job: `control`'s sweep backfills it (Observe + Refresh for every installation), so a missed
  installation webhook delays the trial start by at most one sweep interval.
- Every write to GitHub for one head SHA holds that head's publication lease in the Store (`gate/feedback`), so an
  older publication cannot overwrite a newer one from a stale read, also across instances. When the lease cannot be
  taken because the Store fails, nothing is written: the upload answers 503 `unavailable`, `HandleEvent` returns the
  error and the sweep counts it, and the check stays as it was until the Store is back **[default: fail closed]**.
  An unserialized write would reopen the overwrite. So a Store outage now blocks new verdicts, where before only
  the entitlement read depended on it. A publication that failed after a GitHub request keeps the lease until it
  expires (60 s), so a write still in flight lands first; a retry of the same head waits for it.
- An upload decision is written only while the inputs it was decided from are still trusted inside the lease (step
  7), so it never replaces a sweep correction computed from newer inputs.
- Suspension is `action_required` in category `subscription`: never an architecture failure, never a success.
  A restored entitlement re-runs the workflow; an old receipt is never promoted to `success`.
- Persisted gate state is limited to publication leases and the Archfit 4 records. They contain no report payloads. Publication leases hold only a token and expiry and are deleted after writes. Reports and payloads live in memory for one request and are dropped.
- Handlers do their work in the request and are idempotent; GitHub holds the dedup keys (`external_id`, comment marker,
  repair issue marker).
- The repair issue never gates: no `Repair` outcome changes an upload answer or a check, and the sweep never calls it.
- GitHub rate-limit exhaustion yields 429 `rate_limited` during `Receive`, 503 with `Retry-After` after it, or a
  stopped sweep; the Action retries both statuses; checks stay pending; never green.
- Logs (`log/slog` default logger **[default]**, because `Deps` carries none): installation ID, repository ID, PR number,
  head SHA, run ID, attempt, digests, conclusion, reason codes, HTTP status, timings. Never report bodies, paths, messages, tokens.
- Standard library first; the gate adds no third-party dependency.
- One owner per rule inside the gate. The digest predicate and the protected path set belong to `gate/inputs`; the
  re-run guard and the publication without a report belong to `gate/sweep`. The façade calls them and holds no digest
  comparison, no protected path, no `Actions.Rerun` call and no `verdict.Without` call of its own.
- Declared restatement, the fork rule: "a pull request is a fork when its head repository is not the base repository"
  is computed three times, because each path gets the two names from a different source. The owner is `gate/ingest`
  (`Binding.Fork`, from the verified OIDC claims and `Pulls.Pull`); `gate` restates it in `eventTarget` from the signed
  webhook payload (`Event.HeadRepo != Event.Repo`), and `gate/sweep` from `OpenPulls` (`Pull.HeadRepo != Repo.FullName`).
  All three treat an empty head repository (a deleted fork) as a fork. If they drift, a fork is refused on one path
  and judged on another; `TestHandleEventRules`, the ingest binding tests and `TestForksStayBlocked` pin each copy.
  Remove the copies when `inputs.Target` gains a constructor that takes the two names.

## Test Specification

Composition tests wire the real children through the unexported `compose` (the body of `New`, taking the
`github/api` role interfaces instead of the concrete `*Client`) over an in-memory fake of those role interfaces, a
stub `Verifier`, `store.NewMemory` (`NewMemory` is the in-process Store constructor of `internal/store`) behind a
wrapper that panics on every write, a failing Store stub, a fixed clock, and report fixtures copied into
`internal/gate/testdata/report/`. The pipeline, the HTTP mapping, the receipt and the `HandleEvent` rules are also
tested over hand-written fakes of the children through the unexported `newGate`. `New` and `NewReceiver` themselves
are driven only as far as they go without GitHub. Child internals are tested in the children.

### Unit Tests

- **Test name**: `TestGateImportsNothingForbidden`
  **Scenario**: run `go list -deps ./internal/gate/...`.
  **Expected behavior**: no package under `internal/control` or `internal/app`, no LLM provider package, no `html/template` or UI package; only standard library, `internal/wire/*`, `internal/github/*`, `internal/store` and gate packages.
- **Test name**: `TestNobodyImportsGateChildren`
  **Scenario**: run `go list` over the module and collect importers of `internal/gate/*`.
  **Expected behavior**: importers are only `internal/gate` and gate siblings.
- **Test name**: `TestIdentityApproved` (T01)
  **Scenario**: table: manifest hit with matching profile version; hit with another profile version; hit with no profile; unknown image digest; empty engine version; empty platform; both report and manifest "unknown"; nil report.
  **Expected behavior**: true only for the first row; unknown never compares equal.
- **Test name**: `TestWorkflowApproved`
  **Scenario**: binding path equals settings; differs; empty binding path with empty settings (not onboarded); reusable workflow from another repository (`""`).
  **Expected behavior**: true, false, false, false.
- **Test name**: `TestReceiptConstruction`
  **Scenario**: matching policy, baseline and labels digests; approved head policy digest; mismatching `config_hash`; a claimed baseline digest not on the protected ref; no baseline and no labels file anywhere; nil report; a merged pull request, a head that conflicts and a push with a base and a merge base in the binding.
  **Expected behavior**: the values of the receipt table, all bare hex; the first accepted digest when a claim does not match; `""` when both sides are empty; `BaseSHA` is the base tip, the merge base and `""` respectively.
- **Test name**: `TestReceiptDigestsAreAlwaysAccepted`
  **Scenario**: every combination of a trusted list (empty, one digest, two digests) and a claim (`""`, each entry, an unknown digest, a `sha256:`-prefixed entry), for the policy (as the report's `ConfigHash`), the baseline and the labels.
  **Expected behavior**: the receipt digest is the claim exactly when `inputs.Accepts(list, claim)`, and `inputs.First(list)` otherwise; `inputs.Accepts(list, receipt digest)` is true in every case, which is the sweep's "not stale". `verdict.Decide` reports `baseline_mismatch` (or `labels_mismatch`, `policy_mismatch`) for exactly the combinations where the receipt digest is not the claim.
- **Test name**: `TestProtectedPushFollowsInputs`
  **Scenario**: iterate `inputs.ProtectedPaths` for a parsed `Settings`; one push per path; a push with only unprotected paths; a push with no paths.
  **Expected behavior**: `RunRepo` is called for every path of the set and for the empty list, never for the unprotected push. The test names no path, so a sixth path in `gate/inputs` needs no edit here.
- **Test name**: `TestHTTPMapping`
  **Scenario**: every row of "Upload answers", with each rejection status once.
  **Expected behavior**: the listed status, JSON body and `Retry-After`; no body ever contains report content.

- **Test name**: `TestUploadPipeline`
  **Scenario**: hand-written fakes of the children through `newGate`: a clean pull request upload; a non-PR upload with `ProblemUnsupportedEvent` and `ProblemFork` whose binding names a feature branch; an upload whose claimed baseline digest is not trusted.
  **Expected behavior**: `Receive` gets `KindReport` under a deadline of about 50 seconds; `Resolve` gets the target of the binding and the clock; the `Publication` equals the one built from `verdict.Decide` and the receipt table, apart from `Resolve`, which is set and returns the resolver's current inputs for the same target. The non-PR target has `BaseRef` = the default branch and keeps `Fork`; the answer is `fork_unsupported`. An upload for a pull request into another base resolves against the default branch too, gets `ProblemUnsupportedEvent` added, and is never a success: `unsupported_event`. The sweep skips it (`reactionOtherBase` for an event; the webhook `Event` carries `DefaultBranch`), so the sweep never re-runs a receipt that the inputs read at that base could not match. Its `PullOpened` and `PullSynchronized` events decide nothing but check the pull requests into the default branch on the same head (`shared_head`, see the events table), returning an error only when one of those units fails so the queue retries the delivery. The upload lists the open pull requests again inside the publication lease (the `Resolve` hook): if the shared pull requests differ from those the decision was made with, or the listing fails, nothing is written and the answer is 503; the retry decides `shared_head`. The claimed digest reaches `Decide` unchanged: `baseline_mismatch`.
- **Test name**: `TestUploadPipeline`, subtest "only a published default-branch push reaches the repair issue"
  **Scenario**: bindings of a push to the default branch, a push to another branch, a pull request, an unbound pull
  request run, a dispatched run on the default branch; and a default-branch push whose `Publish` fails.
  **Expected behavior**: `Repair` is called once, with the published receipt, for the first row only.
- **Test name**: `TestHandleEventRules`
  **Scenario**: the `HandleEvent` table over fakes of the children: a new head in required mode; a fork of a repository that is not onboarded; a deleted fork repository (`HeadRepo == ""`); an error from each child call (`Resolve`, `Pending`, `RunRepo`); `Sweep`.
  **Expected behavior**: the target is built from the verified event; the not-onboarded fork gets nothing; the deleted fork gets the `sweep.Without(ReasonForkUnsupported, …)` publication with a zero run and the first trusted digests; every child error is returned and nothing is published; `Sweep` passes the clock and the summary through.
- **Test name**: `TestNewComposesTheChildren`
  **Scenario**: `New` with a real `*api.Client` that has no transport, a stub verifier and a nil `Deps.Now`; one `POST /v1/reports` without an envelope.
  **Expected behavior**: a nil `Deps.Now` means `time.Now`; the composed handler answers 400 with an error code, so the real children are wired as far as they go without GitHub.

### Integration Contract Tests

- **Test name**: `TestUploadPublishesApprovedProducer` (T09)
  **Scenario**: healthy fixture, valid token, matching envelope and digest, required mode, pending check present.
  **Expected behavior**: 200 `success`/`healthy`; the pending `archfit/gate` run is completed in place; `archfit/state` and one comment exist; the receipt holds run, attempt and the three digests.
- **Test name**: `TestUploadNegativeProvenance` (T03, T09)
  **Scenario**: table: bad signature, wrong audience, expired token, wrong tenant, unknown envelope field, oversized body, modified report bytes, stale head, older attempt, signing keys unavailable.
  **Expected behavior**: 401, 401, 401, 403, 400, 413, 400, 409, 409, 503 `oidc_unavailable`; no check or comment written; no `success` exists.
- **Test name**: `TestUploadProblemsArePublished` (T03, T13)
  **Scenario**: `workflow_run` upload; fork PR upload; invalid report with correct digest; envelope naming another PR; modified workflow path; wrong image digest; missing profile.
  **Expected behavior**: 200 with `action_required` and the matching reason each time (`unsupported_event`, `fork_unsupported`, `report_invalid`, `envelope_mismatch`, `workflow_not_approved`, `unknown_engine_identity`, `measurement_profile_missing`).
- **Test name**: `TestHandleEventReactions`
  **Scenario**: `PullOpened` in required mode, in advisory mode, without settings, from a fork; `PullSynchronized`; `PullClosed`; `DefaultBranchPush` touching only code; `InstallationAdded`; the same delivery twice.
  **Expected behavior**: pending check; nothing; nothing; `fork_unsupported` published; pending check on the new head; nothing for the rest (a code-only default-branch push runs the rule 5c recheck, which writes nothing here); redelivery changes nothing.
- **Test name**: `TestProtectedPushTriggersRepoSweep`
  **Scenario**: table of `DefaultBranchPush.ChangedPaths`: `.archfit.yaml`; `.archfit-baseline.json`; `.archfit-labels.yaml`; `.archfit-app.yaml`; `.github/workflows/archfit.yaml`; another workflow file; only source files; empty list (unknown); each with a second repository that has stale PRs.
  **Expected behavior**: `RunRepo` for the pushed repository in the first six rows and for the empty list; `Recheck` (rule 5c alone) instead for source-only pushes, and only after `BranchHead` is the pushed commit (`TestDefaultBranchPushRechecksTheTarget` pins the call, the tip and the retryable error); the other repository is never touched; a rate limit inside `RunRepo` is returned as a retryable error that wraps `api.ErrRateLimited`, and nothing is written.
- **Test name**: `TestNewReceiverIsTheSameReceiver`
  **Scenario**: run the ingest rejection table through `NewReceiver(...)` with `KindDiscovery`.
  **Expected behavior**: identical rejections; a discovery `Upload` carries `Payload` and a nil `Report`; no check is written.
- **Test name**: `TestEntitlementContract`
  **Scenario**: write `Entitlement` JSON under `EntitlementKey` as `control/account` does; upload for a private repository past `SuspendAfter`.
  **Expected behavior**: `action_required`/`subscription_suspended` with the record's `Reason` as next action.

### Boundary Tests

- **Test name**: `TestNoNeutralNoSkippedEver` (T13)
  **Scenario**: run every fixture and every negative case of this specification in required mode; collect all `archfit/gate` writes.
  **Expected behavior**: each conclusion is `success`, `failure` or `action_required`; pending checks are `in_progress`.
- **Test name**: `TestTransientFailuresLeavePending`
  **Scenario**: GitHub 5xx and rate-limit exhaustion during `Resolve` and during `Publish`.
  **Expected behavior**: 503 (`Retry-After` for rate limit); the pending check is unchanged; a retry of the same upload 60 s later, when a lease kept by a failed write has expired, completes once.
- **Test name**: `TestClientSuppliedTenantIsIgnored`
  **Scenario**: another installation's ID in headers, query and envelope-like fields, with an entitlement stored for that other tenant.
  **Expected behavior**: the Store is read under the claim's tenant only; no API call addresses the other tenant.
- **Test name**: `TestNoSourceLeaves` (T04)
  **Scenario**: seeded leakage fixtures uploaded with correct digests.
  **Expected behavior**: `action_required`/`report_invalid`; seeded strings appear in no log line, check, comment or HTTP body.
- **Test name**: `TestLogsCarryOnlyIdentifiers`
  **Scenario**: upload, webhook and sweep flows with marker strings in paths and messages, capturing logger.
  **Expected behavior**: IDs, digests, outcomes, timings only.

- **Test name**: `TestStoreWritesOnlyLeasesAndInputsRecords`
  **Scenario**: a Store wrapper that panics on `Put`, `Delete`, `Add`, `DeleteTenant`, `Tenants` and on a `CompareAndSwap` outside feedback leases; exercise event, upload and sweep flows, also for a pull request that replaces the baseline.
  **Expected behavior**: no panic; every record/lease uses the installation tenant; a push records no pusher; the publication lease is released after success. Reads are of the entitlement and of the repository's `v4/` records, which a default-branch push reads to judge its policy (M2 task 20).
- **Test name**: `TestUnadmittedInstallationGetsNothing`
  **Scenario**: the installation is refused by the admission fake; a `PullOpened` event, then an upload; then the refusal is lifted and a `PullSynchronized` event and an upload follow.
  **Expected behavior**: no pending check and no state check while refused; the upload answers 403 `installation_not_approved`; after the lift the upload answers 200 `success`/`healthy`.

### Behavior Tests

- **Test name**: `TestMergeGroupThatViolatesPolicyFailsOnTheGroupCommit`, `TestValidMergeGroupSucceedsOnTheGroupCommitOnly`
  **Scenario**: two queued pull requests that each pass alone; a group upload that breaks a rule only together, and a healthy one.
  **Expected behavior**: `failure` `blocking_findings`, and `success` `healthy`, on the group commit only; the pull request heads keep their own results; no comment and no details link for a group; another group's commit is untouched.
- **Test name**: `TestMergeGroupUploadNotQueuedIsRefused`, `TestMergeGroupQueueUnreadableIsNeverASuccess`, `TestMergeGroupWithoutSettingsGetsNoPendingCheck`
  **Scenario**: an upload for a group the queue no longer lists; an upload while the queue answers `ErrQueueForbidden`; a `checks_requested` event of a repository without settings.
  **Expected behavior**: 409 `stale_group` and no check; 503 and no success; no pending check.
- **Test name**: `TestMergeGroupWithUnapprovedProtectedChangeInAnyMember`, `TestMergeGroupPolicyChangeNeverPasses`, `TestMergeGroupWithForkMember`
  **Scenario**: a policy change by an earlier member; the same change with the upload naming the combined file's digest, for the later group and for the earlier group; a fork member.
  **Expected behavior**: `owner_approval_missing` for the later group, also if GitHub let the earlier group fail; `owner_approval_missing` for both groups, whichever policy the upload names, and no success; `fork_unsupported`.
- **Test name**: `TestMergeGroupDestroyedInvalidatesItsResult`, `TestMergeGroupMergedKeepsItsResult`
  **Scenario**: a decided or pending group check; `merge_group` `destroyed` with a reason other than `merged`, and with `merged`.
  **Expected behavior**: `action_required` `merge_group_unverified` (a late upload then gets 409 `stale_group`), never a success; the result stands.
- **Test name**: `TestLegitimateChangePassesAndViolationFails` (T02)
  **Scenario**: the paired healthy and blocked fixtures of each claimed erosion scenario; the warning-only attention fixture.
  **Expected behavior**: `success`, `failure`, `success` on `archfit/gate`; the upload answer for the `needs_attention` verdict is `success`/`attention_only`, with `archfit/state` `neutral`; feedback names rule, modules, locations and the validation command.
- **Test name**: `TestTamperingNeverYieldsSuccess` (T12, T14)
  **Scenario**: head-edited policy, baseline, settings (owners, gate mode), workflow and CODEOWNERS; a head-edited labels file; an envelope that claims no baseline while a baseline exists; a report whose `gate_reference` is missing or `non_comparable` while a baseline exists.
  **Expected behavior**: `policy_mismatch`, `baseline_mismatch`, `labels_mismatch`, `baseline_not_comparable` or `owner_approval_missing`; never `success`.
- **Test name**: `TestCodeAndPolicyPullRequestNeverPasses` (T14)
  **Scenario**: a PR changes code and policy; the engine ran with the head policy; the same upload twice.
  **Expected behavior**: `owner_approval_missing`, and the feedback names `policy_mismatch` too; the receipt keeps the protected ref's digest; the second upload is `action_required` again, with no re-run; never `success`.
- **Test name**: `TestNonComparableBaselineRecovery` (T15)
  **Scenario**: a measurement profile that differs from the baseline's makes `gate_reference.status` `non_comparable` while `verdict` is `healthy`; then a baseline change reaches the default branch and the workflow re-runs.
  **Expected behavior**: `baseline_not_comparable` with the baseline-PR recovery text; `success` only after the new baseline is on the protected ref.
- **Test name**: `TestDuplicateAndOutOfOrderDelivery` (T03, T11)
  **Scenario**: the same upload twice; an older attempt after a newer one; an upload for a superseded head.
  **Expected behavior**: one comment, updated in place; the newer result stands; 409 for the stale uploads.
- **Test name**: `TestSuspensionAndRestoration` (T21)
  **Scenario**: private repository past trial and grace; upload; then the record is replaced by a grant and the sweep runs; separately the Store fails during an upload.
  **Expected behavior**: `action_required`/`subscription`, not `failure` and not `success`; after restoration a re-run is requested and only the fresh report turns the check green; with the entitlement unreadable the upload is decided normally (an unreadable record is not non-payment).
- **Test name**: `TestGateSurvivesControlPlaneFailure`
  **Scenario**: no `control` package wired at all; the entitlement unreadable while the publication leases work; `control.HandleEvent` returning errors after `gate.HandleEvent`.
  **Expected behavior**: uploads, pending checks and the sweep complete.
- **Test name**: `TestStoreOutageFailsClosed`
  **Scenario**: every Store operation fails, the leases included: a `PullOpened` event, an upload and a `Sweep`; then the Store recovers and the event and upload are repeated.
  **Expected behavior**: `HandleEvent` returns an error; the upload answers 503 `unavailable` without a conclusion; the sweep counts errors; no check or comment is written and nothing is green. After recovery the pending check and then `success` are written.
- **Test name**: `TestBasePolicyChangeReachesOpenPullsAtOnce` (T12, T16)
  **Scenario**: three open PRs with `success` receipts; a policy PR merges and the `DefaultBranchPush` event arrives; separately the same merge with the event lost, followed by the hourly `Gate.Sweep`.
  **Expected behavior**: within `HandleEvent` each PR shows `stale_inputs` and one re-run is requested per PR; with the event lost the sweep performs the same repair; in both cases nothing is green until a fresh report arrives, and a later sweep adds nothing.
- **Test name**: `TestBrokenDefaultBranchOpensOneRepairIssue`
  **Scenario**: in the world, a push to the default branch uploads the blocked fixture; separately the
  missing-evidence fixture.
  **Expected behavior**: `failure` and `action_required` answers and state checks as without the issue; exactly one open
  issue titled "Architecture repair needed on main", unassigned, whose body starts with the repair and head markers
  and names the HEAD and the run link; for the blocked fixture the cause, the blocking findings, the agent task block
  between its markers and the validation command.
- **Test name**: `TestFurtherBrokenPushUpdatesTheSameIssue`
  **Scenario**: two broken pushes, with the same and with other findings; then the second upload once more.
  **Expected behavior**: one issue, the same number, naming only the second push; the repeated upload edits nothing.
- **Test name**: `TestPassingPushClosesTheRepairIssue`, `TestPassingPushWithoutAnIssueWritesNothing`
  **Scenario**: a broken push, then a passing one; separately a passing push with no issue.
  **Expected behavior**: the issue is closed with one comment naming the passing HEAD; without an issue no issue is
  created, edited or closed and no comment is written.
- **Test name**: `TestIssueFailureNeverChangesTheUpload`
  **Scenario**: every `Issues` call fails, as without the Issues permission (`403`) and as rate limited.
  **Expected behavior**: the answer is `failure`/`blocking_findings`, the state check is published, the log has a
  failed `feedback repair issue` line, and the head's lease is released.
- **Test name**: `TestOlderRunNeverRewritesTheIssue`
  **Scenario**: on one commit, an older run after a newer break and after a newer pass; a re-run of a commit the
  branch left, after a pass and after a newer break.
  **Expected behavior**: the issue keeps the newer run's body; no issue is opened over the newer pass; the old commit
  neither reopens the closed issue nor renames the open one.
- **Test name**: `TestOnlyDefaultBranchPushesTouchIssues`
  **Scenario**: a blocked pull request upload; a blocked upload of a push to another branch.
  **Expected behavior**: no `Issues` call.
- **Test name**: `TestRepairIssueAssignee`
  **Scenario**: settings with `repair_assignee: alice`; GitHub assigns her, or refuses her.
  **Expected behavior**: the issue is assigned to alice; when refused it is opened unassigned and the refusal is logged.
- **Test name**: `TestSweepThroughFacade` (T13, T16)
  **Scenario**: lost `PullOpened` webhook; no upload for 60 minutes; base policy change; waiver expiry; all via `Gate.Sweep` with the fixed clock.
  **Expected behavior**: the repairs of `gate/sweep`; `Summary` counts match; nothing turned green without a fresh report.
- **Test name**: `TestUpload4AcceptsAPullRequest`, `TestUpload4AcceptsAPushAndAMergeGroup`
  **Scenario**: envelope 2 of the engine goldens `pass` and `deny` on a pull request; a push to the default branch; a merge group.
  **Expected behavior**: 200 with the result's status; the pull record (run, attempt, payload digest, status, the result's date, error keys) and its index entry for the pull request only; no check or comment write.
- **Test name**: `TestUpload4GivesUnknown`, `TestUpload4FirstPolicy`, `TestUpload4KeepsNoErrorKeysOfAResultItOverrides`
  **Scenario**: a wrong policy digest; a policy from the head; a policy that changed since the run; `target_sha` off the default branch or unknown to GitHub; an unknown engine, and the shipped manifest 4; an envelope that disagrees with GitHub; a base other than the default branch; an unreadable result; no policy or a version 2 policy on the default branch, also with the workflow added; a `fail` from an edited workflow.
  **Expected behavior**: 200 `unknown` with the rule's reason code first; the pull record stored `unknown` with no error keys; the first policy keeps the run and payload digest.
- **Test name**: `TestUpload4WorkflowFile`
  **Scenario**: a pull request that edits the workflow and uploads a forged `pass`; a ref-only Action bump to a manifest 4 commit, with and without its comment, quoted; a bump to an unlisted commit, to a tag, with another edit, glued to a `#`, with a quote that does not close, with a CRLF line end; a comment hiding a U+2028 or NEL line break or a command substitution; a changed Action reference inside a `run:` script; the workflow removed; a test merge commit older than a ref bump on the default branch.
  **Expected behavior**: `unknown` `workflow_changed` for every change but the ref-only bumps, which `pass`; the record says to update the branch or have an owner approve.
- **Test name**: `TestUpload4KeepsTheNewestRecord`, `TestUpload4RejectionsStoreNothing`, `TestUpload4AnswersAFailedReadWithARetry`, `TestUploadHandlerKeepsEnvelope1OnItsPath`, `TestUpload4LogsIdentifiersOnly`
  **Scenario**: records written out of order; a moved head; a rate limit and a failed read; an envelope 1 upload; the log of a `fail`.
  **Expected behavior**: a newer run or attempt is never replaced; nothing stored for a rejection or a failed read (503, `Retry-After` for the rate limit); envelope 1 answers a conclusion; the log has identifiers and the status, no rule, path, policy or token.
- **Test name**: `TestAnUploadOfThePolicyPullRequestSetsNoVerdict`
  **Scenario**: an envelope 2 upload for the pull request of an open proposal (M2 D13); one for another pull request.
  **Expected behavior**: 200; the pull record stored; no `archfit/gate` and no comment on the policy pull request; the other gets its check.
- **Test name**: `TestAnAnswerIsTakenForAnAdmittedInstallationOnly`, `TestAnAnswerThroughTheGate`
  **Scenario**: `AnswerDigest` and `Answer` for an installation no operator approved; a real request line, then its digest, an answer by the worker, and an owner's reject whose branch write GitHub refuses.
  **Expected behavior**: `errNotAdmitted` and no GitHub call; a `sha256:` digest; `ErrNotPolicyOwner`; the write's error with the request still open and its issue open. `gate/proposals` tests the whole flow.
- **Test name**: `TestACodePullRequestThatEditsThePolicyIsUnknown`, `TestAPolicyNoOwnerApprovedGivesNoVerdict`
  **Scenario**: the engine's `policy_differs` result of a pull request that edits the policy; a `pass` on a repository whose schema 3 policy has no recorded approval.
  **Expected behavior**: `unknown`, `action_required`, the engine's record shown as it is; `unknown` `first_approval`, the record says an administrator approves the policy in the App.
- **Test name**: `TestADirectPushPutsEveryUploadOutsideTheFlow`
  **Scenario**: a default-branch push that changes the policy with no recorded approval, delivered twice; then an upload of a `pass`.
  **Expected behavior**: one open issue, assigned to the owners, naming the commit; the upload `unknown` `outside_flow`, its record naming the issue. `gate/approvals` tests the judgement.
- **Test name**: `TestAPushRunWithDriftOpensTheDriftIssue`, `TestOnlyAnAcceptedPushRunAtTheTipCountsForDrift`, `TestAFailedDriftIssueFailsTheUpload`
  **Scenario**: a push run of the tip with the engine's `repository_drift` result, then the same upload again; a late older push, a result with an App record, a pull request and a merge group, each with a drift block; a push run whose issue GitHub fails to open, then its retry.
  **Expected behavior**: one drift issue with the drift record, recorded with its body digest, and no write on the retry; nothing for the others; 503, then the retry opens it. `gate/drift` tests the issue's rows.
- **Test name**: `TestAnUploadBeforeItsPushIsJudgedGivesNoVerdict`
  **Scenario**: the default branch's policy changed and no push event was handled yet; then the push arrives.
  **Expected behavior**: `unknown` `policy_unapproved`; after the push, `unknown` `outside_flow`.
- **Test name**: `TestAFirstApprovalGivesTheOnboardingPullRequestItsVerdict`, `TestAFirstApprovalAdmitsOnlyTheWorkflowItWasMadeFor`
  **Scenario**: an onboarding pull request (no policy and no workflow on the default branch) uploads a `pass`; a user who
  is not an administrator, then an administrator, approves its head; the re-run uploads; a later push uploads; a run
  that executed another workflow than the approved one.
  **Expected behavior**: `first_policy` before; refused for the non-administrator; the stored run re-run, its upload
  `pass` and `archfit/gate` success; the later head `first_policy` again; the other workflow `workflow_changed` alone.
- **Test name**: `TestAFirstApprovalOnAHeadWhoseRunIsGoneSaysSo`, `TestAFirstApprovalWhoseRunCannotRunAgainNamesTheCondition`
  **Scenario**: the approved head's run is gone; the default branch moved in more than the policy since the run.
  **Expected behavior**: no re-run; `archfit/gate` `action_required` with a record that says the head is approved,
  names the condition (`no_run`, `base_changed`) and the way out (push a commit, update the branch; then approve the
  new head).
- **Test name**: `TestAConsumedProposalsHeadMergesNothing`, `TestAPullRequestAtAProposalsHeadTakesBackItsSuccess`
  **Scenario**: the App's policy pull request merged and its push used its approvals, then another pull request opens
  at its old head; a success left on a proposal's head when another pull request opens there.
  **Expected behavior**: the latest `archfit/gate` on that head is not a success (M2 review H2).
- **Test name**: `TestAStaleFirstApprovalAdmitsNothingOnceChangeControlStarts`
  **Scenario**: an onboarding pull request approved, then the repository approved another way, or outside the flow,
  with no policy on the default branch.
  **Expected behavior**: `policy_unapproved` or `outside_flow`, and `workflow_changed`: the approval lifts nothing.
- **Test name**: `TestAnOnboardingPullRequestClosedWithoutAMerge`
  **Scenario**: the approved onboarding pull request closes; the policy lands another way; another pull request uploads.
  **Expected behavior**: no approved policy; the upload `unknown` `first_approval`; `v4/repos` lists the repository;
  the first approval of the landed policy is made.
- **Test name**: `TestTheFirstApprovalOfAnAlreadyConvertedRepositoryGivesItsPullRequestsAVerdict`, `TestAWorkflowApprovalGivesItsHeadAVerdict`, `TestAWorkflowMergePushConsumesItsApproval`
  **Scenario**: a schema 3 policy with no record and an open pull request `first_approval`; a pull request that edits
  the workflow; the push that merges it.
  **Expected behavior**: an issue records the approval and the pull request's run is re-run to `pass`; the workflow
  approval re-runs the head to `pass`; the merge push consumes the workflow proposal. `gate/approvals` and
  `gate/proposals` test the actions.
- **Test name**: `TestAnAnsweredRequestCoversItsKeyUntilItsProposalIsConsumed`
  **Scenario**: a request answered with "merge later"; a second pull request fails on the same key and asks again; the policy pull request merges and its push arrives; a third pull request fails on the key.
  **Expected behavior**: the second waits on the answered request, no second issue opens, both pull requests are in its `Waiting`; the push consumes the proposal; the third is `fail`: the key is free.

## Home read façade

`home.go` publishes `HomeViews.Home(context.Context, api.Repo) (HomeState, error)` and
`NewHome(gh, store, admissions) HomeViews`. It checks admission before reading repository data, reads the
default-branch tip and policy at that immutable commit, and compares its digest with the recorded approval.
A recorded outside-flow change or an unmatched digest is exposed as outside-flow state, never verified policy.

`HomeState` carries the target policy, open requests and their records, the latest stored drift's rule/module
pairs, evidence completeness, drift and expiry issue numbers, touched deny-rule pairs, and workflow pull numbers.
It does not publish storage keys. Requests come from the v4 request index, not labels. Pull errors count only
for a currently open, same-repository pull into the default branch whose head matches its stored result.
Workflow changes are read from those same open pulls; a workflow already approved at that head is omitted.
Read failures are returned, not turned into a clean answer. The setup state still belongs to `SetupViews`
and `control/onboarding`; Home does not implement onboarding again.

# Gate

**Path**: internal/gate/
**Parent**: internal/ (Archfit App root)
**Submodules**: ingest, inputs, verdict, feedback, sweep

## Purpose

`gate` is the fail-closed gate path: from an uploaded engine report to a concluded `archfit/gate` check.
It is the façade the rest of the App sees: one upload handler, one webhook reaction, one sweep. It
composes five children so that authentication, trusted inputs, the decision and the publication are
separate, testable steps. Without this module a report is a self-reported file and a required check is a bypass waiting to happen.

## Functional Responsibilities

- Serve `POST /v1/reports`: authenticate and bind the upload, resolve trusted inputs, check the engine
  identity against the locked manifest and the workflow path against settings, decide, publish, answer.
- React to verified webhook events: pending gate check on a new head (required mode), fork refusal,
  `owner_approval_missing` when a review withdraws the approval a `success` or `failure` check relied on,
  workflow re-run on a review after `owner_approval_missing` once no approval is missing, and a one-repository sweep
  when a default-branch push changes a protected path.
- For the App's pull request page only (`Approvals`): read the owner approval of a head, record an architecture
  owner's approval of the exact head, and re-decide the pull request after it, as a review event does.
- Run one reconciliation pass on request.
- Publish, for the control plane, the `Entitlement` record contract, the `Receiver` for discovery uploads,
  and the sweep types.
- Stay operational when the control plane, billing, UI or an LLM provider fails.

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
// Deps are the only things the gate path may touch: GitHub, OIDC, the Store and a clock. On the Store the gate
// path reads the entitlement and the admission, and reads and writes the pusher and approval records of gate/inputs;
// its only other write is gate/feedback's publication leases.
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

// HandleEvent reacts to a verified webhook event: pending check on a new head, fork refusal,
// owner_approval_missing on a review change that withdraws the approval a receipt relied on, re-run on a
// review change when the last receipt was owner_approval_missing, and a one-repository sweep when a
// default-branch push changes a protected path. An event for a repository whose owner is not admitted gets
// the reaction "not_admitted" and nothing else: no read, no write. An approval that cannot be read is returned as an
// error, so the delivery is retried.
func (g *Gate) HandleEvent(ctx context.Context, e Event) error

// Sweep runs one reconciliation pass.
func (g *Gate) Sweep(ctx context.Context) (Summary, error)
```

```go
// Approvals is what the App's pull request page may do about an owner approval of a head: read it, record one and
// re-decide the pull request. *Gate implements it; no other part of the gate path is reachable through it.
type Approvals interface {
    PullApproval(ctx context.Context, t Target, login, author string) (PullApproval, error)
    Approve(ctx context.Context, t Target, login string, seen map[string]string) error
    Reevaluate(ctx context.Context, t Target) error
}

// PullApproval is the owner approval of a pull request head as the App's pull request page shows it.
type PullApproval struct {
    Candidate            // no Paths: the pull request needs no owner approval, and nothing below is set
    Granted       bool   // an owner approved this exact head, on GitHub or in the App
    ApprovedBy    string // the approving login when Granted
    Refusal       error  // while not Granted: why login may not approve in the App (an Err* refusal); nil when it may
    AuthorIsAgent bool   // the pull request was opened by an agent identity of the protected settings
}

// PullApproval reads the owner approval of t's head for the signed-in login; author is the pull request's.
func (g *Gate) PullApproval(ctx context.Context, t Target, login, author string) (PullApproval, error)

// Approve records login's approval of t's head in the App: the way a sole owner approves, because GitHub does not let
// an author approve their own pull request. seen is the Candidate digests the owner was shown. It does not re-decide
// the pull request; Reevaluate does.
func (g *Gate) Approve(ctx context.Context, t Target, login string, seen map[string]string) error

// Reevaluate re-decides the pull request t after its owner approval changed, as a review event does: an
// owner_approval_missing check whose approval is now granted re-runs the workflow, and a result whose approval is gone
// is withdrawn. Nothing here turns a check green; the re-run's upload does.
func (g *Gate) Reevaluate(ctx context.Context, t Target) error
```

The three methods ask `Admitted` first, like every other entry point; an installation no operator approved gets an
error and nothing is read or written. `PullApproval` calls `Candidate` (no paths → the zero value), then `Resolve`
for the state, and, only while the approval is missing, `CanApprove` for `login`: a refusal is `Refusal`, a failed
read is the error. `AuthorIsAgent` is `Settings.IsAgent(author)` of the resolved base settings. `Reevaluate` is the
`ReviewChanged` reaction (`approvalChanged`) on `t` and logs `gate reevaluate` with identifiers only.

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
// "Admission" keeps it apart from Approval, a policy owner's approval of a pull request.
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

<!-- contract: Candidate, ErrNothingToApprove, ErrForkPull, ErrAgentIdentity, ErrNotOwner, ErrDigestsChanged — restated from internal/gate/inputs/module.md -->
```go
// Candidate is what an owner approves in the App: the protected paths a pull request changes and what each holds at
// its head.
type Candidate struct {
    Paths []string // in ProtectedPaths order; empty when the pull request needs no owner approval
    // Digests is policy.Digest of each path at the head, "" for a path the head deletes, so a deletion is part of what
    // is approved. Nil for a fork: no head file of a fork is read.
    Digests map[string]string
}

// The refusals of Approve and CanApprove. Any other error is a failed read or write.
var (
    ErrNothingToApprove = errors.New("inputs: approve: the pull request changes no protected path")
    ErrForkPull         = errors.New("inputs: approve: the pull request comes from a fork")
    ErrAgentIdentity    = errors.New("inputs: approve: an agent identity never approves")
    ErrNotOwner         = errors.New("inputs: approve: not an architecture owner of the protected settings")
    ErrDigestsChanged   = errors.New("inputs: approve: the protected files at the head are not the ones shown")
)
```

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
}
```

### Upload answers (`POST /v1/reports`)

| Situation                                                       | Status                     | Body                                     |
| --------------------------------------------------------------- | -------------------------- | ---------------------------------------- |
| Outcome published (any conclusion, including `action_required`) | 200                        | `{"conclusion": "...", "reason": "..."}` |
| Publication skipped (newer run reported, or PR head moved)      | 200                        | same, with this upload's outcome         |
| `*Rejection` from `Receive`                                     | `Rejection.Status`         | `{"error": "<Rejection.Code>"}`          |
| Installation not admitted (a `*Rejection` of `Receive`)         | 403                        | `{"error": "installation_not_approved"}` |
| Approval unreadable (a `*Rejection` of `Receive`)               | 503                        | `{"error": "admission_unavailable"}`     |
| `api.ErrRateLimited` while resolving inputs or publishing       | 503 with `Retry-After: 60` | `{"error": "rate_limited"}`              |
| Any other error after authentication                            | 503 **[default]**          | `{"error": "unavailable"}`               |

`reason` is `Outcome.Reasons[0].Code`. A 2xx means "accepted and published", not "passed". The producer
retries on every non-2xx except 400, 401, 403, 409 and 413. On 503 the pending check stays pending.
The `Retry-After` value of 60 seconds is **[default]**.

Rate-limit rule, one for the whole gate: `api.ErrRateLimited` during `Receive` (before a decision exists) is the
429 `rate_limited` rejection from `gate/ingest`, passed through unchanged. `api.ErrRateLimited` after `Receive`
(resolving inputs or publishing) is 503 with `Retry-After`. The Action retries both, and the sweep repairs what the
retries do not. A rate limit before the gate check is written leaves the pending check pending and writes nothing
green. The one rate limit that `feedback` returns after the gate check is written, from the changed-file list that
only filters annotations, leaves the gate check with the decided conclusion; the retry adds the annotations. In the sweep the same error
stops the pass with `Summary.RateLimited`. In `HandleEvent` it is returned, also from `RunRepo`, so the webhook queue
retries the delivery.
The handler works under a 50 second deadline **[default]**, inside the 60 second feedback target.

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
  `Binding.BaseRef` is the PR base branch, or for other events the branch the run executed on. A JWKS outage is
  the 503 `oidc_unavailable` rejection, which the Action retries. `Actions` and `Verifier` are restated below.
  `Repos` is a role interface of `github/api` that `*Client` implements; the façade only passes it to the children.
  `Pulls` is a role interface of `github/api` that `*Client` implements; the façade only passes it to the children.

<!-- contract: New — restated from internal/gate/ingest/module.md -->
```go
func New(repos Repos, pulls Pulls, actions Actions, v Verifier, audience string, admit Admitter) Receiver
```

After it resolves the repository and checks the repository ID, `Receive` asks `Admitter.Admitted` for the installation
and the owner of that repository. Not admitted is the 403 `installation_not_approved` rejection, before the run is read.
An approval that cannot be read is the 503 `admission_unavailable` rejection, which the Action retries.

### gate/inputs (child)

- **Counterpart**: internal/gate/inputs
- **Direction**: gate depends on inputs
- **Strength**: functional (composition)
- **LCA / Rank / Distance**: gate / 1 / 1
- **Volatility**: high
- **Balanced?**: yes
- **Shared knowledge**: `Resolve` reads only the protected ref (plus an owner-approved head digest), returns an
  error on GitHub failures and on a failed read of an approval record (fail closed), never on a failed read of the
  entitlement. A push never approves; `RecordPusher` is attribution only. `Candidate`, `CanApprove` and `Approve` are
  called only from `PullApproval` and `Approve`, for `control/ui`. `SettingsFound == false` means "not onboarded". `Admissions` is built by `app` and passed in
  `Deps.Admissions`; the façade, `ingest` and `sweep` ask it `Admitted`, `Resolve` does not.
  `PolicyDigests[0]` and `BaselineDigests[0]` are the first accepted digests: the base digest when the file exists on
  the protected ref, otherwise the owner-approved head digest; the lists are empty when neither exists. The inputs constructor is `New(repos Repos, pulls Pulls, s Store) Resolver`.
  `inputs` owns the digest predicate and the protected path set. The façade compares no digest and names no
  protected path: a receipt digest is the claim when `Accepts` takes it and `First` of the list otherwise, and the
  `DefaultBranchPush` trigger asks `MayBeProtected` for every changed path. `ProtectedPaths` is not called here
  (a push event carries no settings); the tests iterate it.

<!-- contract: Target, Trusted, Approval — restated from internal/gate/inputs/module.md -->
```go
// Target is the pull request (or default-branch push) whose trusted inputs are resolved.
type Target struct {
    Repo        Repo
    PullRequest int // 0 for a default-branch push
    HeadSHA     string
    BaseRef     string // protected ref; the default branch for every upload. Events and the sweep still set the pull request's base
    Fork        bool
}

type Approval string

const (
    ApprovalNotRequired Approval = "not_required" // no protected path changed
    ApprovalGranted     Approval = "granted"      // a base-version owner, no agent, approved this exact head SHA
    ApprovalMissing     Approval = "missing"
)

// Trusted is everything the gate believes, all read from the protected ref or GitHub itself.
type Trusted struct {
    Settings             Settings
    SettingsFound        bool
    PolicyDigests        []string // base digest, plus the head digest when Approval == ApprovalGranted
    BaselineDigests      []string // same rule; empty = no baseline on the protected ref
    LabelsDigests        []string // same rule; empty = no labels file on the protected ref
    ProtectedPaths       []string // protected paths this PR changes
    Approval             Approval
    ApprovedBy           string
    EarliestWaiverExpiry time.Time // zero = no waivers
    Suspended            bool
    Entitlement          Entitlement
}
```

<!-- contract: Resolver — restated from internal/gate/inputs/module.md -->
```go
type Resolver interface {
    // Resolve reads the protected ref and reviews. It never reads policy, settings, baseline,
    // owners, or CODEOWNERS from the head tree, except to hash an owner-approved candidate.
    Resolve(ctx context.Context, t Target, now time.Time) (Trusted, error)
    // RecordPusher adds the login that pushed t's head, from a signed push delivery, to the record of who pushed it.
    // The record is attribution only: no push approves anything. "" records nothing. Opening a pull request records
    // nothing.
    RecordPusher(ctx context.Context, t Target, login string) error
    // Candidate returns the protected paths the pull request t changes, by the rule of Resolve, and the digest of each
    // at t.HeadSHA: what an owner approves in the App.
    Candidate(ctx context.Context, t Target) (Candidate, error)
    // CanApprove reports whether login may approve t's head in the App now: nil, or ErrNothingToApprove, ErrForkPull,
    // ErrAgentIdentity or ErrNotOwner, in that order. Owners and agents come from the settings of the protected ref.
    CanApprove(ctx context.Context, t Target, login string) error
    // Approve records login's approval of t's head, bound to its SHA, when CanApprove allows it and seen equals the
    // Candidate digests (else ErrDigestsChanged). A record for that head already, by any owner, is success and stays
    // as it is, unless it no longer holds: then it is replaced.
    Approve(ctx context.Context, t Target, login string, seen map[string]string, now time.Time) error
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
  A `ReasonCode` appears at most once. `owner_approval_missing` precedes `policy_mismatch` and `baseline_mismatch`, so an
  unapproved code-and-policy PR carries the receipt reason `owner_approval_missing`, which is what `HandleEvent` and the
  sweep key their re-run on. `engine_error` precedes `missing_required_coverage`.

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
// governance (owner approval), input integrity, evidence completeness, then the engine decision.
// The step table of gate/verdict is the normative order.
func Decide(f Facts) Outcome

// Without returns the fixed outcome for a target that has no report: code is one of ReasonMissingUpload,
// ReasonStaleInputs, ReasonWaiverExpired, ReasonForkUnsupported, ReasonSuspended, ReasonOwnerApprovalMissing.
func Without(code ReasonCode) Outcome
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
  The feedback constructor is `New(checks Checks, comments Comments, pulls Pulls, now func() time.Time) Publisher`;
  `Checks` and `Comments` are role interfaces of `github/api` that `*Client` implements.

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
    // written. When the result differs from Trusted in any field (a digest, the owner approval, the settings) or no
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
  The sweep owns the rules the façade also needs, and exports them: the re-run guard (`Rerun`, called for
  `ReviewChanged`), the publication for a target without a report (`Without`, called for the fork refusal of a
  new head), and the withdrawn-approval rule 5a (`Withdrawn` and `Withdrawal`, called for `ReviewChanged`). The
  façade has no copy of any of them.

<!-- contract: New — restated from internal/gate/sweep/module.md -->
```go
func New(repos Repos, pulls Pulls, actions Actions, r Resolver, p Publisher, c Config) Sweeper
```

<!-- contract: RerunResult, Rerun — restated from internal/gate/sweep/module.md -->
```go
// RerunResult is what the re-run guard found or did. It is meaningful only with a nil error.
type RerunResult int

const (
    RerunInFlight   RerunResult = iota // the run is queued or in progress: wait for its upload
    RerunRequested                     // Actions.Rerun accepted the request
    RerunImpossible                    // rc.RunID is 0, the run is gone, or a newer attempt already completed
)

// Rerun is the re-run guard, the only caller of Actions.Rerun on the gate path. It requests a re-run of
// rc.RunID only while that run is completed and still at rc.RunAttempt, so a receipt is re-run at most once.
// rc.RunID == 0 and ErrNotFound from Actions.Run are RerunImpossible with a nil error.
func Rerun(ctx context.Context, actions Actions, r RepoRef, rc Receipt) (RerunResult, error)
```

<!-- contract: Without, Withdrawn, Withdrawal — restated from internal/gate/sweep/module.md -->
```go
// Without builds the publication for a target that has no report: verdict.Without(code),
// verdict.Suspended(tr.Entitlement) for ReasonSuspended, or verdict.ApprovalMissing(tr) for
// ReasonOwnerApprovalMissing, with the run identity of rc (zero when there is no previous receipt) and the
// current digests of tr.
func Without(code ReasonCode, t Target, tr Trusted, rc Receipt) Publication

// Withdrawn reports whether rc concluded without asking for action although tr now lacks the owner approval that
// verdict.Decide demands. Success, failure and an action_required whose reason is report-derived and itself asks for action (missing required coverage, a baseline that is not comparable, a policy defect: see `Decided`) match, because the report is shown under them; any other receipt never does.
func Withdrawn(rc Receipt, tr Trusted) bool

// Withdrawal is rule 5a, decided inside the head's publication lease (Publisher.Reconcile) from the gate check
// current there and inputs tr resolved there: Without(ReasonOwnerApprovalMissing) over the current receipt when the
// repository is in required mode and that receipt is Withdrawn, else nil.
func Withdrawal(t Target, current CheckState, tr Trusted) *Publication
```

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
    ReviewChanged      EventKind = "review_changed"      // submitted, edited, dismissed
    DefaultBranchPush  EventKind = "default_branch_push"
    BranchPush         EventKind = "branch_push"         // a push to a branch other than the default branch
    InstallationAdded  EventKind = "installation_added"  // created, or repositories added
    InstallationRemoved EventKind = "installation_removed" // deleted
    ReposRemoved       EventKind = "repos_removed"
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
    Branch         string   // push kinds only: the branch, without "refs/heads/"
    Pusher         string   // push kinds only: the login that moved the branch; never a git author or committer
    PusherType     string   // push kinds only: the payload's pusher_type ("user", or a deploy key); "" when absent
    ChangedPaths   []string // default_branch_push only
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
  under the role it needs and itself calls only `Actions.Run` and `Actions.Rerun`. `ErrRateLimited` is the sentinel
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
  admission and reads and writes the pusher and approval records, and to `gate/feedback`, which holds the publication
  leases)
- **Strength**: contract
- **LCA / Rank / Distance**: root / 1 / 1
- **Volatility**: high (gate side); store is low
- **Balanced?**: yes
- **Shared knowledge**: the gate path calls `Get(Tenant(installationID), EntitlementKey)` and
  `Get(Tenant(installationID), AdmissionKey)` inside `gate/inputs`, `Get`
  and `CompareAndSwap` on `pusher/<repo>/<head_sha>` and `approval/<repo>/<pull_request>/<head_sha>` under the same tenant inside
  `gate/inputs`, and `Get` and
  `CompareAndSwap` on `feedback/lease/<head_sha>` under the same tenant inside `gate/feedback`. It calls no
  other Store operation. `ErrNotFound` is the store sentinel for a missing key; for the entitlement it means "not
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
  the `*Gate` to `control` as its `Approvals`.
- **internal/control/account**: contract, root / 2 / 2, moderate, balanced. Writes the JSON of `Entitlement`
  under `EntitlementKey`, and writes and deletes the JSON of `Admission` under `AdmissionKey`. The gate owns the
  records so that it never imports billing or operator code.
- **internal/control/onboarding**: contract, root / 2 / 2, high, balanced. Uses `Receiver`, `Rejection`, `Upload`,
  `Binding`, `Problem` for `POST /v1/discoveries`. A discovery upload never reaches `verdict` or `feedback`.
- **internal/control/ui** (through `control.Deps.Approvals`, which `app` sets to the `*Gate`): contract, root / 2 / 2,
  high, balanced. Calls `PullApproval` for the pull request page and `Approve` then `Reevaluate` for its Approve
  button, behind the browser session, CSRF token and write access; maps the refusals to its wording. Nothing on the
  upload (OIDC) path holds `Approvals`.

## Internal Design

### Composition

`New(d)` builds, once: `ingest.New(d.GitHub, d.GitHub, d.GitHub, d.OIDC, d.Audience, d.Admissions)`,
`inputs.New(d.GitHub, d.GitHub, d.Store)`, `feedback.New(d.GitHub, d.GitHub, d.GitHub, d.Store, d.Audience, d.Now)` (the audience is the App's base URL, so
the comment links to the App's pull request page), and
`sweep.New(d.GitHub, d.GitHub, d.GitHub, resolver, publisher, d.Admissions, d.Sweep)`. `verdict` has no constructor.
`NewReceiver` is `ingest.New` with the same argument pattern. A nil `Deps.Now` means `time.Now` **[default]**.
`New` delegates to the unexported `compose`, which takes the five `github/api` roles as one interface, and
`compose` to `newGate`, which takes the children as interfaces; both exist so that tests can replace GitHub or a child.

| Child      | Contributes                                                                                              | Depends on siblings                                           |
| ---------- | -------------------------------------------------------------------------------------------------------- | ------------------------------------------------------------- |
| `ingest`   | authenticated, bound `Upload` or a `Rejection`                                                           | none                                                          |
| `inputs`   | `Trusted` for a `Target`; owns the `Entitlement` and `Admission` records, the digest predicate and the protected path set | none                                                        |
| `verdict`  | `Outcome` from `Facts`; `Without`, `Suspended`                                                           | `ingest` (`Problem`), `inputs` (`Trusted`) — model            |
| `feedback` | checks, comment, receipt; `State`, `Pending`, `Warn`                                                     | `verdict` (`Outcome`), `inputs` (`Target`, `Trusted`) — model |
| `sweep`    | reconciliation of open PRs; owns `Rerun` and `Without`                                                   | `inputs`, `feedback`, `verdict` — contract                    |

### Report upload flow

1. `u, err := receiver.Receive(ctx, req, KindReport)`. `*Rejection` → answer and stop. Nothing is published.
2. `target = Target{Repo: u.Binding.Repo, PullRequest, HeadSHA, BaseRef, Fork}` from the binding. For a non-PR
   upload (`PullRequest == 0`) `Target.BaseRef` is `Repo.DefaultBranch`, not `Binding.BaseRef`: the binding names the
   branch the run executed on, which is a protected ref only when it is the default branch.
3. `trusted, err := resolver.Resolve(ctx, target, now)`. Error → 503; the pending check stays.
4. `IdentityApproved` = `manifest.Lookup(u.Identity)` hits **and** `entry.ProfileVersion != ""` **and**
   `u.Report.Comparison.MeasurementProfile != nil` **and** its `Version == entry.ProfileVersion`. A manifest row
   without a profile version approves nothing, so two empty versions never compare equal (for engine v3.2.0 the manifest holds `archfit.measurement.v2`). False when `u.Report == nil`.
5. `WorkflowApproved` = `trusted.SettingsFound` **and** `u.Binding.WorkflowPath != ""` **and** it equals
   `trusted.Settings.Workflow`. A workflow file changed by the PR is a protected path; `inputs` then demands owner approval.
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
   from newer inputs between step 3 and the lease, under the same run identity, and an owner may dismiss the
   approval step 5 relied on. When the new inputs differ from `trusted` in any field or no longer accept a receipt
   digest, feedback writes nothing and returns `ErrStaleInputs`; the upload answers 503 `unavailable` and
   the Action's retry decides from the new inputs. The correction stays until then.
8. Answer 200 with conclusion and reason.

| `Receipt` field        | Value                                                                                            |
| ---------------------- | ------------------------------------------------------------------------------------------------ |
| `RunID`, `RunAttempt`  | from `Binding`                                                                                   |
| `ReportDigest`         | `Upload.Digest`                                                                                  |
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
  the commit only; no gate check, no comment.
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
| `PullOpened`, `PullSynchronized` | `Resolve`. `!SettingsFound` → nothing. Fork → `Publish(sweep.Without(ReasonForkUnsupported, target, trusted, Receipt{}))`: a zero-run receipt carrying the base digests. Else `Settings.Gate == GateRequired` → `publisher.Pending(target)`. Advisory → nothing. |
| `ReviewChanged`                  | `Reconcile(target, …)`: inside the lease, a withdrawn approval → `sweep.Withdrawal` (below); reason `owner_approval_missing` and `!verdict.Unapproved(trusted)` **[default: no re-run while an approval is missing]** → `sweep.Rerun`.                           |
| `BranchPush`                     | `resolver.RecordPusher(target, Pusher)` first, for attribution (the record approves nothing); a failed write is returned and the queue retries. Then the newest pull request of this repository whose head is `Branch` (`Pulls.PullByHead`): when it is open and its head is `HeadSHA`, run the `ReviewChanged` reaction on it. A push whose `PusherType` is present and not `"user"` (a deploy key) records nothing and logs `gate pusher not recorded` with the delivery id, never the key; the re-evaluation and the sweep still run. No such pull request, a closed one or another head → nothing more. A pull request from a fork is never found. |
| `DefaultBranchPush`              | `RecordPusher` and the pull request lookup as for `BranchPush`; then `ChangedPaths` empty (= unknown), or `inputs.MayBeProtected(path)` for any changed path → `sweeper.RunRepo(ctx, repo, now)` in the request. Otherwise nothing.                                                                                                   |
| `PullClosed`                     | nothing.                                                                                                                                                                                                                                                         |
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

Withdrawn approval: `ReviewChanged` runs entirely inside `publisher.Reconcile`, so the gate check read, the
decision and the write all happen under the head's publication lease. The decision calls `Resolve` (once, at
`g.now()`) only when the check it is given concludes `success` or `failure`, or its reason is
`owner_approval_missing`; every other receipt, a pending check and no check get no `Resolve` (`State` itself lists
the check runs). In required mode, when the current receipt is `sweep.Withdrawn`, it returns
`sweep.Withdrawal(target, current, trusted)`: `owner_approval_missing` under the current receipt's run identity,
with the protected paths in `Detail`, logged as `approval_withdrawn`, and no re-run. This is rule 5a of the sweep,
which repairs a lost event within the hour. After `owner_approval_missing`, once `!verdict.Unapproved(trusted)`
(approval granted, or no longer required), it calls `sweep.Rerun` inside the same lease and publishes nothing.

Because nothing is decided outside the lease, every ordering of these events and an upload ends right:

- A withdrawal that lands while an upload holds the lease waits, then reads the check the upload wrote, a newer run
  attempt included, and downgrades it.
- An upload decided before a withdrawal that waits for the lease while the withdrawal writes re-reads its inputs
  inside the lease. Any change of `Trusted` (whole-value equality, `decidedFrom`), the dismissed approval included,
  makes it write nothing and answer 503 `unavailable`; the Action's retry then decides from the new inputs.
- An approval that lands while the withdrawal holds the lease waits, then reads `owner_approval_missing` and
  re-runs the workflow. An approval that lands before the withdrawal reads its inputs leaves the green check alone:
  the withdrawal no longer finds the receipt withdrawn and writes nothing.

A Store outage writes nothing and returns the lease error; the check keeps its conclusion, green included, until the
Store is back and the next event or sweep pass runs. Every `ReviewChanged` takes the lease before it knows whether
anything qualifies, also for a pending or absent check, so during an outage the re-run after an approval waits for
rule 7b of the sweep, which needs no lease. Do not add a `State` prefilter outside the lease to save that: a filter
that sees a pending check while an upload holds the lease returns early, and the upload's `success` then stands over
a withdrawn approval.

Re-run guard: `sweep.Rerun` is the only implementation; the façade has none. Its single behavior, for this reaction:

| `Rerun` gives                                                                   | Reaction of `HandleEvent`                                                      |
| ------------------------------------------------------------------------------- | ------------------------------------------------------------------------------ |
| `RerunRequested`                                                                | logged as `rerun`; nil                                                         |
| `RerunInFlight` (queued or in progress)                                         | nothing; nil. A redelivered event lands here                                   |
| `RerunImpossible`: `receipt.RunID == 0`                                         | nothing; nil; no GitHub call                                                   |
| `RerunImpossible`: `Actions.Run` returned `api.ErrNotFound`, or a newer attempt | nothing; nil (skip). Rule 7b of the next sweep pass publishes `missing_upload` |
| an error (any other `Actions.Run` failure, or a failed `Actions.Rerun`)         | the wrapped error                                                              |

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
  the check pending. Never `neutral` or `skipped` on `archfit/gate`. `success` comes only from `verdict.Decide`.
- Never trust head-tree config, labels, baselines, owners or CODEOWNERS. Trusted inputs come from the protected ref
  and are verified by digest; the only exception is an owner-approved head policy or baseline digest for that exact head SHA.
- Provenance is necessary, not sufficient. Feedback says "customer-attested"; nothing says "verified". The
  fabricated-report gap (a trusted workflow running a PR script that forges a clean report) is not closed and not claimed closed.
- Unknown identity never compares equal: a manifest miss, a missing profile or a profile version mismatch is never approved.
- The tenant comes from verified OIDC claims (`ResolveRepo`) or the verified webhook; never from the envelope or a client-supplied ID.
- Nothing runs for an installation no operator approved. The façade (`HandleEvent`, `PullApproval`, `Approve`,
  `Reevaluate`), `ingest` (upload) and `sweep` (pass) each ask `Admitted` before they read or write for it. An approval that cannot be read (a Store error, or a
  record that does not decode) does nothing and is retried: `HandleEvent` returns the error, the upload answers 503
  `admission_unavailable`, the sweep counts the installation in `Summary.Errors`. It is never a refusal and never
  "admitted", unlike the entitlement. The gate only reads the admission; `control/account` writes it.
- The gate path reads the `Entitlement`, the `Admission` and the pusher and approval records, and writes only the pusher
  and approval records of `gate/inputs` and `gate/feedback`'s publication leases. A pusher record is
  `pusher/<repo>/<head_sha>` = the logins that pushed the head, written only from a signed `push` delivery, only ever
  added to, and read by nothing that decides. An approval record is `approval/<repo>/<pull_request>/<head_sha>`, written once (or replaced once it no longer counts), only by
  `Approve` for `control/ui`'s browser session. Both are kept until the installation is deleted; one small key per
  pushed or approved head is the accepted growth **[default]**, to be pruned by the sweep if tenants grow large.
- No push approves, and no agent identity approves: an approval is an owner's review on GitHub or an owner's Approve
  in the App, for the exact head SHA. No approval API or credential is reachable from the upload (OIDC) routes or the
  CLI (`TestOwnerApprovalIsReachableOnlyFromTheBrowserSession` in `internal/app`).
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
- Nothing is persisted by the gate but the publication leases, which hold a token and an expiry only and are
  deleted when the writes end, and the pusher and approval records of `gate/inputs`, which hold logins, a head SHA,
  digests and a time. Reports and payloads live in memory for one request and are dropped.
- Handlers do their work in the request and are idempotent; GitHub holds the dedup keys (`external_id`, comment marker).
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
  **Scenario**: matching policy, baseline and labels digests; approved head policy digest; mismatching `config_hash`; a claimed baseline digest not on the protected ref; no baseline and no labels file anywhere; nil report.
  **Expected behavior**: the values of the receipt table, all bare hex; the first accepted digest when a claim does not match; `""` when both sides are empty.
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
  **Expected behavior**: `Receive` gets `KindReport` under a deadline of about 50 seconds; `Resolve` gets the target of the binding and the clock; the `Publication` equals the one built from `verdict.Decide` and the receipt table, apart from `Resolve`, which is set and returns the resolver's current inputs for the same target. The non-PR target has `BaseRef` = the default branch and keeps `Fork`; the answer is `fork_unsupported`. An upload for a pull request into another base resolves against the default branch too, gets `ProblemUnsupportedEvent` added, and is never a success: `unsupported_event`. The events of such a pull request (`PullOpened`, `PullSynchronized`, `ReviewChanged`, and the pull found by a branch push) and the sweep skip it (`reactionOtherBase`; the webhook `Event` carries `DefaultBranch`), so the sweep never re-runs a receipt that the inputs read at that base could not match. The claimed digest reaches `Decide` unchanged: `baseline_mismatch`.
- **Test name**: `TestHandleEventRules`
  **Scenario**: the `HandleEvent` table over fakes of the children: a new head in required mode; a fork of a repository that is not onboarded; a deleted fork repository (`HeadRepo == ""`); a review against each row of the re-run guard table (receipt without a run, run not found, in progress, newer attempt, completed at the receipt's attempt), with approval still missing, with no approval needed any more, with another receipt reason, with a pending check and with no check; a review on a `success` receipt whose approval is now missing, and the same with the approval still granted, in advisory mode, and on an `action_required` receipt; an error from each child call (`Resolve`, `Pending`, `State`, `Actions.Run`, `Actions.Rerun`, `RunRepo`, the withdrawal `Publish`); `Sweep`.
  **Expected behavior**: the target is built from the verified event; the not-onboarded fork gets nothing; the deleted fork gets the `sweep.Without(ReasonForkUnsupported, …)` publication with a zero run and the first trusted digests; `Actions.Rerun` is called only for the completed run at the receipt's attempt (with an approval granted or no longer needed); a receipt without a run makes no GitHub call; a run that is not found is a nil error without a re-run; the withdrawn approval gets exactly the `sweep.Without(ReasonOwnerApprovalMissing, …)` publication through `Reconcile`, from one check read and one `Resolve`, and no re-run; every other review event writes no feedback; every child error is returned and nothing is published; `Sweep` passes the clock and the summary through.
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
  **Scenario**: `PullOpened` in required mode, in advisory mode, without settings, from a fork; `PullSynchronized`; `ReviewChanged` with and without an `owner_approval_missing` receipt and with and without a granting review; `PullClosed`; `DefaultBranchPush` touching only code; `InstallationAdded`; the same delivery twice.
  **Expected behavior**: pending check; nothing; nothing; `fork_unsupported` published; pending check on the new head; one re-run only for the receipt-plus-granted case; nothing for the rest; redelivery changes nothing.
- **Test name**: `TestProtectedPushTriggersRepoSweep`
  **Scenario**: table of `DefaultBranchPush.ChangedPaths`: `.archfit.yaml`; `.archfit-baseline.json`; `.archfit-labels.yaml`; `.archfit-app.yaml`; `.github/workflows/archfit.yaml`; another workflow file; only source files; empty list (unknown); each with a second repository that has stale PRs.
  **Expected behavior**: `RunRepo` for the pushed repository in the first six rows and for the empty list; no call for source-only pushes; the other repository is never touched; a rate limit inside `RunRepo` is returned as a retryable error that wraps `api.ErrRateLimited`, and nothing is written.
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
  **Scenario**: a Store wrapper that panics on `Put`, `Delete`, `Add`, `DeleteTenant`, `Tenants` and on a `CompareAndSwap` outside `feedback/lease/`, `pusher/` and `approval/`; a private repository with an entitlement; drive every façade entry point over it: `HandleEvent` (`PullOpened`, `ReviewChanged`, `DefaultBranchPush`, `BranchPush`), an upload, `Sweep`, `PullApproval`, `Approve` and `Reevaluate`.
  **Expected behavior**: no panic; the Store was read at least once; every read outside the leases and the records is `Get` of `EntitlementKey` under the installation's tenant, never `SystemTenant`; every lease operation and every record is under the installation's tenant; a pusher and an approval record were written; the pull's lease is gone afterwards.
- **Test name**: `TestUnadmittedInstallationGetsNothing`
  **Scenario**: the installation is refused by the admission fake; a `PullOpened` event, then an upload; then the refusal is lifted and a `PullSynchronized` event and an upload follow.
  **Expected behavior**: no pending check and no state check while refused; the upload answers 403 `installation_not_approved`; after the lift the upload answers 200 `success`/`healthy`.
- **Test name**: `TestUnreadableApprovalIsRetriedNotDropped`
  **Scenario**: the admission fake cannot read the approval; a `PullOpened` event and an upload; then the fake recovers and a `PullOpened` event and an upload follow.
  **Expected behavior**: `HandleEvent` returns an error and no check exists; the upload answers 503 `admission_unavailable`; after recovery the event is handled and the upload answers 200 `success`/`healthy`.
- **Test name**: `TestOwnerPushNeverApproves`
  **Scenario**: a pull request that replaces the baseline; an owner's `BranchPush` for its head (1) before the `PullOpened` event and the report, (2) after the report left `owner_approval_missing`, (3) from a member of an owner team.
  **Expected behavior**: (1) the report is `owner_approval_missing`, never `success`; (2) and (3) no re-run, and the report stays `owner_approval_missing`.
- **Test name**: `TestPusherTypeOtherThanUserRecordsNothing`
  **Scenario**: an owner's `BranchPush` for a head whose report is `owner_approval_missing`, with `PusherType` absent, `"user"`, `"deploy_key"` and an unknown value.
  **Expected behavior**: absent and `"user"` record the pusher, the other two record nothing; none re-runs the workflow.
- **Test name**: `TestAppApprovalReDecidesThePullRequest`
  **Scenario**: an `owner_approval_missing` receipt on a baseline change; the owner reads `PullApproval`, then `Approve` with its digests and `Reevaluate`; the upload of the re-run; then a new commit.
  **Expected behavior**: one re-run of the report's run; the check stays `action_required` until the new upload, which is `success` with the approved head baseline digest; `PullApproval` then reads granted by the owner; the new commit is `owner_approval_missing` again.
- **Test name**: `TestAppApprovalRefusesAgentsAndOthers`
  **Scenario**: an owner listed as an agent, the `[bot]` account of an agent, a member of an owner team listed as an agent, a non-owner: each approves on GitHub (a review event) and in the App.
  **Expected behavior**: `ErrAgentIdentity` three times and `ErrNotOwner` from `PullApproval` and `Approve`; no re-run; the report stays `owner_approval_missing`.
- **Test name**: `TestPullApprovalNamesAnAgentAuthor`, `TestPullApprovalWithoutAProtectedChangeIsEmpty`
  **Scenario**: a pull request opened by the `[bot]` account of a listed agent, then by an owner; a pull request that changes no protected path.
  **Expected behavior**: `AuthorIsAgent` true, then false; the zero `PullApproval`, and `Approve` refuses with `ErrNothingToApprove`.
- **Test name**: `TestApprovalNeedsAnAdmittedInstallation`
  **Scenario**: the installation is refused after the page read its candidate.
  **Expected behavior**: `PullApproval`, `Approve` and `Reevaluate` return an error; no GitHub call; no approval record.
- **Test name**: `TestPusherRecordNeverAuthorizesAFork`
  **Scenario**: a pull request whose head repository is a fork; an owner's `BranchPush` with the fork head's SHA.
  **Expected behavior**: no re-run; the report is `fork_unsupported`.

### Behavior Tests

- **Test name**: `TestLegitimateChangePassesAndViolationFails` (T02)
  **Scenario**: the paired healthy and blocked fixtures of each claimed erosion scenario; the warning-only attention fixture.
  **Expected behavior**: `success`, `failure`, `success` on `archfit/gate`; the upload answer for the `needs_attention` verdict is `success`/`attention_only`, with `archfit/state` `neutral`; feedback names rule, modules, locations and the validation command.
- **Test name**: `TestTamperingNeverYieldsSuccess` (T12, T14)
  **Scenario**: head-edited policy, baseline, settings (owners, gate mode), workflow and CODEOWNERS; an ordinary non-owner approval; an owner approval followed by a new commit; a head-edited labels file; an envelope that claims no baseline while a baseline exists; a report whose `gate_reference` is missing or `non_comparable` while a baseline exists.
  **Expected behavior**: `policy_mismatch`, `baseline_mismatch`, `labels_mismatch`, `baseline_not_comparable` or `owner_approval_missing`; never `success`; after the owner approves the new head, the same upload re-run passes.
- **Test name**: `TestAtomicCodeAndPolicyPullRequest` (T14)
  **Scenario**: a PR changes code and policy; the engine ran with the head policy; an owner approved the exact head.
  **Expected behavior**: before approval `owner_approval_missing` (and `policy_mismatch`); after approval and re-run `success`; a further commit returns it to `action_required`.
- **Test name**: `TestNonComparableBaselineRecovery` (T15)
  **Scenario**: a measurement profile that differs from the baseline's makes `gate_reference.status` `non_comparable` while `verdict` is `healthy`; then an owner-approved baseline PR merges and the workflow re-runs.
  **Expected behavior**: `baseline_not_comparable` with the baseline-PR recovery text; `success` only after the new baseline is on the protected ref.
- **Test name**: `TestDuplicateAndOutOfOrderDelivery` (T03, T11)
  **Scenario**: the same upload twice; an older attempt after a newer one; an upload for a superseded head.
  **Expected behavior**: one comment, updated in place; the newer result stands; 409 for the stale uploads.
- **Test name**: `TestReviewTriggersRerun` (T14)
  **Scenario**: `owner_approval_missing` receipt; the owner approves; webhook delivered; then delivered again.
  **Expected behavior**: one re-run; the check stays `action_required` until the new upload; the second delivery finds the run in progress and does nothing.
- **Test name**: `TestWithdrawnApprovalEndsSuccess` (T14)
  **Scenario**: a pull that edits the policy, the settings file or the workflow file, approved by the owner, with a `success` check; the owner's review is dismissed, or the owner requests changes; the `ReviewChanged` event arrives, or it is lost and the sweep runs; the trigger repeats; the owner approves again. Separately, a non-owner requests changes and the owner only comments.
  **Expected behavior**: the gate and state checks become `action_required` with `owner_approval_missing` under the same run; no re-run; the repeat writes nothing; the new approval re-runs the workflow once and the next upload passes. A review that withdraws nothing leaves the check green with no write.
- **Test name**: `TestWithdrawalRacesWithPublication` (T14)
  **Scenario**: (1) an upload decided under the approval; before it holds the publication lease the owner requests changes and the `ReviewChanged` event publishes the withdrawal. (2) A dismissal; before the withdrawal holds the lease the owner approves again, through the event and through the sweep. (3) A re-run attempt 2 that read the approval inside the lease; as it writes `success`, the owner requests changes and the `ReviewChanged` event runs concurrently. (4) The sweep finds attempt 1 withdrawn; before it holds the lease, attempt 2 turns green and the approval is withdrawn again. (5) A withdrawal that holds the lease and has read its inputs; as it writes, the owner approves again and that `ReviewChanged` event runs concurrently.
  **Expected behavior**: (1) the upload answers 503 `unavailable`, writes no `success`, and its retry gets `owner_approval_missing`. (2) nothing is written, no error, the check stays `success`. (3) and (4) the check ends `owner_approval_missing` over attempt 2. (5) the approval event waits for the lease, reads `owner_approval_missing` and re-runs the workflow once; the re-run's upload passes.
- **Test name**: `TestApprovalNoLongerNeededReruns`
  **Scenario**: an `owner_approval_missing` receipt on a pull that edited the workflow file; the pull then changes no protected path; a `ReviewChanged` event, or a sweep.
  **Expected behavior**: one re-run of the receipt's run, and the re-run's upload passes.
- **Test name**: `TestWithdrawalDuringStoreOutage`
  **Scenario**: a green check whose approval is dismissed while the Store is unreachable: the `ReviewChanged` event, a sweep and an upload retry; then the Store is back and the sweep runs.
  **Expected behavior**: during the outage the event returns an error, the sweep counts one error, the upload answers 503, and nothing is written, `success` included; after it the check becomes `owner_approval_missing`.
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
- **Test name**: `TestSweepThroughFacade` (T13, T16)
  **Scenario**: lost `PullOpened` webhook; no upload for 60 minutes; base policy change; waiver expiry; all via `Gate.Sweep` with the fixed clock.
  **Expected behavior**: the repairs of `gate/sweep`; `Summary` counts match; nothing turned green without a fresh report.

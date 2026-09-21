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
  workflow re-run when an owner's review arrives after `owner_approval_missing`, and a one-repository sweep
  when a default-branch push changes a protected path.
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
// Deps are the only things the gate path may touch: GitHub, OIDC, the Store (read-only) and a clock.
type Deps struct {
    GitHub   *Client
    OIDC     Verifier
    Store    Store
    Audience string // the App URL; OIDC audience
    Sweep    Config
    Now      func() time.Time
}

type Gate struct{ /* unexported */ }

func New(d Deps) *Gate

// UploadHandler serves POST /v1/reports.
func (g *Gate) UploadHandler() http.Handler

// HandleEvent reacts to a verified webhook event: pending check on a new head, fork refusal,
// re-run on a review change when the last receipt was owner_approval_missing, and a one-repository
// sweep when a default-branch push changes a protected path.
func (g *Gate) HandleEvent(ctx context.Context, e Event) error

// Sweep runs one reconciliation pass.
func (g *Gate) Sweep(ctx context.Context) (Summary, error)
```

```go
// NewReceiver exposes the upload authentication and binding step so that the discovery
// upload endpoint reuses exactly the same provenance checks as the report endpoint.
func NewReceiver(gh *Client, v Verifier, audience string) Receiver
```

### Re-published child contracts

Modules outside `gate` cite this file for the four blocks below. They are type aliases of the child
types in Go. `Repo` is the repository type of `github/api`, `Identity` is the claimed identity type of
`wire/manifest`, `Report` is the transport type of `wire/report`, and `Kind` is the payload kind of
`wire/envelope`; all four are restated under Integrations.

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
    Event        string // "pull_request" | "push"; discovery uploads also "workflow_dispatch"
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
    Payload  []byte   // the exact verified bytes; the only content for KindDiscovery
    Report   *Report  // nil for KindDiscovery and when ProblemReportInvalid
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
    RateLimited                          bool // sweep stopped early; the next sweep continues
}

type Sweeper interface {
    // Run re-derives the expected check state of every open PR from GitHub and repairs differences.
    Run(ctx context.Context, now time.Time) (Summary, error)
    // RunRepo is the same pass limited to one repository (used when the protected ref changes).
    RunRepo(ctx context.Context, r Repo, now time.Time) (Summary, error)
}
```

### Upload answers (`POST /v1/reports`)

| Situation                                                       | Status                     | Body                                     |
| --------------------------------------------------------------- | -------------------------- | ---------------------------------------- |
| Outcome published (any conclusion, including `action_required`) | 200                        | `{"conclusion": "...", "reason": "..."}` |
| Publication skipped (newer run reported, or PR head moved)      | 200                        | same, with this upload's outcome         |
| `*Rejection` from `Receive`                                     | `Rejection.Status`         | `{"error": "<Rejection.Code>"}`          |
| `api.ErrRateLimited` while resolving inputs or publishing       | 503 with `Retry-After: 60` | `{"error": "rate_limited"}`              |
| Any other error after authentication                            | 503 **[default]**          | `{"error": "unavailable"}`               |

`reason` is `Outcome.Reasons[0].Code`. A 2xx means "accepted and published", not "passed". The producer
retries on every non-2xx except 400, 401, 403, 409 and 413. On 503 the pending check stays pending.
The `Retry-After` value of 60 seconds is **[default]**.

Rate-limit rule, one for the whole gate: `api.ErrRateLimited` during `Receive` (before a decision exists) is the
429 `rate_limited` rejection from `gate/ingest`, passed through unchanged. `api.ErrRateLimited` after `Receive`
(resolving inputs or publishing) is 503 with `Retry-After`. The Action retries both, the pending check stays
pending, nothing green is written, and the sweep repairs what the retries do not. In the sweep the same error
stops the pass with `Summary.RateLimited`.
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
func New(repos Repos, pulls Pulls, actions Actions, v Verifier, audience string) Receiver
```

### gate/inputs (child)

- **Counterpart**: internal/gate/inputs
- **Direction**: gate depends on inputs
- **Strength**: functional (composition)
- **LCA / Rank / Distance**: gate / 1 / 1
- **Volatility**: high
- **Balanced?**: yes
- **Shared knowledge**: `Resolve` reads only the protected ref (plus an owner-approved head digest), returns an
  error on GitHub failures and never on Store failures. `SettingsFound == false` means "not onboarded".
  `PolicyDigests[0]` and `BaselineDigests[0]` are the first accepted digests: the base digest when the file exists on
  the protected ref, otherwise the owner-approved head digest; the lists are empty when neither exists. The inputs constructor is `New(repos Repos, pulls Pulls, s Store) Resolver`.

<!-- contract: Target, Trusted, Approval — restated from internal/gate/inputs/module.md -->
```go
// Target is the pull request (or default-branch push) whose trusted inputs are resolved.
type Target struct {
    Repo        Repo
    PullRequest int // 0 for a default-branch push
    HeadSHA     string
    BaseRef     string // protected ref; the default branch for a push
    Fork        bool
}

type Approval string

const (
    ApprovalNotRequired Approval = "not_required" // no protected path changed
    ApprovalGranted     Approval = "granted"      // a base-version owner approved this exact head SHA
    ApprovalMissing     Approval = "missing"
)

// Trusted is everything the gate believes, all read from the protected ref or GitHub itself.
type Trusted struct {
    Settings             Settings
    SettingsFound        bool
    PolicyDigests        []string // base digest, plus the head digest when Approval == ApprovalGranted
    BaselineDigests      []string // same rule; empty = no baseline on the protected ref
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
}
```

### gate/verdict (child)

- **Counterpart**: internal/gate/verdict
- **Direction**: gate depends on verdict
- **Strength**: functional (composition)
- **LCA / Rank / Distance**: gate / 1 / 1
- **Volatility**: high
- **Balanced?**: yes
- **Shared knowledge**: `Decide` is pure and total; `Reasons[0]` sets the conclusion; `Without` always returns `ActionRequired`.
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
    ReasonBlockingFindings    ReasonCode = "blocking_findings"
    ReasonEngineError         ReasonCode = "engine_error"
    ReasonMissingCoverage     ReasonCode = "missing_required_coverage"
    ReasonBaselineNotComparable ReasonCode = "baseline_not_comparable"
    ReasonBaselineMismatch    ReasonCode = "baseline_mismatch"
    ReasonPolicyMismatch      ReasonCode = "policy_mismatch"
    ReasonPolicyMissing       ReasonCode = "policy_missing" // settings merged, no policy on the protected ref yet
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
    Report           *Report
    IdentityApproved bool
    WorkflowApproved bool
    Trusted          Trusted
}

// Decide is a pure function. It checks, in order: subscription, settings, source and producer eligibility,
// governance (owner approval), input integrity, evidence completeness, then the engine decision.
// The step table of gate/verdict is the normative order.
func Decide(f Facts) Outcome

// Without returns the fixed outcome for a target that has no report: code is one of
// ReasonMissingUpload, ReasonStaleInputs, ReasonWaiverExpired, ReasonForkUnsupported, ReasonSuspended.
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
    EarliestWaiverExpiry time.Time  `json:"earliest_waiver_expiry"`
    Conclusion           Conclusion `json:"conclusion"`
    Reason               ReasonCode `json:"reason"`
}
```

<!-- contract: Publication, Publisher, CheckState — restated from internal/gate/feedback/module.md -->
```go
// Publication is one complete piece of feedback for one head SHA.
type Publication struct {
    Target   Target
    Identity Identity // claimed engine and image identity; zero when there was no upload
    Outcome  Outcome
    Receipt  Receipt
    Report   *Report // nil when there is no usable report
    Trusted  Trusted
}

// CheckState is the gate check currently on a head SHA.
type CheckState struct {
    Exists    bool
    Pending   bool      // in_progress, no upload yet
    StartedAt time.Time
    Receipt   Receipt   // zero when Pending
}

type Publisher interface {
    // Publish writes archfit/state, archfit/gate (only when Settings.Gate is required) and the
    // single PR comment. It is idempotent per Receipt and never overwrites feedback of a newer head or run.
    Publish(ctx context.Context, p Publication) error
    // Pending creates the in_progress gate check if none exists on the head SHA.
    Pending(ctx context.Context, t Target) error
    State(ctx context.Context, t Target) (CheckState, error)
    // Warn adds or refreshes the waiver-expiry notice in the PR comment. Idempotent.
    Warn(ctx context.Context, t Target, expiry time.Time) error
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
  repository; it needs only `RepoRef` and `Private` of the `Repo` it is given. `Errors` counts skipped units.

<!-- contract: New — restated from internal/gate/sweep/module.md -->
```go
func New(repos Repos, pulls Pulls, actions Actions, r Resolver, p Publisher, c Config) Sweeper
```

### wire/manifest

- **Counterpart**: internal/wire/manifest
- **Direction**: gate depends on wire/manifest
- **Strength**: contract
- **LCA / Rank / Distance**: root / 2 / 2
- **Volatility**: high (gate side); the manifest is moderate
- **Balanced?**: yes
- **Shared knowledge**: the manifest is locked in the binary (engine v2.3.1). `Lookup` is an exact match on all
  three fields; any empty field is a miss, so unknown never equals unknown. `Current` is not used here.

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
- **Direction**: gate depends on wire/report (types only)
- **Strength**: contract (published language)
- **LCA / Rank / Distance**: root / 2 / 2
- **Volatility**: high (gate side); the schema is moderate
- **Balanced?**: yes
- **Shared knowledge**: the façade reads `Comparison.ConfigHash`, `Comparison.GateReference.Digest` and
  `Comparison.MeasurementProfile.Version`; the rest of the report passes through to verdict and feedback.
  `Decision` is an opaque part of `Report` for this module. `Finding` is an opaque part of `Report` for this module.
  `Dimension` is an opaque part of `Report` for this module. `AgentTask` is an opaque part of `Report` for this module.
  `Module` is an opaque part of `Report` for this module. `Edge` is an opaque part of `Report` for this module. `StateSchemaV1` is the schema constant the decoder enforced.

<!-- contract: Report, StateSchemaV1, MaxBytes — restated from internal/wire/report/module.md -->
```go
// StateSchemaV1 is the engine state contract accepted (engine v2.3.1).
const StateSchemaV1 = "archfit.architecture-state.v1"

// MaxBytes caps an uploaded payload: 5 MiB (the "5 MB" cap of decisions.md).
const MaxBytes = 5 << 20

// Report is the no-source transport form of an engine state report: only allowlisted
// fields exist, so anything else in the payload is a decode error. One exception: a finding
// may carry "matched_by" as an empty object, which is accepted and dropped.
type Report struct {
    SchemaVersion     string      `json:"schema_version"` // must equal StateSchemaV1
    Decision          Decision    `json:"decision"`
    Comparison        Comparison  `json:"comparison"`
    Findings          []Finding   `json:"findings"`
    Dimensions        []Dimension `json:"dimensions"`
    AgentTasks        []AgentTask `json:"agent_tasks"`
    Modules           []Module    `json:"modules"`
    Edges             []Edge      `json:"edges"`
    ValidationCommand []string    `json:"validation_command"` // argv, replayed verbatim; never contains --refresh
}
```

<!-- contract: Comparison, MeasurementProfile, Producer, GateReference — restated from internal/wire/report/module.md -->
```go
// Comparison binds the report to the inputs the engine actually used.
type Comparison struct {
    ConfigHash         string              `json:"config_hash"` // "sha256:<hex>" of the raw policy bytes
    ModelHash          string              `json:"model_hash"`
    LabelsHash         string              `json:"labels_hash"`
    RubricHash         string              `json:"rubric_hash"`
    MeasurementProfile *MeasurementProfile `json:"measurement_profile,omitempty"` // nil = unknown identity
    GateReference      *GateReference      `json:"gate_reference,omitempty"`      // nil = no persisted baseline used
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
    Status           string `json:"status"`
    ToolVersion      string `json:"tool_version,omitempty"`
    PartialBasis     string `json:"partial_basis,omitempty"`
}

// GateReference describes the persisted baseline the engine gate used.
type GateReference struct {
    Digest     string   `json:"digest"`     // "sha256:<hex>" of the raw baseline bytes
    Comparable bool     `json:"comparable"` // false = non-comparable; Reasons says why
    Reasons    []string `json:"reasons,omitempty"`
}
```

### wire/policy

- **Counterpart**: internal/wire/policy
- **Direction**: gate depends on wire/policy (path constants only)
- **Strength**: contract
- **LCA / Rank / Distance**: root / 2 / 2
- **Volatility**: high (gate side); the engine file contract is moderate
- **Balanced?**: yes
- **Shared knowledge**: the façade compares `DefaultBranchPush.ChangedPaths` with `PolicyPath` and `BaselinePath`.
  `Digest` is not called here; digests are computed inside `gate/inputs`.

<!-- contract: PolicyPath, BaselinePath, Digest — restated from internal/wire/policy/module.md -->
```go
// PolicyPath and BaselinePath are the engine files on the protected ref. [default]
const (
    PolicyPath   = ".archfit.yaml"
    BaselinePath = ".archfit/baseline.json"
)

// Digest returns "sha256:<hex>" of the raw file bytes. It is the value the engine reports as
// comparison.config_hash (policy) and gate_reference.digest (baseline).
func Digest(raw []byte) string
```

### wire/envelope

- **Counterpart**: internal/wire/envelope
- **Direction**: gate depends on wire/envelope (the `Kind` type and `KindReport`)
- **Strength**: contract
- **LCA / Rank / Distance**: root / 2 / 2
- **Volatility**: high (gate side); the schema is moderate
- **Balanced?**: yes
- **Shared knowledge**: the report route passes `KindReport` to `Receive`. The envelope itself is handled inside `gate/ingest`.

<!-- contract: Envelope, Kind, SchemaV1, KindReport, KindDiscovery — restated from internal/wire/envelope/module.md -->
```go
// SchemaV1 is the only envelope schema version accepted.
const SchemaV1 = "archfit.report-envelope.v1"

// Kind names the payload that accompanies the envelope.
type Kind string

const (
    KindReport    Kind = "report"    // engine state report, POST /v1/reports
    KindDiscovery Kind = "discovery" // discovery draft policy, POST /v1/discoveries
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
    RunID         int64  `json:"run_id"`
    RunAttempt    int    `json:"run_attempt"`
    EngineVersion string `json:"engine_version"` // e.g. "v2.3.1"
    ImageDigest   string `json:"image_digest"`   // "sha256:<64 hex>"
    Platform      string `json:"platform"`       // e.g. "linux/amd64"
    PayloadDigest string `json:"payload_digest"` // "sha256:<64 hex>" of the exact payload bytes
}
```

### wire/settings

- **Counterpart**: internal/wire/settings
- **Direction**: gate depends on wire/settings (types only)
- **Strength**: contract
- **LCA / Rank / Distance**: root / 2 / 2
- **Volatility**: high (gate side); the file format is moderate
- **Balanced?**: yes
- **Shared knowledge**: the façade reads `Trusted.Settings.Workflow` and `Trusted.Settings.Gate`, and uses the
  `Path` constant (`.archfit-app.yaml`) in the `DefaultBranchPush` protected-path match.

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
    Version  int      `yaml:"version"`   // must be 1
    Owners   []string `yaml:"owners"`    // GitHub logins, or "org/team-slug"; at least one
    Gate     GateMode `yaml:"gate"`      // default advisory
    Workflow string   `yaml:"workflow"`  // e.g. ".github/workflows/archfit.yaml"
    DataMode DataMode `yaml:"data_mode"` // default customer
}
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
    RepoPrivate    bool
    PullRequest    int
    HeadSHA        string
    HeadRepo       string // differs from Repo for forks
    BaseRef        string
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

type Actions interface {
    Run(ctx context.Context, r RepoRef, runID int64) (WorkflowRun, error)
    Rerun(ctx context.Context, r RepoRef, runID int64) error
    Dispatch(ctx context.Context, r RepoRef, workflowPath, ref string, inputs map[string]string) error
    LatestRun(ctx context.Context, r RepoRef, workflowPath, branch string) (WorkflowRun, error)
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
- **Direction**: gate depends on store (passes `Deps.Store` to `gate/inputs`; read-only)
- **Strength**: contract
- **LCA / Rank / Distance**: root / 1 / 1
- **Volatility**: high (gate side); store is low
- **Balanced?**: yes
- **Shared knowledge**: the gate path only ever calls `Get(Tenant(installationID), EntitlementKey)`, and only inside
  `gate/inputs`. `ErrNotFound` is the store sentinel for a missing key; to the gate it means "not suspended".
  `SystemTenant` is never used by the gate, and the gate never calls `Tenants`.

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
    // DeleteTenant removes every key of the tenant. Idempotent.
    DeleteTenant(ctx context.Context, t Tenant) error
    // Tenants lists every tenant that owns at least one key, SystemTenant excluded.
    // Used only by the sweep to forget installations whose uninstall webhook was missed.
    Tenants(ctx context.Context) ([]Tenant, error)
}
```

### Consumers (inbound)

- **internal/app**: contract (constructor, handlers), root / 1 / 1, low, balanced. Calls `New`, mounts
  `UploadHandler`, calls `HandleEvent` before `control.HandleEvent`, calls `Sweep` from `/internal/sweep`,
  passes `NewReceiver(...)` to `control`.
- **internal/control/account**: contract, root / 2 / 2, moderate, balanced. Writes the JSON of `Entitlement`
  under `EntitlementKey`. The gate owns the record so that it never imports billing code.
- **internal/control/onboarding**: contract, root / 2 / 2, high, balanced. Uses `Receiver`, `Rejection`, `Upload`,
  `Binding`, `Problem` for `POST /v1/discoveries`. A discovery upload never reaches `verdict` or `feedback`.

## Internal Design

### Composition

`New(d)` builds, once: `ingest.New(d.GitHub, d.GitHub, d.GitHub, d.OIDC, d.Audience)`,
`inputs.New(d.GitHub, d.GitHub, d.Store)`, `feedback.New(d.GitHub, d.GitHub, d.GitHub, d.Now)`, and
`sweep.New(d.GitHub, d.GitHub, d.GitHub, resolver, publisher, d.Sweep)`. `verdict` has no constructor.
`NewReceiver` is `ingest.New` with the same argument pattern. A nil `Deps.Now` means `time.Now` **[default]**.

| Child      | Contributes                                             | Depends on siblings                                           |
| ---------- | ------------------------------------------------------- | ------------------------------------------------------------- |
| `ingest`   | authenticated, bound `Upload` or a `Rejection`          | none                                                          |
| `inputs`   | `Trusted` for a `Target`; owns the `Entitlement` record | none                                                          |
| `verdict`  | `Outcome` from `Facts`; `Without`                       | `ingest` (`Problem`), `inputs` (`Trusted`) — model            |
| `feedback` | checks, comment, receipt; `State`, `Pending`, `Warn`    | `verdict` (`Outcome`), `inputs` (`Target`, `Trusted`) — model |
| `sweep`    | reconciliation of open PRs                              | `inputs`, `feedback`, `verdict` — contract                    |

### Report upload flow

1. `u, err := receiver.Receive(ctx, req, KindReport)`. `*Rejection` → answer and stop. Nothing is published.
2. `target = Target{Repo: u.Binding.Repo, PullRequest, HeadSHA, BaseRef, Fork}` from the binding. For a non-PR
   upload (`PullRequest == 0`) `Target.BaseRef` is `Repo.DefaultBranch`, not `Binding.BaseRef`: the binding names the
   branch the run executed on, which is a protected ref only when it is the default branch.
3. `trusted, err := resolver.Resolve(ctx, target, now)`. Error → 503; the pending check stays.
4. `IdentityApproved` = `manifest.Lookup(u.Identity)` hits **and** the report carries a measurement profile **and**
   `profile.Version == entry.ProfileVersion`. False when `u.Report == nil`.
5. `WorkflowApproved` = `trusted.SettingsFound` **and** `u.Binding.WorkflowPath != ""` **and** it equals
   `trusted.Settings.Workflow`. A workflow file changed by the PR is a protected path; `inputs` then demands owner approval.
6. `outcome = verdict.Decide(Facts{u.Problems, u.Report, IdentityApproved, WorkflowApproved, trusted})`.
7. Receipt (table below); `publisher.Publish(ctx, Publication{target, u.Identity, outcome, receipt, u.Report, trusted})`.
   `Identity` is the envelope's claim; feedback prints it as claimed.
8. Answer 200 with conclusion and reason.

| `Receipt` field        | Value                                                                                                     |
| ---------------------- | --------------------------------------------------------------------------------------------------------- |
| `RunID`, `RunAttempt`  | from `Binding`                                                                                            |
| `ReportDigest`         | `Upload.Digest`                                                                                           |
| `PolicyDigest`         | the report's `ConfigHash` when it is in `Trusted.PolicyDigests`; else `PolicyDigests[0]`; else `""`       |
| `BaselineDigest`       | the report's `GateReference.Digest` when it is in `BaselineDigests`; else `BaselineDigests[0]`; else `""` |
| `EarliestWaiverExpiry` | `Trusted.EarliestWaiverExpiry`                                                                            |
| `Conclusion`, `Reason` | `outcome.Conclusion`, `outcome.Reasons[0].Code`                                                           |

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

### HandleEvent

`Target.Repo` is built from the verified event: `Repo{RepoRef{e.InstallationID, e.Repo}, Private: e.RepoPrivate}`;
`ID` and `DefaultBranch` stay zero and are not needed for pull request targets **[default: saves one API call]**.

| `Event.Kind`                     | Reaction                                                                                                                                                                                                                           |
| -------------------------------- | ---------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------- |
| `PullOpened`, `PullSynchronized` | `Resolve`. `!SettingsFound` → nothing. Fork → `Publish(Without(ReasonForkUnsupported))` with a zero-run receipt carrying the base digests. Else `Settings.Gate == GateRequired` → `publisher.Pending(target)`. Advisory → nothing. |
| `ReviewChanged`                  | `State(target)`; only when its receipt reason is `owner_approval_missing`: `Resolve`; when `Approval == ApprovalGranted` **[default: no re-run for a review that grants nothing]** → re-run `receipt.RunID` under the guard below. |
| `DefaultBranchPush`              | `ChangedPaths` empty (= unknown), or it contains `policy.PolicyPath`, `policy.BaselinePath`, `settings.Path`, or any path under `.github/workflows/` → `sweeper.RunRepo(ctx, repo, now)` in the request. Otherwise nothing.        |
| `PullClosed`                     | nothing.                                                                                                                                                                                                                           |
| installation kinds, `Ignored`    | nothing; `control` handles installations.                                                                                                                                                                                          |

Protected default-branch push: the exact fourth protected path is `Settings.Workflow`, which lives on the protected
ref. The façade matches the whole `.github/workflows/` directory instead **[default]**: `wire/settings` only accepts
a workflow path under it, so this is a superset, it needs no file read, and an extra `RunRepo` is harmless because the
pass is idempotent. `RunRepo` re-resolves every open PR of the repository, so a changed base policy or baseline shows
as `stale_inputs` (plus a re-run) within the webhook request instead of within the hour. The hourly sweep stays the
recovery path when the push event is lost or `RunRepo` stops on a rate limit.

Fork refusal publication: `Publication{target, Identity: zero, Outcome: verdict.Without(ReasonForkUnsupported),
Receipt, Report: nil, trusted}` with this receipt (the same construction the sweep uses for a target without a prior receipt):

| `Receipt` field                       | Value                                        |
| ------------------------------------- | -------------------------------------------- |
| `RunID`, `RunAttempt`, `ReportDigest` | zero: no workflow run reported for this head |
| `PolicyDigest`                        | `trusted.PolicyDigests[0]`, or `""`          |
| `BaselineDigest`                      | `trusted.BaselineDigests[0]`, or `""`        |
| `EarliestWaiverExpiry`                | `trusted.EarliestWaiverExpiry`               |
| `Conclusion`, `Reason`                | `ActionRequired`, `ReasonForkUnsupported`    |

The `external_id` is then `v1:0:0:`. Any later upload for the head has a higher run pair and replaces it.

Re-run guard (same rule as the sweep): `Actions.Run(receipt.RunID)`; call `Actions.Rerun` only when the run is
`completed` and `run.Attempt == receipt.RunAttempt`. The handler is idempotent; a redelivered event changes nothing.
An error is returned to `app` (which logs it); the sweep repairs what a failed or lost event left undone.

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
- The Store is read-only on the gate path, and only for the `Entitlement` record. A Store outage or a missing
  or unreadable record means "not suspended" **[default]**; it never establishes non-payment and never blocks the
  pipeline or a merge (2 second read timeout in `inputs` **[default]**). Recovery of a missing record is not the gate's
  job: `control`'s sweep backfills it (Observe + Refresh for every installation), so a missed installation webhook
  delays the trial start by at most one sweep interval.
- Suspension is `action_required` in category `subscription`: never an architecture failure, never a success.
  A restored entitlement re-runs the workflow; an old receipt is never promoted to `success`.
- Nothing is persisted by the gate. Reports and payloads live in memory for one request and are dropped.
- Handlers do their work in the request and are idempotent; GitHub holds the dedup keys (`external_id`, comment marker).
- GitHub rate-limit exhaustion yields 429 `rate_limited` during `Receive`, 503 with `Retry-After` after it, or a
  stopped sweep; the Action retries both statuses; checks stay pending; never green.
- Logs (`log/slog` default logger **[default]**, because `Deps` carries none): installation ID, repository ID, PR number,
  head SHA, run ID, attempt, digests, conclusion, reason codes, HTTP status, timings. Never report bodies, paths, messages, tokens.
- Standard library first; the gate adds no third-party dependency.

## Test Specification

Composition tests use a fake GitHub HTTP server behind a real `*Client`, a stub `Verifier`, `store.NewMemory`
(`NewMemory` is the in-process Store constructor of `internal/store`), a failing Store stub, a fixed clock, and the
`internal/wire/testdata` fixtures. Child internals are tested in the children.

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
  **Scenario**: matching digests; approved head policy digest; mismatching `config_hash`; no baseline; nil report.
  **Expected behavior**: the values of the receipt table; base digests when the report's do not match.
- **Test name**: `TestHTTPMapping`
  **Scenario**: every row of "Upload answers", with each rejection status once.
  **Expected behavior**: the listed status, JSON body and `Retry-After`; no body ever contains report content.

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
  **Scenario**: table of `DefaultBranchPush.ChangedPaths`: `.archfit.yaml`; `.archfit/baseline.json`; `.archfit-app.yaml`; `.github/workflows/archfit.yaml`; another workflow file; only source files; empty list (unknown); each with a second repository that has stale PRs.
  **Expected behavior**: `RunRepo` for the pushed repository in the first five rows and for the empty list; no call for source-only pushes; the other repository is never touched; a `RateLimited` summary is not an error.
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
  **Expected behavior**: 503 (`Retry-After` for rate limit); the pending check is unchanged; a retry of the same upload completes once.
- **Test name**: `TestClientSuppliedTenantIsIgnored`
  **Scenario**: another installation's ID in headers, query and envelope-like fields, with an entitlement stored for that other tenant.
  **Expected behavior**: the Store is read under the claim's tenant only; no API call addresses the other tenant.
- **Test name**: `TestNoSourceLeaves` (T04)
  **Scenario**: seeded leakage fixtures uploaded with correct digests.
  **Expected behavior**: `action_required`/`report_invalid`; seeded strings appear in no log line, check, comment or HTTP body.
- **Test name**: `TestLogsCarryOnlyIdentifiers`
  **Scenario**: upload, webhook and sweep flows with marker strings in paths and messages, capturing logger.
  **Expected behavior**: IDs, digests, outcomes, timings only.

### Behavior Tests

- **Test name**: `TestLegitimateChangePassesAndViolationFails` (T02)
  **Scenario**: the paired healthy and blocked fixtures of each claimed erosion scenario; the warning-only attention fixture.
  **Expected behavior**: `success`, `failure`, `success` with `needs_attention` visible on `archfit/state`; feedback names rule, modules, locations and the validation command.
- **Test name**: `TestTamperingNeverYieldsSuccess` (T12, T14)
  **Scenario**: head-edited policy, baseline, settings (owners, gate mode), workflow and CODEOWNERS; an ordinary non-owner approval; an owner approval followed by a new commit; a report without `gate_reference` while a baseline exists.
  **Expected behavior**: `policy_mismatch`, `baseline_mismatch` or `owner_approval_missing`; never `success`; after the owner approves the new head, the same upload re-run passes.
- **Test name**: `TestAtomicCodeAndPolicyPullRequest` (T14)
  **Scenario**: a PR changes code and policy; the engine ran with the head policy; an owner approved the exact head.
  **Expected behavior**: before approval `owner_approval_missing` (and `policy_mismatch`); after approval and re-run `success`; a further commit returns it to `action_required`.
- **Test name**: `TestNonComparableBaselineRecovery` (T15)
  **Scenario**: profile upgrade makes `gate_reference` non-comparable; then an owner-approved baseline PR merges and the workflow re-runs.
  **Expected behavior**: `baseline_not_comparable` with the baseline-PR recovery text; `success` only after the new baseline is on the protected ref.
- **Test name**: `TestDuplicateAndOutOfOrderDelivery` (T03, T11)
  **Scenario**: the same upload twice; an older attempt after a newer one; an upload for a superseded head.
  **Expected behavior**: one comment, updated in place; the newer result stands; 409 for the stale uploads.
- **Test name**: `TestReviewTriggersRerun` (T14)
  **Scenario**: `owner_approval_missing` receipt; the owner approves; webhook delivered; then delivered again.
  **Expected behavior**: one re-run; the check stays `action_required` until the new upload; the second delivery finds the run in progress and does nothing.
- **Test name**: `TestSuspensionAndRestoration` (T21)
  **Scenario**: private repository past trial and grace; upload; then the record is replaced by a grant and the sweep runs; separately the Store fails during an upload.
  **Expected behavior**: `action_required`/`subscription`, not `failure` and not `success`; after restoration a re-run is requested and only the fresh report turns the check green; with the Store down the upload is decided normally (outage is not non-payment).
- **Test name**: `TestGateSurvivesControlPlaneFailure`
  **Scenario**: no `control` package wired at all; Store failing; `control.HandleEvent` returning errors after `gate.HandleEvent`.
  **Expected behavior**: uploads, pending checks and the sweep complete.
- **Test name**: `TestBasePolicyChangeReachesOpenPullsAtOnce` (T12, T16)
  **Scenario**: three open PRs with `success` receipts; a policy PR merges and the `DefaultBranchPush` event arrives; separately the same merge with the event lost, followed by the hourly `Gate.Sweep`.
  **Expected behavior**: within `HandleEvent` each PR shows `stale_inputs` and one re-run is requested per PR; with the event lost the sweep performs the same repair; in both cases nothing is green until a fresh report arrives, and a later sweep adds nothing.
- **Test name**: `TestSweepThroughFacade` (T13, T16)
  **Scenario**: lost `PullOpened` webhook; no upload for 60 minutes; base policy change; waiver expiry; all via `Gate.Sweep` with the fixed clock.
  **Expected behavior**: the repairs of `gate/sweep`; `Summary` counts match; nothing turned green without a fresh report.

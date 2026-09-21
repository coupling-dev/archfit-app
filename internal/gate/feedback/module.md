# Gate Feedback

**Path**: internal/gate/feedback/
**Parent**: internal/gate/ (gate façade)
**Submodules**: none (leaf)

## Purpose

`feedback` is the only writer of what a developer sees: the `archfit/gate` and `archfit/state` check runs
and the single PR comment. It also makes the gate check its own receipt, so that GitHub holds the
idempotency key and the facts a later sweep compares. Without it, retries would spam comments, an old run
could paint over a newer head, and the App would need a database to remember what it said.

## Functional Responsibilities

- Publish one `Publication`: `archfit/state` always, `archfit/gate` only in required mode, the comment for pull requests.
- Create the pending (`in_progress`) gate check for a new head.
- Read back the gate check of a head SHA as `CheckState`, including the parsed `Receipt`.
- Add or refresh the waiver-expiry notice in the comment.
- Render feedback that is useful without an LLM: verdict, reason and next action first; findings with rule,
  modules and locations; introduced versus accepted debt; dimensions; a mandatory evidence-gap section;
  input digests and measurement identity; per-dimension trust and completeness; validation command replay;
  a marker-delimited JSON block of agent tasks.
- Keep every write idempotent and never let an older run or head overwrite newer feedback.
- Stay under GitHub size limits with safe truncation.

## Subdomain Classification

Core. Feedback quality is what makes the gate acceptable to developers and agents. Volatility: high;
wording, layout and limits will be tuned with every pilot.

## Encapsulated Knowledge

- Check names, titles, the `external_id` format, and the hidden marker formats.
- The comment and summary layout, truncation order and size budgets.
- The idempotency and ordering rules (run ordering, head ordering).
- The mapping of `Outcome` to the `archfit/state` conclusion, including the only use of `neutral`.
- Sanitizing of engine-authored strings before they enter Markdown.
- Annotation selection and the cap.

## Public Contract

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

```go
func New(checks Checks, comments Comments, pulls Pulls, now func() time.Time) Publisher
```

### Fixed names and markers

| Item                  | Value                                                                           |
| --------------------- | ------------------------------------------------------------------------------- |
| Gate check name       | `archfit/gate`                                                                  |
| State check name      | `archfit/state`                                                                 |
| `external_id`         | `v1:<run_id>:<attempt>:<report_digest>`; empty for a pending check              |
| Receipt marker        | `<!-- archfit:receipt {compact JSON of Receipt} -->`, last line of each summary |
| Comment marker        | `<!-- archfit:comment -->`, first line of the comment                           |
| Comment head marker   | `<!-- archfit:head <sha> run <id>.<attempt> -->`, second line                   |
| Waiver notice section | `<!-- archfit:waiver <YYYY-MM-DD> -->` … `<!-- /archfit:waiver -->`             |
| Agent task block      | `<!-- archfit:agent-tasks -->`, a `json` fence, `<!-- /archfit:agent-tasks -->` |

The check names and the comment marker come from the root contract. The `v1:` layout of `external_id` comes from
the `Receipt` contract. Every other `archfit:*` marker string, the JSON-in-comment form of the receipt marker, and the
placement rules ("last line", "first line", "second line") are **[default]**: decisions.md only says that a hidden
marker and an `external_id` with run ID, attempt and report digest exist.

### What is written when

| Target                                        | `archfit/state` | `archfit/gate`                                 | Comment |
| --------------------------------------------- | --------------- | ---------------------------------------------- | ------- |
| Pull request, `Settings.Gate == GateRequired` | yes             | yes, `Conclusion = string(Outcome.Conclusion)` | yes     |
| Pull request, advisory                        | yes             | no                                             | yes     |
| Pull request, `!Trusted.SettingsFound`        | yes             | no                                             | no      |
| Push (`Target.PullRequest == 0`)              | yes             | no                                             | no      |

`archfit/state` conclusion: `success` when `Outcome.Conclusion == Success` and the report's
`NeedsAttention` is false; otherwise the literal `neutral` **[default]**. The state check is never
required, so `neutral` is allowed here and only here. It is a string literal in this package and never a `Conclusion` value.

### Publish algorithm

Order: gate check, state check, comment **[default: the required check first]**. Any error ends the call;
a retry repeats the whole call safely.

For each check to write:

1. `Checks.CheckRuns(repo, Target.HeadSHA, name)`; take the run with the highest ID **[default]**.
2. None → `CreateCheckRun` (completed).
3. Existing is `in_progress`, or its receipt marker is absent or unreadable → `UpdateCheckRun`.
4. Existing receipt has a higher `(RunID, RunAttempt)` pair than `p.Receipt` → write nothing for this check.
5. Otherwise (same `external_id`, same pair, or lower pair) → `UpdateCheckRun` in place.

Every create and update sets `Status = "completed"`, `ExternalID`, `StartedAt = now()` **[default: the
check's `StartedAt` is the time of the last publication; the sweep relies on it]**, title, summary and,
for `archfit/state`, annotations.

Comment (pull requests with settings only):

1. `Pulls.Pull`; if `pull.HeadSHA != Target.HeadSHA` the publication is for an old head → leave the comment alone.
2. `Comments.AppComments`; the comment is the one with the lowest ID whose body starts with the comment marker **[default]**.
3. None → `CreateComment`.
4. Found, its head marker names the same head and a higher `(run, attempt)` → leave it.
5. Otherwise → `UpdateComment`, unless the new body is byte-identical. A waiver notice section in the old
   body is carried over when its date equals `Trusted.EarliestWaiverExpiry` (UTC date); otherwise dropped.

`Pending`: `CheckRuns` for `archfit/gate`; any run exists → nothing. Else create `in_progress`, empty
`ExternalID`, `StartedAt = now()`, summary "Waiting for the Archfit report. If no report arrives in time, this check asks for action." (no duration: the
timeout is the sweep's `MissingUploadAfter`, which feedback never receives).
The caller decides whether the repository is in required mode.

`State`: highest-ID `archfit/gate` run on `Target.HeadSHA`. None → zero `CheckState`. `Pending` =
`Status != "completed"`. `Receipt` = the last receipt marker of the summary; absent or unreadable → zero `Receipt`.
`CheckRuns` returns only check runs created by this App, so a same-named check of another producer is never
read as a receipt.

`Warn`: find the comment as above. Section with the same date present → nothing. Present with another
date → replace. Comment without section → insert after the verdict block. No comment → create one holding the
comment marker and the section. No-op for `Target.PullRequest == 0`.

### Layout

Comment, in order. Check summaries use the same blocks: gate = 1, 2, 8, receipt marker; state = 1, 2, 5, 6, 7, 8, receipt marker.

1. Markers, then the verdict line: "Passed", "Blocked by architecture findings", or "Action required" **[default]**,
   then `Reasons[0]`: code, category, `Detail`, `Next`.
2. Further reasons, one line each (for example unevaluated rules beside a blocker).
3. `Trusted.Entitlement.Notice`, when `WarnFrom` is set, `now()` is at or past it, and the notice is not empty.
4. Waiver notice section (owned by `Warn`).
5. Findings: blocking first. Group "Introduced" (`introduced`, `expired_waiver`), "Accepted debt — display
   only" (`baseline`, `waived`), "Fixed" (`fixed`). Per finding: rule ID, `From` → `To`, locations as
   `path:line`, message. At most 50 per group, then "and N more" **[default]**. Grouping never changes the verdict.
6. Dimensions: name, status, confidence, coverage, delta, trust, completeness. Trust prints
   "customer-attested" for `customer_attested` and "unknown" for anything else **[default]**.
7. Evidence gaps, always present: unevaluated required rules with their reasons, dimensions not `complete`,
   profile `Unknowns`, producers whose status is not complete. "None reported" when empty; "No usable report" when `Report == nil`.
8. Inputs and identity: `Receipt.PolicyDigest`, `Receipt.BaselineDigest` (or "no baseline"), measurement profile
   version and settings hash (or "missing"), the claimed producer identity from `Publication.Identity` as
   "engine <version>, image <digest>, <platform>" (or "no upload" when it is zero; labeled "claimed", because
   only the manifest check in `gate` judges it), `ApprovedBy` when approval was granted, and the fixed sentence
   "Measurement is customer-attested: the producer is authenticated, the report content is not independently checked."
9. Validation command: `Report.ValidationCommand` joined with shell quoting, verbatim; an argument `--refresh` is dropped if ever present.
10. Agent task block: the JSON array of `AgentTask` exactly as decoded (`repair_kind` preserved), whole tasks
    only; tasks that do not fit are counted in a line below the block.

When `Reasons[0].Category` is `subscription`, blocks 5 to 7, 9 and 10 are omitted **[default]**: paid output is suspended, the reason and recovery stay.

Annotations: only on `archfit/state`; from `Finding.Locations` only; blocking findings first, report order;
level `failure` for blocking, `warning` otherwise; `Line == 0` is sent as line 1; at most 50 per update
**[default]**. A finding with `locations: []` gets no annotation.

Size: summaries and the comment stay at or under 65 535 bytes. The receipt marker and blocks 1, 2, 7, 8 are
budgeted first and never cut. Then block 10 shrinks by whole tasks, then block 5 is cut at a line end and
ends with "Output truncated." Cuts never split a UTF-8 sequence or a marker.

## Integrations

### gate/verdict

- **Counterpart**: internal/gate/verdict
- **Direction**: feedback depends on verdict
- **Strength**: model (renders `Outcome`, stores `Conclusion` and `ReasonCode` in the receipt)
- **LCA / Rank / Distance**: gate / 1 / 1
- **Volatility**: high
- **Balanced?**: yes (model strength at distance 1)
- **Shared knowledge**: `Reasons[0]` determines the conclusion; `Detail` holds IDs and digests; `Next` is the recovery action.

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

### gate/inputs

- **Counterpart**: internal/gate/inputs
- **Direction**: feedback depends on inputs
- **Strength**: model (reads `Target` and `Trusted`)
- **LCA / Rank / Distance**: gate / 1 / 1
- **Volatility**: high
- **Balanced?**: yes
- **Shared knowledge**: `Target.PullRequest == 0` is a push; `Trusted.Settings.Gate` selects required mode;
  `Entitlement.Notice` is shown from `WarnFrom` on. feedback never resolves inputs itself.

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

### wire/settings

- **Counterpart**: internal/wire/settings
- **Direction**: feedback depends on wire/settings (types only)
- **Strength**: contract
- **LCA / Rank / Distance**: root / 2 / 2
- **Volatility**: high (feedback side); the file format is moderate
- **Balanced?**: yes
- **Shared knowledge**: only `Settings.Gate` and the `GateRequired` constant are read.

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

### wire/report

- **Counterpart**: internal/wire/report
- **Direction**: feedback depends on wire/report
- **Strength**: contract (published language)
- **LCA / Rank / Distance**: root / 2 / 2
- **Volatility**: high (feedback side); the schema is moderate
- **Balanced?**: yes
- **Shared knowledge**: every string in a `Report` is engine-authored, single-line and length-capped by the
  decoder, yet still untrusted for Markdown. `Origin` sorts presentation only. `Locations` may be empty.
  `Module` is an opaque part of `Report` for this module. `Edge` is an opaque part of `Report` for this module;
  neither is rendered. `StateSchemaV1` is the schema constant the decoder enforced.

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

<!-- contract: Decision, UnevaluatedRule — restated from internal/wire/report/module.md -->
```go
// Decision carries the engine's structured gate decision. ExitCode follows `archfit check`:
// 0 healthy, 1 blocked, 2 attention, 3 error.
type Decision struct {
    ExitCode                 int               `json:"exit_code"`
    HardGates                string            `json:"hard_gates"` // "pass" | "fail" | "unmeasured"
    NeedsAttention           bool              `json:"needs_attention"`
    UnevaluatedRequiredRules []UnevaluatedRule `json:"unevaluated_required_rules,omitempty"`
}

type UnevaluatedRule struct {
    RuleID string `json:"rule_id"`
    Reason string `json:"reason"`
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

<!-- contract: Finding, Location, Dimension, AgentTask — restated from internal/wire/report/module.md -->
```go
type Finding struct {
    ID        string     `json:"id"`        // unique per report across lifecycles
    RuleID    string     `json:"rule_id"`
    Lifecycle string     `json:"lifecycle"` // "introduced" | "baseline" | "fixed" | "waived" | "expired_waiver"
    Blocking  bool       `json:"blocking"`
    From      string     `json:"from"` // module name, may be ""
    To        string     `json:"to"`   // module name, may be ""
    Message   string     `json:"message"` // engine-authored, length-capped
    Locations []Location `json:"locations"` // may be empty; never invent one
}

type Location struct {
    Path string `json:"path"`
    Line int    `json:"line"` // 0 = whole file
}

// Dimension mirrors the engine's per-dimension status; trust and completeness stay separate.
type Dimension struct {
    Name         string  `json:"name"`
    Status       string  `json:"status"`
    Confidence   string  `json:"confidence"`
    Coverage     float64 `json:"coverage"`
    Delta        float64 `json:"delta"`
    Trust        string  `json:"trust"`        // "customer_attested" in v1
    Completeness string  `json:"completeness"` // "complete" | "partial" | "missing"
}

type AgentTask struct {
    ID         string   `json:"id"`
    RuleID     string   `json:"rule_id"`
    RepairKind string   `json:"repair_kind"` // "code_change" | "needs_owner_decision"
    Origin     string   `json:"origin"`      // presentation only; never exempts a finding
    Summary    string   `json:"summary"`
    FindingIDs []string `json:"finding_ids"`
}
```

### wire/manifest

- **Counterpart**: internal/wire/manifest
- **Direction**: feedback depends on wire/manifest (the `Identity` type only)
- **Strength**: contract
- **LCA / Rank / Distance**: root / 2 / 2
- **Volatility**: high (feedback side); the manifest is moderate
- **Balanced?**: yes
- **Shared knowledge**: `Identity` is a claim. feedback prints its three fields and never calls `Lookup` or `Current`.

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

### github/api

- **Counterpart**: internal/github/api
- **Direction**: feedback depends on github/api (role interfaces `Checks`, `Comments`, `Pulls`)
- **Strength**: contract
- **LCA / Rank / Distance**: root / 2 / 2
- **Volatility**: high (feedback side); the client is low
- **Balanced?**: yes
- **Shared knowledge**: a `Summary` over 65 535 bytes is an error from the client, so feedback truncates first.
  `AppComments` returns only comments authored by the App's bot user. `ErrRateLimited` is the sentinel
  returned after bounded backoff; feedback passes it up unchanged. `ErrNotFound` is the sentinel for a
  missing object. Used methods: all of `Checks` and `Comments`, and `Pulls.Pull`. `Review` is not used here.

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

<!-- contract: Pulls, Pull, Review — restated from internal/github/api/module.md -->
```go
type Pull struct {
    Number       int
    State        string // "open" | "closed"
    Merged       bool
    Draft        bool
    Body         string
    URL          string // html_url
    HeadSHA      string
    HeadRef      string
    HeadRepo     string // "owner/name"; differs from the base repository for forks
    BaseRef      string
    BaseSHA      string
    MergeBaseSHA string
    UpdatedAt    time.Time
}

type Review struct {
    Reviewer  string // login
    State     string // "APPROVED" | "CHANGES_REQUESTED" | "COMMENTED" | "DISMISSED"
    CommitSHA string // the head SHA the review was submitted on
}

type Pulls interface {
    Pull(ctx context.Context, r RepoRef, number int) (Pull, error)
    OpenPulls(ctx context.Context, r RepoRef) ([]Pull, error)
    // PullByHead returns the newest PR, in any state, whose head is branch in this repository. ErrNotFound if none.
    PullByHead(ctx context.Context, r RepoRef, branch string) (Pull, error)
    // ChangedFiles lists changed paths; a rename lists both the old and the new path.
    // truncated is true when GitHub's file list was cut off: callers must assume any path changed.
    ChangedFiles(ctx context.Context, r RepoRef, number int) (paths []string, truncated bool, err error)
    Reviews(ctx context.Context, r RepoRef, number int) ([]Review, error)
    // TeamMember reports whether login belongs to "org/team-slug".
    TeamMember(ctx context.Context, r RepoRef, team, login string) (bool, error)
}
```

<!-- contract: Checks, CheckRun, Annotation — restated from internal/github/api/module.md -->
```go
type Annotation struct {
    Path    string
    Line    int
    Level   string // "failure" | "warning" | "notice"
    Message string
}

type CheckRun struct {
    ID          int64 // 0 on create
    Name        string
    HeadSHA     string
    ExternalID  string
    Status      string // "in_progress" | "completed"
    Conclusion  string // "" unless completed
    Title       string
    Summary     string // markdown, max 65535 bytes
    Annotations []Annotation
    StartedAt   time.Time
}

type Checks interface {
    // CheckRuns returns only check runs created by this App.
    CheckRuns(ctx context.Context, r RepoRef, headSHA, name string) ([]CheckRun, error)
    CreateCheckRun(ctx context.Context, r RepoRef, c CheckRun) (CheckRun, error)
    UpdateCheckRun(ctx context.Context, r RepoRef, c CheckRun) error
}
```

<!-- contract: Comments, Comment — restated from internal/github/api/module.md -->
```go
type Comment struct {
    ID   int64
    Body string
}

// Comments are PR conversation comments authored by the App.
type Comments interface {
    AppComments(ctx context.Context, r RepoRef, pull int) ([]Comment, error)
    CreateComment(ctx context.Context, r RepoRef, pull int, body string) (Comment, error)
    UpdateComment(ctx context.Context, r RepoRef, id int64, body string) error
}
```

### Consumers (inbound)

- **internal/gate** (parent): functional, gate / 1 / 1, high, balanced. Builds `Publication` and `Receipt`; calls `Publish`, `Pending`, `State`.
- **internal/gate/sweep**: contract, gate / 1 / 1, high, balanced. Calls all four `Publisher` methods and compares `Receipt` fields.

## Change Vectors

- Wording, layout, grouping, caps and truncation order.
- A new marker version (`v2:` external IDs) when the receipt changes shape.
- SARIF-based annotations, or annotations on the gate check.
- A different `archfit/state` conclusion mapping.

## Constraints and Invariants

- The gate check conclusion is always `string(Outcome.Conclusion)`: `success`, `failure` or `action_required`.
  feedback never downgrades, upgrades or skips it. `neutral` is written only to `archfit/state`.
- In advisory mode no `archfit/gate` check is written. A ruleset that requires it then stays blocked: closed, never green.
- An older `(RunID, RunAttempt)` never overwrites a newer one on the same head; a publication for an old
  head never touches the comment. Check runs are per SHA, so an old head's checks cannot affect the new head.
- Exactly one comment per PR under sequential delivery. Two concurrent first publications may create two;
  later calls update only the lowest-ID one **[default, stated limit]**. The same limit holds for check runs (highest ID wins).
- Never print "verified", a score, a grade or a percentage of health. Trust is "customer-attested".
  Provenance is necessary, not sufficient.
- Never invent a location, a repair route or independent evidence. No annotation without a `Location`.
- Origin and lifecycle are presentation only; grouping never hides a blocking finding.
- Engine-authored strings are sanitized before rendering: `<!--`, `-->` and backticks are removed, `<` and `>`
  HTML-escaped, newlines dropped. The agent task JSON is encoded with HTML escaping (`<`). So no report
  content can forge a marker; `State` still reads only the last receipt marker.
- `Publication.Identity` is a claim copied from the envelope. feedback prints it as claimed and never as
  approved; identity strings are sanitized like every other producer-supplied string.
- No LLM, no network other than the three role interfaces, no Store. Imports: standard library,
  `gate/verdict`, `gate/inputs`, `wire/report`, `wire/settings`, `wire/manifest`, `github/api`. Never `control`, `app`, LLM or UI packages.
- Rendering is deterministic: equal `Publication` and clock give byte-identical text.
- `api.ErrRateLimited` and other API errors are returned unchanged. A failed publication leaves the pending
  check pending; it never produces green.
- Logs (`log/slog` default logger **[default]**): repository ID, PR number, head SHA, run ID, attempt, digests,
  conclusion, reason codes, action taken (`created`, `updated`, `skipped_newer`, `skipped_old_head`), timings.
  Never bodies, paths, messages or names.
- Safe for concurrent use; no state between calls.

## Test Specification

Fakes: in-memory `Checks`, `Comments`, `Pulls` with call recording and injectable errors; a fixed clock.
Golden files hold rendered bodies for the wire fixtures.

### Unit Tests

- **Test name**: `TestReceiptRoundTrip`
  **Scenario**: render a summary for a receipt with every field set, then parse it; also a summary with two markers and one with broken JSON.
  **Expected behavior**: equal receipt; the last marker wins; broken JSON gives the zero receipt. `external_id` is `v1:<run_id>:<attempt>:<report_digest>`.
- **Test name**: `TestRunOrdering`
  **Scenario**: table of existing versus new `(RunID, RunAttempt)`: equal; new attempt higher; new run higher; new attempt lower; new run lower; existing pending; existing without marker.
  **Expected behavior**: update, update, update, skip, skip, update, update.
- **Test name**: `TestStateConclusionMapping`
  **Scenario**: success without attention; success with `NeedsAttention`; failure; action required; nil report.
  **Expected behavior**: `success`, `neutral`, `neutral`, `neutral`, `neutral` on `archfit/state`; the gate check always carries the outcome's conclusion.
- **Test name**: `TestSanitizeEngineStrings`
  **Scenario**: messages, summaries, rule IDs, module names and paths containing `<!-- archfit:receipt {...} -->`, `-->`, backticks, HTML and a forged comment marker.
  **Expected behavior**: no marker sequence survives outside App-authored markers; `State` on the result returns the real receipt.
- **Test name**: `TestTruncation`
  **Scenario**: 10 000 findings with maximum-length messages and 2 000 agent tasks; multi-byte module names at the cut point.
  **Expected behavior**: summary and comment ≤ 65 535 bytes; receipt marker, verdict, evidence gaps and inputs block intact; valid UTF-8; valid JSON in the task block; "Output truncated." present.
- **Test name**: `TestIdentityLine`
  **Scenario**: a full `Identity`; the zero `Identity` (sweep publication); an identity whose fields contain marker text.
  **Expected behavior**: "claimed: engine v2.3.1, image sha256:…, linux/amd64"; "no upload"; sanitized text; never the word "approved" or "verified".
- **Test name**: `TestAnnotationSelection`
  **Scenario**: findings with no, one and many locations; 80 locations in total; `Line == 0`.
  **Expected behavior**: at most 50 annotations, blocking first; none for `locations: []`; line 1 for whole-file; only on `archfit/state`.

### Integration Contract Tests

- **Test name**: `TestPublishMatrix`
  **Scenario**: the four rows of "What is written when".
  **Expected behavior**: exactly the listed objects are created, with the fixed names; push and settings-missing targets get no comment and no gate check.
- **Test name**: `TestPendingIsIdempotent`
  **Scenario**: `Pending` twice; `Pending` after a completed check exists.
  **Expected behavior**: one `in_progress` `archfit/gate` run with `StartedAt` from the clock; no write in the other cases.
- **Test name**: `TestStateReadsBack`
  **Scenario**: no check; pending check; completed check published by `Publish`.
  **Expected behavior**: `Exists` false; `Pending` true with `StartedAt`; `Receipt` equal to the published one and `StartedAt` equal to the publication time.
- **Test name**: `TestWarnIsIdempotent`
  **Scenario**: `Warn` with no comment; again with the same expiry; with another expiry; then `Publish` with matching and with non-matching `EarliestWaiverExpiry`; `Warn` on a push target.
  **Expected behavior**: one comment created; no call; section replaced; section kept; section dropped; no call.
- **Test name**: `TestGoldenFeedback` (T02, T11)
  **Scenario**: render every wire fixture: healthy, warning-only attention, blocked, missing required evidence, unknown identity, incompatible profile, invalid input (nil report), blocker plus unevaluated rule, non-applicable rule, `locations: []`, `matched_by: {}`, both repair kinds, waiver expiry.
  **Expected behavior**: golden bodies match; verdict, reason and next action come first; the evidence-gap section exists in each; `repair_kind` values appear unchanged in the JSON block; the validation command is replayed verbatim with `--base`, `--lang`, `--require-tools` when present and never `--refresh`.

### Boundary Tests

- **Test name**: `TestNoVerifiedNoScore` (T11)
  **Scenario**: scan every golden body and every title for App-authored text.
  **Expected behavior**: no "verified" (any case), no "score", no grade; "customer-attested" present whenever a report is rendered.
- **Test name**: `TestGateCheckNeverNeutral` (T13)
  **Scenario**: publish every `Outcome` the verdict tests produce, in required mode.
  **Expected behavior**: `archfit/gate` conclusion is one of `success`, `failure`, `action_required`, equal to the outcome.
- **Test name**: `TestRateLimitLeavesPendingAlone`
  **Scenario**: `ErrRateLimited` from `CheckRuns`, from `UpdateCheckRun`, from `CreateComment`.
  **Expected behavior**: the error is returned with `errors.Is` intact; the pending check is unchanged; a retry completes the publication without duplicates.
- **Test name**: `TestSummaryLimitIsRespected`
  **Scenario**: the fake `Checks` rejects summaries over 65 535 bytes, as the real client does.
  **Expected behavior**: no publication of any fixture or stress input is rejected.
- **Test name**: `TestLogsCarryOnlyIdentifiers`
  **Scenario**: fixtures with marker strings in paths and messages, capturing logger.
  **Expected behavior**: no marker string in logs.
- **Test name**: `TestForeignCommentsAreIgnored`
  **Scenario**: App comments without the marker and a user comment that quotes the marker (not returned by `AppComments`).
  **Expected behavior**: neither is updated; a new marked comment is created.

### Behavior Tests

- **Test name**: `TestRepeatedDeliveryDoesNotSpam` (T11)
  **Scenario**: the same publication five times; then the same run with a new attempt.
  **Expected behavior**: one comment and one check run per name; the comment is updated once for the new attempt; identical bodies cause no comment update call.
- **Test name**: `TestOldRunCannotOverwriteNewerHead` (T11, T03)
  **Scenario**: publish run 7 for head B; then run 6 for head B; then run 5 for the old head A while the PR head is B.
  **Expected behavior**: head B feedback is unchanged; head A gets its own checks; the comment still describes head B run 7.
- **Test name**: `TestUsefulWithoutLLM` (T11)
  **Scenario**: healthy, blocked and partial fixtures.
  **Expected behavior**: each body names rule, modules, available locations, next action and validation command; missing data is explained in the evidence-gap section, not hidden.
- **Test name**: `TestBlockerKeepsMissingRuleReasons` (T02)
  **Scenario**: the blocker-plus-unevaluated fixture.
  **Expected behavior**: conclusion `failure`; the unevaluated rule and its reason appear in reasons and in the evidence-gap section.
- **Test name**: `TestAcceptedDebtIsDisplayOnly`
  **Scenario**: blocked report whose blocking finding has lifecycle `baseline` and whose task origin is `unknown`.
  **Expected behavior**: the finding is listed first as blocking; nothing in the text says it is exempt.
- **Test name**: `TestSuspensionFeedback` (T21)
  **Scenario**: suspended outcome with a report; entitlement with `WarnFrom` in the past and not suspended; `WarnFrom` in the future.
  **Expected behavior**: subscription reason with `Entitlement.Reason` as next action and no findings; the notice line appears; no notice.
- **Test name**: `TestPendingThenFinal` (T13)
  **Scenario**: `Pending`, then `Publish` of a failure, in required mode.
  **Expected behavior**: the same check run goes from `in_progress` to `completed`/`failure`; no second gate check exists.

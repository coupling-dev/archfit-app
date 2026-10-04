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
  input digests and measurement identity; per-dimension completeness and the customer-attested trust statement;
  validation command replay;
  a marker-delimited JSON block of agent tasks that a coding agent on the PR can act on.
- Name the blocking findings in the `archfit/gate` summary, so the required check says what blocks.
- Annotate live findings on the files the pull request changed, at most 50 per request.
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
- Annotation selection (live findings, files the PR changed), the caps and the batching.
- Which coverage and producer rows are evidence gaps, which are folded into one line, and which repeat each other.

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
    LabelsDigest         string     `json:"labels_digest"`
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

`archfit/state` conclusion:

| `Outcome.Conclusion` | `Report.Verdict`   | `archfit/state`                          |
| -------------------- | ------------------ | ---------------------------------------- |
| `action_required`    | any, or no report  | `action_required`                        |
| `success`            | `healthy`          | `success`                                |
| `success`            | `needs_attention`  | `neutral` (reason `attention_only`)      |
| `failure`            | any                | `neutral` **[default]**                  |

The state check fails closed like the gate: an outcome that asks for action (an error, missing, unverifiable or
ineligible evidence, governance, subscription) is `action_required` there too, never `neutral` or `skipped`, so a
ruleset that requires the state check by mistake cannot be passed by bad evidence. A `needs_attention` report that
passes the gate is `success` on `archfit/gate` and `neutral` on `archfit/state`, which keeps the attention visible;
architecture findings are `neutral` there because this check never enforces them. The state check is never
required, so `neutral` is allowed here and only here. It is a string literal in this package and never a
`Conclusion` value.

### Publish algorithm

Order: gate check, the changed-file list (pull requests with a possible annotation only), state check, comment
**[default: the required check first]**. The file list only filters annotations, so it is read after the gate check
is written: a failure to read it never leaves the required check pending. On any `ChangedFiles` error the state check
is written without annotations and the comment as usual; the call then returns `api.ErrRateLimited` when that was the
error, so the Action retries, and `nil` after logging for any other error. Any other error ends the call; a retry
repeats the whole call safely.

For each check to write:

1. `Checks.CheckRuns(repo, Target.HeadSHA, name)`; take the run with the highest ID **[default]**.
2. None → create a run (below).
3. Existing receipt has a higher `(RunID, RunAttempt)` pair than `p.Receipt` → write nothing for this check. An
   `in_progress` run, or one whose receipt marker is absent or unreadable, is never newer.
4. `archfit/gate` → `UpdateCheckRun` in place, the pending run included. The gate check carries no annotation.
5. `archfit/state` with the same `external_id` and `AnnotationCount` equal to the number of annotations this
   publication would send → `UpdateCheckRun` in place without annotations: the run already holds them.
6. Otherwise → create a new `archfit/state` run. GitHub appends the annotations of every request and never removes
   one, so an update in place could only add annotations to stale ones: annotations go only on a run this call
   creates. A new attempt, a new report on the same head, a sweep publication without a report over a run with
   annotations, and a retry after a failed file list each get a new state run; the newest is the one GitHub shows.

Every create and update sets `Status = "completed"`, `ExternalID`, `StartedAt = now()` **[default: the
check's `StartedAt` is the time of the last publication; the sweep relies on it]**, title and summary. A created
`archfit/state` run carries up to 50 annotations; more follow in further `UpdateCheckRun` calls on that run, 50 each,
with the same fields (see Annotations below). Before each further batch `CheckRuns` is read again; when the newest run
is not the created one, or its receipt is newer than `p.Receipt`, the remaining batches are dropped, so an older
publication never overwrites a newer summary.

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

Comment, in order. Check summaries use the same blocks: gate = 1, 2, G, 8, receipt marker; state = 1, 2, 5, 6, 7, 8,
receipt marker. Block G is in the gate summary only.

1. Markers, then the verdict line: "Passed", "Blocked by architecture findings", or "Action required" **[default]**,
   then `Reasons[0]`: code, category, `Detail`, `Next`.
2. Further reasons, one line each (for example unevaluated rules beside a blocker).

   G. Blocking findings (gate summary only, and only when `Outcome.Reasons` holds `blocking_findings`: an outcome
   that blocks on evidence, governance or subscription alone names no finding as blocking), so the required check
   names what blocks: every finding for which
   `Finding.Active()` is true, in report order, one line each: rule ID, `Edge.From.Module` → `Edge.To.Module`
   ("(no module)" when empty), the first location as `file:line` (the file alone for line 0; "no location reported"
   when `locations: []`), the first 8 characters of the finding ID (the prefix the engine prints), and the `Goal` of
   the first agent task with that `FindingID` ("none reported" without one). At most 20, then "and N more blocking
   findings" **[default]**. When no finding is active (a hard gate that failed without a finding, for example a
   metric ratchet), the block is the line "No active gate finding is listed…" instead. Without `blocking_findings`,
   without a report, or when suspended, it is omitted. Display only: the conclusion still comes from `Outcome`.
3. `Trusted.Entitlement.Notice`, when `WarnFrom` is set, `now()` is at or past it, and the notice is not empty.
4. Waiver notice section (owned by `Warn`).
5. Findings: blocking first. A finding is shown as blocking when `Finding.Active()` of `wire/report` is true
   (that package owns the status vocabulary; feedback has no formula of its own) **[default, display only: the
   verdict never reads findings]**. Groups by `Status`: "Introduced"
   (`new`, `expired_waiver`), "Accepted debt — display only" (`baseline`, `waived`), "Fixed" (`fixed`). Per finding:
   rule ID, severity, `Edge.From.Module` → `Edge.To.Module` (with `Path` when set), locations as `file:line`, `Why`,
   then the policy-permitted repair: `Constraint` and `AllowedAlternatives`. feedback adds no repair of its own and
   invents no access route. At most 50 per group, then "and N more" **[default]**. Grouping never changes the verdict.
6. Dimensions: the nine engine keys of `Report.Dimensions` in the engine's fixed order (`intent`, `structure`,
   `modularity`, `coupling`, `change_locality`, `complexity`, `testability`, `operations`, `drift`). Per dimension:
   completeness = `Status` (`measured`, `partial`, `unmeasured`) with `Coverage` as "observed/total (basis)",
   `Confidence`, `Gate`, and the change from `Delta` (status, metric changes, counts of new and resolved findings)
   when present. Measurement trust is not an engine field: the constant "customer-attested" is printed once for the
   block. A missing key is printed as "not reported"; keys outside the nine cannot pass the decoder.
7. Evidence gaps, always present: `Decision.UnevaluatedRequiredRules` with their reasons (a dead selector shows its
   engine reason, `selector matches nothing: <side> <glob>`, here); `Coverage.Tools` entries whose `Status` is not
   `ok`, except that the rows that did not run, `disabled` ones and `absent` ones without a `Reason`, are folded into
   the one line "Analyzers that did not run (absent or disabled): …" naming each tool in report order; every
   `Dimension.Unknown` fact with its reason; the measurement profile's `Unknowns`; and producers whose `Status` is not
   `ok`, except a producer whose tool and status repeat a coverage row. The fold drops noise, not facts: an `absent`
   row with a reason is a full line, and what a missing analyzer leaves unmeasured stays visible as dimension
   unknowns and unevaluated rules. The App cannot tell from a row alone whether the language is present (an absent
   tool over a present language and a language that is not there look the same), so the line claims neither.
   When the protected ref has no baseline (`Trusted.BaselineDigests` empty):
   the line "No baseline: every current violation blocks." When a baseline exists and the gate reference is not
   comparable, by the rule of `gate/verdict` step 7a (a nil `GateReference`, or any `Status` other than
   `comparable`, `not_requested` included): one line per entry of its `Reasons`, "no reason given" when it has none,
   and for a nil reference the line "Baseline comparison: the report has no gate reference". The note therefore
   appears exactly when verdict reports `baseline_not_comparable`. "None reported" when empty; "No usable report"
   when `Report == nil`.
8. Inputs and identity: `Receipt.PolicyDigest`, `Receipt.BaselineDigest` (or "no baseline"), `Receipt.LabelsDigest`
   (or "no labels file"), all bare hex; the baseline and labels digests carry their source (table below); measurement profile
   version and settings hash (or "missing"), the claimed producer identity from `Publication.Identity` as
   "engine <version>, image <digest>, <platform>" (or "no upload" when it is zero; labeled "claimed", because
   only the manifest check in `gate` judges it), `ApprovedBy` when approval was granted, and the fixed sentence
   "Measurement is customer-attested: the producer is authenticated, the report content is not independently checked."
9. Validation commands: the union of `AgentTasks[].Validation`, de-duplicated in first-seen order, each printed
   verbatim as one shell line in a code block. They are engine-authored shell strings; feedback neither builds nor edits
   them. The decoder guarantees none contains `--refresh`. No task → the block is omitted.
10. Agent task block: a JSON array with exactly the keys `finding_id`, `rule_id`, `repair_kind`, `goal`,
    `constraints`, `files`, `validation`, `origin`, as decoded (`repair_kind` preserved), whole tasks only; tasks that
    do not fit are counted in a line below the block.

Source label of the baseline and labels digests in block 8. The receipt does not carry the source; it follows from
fields the publication already has, by the receipt rule of `gate` (the claim when `inputs.Accepts` takes it, else the
first trusted digest) and of `gate/sweep` (always the first trusted digest):

| Publication                                                                  | Label                               |
| ---------------------------------------------------------------------------- | ----------------------------------- |
| `Identity` is zero (no upload: a sweep publication or a fork refusal)        | "from the protected ref; no upload" |
| an upload, and `Outcome.Reasons` holds the mismatch code of that digest      | "from the protected ref"            |
| an upload without that code (the envelope's claim was accepted and is shown) | "stated by the producer"            |

The mismatch codes are `baseline_mismatch` and `labels_mismatch`. "stated by the producer" is never printed for a
digest the App wrote itself. An upload always has an identity: the envelope decoder requires every identity field.

When `Reasons[0].Category` is `subscription`, blocks G, 5 to 7, 9 and 10 are omitted **[default]**: paid output is suspended, the reason and recovery stay.

Annotations: only on `archfit/state`; from `Finding.Locations{File, Line}` only; live findings only (accepted debt,
`baseline` and `waived`, and `fixed` findings get none; a status this version does not know is live); findings shown
as blocking first, then report order; level `failure` for those, `warning` otherwise; message = rule ID and `Why`;
`Line == 0` is sent as line 1. A finding with `locations: []` gets no annotation. On a pull request only locations in
files the PR changed are annotated: `Publish` calls `Pulls.ChangedFiles` once, after the gate check and before the
state check, and only when some annotation is possible; a `truncated` list means any file may have changed, so every
location counts; on its error the state check gets no annotation (see Publish algorithm). A push annotates every
location. At most 150 annotations in total **[default]**, sent 50 per request (GitHub's limit): the create of the
state run carries the first 50, and further updates of the same run carry the rest, each with the same title and
summary. GitHub appends the annotations of each request, so annotations are only ever sent to a run this
publication created.

Size: summaries and the comment stay at or under 65 535 bytes. The receipt marker and blocks 1, 2, 7, 8 are
budgeted first and never cut. Of the rest, the budget is handed out in this order: block 6, block 9, block 5, block
10. Block G takes the place of block 5 in the gate summary and is cut the same way. Block 6 is cut at a line end, like block 5, when the never-cut blocks leave it too little room; block 9 takes
at most its own budget (below) before block 5 gets anything, so a long finding list cannot push the validation
commands out. Then block 5 is cut at a line end and ends with "Output truncated.", and block 10 shrinks by whole
tasks into what is left, so tasks give way first and findings second. Cuts never split a UTF-8 sequence or a marker.
Block 9 has its own budget of 4 096 bytes and 20 commands, whole commands only, then "N more commands are not
shown." **[default]**; a command that contains `<!--`, `-->` or a code fence is counted, not printed. The comment
budget keeps room for the waiver notice section, so `Warn` never needs a size check. Display caps, each ending with
"and N more" **[default]**: 50 further reasons, 50 evidence gaps per source, 10 locations per finding, 10 metric
changes per dimension. Last resort, only when the never-cut blocks alone exceed the limit: block 7 is cut at a
line end.

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
    LabelsDigests        []string // same rule; empty = no labels file on the protected ref
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
  A report has no exit code, no `needs_attention` flag, no per-dimension trust and no single validation command.
  `Seam` is an opaque part of `Report` for this module and is not rendered. `Metric` values are shown only through `Delta`. `StateSchemaV1` is the schema constant the decoder enforced.
  `Finding.Active()` is the single definition of "active gate finding"; feedback calls it for the "blocking" label,
  the group order, block G and the annotation level. For display feedback also reads the finding statuses
  `baseline`, `waived` and `fixed` (debt and fixed groups; no annotation) and the coverage statuses `ok`, `absent` and
  `disabled` (block 7), as named constants in `render.go`; `wire/report` owns both vocabularies, and a value outside
  them is shown, never hidden. `AgentTask.FindingID` and `Goal` name the repair goal in block G.

<!-- contract: Active, Finding — restated from internal/wire/report/module.md -->
```go
// Active reports whether the finding currently blocks: a gate finding that is new or whose waiver expired.
// It is the single definition of "active gate finding" for every consumer.
func (f Finding) Active() bool
```

<!-- contract: Report, StateSchemaV1, MaxBytes — restated from internal/wire/report/module.md -->
```go
// StateSchemaV1 is the engine state contract accepted (engine v2.3.1, archfit.state.schema.json).
const StateSchemaV1 = "archfit.architecture-state.v1"

// MaxBytes caps an uploaded payload: 5 MiB (the "5 MB" cap of decisions.md).
const MaxBytes = 5 << 20

// Report is the no-source transport form of an engine state report. The decoder knows every key of the
// v2.3.1 state schema: keys listed here are kept (length-capped), the other known keys are validated and
// dropped (measurement, findings[].matched_by, agent_tasks[].declarations, metric provenance, seam scores),
// and any key the schema does not define is a decode error.
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

<!-- contract: Decision, UnevaluatedRule — restated from internal/wire/report/module.md -->
```go
// Decision is the engine's structured gate decision. The process exit code is not part of a report:
// healthy, needs_attention and blocked are Report.Verdict; an engine error produces no report at all.
type Decision struct {
    HardGates                string            `json:"hard_gates"` // "pass" | "fail" | "unmeasured"
    UnevaluatedRequiredRules []UnevaluatedRule `json:"unevaluated_required_rules,omitempty"`
    ActiveBlockers           int               `json:"active_blockers"`
    AttentionDimensions      int               `json:"attention_dimensions"`
    UnknownDimensions        int               `json:"unknown_dimensions"`
}

type UnevaluatedRule struct {
    RuleID string `json:"rule_id"`
    Reason string `json:"reason"`
}
```

<!-- contract: Comparison, Reference, MeasurementProfile, Producer — restated from internal/wire/report/module.md -->
```go
// Comparison binds the report to the inputs the engine actually used (report-only --base comparison).
type Comparison struct {
    Status             string              `json:"status"` // "not_requested" | "comparable" | "non_comparable"
    BaseRef            string              `json:"base_ref,omitempty"`
    ConfigHash         string              `json:"config_hash,omitempty"` // bare lowercase hex SHA-256 of the raw policy bytes
    ModelHash          string              `json:"model_hash,omitempty"`
    LabelsHash         string              `json:"labels_hash,omitempty"` // canonical hash of approved labels; not reproducible from raw bytes
    RubricVersion      string              `json:"rubric_version,omitempty"`
    MeasurementProfile *MeasurementProfile `json:"measurement_profile,omitempty"` // nil = unknown identity
    Reasons            []string            `json:"reasons"`
    TaskOriginStatus   string              `json:"task_origin_status,omitempty"`
    TaskOriginReasons  []string            `json:"task_origin_reasons,omitempty"`
}

// Reference is the engine gate's comparison with the persisted baseline (top-level gate_reference).
// The engine exposes no baseline digest: only whether the baseline it loaded was comparable, and why not.
type Reference struct {
    Status  string   `json:"status"`             // "comparable" | "non_comparable" | "not_requested"
    BaseRef string   `json:"base_ref,omitempty"` // "baseline"
    Reasons []string `json:"reasons"`            // e.g. "no baseline file was loaded"
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

<!-- contract: Finding, Edge, Endpoint, Location, AgentTask — restated from internal/wire/report/module.md -->
```go
type Finding struct {
    ID                  string     `json:"id"`        // 32-hex fingerprint, unique per report
    Kind                string     `json:"kind"`      // "gate" | "advisory"
    RuleID              string     `json:"rule_id"`
    Status              string     `json:"status"`    // "new" | "baseline" | "waived" | "expired_waiver" | "fixed"
    Severity            string     `json:"severity"`  // "critical" | "high" | "medium" | "low"
    Confidence          string     `json:"confidence"`
    Edge                Edge       `json:"edge"`
    Locations           []Location `json:"locations"` // may be empty; never invent one
    Why                 string     `json:"why"`        // engine-authored, single line, length-capped
    Constraint          string     `json:"constraint"` // engine-authored, single line, length-capped
    AllowedAlternatives []string   `json:"allowed_alternatives,omitempty"`
}

type Edge struct {
    From Endpoint `json:"from"`
    To   Endpoint `json:"to"`
    Kind string   `json:"kind"`
}

type Endpoint struct {
    Module string `json:"module"` // may be ""
    Path   string `json:"path"`   // may be ""
}

type Location struct {
    File string `json:"file"`
    Line int    `json:"line"` // 0 = whole file
}

type AgentTask struct {
    FindingID   string   `json:"finding_id"`
    RuleID      string   `json:"rule_id"`
    RepairKind  string   `json:"repair_kind"` // "code_change" | "needs_owner_decision"
    Goal        string   `json:"goal"`
    Constraints []string `json:"constraints"`
    Files       []string `json:"files"`
    Validation  []string `json:"validation"`       // shell command strings, replayed verbatim; never contain --refresh
    Origin      string   `json:"origin,omitempty"` // "introduced" | "pre_existing" | "unknown"; presentation only
}
```

<!-- contract: Dimension, DimensionCoverage, Metric, FindingRef, UnknownFact, Delta, MetricDelta, Coverage, ToolCoverage, Seam — restated from internal/wire/report/module.md -->
```go
// Dimension mirrors the engine's per-dimension state. Completeness is Status; measurement trust is not an
// engine field: in v1 every fact is customer-attested and feedback says so.
type Dimension struct {
    Name       string            `json:"name"`
    Owner      string            `json:"owner"`
    Status     string            `json:"status"` // "measured" | "partial" | "unmeasured"
    Confidence string            `json:"confidence"`
    Gate       string            `json:"gate"` // "pass" | "warn" | "fail" | "not_applicable"
    Coverage   DimensionCoverage `json:"coverage"`
    Metrics    []Metric          `json:"metrics"`
    Findings   []FindingRef      `json:"findings"`
    Unknown    []UnknownFact     `json:"unknown"`
    Delta      *Delta            `json:"delta,omitempty"`
}

type DimensionCoverage struct {
    Basis    string `json:"basis"`
    Observed int    `json:"observed"`
    Total    int    `json:"total"`
}

type Metric struct {
    Name  string  `json:"name"`
    Value float64 `json:"value"`
    Unit  string  `json:"unit"`
}

type FindingRef struct {
    ID       string `json:"id"`
    RuleID   string `json:"rule_id"`
    Kind     string `json:"kind"`
    Severity string `json:"severity"`
    Status   string `json:"status"`
}

type UnknownFact struct {
    Fact   string `json:"fact"`
    Reason string `json:"reason"`
    Owner  string `json:"owner"`
}

type Delta struct {
    Status           string        `json:"status"`
    Reasons          []string      `json:"reasons,omitempty"`
    Metrics          []MetricDelta `json:"metrics,omitempty"`
    NewFindings      []string      `json:"new_findings,omitempty"`
    ResolvedFindings []string      `json:"resolved_findings,omitempty"`
}

type MetricDelta struct {
    Name   string  `json:"name"`
    Before float64 `json:"before"`
    After  float64 `json:"after"`
    Change float64 `json:"change"`
}

type Coverage struct {
    Measured   int            `json:"measured"`
    Partial    int            `json:"partial"`
    Unmeasured int            `json:"unmeasured"`
    Tools      []ToolCoverage `json:"tools"`
}

type ToolCoverage struct {
    Tool   string `json:"tool"`
    Status string `json:"status"`
    Reason string `json:"reason,omitempty"`
}

// Seam is an observed module-to-module relationship. Only these fields are kept.
type Seam struct {
    ID         string `json:"id"`
    FromModule string `json:"from_module"`
    ToModule   string `json:"to_module"`
    Edges      int    `json:"edges"` // observed dependency count
    Strength   string `json:"strength"`
    Distance   string `json:"distance"`
    Volatility string `json:"volatility"`
    Severity   string `json:"severity,omitempty"`
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
- **Shared knowledge**: a `Summary` over 65 535 bytes, or more than 50 `Annotations` in one request, is an error from
  the client, so feedback truncates and batches first; GitHub appends the annotations of each update.
  `AppComments` returns only comments authored by the App's bot user. `ErrRateLimited` is the sentinel
  returned after bounded backoff; feedback passes it up unchanged. `ErrNotFound` is the sentinel for a
  missing object. `ChangedFiles` lists the old and the new path of a rename and sets `truncated` when GitHub cut the
  list off. Used methods: all of `Checks` and `Comments`, `Pulls.Pull` and `Pulls.ChangedFiles`. `Review` is not used here.

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
    // AnnotationCount is read-only: the annotations GitHub holds for the run, set by CheckRuns.
    // Create and update never send it; GitHub appends the Annotations of each request.
    AnnotationCount int
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
  feedback never downgrades, upgrades or skips it. `neutral` is written only to `archfit/state`, and never for an
  outcome that asks for action: that is `action_required` on both checks.
- In advisory mode no `archfit/gate` check is written. A ruleset that requires it then stays blocked: closed, never green.
- An older `(RunID, RunAttempt)` never overwrites a newer one on the same head; a publication for an old
  head never touches the comment. Check runs are per SHA, so an old head's checks cannot affect the new head.
- Exactly one comment per PR under sequential delivery. Two concurrent first publications may create two;
  later calls update only the lowest-ID one **[default, stated limit]**. The same limit holds for check runs (highest ID wins).
- Annotations are sent only to an `archfit/state` run the same call created, because GitHub appends the annotations
  of every update. A repeated delivery updates the state run in place only when it already holds the publication's
  annotations (same `external_id`, equal `AnnotationCount`); a new attempt, a new report, a sweep publication over an
  annotated run, or a retry after a missing file list creates a new state run. Old state runs stay on the head SHA;
  GitHub shows the newest. `State` and the sweep read only `archfit/gate`, which is still updated in place, so the
  state run identity does not affect them. Stated limits **[default]**: an equal annotation count stands in for equal
  annotations; and a newer publication that lands between the re-read and a further annotation batch of an older one
  can still see that batch appended to the older run, never to its own.
- Never print "verified", a score, a grade or a percentage of health. Trust is "customer-attested".
  Provenance is necessary, not sufficient.
- Never invent a location, a repair route or independent evidence. No annotation without a `Location`.
- Declared restatement: the nine dimension names and their order (block 6) are a literal list in `render.go`. The
  owner is `wire/report`, whose decoder accepts exactly these keys and exports no list of them. If the two drift, a
  new engine dimension decodes but is not printed, and a removed one prints "not reported". Trigger to change: a new
  engine dimension; remove the list when `wire/report` exports the keys in order.
- Task origin and finding status are presentation only; grouping never hides a blocking finding. The "blocking" label
  is `Finding.Active()` of `wire/report`, a display heuristic here; the conclusion always comes from `Outcome`.
- Engine-authored strings are sanitized before rendering: `<!--`, `-->` and backticks are removed, `<` and `>`
  HTML-escaped, newlines dropped. The agent task JSON is encoded with HTML escaping (`<`). So no report
  content can forge a marker; `State` still reads only the last receipt marker.
- `Publication.Identity` is a claim copied from the envelope. feedback prints it as claimed and never as
  approved; identity strings are sanitized like every other producer-supplied string.
- No LLM, no network other than the three role interfaces, no Store. Imports: standard library,
  `gate/verdict`, `gate/inputs`, `wire/report`, `wire/settings`, `wire/manifest`, `github/api`. Never `control`, `app`, LLM or UI packages.
- Rendering is deterministic: equal `Publication` and clock give byte-identical text.
- `api.ErrRateLimited` and other API errors are returned unchanged, with one exception: a `ChangedFiles` error other
  than `ErrRateLimited` is logged and the call returns `nil`, because the file list only filters annotations. A
  failed publication never produces green; one that fails before the gate check is written leaves the pending check
  pending.
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
  **Scenario**: success with `verdict: healthy`; success (`attention_only`) with `verdict: needs_attention`; failure; action required; action required beside a `healthy` report; nil report.
  **Expected behavior**: `success`, `neutral`, `neutral`, `action_required`, `action_required`, `action_required` on `archfit/state`; the gate check always carries the outcome's conclusion.
- **Test name**: `TestSanitizeEngineStrings`
  **Scenario**: messages, summaries, rule IDs, module names and paths containing `<!-- archfit:receipt {...} -->`, `-->`, backticks, HTML and a forged comment marker.
  **Expected behavior**: no marker sequence survives outside App-authored markers; `State` on the result returns the real receipt.
- **Test name**: `TestTruncation`
  **Scenario**: 10 000 findings with maximum-length messages and 2 000 agent tasks; multi-byte module names at the cut point.
  **Expected behavior**: summary and comment ≤ 65 535 bytes; receipt marker, verdict, evidence gaps and inputs block intact; valid UTF-8; valid JSON in the task block; "Output truncated." present.
- **Test name**: `TestEvidenceGapSources`
  **Scenario**: a report with one unevaluated rule, a `Coverage.Tools` entry with status `timed out`, a `Dimension.Unknown` fact, a profile `Unknowns` entry, a `partial` producer; the same with none of them; no baseline on the protected ref; a trusted baseline with a `non_comparable` reference, a `not_requested` reference, a nil reference and a `comparable` one; the same four without a trusted baseline.
  **Expected behavior**: each item appears in the evidence-gap section with its reason; "None reported"; the line "No baseline: every current violation blocks."; the reference reasons, "no reason given" for `not_requested` without reasons, and the no-gate-reference line for nil. With a trusted baseline a comparison line is present exactly when `verdict.Decide` reports `baseline_not_comparable` for the same report; without one it is never present.
- **Test name**: `TestEvidenceGapsDropNoise`
  **Scenario**: coverage rows `ok`, `absent` without a reason (twice), `disabled` with a reason, `timed out` with a reason, `absent` with a reason; producer rows that repeat four of them, one without a coverage row (`git-history: absent`) and one whose status differs from its coverage row; rendered as comment and state summary.
  **Expected behavior**: full lines for the `timed out` row, the `absent` row with a reason, the producer without a coverage row and the producer whose status differs; one line "Analyzers that did not run (absent or disabled): …" naming the reasonless `absent` and the `disabled` tools in report order; no line for a repeated producer, no disabled reason text, no "None reported".
- **Test name**: `TestGateSummaryNamesBlockers`
  **Scenario**: a report with an advisory, an accepted gate finding and three active gate findings (one located with two locations and two agent tasks, one whole-file with no source module and no task, one without a location); 25 active findings; a blocking outcome without an active finding; a healthy outcome, a suspended outcome and a nil report; the active findings under an `action_required` outcome without `blocking_findings`, and under one with `blocking_findings` as a later reason.
  **Expected behavior**: block G in the gate summary only, in report order, with rule, module pair, first location, 8-character ID prefix and the first task's goal, "(no module)", "no location reported" and "Goal: none reported" where they apply; never the advisory or the accepted finding; the receipt marker stays last; 20 lines then "and 5 more blocking findings"; the "No active gate finding is listed" line; no block for healthy, suspended, nil report, or an outcome without `blocking_findings`; the block when `blocking_findings` is a later reason.
- **Test name**: `TestDigestSourceLabels`
  **Scenario**: an upload whose claims were accepted; an upload with `baseline_mismatch`; one with `labels_mismatch`; the zero `Identity` (a `sweep.Without` publication); no baseline and no labels file; rendered as comment, gate summary and state summary.
  **Expected behavior**: "stated by the producer" on both lines; "from the protected ref" on the mismatched line only; "from the protected ref; no upload" on both lines; "no baseline" and "no labels file" without a label. "stated by the producer" never appears without an upload.
- **Test name**: `TestIdentityLine`
  **Scenario**: a full `Identity`; the zero `Identity` (sweep publication); an identity whose fields contain marker text.
  **Expected behavior**: "claimed: engine v2.3.1, image sha256:…, linux/amd64"; "no upload"; sanitized text; never the word "approved" or "verified".
- **Test name**: `TestAnnotationSelection`
  **Scenario**: findings with no, one and many locations, `Line == 0`, live `advisory` and `gate` findings, `baseline`, `waived` and `fixed` findings, and a finding in a file the PR did not change; published on a pull request whose file list names four files, with a truncated list, on a push, with 200 live locations, and with no live located finding. The fake rejects more than 50 annotations in one request, as the client does.
  **Expected behavior**: on the PR only the changed files: whole-file first as `failure` on line 1, then the blocking findings, then the advisory in report order as `warning`; no annotation for `locations: []`, accepted debt, fixed findings or the unchanged file; requests of 50 then 21 on the state check (none on the gate check); one file-list call. The truncated list and the push annotate every file, and the push lists none. 200 locations give 150 annotations, blocking first, in requests of 50. Without a possible annotation the file list is not read.

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
  **Expected behavior**: golden bodies match; verdict, reason and next action come first; the evidence-gap section exists in each; `repair_kind` values appear unchanged in the JSON block, whose objects have exactly the eight task keys; validation commands from `AgentTasks[].Validation` are de-duplicated and printed verbatim with `--base`, `--lang`, `--require-tools` when present and never `--refresh`; dimensions appear in the engine's fixed order of nine.

### Boundary Tests

- **Test name**: `TestNoVerifiedNoScore` (T11)
  **Scenario**: scan every golden body and every title for App-authored text.
  **Expected behavior**: no "verified" (any case), no "score", no grade; "customer-attested" present whenever a report is rendered.
- **Test name**: `TestGateCheckNeverNeutral` (T13)
  **Scenario**: publish an outcome for every reason code, with and without a report, in required mode.
  **Expected behavior**: `archfit/gate` conclusion is one of `success`, `failure`, `action_required`, equal to the outcome; `archfit/state` is `action_required` whenever the outcome is.
- **Test name**: `TestRateLimitLeavesPendingAlone`
  **Scenario**: `ErrRateLimited` from `CheckRuns`, from `ChangedFiles`, from `UpdateCheckRun`, from `CreateComment`.
  **Expected behavior**: the error is returned with `errors.Is` intact; the pending check is unchanged when the error comes before the gate check is written (`CheckRuns`, `UpdateCheckRun`); no failed publication produces green; a retry completes the publication with one gate check and one comment.
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
  **Scenario**: the same publication, with two annotated locations in a changed file, five times; then the same run with a new attempt. The fake `Checks` appends the annotations of every update and lists only their count, as GitHub does.
  **Expected behavior**: one comment and one check run per name, the state run holding exactly two annotations; identical bodies cause no comment update call. The new attempt updates the gate run and the comment once and creates a second state run, which holds exactly two annotations.
- **Test name**: `TestRepublishWithoutReportDropsAnnotations`
  **Scenario**: an annotated publication, then twice a sweep-style publication with the same run identity, no report and `stale_inputs`.
  **Expected behavior**: one gate run updated to `action_required`; a second state run, `action_required`, with no annotation; the repeat writes no third state run.
- **Test name**: `TestNewerPublicationStopsAnnotationBatches`
  **Scenario**: a push publication with 121 annotations; right after its state run is created, a newer run (run 8) is stored on the head.
  **Expected behavior**: one request of 50 annotations and no update; the newer run's summary and annotations are unchanged.
- **Test name**: `TestChangedFilesErrorStillPublishes`
  **Scenario**: in required mode with a pending gate check, `ChangedFiles` fails once with `ErrRateLimited`, and once with another error; then a retry.
  **Expected behavior**: the gate check is completed as `failure`, one state run without annotations and the comment are written; `ErrRateLimited` is returned, the other error is not. The retry adds a state run with the two annotations and no second gate check or comment.
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
  **Scenario**: blocked report (`hard_gates: fail`) whose only `gate` finding has status `baseline` and whose task origin is `unknown`, on a PR that changed the finding's file.
  **Expected behavior**: conclusion `failure` from the outcome; the finding is listed under accepted debt without any text that says it is exempt or that the PR is clean; the reason block still says blocking findings exist, and block G says no active gate finding is listed; the accepted finding gets no annotation, the live advisory on the same line one `warning`.
- **Test name**: `TestSuspensionFeedback` (T21)
  **Scenario**: suspended outcome with a report; entitlement with `WarnFrom` in the past and not suspended; `WarnFrom` in the future.
  **Expected behavior**: subscription reason with `Entitlement.Reason` as next action and no findings; the notice line appears; no notice.
- **Test name**: `TestPendingThenFinal` (T13)
  **Scenario**: `Pending`, then `Publish` of a failure, in required mode.
  **Expected behavior**: the same check run goes from `in_progress` to `completed`/`failure`; no second gate check exists.

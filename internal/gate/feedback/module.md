# Gate Feedback

**Path**: internal/gate/feedback/
**Parent**: internal/gate/ (gate façade)
**Submodules**: none (leaf)

## Purpose

`feedback` is the only writer of what a developer sees: the `archfit/gate` and `archfit/state` check runs
and the single PR comment. It also makes the gate check its own receipt, so that GitHub holds the
idempotency key and the facts a later sweep compares. Without it, retries would spam comments, an old run
could paint over a newer head, and the App would need a database to remember what it said. The only thing it
keeps in the Store is a short-lived lease per head SHA, because GitHub has no conditional check-run write.

## Functional Responsibilities

- Publish one `Publication`: `archfit/state` always, `archfit/gate` only in required mode, the comment for pull requests.
- Create the pending (`in_progress`) gate check for a new head.
- Read back the gate check of a head SHA as `CheckState`, including the parsed `Receipt`.
- Add or refresh the waiver-expiry notice in the comment.
- Render the comment's first screen for a person: the assessment with what the App checked it with (the approved
  engine, the accepted policy digest; never the report's own identity claim);
  at most three findings led by place and rule, an active one with its why and fix (300 characters each), an advisory
  without its coupling why; the owner's repository attention (the engine's gate reasons, at most ten in the comment and fifty in the
  archfit/state check) in its own fold; the result identity, labelled as the engine's statement, first in the fold for
  coding agents. No table wider than four columns.
- Render the policy change card (`policyCard`) in the comment and the `archfit/state` check when the publication
  carries an engine classification of a change to the protected policy: the direction, the changes in plain words
  (each value cut to 80 characters, the list bounded to about 3 KB, then a count: the card sits in a block that is never
  cut and the author controls the values), and the approval rule; a `policy_authority` rung held at human says so.
  Display only. The classification is the Action's, carried by the upload (engine v3.4.0 or later) and passed on by `gate`
  only when its own trusted inputs say the pull request changes the policy file; a sweep publication has no envelope and
  carries no card. No card means "not classified", never "no change".
- Render feedback that is useful without an LLM: the verdict, one sentence of what the pull request changed, the
  picture of the dependency cycle, then the next action; findings with rule, modules and locations; the balancing
  move of each advisory in the words of Balanced Coupling; active versus accepted debt; dimensions; a mandatory evidence-gap section;
  input digests and measurement identity; per-dimension completeness and the customer-attested trust statement;
  validation command replay;
  a marker-delimited JSON block of agent tasks that a coding agent on the PR can act on.
- Name the blocking findings in the `archfit/gate` summary, so the required check says what blocks.
- Annotate live findings on the files the pull request changed, at most 50 per request.
- Keep every write idempotent and never let an older run or head overwrite newer feedback.
- Serialize every write to one head SHA across all instances with a publication lease in the Store.
- Stay under GitHub size limits with safe truncation.

## Subdomain Classification

Core. Feedback quality is what makes the gate acceptable to developers and agents. Volatility: high;
wording, layout and limits will be tuned with every pilot.

## Encapsulated Knowledge

- Check names, titles, the `external_id` format, and the hidden marker formats.
- The comment and summary layout, truncation order and size budgets.
- The idempotency and ordering rules (run ordering, head ordering).
- The publication lease: its key, value, lifetime, work deadline and polling.
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
    IdentityKey          string     `json:"identity_key,omitempty"` // the report's result identity, hashed; "" without one
    BaseSHA              string     `json:"base_sha,omitempty"`     // the default-branch commit the report was measured against; "" for a push or an old receipt
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

```go
// New returns a Publisher that is safe for concurrent use, also across instances that share s: Publish, Pending,
// Warn and Reconcile hold a per-head publication lease in s, so that two publishers for one head SHA never interleave
// their reads and writes. It keeps no other state between calls; GitHub holds the receipts.
// appURL is the App's base URL without a trailing slash (ARCHFIT_BASE_URL); the PR comment links to the App's pull
// request page under it. "" writes no link.
func New(checks Checks, comments Comments, pulls Pulls, s Store, appURL string, now func() time.Time) Publisher
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

### Publication lease

GitHub has no conditional write for check runs: two publishers that both read a check before either writes can
each act on a stale read, and the older one can then replace a newer `failure` with `success`. So `Publish`,
`Pending`, `Warn` and `Reconcile` run inside a lease in the Store, and every read, supersedes check and write of the
steps above happens while it is held. `State` only reads and takes no lease.

- Slot: tenant `Tenant(Target.Repo.InstallationID)`, key `feedback/lease/<Target.HeadSHA>`. The repository is not
  part of the key **[default]**: webhook targets carry no repository ID and a full name changes on rename, so a
  repository part could give two publishers of one head two leases. Two repositories of one installation that share
  a commit share a lease too; they wait for each other, which costs time and never correctness. A target without
  an installation ID (`<= 0`) or a head SHA is an error.
- Value: JSON `{"token", "expires"}`. The token is random per call; `expires` is `now() + 60 s` **[default]**.
- Acquire: `Get`; missing, expired (`now() >= expires`) or unreadable → `CompareAndSwap` from the bytes read
  (`nil` when missing) to the new value; held → wait 200 ms **[default]** and read again until the context ends.
  A lost swap reads again. Only this package writes the key, so an unreadable value is taken over rather than
  waited on forever.
- Work: the call runs under a context that ends 50 s after acquisition **[default: lifetime minus a 10 s margin]**,
  measured by the process's own elapsed time, so the holder stops writing before any other instance may take an
  expired lease over. The margin covers clock skew between instances and a request already sent when the call
  ended: one cut by the work deadline, by the API client's timeout, or answered with a 5xx, which GitHub may still
  apply. That holds because a failed call keeps its lease (next item).
- Release: only after a call that returned `nil` or failed before its first GitHub request (`ErrStaleInputs`, a
  failed `Publication.Resolve`). Every other error keeps the lease until it expires, 60 s after acquisition, so a
  write in doubt lands before a waiting publisher reads the check **[default]**. The cost is that the next
  publisher of that head waits up to 60 s; an upload whose 50 s deadline ends first answers 503 and the Action
  retries, and a retry after `Retry-After: 60` meets an expired lease.
  Release is `CompareAndSwap` from this call's own bytes to `nil`, under a fresh 5 s context **[default]** detached
  from the request, so it works after the request context ended. A lease another holder took over is never deleted.
  A failed release is logged; the lease then expires.
- Fail closed: any Store error while acquiring returns the error and writes nothing to GitHub. An unserialized
  write could reopen the overwrite, so a Store outage holds new verdicts back instead (`gate` answers 503 and the
  check stays as it was).

Within the lease, every write is still preceded by its own read: `writeCheck` reads the latest run and checks
supersession right before it creates or updates, a further annotation batch re-reads before each request, and
the comment re-reads the head marker before it updates. The lease makes those reads current; it does not replace
them, so a publisher that waited for the lease still skips a newer receipt it now sees.

The run identity orders two uploads, but not an upload and a sweep correction: the sweep keeps the receipt's
`(RunID, RunAttempt)`, so the equal identity counts as replaceable. An upload decided from policy P1 that waits for
the lease while the sweep, already reading P2, writes `stale_inputs` would then replace the correction with its P1
`success`. So, inside the lease and before its first GitHub request, `Publish` calls `Publication.Resolve` when it is
set and checks two things against the result. It must equal `Publication.Trusted` in every field, so that a change
the digests do not show (an owner approval dismissed, settings edited, a subscription suspended) also makes the
decision stale; whole-value equality covers every field `verdict.Decide`, the receipt and the rendering read, now and
after a field is added. And each receipt digest (`PolicyDigest`, `BaselineDigest`, `LabelsDigest`) must pass
`inputs.Accepts`: the sweep's stale condition, negated. A result that fails either writes nothing and returns
`ErrStaleInputs`; a `Resolve` error writes nothing and is returned wrapped, `errors.Is` intact. Both release the
lease at once. `gate` sets `Resolve` on every upload publication; the sweep's publications carry the inputs they were
just resolved from and are never a success, so they leave it nil.

`Reconcile(ctx, target, decide)` is for a caller whose decision depends on the gate check itself: the withdrawal of
an owner approval (sweep rule 5a and `ReviewChanged` in `gate`), which replaces a receipt that asked for no action,
and the re-run after `owner_approval_missing`. Inside one hold of the lease it calls `State`, passes the result to
`decide`, and publishes the publication `decide` returns (with the `Resolve` check above when it is set). `decide`
resolves the trusted inputs itself, inside the lease, so the read, the decision and the write see no other
publication for the head in between: a newer run that turned green while the caller waited is the receipt it
decides about, and a review that lands after the read is handled by its own event, which waits for this lease and
then reads the written check. A nil publication writes nothing. A `State` or `decide` error writes no check and
releases the lease at once; `decide` may have requested a re-run, which writes no check. Without the lease nothing
is read and `decide` is not called.

### Layout

Presentation is not report admission or a gate decision. `presentation` returns a copy and reads every structured
reason through the verdict-owned code/category classifier shared with historical UI context. Only `healthy`, `attention_only`, `blocking_findings`, `baseline_not_comparable`,
`missing_required_coverage` and `policy_defect` permit report-derived display. A missing report, absent reasons,
any rejection/withdrawal code or an unknown future reason withholds findings, counts, origin, engine verdict,
dimensions, commands, tasks and annotations. Dependent assessment/recovery reasons are also omitted; independent
App-authority recovery diagnostics remain. The original report, reason backing slice, conclusion and receipt do not
change. Incomplete but source-admissible reports may retain qualified current facts; this does not admit them into
the App under `action_required`.

The check prelude remains the parser contract: `- **Next:**`, `- Reason:`, `- Detail:`, then optional
`Also reported:`. Reasons and claimed producer/receipt metadata remain on the check. Repository-controlled finding
text and folds are not recovery metadata. An unevaluated `metric_ratchets` recovery is shared with baseline recovery
only when the exact producer reason, positive metric count, present non-comparable reference and full drift list
match. The required-gap detail remains, but its duplicate Next is omitted. Other required-rule/tool failures survive.

The compact PR comment is text-first; GitHub can notify the same bytes by email:
- Configured merge effect from Outcome and gate mode, never a promise that GitHub will merge.
- Assessment, then comparable Git-base finding-identity impact or an explicit unknown. Introduced identity does not
  prove a new module pair or that each source site was added.
- At most three active findings/advisories: rule, reported relationship or metric, first supplied location and
  engine constraint. Line zero is file-only. Accepted debt details and dimension tables stay in the exact check.
  No Mermaid/image/graph is emitted in the comment.
- Role-aware reported recovery; no invented validation flags or selected cycle direction. Owner-decision tasks
  identify the architecture owner's role. The recovery of `target_moved` (update the branch) is the pull request
  author's: "Pull request author:", never "CI maintainer:".
- Exact HEAD, workflow run/attempt and actual state-check object link when its successful write returned an ID.
  The App link is explicitly the current PR result/recovery diagnostics, not a historical snapshot. A check object can be
  updated; its ID does not promise immutable content. Failed/superseded writes and annotation-batch races supply no ID.
- Compact evidence inventory, the trust sentence, mandatory notices/waiver notice and exact delimited tasks.
  The same comment is the email content; there is no separate email transport/body.

A positive “No required architecture change requested in the evaluated scope” needs a passing required Outcome,
no active/unknown-status violation, all required rules evaluated and a reference consistent with protected inputs.
Accepted debt can remain. A fixed lifecycle entry says “Previously accepted finding no longer observed; resolution
not established”; no historical severity, endpoints, source sites or green removed edge is invented.
Zero tasks retain the exact `[]` array and explain “No automated source-repair task supplied.” Rejected-source tasks
are withheld, not represented as a successful empty repair array.

The detailed `archfit/state` check retains report groups, engine decision, required-rule/tool gaps, nine dimensions,
protected receipt digests, claimed producer/profile, exact validation command union and delimited task JSON for
source-admissible reports. Declared rule counts come from intent coverage with the declared-rule basis. Implicit
accepted-reference metric checks are a separate inventory; no measured count is inferred when the report lacks one.
`archfit/gate` retains the recovery prelude and at most 20 active blockers only when the display Outcome includes
`blocking_findings`. Gate/check conclusions and receipt markers are unchanged.

Tasks keep exactly eight keys: `finding_id`, `rule_id`, `repair_kind`, `goal`, `constraints`, `files`,
`validation`, `origin`. Values and command flags are preserved. JSON HTML/backtick escaping prevents forged
markers/fences; a validation replay block omits unsafe marker/fence commands with a count rather than editing them.

Bodies stay within 65,535 bytes. Head/tail, decision, gaps, inputs and receipt reserve space first. Whole task data
reserves about half of the remaining budget before discretionary dimensions/commands/findings; at least the first
whole task is kept if it fits the available space. Every omission is counted and points to the run's unmodified
report artifact if retained, or an approved rerun if expired. No task or delimiter is split. Other cuts end at a
line boundary, close fences/folds and say “Output truncated.” The comment reserves waiver-notice space.

Annotations remain state-only, capped at 150 in batches of 50. Only source-admissible live findings with supplied
positive line numbers and nonempty files qualify; accepted/fixed findings do not. File-only/nonpositive-line
locations remain in textual detail but get no annotation. PR changed-file filtering, rate-limit behavior, leases
and newer-head/run protection are unchanged.

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
    ReasonTargetMoved         ReasonCode = "target_moved" // the default branch moved and, with this change, closes a module cycle
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
- **Strength**: model (reads `Target` and `Trusted`; calls `Accepts`)
- **LCA / Rank / Distance**: gate / 1 / 1
- **Volatility**: high
- **Balanced?**: yes
- **Shared knowledge**: `Target.PullRequest == 0` is a push; `Trusted.Settings.Gate` selects required mode;
  `Entitlement.Notice` is shown from `WarnFrom` on. feedback never resolves inputs itself: it calls the caller's
  `Publication.Resolve` and checks the receipt digests with `Accepts`, the predicate the sweep's stale rule uses.

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
    Version  int      `yaml:"version"`          // must be 1
    Owners   []string `yaml:"owners"`           // GitHub logins, or "org/team-slug"; at least one
    Agents   []string `yaml:"agents,omitempty"` // logins or App slugs of coding agents, which never approve; nil when none
    Gate     GateMode `yaml:"gate"`             // default advisory
    Workflow string   `yaml:"workflow"`         // e.g. ".github/workflows/archfit.yaml"
    DataMode DataMode `yaml:"data_mode"`        // default customer
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
  decoder, yet still untrusted for Markdown. `Origin` is shown on a finding line and in the task block; it is presentation only. `Locations` may be empty.
  A report has no exit code, no `needs_attention` flag, no per-dimension trust and no single validation command.
  `Seam` is read only for the coupling columns of a dependency table (Strength, Distance, Volatility of the seam with the same modules) and for its `Hypothesis`, the "Suggested move" of an advisory. `Finding.MatchedBy` holds only the five `MatchedByKeys`: the four ratchet keys (`metric`, `before`, `after`, `threshold`), read to name a metric ratchet, and `locations_total`, read through `report.LocationsTotal` for every location count; the decoder drops every other `matched_by` key. `Metric` values are shown only through `Delta`. `StateSchemaV1` is the schema constant the decoder enforced.
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

// Reverses reports whether f and g are the two directions of one module cycle: one rule, one kind, status and severity,
// and module pairs that are reversed, non-empty and distinct. Display only: each finding keeps its ID and gate effect.
func (f Finding) Reverses(g Finding) bool
```

<!-- contract: Report, StateSchemaV1, MaxBytes — restated from internal/wire/report/module.md -->
```go
// StateSchemaV1 is the engine state contract accepted (archfit.state.schema.json, engine v3.0.0 and v3.0.1: one file).
const StateSchemaV1 = "archfit.architecture-state.v1"

// MaxBytes caps an uploaded payload: 5 MiB (the "5 MB" cap of decisions.md).
const MaxBytes = 5 << 20

// Report is the no-source transport form of an engine state report. The decoder knows every key of the engine
// state schema of v3.0.0 and v3.0.1: keys listed here are kept (length-capped), the other known keys are validated and dropped
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

<!-- contract: Finding, Edge, Endpoint, Location, AgentTask — restated from internal/wire/report/module.md -->
```go
type Finding struct {
    ID                  string     `json:"id"`   // 32-hex fingerprint, unique per report
    Kind                string     `json:"kind"` // "gate" | "advisory"
    RuleID              string     `json:"rule_id"`
    Status              string     `json:"status"`   // "new" | "baseline" | "waived" | "expired_waiver" | "fixed"
    Severity            string     `json:"severity"` // "critical" | "high" | "medium" | "low"
    Confidence          string     `json:"confidence"`
    Edge                Edge       `json:"edge"`
    Locations           []Location `json:"locations"`  // may be empty; never invent one
    Why                 string     `json:"why"`        // engine-authored, single line, length-capped
    Constraint          string     `json:"constraint"` // engine-authored, single line, length-capped
    AllowedAlternatives []string   `json:"allowed_alternatives,omitempty"`
    Origin              string     `json:"origin,omitempty"` // "introduced" | "pre_existing" | "unknown", only with --base; presentation only
    // MatchedBy holds only the MatchedByKeys of the engine's matched_by: a metric ratchet names its numbers there, and a
    // dependency finding its full location count (the engine lists at most 50 locations).
    MatchedBy map[string]string `json:"matched_by,omitempty"`
}

// The matched_by keys a consumer shows. The engine writes more keys; the others stay out of the App.
const (
    MatchedByMetric    = "metric"
    MatchedByBefore    = "before"
    MatchedByAfter     = "after"
    MatchedByThreshold = "threshold"
    // MatchedByLocationsTotal is the deduplicated location count of a dependency finding, whose Locations the engine
    // caps at 50.
    MatchedByLocationsTotal = "locations_total"
)

var MatchedByKeys = []string{MatchedByMetric, MatchedByBefore, MatchedByAfter, MatchedByThreshold, MatchedByLocationsTotal}

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
    Status  string        `json:"status"`
    Reasons []string      `json:"reasons,omitempty"`
    Metrics []MetricDelta `json:"metrics,omitempty"`
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
    // Policy is what the engine's policy says about the seam: "violation" | "accepted" |
    // "advisory" | "allowed" | "observed", a closed set. "" = the key is absent.
    Policy string `json:"policy,omitempty"`
    // Hypothesis is the engine's balancing move for the seam: "balanced" | "accept_low_volatility" |
    // "introduce_contract" | "move_functionality" | "declare_volatility" | "expected_by_role" | "follow_rule".
    // "" = the engine gave no move.
    Hypothesis string `json:"hypothesis,omitempty"`
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
    Title        string
    Author       string // the login of the user who opened it
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
    DetailsURL  string // the "Details" link GitHub shows on the check; "" = GitHub's default
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

### store

- **Counterpart**: internal/store
- **Direction**: feedback depends on store
- **Strength**: contract
- **LCA / Rank / Distance**: root / 2 / 2
- **Volatility**: high (feedback side); store is low
- **Balanced?**: yes
- **Shared knowledge**: tenant = installation ID; the key `feedback/lease/<head_sha>`, private to feedback; `Get`
  returning `ErrNotFound` for a missing key and a non-nil slice for a present one; `CompareAndSwap` with a `nil` old
  for "only if missing" and a `nil` value for "delete", atomic across every instance that shares the store.
  feedback never calls `Put`, `Delete`, `Add`, `DeleteTenant` or `Tenants`. `SystemTenant` is never used here.

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

- **internal/gate** (parent): functional, gate / 1 / 1, high, balanced. Builds `Publication` and `Receipt`; passes `Deps.Store` to `New`; calls `Publish`, `Pending`, `State`.
- **internal/gate/sweep**: contract, gate / 1 / 1, high, balanced. Calls all four `Publisher` methods and compares `Receipt` fields.

## Change Vectors

- Wording, layout, grouping, caps and truncation order.
- A new marker version (`v2:` external IDs) when the receipt changes shape.
- SARIF-based annotations, or annotations on the gate check.
- A different `archfit/state` conclusion mapping.
- A change to the lease value. During a rollout two revisions run together, and an unreadable lease is taken over,
  so a new value must keep `token` and `expires` readable by the previous revision; otherwise the two revisions
  would not serialize against each other.

## Constraints and Invariants

- The gate check conclusion is always `string(Outcome.Conclusion)`: `success`, `failure` or `action_required`.
  feedback never downgrades, upgrades or skips it. `neutral` is written only to `archfit/state`, and never for an
  outcome that asks for action: that is `action_required` on both checks.
- In advisory mode no `archfit/gate` check is written. A ruleset that requires it then stays blocked: closed, never green.
- An older `(RunID, RunAttempt)` never overwrites a newer one on the same head; a publication for an old
  head never touches the comment. Check runs are per SHA, so an old head's checks cannot affect the new head.
- Every write to one head SHA holds that head's publication lease, so writers of one head never interleave, also
  across instances. An older publication that read a check before a newer one wrote it cannot write at all until
  the newer one is done, and then sees the newer receipt and skips. `Pending` holds the same lease, so it never
  creates an `in_progress` run after a publication completed the check. A Store error writes nothing.
  A call that failed after a GitHub request keeps its lease until it expires, so a write still in flight after a
  client timeout, a 5xx or the work deadline lands before the next publisher of the head reads.
  Stated limit **[default]**: a lease is not a fence for GitHub. A holder stops by its own clock 10 s before the
  lease expires; a request GitHub applies more than 10 s after the work deadline (or after a clock skew that eats
  the margin) can still land after another instance took the lease over.
- A publication with `Resolve` set is written only while its receipt digests are still accepted by the inputs read
  inside the lease, so an upload decided from older inputs never replaces a sweep correction made from newer ones,
  although both carry the same run identity. Stated limit **[default]**: inputs that change after that read and
  before the write are the sweep's to correct, as for any change after a publication.
- Exactly one comment per PR under sequential delivery and for concurrent publications of one head. Two concurrent
  first publications for two heads of one PR hold two leases and may create two comments; later calls update only
  the lowest-ID one **[default, stated limit]**. Check runs are per head, so the lease covers them; a second run of
  one name could only come from an expired lease, and then the highest ID wins.
- Annotations are sent only to an `archfit/state` run the same call created, because GitHub appends the annotations
  of every update. A repeated delivery updates the state run in place only when it already holds the publication's
  annotations (same `external_id`, equal `AnnotationCount`); a new attempt, a new report, a sweep publication over an
  annotated run, or a retry after a missing file list creates a new state run. Old state runs stay on the head SHA;
  GitHub shows the newest. `State` and the sweep read only `archfit/gate`, which is still updated in place, so the
  state run identity does not affect them. Stated limit **[default]**: an equal annotation count stands in for equal
  annotations. The re-read before each further annotation batch stays as a second guard behind the lease.
- Never print "verified", a score, a grade or a percentage of health. Trust is "customer-attested".
  Provenance is necessary, not sufficient.
- Never invent a location, a repair route or independent evidence. No annotation without a `Location`.
- Declared restatement: the nine dimension names and their order (block 6) are a literal list in `render.go`. The
  owner is `wire/report`, whose decoder accepts exactly these keys and exports no list of them. If the two drift, a
  new engine dimension decodes but is not printed, and a removed one prints "not reported". Trigger to change: a new
  engine dimension; remove the list when `wire/report` exports the keys in order.
- Origin and finding status are presentation only; grouping never hides a blocking finding. The "blocking" label
  is `Finding.Active()` of `wire/report`, a display heuristic here; the conclusion always comes from `Outcome`.
- Engine-authored strings are sanitized before rendering: `<!--`, `-->` and backticks are removed, `<` and `>`
  HTML-escaped, newlines dropped. The agent task JSON is encoded with HTML escaping (`<`). So no report
  content can forge a marker; `State` still reads only the last receipt marker.
- `Publication.Identity` is a claim copied from the envelope. feedback prints it as claimed and never as
  approved; identity strings are sanitized like every other producer-supplied string.
- No LLM, no network other than the three role interfaces, the Store and the caller's `Publication.Resolve`. The Store holds only the publication
  leases: `Get` and `CompareAndSwap` on `feedback/lease/<head_sha>`, never a receipt, a report or a body. Imports:
  standard library, `gate/verdict`, `gate/inputs`, `wire/report`, `wire/settings`, `wire/manifest`, `github/api`,
  `store`. Never `control`, `app`, LLM or UI packages.
- Rendering is deterministic: equal `Publication` and clock give byte-identical text.
- `api.ErrRateLimited` and other API errors are returned unchanged, with one exception: a `ChangedFiles` error other
  than `ErrRateLimited` is logged and the call returns `nil`, because the file list only filters annotations. A
  `Publication.Resolve` error is returned wrapped, with `errors.Is` intact. A failed publication never produces green; one that fails before the gate check is written leaves the pending check
  pending.
- Logs (`log/slog` default logger **[default]**): repository ID, PR number, head SHA, run ID, attempt, digests,
  conclusion, reason codes, action taken (`created`, `updated`, `skipped_newer`, `skipped_old_head`), timings.
  Never bodies, paths, messages or names.
- Safe for concurrent use, also across instances that share the Store. No state between calls but the lease,
  which is deleted when the call succeeds or fails before its first GitHub request, and otherwise expires.

## Test Specification

Fakes: in-memory `Checks`, `Comments`, `Pulls` with call recording and injectable errors; a fixed clock; the
memory Store, wrapped where a test needs a Store fault; a replaceable lease wait that observes contention.
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
  **Scenario**: 10,000 findings, 2,000 tasks and UTF-8 locations.
  **Expected behavior**: bodies stay within 65,535 bytes; receipt/recovery survive; whole nonempty tasks reserve space before discretionary detail; every omitted task is counted; detailed state cuts say Output truncated.
- **Test name**: `TestEvidenceGapSources`
  **Scenario**: a report with one unevaluated rule, a `Coverage.Tools` entry with status `timed out`, a `Dimension.Unknown` fact, a profile `Unknowns` entry, a `partial` producer; the same with none of them; no baseline on the protected ref; a trusted baseline with a `non_comparable` reference, a `not_requested` reference, a nil reference and a `comparable` one; the same four without a trusted baseline.
  **Expected behavior**: each item appears in the evidence-gap section with its reason; "None reported"; the line "No baseline: every finding counts as new, including older debt. New does not mean added by this pull request."; the reference reasons, "no reason given" for `not_requested` without reasons, and the no-gate-reference line for nil. With a trusted baseline a comparison line is present exactly when `verdict.Decide` reports `baseline_not_comparable` for the same report; without one it is never present.
- **Test name**: `TestEvidenceGapsDropNoise`
  **Scenario**: coverage rows `ok`, `absent` without a reason (twice), `disabled` with a reason, `timed out` with a reason, `absent` with a reason; producer rows that repeat four of them, one without a coverage row (`git-history: absent`) and one whose status differs from its coverage row; rendered as comment and state summary.
  **Expected behavior**: full lines for the `timed out` row, the `absent` row with a reason, the producer without a coverage row and the producer whose status differs; one line "Analyzers that did not run (absent or disabled): …" naming the reasonless `absent` and the `disabled` tools in report order; no line for a repeated producer, no disabled reason text, no "None reported".
- **Test name**: `TestGateSummaryNamesBlockers`
  **Scenario**: source-admissible blockers, accepted debt, whole-file/missing locations, caps and a blocker trailing an unknown-identity rejection.
  **Expected behavior**: up to 20 source-admissible active blockers are named. The rejected-source case instead retains safe identity recovery and withholds the purported blocker; receipt/conclusion remain unchanged.
- **Test name**: `TestHeadlineStatesTheEffectOnMerge`
  **Scenario**: table of gate mode (advisory, required), target (pull request, push), conclusion, report verdict and trusted baseline (present, absent), plus `action_required` on an advisory pull request.
  **Expected behavior**: the headline of the Layout table for each row; a `failure` conclusion beside a `healthy` or `needs_attention` verdict reads "Architecture violations found"; " · no baseline" exactly when no baseline is trusted; "Action required" wins.
- **Test name**: `TestAdvisoryOfUnknownStatusStaysOpen`
  **Scenario**: an advisory finding with status `new` and one with a status this version does not know.
  **Expected behavior**: the known one in the advisory fold; the unknown one open with the violations, counted in the tally.
- **Test name**: `TestWaiverNoticeStaysOutsideFolds`
  **Scenario**: a body whose first `###` heading is inside a fold.
  **Expected behavior**: the notice lands before the fold.
- **Test name**: `TestDependencyCyclesAreOneItem`
  **Scenario**: two findings of one rule in both directions; one direction; two rules in both directions; one rule twice
  in one direction; three modules in a cycle without a reverse pair; two cycles through one module; two separate
  cycles; a cycle beside a plain finding; a partner at index 50, past the cap; a direction with a `Path`, allowed
  alternatives and a constraint both directions share; the check-summary rendering.
  **Expected behavior**: one cycle item "between `a` and `b`" with a diagram; one, two and two list items and no
  cycle; one item "among `a`, `b`, `c`" twice; two items; one item and one list item; the tally counts every finding;
  50 items, "and 1 more" and no cycle; a table row per direction with the path, the alternatives, and the shared
  constraint once; the same item without a diagram.
- **Test name**: `TestCycleDiagramAndTablesCannotBeBrokenByReportStrings`
  **Scenario**: a cycle whose module name holds a quote, a bracket, pipes, backticks, an arrow and a code fence.
  **Expected behavior**: every diagram line matches the generated Mermaid grammar; exactly one fence pair; every table
  row keeps its columns.
- **Test name**: `TestCouplingColumnsComeFromTheSeam`
  **Scenario**: two advisories with one `Why`; a seam for the first module pair only.
  **Expected behavior**: strength, distance and volatility of the seam on the first row, empty cells on the second;
  the `Why` once.
- **Test name**: `TestCycleDirectionsShowFindingOriginNotPairNovelty`
  **Scenario**: introduced cycle identities with mixed other evidence, another cycle, and unknown comparison.
  **Expected behavior**: TestCycleDirectionsShowFindingOriginNotPairNovelty preserves each finding's own origin; no pair/site-added claim; unknown comparison clears every origin.
- **Test name**: `TestLeadSentenceSaysWhatThePullRequestDid`
  **Scenario**: cycles, mixed/introduced/pre-existing findings and unknown Git-base comparison.
  **Expected behavior**: shared finding-identity counts or explicit unknown; no inferred new dependency/pair/site or chosen repair direction.
- **Test name**: `TestCommentHeadHasNoReasonCodes`
  **Scenario**: a blocker beside independent missing coverage.
  **Expected behavior**: compact comment carries both role-aware actions without raw reason codes/digests; detailed state preserves reasons/categories/details and the parsed prelude.
- **Test name**: `TestDimensionsStateTrustOnceAndOnlyRealChanges`
  **Scenario**: a delta that is only "comparable", then one with a metric change; the whole comment.
  **Expected behavior**: no Change column, then one with "edges 1 → 2 (+1)"; "customer-attested" once in the comment.
- **Test name**: `TestManyViolationsRenderQuickly`
  **Scenario**: 5 000 introduced violations of one rule in a chain, compared with the base.
  **Expected behavior**: block 5 and the lead sentence render within 5 s: the cycle search sees only the shown 50.
- **Test name**: `TestCouplingWordsFollowBalancedCoupling`
  **Scenario**: each known distance token and an unknown one; an engine rule and a policy rule.
  **Expected behavior**: the book's words, the unknown token as written; "Imbalanced coupling"; the policy rule ID as a code span.
- **Test name**: `TestCutClosesACodeFence`
  **Scenario**: a findings block whose diagram is cut in the middle.
  **Expected behavior**: within the limit, and the fence is closed.
- **Test name**: `TestCommentLinksToTheAppPullPage`
  **Scenario**: a publication on a pull request with an App URL; no App URL; a push; a repository name with a space and a question mark.
  **Expected behavior**: the "Details in Archfit" link to `/r/acme/shop/pull/7` in the visible part, after "Next" and an entitlement notice and before the folds; no link; no link; a path-escaped link. The state check, the gate check and the pending gate check carry the same URL as `DetailsURL`.
- **Test name**: `TestAdvisoryCommentNeverSaysBlocking`
  **Scenario**: a blocked publication on an advisory pull request, rendered as comment.
  **Expected behavior**: the advisory headline and, in the Decision block, "The gate is advisory for this repository"; no note box; "violation" labels; no "Blocked by" and no "blocking" label; violations before the first fold.
- **Test name**: `TestNextStepNeverInventsValidationFlags`
  **Scenario**: blocking outcome and safe/unsafe base branch names.
  **Expected behavior**: TestNextStepNeverInventsValidationFlags uses only supplied task validation; no origin branch command, fabricated flags or container path is constructed.
- **Test name**: `TestMetricRatchetReadsAsNumbersNotAsModules`
  **Scenario**: a `metric/cycle_count` finding with all four `matched_by` keys, without a threshold, and without `matched_by`, rendered as comment, check summary and gate blocker line.
  **Expected behavior**: "metric `cycle_count`: 0 → 2 (max_new 0)", without the parenthesis, and "metric `cycle_count`"; never "(no module)"; no Locations line.
- **Test name**: `TestSameColumnsFoldIntoTheLineUnderTheTable`
  **Scenario**: two rows with the same strength, distance and volatility; two rows that differ in volatility; one row; no seam.
  **Expected behavior**: no strength, distance or volatility column and "On every row: functional coupling · across modules · high volatility."; only the volatility column kept and the other two words under the table; all columns for one row; no coupling column without a seam.
- **Test name**: `TestSuggestedMoveIsInBookWords`
  **Scenario**: advisories whose seams carry different hypotheses, the same hypothesis, an unknown token, none; and a table built without moves.
  **Expected behavior**: a "Suggested move" column with `report.BalancingMoves` words; one line under the table when equal; an unknown token as written; no column without a hypothesis or when moves are off.
- **Test name**: `TestEngineDecisionNeverHidesABlockedVerdict`
  **Scenario**: verdict `blocked`, hard gates `fail`, 4 blockers, one unevaluated rule, in advisory and required mode; verdict `needs_attention` with `unmeasured` gates.
  **Expected behavior**: "Engine verdict **blocked**: hard gates fail, 4 active blockers, 1 required rule not evaluated"; the advisory line only in advisory mode; "needs attention" and "hard gates not measured" read as words.
- **Test name**: `TestDriftCountsAreShownOnlyWhenTheEngineCompared`
  **Scenario**: the drift dimension with `new_seams` and `resolved_seams` under a comparable delta, a non-comparable one, one count missing, no delta.
  **Expected behavior**: "8 tight cross-module seams at high volatility new since the baseline, 0 resolved" only for the first.
- **Test name**: `TestLeadSentenceForAnAddedDependencyThatFormsACycle`
  **Scenario**: introduced cycle findings plus an introduced advisory on one pair.
  **Expected behavior**: three introduced finding identities, not a claim that one dependency or source site was newly added.
- **Test name**: `TestNextStepAndLinkSurviveACutOfTheViolations`
  **Scenario**: 50 long violations and an App link.
  **Expected behavior**: compact comment lists three and counts 47 more; role-aware Next, current-App link, task/trust content and size limits survive without fabricated validation flags.
- **Test name**: `TestAssembleKeepsTheTailWhenTheFindingsAreCut`
  **Scenario**: a layout with huge findings, a tail longer than the slack a cut leaves, and a receipt.
  **Expected behavior**: the tail and the receipt stay whole, after the cut note.
- **Test name**: `TestAnAdvisoryOnAViolationsPairJoinsItsRow`
  **Scenario**: a cycle with an advisory on one of its pairs; another advisory on an unrelated pair; one advisory alone.
  **Expected behavior**: an "Also reported as" cell on that row and no advisory fold; the fold says "on other dependencies" only when a covered advisory exists, in the comment and in the tally.
- **Test name**: `TestLegendSaysWhatRedMeansWhenSomeDirectionsAreUnplaced`
  **Scenario**: all directions placed, some unplaced, none placed.
  **Expected behavior**: three legends; only the second says "or not known to have existed before".
- **Test name**: `TestLocationCountsUseTheEnginesFullCount`
  **Scenario**: a cycle direction and a plain violation with 50 listed locations and `matched_by.locations_total` 120.
  **Expected behavior**: the diagram label "120 locations", "and 117 more" in the cycle table, "and 110 more" in the list item; no count of 50.
- **Test name**: `TestDriftSaysSoWhenTheBaselineHoldsNoSeam`
  **Scenario**: every current seam new and none resolved; some new; all new but one resolved; none changed.
  **Expected behavior**: "N tight cross-module seams at high volatility, new since the baseline: it holds none" only for the first; counts for the rest.
- **Test name**: `TestLinkFollowsTheNoticeWhenNoViolationIsListed`
  **Scenario**: a healthy publication with an entitlement notice and an App link.
  **Expected behavior**: the link is after the notice.
- **Test name**: `TestDigestSourceLabels`
  **Scenario**: an upload whose claims were accepted; an upload with `baseline_mismatch`; one with `labels_mismatch`; the zero `Identity` (a `sweep.Without` publication); no baseline and no labels file; rendered as comment, gate summary and state summary.
  **Expected behavior**: "stated by the producer" on both lines; "from the protected ref" on the mismatched line only; "from the protected ref; no upload" on both lines; "no baseline" and "no labels file" without a label. "stated by the producer" never appears without an upload.
- **Test name**: `TestIdentityLine`
  **Scenario**: a full `Identity`; the zero `Identity` (sweep publication); an identity whose fields contain marker text.
  **Expected behavior**: "claimed: engine v3.0.0, image sha256:…, linux/amd64"; "no upload"; sanitized text; never the word "approved" or "verified".
- **Test name**: `TestAnnotationSelection`
  **Scenario**: findings with no, one and many locations, `Line == 0`, live `advisory` and `gate` findings, `baseline`, `waived` and `fixed` findings, and a finding in a file the PR did not change; published on a pull request whose file list names four files, with a truncated list, on a push, with 200 live locations, and with no live located finding. The fake rejects more than 50 annotations in one request, as the client does.
  **Expected behavior**: on the PR only reported positive lines of changed files: blockers first as `failure`, then advisories in report order as `warning`; no annotation for file-only/nonpositive lines, empty files, `locations: []`, accepted debt, fixed findings or unchanged files; requests of 50 then 20 on the state check (none on the gate check); one file-list call. The truncated list and the push annotate every file, and the push lists none. 200 locations give 150 annotations, blocking first, in requests of 50. Without a possible annotation the file list is not read.

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
- **Test name**: `TestAdvisoryStateReadsTheStateCheck`
  **Scenario**: a publication on an advisory pull request.
  **Expected behavior**: `State` finds no gate check; `AdvisoryState` returns the published receipt from `archfit/state`.
- **Test name**: `TestWarnIsIdempotent`
  **Scenario**: `Warn` with no comment; again with the same expiry; with another expiry; then `Publish` with matching and with non-matching `EarliestWaiverExpiry`; `Warn` on a push target.
  **Expected behavior**: one comment created; no call; section replaced; section kept; section dropped; no call.
- **Test name**: `TestGoldenFeedback` (T02, T11)
  **Scenario**: render every wire fixture: healthy, warning-only attention, blocked, missing required evidence, unknown identity, incompatible profile, invalid input (nil report), blocker plus unevaluated rule, non-applicable rule, `locations: []`, `matched_by: {}`, both repair kinds, waiver expiry, a metric ratchet finding, two advisories with different balancing moves, and the pilot's PR #11 report (engine v3.0.1, advisory mode: one dependency cycle among three modules, coupling advisories, accepted debt).
  **Expected behavior**: golden bodies match; the verdict leads, and the next action is in the visible part; the evidence-gap section exists in each; `repair_kind` values appear unchanged in the JSON block, whose objects have exactly the eight task keys; validation commands from `AgentTasks[].Validation` are de-duplicated and printed verbatim with `--base`, `--lang`, `--require-tools` when present and never `--refresh`; dimensions appear in the engine's fixed order of nine.

### Boundary Tests

- **Test name**: `TestNoVerifiedNoScore` (T11)
  **Scenario**: scan every golden body and every title for App-authored text.
  **Expected behavior**: no "verified" (any case), no "score", no grade; "customer-attested" present whenever a report is rendered.
- **Test name**: `TestGateCheckNeverNeutral` (T13)
  **Scenario**: publish an outcome for every reason code, with and without a report, in required mode.
  **Expected behavior**: `archfit/gate` conclusion is one of `success`, `failure`, `action_required`, equal to the outcome; `archfit/state` is `action_required` whenever the outcome is.
- **Test name**: `TestRateLimitLeavesPendingAlone`
  **Scenario**: `ErrRateLimited` from `CheckRuns`, from `ChangedFiles`, from `UpdateCheckRun`, from `CreateComment`.
  **Expected behavior**: the error is returned with `errors.Is` intact; the pending check is unchanged when the error comes before the gate check is written (`CheckRuns`, `UpdateCheckRun`); no failed publication produces green; a retry once the kept lease expired completes the publication with one gate check and one comment.
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
- **Test name**: `TestConcurrentWritersOnOneHead` (T03)
  **Scenario**: required mode; the first writer is held right after its gate-check read while the second starts, and is released once the second finishes or waits for the lease. Cases: an older success (run 6) first and a newer failure (run 7) second, both over a pending check; the reverse order; `Pending` with no check first and a publication of run 7 second.
  **Expected behavior**: in every case one gate check, `completed`/`failure` from run 7, and no lease left. Without the lease the first case ends `success` from run 6 and the third with a second, `in_progress` gate check; the reverse order passes either way.
- **Test name**: `TestLeaseTakeover`
  **Scenario**: `Publish` with no lease, an expired lease, an unreadable lease value, an empty lease value, and a lease another holder holds for one more second (the wait cancels the context).
  **Expected behavior**: the first four publish without waiting and release the lease; the held lease is waited for, the call returns `context.Canceled` and makes no GitHub call.
- **Test name**: `TestLeaseWaitsUntilExpiry`
  **Scenario**: a held lease that expires 60 s after the clock; each wait advances the clock 20 s.
  **Expected behavior**: three waits, then the publication.
- **Test name**: `TestLeaseFailsClosed`
  **Scenario**: `Publish`, `Pending`, `Warn` and `Reconcile` with the Store's `Get` failing, with its `CompareAndSwap` failing, with a target without an installation ID, and with a target without a head SHA.
  **Expected behavior**: each call returns an error; no check or comment call is made; `Reconcile` never calls its decision.
- **Test name**: `TestReconcileDecidesInsideTheLease`
  **Scenario**: a gate check of run 7.1; another holder has the lease and publishes run 7.2 before it releases; `Reconcile` with a decision that returns a publication over the check it is given, one that returns nil, and one that fails.
  **Expected behavior**: each decision is given run 7.2; the first writes over 7.2, the other two write nothing and the third's error is returned; no lease is left.
- **Test name**: `TestLeaseHolding`
  **Scenario**: the work context of a held lease; a request context cancelled during the work; another holder's lease written over this one during the work; a second head while the first is held.
  **Expected behavior**: the work deadline is about 50 s from now by elapsed time, not by the injected clock; the lease is released after the cancelled request; the other holder's lease is not deleted; the second head does not wait.
- **Test name**: `TestLeaseKeptAfterFailedCall`
  **Scenario**: a held call that fails with a possibly sent write (`context.DeadlineExceeded`), and one that fails with `ErrStaleInputs`; then a second holder whose waits advance the clock 20 s each.
  **Expected behavior**: the first keeps its lease and the second holder waits 60 s, until it expires; the second releases at once and the next holder does not wait.
- **Test name**: `TestStaleDecisionNeverReplacesNewerInputs`
  **Scenario**: a sweep-style `stale_inputs` correction with policy P2 and run 7.1, then an upload of run 7.1 decided `success` from P1 whose `Resolve` returns P2; the same with `Resolve` failing; an upload decided from P2 with owner approval `granted` whose `Resolve` returns the same inputs with approval `missing`; and two uploads decided from P2 over a correction from P1 whose `Resolve` returns exactly the inputs they were decided from, one without and one with a granted approval.
  **Expected behavior**: the first and third return `ErrStaleInputs`, the second the `Resolve` error, and in all three the gate check stays `action_required`/`stale_inputs`; the last two write `success`. No lease is left. Without the check the first three end `success`; with a digest-only check the third does.
- **Test name**: `TestChangedFilesErrorStillPublishes`
  **Scenario**: in required mode with a pending gate check, `ChangedFiles` fails once with `ErrRateLimited`, and once with another error; then a retry 60 s later.
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
  **Scenario**: suspended outcome with a report compared with the base, whose violations are introduced; entitlement with `WarnFrom` in the past and not suspended; `WarnFrom` in the future.
  **Expected behavior**: subscription reason with `Entitlement.Reason` as next action, no findings and no lead sentence; the notice line appears; no notice.
- **Test name**: `TestPendingThenFinal` (T13)
  **Scenario**: `Pending`, then `Publish` of a failure, in required mode.
  **Expected behavior**: the same check run goes from `in_progress` to `completed`/`failure`; no second gate check exists.

# Gate Sweep

**Path**: internal/gate/sweep/
**Parent**: internal/gate/ (gate façade)
**Submodules**: none (leaf)

## Purpose

`sweep` is the single recovery mechanism of the gate. One pass re-derives, from GitHub alone, what the
gate check of every open pull request should say, and repairs the difference. Without it, a lost webhook
would leave a PR without a pending check, a workflow that never uploaded would leave it pending forever,
and a changed base policy, an expired waiver or a restored subscription would never reach open PRs that get no new commit.

## Functional Responsibilities

- Skip installations no operator approved, and count them.
- Walk installations → repositories → open pull requests (drafts included) whose base has a settings file.
- In required mode: ensure fork PRs show `fork_unsupported` and suspended repositories show
  `subscription_suspended`. In advisory mode only the waiver warning runs.
- Create the missing pending gate check; turn a pending check into `missing_upload` after `MissingUploadAfter` (60 minutes).
- Detect `success` and `failure` receipts whose owner approval is gone (dismissed, followed by a request for changes,
  the approver no longer an owner, a protected path newly changed); publish `owner_approval_missing`, with no re-run.
- Detect receipts whose policy, baseline or labels digest no longer matches the protected ref; publish
  `stale_inputs` and re-run the customer's workflow.
- Warn 7 days before the earliest waiver expiry; after expiry publish `waiver_expired` and re-run.
- Re-run after a restored entitlement or a late owner approval.
- Run the same pass for one repository on request (`RunRepo`), used when the protected ref changes.
- Stop cleanly on GitHub rate-limit exhaustion: the next `Run` continues, and `RunRepo` returns the limit so its caller retries.
- Return counts only, including the number of skipped units.

## Subdomain Classification

Core. Reconciliation is what makes "fail closed" livable. Volatility: high; rules and thresholds will be
tuned against rate limits and pilot behavior.

## Encapsulated Knowledge

- The rule order per pull request and which rule ends the evaluation.
- What "stale" and "withdrawn" mean for a receipt, and the expiry test based on the check's `StartedAt`.
- The re-run guard that keeps re-runs to one per receipt.
- How a `Without` publication keeps the previous run identity so that feedback updates the gate check in place.
- The traversal order and its rotation.
- Which errors stop the pass and which are skipped.

## Public Contract

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
    NotAdmitted                          int  // installations skipped: no operator approved them
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

```go
// Admitter answers whether the App serves an installation; owner is its account login. gate/inputs.Admissions
// implements it.
type Admitter interface {
    Admitted(ctx context.Context, installationID int64, owner string) (bool, error)
}

func New(repos Repos, pulls Pulls, actions Actions, r Resolver, p Publisher, admit Admitter, c Config) Sweeper
```

Zero `Config` durations are replaced by 60 minutes and 7 days **[default]**.

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

### Traversal

1. `Repos.ListInstallations`. An error here is returned; nothing was done.
2. Installations are visited in ID order, starting at index `(now.Unix() / 3600) mod len` **[default]**, so a
   pass that stops early does not starve the same tail every hour. No cursor is stored.
3. Per installation: `Admitter.Admitted(Installation.ID, Installation.Account)`; false → `Summary.NotAdmitted++`, skip the
   installation (no `ListRepos`, nothing read or written); an error → skip it the same way and count it in
   `Summary.Errors` (not `NotAdmitted`), like any other skipped unit. Then `Repos.ListRepos`; per repository `Pulls.OpenPulls`. `Summary.Repos` counts repositories visited.
4. Per pull: `Target{Repo, PullRequest: Number, HeadSHA, BaseRef, Fork: pull.HeadRepo != Repo.FullName}`, then
   `Resolver.Resolve(target, now)`. `!SettingsFound` → skip (not onboarded); the miss is remembered per
   `(repository, BaseRef)` for the rest of the pass. Otherwise `Summary.Pulls++` and the rules below run.
5. Sequential, one pull at a time **[default]**.

`RunRepo(ctx, r, now)` is steps 3 to 5 for the one repository `r`, without the admission check (its caller, the
gate's `HandleEvent`, asked `Admitted` for the event already): same rules, same guards, same error
handling, `Summary.Repos == 1`. The caller supplies `r` from a verified webhook; only `r.RepoRef` and
`r.Private` are needed. It exists so that a default-branch push that changes a protected path reaches the
repository's open PRs at once; the hourly `Run` stays the recovery path when that event is lost.

### Rules per pull request

`state` is `Publisher.State(target)`, `rc` is `state.Receipt`, "has receipt" means `state.Exists && !state.Pending`.
The first rule whose condition holds ends the evaluation of that pull, whether or not it had to write anything (a fork already marked, a pending check younger than the limit), except rule 6a, which never ends it. Otherwise a fresh pending check would fall through to rule 5b and read as a stale zero receipt.

| #   | Condition                                                                       | Action                                                                                                 | Counter     |
| --- | ------------------------------------------------------------------------------- | ------------------------------------------------------------------------------------------------------ | ----------- |
| 0   | `Settings.Gate != GateRequired` (advisory) **[default]**                        | rules 0b, 0c, then rule 6a with `Trusted.EarliestWaiverExpiry`; `AdvisoryState`, never `State`         | `Warned`    |
| 0b  | advisory, not a fork or suspension refusal, the state check's receipt is stale  | publish `Without(ReasonStaleInputs)`, then re-run                                                      | `Rerun`     |
| 0c  | advisory, `rc.Reason == stale_inputs`                                           | re-run (a re-run that failed in an earlier pass; the guard makes it a no-op otherwise)                 | `Rerun`     |
| 1   | `target.Fork`                                                                   | unless `rc.Reason == fork_unsupported`: publish `Without(ReasonForkUnsupported)`                       | —           |
| 2   | `Trusted.Suspended`                                                             | unless `rc.Reason == subscription_suspended`: publish `Without(ReasonSuspended)`                       | `Suspended` |
| 3   | `!state.Exists`                                                                 | `Publisher.Pending(target)`                                                                            | `Pended`    |
| 4   | `state.Pending`                                                                 | if `now − state.StartedAt ≥ MissingUploadAfter`: publish `Without(ReasonMissingUpload)`                | `TimedOut`  |
| 5a  | `Withdrawn(rc, Trusted)`: the approval a decided receipt relied on is gone      | `Publisher.Reconcile` with `Withdrawal` decided inside the lease (below); no re-run                    | —           |
| 5b  | has receipt and it is stale (below)                                             | publish `Without(ReasonStaleInputs)`, then re-run                                                      | `Rerun`     |
| 6a  | expiry `e` set, `now < e` and `e − now ≤ WaiverWarnBefore`                      | `Publisher.Warn(target, e)`                                                                            | `Warned`    |
| 6b  | expiry `e` set, `now ≥ e` and `state.StartedAt < e`                             | publish `Without(ReasonWaiverExpired)`, then re-run                                                    | `Rerun`     |
| 7a  | `rc.Reason == subscription_suspended` and `!Trusted.Suspended`                  | re-run; if no re-run is possible publish `Without(ReasonMissingUpload)`                                | `Rerun`     |
| 7b  | `rc.Reason == owner_approval_missing` and `!verdict.Unapproved(Trusted)`        | re-run; if no re-run is possible publish `Without(ReasonMissingUpload)`                                | `Rerun`     |
| 7c  | `rc.Reason` is `stale_inputs` or `waiver_expired`                               | re-run (retries a re-run request that failed in an earlier pass; the guard makes it a no-op otherwise) | `Rerun`     |

Definitions:

- Advisory pull requests (rules 0, 0b, 0c): the receipt is the one on `archfit/state`, read with `AdvisoryState`.
  An advisory result that predates a change of the protected policy, baseline or labels would otherwise stay on the
  pull request with no sign that it is out of date (owner decision, 18 October 2026). Rule 0b withdraws it as
  `stale_inputs`, the same `Without` publication as rule 5b, and re-runs; rule 0c retries the re-run like rule 7c.
  A fork pull, a suspended installation, and a `fork_unsupported` or `subscription_suspended` receipt are left alone,
  as rules 1 and 2 come before rule 5b in required mode: a re-run can only refuse again. Rule 6a runs after rules
  0b and 0c in every case.
  Nothing else of the required rules applies: no pending check, no `missing_upload`, no fork or suspension
  publication, no approval withdrawal, because advisory feedback never blocks and has no gate check to keep honest.
- Withdrawn receipt: `Decided(rc)` (`rc.Conclusion` is `success` or `failure`, or `action_required` with a report-derived reason that asks for action), and `verdict.Unapproved(Trusted)` holds, the
  condition of step 5 of `Decide`. `Decide` cannot reach any of these conclusions with such inputs (step 5 comes first), so the receipt was
  decided under an owner approval that is gone: the review was dismissed or followed by a request for changes, the
  approver is no longer in the protected settings' `owners`, or the pull now changes a protected path (a base that
  moved or a new base branch). Edits of the settings file and the workflow file change no receipt digest, so only
  this rule sees them. Rule 5a comes before rule 5b: a withdrawal that also left a digest stale is still
  `owner_approval_missing`, because no re-run can pass until an owner approves again. That approval then re-runs
  through `HandleEvent(ReviewChanged)` or rule 7b. A pending, zero or unreadable receipt, and every receipt that
  already asks for action, never match.
- Rule 5a is decided twice. The pass finds `Withdrawn` from the `State` and `Trusted` it read outside the lease; then
  `Publisher.Reconcile` holds the head's lease, reads the gate check again, and the decision resolves the inputs
  again at the pass's `now`, both inside the lease, and returns `Withdrawal(target, current, trusted)`. That is
  `Without(ReasonOwnerApprovalMissing, ...)` over the current receipt, whose outcome is
  `verdict.ApprovalMissing(Trusted)` (`Detail` names `Trusted.ProtectedPaths`, as `Decide` does), or nil when the
  receipt there is no longer withdrawn. So a newer run attempt that turned green while the pass waited for the lease
  is the receipt withdrawn, under its own run identity, and an owner who approved again meanwhile leaves the check
  alone with no write. A later review is handled by its own `ReviewChanged`, which waits for this lease and then
  reads the written check. A `Resolve` error inside the lease writes nothing and counts the pull in `Summary.Errors`.
- Rule 7b fires on any approval that no longer misses, `granted` or `not_required`: a retargeted base or a repointed
  settings workflow can drop the protected path from the pull, and the `owner_approval_missing` receipt must not wait
  for an approval nobody needs. The re-run guard still allows one re-run per attempt.
- Stale receipt, any of four clauses. The digest clauses are the digest predicate of `gate/inputs`
  (`inputs.Accepts`); the sweep has no comparison of its own:
  - policy: `!Accepts(Trusted.PolicyDigests, rc.PolicyDigest)`;
  - baseline: `!Accepts(Trusted.BaselineDigests, rc.BaselineDigest)`;
  - labels: `!Accepts(Trusted.LabelsDigests, rc.LabelsDigest)`;
  - unreadable: `rc.Reason == ""`. A readable receipt always has a reason, so this is exactly the zero receipt that
    feedback returns for a missing or broken marker.
- "Empty list and empty digest" is fresh. In the no-policy window (settings merged, no policy on the protected ref,
  or the policy deleted from the base) the receipt says `policy_missing` with `PolicyDigest == ""`, the trusted list
  is empty, and nothing fires: no write, no re-run, the `policy_missing` feedback stays. When the policy merges, the
  list is no longer empty, `""` is not in it, and rule 5b fires once.
- Bound of the unreadable clause: the zero receipt has `RunID == 0`, so rule 5b makes one `Without` publication and
  the guard allows no re-run; the new receipt is readable and carries the current digests, so the next pass is fresh.
- Expiry `e`: `rc.EarliestWaiverExpiry`; when that is zero, `Trusted.EarliestWaiverExpiry`. `state.StartedAt` is the
  time of the last publication (feedback sets it), so `StartedAt < e ≤ now` means "evaluated before the waiver
  expired". The expired waiver may belong to an unrelated finding; the PR is still re-evaluated without a new commit.
- `Suspended` counts pull requests found suspended, whether or not a publication was needed.
- Rule 4 is also the only place an engine error surfaces: `archfit check` exiting 3 writes no report, so no upload
  arrives and the pending check ends `missing_upload`.
- "No re-run is possible" in rules 7a and 7b: `rc.RunID == 0`, the run is gone, or the run already has a newer
  completed attempt that uploaded nothing. The reason then becomes `missing_upload`, which is true and names the recovery.

### Without publications

`Without(code, target, trusted, rc)` is exported: the sweep owns the construction, and `gate` calls it for the fork
refusal of a new head with the zero `rc`. It returns
`Publication{Target, Identity: zero, Outcome, Receipt, Report: nil, Trusted}` with a nil `Resolve`.
`Outcome` is `verdict.Without(code)`; for `ReasonSuspended` it is
`verdict.Suspended(Trusted.Entitlement)`, so a suspension published by the sweep shows `Entitlement.Reason` as its
recovery, the same text an upload gets from `Decide`; for `ReasonOwnerApprovalMissing` it is
`verdict.ApprovalMissing(Trusted)`, whose `Detail` names the protected paths as `Decide` does. The zero `Identity`
makes feedback print "no upload" and label the digests "from the protected ref". The receipt:

| Field                                 | Value                                       |
| ------------------------------------- | ------------------------------------------- |
| `RunID`, `RunAttempt`, `ReportDigest` | copied from `rc` (zero when there is none)  |
| `PolicyDigest`                        | `inputs.First(Trusted.PolicyDigests)`       |
| `BaselineDigest`                      | `inputs.First(Trusted.BaselineDigests)`     |
| `LabelsDigest`                        | `inputs.First(Trusted.LabelsDigests)`       |
| `EarliestWaiverExpiry`                | `Trusted.EarliestWaiverExpiry`              |
| `Conclusion`, `Reason`                | from the outcome (`ActionRequired`, `code`) |

Copying the run identity keeps the `external_id`, so feedback updates the existing `archfit/gate` check in
place instead of refusing the write as "older" (an `archfit/state` run that carries annotations is replaced by a
new run without them; the sweep reads only `archfit/gate`). Writing the current digests and the new reason is the
de-duplication: the next pass finds the receipt neither stale nor expired.

### Re-run guard

`Rerun(ctx, actions, repo, rc)` is exported: the sweep owns the guard, and `gate` calls it for `ReviewChanged`.
There is one behavior for every caller:

| Situation                                                 | Calls          | Result            | Error     |
| --------------------------------------------------------- | -------------- | ----------------- | --------- |
| `rc.RunID == 0` (no workflow run reported for this head)  | none           | `RerunImpossible` | nil       |
| `Actions.Run` returns `api.ErrNotFound` (the run is gone) | `Run`          | `RerunImpossible` | nil       |
| `Actions.Run` returns another error                       | `Run`          | not meaningful    | the error |
| `run.Status != "completed"` (queued or in progress)       | `Run`          | `RerunInFlight`   | nil       |
| completed and `run.Attempt != rc.RunAttempt`              | `Run`          | `RerunImpossible` | nil       |
| completed and `run.Attempt == rc.RunAttempt`              | `Run`, `Rerun` | `RerunRequested`  | nil       |
| the same, and `Actions.Rerun` fails                       | `Run`, `Rerun` | not meaningful    | the error |

A missing run and `RunID == 0` are never errors: nobody can re-run them, so the caller skips. In the sweep, rules
7a and 7b then publish `missing_upload`; a `ReviewChanged` reaction in `gate` does nothing and leaves that
publication to rule 7b of the next pass. `Rerun` writes no log and counts nothing; the pass counts
`Summary.Rerun` for every `RerunRequested` and logs it with the rule number. If a re-run produces no upload, the check keeps its `action_required` reason with the
recovery text "re-run the Archfit workflow"; the sweep does not loop.

Termination: what changes in GitHub state after each re-run site, so the next pass does not fire again.

| Site                         | Why it does not fire again                                                                                                                                                            |
| ---------------------------- | ------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------- |
| Rule 5a                      | no re-run; the receipt concludes `action_required` with the current digests, so neither 5a nor 5b holds; rule 7b acts once no approval is missing                                     |
| Rules 0b, 0c                 | as rules 5b and 7c: the `Without` receipt on `archfit/state` carries the current digests and `stale_inputs`, and the guard stops 0c once the attempt advances |
| Rule 5b                      | the `Without` receipt carries the current digests (or `""` for an empty list), so no stale clause holds; the reason is `stale_inputs`, which leaves only rule 7c                      |
| Rule 6b                      | the publication sets `StartedAt ≥ e`, and the new receipt carries the next unexpired expiry from `Trusted`, so `StartedAt < e ≤ now` is false                                         |
| Rules 7a, 7b                 | a successful `Rerun` advances the run's attempt, so the guard blocks; an upload then replaces the reason; no upload → the next pass publishes `missing_upload`, which no rule re-runs |
| Rule 7c                      | guard only: it fires while the run is still completed at the receipt's attempt (the earlier `Rerun` failed) and stops once the attempt advances                                       |
| `HandleEvent(ReviewChanged)` | the same `Rerun` function, called by `gate`; a redelivered event finds the run queued, in progress, or at a newer attempt                                                             |

A `Rerun` call that keeps failing (for example a revoked `actions: write` permission) is retried once per pass and
counted in `Summary.Errors`; it costs one API call and no CI minutes.

### Errors

| Error                                                   | Effect                                                                    |
| ------------------------------------------------------- | ------------------------------------------------------------------------- |
| `api.ErrRateLimited` from any call (`errors.Is`)        | stop the pass; `Summary.RateLimited = true`; return the summary and `nil` |
| `ctx` cancelled                                         | return the partial summary and `ctx.Err()`                                |
| `ListInstallations` fails                               | return the error                                                          |
| any other error for an installation, repository or pull | log IDs and outcome, `Summary.Errors++`, skip that unit, continue         |

`Errors` counts skipped units; the details are in the log. `RunRepo` follows the same table (no `ListInstallations`
row), except for the rate limit: it sets `Summary.RateLimited` and returns an error that wraps `api.ErrRateLimited`.
Its one repository is the whole pass, so a stop leaves the work undone, and the caller retries it.

## Integrations

### gate/inputs

- **Counterpart**: internal/gate/inputs
- **Direction**: sweep depends on inputs
- **Strength**: contract (`Resolver`), reading `Trusted` fields
- **LCA / Rank / Distance**: gate / 1 / 1
- **Volatility**: high
- **Balanced?**: yes
- **Shared knowledge**: `Resolve` is fork-safe and reads only the protected ref; it returns an error on GitHub
  failures and on a failed read of an approval record, never on a failed read of the entitlement. `Trusted.EarliestWaiverExpiry` is the earliest expiry not before `now`.
  `Entitlement` is an opaque part of `Trusted` for this module; `Without` hands it to `verdict.Suspended` unread.
  The stale test calls `Accepts`; `Without` calls `First`. `ProtectedPaths` and `MayBeProtected` are not used here.

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
    // as it is.
    Approve(ctx context.Context, t Target, login string, seen map[string]string, now time.Time) error
}
```

The sweep calls only `Resolve`; `RecordPusher`, `Candidate`, `CanApprove` and `Approve` belong to the façade.

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

### gate/feedback

- **Counterpart**: internal/gate/feedback
- **Direction**: sweep depends on feedback
- **Strength**: contract (`Publisher`), reading `Receipt` and `CheckState` fields
- **LCA / Rank / Distance**: gate / 1 / 1
- **Volatility**: high
- **Balanced?**: yes
- **Shared knowledge**: `State` reads the `archfit/gate` check of the head SHA; `StartedAt` is the time of the
  last publication; `Publish` never overwrites a receipt with a higher `(RunID, RunAttempt)`; `Pending` and
  `Warn` are idempotent; a `Report` of nil is a valid publication. `Identity` is an opaque part of `Publication`
  for this module; the sweep always leaves it zero.

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
    // single PR comment. It is idempotent per Receipt and never overwrites feedback of a newer head or run.
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

### gate/verdict

- **Counterpart**: internal/gate/verdict
- **Direction**: sweep depends on verdict
- **Strength**: contract (`Without`, `ReasonCode` constants)
- **LCA / Rank / Distance**: gate / 1 / 1
- **Volatility**: high
- **Balanced?**: yes
- **Shared knowledge**: `Without` always returns `ActionRequired`; the sweep never calls `Decide` and never
  produces `Success`. `Facts` is not used here. `Problem` is an opaque part of `Facts` for this module, and
  `Report` is always nil in a sweep publication.

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

<!-- contract: Suspended, ApprovalMissing, Unapproved — restated from internal/gate/verdict/module.md -->
```go
// Suspended is Without(ReasonSuspended) for a caller that holds the entitlement record: Next is
// e.Reason when it is not empty, exactly as step 1 of Decide sets it.
func Suspended(e Entitlement) Outcome

// ApprovalMissing is Without(ReasonOwnerApprovalMissing) for a caller that holds the trusted inputs: Detail names
// t.ProtectedPaths, exactly as step 5 of Decide sets it.
func ApprovalMissing(t Trusted) Outcome

// Unapproved is the condition of step 5: t needs an owner approval that it does not have. Only the two known passing
// values clear it, so an unknown Approval fails closed.
func Unapproved(t Trusted) bool
```

### wire/settings

- **Counterpart**: internal/wire/settings
- **Direction**: sweep depends on wire/settings (types only)
- **Strength**: contract
- **LCA / Rank / Distance**: root / 2 / 2
- **Volatility**: high (sweep side); the file format is moderate
- **Balanced?**: yes
- **Shared knowledge**: only `Trusted.Settings.Gate` and `GateRequired` are read.

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

### github/api

- **Counterpart**: internal/github/api
- **Direction**: sweep depends on github/api (role interfaces `Repos`, `Pulls`, `Actions`)
- **Strength**: contract
- **LCA / Rank / Distance**: root / 2 / 2
- **Volatility**: high (sweep side); the client is low
- **Balanced?**: yes
- **Shared knowledge**: `OpenPulls` includes drafts. `Run` returns the latest attempt. `Rerun` needs the App's
  `actions: write` permission and spends the customer's CI minutes. `ErrRateLimited` is the sentinel returned
  after bounded backoff; `ErrNotFound` is the sentinel for a missing object. Conditional requests make repeated
  reads cheap. Used methods: `Repos.ListInstallations`, `Repos.ListRepos`, `Pulls.OpenPulls`, `Actions.Run`,
  `Actions.Rerun`. `Review` is not used here.

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

<!-- contract: Repos, Installation — restated from internal/github/api/module.md -->
```go
type Installation struct {
    ID          int64
    Account     string    // organization or user login
    AccountType string    // "Organization" or "User"
    CreatedAt   time.Time // when the App was installed
}

// Repos reads installations, repositories and file contents.
type Repos interface {
    ListInstallations(ctx context.Context) ([]Installation, error)
    ListRepos(ctx context.Context, installationID int64) ([]Repo, error)
    // ResolveRepo finds the installation that covers fullName using the App JWT.
    ResolveRepo(ctx context.Context, fullName string) (Repo, error)
    // File returns the raw bytes and git blob SHA of path at ref. ErrNotFound if absent.
    File(ctx context.Context, r RepoRef, ref, path string) (content []byte, blobSHA string, err error)
    // BranchHead returns the head commit SHA of branch. ErrNotFound if the branch does not exist.
    BranchHead(ctx context.Context, r RepoRef, branch string) (string, error)
}
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

### Consumers (inbound)

- **internal/gate** (parent): functional, gate / 1 / 1, high, balanced. Constructs the sweeper with `Deps.Sweep` and the `Admissions` as the `Admitter`,
  calls `Run(ctx, Deps.Now())` from `Gate.Sweep` and `RunRepo` from `HandleEvent` on a protected default-branch push,
  calls `Rerun` for `ReviewChanged` and `Without` for the fork refusal of a new head, re-publishes `Sweeper`, `Summary` and `Config` to `app`.

## Change Vectors

- New repair rules or thresholds; a per-repository threshold.
- Bounded parallelism, or a stored cursor, if rate limits demand it.
- More triggers for `RunRepo` (for example after an entitlement change).

## Constraints and Invariants

- The sweep never produces `Success`. It only creates pending checks, publishes `Without` outcomes
  (`action_required`), warns, and re-runs. Green comes only from a fresh report through the upload path.
- Re-run is the single re-evaluation mechanism; the App stores no report to re-evaluate.
- Everything is derived from GitHub and the protected ref on every pass. No state between passes, no cursor, no job table.
- Trusted inputs are resolved at the pull's `BaseRef`; the head tree is never read here.
- Idempotent: a second pass over unchanged GitHub state makes no write and no re-run, except retrying a re-run
  request that failed in the prior pass (rule 7c).
- At most one re-run request per receipt (the guard); a re-run in flight is never doubled. `Rerun` is the only
  code on the gate path that calls `Actions.Rerun`, and `Without` the only construction of a publication without a report.
- Declared restatement: the fork rule `pull.HeadRepo != repo.FullName` is computed here for every open pull, because
  `OpenPulls` returns raw pulls. The owner of the rule is `gate/ingest` (`Binding.Fork`); `gate` computes it a third
  time from the webhook payload. If they drift, a fork is refused on one path and judged on another. It stays while
  `inputs.Target.Fork` is a plain boolean each caller fills; remove it when `github/api` or `gate/ingest` publishes a predicate.
- Rate-limit exhaustion leaves checks pending or unchanged; it never produces green. `Run` ends with
  `RateLimited` and a nil error so the scheduler does not treat it as a failure. `RunRepo` returns the error, so the
  webhook queue retries the push that started it.
- A failure for one installation, repository or pull never aborts the pass.
- An installation that is not admitted is skipped whole: no `ListRepos`, no `Resolve`, no write. An approval that
  cannot be read is an error, not a refusal: the installation is skipped and counted in `Errors`, and a later pass
  tries again.
- Reconciliation window (stated, not zero): a missed event or an expiry is repaired within one sweep interval
  (hourly **[default]**) plus the customer's CI time. During that window the old check result stands.
- A Store outage inside `Resolve` means "not suspended"; the sweep never suspends on missing data. A pull whose approval
  record cannot be read counts as an error and keeps its check, like the lease failure below. The same outage
  fails every `Publish`, `Pending`, `Warn` and `Reconcile`, which need `gate/feedback`'s publication lease: each such
  pull counts as an error and its check stays as it was until a pass after the Store is back. The sweep never writes
  `success`; a check that was green, with an approval withdrawn meanwhile, stays green until that pass.
- Logs (`log/slog` default logger **[default]**): installation ID, repository ID, PR number, head SHA, rule
  number, action, digests, timings, and the `Summary`. Never paths, messages, owner or reviewer names.
- Imports: standard library, `gate/inputs`, `gate/feedback`, `gate/verdict`, `wire/settings`, `github/api`.
  Never `control`, `app`, `store`, LLM or UI packages.
- `now` is a parameter; the sweep never reads the clock.

## Test Specification

Fakes: in-memory `Repos`, `Pulls`, `Actions`; a scripted `Resolver`; an in-memory `Publisher` that records
calls and serves `State` from what was published; injectable errors. Time is the `now` argument.

### Unit Tests

- **Test name**: `TestStaleReceipt`
  **Scenario**: table: policy digest equal to base; equal to approved head digest; different; trusted policy list empty with receipt digest `""`; trusted policy list empty with a receipt digest; policy `""` with a trusted policy; baseline `""` with no trusted baseline; baseline `""` with a trusted baseline; baseline set with none trusted; zero receipt (empty reason) with empty trusted lists.
  **Expected behavior**: fresh, fresh, stale, fresh, stale, stale, fresh, stale, stale, stale. For every combination of a trusted list (empty, one, two digests) and a receipt digest (`""`, each entry, an unknown one), in each of the three digest fields, a receipt with a reason is stale exactly when `inputs.Accepts` is false.
- **Test name**: `TestWithdrawnReceipt`
  **Scenario**: every receipt conclusion (`success`, `failure`, `action_required`, empty, an unknown value) × every
  approval (`missing`, empty, an unknown value, `granted`, `not_required`).
  **Expected behavior**: `Withdrawn` is true exactly for `success` or `failure` with an approval other than
  `granted` and `not_required`.
- **Test name**: `TestNoPolicyWindowIsQuiet`
  **Scenario**: required mode, no policy on the base, a pull whose receipt is `policy_missing` with `PolicyDigest == ""` from run 9 attempt 1; two passes; then the policy merges (trusted list = one digest) and two more passes; the same start with the policy deleted from the base.
  **Expected behavior**: passes 1 and 2 make no `Publish`, no `Rerun`, and leave the `policy_missing` feedback; pass 3 publishes `stale_inputs` and requests exactly one re-run of run 9; pass 4 is quiet. `RunRepo` from the push trigger gives the same single repair as pass 3.
- **Test name**: `TestUnreadableReceiptIsBounded`
  **Scenario**: a completed gate check whose summary has no receipt marker; two passes.
  **Expected behavior**: pass 1 publishes `stale_inputs` once with a zero run identity and calls neither `Run` nor `Rerun`; pass 2 makes no write.
- **Test name**: `TestExpiryRule`
  **Scenario**: table over `now` relative to `e` (8 days before, 7 days before, 1 second before, equal, after) × `StartedAt` before or after `e`; receipt expiry zero with a trusted expiry.
  **Expected behavior**: nothing; warn; warn; expired only when `StartedAt < e`; the trusted expiry is used when the receipt has none.
- **Test name**: `TestRerunGuard`
  **Scenario**: `RunID` 0; run not found; run queued; run in progress; run completed at the receipt's attempt; run completed at a later attempt; `Actions.Run` fails; `Actions.Rerun` fails. Each once through a pass (rule 7c) and once through the exported `Rerun`.
  **Expected behavior**: `Actions.Rerun` is called only in the fifth and the last case. `Rerun` returns `RerunImpossible`, `RerunImpossible`, `RerunInFlight`, `RerunInFlight`, `RerunRequested`, `RerunImpossible` with a nil error, and the error in the last two cases. `RunID` 0 calls nothing.
- **Test name**: `TestWithoutReceipt`
  **Scenario**: build the publication for each `Without` code with and without a previous receipt.
  **Expected behavior**: run identity copied; digests and expiry from `Trusted`, and every digest passes `inputs.Accepts` for its list; conclusion `action_required`; `Report` nil; the outcome is `verdict.Without(code)`, and for `ReasonSuspended` with an `Entitlement.Reason` it is `verdict.Suspended(Entitlement)`, whose `Next` is that reason.
- **Test name**: `TestWithoutShowsTheEntitlementReason`
  **Scenario**: `Without(ReasonSuspended, …)` for trusted inputs whose `Entitlement.Reason` is set; `Without(ReasonMissingUpload, …)` for the same inputs.
  **Expected behavior**: the first outcome equals `verdict.Suspended(Entitlement)` and its `Next` is the record's reason; the second equals `verdict.Without(ReasonMissingUpload)`.
- **Test name**: `TestTraversalRotation`
  **Scenario**: three installations; `now` at three consecutive hours.
  **Expected behavior**: the start index rotates; every installation is visited each pass.

### Integration Contract Tests

- **Test name**: `TestRunRepairsEachRule`
  **Scenario**: one repository with one pull per rule row (fork, suspended, no check, pending 59 minutes, pending 60 minutes, stale policy, stale baseline, stale labels, waiver in 3 days, waiver expired, restored entitlement, late approval, withdrawn approval) plus a healthy one.
  **Expected behavior**: the action of each row and nothing else; the healthy pull gets no write; `Summary` counters match.
- **Test name**: `TestSecondPassIsQuiet`
  **Scenario**: run twice with unchanged fakes (the publisher reflects the first pass); every `Rerun` of the first pass succeeded, so those runs are queued or at a newer attempt.
  **Expected behavior**: the second pass makes no `Publish`, `Pending`, `Warn` with a new date, or `Rerun` call.
- **Test name**: `TestFailedRerunIsRetriedOnce`
  **Scenario**: first pass publishes `stale_inputs` but `Actions.Rerun` returns an error; second pass with `Rerun` working; third pass.
  **Expected behavior**: first pass counts the pull in `Errors` and keeps the `stale_inputs` publication; the second pass makes no `Publish` and exactly one `Rerun` (rule 7c, run still completed at the receipt's attempt); the third pass makes no call.
- **Test name**: `TestAdvisoryRepositories`
  **Scenario**: advisory settings with a waiver expiring in 2 days, no gate check, a fork pull.
  **Expected behavior**: one `Warn` and one `AdvisoryState`; no `State`, `Pending`, `Publish` or `Rerun` call.
- **Test name**: `TestAdvisoryStaleInputsRerun`
  **Scenario**: advisory pulls with a fresh receipt; no state check; a receipt that predates a new baseline; a `stale_inputs` receipt whose re-run failed earlier; a stale receipt with no workflow run. Each is swept twice.
  **Expected behavior**: nothing; nothing; one `stale_inputs` publication and one re-run; one re-run; one publication and no re-run. A stale fork refusal and a stale suspension are left alone and not re-run. The second pass writes nothing.
- **Test name**: `TestAdvisoryStaleStillWarns`
  **Scenario**: a stale advisory receipt with a waiver expiring in 2 days, swept twice (rule 0b, then rule 0c).
  **Expected behavior**: one `stale_inputs` publication and one waiver notice per pass.
- **Test name**: `TestNotOnboardedIsSkipped`
  **Scenario**: repositories without a settings file on the base; two pulls on the same base.
  **Expected behavior**: no publisher call; `Resolve` called once for that base; `Summary.Pulls` does not count them.
- **Test name**: `TestUnadmittedInstallationIsNotSwept`
  **Scenario**: three installations, each with one stale pull request; the admission fake refuses the second.
  **Expected behavior**: `ListRepos` is called for installations 1 and 3 only; `Summary.NotAdmitted == 1` and `Summary.Repos == 2`.
- **Test name**: `TestUnreadableAdmissionSkipsTheInstallationAsAnError`
  **Scenario**: three installations; the admission fake cannot read the second.
  **Expected behavior**: `ListRepos` is called for installations 1 and 3 only; `Summary.Errors == 1`, `Summary.NotAdmitted == 0` and `Summary.Repos == 2`.
- **Test name**: `TestRunRepoIsTheSamePassForOneRepository`
  **Scenario**: two repositories with stale receipts; `RunRepo` for the first; then `Run`.
  **Expected behavior**: `RunRepo` repairs only the first repository, never calls `ListInstallations` or `ListRepos`, returns `Repos == 1`; the following `Run` repairs the second and makes no write for the first.
- **Test name**: `TestDraftsAreIncluded`
  **Scenario**: a draft pull without a check in required mode.
  **Expected behavior**: a pending check is created.

### Boundary Tests

- **Test name**: `TestRateLimitStopsThePass`
  **Scenario**: `ErrRateLimited` from `ListRepos`, `OpenPulls`, `Resolve`, `State`, `Publish`, `Run` and `Rerun`, each at the second repository.
  **Expected behavior**: the pass stops; `RateLimited` true; nil error; the first repository's repairs are kept; no check turned `success`. `RunRepo` with `ErrRateLimited` from `OpenPulls` returns `RateLimited` true and an error that wraps `api.ErrRateLimited`.
- **Test name**: `TestUnitErrorsAreSkipped`
  **Scenario**: a generic error for one installation, one repository and one pull.
  **Expected behavior**: the others are processed; nil error; `Summary.Errors == 3`; one log line per skipped unit with IDs only. The same pull error inside `RunRepo` gives `Errors == 1` and a nil error.
- **Test name**: `TestListInstallationsFailure`
  **Scenario**: `ListInstallations` returns an error; `ctx` cancelled mid-pass.
  **Expected behavior**: the error is returned; the partial summary and `ctx.Err()` for cancellation.
- **Test name**: `TestSweepNeverPublishesSuccess`
  **Scenario**: every scenario of this specification with a recording publisher.
  **Expected behavior**: every published conclusion is `action_required`.
- **Test name**: `TestLogsCarryOnlyIdentifiers`
  **Scenario**: fakes with marker strings in branch names, logins and paths.
  **Expected behavior**: no marker string in logs.

### Behavior Tests

- **Test name**: `TestMissedWebhookAndMissingUpload` (T13)
  **Scenario**: the PR-opened webhook was lost; then the workflow is queued, fails, is cancelled or times out without an upload.
  **Expected behavior**: first pass creates the pending check; a pass 60 minutes later publishes `missing_upload`; at no time is the gate check `success`, `neutral` or `skipped`.
- **Test name**: `TestForksStayBlocked` (T13)
  **Scenario**: a fork pull in required mode with no check, and one that already shows `fork_unsupported`.
  **Expected behavior**: `fork_unsupported` published once; the second is left alone; no re-run.
- **Test name**: `TestBasePolicyChangeRerunsOpenPulls` (T12, T16)
  **Scenario**: three open pulls with `success` receipts; the base policy digest changes.
  **Expected behavior**: each shows `stale_inputs` and one re-run is requested per pull; the next pass does nothing more.
- **Test name**: `TestExpiryBlocksUnrelatedPullsWithoutNewCommit` (T16)
  **Scenario**: a `success` receipt published before a waiver's expiry; passes 8 days before, 3 days before, and after the expiry; the scheduler then misses a delivery and runs a day late; `Rerun` fails once.
  **Expected behavior**: nothing; one warning (idempotent on repeat); `waiver_expired` plus a re-run; the late pass performs the same repair; the failed re-run is retried by rule 7c on the next pass because the run is still completed at the receipt's attempt, and is not repeated once the attempt advanced.
- **Test name**: `TestRestoredEntitlementNeedsFreshEvidence` (T21)
  **Scenario**: a suspended pull whose receipt came from an upload; the entitlement is restored; then the same for a pull suspended before any upload.
  **Expected behavior**: a re-run is requested and the check stays `action_required` until a new upload; the second becomes `missing_upload`; no old receipt is turned into `success`.
- **Test name**: `TestLateOwnerApproval` (T14)
  **Scenario**: receipt `owner_approval_missing`; an owner approves the head but the review webhook was lost; the
  approval is for an older SHA; no approval is needed any more (`not_required`).
  **Expected behavior**: one re-run; nothing for the older SHA; one re-run when no approval is needed.
- **Test name**: `TestWithdrawnApprovalEndsSuccess` (T14)
  **Scenario**: a `success` receipt, a `failure` receipt, and a `success` receipt whose policy digest is also stale,
  each while the owner approval is now missing; then a `success` receipt with the approval still granted, an
  `action_required` receipt with the approval missing, and advisory mode. Two passes each.
  **Expected behavior**: the first three publish `owner_approval_missing` once, with the protected paths in
  `Detail`, the run identity of the receipt and the current digests, from inputs resolved a second time inside the
  lease at the pass time, and without `Resolve`; no re-run. The last three publish nothing.
- **Test name**: `TestWithdrawalDecidesInsideTheLease` (T14)
  **Scenario**: a withdrawn `success` receipt of run 9.1; while the pass waits for the lease nothing lands, attempt
  9.2 turns green, the owner approves again, or the inputs become unreadable; separately, the lease is unavailable.
  **Expected behavior**: `owner_approval_missing` over 9.1, then over 9.2; nothing for the new approval; nothing
  and one counted error for the unreadable inputs and for the missing lease.
- **Test name**: `TestStoreOutageDoesNotSuspend` (T21)
  **Scenario**: the resolver reports `Suspended` false because the Store failed, for a pull with a `success` receipt.
  **Expected behavior**: no publication; the receipt stands.

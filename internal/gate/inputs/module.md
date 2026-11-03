# Gate Inputs

**Path**: internal/gate/inputs/
**Parent**: internal/gate/ (gate façade)
**Submodules**: none (leaf)

## Purpose

`inputs` resolves everything the gate is allowed to believe about a pull request: settings, policy, baseline and labels digests from an immutable protected-ref snapshot, changed protected paths, native and App owner decisions on an exact candidate, active objections, the earliest waiver expiry, and the cached entitlement. It owns the durable App decision ledger and bounded audit history. Without it, the gate could trust a pull request's head-tree authority or silently approve a candidate that an owner objected to.

## Functional Responsibilities

- Pin the current protected branch head for a pull-request target when `Target.BaseSHA` is empty, then read `.archfit-app.yaml`, `.archfit.yaml`, `.archfit-baseline.json` and `.archfit-labels.yaml` at that immutable SHA. A supplied `BaseSHA` is the exact settings snapshot shown for an interactive decision.
- Compute raw-byte digests (bare lowercase hex SHA-256) of policy, baseline and labels.
- Find which protected paths the PR changes.
- Decide `Approval`: a current base-version owner who is no agent identity approved this exact head and protected-file candidate, on GitHub or in the App, and no active owner objection remains. A push never approves.
- Return current native/App objections and the bounded App decision history for the PR. Imported native review ID, state, body and identity come from GitHub. A comment, including a mirrored App decision comment, is never a decision.
- Record `approve`, `request_changes`, `withdraw` and explicit `override` actions with CAS against the current candidate and objection versions. A reason is required for request changes, withdrawal and override. An override can cover App or native objections for Archfit only; it never dismisses a GitHub review or bypasses branch protection.
- Preserve valid unchanged-head legacy `approval/` records. A current-candidate withdrawal suppresses the owner's prior App/legacy grant and the exact native approval version present at withdrawal for Archfit; a newly submitted native approval or App approval can grant again. It never changes the GitHub review. A new head or change to protected settings content cannot reuse an old App grant or override.
- Best-effort request missing configured owners/teams without requesting the PR author, known agent logins, already requested principals or every member of a team. Notification state is read without writes on UI/inbox GETs. Requests run on PR events, and notification failure is separate from authority and retried at most three times per pull-request head and protected-settings snapshot.
- `CanDecide` is the owner rule without a pull request's protected paths: who may decide a decision request.
- Record who pushed a head (`RecordPusher`), from a signed `push` delivery, for attribution only.
- Resolve the trusted inputs of a merge group commit (`Target.MergeGroup`): read the default branch's merge queue,
  take the queued pull requests ahead of and at the group as its members, read every protected file at two commit SHAs,
  and accept a changed file only when one owner-approved member made exactly it.
- When approved, also accept the head digest of a changed policy, baseline or labels file, for this PR only.
- Report the earliest upcoming waiver expiry.
- Read the cached `Entitlement` from the Store and derive `Suspended`.
- Own the `Entitlement` record shape and its Store key, so the gate never imports billing code.
- Answer whether the App serves an installation (`Admissions.Admitted`): the account is on the configured list, or an
  operator's `Admission` record exists. Own the `Admission` record shape and its Store key.

## Subdomain Classification

Core. Trusted-input and owner-approval rules are the product's trust model. Volatility: high; approval
rules and the protected path set will change with pilots.

## Encapsulated Knowledge

- The protected path set and how a changed path is detected, for a caller that has the settings of the
  protected ref (`ProtectedPaths`) and for one that has not read them (`MayBeProtected`).
- The digest predicate: what a trusted digest list accepts (`Accepts`), and which digest stands for a list (`First`).
- The decision algorithm: latest native owner review, active native/App objections, exact-head grants, current owner/team membership, agent exclusion, explicit reasoned override, and how legacy App approvals are preserved or superseded.
- The exception rule for owner-approved head policy, baseline and labels digests.
- The merge group rules: who the members are, the snapshot commit `S`, per-file attribution of a change to exactly one
  owner-approved member, and that the group commit itself is never approved.
- Bootstrap behavior when no settings file exists.
- How a Store failure, a missing record, or a malformed record maps to "not suspended".
- The suspension formula and the rule that public repositories are never suspended.
- Which waiver expiry counts as "earliest".
- How the configured account list and the operator's record combine into "admitted", and that a Store failure or a
  record that cannot be read is an error, never "not admitted": the caller does nothing and retries.

## Public Contract

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

// Auto reports whether the configuration approves the account.
func (a *Admissions) Auto(account string) bool

// Admitted reports whether the App serves the installation. owner is its account login, the owner of every
// repository it holds. A configured account needs no Store read. An error means the record could not be read: the
// caller does nothing for the installation and retries, so an approved account loses no event to a Store outage.
func (a *Admissions) Admitted(ctx context.Context, installationID int64, owner string) (bool, error)

// Record reads the operator's admission: (record, true, nil) when approved, (zero, false, nil) when not, and an error
// when the Store fails or the record does not decode. A record without the operator's ID (`by_id <= 0`, for example
// `{}` or `null`) is unreadable, never an approval.
func (a *Admissions) Record(ctx context.Context, installationID int64) (Admission, bool, error)

// Owner is the account login of a repository's "owner/name".
func Owner(fullName string) string
```

```go
// Target is the pull request (or default-branch push, or merge group) whose trusted inputs are resolved.
type Target struct {
    Repo        Repo
    PullRequest int // 0 for a default-branch push and a merge group
    HeadSHA     string
    BaseRef     string // protected ref; the default branch for a push and a merge group
    BaseSHA     string // exact protected-settings snapshot for an interactive PR decision; otherwise pinned by Resolve
    Fork        bool
    // MergeGroup marks HeadSHA as a merge group commit of BaseRef's merge queue; PullRequest is 0. Its inputs are read
    // at immutable commit SHAs and its members' approvals are their own pull requests' (resolveGroup).
    MergeGroup bool
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
    SettingsSHA          string // exact commit whose protected settings were resolved
    PolicyDigests        []string // base digest, plus the head digest when Approval == ApprovalGranted
    BaselineDigests      []string // same rule; empty = no baseline on the protected ref
    LabelsDigests        []string // same rule; empty = no labels file on the protected ref
    ProtectedPaths       []string // protected paths this PR changes
    Approval             Approval
    ApprovedBy           string
    Objections           []Objection // includes native objections explicitly overridden for Archfit, marked Overridden
    DecisionHistory      []DecisionHistory
    EarliestWaiverExpiry time.Time // zero = no waivers
    Suspended            bool
    Entitlement          Entitlement
    // Group is set for a merge-group Target only: what the queue says about the group's members. nil otherwise.
    Group *Group
}

// GroupProblem is why a merge group's inputs cannot be trusted as a whole; "" when they can.
type GroupProblem string

const (
    GroupFork       GroupProblem = "fork"       // a member comes from a fork
    GroupUnverified GroupProblem = "unverified" // the queue, a member or the group commit is not what GitHub says now
    GroupConflict   GroupProblem = "conflict"   // a changed protected file is not exactly one approved member's file
)

// Group is the merge-group part of Trusted. Detail names members and paths ("#12 .archfit.yaml"), never content.
type Group struct {
    Problem GroupProblem
    Detail  string
    // Unapproved names each member that changes a protected path without a current owner approval of its own head,
    // as "#<n> <path>, <path>"; "" when there is none. Approval is then ApprovalMissing.
    Unapproved string
}
```

```go
type Resolver interface {
    // Resolve pins a pull request to the current protected branch head when BaseSHA is empty, then reads trusted inputs
    // at that commit. It never trusts settings, owners, policy, baseline or CODEOWNERS from the candidate head.
    // A merge-group Target takes the snapshot path below.
    Resolve(ctx context.Context, t Target, now time.Time) (Trusted, error)
    // RecordPusher adds the login from a signed push delivery for attribution only; it never approves anything.
    RecordPusher(ctx context.Context, t Target, login string) error
    // Candidate returns the protected paths and head digests shown for an App decision.
    Candidate(ctx context.Context, t Target) (Candidate, error)
    // CanApprove applies the current protected-owner, agent, fork and protected-path rules.
    CanApprove(ctx context.Context, t Target, login string) error
    // CanWithdraw reports whether login has an App or native grant they can withdraw for this candidate.
    CanWithdraw(ctx context.Context, t Target, login string) (bool, error)
    // Decide serializes an authenticated transition by CAS. Request changes, withdrawal and override require a reason.
    // Override IDs must exactly match all App/GitHub objection versions displayed, including earlier overrides for this current candidate.
    Decide(ctx context.Context, t Target, login string, seen map[string]string, action DecisionAction, reason string, overrideIDs []string, now time.Time) error
    // MirrorDecisions retries persisted App reasons with idempotent marker comments; a mirror failure never rolls back authority.
    MirrorDecisions(ctx context.Context, t Target, comments api.Comments) error
    // RequestEligibleReviewers best-effort requests missing configured owners/teams. It returns notification failure only.
    RequestEligibleReviewers(ctx context.Context, t Target, author string, now time.Time) bool
    // Read saved notification state without sending reviewer requests or writing state.
    ReviewerNotificationProblem(ctx context.Context, t Target) bool
    // CanDecide applies the current owner rule without a particular pull-request candidate.
    CanDecide(ctx context.Context, t Target, login string) error
    // Approve is a compatibility façade for Decide(DecisionApprove).
    Approve(ctx context.Context, t Target, login string, seen map[string]string, now time.Time) error
}

// DecisionAction is one authenticated architecture decision. It never merges or publishes a pull request.
type DecisionAction string

const (
    DecisionApprove        DecisionAction = "approve"
    DecisionRequestChanges DecisionAction = "request_changes"
    DecisionWithdraw       DecisionAction = "withdraw"
    DecisionOverride       DecisionAction = "override"
)

// Objection is the latest active request for changes from an eligible non-agent owner. Native fields are imported from GitHub.
type Objection struct {
    ID, Login, Source, Reason, HeadSHA string
    At time.Time
    Overridden bool // an exact reasoned Archfit override; a native GitHub review remains on GitHub
}

// DecisionHistory is one bounded App audit entry, including the objection snapshots an override covered.
type DecisionHistory struct {
    ID, Login string
    Action DecisionAction
    Reason, HeadSHA, BaseSHA string
    At time.Time
    Overrode []string
    OverrodeObjections []Objection
}

// The refusals of App decisions. Other errors are failed authority, GitHub or Store reads/writes.
var (
    ErrActiveObjections = errors.New("inputs: active owner objections require an explicit override")
    ErrOwnerDecisionStale = errors.New("inputs: decision candidate or objection set changed")
    ErrDecisionReason = errors.New("inputs: this architecture decision requires a reason")
    ErrNoApprovalToWithdraw = errors.New("inputs: no active architecture approval by this owner to withdraw")
    ErrDecisionInvalid = errors.New("inputs: unknown or malformed architecture decision")
    ErrDecisionHistoryFull = errors.New("inputs: architecture decision history is full")
)

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

```go
// New returns the Resolver. queues is read only for a merge-group Target.
func New(repos Repos, pulls Pulls, queues MergeQueues, s Store) Resolver
```

```go
// Accepts is the digest predicate of the gate: whether claim is acceptable for one digest list of Trusted.
// An empty list accepts only the empty claim ("nothing trusted, nothing claimed"). A list with entries
// accepts only a claim that is not empty and is in the list, by exact string equality.
func Accepts(trusted []string, claim string) bool

// First returns the first digest of a list of Trusted, or "" for an empty list.
// For every list that Resolve builds, Accepts(trusted, First(trusted)) is true.
func First(trusted []string) string
```

```go
// ProtectedPaths returns the protected path set for the settings of the protected ref, in fixed order:
// PolicyPath, BaselinePath, LabelsPath, Path (the settings file), s.Workflow. An empty s.Workflow is left out.
func ProtectedPaths(s Settings) []string

// MayBeProtected is the same rule for a caller that has not read the settings file (a push event):
// true for every path of ProtectedPaths(Settings{}) and for every path under ".github/workflows/",
// the only directory wire/settings accepts a workflow in. It is a superset of ProtectedPaths(s) for every parsed s.
func MayBeProtected(path string) bool
```

### Digest predicate

`Accepts` is the only implementation of the digest rule on the gate path. `gate/verdict` reports a mismatch when it
is false for the envelope's claim, `gate/sweep` calls a receipt stale when it is false for the receipt's digest, and
`gate` writes into a receipt the claim when it is true and `First` otherwise. Because a receipt therefore always
holds a digest that `Accepts` takes, a fresh receipt is never stale; the sweep's no-re-run-loop guarantee rests on
this one function.

| `trusted` | `claim`       | `Accepts` |
| --------- | ------------- | --------- |
| empty     | `""`          | true      |
| empty     | anything else | false     |
| not empty | `""`          | false     |
| not empty | an entry      | true      |
| not empty | anything else | false     |

Digests are bare lowercase hex; a `sha256:` prefix or another letter case is "anything else".

### Protected paths

`ProtectedPaths` is the only list of the protected paths. `Resolve` step 8 intersects it with the changed files.
`MayBeProtected` is the push form of the same rule: a default-branch push carries changed paths but no settings, and
`Settings.Workflow` lives on the protected ref, so the exact fifth path is unknown without a file read. Matching the
whole `.github/workflows/` directory is a superset **[default]**: it needs no read, and the caller's reaction (a
one-repository sweep) is idempotent, so a false positive costs API calls only. A sixth fixed path is one edit, in
`ProtectedPaths`; `MayBeProtected` and `Resolve` follow. A sixth path that comes from `Settings` also needs its
superset stated in `MayBeProtected`.

### Resolve, step by step

1. Entitlement is read first under its bounded timeout. Missing, invalid or unreadable entitlement data gives the zero value; it never grants owner authority. The outcome and installation ID are logged.
2. `Suspended = t.Repo.Private && !SuspendAfter.IsZero() && !now.Before(SuspendAfter)`.
3. For a PR with empty `Target.BaseSHA`, pin `BaseRef` with `Repos.BranchHead`; a supplied `BaseSHA` is the already selected immutable protected-settings snapshot. A read failure is an error. Set `Trusted.SettingsSHA` to that commit.
4. Read and parse settings at `Trusted.SettingsSHA`. Missing or unparsable settings returns `SettingsFound: false`, no owners and `ApprovalNotRequired`. Read policy, baseline and labels at the same commit; only `api.ErrNotFound` means absent.
5. For a PR, intersect `ChangedFiles` with `ProtectedPaths(settings)` in fixed order. A truncated list counts every protected path. No protected path gives `ApprovalNotRequired`. A fork is `ApprovalMissing`; no fork head file is read.
6. Read current GitHub reviews and the current App decision ledger. Compute native objections, latest eligible App objections, and which objection IDs an exact-current-candidate override covers. `Trusted.Objections` includes overridden objects with `Overridden: true` for display/audit; only unoverridden objects block.
7. Any unoverridden owner objection blocks ordinary approval. Otherwise a current-head native approval or a valid current-candidate App grant gives `ApprovalGranted`. A current-candidate withdrawal suppresses the withdrawing owner's native approval version(s) that existed at the time of withdrawal, for Archfit only; a newly submitted native approval or App approval can grant again. An explicit App override is itself an App grant, including for a GitHub-origin objection; the native review stays on GitHub. No decision may dismiss that native review or alter branch rules.
8. When granted, hash only changed protected policy/baseline/labels files at the candidate head and append those digests. Head contents are never parsed as authority. Settings owners/gate mode remain from the protected snapshot.
9. Compute the earliest upcoming waiver expiry from the protected policy and, only for an approved candidate, the approved head policy.

### Merge group resolution

`Resolve` with `t.MergeGroup` runs steps 1 and 2, then this path; steps 3 to 13 do not run. `t.HeadSHA` is the group
commit `X`, `t.BaseRef` the default branch, `t.PullRequest` 0. `Trusted.Group` is always set.

1. Queue: `MergeQueues.MergeQueue(t.BaseRef)`, read before the snapshot so that a member merged in between is still a
   member and its change is in `S`. Snapshot: `Repos.BranchHead(t.BaseRef)` once, `S`. A failure of either is an error.
2. Settings at `S` (`Repos.File(ref = S, settings.Path)`). Absent or unparsable → `Trusted{SettingsFound: false}`,
   `Group` empty, `Approval` `ApprovalNotRequired`. Every protected file is read by commit SHA, `S` or `X`, never by
   branch name. Policy, baseline and labels digests of `S` and the waivers of the policy at `S` are the starting result.
3. Members: the queue entries from position 1 up to the entry whose `HeadSHA` is `X`. None → `GroupUnverified`. Per
   entry: `CrossRepo` → `GroupFork`; `Pulls.Pull` by number (`api.ErrNotFound` → `GroupUnverified`, other errors are
   errors); `pull.HeadRepo` not this repository → `GroupFork`; closed and not merged (a member that merged since the
   queue read stays a member, and its approval is still checked), `BaseRef` not `t.BaseRef`, or head not the
   entry's `PullHeadSHA` (or that is empty) → `GroupUnverified`. A problem stops the resolution with the `S` digests.
4. Per member: the protected paths its own pull request changes (the rule of step 8, by the settings at `S`), and when
   there are any, its owner approval of its own head by the rules of the pull request (reviews of the head, else the
   App record) and the digests of those paths at its head. A member that changes a protected path without an approval
   → `Group.Unapproved` (`"#<n> <path>, <path>; ..."`), `Approval` `ApprovalMissing`; this is so also when the group's
   file equals `S`'s. Stop with the `S` digests.
5. Per protected path, compare the blob at `S` with the blob at `X` (both absent, or equal bytes, is the same; an empty
   file is present). `Trusted.ProtectedPaths` lists each path that differs or that a member changes. A differing path
   is accepted only when exactly one member changes it and the digest of its head's blob equals that of `X`'s (both
   absent when it deletes the file). No member, several, or another blob → `GroupConflict` (`Detail` lists
   `"<path> <why>"`), `Approval` `ApprovalMissing`; stop with the `S` digests.
6. Otherwise: when any member holds an approval, `Approval` `ApprovalGranted` and `ApprovedBy` is
   `"<login> (#<n>), ..."`; else `ApprovalNotRequired`. The accepted digests, and the waivers, are those of the blobs at
   `X` (equal to `S`'s for a path no member changed). The group commit is never an approval subject.

### Native review and objection algorithm

- Owners and agents come only from `.archfit-app.yaml` at the pinned protected-settings commit. Head settings, CODEOWNERS and repository write/admin permission do not confer authority.
- `Reviews` are processed in GitHub order. `COMMENTED` reviews do not supersede an earlier review; the latest non-comment review by each reviewer wins. Identity, review ID, state, body and submitted time are imported from GitHub. Native objection IDs include a digest of those review-version fields, so an edit is a new objection version.
- An eligible owner review with `APPROVED` on `t.HeadSHA` grants Archfit authorization unless an unoverridden owner objection remains. An owner `CHANGES_REQUESTED` review is an objection even with an empty body. A dismissed review grants nothing and is not an objection. Dismissal does not revoke an unrelated App grant based on the old `SubmittedAt` time.
- A native review or App request for changes blocks ordinary approval. An explicit App `DecisionOverride` may cover a displayed native or App objection, but it is bound to `HeadSHA`, the protected-settings snapshot, candidate digests and every objection ID shown. It changes only Archfit authorization; the native GitHub review is not dismissed and branch protection remains independent.
- Agents never approve or object as architecture owners, even when an owner team contains an agent. Team membership errors fail closed.
- A re-push requires an approval of the new head. App objections remain visible across heads; an override or grant from an old head/settings snapshot does not apply to a new candidate.

### Decisions in the App

The pull-request page resolves authoritative owners and changed protected paths even when the report is absent or rejected. The page binds every form to the current head, protected-settings commit and the raw-byte digests shown.

- `Candidate(t)` reads the protected settings snapshot, `ChangedFiles` ∩ `ProtectedPaths` (a truncated list counts every path), and `policy.Digest` for each changed path at `t.HeadSHA`; `""` means deleted. It never reads a fork head.
- `Decide(t, login, seen, action, reason, overrideIDs, now)` rechecks current owner/team membership, known-agent status, the protected candidate and native/App objections inside a bounded CAS loop. `request_changes`, `withdraw` and `override` require a bounded reason. Ordinary `approve` fails while another active objection exists; a same-owner App objection can be resolved with a new explicit approval. Overrides must name the complete current objection-ID set and exact candidate, including native review IDs.
- The current decision and up to 64 audit actions are stored in `owner-decision/<lowercase repo>/<pull>` under the installation tenant. Each action records owner, action, reason, head SHA, protected settings SHA, digests, timestamp and (for override) objection snapshots. At the cap, the App refuses a new decision rather than dropping history.
- Each stored App reason is mirrored to an idempotent App-authored PR comment. A mirror failure does not roll back the authenticated decision; the App retries it from the same record. Mirrored comments are display/audit data and are never read as authority.
- Existing `approval/<repo>/<pull>/<head>` records are read for compatibility. A current unchanged-head legacy grant remains valid until a newer App request/withdrawal supersedes that owner's grant or current owner/input checks invalidate it. Unknown or malformed new decision records fail closed. See rollback note in the user guide: older deployed App images do not understand negative decision records.
- `RequestEligibleReviewers` best-effort requests only configured direct owners/teams missing from GitHub's requested-reviewer list. It skips the PR author, known agent logins, teams containing the PR author, current-head reviewers, and already requested principals. It never requests every team member, removes unrelated requests, or affects authority. Requests are deduplicated per pull-request head and protected-settings snapshot with at most three attempts for each snapshot; API, membership or permission failures are surfaced as notification problems only.
- `CanDecide(t, login)` applies the current owner/agent/team rule without a particular pull-request candidate.
- A fork never receives an App grant or reviewer request.

### Pusher record

`RecordPusher(t, login)` adds `login` to the set stored at `pusher/<lowercase repo full name>/<t.HeadSHA>` under the
installation tenant, by compare-and-swap (a few attempts, then an error the queue retries). The caller passes the
`sender.login` of a signed `push` delivery. An empty login writes nothing; a login already in the set (ignoring case)
writes nothing; the set holds at most 32 logins. The record is attribution only: nothing reads it to approve. A local
coding agent pushes with its owner's credentials, so a push from an owner account says nothing about who decided.

### Errors

`Resolve` returns an error for any GitHub failure other than "file absent" (including `api.ErrRateLimited`). The zero `Trusted` accompanies an error; callers never publish a pass on an error. A failed entitlement read does not fail `Resolve`; a failed decision-ledger or legacy-approval read does. `Candidate`, `CanApprove`, `CanWithdraw`, `Decide` and `MirrorDecisions` return authority/Store failures as errors. Reviewer-request failures are deliberately reduced to a notification-problem flag and never alter authority.

A protected file that exists but cannot be read is such a failure, never "absent": a file over the API client's
size cap (1 MiB), a 5xx, a timeout. "Absent" is only `api.ErrNotFound`. `Resolve` returns the error, `gate` answers
503, nothing green is published, the Action retries, and if the condition persists the pending check ends
`action_required` (`missing_upload`) through the sweep. An unreadable policy is never treated as "no policy".

## Integrations

### store

- **Counterpart**: internal/store
- **Direction**: inputs depends on store (`Get` of entitlements, pusher, legacy approval and decision records; `CompareAndSwap` of pusher, decision and reviewer-request records/leases)
- **Strength**: contract
- **LCA / Rank / Distance**: root / 2 / 2
- **Volatility**: high (inputs side); store is low
- **Balanced?**: yes
- **Shared knowledge**: tenant = installation ID; key `AdmissionKey`; value is the JSON of `Admission`; `Admissions`
  only calls `Get`. `ErrNotFound` is "not admitted" without an error; any other Store error, a value that does not
  decode and a record with `by_id <= 0` are an error from `Admitted` and `Record`.
  Key `EntitlementKey`; value is the JSON of `Entitlement`.
  `ErrNotFound` is the sentinel of `store` for a missing key; for the entitlement inputs treats it like every other
  Store error, for a pusher record it means "no pusher" and for a legacy approval record "not approved". A malformed or unknown owner-decision record is an error, never a grant.
  Key `pusher/<lowercase repo>/<head sha>`; the value is the pushers' logins, one per line.
  Legacy key `approval/<lowercase repo>/<pull>/<head sha>`; valid records remain readable for unchanged candidates.
  New key `owner-decision/<lowercase repo>/<pull>`; a versioned bounded ledger stores actions, reasons, candidate head/settings SHA/digests, objection snapshots and mirror state. CAS serializes transitions and mirror acknowledgement.
  Reviewer notification key `review-request/<lowercase repo>/<pull>/<head sha>/<protected settings SHA>` plus a short lease; it records bounded attempts and completion per settings snapshot and never authorizes a decision. inputs calls only `Get` and `CompareAndSwap` for these gate-owned records; it never uses `Put`, `Delete`, `Add`, `CompareAndSwapAll`, `DeleteTenant` or `Tenants`. `SystemTenant` is never used here.

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

### wire/settings

- **Counterpart**: internal/wire/settings
- **Direction**: inputs depends on wire/settings
- **Strength**: contract
- **LCA / Rank / Distance**: root / 2 / 2
- **Volatility**: high (inputs side); the file format is moderate
- **Balanced?**: yes
- **Shared knowledge**: file path, strict parsing, owner entry forms (login or `org/team-slug`), `Workflow` path,
  `Agents` and the identity rule of `IsAgent`. `Render` is not used here.

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
    // PolicyAuthority is who may approve a change to the protected files. Absent means human.
    PolicyAuthority *PolicyAuthority `yaml:"policy_authority,omitempty"`
    // RepairAssignee is the one login the default branch's repair issue is assigned to; "" assigns nobody.
    RepairAssignee string `yaml:"repair_assignee,omitempty"`
}

// IsAgent reports whether login is one of s.Agents. Case is ignored, and "name" and "name[bot]" are one identity:
// GitHub names an App's account "slug[bot]" and a file may name either form, so a missed suffix never lets an agent
// through. "" is never an agent.
func (s Settings) IsAgent(login string) bool
```

<!-- contract: Parse, Render — restated from internal/wire/settings/module.md -->
```go
// Parse strictly decodes and validates a settings file. Unknown keys are errors.
func Parse(raw []byte) (Settings, error)

// Render returns canonical YAML for s. For any s returned by Parse, Parse(Render(s)) == s.
func Render(s Settings) []byte
```

### wire/policy

- **Counterpart**: internal/wire/policy
- **Direction**: inputs depends on wire/policy
- **Strength**: contract
- **LCA / Rank / Distance**: root / 2 / 2
- **Volatility**: high (inputs side); the engine file contract is moderate
- **Balanced?**: yes
- **Shared knowledge**: the three engine file paths; `Digest` is the bare lowercase hex SHA-256 of the raw bytes
  (no `sha256:` prefix). For the policy it equals the engine's `comparison.config_hash`. The engine reports no digest
  for baseline or labels; the producer states those in the envelope and `gate/verdict` compares them with the lists
  built here; `Waiver.Expires` is the end of the
  day, UTC; `ApprovedBy` text in a waiver is descriptive and never authentication.

<!-- contract: PolicyPath, BaselinePath, LabelsPath, Digest — restated from internal/wire/policy/module.md -->
```go
// The engine's three trusted input files on the protected ref. Baseline and labels sit beside the policy.
const (
    PolicyPath   = ".archfit.yaml"
    BaselinePath = ".archfit-baseline.json"
    LabelsPath   = ".archfit-labels.yaml"
)

// Digest returns the bare lowercase hex SHA-256 of the raw file bytes. For the policy file it equals the
// engine's comparison.config_hash. The engine reports no digest for baseline or labels; for those the
// producer states Digest of the bytes it used in the envelope, and the App compares it with the protected ref.
func Digest(raw []byte) string
```

<!-- contract: Waiver, Waivers — restated from internal/wire/policy/module.md -->
```go
// Waiver is the subset of an engine waiver the App reads. The engine enforces it; the App only schedules around Expires.
type Waiver struct {
    Rule       string    // engine rule ID
    From, To   string    // endpoint selectors, may be ""
    Reason     string
    ApprovedBy string    // descriptive only, never authentication
    Expires    time.Time // end of the YYYY-MM-DD day, UTC
}

// Waivers extracts the waivers of a policy file. It ignores every other key.
func Waivers(policy []byte) ([]Waiver, error)
```

### github/api

- **Counterpart**: internal/github/api
- **Direction**: inputs depends on github/api (role interfaces `Repos`, `Pulls`, `MergeQueues`)
- **Strength**: contract
- **LCA / Rank / Distance**: root / 2 / 2
- **Volatility**: high (inputs side); the client is low
- **Balanced?**: yes
- **Shared knowledge**: `File` takes any ref (branch name or SHA) and returns `api.ErrNotFound` for an
  absent file. `ErrRateLimited` is the sentinel returned after bounded backoff. `Review.CommitSHA` is the
  head SHA the review was submitted on. `ChangedFiles` lists both the old and the new path of a rename, and
  sets `truncated` when GitHub cut the list off. `PullByHead` and `OpenPulls` are not
  called here; `BranchHead` pins each PR to an immutable protected snapshot; `Pull` is also read for merge groups. `MergeQueue` returns the entries of a branch's queue in
  position order, none when it has no queue; a missing Merge queues permission (`api.ErrQueueForbidden`) and a queue
  over the read bound (`api.ErrQueueTooLong`) are errors, never an empty queue. Used methods: `Repos.File`,
  `Pulls.ChangedFiles`, `Pulls.Reviews`, `Pulls.TeamMember`, `Repos.BranchHead`, reviewer request roles and comment mirroring; and for a merge group `Pulls.Pull`,
  `MergeQueues.MergeQueue`. `Installation` is not used here.

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

<!-- contract: MergeQueues, QueueEntry — restated from internal/github/api/module.md -->
```go
type QueueEntry struct {
    Position    int    // 1 is the front of the queue
    HeadSHA     string // the entry's merge group commit
    BaseSHA     string
    PullRequest int
    PullHeadSHA string // the pull request head the entry was built from
    CrossRepo   bool   // the pull request comes from a fork
}

type MergeQueues interface {
    MergeQueue(ctx context.Context, r RepoRef, branch string) ([]QueueEntry, error)
}
```

### Consumers (inbound)

- **internal/gate** (parent): functional, gate / 1 / 1, high, balanced. Constructs the resolver, calls `Resolve` per upload/webhook, re-publishes the gate-owned decision façade to `control/ui`, mirrors decisions and re-evaluates required/advisory state and queued groups. It never routes Store decision writes through the upload/OIDC path.
- **internal/gate/verdict**: model, gate / 1 / 1, high, balanced. Reads `Trusted` (`Group` included); calls `Accepts` for the digest checks.
- **internal/gate/feedback**: model, gate / 1 / 1, high, balanced. Reads `Target` and `Trusted`.
- **internal/gate/ingest** and **internal/gate/sweep** (through the gate façade): contract, gate / 1 / 1, high,
  balanced. Ask `Admitted` for the resolved installation and owner, and treat an error as "retry".
- **internal/gate/sweep**: contract, gate / 1 / 1, high, balanced. Calls `Resolver.Resolve`; calls `Accepts` for the
  stale test and `First` for the receipt of a publication without a report.
- **internal/control/account** (through the gate façade): contract, root / 2 / 2, moderate, balanced.
  Writes the JSON of `Entitlement` under `EntitlementKey`; writes and deletes the JSON of `Admission` under
  `AdmissionKey`.

## Change Vectors

- A new protected path, or a different approval rule (two owners, required team).
- A different baseline path or an additional trusted file (labels).
- A changed suspension formula or entitlement field.
- Another way to approve an installation (a different configured list, a self-service flow).
- Caching of protected-ref reads inside one process, if rate limits demand it.
- A queue on another branch, or a queue over 1,000 entries (`api.ErrQueueTooLong` today).

## Constraints and Invariants

- Never trust the head tree. Settings, owners, policy, baseline and workflow path come from the current protected-ref commit (`BaseSHA`/`SettingsSHA`), never the candidate head. The single exception is the digest of a changed head policy, baseline or labels file after the exact candidate is authorized. `Candidate` hashes protected head bytes for display/binding; it never parses them. CODEOWNERS is not an authority source.
- For a fork, no head file is read and approval is never granted. A merge group with a fork member is `GroupFork`.
- A merge group's membership comes from GitHub's queue and the pull requests read again by number; nothing the uploader
  states selects a member, and the `gh-readonly-queue/...` branch name is never read. A merge group commit is never an
  approval subject: only a member's own head is. An accepted group digest is the blob at `X`, once exactly one
  approved member's head holds it.
- App grants/overrides bind the exact head SHA, protected settings SHA, digest map and objection-ID set. New heads, protected-settings snapshots or objections cannot inherit stale overrides. Active App objections survive a push until a later explicit decision resolves them.
- One digest predicate and one protected path list. No other gate module compares a digest with a trusted list or
  names a protected path; they call `Accepts`, `First`, `ProtectedPaths` and `MayBeProtected`. The literal
  `.github/workflows/` exists here only; it restates the directory rule of `wire/settings` (owner: `wire/settings`,
  which exports no constant for it). If the two drift, `MayBeProtected` stops being a superset and a workflow push
  is noticed by the hourly sweep instead of at once. Remove the literal when `wire/settings` exports the directory.
- The App never writes the baseline or policy. On GitHub, inputs is read-only except for the narrow reviewer-request capability, which is notification-only. On Store it writes only pusher records, the bounded owner-decision ledger, and per-head/protected-settings-snapshot reviewer notification state/leases; the admission record is read-only. Decision writes are reachable only from the authenticated UI façade, never from report upload/OIDC.
- A push never approves, and an agent identity never approves, on GitHub or in the App.
- A Store outage, missing record, or malformed record never means non-payment: `Suspended` is false.
- Public repositories are never suspended.
- Admission fails closed by doing nothing and retrying: a Store error, an undecodable record or one without the
  operator's ID is an error from `Admitted`, never "admitted" and never a plain refusal. This is the opposite of the
  entitlement rule, because serving an unapproved account is the risk, and a refusal would drop an approved account's
  event during an outage. Callers do nothing for the installation and make the work retryable.
- Admission is per installation, keyed by the account login of the repository owner; the record is read-only here.
- Fail closed on GitHub errors: return the error; never return partial `Trusted` that could pass.
- The tenant is `t.Repo.InstallationID`, which callers build from verified OIDC claims or a verified
  webhook; never from a client-supplied ID.
- No process cache. Decision history and reviewer-request dedupe are explicit tenant-scoped persisted state. Reviewer requests are serialized per pull-request head and protected-settings snapshot, idempotent against GitHub's current request list, and bounded to three attempts for each snapshot; failure never changes authority.
- Logs (`log/slog` default logger **[default]**, because the constructor carries none): installation ID,
  repository ID, digests, outcomes, timings. Never file content, owner lists, or reviewer names.
- Imports: standard library, `wire/settings`, `wire/policy`, `github/api`, `store`. Never `control`, `app`, LLM or UI packages.
- `now` is a parameter; inputs never reads the clock.

## Test Specification

Fakes: in-memory `Repos`, `Pulls` and `MergeQueues` with per-ref file maps and call recording; `store.NewMemory` plus a
failing `Store` stub. `NewMemory` is the in-process Store constructor of `internal/store`.

### Unit Tests

- **Test name**: `TestProtectedPathsIntersection`
  **Scenario**: table of changed-file lists: none protected; policy only; baseline and workflow; settings; a custom `Settings.Workflow` path; a policy file renamed away (old path listed); a list without a protected path but `truncated == true`.
  **Expected behavior**: `ProtectedPaths` in fixed order; the policy path for the rename; all five for the truncated list, so approval is required. A changed `.archfit-labels.yaml` is listed like the other four.
- **Test name**: `TestAcceptsTruthTable`
  **Scenario**: every combination of the lists empty, one digest, two digests and a list holding `""` with the claims `""`, the first entry, the second entry, an unknown digest, a `sha256:`-prefixed entry and an upper-case entry.
  **Expected behavior**: the table of "Digest predicate"; a list holding `""` does not accept `""`. `First` is `""` for the empty list and entry 0 otherwise, and `Accepts(list, First(list))` is true for every list without an empty entry.
- **Test name**: `TestProtectedPathSet`
  **Scenario**: iterate `ProtectedPaths(s)` for a parsed `Settings`; the zero `Settings`; paths under `.github/workflows/`; near misses (`docs/.archfit.yaml`, `.github/workflows`, `.github/dependabot.yml`, `""`).
  **Expected behavior**: five paths in fixed order, four for the zero `Settings`; `MayBeProtected` is true for every path of the set and for every workflow file, false for the near misses; a pull request that changes any single path of the set gets exactly that path in `Trusted.ProtectedPaths` and `ApprovalMissing`. The test names no path itself, so a sixth path in `ProtectedPaths` is covered without a test edit.
- **Test name**: `TestApprovalAlgorithm`
  **Scenario**: table: owner approved head; owner approved an older SHA; non-owner approved head; owner approved then requested changes; owner approved then commented; owner approval dismissed; login case differs; team owner with member and with non-member; two owners, second approves; an owner listed as an agent; the `[bot]` account of an agent; an agent in an owner team; an owner approving after an agent.
  **Expected behavior**: granted, missing, missing, missing, granted, missing, granted, granted, missing, granted with the second owner as `ApprovedBy`; missing three times, no team lookup for the agent; granted with the owner.
- **Test name**: `TestReviewerRequestsOnlyMissingEligiblePrincipalsAndDedupesHead`, `TestReviewerRequestsNewOwnerAfterSettingsChangeAtSameHead`, `TestReviewerRequestsSkipTeamContainingPRAuthor`, `TestReviewerRequestFailureIsBoundedAndDoesNotGrantAuthority`
  **Scenario**: a changed protected candidate with an existing CODEOWNERS request, a current-head reviewer, the PR author, an agent, an owner team and a failing notification endpoint; then owner settings change while the PR head stays fixed.
  **Expected behavior**: only missing eligible configured principals are requested; the PR author, known agent and unrelated requests are untouched; notifications are bounded and deduped by head plus settings SHA, and a new snapshot requests newly configured owners only; notification failure leaves `ApprovalMissing` and never changes authority.
- **Test name**: `TestCandidate`
  **Scenario**: nothing protected changed; baseline and settings; a labels file deleted at the head; a truncated list;
  a fork; no settings on the protected ref.
  **Expected behavior**: no paths; both paths with their head digests; the path with `""`; every path, an absent
  workflow `""`; the path and no digest, no head read; nothing.
- **Test name**: `TestRequestChangesAndExplicitOverride`, `TestNativeReviewObjectionAndDismissalAreIndependentOfAppGrant`, `TestEditingOverriddenNativeReviewCreatesNewObjectionVersion`, `TestWithdrawalSuppressesPriorNativeApprovalWithoutChangingGitHubReview`, `TestOverrideIsBoundToNativeReviewVersions`, `TestOverrideDoesNotCarryToANewProtectedSettingsSnapshot`
  **Scenario**: an App objection after a grant; ordinary approve; wrong and exact override IDs; a native `CHANGES_REQUESTED` review with an empty body; an explicit override followed by an unchanged native review; a new native review; a new protected settings SHA.
  **Expected behavior**: objections block ordinary approval; overrides apply to either App or GitHub objections only for the exact head/settings/digests/objection IDs. An override grants Archfit while the native review remains `CHANGES_REQUESTED`; GitHub is not mutated. New objection versions and new protected-settings snapshots are not covered by stale overrides.
- **Test name**: `TestAppApprovalRefusals`
  **Scenario**: digests changed or a path left out; a non-owner; an owner of the head settings only; no login; an
  owner listed as an agent; the `[bot]` account of an agent; an agent in an owner team; a fork; nothing protected
  changed; no settings.
  **Expected behavior**: `ErrDigestsChanged` twice, `ErrNotOwner` three times, `ErrAgentIdentity` three times,
  `ErrForkPull`, `ErrNothingToApprove` twice, from `Approve` and from `CanApprove`; no record; `Resolve` not granted.
- **Test name**: `TestCanDecide`
  **Scenario**: an owner; a team member; a non-owner; an owner of the head settings only; no login; an owner listed as
  an agent; no settings on the protected ref.
  **Expected behavior**: nil twice, `ErrNotOwner` four times, `ErrAgentIdentity` once.
- **Test name**: `TestAgentNeverApproves`
  **Scenario**: an agent named in `owners` and `agents`, and an agent in an owner team, approve on GitHub and in the App.
  **Expected behavior**: missing both ways, `ErrAgentIdentity`, and no team lookup.
- **Test name**: `TestAppApprovalVoidedByTheProtectedRef`
  **Scenario**: after an App approval: the approver leaves the owners; becomes an agent; leaves the owner team; the
  pull request changes another protected path, or no longer one the owner approved.
  **Expected behavior**: missing each time; the head digest is not accepted.
- **Test name**: `TestAppApprovalHistoryRecordsEveryOwnerDecision`, `TestDecisionCASKeepsAConcurrentApproval`
  **Scenario**: repeated approvals and a second owner action racing between a read and CAS.
  **Expected behavior**: each authenticated action remains in bounded history; CAS preserves both owners' records and re-evaluates the current objection set after a lost write.
- **Test name**: `TestLegacyApprovalIsPreservedAndExplicitlyWithdrawn`, `TestUnknownDecisionRecordFailsClosed`, `TestMirrorDecisionsEscapesReasonsAndRetriesUnknownCreateOutcome`, `TestMirrorFailureLeavesDecisionSavedAndUnpublished`
  **Scenario**: a valid released approval record; an unknown version; a public reason containing HTML/mentions; an App comment create that landed with an unknown response; mirror API failure.
  **Expected behavior**: valid legacy grant remains usable until a newer negative action; unknown decision state fails closed; mirrored text is escaped and idempotent; mirror failure keeps the authenticated decision saved and retryable.
- **Test name**: `TestPusherRecordIsBounded`
  **Scenario**: more than 32 logins recorded for one head.
  **Expected behavior**: the record holds 32.
- **Test name**: `TestSuspendedFormula`
  **Scenario**: table over private/public × `SuspendAfter` zero / future / equal to now / past.
  **Expected behavior**: true only for private with `SuspendAfter` at or before `now`.
- **Test name**: `TestEarliestWaiverExpiry`
  **Scenario**: no waivers; one future; one expired and one future; all expired; malformed `expires`.
  **Expected behavior**: zero; that date; the future date; zero; zero with one logged outcome.

- **Test name**: `TestAdmitted`
  **Scenario**: table: configured account (also with spaces and in another case); an operator's record; no record; Store error; record that does not decode; a record without the operator's ID (`{}`); `null`; an empty configured login.
  **Expected behavior**: admitted for the configured accounts without a Store read and for the record; not admitted, no error, for a missing record and an empty configured login; an error (and not admitted) for a Store failure, a record that does not decode, `{}` and `null`.
- **Test name**: `TestAdmissionRecordTellsMissingFromUnreadable`
  **Scenario**: `Record` with `ErrNotFound`, then with a failing Store.
  **Expected behavior**: (zero, false, nil) for the missing record; not found and an error for the failure.
- **Test name**: `TestOwner`
  **Scenario**: `acme/shop`, `acme`, `""`.
  **Expected behavior**: `acme`, `acme`, `""`.

### Integration Contract Tests

- **Test name**: `TestResolveCleanPullRequest`
  **Scenario**: settings, policy and baseline on base; PR changes only code.
  **Expected behavior**: `SettingsFound`, `SettingsSHA` equals the immutable protected branch head, one policy digest equal to `policy.Digest(raw)` (bare hex), one baseline digest, one labels digest, no protected paths, `ApprovalNotRequired`; `Reviews` was never called.
- **Test name**: `TestResolveApprovedPolicyChange`
  **Scenario**: PR changes `.archfit.yaml`; an owner approved the head SHA.
  **Expected behavior**: `ApprovalGranted`, `ApprovedBy` set, `PolicyDigests` = base digest then head digest; `BaselineDigests` and `LabelsDigests` unchanged. The same PR changing only the labels file gives two labels digests and one policy digest.
- **Test name**: `TestResolvePushTarget`
  **Scenario**: `PullRequest == 0`, `BaseRef` = default branch.
  **Expected behavior**: digests from that branch; no `ChangedFiles` or `Reviews` call; `ApprovalNotRequired`.
- **Test name**: `TestResolveBootstrap`
  **Scenario**: no settings file at `BaseRef`; then an unparsable one.
  **Expected behavior**: `SettingsFound` false, empty digests, no error, entitlement still read.
- **Test name**: `TestEntitlementRecordShape`
  **Scenario**: a record written as `control/account` writes it (JSON with the four snake_case fields).
  **Expected behavior**: decoded field by field into `Trusted.Entitlement`; key is `entitlement` under `Tenant(installationID)`.

### Boundary Tests

- **Test name**: `TestHeadTreeIsNeverRead`
  **Scenario**: every unapproved and fork scenario with a recording `Repos` fake.
  **Expected behavior**: no `File` call with `ref == HeadSHA`; in the approved scenario only the changed policy, baseline or labels path is read at head.
- **Test name**: `TestStoreFailureIsNotSuspension` (T21)
  **Scenario**: Store returns `ErrNotFound`, a generic error, blocks past the timeout, returns invalid JSON.
  **Expected behavior**: zero `Entitlement`, `Suspended` false, `Resolve` succeeds; one log line with the outcome and no payload.
- **Test name**: `TestGitHubErrorsFailClosed`
  **Scenario**: `File`, `ChangedFiles`, `Reviews`, `TeamMember` each return a generic error and `ErrRateLimited`; `File` returns the client's oversize error for the policy, the baseline and the settings file.
  **Expected behavior**: `Resolve` returns an error matching with `errors.Is`; zero `Trusted`.
- **Test name**: `TestTenantIsolation`
  **Scenario**: entitlement stored for installation A; resolve a repository of installation B.
  **Expected behavior**: B sees the zero entitlement; the Store was read only under B's tenant.
- **Test name**: `TestLogsCarryOnlyIdentifiers`
  **Scenario**: files with marker strings in owners, waiver reasons and policy text; an approval recorded in the App by a
  marked login; an approval record that does not decode.
  **Expected behavior**: no marker string in any log line.

- **Test name**: `TestImportsStayInsideAllowedSet`
  **Scenario**: `go list` the imports of the package's non-test files.
  **Expected behavior**: only the standard library, `wire/settings`, `wire/policy`, `github/api` and `store`.

### Behavior Tests

- **Test name**: `TestMergeGroupInputs`
  **Scenario**: table over a two-entry queue: a group that changes no protected file; an approved policy change that is the combined blob (also approved by a team member); an unapproved member change, also one the group reverts; an agent's approval, an approval of an older head, a withdrawn approval; two approved members changing one file; a combined file that is not the approved head's; a protected change no member makes; an approved deletion of the baseline; a member behind the group; a fork member (flagged by the queue, or only by the pull request); a member that merged since the queue read; a member whose head moved, into another base, or closed; a group commit the queue no longer lists.
  **Expected behavior**: `S`'s digests and `ApprovalNotRequired`; `X`'s digests, `ApprovalGranted`, `ApprovedBy` naming the member; `Group.Unapproved` and `ApprovalMissing`; same, with no approval counted; `GroupConflict`; `GroupConflict`; `GroupConflict`; the deletion accepted; the member behind does not count; `GroupFork`; `GroupUnverified`; `GroupUnverified`.
- **Test name**: `TestMergeGroupWaiversComeFromTheAcceptedPolicy`, `TestMergeGroupSoleOwnerApprovesInTheApp`, `TestMergeGroupReadFailuresAreErrors`, `TestMergeGroupWithoutSettingsIsNotOnboarded`
  **Scenario**: a waiver in the policy at `X`; an owner approval recorded in the App; a failing queue, snapshot, file or pull request read; no settings file at `S`.
  **Expected behavior**: the expiry of the accepted policy; the member counts as approved; an error and the zero `Trusted`; `SettingsFound` false.
- **Test name**: `TestHeadEditsCannotWeakenInputs` (T12)
  **Scenario**: the PR edits policy, baseline, settings (adds the author as owner, switches to advisory) and the workflow, with no owner approval.
  **Expected behavior**: digests, owners, gate mode and workflow path are the base values; `ApprovalMissing`; all changed protected paths listed; the head baseline and labels digests are never accepted (no blanket re-baseline, no head-edited labels).
- **Test name**: `TestOrdinaryReviewConfersNothing` (T14)
  **Scenario**: a non-owner approves; the head adds that reviewer to `owners` and to CODEOWNERS.
  **Expected behavior**: `ApprovalMissing`; head digests not accepted.
- **Test name**: `TestNewCommitInvalidatesApproval` (T14)
  **Scenario**: an owner approves SHA 1; the PR is re-pushed to SHA 2; then the owner approves SHA 2.
  **Expected behavior**: missing for SHA 2 until the second approval; then granted with the SHA 2 policy digest.
- **Test name**: `TestOwnerApprovedBaselinePullRequest` (T15)
  **Scenario**: a PR that replaces `.archfit-baseline.json`, approved by an owner on its head.
  **Expected behavior**: `BaselineDigests` holds base and head digests; without the approval only the base digest.
- **Test name**: `TestForkNeverGetsHeadInputs`
  **Scenario**: fork target that changes the policy, with an owner approval present.
  **Expected behavior**: `ApprovalMissing`; no head read; base digests only.
- **Test name**: `TestSuspensionFacts` (T21)
  **Scenario**: private repository past `SuspendAfter`; the same record for a public repository; the record replaced by a later `SuspendAfter` (restored).
  **Expected behavior**: suspended; not suspended; not suspended. `Entitlement.Reason` and `Notice` pass through unchanged.

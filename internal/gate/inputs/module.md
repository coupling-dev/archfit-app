# Gate Inputs

**Path**: internal/gate/inputs/
**Parent**: internal/gate/ (gate façade)
**Submodules**: none (leaf)

## Purpose

`inputs` resolves everything the gate is allowed to believe about a pull request: settings, policy,
baseline and labels digests from the protected ref, the changed protected paths, the architecture owner's approval
on the exact head SHA (a GitHub review, or an approval recorded in the App), the earliest waiver expiry, and the
cached entitlement. Without it, the gate would
compare a report with whatever the head tree says, and a contributor could weaken the gate in the same
PR that violates it.

## Functional Responsibilities

- Read `.archfit-app.yaml`, `.archfit.yaml`, `.archfit-baseline.json` and `.archfit-labels.yaml` at the target's `BaseRef`.
- Compute raw-byte digests (bare lowercase hex SHA-256) of policy, baseline and labels.
- Find which protected paths the PR changes.
- Decide `Approval`: a base-version owner who is no agent identity approved this exact head SHA, on GitHub or in the
  App, or not. A push never approves.
- Record an owner's approval made in the App (`Approve`), bound to the head SHA and to the digests of the protected
  files the owner was shown (`Candidate`), and say who may make one (`CanApprove`).
- Record who pushed a head (`RecordPusher`), from a signed `push` delivery, for attribution only.
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
- The approval algorithm: which reviews count, how owners and teams are matched, latest-review-wins, that an agent
  identity of the settings (`Settings.IsAgent`) never approves, and when an approval recorded in the App holds.
- The exception rule for owner-approved head policy, baseline and labels digests.
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
func New(repos Repos, pulls Pulls, s Store) Resolver
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

1. Entitlement (always first, never fails the call): `Store.Get(Tenant(t.Repo.InstallationID), EntitlementKey)`
   under a 2 second timeout **[default]**. `store.ErrNotFound`, any other error, a timeout, or JSON that
   does not decode gives the zero `Entitlement`, which means "not suspended" **[default]**. Log the outcome
   (`found`, `missing`, `unreadable`) and the installation ID only. Recovery of a missing record is outside the
   gate: `control`'s sweep backfills it (Observe + Refresh for every installation), so a missed installation
   webhook delays the trial start by at most one sweep interval. A Store outage never fails the entitlement
   read, so it never fails `Resolve` by itself; only a failed read of an approval record does (below). That
   publishing then also needs the Store for its lease is `gate/feedback`'s rule, not this module's.
2. `Suspended = t.Repo.Private && !SuspendAfter.IsZero() && !now.Before(SuspendAfter)`.
3. Settings: `Repos.File(ref = t.BaseRef, settings.Path)`. `api.ErrNotFound` → return
   `Trusted{SettingsFound: false}` with only `Entitlement` and `Suspended` filled; `Approval` is
   `ApprovalNotRequired`. `settings.Parse` error → same result (a broken settings file is a missing one) **[default]**.
4. Policy: `Repos.File(t.BaseRef, policy.PolicyPath)`. Found → `PolicyDigests = [policy.Digest(raw)]`.
   Absent (`api.ErrNotFound` only) → empty list; the verdict then reports `policy_missing`.
5. Baseline: `Repos.File(t.BaseRef, policy.BaselinePath)`. Found → `BaselineDigests = [policy.Digest(raw)]`. Absent → empty.
   Labels: the same with `policy.LabelsPath` and `LabelsDigests`. inputs never parses either file.
6. Waivers: `policy.Waivers(basePolicy)`. An error gives no waivers and one log line with the outcome;
   the engine enforces waivers anyway.
7. `t.PullRequest == 0` (push): stop. `ProtectedPaths` empty, `ApprovalNotRequired`.
8. `Trusted.ProtectedPaths` = `Pulls.ChangedFiles` ∩ `ProtectedPaths(settings)` ({`policy.PolicyPath`,
   `policy.BaselinePath`, `policy.LabelsPath`, `settings.Path`, `Settings.Workflow`}), in that fixed order. When
   `ChangedFiles` reports `truncated == true`, any path may have changed: all five paths count as changed (fail closed).
9. `ProtectedPaths` empty → `ApprovalNotRequired`. Stop.
10. `t.Fork` → `ApprovalMissing`. Stop. No head file is read for a fork.
11. Approval from `Pulls.Reviews` (algorithm below). When no owner approved there, the approval recorded in the App for
    `t.HeadSHA` (below). Neither → `ApprovalMissing`. Stop. Who pushed the head is never read here.
12. Granted → `ApprovedBy` = the approving login. If the policy path is in `ProtectedPaths`: read it at
    `ref = t.HeadSHA`, append its digest to `PolicyDigests` (absent at head → nothing appended). Same for
    the baseline and the labels file. Head waivers are parsed from the approved head policy.
13. `EarliestWaiverExpiry` = the earliest `Waiver.Expires` that is not before `now`, over the base policy
    and the approved head policy; zero when there is none **[default: expired waivers are skipped so that
    an old expiry does not hide the next one]**.

### Approval algorithm

- Owners are `Settings.Owners` of the file at `BaseRef`. Nothing at head (settings, CODEOWNERS) is read.
- Reviews are taken in the order GitHub returns them (chronological). `COMMENTED` reviews are ignored
  **[default]**; of the rest, the latest review per reviewer wins.
- An agent identity (`Settings.IsAgent` of the base settings: case ignored, `name` and `name[bot]` one identity)
  never approves, before any owner check: also when `owners` names it, or an owner team holds it. No team lookup is
  made for it. A local coding agent acts with its owner's credentials, so naming that account in `agents` is the
  owner's way to keep it from approving.
- A reviewer approves when that latest review has `State == "APPROVED"` and `CommitSHA == t.HeadSHA`.
  A `DISMISSED` or `CHANGES_REQUESTED` latest review does not approve. A re-push changes the head SHA, so
  every earlier approval stops counting.
- A reviewer is an owner when the login equals an owner entry without `/` (case-insensitive), or
  `Pulls.TeamMember(repo, entry, login)` is true for an entry with `/`. Team lookups happen only for
  reviewers with a qualifying approval.
- The first approving owner in review order is `ApprovedBy`.
- An ordinary approving review by a non-owner confers nothing.

### Approval in the App

A design rule of the product: anyone may propose a policy change, only an architecture owner can authorize it, and
GitHub does not let an author approve their own pull request. A sole owner would never get a change through. So an
owner may approve the exact head in the App instead (the Approve button of `control/ui`'s pull request page).

- `Candidate(t)`: the settings of `BaseRef` (none or unparsable → no paths), `ChangedFiles` ∩ `ProtectedPaths` by the
  rule of `Resolve` step 8 (a truncated list counts every path), and for each path `policy.Digest` of the file at
  `t.HeadSHA`, `""` when the head deletes it. The bytes are hashed, never parsed. A fork gets its paths and no digest:
  no head file of a fork is read.
- `CanApprove(t, login)` refuses, in this order: no protected path changed (`ErrNothingToApprove`), a fork
  (`ErrForkPull`), an agent identity of the base settings (`ErrAgentIdentity`), a login that is not an owner by the
  rule of a reviewer, team membership included (`ErrNotOwner`). Owners and agents come from `BaseRef` only.
- `Approve(t, login, seen, now)` runs the same checks, computes `Candidate` again and refuses when `seen` is not equal
  to its digests (`ErrDigestsChanged`): what the owner looked at is not what the head holds now. Then it writes
  `{login, head_sha, digests, at}` as JSON at `approval/<lowercase repo full name>/<t.HeadSHA>` under the installation
  tenant, by `CompareAndSwap` from nothing. A record already there, by any owner, is success and is never replaced:
  the first approval of a head is the one kept. One that no longer holds (below) therefore stays void
  for that head; a new commit, or an approving review on GitHub, is the way on.
- `Resolve` step 11 reads the record when no review approved. It holds only when the recorded login is still an
  owner and no agent under the current base settings, and the recorded digests equal the `Candidate` digests computed
  now. The key holds the head SHA, so a new commit never finds it. A base that moved so that the pull request changes
  another set of protected paths gets other digests, so the record no longer holds.
- A failed read of the record is an error: the decision is retried, never published on a guess. A missing record is
  "not approved"; a record that does not decode approves nothing (one log line, outcome `unreadable`).
- A fork never gets here: `Resolve` stops at step 10, and `Approve` refuses it.

### Pusher record

`RecordPusher(t, login)` adds `login` to the set stored at `pusher/<lowercase repo full name>/<t.HeadSHA>` under the
installation tenant, by compare-and-swap (a few attempts, then an error the queue retries). The caller passes the
`sender.login` of a signed `push` delivery. An empty login writes nothing; a login already in the set (ignoring case)
writes nothing; the set holds at most 32 logins. The record is attribution only: nothing reads it to approve. A local
coding agent pushes with its owner's credentials, so a push from an owner account says nothing about who decided.

### Errors

`Resolve` returns an error for any GitHub failure other than "file absent" (including
`api.ErrRateLimited`, matchable with `errors.Is`). The zero `Trusted` accompanies an error. The caller
must not publish a passing result on error; the pending check stays. A failed Store read of the entitlement never
returns an error; a failed Store read of an approval record does, because "not approved" would publish a guess.
`Candidate`, `CanApprove` and `Approve` return GitHub and Store failures as errors too, never as a refusal.

A protected file that exists but cannot be read is such a failure, never "absent": a file over the API client's
size cap (1 MiB), a 5xx, a timeout. "Absent" is only `api.ErrNotFound`. `Resolve` returns the error, `gate` answers
503, nothing green is published, the Action retries, and if the condition persists the pending check ends
`action_required` (`missing_upload`) through the sweep. An unreadable policy is never treated as "no policy".

## Integrations

### store

- **Counterpart**: internal/store
- **Direction**: inputs depends on store (`Get` of the entitlement and of the pusher and approval records,
  `CompareAndSwap` of a pusher or approval record)
- **Strength**: contract
- **LCA / Rank / Distance**: root / 2 / 2
- **Volatility**: high (inputs side); store is low
- **Balanced?**: yes
- **Shared knowledge**: tenant = installation ID; key `AdmissionKey`; value is the JSON of `Admission`; `Admissions`
  only calls `Get`. `ErrNotFound` is "not admitted" without an error; any other Store error, a value that does not
  decode and a record with `by_id <= 0` are an error from `Admitted` and `Record`.
  Key `EntitlementKey`; value is the JSON of `Entitlement`.
  `ErrNotFound` is the sentinel of `store` for a missing key; for the entitlement inputs treats it like every other
  Store error, for a pusher record it means "no pusher" and for an approval record "not approved".
  Key `pusher/<lowercase repo full name>/<head sha>`; the value is the pushers' logins, one per line.
  Key `approval/<lowercase repo full name>/<head sha>`; the value is the JSON `{login, head_sha, digests, at}`.
  inputs calls only `Get` and, for those two records, `CompareAndSwap`; never `Put`, `Delete`, `Add`,
  `CompareAndSwapAll`, `DeleteTenant` or `Tenants`. `SystemTenant` is never used here.

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
- **Direction**: inputs depends on github/api (role interfaces `Repos`, `Pulls`)
- **Strength**: contract
- **LCA / Rank / Distance**: root / 2 / 2
- **Volatility**: high (inputs side); the client is low
- **Balanced?**: yes
- **Shared knowledge**: `File` takes any ref (branch name or SHA) and returns `api.ErrNotFound` for an
  absent file. `ErrRateLimited` is the sentinel returned after bounded backoff. `Review.CommitSHA` is the
  head SHA the review was submitted on. `ChangedFiles` lists both the old and the new path of a rename, and
  sets `truncated` when GitHub cut the list off. `BranchHead`, `PullByHead`, `OpenPulls` and `Pull` are not
  called here. Used methods: `Repos.File`, `Pulls.ChangedFiles`, `Pulls.Reviews`,
  `Pulls.TeamMember`. `Installation` is not used here.

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

### Consumers (inbound)

- **internal/gate** (parent): functional, gate / 1 / 1, high, balanced. Constructs the resolver, calls `Resolve`
  per upload and per webhook, builds receipt digests with `Accepts` and `First`, decides the protected-push trigger
  with `MayBeProtected`, re-publishes `Entitlement` and `EntitlementKey` to `control/account`, and `Target`,
  `Candidate` and the refusals of an approval to `control/ui`, whose pull request page reaches `Candidate`,
  `CanApprove` and `Approve` only through the façade.
- **internal/gate/verdict**: model, gate / 1 / 1, high, balanced. Reads `Trusted`; calls `Accepts` for the digest checks.
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

## Constraints and Invariants

- Never trust the head tree. Settings, owners, policy, baseline and the workflow path come from
  `BaseRef` only. The single exception: the digest of a head policy, baseline or labels file for a PR whose exact head
  SHA a base-version owner approved. `Candidate` hashes the changed protected files at the head to show and bind what
  an owner approves; nothing in them is believed. CODEOWNERS is never read.
- For a fork, no head file is read and approval is never granted.
- Approval is bound to `t.HeadSHA`. Any new commit invalidates it.
- One digest predicate and one protected path list. No other gate module compares a digest with a trusted list or
  names a protected path; they call `Accepts`, `First`, `ProtectedPaths` and `MayBeProtected`. The literal
  `.github/workflows/` exists here only; it restates the directory rule of `wire/settings` (owner: `wire/settings`,
  which exports no constant for it). If the two drift, `MayBeProtected` stops being a superset and a workflow push
  is noticed by the hourly sweep instead of at once. Remove the literal when `wire/settings` exports the directory.
- The App never writes the baseline or the policy; inputs is read-only on GitHub, and on the Store it writes only the
  pusher and approval records (the admission record is only read). An approval record is written only by `Approve`,
  which the gate façade calls only for `control/ui`'s browser session; nothing on the upload (OIDC) path reaches it.
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
- No cache, no state between calls **[default]**; the API client's conditional requests absorb repeat reads.
- Logs (`log/slog` default logger **[default]**, because the constructor carries none): installation ID,
  repository ID, digests, outcomes, timings. Never file content, owner lists, or reviewer names.
- Imports: standard library, `wire/settings`, `wire/policy`, `github/api`, `store`. Never `control`, `app`, LLM or UI packages.
- `now` is a parameter; inputs never reads the clock.

## Test Specification

Fakes: in-memory `Repos` and `Pulls` with per-ref file maps and call recording; `store.NewMemory` plus a
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
- **Test name**: `TestOwnerPushNeverApproves`
  **Scenario**: a baseline change whose head an owner, then a member of an owner team, pushed (a pusher record).
  **Expected behavior**: missing; the base baseline digest only; no head read.
- **Test name**: `TestCandidate`
  **Scenario**: nothing protected changed; baseline and settings; a labels file deleted at the head; a truncated list;
  a fork; no settings on the protected ref.
  **Expected behavior**: no paths; both paths with their head digests; the path with `""`; every path, an absent
  workflow `""`; the path and no digest, no head read; nothing.
- **Test name**: `TestAppApprovalGrantsTheExactHead`
  **Scenario**: an owner approves what `Candidate` showed; then a new head SHA; a member of an owner team approves.
  **Expected behavior**: granted with the owner as `ApprovedBy`, base and head baseline digests, the record fields;
  missing for the new head; granted for the team member.
- **Test name**: `TestAppApprovalRefusals`
  **Scenario**: digests changed or a path left out; a non-owner; an owner of the head settings only; no login; an
  owner listed as an agent; the `[bot]` account of an agent; an agent in an owner team; a fork; nothing protected
  changed; no settings.
  **Expected behavior**: `ErrDigestsChanged` twice, `ErrNotOwner` three times, `ErrAgentIdentity` three times,
  `ErrForkPull`, `ErrNothingToApprove` twice, from `Approve` and from `CanApprove`; no record; `Resolve` not granted.
- **Test name**: `TestAgentNeverApproves`
  **Scenario**: an agent named in `owners` and `agents`, and an agent in an owner team, approve on GitHub and in the App.
  **Expected behavior**: missing both ways, `ErrAgentIdentity`, and no team lookup.
- **Test name**: `TestAppApprovalVoidedByTheProtectedRef`
  **Scenario**: after an App approval: the approver leaves the owners; becomes an agent; leaves the owner team; the
  pull request changes another protected path, or no longer one the owner approved.
  **Expected behavior**: missing each time; the head digest is not accepted.
- **Test name**: `TestAppApprovalIsCreateOnly`
  **Scenario**: a second `Approve` by the same owner and by another, later.
  **Expected behavior**: success; the record keeps the first login and time.
- **Test name**: `TestAppApprovalRecordFailures`, `TestPusherRecordFailuresAreErrors`
  **Scenario**: the Store fails the read of the approval record; a record that does not decode; a record of another
  installation; a failed compare-and-swap; the Store fails a pusher record read.
  **Expected behavior**: `Resolve` returns the error; missing; missing; `Approve` returns the error; `RecordPusher`
  returns the error.
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
  **Expected behavior**: `SettingsFound`, one policy digest equal to `policy.Digest(raw)` (bare hex), one baseline digest, one labels digest, no protected paths, `ApprovalNotRequired`; `Reviews` was never called.
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

# Gate Inputs

**Path**: internal/gate/inputs/
**Parent**: internal/gate/ (gate façade)
**Submodules**: none (leaf)

## Purpose

`inputs` resolves everything the gate is allowed to believe about a pull request: settings, policy, baseline and labels digests from an immutable protected-ref snapshot, changed protected paths, the earliest waiver expiry, and the cached entitlement. Nothing approves a protected change, so it owns no decision or approval state. Without it, the gate could trust a pull request's head-tree authority.

## Functional Responsibilities

- Pin the current protected branch head for a pull-request target when `Target.BaseSHA` is empty, then read `.archfit-app.yaml`, `.archfit.yaml`, `.archfit-baseline.json` and `.archfit-labels.yaml` at that immutable SHA. A supplied `BaseSHA` is the exact settings snapshot already selected.
- Compute raw-byte digests (bare lowercase hex SHA-256) of policy, baseline and labels.
- Find which protected paths the PR changes.
- Never approve a protected change. A pull request that changes a protected path keeps the protected ref's digests and lists
  the paths in `Trusted.ProtectedPaths`; the verdict asks for action (`owner_approval_missing`). No push, review or App
  answer, and no agent identity, changes that. The head tree, a fork's included, is never read.
- Resolve the trusted inputs of a merge group commit (`Target.MergeGroup`): read the default branch's merge queue,
  take the queued pull requests ahead of and at the group as its members, read every protected file at two commit SHAs,
  and flag a member that changes a protected path in its own pull request, and a protected file of the group commit
  that differs from the default branch while no member changes it.
- Report the earliest upcoming waiver expiry.
- Read the cached `Entitlement` from the Store and derive `Suspended`.
- Own the `Entitlement` record shape and its Store key, so the gate never imports billing code.
- Answer whether the App serves an installation (`Admissions.Admitted`): the account is on the configured list, or an
  operator's `Admission` record exists. Own the `Admission` record shape and its Store key.

## Subdomain Classification

Core. Trusted-input rules are the product's trust model. Volatility: high; the protected path set will
change with pilots.

## Encapsulated Knowledge

- The protected path set and how a changed path is detected, for a caller that has the settings of the
  protected ref (`ProtectedPaths`) and for one that has not read them (`MayBeProtected`).
- The digest predicate: what a trusted digest list accepts (`Accepts`), and which digest stands for a list (`First`).
- That a protected change is never approved: the trusted digests stay the protected ref's, whatever the head holds.
- The merge group rules: who the members are, the snapshot commit `S`, which member changes which protected file, and
  that the group commit itself is never read as authority.
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
    BaseSHA     string // exact protected-settings snapshot; pinned by Resolve when empty
    Fork        bool
    // MergeGroup marks HeadSHA as a merge group commit of BaseRef's merge queue; PullRequest is 0. Its inputs are read
    // at immutable commit SHAs (resolveGroup).
    MergeGroup bool
}

// Trusted is everything the gate believes, all read from the protected ref or GitHub itself.
type Trusted struct {
    Settings             Settings
    SettingsFound        bool
    SettingsSHA          string // exact commit whose protected settings were resolved
    PolicyDigests        []string // the protected ref's digest; empty = no policy on the protected ref
    BaselineDigests      []string // same rule; empty = no baseline on the protected ref
    LabelsDigests        []string // same rule; empty = no labels file on the protected ref
    ProtectedPaths       []string // protected paths this PR changes; nothing approves such a change (owner_approval_missing)
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
    GroupConflict   GroupProblem = "conflict"   // a protected file of the group commit is changed by no queued pull request
)

// Group is the merge-group part of Trusted. Detail names members and paths ("#12 .archfit.yaml"), never content.
type Group struct {
    Problem GroupProblem
    Detail  string
    // Unapproved names each member that changes a protected path, as "#<n> <path>, <path>"; "" when there is none.
    // Nothing approves such a member.
    Unapproved string
}
```

```go
type Resolver interface {
    // Resolve pins a pull request to the current protected branch head when BaseSHA is empty, then reads trusted inputs
    // at that commit. It never reads policy, settings, baseline, owners or CODEOWNERS from the head tree.
    // A merge-group Target takes the snapshot path below.
    Resolve(ctx context.Context, t Target, now time.Time) (Trusted, error)
}
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

`ProtectedPaths` is the only list of the protected paths. `Resolve` step 5 intersects it with the changed files.
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
4. Read and parse settings at `Trusted.SettingsSHA`. Missing or unparsable settings returns `SettingsFound: false`, no owners and no protected paths. Read policy, baseline and labels at the same commit; only `api.ErrNotFound` means absent. Their digests are the whole of `PolicyDigests`, `BaselineDigests` and `LabelsDigests`.
5. Compute the earliest upcoming waiver expiry from the protected policy.
6. For a PR, a fork included, intersect `ChangedFiles` with `ProtectedPaths(settings)` in fixed order. A truncated list counts every protected path. Nothing approves a protected change: the digests stay those of the protected ref, no head file is read, and `gate/verdict` asks for action (`owner_approval_missing`) while the list is not empty.

### Merge group resolution

`Resolve` with `t.MergeGroup` runs steps 1 and 2, then this path; steps 3 to 6 do not run. `t.HeadSHA` is the group
commit `X`, `t.BaseRef` the default branch, `t.PullRequest` 0. `Trusted.Group` is always set.

1. Queue: `MergeQueues.MergeQueue(t.BaseRef)`, read before the snapshot so that a member merged in between is still a
   member and its change is in `S`. Snapshot: `Repos.BranchHead(t.BaseRef)` once, `S`. A failure of either is an error.
2. Settings at `S` (`Repos.File(ref = S, settings.Path)`). Absent or unparsable → `Trusted{SettingsFound: false}`,
   `Group` empty. Every protected file is read by commit SHA, `S` or `X`, never by
   branch name. Policy, baseline and labels digests of `S` and the waivers of the policy at `S` are the starting result.
3. Members: the queue entries from position 1 up to the entry whose `HeadSHA` is `X`. None → `GroupUnverified`. Per
   entry: `CrossRepo` → `GroupFork`; `Pulls.Pull` by number (`api.ErrNotFound` → `GroupUnverified`, other errors are
   errors); `pull.HeadRepo` not this repository → `GroupFork`; closed and not merged (a member that merged since the
   queue read stays a member, and its protected change is still flagged), `BaseRef` not `t.BaseRef`, or head not the
   entry's `PullHeadSHA` (or that is empty) → `GroupUnverified`. A problem stops the resolution with the `S` digests.
4. Per member: the protected paths its own pull request changes (the rule of step 6, by the settings at `S`). A member
   that changes a protected path → `Group.Unapproved` (`"#<n> <path>, <path>; ..."`); this is so also when the group's
   file equals `S`'s. Nothing approves it. Stop with the `S` digests.
5. Per protected path, compare the blob at `S` with the blob at `X` (both absent, or equal bytes, is the same; an empty
   file is present). `Trusted.ProtectedPaths` lists each path that differs or that a member changes. A differing path
   that no member changes → `GroupConflict` (`Detail` lists `"<path> changed by no queued pull request"`); stop with
   the `S` digests.
6. Otherwise every protected file of `X` equals `S`'s, and the digests and the waivers are those of `S`.

### Errors

`Resolve` returns an error for any GitHub failure other than "file absent" (including `api.ErrRateLimited`). The zero `Trusted` accompanies an error; callers never publish a pass on an error. A failed entitlement read does not fail `Resolve`.

A protected file that exists but cannot be read is such a failure, never "absent": a file over the API client's
size cap (1 MiB), a 5xx, a timeout. "Absent" is only `api.ErrNotFound`. `Resolve` returns the error, `gate` answers
503, nothing green is published, the Action retries, and if the condition persists the pending check ends
`action_required` (`missing_upload`) through the sweep. An unreadable policy is never treated as "no policy".

## Integrations

### store

- **Counterpart**: internal/store
- **Direction**: inputs depends on store (`Get` of the entitlement and the admission)
- **Strength**: contract
- **LCA / Rank / Distance**: root / 2 / 2
- **Volatility**: high (inputs side); store is low
- **Balanced?**: yes
- **Shared knowledge**: tenant = installation ID; key `AdmissionKey`; value is the JSON of `Admission`; `Admissions`
  only calls `Get`. `ErrNotFound` is "not admitted" without an error; any other Store error, a value that does not
  decode and a record with `by_id <= 0` are an error from `Admitted` and `Record`.
  Key `EntitlementKey`; value is the JSON of `Entitlement`.
  `ErrNotFound` is the sentinel of `store` for a missing key; for the entitlement inputs treats it like every other
  Store error. inputs calls only `Get`; it never uses `Put`, `Delete`, `Add`, `CompareAndSwap`, `CompareAndSwapAll`, `DeleteTenant` or `Tenants`. `SystemTenant` is never used here. The Archfit 3 keys
  (`pusher/`, `approval/`, `owner-decision/`, `review-request/`) stay in the Store, unread until a post-release cleanup.

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
  absent file. `ErrRateLimited` is the sentinel returned after bounded backoff. `ChangedFiles` lists both the old and the new path of a rename, and
  sets `truncated` when GitHub cut the list off. `PullByHead` and `OpenPulls` are not
  called here; `BranchHead` pins each PR to an immutable protected snapshot; `Pull` is also read for merge groups. `MergeQueue` returns the entries of a branch's queue in
  position order, none when it has no queue; a missing Merge queues permission (`api.ErrQueueForbidden`) and a queue
  over the read bound (`api.ErrQueueTooLong`) are errors, never an empty queue. Used methods: `Repos.File`,
  `Pulls.ChangedFiles` and `Repos.BranchHead`; and for a merge group `Pulls.Pull`,
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

<!-- contract: Pulls, Pull — restated from internal/github/api/module.md -->
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

type Pulls interface {
    Pull(ctx context.Context, r RepoRef, number int) (Pull, error)
    OpenPulls(ctx context.Context, r RepoRef) ([]Pull, error)
    // PullByHead returns the newest PR, in any state, whose head is branch in this repository. ErrNotFound if none.
    PullByHead(ctx context.Context, r RepoRef, branch string) (Pull, error)
    // ChangedFiles lists changed paths; a rename lists both the old and the new path.
    // truncated is true when GitHub's file list was cut off: callers must assume any path changed.
    ChangedFiles(ctx context.Context, r RepoRef, number int) (paths []string, truncated bool, err error)
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

- **internal/gate** (parent): functional, gate / 1 / 1, high, balanced. Constructs the resolver and calls `Resolve` per upload and webhook. Its façade has no decision or approval write.
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

- A new protected path.
- A different baseline path or an additional trusted file (labels).
- A changed suspension formula or entitlement field.
- Another way to approve an installation (a different configured list, a self-service flow).
- Caching of protected-ref reads inside one process, if rate limits demand it.
- A queue on another branch, or a queue over 1,000 entries (`api.ErrQueueTooLong` today).

## Constraints and Invariants

- Never trust the head tree. Settings, owners, policy, baseline and workflow path come from the current protected-ref commit (`BaseSHA`/`SettingsSHA`), never the candidate head. There is no exception: no approval makes a head file trusted, and no head file is hashed. CODEOWNERS is not an authority source.
- For a fork, no head file is read. A merge group with a fork member is `GroupFork`.
- A merge group's membership comes from GitHub's queue and the pull requests read again by number; nothing the uploader
  states selects a member, and the `gh-readonly-queue/...` branch name is never read. A merge group commit is never read as
  authority: the digests are those of the snapshot `S`, and a group whose files differ from `S` without a queued
  member that changes them is a `GroupConflict`.
- Nothing approves a protected change: a push, a review, the App and an agent identity change no digest and no path list.
- One digest predicate and one protected path list. No other gate module compares a digest with a trusted list or
  names a protected path; they call `Accepts`, `First`, `ProtectedPaths` and `MayBeProtected`. The literal
  `.github/workflows/` exists here only; it restates the directory rule of `wire/settings` (owner: `wire/settings`,
  which exports no constant for it). If the two drift, `MayBeProtected` stops being a superset and a workflow push
  is noticed by the hourly sweep instead of at once. Remove the literal when `wire/settings` exports the directory.
- The App never writes the baseline or policy. inputs is read-only on GitHub and on the Store (`Get` only); the admission record is read-only here.
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
- No process cache and no persisted state of its own.
- Logs (`log/slog` default logger **[default]**, because the constructor carries none): installation ID,
  repository ID, digests, outcomes, timings. Never file content or owner lists.
- Imports: standard library, `wire/settings`, `wire/policy`, `github/api`, `store`. Never `control`, `app`, LLM or UI packages.
- `now` is a parameter; inputs never reads the clock.

## Test Specification

Fakes: in-memory `Repos`, `Pulls` and `MergeQueues` with per-ref file maps and call recording; `store.NewMemory` plus a
failing `Store` stub. `NewMemory` is the in-process Store constructor of `internal/store`.

### Unit Tests

- **Test name**: `TestProtectedPathsIntersection`
  **Scenario**: table of changed-file lists: none protected; policy only; baseline and workflow; settings; a custom `Settings.Workflow` path; a policy file renamed away (old path listed); a list without a protected path but `truncated == true`.
  **Expected behavior**: `ProtectedPaths` in fixed order; the policy path for the rename; all five for the truncated list. A changed `.archfit-labels.yaml` is listed like the other four.
- **Test name**: `TestAcceptsTruthTable`
  **Scenario**: every combination of the lists empty, one digest, two digests and a list holding `""` with the claims `""`, the first entry, the second entry, an unknown digest, a `sha256:`-prefixed entry and an upper-case entry.
  **Expected behavior**: the table of "Digest predicate"; a list holding `""` does not accept `""`. `First` is `""` for the empty list and entry 0 otherwise, and `Accepts(list, First(list))` is true for every list without an empty entry.
- **Test name**: `TestProtectedPathSet`
  **Scenario**: iterate `ProtectedPaths(s)` for a parsed `Settings`; the zero `Settings`; paths under `.github/workflows/`; near misses (`docs/.archfit.yaml`, `.github/workflows`, `.github/dependabot.yml`, `""`).
  **Expected behavior**: five paths in fixed order, four for the zero `Settings`; `MayBeProtected` is true for every path of the set and for every workflow file, false for the near misses; a pull request that changes any single path of the set gets exactly that path in `Trusted.ProtectedPaths` and nothing else. The test names no path itself, so a sixth path in `ProtectedPaths` is covered without a test edit.
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
  **Expected behavior**: `SettingsFound`, `SettingsSHA` equals the immutable protected branch head, one policy digest equal to `policy.Digest(raw)` (bare hex), one baseline digest, one labels digest, no protected paths.
- **Test name**: `TestResolveProtectedChangeTrustsOnlyTheBase`
  **Scenario**: a PR that changes code and, in turn, the policy, the labels file and the baseline.
  **Expected behavior**: the changed path is the only one in `ProtectedPaths`; every digest list is the protected ref's; no file is read at the head.
- **Test name**: `TestResolvePushTarget`
  **Scenario**: `PullRequest == 0`, `BaseRef` = default branch.
  **Expected behavior**: digests from that branch; no `ChangedFiles` call.
- **Test name**: `TestResolveBootstrap`
  **Scenario**: no settings file at `BaseRef`; then an unparsable one.
  **Expected behavior**: `SettingsFound` false, empty digests, no error, entitlement still read.
- **Test name**: `TestEntitlementRecordShape`
  **Scenario**: a record written as `control/account` writes it (JSON with the four snake_case fields).
  **Expected behavior**: decoded field by field into `Trusted.Entitlement`; key is `entitlement` under `Tenant(installationID)`.

### Boundary Tests

- **Test name**: `TestHeadTreeIsNeverRead`
  **Scenario**: every protected-change and fork scenario with a recording `Repos` fake.
  **Expected behavior**: no `File` call with `ref == HeadSHA`.
- **Test name**: `TestStoreFailureIsNotSuspension` (T21)
  **Scenario**: Store returns `ErrNotFound`, a generic error, blocks past the timeout, returns invalid JSON.
  **Expected behavior**: zero `Entitlement`, `Suspended` false, `Resolve` succeeds; one log line with the outcome and no payload.
- **Test name**: `TestGitHubErrorsFailClosed`
  **Scenario**: `File` and `ChangedFiles` each return a generic error and `ErrRateLimited`; `File` returns the client's oversize error for the policy, the baseline and the settings file.
  **Expected behavior**: `Resolve` returns an error matching with `errors.Is`; zero `Trusted`.
- **Test name**: `TestTenantIsolation`
  **Scenario**: entitlement stored for installation A; resolve a repository of installation B.
  **Expected behavior**: B sees the zero entitlement; the Store was read only under B's tenant.
- **Test name**: `TestLogsCarryOnlyIdentifiers`
  **Scenario**: files with marker strings in owners, waiver reasons and policy text.
  **Expected behavior**: no marker string in any log line.

- **Test name**: `TestImportsStayInsideAllowedSet`
  **Scenario**: `go list` the imports of the package's non-test files.
  **Expected behavior**: only the standard library, `wire/settings`, `wire/policy`, `github/api` and `store`.

### Behavior Tests

- **Test name**: `TestMergeGroupInputs`
  **Scenario**: table over a two-entry queue: a group that changes no protected file; a member's policy change, also one the group reverts and one the member makes behind another; two members changing one file; a protected change no member makes; a member that deletes the baseline; a member behind the group; a fork member (flagged by the queue, or only by the pull request); a member that merged since the queue read; a member whose head moved, into another base, or closed; a group commit the queue no longer lists.
  **Expected behavior**: `S`'s digests in every row; no flag; `Group.Unapproved` naming the member and its path; `Group.Unapproved` naming both members; `GroupConflict` ("changed by no queued pull request"); `Group.Unapproved` naming the baseline; the member behind does not count; `GroupFork`; `GroupUnverified`; the merged member is still flagged; `GroupUnverified`.
- **Test name**: `TestMergeGroupReadFailuresAreErrors`, `TestMergeGroupWithoutSettingsIsNotOnboarded`
  **Scenario**: a failing queue, snapshot, file or pull request read; no settings file at `S`.
  **Expected behavior**: an error and the zero `Trusted`; `SettingsFound` false.
- **Test name**: `TestHeadEditsCannotWeakenInputs` (T12)
  **Scenario**: the PR edits policy, baseline, settings (adds the author as owner, switches to advisory) and the workflow.
  **Expected behavior**: digests, owners, gate mode and workflow path are the base values; all changed protected paths listed; the head baseline and labels digests are never accepted (no blanket re-baseline, no head-edited labels).
- **Test name**: `TestHeadOwnersConferNothing`
  **Scenario**: the head settings and CODEOWNERS name another owner; the PR changes settings, CODEOWNERS, policy and baseline.
  **Expected behavior**: base digests only; no head read.
- **Test name**: `TestForkNeverGetsHeadInputs`
  **Scenario**: fork target that changes the policy.
  **Expected behavior**: the policy path in `ProtectedPaths`; no head read; base digests only.
- **Test name**: `TestSuspensionFacts` (T21)
  **Scenario**: private repository past `SuspendAfter`; the same record for a public repository; the record replaced by a later `SuspendAfter` (restored).
  **Expected behavior**: suspended; not suspended; not suspended. `Entitlement.Reason` and `Notice` pass through unchanged.

# Control

**Path**: internal/control/
**Parent**: internal/ (Archfit App root)
**Submodules**: account, proposal, onboarding, assist, ui

## Purpose

`control` is the control plane: everything a person or an operator does with Archfit that is not the
gate decision. It wires entitlements and consent, proposals as draft PRs, both onboarding modes, AI
assistance and the UI into one façade that `internal/app` mounts. It exists as one branch so that the
volatile product surface can change, fail, or be switched off without touching the gate path. Without it
the gate would still verify reports, but nobody could onboard, edit a policy, start a trial, or be suspended.

## Functional Responsibilities

- Construct the five children from one `Deps` value and hand each only what it needs.
- Serve the UI handler (`/`) and the discovery upload handler (`POST /v1/discoveries`).
- React to installation webhooks: start or keep the trial (`Observe`), delete the tenant on uninstall (`Forget`).
- Refresh every installation's `Entitlement` record on the periodic sweep, and forget Store tenants whose installation no longer exists.
- Leave an installation whose account no operator approved alone: it gets no entitlement record, so its trial starts at the approval.
- Apply the operator's manual beta grant.
- Pass the admission seam (`Admissions`) and the operator IDs to `ui`, which lets an operator approve an installation.
- Pass the gate's owner approval seam (`Approvals`) to `ui`, whose pull request page shows an owner approval and
  records one with its Approve button, and the decision seam (`Decisions`), through which its pull request page lists
  the decision requests of unattended agents and lets an owner decline or approve one.
- Pass the gate's decision queue seam (`DecisionQueue`) to `ui`, whose overview and decisions page list the decision
  requests that wait for an owner. It reads only.
- Re-publish the two platform seams, `Sandbox` and `Provider`, so `app` and platform packages cite one place.

## Subdomain Classification

Core-adjacent product surface with one supporting part (`account`) and one low-volatility part (`proposal`).
Volatility **high** as a whole: onboarding, UI and assistance change with every pilot. The façade itself
(`Control`, `Deps`) is small and changes only when a child or a platform seam is added.

## Encapsulated Knowledge

- Which children exist and how they are wired; nobody outside imports `control/*`, except `app` calling the
  constructor of a platform implementation (`control/assist/anthropic`, later `control/onboarding/<sandbox>`).
- The webhook-to-account mapping and that `hasPrivate` is read from GitHub, not from the event.
- The control sweep algorithm and its failure policy (log, skip, continue).
- That a nil `Sandbox` or `Provider` means "managed feature not available", not an error at startup.
- The 10-second budget for `HandleEvent`. **[default]**

## Public Contract

```go
// Deps are wired by internal/app. Sandbox and Provider may be nil: managed features then report "not available".
type Deps struct {
    GitHub    *Client
    Tokens    TokenSource
    OAuth     OAuth
    Store     Store
    Receiver  Receiver
    // Admissions says which installations the App serves; Operators (GitHub user IDs) approve the others on /admin.
    Admissions *Admissions
    // Approvals reads and records owner approvals of pull request heads for the pull request page: the gate façade.
    Approvals Approvals
    // Decisions lists, declines and approves the decision requests of unattended agents for the pull request page.
    Decisions Decisions
    // DecisionQueue lists the decision requests that wait for an owner, for the overview and the decisions page: the gate façade.
    DecisionQueue DecisionQueue
    Operators []int64
    Sandbox    Sandbox
    Provider   Provider
    CookieKey  []byte
    BaseURL    string
    Now        func() time.Time
}

// WorkerDeps are the dependencies of the worker façade: it handles events, the sweep and grants, and serves no UI.
// So it takes no OAuth, no cookie key, no Receiver, no Sandbox and no Provider.
type WorkerDeps struct {
    GitHub     *Client
    Store      Store
    Admissions *Admissions // the sweep and installation events do nothing for an account no operator approved
    Now        func() time.Time
}

type Control struct{ /* unexported */ }

func New(d Deps) (*Control, error)

// NewWorker builds the façade of the worker role. Only HandleEvent, Sweep and Grant work on it; the handler
// methods return nil, and the worker mounts no route that needs them.
func NewWorker(d WorkerDeps) (*Control, error)

func (c *Control) UIHandler() http.Handler        // "/"
func (c *Control) DiscoveryHandler() http.Handler // POST /v1/discoveries
func (c *Control) BaselineHandler() http.Handler  // POST /v1/baselines
func (c *Control) ReanchorHandler() http.Handler  // POST /v1/reanchors
// HandleEvent maintains entitlements on installation events and forgets a tenant on uninstall.
func (c *Control) HandleEvent(ctx context.Context, e Event) error
// Sweep refreshes every installation's Entitlement record and forgets Store tenants
// whose installation no longer exists.
func (c *Control) Sweep(ctx context.Context) error
// Grant is the operator-only manual beta grant.
func (c *Control) Grant(ctx context.Context, installationID int64, until time.Time) error
```

Re-published platform seams. Platform packages and `app` cite this file for them.

<!-- contract: Sandbox, DiscoveryJob, DiscoveryResult — restated from internal/control/onboarding/module.md -->
```go
// Sandbox is the only platform-dependent seam of managed discovery: one ephemeral, isolated
// container job. Implementations must destroy the container and its disk on every exit path,
// must not execute repository scripts, and must give the job no credential except Token.
type Sandbox interface {
    Discover(ctx context.Context, job DiscoveryJob) (DiscoveryResult, error)
}

type DiscoveryJob struct {
    CloneURL    string
    Token       string // short-lived installation token, contents:read
    Ref         string
    ImageDigest string // engine image from the locked manifest
    Timeout     time.Duration
}

type DiscoveryResult struct {
    DraftPolicy []byte   // engine-generated policy YAML
    Notes       []string // engine-authored, no-source
}

// ErrNoSandbox is returned by the default Sandbox until a platform implementation is configured.
var ErrNoSandbox = errors.New("onboarding: managed discovery is not available")
```

<!-- contract: Provider, Prompt, Completion — restated from internal/control/assist/module.md -->
```go
// Provider is the LLM seam. Implementations own endpoint, model choice, and pricing.
type Provider interface {
    // MaxCostMicroUSD is the worst-case cost of one Complete call for p; used to reserve budget.
    MaxCostMicroUSD(p Prompt) int64
    Complete(ctx context.Context, p Prompt) (Completion, error)
}

type Prompt struct {
    System    string
    User      string
    MaxTokens int
}

type Completion struct {
    Text         string
    CostMicroUSD int64
}
```

`ErrNoSandbox` is the sentinel a default or missing `Sandbox` yields; the UI shows "not available".

### Operation rules

- `New` returns an error, and no `Control`, on a wiring bug: nil `GitHub`, `OAuth`, `Store`, `Receiver`, `Admissions`, `Approvals`, `Decisions` or `DecisionQueue`, `CookieKey` not
  32 bytes, empty `BaseURL`, a non-nil `Sandbox` with nil `Tokens`, or an error from `ui.New`. Nil `Now` means `time.Now`.
  Nil `Sandbox` and nil `Provider` are valid. It never panics. **[default]**
- `NewWorker` returns an error, and no `Control`, on a nil `GitHub`, `Store` or `Admissions`. It builds only `account`: no `ui`,
  `onboarding`, `assist` or `proposal`. So the worker process holds no OAuth secret, cookie key or Anthropic key.
  `HandleEvent`, `Sweep` and `Grant` behave as on a `Control` from `New`; `UIHandler`, `DiscoveryHandler` and
  `BaselineHandler` return nil.
- `UIHandler` returns the `ui` handler wrapped in a panic recovery that answers `500` and logs the route pattern only.
- `DiscoveryHandler` returns `Onboarding.DiscoveryHandler()` unchanged; `BaselineHandler` returns `Onboarding.BaselineHandler()` unchanged.
- `HandleEvent` has its own 10 s timeout **[default]**. `e.InstallationID <= 0` is an error. By `e.Kind`:
  - `InstallationAdded`, `ReposRemoved` → `Admissions.Admitted(id, e.Account)` first: not admitted → nothing is read or written, `nil` (no GitHub call, no entitlement record, so the trial starts at approval); a read error is returned, so the delivery is retried. Then `Repos.ListRepos(e.InstallationID)` → `hasPrivate` = any `Private` → `Account.Observe(id, hasPrivate, Now())`. An empty `Account` is admitted only by the installation's record.
  - `InstallationRemoved` → `Account.Forget(e.InstallationID)`. No GitHub call.
  - anything else → nil, no call.
- Installation events carry `Repo == ""`, so `e.RepoPrivate` is never used. A `HandleEvent` error is returned to `app`,
  which logs it; the sweep repairs a missed `Observe`.
- `Sweep`: `Repos.ListInstallations`; error → return it. Per installation: `ListRepos` ok → `Observe` (which rewrites the
  record); `ListRepos` failed → `Refresh` only (needs no GitHub). Per-installation errors are logged by ID and skipped.
  Before either, `Admissions.Admitted(id, Installation.Account)`: not admitted → skipped and counted as `waiting` in the
  log line; an admission read error counts as failed. So an account waiting for approval gets no `account` or
  `Entitlement` record, and its trial starts at the first sweep after the approval.
  Backfill rule: the sweep is the recovery path for a missed installation webhook. `Observe` runs for every installation
  `ListInstallations` returns, whether or not a record exists; on a missing record it creates the `account` record, starts the
  trial at sweep time (`first_private_at = Now()` when a private repository exists) and writes the `Entitlement` record. Until
  then the gate reads "not suspended" for that installation, by design: a missing record never establishes non-payment.
  Then orphan cleanup: `Store.Tenants`; every tenant whose ID is not in the installation list → `Account.Forget`.
  Cleanup runs only when `ListInstallations` returned a nil error (a complete list by the counterpart's contract) and at
  least one installation **[default: an empty answer never deletes anything]**. A `Tenants` error skips cleanup; a `Forget`
  error is logged and skipped. On any doubt the keys stay. Returns nil after the loop; logs counts (installations,
  observed, refreshed, forgotten, failed).
- `Grant` → `Account.Grant(ctx, installationID, until, Now())`. `app` authenticates the operator; `control` does not.

## Integrations

### Children (composition)

| Counterpart                    | Direction                    | Strength   | LCA / Rank / Distance | Volatility | Balanced? |
| ------------------------------ | ---------------------------- | ---------- | --------------------- | ---------- | --------- |
| `internal/control/account/`    | `control` constructs, calls  | Functional | control / 1 / 1       | High       | Yes       |
| `internal/control/proposal/`   | `control` constructs         | Functional | control / 1 / 1       | High       | Yes       |
| `internal/control/onboarding/` | `control` constructs, mounts | Functional | control / 1 / 1       | High       | Yes       |
| `internal/control/assist/`     | `control` constructs         | Functional | control / 1 / 1       | High       | Yes       |
| `internal/control/ui/`         | `control` constructs, mounts | Functional | control / 1 / 1       | High       | Yes       |

Shared knowledge: the constructors and the few methods the façade calls.

<!-- contract: Account, Status, Plan — restated from internal/control/account/module.md -->
```go
type Plan string

const (
    PlanPublic    Plan = "public"    // no private repositories: free
    PlanTrial     Plan = "trial"     // 30 days from the first private-repository installation
    PlanGrace     Plan = "grace"     // 7 days after the trial
    PlanGranted   Plan = "granted"   // manual beta grant
    PlanSuspended Plan = "suspended"
)

type Status struct {
    Plan         Plan
    TrialEnds    time.Time
    SuspendAfter time.Time
    GrantUntil   time.Time
}

type Account interface {
    // Observe records that the installation has (or no longer has) private repositories and
    // starts the trial on the first private one. It rewrites the gate's Entitlement record.
    Observe(ctx context.Context, installationID int64, hasPrivate bool, now time.Time) error
    Status(ctx context.Context, installationID int64, now time.Time) (Status, error)
    // Grant sets a manual beta grant. Operator-only; audited in the log by installation ID.
    Grant(ctx context.Context, installationID int64, until time.Time, now time.Time) error
    // Refresh recomputes the Entitlement record of one installation (called by the sweep).
    Refresh(ctx context.Context, installationID int64, now time.Time) error
    // Forget deletes every key of the installation (uninstall), its admission included.
    Forget(ctx context.Context, installationID int64) error
    // Admit records an operator's approval of the installation: the gate serves it from the next event on.
    // Operator-only; audited in the log by installation ID and operator.
    Admit(ctx context.Context, installationID int64, a gate.Admission) error
    // Unadmit deletes the approval: the gate serves nothing for the installation from the next event on.
    // Unadmitting an installation without an approval is not an error.
    Unadmit(ctx context.Context, installationID int64, byID int64) error
}
```

<!-- contract: New — restated from internal/control/account/module.md -->
```go
func New(s Store) (Account, Consents, Budget)
```

<!-- contract: ErrStale, New — restated from internal/control/proposal/module.md -->
```go
// ErrStale means the branch moved since the caller read it; the caller must reload and re-apply.
var ErrStale = errors.New("proposal: branch head changed")

func New(gh Changes, pulls Pulls) Proposals
```

<!-- contract: Onboarding, Stage, Progress — restated from internal/control/onboarding/module.md -->
```go
type Stage string

const (
    StageInstalled      Stage = "installed"
    StageWorkflowMerged Stage = "workflow_merged"
    StageReportReceived Stage = "report_received"
    StagePolicyMerged   Stage = "policy_merged"
    StageAdvisory       Stage = "gate_advisory"
    StageRequired       Stage = "gate_required"
)

// Progress is derived from GitHub on every call; nothing is stored.
type Progress struct {
    Stage      Stage
    Diagnostic string // what to do next
    OpenDraft  *Draft
}

type Onboarding interface {
    Progress(ctx context.Context, r Repo) (Progress, error)
    // StartCustomer opens PR 1: the settings file with owners and CODEOWNERS entries. The owner adds the workflow.
    StartCustomer(ctx context.Context, r Repo, owners []string) (Draft, error)
    // StartManaged requires ConsentSourceProcessing, opens the onboarding PR and runs the sandbox job.
    StartManaged(ctx context.Context, r Repo, owners []string) (Draft, error)
    // DiscoveryHandler serves POST /v1/discoveries and opens PR 2 with the proposed policy.
    DiscoveryHandler() http.Handler
    // BaselineHandler serves POST /v1/baselines and opens or updates the baseline PR. It never writes the default branch.
    BaselineHandler() http.Handler
}
```

<!-- contract: New, Deps — restated from internal/control/onboarding/module.md -->
```go
type Deps struct {
    GitHub    *Client
    Tokens    TokenSource // RepoReadToken for the sandbox clone
    Receiver  Receiver
    Proposals Proposals
    Consents  Consents
    Sandbox   Sandbox
    BaseURL   string // upload endpoint and OIDC audience written into the generated workflow
    Now       func() time.Time
}

func New(d Deps) Onboarding
```

<!-- contract: New, Deps, AssistWorkflowPath — restated from internal/control/assist/module.md -->
```go
// AssistWorkflowPath is the customer-mode workflow dispatched with inputs {branch, head_sha, instruction}.
const AssistWorkflowPath = ".github/workflows/archfit-assist.yaml"

type Deps struct {
    GitHub    *Client
    Provider  Provider // nil disables managed mode
    Consents  Consents
    Budget    Budget
    Proposals Proposals
    Now       func() time.Time
}

func New(d Deps) Assist
```

<!-- contract: New, Deps — restated from internal/control/ui/module.md -->
```go
type Deps struct {
    OAuth      OAuth  // ui builds its own session child from OAuth, CookieKey and BaseURL
    CookieKey  []byte
    BaseURL    string
    GitHub     *Client
    Account    Account
    Consents   Consents
    Onboarding Onboarding
    Assist     Assist
    Proposals  Proposals
    Admissions *Admissions // which installations the App serves; the others see the Waiting page
    Approvals  Approvals   // owner approvals of pull request heads: read, and recorded by the Approve button
    Decisions  Decisions   // decision requests of unattended agents: listed, declined, approved by an owner
    DecisionQueue DecisionQueue // decision requests that wait for an owner: read only
    Operators  []int64     // GitHub user IDs that approve installations on /admin
    Now        func() time.Time
}

// New returns the UI mounted at "/". It serves the web app's build from the binary.
func New(d Deps) (http.Handler, error)
```

Pass-through types, never inspected by the façade: `Consents` is the consent interface and `Budget` is the LLM budget
interface, both returned by `account.New`. `Proposals` is the draft-PR interface returned by `proposal.New`; `Changes` and
`Pulls` are `github/api` role interfaces that `*Client` implements. `Assist` is the interface returned by `assist.New`.
`Draft` is an opaque part of `Progress` for this module. `ErrStale` is the sentinel of `proposal`; the façade does not handle it.
`ui` builds its own `session` child; the façade never imports `control/ui/session`. `StageInstalled` … `StageRequired`, the `Plan`
constants and `AssistWorkflowPath` are unused by the façade; they are part of the restated fences only.

### internal/gate

- **Counterpart**: `internal/gate/` (façade only)
- **Direction**: `control` depends on `gate`; `gate` never imports or calls `control`
- **Strength**: contract
- **LCA / Rank / Distance**: root / 1 / 1
- **Volatility**: moderate
- **Balanced?**: yes
- **Shared knowledge**: `Receiver` is the upload authentication interface built by `gate.NewReceiver` in `app`; the façade
  passes it to `onboarding` untouched. `account` alone knows the `Entitlement` record, and alone writes the gate's
  `Admission` record. `Deps.Admissions` is built by `app` (the same value the gate and the receiver use); the façade
  hands it, with `Deps.Operators`, to `ui` and never calls it. `control` never admits or revokes: `ui` does, for an
  operator. `InstallationRemoved` and the orphan cleanup call `Forget`, which deletes the admission with the tenant.
  `Deps.Approvals` and `Deps.DecisionQueue` are the `*Gate` that `app` builds; the façade hands it to `ui` untouched and never calls it, and
  hands it to no other child, so the discovery, baseline and re-anchor uploads (OIDC) cannot reach an approval.
  `Deps.Decisions` is the same `*Gate`, handed to `ui` the same way and reaching nothing else.

### internal/store

- **Counterpart**: `internal/store/`
- **Direction**: `control` depends on `store`
- **Strength**: contract
- **LCA / Rank / Distance**: root / 1 / 1
- **Volatility**: moderate
- **Balanced?**: yes
- **Shared knowledge**: the façade passes `Store` to `account.New` and itself calls only `Tenants`, in the sweep's orphan cleanup.

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

`Tenant` values from `Tenants` convert to installation IDs with `int64(t)`. The store's `ErrNotFound` is unused by the façade.

### internal/github/webhook

- **Counterpart**: `internal/github/webhook/`
- **Direction**: `control` depends on `github/webhook` (`Event` only; `app` verifies and parses)
- **Strength**: contract
- **LCA / Rank / Distance**: root / 2 / 2
- **Volatility**: moderate
- **Balanced?**: yes
- **Shared knowledge**:

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
    Account        string // installation kinds only: the account login from the payload; "" when it has none
    RepoPrivate    bool
    PullRequest    int
    HeadSHA        string
    HeadRepo       string // differs from Repo for forks
    BaseRef        string
    ChangedPaths   []string // default_branch_push only
}
```

### internal/github/api

- **Counterpart**: `internal/github/api/`
- **Direction**: `control` depends on `github/api` (`Repos.ListInstallations`, `Repos.ListRepos`; `*Client` passed to children)
- **Strength**: contract
- **LCA / Rank / Distance**: root / 2 / 2
- **Volatility**: moderate
- **Balanced?**: yes
- **Shared knowledge**:

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

<!-- contract: Client, New — restated from internal/github/api/module.md -->
```go
// Client implements every role interface. Consumers depend on the role interfaces they need.
type Client struct{ /* unexported */ }

func New(ts TokenSource, hc *http.Client) *Client
```

`ErrNotFound` is the sentinel for a missing installation or repository (for example right after an uninstall);
`ErrRateLimited` is the sentinel after bounded backoff. `TokenSource` is also an opaque part of `Client` for this module.

### internal/github/auth

- **Counterpart**: `internal/github/auth/`
- **Direction**: `control` depends on `github/auth` (type names only)
- **Strength**: contract
- **LCA / Rank / Distance**: root / 2 / 2
- **Volatility**: low
- **Balanced?**: yes
- **Shared knowledge**: `OAuth` is the GitHub user login flow interface and `TokenSource` is the App credential minter, both built
  by `auth.New` in `app`. The façade passes `OAuth` to `ui.New` and `Tokens` to `onboarding.New` (sandbox clone token); it calls neither.

### internal/app (inbound)

- **Counterpart**: `internal/app/`
- **Direction**: `app` depends on `control` (`New`, `NewWorker`, handlers, `HandleEvent`, `Sweep`, `Grant`, the two seams)
- **Strength**: contract
- **LCA / Rank / Distance**: root / 1 / 1
- **Volatility**: low
- **Balanced?**: yes
- **Shared knowledge**: the Public Contract above. `app` calls `gate.HandleEvent` before `control.HandleEvent`, and
  `control.Sweep` before `gate.Sweep`; each pair fails independently.

## Internal Design

`New` wires, in this order:

1. `account.New(d.Store)` → `Account`, `Consents`, `Budget`.
2. `proposal.New(d.GitHub, d.GitHub)` → `Proposals`.
3. `onboarding.New(Deps{GitHub, Tokens, Receiver, Proposals, Consents, Sandbox, BaseURL, Now})` → `Onboarding`.
4. `assist.New(Deps{GitHub, Provider, Consents, Budget, Proposals, Now})` → `Assist`.
5. `ui.New(Deps{OAuth, CookieKey, BaseURL, GitHub, Account, Consents, Onboarding, Assist, Proposals, Admissions, Approvals, Decisions, DecisionQueue, Operators, Now})` → the UI handler, or an error that `New` returns.

| Child        | Contributes                                                        | Depends on siblings                          |
| ------------ | ------------------------------------------------------------------ | -------------------------------------------- |
| `account`    | all KV state: entitlement writer, consent, LLM budget              | none                                         |
| `proposal`   | a change as branch + draft PR, idempotent, stale-safe              | none                                         |
| `onboarding` | stages, PR 1, discovery upload → PR 2, managed job, `Sandbox` seam | `proposal`, `account` (`Consents`)           |
| `assist`     | explicit AI proposals, `Provider` seam, budget use                 | `proposal`, `account` (`Consents`, `Budget`) |
| `ui`         | pages, editor, graph, consent screens; owns `session` and `graph`  | all four                                     |

`Control` also keeps four unexported fields that the façade itself never calls: `consents`, `budget`, `onboarding` and
`assist`, the values built in steps 1, 3 and 4. They exist only so that the package's own composition tests can reach
the wired children (record a consent, exhaust the budget, start onboarding, ask assist) without a second wiring in the
test harness. They are not part of the Public Contract and no code outside the tests reads them.

Sibling dependencies form a DAG: `account`, `proposal` ← `onboarding`, `assist` ← `ui`. All sibling links are contract
strength through interfaces, so each child is tested with fakes of the others.

Key flows through the façade:

- **Install**: webhook → `HandleEvent` → `ListRepos` → `Observe` → `Entitlement` record exists before the first upload.
  If it does not, the gate reads "not suspended".
- **Uninstall**: webhook → `Forget` → tenant keys gone: entitlement, account record, consents. GitHub access is already
  revoked, so every later call for that installation fails with `ErrNotFound` and nothing is scheduled.
- **Sweep**: keeps notices current ("1 day left"), catches missed installation events, and forgets tenants whose uninstall
  webhook was missed. It never touches checks;
  suspension and restoration on PRs are the gate sweep's work, from the record.
- **Grant**: operator endpoint → `Grant` → record rewritten → the gate sweep re-runs suspended PRs. Only a fresh report can turn a PR green.
- **UI and discovery**: mounted handlers; the façade adds nothing but panic recovery.

`control` reaches no grandchild: `ui` builds `session` and `graph` itself from `OAuth`, `CookieKey` and `BaseURL`.

## Change Vectors

- A new control-plane capability: a new child and one wiring line; `Deps` grows only for a new platform seam.
- A billing provider: inside `account`; the façade may gain one webhook route handler.
- A summary return value for `Sweep` if operators need counts over HTTP.

## Constraints and Invariants

- Nothing in `control` is ever called by the gate path. A failure here never changes a gate result. The gate reads only
  the cached `Entitlement` record that `account` writes; missing or unreadable means "not suspended".
- `control` imports `gate` as a façade only, never `gate/*`. `gate` never imports `control`. Children import siblings, never the parent.
- Tenant identity comes from the verified webhook (`Event.InstallationID`), the App's own installation list (sweep), or an
  authorized session (UI). `Grant` takes the ID from the operator, whom `app` authenticates. Never from a client-supplied ID.
- Uninstall deletes every key of the tenant and nothing else exists to clean up. A missed `installation_removed` delivery
  is repaired by the next sweep's orphan cleanup. Cleanup fails safe: it deletes only on a complete, non-empty installation
  list; any GitHub or Store error keeps the keys. `SystemTenant` is never listed by `Tenants` and never forgotten.
- Handlers are idempotent: duplicate or replayed deliveries of any installation event converge to the same keys.
- The sweep backfills missed installation events (see Operation rules): at most one sweep interval of trial time is lost to the customer's benefit.
- The sweep never writes suspension on failure: `Observe` and `Refresh` write nothing when the Store read fails.
- Budget exhaustion, a nil `Provider`, a nil `Sandbox`, an LLM outage or a UI panic affect only their own feature.
- No persistence besides `account`'s keys. No queues, jobs or stored reports. The managed discovery job is a goroutine in `onboarding`.
- Logs: installation ID, event kind, delivery ID, counts, outcomes, timings. No paths, bodies, logins in bulk, or tokens.
- Standard library only in the façade.

## Test Specification

Tests build a real `Control` over a memory `Store`, a fake GitHub HTTP server behind a real `*Client`, a fake `Receiver`,
fake `Sandbox` and `Provider`, and a fixed clock.

### Unit Tests

- **Test name**: `TestNewValidatesWiring`
  **Scenario**: table: nil `GitHub`; nil `OAuth`; nil `Store`; nil `Receiver`; nil `Admissions`; nil `Approvals`; nil `Decisions`; nil `DecisionQueue`; 16-byte `CookieKey`; empty `BaseURL`; `Sandbox` set with nil `Tokens`; nil `Now`; nil `Sandbox`, `Provider` and `Tokens`.
  **Expected behavior**: error and nil `Control` for the first ten, no panic; the last two construct a working `Control`.
- **Test name**: `TestNewWorkerNeedsNoUISecrets`
  **Scenario**: `NewWorker` with nil `GitHub`, with nil `Store`, with nil `Admissions`, then with `GitHub`, `Store`, `Admissions` and `Now`;
  `InstallationAdded`, `Grant` and `Sweep` on the result.
  **Expected behavior**: errors and nil `Control` for the first three; the third handles all three calls, the tenant is
  stored, and the three handler methods return nil.
- **Test name**: `TestHandleEventRouting`
  **Scenario**: table over every `EventKind`, plus `InstallationID` 0.
  **Expected behavior**: `InstallationAdded` and `ReposRemoved` → one `ListRepos` and one `Observe`; `InstallationRemoved` → `Forget`, no GitHub call; all others → no call, nil; ID 0 → error, no call.
- **Test name**: `TestHasPrivateComesFromGitHub`
  **Scenario**: event with `RepoPrivate: true` while `ListRepos` returns only public repositories, and the reverse.
  **Expected behavior**: the `ListRepos` answer decides; the trial starts only in the reverse case.

### Integration Contract Tests

- **Test name**: `TestAccountWaitingForApprovalGetsNoEntitlement`
  **Scenario**: an installation with a private repository whose account is not approved, then one whose approval cannot be read; an `InstallationAdded` event and a `Sweep` for each.
  **Expected behavior**: no `Account` call at all, so no record and no trial; the event answers `nil` for the first and an error for the second; `Sweep` returns nil in both.
- **Test name**: `TestSweepRefreshesEveryInstallation`
  **Scenario**: three installations; `ListRepos` fails for the second; the Store write of the third (`CompareAndSwapAll`, the account write path) fails.
  **Expected behavior**: first observed; second refreshed without GitHub; third logged and skipped; `Sweep` returns nil; counts logged by ID only.
- **Test name**: `TestSweepBackfillsMissedInstallation`
  **Scenario**: no webhook was delivered; GitHub lists installation 7 with one private repository; the Store is empty; run `Sweep` once, then again an hour later.
  **Expected behavior**: after the first sweep the `entitlement` record exists for tenant 7, `Status` is `trial` with `TrialEnds` = first sweep time + 30 d; the second sweep does not move it. With only public repositories the record exists with zero `SuspendAfter`.
- **Test name**: `TestSweepForgetsOrphanTenants`
  **Scenario**: Store holds tenants 1, 2, 3 and budget activity under `SystemTenant`; GitHub lists installations 1 and 3.
  **Expected behavior**: tenant 2 has no keys afterwards; 1, 3 and the budget counter are unchanged; "forgotten: 1" logged by ID (T07 cleanup after a missed webhook).
- **Test name**: `TestSweepStopsWhenInstallationsUnavailable`
  **Scenario**: `ListInstallations` returns `ErrRateLimited`.
  **Expected behavior**: that error is returned; no record is written.
- **Test name**: `TestGrantRewritesEntitlement`
  **Scenario**: suspended installation; `Grant(until = now + 90 d)`.
  **Expected behavior**: the record under `entitlement` has `SuspendAfter == until`; one audit log line with the installation ID.
- **Test name**: `TestHandlersAreMounted`
  **Scenario**: request `/` without a session; `POST` to the discovery handler with a `Receiver` that rejects with 401.
  **Expected behavior**: redirect to login (or 401) from the UI; `401` with the rejection code from discovery.
- **Test name**: `TestSeamsDefaultToNotAvailable`
  **Scenario**: nil `Sandbox`, nil `Provider`; start managed onboarding with consent; request managed assist.
  **Expected behavior**: `ErrNoSandbox` and the assist "not available" error; no PR, no budget reservation.

### Boundary Tests

- **Test name**: `TestUninstallLeavesNoTenantKeys`
  **Scenario**: install, observe, record both consents, reserve budget, then `InstallationRemoved` twice.
  **Expected behavior**: no key remains for the tenant; another tenant and the `SystemTenant` budget counter are unchanged; the second delivery is a no-op (T07, T08 tenant scoping).
- **Test name**: `TestOrphanCleanupFailsSafe`
  **Scenario**: table: `ListInstallations` errors (`ErrRateLimited`, generic); returns an empty list with nil error; `Tenants` errors; `Forget` errors for one of two orphans.
  **Expected behavior**: no key deleted in the first three rows; refresh still ran when the list was available; in the last row the other orphan is forgotten and `Sweep` returns nil.
- **Test name**: `TestRevokedAccessSchedulesNothing`
  **Scenario**: after uninstall, GitHub answers 404 for the installation; call `Sweep`, the UI overview API for its repository, and a discovery upload.
  **Expected behavior**: sweep does not list it; UI authorization fails; upload is rejected by the `Receiver`; no sandbox job, no LLM call, no key written (T07).
- **Test name**: `TestReplayedAndForgedInstallationIDs`
  **Scenario**: replay `InstallationAdded` ten times; send a UI form and a discovery envelope carrying another tenant's installation ID.
  **Expected behavior**: one `first_private_at`, unchanged by replays; the foreign ID is ignored everywhere.
- **Test name**: `TestUIPanicIsContained`
  **Scenario**: a UI dependency panics during a request.
  **Expected behavior**: `500`; the process and the other handlers keep working.
- **Test name**: `TestControlImportsOnlyAllowedPackages`
  **Scenario**: direct imports from `go list ./internal/control/...`; `go list -deps` for the YAML library only.
  **Expected behavior**: no package imports `internal/gate/<child>` or `internal/app` directly (the `gate` façade reaches its
  children transitively); the façade imports no grandchild; YAML library only through `wire/*`.

### Behavior Tests

- **Test name**: `TestTrialLifecycleThroughTheFacade`
  **Scenario**: install with a private repository; hourly `Sweep` with the fake clock for 40 days; then `Grant`.
  **Expected behavior**: record shows notice from day 23, "1 day left" on day 29, `SuspendAfter` on day 37; after `Grant` it moves to the grant end. No check run is touched by `control` at any point (T21).
- **Test name**: `TestStoreOutageIsNotNonPayment`
  **Scenario**: Store reads fail during three sweeps across the suspension date.
  **Expected behavior**: no record is written; the last good record stands; `Sweep` returns nil (T21).
- **Test name**: `TestBudgetExhaustionStopsOnlyAssist`
  **Scenario**: exhaust the LLM budget; then run install, sweep, onboarding start and a policy save.
  **Expected behavior**: managed assist reports the budget state; everything else succeeds (T21).
- **Test name**: `TestBothOnboardingModesThroughTheFacade`
  **Scenario**: customer mode: start, merge PR 1, discovery upload, merge PR 2. Managed mode: without consent, with consent and a fake sandbox, with a nil sandbox.
  **Expected behavior**: exactly two PRs in customer mode after retries; managed: no consent → no job; success → policy on the onboarding PR; nil → "not available" (T18, T19).
- **Test name**: `TestControlFailureNeverReachesTheGate`
  **Scenario**: every `control` entry point fails (Store down, GitHub down, provider down).
  **Expected behavior**: errors return to `app` only; no check run, comment or `entitlement` value changes.

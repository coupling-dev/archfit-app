# UI

**Path**: internal/control/ui/
**Parent**: internal/control/ (Control plane)
**Submodules**: session, graph

## Purpose

UI is the browser surface of Archfit: sign-in, the organization overview, repository status and
onboarding actions, the YAML policy editor with drafts as pull requests, the read-only graph, the
explicit AI request, consent pages and the entitlement banner. It holds no state of its own: every
page is derived from GitHub and the control-plane modules at request time. Without it customers
could still work GitHub-first, but onboarding, schema-guided editing and consent would have no home.

## Functional Responsibilities

- Serve server-rendered pages (`html/template`) and embedded static assets (Monaco, `monaco-yaml`, small scripts).
- Authenticate every request through `session` and authorize every repository page at the needed permission.
- Overview: the user's repositories with their latest default-branch `archfit/state` check conclusion. No score.
- Repository status: onboarding `Progress`, actions to start onboarding or dispatch discovery, entitlement banner.
- Policy editor: load policy and schema, validate in the browser, save as a commit on a draft branch through
  `proposal` bound to the head SHA that was loaded, show the draft state (draft / awaiting review / published),
  mark ready for review.
- Graph: fetch the latest report artifact, decode it, build the view through `graph`, draw it, mark it stale.
- Assist: turn the form into an `assist.Request`, show the outcome or the pending customer run.
- Consent: record and revoke `source_processing` and `managed_llm` consent (repository administrators only).

## Subdomain Classification

Core-adjacent product surface. Volatility: high. Page flow, wording and editor behavior change with every
pilot; product.md leaves usability targets open.

## Encapsulated Knowledge

- Routes, templates, static assets, page wording, the HTTP status mapping of every error.
- The handler chain: recover → security headers → body limit → session → CSRF → authorize → page.
- The CSRF scheme: `hex(HMAC-SHA256(key = User.Token, "archfit-csrf"))` **[default]**, stateless and per session.
- The edit branch name `archfit/edit-<login>` **[default]** and the rules that turn a form into a `Change`.
- How check conclusions, draft states, plans and assist errors are worded for people.
- The drawing of the graph (vanilla JavaScript and SVG, layers as columns **[default]**; no SPA framework, no graph library).
- Bounds, each **[default]**: 100 repositories on the overview, 8 parallel lookups, 256 KB policy text, 512 KB request body,
  drawing up to 300 nodes and a filterable list above that.

## Public Contract

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
    Now        func() time.Time
}

// New returns the server-rendered UI mounted at "/". It serves its own static assets.
func New(d Deps) (http.Handler, error)
```

### Routes

Minimum repository permission in brackets. `{o}/{n}` is only a lookup key for `Sessions.Authorize`.

- `GET /` — overview; a landing page with a sign-in link when not signed in.
- `GET /auth/login`, `GET /auth/callback`, `POST /auth/logout` — handlers of `session`.
- `GET /static/…`, `GET /schema/policy.json` — embedded assets and `policy.Schema()`; no sign-in needed, no tenant data.
- `GET /r/{o}/{n}` [read] — status, onboarding progress, entitlement banner, links.
- `POST /r/{o}/{n}/onboarding` [admin] — form `mode` (`customer` | `managed`), `owners` (one per line).
- `POST /r/{o}/{n}/discover` [admin] — dispatch the discovery run of the Archfit workflow.
- `GET /r/{o}/{n}/policy` [read] — editor; optional `?branch=archfit/…`.
- `POST /r/{o}/{n}/policy` [write] — save; form `branch`, `head_sha`, `content`.
- `POST /r/{o}/{n}/policy/ready` [write] — mark the draft PR ready for review; form `branch`, `head_sha`.
- `GET /r/{o}/{n}/graph` [read] — graph page.
- `POST /r/{o}/{n}/assist` [write] — form `branch`, `head_sha`, `content`, `instruction`, `mode`.
- `GET /r/{o}/{n}/consent`, `POST /r/{o}/{n}/consent` [admin] — form `kind`, `action` (`record` | `revoke`).

### Status mapping

- Not signed in: `303` to `/auth/login?return=<path>` for `GET`, `401` for `POST`.
- `ErrForbidden` from `Authorize`: `404` **[default]**, so that repository names are not confirmed to outsiders.
- Missing or wrong CSRF token: `403`. Body over 512 KB **[default]**: `413`.
- `ErrStale`, or a `head_sha` that is not the draft head: `409`, the user's text kept in the editor, nothing written.
- Invalid form, unparseable YAML, `ErrInvalidRequest`, `ErrInvalidProposal`: `422`, text kept.
- Assist preconditions (`ErrNoDraft`, `ErrNoConsent`, `ErrNotManaged`, `ErrPolicyTooLarge`, `ErrNoAssistWorkflow`): `409` with a clear state.
  `ErrBusy`: `429`. `ErrManagedUnavailable`, `ErrBudgetExhausted`, `ErrProvider`: `503` with a clear state; the editor stays usable.
- `ErrRateLimited`: `503` with `Retry-After: 60` **[default]**. Any other upstream error: `502`. Panic: `500`.
- Every successful `POST` answers `303` to the page that shows the result.

## Integrations

### internal/control/ui/session (child)

- **Counterpart**: `internal/control/ui/session/module.md`
- **Direction**: ui depends on session
- **Strength**: functional (composition)
- **LCA / Rank / Distance**: control/ui / 1 / 1
- **Volatility**: high
- **Balanced?**: yes — parent and child
- **Shared knowledge**: ui mounts the three handlers at `/auth/login`, `/auth/callback`, `/auth/logout` (session builds the
  OAuth redirect URL from the callback path) and wraps logout in the CSRF check. Every other handler calls `User`, then
  `Authorize`. `ErrForbidden` is the sentinel `Authorize` returns for "not permitted". `ui.New` constructs the child itself:
  `session.New(session.Config{CookieKey, BaseURL}, d.OAuth, d.GitHub, d.GitHub, d.Now)`, with the github/api `Client` serving as
  `Users` and `Repos`. A `session.New` error is returned from `ui.New` unchanged. ui hands the cookie key over and never uses it.

<!-- contract: Sessions, User — restated from internal/control/ui/session/module.md -->
```go
type User struct {
    Login  string
    UserID int64
    Token  string // OAuth access token; lives only in the encrypted, signed cookie
}

type Sessions interface {
    // LoginHandler and CallbackHandler run the GitHub OAuth flow with a state cookie.
    LoginHandler() http.Handler
    CallbackHandler() http.Handler
    LogoutHandler() http.Handler
    // User returns the signed-in user, or false.
    User(r *http.Request) (User, bool)
    // Authorize returns the repository if the user has at least minPermission ("read" | "write" | "admin")
    // on it right now (cached for 60 seconds). The Repo carries the authoritative installation ID.
    Authorize(ctx context.Context, u User, fullName, minPermission string) (Repo, error)
}
```

<!-- contract: New, Config, ErrForbidden — restated from internal/control/ui/session/module.md -->
```go
var ErrForbidden = errors.New("session: not permitted")

type Config struct {
    CookieKey []byte // 32 bytes, from a secret reference
    BaseURL   string
}

// New fails unless CookieKey is 32 bytes and BaseURL is an absolute https URL (http allowed for localhost).
func New(c Config, o OAuth, u Users, repos Repos, now func() time.Time) (Sessions, error)
```

### internal/control/ui/graph (child)

- **Counterpart**: `internal/control/ui/graph/module.md`
- **Direction**: ui depends on graph
- **Strength**: functional (composition)
- **LCA / Rank / Distance**: control/ui / 1 / 1
- **Volatility**: high
- **Balanced?**: yes — parent and child
- **Shared knowledge**: ui supplies a decoded `Report` and the policy text, gets a `View`, serializes it as JSON into
  the page, links each node to `/r/{o}/{n}/policy#L<PolicyLine>` when the line is not 0, and shows the stale banner when `Stale`.

<!-- contract: View, Node, Link, Build — restated from internal/control/ui/graph/module.md -->
```go
type Node struct {
    Name       string
    Layer      string
    PolicyLine int // 1-based line of the module in the policy YAML; 0 if not found
}

type Link struct {
    From, To  string
    Observed  bool // seen in the report
    Permitted bool // allowed by policy
    Count     int
}

// View is a read-only picture. Stale is true when the policy text no longer matches the report's config_hash.
type View struct {
    Nodes []Node
    Links []Link
    Stale bool
}

// Build is pure. Nodes come from the policy's modules, links from the report's seams; a link is not
// Permitted when a gate finding with status "new" or "expired_waiver" names that module pair.
func Build(rep Report, policy []byte) View
```

### internal/control/account

- **Counterpart**: `internal/control/account/module.md`
- **Direction**: ui depends on account
- **Strength**: contract
- **LCA / Rank / Distance**: control / 1 / 1
- **Volatility**: high
- **Balanced?**: yes
- **Shared knowledge**: `Account.Status` for the banner (ui calls no other `Account` method); `Consents.Record`, `Revoke`, `Has`.
  `ErrBudgetExhausted` is the sentinel of account's budget that reaches ui only through `assist`.

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
    // Forget deletes every key of the installation (uninstall).
    Forget(ctx context.Context, installationID int64) error
}
```

<!-- contract: Consents, Consent, ConsentKind, ErrNoConsent — restated from internal/control/account/module.md -->
```go
type ConsentKind string

const (
    ConsentSourceProcessing ConsentKind = "source_processing" // managed discovery
    ConsentManagedLLM       ConsentKind = "managed_llm"
)

type Consent struct {
    Kind ConsentKind
    By   string // GitHub login of the administrator
    At   time.Time
}

// ErrNoConsent is returned by callers' guards when Has is false.
var ErrNoConsent = errors.New("account: consent not recorded")

type Consents interface {
    Record(ctx context.Context, installationID int64, c Consent) error
    Revoke(ctx context.Context, installationID int64, k ConsentKind) error
    // Has is false on any error: no consent, no call.
    Has(ctx context.Context, installationID int64, k ConsentKind) bool
}
```

### internal/control/onboarding

- **Counterpart**: `internal/control/onboarding/module.md`
- **Direction**: ui depends on onboarding
- **Strength**: contract
- **LCA / Rank / Distance**: control / 1 / 1
- **Volatility**: high
- **Balanced?**: yes
- **Shared knowledge**: `Progress`, `StartCustomer`, `StartManaged`. ui never mounts `DiscoveryHandler` (the façade does).
  Before `StartManaged` ui checks `Consents.Has(ConsentSourceProcessing)` and sends the administrator to the consent page
  when it is false **[default]**. `ErrNoSandbox` is the sentinel onboarding returns when managed discovery is not
  configured; ui shows "managed discovery is not available".

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
    // StartCustomer opens PR 1: the workflow, the settings file with owners, and CODEOWNERS entries.
    StartCustomer(ctx context.Context, r Repo, owners []string) (Draft, error)
    // StartManaged requires ConsentSourceProcessing, opens the onboarding PR and runs the sandbox job.
    StartManaged(ctx context.Context, r Repo, owners []string) (Draft, error)
    // DiscoveryHandler serves POST /v1/discoveries and opens PR 2 with the proposed policy.
    DiscoveryHandler() http.Handler
}
```

### internal/control/assist

- **Counterpart**: `internal/control/assist/module.md`
- **Direction**: ui depends on assist
- **Strength**: contract
- **LCA / Rank / Distance**: control / 1 / 1
- **Volatility**: high
- **Balanced?**: yes
- **Shared knowledge**: `Assist.Propose` and its sentinels, all tested with `errors.Is`: `ErrInvalidRequest`, `ErrNoDraft`,
  `ErrManagedUnavailable`, `ErrNoConsent`, `ErrNotManaged`, `ErrPolicyTooLarge`, `ErrBusy`, `ErrProvider`,
  `ErrInvalidProposal`, `ErrNoAssistWorkflow`, plus `ErrStale` and `ErrBudgetExhausted` passed through from their owners.
  `AssistWorkflowPath` is the constant `.github/workflows/archfit-assist.yaml`, the customer-mode workflow whose latest
  run ui reads to show pending, failed and completed. ui never sees a `Provider`, a prompt, or model text.

<!-- contract: Assist, Request, Result, Mode — restated from internal/control/assist/module.md -->
```go
type Mode string

const (
    ModeManaged  Mode = "managed"  // App calls the Provider after consent, under the budget
    ModeCustomer Mode = "customer" // App dispatches the assist workflow in the customer's CI
)

// Request is an explicit user request. Context is limited to the draft policy and the instruction.
type Request struct {
    Repo        Repo
    Branch      string // the draft branch being edited
    HeadSHA     string // branch head the user was looking at; binds the proposal
    Instruction string
    By          string // login
}

type Result struct {
    Mode  Mode
    Draft Draft  // managed: the draft PR now carrying the proposal commit
    RunURL string // customer: the dispatched workflow's runs page
}

type Assist interface {
    // Propose never publishes: the result is always a reviewable diff on the draft PR.
    Propose(ctx context.Context, mode Mode, r Request) (Result, error)
}
```

### internal/control/proposal

- **Counterpart**: `internal/control/proposal/module.md`
- **Direction**: ui depends on proposal
- **Strength**: contract
- **LCA / Rank / Distance**: control / 1 / 1
- **Volatility**: high (this side), low (proposal)
- **Balanced?**: yes
- **Shared knowledge**: `Get` to load a draft and its head, `Propose` to save, `Ready` to ask for review. ui never calls
  `SetBody`. `ErrStale` is the sentinel meaning the branch moved since the editor loaded it. `Pulls` is the github/api
  pull-request role interface that only proposal's constructor takes.

<!-- contract: Proposals, Change, Draft, DraftState — restated from internal/control/proposal/module.md -->
```go
// Change is a set of file edits proposed as a branch plus a draft pull request.
type Change struct {
    Repo    RepoRef
    Branch  string // deterministic, e.g. "archfit/onboarding"; the idempotency key
    Base    string // base branch
    Title   string
    Body    string
    Message string
    Files   []FileChange
}

type DraftState string

const (
    DraftNone     DraftState = "none"
    DraftOpen     DraftState = "draft"           // draft PR
    DraftInReview DraftState = "awaiting_review" // ready for review, not merged
    DraftMerged   DraftState = "published"
    DraftClosed   DraftState = "closed"
)

type Draft struct {
    Number  int
    State   DraftState
    HeadSHA string
    URL     string
}

type Proposals interface {
    // Propose creates or updates the branch and its draft PR. Retrying never opens a second PR.
    // expectHeadSHA, when not "", must equal the branch head or ErrStale is returned.
    Propose(ctx context.Context, c Change, expectHeadSHA string) (Draft, error)
    Get(ctx context.Context, r RepoRef, branch string) (Draft, error)
    SetBody(ctx context.Context, r RepoRef, number int, body string) error
    Ready(ctx context.Context, r RepoRef, number int) error
}
```

<!-- contract: ErrStale, New — restated from internal/control/proposal/module.md -->
```go
// ErrStale means the branch moved since the caller read it; the caller must reload and re-apply.
var ErrStale = errors.New("proposal: branch head changed")

func New(gh Changes, pulls Pulls) Proposals
```

### internal/github/api

- **Counterpart**: `internal/github/api/module.md`
- **Direction**: ui depends on github/api
- **Strength**: contract
- **LCA / Rank / Distance**: root / 2 / 2
- **Volatility**: high (this side), low (github/api)
- **Balanced?**: yes — contract coupling
- **Shared knowledge**: `Deps.GitHub` is used as `Repos` (`File`, `BranchHead`; and whole, with `Users`, as the
  argument of `session.New`), `Checks` (`CheckRuns`, read only), `Actions` (`LatestRun`,
  `Artifact`, `Dispatch`) and `Users` (`UserRepos`). ui builds `FileChange` values. `ErrNotFound` is the sentinel for a
  missing file, run or artifact; `ErrRateLimited` is the sentinel after bounded backoff. `TokenSource` is an opaque part
  of `New`'s signature for this module. `Pull` is the github/api pull-request record; ui never sees one. The check names
  `archfit/state` and `archfit/gate` and the artifact name `archfit-report` are GitHub objects of the root contract.

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
    ID      int64
    Account string // organization or user login
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

<!-- contract: Changes, FileChange, NewPull — restated from internal/github/api/module.md -->
```go
type FileChange struct {
    Path    string
    Content []byte
}

type NewPull struct {
    Title, Body string
    Head, Base  string
    Draft       bool
}

// Changes writes branches, commits and pull requests as the App.
type Changes interface {
    // EnsureBranch creates branch from fromRef if it does not exist.
    EnsureBranch(ctx context.Context, r RepoRef, branch, fromRef string) error
    Commit(ctx context.Context, r RepoRef, branch, message string, files []FileChange) (sha string, err error)
    // EnsurePull returns the open PR for Head, creating it if needed.
    EnsurePull(ctx context.Context, r RepoRef, p NewPull) (Pull, error)
    UpdatePullBody(ctx context.Context, r RepoRef, number int, body string) error
    MarkReady(ctx context.Context, r RepoRef, number int) error
}
```

<!-- contract: Users — restated from internal/github/api/module.md -->
```go
// Users answers questions with a user's OAuth token; nothing here uses App credentials.
type Users interface {
    // Permission returns "admin" | "write" | "read" | "none" for the user on the repository.
    Permission(ctx context.Context, userToken, fullName string) (string, error)
    // UserRepos lists repositories the user can access through App installations.
    UserRepos(ctx context.Context, userToken string) ([]Repo, error)
}
```

<!-- contract: Client, New — restated from internal/github/api/module.md -->
```go
// Client implements every role interface. Consumers depend on the role interfaces they need.
type Client struct{ /* unexported */ }

func New(ts TokenSource, hc *http.Client) *Client
```

### internal/wire/policy

- **Counterpart**: `internal/wire/policy/module.md`
- **Direction**: ui depends on wire/policy
- **Strength**: contract
- **LCA / Rank / Distance**: root / 2 / 2
- **Volatility**: high (this side), moderate (wire/policy)
- **Balanced?**: yes — contract coupling
- **Shared knowledge**: `PolicyPath` is the constant naming the file the editor loads and saves. `Schema()` is served
  unchanged at `/schema/policy.json` for `monaco-yaml`. `Waivers` is the server-side YAML syntax check of a save
  **[default]**: only `wire/*` may import a YAML library and wire/policy publishes no plain validation function.
  `Modules` is the second half of that check and the graph page's test for "this text parses". `Digest` is used only
  inside `graph`. `BaselinePath` and `LabelsPath` are the constants naming `.archfit-baseline.json` and
  `.archfit-labels.yaml`; ui never reads or writes either.

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

<!-- contract: Schema — restated from internal/wire/policy/module.md -->
```go
// Schema returns the engine v2.3.1 config JSON Schema, embedded unchanged, for the editor.
func Schema() []byte
```

<!-- contract: ModuleDef, Modules — restated from internal/wire/policy/module.md -->
```go
// ModuleDef is the subset of a policy module the App displays.
type ModuleDef struct {
    Name  string
    Layer string // may be ""
    Line  int    // 1-based line of the module key in the policy text
}

// Modules lists the modules declared under the policy's `modules:` map, in file order. It ignores every other key.
func Modules(policy []byte) ([]ModuleDef, error)
```

### internal/wire/settings

- **Counterpart**: `internal/wire/settings/module.md`
- **Direction**: ui depends on wire/settings
- **Strength**: contract
- **LCA / Rank / Distance**: root / 2 / 2
- **Volatility**: high (this side), moderate (wire/settings)
- **Balanced?**: yes — contract coupling
- **Shared knowledge**: ui reads `Path` at the default branch and calls `Parse` to learn `Workflow` (which run holds the
  report, which workflow to dispatch for discovery), `Gate` and `DataMode` (which assist mode to offer). ui does not edit
  the settings file: wire/settings publishes no schema, and settings change through the onboarding PR or GitHub **[default]**.

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

<!-- contract: Parse, Render — restated from internal/wire/settings/module.md -->
```go
// Parse strictly decodes and validates a settings file. Unknown keys are errors.
func Parse(raw []byte) (Settings, error)

// Render returns canonical YAML for s. For any s returned by Parse, Parse(Render(s)) == s.
func Render(s Settings) []byte
```

### internal/wire/report

- **Counterpart**: `internal/wire/report/module.md`
- **Direction**: ui depends on wire/report
- **Strength**: contract (published language)
- **LCA / Rank / Distance**: root / 2 / 2
- **Volatility**: high (this side), moderate (wire/report)
- **Balanced?**: yes — contract coupling
- **Shared knowledge**: ui downloads the artifact with `MaxBytes` as the cap, calls `Decode`, and hands the `Report` to
  `graph.Build` unread. The artifact holds the raw engine state JSON; `Decode` validates it against the v2.3.1 state
  schema and reduces it to the no-source form, so fields the decoder drops (measurement, `matched_by`, task declarations,
  metric provenance, seam scores) never reach a template and ui never renders them. ui parses the artifact in no other way.
  `StateSchemaV1` is the constant `Decode` checks. For this module each nested type is opaque:
  - `Decision` is an opaque part of `Report` for this module.
  - `Comparison` is an opaque part of `Report` for this module.
  - `Reference` is an opaque part of `Report` for this module.
  - `Dimension` is an opaque part of `Report` for this module.
  - `Coverage` is an opaque part of `Report` for this module.
  - `Finding` is an opaque part of `Report` for this module.
  - `AgentTask` is an opaque part of `Report` for this module.
  - `Seam` is an opaque part of `Report` for this module.

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

<!-- contract: Decode — restated from internal/wire/report/module.md -->
```go
// Decode strictly parses payload as the no-source transport. It fails on: size over MaxBytes, a key the
// v2.3.1 state schema does not define, wrong schema_version, an enum value outside its set, strings over
// the per-field caps, absolute or parent-escaping paths, multi-line text, or duplicate finding IDs.
func Decode(payload []byte) (Report, error)
```

### internal/control (parent façade)

- **Counterpart**: `internal/control/module.md`
- **Direction**: control depends on ui (calls `New`, exposes the handler as `UIHandler`)
- **Strength**: functional (composition)
- **LCA / Rank / Distance**: control / 1 / 1
- **Volatility**: high
- **Balanced?**: yes — parent and child
- **Shared knowledge**: `Deps` and `New` above. `control` passes its `OAuth`, `CookieKey` and `BaseURL` through and returns a
  `ui.New` error from its own constructor.

### internal/github/auth

- **Counterpart**: `internal/github/auth/module.md`
- **Direction**: ui depends on github/auth (type only)
- **Strength**: contract
- **LCA / Rank / Distance**: root / 2 / 2
- **Volatility**: high (this side), low (github/auth)
- **Balanced?**: yes — contract coupling
- **Shared knowledge**: ui receives an `OAuth` in `Deps` and hands it to `session.New`. ui calls none of its methods.

<!-- contract: OAuth, UserToken — restated from internal/github/auth/module.md -->
```go
type UserToken struct {
    AccessToken string
    Login       string
    UserID      int64
}

// OAuth is the GitHub user login flow.
type OAuth interface {
    AuthorizeURL(state, redirectURL string) string
    Exchange(ctx context.Context, code string) (UserToken, error)
}
```

## Internal Design

### Composition

- `ui.New` builds `session` first and fails if it fails; nothing else in `New` can fail.
- `session` turns a request into a `User` and a repository name into an authorized `Repo`. It is the only source of
  tenant identity in the UI.
- `graph` turns a `Report` and policy text into a `View`. Pure.
- ui's own code is handlers, templates (`templates/`, transparent folder), static assets (`static/`, transparent folder,
  Monaco and `monaco-yaml` vendored, all embedded with `go:embed`) and the handler chain. The two children do not know each other.

### Handler chain

1. Recover panics → `500`; log route pattern and request ID only.
2. Security headers: `Content-Security-Policy: default-src 'self'; script-src 'self'; style-src 'self' 'unsafe-inline';
   worker-src 'self' blob:; frame-ancestors 'none'` (Monaco needs inline styles and blob workers), `X-Content-Type-Options: nosniff`,
   `Referrer-Policy: same-origin` **[default]**.
3. `http.MaxBytesReader` at 512 KB **[default]** on every `POST`.
4. `Sessions.User`. Missing → login redirect or `401`.
5. `POST` only: constant-time compare of form field `csrf` with the token derived from `User.Token`.
6. Repository routes: `Sessions.Authorize(user, "{o}/{n}", min)`. The returned `Repo` is handed to the page handler as an
   argument and is the only repository identity a page may use.

Every response carries an `X-Request-Id` header with the request ID of its log line **[default]**.

### Pages

- Overview: `Users.UserRepos(user.Token)`; the list is already scoped by GitHub to what the user can access, so no
  per-repository `Authorize` is needed. At most 100 repositories **[default]**, sorted by name, 8 lookups in parallel **[default]**. Per repository:
  `Repos.BranchHead(repo.RepoRef, repo.DefaultBranch)` → `Checks.CheckRuns(head, "archfit/state")`.
  A default-branch upload publishes `archfit/state` only; `archfit/gate` lives on pull requests and is shown on the editor
  page for the draft head. No check run on the head: "no state yet". A failed lookup: "unknown" for that
  row only. Conclusions are shown as words with their reason title. No score, no totals, no ranking.
- Status: `Onboarding.Progress(repo)` (stage, diagnostic, open draft link), `Account.Status(repo.InstallationID, now)` banner:
  trial end date, grace, suspended with the recovery sentence; nothing for `public` and `granted`. A `Status` error hides the
  banner and is logged; it never fails the page. History is a link to the repository's pull requests from `archfit/` branches
  and to its check runs **[default]**: report history is what GitHub keeps.
- Discover: requires stage `workflow_merged` or later; `Actions.Dispatch(Settings.Workflow, DefaultBranch, {"discover": "true"})`.
- Editor: branch is `archfit/edit-<lower-case login>`, or `?branch=` matching `^archfit/[A-Za-z0-9._/-]+$` with no `..`, no
  `//` and no trailing `/`. `Proposals.Get`:
  when the draft is `DraftOpen` or `DraftInReview`, load `PolicyPath` at `Draft.HeadSHA` and set `head_sha` to it; otherwise load
  from the default branch with `head_sha` empty. The page shows the draft state in the words draft / awaiting review /
  published (and closed), the draft's check conclusions (engine validation is the check on the draft PR), and the PR link. The
  form is a plain `textarea` that Monaco upgrades; saving works without JavaScript. The script marks the graph link "stale"
  as soon as the buffer differs from the loaded text or has diagnostics.
- Save: checks in order: size ≤ 256 KB **[default]** and valid UTF-8; `policy.Waivers(content)` and `policy.Modules(content)`
  succeed, else `422`; `Proposals.Get`; content
  equal to the file currently on the branch (at `Draft.HeadSHA` for an open draft, else on the default branch) is a no-op with
  a notice, whatever `head_sha` says, so a retried save never conflicts with itself; then an open draft whose head differs
  from `head_sha` (including an empty `head_sha`) is `409`; then `Proposals.Propose(Change{Repo: repo.RepoRef, Branch, Base: repo.DefaultBranch, Title, Body, Message,
  Files: [{PolicyPath, content}]}, head_sha)`. Title `"Archfit policy edit by @<login>"`, fixed body, message
  `"archfit: edit policy (@<login>)"` **[default]**. A browser submits every `textarea` with CRLF line ends: before the
  comparison ui turns CRLF back into LF unless the file on the branch already uses CRLF **[default]**. Nothing else in the
  text is touched. A refused save, ready or assist re-renders the editor from the form alone and asks GitHub nothing.
- Ready: `Proposals.Get`; head must equal `head_sha`, state must be `DraftOpen`; then `Proposals.Ready(number)`. ui never merges:
  publication is the merge on GitHub under the customer's ruleset and owner approval, the same path as a GitHub-first edit.
- Graph: settings → `LatestRun(Settings.Workflow, DefaultBranch)` → `Artifact(run.ID, "archfit-report", report.MaxBytes)` →
  `report.Decode` → policy text (open draft of the user, else default branch) → `graph.Build`. The artifact is the raw engine
  state JSON; only the decoded no-source `Report` is used. When the chosen policy text fails `policy.Modules` (a broken draft
  pushed on GitHub), ui builds from the default-branch policy instead, the last valid text, and forces the stale banner with
  "the draft does not parse" **[default]**. No run or artifact (GitHub retention): "no report available". Decode error:
  "report unavailable"; never a partial graph. The `View` is embedded as JSON in a `script type="application/json"` element.
  Links with `Permitted` false are drawn as violations.
- Assist: authorize `write`; if `content` differs from the file at `head_sha`, save it first (same rules as Save) and use the
  new head; then `Assist.Propose(mode, Request{Repo, Branch, HeadSHA, Instruction, By: login})`. Managed is offered only when
  settings say `managed` and `Consents.Has(ConsentManagedLLM)`; assist enforces both anyway. Managed success: redirect to the
  editor on the new head with a link to the PR diff. Customer success: redirect to the editor with `?since=<head_sha>`, the
  head the request was bound to; with `since` the editor shows the run state from
  `Actions.LatestRun(AssistWorkflowPath, branch)`: no run yet or not completed → pending; completed and not `success` → failed;
  completed and the draft head differs from `since` → completed, reload offered; completed with the head still at `since` →
  pending (that run is an older one). ui keeps no state, so the link beside the state is the workflow's runs page built from
  the authorized repository name and `AssistWorkflowPath`, the same page `Result.RunURL` names **[default]**. `WorkflowRun` has no timestamps, so latency is visible as
  the pending state and the link, not as a duration. Stated limit.
- Consent: `GET` shows both kinds with `Consents.Has` and a plain disclosure of what leaves the customer's environment.
  `POST record` → `Consents.Record(repo.InstallationID, Consent{Kind, By: login, At: now})`; `revoke` → `Consents.Revoke`. Consent
  is per installation while the permission check is per repository: a repository administrator consents for the installation,
  and the page says so **[default]**. Managed features additionally need `data_mode: managed` in the owner-approved settings of each repository.

## Change Vectors

- Page flow, wording, styling, templates, the graph drawing, the editor integration.
- New pages that read existing contracts (check details, waiver list).
- The CSRF scheme or security headers.
- Bounded visual edits in the graph: a new handler proposing a text patch through the same save path.
- Splitting handlers into more child modules when a second contributor works here.

## Constraints and Invariants

- Tenant identity only from the authorized session: the installation ID and repository used by any page come from the `Repo`
  that `Sessions.Authorize` returned (or from `Users.UserRepos` on the overview). IDs in URLs, queries, forms or headers are
  never read. The `{o}/{n}` path segment is a lookup key and proves nothing.
- Every repository handler authorizes on every request, `GET` included. A revoked user loses access within session's 60 second cache **[default]**.
- No repository score, no aggregate, no ranking, anywhere.
- Draft, awaiting review and published are pull-request states read from GitHub. ui stores no draft, no version, no lock.
- Stale drafts are refused, not merged: every save, ready and assist carries the loaded `head_sha`.
- Invalid YAML is never saved by ui and marks the graph stale. Schema validity is not engine validity: the page says that
  the check on the draft PR is the authoritative validation.
- ui never rewrites policy text: it saves the user's bytes as they are. Comments and YAML constructs the App does not
  understand survive every round trip. The graph never writes.
- AI only on an explicit `POST` by a user with `write`. Nothing is auto-published. Model text is never rendered by ui; users
  see proposals as the diff on GitHub. A failed assist request leaves the editor, schema completion and saving working.
- `html/template` auto-escaping everywhere; no `template.HTML` for values that came from GitHub, a report, or a form.
- Static assets and the schema carry no tenant data; everything else requires a session.
- Logs: request ID, route pattern, status, duration, user ID, repository ID, installation ID, outcome. No form content, no
  policy text, no instruction, no report content, no cookie or token.
- Inherited: control-plane code. The gate never calls ui; a panic or outage here changes no check. Standard library only
  on the Go side; vendored browser assets are files, not Go dependencies.
- Bounded work per request, each **[default]** except the last: 100 repositories, 8 parallel GitHub lookups, 512 KB bodies;
  5 MB artifact (`report.MaxBytes`).

## Test Specification

Harness: `ui.New` behind `httptest` with a fixed 32-byte cookie key and a fake `OAuth`, so the real `session` and `graph` run, the real `account` over a memory Store and the real
`proposal`. GitHub is faked at the HTTP boundary behind a real github/api `Client`. `Onboarding` and `Assist` are faked at
their interfaces except where a test says otherwise. The clock is injected.

### Unit Tests

- **Test name**: `TestCSRFToken`
  **Scenario**: table: token of user A checked for A; for B; empty; truncated; A after re-login with a new OAuth token.
  **Expected behavior**: only the first row passes; comparison is constant-time.
- **Test name**: `TestEditBranchName`
  **Scenario**: table: login `Alice`; login with a hyphen; `?branch=archfit/policy`; `?branch=main`; `?branch=archfit/../main`; `?branch=archfit/a b`.
  **Expected behavior**: `archfit/edit-alice`; hyphen kept; accepted; the last three are `422`.
- **Test name**: `TestStatusMapping`
  **Scenario**: table of every error in the Status mapping list, injected through the fakes.
  **Expected behavior**: the documented status; `Retry-After` on `503` for rate limits; the editor text is echoed back on `409` and `422`.
- **Test name**: `TestWording`
  **Scenario**: table of `DraftState`, `Plan`, check conclusions and assist sentinels.
  **Expected behavior**: draft / awaiting review / published / closed; a banner only for trial, grace, suspended; one distinct sentence per sentinel.

### Integration Contract Tests

- **Test name**: `TestRoutesAndMinimumPermissions`
  **Scenario**: table of every route × user permission `none`, `read`, `write`, `admin`, and not signed in.
  **Expected behavior**: `303` to login or `401` without a session; `404` below the minimum; success at and above it;
  `/static/` and `/schema/policy.json` answer without a session.
- **Test name**: `TestOverviewReadsStateOnDefaultBranchHead`
  **Scenario**: table of repositories: `archfit/state` success on the default-branch head; neutral; no check run; `BranchHead` fails.
  **Expected behavior**: one `BranchHead` and one `CheckRuns(head, "archfit/state")` per repository; the rows read healthy,
  attention, "no state yet", "unknown"; `archfit/gate` is never looked up here; one failed row does not fail the page.
- **Test name**: `TestSaveProposesBoundChange`
  **Scenario**: load the editor on an open draft at head `a…`, change the text, save.
  **Expected behavior**: one commit on `archfit/edit-<login>` with exactly `.archfit.yaml` and the submitted bytes, expected head `a…`,
  base = default branch; `303` to the editor showing "draft".
- **Test name**: `TestFirstSaveOpensOneDraftPR`
  **Scenario**: no draft exists; save twice with the same content, the second as a retry of a timed-out first.
  **Expected behavior**: one branch, one draft PR, one commit; the second save (empty `head_sha`, identical content) is a
  no-op with a notice, not a `409`; the same retry with different content is `409`.
- **Test name**: `TestGraphPageBuildsFromArtifact`
  **Scenario**: the healthy wire fixture as the `archfit-report` artifact of the latest default-branch run.
  **Expected behavior**: the embedded JSON equals `graph.Build(report, policy)`; node links point to `#L<line>`; no stale banner;
  marker strings placed in fields the decoder drops (`matched_by`, measurement, task declarations) appear nowhere in the page.
- **Test name**: `TestConsentRecordAndRevoke`
  **Scenario**: an administrator records `managed_llm`, then revokes it.
  **Expected behavior**: `Consents.Has` is true then false for the installation of the authorized `Repo`; `By` is the session login.
- **Test name**: `TestOnboardingAndDiscoverActions`
  **Scenario**: start customer onboarding with two owners; start managed without consent; dispatch discovery before and after `workflow_merged`.
  **Expected behavior**: `StartCustomer` gets the parsed owners; managed without consent redirects to the consent page and never calls
  `StartManaged`; discovery dispatch is refused before the stage and sends input `discover=true` on the settings workflow after it.

### Boundary Tests

- **Test name**: `TestTenantIdentityOnlyFromSession` (T07, T17)
  **Scenario**: a user of installation A sends every route with installation B's ID and repository ID in query, form and headers, and
  with `{o}/{n}` of a repository in B.
  **Expected behavior**: supplied IDs have no effect; B's repository answers `404`; no Store key of B is read or written; no GitHub
  call uses B's installation.
- **Test name**: `TestOverviewShowsOnlyAccessibleRepositories` (T17)
  **Scenario**: GitHub lists three repositories for the user; the fake holds five more in the same and other installations.
  **Expected behavior**: exactly three rows; no lookup is made for the others.
- **Test name**: `TestNewRejectsBadSessionConfig`
  **Scenario**: table: 16-byte `CookieKey`; `BaseURL` `http://app.example`; relative `BaseURL`; then a valid config.
  **Expected behavior**: `ui.New` returns the `session.New` error and a nil handler for the first three; no panic; a handler for the last.
- **Test name**: `TestCSRFRequiredOnEveryPost`
  **Scenario**: every `POST` route, logout included, without a token and with another user's token.
  **Expected behavior**: `403`; no sibling module or GitHub write is called.
- **Test name**: `TestOversizedAndInvalidInput`
  **Scenario**: table: 600 KB body; 300 KB policy; invalid UTF-8; YAML with a tab indent; 50 owners lines with one malformed.
  **Expected behavior**: `413` for the first, `422` for the rest; nothing is written.
- **Test name**: `TestNoScoreAnywhere` (T17)
  **Scenario**: render every page with healthy, blocked and attention fixtures.
  **Expected behavior**: no number that aggregates findings or dimensions, no grade, no percentage, no sort by health.
- **Test name**: `TestHostileStringsAreEscaped`
  **Scenario**: repository names, module names, diagnostics and check titles containing HTML and script text.
  **Expected behavior**: escaped in HTML and in the embedded JSON; the CSP header is present on every page.
- **Test name**: `TestLogsCarryOnlyIdentifiers`
  **Scenario**: run save, assist, consent and graph with marker strings in content, instruction and report.
  **Expected behavior**: no marker, token or cookie value in any log line.

### Behavior Tests

- **Test name**: `TestRevokedUserLosesEveryPage` (T07, T17)
  **Scenario**: a signed-in user with `write` loses repository access at GitHub; 61 seconds pass.
  **Expected behavior**: status, editor, graph and every `POST` answer `404`; the overview no longer lists the repository.
- **Test name**: `TestRemovedRepositoryDisappears` (T07)
  **Scenario**: the repository is removed from the installation while the user keeps `admin` on it.
  **Expected behavior**: `404` on its pages; no write is attempted.
- **Test name**: `TestStaleDraftCannotBePublishedUnnoticed` (T17)
  **Scenario**: two tabs load head `a…`; tab one saves (head `b…`); tab two saves, then presses ready; also: tab two loaded from the
  default branch before any draft existed.
  **Expected behavior**: every action of tab two answers `409` with its text kept and a reload link; the branch holds only tab one's
  commit; the PR is not marked ready.
- **Test name**: `TestInvalidYAMLIsRefusedAndGraphGoesStale` (T17)
  **Scenario**: save broken YAML; then open the graph while the draft differs from the report's policy.
  **Expected behavior**: `422` and no commit; the graph page shows the stale banner and still draws the report. Then a broken
  draft is pushed on GitHub: the graph page draws from the default-branch policy with the stale banner and "the draft does not parse".
- **Test name**: `TestUIAndGitHubFirstEditsShareOnePath` (T17)
  **Scenario**: one policy edit through the editor, one pushed directly to the draft branch on GitHub.
  **Expected behavior**: both are commits on a PR against the default branch; the editor shows the GitHub-pushed head after reload and
  refuses to save over it from an older load; ui offers no merge or approval action.
- **Test name**: `TestUnknownYAMLConstructsSurviveASave` (T17)
  **Scenario**: a policy with comments, anchors and keys the schema does not know; change one line and save.
  **Expected behavior**: the committed bytes equal the submitted bytes; nothing is reformatted or dropped.
- **Test name**: `TestAssistIsExplicitAndReviewable` (T20)
  **Scenario**: real `assist` with a fake `Provider`; load pages, save, open the graph; then one assist `POST`.
  **Expected behavior**: the provider is called once, only by the `POST`; the result is a new commit on the draft PR; the PR state is
  unchanged; the provider's text appears in no response body.
- **Test name**: `TestAssistFailureLeavesEditingWorking` (T20)
  **Scenario**: table: provider failing; budget exhausted; managed unavailable; stale head.
  **Expected behavior**: a clear state per row with the documented status; the editor text is kept; a following save succeeds;
  `/schema/policy.json` still serves.
- **Test name**: `TestCustomerAssistShowsPendingFailedCompleted` (T20)
  **Scenario**: customer-mode request; the fake run is queued, then failed; a second run succeeds and pushes a commit.
  **Expected behavior**: pending with the run link; failed; completed with a reload offer; no Store key is written and no credential field
  exists on the form.

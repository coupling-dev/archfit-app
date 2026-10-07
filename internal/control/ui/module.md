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

- Serve server-rendered pages (`html/template`) and embedded static assets (one stylesheet, two small scripts).
- Authenticate every request through `session` and authorize every repository page at the needed permission.
- Overview: the user's repositories with their latest default-branch `archfit/state` check conclusion. No score.
- Repository status: onboarding `Progress`, actions to start onboarding or dispatch discovery, entitlement banner.
- Policy editor: load the policy into a plain text area, check it on the server (Check, and before every save) and
  list its problems by line, save as a commit on a draft branch through `proposal` bound to the head SHA that was
  loaded, show the draft state (draft / awaiting review / published), mark ready for review.
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
- `POST /r/{o}/{n}/policy/check` [read] — check the text and save nothing; form `branch`, `head_sha`, `content`.
- `POST /r/{o}/{n}/policy/ready` [write] — mark the draft PR ready for review; form `branch`, `head_sha`.
- `GET /r/{o}/{n}/graph` [read] — graph page.
- `POST /r/{o}/{n}/assist` [write] — form `branch`, `head_sha`, `content`, `instruction`, `mode`.
- `GET /r/{o}/{n}/consent`, `POST /r/{o}/{n}/consent` [admin] — form `kind`, `action` (`record` | `revoke`).

### Status mapping

- Not signed in: `303` to `/auth/login?return=<path>` for `GET`, `401` for `POST`.
- `ErrForbidden` from `Authorize`: `404` **[default]**, so that repository names are not confirmed to outsiders.
  `api.ErrNotFound` from any GitHub call a page does not handle itself: `404` with the same "Not found." wording.
- Missing or wrong CSRF token: `403`. Body over 512 KB **[default]**: `413`.
- `ErrStale`, or a `head_sha` that is not the draft head: `409`, the user's text kept in the editor, nothing written.
- Invalid form, a policy text over 256 KB or not UTF-8, a text `policy.Diagnose` finds problems in, `ErrInvalidRequest`,
  `ErrInvalidProposal`: `422`, text kept. A save or assist refused by `Diagnose` also lists the problems under the editor.
- Assist preconditions (`ErrNoDraft`, `ErrNoConsent`, `ErrNotManaged`, `ErrPolicyTooLarge`, `ErrNoAssistWorkflow`): `409` with a clear state.
  `assist.ErrBusy`: `429`. `ErrManagedUnavailable`, `ErrBudgetExhausted`, `ErrProvider`: `503` with a clear state; the editor stays usable.
- Onboarding states, each `409` with fixed wording: `onboarding.ErrNoSandbox` "Managed discovery is not available.",
  `onboarding.ErrAlreadyOnboarded` "This repository is already onboarded. Reload the page to see its state.",
  `onboarding.ErrBusy` "Managed discovery is busy with other repositories. Try again in a few minutes."
- `ErrRateLimited`: `503` with `Retry-After: 60` **[default]**. Any other upstream error: `502`. Panic: `500`.
- Every successful `POST` answers `303` to the page that shows the result, except Check: it answers `200` with the
  editor, the problem list or "The check found no problems.", because nothing changed that a redirect could show.

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
  the page, links each node to `/r/{o}/{n}/policy#L<PolicyLine>` when the line is not 0, shows the stale banner
  when `Stale`, and the incomplete banner (`id="incomplete"`) when `Incomplete`. The template and the script use a
  link's `Status` as its CSS class and print it as is.

<!-- contract: View, Node, Link, LinkStatus, Build — restated from internal/control/ui/graph/module.md -->
```go
type Node struct {
    Name       string
    Layer      string
    PolicyLine int // 1-based line of the module in the policy YAML; 0 if not found
}

// LinkStatus is what the report's findings say about a module pair. It is a display fact, never a check input.
type LinkStatus string

const (
    StatusViolation LinkStatus = "violation" // an active gate finding (wire/report Finding.Active), or a gate finding of unknown status, names the pair
    StatusAccepted  LinkStatus = "accepted"  // a gate finding accepted by the baseline or a waiver names the pair
    StatusAdvisory  LinkStatus = "advisory"  // another finding that is not fixed names the pair
    StatusObserved  LinkStatus = "observed"  // a seam that no current finding names
)

type Link struct {
    From, To string
    Status   LinkStatus
    Count    int // observed dependencies of the pair's seams; 0 when only a finding names the pair
}

// View is a read-only picture. Stale is true when the policy text no longer matches the report's config_hash.
// Incomplete is true when the report's hard gates did not pass or a required rule was not evaluated: an observed
// link may then still break a rule.
type View struct {
    Nodes      []Node
    Links      []Link
    Stale      bool
    Incomplete bool
}

// Build is pure. Nodes come from the policy's modules, links from the report's seams and findings; a link's
// Status comes from the findings that name its module pair, never from the policy text.
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
- **Shared knowledge**: `Progress`, `StartCustomer`, `StartManaged`. ui never mounts `DiscoveryHandler` or `BaselineHandler` (the façade does).
  Before `StartManaged` ui checks `Consents.Has(ConsentSourceProcessing)` and sends the administrator to the consent page
  when it is false **[default]**. `ErrNoSandbox` is the sentinel onboarding returns when managed discovery is not
  configured; ui shows "managed discovery is not available". `ErrAlreadyOnboarded` and `ErrBusy` are the sentinels
  of a refused start; ui maps all three to `409` (Status mapping), never to the `502` of an upstream failure.

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
    // BaselineHandler serves POST /v1/baselines and opens or updates the baseline PR. It never writes the default branch.
    BaselineHandler() http.Handler
}
```

<!-- contract: ErrAlreadyOnboarded, ErrBusy — restated from internal/control/onboarding/module.md -->
```go
// ErrAlreadyOnboarded is returned by StartCustomer and StartManaged when Progress is past StageInstalled.
var ErrAlreadyOnboarded = errors.New("onboarding: already onboarded")

// ErrBusy is returned by StartManaged when this process already runs its maximum of managed jobs.
var ErrBusy = errors.New("onboarding: busy, retry later")
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
  pull-request role interface that only proposal's constructor takes. proposal owns the branch-name and head-SHA
  rules: ui checks `?branch=`, the form `branch`, the form `head_sha` (when not empty) and `?since=` with `ValidBranch`
  and `ValidHeadSHA` and holds no pattern of its own, so ui never saves on a branch that assist or proposal then refuses.

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

<!-- contract: ValidBranch, ValidHeadSHA — restated from internal/control/proposal/module.md -->
```go
// ValidBranch reports whether branch is a legal draft branch name: it matches
// ^archfit/[A-Za-z0-9._/-]+$, has no empty, "." or ".." path segment (so no "//" and no trailing "/").
func ValidBranch(branch string) bool

// ValidHeadSHA reports whether sha is a full commit SHA: exactly 40 lowercase hex characters.
// "" is not valid; a caller whose own contract allows "no draft yet" checks for "" itself.
func ValidHeadSHA(sha string) bool
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
  unchanged at `/schema/policy.json` for editors outside the App; no page of the App reads it. `Diagnose` is the one
  check of an edited text, for Check and for every save, the save before an assist request included **[default]**:
  only `wire/*` may import a YAML library. Its `nil` implies that `Validate`, `Waivers` and `Modules` accept the text,
  so a saved draft is always readable by the graph and the gate. ui shows its `Diagnostic` lines to the user who
  submitted the text and never logs them. `Modules` is the graph page's test for "this text parses". `Digest` is used only
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

<!-- contract: Schema — restated from internal/wire/policy/module.md -->
```go
// Schema returns the engine v2.5.0 config JSON Schema, embedded unchanged, for the editor.
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

<!-- contract: Diagnostic, Diagnose — restated from internal/wire/policy/module.md -->
```go
// Diagnostic is one problem of a policy text, worded for the person who wrote the text.
type Diagnostic struct {
    Line    int    // 1-based line in the text; 0 when the problem has no single place
    Column  int    // 1-based column; 0 when unknown
    Message string // one sentence; may quote keys of the text, at most 64 characters of each
}

// Diagnose checks a policy text against the embedded engine config schema and the App's own readers. It returns
// nil exactly when the text is fit to save: then Validate, Waivers and Modules all accept it too.
func Diagnose(policy []byte) []Diagnostic
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
  wire/settings owns the owner-entry rule (login or `org/team-slug`, the cap, no duplicate ignoring case): ui validates
  the submitted owners with `Parse(Render(Settings{…}))`, the way onboarding does, and holds no owner pattern or cap.

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
  `graph.Build` unread. The artifact holds the raw engine state JSON; `Decode` validates it against the state schema of
  an engine release and reduces it to the no-source form, so fields the decoder drops (measurement, `matched_by`, task
  declarations, metric provenance, seam scores) never reach a template and ui never renders them. ui parses the artifact
  in no other way. The artifact comes without the envelope that names its release, so ui passes the last of
  `EngineVersions`: the newest key set holds every older one, and the graph only displays what it reads.
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
// StateSchemaV1 is the engine state contract accepted (archfit.state.schema.json, engine v2.3.1 to v2.5.0).
const StateSchemaV1 = "archfit.architecture-state.v1"

// MaxBytes caps an uploaded payload: 5 MiB (the "5 MB" cap of decisions.md).
const MaxBytes = 5 << 20

// Report is the no-source transport form of an engine state report. The decoder knows every key of the state
// schema of each release it reads: keys listed here are kept (length-capped), the other known keys are validated
// and dropped (measurement, findings[].matched_by, agent_tasks[].declarations, metric provenance, seam scores),
// and any key that release's schema does not define is a decode error.
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
// Decode strictly parses payload as the no-source transport of the state document engineVersion emits. It
// fails on: an engineVersion outside EngineVersions, size over MaxBytes, a key that release's state schema
// does not define, wrong schema_version, an enum value outside its set, strings over the per-field caps,
// absolute or parent-escaping paths, multi-line text, or duplicate finding IDs.
func Decode(payload []byte, engineVersion string) (Report, error)
```

<!-- contract: EngineVersions — restated from internal/wire/report/module.md -->
```go
// EngineVersions returns the engine releases whose state document Decode reads, oldest first. Each emits
// StateSchemaV1; a later release only adds optional keys, so the last key set holds every earlier one.
func EngineVersions() []string
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
- ui's own code is handlers, templates (`templates/`, transparent folder), static assets (`static/`, transparent folder:
  `app.css`, `editor.js`, `graph.js`, all embedded with `go:embed`; no vendored library) and the handler chain. The two children do not know each other.

### Handler chain

1. Recover panics → `500`; log route pattern and request ID only.
2. Security headers: `Content-Security-Policy: default-src 'self'; script-src 'self'; style-src 'self';
   frame-ancestors 'none'` (no inline style, no inline script, no worker: every style is a class in `app.css`),
   `X-Content-Type-Options: nosniff`, `Referrer-Policy: same-origin` **[default]**.
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
  and to its check runs **[default]**: report history is what GitHub keeps. The route needs `read`; the page calls
  `Sessions.Authorize(user, "{o}/{n}", "admin")` a second time and shows the onboarding, discover and consent controls
  only when it succeeds. Its error is not an error of the page; the `POST` routes enforce `admin` themselves.
- Onboarding start: `owners` is split into lines, each trimmed, blank lines dropped: that is all ui decides. The list
  is valid when `settings.Parse(settings.Render(Settings{Version: 1, Owners, Gate: GateAdvisory, Workflow, DataMode:
  DataCustomer}))` succeeds; `Gate`, `Workflow` (a fixed placeholder path) and `DataMode` only make the probe document
  complete and are never written. Any error, an empty list included, is `422` with the fixed wording "Name at least one
  owner. Owners are GitHub logins or org/team-slug, one per line, each named once." **[default]**, never `Parse`'s error
  text, and onboarding is not called. Then `mode`: `customer` → `StartCustomer`; `managed` → consent check →
  `StartManaged`; anything else `422`.
- Discover: requires stage `workflow_merged` or later; `Actions.Dispatch(Settings.Workflow, DefaultBranch, {"discover": "true"})`.
- Editor: branch is `archfit/edit-<lower-case login>`, or a `?branch=` that `proposal.ValidBranch` accepts, else `422`.
  A form `head_sha` is empty (no open draft was loaded) or passes `proposal.ValidHeadSHA`, else `422`; a `?since=` that
  fails `ValidHeadSHA` is ignored. `Proposals.Get`:
  when the draft is `DraftOpen` or `DraftInReview`, load `PolicyPath` at `Draft.HeadSHA` and set `head_sha` to it; otherwise load
  from the default branch with `head_sha` empty. The page shows the draft state in the words draft / awaiting review /
  published (and closed), the draft's check conclusions (engine validation is the check on the draft PR), and the PR link. The
  form is a plain `textarea` with no editor library **[default]**; Check and save work without JavaScript. The template
  puts a newline right after the opening `<textarea>` tag: HTML drops exactly one there, so a text that starts with a
  blank line keeps it through a save. One line says
  "Full checks run in CI with `archfit config lint`.": the App cannot run the engine, so vacuous selectors, ownership and
  rule types are not checked here. Below the text area, a problem list shows each `Diagnostic` as "Line 12, column 3:
  message"; the line is a link `#L12`, and the message is plain text (escaped, never `template.HTML`). The script
  (`static/editor.js`) moves the caret to the line of a `#L<n>` hash or of a clicked link, and marks the graph link
  "stale" as soon as the buffer differs from the loaded text, the page lists problems, or the text area carries
  `data-unsaved`: a page re-rendered from a form (Check, a refused save, ready or assist) holds the submitted text, not
  the text on the branch. Without JavaScript the list and its text still show.
- Check: the same form check as Save (`branch`, `head_sha`), then the one policy check: size ≤ 256 KB **[default]**,
  valid UTF-8, `policy.Diagnose(content)`. It saves nothing and asks GitHub nothing beyond authorization: the editor is re-rendered from the
  form, `200`, with the problem list and "The check found problems, listed below the editor.", or with "The check found
  no problems.". An invalid form, a text too large or not UTF-8 is `422` as for Save. `read` is enough, since nothing is written.
- Save: the policy check of Check first; a problem is `422` with the editor re-rendered from the form, the problem list,
  and "The policy has problems, listed below the editor. Nothing was saved." **[default]**; `Proposals.Get`; content
  equal to the file currently on the branch (at `Draft.HeadSHA` for an open draft, else on the default branch) is a no-op with
  a notice, whatever `head_sha` says, so a retried save never conflicts with itself; then an open draft whose head differs
  from `head_sha` (including an empty `head_sha`) is `409`; then `Proposals.Propose(Change{Repo: repo.RepoRef, Branch, Base: repo.DefaultBranch, Title, Body, Message,
  Files: [{PolicyPath, content}]}, head_sha)`. Title `"Archfit policy edit by @<login>"`, fixed body, message
  `"archfit: edit policy (@<login>)"` **[default]**. A browser submits every `textarea` with CRLF line ends: before the
  comparison ui turns CRLF back into LF unless the file on the branch already uses CRLF **[default]**. Nothing else in the
  text is touched. A refused save, ready or assist re-renders the editor from the form alone and asks GitHub nothing
  beyond authorization, except that an assist refused for problems first read the branch file to compare.
- Ready: `Proposals.Get`; head must equal `head_sha`, state must be `DraftOpen`; then `Proposals.Ready(number)`. ui never merges:
  publication is the merge on GitHub under the customer's ruleset and owner approval, the same path as a GitHub-first edit.
- Graph: settings → `LatestRun(Settings.Workflow, DefaultBranch)` → `Artifact(run.ID, "archfit-report", report.MaxBytes)` →
  `report.Decode` (with the newest release of `report.EngineVersions`: the artifact names none, and the newest key set
  holds every older one) → policy text (open draft of the user, else default branch) → `graph.Build`. The artifact is the raw engine
  state JSON; only the decoded no-source `Report` is used. When the chosen policy text fails `policy.Modules` (a broken draft
  pushed on GitHub), ui builds from the default-branch policy instead, the last valid text, and forces the stale banner with
  "the draft does not parse" **[default]**. No run or artifact (GitHub retention): "no report available". Decode error:
  "report unavailable"; never a partial graph. The `View` is embedded as JSON in a `script type="application/json"` element.
  Each link is drawn and listed with its `Status` (`violation`, `accepted`, `advisory`, `allowed`, `observed`), which is also
  its CSS class. `allowed` comes only from the engine's `seams[].policy` (engine v2.5.0 and later); the page explains that
  accepted debt is never allowed. When `View.Incomplete`, a banner says the report's hard gates did not
  pass or not all required rules were evaluated, so an observed link can still break a rule.
- Assist: authorize `write`; if `content` differs from the file at `head_sha`, save it first (same rules as Save) and use the
  new head. Owner decision: when `content` equals the file the editor would load for the branch (after the CRLF rule) and
  `Diagnose` finds problems in it, nothing is saved and the request goes ahead on the loaded head, so a user can ask AI
  to fix a broken draft; the answer is checked by `Diagnose` in assist. A changed text with problems is refused as for Save; then `Assist.Propose(mode, Request{Repo, Branch, HeadSHA, Instruction, By: login})`. Managed is offered only when
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
- A text with a `policy.Diagnose` problem (invalid YAML, a key, type or value the engine schema refuses) is never
  saved by ui: not by save, and not by the save that precedes an assist request. Assist may be asked about such a text
  only when it is already the draft on the branch (it was committed outside ui); ui commits nothing then, and assist
  commits only an answer that `Diagnose` accepts. Ready sends no text. Invalid YAML marks the graph
  stale. Schema validity is not engine validity: the page says that the check on the draft PR is the authoritative
  validation and that full checks run in CI with `archfit config lint`.
- ui never rewrites policy text: it saves the user's bytes as they are. Comments and YAML constructs the App does not
  understand survive every round trip. The graph never writes.
- AI only on an explicit `POST` by a user with `write`. Nothing is auto-published. Model text is never rendered by ui; users
  see proposals as the diff on GitHub. A failed assist request leaves the editor, Check and saving working.
- `html/template` auto-escaping everywhere; no `template.HTML` for values that came from GitHub, a report, or a form.
- Static assets and the schema carry no tenant data; everything else requires a session.
- Logs: request ID, route pattern, status, duration, user ID, repository ID, installation ID, outcome. No form content, no
  policy text, no instruction, no report content, no cookie or token.
- Inherited: control-plane code. The gate never calls ui; a panic or outage here changes no check. Standard library only
  on the Go side, and no browser library: the static assets are this module's own files.
- Declared restatement: three kinds of link are built from the literal host `https://github.com/`: the assist
  workflow's runs page, the pull-request search and the default-branch commits page. `internal/github` owns the
  knowledge of GitHub hosts and publishes no URL builder; `control/assist` (`RunURL`) and `control/onboarding`
  (`CloneURL`) restate the same host. On drift a link opens the wrong host; no write and no check depends on it.
  Removal trigger: GitHub Enterprise host support, when `internal/github` publishes the web base URL.
- Declared restatement: the check names `archfit/state` and `archfit/gate` are literals here. `internal/gate`
  (feedback) owns and publishes them but exports no constant, and control imports only the gate façade;
  `control/onboarding` restates `archfit/state` too. On drift the overview and the editor show "no state yet" for every
  repository; no gate result changes. Removal trigger: a rename of a check, at which point gate exports the names.
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
  **Scenario**: table: login `Alice`; login with a hyphen; `?branch=archfit/policy`; `?branch=main`; `?branch=archfit/../main`; `?branch=archfit/a b`;
  then every branch of a list that includes `archfit/./x`, `archfit/a//b` and `archfit/a/`, against `proposal.ValidBranch`.
  **Expected behavior**: `archfit/edit-alice`; hyphen kept; accepted; the next three are `422`; for the list ui answers
  `422` exactly when `ValidBranch` is false, so ui delegates the rule.
- **Test name**: `TestStatusMapping`
  **Scenario**: table of every error in the Status mapping list, injected through the fakes.
  **Expected behavior**: the documented status; `Retry-After` on `503` for rate limits; the editor text is echoed back on `409` and `422`.
  The rows include `api.ErrNotFound` → `404` and the three onboarding sentinels `ErrNoSandbox`, `ErrAlreadyOnboarded`,
  `ErrBusy` → `409`, none of them `502`.
- **Test name**: `TestWording`
  **Scenario**: table of `DraftState`, `Plan`, check conclusions and assist sentinels.
  **Expected behavior**: draft / awaiting review / published / closed; a banner only for trial, grace, suspended; one distinct sentence per sentinel.

### Integration Contract Tests

- **Test name**: `TestRoutesAndMinimumPermissions`
  **Scenario**: table of every route × user permission `none`, `read`, `write`, `admin`, and not signed in.
  **Expected behavior**: `303` to login or `401` without a session; `404` below the minimum; success at and above it
  (`200` for a page and for Check, `303` for every other `POST`);
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
  **Scenario**: the blocked wire fixture (`hard_gates: fail`, no baseline) as the `archfit-report` artifact of the latest default-branch run.
  **Expected behavior**: the embedded JSON equals `graph.Build(report, policy)`; node links point to `#L<line>`; no stale banner;
  the incomplete banner; marker strings placed in fields the decoder drops (`matched_by`, measurement, task declarations)
  appear nowhere in the page.
- **Test name**: `TestGraphPageShowsTheEngineSeamPolicy`
  **Scenario**: the unmodified v2.5.0 capture `real-seam-policy-v250` of the shared wire corpus as the `archfit-report` artifact.
  **Expected behavior**: the page decodes it (newest release); the embedded JSON equals `graph.Build(report, policy)`; the links
  table has rows of class `violation`, `accepted`, `allowed` and `observed`.
- **Test name**: `TestConsentRecordAndRevoke`
  **Scenario**: an administrator records `managed_llm`, then revokes it.
  **Expected behavior**: `Consents.Has` is true then false for the installation of the authorized `Repo`; `By` is the session login.
- **Test name**: `TestOwnersRuleIsSettings`
  **Scenario**: table of submitted owners: `-alice` (a leading hyphen, which wire/settings accepts); a team; `alice` and
  `Alice`; `not a login!`; 101 owners; only blank lines. Each row is also run through `settings.Parse(settings.Render(…))`.
  **Expected behavior**: ui starts onboarding with the trimmed owners exactly when settings accepts them; every other
  row is `422` with ui's fixed wording, without settings' error text, and with no onboarding call.
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
  **Scenario**: table: 600 KB body; 300 KB policy; invalid UTF-8; YAML with a tab indent; 50 owners lines with one malformed;
  a `head_sha` that is not hex; a 7-character hex `head_sha` (`proposal.ValidHeadSHA` refuses both).
  **Expected behavior**: `413` for the first, `422` for the rest; nothing is written.
- **Test name**: `TestSaveRejectsForeignDocuments`
  **Scenario**: table, through save and through assist: one line of prose with a colon; a JSON object with foreign keys; a
  mapping without `version`; a mapping with one unknown top-level key; then a valid policy.
  **Expected behavior**: `422` for the first four, no `Propose` and no assist call, the editor re-rendered with the text kept,
  the "The policy has problems" sentence and the row's problem as a linked line (`Unknown key` or `Missing required key`),
  and nothing of `Validate`'s error text; the valid policy saves with `303`.
- **Test name**: `TestNoScoreAnywhere` (T17)
  **Scenario**: render every page with healthy, blocked and attention fixtures.
  **Expected behavior**: no number that aggregates findings or dimensions, no grade, no percentage, no sort by health.
- **Test name**: `TestHostileStringsAreEscaped`
  **Scenario**: repository names, module names, diagnostics and check titles containing HTML and script text.
  **Expected behavior**: escaped in HTML and in the embedded JSON; every page carries exactly the CSP of the handler
  chain, with no `'unsafe-inline'` and no `worker-src`.
- **Test name**: `TestNoAssetNeedsInlineStyleOrBlobWorker`
  **Scenario**: read every embedded template and static file.
  **Expected behavior**: no `<style>`, no `style=` attribute, no script that sets `.style` or a style attribute, no
  `blob:` and no worker: the CSP blocks all of them.
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
  **Expected behavior**: `422` and no commit; before the edit the healthy report shows no incomplete banner; the graph page
  shows the stale banner and still draws the report. Then a broken
  draft is pushed on GitHub: the graph page draws from the default-branch policy with the stale banner and "the draft does not parse".
- **Test name**: `TestUIAndGitHubFirstEditsShareOnePath` (T17)
  **Scenario**: one policy edit through the editor, one pushed directly to the draft branch on GitHub.
  **Expected behavior**: both are commits on a PR against the default branch; the editor shows the GitHub-pushed head after reload and
  refuses to save over it from an older load; ui offers no merge or approval action.
- **Test name**: `TestUnknownYAMLConstructsSurviveASave` (T17)
  **Scenario**: a valid policy with comments, anchors, merge keys and, below a schema top-level key, an explicit `!!str` tag and a
  block scalar; change one line and save. (An unknown key is refused: `TestSaveRejectsForeignDocuments`.)
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
- **Test name**: `TestCheckListsProblemsAndSavesNothing`
  **Scenario**: table, posted to Check by a user with `read`: an unknown key and an enum miss, each with a close match; the
  same text with CRLF line ends; a tab indent; a comment-only text; an `<img>` tag as a key; a valid policy.
  **Expected behavior**: `200`; the problems as list items with `#L<n>` links, line, column and "Did you mean"; the same
  line numbers for CRLF; a problem without a line has no link; the tag is escaped in the list; the valid policy shows "The check found no problems." and
  no list; the text is kept and the `archfit config lint` line is shown; nothing is written and GitHub is not asked for
  the policy or the draft.
- **Test name**: `TestRefusedSaveListsTheProblems`
  **Scenario**: save, and assist, a text with a missing required key and a misspelled key.
  **Expected behavior**: `422`, "The policy has problems" with both problems as linked lines, the text kept and the editor
  script on the page; no write and no assist call.
- **Test name**: `TestEditorKeepsALeadingBlankLine`
  **Scenario**: the policy on the default branch starts with a blank line; load the editor, Check the same text, save it with one more line.
  **Expected behavior**: both pages carry two newlines after the opening `<textarea>` tag; the committed bytes keep the blank line.
- **Test name**: `TestOnlyAFormRerenderIsMarkedUnsaved`
  **Scenario**: load the editor; then Check a valid text and save a text with a problem.
  **Expected behavior**: the loaded page has no `data-unsaved`; both re-rendered pages have it.
- **Test name**: `TestAssistOnABrokenDraft`
  **Scenario**: the draft on the branch has a missing required key; assist is asked with that same text (CRLF from the
  browser), then with a changed text that still has problems.
  **Expected behavior**: the first request reaches assist bound to the draft head with no commit; the second is `422`
  with no assist call and no commit.
- **Test name**: `TestGraphWithoutAReport`
  **Scenario**: the graph page with no workflow run; then with an `archfit-report` artifact that `report.Decode` refuses.
  **Expected behavior**: `200` "No report available"; then `200` "Report unavailable" and no `graph-data` element: never a partial graph.

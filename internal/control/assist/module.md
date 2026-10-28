# Assist

**Path**: internal/control/assist/
**Parent**: internal/control/ (Control plane)
**Submodules**: anthropic

## Purpose

Assist handles one thing: a user explicitly asks for an AI proposal on a draft policy. It turns that
request into a reviewable commit on the draft pull request (managed mode) or into a workflow dispatch
in the customer's CI (customer mode). It owns the LLM `Provider` seam, so that no other package knows a
model exists. Without it, consent, budget, context limits and the stale-proposal rule would be
scattered over UI handlers, and an LLM could drift toward the gate path.

## Functional Responsibilities

- Accept a request bound to a draft branch and the branch head the user was looking at.
- Managed mode: check that a provider is configured, that an administrator consented, that the repository's
  approved settings say `data_mode: managed`; reserve budget; call the provider once; settle the budget; validate
  the answer; commit it to the draft branch through `proposal` with the head binding.
- Customer mode: dispatch the customer's assist workflow with the branch, head SHA and instruction. The
  customer's runner calls the customer's model and pushes to the draft PR itself.
- Build the prompt from permitted context only: the draft policy YAML, the engine config schema, the instruction.
- Report every refusal as a distinct, stable error so that the UI can show a clear state.

## Subdomain Classification

Core-adjacent product surface. Volatility: high. Prompt wording, context rules, limits and the customer
workflow inputs will be tuned with every pilot. The `Provider` seam itself is stable.

## Encapsulated Knowledge

- The prompt: system text, how schema and policy are framed, the instruction to answer with one complete YAML file.
- The permitted-context rule and its limits: policy at most 16 KB **[default]**, instruction at most 2 000 bytes **[default]**,
  answer at most 64 KB **[default]**, `MaxTokens` 8 192 **[default]**.
- Output normalization and validation (code-fence stripping, UTF-8, `policy.Diagnose`, "must differ").
- The order of checks, chosen so that nothing is spent before every free check has passed.
- The reservation protocol against `Budget` and the rule that settlement survives request cancellation.
- The in-flight guard: one managed request per repository and branch at a time **[default]**.
- Commit message text and the customer workflow's input names.

## Public Contract

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

### Provider cost rule

On success `Completion.CostMicroUSD` is the actual cost. On failure a `Provider` still returns a cost with the
error **[default]**: `0` when the vendor rejected the call, the usage-based cost when an answer arrived but was
unusable, and `MaxCostMicroUSD(p)` when the outcome is unknown (timeout, cancellation, transport error after
sending). Assist always settles with that value, so the monthly cap cannot be exceeded by failed calls.

### Errors of `Propose`

All are sentinels for `errors.Is` **[default names]**. Messages carry no user text.

- `ErrInvalidRequest` — unknown mode; empty or oversized instruction; a branch that `proposal.ValidBranch` refuses or
  that equals the default branch; empty `By`; and, once the draft is known to be open, a `HeadSHA` that
  `proposal.ValidHeadSHA` refuses.
- `ErrNoDraft` — the branch has no open draft PR (`DraftNone`, `DraftMerged`, `DraftClosed`) or no policy file. It
  wins over the `HeadSHA` shape: a caller that has no draft yet has no head to send, and sends `""`.
- `ErrStale` (owned by `proposal`, passed through) — the branch head is not `Request.HeadSHA`. Nothing was written.
- `ErrManagedUnavailable` — managed mode and `Deps.Provider` is nil.
- `ErrNoConsent` (owned by `account`, returned by assist's guard) — managed mode and `Consents.Has(ConsentManagedLLM)` is false.
- `ErrNotManaged` — managed mode and the settings file on the default branch is missing, invalid, or not `data_mode: managed`.
- `ErrPolicyTooLarge` — managed mode and the draft policy exceeds 16 KB **[default]**. The UI suggests customer mode or a manual edit.
- `ErrBusy` — a managed request for the same repository and branch is still running.
- `ErrBudgetExhausted` (owned by `account`, passed through) — the monthly cap would be exceeded. No call was made.
- `ErrProvider` — the provider call failed or timed out. Wraps status information only.
- `ErrInvalidProposal` — the answer is empty, too large, not a policy file the editor would save (`policy.Diagnose` lists a problem), or identical to the input. Nothing was written.
- `ErrNoAssistWorkflow` — customer mode and the workflow file does not exist on the branch.
- Any other GitHub or Store error is returned wrapped. `ErrRateLimited` stays testable with `errors.Is`.

### Result

Managed: `Mode`, and `Draft` as returned by `Proposals.Propose` (new `HeadSHA`). Customer: `Mode`, the unchanged
`Draft`, and `RunURL = "https://github.com/" + FullName + "/actions/workflows/archfit-assist.yaml"` **[default]**:
`Actions.Dispatch` returns no run ID, so the workflow's runs page is the most precise link available.

## Integrations

### internal/control/assist/anthropic (child)

- **Counterpart**: `internal/control/assist/anthropic/module.md`
- **Direction**: anthropic depends on assist (implements `Provider`). Assist never imports it; `internal/app` constructs
  it and passes it down as `Deps.Provider`. Root import rule: an implementation package of a seam may import the package
  that declares the interface.
- **Strength**: contract
- **LCA / Rank / Distance**: control/assist / 1 / 1
- **Volatility**: moderate
- **Balanced?**: yes
- **Shared knowledge**: the `Provider`, `Prompt`, `Completion` fence and the Provider cost rule above. Assist knows
  nothing of the child's constructor or configuration; only `internal/app` does.

### internal/control (parent façade) and internal/control/ui

- **Counterpart**: `internal/control/module.md`, `internal/control/ui/module.md`
- **Direction**: both depend on assist. `control` calls `New` and re-publishes `Provider` for `internal/app`; `ui` calls `Assist.Propose`.
- **Strength**: functional (composition) for `control`; contract for `ui`
- **LCA / Rank / Distance**: control / 1 / 1
- **Volatility**: high
- **Balanced?**: yes — distance 1
- **Shared knowledge**: the Public Contract above, including the error list. Assist knows nothing about pages or handlers.

### internal/control/account

- **Counterpart**: `internal/control/account/module.md`
- **Direction**: assist depends on account (`Consents`, `Budget`)
- **Strength**: contract
- **LCA / Rank / Distance**: control / 1 / 1
- **Volatility**: high (this side), moderate (account)
- **Balanced?**: yes
- **Shared knowledge**: assist calls `Consents.Has(installationID, ConsentManagedLLM)`, `Budget.Reserve` and
  `Budget.Settle` only. `Has` is false on any error, so a Store outage means "no consent, no call".
  `ErrNoConsent` is the sentinel account owns for that guard; assist returns it when `Has` is false.
  `ErrBudgetExhausted` is the sentinel `Reserve` returns when the cap would be exceeded. `Reserve` and `Settle` of one
  request carry the same `now`: account keys the counter by the UTC month of `now`.

<!-- contract: Consents, Consent, ConsentKind, ErrNoConsent — restated from internal/control/account/module.md -->
```go
type ConsentKind string

const (
    ConsentSourceProcessing ConsentKind = "source_processing" // managed discovery
    ConsentManagedLLM       ConsentKind = "managed_llm"
)

type Consent struct {
    Kind ConsentKind
    By   string // GitHub login of the administrator, as it was when the consent was recorded
    ByID int64  // GitHub user ID of the administrator; 0 on a record written before the App stored it
    At   time.Time
}

// ErrNoConsent is returned by callers' guards when Has is false.
var ErrNoConsent = errors.New("account: consent not recorded")

// ErrConsentExists is returned by Record when a consent of that kind is already recorded; the record is unchanged.
var ErrConsentExists = errors.New("account: consent already recorded")

type Consents interface {
    // Record creates the consent atomically; ErrConsentExists if one is recorded, which keeps who and when.
    Record(ctx context.Context, installationID int64, c Consent) error
    Revoke(ctx context.Context, installationID int64, k ConsentKind) error
    // Has is false on any error: no consent, no call.
    Has(ctx context.Context, installationID int64, k ConsentKind) bool
    // Get answers who recorded the consent and when. Unlike Has it tells "not recorded" (false, nil) from "cannot
    // tell" (an error: the Store failed or the record is corrupt), so a page never shows an outage as "not allowed".
    Get(ctx context.Context, installationID int64, k ConsentKind) (Consent, bool, error)
}
```

<!-- contract: Budget, ErrBudgetExhausted — restated from internal/control/account/module.md -->
```go
var ErrBudgetExhausted = errors.New("account: managed LLM budget exhausted")

// Budget is the App-wide managed-LLM hard cap: USD 20 per calendar month (UTC), in micro-USD.
type Budget interface {
    // Reserve atomically books maxCost; ErrBudgetExhausted if the cap would be exceeded.
    Reserve(ctx context.Context, maxCostMicroUSD int64, now time.Time) error
    // Settle returns the unused part of a reservation.
    Settle(ctx context.Context, reservedMicroUSD, actualMicroUSD int64, now time.Time) error
}
```

### internal/control/proposal

- **Counterpart**: `internal/control/proposal/module.md`
- **Direction**: assist depends on proposal
- **Strength**: contract
- **LCA / Rank / Distance**: control / 1 / 1
- **Volatility**: high (this side), low (proposal)
- **Balanced?**: yes
- **Shared knowledge**: `Get` to read the draft and its head; `Propose` with `expectHeadSHA = Request.HeadSHA` to write.
  Assist never calls `Ready` or `SetBody`. `ErrStale` is the sentinel meaning the branch moved. `Changes` is restated
  below with github/api; `Pulls` is the github/api pull-request role interface that only proposal's constructor takes.
  proposal owns the branch-name and head-SHA rules: assist calls `ValidBranch` and `ValidHeadSHA` and holds no pattern
  of its own.

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
- **Direction**: assist depends on github/api
- **Strength**: contract
- **LCA / Rank / Distance**: root / 2 / 2
- **Volatility**: high (this side), low (github/api)
- **Balanced?**: yes — contract coupling
- **Shared knowledge**: `Deps.GitHub` is used as `Repos` (`File` only) and `Actions` (`Dispatch` only). Assist builds
  `FileChange` values for `Change.Files`. `ErrNotFound` is the sentinel for a missing file or workflow;
  `ErrRateLimited` is the sentinel after bounded backoff. `TokenSource` is an opaque part of `New`'s signature for this
  module. `Pull` is the github/api pull-request record returned by `EnsurePull`; assist never sees one.

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

<!-- contract: Client, New — restated from internal/github/api/module.md -->
```go
// Client implements every role interface. Consumers depend on the role interfaces they need.
type Client struct{ /* unexported */ }

func New(ts TokenSource, hc *http.Client) *Client
```

### internal/wire/policy

- **Counterpart**: `internal/wire/policy/module.md`
- **Direction**: assist depends on wire/policy
- **Strength**: contract
- **LCA / Rank / Distance**: root / 2 / 2
- **Volatility**: high (this side), moderate (wire/policy)
- **Balanced?**: yes — contract coupling
- **Shared knowledge**: `PolicyPath` is the constant naming the file assist reads and writes. `Schema()` is the prompt's
  rule reference. `Diagnose` is the check of an answer, the same check the editor runs before every save, so a
  proposal commit can always be saved again from the editor: only `wire/settings` and `wire/policy` may import a
  YAML library. It knows the schema, not the engine's rules; the engine check on the draft PR is the
  authority on the file's meaning. Assist drops `Diagnose`'s messages: they may quote the answer. `BaselinePath` and `LabelsPath` are the constants naming the baseline and labels files; assist never touches either, and never calls `Digest`.

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

<!-- contract: Schema — restated from internal/wire/policy/module.md -->
```go
// Schema returns the engine v3.0.1 config JSON Schema (byte-identical to v3.0.0), embedded unchanged: Diagnose's schema, the assist
// prompt's context, and the file served for editors outside the App.
func Schema() []byte
```

### internal/wire/settings

- **Counterpart**: `internal/wire/settings/module.md`
- **Direction**: assist depends on wire/settings
- **Strength**: contract
- **LCA / Rank / Distance**: root / 2 / 2
- **Volatility**: high (this side), moderate (wire/settings)
- **Balanced?**: yes — contract coupling
- **Shared knowledge**: assist reads `Path` at the repository's default branch, calls `Parse`, and checks
  `DataMode == DataManaged`. It never reads settings from the draft branch: that is head-tree configuration and could
  switch the data mode without owner approval.

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
}
```

<!-- contract: Parse, Render — restated from internal/wire/settings/module.md -->
```go
// Parse strictly decodes and validates a settings file. Unknown keys are errors.
func Parse(raw []byte) (Settings, error)

// Render returns canonical YAML for s. For any s returned by Parse, Parse(Render(s)) == s.
func Render(s Settings) []byte
```

## Internal Design

Assist has its own code and one child. The child is a platform implementation of the seam assist owns; assist
never imports it. `internal/app` builds `anthropic.New(...)` when a key is configured and passes the `Provider` through
`control.Deps` to `assist.Deps`. With no provider, managed mode answers `ErrManagedUnavailable` and customer mode
works unchanged.

### Checks common to both modes, in order

1. Validate mode, instruction, branch and `By` (see `ErrInvalidRequest`). The branch must pass `proposal.ValidBranch`
   and must not be the default branch. No dependency is called for a request that fails here.
2. Managed only, before reading the draft branch: `Provider != nil`, then `Consents.Has`, then settings at the default branch.
3. `Proposals.Get(Repo.RepoRef, Branch)`: state must be `DraftOpen` or `DraftInReview`, else `ErrNoDraft`; then
   `proposal.ValidHeadSHA(Request.HeadSHA)`, else `ErrInvalidRequest`; then `Draft.HeadSHA != Request.HeadSHA` is
   `ErrStale` now, before anything is spent. The shape check comes after the state check so that a request without a
   draft, which carries an empty `HeadSHA`, gets `ErrNoDraft`.

### Managed flow

1. `Repos.File(ref = Request.HeadSHA, PolicyPath)`; missing is `ErrNoDraft`; over 16 KB **[default]** is `ErrPolicyTooLarge`.
2. Take the in-flight slot for `(Repo.ID, Branch)`, else `ErrBusy`. Released on every exit path.
3. Build the prompt. System: fixed instructions ("answer with the complete updated YAML file and nothing else") plus the
   whole `policy.Schema()` document, which carries the rule descriptions **[default]**. User: the instruction, then the
   policy between fixed delimiters. `MaxTokens` 8 192 **[default]**.
4. `max = Provider.MaxCostMicroUSD(prompt)`; read the clock once: `now`; `Budget.Reserve(max, now)`. Any error stops
   here; no call is made.
5. `Provider.Complete` under a 150 second deadline **[default]**. Then always `Budget.Settle(max, completion.CostMicroUSD, now)`
   with the same `now` value as step 4, never a second clock read (see Constraints),
   in a context detached from request cancellation with its own 5 second timeout **[default]**. A failed settle is logged and keeps
   the full reservation: the error is on the safe side of the cap.
6. Normalize: trim; strip one surrounding Markdown code fence; end with one newline. Validate: UTF-8, no NUL, at most
   64 KB **[default]**, no fence line left over (prose around a fence, an unclosed fence), bytes differ from the input
   apart from surrounding white space, and `policy.Diagnose` lists no problem: prose with a colon, a JSON document, a
   mapping without `version` or with a foreign top-level key, and a key, type or value `Diagnose` refuses (stricter
   than the engine in places) are not a policy. Else `ErrInvalidProposal`: nothing is
   proposed, the text is discarded, and the budget stays settled by step 5 exactly as for any other invalid answer.
   Neither the answer nor `Diagnose`'s messages are logged or returned; the error names the failed check only.
7. `Proposals.Propose(Change{Repo, Branch, Base: Repo.DefaultBranch, Message, Files: [{PolicyPath, answer}]}, Request.HeadSHA)`.
   `Message` is `"archfit: AI proposal requested by @<By>"` **[default]**. `Title` and `Body` are set to the same fixed
   text and matter only if the PR had to be recreated. `ErrStale` here means that during the provider call someone pushed to the branch, or the
   draft PR was merged or closed (proposal refuses a missing, merged or closed PR when a head is expected). Nothing is
   written; the budget was already settled in step 5 with the actual cost, so the money is spent; the UI asks to reload and retry.

### Customer flow

After the common checks: `Actions.Dispatch(Repo.RepoRef, AssistWorkflowPath, Branch, {"branch", "head_sha", "instruction"})`.
`ErrNotFound` becomes `ErrNoAssistWorkflow`. No consent, budget, provider or settings check applies: nothing leaves the
customer's environment toward Archfit, and no Archfit money is spent. The workflow refuses a moved head by itself using
`head_sha`; pending, failed and completed are the run's states, which the UI reads from GitHub.

## Change Vectors

- Prompt wording, context framing, limits, output normalization.
- A new request kind (explain a finding) once a no-source context for it is defined.
- Partial-file answers (patches) instead of whole-file answers, to lift the 16 KB limit.
- Customer workflow inputs and the link shown for it.
- Per-installation budgets or rate limits on top of the App-wide cap.
- A second provider: a sibling of `anthropic`, selected in `internal/app`.

## Constraints and Invariants

- Explicit request only. Assist has no timer, no webhook handler and no sweep hook. Nothing under `internal/gate`
  imports it, and it imports nothing of `gate` (root import test).
- Nothing is auto-published: the only write is one commit on an `archfit/` draft branch through `proposal`. Assist never
  marks a PR ready, never merges, never writes to the default branch.
- A stale proposal cannot overwrite newer edits: every write carries `expectHeadSHA = Request.HeadSHA`.
- Prompt context is exactly: draft policy YAML, engine config schema, instruction. Never source code, file paths outside
  the policy, reports, findings, check output, or PR conversation **[default]**.
- Raw model output is never logged, never returned in an error, never posted in a comment, PR body or commit message.
  It reaches GitHub only as the content of `.archfit.yaml` after validation, as a diff for human review.
- Customer-mode credentials never reach the App: the request carries no secret, the App stores nothing about it, and
  the customer's model key exists only in the customer's CI.
- The installation ID comes from `Request.Repo`, which the caller obtained from an authorized session. Assist takes no ID from text.
- Failure isolation: any failure here (provider, budget, Store, GitHub) returns an error to the caller and changes
  nothing else. Editing, the editor's schema check, checks and the gate do not depend on assist.
- One budget instant per request: the clock is read once before `Reserve`, and `Settle` gets that same value, as
  account's Operation rules require. The provider call between them can take 150 seconds. With two clock reads a call
  that crosses a UTC month end would leave the whole reservation in the old month and book a negative delta in the new
  month, which raises the hard cap. The clock reads for the logged duration are separate and never reach the budget.
- Bounded work: one provider call per request (the provider may retry once), 150 second deadline **[default]**, one request per
  repository and branch at a time, cancellation honored at every step except settlement.
- Logs: installation ID, repository ID, mode, user ID or login, outcome, policy and instruction byte lengths, cost,
  duration. No instruction text, no policy text, no answer text.
- Declared restatement: the customer-mode `RunURL` is built from the literal host `https://github.com/` here.
  `internal/github` owns the knowledge of GitHub hosts and publishes no URL builder. Also restated in
  `control/ui` (runs page, pulls and commits links) and `control/onboarding` (`CloneURL`). On drift the link opens the
  wrong host; no write and no check depends on it. Removal trigger: GitHub Enterprise host support, when
  `internal/github` publishes the web base URL.
- The in-flight guard is per process. With several replicas a second request can run in parallel; the head binding
  still prevents a double write and the budget still bounds cost. Upgrade trigger: more than one replica in production.

## Test Specification

Fakes: `Provider`, `Consents`, `Budget` (or the real account over a memory Store), `Proposals`, and the two github/api
roles at their interfaces. The clock is injected. No test calls a real model.

### Unit Tests

- **Test name**: `TestValidateRequest`
  **Scenario**: table: empty instruction; 2 001-byte instruction; branch `main`; branch equal to the default branch;
  branch `archfit/../x`; branch with a space; 39-character SHA; upper-case SHA; empty `By`; unknown mode; a valid request.
  **Expected behavior**: `ErrInvalidRequest` for all but the last; no dependency is called for invalid rows, except
  that the two SHA rows have read settings and the draft (`Consents.Has`, `Repos.File`, `Proposals.Get`) and nothing else.
- **Test name**: `TestBranchAndHeadRulesAreProposals`
  **Scenario**: table of branches and head SHAs on an open draft, among them `archfit/./x`, `archfit/a//b`, `archfit/a/` and a 7-character SHA.
  **Expected behavior**: `Propose` answers `ErrInvalidRequest` exactly when `proposal.ValidBranch` or `proposal.ValidHeadSHA` is false.
- **Test name**: `TestNoDraftWinsOverHeadSHA`
  **Scenario**: both modes; the branch has no pull request, a merged one, a closed one; `HeadSHA` is `""`.
  **Expected behavior**: `ErrNoDraft` in every row, never `ErrInvalidRequest`; nothing is reserved, dispatched or written.
- **Test name**: `TestPromptContainsOnlyPermittedContext` (T20)
  **Scenario**: build the prompt for a policy, an instruction and a schema; the fakes also hold a report, findings and
  source-like strings that assist could reach if it tried.
  **Expected behavior**: the prompt contains the three permitted texts and none of the other marker strings; `MaxTokens` is 8 192.
- **Test name**: `TestNormalizeAndValidateAnswer`
  **Scenario**: table: plain YAML; YAML inside one code fence; prose before a fence; empty; identical to input; 65 KB;
  invalid UTF-8; a tab-indented mapping (not YAML).
  **Expected behavior**: the first two are accepted and equal the bare YAML with one trailing newline; all others are `ErrInvalidProposal`.

### Integration Contract Tests

- **Test name**: `TestManagedProposeCommitsToDraft`
  **Scenario**: provider configured, consent recorded, settings `managed`, draft at head `a…`, provider returns edited YAML costing 7 000.
  **Expected behavior**: `Reserve(max)` then `Complete` then `Settle(max, 7000)`; one `Proposals.Propose` with
  `expectHeadSHA == "a…"`, one file `.archfit.yaml`, base = default branch; `Result.Mode == ModeManaged` with the new draft head.
- **Test name**: `TestManagedPreconditions`
  **Scenario**: table: provider nil; consent missing; `Consents.Has` false because the Store fails; settings file missing;
  settings invalid; `data_mode: customer`; draft branch says `managed` while the default branch says `customer`;
  no draft; merged draft; policy missing; policy of 17 KB.
  **Expected behavior**: `ErrManagedUnavailable`, `ErrNoConsent` (twice), `ErrNotManaged` (four times), `ErrNoDraft` (three
  times), `ErrPolicyTooLarge`; in every row `Reserve`, `Complete` and `Propose` are never called.
- **Test name**: `TestManagedPolicyAtTheLimitIsAccepted`
  **Scenario**: the draft policy is exactly 16 KB (16 384 bytes).
  **Expected behavior**: the request succeeds; `ErrPolicyTooLarge` starts at 16 385 bytes.
- **Test name**: `TestCustomerProposeDispatchesWorkflow`
  **Scenario**: customer mode on a valid draft; then the same with `Dispatch` returning `ErrNotFound`.
  **Expected behavior**: one `Dispatch` of `.github/workflows/archfit-assist.yaml` on the branch with inputs `branch`,
  `head_sha`, `instruction`; `RunURL` is the workflow's runs page; no consent, budget or provider call. Second case: `ErrNoAssistWorkflow`.
- **Test name**: `TestErrorsAreDistinguishable`
  **Scenario**: every sentinel of the error list, produced through `Propose`.
  **Expected behavior**: `errors.Is` matches exactly one sentinel each; `ErrStale`, `ErrBudgetExhausted` and `ErrRateLimited` keep their owners' identity.

### Boundary Tests

- **Test name**: `TestBudgetProtocol` (T20)
  **Scenario**: table: `Reserve` returns `ErrBudgetExhausted`; `Reserve` returns a Store error; provider fails with cost 0;
  provider times out with cost = max; provider succeeds but the answer is invalid; request context cancelled during `Complete`; `Settle` fails.
  **Expected behavior**: no provider call in the first two rows; in all others exactly one `Settle(max, returned cost)`,
  also after cancellation; a failed settle surfaces only in the log; `Propose` is never called in any row.
- **Test name**: `TestSettleUsesTheReserveInstant`
  **Scenario**: the fake clock stands at 30 September 23:59:30 UTC; the provider's `Complete` advances it by 60 seconds
  into October; the fake `Budget` records every `now` and keeps one counter per UTC month.
  **Expected behavior**: `Reserve` and `Settle` carry the same instant; the September counter holds the actual cost;
  no October counter exists.
- **Test name**: `TestRawOutputNeverLeaves` (T20)
  **Scenario**: the provider returns recognizable text: once valid YAML, once prose, once an oversize blob; capturing
  logger; recording GitHub fakes.
  **Expected behavior**: the marker never appears in a log line or error; for prose and blob nothing is written anywhere;
  for valid YAML it appears only as the `.archfit.yaml` file content, not in commit message, title or body.
- **Test name**: `TestForeignAnswerIsNotAProposal`
  **Scenario**: table: the provider returns one-line prose with a colon; a JSON object with foreign keys; a YAML mapping
  without `version`; a mapping with one unknown top-level key; a module with an unknown key; a valid edited policy. Every answer carries a seeded
  marker string; capturing logger.
  **Expected behavior**: `ErrInvalidProposal` and zero `Propose` calls for all but the last, which is proposed as
  `.archfit.yaml`; in every row exactly one `Settle(max, returned cost)`; the marker never appears in a log line or error.
- **Test name**: `TestInstructionIsNotLogged`
  **Scenario**: both modes with a recognizable instruction.
  **Expected behavior**: the logs hold its length only; in customer mode it appears only in the `Dispatch` inputs.
- **Test name**: `TestOneRequestPerBranch`
  **Scenario**: two concurrent managed requests for the same repository and branch while the provider blocks; a third for another branch.
  **Expected behavior**: the second gets `ErrBusy` with no reservation; the third proceeds; after the first ends, a new request is accepted.
- **Test name**: `TestNeverWritesOutsideDraftBranches`
  **Scenario**: valid flows plus requests naming the default branch and a non-`archfit/` branch.
  **Expected behavior**: every recorded write targets an `archfit/` branch through `Proposals.Propose`; `Ready` and `SetBody` are never called.

### Behavior Tests

- **Test name**: `TestStaleProposalCannotOverwriteEdits` (T20)
  **Scenario**: table: the branch head differs already at request time; the head moves while the provider is working; the
  draft PR is merged or closed while the provider is working.
  **Expected behavior**: `ErrStale` in all; in the first nothing is reserved; in the others the budget is settled with the
  actual cost and no commit exists; the newer edit is intact.
- **Test name**: `TestProposalIsNeverAutoPublished` (T20)
  **Scenario**: a successful managed request on a draft PR and on a PR awaiting review.
  **Expected behavior**: the PR state is unchanged; one new commit; no merge, no ready call, nothing on the default branch.
- **Test name**: `TestLLMFailureLeavesEverythingElseWorking` (T20)
  **Scenario**: provider always fails, then budget exhausted, then Store down; between the calls a plain editor save goes
  through `Proposals.Propose` with the same fakes.
  **Expected behavior**: `ErrProvider`, `ErrBudgetExhausted`, `ErrNoConsent` respectively; every editor save succeeds;
  no check run or gate call is made by assist at all.
- **Test name**: `TestCustomerModeKeepsCredentialsAndSourceOut` (T20)
  **Scenario**: a customer-mode request against a memory Store and recording fakes.
  **Expected behavior**: no Store key is written; no provider call; the only outbound data is the three dispatch inputs;
  the run link is returned so that the UI can show the pending run.
- **Test name**: `TestManagedModeWithoutConsentMakesNoCall`
  **Scenario**: consent recorded then revoked; request after each.
  **Expected behavior**: first succeeds; after revocation `ErrNoConsent` and no provider call.

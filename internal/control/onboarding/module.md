# Onboarding

**Path**: internal/control/onboarding/
**Parent**: internal/control/ (control plane façade)
**Submodules**: none (leaf)

## Purpose

`onboarding` takes a repository from "App installed" to "advisory feedback running" in both execution
modes, using only pull requests a human merges. Customer mode is always two PRs: PR 1 adds the workflow,
the App settings file and CODEOWNERS entries; PR 2 carries the policy that discovery in the customer's
CI proposed. Managed mode runs one ephemeral sandbox job after recorded consent and puts the policy on
the onboarding PR. Without this module a customer would hand-write the workflow, settings, CODEOWNERS and policy files and nobody could tell
which step is missing. The same upload path then carries the baseline: a capture by the approved workflow on the
default branch becomes a baseline pull request that an architecture owner approves, never a commit to the default branch.

## Functional Responsibilities

- Derive the onboarding stage and the next action from GitHub on every call. Store nothing.
- Generate PR 1: pinned workflow from the locked manifest, settings with the first owner list, CODEOWNERS entries.
- Serve `POST /v1/discoveries`: reuse the gate's `Receiver`, check the producer, check the draft is no-source policy text, open PR 2.
- Serve `POST /v1/baselines`: reuse the gate's `Receiver`, check the producer at the default-branch head, check the capture
  against the approved identity and the protected policy and labels, then open or update the baseline PR on `archfit/baseline`
  with a body that counts the accepted findings added and removed. Never write the default branch.
- Serve `POST /v1/reanchors`: the same checks, plus the protected baseline's digest and the subset rules of the re-anchor
  section; then the same pull request on `archfit/baseline`, with a body that states what the re-anchor keeps and drops.
- Managed discovery: require `ConsentSourceProcessing`, open the onboarding PR, run `Sandbox.Discover` once, commit the draft policy or a clear failure state.
- Own the `Sandbox` seam and its default ("not available").
- Never enable the required gate: generated settings always say `gate: advisory`.

## Subdomain Classification

Core-adjacent product surface. Volatility **high**: adoption UX, texts, generated files and the managed
path will change with every pilot (product.md §4 leaves usability targets open).

## Encapsulated Knowledge

- Stage derivation rules and diagnostic texts.
- Branch names `archfit/onboarding`, `archfit/policy` and `archfit/baseline`; workflow path `.github/workflows/archfit.yaml`. **[default]**
- The workflow template (embedded, `text/template`) and what it pins.
- CODEOWNERS location lookup and the append-only edit.
- The discovery draft checks (size, text shape, zero waivers).
- The baseline upload checks (head pin, identity, structure, policy and labels binding) and the baseline PR body.
- Managed job state: one hidden marker line in the onboarding PR body.
- Limits, all **[default]** (none is in decisions.md): discovery draft ≤ 1 MB and ≤ 500 bytes per line; sandbox timeout 10 min;
  a `running` marker counts as stalled after 11 min (timeout + 1 min); at most 2 concurrent managed jobs per process; at most 20 notes in a PR body;
  at most 150 findings listed per section of a baseline PR body.
- PR titles and bodies, failure codes shown in the managed PR body.

## Public Contract

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

```go
// ErrAlreadyOnboarded is returned by StartCustomer and StartManaged when Progress is past StageInstalled.
var ErrAlreadyOnboarded = errors.New("onboarding: already onboarded")

// ErrBusy is returned by StartManaged when this process already runs its maximum of managed jobs.
var ErrBusy = errors.New("onboarding: busy, retry later")
```

`ErrAlreadyOnboarded` and `ErrBusy` are sentinels for `errors.Is`; nothing was written when either is returned. The
caller shows them as a state of the repository (`control/ui`: `409`), not as an upstream failure.

`ErrNoSandbox` is the sentinel for "managed discovery is not available". `Deps.Sandbox == nil` (or `Deps.Tokens == nil`)
means the default Sandbox: `StartManaged` then returns `ErrNoSandbox` before it opens anything.
`Deps.BaseURL` is the App URL; it is written into the generated workflow as upload endpoint and OIDC audience.

### Progress

All reads are at `r.DefaultBranch`. `ErrNotFound` from `Repos.File` or `Actions.LatestRun` means "absent";
any other error is returned. "Reported" means: `Actions.LatestRun(r, Settings.Workflow, r.DefaultBranch)`
finds a run AND `Checks.CheckRuns(r, run.HeadSHA, "archfit/state")` is non-empty. Only the App publishes
that check, and only for an accepted upload. **[default]** First match wins:

1. Settings file absent or not parseable, or `Settings.Workflow` file absent → `installed`. `OpenDraft` = `Get("archfit/onboarding")`.
2. Policy absent, not reported → `workflow_merged`. Policy absent, reported → `report_received`. `OpenDraft` = `Get("archfit/policy")`.
3. Policy present, not reported → `policy_merged`.
4. Policy present, reported → `gate_advisory` or `gate_required` from `Settings.Gate`.

`OpenDraft` is set only when `Get` returns `DraftOpen` or `DraftInReview`; merged, closed and none give nil. Diagnostics, one sentence each: "Start onboarding" /
"Review and merge PR #n" / "`.archfit-app.yaml` on the default branch is invalid" / "Run discovery
(workflow_dispatch with `mode` set to `discovery`)" / "Review and merge the policy PR #n" / "Waiting for the first run on
the default branch" / "Advisory feedback is active; an owner enables the gate by PR" / "Required gate is active".
Managed additions at stage `installed`: `Pulls.PullByHead(r, "archfit/onboarding")`; if the PR is open and its `Body`
carries the managed marker (see StartManaged), the diagnostic follows the marker: `running` and
`Now() − started < 11 min` **[default]** → "Managed discovery running since …"; `running` and older → "Managed discovery stalled — retry";
`failed` → "Managed discovery failed (`<code>`) — retry"; `complete` → "Review and merge PR #n". A marker that does not
parse counts as stalled.

### PR 1 content (StartCustomer and StartManaged)

- `owners` must be non-empty and pass `settings.Parse(settings.Render(s))`; otherwise error, no GitHub write.
- If `Progress` is past `installed` → `ErrAlreadyOnboarded`; nothing is written.
- `.archfit-app.yaml` = `settings.Render(Settings{Version: 1, Owners: owners, Gate: GateAdvisory, Workflow: <workflow path>, DataMode: DataCustomer | DataManaged})`.
- Workflow file from the template with `manifest.Current()`: Action pinned to `ActionRef` (commit SHA), `EngineVersion`,
  `ImageDigest`; triggers exactly `pull_request`, `push` to `r.DefaultBranch` and `workflow_dispatch` (the Action refuses
  every other event, `workflow_run`, `pull_request_target` and `merge_group` included); the one dispatch input `mode`
  (choice `report` | `discovery` | `baseline` | `reanchor`, default `report`), passed to the Action as
  `mode: ${{ inputs.mode }}`. The UI's Discover button dispatches `{"mode": "discovery"}`. A dispatched `report` is refused
  by the Action: reports come from `pull_request` and `push` runs.
  `permissions: contents: read, id-token: write`; no secrets; no step runs repository scripts.
  `Deps.BaseURL` is written as the Action's upload endpoint and OIDC audience. **[default: Action input names pinned in the plan]**
  Action obligation, stated in the workflow's header comment and the PR body: the Action reads `.archfit.yaml`,
  `.archfit-baseline.json` and `.archfit-labels.yaml` per file, on a pull request from the PR head when the PR changes that
  file and otherwise from the tip of the base branch (a push or dispatch run from the branch it ran on), and fills the
  envelope's `baseline_digest` and `labels_digest` (bare hex SHA-256, `""` when the file is absent) from exactly the files
  the engine read. The App accepts a head digest only with owner approval of the exact head SHA. Onboarding computes no digest.
- CODEOWNERS: the first existing of `.github/CODEOWNERS`, `CODEOWNERS`, `docs/CODEOWNERS`, else a new `.github/CODEOWNERS`.
  Append, under the line `# Archfit protected paths`, one line per protected path — the five are `/` + `PolicyPath`, `BaselinePath`,
  `LabelsPath`, `settings.Path` and the workflow path: `/.archfit.yaml`, `/.archfit-baseline.json`, `/.archfit-labels.yaml`,
  `/.archfit-app.yaml`, `/<workflow path>` — with the owners as `@login` / `@org/team`.
  Existing bytes are kept unchanged; a line already present is not added again.
- `Proposals.Propose(Change{Branch: "archfit/onboarding", Base: r.DefaultBranch, …}, "")`.
- PR body: what each file does; the five protected paths and that a change to any of them needs an architecture owner's approval
  on the exact head SHA; that Archfit belongs in its own ruleset with a documented bypass, and that a bypass is
  never reported as success; "enable automatic deletion of head branches"; next step (run discovery).

### Discovery upload (`POST /v1/discoveries`)

Statuses stay inside the producer's no-retry set (`400 401 403 409 413`) for permanent errors; body is `{"error": "<code>"}`.

1. `Receiver.Receive(ctx, req, KindDiscovery)`. `*Rejection` → its `Status` and `Code`. `ErrRateLimited` → `503`. Other error → `500`.
2. `Upload.Problems` non-empty → `400` with the first problem as code. This is the only event rule: `Receiver` reports any event other than
   `workflow_dispatch` or `push` (so also `pull_request`) as `ProblemUnsupportedEvent` → `400 unsupported_event`.
3. `Binding.BaseRef != Binding.Repo.DefaultBranch` → `403 not_default_branch`.
4. Settings at the default branch: absent or invalid → `403 settings_missing`; `Binding.WorkflowPath != Settings.Workflow` → `403 workflow_not_approved`.
5. Draft checks on `Upload.Payload`, in this order; the first failure decides and nothing is proposed:
   1. Size: over 1 MB **[default]** → `413 discovery_too_large`; empty → `400 discovery_invalid`.
   2. Shape: valid UTF-8; no control character except `\n`; every line ≤ 500 bytes **[default]** → else `400 discovery_invalid`.
   3. `policy.Validate(payload)` returns an error → `400 discovery_invalid`. The error text is dropped: never echoed in the response, never logged.
   4. `policy.Waivers(payload)` returns an error or any waiver → `400 discovery_invalid` (discovery never proposes exceptions).

   The shape checks are not a classifier: any short-lined text passes them. `Validate` is what keeps a foreign document (an engine
   report, prose, JSON) from becoming a pull request. Residual exposure: free-text fields inside a structurally valid policy (for example
   a waiver `reason`) are the customer's own authored policy text. **[default]** `control` packages import no YAML library; `wire/policy` parses.
6. `Proposals.Propose(Change{Branch: "archfit/policy", Base: DefaultBranch, Files: [{PolicyPath, payload}], …}, "")`.
   `ErrRateLimited` → `503`; other error → `500`.
7. `200` with `{"pull_request": <number>, "url": "<url>"}`.

The PR body states: engine discovery draft from run `<RunID>`, digest `<Upload.Digest>`; a proposal to review, not
measurement evidence. The handler never creates or updates a check run or comment.

Steps 1 to 4 are one function shared with the baseline upload (`checkProducer`); discovery reads the settings at the
default branch by name.

### Re-anchored baseline upload (`POST /v1/reanchors`)

After an engine or profile change, the Action runs `archfit baseline --reanchor` in the pinned image with the protected
baseline mounted read-only. The engine keeps only the accepted debt and seams the protected baseline already has. The
App does not trust that. It runs `Baseline upload` below with these additions, after step 8 and before the unchanged check:

- The protected `.archfit-baseline.json` at the head commit is absent or not a readable `archfit.baseline.v3` file →
  `403 reanchor_baseline_missing`.
- `policy.Digest` of the protected bytes is not `Upload.BaselineDigest` (the envelope's `baseline_digest`, required and
  64 lowercase hex for `kind: reanchor`) → `403 reanchor_digest_mismatch`.
- A capture finding whose (fingerprint, rule ID) pair the protected baseline does not accept, or a capture seam not in the
  protected `state.qualifying_seam_ids` → `403 reanchor_not_subset`. A re-anchor keeps debt; it never accepts new debt.

The pull request body states kept, dropped, added (always `0`), seams no longer qualifying, and each metric whose value
changes at the same version. Approving resets those metrics. The engine's full report stays in the run's job summary and
the `archfit-baseline` artifact, and the body names the run. The route is `POST /v1/reanchors`, with `kind: reanchor`.
The body is the new baseline file only, like `kind: baseline`.

### Baseline upload (`POST /v1/baselines`)

decisions.md §4: the baseline is a file on the protected ref, it changes only through an owner-approved pull request,
and the App never commits it there. This handler is the App side of the Action's `mode: baseline`: the Action runs
`archfit baseline` in the pinned image on the default branch and uploads the file it wrote; the handler turns it into
a pull request. Statuses and body format are those of the discovery upload. The first failure decides; nothing is
proposed on any failure.

1. `Receiver.Receive(ctx, req, KindBaseline)`, mapped as in discovery step 1.
2. `Upload.Problems` non-empty → `400` with the first problem as code (a `pull_request` run gives `unsupported_event`).
3. `Binding.BaseRef != DefaultBranch` → `403 not_default_branch`.
4. `Repos.BranchHead(DefaultBranch)`: error → `503 rate_limited` or `500 internal`; not equal to `Binding.HeadSHA` →
   `409 stale_head`. The capture describes an older tree; the Action treats `409` as superseded, and an owner re-dispatches.
   Every later read is at that head commit, not at the branch name, so a push between two reads cannot mix two trees.
5. Settings at the head commit: absent or invalid → `403 settings_missing`; `Binding.WorkflowPath != Settings.Workflow` →
   `403 workflow_not_approved`.
6. `manifest.Lookup(Upload.Identity)` misses → `403 unknown_engine_identity`.
7. More than `policy.MaxBaselineBytes` (1 MiB) → `413 baseline_too_large`.
8. `policy.ReadBaseline(payload)` fails → `400 baseline_invalid`. The error text is dropped.
9. The capture's `ProfileVersion` differs from the manifest row's `ProfileVersion` → `403 unknown_engine_identity` (the
   gate's identity predicate: an approved row and that row's profile).
10. Policy at the head commit: absent → `403 policy_missing`; `policy.Digest(policy) != ConfigHash` → `403 policy_mismatch`.
    The capture must have run under the protected policy, never under a head-tree or older one.
11. Labels at the head commit: `policy.Digest` of the file, or `""` when absent, must equal `Upload.LabelsDigest`, else
    `403 labels_mismatch`.
12. Baseline at the head commit: byte-equal to the payload → `200 {"unchanged": true}`, and nothing is written (an
    unchanged file would give GitHub a pull request without commits). Absent, readable or unreadable decides the body.
13. `Proposals.Propose(Change{Branch: "archfit/baseline", Base: DefaultBranch, Files: [{BaselinePath, payload}], …}, "")`,
    then `Proposals.SetBody` with the same body: `Propose` keeps the body of an open pull request, and a re-upload
    must describe the bytes it committed. `ErrRateLimited` → `503`; other error → `500`.
14. `200` with `{"pull_request": <number>, "url": "<url>"}`.

The committed bytes are the payload, unchanged: the engine wrote them and the Action's self-check found them
comparable. A retry of the same capture commits nothing new (the tree is unchanged) and rewrites the same body, so it is
idempotent; a new capture adds a commit to the same branch and the same pull request.

The PR body is deterministic and carries IDs, digests and counts only:

- the default-branch head the capture ran at, and that the pull request changes only `.archfit-baseline.json` and the
  App never commits a baseline to the default branch;
- "Approving accepts N findings that the baseline on `main` does not accept today", where N counts fingerprints of the
  capture that the protected baseline lacks;
- a table: accepted findings on the default branch (`none` when absent, `unreadable` when not a readable v3 file), in
  this pull request, added, removed (`unknown` when unreadable);
- the added and the removed findings, one line each, `rule_id` and fingerprint, sorted by rule then fingerprint, at
  most 150 per list **[default]** with an "and N more" line (GitHub refuses a body over 65 536 characters; a listed
  line is at most 168 characters, so two full lists stay under 51 000);
- run ID and attempt, engine version, platform, image digest, profile version, policy digest, labels digest or "no
  labels file", payload digest, and that the capture is customer-attested: the App checked the run, workflow, head,
  identity and digests and did not run the engine;
- that an architecture owner named in `.archfit-app.yaml` must approve the exact head commit, and that a new commit needs
  a new approval.

When the protected baseline is absent or unreadable, every captured finding counts as added: overstating what approval
accepts is the safe direction. The existing protected-path rule of the gate then governs the merge: the pull request
changes `.archfit-baseline.json`, so its own report needs an owner approval of the exact head SHA. The handler never
creates or updates a check run or comment.

### StartManaged

1. `Consents.Has(ctx, r.InstallationID, ConsentSourceProcessing)` false → `ErrNoConsent`. Nothing else happens.
2. `Deps.Sandbox == nil` or `Deps.Tokens == nil` → `ErrNoSandbox`.
3. The onboarding PR is open and its marker says `running` and is younger than 11 min **[default]** → return its `Draft`; no second job
   (holds across instances). Two jobs **[default]** already running in this process → `ErrBusy`.
4. Open draft exists (`Get`) → reuse it; else PR 1 content with `DataManaged`. `SetBody`: "Managed discovery running since `<Now()>`"
   plus the marker line `<!-- archfit:managed state=running started=<RFC 3339 UTC> -->`.
5. Start one goroutine with `context.WithTimeout(context.Background(), 10*time.Minute)` **[default]** (not the request context):
   `Tokens.RepoReadToken(ctx, r.InstallationID, r.FullName)` → `Token` (uncached, `contents: read`, this repository only; never logged), then
   `Sandbox.Discover(DiscoveryJob{CloneURL: "https://github.com/<FullName>.git", Token, Ref: r.DefaultBranch, ImageDigest: manifest.Current().ImageDigest, Timeout: 10 min})`.
6. Success: run the draft checks of the discovery upload on `DraftPolicy` — the same function, so size, shape, `policy.Validate` and
   zero waivers; a result that fails any of them ends in the failure state `invalid_draft` of step 7 with no policy commit. Else `Propose` the policy onto the same branch;
   `SetBody` with marker `state=complete`, "discovery complete", the consent reference (kind, and the time step 1 CHECKED the
   consent with `Deps.Now`, worded "checked at": `Consents` exposes only `Has`, so the time the consent was recorded is not available here), and up to 20 **[default]** `Notes` that pass the line checks, each sanitized (see Constraints).
7. Failure: `SetBody` with marker `state=failed code=<code>` and one code — `token_error`, `sandbox_unavailable`, `timeout`, `sandbox_failed`, `invalid_draft`, `github_error` — and "retry from the Archfit UI".
   The sandbox error text is never copied anywhere. Return the `Draft` from step 4 immediately; the job outcome is read through `Progress`.

No LLM runs here. Managed LLM refinement is a separate `assist` request on the resulting draft, under its own consent.

## Integrations

Every integration is **contract** strength, volatility **high** (this module's side), and balanced. Outbound only;
inbound consumers are `internal/control/` (`New`, `DiscoveryHandler`, `BaselineHandler`; control / 1 / 1) and `internal/control/ui/`
(`Progress`, `StartCustomer`, `StartManaged`; control / 1 / 1).

### internal/gate

- **Counterpart**: `internal/gate/` (façade only; never `gate/ingest`)
- **Direction**: `onboarding` depends on `gate`; the gate never imports or calls `onboarding`
- **Strength**: contract
- **LCA / Rank / Distance**: root / 2 / 2
- **Volatility**: high
- **Balanced?**: yes
- **Shared knowledge**: one provenance path for every upload route.

<!-- contract: Receiver, Rejection — restated from internal/gate/module.md -->
```go
// Rejection is returned when no tenant could be authenticated or the upload is stale.
// Nothing is published for a Rejection; the HTTP status is the whole answer.
type Rejection struct {
    Status int    // 400, 401, 403, 409, 413, 429, 503
    Code   string // stable machine-readable reason
}

func (r *Rejection) Error() string

type Receiver interface {
    // Receive authenticates and binds one upload of the given kind. A stale upload
    // (head is no longer the PR head, or an older attempt of the same run) is a 409 Rejection.
    Receive(ctx context.Context, req *http.Request, kind Kind) (Upload, error)
}
```

<!-- contract: Upload, Binding, Problem — restated from internal/gate/module.md -->
```go
// Binding is the authoritative identity of an upload, resolved from OIDC claims and the GitHub API.
type Binding struct {
    Repo         Repo   // resolved by the App, never from the envelope
    Event        string // "pull_request" | "push"; discovery and baseline uploads also "workflow_dispatch"
    PullRequest  int    // 0 unless pull_request
    HeadSHA      string
    BaseRef      string // PR base branch; for other events the branch the run executed on
    BaseSHA      string
    MergeBaseSHA string
    Fork         bool
    RunID        int64
    RunAttempt   int
    WorkflowPath string
}

// Problem is an eligibility defect found after the tenant was authenticated.
// Problems never reject the request; they force the gate to action_required.
type Problem string

const (
    ProblemEnvelopeMismatch Problem = "envelope_mismatch" // envelope disagrees with authoritative data
    ProblemUnsupportedEvent Problem = "unsupported_event" // e.g. workflow_run
    ProblemFork             Problem = "fork"
    ProblemReportInvalid    Problem = "report_invalid" // payload failed no-source decoding
)

type Upload struct {
    Binding  Binding
    Identity Identity // claimed engine/image identity from the envelope
    Digest   string   // payload digest, verified against the envelope
    BaselineDigest string // claimed by the envelope; "" = the producer used no baseline
    LabelsDigest   string // claimed by the envelope; "" = the producer used no labels file
    Payload  []byte   // the exact verified bytes; the only content for KindDiscovery and KindBaseline
    Report   *Report  // nil for KindDiscovery, KindBaseline and when ProblemReportInvalid
    Problems []Problem
}
```

`Report` is an opaque part of `Upload` for this module; it is always nil for `KindDiscovery` and `KindBaseline`.
Discovery uploads leave `BaselineDigest` and `LabelsDigest` empty; this module never reads them for discovery. A
baseline upload has an empty `BaselineDigest` (the envelope decoder enforces it), and its `LabelsDigest` must equal the
digest of the protected labels file. `Identity` is looked up in the manifest for a baseline upload.
For discovery and baseline, `Binding.Event` may also be `workflow_dispatch`; for non-PR events `Binding.BaseRef` is the
branch the run executed on, and `Binding.HeadSHA` the commit the run checked out.

### internal/control/proposal

- **Counterpart**: `internal/control/proposal/`
- **Direction**: `onboarding` depends on `proposal`
- **Strength**: contract
- **LCA / Rank / Distance**: control / 1 / 1
- **Volatility**: high
- **Balanced?**: yes
- **Shared knowledge**: `Propose` with `expectHeadSHA == ""`, `Get`, `SetBody`. Retrying never opens a second PR.
  `Propose` keeps the body of an open PR, so the baseline upload calls `SetBody` after it.

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

`ErrStale` is the sentinel `Propose` returns for a non-empty `expectHeadSHA` mismatch; this module never passes one.
`Get` returns `DraftNone` with a nil error when the branch has no open PR.

### internal/control/account

- **Counterpart**: `internal/control/account/`
- **Direction**: `onboarding` depends on `account` (`Consents.Has` only)
- **Strength**: contract
- **LCA / Rank / Distance**: control / 1 / 1
- **Volatility**: high
- **Balanced?**: yes
- **Shared knowledge**:

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

`ErrNoConsent` is the sentinel `StartManaged` returns when `Has` is false; the UI maps it to the consent page.

### internal/wire/envelope

- **Counterpart**: `internal/wire/envelope/`
- **Direction**: `onboarding` depends on `wire/envelope` (`KindDiscovery`, `KindBaseline`, `KindReanchor` and `PayloadDigest`; decoding happens inside `Receiver`)
- **Strength**: contract
- **LCA / Rank / Distance**: root / 2 / 2
- **Volatility**: high
- **Balanced?**: yes
- **Shared knowledge**:

<!-- contract: Envelope, Kind, SchemaV1, KindReport, KindDiscovery, KindBaseline — restated from internal/wire/envelope/module.md -->
```go
// SchemaV1 is the only envelope schema version accepted.
const SchemaV1 = "archfit.report-envelope.v1"

// Kind names the payload that accompanies the envelope.
type Kind string

const (
    KindReport    Kind = "report"    // engine state report, POST /v1/reports
    KindDiscovery Kind = "discovery" // discovery draft policy, POST /v1/discoveries
    KindBaseline  Kind = "baseline"  // captured engine baseline file, POST /v1/baselines
    KindReanchor  Kind = "reanchor"  // re-anchored baseline, POST /v1/reanchors
)

// Envelope is what the producer states about a payload. Every field is self-reported:
// it must match authoritative GitHub data and is never trusted alone.
type Envelope struct {
    SchemaVersion string `json:"schema_version"` // must equal SchemaV1
    Kind          Kind   `json:"kind"`
    Repository    string `json:"repository"`     // "owner/name"
    Event         string `json:"event"`          // "pull_request" | "push" | "workflow_dispatch"
    PullRequest   int    `json:"pull_request"`   // 0 unless Event == "pull_request"
    HeadSHA       string `json:"head_sha"`       // 40 lowercase hex
    BaseSHA       string `json:"base_sha"`       // "" unless pull_request
    MergeBaseSHA  string `json:"merge_base_sha"` // "" unless pull_request
    RunID         int64  `json:"run_id"`
    RunAttempt    int    `json:"run_attempt"`
    EngineVersion string `json:"engine_version"` // e.g. "v3.0.0"
    ImageDigest   string `json:"image_digest"`   // "sha256:<64 hex>"
    Platform      string `json:"platform"`       // e.g. "linux/amd64"
    PayloadDigest  string `json:"payload_digest"`  // "sha256:<64 hex>" of the exact payload bytes
    BaselineDigest string `json:"baseline_digest"` // bare hex SHA-256 of the baseline file the engine loaded; "" = none; always "" for discovery and baseline
    LabelsDigest   string `json:"labels_digest"`   // bare hex SHA-256 of the labels file the engine loaded; "" = none
}
```

### internal/wire/settings

- **Counterpart**: `internal/wire/settings/`
- **Direction**: `onboarding` depends on `wire/settings`
- **Strength**: contract
- **LCA / Rank / Distance**: root / 2 / 2
- **Volatility**: high
- **Balanced?**: yes
- **Shared knowledge**:

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

Owner entries are validated by `Parse`: login `^[A-Za-z0-9-]+$` or team `^[A-Za-z0-9-]+/[A-Za-z0-9._-]+$`;
the workflow path must be under `.github/workflows/` and end in `.yml` or `.yaml`.

### internal/wire/policy

- **Counterpart**: `internal/wire/policy/`
- **Direction**: `onboarding` depends on `wire/policy`
- **Strength**: contract
- **LCA / Rank / Distance**: root / 2 / 2
- **Volatility**: high
- **Balanced?**: yes
- **Shared knowledge**: the three engine file paths (protected paths in CODEOWNERS and the PR body), `Validate` as the structural
  check of a draft, and `Waivers` as its zero-exceptions check. `Validate`'s error text is never echoed or logged.
  For a baseline upload: `ReadBaseline` as its structural check, `MaxBaselineBytes`, and `Digest` (bare lowercase hex,
  no `sha256:` prefix) of the protected policy and labels files, compared with `Baseline.ConfigHash` and the envelope's
  labels digest. `ReadBaseline`'s error text is never echoed or logged.

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

<!-- contract: Validate — restated from internal/wire/policy/module.md -->
```go
// Validate reports whether policy is plausibly an engine policy file: a YAML document whose root is a
// mapping, whose top-level keys all belong to the engine config schema's closed key set, and which has
// the required `version` key. It is a structural check, not engine validation: the engine remains the
// authority on the file's meaning. Errors never echo keys or values.
func Validate(policy []byte) error
```

<!-- contract: BaselineSchemaV3, MaxBaselineBytes, Accepted, Baseline, ReadBaseline — restated from internal/wire/policy/module.md -->
```go
// BaselineSchemaV3 is the only engine baseline schema the App reads (engine v3.0.0 writes it).
const BaselineSchemaV3 = "archfit.baseline.v3"

// MaxBaselineBytes caps a baseline file: 1 MiB, the size github/api reads back from the protected ref.
const MaxBaselineBytes = 1 << 20

// Accepted is one accepted finding of a baseline: its engine finding ID and the rule that produced it.
type Accepted struct {
    Fingerprint string // 32 lowercase hex
    RuleID      string
}

// Baseline is the App's view of an engine baseline file: what it accepts and what it was captured under.
type Baseline struct {
    Accepted       []Accepted // file order
    ConfigHash     string     // state.config_hash: Digest of the policy the capture ran under
    ProfileVersion string     // state.measurement_profile.version
}

// ReadBaseline decodes an engine baseline file. It is a structural check, not engine validation: one JSON
// object of schema BaselineSchemaV3, at most MaxBaselineBytes, with a state block naming a 64-hex config hash
// and a measurement profile version, and accepted findings with distinct 32-hex fingerprints and token rule IDs.
// Errors never echo keys or values.
func ReadBaseline(raw []byte) (Baseline, error)
```

Fingerprints and rule IDs are token classes after `ReadBaseline`, so the PR body's sanitizer (see Constraints) leaves
them unchanged in their code spans.

### internal/wire/manifest

- **Counterpart**: `internal/wire/manifest/`
- **Direction**: `onboarding` depends on `wire/manifest` (`Current` for the workflow, `Lookup` for a baseline upload)
- **Strength**: contract
- **LCA / Rank / Distance**: root / 2 / 2
- **Volatility**: high
- **Balanced?**: yes
- **Shared knowledge**:

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

### internal/github/api

- **Counterpart**: `internal/github/api/`
- **Direction**: `onboarding` depends on `github/api` (roles `Repos`, `Actions`, `Checks`, `Pulls.PullByHead` through `*Client`; `FileChange` for changes)
- **Strength**: contract
- **LCA / Rank / Distance**: root / 2 / 2
- **Volatility**: high
- **Balanced?**: yes
- **Shared knowledge**: internally the module holds `Repos`, `Actions`, `Checks`, `Pulls` interface values taken from `Deps.GitHub`, so tests inject fakes.

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

<!-- contract: Client, New — restated from internal/github/api/module.md -->
```go
// Client implements every role interface. Consumers depend on the role interfaces they need.
type Client struct{ /* unexported */ }

func New(ts TokenSource, hc *http.Client) *Client
```

`ErrNotFound` is the sentinel for an absent file or run; `ErrRateLimited` is the sentinel after bounded backoff.
`Repos.BranchHead` pins a baseline upload to the default-branch head, and `Repos.File` then reads at that commit SHA.
Of `Pulls` only `PullByHead` is called, to read the onboarding PR's `Body`; `Review` is unused. This
module never calls `Changes`, it only builds `FileChange` values. It only reads check runs; it never calls
`CreateCheckRun`, `UpdateCheckRun`, `Rerun` or `Dispatch` (the UI dispatches discovery).

### internal/github/auth

- **Counterpart**: `internal/github/auth/`
- **Direction**: `onboarding` depends on `github/auth` (`TokenSource.RepoReadToken` only)
- **Strength**: contract
- **LCA / Rank / Distance**: root / 2 / 2
- **Volatility**: high
- **Balanced?**: yes
- **Shared knowledge**: the sandbox clone token. `AppJWT`, `InstallationToken` and `Invalidate` are never called here; `Credentials` is unused.

<!-- contract: Credentials, TokenSource — restated from internal/github/auth/module.md -->
```go
// Credentials are loaded by the composition root from secret references, never inline.
type Credentials struct {
    AppID             int64
    PrivateKeyPEM     []byte
    OAuthClientID     string
    OAuthClientSecret string
}

// TokenSource mints GitHub App credentials. Installation tokens are cached until 5 minutes before expiry.
type TokenSource interface {
    AppJWT() (string, error)
    InstallationToken(ctx context.Context, installationID int64) (string, error)
    // Invalidate drops the cached installation token (called after a 401).
    Invalidate(installationID int64)
    // RepoReadToken mints an uncached token limited to contents:read on one repository.
    RepoReadToken(ctx context.Context, installationID int64, fullName string) (string, error)
}
```

## Change Vectors

- Texts, diagnostics, PR bodies, the workflow template, a new manifest entry.
- The one-draft-PR flow (deferred, decisions.md §5): a new start method; the two-PR flow stays.
- A real sandbox: a platform package `control/onboarding/<driver>` implementing `Sandbox`, chosen in `app`.
- A cross-instance concurrency quota for managed jobs, if more than one instance runs.

## Constraints and Invariants

- Nothing here is ever called by the gate path. A failure here never changes a gate result. The gate reads only the cached `Entitlement` record that `account` writes.
- Two PRs always in customer mode. The one-draft-PR flow is deferred (decisions.md §5) and not part of v1.
- Discovery output is onboarding evidence only: it never creates, updates or satisfies a check. Provenance of the upload is necessary, not sufficient; nothing says "verified".
- A baseline reaches the default branch only by an owner-approved merge of `archfit/baseline`. The handler writes that
  branch only, with exactly one file, and never commits to the default branch; there is no silent re-baseline. A
  baseline upload never creates, updates or satisfies a check, and its body never says "verified".
- A baseline upload reads settings, policy, labels and the current baseline at one commit, the default-branch head
  that the capturing run checked out. A run at any other commit is `stale_head`.
- Nothing is published without review: every output is a draft PR. `gate: required` is never generated.
- No consent → no sandbox job, no clone, no token minted. Consent is read at call time, never cached.
- No persistent source: this module never clones, never writes source or draft bytes to disk, Store, logs or caches. `DraftPolicy` lives in memory until committed.
- No repository scripts: the generated workflow runs only the pinned Action; the `Sandbox` contract forbids script execution and extra credentials. Customer LLM keys stay in the customer's CI; the App never receives them.
- Tenant identity: `Repo` comes from `Sessions.Authorize` (UI) or `Upload.Binding.Repo` (upload). Never from the envelope, URL or form.
- Strings the App does not control are sanitized before they enter a PR body, by the rule of `gate/feedback`:
  `<!--`, `-->`, backticks and line breaks are removed, then `<` and `>` are HTML-escaped. Every code span of the
  baseline PR body goes through it, so the default branch, which GitHub does not restrict to a token class and which
  may hold a backtick, `<` or `>`, cannot leave its span; in the table its `|` is also written as `\|`. Engine notes
  in the managed PR body go through it after the line checks. Inside a code span an escaped `<` shows as `&lt;`, a
  cosmetic cost of one rule for every span. Logins in PR titles and commit messages (`control/assist`, `control/ui`)
  are not sanitized here; they come from GitHub's user API, whose login rule allows only letters, digits and `-`.
- Never trust head-tree settings: the discovery workflow check reads settings at the default branch only.
- Managed job state lives in the PR body marker, so every instance and every restart reads the same state. A job lost with its process shows "stalled — retry" after 11 min; retry is idempotent on the same branch and PR. The 2-job quota is per process (ceiling: one instance).
- The sandbox token is minted per job, after the consent check, passed only in `DiscoveryJob.Token`, and never stored or logged.
- Failure is always a visible, resumable state: a code in the PR body plus the `Progress` diagnostic. Sandbox error text is never shown or logged.
- Logs: installation ID, repository ID, run ID, digest, PR number, stage, outcome, timings. Never payload bytes, paths, notes, owners' file content or tokens.
- Declared restatement: `CloneURL` is built from the literal host `https://github.com/`. `internal/github` owns the
  knowledge of GitHub hosts and publishes no URL builder; `control/assist` and `control/ui` restate the same host for
  links. On drift the sandbox clones from the wrong host and the job ends in `sandbox_failed`; nothing is published.
  Removal trigger: GitHub Enterprise host support, when `internal/github` publishes the clone base URL.
- Declared restatement: the check name `archfit/state` is a literal here (`Progress` reads it). `internal/gate`
  (feedback) owns and publishes the check names but exports no constant, and control imports only the gate façade.
  `control/ui` restates `archfit/state` and `archfit/gate` too. On drift `Progress` never reaches `report_received`; no
  gate result changes. Removal trigger: a rename of a check, at which point gate exports the names.
- Declared restatement: the 1 MB size limit of a discovery draft equals the input limit of `wire/policy`
  (`Validate`, `Waivers` refuse more than 1 MiB), which owns it and exports no constant. The copy exists to answer
  `413 discovery_too_large` instead of `400`. On drift a draft between the two limits gets the other status; it is
  refused either way. Removal trigger: wire/policy exports its limit, or the two limits are meant to differ.
- Standard library only (`text/template`, `embed`, `unicode/utf8`, `net/http`, `encoding/json`).

## Test Specification

Test doubles only at boundaries: in-memory GitHub roles (`Repos`, `Actions`, `Checks`, `Changes`, `Pulls`), fake `Receiver`, fake `Sandbox`, fake `TokenSource`, fixed clock.
`Proposals` is the real `proposal.New` over the GitHub fake and `Consents` the real `account.New` over a memory Store (failing Store for outage cases);
"one `Propose`" below is observed as one branch and one PR in the GitHub fake.

### Unit Tests

- **Test name**: `TestStageDerivation`
  **Scenario**: table over (settings, workflow file, policy, latest run, state check, gate mode): nothing; settings only; invalid settings; settings+workflow; +run without check; +run with check; policy without report; policy+report advisory; policy+report required.
  **Expected behavior**: `installed` ×3, `workflow_merged` ×2, `report_received`, `policy_merged`, `gate_advisory`, `gate_required`; each with its diagnostic; `OpenDraft` set only when the matching branch has an open PR.
- **Test name**: `TestManagedMarker`
  **Scenario**: table over PR bodies: no marker; `running` at −5 min; `running` at −12 min; `failed code=timeout`; `complete`; garbled marker; marker on a closed PR.
  **Expected behavior**: no managed diagnostic; running; stalled; failed with the code; review PR; stalled; no managed diagnostic. Render then parse round-trips. A complete body keeps at most 20 notes and the marker last; a note with a backtick, `<img>` and `<details>` appears with the backtick removed and the angle brackets escaped, a note with `<!--` is dropped, and the body holds exactly one marker.
- **Test name**: `TestSanitize`
  **Scenario**: table: a branch name, a rule ID, a backtick, `<!--…-->`, angle brackets, line breaks, a pipe; then strings from which stripping could reassemble `<!--`.
  **Expected behavior**: unchanged; unchanged; removed; removed; `&lt;`/`&gt;`; removed; unchanged; `mono` is the sanitized value in backticks; no reassembled string holds `<`, `>`, a backtick, a line break, `<!--` or `-->`.
- **Test name**: `TestWorkflowTemplate`
  **Scenario**: render with a manifest entry and default branch `trunk`.
  **Expected behavior**: Action pinned by 40-hex `ActionRef`; image digest and engine version present; `Deps.BaseURL` as endpoint and audience; triggers exactly `pull_request`, `push` on `trunk` and `workflow_dispatch`; dispatch inputs `mode` (choice `report`, `discovery`, `baseline`, default `report`) and boolean `discover`, passed as `mode: ${{ inputs.discover && 'discovery' || inputs.mode }}` and `discover: ${{ inputs.discover }}`; `id-token: write`, `contents: read`; the header comment names the three files, says they are read per file (PR head when the PR changes the file, else the base branch tip), never "base-ref", and names the two digests the Action reports; no `secrets.`, no `run:` step, no `workflow_run`, no `pull_request_target`, no `merge_group`.
- **Test name**: `TestCodeownersAppend`
  **Scenario**: table: no file; file in `.github/`; file at root only; file without trailing newline; entries already present; team owner.
  **Expected behavior**: original bytes are a prefix of the result; five lines added once (policy, baseline, labels, settings, workflow); second run adds nothing; location precedence honored; `@org/team` form used.
- **Test name**: `TestDraftChecks`
  **Scenario**: table: valid policy; 1 MB + 1 byte; invalid UTF-8; NUL byte; carriage return; 501-byte line; malformed YAML; policy with one waiver; a JSON report; a mapping without `version`; an unknown top-level key; seeded leakage fixtures (source snippet block, log blob).
  **Expected behavior**: only the first passes; size → `discovery_too_large`; all others → `discovery_invalid`.
- **Test name**: `TestOwnersValidation`
  **Scenario**: empty list; `bad owner`; `org/team/extra`; valid login; valid team.
  **Expected behavior**: errors for the first three with no GitHub write; valid entries render into settings.

### Integration Contract Tests

- **Test name**: `TestStartCustomerOpensPROne`
  **Scenario**: fresh repository, owners `alice`, `acme/architects`.
  **Expected behavior**: one `Propose` on `archfit/onboarding` against the default branch with exactly three files; settings parse back with `gate: advisory`, `data_mode: customer`; returned `Draft` is the proposal's.
- **Test name**: `TestDiscoveryUploadOpensPRTwo`
  **Scenario**: `Receiver` returns an upload: event `workflow_dispatch`, default branch, approved workflow path, valid draft.
  **Expected behavior**: `200` with the PR number; one `Propose` on `archfit/policy` with one file at `PolicyPath` whose bytes equal `Upload.Payload`; empty `BaselineDigest` and `LabelsDigest` are accepted and ignored; no check or comment call.
- **Test name**: `TestDiscoveryStatusMapping`
  **Scenario**: table: `*Rejection` 401/403/409/413; `ErrRateLimited`; generic error; each `Problem` (including `ProblemUnsupportedEvent` for a `pull_request` discovery); non-default branch; settings missing; other workflow path; oversized; invalid draft; `Propose` rate limited.
  **Expected behavior**: rejection status and code; 503; 500; `400` with the problem code; `403 not_default_branch`, `403 settings_missing`, `403 workflow_not_approved`; 413; 400; 503. No `Propose` call except in the last row. Every permanent error is inside `400 401 403 409 413`.
- **Test name**: `TestBaselineUploadOpensBaselinePR`
  **Scenario**: the protected head holds the real capture with two accepted findings; the upload is the real capture of
  the same policy with none.
  **Expected behavior**: `200` with the PR number; `Receive` asked for `KindBaseline`; one `Propose` on `archfit/baseline`
  into the default branch with exactly `.archfit-baseline.json` equal to the payload; nothing written to the default branch;
  the body names both removed fingerprints, "Approving accepts no finding", the run, the head, the policy and payload
  digests, an architecture owner and the exact head commit, "customer-attested", and never "verified"; no check call.
- **Test name**: `TestBaselineStatusMapping`
  **Scenario**: table: rejections; `ErrRateLimited`; generic error; a `pull_request` run; envelope mismatch; fork; another
  branch; the default branch moved; `BranchHead` rate limited; settings missing; other workflow; settings and policy edited
  on the branch name but not at the head commit; unlisted image; unapproved profile; oversized; a policy and a report as
  payload; schema v1; policy missing; another policy; labels claimed but absent, present but not claimed, present and
  claimed; `Propose` rate limited and failing; `SetBody` failing.
  **Expected behavior**: the status and code of the step that refuses; `200` for the three accepted rows; no `Propose` call
  and no PR on any refusal; no baseline on the default branch.
- **Test name**: `TestStartManagedHappyPath`
  **Scenario**: consent present; fake sandbox returns a valid draft and two notes.
  **Expected behavior**: draft PR opened with `data_mode: managed` and a "running since" body; job receives a token from `RepoReadToken` for exactly this repository, `Ref` = default branch, manifest image digest, 10-minute timeout; policy committed to the same branch; body updated to complete; `Progress` diagnostic follows.
- **Test name**: `TestNilSandboxIsNotAvailable`
  **Scenario**: `Deps.Sandbox == nil`, and `Deps.Tokens == nil`, consent present.
  **Expected behavior**: `ErrNoSandbox`; no `Propose`, no PR.

### Boundary Tests

- **Test name**: `TestNoConsentNoJob`
  **Scenario**: `Has` false; `Has` false because the Store fails; consent revoked between two calls.
  **Expected behavior**: `ErrNoConsent` each time; zero sandbox calls, zero `Propose` calls, no token requested (T19).
- **Test name**: `TestManagedFailureStates`
  **Scenario**: table: `RepoReadToken` fails; sandbox returns `ErrNoSandbox`; generic error containing a seeded source string; blocks past the timeout; returns an invalid draft; returns a foreign document (JSON with a seeded string) as the draft; `Propose` of the policy fails.
  **Expected behavior**: body marker `state=failed` with codes `token_error`, `sandbox_unavailable`, `sandbox_failed`, `timeout`, `invalid_draft` ×2, `github_error`; no policy commit; the seeded string appears in no body or log; context is cancelled at the timeout; a retry reuses the same PR (T19 resumable state).
- **Test name**: `TestManagedJobLimits`
  **Scenario**: `StartManaged` twice for one repository while the marker says `running` (second call on a fresh `Onboarding` value); a third repository while two jobs run; request context cancelled right after the call.
  **Expected behavior**: one sandbox call per repository; third gets `ErrBusy`; the job survives request cancellation.
- **Test name**: `TestAlreadyOnboardedRefusesStart`
  **Scenario**: settings and workflow already on the default branch; call both start methods.
  **Expected behavior**: `ErrAlreadyOnboarded` from both; no `Propose`.
- **Test name**: `TestDiscoveryNeverTouchesChecks`
  **Scenario**: run every handler path against recording fakes.
  **Expected behavior**: `CreateCheckRun`, `UpdateCheckRun`, `Rerun`, `Dispatch` are never called.
- **Test name**: `TestHeadSettingsAreIgnored`
  **Scenario**: discovery upload from a run whose branch changed `.archfit-app.yaml` to another workflow path.
  **Expected behavior**: the default-branch settings decide; `403 workflow_not_approved`.
- **Test name**: `TestLogsAndBodiesCarryNoPayload`
  **Scenario**: drafts, notes and errors with marker strings through all flows; capture logs and PR bodies.
  **Expected behavior**: markers appear only in the committed policy file and in accepted notes; never in logs or error responses.
- **Test name**: `TestDiscoveryRejectsForeignDocuments`
  **Scenario**: table of uploads that all pass the shape checks, each foreign row carrying a seeded marker string: an engine state report JSON; one-line prose with a colon; a YAML sequence; a mapping without `version`; a mapping with an unknown top-level key; and a real engine policy (`version`, `modules`, `rules`).
  **Expected behavior**: every foreign row → `400 discovery_invalid`, zero `Propose` calls, no PR, and the marker in no log line and no response body; the real policy → `200`, one `Propose`, committed bytes equal the upload.
- **Test name**: `TestBaselineBodyAndLogsCarryOnlyIDs`
  **Scenario**: a real capture with an unknown key whose value is a marker string.
  **Expected behavior**: the marker is committed in the file and appears in no PR body, response or log line.
- **Test name**: `TestBaselineBodySanitizesDefaultBranch`
  **Scenario**: the baseline PR body for default branches `main`, ``rel`ease``, `a<!--b`, `x-->y`, `<!<!---->--x`, `x<img src=y>` and `a|b`.
  **Expected behavior**: the branch renders as `` `main` ``, `` `release` ``, `` `ab` ``, `` `xy` ``, an inert string, `` `x&lt;img src=y&gt;` `` and `` `a|b` ``; no `<!--`, `-->` or `<img` in the body; every line closes its code spans; every table row keeps exactly three cell borders.
- **Test name**: `TestBaselineBodyStaysUnderGitHubLimit`
  **Scenario**: a protected baseline of 300 findings and a capture of 3 000 other findings, all with 128-character rule IDs.
  **Expected behavior**: the body counts 3 000 added and 300 removed, lists 150 of each with "and 2850 more" and
  "and 150 more", and stays under 65 536 characters.
- **Test name**: `TestProgressErrors`
  **Scenario**: `File` returns `ErrRateLimited`; `LatestRun` returns `ErrNotFound`; `CheckRuns` fails.
  **Expected behavior**: error returned; "not reported" without error; error returned. No stage is guessed.

### Behavior Tests

- **Test name**: `TestCustomerFlowEndToEnd`
  **Scenario**: start → merge PR 1 in the fake → `Progress` → discovery upload → merge PR 2 → state check appears → settings switched to required.
  **Expected behavior**: stages `installed` (with draft), `workflow_merged`, `workflow_merged` with the policy draft, `policy_merged`, `gate_advisory`, `gate_required`; exactly two PRs (T18; one-draft-PR flow not used).
- **Test name**: `TestRetriesNeverDuplicatePRs`
  **Scenario**: `StartCustomer` five times; the same discovery upload three times; `StartManaged` retried after a failure.
  **Expected behavior**: one PR per branch; the newest content on the branch (T18).
- **Test name**: `TestBaselineReuploadUpdatesTheSamePR`
  **Scenario**: the same capture uploaded twice, then a new capture of the same policy.
  **Expected behavior**: one PR on `archfit/baseline`; the branch carries the newest capture; the body is rewritten for it;
  the default branch gets no file.
- **Test name**: `TestBaselineFirstAdoptionCountsEveryFindingAsAdded` / `TestBaselineUnreadableProtectedBaselineOvercountsAdded`
  **Scenario**: no protected baseline, with a labels file; a protected baseline of schema v1.
  **Expected behavior**: every captured finding counts as added; the body says there is no baseline yet, or that the
  protected file is not readable and removals are unknown.
- **Test name**: `TestBaselineUnchangedOpensNothing`
  **Scenario**: the capture is byte-equal to the protected baseline.
  **Expected behavior**: `200 {"unchanged": true}`; no `Propose`, no `SetBody`, no PR.
- **Test name**: `TestRestrictedOrganizationFallback`
  **Scenario**: `workflow_dispatch` is unavailable; the customer runs the CLI in existing CI on a default-branch `push` and uploads the draft.
  **Expected behavior**: accepted through the same endpoint and checks; PR 2 opens; no elevated permission is requested (T18).
- **Test name**: `TestManagedRestartShowsStalled`
  **Scenario**: start a managed job that never returns, build a new `Onboarding` over the same fakes (process restart); call `Progress` at +5 min and +12 min; then `StartManaged` again.
  **Expected behavior**: "running since" at +5 min with no second job possible; "stalled — retry" at +12 min; the retry runs one new job on the same PR and rewrites the marker (T19).
- **Test name**: `TestOnboardingFailureLeavesGateAlone`
  **Scenario**: every dependency of this module fails in turn.
  **Expected behavior**: errors surface to the caller only; no check run, comment or Store key is written by this module.

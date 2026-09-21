# Gate Ingest

**Path**: internal/gate/ingest/
**Parent**: internal/gate/ (gate façade)
**Submodules**: none (leaf)

## Purpose

`ingest` authenticates one upload and binds it to authoritative GitHub data: who produced it, for which
repository, run, attempt, pull request and head SHA, and whether the bytes are the ones the producer
stated. Without it, the gate would act on a self-reported envelope, and any workflow with a token could
speak for any pull request.

## Functional Responsibilities

- Enforce method and the 5 MB size cap before anything else is parsed.
- Strictly decode the envelope from the `X-Archfit-Envelope` header.
- Verify the GitHub Actions OIDC bearer token (signature, issuer, audience, time).
- Resolve repository, installation, workflow run, attempt, event, pull request, head, base and merge-base
  from the GitHub API, using only the run identity in the verified claims.
- Compare every envelope field with the authoritative value.
- Verify the payload digest; decode a report payload with the no-source decoder.
- Answer with a `Rejection` (HTTP status only) while no tenant is authenticated or when the upload is
  stale; after that, record every defect as a `Problem` so the gate can publish it.
- Serve both upload kinds (`report`, `discovery`) with exactly the same provenance checks.

## Subdomain Classification

Core. Revision binding is the difference between an attested report and an anonymous file. Volatility:
high; event support, binding rules and rejection codes will move with the Action and with pilots.

## Encapsulated Knowledge

- The order of checks and which failure is a `Rejection` versus a `Problem`.
- The rejection code table and HTTP statuses.
- How a pull request is resolved from a workflow run, including ambiguity handling.
- Which GitHub events are eligible per upload kind.
- The envelope header name and how the body is read.
- That the envelope's PR number may serve as a lookup hint but never as a fact.

## Public Contract

```go
// Binding is the authoritative identity of an upload, resolved from OIDC claims and the GitHub API.
type Binding struct {
    Repo         Repo   // resolved by the App, never from the envelope
    Event        string // "pull_request" | "push"; discovery uploads also "workflow_dispatch"
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
    Payload  []byte   // the exact verified bytes; the only content for KindDiscovery
    Report   *Report  // nil for KindDiscovery and when ProblemReportInvalid
    Problems []Problem
}
```

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

```go
func New(repos Repos, pulls Pulls, actions Actions, v Verifier, audience string) Receiver
```

### Receive, step by step

Steps run in this order. The first failing `Rejection` step ends the call with `*Rejection` as the error
(matchable with `errors.As`) and the zero `Upload`. Code strings and the 400 for a wrong method are **[default]**.

| #   | Check                                                                                        | Failure                                                    |
| --- | -------------------------------------------------------------------------------------------- | ---------------------------------------------------------- |
| 1   | Method is `POST`                                                                             | 400 `method_not_allowed`                                   |
| 2   | Body read through `http.MaxBytesReader` with `report.MaxBytes`                               | 413 `payload_too_large`                                    |
| 3   | Header `X-Archfit-Envelope` present; `envelope.Decode` succeeds                              | 400 `envelope_missing`, `envelope_invalid`                 |
| 4   | `Envelope.Kind == kind` (the route's kind)                                                   | 400 `kind_mismatch`                                        |
| 5   | `Authorization: Bearer <token>` present                                                      | 401 `token_missing`                                        |
| 6   | `Verifier.Verify(token, audience)`; `oidc.ErrUnavailable` is the JWKS outage                 | 401 `token_invalid`; 503 `oidc_unavailable`                |
| 7   | `Claims.Repository == Envelope.Repository`                                                   | 403 `repository_mismatch`                                  |
| 8   | `Repos.ResolveRepo(Claims.Repository)`; `api.ErrNotFound` means the App is not installed     | 403 `not_installed`                                        |
| 9   | `Repo.ID == Claims.RepositoryID` (a renamed or re-created repository is not the same tenant) | 403 `repository_mismatch`                                  |
| 10  | `Actions.Run(repo, Claims.RunID)`; `api.ErrNotFound`                                         | 403 `run_not_found`                                        |
| 11  | `run.Event == Claims.EventName`; for `push`, `run.HeadSHA == Claims.SHA`                     | 403 `run_mismatch`                                         |
| 12  | `run.Attempt == Claims.RunAttempt` (the API returns the latest attempt)                      | 409 `stale_attempt`                                        |
| 13  | Event eligibility (below)                                                                    | `ProblemUnsupportedEvent`                                  |
| 14  | Pull request resolution (below), only for `pull_request` runs                                | 409 `stale_head`, `ProblemEnvelopeMismatch`, `ProblemFork` |
| 15  | Envelope fields equal authoritative values (below)                                           | `ProblemEnvelopeMismatch`                                  |
| 16  | `envelope.PayloadDigest(body) == Envelope.PayloadDigest`                                     | 400 `digest_mismatch`                                      |
| 17  | `kind == KindReport`: `report.Decode(body)`                                                  | `ProblemReportInvalid`, `Report` nil                       |

Any step: `api.ErrRateLimited` → 429 `rate_limited`. Any other GitHub API error → a plain error (not a
`Rejection`); the caller answers 503 and publishes nothing. A `Verifier` error matching `oidc.ErrUnavailable`
(`errors.Is`) is the 503 `oidc_unavailable` rejection, so the Action retries; every other `Verifier` error is 401.

The tenant is authenticated after step 12. Steps 13 to 17 never reject, except the two cases where nothing
could be published truthfully: the head moved (step 14) and the bytes are not what the producer stated (step 16).

### Event eligibility (step 13)

| Kind        | Eligible `run.Event`                                                                |
| ----------- | ----------------------------------------------------------------------------------- |
| `report`    | `pull_request`; `push` when `Claims.Ref == "refs/heads/" + Repo.DefaultBranch`      |
| `discovery` | `workflow_dispatch`; `push` when `Claims.Ref == "refs/heads/" + Repo.DefaultBranch` |

Everything else, including `workflow_run`, `pull_request_target`, `schedule` and a push to another branch,
adds `ProblemUnsupportedEvent`. `Binding.Event` is `run.Event` verbatim; for a discovery upload it can be
`workflow_dispatch`.

### Pull request resolution (step 14)

1. Candidates = `run.PullRequests`. When that list is empty (GitHub leaves it empty for fork runs) and
   `Envelope.PullRequest > 0`, the envelope number is the single candidate. It is a lookup hint only; every
   property is then checked against GitHub **[default]**.
2. No candidate, or more than 10 **[default]** → `ProblemEnvelopeMismatch`, `PullRequest` stays 0.
3. One candidate → `Pulls.Pull`. `pull.HeadSHA != run.HeadSHA` → 409 `stale_head`.
4. Several candidates → `Pulls.Pull` each; keep those with `HeadSHA == run.HeadSHA`. Exactly one → it is the
   pull request. None or several → `ProblemEnvelopeMismatch` (ambiguous association is not eligible), `PullRequest` stays 0.
   In steps 3 and 4, `api.ErrNotFound` from `Pulls.Pull` means GitHub does not know that number: the candidate is
   dropped and is "no association". When no candidate is left, the result is `ProblemEnvelopeMismatch` with
   `PullRequest` 0; it is never `stale_head` and never an error. Every other `Pulls.Pull` error ends `Receive`
   (429 for the rate limit, a plain error otherwise).
5. `pull.State != "open"` → `ProblemEnvelopeMismatch`.
6. `Fork = pull.HeadRepo != Repo.FullName`. A fork adds `ProblemFork`. A run with `run.HeadRepo != Repo.FullName` is a fork too.
   ingest owns this rule; `gate` and `gate/sweep` restate it for targets that come from a webhook or from
   `OpenPulls` (declared in their Constraints).

Body read (step 2): only the size cap is a rejection. When `http.MaxBytesReader` stops the read, the answer is 413
`payload_too_large`. Any other read error (the client went away, a broken chunk) is a plain error, not a
`*Rejection`: `gate` answers 503 `unavailable` and the Action retries.

### Binding values

| Field                                    | Source                                                                       |
| ---------------------------------------- | ---------------------------------------------------------------------------- |
| `Repo`                                   | `Repos.ResolveRepo` (carries the authoritative installation ID)              |
| `Event`, `HeadSHA`                       | the workflow run                                                             |
| `RunID`, `RunAttempt`                    | verified claims (equal to the run by steps 10 and 12)                        |
| `PullRequest`, `BaseSHA`, `MergeBaseSHA` | the resolved pull; zero values when there is none                            |
| `BaseRef`                                | `pull.BaseRef`; without a pull, the branch of `Claims.Ref` (see below)       |
| `Fork`                                   | step 14                                                                      |
| `WorkflowPath`                           | `oidc.WorkflowPath(Claims.JobWorkflowRef, Repo.FullName)`; `""` if elsewhere |

`BaseRef` without a pull: `Claims.Ref` minus `refs/heads/` (the branch the run executed on). A ref that is
not a branch (`refs/pull/…`, a tag) gives `Repo.DefaultBranch` **[default]**. This value describes the run; it
is not a protected ref by itself, and `gate` resolves trusted inputs of non-PR targets at the default branch.

Envelope comparison (step 15): `Event`, `PullRequest`, `HeadSHA`, `BaseSHA`, `MergeBaseSHA`, `RunID`,
`RunAttempt` must equal the binding. `Repository` was compared in step 7. `EngineVersion`, `ImageDigest`
and `Platform` have no authoritative source; they are copied to `Upload.Identity` as a claim for the manifest check in `gate`.
`Envelope.BaselineDigest` and `Envelope.LabelsDigest` (bare hex, `""` = none used; format already checked by
`envelope.Decode`) have no authoritative source either: the engine reports no digest for those files. They are copied
unchanged to `Upload.BaselineDigest` and `Upload.LabelsDigest` as producer claims; `gate/verdict` compares them with
the protected ref. They are as trustworthy as the report itself (customer-attested), not more.

`Upload.Digest` is the computed digest. `Upload.Payload` is always the verified body. `Upload.Problems`
is de-duplicated and in the constant declaration order.

## Integrations

### wire/envelope

- **Counterpart**: internal/wire/envelope
- **Direction**: ingest depends on wire/envelope
- **Strength**: contract (versioned JSON Schema, unknown fields rejected)
- **LCA / Rank / Distance**: root / 2 / 2
- **Volatility**: high (ingest side); the schema is moderate
- **Balanced?**: yes
- **Shared knowledge**: every envelope field is self-reported. `Decode` already enforces `SchemaV1`, SHA and
  digest formats. `Schema` is not used here.

<!-- contract: Envelope, Kind, SchemaV1, KindReport, KindDiscovery — restated from internal/wire/envelope/module.md -->
```go
// SchemaV1 is the only envelope schema version accepted.
const SchemaV1 = "archfit.report-envelope.v1"

// Kind names the payload that accompanies the envelope.
type Kind string

const (
    KindReport    Kind = "report"    // engine state report, POST /v1/reports
    KindDiscovery Kind = "discovery" // discovery draft policy, POST /v1/discoveries
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
    EngineVersion string `json:"engine_version"` // e.g. "v2.3.1"
    ImageDigest   string `json:"image_digest"`   // "sha256:<64 hex>"
    Platform      string `json:"platform"`       // e.g. "linux/amd64"
    PayloadDigest  string `json:"payload_digest"`  // "sha256:<64 hex>" of the exact payload bytes
    BaselineDigest string `json:"baseline_digest"` // bare hex SHA-256 of the baseline file the engine loaded; "" = none
    LabelsDigest   string `json:"labels_digest"`   // bare hex SHA-256 of the labels file the engine loaded; "" = none
}
```

<!-- contract: Decode, PayloadDigest, Schema — restated from internal/wire/envelope/module.md -->
```go
// Decode parses and validates raw against the v1 JSON Schema. Unknown fields,
// a wrong schema_version, malformed SHAs or digests are errors.
func Decode(raw []byte) (Envelope, error)

// PayloadDigest returns "sha256:<hex>" of payload.
func PayloadDigest(payload []byte) string

// Schema returns the published JSON Schema document for SchemaV1.
func Schema() []byte
```

### wire/report

- **Counterpart**: internal/wire/report
- **Direction**: ingest depends on wire/report
- **Strength**: contract (versioned schema, no-source allowlist)
- **LCA / Rank / Distance**: root / 2 / 2
- **Volatility**: high (ingest side); the schema is moderate
- **Balanced?**: yes
- **Shared knowledge**: `MaxBytes` is the upload cap for both kinds. `report.Decode` is the no-source boundary;
  ingest never inspects report content. `Decision` is an opaque part of `Report` for this module.
  `Comparison` is an opaque part of `Report` for this module. `Finding` is an opaque part of `Report` for this module.
  `Dimension` is an opaque part of `Report` for this module. `AgentTask` is an opaque part of `Report` for this module.
  `Coverage` is an opaque part of `Report` for this module. `Seam` is an opaque part of `Report` for this module.
  `Reference` is an opaque part of `Report` for this module.

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

### wire/manifest

- **Counterpart**: internal/wire/manifest
- **Direction**: ingest depends on wire/manifest (type only)
- **Strength**: contract
- **LCA / Rank / Distance**: root / 2 / 2
- **Volatility**: high (ingest side); the manifest is moderate
- **Balanced?**: yes
- **Shared knowledge**: ingest fills `Identity` from the envelope and never calls `Lookup` or `Current`; the
  approval decision belongs to `gate`.

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

### github/oidc

- **Counterpart**: internal/github/oidc
- **Direction**: ingest depends on github/oidc
- **Strength**: contract
- **LCA / Rank / Distance**: root / 2 / 2
- **Volatility**: high (ingest side); OIDC verification is low
- **Balanced?**: yes
- **Shared knowledge**: a verified token authenticates the producer and nothing more. `Claims.SHA` is the
  merge commit for `pull_request` runs, so it is compared only for `push`. Self-hosted runners are accepted
  **[default]**; `RunnerEnvironment` appears only in logs. `ErrUnavailable` is the sentinel of `github/oidc` for a
  signing-key outage; ingest maps it to 503 so the producer retries. The `New` of `github/oidc` is called by the composition root, not here.

<!-- contract: Claims, Verifier, ErrUnavailable — restated from internal/github/oidc/module.md -->
```go
// Claims are the verified GitHub Actions OIDC claims the App uses.
type Claims struct {
    Repository        string // "owner/name"
    RepositoryID      int64
    RunID             int64
    RunAttempt        int
    SHA               string
    Ref               string
    EventName         string
    JobWorkflowRef    string // "owner/name/<path>@<ref>"
    RunnerEnvironment string // "github-hosted" | "self-hosted"
}

// ErrUnavailable means the signing keys could not be fetched; the caller answers 503 so the producer retries.
var ErrUnavailable = errors.New("oidc: signing keys unavailable")

// Verifier validates signature (cached JWKS), issuer, audience, and time claims.
// A verified token authenticates the producer; it never proves the payload is true.
type Verifier interface {
    Verify(ctx context.Context, token, audience string) (Claims, error)
}
```

<!-- contract: New, WorkflowPath — restated from internal/github/oidc/module.md -->
```go
func New(hc *http.Client) Verifier

// WorkflowPath extracts "<path>" from a job_workflow_ref for repository fullName; "" if it points elsewhere.
func WorkflowPath(jobWorkflowRef, fullName string) string
```

### github/api

- **Counterpart**: internal/github/api
- **Direction**: ingest depends on github/api (role interfaces `Repos`, `Pulls`, `Actions`)
- **Strength**: contract
- **LCA / Rank / Distance**: root / 2 / 2
- **Volatility**: high (ingest side); the client is low
- **Balanced?**: yes
- **Shared knowledge**: `ResolveRepo` uses the App JWT and returns the installation that covers the repository.
  `Run` returns the latest attempt of a run. `WorkflowRun.PullRequests` is empty for fork runs.
  `ErrNotFound` and `ErrRateLimited` are the sentinels of `github/api`. Used methods: `Repos.ResolveRepo`,
  `Actions.Run`, `Pulls.Pull`. `Installation` and `Review` are not used here.

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

### Consumers (inbound)

- **internal/gate** (parent): functional, gate / 1 / 1, high, balanced. Constructs the receiver, serves
  `POST /v1/reports` with it, maps `Rejection` to HTTP, re-publishes `Receiver`, `Rejection`, `Upload`,
  `Binding` and `Problem` so `control/onboarding` reuses the same checks for `POST /v1/discoveries`.
- **internal/gate/verdict**: model, gate / 1 / 1, high, balanced. Reads `Problem` values.

## Change Vectors

- A new supported event or a supported fork acquisition path.
- A new envelope version (new decode path beside v1).
- Different PR-association rules when GitHub changes `pull_requests` on runs.
- An approved self-hosted runner group check using `RunnerEnvironment`.
- New rejection codes.

## Constraints and Invariants

- Self-reported envelope fields never establish eligibility. Every bound value comes from verified claims
  or the GitHub API; the envelope is only compared.
- The tenant (`Repo.InstallationID`) comes from `ResolveRepo` on the verified claim; never from the
  envelope, a header, a query parameter, or the URL.
- Before the tenant is authenticated nothing is published and nothing about other tenants is revealed;
  the answer is a status and a code.
- A `Rejection` never becomes a `Problem` and the reverse; `digest_mismatch` and `stale_head` are rejections by design.
- Provenance is necessary, not sufficient: a returned `Upload` is authenticated and bound, not true. No
  code, log or text here says "verified report".
- The body is read once, capped, and held in memory only. Nothing is persisted. Payload bytes, envelope
  JSON, tokens, paths and messages are never logged. Logs (`log/slog` default logger **[default]**):
  repository ID, installation ID, run ID, attempt, digests, rejection code or problem list, runner environment, timings.
- An undecodable report is a `Problem` with `Report == nil`; the decode error text is not propagated
  (it could quote payload content); only the fact is.
- No retries inside `Receive`; the API client owns backoff. The Action retries on 429, 503 and other 5xx.
- Stateless and safe for concurrent use.
- Declared knowledge of GitHub formats: the ref prefix `refs/heads/` (the form of `Claims.Ref` for a branch) and the
  event names `pull_request`, `push` and `workflow_dispatch` (the values of `WorkflowRun.Event` and
  `Envelope.Event`) are literals here. Their owners are `github/oidc` and `github/api`, whose documents state the
  formats and export no constants for them. If GitHub changed a format, the owners' documents and these literals
  change together; until then a drift shows as `ProblemUnsupportedEvent` on every upload, which fails closed.
  Remove a literal when its owner exports a constant.
- Imports: standard library, `wire/envelope`, `wire/report`, `wire/manifest`, `github/oidc`, `github/api`.
  Never `control`, `app`, `store`, LLM or UI packages.

## Test Specification

Fakes: a `Verifier` stub returning fixed claims or an error; in-memory `Repos`, `Pulls`, `Actions`;
requests built with `httptest.NewRequest`. No network.

### Unit Tests

- **Test name**: `TestRejectionTable`
  **Scenario**: one row per rejecting step: `GET`; body of `MaxBytes + 1`; no envelope header; envelope with an unknown field; envelope kind `discovery` on the report route; no bearer; verifier error (bad signature, wrong audience, expired); verifier returns `oidc.ErrUnavailable` (503 `oidc_unavailable`); claim repository differs from envelope; App not installed; repository ID differs; run not found; claim event differs from run; push claim SHA differs; claim attempt 1 while the run is at attempt 2; PR head moved; payload digest differs; GitHub rate limit at each API call.
  **Expected behavior**: the status and code of the step table; zero `Upload`; `errors.As` finds `*Rejection`; later fakes were not called.
- **Test name**: `TestCheckOrder`
  **Scenario**: requests that violate two steps at once (oversized and unauthenticated; bad envelope and bad token; stale head and digest mismatch).
  **Expected behavior**: the earlier step's rejection wins.
- **Test name**: `TestPullRequestResolution`
  **Scenario**: table: one associated PR with matching head; one with a moved head; two PRs, one matching; two PRs, both matching; two PRs, none matching; empty list with a valid envelope hint; empty list with hint whose head differs; empty list and no hint; eleven candidates; a closed PR; an empty list with a hint GitHub does not know (`api.ErrNotFound`).
  **Expected behavior**: bound; 409; bound to the match; `envelope_mismatch`; `envelope_mismatch`; bound; 409; `envelope_mismatch`; `envelope_mismatch`; `envelope_mismatch`; `envelope_mismatch` with `PullRequest` 0 (no association, not an error).
- **Test name**: `TestEnvelopeComparison`
  **Scenario**: flip one envelope field at a time: event, PR number, head, base, merge-base, run ID, attempt; then change only `baseline_digest` and `labels_digest`.
  **Expected behavior**: `Upload` returned with `ProblemEnvelopeMismatch`; binding still holds the authoritative values. The two claimed digests cause no problem here; they are passed on for `gate/verdict`.
- **Test name**: `TestWorkflowPathBinding`
  **Scenario**: `job_workflow_ref` in the same repository; in another repository (reusable workflow).
  **Expected behavior**: the path; `""`.

### Integration Contract Tests

- **Test name**: `TestReceivePullRequestReport`
  **Scenario**: the healthy fixture with matching claims, run, pull and envelope.
  **Expected behavior**: no error, no problems; `Binding` fully filled from GitHub; `Identity`, `BaselineDigest` and `LabelsDigest` copied from the envelope (also when they are `""`); `Digest` equals `envelope.PayloadDigest(body)`; `Report` decoded.
- **Test name**: `TestReceiveDefaultBranchPush`
  **Scenario**: `push` run on the default branch.
  **Expected behavior**: `PullRequest` 0, `BaseRef` = default branch, no `Pulls` call, no problems. A push to `feature/x` gives `BaseRef` `feature/x` plus `ProblemUnsupportedEvent`; an unbound `pull_request` run (`refs/pull/7/merge`) gives the default branch.
- **Test name**: `TestReceiveDiscovery`
  **Scenario**: kind `discovery` from a `workflow_dispatch` run with a YAML payload; the same from a `pull_request` run.
  **Expected behavior**: `Report` nil, `Payload` equals the body, `Binding.Event` `workflow_dispatch`; the second carries `ProblemUnsupportedEvent`. `report.Decode` was not called.
- **Test name**: `TestProblemsNeverReject`
  **Scenario**: `workflow_run` upload; fork PR; invalid report bytes with a correct digest; all three at once.
  **Expected behavior**: nil error each time; problems de-duplicated in declaration order; `Report` nil only for the invalid bytes.

### Boundary Tests

- **Test name**: `TestBodyIsCapped`
  **Scenario**: a body of exactly `MaxBytes`, one byte more, an endless reader, and a reader that fails with another error.
  **Expected behavior**: accepted; 413; 413 after reading at most `MaxBytes + 1` bytes; a plain error that wraps the read error and is not a `*Rejection`.
- **Test name**: `TestClientSuppliedTenantIsIgnored`
  **Scenario**: another installation's ID in headers, query string and extra envelope-like headers; envelope repository of another tenant with a valid token for tenant A.
  **Expected behavior**: the first is bound to the claim's tenant; the second is 403 `repository_mismatch`; no API call addressed the other tenant.
- **Test name**: `TestLeakageFixturesBecomeProblems` (T04, T09)
  **Scenario**: seeded leakage fixtures (snippet field, log blob, raw model output, multi-line message) with correct digests.
  **Expected behavior**: `ProblemReportInvalid`, `Report` nil; no seeded string in any log line or error text.
- **Test name**: `TestNothingSensitiveIsLogged`
  **Scenario**: capture logs over all tests above.
  **Expected behavior**: no token, envelope JSON, payload byte, path or message; only IDs, digests, codes, timings.

### Behavior Tests

- **Test name**: `TestRevisionBindingCases` (T03)
  **Scenario**: PR; default-branch push; `workflow_run`; multi-PR ambiguous association; re-push before upload; stale result; wrong tenant; mismatched report digest; duplicate attempt (same upload twice); older attempt after a re-run; run ID of a cancelled or missing run.
  **Expected behavior**: bound; bound; `unsupported_event`; `envelope_mismatch`; 409 `stale_head`; 409 `stale_head`; 403; 400 `digest_mismatch`; identical `Upload` both times (idempotency is feedback's job); 409 `stale_attempt`; bound for a cancelled run that did upload, 403 `run_not_found` for a missing one.
- **Test name**: `TestEnvelopeAloneEstablishesNothing` (T03, T09)
  **Scenario**: a perfectly self-consistent envelope and report with a token for a different run of the same repository.
  **Expected behavior**: `ProblemEnvelopeMismatch`; the binding describes the token's run, not the envelope's.
- **Test name**: `TestModifiedWorkflowAndWrongImageAreBoundNotJudged` (T03)
  **Scenario**: upload from a workflow file other than the approved one; upload claiming an unknown image digest.
  **Expected behavior**: no rejection; `WorkflowPath` and `Identity` carry the facts; the decision is left to `gate` and `verdict`.
- **Test name**: `TestApprovedProducerIsAccepted` (T09)
  **Scenario**: the valid approved-producer fixture end to end through `Receive`.
  **Expected behavior**: an `Upload` without problems; replaces the skeleton's refuse-everything test together with `TestRejectionTable`.

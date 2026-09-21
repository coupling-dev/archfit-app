# GitHub API

**Path**: internal/github/api/
**Parent**: internal/github/ (GitHub integration, publishes its children)
**Submodules**: none (leaf)

## Purpose

`api` is the App's only GitHub REST client. It offers small role interfaces (repositories, pull requests,
checks, comments, Actions, changes, users) over one `Client`, and hides URL shapes, headers, pagination,
conditional requests and rate-limit handling. Without it, every gate and control module would speak HTTP to
GitHub, and "never a false green on rate-limit exhaustion" would depend on each caller.

## Functional Responsibilities

- Authenticate each call with the right credential: App JWT, installation token, or a user's OAuth token.
- Read installations, repositories, files at a ref, pull requests, changed files, reviews, team membership.
- Create and update check runs; list, create and update the App's own PR comments.
- Read workflow runs, re-run and dispatch workflows, download one artifact file under a size cap.
- Write branches, commits and pull requests idempotently (`EnsureBranch`, `EnsurePull`).
- Follow pagination to a cap and fail, not truncate, beyond it.
- Use conditional requests; back off within a bound on rate limits; then return `ErrRateLimited`.
- Map "gone or no access" to `ErrNotFound`.

Not here: any decision about what the data means (approval, staleness, fork policy, which check to publish),
token minting (`github/auth`), webhooks, OIDC.

## Subdomain Classification

Generic. A REST client for a documented API. Functional volatility is low; it grows by a method when a consumer
needs one. It is a wide leaf on purpose: role interfaces keep each consumer's knowledge small.

## Encapsulated Knowledge

### Transport

- Base URL `https://api.github.com`, fixed; tests replace the transport of the `*http.Client` **[default]**.
  Headers: `Accept: application/vnd.github+json`, `X-GitHub-Api-Version: 2022-11-28` (pinned),
  `User-Agent: archfit-app`. A `nil` client means a client with a 10 s timeout **[default]**.
- Credential per call: App JWT for `ListInstallations`, `ResolveRepo` (installation lookup) and `GET /app`;
  the installation token of `RepoRef.InstallationID` for every `RepoRef` method; the caller's token for `Users`.
- `GET /app` is called once and kept for the process: App ID (check-run filter) and bot login `<slug>[bot]`.
- Input guards before any request: `InstallationID` > 0; `FullName` matches `^[A-Za-z0-9_.-]+/[A-Za-z0-9_.-]+$`;
  refs, branches and paths are escaped per path segment, and an empty, `.` or `..` segment (also as owner or
  name) is an error before any request. No caller string can change the route.
- Redirects are never followed automatically; a `3xx` is an error. `Artifact` follows its one redirect itself.
- Response JSON is decoded leniently (unknown fields ignored); each body is read through a 10 MiB limit **[default]**.
- Unexported `now` and `sleep` hooks exist for same-package tests **[default]**.

### Status mapping

- `404`, `410` → `ErrNotFound`. GitHub answers `404` for a repository removed from the installation, an
  installation that lost access, a deleted branch, file, run or comment. All of these are `ErrNotFound`.
- Rate limit: `429`, or `403` with `x-ratelimit-remaining: 0` or a `Retry-After` header (secondary limit).
  `x-ratelimit-reset` is used for the wait only when `x-ratelimit-remaining` is `0`.
- `401` on a call made with an installation token: call `TokenSource.Invalidate(installationID)`, fetch a fresh
  token, and repeat the request exactly once, for any method (a `401` request did not execute). A second `401`
  is an error. `401` on an App JWT or user-token call is an error at once.
- Other `403` (missing App permission), `409`, `422` → error with method, route template, status and
  `x-github-request-id`. Never the URL's variable parts, the body, or a token.
- A `TokenSource` error is returned wrapped and treated as opaque.

### Backoff and retry

- Rate-limited responses are retried for every method (the request did not execute). Wait = `Retry-After`, else
  until `x-ratelimit-reset`, else 1 s doubling **[default]**. Total waiting per call is at most 30 s **[default]**. If the
  needed wait exceeds what is left, return `ErrRateLimited` at once without sleeping.
- `502`, `503`, `504` and transport errors are retried for `GET` only, after 1 s, 2 s, 4 s **[default]**, inside the same 30 s
  budget **[default]**. Writes are never blindly repeated; `Ensure*` semantics and the sweep make the caller's retry safe.
- `ctx` cancellation ends any wait immediately with `ctx.Err()`.

### Conditional requests

- In-memory LRU: 1 000 entries and 64 MiB of bodies at most **[default]**; bodies over 1 MiB are not cached.
  Key = credential principal (`app` or `inst:<id>`) plus full URL. `Users` calls are not cached.
- A cached `GET` is sent with `If-None-Match`. `304` → the cached body is used; GitHub does not count it against
  the primary rate limit. Any other status replaces or removes the entry.
- A cached body is only ever used after GitHub answered `304` to the current credential. Revoked access
  answers `404` or `401`, so the cache can never outlive access, and never crosses installations.

### Pagination

`per_page=100`, follow `Link: rel="next"`. Cap: 1 000 items per call **[default]**. Beyond the cap the call
returns an error, never a silent partial list. This holds for `ListInstallations`, `ListRepos`, `UserRepos`,
`OpenPulls`, `Reviews`, `AppComments` and `CheckRuns`. The one exception is `ChangedFiles`, whose contract has
a `truncated` result: it returns the paths read so far with `truncated` true.

### Endpoints

- `ListInstallations`: `GET /app/installations`; suspended installations are skipped **[default]**.
- `ListRepos`: `GET /installation/repositories`.
- `ResolveRepo`: `GET /repos/{r}/installation` (JWT) then `GET /repos/{r}` with that installation's token.
- `File`: `GET /repos/{r}/contents/{path}?ref=`; `type` must be `file`; base64 body; over 1 MiB → error
  **[default]** (GitHub's contents API returns no inline content above that size); a directory, symlink or
  submodule → error. Consequence for an oversized settings, policy or baseline file: the caller fails closed,
  the gate check stays pending and ends `action_required` through the sweep; it is never green.
- `Pull`: `GET /repos/{r}/pulls/{n}`, then `GET /repos/{r}/compare/{base_sha}...{head_sha}` for
  `merge_base_commit.sha` (sent with `per_page=1` to keep the commit list out of the answer). Only `Pull` fills `MergeBaseSHA`; `OpenPulls`, `PullByHead` and `EnsurePull` leave it `""` **[default]**.
- `OpenPulls`: `GET /repos/{r}/pulls?state=open`, drafts included.
- `PullByHead`: `GET /repos/{r}/pulls?state=all&head={owner}:{branch}&sort=created&direction=desc&per_page=1`,
  `{owner}` being the owner of `r`, so fork heads never match; first hit; none → `ErrNotFound`. A hit whose
  `head.repo.full_name` is not `r` or whose `head.ref` is not the branch is dropped (also in `EnsurePull`).
- Pull mapping, everywhere: `Merged` = `merged_at` is set **[default: list responses carry no `merged` flag]**;
  `Body` = `body` (`null` → `""`); `URL` = `html_url`; `State` as GitHub gives it (a merged pull is `closed`).
- `BranchHead`: `GET /repos/{r}/git/ref/heads/{branch}` → `object.sha`; `404` → `ErrNotFound`.
- `ChangedFiles`: `GET /repos/{r}/pulls/{n}/files`; returns `filename` and, for renames, `previous_filename` too,
  deduplicated: renaming a protected file away is a change to it. `truncated` is true when the pagination cap
  was hit, or when the pull's `changed_files` count (one extra `GET /repos/{r}/pulls/{n}`) exceeds the number of
  file entries read: GitHub silently stops this list at 3 000 files. An error still returns no paths.
- `Reviews`: `GET /repos/{r}/pulls/{n}/reviews`, ascending by submission as GitHub returns them; `PENDING`
  reviews are dropped; `CommitSHA` = `commit_id`.
- `TeamMember`: `GET /orgs/{org}/teams/{slug}/memberships/{login}`; `state: active` → true; `pending` or `404` →
  false with `nil` error; a team string without exactly one `/` → error.
- `CheckRuns`: `GET /repos/{r}/commits/{sha}/check-runs?check_name=&app_id=<own>&filter=latest`: only this
  App's check runs, so another app's check of the same name is never seen (a run whose `app.id` is not the
  App's is dropped as well); latest per name is GitHub's `filter=latest`; annotations are not read back.
- `CreateCheckRun`: `POST /repos/{r}/check-runs`; `UpdateCheckRun`: `PATCH /repos/{r}/check-runs/{id}`. An
  annotation maps to `start_line = end_line = Line`, and `Line` 0 to line 1. GitHub appends annotations on update.
- `AppComments`: `GET /repos/{r}/issues/{n}/comments`, kept when `user.login` is the bot login.
  `CreateComment`: `POST` there; `UpdateComment`: `PATCH /repos/{r}/issues/comments/{id}`.
- `Run`: `GET /repos/{r}/actions/runs/{id}`; `WorkflowPath` = `path` unchanged; `PullRequests` =
  `pull_requests[].number` (GitHub leaves it empty for fork PRs). `Rerun`: `POST .../runs/{id}/rerun`.
- `Dispatch`: `POST /repos/{r}/actions/workflows/{file name}/dispatches` with `ref` and `inputs`.
- `LatestRun`: `GET .../workflows/{file name}/runs?branch=&per_page=1`; none → `ErrNotFound`.
- `Artifact`: `GET .../runs/{id}/artifacts?name=` (newest unexpired; none → `ErrNotFound`), then
  `GET .../artifacts/{id}/zip`, which redirects to blob storage; the `Authorization` header is not forwarded
  across hosts. The zip must hold exactly one regular file; its declared size and the bytes actually read
  (through a limited reader) must both be ≤ `maxBytes`, else error. The zip itself is read through a limit of
  `maxBytes` + 1 MiB **[default]**; any second entry, even a directory, is an error.
- `EnsureBranch`: `GET .../git/ref/heads/{branch}`; if missing, `POST .../git/refs` at the SHA of `fromRef`
  (a 40-hex commit SHA is used as is, anything else is a branch name **[default]**); a `422` "already exists"
  race is success: after any `422` the ref is read again, and the `422` stands if it is still missing.
- `Commit`: Git Data API: blobs → tree with `base_tree` → commit with the branch head as parent → `PATCH` the ref
  with `force: false`. No files → error. A tree equal to the parent's → the current head SHA, no new commit
  **[default]**. A non-fast-forward is an error; the caller reloads.
- `EnsurePull`: `GET .../pulls?state=open&head={owner}:{Head}&base={Base}`; first hit wins; else `POST .../pulls`;
  a `422` "already exists" race → list again; the `422` stands if the list is still empty.
- `UpdatePullBody`: `PATCH .../pulls/{n}`. `MarkReady`: REST cannot leave draft state, so one GraphQL mutation
  `markPullRequestReadyForReview` on the pull's `node_id`; an already ready pull is success.
- `Permission`: `GET /repos/{fullName}` with the user token; `permissions.admin` → `admin`, `.push` → `write`,
  `.pull` → `read`; `404` → `none` with `nil` error.
- `UserRepos`: `GET /user/installations`, then `GET /user/installations/{id}/repositories` for each; the cap
  applies to the total; each `Repo` carries that installation's ID.

## Public Contract

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

```go
type Comment struct {
    ID   int64
    Body string
}

// Comments are PR conversation comments authored by the App.
type Comments interface {
    AppComments(ctx context.Context, r RepoRef, pull int) ([]Comment, error)
    CreateComment(ctx context.Context, r RepoRef, pull int, body string) (Comment, error)
    UpdateComment(ctx context.Context, r RepoRef, id int64, body string) error
}
```

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

```go
// Users answers questions with a user's OAuth token; nothing here uses App credentials.
type Users interface {
    // Permission returns "admin" | "write" | "read" | "none" for the user on the repository.
    Permission(ctx context.Context, userToken, fullName string) (string, error)
    // UserRepos lists repositories the user can access through App installations.
    UserRepos(ctx context.Context, userToken string) ([]Repo, error)
}
```

```go
// Client implements every role interface. Consumers depend on the role interfaces they need.
type Client struct{ /* unexported */ }

func New(ts TokenSource, hc *http.Client) *Client
```

`ErrNotFound` is the sentinel for "absent, or not accessible to this credential". `ErrRateLimited` is the
sentinel returned when the 30 s **[default]** backoff budget cannot cover the wait GitHub asks for. Both are tested with
`errors.Is`. `TokenSource` is restated under Integrations, from `github/auth`. `OAuth` is the user login flow
of `github/auth`; `api` only receives its result, a user token string, in the `Users` methods.

### Caller contract

- `CreateCheckRun`, `UpdateCheckRun`: `Summary` over 65 535 bytes or more than 50 annotations (GitHub's limits), `completed` without
  `Conclusion`, `in_progress` with one, or `UpdateCheckRun` with `ID` 0 → error before any request. `api`
  never truncates.
- `CreateComment`, `UpdateComment`: a body over 65 536 characters (GitHub's limit) → error before any request.
- `Artifact`: `maxBytes` ≤ 0 → error.
- Lists return an empty slice, not `ErrNotFound`, when the parent exists and has no items.
- `RepoRef` identity is trusted by the caller's construction; `api` does not re-derive the installation.
  A `RepoRef` that pairs a repository with the wrong installation gets GitHub's `404`, so `ErrNotFound`.
- Every method is safe for concurrent use and honors `ctx`.

## Integrations

### github/auth

- **Counterpart**: internal/github/auth/
- **Direction**: `api` depends on `auth`
- **Strength**: Contract
- **LCA / Rank / Distance**: github / 1 / 1
- **Volatility**: Low
- **Balanced?**: Yes
- **Shared knowledge**: `api` calls `AppJWT`, `InstallationToken`, and `Invalidate` after a `401` (then one retry).
  It treats their errors as opaque and never calls `RepoReadToken`.
  `Credentials` is part of the restated fence only; `api` never sees one.

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

### gate children

- **Counterpart**: internal/gate/ingest/, internal/gate/inputs/, internal/gate/feedback/, internal/gate/sweep/
- **Direction**: each depends on `api`
- **Strength**: Contract
- **LCA / Rank / Distance**: root / 2 / 2
- **Volatility**: High (the gate side)
- **Balanced?**: Yes
- **Shared knowledge**: role interfaces only. `ingest`: `Repos`, `Pulls`, `Actions`. `inputs`: `Repos`, `Pulls`.
  `feedback`: `Checks`, `Comments`, `Pulls`. `sweep`: `Repos`, `Pulls`, `Actions`. They rely on: `ErrRateLimited`
  leaves checks pending for the sweep; lists are complete or an error; `Reviews` order; `ChangedFiles` includes
  rename sources and `truncated` means "assume any path changed"; `CheckRuns` holds this App's runs only;
  GitHub appends annotations.

### gate and control façades, app

- **Counterpart**: internal/gate/, internal/control/, internal/app/
- **Direction**: each depends on `api` (`*Client` in `Deps`; `app` calls `New`)
- **Strength**: Contract
- **LCA / Rank / Distance**: root / 2 / 2
- **Volatility**: High (`gate`), Moderate (`control`), Low (`app`)
- **Balanced?**: Yes
- **Shared knowledge**: `Client`, `New`. Façades hand the client to children as role interfaces. Used directly:
  `gate`: `Actions` (`Rerun` on a review change); `control`: `Repos` (`ListInstallations`, `ListRepos`); `app`: `New` only.

### control children

- **Counterpart**: internal/control/proposal/, internal/control/onboarding/, internal/control/assist/,
  internal/control/ui/, internal/control/ui/session/
- **Direction**: each depends on `api`
- **Strength**: Contract
- **LCA / Rank / Distance**: root / 2 / 2; `control/ui/session`: root / 3 / 3
- **Volatility**: Low (`proposal`, `session`), High (`onboarding`, `assist`, `ui`)
- **Balanced?**: Yes
- **Shared knowledge**: the role interfaces each one restates in its own `module.md`:
  `proposal`: `Changes`, `Pulls` (`PullByHead` for a branch's draft in any state).
  `onboarding`: `Repos`, `Pulls` (`PullByHead`), `Checks` (`CheckRuns` for the report-received stage), `Actions`, `Changes`.
  `assist`: `Repos`, `Actions`, `Changes`.
  `ui`: `Users`, `Repos`, `Checks`, `Actions`, `Changes`.
  `session`: `Users`, `Repos`.

### GitHub REST API (external)

- **Counterpart**: `https://api.github.com`, REST version `2022-11-28`, plus one GraphQL mutation
- **Direction**: `api` depends on GitHub
- **Strength**: Contract (public, versioned API)
- **LCA / Rank / Distance**: outside the tree; the highest distance the system has
- **Volatility**: Low
- **Balanced?**: Yes: contract strength tolerates the distance
- **Shared knowledge**: the Endpoints list; rate-limit and ETag semantics above. App permissions needed:
  `checks: write`, `pull_requests: write`, `contents: write`, `actions: write`, `metadata: read`,
  `members: read` (`TeamMember`), and `workflows: write`, which GitHub demands for any commit that touches
  `.github/workflows/` (the onboarding PR).

## Change Vectors

- A consumer needs another endpoint or field: one method or field on a role interface.
- GitHub retires the pinned API version.
- Different caps: pagination, cache size, backoff budget, file size.
- GitHub Enterprise Server: a base URL on the constructor.

## Constraints and Invariants

- No domain decision lives here. `api` reports what GitHub says; it never interprets approval, staleness,
  forks, or gate state.
- Rate-limit exhaustion is always `ErrRateLimited`, never an empty or partial result. Consumers leave checks
  pending and the sweep retries: never a false green.
- Lists are complete or an error. `ChangedFiles` alone may be partial, and then says so with `truncated`.
- Access is re-proved on every call: the cache is used only after a `304` from GitHub for the same credential.
  Revoked access, a removed repository and a cross-installation `RepoRef` all end as `ErrNotFound` or an error,
  never as data (T07).
- User-token calls never use App credentials; App calls never use a user token.
- Writes are never force-pushes; `api` has no method that deletes a branch, merges, or closes a pull request.
- Wire contracts stay separate: `api` imports no `wire/*` package; its structs are not serialized by anyone.
- No logging. Errors hold method, route template, status and request ID: no tokens, bodies, paths, or messages.
- Standard library and `github/auth` only. No GitHub SDK.

## Test Specification

GitHub is a local `httptest` server with scripted responses, reached through a fake `http.RoundTripper` that
routes every host to it; the clock and sleep are the unexported hooks; `TokenSource` is a stub. No test touches
the network.

### Unit Tests

- **Test name**: `TestStatusMapping`
  **Scenario**: table: `404`; `410`; `401`; `403` permission; `403` with `x-ratelimit-remaining: 0`; `403` with
  `Retry-After`; `429`; `422`; `500` on `POST`.
  **Expected behavior**: `ErrNotFound` ×2; error; error; rate-limit path ×3; error; error without a retry.
- **Test name**: `TestUnauthorizedInvalidatesAndRetriesOnce`
  **Scenario**: table with a recording `TokenSource` stub: `401` then `200` on `GET`; `401` then `200` on `PATCH`;
  `401` twice; `401` on an App JWT route; `401` on a `Users` call.
  **Expected behavior**: `Invalidate(id)` called once, second request carries the new token, success (rows 1–2);
  error after exactly two requests; error after one request and no `Invalidate` (rows 4–5).
- **Test name**: `TestBackoffBudget`
  **Scenario**: table: `Retry-After: 2` then `200`; reset in 10 s then `200`; reset in 31 s; three waits of 12 s;
  `ctx` cancelled mid-wait; `503` ×3 then `200` on `GET`; `503` on `PATCH`.
  **Expected behavior**: ok after 2 s; ok after 10 s; `ErrRateLimited` with no sleep; `ErrRateLimited` before the
  third wait; `ctx.Err()`; ok after 1 + 2 + 4 s; error at once.
- **Test name**: `TestPaginationFollowsLinkToCap`
  **Scenario**: 3 pages of 100; 10 pages of 100; 11 pages; a `next` link to another host.
  **Expected behavior**: 300 items; 1 000 items; error, no partial list; error.
- **Test name**: `TestETagCache`
  **Scenario**: `GET` twice with `304` on the second; entry 1 001 evicts the oldest; a 2 MiB body; same URL under
  `inst:1` and `inst:2`; a `404` after a cached `200`.
  **Expected behavior**: second call sends `If-None-Match` and returns the cached body; evicted entry refetches;
  large body not cached; no entry is shared between principals; the entry is removed and `ErrNotFound` returned.
- **Test name**: `TestInputGuards`
  **Scenario**: `FullName` values `a/b/c`, `../x`, `a`, `a/b?x=1`, empty; `InstallationID` 0; paths `a/../b`
  and `a//b`; branch `feat/x y`; path `dir/a b/c?d#e%f.yaml`.
  **Expected behavior**: error without a request for the bad names, the ID and the dot or empty segments; the
  branch and the last path are escaped per segment.
- **Test name**: `TestZipExtraction`
  **Scenario**: one file under the cap; one file over; declared size small but content large; two files; a
  directory entry only; not a zip.
  **Expected behavior**: bytes; error; error; error; error; error.

### Integration Contract Tests

One test per role interface; each asserts method, route, credential, request body and the mapped result.

- **Test name**: `TestReposContract`
  **Scenario**: `ListInstallations` (one suspended); `ListRepos`; `ResolveRepo`; `File` for a file, a missing file,
  a directory, a 2 MiB file; `BranchHead` for a branch and a missing branch.
  **Expected behavior**: JWT for App routes, installation token elsewhere; suspended skipped; `Repo` fully
  mapped with the installation ID; file bytes and blob SHA; `ErrNotFound`; error; error; the ref's commit SHA; `ErrNotFound`.
- **Test name**: `TestPullsContract`
  **Scenario**: `Pull` (same-repo, fork, merged); `OpenPulls` with a draft; `PullByHead` with an open, a merged-only,
  a fork-only and no pull for the branch; `ChangedFiles` with a rename; `Reviews` with a `PENDING` one and two by
  one reviewer; `TeamMember` active, pending, `404`, bad team string.
  **Expected behavior**: `MergeBaseSHA` from compare, `Body` and `URL` mapped, `Merged` true with `State` `closed`;
  `HeadRepo` differs for the fork; draft listed; newest pull, the merged pull, `ErrNotFound`, `ErrNotFound`; both
  rename names present and `truncated` false; `PENDING` dropped, order kept; true, false, false, error.
- **Test name**: `TestChecksContract`
  **Scenario**: create `in_progress`; update to `completed` with 50 annotations (one with `Line` 0); list by name.
  **Expected behavior**: request bodies match; line 0 sent as 1; only this App's runs listed; `ID`, `ExternalID`,
  `Summary`, `StartedAt` mapped.
- **Test name**: `TestCommentsContract`
  **Scenario**: a thread with user comments, another bot's comments and two of the App's; create; update.
  **Expected behavior**: only the App's two returned; `GET /app` called once across calls.
- **Test name**: `TestActionsContract`
  **Scenario**: `Run`; `Rerun`; `Dispatch` with inputs; `LatestRun` with and without runs; `Artifact` through a
  redirect to another host.
  **Expected behavior**: fields mapped, `WorkflowPath` unchanged; `201`/`204` are success; `ErrNotFound` when no
  run; the redirected request carries no `Authorization` header; file bytes returned.
- **Test name**: `TestChangesContract`
  **Scenario**: `EnsureBranch` new, existing, and lost race; `Commit` of two files; `Commit` with identical
  content; non-fast-forward; `EnsurePull` new, existing, lost race; `UpdatePullBody`; `MarkReady` draft and ready.
  **Expected behavior**: one ref created or none; one commit with both files and `force: false`; head SHA and no
  commit; error; exactly one pull in every case; GraphQL mutation sent once, none for the ready pull.
- **Test name**: `TestUsersContract`
  **Scenario**: `Permission` for admin, push-only, pull-only, `404`, `401`; `UserRepos` over two installations.
  **Expected behavior**: `admin`, `write`, `read`, `none`, error; repos carry their installation ID; only the
  user token is ever sent.

### Boundary Tests

- **Test name**: `TestCheckRunCallerContract`
  **Scenario**: table: summary of 65 535 and 65 536 bytes; 50 and 51 annotations; `completed` without conclusion;
  `in_progress` with one; update with `ID` 0.
  **Expected behavior**: ok, error; ok, error; error; error; error. No request for an error row; nothing truncated.
- **Test name**: `TestRevokedAccessAndRemovedRepository` (T07)
  **Scenario**: a repository read and cached; then GitHub answers `404` (removed from the installation); then the
  token mint fails (uninstalled).
  **Expected behavior**: `ErrNotFound`, then the wrapped mint error; the cached body is never returned.
- **Test name**: `TestCrossInstallationRepoRef` (T07)
  **Scenario**: `RepoRef{InstallationID: A, FullName: repository of B}` on every role interface.
  **Expected behavior**: A's token is used, GitHub's `404` becomes `ErrNotFound`; B's token is never requested.
- **Test name**: `TestErrorsCarryNoSecretsOrContent`
  **Scenario**: every error path with marker strings in tokens, file paths, bodies and response bodies.
  **Expected behavior**: no marker in any error text.
- **Test name**: `TestAPIImportsOnlyStdlibAndAuth`
  **Scenario**: `go list -deps ./internal/github/api`.
  **Expected behavior**: the only internal import is `internal/github/auth`; no third-party module.

### Behavior Tests

- **Test name**: `TestRateLimitExhaustionNeverLooksLikeData` (T11, decisions §8)
  **Scenario**: every list and read method while GitHub answers rate-limited with a reset 10 minutes away,
  including on page 3 of a paginated list.
  **Expected behavior**: `ErrRateLimited` within the budget; never an empty or partial result.
- **Test name**: `TestLargePullRequestIsCompleteOrSaysTruncated` (T11 pagination)
  **Scenario**: 950 changed files over 10 pages with the protected path on the last page; 1 001 files; a pull whose
  `changed_files` is 3 500 while the list ends at 3 000 or at the cap; `OpenPulls` with 1 001 pulls.
  **Expected behavior**: the protected path is present, `truncated` false; `truncated` true with the paths read so
  far; `truncated` true; an error and no list.
- **Test name**: `TestRetriedWritesStayIdempotent` (T11 retry)
  **Scenario**: run `EnsureBranch` → `Commit` → `EnsurePull` twice with the same inputs.
  **Expected behavior**: one branch, one commit, one pull request.
- **Test name**: `TestSweepStaysCheapWithConditionalRequests`
  **Scenario**: list open pulls and check runs for 50 repositories twice with nothing changed.
  **Expected behavior**: the second pass is all `304`; results equal the first pass.
- **Test name**: `TestUserSeesOnlyTheirRepositories` (T07 user authorization)
  **Scenario**: two users with different installations; one loses access between calls.
  **Expected behavior**: each list holds only that user's repositories; after the loss `Permission` is `none`;
  nothing is served from a cache.

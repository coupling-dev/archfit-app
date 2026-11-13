# GitHub API

**Path**: internal/github/api/
**Parent**: internal/github/ (GitHub integration, publishes its children)
**Submodules**: none (leaf)

## Purpose

`api` is the App's only GitHub REST client. It offers small role interfaces (repositories, pull requests,
merge queues, checks, comments, issues, issue triage, Actions, workflow runs, changes, merges, commits, users) over one `Client`, and hides URL shapes, headers, pagination,
conditional requests and rate-limit handling. Without it, every gate and control module would speak HTTP to
GitHub, and "never a false green on rate-limit exhaustion" would depend on each caller.

## Functional Responsibilities

- Authenticate each call with the right credential: App JWT, installation token, or a user's OAuth token.
- Read installations, repositories, files at a ref, pull requests and changed files.
- Read a branch's merge queue: the entries and their merge group commits (`MergeQueue`, GraphQL).
- Create and update check runs, only `in_progress` or `completed` with `success`, `failure`, `neutral` or
  `action_required`; list, create and update the App's own PR comments.
- List, open, edit, close and reopen the App's own issues; set the assignees and labels of any issue; never a pull
  request through the issues endpoints.
- Read workflow runs, also every run of a workflow for one head commit with its creation time; re-run and dispatch
  workflows, download one artifact file under a size cap.
- Write branches, commits and pull requests idempotently (`EnsureBranch`, `EnsurePull`), and one commit on a parent
  the caller names, the branch moved to it with or without force (`CommitOn`, the App's `archfit/policy` branch).
- Squash-merge a pull request and update its branch, each only at the head SHA the caller expects.
- Compare two commits: the changed paths and how the head relates to the base.
- Follow pagination to a cap and fail, not truncate, beyond it.
- Use conditional requests; back off within a bound on rate limits; then return `ErrRateLimited`.
- Map "gone or no access" to `ErrNotFound`.
- Prove on request that the App key works with GitHub (`CheckApp`), for the readiness check.

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
  `CheckApp` is the exception: it sends `GET /app` with a fresh App JWT on every call and never uses the kept
  identity, so each call proves the key again. It returns `nil` for a `2xx` answer with a positive `id`, and the
  usual errors otherwise. The ETag cache applies: a `304` is also an answer to an authenticated request.
- Input guards before any request: `InstallationID` > 0; `FullName` matches `^[A-Za-z0-9_.-]+/[A-Za-z0-9_.-]+$`;
  refs, branches and paths are escaped per path segment, and an empty, `.` or `..` segment (also as owner or
  name) is an error before any request. No caller string can change the route.
- Redirects are never followed automatically; a `3xx` is an error. `Artifact` and `ArtifactByID` follow their one
  redirect themselves.
- Response JSON is decoded leniently (unknown fields ignored); each body is read through a 10 MiB limit **[default]**.
- Unexported `now` and `sleep` hooks exist for same-package tests **[default]**.

### Status mapping

- `404`, `410` → `ErrNotFound`. GitHub answers `404` for a repository removed from the installation, an
  installation that lost access, a deleted branch, file, run or comment. All of these are `ErrNotFound`.
- Rate limit: `429`, or `403` with `x-ratelimit-remaining: 0` or a `Retry-After` header (secondary limit).
  `x-ratelimit-reset` is used for the wait only when `x-ratelimit-remaining` is `0`.
- `401` on a call made with an installation token: call `TokenSource.Invalidate(installationID)`, fetch a fresh
  token, and repeat the request exactly once, for any method (a `401` request did not execute). A second `401`
  is an error. `401` on an App JWT is an error at once.
- `401` on a call made with a user token → `ErrUserTokenRejected` at once, no retry: GitHub user tokens expire after
  8 hours or can be revoked, and a repeat would send the same token. Only `Users` calls and pages of their lists can
  return it. The caller ends the session and asks for a new sign-in.
- On `MergePull` and `UpdateBranch`: `405` → `ErrNotMergeable`, `409` → `ErrHeadChanged`, `422` →
  `ErrUnprocessable`; on `SetAssigneesAndLabels` and on `CreateIssue` with an assignee: `422` → `ErrUnprocessable`. Each
  wraps the status error, so `Status` still gives the code. The `405` answers measured on the test repository
  (design D.2): a required check absent, `in_progress`, `queued`, `action_required` or set by another app, and a
  required review missing.
- Other `403` (missing App permission), `409`, `422` → error with method, route template, status and
  `x-github-request-id`. Never the URL's variable parts, the body, or a token.
- A `TokenSource` error is returned wrapped and treated as opaque.
- `Status(err)` returns the HTTP status of an answer that maps to no sentinel, else `0`; callers log it to tell
  `403` from `500`. It exposes nothing else of the error.
- `Permanent` classifies a returned error for a caller that can repeat the work later: `ErrNotFound` and a final
  `4xx` (`400`, `422`) are permanent. `401` (the App credential can change), `ErrUserTokenRejected` (the user must
  sign in again; no queue repeats a user call), `403` (a secondary rate limit can
  arrive without its headers, so a missing permission is retried too), `408`, `409` (a conflict can clear) and
  every rate limit are not.

### Backoff and retry

- Rate-limited responses are retried for every method (the request did not execute). Wait = `Retry-After`, else
  until `x-ratelimit-reset`, else 1 s doubling **[default]**. Total waiting per call is at most 30 s **[default]**. If the
  needed wait exceeds what is left, return `ErrRateLimited` at once without sleeping.
- `502`, `503`, `504` and transport errors are retried for `GET` only, after 1 s, 2 s, 4 s **[default]**, inside the same 30 s
  budget **[default]**. Writes are never blindly repeated; `Ensure*` semantics and the sweep make the caller's retry safe.
- `ctx` cancellation ends any wait immediately with `ctx.Err()`.
- GraphQL reports a rate limit inside a `200` body, as an error of type `RATE_LIMITED`, with no `Retry-After`
  and no usable reset header. `MarkReady` maps it to `ErrRateLimited` at once: no backoff, no retry, no sleep.
  The caller's retry is safe because an already ready pull is success. `MergeQueue` does the same.

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
`OpenPulls`, `AppComments`, `AppIssues`, `CheckRuns` and `HeadRuns`. The exceptions are `ChangedFiles`, whose contract has
a `truncated` result: it returns the paths read so far with `truncated` true; and `Compare`, whose files GitHub lists
on one answer, at most 300: 300 files set `Truncated`.

### Endpoints

- `ListInstallations`: `GET /app/installations`; suspended installations are skipped **[default]**.
  Each `Installation` carries the account login, its type (`Organization` or `User`, from `account.type`) and the
  install time (`created_at`).
- `ListRepos`: `GET /installation/repositories`.
- `ResolveRepo`: `GET /repos/{r}/installation` (JWT) then `GET /repos/{r}` with that installation's token.
- `File`: `GET /repos/{r}/contents/{path}?ref=`; `type` must be `file`; base64 body; over 1 MiB → error
  **[default]** (GitHub's contents API returns no inline content above that size); a directory, symlink or
  submodule → error. Consequence for an oversized settings, policy or baseline file: the caller fails closed,
  the gate check stays pending and ends `action_required` through the sweep; it is never green.
- `Pull`: `GET /repos/{r}/pulls/{n}`, then `GET /repos/{r}/compare/{base_sha}...{head_sha}` for
  `merge_base_commit.sha` (sent with `per_page=1` to keep the commit list out of the answer). Only `Pull` fills `MergeBaseSHA`; `PullSummary`, `OpenPulls`, `PullByHead` and `EnsurePull` leave it `""` **[default]**.
- `PullSummary`: `Pull`'s first request only, for a reader that never uses the merge base.
- `OpenPulls`: `GET /repos/{r}/pulls?state=open`, drafts included.
- `PullByHead`: `GET /repos/{r}/pulls?state=all&head={owner}:{branch}&sort=created&direction=desc&per_page=1`,
  `{owner}` being the owner of `r`, so fork heads never match; first hit; none → `ErrNotFound`. A hit whose
  `head.repo.full_name` is not `r` or whose `head.ref` is not the branch is dropped (also in `EnsurePull`).
- Pull mapping, everywhere: `Merged` = `merged_at` is set **[default: list responses carry no `merged` flag]**;
  `Body` = `body` (`null` → `""`); `URL` = `html_url`; `State` as GitHub gives it (a merged pull is `closed`).
- `BranchHead`: `GET /repos/{r}/git/ref/heads/{branch}` → `object.sha`; `404` → `ErrNotFound`.
- `RequiredChecks` (role `Rules`): `GET /repos/{r}/rules/branches/{branch}` (paged) → the `context` of every `required_status_checks` rule; then `GET /repos/{r}/branches/{branch}` → `protected` and, when GitHub lists them to the App, `protection.required_status_checks.contexts`, which count like a ruleset's. Either read failing fails the call, so a caller shows "unknown", never "not enforced". `Protected` is set when the branch is protected and its checks are not listed (classic protection may need the Administration permission, which the App does not hold).
- `ChangedFiles`: `GET /repos/{r}/pulls/{n}/files`; returns `filename` and, for renames, `previous_filename` too,
  deduplicated: renaming a protected file away is a change to it. `truncated` is true when the pagination cap
  was hit, or when the pull's `changed_files` count (one extra `GET /repos/{r}/pulls/{n}`) exceeds the number of
  file entries read: GitHub silently stops this list at 3 000 files. An error still returns no paths.
- `CheckRuns`: `GET /repos/{r}/commits/{sha}/check-runs?check_name=&app_id=<own>&filter=latest`: only this
  App's check runs, so another app's check of the same name is never seen (a run whose `app.id` is not the
  App's is dropped as well); latest per name is GitHub's `filter=latest`; annotations are not read back, only
  their number: `AnnotationCount` = `output.annotations_count`. Create and update never send that field.
- `CreateCheckRun`: `POST /repos/{r}/check-runs`; `UpdateCheckRun`: `PATCH /repos/{r}/check-runs/{id}`. An
  annotation maps to `start_line = end_line = Line`, and `Line` 0 to line 1. GitHub appends annotations on update:
  one request takes at most 50, and a caller with more sends them in further updates of the same run, each with the
  title and summary (`gate/feedback` does this). The status is `in_progress` or `completed`; a conclusion is
  `success`, `failure`, `neutral` or `action_required`. Anything else, `queued` and `skipped` included, is an error
  before any request: GitHub counts a required check that ends `skipped` as passing, and the App never sets it.
  `neutral` passes a required check too; it stays allowed for checks that are not required and for advisory mode,
  and only the caller knows which a run is.
- `AppComments`: `GET /repos/{r}/issues/{n}/comments`, kept when `user.login` is the bot login.
  `CreateComment`: `POST` there; `UpdateComment`: `PATCH /repos/{r}/issues/comments/{id}`. A comment on an issue is
  the same call with the issue number.
- `AppIssues` (role `Issues`): `GET /repos/{r}/issues?state=open` (paged), kept when `user.login` is the bot login
  and the item has no `pull_request` key (GitHub lists pull requests among issues). GitHub's `creator` parameter is not
  sent **[default]**: whether it matches an App's `slug[bot]` login is not documented, and a filter that silently
  matched nothing would make the caller open a second issue. The cost is a list of every open issue and pull request:
beyond the 1 000-item cap the call fails, so such a repository gets no repair issue, never a second one.
  `CreateIssue`: `POST /repos/{r}/issues` with `title`, `body` and, when `Assignee` is set, `assignees: [login]`; the
  number from the answer. `UpdateIssueBody`: `PATCH /repos/{r}/issues/{n}` with `body`. `CloseIssue` and
  `ReopenIssue`: `GET /repos/{r}/issues/{n}` first; an answer with a `pull_request` key is an error and nothing is
  sent; else `PATCH` there with `state: closed` or `state: open`. All need the App's Issues permission; without it GitHub answers `403`.
- `SetAssigneesAndLabels` (role `IssueTriage`): `GET /repos/{r}/issues/{n}` first, as `CloseIssue` does; a pull
  request is an error and nothing is sent. Then `PATCH` there with `assignees` and `labels`, which replace both sets;
  `nil` is sent as `[]` and clears a set. The issue may be anyone's, not only the App's. The result is the sets GitHub
  holds in its answer: GitHub drops a change it does not allow without an error, so the caller compares. Needs the
  Issues permission.
- `CollaboratorPermission` (role `Collaborators`): `GET /repos/{r}/collaborators/{login}/permission` with the
  installation token; `permission` as GitHub gives it (`admin`, `write`, `read`, `none`; GitHub folds `maintain` into
  `write` and `triage` into `read`); any other value and a `404` are `none`. Needs `metadata: read`. It answers
  whether a request counts (M2 D21).
- `Run`: `GET /repos/{r}/actions/runs/{id}`; `WorkflowPath` = `path` unchanged; `Event` = `event` unchanged;
  `PullRequests` =
  `pull_requests[].number` (GitHub leaves it empty for fork PRs); `CreatedAt` = `created_at`, which a re-run keeps.
  `Rerun`: `POST .../runs/{id}/rerun`.
- `HeadRuns` (role `WorkflowRuns`): `GET .../workflows/{file name}/runs?head_sha=` (paged); every run of that head,
  newest first as GitHub lists them, mapped as `Run`. No run → an empty list. 1 000 runs or more → an error: GitHub
  stops a filtered run search at 1 000 results without a next page, so a full cap may be a cut list.
- `Dispatch`: `POST /repos/{r}/actions/workflows/{file name}/dispatches` with `ref` and `inputs`.
- `LatestRun`: `GET .../workflows/{file name}/runs?per_page=1`, plus `branch`, `event` and `status` for each non-empty
  `RunFilter` field (GitHub's `status` takes a run status or a conclusion); none → `ErrNotFound`.
- `Artifact`: `GET .../runs/{id}/artifacts?name=` (newest unexpired; none → `ErrNotFound`), then
  `GET .../artifacts/{id}/zip`, which redirects to blob storage; the `Authorization` header is not forwarded
  across hosts. The zip must hold exactly one regular file; its declared size and the bytes actually read
  (through a limited reader) must both be ≤ `maxBytes`, else error. The zip itself is read through a limit of
  `maxBytes` + 1 MiB **[default]**; any second entry, even a directory, is an error. Each of these download defects wraps `ErrArtifactInvalid` (the same bytes come back on every download), unlike a failed read, so a caller can tell a defective artifact from an outage.
- `ArtifactID` is `Artifact`'s listing alone and returns the artifact's ID; `ArtifactByID` is its download alone.
  `Artifact` is exactly the two in turn: the same requests. An artifact never changes, so a reader may keep a
  download under its ID.
- `EnsureBranch`: `GET .../git/ref/heads/{branch}`; if missing, `POST .../git/refs` at the SHA of `fromRef`
  (a 40-hex commit SHA is used as is, anything else is a branch name **[default]**); a `422` "already exists"
  race is success: after any `422` the ref is read again, and the `422` stands if it is still missing.
- `Commit`: Git Data API: blobs → tree with `base_tree` → commit with the branch head as parent → `PATCH` the ref
  with `force: false`. No files → error. A tree equal to the parent's → the current head SHA, no new commit
  **[default]**. A non-fast-forward is an error; the caller reloads.
- `CommitOn`: the same commit, on the 40-hex `parent` the caller names (anything else is an error before any request);
  then `GET .../git/ref/heads/{branch}`: missing → `POST .../git/refs`; at the new commit already → nothing; else
  `PATCH` the ref with the caller's `force`. Without force a branch that moved away from `parent` is GitHub's `422`,
  `ErrUnprocessable`, and stays. A forced move whose new commits change `.github/workflows/` may be refused by GitHub, because the App has
  no `workflows` permission; this is not measured (M2 task 19).
- `EnsurePull`: `GET .../pulls?state=open&head={owner}:{Head}&base={Base}`; first hit wins; else `POST .../pulls`;
  a `422` "already exists" race → list again; the `422` stands if the list is still empty.
- `MergePull` (role `Merges`): `PUT .../pulls/{n}/merge` with `merge_method: squash` and `sha` = the expected head;
  `200` → the merge commit SHA (an answer without `merged: true` and a 40-hex `sha` is an error). `UpdateBranch`:
  `PUT .../pulls/{n}/update-branch` with `expected_head_sha`; `202` → success. GitHub makes the update after the
  answer, so the new head may not exist yet. Neither is repeated after a `5xx`.
- `Compare` (role `Commits`): `GET /repos/{r}/compare/{base}...{head}?per_page=1`, from the merge base to `head`;
  `Status` = `status` (`ahead`, `behind`, `identical`, `diverged`; any other value is an error); `Paths` as
  `ChangedFiles` (a rename lists both names); an answer without `files` is an error, never "nothing changed".
- `UpdatePullBody`: `PATCH .../pulls/{n}`. `MarkReady`: REST cannot leave draft state, so one GraphQL mutation
  `markPullRequestReadyForReview` on the pull's `node_id`; an already ready pull is success. An `errors` entry
  of type `RATE_LIMITED` → `ErrRateLimited` at once, without backoff; any other entry → an error that names the
  route only, never the GraphQL message (it can quote content).
- `MergeQueue` (role `MergeQueues`): one GraphQL query, `repository(owner, name).mergeQueue(branch).entries(first: 100)`,
  paged by `endCursor` up to 10 pages (1 000 entries) **[default]**. Each node maps to a `QueueEntry`: `position`,
  `headCommit.oid` (the merge group commit), `baseCommit.oid`, `pullRequest.number`, `.headRefOid` and
  `.isCrossRepository`. Entries are sorted by position. `mergeQueue: null` (no queue on the branch) → no entries and
  no error. An `errors` entry of type `RATE_LIMITED` → `ErrRateLimited` at once, without backoff; `FORBIDDEN` (the App
  lacks the Merge queues permission) → `ErrQueueForbidden`; any other entry → an error that names the route only,
  never the GraphQL message; `repository: null` → `ErrNotFound`; a node without a head commit, base commit or pull
  request → an error; more than 10 pages → `ErrQueueTooLong`. Never a partial or guessed membership.
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

    // ErrQueueForbidden is GraphQL's FORBIDDEN on the merge queue: the installation lacks the Merge queues permission.
    ErrQueueForbidden = errors.New("github: merge queue not readable: Merge queues permission missing")

    // ErrQueueTooLong is returned when a merge queue holds more entries than one read takes.
    ErrQueueTooLong = errors.New("github: merge queue too long to read")

    // ErrUserTokenRejected is GitHub's 401 on a user's OAuth token: it expired or was revoked. The token cannot
    // recover, so the call is not repeated and the user must sign in again.
    ErrUserTokenRejected = errors.New("github: user token rejected")

    // ErrUnprocessable is GitHub's 422 on a write that maps it: the request was refused as a whole and nothing changed.
    // On creating an issue with an assignee, the same request without the assignee is safe to send.
    ErrUnprocessable = errors.New("github: request refused")

    // ErrNotMergeable is GitHub's 405 on a merge: GitHub cannot merge the pull request now, for example because a
    // required check is absent, pending, failed or set by another app, or a required review is missing.
    ErrNotMergeable = errors.New("github: pull request not mergeable")

    // ErrHeadChanged is GitHub's 409 on a merge: the pull request head is not the expected SHA.
    ErrHeadChanged = errors.New("github: pull request head changed")
)

// Status returns the HTTP status of a GitHub answer that mapped to no sentinel, or 0 for any other error.
func Status(err error) int
```

```go
// Permanent reports whether err is a GitHub answer that the same call cannot change when it is repeated:
// ErrNotFound, or a 4xx status other than 401, 403, 408, 409 and 429. A 403 stays retryable because a secondary
// rate limit can arrive without the headers that mark it. Rate limits, 5xx, transport and every other error are
// not permanent.
func Permanent(err error) bool
```

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

```go
// QueueEntry is one entry of a merge queue as GitHub's GraphQL API states it.
type QueueEntry struct {
    Position    int    // 1 is the front of the queue
    HeadSHA     string // the entry's merge group commit: the base plus every entry ahead of it plus this pull request
    BaseSHA     string
    PullRequest int
    PullHeadSHA string // the pull request head the entry was built from
    CrossRepo   bool   // the pull request comes from a fork
}

// MergeQueues reads a branch's merge queue: the authoritative membership of its merge groups.
type MergeQueues interface {
    // MergeQueue returns the entries in position order; none when the branch has no queue. A GraphQL error, such as
    // a missing Merge queues permission, is an error, never an empty queue.
    MergeQueue(ctx context.Context, r RepoRef, branch string) ([]QueueEntry, error)
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
    DetailsURL  string // the "Details" link GitHub shows on the check; "" = GitHub's default
    Status      string // "in_progress" | "completed"; nothing else is sent
    Conclusion  string // "" unless completed; then "success" | "failure" | "neutral" | "action_required"
    Title       string
    Summary     string // markdown, max 65535 bytes
    // Annotations go in one request: at most 50, and only with a Title and a Summary. GitHub appends the annotations
    // of each request, so more go in further updates of the same run.
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
type Issue struct {
    Number int
    Body   string
}

type NewIssue struct {
    Title, Body string
    Assignee    string // one login; "" assigns nobody
}

// Issues are repository issues opened by the App. A pull request is never one: GitHub lists pull requests among
// issues, and they are left out.
type Issues interface {
    // AppIssues lists the open issues the App opened.
    AppIssues(ctx context.Context, r RepoRef) ([]Issue, error)
    CreateIssue(ctx context.Context, r RepoRef, i NewIssue) (Issue, error)
    UpdateIssueBody(ctx context.Context, r RepoRef, number int, body string) error
    // CloseIssue closes the issue number. A number that names a pull request is an error, and nothing is closed.
    CloseIssue(ctx context.Context, r RepoRef, number int) error
    // ReopenIssue reopens the issue number, as CloseIssue closes it.
    ReopenIssue(ctx context.Context, r RepoRef, number int) error
}

// IssueTriage sets the assignees and labels of any issue in the repository, not only of the App's own.
type IssueTriage interface {
    // SetAssigneesAndLabels replaces the issue's assignees and labels with exactly these; nil clears a set. It returns
    // the sets GitHub holds after the call: GitHub drops a change it does not allow without an error. A number that
    // names a pull request is an error, and nothing is changed.
    SetAssigneesAndLabels(ctx context.Context, r RepoRef, number int, assignees, labels []string) (gotAssignees, gotLabels []string, err error)
}

// Collaborators reads what a person may do in a repository, with the installation token.
type Collaborators interface {
    // CollaboratorPermission returns "admin", "write", "read" or "none" for login on the repository; a login GitHub
    // does not know is "none".
    CollaboratorPermission(ctx context.Context, r RepoRef, login string) (string, error)
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
    PullRequests []int     // PR numbers GitHub associates with the run
    CreatedAt    time.Time // when the run was created; a re-run keeps it
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

// WorkflowRuns lists a workflow's runs for one head commit.
type WorkflowRuns interface {
    // HeadRuns returns every run of the workflow whose head is headSHA, a 40-hex commit SHA; none is an empty list.
    HeadRuns(ctx context.Context, r RepoRef, workflowPath, headSHA string) ([]WorkflowRun, error)
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
    // CommitOn makes one commit of files on parent (parent's tree with the files) and points branch at it: created when
    // missing, else moved with force or only as a fast-forward.
    CommitOn(ctx context.Context, r RepoRef, branch, parent, message string, files []FileChange, force bool) (sha string, err error)
    // EnsurePull returns the open PR for Head, creating it if needed.
    EnsurePull(ctx context.Context, r RepoRef, p NewPull) (Pull, error)
    UpdatePullBody(ctx context.Context, r RepoRef, number int, body string) error
    MarkReady(ctx context.Context, r RepoRef, number int) error
}

// Merges merges a pull request and updates its branch as the App. Each call names the head SHA it expects, a 40-hex
// commit SHA, so it never acts on a head its caller has not seen.
type Merges interface {
    // MergePull squash-merges the pull request when its head is headSHA and returns the merge commit SHA.
    // ErrNotMergeable (405), ErrHeadChanged (409) and ErrUnprocessable (422) wrap GitHub's refusal.
    MergePull(ctx context.Context, r RepoRef, number int, headSHA string) (mergeSHA string, err error)
    // UpdateBranch merges the base branch into the pull request's branch when its head is headSHA. GitHub accepts the
    // update (202) and makes it after the answer, so the new head may not exist yet on return. ErrUnprocessable (422):
    // the head is not headSHA, or GitHub refused the update.
    UpdateBranch(ctx context.Context, r RepoRef, number int, headSHA string) error
}

// Comparison is what changed from one commit to another, as GitHub's compare states it.
type Comparison struct {
    Status    string   // "ahead" | "behind" | "identical" | "diverged": head relative to base; any other is an error
    Paths     []string // changed paths; a rename lists both the old and the new path
    Truncated bool     // GitHub lists at most 300 files: true at 300, and callers must assume any path changed
}

// Commits compares commits.
type Commits interface {
    // Compare compares base and head, both 40-hex commit SHAs, with GitHub's three-dot compare: from their merge base
    // to head.
    Compare(ctx context.Context, r RepoRef, base, head string) (Comparison, error)
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

// CheckApp proves that the App key works with GitHub: GET /app with a fresh App JWT, on every call. It reads no
// installation. Bound it with ctx: on a rate limit or a 5xx the client waits and retries within its own budget.
func (c *Client) CheckApp(ctx context.Context) error

// Readers outside the role interfaces, for the UI only; the gate and its children never call them.
func (c *Client) PullSummary(ctx context.Context, r RepoRef, number int) (Pull, error) // Pull without the merge base
func (c *Client) ArtifactID(ctx context.Context, r RepoRef, runID int64, name string) (int64, error)
func (c *Client) ArtifactByID(ctx context.Context, r RepoRef, id int64, maxBytes int64) ([]byte, error)

// CallStats counts the round trips sent under one context and their summed time; concurrent calls add up past
// the wall-clock time. Token minting is not counted. Safe for concurrent use.
type CallStats struct{ /* unexported */ }

func WithCallStats(ctx context.Context) (context.Context, *CallStats)
func (s *CallStats) Calls() int64
func (s *CallStats) Duration() time.Duration
```

`WorkflowRun.Event` is GitHub's event name for the run (`pull_request`, `push`, `merge_group`, `workflow_dispatch`,
`workflow_run`, `schedule`, …), the run's `event` field passed through unchanged: not mapped, not lowercased,
not checked against a list. Consumers compare it with GitHub's literal names; it is the same vocabulary as the
`event_name` claim of an Actions OIDC token.

`ErrNotFound` is the sentinel for "absent, or not accessible to this credential". `ErrRateLimited` is the
sentinel returned when the 30 s **[default]** backoff budget cannot cover the wait GitHub asks for, and at once when the
GraphQL mutation of `MarkReady` or the GraphQL query of `MergeQueue` answers `RATE_LIMITED`. Both are tested with
`errors.Is`. `TokenSource` is restated under Integrations, from `github/auth`. `OAuth` is the user login flow
of `github/auth`; `api` only receives its result, a user token string, in the `Users` methods.

### Caller contract

- `CreateCheckRun`, `UpdateCheckRun`: `Summary` over 65 535 bytes or more than 50 annotations (GitHub's limits),
  annotations without a `Title` and a `Summary`, a status other than `in_progress` or `completed`, `completed`
  without `success`, `failure`, `neutral` or `action_required`, `in_progress` with a conclusion, or
  `UpdateCheckRun` with `ID` 0 → error before any request.
- `MergePull`, `UpdateBranch`, `Compare`, `HeadRuns`: a head or base that is not a 40-hex commit SHA → error before
  any request; an empty one would let GitHub act on whatever head it has. `MergePull`, `UpdateBranch`,
  `SetAssigneesAndLabels`: a number ≤ 0 → error. `api`
  never truncates.
- `CreateComment`, `UpdateComment`, `CreateIssue`, `UpdateIssueBody`: a body over 65 536 characters (GitHub's
  limit) → error before any request.
- `Artifact`, `ArtifactByID`: `maxBytes` ≤ 0 → error.
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
- **Shared knowledge**: role interfaces only. `ingest`: `Repos`, `Pulls`, `Actions`, `MergeQueues`. `inputs`: `Repos` (`BranchHead` too, for a merge group), `Pulls`, `MergeQueues`.
  `feedback`: `Checks`, `Comments`, `Pulls`, `Issues`, `Repos` (`BranchHead`). `sweep`: `Repos`, `Pulls`, `Actions`.
  They rely on: `ErrRateLimited`
  is returned after bounded backoff, so the caller can stop before it writes and leave checks pending for the sweep;
  lists are complete or an error; `ChangedFiles` includes rename sources and `truncated` means
  "assume any path changed"; `CheckRuns` holds this App's runs only, with `AnnotationCount`; GitHub appends annotations.

### gate and control façades, app

- **Counterpart**: internal/gate/, internal/control/, internal/app/
- **Direction**: each depends on `api` (`*Client` in `Deps`; `app` calls `New`)
- **Strength**: Contract
- **LCA / Rank / Distance**: root / 2 / 2
- **Volatility**: High (`gate`), Moderate (`control`), Low (`app`)
- **Balanced?**: Yes
- **Shared knowledge**: `Client`, `New`. Façades hand the client to children as role interfaces. Used directly:
  `control`: `Repos` (`ListInstallations`, `ListRepos`); `app`: `New`,
  and `Permanent` for the error class of queued webhook work.

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
  `ui`: `Users`, `Repos`, `Checks`, `Pulls` (`OpenPulls`), `Actions`, `Changes`, the `*Client` readers
  `PullSummary`, `ArtifactID`, `ArtifactByID`, and `WithCallStats`.
  `session`: `Users`, `Repos`.

### GitHub REST API (external)

- **Counterpart**: `https://api.github.com`, REST version `2022-11-28`, plus one GraphQL mutation and one GraphQL query
- **Direction**: `api` depends on GitHub
- **Strength**: Contract (public, versioned API)
- **LCA / Rank / Distance**: outside the tree; the highest distance the system has
- **Volatility**: Low
- **Balanced?**: Yes: contract strength tolerates the distance
- **Shared knowledge**: the Endpoints list; rate-limit and ETag semantics above. App permissions needed:
  `checks: write`, `pull_requests: write`, `contents: write`, `actions: write`, `metadata: read`. No `workflows` permission **[decided]** (`internal/module.md`): no method writes
  `.github/workflows/`, and GitHub refuses an App's commit that does. `issues: write` (the `Issues` role, for the repair issue, and
  `IssueTriage`) is added with the release that brings that issue; until the owner grants it, every `Issues` and
  `IssueTriage` call is GitHub's `403`.
  `merge_queues: read` (repository permission **Merge queues: Read**, for `MergeQueue`); without it GraphQL answers
  `FORBIDDEN`, which is `ErrQueueForbidden`.

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
- Lists are complete or an error. `ChangedFiles` and `Compare` alone may be partial, and then say so with
  `truncated`.
- Access is re-proved on every call: the cache is used only after a `304` from GitHub for the same credential.
  Revoked access, a removed repository and a cross-installation `RepoRef` all end as `ErrNotFound` or an error,
  never as data (T07).
- User-token calls never use App credentials; App calls never use a user token.
- The only forced ref move is `CommitOn` with `force`, which the answers use on the App's own `archfit/policy`
  branch; `api` has no method that deletes a branch or closes a pull request: `CloseIssue`, `ReopenIssue` and
  `SetAssigneesAndLabels` read the issue first and refuse a pull request. `api` merges only by squash, and merges
  or updates a pull request's branch only at the head SHA its caller names.
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
- **Test name**: `TestPermanent`
  **Scenario**: table of final answers to a `POST`: `404`; `410`; `400`; `422`; `403` without rate-limit headers; `401`
  twice; `408`; `409`; a rate limit beyond the budget; `500`; `503`. Then plain errors: a Store error,
  `context.DeadlineExceeded`, a wrapped `ErrRateLimited`, a wrapped `ErrNotFound`.
  **Expected behavior**: permanent for the first four and for the wrapped `ErrNotFound`; not permanent for the rest.
- **Test name**: `TestStatusOfAnUnmappedAnswer`
  **Scenario**: `403` and `422` answers; a `401` on a user token; a plain error.
  **Expected behavior**: `Status` gives `403`, `422`, `0`, `0`.
- **Test name**: `TestUnauthorizedInvalidatesAndRetriesOnce`
  **Scenario**: table with a recording `TokenSource` stub: `401` then `200` on `GET`; `401` then `200` on `PATCH`;
  `401` twice; `401` on an App JWT route; `401` on a `Users` call; `401` on a later page of a `Users` list.
  **Expected behavior**: `Invalidate(id)` called once, second request carries the new token, success (rows 1–2);
  error after exactly two requests; error after one request and no `Invalidate` (row 4); `ErrUserTokenRejected` after one request
  (row 5) and after the request that met the `401` (row 6), with no `Invalidate` and no repeat.
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

- **Test name**: `TestCheckAppProvesTheKeyOnEveryCall`
  **Scenario**: `CheckApp` twice against a `200` answer; then once against a `401` answer with a message body.
  **Expected behavior**: `nil` twice and two `GET /app` requests with the App JWT; then an error that names the
  status and does not quote the body.

- **Test name**: `TestReposContract`
  **Scenario**: `ListInstallations` (one suspended); `ListRepos`; `ResolveRepo`; `File` for a file, a missing file,
  a directory, a 2 MiB file; `BranchHead` for a branch and a missing branch.
  **Expected behavior**: JWT for App routes, installation token elsewhere; suspended skipped; `Repo` fully
  mapped with the installation ID; file bytes and blob SHA; `ErrNotFound`; error; error; the ref's commit SHA; `ErrNotFound`.
- **Test name**: `TestMergeQueueReadsEveryPageInPositionOrder`, `TestMergeQueueNeverGuessesMembership`
  **Scenario**: a two-page queue returned out of order; no queue on the branch; `FORBIDDEN`; another GraphQL error; `RATE_LIMITED`; a missing repository; an entry without a head commit; an endless queue.
  **Expected behavior**: all entries in position order, the branch sent in the query; no entries and no error; `ErrQueueForbidden`; an error; `ErrRateLimited`; `ErrNotFound`; an error; `ErrQueueTooLong`.
- **Test name**: `TestPullsContract`
  **Scenario**: `Pull` (same-repo, fork, merged); `OpenPulls` with a draft; `PullByHead` with an open, a merged-only,
  a fork-only and no pull for the branch; `ChangedFiles` with a rename; `PullSummary` of the same pull and of a
  missing one.
  **Expected behavior**: `MergeBaseSHA` from compare, `Body` and `URL` mapped, `Merged` true with `State` `closed`;
  `HeadRepo` differs for the fork; draft listed; newest pull, the merged pull, `ErrNotFound`, `ErrNotFound`; both
  rename names present and `truncated` false;
  `PullSummary` equals `Pull` without `MergeBaseSHA` after one request, then `ErrNotFound`.
- **Test name**: `TestChecksContract`
  **Scenario**: create `in_progress`; update to `completed` with 50 annotations (one with `Line` 0); list by name.
  **Expected behavior**: request bodies match; line 0 sent as 1; only this App's runs listed; `ID`, `ExternalID`,
  `Summary`, `StartedAt` and `AnnotationCount` (from `output.annotations_count`) mapped.
- **Test name**: `TestCommentsContract`
  **Scenario**: a thread with user comments, another bot's comments and two of the App's; create; update.
  **Expected behavior**: only the App's two returned; `GET /app` called once across calls.
- **Test name**: `TestIssuesContract`
  **Scenario**: an issue list with a user's issue, another bot's, two of the App's (one with a `null` body), the App's
  own pull request, an impostor login and a ghost user; create with and without an assignee; update; close and reopen an
  issue and a pull request; bodies of 65 536 and 65 537 characters.
  **Expected behavior**: only the App's two issues, the `null` body as `""`; the list query is `state=open` and
  `per_page=100` without `creator`; `assignees` sent only when set; `body` patched; `state: closed` and `state: open`
  patched for the issue, nothing patched and an error for the pull request; the oversized bodies error without a request.
- **Test name**: `TestIssuesFailuresAreErrors`
  **Scenario**: `CreateIssue` answered `403` without rate-limit headers (no Issues permission), `403` with an exhausted
  rate limit, `404`, `422` (an assignee GitHub refuses).
  **Expected behavior**: a plain error with status `403`; `ErrRateLimited`; `ErrNotFound`; a plain error with status `422`.
- **Test name**: `TestActionsContract`
  **Scenario**: `Run`; `Rerun`; `Dispatch` with inputs; `LatestRun` with and without runs; `Artifact` through a
  redirect to another host.
  **Expected behavior**: fields mapped, `WorkflowPath` unchanged; `201`/`204` are success; `ErrNotFound` when no
  run; the redirected request carries no `Authorization` header; file bytes returned. `ArtifactID` then
  `ArtifactByID` sends exactly `Artifact`'s requests and returns its bytes; `ArtifactByID` with `maxBytes` 0 errors.
- **Test name**: `TestLatestRunSendsOnlySetFilters`
  **Scenario**: table over `RunFilter`: empty; branch only; branch, event and status; event and status.
  **Expected behavior**: the query is `per_page=1` plus exactly the set fields under their GitHub names.
- **Test name**: `TestChangesContract`
  **Scenario**: `EnsureBranch` new, existing, and lost race; `Commit` of two files; `Commit` with identical
  content; non-fast-forward; `CommitOn` on a missing branch, as a fast-forward, a fast-forward from a parent the
  branch left, forced, with content the parent holds, with a parent that is not a commit SHA and with no files;
  `EnsurePull` new, existing, lost race; `UpdatePullBody`; `MarkReady` draft and ready.
  **Expected behavior**: one ref created or none; one commit with both files and `force: false`; head SHA and no
  commit; error; `CommitOn` creates the branch at a commit on the parent, fast-forwards, answers `422` and leaves the
  branch, moves it with `force: true`, returns the parent without a commit, and refuses the rest without a request;
  exactly one pull in every case; GraphQL mutation sent once, none for the ready pull.
- **Test name**: `TestMergesContract`, `TestMergeRefusalsAreTold`, `TestUpdateBranchRefusalsAreTold`
  **Scenario**: `MergePull` and `UpdateBranch` answered `200` and `202`; heads that are empty, short, a branch name,
  upper case or too long; numbers 0 and -1. Merge answers: `405` (three D.2 messages), `409`, `422`, `404`, `403`,
  `502`, and `200` not merged, without a SHA, with a non-SHA. Update answers: `422`, `404`, `403`, `502`.
  **Expected behavior**: squash with `sha` and `expected_head_sha` sent, the installation token, the merge commit SHA;
  no request for a guard; `ErrNotMergeable`, `ErrHeadChanged`, `ErrUnprocessable`, `ErrNotFound`, plain errors, each
  with its `Status` and after exactly one request; errors for the malformed `200`s.
- **Test name**: `TestCompareContract`
  **Scenario**: a modification, a rename and an addition; 300 files; identical commits; an answer without `files`;
  an unknown status; an unknown commit; non-SHA bases and heads.
  **Expected behavior**: status and both rename names, `per_page=1`; `Truncated`; an empty list; an error; an error;
  `ErrNotFound`; no request.
- **Test name**: `TestHeadRunsContract`
  **Scenario**: three runs on two pages; a head without runs; 1 001 runs; exactly 1 000 runs, the last page without a
  next link (GitHub's search cap); an unknown workflow; non-SHA heads.
  **Expected behavior**: all three with `CreatedAt`, `head_sha` and `per_page=100` on every page; an empty list; an
  error; an error; `ErrNotFound`; no request.
- **Test name**: `TestIssueTriageContract`
  **Scenario**: set two assignees, one of which GitHub drops, and a label; set `nil` sets; a pull request; `PATCH`
  answered `422`, `403`; a missing issue; number 0.
  **Expected behavior**: both sets sent, the sets GitHub holds returned; `[]` sent and empty sets returned; an error
  and no `PATCH`; `ErrUnprocessable`, a plain `403`; `ErrNotFound`; no request.
- **Test name**: `TestCollaboratorPermission`
  **Scenario**: `admin`, `write`, `read`, `none`, a `404`, an unknown value (`maintain` in `permission`), a `403`, an
  empty login.
  **Expected behavior**: the same four words, `none`, `none`, the error with status `403`, an error and no request;
  the installation token is sent.
- **Test name**: `TestCheckRunAnnotationBatches`
  **Scenario**: 120 annotations sent as a create of 50 and updates of 50 and 20; then 51 in one create.
  **Expected behavior**: three requests in order, none dropped, each with the title and summary; an error and no
  request.
- **Test name**: `TestUsersContract`
  **Scenario**: `Permission` for admin, push-only, pull-only, `404`, `401`; `UserRepos` over two installations.
  **Expected behavior**: `admin`, `write`, `read`, `none`, `ErrUserTokenRejected`; repos carry their installation ID; only the
  user token is ever sent.

### Boundary Tests

- **Test name**: `TestCheckRunCallerContract`
  **Scenario**: table: summary of 65 535 and 65 536 bytes; 50 and 51 annotations; annotations without a title, without
  a summary; `completed` with each allowed conclusion, without one, and with `skipped`, `cancelled`, `stale`,
  `timed_out`; `queued`; no status; `in_progress` with a conclusion; update with `ID` 0.
  **Expected behavior**: ok, error; ok, error; error, error; ok ×4, error ×5; error; error; error; error. No request
  for an error row; nothing truncated.
- **Test name**: `TestRevokedAccessAndRemovedRepository` (T07)
  **Scenario**: a repository read and cached; then GitHub answers `404` (removed from the installation); then the
  token mint fails (uninstalled).
  **Expected behavior**: `ErrNotFound`, then the wrapped mint error; the cached body is never returned.
- **Test name**: `TestCrossInstallationRepoRef` (T07)
  **Scenario**: `RepoRef{InstallationID: A, FullName: repository of B}` on every role interface.
  **Expected behavior**: A's token is used, GitHub's `404` becomes `ErrNotFound`; B's token is never requested.
- **Test name**: `TestGraphQLRateLimitIsImmediate`
  **Scenario**: `MarkReady` on a draft pull; the GraphQL endpoint answers `200` with an error of type `RATE_LIMITED`.
  **Expected behavior**: `ErrRateLimited` by `errors.Is`; exactly one GraphQL request; no sleep.
- **Test name**: `TestErrorsCarryNoSecretsOrContent`
  **Scenario**: every error path with marker strings in tokens, file paths, bodies and response bodies.
  **Expected behavior**: no marker in any error text.
- **Test name**: `TestAPIImportsOnlyStdlibAndAuth`
  **Scenario**: `go list -deps ./internal/github/api`.
  **Expected behavior**: the only internal import is `internal/github/auth`; no third-party module. A missing
  `go` tool fails the test; it never skips.

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
- **Test name**: `TestCallStatsCountEachRoundTripOfOneContext`
  **Scenario**: three concurrent `PullSummary` calls under one `WithCallStats` context, each answered after 5 ms;
  one more call under a plain context.
  **Expected behavior**: 3 calls and at least 15 ms summed; the plain call is not counted.
- **Test name**: `TestUserSeesOnlyTheirRepositories` (T07 user authorization)
  **Scenario**: two users with different installations; one loses access between calls.
  **Expected behavior**: each list holds only that user's repositories; after the loss `Permission` is `none`;
  nothing is served from a cache.

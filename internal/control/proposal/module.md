# Proposal

**Path**: internal/control/proposal/
**Parent**: internal/control/ (control plane façade)
**Submodules**: none (leaf)

## Purpose

`proposal` turns "a set of file edits" into the only form in which the App ever changes a customer
repository: a branch under `archfit/` plus one draft pull request. Onboarding, the editor and AI
assistance all write through it, so none of them can publish unreviewed architecture, open duplicate
PRs on retry, or overwrite an edit somebody else made in the meantime.

## Functional Responsibilities

- Create or update a deterministic branch and its draft PR; a retry reuses both.
- Refuse a write when the branch moved since the caller read it (`ErrStale`).
- Report the draft's state from the newest pull request of the branch: none, draft, awaiting review, published, closed.
- Update the PR body and mark the PR ready for review on request.
- Never merge, never force-push, never write to the base branch.

## Subdomain Classification

Supporting. Volatility **low**: branch-plus-draft-PR mechanics are stable GitHub behavior, and the
draft states are decisions.md §6 ("Draft, awaiting review, published is the PR state").

## Encapsulated Knowledge

- The order of GitHub writes that makes `Propose` idempotent: ensure branch → stale check → commit → ensure PR.
- The branch name as the idempotency key; no state of its own.
- How a `Pull` maps to a `DraftState`.
- The branch namespace rule: every branch starts with `archfit/`. The full name rule and the head-SHA shape are
  published as `ValidBranch` and `ValidHeadSHA`, so that no consumer writes them again.

## Public Contract

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

```go
// ErrStale means the branch moved since the caller read it; the caller must reload and re-apply.
var ErrStale = errors.New("proposal: branch head changed")

func New(gh Changes, pulls Pulls) Proposals
```

```go
// ValidBranch reports whether branch is a legal draft branch name: it matches
// ^archfit/[A-Za-z0-9._/-]+$, has no empty, "." or ".." path segment (so no "//" and no trailing "/").
func ValidBranch(branch string) bool

// ValidHeadSHA reports whether sha is a full commit SHA: exactly 40 lowercase hex characters.
// "" is not valid; a caller whose own contract allows "no draft yet" checks for "" itself.
func ValidHeadSHA(sha string) bool
```

`ErrStale` is the sentinel for an optimistic-concurrency refusal; test it with `errors.Is`.

`ValidBranch` and `ValidHeadSHA` are the only statement of the branch-name and head-SHA rules in the control plane.
`ui` and `assist` call them on user input before they call `Propose` or `Get`; `Propose` applies `ValidBranch` itself.
`expectHeadSHA` is not shape-checked here: a malformed value never equals a branch head, so it ends in `ErrStale`.

### Propose

1. Validate `Change` (see Validation). An invalid change makes no GitHub call.
2. `Changes.EnsureBranch(Repo, Branch, Base)`.
3. If `expectHeadSHA != ""`: `Get(Repo, Branch)`; the draft must be open (`DraftOpen` or `DraftInReview`) and
   `Draft.HeadSHA == expectHeadSHA`, else `ErrStale`, nothing written. No PR, a merged PR or a closed PR also
   means `ErrStale`: the draft the caller looked at is gone.
4. `Changes.Commit(Repo, Branch, Message, Files)` → `sha`.
5. `Changes.EnsurePull(Repo, NewPull{Title, Body, Head: Branch, Base, Draft: true})` → `Pull`.
6. Return `Draft{Number: Pull.Number, State (mapping below), HeadSHA: sha, URL: Pull.URL}`.

`Title` and `Body` apply only when the PR is created. Later body changes go through `SetBody`.
A PR the customer already marked ready stays ready; `Propose` never converts it back to draft.

### Get

`Pulls.PullByHead(r, branch)`: the newest PR in any state whose head is `branch` in this repository (a fork's
same-named branch never matches, by the counterpart's contract). `ErrNotFound` → `Draft{State: DraftNone}` and a
nil error. Otherwise `Draft{Number, State, HeadSHA: Pull.HeadSHA, URL: Pull.URL}`. For an open PR GitHub keeps
`Pull.HeadSHA` equal to the branch head, so `Repos.BranchHead` is not needed; `New` receives no `Repos` role.

### DraftState mapping

First match wins:

| `Pull`                         | `DraftState`    |
| ------------------------------ | --------------- |
| none (`ErrNotFound`)           | `DraftNone`     |
| `Merged`                       | `DraftMerged`   |
| `State == "closed"`            | `DraftClosed`   |
| `State == "open"`, `Draft`     | `DraftOpen`     |
| `State == "open"`, not `Draft` | `DraftInReview` |

After a merge or close, the next `Propose` on the same branch opens a new PR (`EnsurePull` finds no open one);
`Get` then returns the new one, because `PullByHead` returns the newest.

### Validation

`ValidBranch(Branch)` holds (so no space, control or non-ASCII character either), and `Branch` differs from `Base`.
`Base`, `Title`, `Message` non-empty. `Files` non-empty; every `Path` is a relative slash path
without `..` or a leading `/`; no duplicate paths. `Repo.InstallationID > 0` and `FullName` is `owner/name`.

### SetBody and Ready

`SetBody` → `Changes.UpdatePullBody`. `Ready` → `Changes.MarkReady`. Both take the PR number from a
`Draft` this module returned. Errors pass through wrapped.

## Integrations

### internal/github/api

- **Counterpart**: `internal/github/api/`
- **Direction**: `proposal` depends on `github/api` (roles `Changes`, `Pulls`)
- **Strength**: contract
- **LCA / Rank / Distance**: root / 2 / 2
- **Volatility**: low
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

`ErrNotFound` is the sentinel for a missing repository, branch base or pull; `ErrRateLimited` is the
sentinel returned after bounded backoff. Both pass through wrapped; callers show "try again".
`Review` is unused here; it is part of the restated fence only. Only `Pulls.PullByHead` is called.
`EnsureBranch` and `EnsurePull` are idempotent by the counterpart's contract. `Commit` adds one
non-force commit on top of the branch head.

### Consumers (inbound, contract strength, all balanced)

| Counterpart                    | Uses                                                                 | LCA / Rank / Distance | Volatility |
| ------------------------------ | -------------------------------------------------------------------- | --------------------- | ---------- |
| `internal/control/`            | `New`                                                                | control / 1 / 1       | High       |
| `internal/control/onboarding/` | `Propose`, `Get`, `SetBody`                                          | control / 1 / 1       | High       |
| `internal/control/assist/`     | `Propose` with `expectHeadSHA`, `Get`, `ValidBranch`, `ValidHeadSHA` | control / 1 / 1       | High       |
| `internal/control/ui/`         | `Propose`, `Get`, `Ready`, `ValidBranch`, `ValidHeadSHA`             | control / 1 / 1       | High       |

## Change Vectors

- A new PR state worth showing (for example "changes requested"): mapping table and one constant.
- Deleting or renaming files in a change: `Change`/`FileChange` grow; consumers unaffected until they use it.
- Skipping a no-op commit on retry, once the counterpart can report an unchanged tree.

## Constraints and Invariants

- Nothing here is ever called by the gate path. A failure here never changes a gate result. The gate reads only the cached `Entitlement` record that `account` writes.
- Never merges, never force-pushes, never deletes a branch, never writes to `Base`. Publication is always a human merge.
- One open PR per `(Repo, Branch)`. The branch name is the only idempotency key; this module stores nothing.
- `expectHeadSHA` is an optimistic check: a writer landing between steps 3 and 4 is not detected; both commits
  stay in the branch history and the PR diff shows the result. Ceiling: one human editor per branch. Upgrade
  trigger: a conditional ref update in `Changes.Commit`.
- A retry after a partial failure re-runs all steps. It may add a commit with an unchanged tree; it never adds a PR.
- A branch left behind by a merged PR is reused; the customer should enable "automatically delete head branches"
  (documented in the onboarding PR body). Stated limit.
- `RepoRef` comes from the caller's authenticated context (session, verified upload). This module never builds one.
- File contents are customer policy and workflow text: never logged. Logs: repository ID or name, branch, PR number, SHA, outcome, timings.
- Standard library only. No clock, no Store.

## Test Specification

Fakes implement `Changes` and `Pulls` in memory (branches, commits, pulls) and record calls.

### Unit Tests

- **Test name**: `TestDraftStateMapping`
  **Scenario**: table over `Pull{State, Draft, Merged}`: open+draft, open+ready, closed+merged, closed, and no pull.
  **Expected behavior**: `DraftOpen`, `DraftInReview`, `DraftMerged`, `DraftClosed`, `DraftNone`.
- **Test name**: `TestValidateChange`
  **Scenario**: table: branch `main`, `feature/x`, `archfit/../x`, `archfit/./x`, branch equal to base, empty base, empty title, empty message, no files, path `/etc/x`, path `a/../../b`, duplicate path, `InstallationID` 0, `FullName` without slash; one valid change.
  **Expected behavior**: every invalid row errors with zero GitHub calls; the valid row passes.
- **Test name**: `TestValidBranchAndHeadSHA`
  **Scenario**: table for `ValidBranch`: `archfit/onboarding`, `archfit/edit-alice`, `archfit/a/b.c_d`; `main`, `archfit/`,
  `archfit`, `archfit/../x`, `archfit/./x`, `archfit/a//b`, `archfit/a/`, `archfit/a b`, `archfit/a\tb`, `archfit/ä`, `Archfit/x`, empty.
  Table for `ValidHeadSHA`: 40 lowercase hex; 39 and 41 characters; upper case; a 7-character prefix; `HEAD~1`; empty.
  **Expected behavior**: the first three branches and the first SHA are valid; every other row is not.
- **Test name**: `TestDraftCarriesPullURL`
  **Scenario**: `Pull{URL: "https://ghe.example/acme/shop/pull/12"}` from `EnsurePull` and from `PullByHead`.
  **Expected behavior**: `Draft.URL` equals `Pull.URL` verbatim; no URL is composed here.

### Integration Contract Tests

- **Test name**: `TestProposeCreatesBranchCommitAndDraftPull`
  **Scenario**: empty fake repository; `Propose` with two files, `expectHeadSHA == ""`.
  **Expected behavior**: call order `EnsureBranch`, `Commit`, `EnsurePull` with `Draft: true`, `Head` = branch, `Base` = base; returned `Draft` is `DraftOpen` with the commit SHA and URL.
- **Test name**: `TestProposeTwiceKeepsOnePull`
  **Scenario**: the same `Change` proposed three times; then with different files.
  **Expected behavior**: one open PR, same number every time; commits appended; no force update (T18: retry creates no duplicate PR).
- **Test name**: `TestProposeExpectHead`
  **Scenario**: table: expected SHA equals the draft head; differs; set while no PR exists; set while the newest PR is merged.
  **Expected behavior**: commit lands; `ErrStale` with no `Commit` call in the other three rows.
- **Test name**: `TestGetFindsOnlyOwnBranch`
  **Scenario**: table: `PullByHead` returns the App's open pull; a merged pull; `ErrNotFound`; `ErrRateLimited`.
  **Expected behavior**: the draft with `Pull.HeadSHA` and `Pull.URL`; `DraftMerged`; `DraftNone` with nil error; the error.
- **Test name**: `TestSetBodyAndReady`
  **Scenario**: `SetBody` then `Ready` on the draft; `Get` afterwards.
  **Expected behavior**: `UpdatePullBody` and `MarkReady` called once each with the number; state `DraftInReview`.

### Boundary Tests

- **Test name**: `TestGitHubErrorsPassThrough`
  **Scenario**: each of `EnsureBranch`, `PullByHead`, `Commit`, `EnsurePull` fails with `ErrRateLimited`, `ErrNotFound`, and a generic error.
  **Expected behavior**: `errors.Is` finds the sentinel; no later step runs; the error text carries no file content.
- **Test name**: `TestRetryAfterPartialFailure`
  **Scenario**: `EnsurePull` fails once after a successful `Commit`; the caller retries.
  **Expected behavior**: one PR exists; the branch holds both commits; the second call succeeds.
- **Test name**: `TestProposeKeepsReadyState`
  **Scenario**: the PR was marked ready; `Propose` again.
  **Expected behavior**: state stays `DraftInReview`; no call converts it to draft.
- **Test name**: `TestNeverTouchesBaseOrMerges`
  **Scenario**: run every operation against the recording fake.
  **Expected behavior**: no commit targets `Base`; the fake's call log holds only the five `Changes` methods and `PullByHead`.
- **Test name**: `TestLogsCarryNoContent`
  **Scenario**: propose files with marker strings, capture logs, force errors.
  **Expected behavior**: no marker string, path content or body in logs or errors.

### Behavior Tests

- **Test name**: `TestEditorSaveConflict`
  **Scenario**: two editors load the draft at SHA `a`; the first saves (head `b`); the second saves with `expectHeadSHA = a`.
  **Expected behavior**: second gets `ErrStale`; the branch still ends at `b`; after reload with `b` the save succeeds.
- **Test name**: `TestOnboardingRetryStorm`
  **Scenario**: ten concurrent `Propose` calls for `archfit/onboarding` on a fresh repository.
  **Expected behavior**: exactly one open PR; every call returns its number or an error; none opens a second PR.
- **Test name**: `TestDraftLifecycle`
  **Scenario**: propose → `Ready` → the fake merges the PR → `Get` → propose again on the same branch.
  **Expected behavior**: `DraftOpen`, `DraftInReview`, `DraftMerged` after merge, then a new draft PR with a new number that `Get` now returns.

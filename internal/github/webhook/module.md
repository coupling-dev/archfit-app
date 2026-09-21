# GitHub Webhook

**Path**: internal/github/webhook/
**Parent**: internal/github/ (GitHub integration, publishes its children)
**Submodules**: none (leaf)

## Purpose

`webhook` turns a raw GitHub delivery into a verified, normalized `Event`. It is the only package that knows
the signature scheme and the JSON shapes of GitHub webhook payloads. Without it, `gate` and `control` would
parse GitHub payloads themselves, and a forged delivery could schedule work or name another tenant.

## Functional Responsibilities

- Verify `X-Hub-Signature-256` over the exact body bytes with HMAC-SHA256 in constant time.
- Map GitHub event types and actions to the closed `EventKind` set; everything else is `Ignored`.
- Extract identity (installation, repository, pull request, head, base) from the signed body only.
- Detect forks: `HeadRepo` differs from `Repo`.
- Compute `ChangedPaths` for a push to the default branch, or report "unknown" as an empty list.
- Surface `DeliveryID` for logs.

Not here: HTTP handling, reading the secret file, fan-out to `gate` and `control` (all `app`); any reaction to
an event; any GitHub API call; deduplication.

## Subdomain Classification

Generic. Webhook verification and payload mapping are documented GitHub behavior. Volatility is low; the set of
kinds grows only when a consumer needs a new event.

## Encapsulated Knowledge

### Signature

Header value is `sha256=` plus 64 lowercase or uppercase hex characters. Expected value is
`HMAC-SHA256(secret, body)`. Comparison uses `hmac.Equal` on the decoded bytes. The event type header and the
delivery ID header are not covered by the signature; only the body is.

### Mapping

- `pull_request`: `opened`, `reopened`, `ready_for_review` → `PullOpened`; `synchronize` → `PullSynchronized`;
  `closed` → `PullClosed` (merged or not); every other action → `Ignored`. A base change (`edited`) is `Ignored`
  **[default]**: a pending check already exists on the head, and the sweep repairs stale inputs.
- `pull_request_review`: `submitted`, `edited`, `dismissed` → `ReviewChanged`.
- `push`: `ref` equals `refs/heads/` + `repository.default_branch` and `deleted` is false → `DefaultBranchPush`;
  any other ref, a tag, or a deletion → `Ignored`.
- `installation`: `created` → `InstallationAdded`; `deleted` → `InstallationRemoved`; `suspend`, `unsuspend`,
  `new_permissions_accepted` → `Ignored` **[default]**.
- `installation_repositories`: `added` → `InstallationAdded`; `removed` → `ReposRemoved`.
- `ping` and every unknown event type → `Ignored`. The body of an unknown type is not parsed.

### Field sources

- `InstallationID`: `installation.id`. `Repo`: `repository.full_name`. `RepoPrivate`: `repository.private`.
- Pull request kinds and `ReviewChanged`: `PullRequest` = `pull_request.number`, `HeadSHA` = `pull_request.head.sha`,
  `HeadRepo` = `pull_request.head.repo.full_name`, `BaseRef` = `pull_request.base.ref`. A deleted fork has
  `head.repo: null`; `HeadRepo` is then `""`, which differs from `Repo`, so it still reads as a fork.
- `DefaultBranchPush`: `HeadSHA` = `after`, `BaseRef` = the default branch name, `HeadRepo` = `Repo`, `PullRequest` 0.
- Installation kinds: `Repo` is `""`, `RepoPrivate` false, no pull request fields. Consumers list repositories
  through the API; the repository arrays of these payloads are not surfaced.
- `Ignored`: `Kind`, `DeliveryID`, and `InstallationID` when the body was parsed and has one; nothing else.

### ChangedPaths

Union of `added`, `modified`, `removed` over `commits[]`, deduplicated, sorted. The list is empty, meaning
"unknown, treat as everything changed", when any of these holds **[default]**: `forced` is true; `commits` has
2 048 entries or more (GitHub truncates there); the union exceeds 3 000 paths (the cap is ours); `commits` is absent.

### Decoding

Payload JSON is decoded leniently: GitHub adds fields at will, so unknown fields are ignored. Only the fields
above are read. This is deliberate and differs from the strict decoding of `wire/*`.

## Public Contract

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
    RepoPrivate    bool
    PullRequest    int
    HeadSHA        string
    HeadRepo       string // differs from Repo for forks
    BaseRef        string
    ChangedPaths   []string // default_branch_push only
}
```

```go
// Verify checks X-Hub-Signature-256 over body in constant time.
func Verify(secret []byte, signatureHeader string, body []byte) error

// Parse normalizes a verified delivery. Unknown event types yield Kind == Ignored, not an error.
func Parse(eventType, deliveryID string, body []byte) (Event, error)
```

`PullOpened` is the kind for a pull request that needs a first look. `PullSynchronized` is the kind for a new
head SHA. `PullClosed` is the kind for a closed or merged pull request. `ReviewChanged` is the kind for any
review change. `DefaultBranchPush` is the kind for a push to the default branch. `InstallationAdded` is the
kind for a new installation or added repositories. `InstallationRemoved` is the kind for an uninstall.
`ReposRemoved` is the kind for repositories taken out of an installation.

### Behavior rules

- `Verify` errors on: empty secret (fail closed), empty header, missing `sha256=` prefix, non-hex or wrong-length
  digest, mismatch. Errors never contain the secret, the expected digest, or the body.
- `Parse` must be called only after `Verify` succeeded on the same `body`. It is pure: no I/O, no clock, no state.
- `Parse` errors on: empty `deliveryID`; malformed JSON for a known event type; a mapped kind whose identity is
  incomplete (`installation.id` ≤ 0; empty or slash-less `repository.full_name` for repository kinds;
  `pull_request.number` ≤ 0; a head SHA that is not 40 lowercase hex). An error means "answer `400`, do nothing".
- A known event type with an unmapped action is `Ignored`, not an error.
- Because the event type header is unsigned, a body presented under the wrong type either fails the identity
  checks or is `Ignored`. It can never name a repository or installation that the signed body does not contain.
- The caller bounds the body size before calling; GitHub caps payloads at 25 MB.

## Integrations

`webhook` imports only the standard library and depends on no internal module. This file is the normative
home of `Event`, so nothing is restated for the consumers below.

### app

- **Counterpart**: internal/app/
- **Direction**: `app` depends on `webhook`
- **Strength**: Contract
- **LCA / Rank / Distance**: root / 2 / 2
- **Volatility**: Low
- **Balanced?**: Yes
- **Shared knowledge**: `Verify`, `Parse`, `Event`. `app` reads the secret file, the three headers and the body,
  answers `401` on a `Verify` error and `400` on a `Parse` error, then hands the `Event` to `gate.HandleEvent`
  and afterwards to `control.HandleEvent`.

### gate

- **Counterpart**: internal/gate/
- **Direction**: `gate` depends on `webhook`
- **Strength**: Contract
- **LCA / Rank / Distance**: root / 2 / 2
- **Volatility**: High (the gate side)
- **Balanced?**: Yes
- **Shared knowledge**: `Event`, `EventKind`. `gate` reacts to `PullOpened`, `PullSynchronized` (pending check or
  fork refusal, fork = `HeadRepo != Repo`) and `ReviewChanged` (re-run). It builds its repository reference from
  `InstallationID` and `Repo`, which is why both must come from the signed body.

### control

- **Counterpart**: internal/control/
- **Direction**: `control` depends on `webhook`
- **Strength**: Contract
- **LCA / Rank / Distance**: root / 2 / 2
- **Volatility**: Moderate
- **Balanced?**: Yes
- **Shared knowledge**: `Event`, `EventKind`. `control` reacts to `InstallationAdded`, `ReposRemoved` (observe the
  account) and `InstallationRemoved` (delete the tenant's keys).

### GitHub webhooks (external)

- **Counterpart**: GitHub webhook deliveries for the App
- **Direction**: `webhook` depends on GitHub's payload format
- **Strength**: Contract (documented payloads; lenient decoding)
- **LCA / Rank / Distance**: outside the tree; the highest distance the system has
- **Volatility**: Low
- **Balanced?**: Yes: contract strength tolerates the distance
- **Shared knowledge**: headers `X-Hub-Signature-256`, `X-GitHub-Event`, `X-GitHub-Delivery`; the event types,
  actions and JSON fields listed under Encapsulated Knowledge. The App subscribes to `pull_request`,
  `pull_request_review`, `push`, `installation`, `installation_repositories`. GitHub may redeliver, reorder,
  or drop deliveries; nothing here assumes otherwise.

## Change Vectors

- A consumer needs a new event or action: a new `EventKind` constant and a mapping row.
- GitHub changes the commit cap of push payloads or adds a payload field worth reading.
- Secret rotation with two accepted secrets: `Verify` called twice by `app`, or a variadic form here.
- A different cap for `ChangedPaths`.

## Constraints and Invariants

- Identity comes from the signed body only: never from a header, query string, or URL.
- No delivery is parsed into a non-`Ignored` kind unless its identity fields are complete.
- Replay and duplicates are accepted by design. There is no dedup store and no timestamp in a GitHub delivery.
  Handlers are idempotent and derive state from GitHub, so a replayed delivery repeats a harmless action.
  `DeliveryID` exists for logs only.
- A missed or dropped delivery is repaired by the sweep, not here.
- No domain decision: fork handling, pending checks and trials belong to the consumers.
- Logs (written by callers) carry `DeliveryID`, `Kind`, installation ID, outcome. This package logs nothing and
  its errors hold no payload text, path, or secret.
- Pure functions, safe for concurrent use, standard library only (`crypto/hmac`, `crypto/sha256`, `encoding/hex`,
  `encoding/json`).

## Test Specification

Payload fixtures are trimmed copies of real GitHub deliveries under `testdata/`, with extra unknown fields kept.

### Unit Tests

- **Test name**: `TestVerify`
  **Scenario**: table: valid signature; uppercase hex; one flipped body byte; one flipped digest nibble; wrong secret;
  `sha1=` prefix; no prefix; 63 hex characters; non-hex; empty header; empty secret; empty body with a valid signature.
  **Expected behavior**: `nil` for the first two and the last; an error for every other row.
- **Test name**: `TestVerifyUsesConstantTimeCompare`
  **Scenario**: source check in the package test: the comparison is `hmac.Equal`, not `==` or `bytes.Equal`.
  **Expected behavior**: the check passes; a change to a non-constant-time compare fails it.
- **Test name**: `TestParseMapping`
  **Scenario**: table of (event type, action) → kind for every row of the Mapping section, plus `labeled`, `edited`,
  `suspend`, `ping`, `issues`, and an empty event type.
  **Expected behavior**: the mapped kind; `Ignored` for the rest; no error.
- **Test name**: `TestParseFields`
  **Scenario**: one fixture per non-ignored kind.
  **Expected behavior**: every `Event` field equals the Field sources rule; unused fields are zero.
- **Test name**: `TestChangedPaths`
  **Scenario**: table: three commits with overlapping files; a file added then removed; `forced: true`; 2 048 commits;
  3 001 distinct paths; exactly 3 000 paths; no `commits` key.
  **Expected behavior**: sorted deduplicated union (the removed file included); empty list for forced, truncated,
  over-cap and absent; 3 000 paths returned for the exact cap.

### Integration Contract Tests

- **Test name**: `TestVerifyThenParseRealDeliveries`
  **Scenario**: each fixture signed with a test secret, passed through `Verify` and `Parse`.
  **Expected behavior**: no error; the expected `Event`, including `DeliveryID`.
- **Test name**: `TestUnknownEventTypeIsIgnoredNotError`
  **Scenario**: event types `star`, `workflow_run`, `check_run`, with valid JSON, with `{}` and with non-JSON bodies.
  **Expected behavior**: `Kind == Ignored`, `nil` error in all rows; the body is not parsed.
- **Test name**: `TestForkDetectionFields`
  **Scenario**: same-repository PR; fork PR; fork PR whose head repository was deleted (`head.repo: null`).
  **Expected behavior**: `HeadRepo == Repo`; `HeadRepo != Repo`; `HeadRepo == ""` and therefore `!= Repo`.

### Boundary Tests

- **Test name**: `TestParseRejectsIncompleteIdentity`
  **Scenario**: table over mapped kinds: missing `installation`; `installation.id` 0; missing `repository`;
  `full_name` without a slash; PR number 0; head SHA of 39 characters, uppercase, or empty; empty `deliveryID`.
  **Expected behavior**: an error for each; no `Event` with a non-`Ignored` kind is returned.
- **Test name**: `TestParseMalformedJSON`
  **Scenario**: truncated JSON, a JSON array, and `null` for each known event type.
  **Expected behavior**: error.
- **Test name**: `TestEventTypeHeaderCannotForgeIdentity` (T07: forged events)
  **Scenario**: a signed `push` body presented as `pull_request`, and a signed `pull_request` body presented as
  `installation` and as `push`.
  **Expected behavior**: error or `Ignored`; never an `Event` naming an installation or repository absent from the body.
- **Test name**: `TestUnknownFieldsAreIgnored`
  **Scenario**: fixtures with added top-level and nested fields.
  **Expected behavior**: same `Event` as without them.
- **Test name**: `TestErrorsCarryNoPayload`
  **Scenario**: trigger every error with marker strings in the secret, paths, titles and branch names.
  **Expected behavior**: no marker in any error text.
- **Test name**: `TestWebhookImportsOnlyStdlib`
  **Scenario**: `go list -deps ./internal/github/webhook`.
  **Expected behavior**: no internal or third-party package.

### Behavior Tests

- **Test name**: `TestReplayedDeliveryYieldsSameEvent` (T07: replayed events)
  **Scenario**: the same signed delivery parsed twice, and once more with a different `deliveryID`.
  **Expected behavior**: identical events apart from `DeliveryID`; no state is kept between calls.
- **Test name**: `TestPullRequestLifecycle`
  **Scenario**: deliveries for open, push to the PR, review submitted, review dismissed, convert to draft, ready for
  review, close, reopen.
  **Expected behavior**: `PullOpened`, `PullSynchronized`, `ReviewChanged`, `ReviewChanged`, `Ignored`, `PullOpened`,
  `PullClosed`, `PullOpened`; the head SHA follows the payload each time.
- **Test name**: `TestInstallationLifecycle` (T07: removed repositories, uninstall)
  **Scenario**: installation created; repositories added; repositories removed; suspend; installation deleted.
  **Expected behavior**: `InstallationAdded`, `InstallationAdded`, `ReposRemoved`, `Ignored`, `InstallationRemoved`;
  each carries the installation ID of its body.
- **Test name**: `TestDefaultBranchPushOnly`
  **Scenario**: push to `main` (default), to a feature branch, a tag push, deletion of a branch, push when the default
  branch is `trunk`.
  **Expected behavior**: `DefaultBranchPush` only for the default branch of that payload; `Ignored` otherwise.

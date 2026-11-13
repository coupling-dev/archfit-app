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
- Accept a signature from any of several secrets, so a secret rotation drops no delivery.
- Map GitHub event types and actions to the closed `EventKind` set; everything else is `Ignored`.
- Extract identity (installation, repository, pull request, head, base) from the signed body only.
- Detect forks: `HeadRepo` differs from `Repo`.
- Compute `ChangedPaths` for a push to the default branch, or report "unknown" as a `nil` list.
- Publish the three delivery header names as constants, so no caller spells them.
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
- `pull_request_review`, any action → `Ignored`: a review approves nothing.
- `issue_comment`: `created` on a pull request (`issue.pull_request` present) → `PullCommented`; every other action,
  and a comment on a plain issue → `Ignored`. An edited comment is not read again (M2 plan, "The request line").
  GitHub delivers the event only after the owner subscribes the App to "Issue comment"; until then nothing arrives.
- `issues`: `opened` on an issue that is not a pull request → `IssueOpened`; every other action → `Ignored`. GitHub
  delivers it only after the owner subscribes the App to "Issues" (Issues permission).
- `push`: `ref` equals `refs/heads/` + `repository.default_branch` and `deleted` is false → `DefaultBranchPush`;
  any other branch ref, a tag or a deletion → `Ignored`.
- `merge_group`: `checks_requested` → `MergeGroupRequested`; `destroyed` → `MergeGroupDestroyed`; every other action
  → `Ignored`. GitHub delivers the event only after the owner subscribes the App to "Merge group" (repository
  permission Merge queues: Read).
- `installation`: `created` → `InstallationAdded`; `deleted` → `InstallationRemoved`; `suspend`, `unsuspend`,
  `new_permissions_accepted` → `Ignored` **[default]**.
- `installation_repositories`: `added` → `InstallationAdded`; `removed` → `ReposRemoved`.
- `ping` and every unknown event type → `Ignored`. The body of an unknown type is not parsed.

### Field sources

- `InstallationID`: `installation.id`. `Repo`: `repository.full_name`. `RepoPrivate`: `repository.private`.
- Pull request kinds: `PullRequest` = `pull_request.number`, `HeadSHA` = `pull_request.head.sha`,
  `HeadRepo` = `pull_request.head.repo.full_name`, `BaseRef` = `pull_request.base.ref`. A deleted fork has
  `head.repo: null`; `HeadRepo` is then `""`, which differs from `Repo`, so it still reads as a fork.
- `DefaultBranchPush`: `HeadSHA` = `after`, `BaseRef` = the default branch name, `HeadRepo` = `Repo`, `PullRequest` 0,
  `Branch` = `ref` without `refs/heads/`, `Before` = `before` (`""` when it is all zeros, the
  push created the branch, or not a commit SHA): the gate judges the policy change of the push against it.
- `MergeGroupRequested`, `MergeGroupDestroyed`: `HeadSHA` = `merge_group.head_sha` (the group commit), `HeadRepo` =
  `Repo`, `BaseRef` = `merge_group.base_ref` without `refs/heads/`, `PullRequest` 0, `GroupReason` = the payload's
  `reason` verbatim (`MergeGroupDestroyed` only: `merged`, `dequeued`, `invalidated`; `""` when absent).
  `DefaultBranch` comes from the repository object. The payload's member list, `head_ref` and `base_sha` are not read:
  membership comes from GitHub's merge queue API, never from a webhook.
- `PullCommented`: `PullRequest` = `issue.number`, `CommentID` = `comment.id`, `Author` = `comment.user.login`,
  `AuthorType` = `comment.user.type` verbatim (`"User"`, `"Bot"`; `""` when absent), `Body` = `comment.body`. It has no
  head SHA, head repository or base: the payload does not carry them, and the consumer reads the pull request.
  `DefaultBranch` and `RepoPrivate` come from the repository object as for the other pull kinds.
- `IssueOpened`: `Issue` = `issue.number`, `Author` = `issue.user.login`, `AuthorType` = `issue.user.type` verbatim,
  `Body` = `issue.body`; no pull request or head fields.
- Installation kinds: `Repo` is `""`, `RepoPrivate` false, no pull request fields. Consumers list repositories
  through the API; the repository arrays of these payloads are not surfaced.
- `Ignored`: `Kind`, `DeliveryID`, and `InstallationID` when the body was parsed and has one; nothing else.

### ChangedPaths

Union of `added`, `modified`, `removed` over `commits[]`, deduplicated, sorted. The list is `nil`, meaning
"unknown, treat as everything changed", when any of these holds **[default]**: `forced` is true; `commits` has
2 048 entries or more (GitHub truncates there); the union exceeds 3 000 paths (the cap is ours); `commits` is
absent, `null`, or present but empty (`"commits": []`). Unknown is `nil`; consumers test
`len(ChangedPaths) == 0`, never `== nil`. Commits that name no file (an empty commit) yield an empty non-`nil`
list, which that test reads as unknown too: the safe direction, a full rescan.

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
    PullCommented      EventKind = "pull_commented"      // a comment created on a pull request
    IssueOpened        EventKind = "issue_opened"        // an issue opened; never a pull request
    DefaultBranchPush  EventKind = "default_branch_push"
    InstallationAdded  EventKind = "installation_added"  // created, or repositories added
    InstallationRemoved EventKind = "installation_removed" // deleted
    ReposRemoved       EventKind = "repos_removed"
    MergeGroupRequested EventKind = "merge_group_requested" // checks_requested: GitHub built a merge group commit
    MergeGroupDestroyed EventKind = "merge_group_destroyed" // destroyed: merged, dequeued or invalidated
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
    DefaultBranch  string   // the repository's default branch; "" when the payload does not name one
    Branch         string   // push kinds only: the branch, without "refs/heads/"
    ChangedPaths   []string // default_branch_push only
    Before         string   // default_branch_push only: the commit before the push; "" when the push created the branch
    CommentID      int64    // pull_commented only
    Issue          int      // issue_opened only: the issue number
    Author         string   // pull_commented and issue_opened: the comment's or the issue's author login
    AuthorType     string   // pull_commented and issue_opened: "User" or "Bot"; "" when absent
    Body           string   // pull_commented and issue_opened: the comment text or the issue body
    GroupReason    string   // merge_group_destroyed only: "merged", "dequeued", "invalidated" or what GitHub sends
}
```

```go
// VerifyAny checks X-Hub-Signature-256 over body in constant time. It accepts a body signed with any of secrets:
// the current secret and, during a rotation, the previous one.
// It checks every secret, so the time taken does not show which one matched. An empty list or an empty secret
// fails closed.
func VerifyAny(secrets [][]byte, signatureHeader string, body []byte) error

// Parse normalizes a verified delivery. Unknown event types yield Kind == Ignored, not an error.
func Parse(eventType, deliveryID string, body []byte) (Event, error)
```

```go
// Header names of a GitHub webhook delivery. Callers read these headers and pass their values to VerifyAny and Parse.
const (
    HeaderEvent     = "X-GitHub-Event"
    HeaderDelivery  = "X-GitHub-Delivery"
    HeaderSignature = "X-Hub-Signature-256"
)
```

`PullOpened` is the kind for a pull request that needs a first look. `PullSynchronized` is the kind for a new
head SHA. `PullClosed` is the kind for a closed or merged pull request. `PullCommented` is the kind for a new comment on a pull request. `IssueOpened` is the kind for a new issue. `DefaultBranchPush` is the kind for a push to the default branch. `InstallationAdded` is the
kind for a new installation or added repositories. `InstallationRemoved` is the kind for an uninstall.
`ReposRemoved` is the kind for repositories taken out of an installation. `MergeGroupRequested` is the kind for a
merge group commit GitHub built and wants checked. `MergeGroupDestroyed` is the kind for a merge group that was merged,
dequeued or invalidated.

`HeaderEvent` names the header whose value is `Parse`'s `eventType`. `HeaderDelivery` names the header whose
value is `Parse`'s `deliveryID`. `HeaderSignature` names the header whose value is `VerifyAny`'s `signatureHeader`.
They are untyped string constants and the only spelling of these names outside tests: the caller owns
the `http.Request`, this package owns what the headers are called.

### Behavior rules

- `VerifyAny` errors on: an empty list, an empty secret anywhere in the list (fail closed), empty header, missing
  `sha256=` prefix, non-hex or wrong-length digest, mismatch with every secret. Errors never contain a secret, the
  expected digest, or the body. It computes the MAC of every secret and compares each with
  `hmac.Equal` before it decides; it never returns at the first match.
- `Parse` must be called only after `VerifyAny` succeeded on the same `body`. It is pure: no I/O, no clock, no state.
- `Parse` errors on: empty `deliveryID`; malformed JSON for a known event type; a mapped kind whose identity is
  incomplete (`installation.id` ≤ 0; empty or slash-less `repository.full_name` for repository kinds;
  `pull_request.number` ≤ 0; a head SHA that is not 40 lowercase hex; for `PullCommented`: `issue.number` ≤ 0, `comment.id` ≤ 0 or an empty `comment.user.login`; for `IssueOpened`: `issue.number` ≤ 0 or an empty `issue.user.login`; no head SHA is read for either; for the merge group kinds, a `merge_group.head_sha` that is not 40 lowercase hex). An error means "answer `400`, do nothing".
- A `push` needs `repository.full_name` with a slash before it is classified: the repository object names the
  default branch. Without it `Parse` errors, even for a push that would map to `Ignored` (a tag, a deletion). This
  is the one case where an ignorable delivery of a known type is an error.
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
- **Shared knowledge**: `VerifyAny`, `Parse`, `Event`, `HeaderEvent`, `HeaderDelivery`, `HeaderSignature`. `app` reads
  the secret files (the current one, and the previous one during a rotation), the three headers (by these
  constants) and the body, answers `401` on a `VerifyAny` error and `400` on a `Parse` error, then hands the `Event` to `gate.HandleEvent`
  and afterwards to `control.HandleEvent`.

### gate

- **Counterpart**: internal/gate/
- **Direction**: `gate` depends on `webhook`
- **Strength**: Contract
- **LCA / Rank / Distance**: root / 2 / 2
- **Volatility**: High (the gate side)
- **Balanced?**: Yes
- **Shared knowledge**: `Event`, `EventKind`. `gate` reacts to `PullOpened`, `PullSynchronized` (pending check or
  fork refusal, fork = `HeadRepo != Repo`), `PullClosed` (the same),
  `PullCommented` (a request line; a comment without one is ignored), `IssueOpened` (a request issue the CLI
  opened), `PullClosed` (also drops the pull request from the Archfit 4 pull index), `MergeGroupRequested` (pending `archfit/gate` on the group commit, required mode only), `MergeGroupDestroyed` (a reason other than `merged`: correct a pending or decided group check), `DefaultBranchPush` (re-evaluate the open pull request on that head). It builds its repository reference from
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
  `push`, `merge_group`, `installation`, `installation_repositories`, `issue_comment` and `issues`. GitHub may redeliver, reorder,
  or drop deliveries; nothing here assumes otherwise.

## Change Vectors

- A consumer needs a new event or action: a new `EventKind` constant and a mapping row.
- GitHub changes the commit cap of push payloads or adds a payload field worth reading.
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
  **Scenario**: `VerifyAny` with one secret, table: valid signature; uppercase hex; one flipped body byte; one flipped digest nibble; wrong secret;
  `sha1=` prefix; no prefix; 63 hex characters; non-hex; empty header; empty secret; empty body with a valid signature.
  **Expected behavior**: `nil` for the first two and the last; an error for every other row.
- **Test name**: `TestVerifyAnyAcceptsTheCurrentAndThePreviousSecret`
  **Scenario**: table over the secrets `[current, previous]`: signed with the current secret; signed with the
  previous secret; signed with neither; the previous signature against `[current]` only; no secret; an empty secret
  in the list; a malformed header.
  **Expected behavior**: `nil` for the first two; an error for every other row; no error contains either secret value.
- **Test name**: `TestVerifyUsesConstantTimeCompare`
  **Scenario**: source check in the package test: the comparison in `VerifyAny` is `hmac.Equal`, not `==` or `bytes.Equal`.
  **Expected behavior**: the check passes; a change to a non-constant-time compare fails it.
- **Test name**: `TestParseMapping`
  **Scenario**: table of (event type, action) → kind for every row of the Mapping section, plus `labeled`, `edited`,
  `suspend`, `ping`, `issues`, and an empty event type.
  **Expected behavior**: the mapped kind; `Ignored` for the rest; no error.
- **Test name**: `TestParseFields`
  **Scenario**: one fixture per non-ignored kind.
  **Expected behavior**: every `Event` field equals the Field sources rule; unused fields are zero.
- **Test name**: `TestDefaultBranchPushCarriesBefore`
  **Scenario**: a default-branch push with `before` a commit, all zeros, absent, not a SHA; a feature-branch push.
  **Expected behavior**: `Before` is the commit, then `""` three times; the feature-branch push has `""`.
- **Test name**: `TestParseFields` (merge group rows)
  **Scenario**: the fixtures `merge_group_checks_requested.json` and `merge_group_destroyed.json` (reason `dequeued`).
  **Expected behavior**: `MergeGroupRequested` and `MergeGroupDestroyed`; `HeadSHA` the group commit, `HeadRepo == Repo`, `BaseRef` without `refs/heads/`, `DefaultBranch` set, `PullRequest` 0; `GroupReason` `"dequeued"` for the destroyed one, `""` for the requested one.
- **Test name**: `TestChangedPaths`
  **Scenario**: table: three commits with overlapping files; a file added then removed; `forced: true`; 2 048 commits;
  3 001 distinct paths; exactly 3 000 paths; no `commits` key; `"commits": []`; `"commits": null`.
  **Expected behavior**: sorted deduplicated union (the removed file included); a `nil` list for forced, truncated,
  over-cap, absent, empty and `null`; 3 000 paths returned for the exact cap.

### Integration Contract Tests

- **Test name**: `TestVerifyThenParseRealDeliveries`
  **Scenario**: each fixture signed with a test secret, passed through `VerifyAny` and `Parse`.
  **Expected behavior**: no error; the expected `Event`, including `DeliveryID`.
- **Test name**: `TestIssueCommentFixtures`, `TestIssueCommentMapping`, `TestIssueCommentRejectsIncompleteIdentity`
  **Scenario**: a real-shaped comment on a pull request (a Bot author) and on a plain issue; `created`, `edited`, `deleted`;
  payloads without installation, repository, issue number, comment ID, author or comment.
  **Expected behavior**: `PullCommented` with the comment fields for the first; `Ignored` for the issue and for every action
  but `created`; an error and a zero kind for each incomplete payload, with no payload text in the error.
- **Test name**: `TestIssuesOpenedFixture`, `TestIssuesMapping`, `TestIssuesRejectsIncompleteIdentity`
  **Scenario**: a real-shaped issue a worker opened with a request line; `opened`, `edited`, `reopened`, `closed`, and
  `opened` with `issue.pull_request`; payloads without installation, repository, issue number or author.
  **Expected behavior**: `IssueOpened` with the issue fields; `Ignored` for every other action and for a pull request;
  an error for each incomplete payload, with no payload text in the error.
- **Test name**: `TestUnknownEventTypeIsIgnoredNotError`
  **Scenario**: event types `star`, `workflow_run`, `check_run`, with valid JSON, with `{}` and with non-JSON bodies.
  **Expected behavior**: `Kind == Ignored`, `nil` error in all rows; the body is not parsed.
- **Test name**: `TestHeaderNames`
  **Scenario**: a request built with `http.Header.Set` under GitHub's literal header names, read back with
  `Header.Get` under the three constants, then passed through `VerifyAny` and `Parse`.
  **Expected behavior**: the constants equal `X-GitHub-Event`, `X-GitHub-Delivery`, `X-Hub-Signature-256`; the
  delivery verifies and parses.
- **Test name**: `TestForkDetectionFields`
  **Scenario**: same-repository PR; fork PR; fork PR whose head repository was deleted (`head.repo: null`).
  **Expected behavior**: `HeadRepo == Repo`; `HeadRepo != Repo`; `HeadRepo == ""` and therefore `!= Repo`.

### Boundary Tests

- **Test name**: `TestParseRejectsIncompleteIdentity`
  **Scenario**: table over mapped kinds: missing `installation`; `installation.id` 0; missing `repository`;
  `full_name` without a slash; PR number 0; head SHA of 39 characters, uppercase, or empty; empty `deliveryID`.
  **Expected behavior**: an error for each; no `Event` with a non-`Ignored` kind is returned.
- **Test name**: `TestPushWithoutRepositoryIsAnError`
  **Scenario**: table of pushes (feature branch, tag, branch deletion), each with `repository` missing, `full_name`
  empty, and `full_name` without a slash; the same pushes unbroken.
  **Expected behavior**: an error and the zero `Event` for each broken row; unbroken, `Ignored` for the feature
  branch, the tag and the deletion, no error.
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
  **Expected behavior**: `PullOpened`, `PullSynchronized`, `Ignored`, `Ignored`, `Ignored`, `PullOpened`,
  `PullClosed`, `PullOpened`; the head SHA follows the payload each time.
- **Test name**: `TestInstallationLifecycle` (T07: removed repositories, uninstall)
  **Scenario**: installation created; repositories added; repositories removed; suspend; installation deleted.
  **Expected behavior**: `InstallationAdded`, `InstallationAdded`, `ReposRemoved`, `Ignored`, `InstallationRemoved`;
  each carries the installation ID of its body.
- **Test name**: `TestPushKinds`
  **Scenario**: push to `main` (default), to a feature branch, a tag push, deletion of the default branch and of a
  feature branch, push when the default branch is `trunk`, push to `main` when the default is `trunk`.
  **Expected behavior**: `DefaultBranchPush` only for the default branch of that payload; `Ignored` for another
  branch, the tag and both deletions.

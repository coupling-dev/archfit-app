# Gate Decisions

**Path**: internal/gate/decisions/
**Parent**: internal/gate/ (Gate)
**Submodules**: none (leaf)

## Purpose

An unattended agent that a finding blocks cannot change the policy: only an architecture owner can. It files a
decision request as a pull request comment (`wire/decision`). This module keeps what the App does with such a request
after the façade has checked it: one record per request, one status comment per record in the pull request thread,
and the rules that mark a pending request stale. Without it the request would be a comment nobody tracks, the agent
could not read an answer, and the owners' view (Needs you, later) would have nothing to read.

## Functional Responsibilities

- Store a request as decoded (not the raw comment) with the requester login, comment ID, head, status, timestamps and
  the status comment ID; keep the proposed policy text beside it (`File`, `Get`, `Policy`).
- Dedupe: the same request on the same head again writes nothing. A new head replaces the record and makes it pending
  again, unless it was declined or approved (that stays until the policy digest changes, which is a new key). A stale
  record is replaced and goes pending on a refile, even on the same head (a reopened pull request).
- Cap pending records: 20 per pull request, 200 per repository. Beyond a cap `File` returns `Capped` and writes nothing.
- Write the status comment: the App's one comment per record, found by its marker among its own comments, created
  once, edited in place when its text changes; at most 40 status comments per pull request (`Answer` for a request that
  has no record counts too). Every status write runs inside a status lease (below) and derives its text from the
  stored record, never from what a caller meant to publish. A failure to write it never loses the record: the record
  says what it last published (`Published`), the request stays in the pending list while that differs from its status,
  and the next event or sweep writes the comment.
- Move a pending record to `approved`, `declined` or `stale` and update the comment (`Settle`, `SettleAt`). The owner's
  decision (`gate`, stage A8b) reads with `Get`, `Policy` and `List` and writes with `SettleAt`, on the head it read.
- List the records of a pull request (`List`): the ones in the pending list (pending, or settled and not yet in the
  thread) and every other one the App wrote a status comment for, found by the comment's marker. A settled request
  whose comment was deleted, or never written because the thread was full, and that left the list, is not listed.
- A refiled record never inherits `Published`: it starts empty, so the next publish writes (idempotent by body equality).
  `Answer` reports whether it wrote. It never overwrites a pending, approved or declined request; it may answer a stale
  one, whose record stays stale and takes the written line as `Published`.
- Stale rules: `HeadMoved` and `Closed` read the live pull request first (a late or retried delivery names a head or a
  state that no longer holds, and must not stale a newer request), and `Revalidate` (the sweep's rule 8: the head is not the pull request's live head (read again, not the head of the sweep's listing: a request may have been filed after it; a request that looks stale is confirmed with one more read before it is settled, because a walk loads its records one by one), or the policy digest
  is no longer accepted). All three also write any status comment that is out of date. **Stated limit**: the policy digest is checked against the trusted inputs the sweep resolved before it walked, not re-resolved per record; a request filed with a newer digest while the sweep runs can be marked `stale (policy changed)` once, and the agent's next post for the same head refiles it. Nothing fails open: a pending request decides no check.
- Read the queue for the pages (`PendingQueue`): the number of open requests of a repository and the records of the
  first entries of its pending list, from the Store alone: no GitHub call, no lease, no write. A record that is gone
  is skipped; an unreadable list or record is an error.
- Nothing grants anything: the requester is whoever commented; no approval, check or receipt is touched.

Not here: deciding whether a comment may file (the façade), who may decide (`gate`), opening the proposal pull
request (`control/proposal`), any UI.

## Subdomain Classification

Core with the gate path; volatility high (the request flow is new and will gain the owner's decision).

## Encapsulated Knowledge

- Keys under the installation tenant (repository names lowercase, as `gate/inputs` keys them; the event carries no
  repository ID): `decision/<repo>/<pr>/<finding_id>/<policy_digest>` (JSON `Record`), the same plus `/policy` (the raw
  proposed policy), `decision/<repo>/pending` (JSON list: pull request, finding ID, policy digest, `open`) and
  `decision/<repo>/lease/<pr>` (the status lease).
  The list exists because the Store cannot list keys: every rule that walks requests walks it. It holds the pending
  requests (`open: true`) and the ones that left pending but whose new status is not yet in the thread (`open: false`);
  a request leaves it, in one transaction with the record's `Published`, only after its comment was written. The caps
  count `open` entries only. A record and the list change together with `CompareAndSwapAll`, retried five times
  against a concurrent writer; then the delivery is retried by the queue.
- The status lease: the lease of `gate/feedback` (a Store key held with `CompareAndSwap`, 60 s, work stops 10 s
  early, released after success or a definite failure, kept after a write whose outcome is unknown), keyed per pull
  request so that the 40-comment cap is exact. Inside it: re-read the record, derive the line from it, list the App's
  comments, create or edit, then record `Published` and `StatusCommentID`. `Answer` re-checks inside the lease that no
  record exists.
- The policy lives in its own key because one value is at most 64 KiB and the policy alone may be that large. A record
  that would not fit is `ErrTooLarge` and nothing is written.
- Records are encoded without HTML escaping, so the size the decoder allowed is the size stored.
- The status comment: line 1 `wire/decision.StatusMarker`, line 2 `StatusLine`, line 3 the fixed sentence that only an
  architecture owner can approve. `declined (<reason>)` carries a fixed reason (a field and a rule), never request text.
- `Answer` writes nothing when a record exists for the same IDs, so a bad refile cannot overwrite a good request's status.

## Public Contract

```go
const MaxPendingPerPull, MaxPendingPerRepo = 20, 200
var ErrNoRecord, ErrNotPending, ErrTooLarge, ErrStatusComment error

type Record struct { /* PullRequest, FindingID, RuleID, EdgeFrom, EdgeTo, HeadSHA, PolicyDigest, Why, AlternativesTried,
   ProposalDigest, Requester, CommentID, Status, Reason, ProposalPR, StatusCommentID, Published, CreatedAt, UpdatedAt */ }
type Filing struct { Request decision.Request; Requester string; CommentID int64 }
type Outcome string // Filed, Refiled, Duplicate, Sticky, Capped

func New(s store.Store, c api.Comments, p api.Pulls, now func() time.Time) *Requests // now clocks the lease only
func (r *Requests) File(ctx, t inputs.Target, f Filing, now time.Time) (Outcome, error)
func (r *Requests) Answer(ctx, t inputs.Target, findingID, digest string, status decision.Status, reason string) (written bool, err error)
func (r *Requests) PendingQueue(ctx, t inputs.Target, limit int) (Queue, error) // t names the repository; limit 0 counts only
type Queue struct { Open int; Records []Record } // open requests in the repository; the first records of the pending list, filing order
func (r *Requests) List(ctx, t inputs.Target) ([]Record, error)                      // oldest first
func (r *Requests) BranchSuffix(ctx, t inputs.Target, findingID, digest, head string) (string, error)
func (r *Requests) Get(ctx, t inputs.Target, findingID, digest string) (Record, error)
func (r *Requests) Policy(ctx, t inputs.Target, findingID, digest string) (string, error)
func (r *Requests) Settle(ctx, t inputs.Target, findingID, digest string, status decision.Status, reason string, proposalPR int, now time.Time) error
func (r *Requests) SettleAt(ctx, t inputs.Target, findingID, digest, expectedHead string, status decision.Status, reason string, proposalPR int, now time.Time) error
func (r *Requests) HeadMoved(ctx, t inputs.Target, now time.Time) error   // t.HeadSHA is the new head
func (r *Requests) Closed(ctx, t inputs.Target, now time.Time) error
func (r *Requests) Revalidate(ctx, t inputs.Target, tr inputs.Trusted, now time.Time) error
```

`t.PullRequest` names the pull request in every call; the caller has already checked it is open, not a fork, and
onboarded in required mode. `File` returns the outcome even when only the status comment failed (the error says so).
`Settle` returns `ErrNoRecord` or `ErrNotPending` and changes nothing then. `SettleAt` also returns `ErrChanged` when the
record's head is not `expectedHead`, checked in the same transaction as the status. A settle whose comment write fails
returns that error (`ErrStatusComment`, wrapped) with the record already settled; the request stays listed until a later walk writes the comment.

## Integrations

| Counterpart       | Direction                     | Strength | LCA / Rank / Distance | Volatility | Balanced? |
| ----------------- | ----------------------------- | -------- | --------------------- | ---------- | --------- |
| `gate` (façade)   | gate depends on decisions     | Contract | gate / 1 / 1          | High       | Yes       |
| `gate/sweep`      | sweep depends on decisions    | Contract | gate / 1 / 1          | High       | Yes       |
| `gate/inputs`     | decisions uses `Target`, `Trusted`, `Accepts` | Model | gate / 1 / 1  | High       | Yes       |
| `wire/decision`, `wire/policy` | decisions depends on them | Contract | root / 2 / 2 | Moderate | Yes |
| `github/api`      | `Comments` role only          | Contract | root / 2 / 2          | Low        | Yes       |
| `store`           | `Get`, `CompareAndSwap`, `CompareAndSwapAll` | Contract | root / 2 / 2 | Low | Yes |

## Change Vectors

- `BranchSuffix`: the secret part of a request's proposal branch, 128 random bits (lower-case base32), created on first use by
  `CompareAndSwap` at `<record key>/branch/<head>` and returned unchanged to every later caller, so a retry, a second
  instance and a restart name the same branch while nothing the agent states can predict it. One per pull request, finding,
  digest and head.
- A per-pull-request index of all records, if the comment-derived `List` is not enough.
- A5 (built): the overview's Needs you and the decisions page read the pending list and the records (`PendingQueue`).
  The pending list holds no request settled and published; a history view reads each pull request's `List`.
- A cap or a comment limit changes: constants.
- A sweep that visits closed pull requests (today a missed close is repaired by no pass).

## Constraints and Invariants

- A record never grants anything. No approval, receipt or check is read or written here.
- Fail closed: an unreadable record or pending list is an error and nothing is written over it.
- Stale and declined records are never deleted. A declined or approved request never moves again.
- Only pending requests and requests whose status is not yet published are in the pending list; a record and the list change in one atomic step.
- No clock: `now` is a parameter. Logs carry installation ID and pull request number, never request text.
- Writes only keys under `decision/`.
- The thread is written only inside the status lease, from the stored record.

## Test Specification

- `TestFileConcurrentlyKeepsOneRecordAndOneIndexEntry` (-race): 20 goroutines file one request: one `Filed`, one list entry, one comment.
- `TestFileRefusesARecordTooLargeForTheStore`: `ErrTooLarge`, no key, no comment.
- `TestAnswerRacingFileLeavesThePendingCommentAlone`, `TestStatusWritersOfOneRequestCreateOneComment`,
  `TestStatusCommentCapHoldsUnderConcurrency` (-race): the lease; an answer never overwrites a stored request, two writers
  create one comment, the cap is exact.
- `TestSettledRequestStaysListedUntilItsCommentIsWritten`, `TestCapsCountOnlyPendingRequests`,
  `TestRepositoryCapCountsOnlyPendingRequests`: settle with a failing edit, then a later walk repairs it; unpublished requests do not count.
- `TestRefileDoesNotTrustAPublishedLineOlderThanTheThread` (an edit that lands with an unknown outcome; a lost record write): a refile starts with `Published` empty, so the next publish always writes.
- `TestAnswerWritesOverAStaleRequestAndNeverOverALiveOne`, `TestRevalidateSettlesARequestForAnOldHead`.
- `TestSettleAtRefusesAChangedHead`, `TestHeadMovedAndClosedReadTheLivePullRequest`.
- `TestSettleMovesOnlyPendingRecords`: missing, approved, then stale refused; a closed pull request leaves an approved record alone.
- `TestListReturnsPendingAndSettledRequestsOfOnePullRequest`: pending and declined records of pull request 7, not 8, not the answer without a record.
- `TestBranchSuffixIsOneSecretPerRequestAndHead`: 20 goroutines get one suffix, stored under the key; another pull request or head gets its own.
- `TestListKeepsASettledRequestWhoseCommentFailed`: `ErrStatusComment` from `Settle`; the approved request is still listed.
- `TestStatusCommentsOfOnePullRequestAreCapped`: 45 answers leave 40 comments.
- `TestUnreadableRecordsFailClosed`: a corrupt pending list stops `File` and `Closed`, nothing written.
- Behavior through the façade, in `internal/gate/decision_test.go`: record and answer, same-head dedupe, refile, sticky
  decline, answers without a record (table), ignored comments (table), caps, stale rules (new head, closed, sweep),
  comment failure and repair.

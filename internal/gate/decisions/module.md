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
  has no record counts too). A failure to write it never loses the record; the next event or sweep repairs it.
- Move a pending record to `approved`, `declined` or `stale` and update the comment (`Settle`). The seam for the
  owner's decision (stage A8b, not built here) is `Get`, `Policy` and `Settle`.
- Stale rules: `HeadMoved`, `Closed`, and `Revalidate` (the sweep's rule 8: policy digest no longer accepted; also
  writes a missing status comment).
- Nothing grants anything: the requester is whoever commented; no approval, check or receipt is touched.

Not here: deciding whether a comment may file (the façade), the owner's approve and decline, opening the proposal pull
request, any UI.

## Subdomain Classification

Core with the gate path; volatility high (the request flow is new and will gain the owner's decision).

## Encapsulated Knowledge

- Keys under the installation tenant (repository names lowercase, as `gate/inputs` keys them; the event carries no
  repository ID): `decision/<repo>/<pr>/<finding_id>/<policy_digest>` (JSON `Record`), the same plus `/policy` (the raw
  proposed policy), and `decision/<repo>/pending` (JSON list of the pending requests: pull request, finding ID, policy digest).
  The pending list exists because the Store cannot list keys: every rule that walks requests walks it, and only pending
  requests are in it. A record and the list change together with `CompareAndSwapAll`, retried five times against a
  concurrent writer; then the delivery is retried by the queue.
- The policy lives in its own key because one value is at most 64 KiB and the policy alone may be that large. A record
  that would not fit is `ErrTooLarge` and nothing is written.
- Records are encoded without HTML escaping, so the size the decoder allowed is the size stored.
- The status comment: line 1 `wire/decision.StatusMarker`, line 2 `StatusLine`, line 3 the fixed sentence that only an
  architecture owner can approve. `declined (<reason>)` carries a fixed reason (a field and a rule), never request text.
- `Answer` writes nothing when a record exists for the same IDs, so a bad refile cannot overwrite a good request's status.

## Public Contract

```go
const MaxPendingPerPull, MaxPendingPerRepo = 20, 200
var ErrNoRecord, ErrNotPending, ErrTooLarge error

type Record struct { /* PullRequest, FindingID, RuleID, EdgeFrom, EdgeTo, HeadSHA, PolicyDigest, Why, AlternativesTried,
   ProposalDigest, Requester, CommentID, Status, Reason, ProposalPR, StatusCommentID, CreatedAt, UpdatedAt */ }
type Filing struct { Request decision.Request; Requester string; CommentID int64 }
type Outcome string // Filed, Refiled, Duplicate, Sticky, Capped

func New(s store.Store, c api.Comments) *Requests
func (r *Requests) File(ctx, t inputs.Target, f Filing, now time.Time) (Outcome, error)
func (r *Requests) Answer(ctx, t inputs.Target, findingID, digest string, status decision.Status, reason string) error
func (r *Requests) Get(ctx, t inputs.Target, findingID, digest string) (Record, error)
func (r *Requests) Policy(ctx, t inputs.Target, findingID, digest string) (string, error)
func (r *Requests) Settle(ctx, t inputs.Target, findingID, digest string, status decision.Status, reason string, proposalPR int, now time.Time) error
func (r *Requests) HeadMoved(ctx, t inputs.Target, now time.Time) error   // t.HeadSHA is the new head
func (r *Requests) Closed(ctx, t inputs.Target, now time.Time) error
func (r *Requests) Revalidate(ctx, t inputs.Target, tr inputs.Trusted, now time.Time) error
```

`t.PullRequest` names the pull request in every call; the caller has already checked it is open, not a fork, and
onboarded in required mode. `File` returns the outcome even when only the status comment failed (the error says so).
`Settle` returns `ErrNoRecord` or `ErrNotPending` and changes nothing then.

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

- A8b: the owner approves (`Settle` to `approved` with the proposal pull request) or declines.
- A5: a view of Needs you reads the pending list and the records.
- A cap or a comment limit changes: constants.
- A sweep that visits closed pull requests (today a missed close is repaired by no pass).

## Constraints and Invariants

- A record never grants anything. No approval, receipt or check is read or written here.
- Fail closed: an unreadable record or pending list is an error and nothing is written over it.
- Stale and declined records are never deleted. A declined or approved request never moves again.
- Only pending requests are in the pending list; a record and the list change in one atomic step.
- No clock: `now` is a parameter. Logs carry installation ID and pull request number, never request text.
- Writes only keys under `decision/`.

## Test Specification

- `TestFileConcurrentlyKeepsOneRecordAndOneIndexEntry` (-race): 20 goroutines file one request: one `Filed`, one list entry, one comment.
- `TestFileRefusesARecordTooLargeForTheStore`: `ErrTooLarge`, no key, no comment.
- `TestSettleMovesOnlyPendingRecords`: missing, approved, then stale refused; a closed pull request leaves an approved record alone.
- `TestStatusCommentsOfOnePullRequestAreCapped`: 45 answers leave 40 comments.
- `TestUnreadableRecordsFailClosed`: a corrupt pending list stops `File` and `Closed`, nothing written.
- Behavior through the façade, in `internal/gate/decision_test.go`: record and answer, same-head dedupe, refile, sticky
  decline, answers without a record (table), ignored comments (table), caps, stale rules (new head, closed, sweep),
  comment failure and repair.

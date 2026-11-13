# Gate Records

**Path**: internal/gate/records/
**Parent**: internal/gate/ (Gate)
**Submodules**: none (leaf)

## Purpose

Archfit 4 keeps almost nothing in the App: GitHub is the database (design 4). What it keeps is the state that
GitHub cannot hold: which policy digest the owners approved, the requests and their waiting pull requests, the
approvals and the proposals that carry them, and the pull record of each pull request (M2 D6). This module owns
that state: the `v4/` keys, their values, the indexes that replace the Store's missing list, and the one-use
consume of a proposal. Without it, each later module would invent its own keys and retry rules, and a record and
its index could drift apart.

## Functional Responsibilities

- Name every Archfit 4 key under an installation tenant, all under the prefix `v4/` (M2 D7). Archfit 3 keys are never
  read or written, so the previous App revision stays deployable and its keys can be deleted 30 days after the
  release.
- Define one Go type per value. Every value is JSON whose first field is `schema`; `Decode` refuses another schema,
  an unknown field and trailing data, and `Encode` refuses a value over the Store's 64 KiB limit (`ErrTooLarge`).
- Keep the indexes: one `Index` type, one schema per index, entries sorted and unique by key. Every lookup of a
  record goes through an index key, never a scan.
- Change a record and its index together in one `CompareAndSwapAll` (`Indexed`, over `store.Update`).
- Let an approval join one proposal, once, and pin the proposal to the head and digest that carry it (`Join`).
- Record an owner's answer in one swap (`Answer`, M2 task 19): the new approval, its request answered, and the
  approval joining its proposal (created when new) pinned to the head and digest. Void an approval whose change no
  longer applies, opening its request again (`Void`), and void a proposal (`VoidProposal`).
- Consume a proposal once (`Consume`, M2 task 20): open to merged with the merge commit, its index entry removed, the
  repository's approved digest set (only when the default branch's tip holds that digest, the repository is not
  outside the flow, and it has an approved policy already or the proposal is a `first` one: an ordinary proposal
  never starts change control, M2 review H1), the requests of its approvals out of the request index, and the push recorded, in one swap; the
  same commit again is a no-op success.
- Record a default-branch push whose policy no approval made (`Outside`): the repository is "policy changed outside
  the flow" (design 7.1) until an owner confirms or reverts it (M2 task 23), and the issue opened for it
  (`OutsideIssue`). Record a policy push to a repository without an approved policy (`Unguarded`).
- Record in a pull record how the re-check after an answer got its pull request a fresh result (`Rechecked`, M2 task
  21); uploads keep it.
- Record the repository's owner issues (M2 task 22): the one drift issue and the digest of its body
  (`DriftOpened`, `DriftUpdated`, `DriftClosed`), and one expiry issue per exception and date (`ExpiryOpened`,
  `ExpiryClosed`).
- Record the owner actions outside requests (M2 task 23), each in one swap: a first approval of an onboarding pull
  request (`First`) and a workflow approval (`Workflow`), proposals of that pull request bound to its head, policy
  digest and workflow digest; a first approval of the policy already on the default branch (`ApproveCurrent`) and a
  confirm (`Confirm`), which take effect at once as a consume does; a revert (`RevertTo`), which joins the App's
  policy proposal. A workflow proposal is consumed by its own step (`ConsumeWorkflow`). The first approval lists the
  repository in `v4/repos`, the index of the installation's repositories; `Consume` never does.

Not provided: GitHub calls, verdicts, the request line, the policy writer, passkeys. The passkey and challenge keys
(`v4/passkey/<github-user-id>`, `v4/challenge/<github-user-id>`) belong to `control/passkey` (M2 task 18), because `control`
cannot import `gate/*`.

`Merge.Recheck` records `Repo.SweepRecheck` in the consume swap. `SweepRechecked` clears only the matching proposal
when its after-answer handoff finishes. A crash or transient handoff failure is retried by the next hourly pass.

## Subdomain Classification

Supporting. The record shapes follow the M2 plan ("App keys", "Approval records"); the consume and the index rule
protect the policy, so the module is small but strict. Functional volatility is high during milestone 2: later tasks
add fields to the `v1` values before the release. After the release a field change is a new schema version.

## Encapsulated Knowledge

- The repository part of a key is the lower-case `owner/name`, as GitHub compares names (as in `gate/decisions`).
- Approval and proposal ids come from `NewID`: 128 random bits in lower-case base32, never a `/`.
- The index entries and their meaning:

  | Key | `schema` | Entry key | Entry id |
  | --- | --- | --- | --- |
  | `v4/request-index/<repo>` | `archfit-app.request-index.v1` | request key | issue |
  | `v4/proposal-index/<repo>` | `archfit-app.proposal-index.v1` | pull request (`PullEntry`) | proposal id |
  | `v4/pull-index/<repo>` | `archfit-app.pull-index.v1` | pull request (`PullEntry`) | head SHA |
  | `v4/repos` | `archfit-app.repos.v1` | repository full name, lower case | (empty) |

- **An answered request covers its key until its proposal is consumed** (manager, 2026-11-12, release-blocking for
  tasks 20 and 21). An answer that joins a proposal keeps its request in the request index (state `answered`), so
  while the proposal waits ("merge later", a refused merge) a pull request failing on that key waits on it and no
  second request opens for the key. `Consume` takes it out; `Void` opens it again under the same entry. An answer that
  joins no proposal (the default branch already says it, or it writes no line) leaves the index at once.
- **A policy push is judged once.** `Consume`, `Outside` and `Unguarded` write `v4/policy-push/<repo>/<before>..<after>`
  in the swap that records their judgement, so a push delivered again, late or after later pushes (the queue delivers
  at least once, in any order), finds it and is judged the same way. A push is named by both commits: one that moves
  the branch back to a commit judged before is another push, judged again. The value is never deleted: one small
  value per policy push.
- An index value holds at most what fits in 64 KiB (about 800 entries); `Encode` returns `ErrTooLarge` past it. The
  open sets of one repository are far smaller.
- An unchanged index is only checked, never rewritten, so an index that was never needed stays missing.
- **Approvals are consumed through their proposal** (manager decision on M2 Q1, 2026-11-11). The plan's "merged" row
  consumes the proposal and every approval in one swap of 10 keys. The proposal index and the repository's approved
  digest must change in the same swap, which would make 12. So an approval records its proposal id once, when it
  joins, and counts as consumed when that proposal is `merged`; the commit is recorded once, on the proposal. A
  per-approval `void` stays on the approval. The consume is 3 keys, and a proposal still holds up to 9 approvals.

## Public Contract

Keys: `Prefix` (`v4/`), `RepoKey`, `RequestKey`, `RequestIndexKey`, `ApprovalKey`, `ProposalKey`, `ProposalIndexKey`,
`ProposalLeaseKey`, `PolicyPushKey`, `PullKey`, `PullIndexKey`, `ReposKey`, `PullEntry`, `NewID`. `ProposalLeaseKey`
(`v4/proposal-lease/<repo>`) holds a lease, not a record: `gate/proposals` owns its value and holds it while an answer
writes the repository's proposal.

| Key | Value | `schema` | Holds |
| --- | --- | --- | --- |
| `v4/repo/<repo>` | `Repo` | `archfit-app.repo.v1` | approved digest and the merge commit that brought it; the commit of the first approval (change control starts there); outside the flow (digest, issue); drift issue and its body digest; recovered consume awaiting a recheck handoff (`sweep_recheck`); expiry issues (`<exception key> <YYYY-MM-DD>` → issue) |
| `v4/request/<repo>/<issue>` | `Request` | `archfit-app.request.v1` | key, reason, numbers (verified, balance), waiting pull requests, state `open`, `answered` or `closed`; rule, path, modules, expiry date and record, so the issue body can be rendered again; the pull requests the body lists |
| `v4/approval/<repo>/<id>` | `Approval` | `archfit-app.approval.v1` | kind, request, change (raw JSON, owned by the policy writer), owner (user id and login), credential, time, proposal (set once), state `open` or `void` |
| `v4/proposal/<repo>/<id>` | `Proposal` | `archfit-app.proposal.v1` | kind (none for the App's policy pull request, `first` or `workflow` for a pull request someone else wrote), ordered approval ids (at most `MaxApprovals` = 9), pull request, head SHA, expected digest, the workflow digest its merge must land (`first`, `workflow`), state `open`, `merged` or `void`, consuming commit |
| `v4/policy-push/<repo>/<before>..<after>` | `PolicyPush` | `archfit-app.policy-push.v1` | the policy digest of a default-branch push that changed the policy, and the proposal it consumed, or `unguarded` (no approved policy yet); neither: outside the flow. `<before>` is 40 zeros for a push that created the branch |
| `v4/pull/<repo>/<pr>` | `Pull` | `archfit-app.pull.v1` | head SHA, run id, run attempt, payload digest, accepted policy digest, status, an unambiguous policy-changed refusal code, ambiguous sweep range (`sweep_note`), the date the result judged exceptions on (an accepted result only), error keys and the request key of each error ("" when it has none), for the preview (M2 D6; no result bytes); `rechecks` (optional): the proposal, path, unmet condition and head of each re-check after an answer, the latest last, at most `MaxRechecks` (16) (M2 task 21) |

Functions, all under one installation tenant:

- `Encode(v Value) ([]byte, error)`, `Decode(raw []byte, v Value) error`, `Load(ctx, s, t, key, v) error`
  (`ErrNoRecord` for a missing key). An `Index` names its schema before `Decode` or `Load`.
- `(*Index).Lookup`, `Add` (the key with another id: `ErrExists`), `Remove` (a missing key: no change).
- `Indexed(ctx, s, t, recordKey, indexKey, indexSchema, edit) error`: `edit` gets the record bytes (`nil` when
  missing) and the index (empty when missing), changes the index in place, and returns the next record bytes (`nil`
  deletes). Both keys change in one swap; a lost swap calls `edit` again; an `edit` error writes nothing.
- `Join(ctx, s, t, repo, approvalID, proposalID, headSHA, digest) error`: an open approval joins an open proposal,
  and the proposal's `HeadSHA` and `ExpectedDigest` become the head that carries all its approvals and that head's
  digest, in the same swap. So a merge of an older head never consumes the approval that just joined: its digest no
  longer matches. The same join again only pins again (after a branch rewrite); an approval of another proposal is
  `ErrJoined`; a void approval or a proposal that is not open is `ErrMoved`; a full proposal is `ErrFull`; a missing
  record is `ErrNoRecord`.
- `Consume(ctx, s, t, repo, m Merge) error`, `Merge{Proposal, Commit, Digest, Push, AtTip}`: in one swap of 5 keys,
  the proposal `merged` with its merge commit `Commit`, its proposal index entry removed, the repository's
  `ApprovedDigest` set to `Digest`, the requests of its approvals removed from the request index, and
  `PolicyPush{Digest, Proposal}` at `PolicyPushKey(Push)` (a merge queue lands a merge commit inside a push to a later
  commit). With `AtTip` the repository must still hold `Over` as its `ApprovedCommit` (else `ErrApprovedMoved` and
  nothing is written: a consume landed in between, judge again; `store.Update` reads its keys one by one, so this
  verdict across two keys is returned only after a swap of the unchanged bytes proves both current); then `ApprovedDigest` and `ApprovedCommit` become
  `Digest` and `Commit`, and the first approval also sets `GuardedSince`. The approvals are read before the swap (they would add up to 9 keys); a proposal that gained one meanwhile
  is read again, at most 3 times, then `store.ErrConflict`. The approved digest moves only with `AtTip` (the default
  branch's tip holds `Digest` now): a consume handled after a later policy push never puts an older digest back, and
  never approves a tip that holds a change no approval made. While the repository is outside the flow it stays too:
  the merged policy also carries the change no approval made, so it is not approved. A proposal that holds a `revert`
  approval restored the approved policy (task 23): with `AtTip` its consume ends the state and approves its digest. The
  same commit with the same digest again is `nil`. A proposal that is void, merged by another
  commit, without a pull request, or expecting another digest (also once merged), or a push recorded with another
  judgement, is `ErrMoved`; a missing one is `ErrNoRecord`. So is a proposal approved in a state that ended: a `first`
  proposal of a repository with an approved policy or outside the flow, and one holding a revert that no longer
  holds (`Revert.Holds`); being judged across two keys, it is confirmed by a swap of the
  unchanged bytes first. The rule is decided again after every lost swap, so of
  concurrent callers with different commits exactly one wins, and callers with the same commit all succeed.
- `Outside(ctx, s, t, repo, push Push, digest) (Repo, marked bool, error)`: in one swap, `Repo.OutsideFlow.Digest` set
  to `digest` (an issue already recorded is kept) and `PolicyPush{digest}` at `PolicyPushKey(push)`. A push already
  recorded outside the flow changes nothing (`marked` false); one recorded as a consume or unguarded is `ErrMoved`.
- `Unguarded(ctx, s, t, repo, push Push, digest) error`: `PolicyPush{digest, unguarded}` at `PolicyPushKey(push)` when
  the push has no judgement yet; else no change.
- `OutsideIssue(ctx, s, t, repo, issue) (int, error)`: records `issue` as the issue of the state when none is
  recorded, and returns the recorded one (another delivery's, when it was first). A repository not outside the flow
  is `ErrMoved`.
- `Answer(ctx, s, t, repo, approvalID, a Approval, pin *Pin) error`: in one swap of up to 6 keys, the new approval
  (`ErrExists` when the id is in use), its request (`a.Request`) moved from `open` to `answered` (`ErrAnswered` when not
  open), out of the request index without a pin and kept in it with one, and, with a `Pin{Proposal, PullRequest, HeadSHA, Digest}`, the approval joining
  that proposal and the proposal pinned as `Join` pins it. A proposal that does not exist yet is created `open` on
  `PullRequest` with its proposal index entry (`ErrExists` when another proposal holds that pull request). A merged or
  void proposal is `ErrMoved`, a full one `ErrFull`. So no answer is written without its request and its proposal: a
  merge of a head the App wrote before the swap cannot match an expected digest that names an unrecorded answer. The
  repository record is in the swap unchanged: an open request of a repository outside the flow is `ErrOutsideFlow`, so
  no answer is written on a policy no owner approved, even when the state lands after the caller checked it.
- `Void(ctx, s, t, repo, approvalID) (request int, reopened bool, err error)`: the approval becomes `void`, leaves its
  proposal's list (so a proposal's capacity counts only approvals that still apply), and its `answered` request is
  `open` again with its index entry, in one swap of up to 4 keys; when another open request holds the key now, the
  request stays `answered` (`reopened` false). Again is no change.
- `VoidProposal(ctx, s, t, repo, proposalID) error`: `open` to `void` and out of the proposal index; again is no
  change; a merged proposal is `ErrMoved`. Its approvals are voided first: while it is indexed, a later reader finds it
  and finishes a cleanup that stopped half way. A proposal that still lists an approval (one joined after the caller
  voided the ones it read) is `ErrApprovalsLeft`: no approval stays joined to a void proposal.
- Owner actions outside requests (M2 task 23). The repository record is in each swap, so the state an action was
  judged in is the state it is written in; an approval id in use is `ErrExists`.
  - `First(ctx, s, t, repo, approvalID, a, b Bound) error`, `Bound{Proposal, PullRequest, HeadSHA, Digest,
    WorkflowDigest}`: the approval joins the `first` proposal of the pull request, created with its proposal index
    entry or pinned again to the new head, and the repository joins `v4/repos`, in one swap of 5 keys. A repository
    with an approved policy or outside the flow is `ErrMoved`, as is a pull request held by a proposal of another kind
    or one that is not open; a full proposal is `ErrFull`. Its merge push consumes it (`Consume`), and the caller
    checks the merge lands `WorkflowDigest` too.
  - `Workflow(ctx, s, t, repo, approvalID, a, b Bound) error`: the same with a `workflow` proposal and no `v4/repos`
    entry; a repository without an approved policy, or outside the flow, is `ErrMoved`.
  - `ConsumeWorkflow(ctx, s, t, repo, proposalID, commit) error`: an open `workflow` proposal becomes `merged` by
    `commit` and leaves the proposal index; the same commit again is `nil`; anything else is `ErrMoved`.
  - `ApproveCurrent(ctx, s, t, repo, approvalID, a, commit, digest) error`: the approval, `merged` at once, and the
    repository's `ApprovedDigest`, `ApprovedCommit` and `GuardedSince` set as a first consume sets them, with the
    repository in `v4/repos`, in one swap. A repository with an approved policy or outside the flow is `ErrMoved`.
  - `Confirm(ctx, s, t, repo, approvalID, a, commit, digest) (issue int, err error)`: the approval, `merged` at once,
    and `digest` at `commit` as the approved policy, which ends the state, in one swap; it returns the issue of the
    state. A repository not outside the flow, or outside it with another digest, is `ErrMoved`.
  - `RevertTo(ctx, s, t, repo, approvalID, a, rev Revert, pin Pin) error`, `Revert{Commit, Digest, Outside, Issue}`:
    the approval, of kind `revert` with `rev` as its change, joins the App's policy proposal as `Answer`'s does with
    `pin`, while the state it was made in holds (`Revert.Holds`: outside the flow with digest `rev.Outside`, `""` for
    a deleted policy, and the approved policy still `rev.Digest` at `rev.Commit`; else `ErrMoved`). A confirm, or a
    later change, ends that state for good: the same change pushed again does not revive the revert.
  - `FileDigest(file) string`: `sha256:` and the hex SHA-256 of a file's bytes, the digest a workflow approval binds.

- `Rechecked(ctx, s, t, repo, pr, rc Recheck) (bool, error)`: appends `rc` to `Pull.Rechecks` in the pull record,
  whatever run it names now, keeping the latest `MaxRechecks`, and reports whether it wrote; no record is left as it is
  (false). An empty proposal or path is an error. `gate/sweep` (`AfterAnswer`) writes it and asks `Pull.Rechecked(id)`.
  The markers belong to the pull request: `gate`'s upload keeps them when it writes a newer run's record, so a delivery
  of a consuming push again finds its proposal, after the re-run's own upload and after later proposals.

- `DriftOpened(ctx, s, t, repo, issue, digest) (int, error)`: records `issue` as the drift issue with its body digest,
  when none is recorded, and returns the recorded one (another delivery's, when it was first). `DriftUpdated(..., issue,
  digest)` records the new body digest while `issue` is the recorded one; `DriftClosed(..., issue)` forgets
  it while it is. `ExpiryOpened(ctx, s, t, repo, key, issue) (int, error)` and `ExpiryClosed(..., key, issue)` do the same
  for the expiry issue of `key`. `gate/drift` and `gate/expiry` open each issue on GitHub first and record it after; a
  missing repository record is created, and an unchanged one is only checked.

Every write goes through `store.Update`; after 5 lost swaps it returns `store.ErrConflict`, and the caller may read
again and retry.

## Integrations

### store

- **Counterpart**: internal/store/
- **Direction**: `gate/records` depends on `store`
- **Strength**: Contract
- **LCA / Rank / Distance**: root / 2 / 2
- **Volatility**: Low (the store side)
- **Balanced?**: Yes
- **Shared knowledge**: `Tenant`, `Get`, `ErrNotFound`, `Update`, `ErrConflict`, and the limits of
  `CompareAndSwapAll` (10 keys, 64 KiB values). The Firestore driver makes each swap atomic across App instances.

`gate/requests` (M2 task 16) writes the requests and the request index: a request is stored with its index entry
in one `Indexed` swap, and the pull request joins it in another. Later milestone 2 modules (answers, approvals, the
sweep) read and write through this module;
`control` reaches it only through the `gate` façade.

## Constraints and Invariants

- Only keys under `v4/` are read or written.
- A record and its index change in the same `CompareAndSwapAll`; an open record is in its index and a closed one is not.
  An answered request is in the request index while the proposal its approval joined is open.
- An approval joins at most one proposal, and is consumed at most once, by that proposal's merge commit.
- A proposal is merged by exactly one commit. Its digest becomes the repository's approved digest only when the
  default branch's tip held it at the consume, its merge commit was not behind the approved commit, and the repository
  was not outside the flow. The approved commit never moves back along the branch's history.
- No result bytes, report or source are stored (M2 D6). A request keeps the record its issue shows: check output of
  the asked errors, at most 30,000 bytes, never source.
- Errors never quote a stored value.

## Test Specification

- `TestKeyLayout`: every key, a mixed-case repository lower-cased, all under `v4/`.
- `TestNewIDIsFreshAndKeySafe`: two ids differ, 26 lower-case characters, no `/`.
- `TestValuesRoundTrip`: every value type and an index encode with their schema first and decode equal.
- `TestDecodeRefuses`: another schema, no schema, an unknown field, trailing data, not JSON, a request index read as a
  pull index, an unsorted index, a key twice, ten approvals; no error quotes the value.
- `TestEncodeRefuses`: a 64 KiB reason (`ErrTooLarge`), an index without a schema, a proposal over `MaxApprovals`.
- `TestIndexAddRemoveLookup`: sorted insert, the same entry again, `ErrExists`, lookup, remove, remove of a missing key.
- `TestIndexedMovesARecordAndItsEntryTogether`, `TestIndexedLeavesAnUntouchedIndexMissing`.
- `TestJoin`: a join pins the head and digest, the same join again pins again without a second entry, an approval of
  another proposal, a void approval, a merged proposal, a full proposal; a refused join changes nothing.
- `TestAMergeOfAnOlderHeadCannotConsumeAJoinedApproval`: after a join, the digest before it is `ErrMoved` and the
  pinned digest consumes.
- `TestConsume`, `TestConsumeRefuses`: success with the proposal index entry and the requests of its approvals
  removed (another proposal's answered request and an open one stay), the push recorded and the repository kept
  otherwise; another digest; the same commit again changes no byte; another commit; the same commit with another
  digest; a void proposal; no pull request; no proposal; an empty commit; a push recorded outside the flow.
- `TestAConsumeOutsideTheFlowKeepsTheApprovedDigest`, `TestAConsumeReadsAnApprovalThatJoinedMeanwhile` (an answer joins
  at the same digest between the read and the swap: both requests leave the index),
  `TestAConsumeApprovesOnlyWhatTheTipHolds`, `TestAConsumeRecordsTheMergeCommitAndThePush`, `TestUnguarded`,
  `TestAPushBackToAJudgedCommitIsAnotherPush`, `TestAProposalWithAnApprovalLeftIsNotVoided`,
  `TestAConsumeApprovesOverTheApprovedCommitItRead` (a moved approved commit writes nothing; the first approval sets
  `GuardedSince`), `TestAConsumeThatLandsBetweenTwoReadsMakesTheOtherErrMoved` (a consume lands between the other's
  reads of the proposal and the repository: the other is `ErrMoved`, not `ErrApprovedMoved`).
- `TestOutside`: the state and the push recorded; the issue recorded once, a second one returns the first; the same
  push again changes no byte; a later push keeps the issue; a consumed push is `ErrMoved`; `OutsideIssue` inside the
  flow is `ErrMoved`.
- `TestRechecked` (memory store and emulator): the record of the run read, of a newer attempt and of a newer head each
  get the marker and keep the rest; an earlier proposal's marker is kept; no record stays absent; past `MaxRechecks` the
  oldest goes; an empty recheck is refused.
- On the memory store and on the Firestore emulator (`FIRESTORE_EMULATOR_HOST`, run by the CI emulator job):
  `TestAConsumeWinsOnce` (two commits, one wins, the other `ErrMoved`),
  `TestTheSameCommitConsumesConcurrentlyWithoutError`, `TestAnApprovalJoinsOneProposalOnly` (two proposals race for
  one approval), `TestAnIndexStaysInStepUnderConcurrentWriters` (20 writers open requests and answer half; the index
  equals the open records).
- `TestAnswerRecordsTheApprovalTheRequestAndANewProposalInOneSwap` (the answered request keeps its index entry),
  `TestAnswerJoinsAnOpenProposalAndPinsIt`, `TestAnswerWithoutAProposal` (the request leaves the index), `TestAnswerRefusesAndWritesNothing` (an answered request, no request, an id in use, a
  merged or full proposal, a new proposal without a pull request, a pull request another proposal holds, an empty
  digest), `TestVoidReopensTheRequest`, `TestVoidLeavesTheRequestAnsweredWhenAnotherHoldsItsKey`, `TestVoidProposal`;
  on both backends, `TestARequestIsAnsweredOnce` (two answers race; one wins, the other is `ErrAnswered`).
- `TestDriftIssue`, `TestExpiryIssues` (memory store and emulator): recorded once, a second delivery gets the first
  issue, another issue neither updates nor forgets the recorded one, the rest of the record is kept;
  `TestADriftIssueCreatesAMissingRepositoryRecord`, `TestAnUnchangedRepositoryRecordIsKept`.
- `TestFirstOpensTheProposalOfItsPullRequestAndListsTheRepository` (again after a push: the same proposal pinned to the
  new head), `TestAFirstProposalIsConsumedAsTheFirstApproval`, `TestOwnerApprovalsRefuseAndWriteNothing` (each wrong
  state, kind and digest of `First`, `Workflow`, `ApproveCurrent`, `Confirm` and `RevertTo`),
  `TestApproveCurrentStartsChangeControlAtItsCommit`, `TestConfirmEndsTheStateAtItsCommit`,
  `TestARevertConsumeEndsTheState`, `TestAWorkflowProposalIsConsumedByItsMergeOnce`,
  `TestAProposalApprovedInAStateThatEndedIsNotConsumed` (a first approval after change control started, a revert after
  a confirm or after a later change: `ErrMoved`, nothing written), `TestAnOrdinaryProposalNeverStartsChangeControl`; `TestAnswerRefusesAndWritesNothing`
  covers a repository outside the flow.
- `TestArchfit3KeysAreNeitherReadNorWritten`: every Archfit 3 key of `store/module.md` seeded; every helper runs;
  only `v4/` keys are touched and the Archfit 3 bytes are unchanged.

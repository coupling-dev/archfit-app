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
- Consume a proposal once (`Consume`): open to merged with the merge commit, its index entry removed and the
  repository's approved digest set, in one swap; the same commit again is a no-op success.

Not provided: GitHub calls, verdicts, the request line, the policy writer, passkeys. The passkey and challenge keys
(`v4/passkey/<github-user-id>`, `v4/challenge/<github-user-id>`) belong to `control/passkey` (M2 task 18), because `control`
cannot import `gate/*`.

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
  | `v4/repos` | `archfit-app.repos.v1` | repository full name | (empty) |

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
`PullKey`, `PullIndexKey`, `ReposKey`, `PullEntry`, `NewID`.

| Key | Value | `schema` | Holds |
| --- | --- | --- | --- |
| `v4/repo/<repo>` | `Repo` | `archfit-app.repo.v1` | approved digest; outside the flow (digest, issue); drift issue; expiry issues (exception key → issue) |
| `v4/request/<repo>/<issue>` | `Request` | `archfit-app.request.v1` | key, reason, numbers (verified, balance), waiting pull requests, state `open`, `answered` or `closed`; rule, path, modules, expiry date and record, so the issue body can be rendered again; the pull requests the body lists |
| `v4/approval/<repo>/<id>` | `Approval` | `archfit-app.approval.v1` | kind, request, change (raw JSON, owned by the policy writer), owner, credential, time, proposal (set once), state `open` or `void` |
| `v4/proposal/<repo>/<id>` | `Proposal` | `archfit-app.proposal.v1` | ordered approval ids (at most `MaxApprovals` = 9), pull request, head SHA, expected digest, state `open`, `merged` or `void`, consuming commit |
| `v4/pull/<repo>/<pr>` | `Pull` | `archfit-app.pull.v1` | head SHA, run id, run attempt, payload digest, status, error keys and the request key of each error ("" when it has none), for the preview (M2 D6; no result bytes) |

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
- `Consume(ctx, s, t, repo, proposalID, commit, digest) error`: see Functional Responsibilities. The same commit with
  the same digest again is `nil`. A proposal that is void, merged by another commit, without a pull request, or
  expecting another digest (also once merged) is `ErrMoved`; a missing one is `ErrNoRecord`. The rule is decided again after every lost swap, so of concurrent callers with different commits
  exactly one wins, and callers with the same commit all succeed.

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
- An approval joins at most one proposal, and is consumed at most once, by that proposal's merge commit.
- A proposal is merged by exactly one commit; that commit's digest is the repository's approved digest.
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
- `TestConsume`, `TestConsumeRefuses`: success with the index entry removed and the repository kept otherwise; another
  digest; the same commit again changes no byte; another commit; the same commit with another digest; a void
  proposal; no pull request; no proposal; an empty commit.
- On the memory store and on the Firestore emulator (`FIRESTORE_EMULATOR_HOST`, run by the CI emulator job):
  `TestAConsumeWinsOnce` (two commits, one wins, the other `ErrMoved`),
  `TestTheSameCommitConsumesConcurrentlyWithoutError`, `TestAnApprovalJoinsOneProposalOnly` (two proposals race for
  one approval), `TestAnIndexStaysInStepUnderConcurrentWriters` (20 writers open requests and answer half; the index
  equals the open records).
- `TestArchfit3KeysAreNeitherReadNorWritten`: every Archfit 3 key of `store/module.md` seeded; every helper runs;
  only `v4/` keys are touched and the Archfit 3 bytes are unchanged.

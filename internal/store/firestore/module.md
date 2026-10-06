# Store Firestore Driver

**Path**: internal/store/firestore/
**Parent**: internal/store/ (Store)
**Submodules**: none (leaf)

## Purpose

This module is the persistent driver of the `Store` seam: Cloud Firestore in Native mode over the REST API v1.
It keeps entitlements, consent records, the LLM budget counter and the publication leases across restarts and
across all Cloud Run instances. Without it, the App runs only on `memory`, and a restart drops every lease and
trial. The design rejects the Firestore SDK because of its dependencies (docs/tech-stack.md), so the driver uses
`net/http`.

## Functional Responsibilities

- Implement every `store.Store` operation with the semantics of `internal/store/module.md`, and pass `store/storetest`.
- Make `CompareAndSwap` and `Add` atomic across every process that shares the database, with commit preconditions.
- Retry a commit that Firestore rejected because of a conflict, a bounded number of times. Never retry a commit
  whose outcome is unknown.
- Talk to the Firestore emulator when one is configured: plain HTTP, no credentials.
- Contact no server in `New`. The first request goes out with the first operation.

## Subdomain Classification

Generic (a platform client). Volatility: moderate. The REST subset is stable; limits and pricing of the platform change.

## Encapsulated Knowledge

- Endpoint `https://firestore.googleapis.com/v1/`, or `http://<emulator host>/v1/`.
- Document layout. One collection, `kv`. One document for each key. The document ID is
  `<tenant>-<hex of the key>`: a key can hold `/` and any byte except NUL, and a document ID cannot. A 512-byte key
  gives a 1,045-byte ID, under the 1,500-byte limit. An ID starts with a digit, so it is never `.`, `..` or `__…__`.
  Fields: `tenant` (integer) and `value` (bytes, also for a counter, as decimal ASCII). The tenant is in the ID too,
  so tenants never share a document.
- Operations:

  | Operation        | REST calls                                                                                           |
  | ---------------- | ---------------------------------------------------------------------------------------------------- |
  | `Get`            | `GET` the document. `NOT_FOUND` → `store.ErrNotFound`                                                |
  | `Put`, `Delete`  | `documents:commit` with one write, no precondition                                                   |
  | `CompareAndSwap` | `GET`, compare, then `documents:commit` with a precondition on what was read                         |
  | `Add`            | `GET`, parse the counter, then `documents:commit` with a precondition on what was read               |
  | `DeleteTenant`   | `documents:runQuery` (`tenant == t`, names only, limit 500), then one commit of deletes; until empty |
  | `Tenants`        | `documents:runQuery` (`tenant > last`, order by `tenant`, limit 1), once for each tenant             |

- The precondition is `currentDocument.exists = false` when the key was missing, else `currentDocument.updateTime`
  equal to the update time that was read. Firestore gives each write of a document a new update time, so the commit
  fails when any other write came between the read and the commit. The read is the linearization point of a `false`
  result.
- Conflict statuses: `FAILED_PRECONDITION`, `ALREADY_EXISTS`, `NOT_FOUND` and `ABORTED`. Firestore wrote nothing,
  so the driver reads again and decides again: the new value can still equal `old`. At most 10 attempts
  **[default]**, with a jittered pause from 10 ms, doubled to at most 500 ms **[default]**. After the last attempt
  the operation returns an error.
- Unknown outcome: a transport error, a timeout or any other status after the commit was sent. The commit can have
  landed. A retry would then fail the precondition and report a swap that happened as a failed comparison. So the
  driver returns the error and does not retry.
- A local lock (64 stripes by document name **[default]**) serializes `CompareAndSwap` and `Add` on one document
  inside one process. It only lowers contention. The preconditions alone make the operations atomic.
- Errors carry the HTTP status and the RPC status name only. A Firestore message and a request URL can contain a
  document name, and the name holds the key. Validation errors are those of `store`: tenant and key length only.
- Responses are capped at 8 MiB. Request deadlines come from the caller's context and the client timeout.

## Public Contract

```go
// DefaultDatabase is the database ID used when Config.DatabaseID is empty.
const DefaultDatabase = "(default)"

// TokenSource returns an OAuth access token for the Firestore API.
type TokenSource interface {
    Token(ctx context.Context) (string, error)
}

// Config selects the database.
type Config struct {
    ProjectID  string // Google Cloud project ID
    DatabaseID string // empty selects DefaultDatabase
    // EmulatorHost is host:port of a Firestore emulator. When set, the driver uses plain HTTP and no credentials.
    EmulatorHost string
}

// New returns a Store over the database in c. It validates c and contacts no server.
// tokens may be nil only in emulator mode.
func New(c Config, tokens TokenSource, hc *http.Client) (store.Store, error)
```

### Rules

- `New` fails when the project ID is not a valid Google Cloud project ID, when the database ID is neither
  `(default)` nor a valid database ID, when the emulator host contains `/`, `?`, `#`, `@` or a space, when no token
  source is given outside emulator mode, or when `hc` is nil. The error never contains the value.
- In emulator mode the driver ignores `tokens` and sends `Authorization: Bearer owner`. The emulator reads that as
  its admin bypass of security rules. It is not a credential.
- Each request asks `tokens` for a token. A token error is returned before any request is sent.

## Integrations

### internal/store (parent)

- **Counterpart**: `internal/store/module.md`
- **Direction**: firestore depends on store: it implements `Store` and imports the package for `Tenant`,
  `SystemTenant` and `ErrNotFound`. `store` never imports it.
- **Strength**: contract
- **LCA / Rank / Distance**: store / 1 / 1
- **Volatility**: moderate
- **Balanced?**: yes
- **Shared knowledge**: the Public Contract and the semantics of `internal/store/module.md`, the input limits
  (512-byte key, 64 KiB value), the canonical decimal counter, and `storetest.Run`. The parent is the normative home;
  nothing is restated here.

### internal/app

- **Counterpart**: `internal/app/module.md`
- **Direction**: app depends on firestore (constructor only)
- **Strength**: contract
- **LCA / Rank / Distance**: internal (root) / 2 / 2
- **Volatility**: low
- **Balanced?**: yes
- **Shared knowledge**: `New`, `Config`, `TokenSource` and `DefaultDatabase` above. `app` passes `gcptoken.Source`
  as the token source, and nil in emulator mode.

### Cloud Firestore (external)

- **Counterpart**: the Firestore REST API v1, or the Firestore emulator
- **Direction**: firestore depends on it
- **Strength**: contract (published API)
- **Distance**: highest (outside the tree)
- **Volatility**: low
- **Balanced?**: yes. Only this package knows the API.
- **Shared knowledge**: the document layout and REST subset above. Firestore keeps automatic single-field indexes
  on `tenant`; `DeleteTenant` and `Tenants` need them. No composite index and no TTL policy is needed. See
  [Operations](../../../docs/design/cloud/operations.md#firestore).

## Change Vectors

- A new `Store` operation (the multi-key transaction of the cloud design) adds one more read-compare-commit or a
  `beginTransaction` flow here.
- Higher Firestore limits, or a second collection, change the layout notes only.
- A TTL on lease keys needs a timestamp field. It is a driver detail and does not change the `Store` contract.

## Constraints and Invariants

- Standard library and `store` only. No Google SDK. `TestStoreImportsOnlyStdlib` checks it.
- The driver never logs. It never puts a key, a value or a token in an error.
- An uncertain write is an error, never success and never a failed comparison.
- `CompareAndSwap` and `Add` stay atomic across processes without the local lock.

## Test Specification

### Integration Contract Tests

- **Test name**: `TestConformance`
  **Scenario**: `storetest.Run` against the driver on an in-process fake of the REST subset. The fake checks every
  precondition before it applies a commit, gives each write a new update time, and refuses request shapes it does
  not know.
  **Expected behavior**: every conformance case passes.
- **Test name**: `TestConformanceEmulator`
  **Scenario**: `storetest.Run` against the Firestore emulator, one new project for each case. Skips when
  `FIRESTORE_EMULATOR_HOST` is not set. CI runs it in the job "Store on the Firestore emulator".
  **Expected behavior**: every conformance case passes.
### Unit Tests

- **Test name**: `TestCommitFailures`
  **Scenario**: table over the commit of a `CompareAndSwap`: one `ABORTED`; two `FAILED_PRECONDITION`; `ABORTED`
  without end; the commit applied and the answer lost (503); a 500.
  **Expected behavior**: swapped after 2 commits; swapped after 3; an error after 10 commits and 9 pauses; an error
  after 1 commit with the new value stored; an error after 1 commit with the old value kept. Every pause is in (0, 500 ms].
- **Test name**: `TestAddRetriesAConflictAndReportsAnUnknownOutcome`
  **Scenario**: `Add` with one `ABORTED` commit; then `Add` with an applied commit whose answer is lost.
  **Expected behavior**: the first returns the new value; the second returns an error.
- **Test name**: `TestAccessToken`
  **Scenario**: `New`, then `Put`; a token source that fails.
  **Expected behavior**: `New` asks for no token; `Put` sends the token; a token error sends no request.
- **Test name**: `TestEmulatorModeUsesPlainHTTPAndNoCredentials`
  **Scenario**: emulator mode without a token source.
  **Expected behavior**: the request uses `http` and `Authorization: Bearer owner`.
### Boundary Tests

- **Test name**: `TestNewValidatesConfig`
  **Scenario**: table: valid project with the default, a named and the explicit default database; emulator without
  tokens; missing, slashed or upper-case project; slashed database; emulator host with a scheme; no tokens outside the emulator.
  **Expected behavior**: the first four succeed; the others fail.
- **Test name**: `TestDocumentIDIsValidForEveryKey`
  **Scenario**: keys with `/`, `..`, `__x__`, non-UTF-8 bytes and 512 bytes, under the largest tenant; tenants 1 and 12.
  **Expected behavior**: every ID is a valid Firestore ID; the two tenants never share a document.
- **Test name**: `TestCorruptDocumentIsAnErrorNotAMissingKey`
  **Scenario**: a document without the `value` field.
  **Expected behavior**: `Get` and `CompareAndSwap` return an error that is not `ErrNotFound`.
- **Test name**: `TestErrorsCarryNoKeysOrValues`
  **Scenario**: a server error whose message names the document, a transport error, a value over the limit, a
  non-counter value, with a marker in the key and the value.
  **Expected behavior**: each error has the `store:` prefix and contains neither the marker nor its hex form.
- **Test name**: `TestDeleteTenantDeletesMoreThanOneBatch`
  **Scenario**: 507 keys under one tenant and one key under another; `DeleteTenant` of the first.
  **Expected behavior**: `Tenants` lists only the second.

### Behavior Tests

- **Test name**: `TestCompareAndSwapIsAtomicAcrossClients`
  **Scenario**: on the fake and on the emulator: 4 independent clients of one database, 3 goroutines each, 20
  rounds; each round swaps one lease key from the value of the round before.
  **Expected behavior**: exactly one winner in each round; the final value is the last round's.
- **Test name**: `TestAddIsAtomicAcrossClients`
  **Scenario**: on the fake and on the emulator: 3 independent clients add 1 to one counter 25 times each, at the same time.
  **Expected behavior**: the counter is 75.

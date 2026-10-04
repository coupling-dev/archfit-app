# Store

**Path**: internal/store/ (includes the transparent folder `storetest/`, the exported conformance suite)
**Parent**: internal/ (Archfit App root)
**Submodules**: none (leaf)

## Purpose

`store` is the only persistence seam of the App: a tenant-prefixed key-value interface and an
in-memory implementation. Entitlements, consent records and the managed-LLM budget counter live behind
it. Without it, `gate` and `control` would each bind to a KV product, and tenant isolation would be a
habit of every caller instead of a property of one interface.

## Functional Responsibilities

- Define `Tenant`, `SystemTenant`, `Store` and `ErrNotFound`: seven operations, opaque byte values.
- Make tenant scoping structural: every key operation takes a `Tenant` and none crosses tenants.
- List the tenants that own keys (`Tenants`), so the sweep can forget an installation whose uninstall webhook was missed.
- Provide `NewMemory`, the in-process implementation used by tests, local runs, and as `ARCHFIT_STORE=memory`.
- Provide an atomic integer `Add` so the budget counter needs no transaction.
- Provide `CompareAndSwap` on one key, so a caller can hold a lease that every instance sees (the publication
  lease of `gate/feedback`).
- Provide `DeleteTenant` so uninstall is one call.
- Export the conformance suite `storetest.Run` **[default]** so every future driver passes the same cases.

Not provided, on purpose: listing keys, TTL, transactions beyond `Add` and `CompareAndSwap` on one key, schema,
migrations, value encoding.

## Subdomain Classification

Generic. Key-value storage is a solved problem; the module only fixes the smallest interface the App needs.
Functional volatility is low: the operations follow from decisions.md §2 "Own state", plus one compare-and-swap
for leases that serialize work across instances. Implementation
volatility is moderate: the KV platform is unchosen until `docs/tech-stack.md` exists. The interface absorbs
that choice; consumers never see it.

## Encapsulated Knowledge

- Key layout inside an implementation: `<tenant>/<key>`, tenant as decimal `int64`, e.g. `4711/entitlement`,
  `0/llm-budget/2026-09`. Keys may contain `/`; the first `/` ends the tenant prefix. `12/` never matches the
  prefix `1/`, because the separator follows the full decimal number.
- Counter encoding: the value written by `Add` is the decimal ASCII form of an `int64` **[default]**. `Get` on a
  counter key returns those bytes. `Add` on a key holding anything else is an error and changes nothing.
- The memory implementation: one `map[string][]byte` with the layout above, one `sync.Mutex` **[default]**.
  `DeleteTenant` and `Tenants` are scans. This is the accepted ceiling of the memory driver: O(keys) per call;
  it is for tests and local runs only.
- Byte ownership: `Put` stores a copy, `Get` returns a copy. Callers can never alias stored bytes.
- Input limits **[default]**: key is non-empty, at most 512 bytes, has no NUL byte; value at most 64 KiB;
  tenant is never negative.

Known keys are informative only; their owners define them: `entitlement` (owned by `gate/inputs`, written by
`control/account`), `account` and `consent/<kind>` (private to `control/account`), `feedback/lease/<head_sha>`
(private to `gate/feedback`, written only by `CompareAndSwap`), and under `SystemTenant`
`llm-budget/<YYYY-MM>` (`control/account`). `store` never parses a key or a value except a counter in `Add`.

## Public Contract

```go
// Tenant is the authenticated GitHub installation ID that owns a key.
// Every key is stored under its tenant prefix; no operation crosses tenants.
type Tenant int64

// SystemTenant holds App-wide keys (the managed-LLM budget counter). It is never a GitHub installation ID.
const SystemTenant Tenant = 0
```

```go
// ErrNotFound is returned by Get when the key does not exist for the tenant.
var ErrNotFound = errors.New("store: key not found")

// Store is the only persistence seam of the App. Values are opaque bytes.
// Implementations must be safe for concurrent use.
type Store interface {
    Get(ctx context.Context, t Tenant, key string) ([]byte, error)
    Put(ctx context.Context, t Tenant, key string, value []byte) error
    Delete(ctx context.Context, t Tenant, key string) error // deleting a missing key is not an error
    // Add atomically adds delta to the integer at key (missing = 0) and returns the new value.
    Add(ctx context.Context, t Tenant, key string, delta int64) (int64, error)
    // CompareAndSwap atomically sets key to value when it currently holds exactly old, and reports whether it did.
    // A nil old matches only a missing key; a nil value deletes the key. A false result changes nothing.
    CompareAndSwap(ctx context.Context, t Tenant, key string, old, value []byte) (bool, error)
    // DeleteTenant removes every key of the tenant. Idempotent.
    DeleteTenant(ctx context.Context, t Tenant) error
    // Tenants lists every tenant that owns at least one key, SystemTenant excluded.
    // Used only by the sweep to forget installations whose uninstall webhook was missed.
    Tenants(ctx context.Context) ([]Tenant, error)
}
```

```go
// NewMemory returns an in-process Store for tests and local runs. Data is lost on exit.
func NewMemory() Store
```

`GitHub` is the only identity provider: a `Tenant` is always a GitHub App installation ID, except
`SystemTenant`. `SystemTenant` is the constant tenant `0`. `ErrNotFound` is the sentinel returned by `Get`; callers test it
with `errors.Is`. `NewMemory` is the only constructor in this package.

### Semantics every implementation must honor

- All operations: a cancelled or expired `ctx` returns `ctx.Err()` and changes nothing. An invalid tenant, key,
  or value is an error and changes nothing.
- `Get`: missing key → `ErrNotFound` (wrapping is allowed). Returns a copy. An empty stored value is returned
  as empty, not as missing.
- `Put`: creates or replaces. A `nil` value is stored as empty. Last write wins.
- `Delete`: missing key → `nil`.
- `CompareAndSwap`: one atomic step against every other operation on the same key, also across processes that
  share the store. It writes only when the stored bytes equal `old` exactly: a `nil` `old` matches only a missing
  key, and a non-nil empty `old` matches only a present empty value, never a missing key. A `nil` `value` deletes
  the key; an empty non-nil `value` stores an empty value. It returns `true` when it wrote (or deleted), `false`
  with a `nil` error when the comparison failed, and changes nothing then. The value limit of `Put` applies. It
  stores a copy. Because `Get` returns a non-nil slice for a present empty value, `Get`'s result is always a valid
  `old` for the same key.
- `Add`: a missing key counts as 0. Atomic against concurrent `Add` on the same key. `delta` may be negative
  or 0. Overflow of `int64` → error, value unchanged. Non-counter bytes at the key → error, value unchanged.
  A counter is only the canonical decimal form that `Add` itself writes: an optional `-`, then digits without a
  leading zero. `+7`, `007`, `-0`, a space or an empty value are non-counter bytes, even though they parse as a number.
- `Tenants`: every tenant that owns at least one key at the time of the call, `SystemTenant` never included,
  ascending, no duplicates **[default: sorted]**. No keys → empty slice, `nil` error. A tenant whose last key was
  deleted, or that was removed by `DeleteTenant`, is no longer listed. It returns tenant IDs only, never keys or
  values. Cost is O(keys) in the memory driver; it is called once per sweep.
- `DeleteTenant`: removes every key of `t` and nothing of any other tenant. A second call → `nil`.
  `DeleteTenant(SystemTenant)` → error **[default]**: no uninstall can remove the App-wide budget counter.

Validation errors are plain errors with the prefix `store:`. They carry the tenant and the key length, never the value.

### Conformance suite

`storetest` is a transparent folder of this module. It exports one function, in prose:
`storetest.Run(t *testing.T, newStore func() store.Store)`. It runs every case of the Integration Contract
and Boundary test lists below against a fresh store per case. `store`'s own tests call it with `NewMemory`.
Every driver package `store/<driver>` must call it in its tests.

## Integrations

`store` imports only the standard library. It depends on no internal module. Its own contract is the shared
knowledge of every entry below; nothing is restated because this file is the normative home.

### gate/inputs

- **Counterpart**: internal/gate/inputs/
- **Direction**: `gate/inputs` depends on `store`
- **Strength**: Contract
- **LCA / Rank / Distance**: root / 2 / 2
- **Volatility**: High (the gate side)
- **Balanced?**: Yes
- **Shared knowledge**: `Tenant`, `Store.Get`, `ErrNotFound` from the Public Contract above. The consumer reads
  one key, `entitlement`, under `Tenant(installationID)`. Any error, including `ErrNotFound`, means "not suspended"
  on its side. `store` knows nothing about that rule.

### gate/feedback

- **Counterpart**: internal/gate/feedback/
- **Direction**: `gate/feedback` depends on `store`
- **Strength**: Contract
- **LCA / Rank / Distance**: root / 2 / 2
- **Volatility**: High (the gate side)
- **Balanced?**: Yes
- **Shared knowledge**: `Tenant`, `Store.Get`, `Store.CompareAndSwap`, `ErrNotFound` and the `CompareAndSwap`
  semantics above, including "a `nil` value deletes". The consumer holds a lease per head SHA at
  `feedback/lease/<head_sha>` under `Tenant(installationID)`: it reads the key, then swaps from the bytes it read,
  and releases by swapping its own bytes for `nil`. It relies on the swap being atomic across every instance that
  shares the store; a driver that is atomic only within one process breaks its guarantee silently. Any error means
  "no lease" on its side, and it then writes nothing. `store` knows nothing about leases.

### control/account

- **Counterpart**: internal/control/account/
- **Direction**: `control/account` depends on `store`
- **Strength**: Contract
- **LCA / Rank / Distance**: root / 2 / 2
- **Volatility**: Moderate
- **Balanced?**: Yes
- **Shared knowledge**: the whole Public Contract. It uses `Put`/`Get` for `entitlement`, `account`, `consent/<kind>`;
  `Add` on `SystemTenant` key `llm-budget/<YYYY-MM>` (reserve, roll back, settle); `DeleteTenant` on uninstall.

### gate and control façades

- **Counterpart**: internal/gate/, internal/control/
- **Direction**: both depend on `store` (a `Store` field in their `Deps`; they pass it to their children)
- **Strength**: Contract
- **LCA / Rank / Distance**: root / 1 / 1
- **Volatility**: High (`gate`), Moderate (`control`)
- **Balanced?**: Yes
- **Shared knowledge**: the `Store` interface type. The gate path calls only `Get` and, for the publication leases
  of `gate/feedback`, `CompareAndSwap`. The control façade also
  calls `Tenants` in its sweep and reads the `SystemTenant` constant, which it skips before it compares the tenants
  with GitHub's installation list.

### app

- **Counterpart**: internal/app/
- **Direction**: `app` depends on `store`
- **Strength**: Contract (constructor)
- **LCA / Rank / Distance**: root / 1 / 1
- **Volatility**: Low
- **Balanced?**: Yes
- **Shared knowledge**: `NewMemory` when `ARCHFIT_STORE=memory`; later a driver constructor that returns `Store`.

### Future KV platform (external)

- **Counterpart**: a managed KV product, reached through a child package `internal/store/<driver>/` added after `docs/tech-stack.md`
- **Direction**: the driver depends on `store` (implements `Store`); `store` itself never imports its drivers; `app` selects one
- **Strength**: Contract
- **LCA / Rank / Distance**: store / 1 / 1 for driver → `store`; the platform itself is outside the tree (highest distance), so only the driver holds its SDK
- **Volatility**: Moderate (unchosen platform)
- **Balanced?**: Yes
- **Shared knowledge**: the Public Contract, the semantics table, the key layout `<tenant>/<key>`, the decimal counter
  encoding, and `storetest.Run`. A platform that cannot do an atomic add (compare-and-swap, increment, or a
  serializable transaction on one key) cannot host this interface. `CompareAndSwap` must be atomic across every
  App instance, not only within one process: Cloud Run may run several instances at once (docs/design/cloud). On
  Firestore that is a transaction that reads the document and commits under its `updateTime` precondition, or a
  `currentDocument.exists=false` precondition for a `nil` `old`; a client-side lock does not qualify. The
  persistent driver is not built yet; until it is, `storetest` cannot prove this across processes, so the driver
  needs one test that races two clients of the same database.

## Change Vectors

- Choosing the KV platform: a new `store/<driver>` package plus one line in `app`. `store` then becomes a parent;
  its contract does not change.
- Raising key or value limits.
- Replacing the memory map with a sharded or per-tenant structure.
- Adding conformance cases when a driver shows a new failure mode.

Not supported without a contract change, and unwanted: listing keys, TTL, multi-key transactions. A lease whose
holder crashed stays as an expired value until the next lease on that head takes it over, or until `DeleteTenant`;
a platform TTL on `feedback/lease/` keys is allowed as a driver detail and never changes the contract.

## Constraints and Invariants

- Tenant isolation is structural. No operation accepts a key without a tenant; no operation returns data of a
  tenant other than the one passed. Callers build `Tenant` only from a verified webhook, verified OIDC claims,
  or an authorized session; never from a client-supplied ID. `store` cannot check that.
- `Tenants` is the only enumeration. It serves the sweep alone (compare with GitHub's installation list, then
  `DeleteTenant` the leftovers). No request path may call it, and it never exposes a key or a value.
- The App persists nothing except keys under an installation prefix and the system budget counter. No report,
  source, queue, or job table. `store` must not grow features that invite them.
- Values are opaque. `store` never logs a value or a key; logs carry tenant ID, operation, outcome, timing only.
- Safe for concurrent use. `Add` and `CompareAndSwap` are linearizable per key. Other operations are last-write-wins.
- Failure is an error, never a default value. `store` does not decide what an outage means; `gate/inputs` reads
  an error as "not suspended", `control/account` reads an error in consent as "no consent".
- Standard library only in this package. A driver's SDK lives in the driver package only. An implementation
  package of the seam (`store/<driver>`, and `store/storetest`) imports `store`, which declares the interface;
  `store` itself never imports its drivers.
- Storage types stay separate from wire contracts: `store` imports no `wire/*` package and knows no record shape.
- Memory data is lost on exit. It is not a production driver for private-repository tenants: trials would restart.

## Test Specification

### Unit Tests

- **Test name**: `TestKeyPathLayout`
  **Scenario**: table: (4711, `entitlement`), (0, `llm-budget/2026-09`), (1, `consent/managed_llm`), (12, `a`).
  **Expected behavior**: `4711/entitlement`, `0/llm-budget/2026-09`, `1/consent/managed_llm`, `12/a`.
- **Test name**: `TestTenantPrefixDoesNotMatchLongerTenant`
  **Scenario**: memory store with keys for tenants 1, 12 and 123; `DeleteTenant(1)`.
  **Expected behavior**: keys of 12 and 123 remain.
- **Test name**: `TestCounterEncodingIsDecimalASCII`
  **Scenario**: `Add(t, k, 42)`, then `Get(t, k)`; `Put(t, k, "7")`, then `Add(t, k, 1)`.
  **Expected behavior**: `Get` returns `42`; the second `Add` returns 8.
- **Test name**: `TestMemoryCopiesBytes`
  **Scenario**: `Put` a slice, mutate it; `Get`, mutate the result; `Get` again.
  **Expected behavior**: the stored value never changes.

### Integration Contract Tests

All cases live in `storetest.Run` and run against `NewMemory` and every driver.

- **Test name**: `TestConformance/GetPutDelete`
  **Scenario**: table: get missing; put then get; put twice; put `nil`; put empty; delete then get; delete missing.
  **Expected behavior**: `ErrNotFound` by `errors.Is`; the value; the last value; empty and `nil` error; empty and `nil` error (a non-nil empty slice); `ErrNotFound`; `nil`.
- **Test name**: `TestConformance/CompareAndSwap`
  **Scenario**: table: `nil` old over a missing key; `nil` old over a present key; empty old over a missing key; empty old over an empty value; `nil` old over an empty value; a mismatched old; a prefix of the value as old; the matching old; an empty value; a `nil` value; a `nil` value over a missing key with a non-nil old; `nil` old and `nil` value over a missing key. Then the caller's value slice is mutated after a swap, and another tenant swaps the same key.
  **Expected behavior**: true and created; false, unchanged; false, still missing; true; false; false, unchanged; false, unchanged; true, replaced; true, stored as empty; true, deleted; false; true, still missing. The stored value is a copy; the other tenant never matches the first tenant's bytes and creates its own key.
- **Test name**: `TestConformance/CompareAndSwapIsAtomic`
  **Scenario**: 25 rounds; in each, 20 goroutines swap the same key from the previous round's value (`nil` in the first round) to the round number, with `-race`.
  **Expected behavior**: exactly one winner per round; the final value is the last round's.
- **Test name**: `TestConformance/Add`
  **Scenario**: table: missing key +5; then −2; then 0; negative result (−10).
  **Expected behavior**: 5; 3; 3; −7. Each return equals a following `Get` parsed as decimal.
- **Test name**: `TestConformance/AddIsAtomic`
  **Scenario**: 50 goroutines × 100 `Add(+1)` on one key, run with `-race`.
  **Expected behavior**: final value 5000; every returned value is unique.
- **Test name**: `TestConformance/TenantIsolation` (T08)
  **Scenario**: tenant A puts `k`; tenant B gets, deletes, and adds to `k`; A gets `k`.
  **Expected behavior**: B gets `ErrNotFound`; B's delete and add do not change A's value; A reads its own bytes.
- **Test name**: `TestConformance/DeleteTenant` (T08, uninstall)
  **Scenario**: keys under A, B and `SystemTenant`; `DeleteTenant(A)` twice.
  **Expected behavior**: every key of A is `ErrNotFound`; B's keys and the `SystemTenant` counter are unchanged; second call is `nil`.
- **Test name**: `TestConformance/Tenants`
  **Scenario**: table: empty store; keys under 7, 3 and `SystemTenant`; two keys under 3; after `Delete` of 7's only
  key; after `DeleteTenant(3)`; a counter created by `Add` under 9.
  **Expected behavior**: empty slice; `[3, 7]`; 3 listed once; `[3]`; `[]`; `[9]`. `SystemTenant` is never listed.
- **Test name**: `TestConformance/ConcurrentUse`
  **Scenario**: mixed `Get`/`Put`/`Delete`/`DeleteTenant`/`Tenants` from 20 goroutines with `-race`.
  **Expected behavior**: no race, no panic; every `Get` returns a value that was put, or `ErrNotFound`.

### Boundary Tests

- **Test name**: `TestConformance/InvalidInput`
  **Scenario**: table: empty key; 513-byte key; key with NUL; negative tenant; 64 KiB + 1 value (`Put` and `CompareAndSwap`); applied to every operation that takes the input.
  **Expected behavior**: an error for each; no key is created or changed.
- **Test name**: `TestConformance/AddRejectsNonCounterAndOverflow`
  **Scenario**: `Put(k, "abc")` then `Add(k, 1)`; the same with an empty value and with the non-canonical `+7`, `007`, `-0` and ` 7`;
  `Put(k, "9223372036854775807")` then `Add(k, 1)`; same with the minimum and −1.
  **Expected behavior**: error each time; `Get` returns the bytes put.
- **Test name**: `TestConformance/DeleteSystemTenantRefused`
  **Scenario**: `Add(SystemTenant, "llm-budget/2026-09", 10)`, then `DeleteTenant(SystemTenant)`.
  **Expected behavior**: error; the counter still reads 10.
- **Test name**: `TestConformance/CancelledContext`
  **Scenario**: every operation with an already cancelled context, `CompareAndSwap` both as a replace and as a delete.
  **Expected behavior**: an error matching `context.Canceled`; no state change.
- **Test name**: `TestStoreImportsOnlyStdlib`
  **Scenario**: `go list -deps ./internal/store/...`.
  **Expected behavior**: no internal package other than `store` and `store/storetest`; no third-party module.
- **Test name**: `TestErrorsCarryNoValues`
  **Scenario**: trigger every validation error with a marker string in key and value.
  **Expected behavior**: no error text contains the marker.

### Behavior Tests

- **Test name**: `TestUninstallRemovesOneTenantOnly`
  **Scenario**: two installations with `entitlement`, `account` and `consent/source_processing` keys and a system budget counter; uninstall the first.
  **Expected behavior**: the first has no keys; the second and the counter are intact.
- **Test name**: `TestMissedUninstallIsFoundBySweep`
  **Scenario**: keys under installations 1, 2, 3 and a system budget counter; GitHub (the caller's list) still has 1 and 3;
  the caller deletes every tenant from `Tenants` that is not in that list.
  **Expected behavior**: `Tenants` returned `[1, 2, 3]`; after the pass 2 has no keys; 1, 3 and the counter are intact;
  a second pass deletes nothing.
- **Test name**: `TestBudgetReserveRollbackSettle`
  **Scenario**: the caller pattern of `control/account`: `Add(+max)`, over cap → `Add(−max)`; under cap → `Add(actual − reserved)`; two callers race for the last budget.
  **Expected behavior**: the counter equals the sum of settled costs; at most one racer stays under the cap.
- **Test name**: `TestOutageIsAnErrorNotADefault`
  **Scenario**: a failing `Store` double (returns an I/O error) used through the interface.
  **Expected behavior**: every operation returns the error; nothing returns a zero value with `nil` error. Callers decide the meaning.

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

- Define `Tenant`, `SystemTenant`, `Store` and `ErrNotFound`: six operations, opaque byte values.
- Make tenant scoping structural: every key operation takes a `Tenant` and none crosses tenants.
- List the tenants that own keys (`Tenants`), so the sweep can forget an installation whose uninstall webhook was missed.
- Provide `NewMemory`, the in-process implementation used by tests, local runs, and as `ARCHFIT_STORE=memory`.
- Provide an atomic integer `Add` so the budget counter needs no transaction.
- Provide `DeleteTenant` so uninstall is one call.
- Export the conformance suite `storetest.Run` **[default]** so every future driver passes the same cases.

Not provided, on purpose: listing keys, TTL, transactions beyond `Add`, schema, migrations, value encoding.

## Subdomain Classification

Generic. Key-value storage is a solved problem; the module only fixes the smallest interface the App needs.
Functional volatility is low: the six operations follow from decisions.md §2 "Own state". Implementation
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
`control/account`), `account` and `consent/<kind>` (private to `control/account`), and under `SystemTenant`
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
- **Shared knowledge**: the `Store` interface type. The gate path only ever calls `Get`. The control façade also
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
  serializable transaction on one key) cannot host this interface.

## Change Vectors

- Choosing the KV platform: a new `store/<driver>` package plus one line in `app`. `store` then becomes a parent;
  its contract does not change.
- Raising key or value limits.
- Replacing the memory map with a sharded or per-tenant structure.
- Adding conformance cases when a driver shows a new failure mode.

Not supported without a contract change, and unwanted: listing keys, TTL, multi-key transactions.

## Constraints and Invariants

- Tenant isolation is structural. No operation accepts a key without a tenant; no operation returns data of a
  tenant other than the one passed. Callers build `Tenant` only from a verified webhook, verified OIDC claims,
  or an authorized session; never from a client-supplied ID. `store` cannot check that.
- `Tenants` is the only enumeration. It serves the sweep alone (compare with GitHub's installation list, then
  `DeleteTenant` the leftovers). No request path may call it, and it never exposes a key or a value.
- The App persists nothing except keys under an installation prefix and the system budget counter. No report,
  source, queue, or job table. `store` must not grow features that invite them.
- Values are opaque. `store` never logs a value or a key; logs carry tenant ID, operation, outcome, timing only.
- Safe for concurrent use. `Add` is linearizable per key. Other operations are last-write-wins.
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
  **Expected behavior**: `ErrNotFound` by `errors.Is`; the value; the last value; empty and `nil` error; empty and `nil` error; `ErrNotFound`; `nil`.
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
  **Scenario**: table: empty key; 513-byte key; key with NUL; negative tenant; 64 KiB + 1 value; applied to every operation that takes the input.
  **Expected behavior**: an error for each; no key is created or changed.
- **Test name**: `TestConformance/AddRejectsNonCounterAndOverflow`
  **Scenario**: `Put(k, "abc")` then `Add(k, 1)`; the same with an empty value and with the non-canonical `+7`, `007`, `-0` and ` 7`;
  `Put(k, "9223372036854775807")` then `Add(k, 1)`; same with the minimum and −1.
  **Expected behavior**: error each time; `Get` returns the bytes put.
- **Test name**: `TestConformance/DeleteSystemTenantRefused`
  **Scenario**: `Add(SystemTenant, "llm-budget/2026-09", 10)`, then `DeleteTenant(SystemTenant)`.
  **Expected behavior**: error; the counter still reads 10.
- **Test name**: `TestConformance/CancelledContext`
  **Scenario**: every operation with an already cancelled context.
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

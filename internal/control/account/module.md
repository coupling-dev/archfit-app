# Account

**Path**: internal/control/account/
**Parent**: internal/control/ (control plane façade)
**Submodules**: none (leaf)

## Purpose

`account` owns every byte the App persists: the entitlement state of an installation (public, trial,
grace, manual grant, suspended), administrator consent records, and the App-wide managed-LLM budget
counter. It is the only writer of the cached `Entitlement` record the gate reads. Without it private
repositories would never leave the trial, managed features would run without consent, and the LLM
spend would have no cap.

## Functional Responsibilities

- Start the trial on the first private repository of an installation; never restart it.
- Compute the plan from one small record and a clock: trial 30 d, grace 7 d, manual grant, suspension.
- Rewrite the gate's `Entitlement` record on every `Observe`, `Grant`, `Refresh`: suspension instant,
  warning instant, notice text, suspension reason with the recovery action.
- Apply and clear operator grants; log each grant by installation ID.
- Record, revoke and answer consent per installation and kind. No consent, no call.
- Reserve and settle the managed-LLM budget atomically against a hard monthly cap.
- Delete every key of an installation on uninstall.

## Subdomain Classification

Supporting. Needed, not differentiating. Volatility **moderate**: trial length, grace, warning schedule
and the cap are **[default]** values (decisions.md §9, §7) expected to change before launch; a billing
provider may replace manual grants. The `Entitlement` record shape is stable: it is the gate's contract.

## Encapsulated Knowledge

- The private record, Store key `account`, JSON `{"first_private_at": RFC 3339, "grant_until": RFC 3339}`, zero times omitted.
- The durations: trial 30 d, grace 7 d, warn 7 d before, "1 day left" inside the last 24 h. **[default]**
- The plan state machine and the mapping from plan to `Entitlement` fields and texts.
- Consent key layout `consent/<kind>`, value JSON `{"kind","by","at"}`.
- Budget key layout `llm-budget/<YYYY-MM>` (UTC month of `now`) under `SystemTenant`; cap `20_000_000` micro-USD (USD 20). **[default]**
- Reserve-then-compensate as the way to enforce a cap with only `Store.Add`.

Nobody else reads the `account`, `consent/*` or `llm-budget/*` keys. The gate reads only `EntitlementKey`.

## Public Contract

```go
type Plan string

const (
    PlanPublic    Plan = "public"    // no private repositories: free
    PlanTrial     Plan = "trial"     // 30 days from the first private-repository installation
    PlanGrace     Plan = "grace"     // 7 days after the trial
    PlanGranted   Plan = "granted"   // manual beta grant
    PlanSuspended Plan = "suspended"
)

type Status struct {
    Plan         Plan
    TrialEnds    time.Time
    SuspendAfter time.Time
    GrantUntil   time.Time
}

type Account interface {
    // Observe records that the installation has (or no longer has) private repositories and
    // starts the trial on the first private one. It rewrites the gate's Entitlement record.
    Observe(ctx context.Context, installationID int64, hasPrivate bool, now time.Time) error
    Status(ctx context.Context, installationID int64, now time.Time) (Status, error)
    // Grant sets a manual beta grant. Operator-only; audited in the log by installation ID.
    Grant(ctx context.Context, installationID int64, until time.Time, now time.Time) error
    // Refresh recomputes the Entitlement record of one installation (called by the sweep).
    Refresh(ctx context.Context, installationID int64, now time.Time) error
    // Forget deletes every key of the installation (uninstall).
    Forget(ctx context.Context, installationID int64) error
}
```

```go
type ConsentKind string

const (
    ConsentSourceProcessing ConsentKind = "source_processing" // managed discovery
    ConsentManagedLLM       ConsentKind = "managed_llm"
)

type Consent struct {
    Kind ConsentKind
    By   string // GitHub login of the administrator
    At   time.Time
}

// ErrNoConsent is returned by callers' guards when Has is false.
var ErrNoConsent = errors.New("account: consent not recorded")

type Consents interface {
    Record(ctx context.Context, installationID int64, c Consent) error
    Revoke(ctx context.Context, installationID int64, k ConsentKind) error
    // Has is false on any error: no consent, no call.
    Has(ctx context.Context, installationID int64, k ConsentKind) bool
}
```

```go
var ErrBudgetExhausted = errors.New("account: managed LLM budget exhausted")

// Budget is the App-wide managed-LLM hard cap: USD 20 per calendar month (UTC), in micro-USD.
type Budget interface {
    // Reserve atomically books maxCost; ErrBudgetExhausted if the cap would be exceeded.
    Reserve(ctx context.Context, maxCostMicroUSD int64, now time.Time) error
    // Settle returns the unused part of a reservation.
    Settle(ctx context.Context, reservedMicroUSD, actualMicroUSD int64, now time.Time) error
}
```

```go
func New(s Store) (Account, Consents, Budget)
```

`ErrNoConsent` is the sentinel that callers (`onboarding`, `assist`) return from their own guard when `Has` is false; no method of
this module returns it.

`ErrBudgetExhausted` is the sentinel `Reserve` returns when the monthly cap would be exceeded; test it with `errors.Is`.

### Plan rules

All times UTC. `R` is the private record; a missing record is the zero record.

| Derived value  | Rule                                                                      |
| -------------- | ------------------------------------------------------------------------- |
| `TrialEnds`    | zero if `R.first_private_at` is zero, else `first_private_at + 30 d`      |
| grace end      | `TrialEnds + 7 d`                                                         |
| `SuspendAfter` | zero if `first_private_at` is zero, else `max(grace end, R.grant_until)`  |
| paid-through   | `max(TrialEnds, R.grant_until)`                                           |
| `WarnFrom`     | zero if `SuspendAfter` is zero, else paid-through `− 7 d` **[default]**   |

A lapsed grant gets no second grace period. **[default]**

`Plan`, first match: `first_private_at` zero → `public`; `now < grant_until` → `granted`;
`now < TrialEnds` → `trial`; `now < SuspendAfter` → `grace`; else `suspended`.

| Plan      | `Entitlement.Notice` written (dates `YYYY-MM-DD`)                                                                 |
| --------- | ----------------------------------------------------------------------------------------------------------------- |
| public    | `""`                                                                                                              |
| trial     | `"Trial ends <TrialEnds>"`; inside the last 24 h: `"Trial ends <TrialEnds> — 1 day left"`                         |
| granted   | `"Archfit beta access ends <GrantUntil>"`; same 1-day suffix. See the priority rule below                         |
| grace     | `"Trial ended <TrialEnds>. Archfit checks suspend on <SuspendAfter>"`                                             |
| suspended | `"Archfit subscription inactive since <SuspendAfter>"`                                                            |

Priority rule: while `granted`, if `SuspendAfter > GrantUntil` (the grant ends before the grace period does), the trial or
grace text is written instead of the grant text. **[default]** The texts themselves are **[default]**.

`Entitlement.Reason` is always, for a non-public plan: `"Archfit subscription inactive — contact
<support contact> or remove the Archfit requirement in the ruleset"`. The support contact is one
package constant; its value is not in the requirement docs and must be set before launch **[default: placeholder]**.
The gate decides suspension itself from `SuspendAfter`, repository privacy and its own clock; `account` never writes a "suspended" flag.

### Operation rules

- `installationID <= 0` is an error for every operation. `SystemTenant` is never an installation.
- `Observe`: read `R` (`ErrNotFound` → zero). If `hasPrivate` and `first_private_at` is zero, set it to `now` and `Put` `R`.
  `hasPrivate == false` never clears it. Then write `Entitlement`.
- `Grant`: set `grant_until = until` (zero or past `until` clears the grant), `Put` `R`, write `Entitlement`, log `installation_id`, `until`, outcome.
- `Refresh`: read `R`, write `Entitlement`. No change to `R`.
- Backfill: `Observe` on an installation with no record is the normal first call, whether it comes from the installation webhook or,
  when that was missed, from `control.Sweep`. The trial then starts at that call's `now`. Until a record exists the gate reads "not suspended".
- `Status`: read `R`, compute. Missing record → `PlanPublic`, nil error. Writes nothing.
- `Forget`: `Store.DeleteTenant`. Idempotent.
- Any Store read error other than `ErrNotFound` returns the error and **writes nothing**.
- `Record` rejects an unknown `Kind`, empty `By`, or zero `At`. `Revoke` deletes the key; revoking a missing consent is not an error.
- `Reserve`: `maxCostMicroUSD <= 0` is an error. `Add(+max)`; if the result exceeds the cap, `Add(−max)` and return `ErrBudgetExhausted`.
  A Store error is returned as is (not `ErrBudgetExhausted`); the caller makes no LLM call either way.
- `Settle`: `Add(actual − reserved)`. The caller passes the `now` it used for `Reserve`, so both hit the same month key.
  The caller settles every reservation, also after a failed `Complete`, with the cost the `Provider` reported for that outcome
  (it may be 0, usage-based, or the full reservation when the outcome is unknown). `account` only does the arithmetic and never
  guesses a cost. `actual > reserved` is booked as is **[default]**.

## Integrations

### internal/gate

- **Counterpart**: `internal/gate/` (façade; the record is owned by `gate/inputs`, never cited directly)
- **Direction**: `account` depends on the gate's record contract; the gate never imports or calls `account`
- **Strength**: contract
- **LCA / Rank / Distance**: root / 2 / 2
- **Volatility**: moderate
- **Balanced?**: yes. The gate owns the record so it never imports billing code
- **Shared knowledge**: the record, JSON-encoded, under `Tenant(installationID)`:

<!-- contract: Entitlement, EntitlementKey — restated from internal/gate/module.md -->
```go
// EntitlementKey is the Store key, under the installation tenant, of the JSON-encoded Entitlement.
const EntitlementKey = "entitlement"

// Entitlement is the cached subscription state the gate reads. control/account writes it;
// the gate never calls billing code. A missing or unreadable record means "not suspended".
type Entitlement struct {
    SuspendAfter time.Time `json:"suspend_after"` // zero = never suspends
    WarnFrom     time.Time `json:"warn_from"`     // zero = no notice; feedback shows Notice from this instant
    Notice       string    `json:"notice"`        // e.g. "Trial ends 2026-10-20"
    Reason       string    `json:"reason"`        // shown when suspended, with the recovery action
}
```

Informative restatement of a rule owned by `internal/gate` (normative there; on drift the gate wins): suspended = repository private AND `SuspendAfter` non-zero AND `now >= SuspendAfter`.
Public repositories are never suspended. Feedback shows `Notice` from `WarnFrom` on.

### internal/store

- **Counterpart**: `internal/store/`
- **Direction**: `account` depends on `store`
- **Strength**: contract
- **LCA / Rank / Distance**: root / 2 / 2
- **Volatility**: moderate (backend unchosen; interface stable)
- **Balanced?**: yes
- **Shared knowledge**:

<!-- contract: Tenant, SystemTenant — restated from internal/store/module.md -->
```go
// Tenant is the authenticated GitHub installation ID that owns a key.
// Every key is stored under its tenant prefix; no operation crosses tenants.
type Tenant int64

// SystemTenant holds App-wide keys (the managed-LLM budget counter). It is never a GitHub installation ID.
const SystemTenant Tenant = 0
```

<!-- contract: Store, ErrNotFound — restated from internal/store/module.md -->
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

`ErrNotFound` is the sentinel `Get` returns for a missing key; here it means "zero record", never an error.
`Tenants` is never called here; the `control` façade uses it for orphan cleanup and then calls `Forget`.

### Consumers (inbound, contract strength, all balanced)

| Counterpart                    | Uses                                    | LCA / Rank / Distance | Volatility |
| ------------------------------ | --------------------------------------- | --------------------- | ---------- |
| `internal/control/`            | `New`, `Account` (events, sweep, grant) | control / 1 / 1       | High       |
| `internal/control/onboarding/` | `Consents.Has`                          | control / 1 / 1       | High       |
| `internal/control/assist/`     | `Consents.Has`, `Budget`                | control / 1 / 1       | High       |
| `internal/control/ui/`         | `Account.Status`, `Consents`            | control / 1 / 1       | High       |

## Change Vectors

- Trial, grace, warning durations, notice texts, the cap: constants here.
- A billing provider: a new writer of `grant_until` (or a `paid_until` field) inside this module; `Entitlement` stays.
- Per-installation LLM allowance: a second counter key under the installation tenant.
- Consent kinds: a new constant and nothing else.

## Constraints and Invariants

- Nothing here is ever called by the gate path. A failure here never changes a gate result. The gate reads only the cached `Entitlement` record this module writes.
- A Store outage never writes or implies suspension: on any read error nothing is written, so the last good record (or none = "not suspended") stands. A KV or billing outage does not establish non-payment (T21).
- Writes are idempotent: same record and same day produce the same bytes. Two concurrent `Observe` calls may both set `first_private_at`; the later `Put` wins, seconds apart. Accepted.
- The trial never restarts within one installation ID: `Observe(false)`, repository removal, and re-adding change nothing. Uninstall deletes the tenant; a new installation ID starts a new trial. **[default, stated limit]**
- Tenant = `Tenant(installationID)`, which callers take from a verified webhook, the installation list, or an authorized session. Never from a client-supplied ID.
- Budget exhaustion and budget Store errors affect only managed-LLM calls. Deterministic checks never consult `Budget`.
- The counter may over-reserve if the compensating `Add` fails. That fails closed (less spend), and resets next month.
- No clock inside: every operation takes `now`. Standard library only (`encoding/json`, `time`, `errors`, `fmt`).
- Logs: installation ID, plan, outcome, timings. Never the consent login in budget logs, never tokens.

## Test Specification

### Unit Tests

- **Test name**: `TestPlanStateMachine`
  **Scenario**: table over `(first_private_at, grant_until, now)`: no record; day 0; day 29; day 30; day 36; day 37; grant to day 60 at day 10, day 59, day 60; grant to day 20 (shorter than grace end) at day 25 and day 37; grant with no private repository; grant cleared at day 40.
  **Expected behavior**: `public`; `trial` ×2; `grace` ×2; `suspended`; `granted` ×2 then `suspended` (no grace after the grant); `trial` then `suspended` with `SuspendAfter` = grace end; `public` with zero `SuspendAfter`; `suspended`.
- **Test name**: `TestEntitlementFields`
  **Scenario**: same table; decode the record written under `EntitlementKey`.
  **Expected behavior**: `SuspendAfter`, `WarnFrom` per the Plan rules; `WarnFrom` = trial end − 7 d in trial; zero times and empty `Notice` for public; `Reason` names the recovery action.
- **Test name**: `TestNoticeTexts`
  **Scenario**: trial at T−8 d, T−7 d, T−23 h; grace; granted at G−23 h; suspended.
  **Expected behavior**: exact strings of the Notice table, including "1 day left".
- **Test name**: `TestBudgetMonthKey`
  **Scenario**: `Reserve` at `2026-09-30T23:59:59Z` and `2026-10-01T00:00:00Z`, and with a non-UTC `now`.
  **Expected behavior**: keys `llm-budget/2026-09` and `llm-budget/2026-10`; month taken after conversion to UTC.

### Integration Contract Tests

- **Test name**: `TestObserveStartsTrialOnce`
  **Scenario**: memory Store; `Observe(false)`, `Observe(true)` at t0, `Observe(true)` at t0+5 d, `Observe(false)` at t0+6 d.
  **Expected behavior**: public first; `TrialEnds` = t0+30 d afterwards and never moves.
- **Test name**: `TestObserveBackfillsMissingRecord`
  **Scenario**: empty Store (webhook missed); `Observe(true)` called by the sweep at t1; and the same with `Observe(false)`.
  **Expected behavior**: `account` and `entitlement` keys exist; trial from t1 with `SuspendAfter` = t1 + 37 d; for `false` a public record with zero times.
- **Test name**: `TestGrantOverridesAndLapses`
  **Scenario**: suspended installation; `Grant(until = now+90 d)`; then `Refresh` after the grant ends.
  **Expected behavior**: `granted` with `SuspendAfter = until`; then `suspended`. One audit log line per `Grant` with the installation ID.
- **Test name**: `TestRefreshIsIdempotent`
  **Scenario**: `Refresh` twice with the same `now`; count `Put` calls and compare bytes.
  **Expected behavior**: identical `Entitlement` bytes; the `account` record untouched.
- **Test name**: `TestConsentRoundTrip`
  **Scenario**: `Has` before `Record`; after `Record`; after `Revoke`; `Revoke` again; the other kind.
  **Expected behavior**: false, true, false, nil error, false.
- **Test name**: `TestBudgetReserveSettle`
  **Scenario**: reserve 5 000 000 four times, a fifth time, settle one with actual 1 000 000, reserve 4 000 000; then settle table for a 5 000 000 reservation: actual 0, actual 2 000 000, actual 5 000 000 (failed call, outcome unknown), actual 6 000 000.
  **Expected behavior**: four succeed (counter exactly at the cap is allowed); fifth `ErrBudgetExhausted` and the counter is back at 20 000 000; after settle 16 000 000; the last reserve succeeds; the settle rows change the counter by −5 000 000, −3 000 000, 0 and +1 000 000: pure arithmetic, no cost is assumed for a failure.
- **Test name**: `TestForgetDeletesTenantOnly`
  **Scenario**: tenants A and B with records and consents, budget activity; `Forget(A)` twice.
  **Expected behavior**: A has no keys; B and the `SystemTenant` counter are unchanged; second call returns nil (T07 cleanup, T08 tenant scoping).

### Boundary Tests

- **Test name**: `TestStoreFailureNeverSuspends`
  **Scenario**: Store `Get` fails during `Observe`, `Refresh`, `Grant`; Store `Put` fails.
  **Expected behavior**: the error is returned; no `Put` follows a failed `Get`; the previous `Entitlement` bytes are unchanged (T21: outage is not non-payment).
- **Test name**: `TestCorruptRecordIsAnError`
  **Scenario**: the `account` key holds invalid JSON.
  **Expected behavior**: `Observe`, `Refresh`, `Status` return an error and write nothing; they do not reset the trial.
- **Test name**: `TestInvalidInstallationID`
  **Scenario**: `installationID` 0 and −1 on every operation of the three interfaces.
  **Expected behavior**: error (or `false` for `Has`); no `SystemTenant` key is read or written.
- **Test name**: `TestConsentRejectsInvalid`
  **Scenario**: unknown kind, empty `By`, zero `At`; `Has` with a failing Store; `Has` with a corrupt value.
  **Expected behavior**: `Record` errors and writes nothing; `Has` is false.
- **Test name**: `TestBudgetRejectsAndFailsClosed`
  **Scenario**: `Reserve(0)`, `Reserve(-1)`; `Add` fails on reserve; the compensating `Add` fails.
  **Expected behavior**: errors; a Store error is not `ErrBudgetExhausted`; after a failed compensation the counter is higher, never lower.
- **Test name**: `TestConcurrentReserveNeverExceedsCap`
  **Scenario**: 100 goroutines reserve 1 000 000 each against a memory Store.
  **Expected behavior**: exactly 20 succeed; final counter 20 000 000.
- **Test name**: `TestOnlyDocumentedKeys`
  **Scenario**: run every operation against a recording Store.
  **Expected behavior**: keys touched are `account`, `entitlement`, `consent/source_processing`, `consent/managed_llm` under the installation, and `llm-budget/<YYYY-MM>` under `SystemTenant`. Nothing else.

### Behavior Tests

- **Test name**: `TestTrialGraceSuspensionTimeline`
  **Scenario**: install with a private repository; hourly `Refresh` with a fake clock over 40 days; evaluate the gate reading rule on each written record for a private and a public repository.
  **Expected behavior**: not suspended through day 36; notice visible from day 23; "1 day left" on day 29; suspended from day 37 for the private repository only; the public repository is never suspended.
- **Test name**: `TestRestoredEntitlementOnlyMovesTheDate`
  **Scenario**: suspended installation receives a grant.
  **Expected behavior**: the record's `SuspendAfter` moves to the grant end; `account` touches no check and no report. Turning the PR green needs a fresh report (gate sweep re-run), never this module.
- **Test name**: `TestBudgetExhaustionTouchesNothingElse`
  **Scenario**: exhaust the budget, then `Observe`, `Refresh`, `Status`, `Has`.
  **Expected behavior**: all succeed; the `Entitlement` record is unaffected (T21: LLM quota exhaustion does not stop deterministic checks).
- **Test name**: `TestReinstallStartsNewTenant`
  **Scenario**: `Forget(A)`, then `Observe(true)` for new installation ID B of the same organization.
  **Expected behavior**: B gets a fresh 30-day trial; A stays empty. Documented limit.

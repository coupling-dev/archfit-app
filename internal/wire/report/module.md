# No-Source State Report

**Path**: internal/wire/report/
**Parent**: internal/wire/ (Wire Contracts)
**Submodules**: none (leaf)

## Purpose

An engine state report is produced in the customer's CI and uploaded to Archfit. The product promise is that no
source code reaches Archfit. This module is where that promise is enforced on the payload: it defines the
transport form of engine state v1 as an allowlist of fields, and a strict decoder that refuses everything else.
Without it, "schema-valid engine JSON" would be mistaken for "no-source payload" (plan T04), and snippets, logs or
raw model output could ride into checks, comments and logs.

## Functional Responsibilities

- Define the transport types for the engine's gate decision, comparison identity, findings, dimensions, repair
  tasks, modules and edges (engine state contract `archfit.architecture-state.v1`, engine v2.3.1).
- `Decode`: strict JSON decoding, size cap, per-field caps, path rules, single-line rule, duplicate-ID rule.
- Preserve what the gate and feedback need, unchanged: `decision.hard_gates`, `unevaluated_required_rules`,
  `measurement_profile`, `gate_reference`, both repair kinds, the validation command, empty `locations`.
- Nothing else. It does not interpret the decision, compare profiles, filter findings, or compute outcomes.

## Subdomain Classification

Published language between `archfit` (engine), `archfit-action` (producer) and the App. Supporting.
Volatility: moderate. It changes with a versioned engine release; the caps are **[default]** and may be tuned in pilots.

## Encapsulated Knowledge

- The allowlist is the type definitions. There is no separate schema file: a key that is not a field of the
  Public Contract types is a decode error. The App-side field names are a root-level **[default]**, to be pinned
  against the released engine schema in plan task T02.
- The payload is the transport projection, not the raw engine state file. The producer must send only allowlisted
  keys; a raw state file with additional keys fails decoding by design.
- One tolerated key outside the types, stated by the contract: a finding may carry `matched_by`, accepted only as
  an empty object and dropped; any other value is an error. Engine v2.3.1 emits `matched_by: {}` (plan T02).
  `Finding` is decoded through an unexported shadow type that adds this key.
- The strict JSON technique. `encoding/json` alone matches keys case-insensitively, keeps the last duplicate key
  and accepts trailing data. `Decode` therefore runs a generic token pre-pass over the whole document first:
  every object key must match `^[a-z][a-z0-9_]*$`, no object repeats a key, nesting depth is at most 16
  **[default]**, and input ends after the top-level object. Then it decodes with `DisallowUnknownFields`.
- The caps below, all unexported. Every number in the collection caps, string rules, path rules and number
  ranges is **[default]**: decisions.md fixes only the 5 MB upload cap.
- Error text policy: an error names the JSON location (for example `findings[12].message`) and the rule, never the value.

### Order of checks

Size over `MaxBytes` → valid UTF-8 → token pre-pass → typed decode with unknown fields disallowed → required
keys present → `schema_version` equals `StateSchemaV1` → collection caps → string rules → path rules → duplicate
finding IDs. The first failure is returned. No partial `Report` is ever returned.

### Presence rules

- Required keys: `schema_version`, `decision`, `comparison`; `null` for any of them is an error.
- Arrays may be absent, `null`, or empty; all three decode to an empty list. Consumers must not distinguish them.
- `measurement_profile` and `gate_reference` absent or `null` decode to `nil`. `nil` is meaningful: unknown identity, no baseline used.

### Collection caps

All values **[default]**.

| Collection                                     | Maximum |
| ---------------------------------------------- | ------- |
| `findings`                                     | 10 000  |
| `modules`                                      | 5 000   |
| `edges`                                        | 50 000  |
| `agent_tasks`                                  | 10 000  |
| `dimensions`                                   | 64      |
| `decision.unevaluated_required_rules`          | 1 000   |
| `locations` per finding                        | 100     |
| `paths` per module                             | 1 000   |
| `finding_ids` per agent task                   | 1 000   |
| `producers`, `unknowns`, `reasons`             | 64 each |
| `validation_command` arguments                 | 64      |

### String rules

Every string is valid UTF-8 without control characters (no CR, LF, tab, NUL). All lengths and patterns are
**[default]**. Classes:

| Class      | Fields                                                                                                           | Rule                                  |
| ---------- | ---------------------------------------------------------------------------------------------------------------- | ------------------------------------- |
| Message    | `Finding.Message`, `AgentTask.Summary`, `UnevaluatedRule.Reason`, `GateReference.Reasons[]`, `PartialBasis`      | at most 500 characters, single line   |
| Identifier | every `ID`, `RuleID`, `FindingIDs[]`, module names (`Module.Name`, `From`, `To`), `Layer`, `Dimension.Name`      | at most 200 characters, single line   |
| Token      | `HardGates`, `Lifecycle`, `RepairKind`, `Origin`, `Status`, `Confidence`, `Trust`, `Completeness`, `Tool`        | `^[A-Za-z0-9_.:/+-]{0,64}$`           |
| Version    | `MeasurementProfile.Version`, `SemanticsVersion`, `ToolVersion`, `Unknowns[]`                                    | Token characters, at most 128         |
| Hash       | `ModelHash`, `LabelsHash`, `RubricHash`, `SettingsHash`                                                          | Token characters, at most 128         |
| Digest     | `Comparison.ConfigHash`, `GateReference.Digest`                                                                  | `^sha256:[0-9a-f]{64}$`               |
| Path       | `Location.Path`, `Module.Paths[]`                                                                                | see path rules                        |
| Argument   | `ValidationCommand[]`                                                                                            | at most 512 characters, single line   |

- Token values are not checked against the enumerations in the type comments. An unrecognized `hard_gates` or
  `lifecycle` decodes; the consumer fails closed on it. Decoding checks shape and the no-source properties only.
- Path rules (512 is **[default]**): non-empty, at most 512 characters, forward slashes only, relative, equal to its own `path.Clean`
  form, not `.`, no `..` segment, no leading `/`, no backslash, no drive prefix (`C:`), no control characters.
- Numbers (ranges are **[default]**): `exit_code` 0 to 255; `line` 0 to 10 000 000; `count` ≥ 0; `coverage` and `delta` any finite number;
  integers must be written without fraction or exponent.
- `validation_command`: no argument equals `--refresh` or starts with `--refresh=`; otherwise kept verbatim, in order.
- Finding `id` values are unique across the whole report, across lifecycles. A duplicate is an error.
  `finding_ids` are not checked against existing findings **[default]**: the synthetic coupling-gate task may reference none.

## Public Contract

```go
// StateSchemaV1 is the engine state contract accepted (engine v2.3.1).
const StateSchemaV1 = "archfit.architecture-state.v1"

// MaxBytes caps an uploaded payload: 5 MiB (the "5 MB" cap of decisions.md).
const MaxBytes = 5 << 20

// Report is the no-source transport form of an engine state report: only allowlisted
// fields exist, so anything else in the payload is a decode error. One exception: a finding
// may carry "matched_by" as an empty object, which is accepted and dropped.
type Report struct {
    SchemaVersion     string      `json:"schema_version"` // must equal StateSchemaV1
    Decision          Decision    `json:"decision"`
    Comparison        Comparison  `json:"comparison"`
    Findings          []Finding   `json:"findings"`
    Dimensions        []Dimension `json:"dimensions"`
    AgentTasks        []AgentTask `json:"agent_tasks"`
    Modules           []Module    `json:"modules"`
    Edges             []Edge      `json:"edges"`
    ValidationCommand []string    `json:"validation_command"` // argv, replayed verbatim; never contains --refresh
}
```

```go
// Decision carries the engine's structured gate decision. ExitCode follows `archfit check`:
// 0 healthy, 1 blocked, 2 attention, 3 error.
type Decision struct {
    ExitCode                 int               `json:"exit_code"`
    HardGates                string            `json:"hard_gates"` // "pass" | "fail" | "unmeasured"
    NeedsAttention           bool              `json:"needs_attention"`
    UnevaluatedRequiredRules []UnevaluatedRule `json:"unevaluated_required_rules,omitempty"`
}

type UnevaluatedRule struct {
    RuleID string `json:"rule_id"`
    Reason string `json:"reason"`
}
```

```go
// Comparison binds the report to the inputs the engine actually used.
type Comparison struct {
    ConfigHash         string              `json:"config_hash"` // "sha256:<hex>" of the raw policy bytes
    ModelHash          string              `json:"model_hash"`
    LabelsHash         string              `json:"labels_hash"`
    RubricHash         string              `json:"rubric_hash"`
    MeasurementProfile *MeasurementProfile `json:"measurement_profile,omitempty"` // nil = unknown identity
    GateReference      *GateReference      `json:"gate_reference,omitempty"`      // nil = no persisted baseline used
}

type MeasurementProfile struct {
    Version      string     `json:"version"`
    SettingsHash string     `json:"settings_hash"`
    Producers    []Producer `json:"producers"`
    Unknowns     []string   `json:"unknowns"`
}

type Producer struct {
    Tool             string `json:"tool"`
    SemanticsVersion string `json:"semantics_version"`
    Status           string `json:"status"`
    ToolVersion      string `json:"tool_version,omitempty"`
    PartialBasis     string `json:"partial_basis,omitempty"`
}

// GateReference describes the persisted baseline the engine gate used.
type GateReference struct {
    Digest     string   `json:"digest"`     // "sha256:<hex>" of the raw baseline bytes
    Comparable bool     `json:"comparable"` // false = non-comparable; Reasons says why
    Reasons    []string `json:"reasons,omitempty"`
}
```

```go
type Finding struct {
    ID        string     `json:"id"`        // unique per report across lifecycles
    RuleID    string     `json:"rule_id"`
    Lifecycle string     `json:"lifecycle"` // "introduced" | "baseline" | "fixed" | "waived" | "expired_waiver"
    Blocking  bool       `json:"blocking"`
    From      string     `json:"from"` // module name, may be ""
    To        string     `json:"to"`   // module name, may be ""
    Message   string     `json:"message"` // engine-authored, length-capped
    Locations []Location `json:"locations"` // may be empty; never invent one
}

type Location struct {
    Path string `json:"path"`
    Line int    `json:"line"` // 0 = whole file
}

// Dimension mirrors the engine's per-dimension status; trust and completeness stay separate.
type Dimension struct {
    Name         string  `json:"name"`
    Status       string  `json:"status"`
    Confidence   string  `json:"confidence"`
    Coverage     float64 `json:"coverage"`
    Delta        float64 `json:"delta"`
    Trust        string  `json:"trust"`        // "customer_attested" in v1
    Completeness string  `json:"completeness"` // "complete" | "partial" | "missing"
}

type AgentTask struct {
    ID         string   `json:"id"`
    RuleID     string   `json:"rule_id"`
    RepairKind string   `json:"repair_kind"` // "code_change" | "needs_owner_decision"
    Origin     string   `json:"origin"`      // presentation only; never exempts a finding
    Summary    string   `json:"summary"`
    FindingIDs []string `json:"finding_ids"`
}
```

```go
type Module struct {
    Name  string   `json:"name"`
    Layer string   `json:"layer,omitempty"`
    Paths []string `json:"paths"`
}

type Edge struct {
    From      string `json:"from"`
    To        string `json:"to"`
    Permitted bool   `json:"permitted"` // allowed by the policy the engine used
    Count     int    `json:"count"`     // observed dependency count
}
```

```go
// Decode strictly parses payload as the no-source transport. It fails on: size over MaxBytes,
// unknown fields, wrong schema_version, strings over the per-field caps, absolute or
// parent-escaping paths, multi-line messages, or duplicate finding IDs.
func Decode(payload []byte) (Report, error)
```

`StateSchemaV1` is the only state schema version accepted. `MaxBytes` is the payload cap. Reading the
"5 MB" of decisions.md §3 as 5 MiB = 5 242 880 bytes is **[default]**; a payload of exactly `MaxBytes` is within the cap. `ExitCode`, `HardGates` and `RepairKind` are
engine-authored values passed through unchanged.

Semantics not visible in the signatures:

- `Decode` returns a zero `Report` with every error. Errors are plain `error` values; the caller maps any of them
  to one problem (`report_invalid`). The message names location and rule only.
- A decoded `Report` owns its memory; it does not alias `payload`.
- `Decode` never drops, reorders, or rewrites findings, tasks, or arguments. The only dropped input is an empty `matched_by`.
- Everything in a decoded `Report` is still customer-attested. Decoding proves form, not truth.

## Integrations

This module imports only the standard library. It depends on no internal module. The entries below are its
consumers (the consumer depends on this module) and its external counterparts. Values are from the root coupling
table; the `internal/gate` row is computed (LCA root, deeper module two levels down).

| Counterpart                      | Direction                         | Strength                      | LCA / Rank / Distance     | Volatility | Balanced? |
| -------------------------------- | --------------------------------- | ----------------------------- | ------------------------- | ---------- | --------- |
| `internal/gate/ingest`           | ingest depends on report          | Contract (versioned schema)   | root / 2 / 2              | High       | Yes       |
| `internal/gate/verdict`          | verdict depends on report         | Contract (published language) | root / 2 / 2              | High       | Yes       |
| `internal/gate/feedback`         | feedback depends on report        | Contract (published language) | root / 2 / 2              | High       | Yes       |
| `internal/gate`                  | gate depends on report            | Contract                      | root / 2 / 2              | High       | Yes       |
| `internal/control/ui`            | ui depends on report              | Contract                      | root / 2 / 2              | High       | Yes       |
| `internal/control/ui/graph`      | graph depends on report           | Contract                      | root / 3 / 3              | Moderate   | Yes       |
| `archfit` engine state (v2.3.1)  | report follows the engine schema  | Contract (versioned schema)   | outside the tree; highest | Moderate   | Yes       |
| `archfit-action` (external repo) | the Action sends this transport   | Contract                      | outside the tree; highest | Moderate   | Yes       |

Shared knowledge, per counterpart:

- `internal/gate/ingest` holds `MaxBytes` (request size cap, `413`), `Decode` and `Report`. A decode error becomes
  the problem `report_invalid`, never a rejection.
- `internal/gate/verdict` holds `Report`, `Decision`, `UnevaluatedRule`, `Comparison`, `GateReference`. It reads
  `ExitCode`, `HardGates`, `UnevaluatedRequiredRules`, `ConfigHash`, and `GateReference`. It never filters by
  `Origin` or `Lifecycle`.
- `internal/gate/feedback` holds all types: findings and locations for annotations, dimensions with trust and
  completeness, agent tasks with `RepairKind`, the measurement profile, and `ValidationCommand` for replay.
- `internal/gate` holds `Report`, `Comparison` and `MeasurementProfile`: a `nil` profile or a version different
  from the manifest entry means the identity is not approved.
- `internal/control/ui` holds `Decode` and `MaxBytes` to read the `archfit-report` workflow artifact for the graph page.
- `internal/control/ui/graph` holds `Report`, `Module`, `Edge` and `Comparison.ConfigHash`.
- `archfit` engine state: this module holds the v1 field names and decision semantics of the pinned release
  (revision `f8877d25ba36d84d3780071580d23486e3d794d0`). It holds no engine Go types (trust-and-reporting.md §7).
- `archfit-action` must upload the transport projection within the caps. Contract coupling through a version
  constant, strict decoding and shared fixtures tolerates the distance of three repositories.

No counterpart contract is used by this module, so nothing is restated here.

## Change Vectors

- A new engine state version: add `StateSchemaV2` and a `DecodeV2` path; v1 stays while approved images emit it.
- Adding an allowlisted field after a privacy review: extend a type, its string class, and the fixtures. The App
  deploys before any producer sends it.
- Tuning caps from pilot evidence. Loosening is compatible; tightening follows the parent's versioning rule.
- Publishing the allowlist as a JSON Schema artifact for producers: add a `Schema` function here.
- Pinning the real engine field names in T02 changes tags here and fixtures, nothing else.

## Constraints and Invariants

- No-source transport (decisions.md §3): IDs, rule IDs, module names, file paths, line numbers, metrics and
  engine-authored messages only. No snippets, logs, prompts, or raw model output. Paths and module names are
  disclosed metadata; that residual exposure is documented, not hidden.
- The allowlist is closed: adding a field is a reviewed contract change, never a decoder relaxation.
- Decoding never judges the decision. Unknown token values pass through for the consumer to fail closed on.
- Enforcement is never filtered by `Origin` or `Lifecycle`; this module keeps every finding it accepts.
- Empty `locations` stay empty. No consumer may invent a location.
- `Decode` is total and bounded: no input panics; work is bounded by `MaxBytes`, the depth cap and the collection caps.
- Errors never contain payload values. Callers log IDs, digests, outcomes and timings only.
- No I/O, no clock, no logging, no globals. Standard library only. No other internal import, including wire siblings.
- Transport types only: a `Report` is never persisted. The App verifies it, publishes feedback and drops it.

## Test Specification

Fixtures live in `internal/wire/testdata/report/` and `internal/wire/testdata/leakage/`, read by relative path.
Each fixture has an `.expect.json` sibling recording its producer artifact and expected outcome (see the parent document).

### Unit Tests

- **Test name**: `TestStringClassRules`
  **Scenario**: table, one row per string class at its limit, one over it, one with LF, one with a tab, one with NUL; token rows
  with a space and with a backtick; digest rows uppercase and 63 hex.
  **Expected behavior**: at-limit rows decode; every other row is an error naming the JSON location.
- **Test name**: `TestPathRules`
  **Scenario**: table for `locations[].path` and `modules[].paths[]`: `internal/a/b.go`; `a.go`; empty; `.`; `/etc/passwd`; `../x`;
  `a/../../x`; `a//b`; `a/./b`; `a\b`; `C:/x`; 513 characters.
  **Expected behavior**: only the first two decode.
- **Test name**: `TestCollectionCaps`
  **Scenario**: generated reports at each cap and one element over it.
  **Expected behavior**: at-cap decodes; over-cap is an error naming the collection.
- **Test name**: `TestMatchedBy`
  **Scenario**: finding with `matched_by` absent; `{}`; `{ }` with whitespace; `{"rule":"x"}`; `[]`; `"text"`; `null`.
  **Expected behavior**: the first three decode to the same `Finding`; the others are errors.
- **Test name**: `TestPresenceRules`
  **Scenario**: table: each required key absent or `null`; each array absent, `null`, `[]`; profile and gate reference absent and `null`.
  **Expected behavior**: required-key rows fail; array variants decode to equal reports; pointers are `nil`.
- **Test name**: `TestDuplicateFindingIDs`
  **Scenario**: the same `id` once as `baseline` and once as `fixed`; the same `id` twice as `introduced`.
  **Expected behavior**: both are errors.
- **Test name**: `TestValidationCommandRules`
  **Scenario**: argv with `--base`, `--lang`, `--require-tools`; argv containing `--refresh`; `--refresh=true`; 65 arguments; an argument with LF.
  **Expected behavior**: the first decodes verbatim and in order; the rest are errors.

### Integration Contract Tests

- **Test name**: `TestDecodeConformanceFixtures`
  **Scenario**: every T02 fixture: healthy; warning-only attention with passing hard gates; blocked; missing required evidence;
  unknown identity; comparable profile; incompatible profile; blocker plus unevaluated required rule; non-applicable rule;
  `locations: []`; `matched_by: {}`; both repair kinds; waiver expiry and order; invalid input.
  **Expected behavior**: each decodes or fails as its expectation says; for decoded ones `ExitCode`, `HardGates`, `NeedsAttention`,
  unevaluated rule IDs, profile presence and version, and `GateReference.Comparable` equal the recorded values.
- **Test name**: `TestDecodePreservesEverything`
  **Scenario**: decode each valid fixture, marshal the `Report`, decode again.
  **Expected behavior**: equal reports; counts of findings, tasks, modules and edges equal the counts in the fixture file.
- **Test name**: `TestSizeCap`
  **Scenario**: a valid report padded to exactly `MaxBytes`, and to `MaxBytes + 1`.
  **Expected behavior**: the first decodes; the second fails before any parsing.
- **Test name**: `TestWrongSchemaVersion`
  **Scenario**: `schema_version` empty, `archfit.architecture-state.v2`, and the envelope's version string.
  **Expected behavior**: errors.

### Boundary Tests

- **Test name**: `TestSeededLeakageFixturesFail` (release test, T04)
  **Scenario**: every file in `testdata/leakage/`: a `snippet` key on a finding; a top-level `logs` blob; a `raw_response` key on an
  agent task; a multi-line message holding code; a 5 000-character message; a `prompt` key in the profile. Each contains `LEAK-7f3a`.
  **Expected behavior**: every one fails to decode; no error string contains the marker.
- **Test name**: `TestUnknownFieldAtEveryLevel`
  **Scenario**: add one unknown key to each object type in turn, including nested ones (`producers[]`, `locations[]`, `edges[]`).
  **Expected behavior**: error for each.
- **Test name**: `TestDecodeRejectsNonStrictJSON`
  **Scenario**: table: key `Findings`; duplicate `decision` key with a healthy second value; trailing data; top-level array; invalid
  UTF-8; nesting deeper than 16; `exit_code` `1.0`, `1e0`, `"1"`, 256, -1; `line` -1.
  **Expected behavior**: every row is an error; none panics.
- **Test name**: `FuzzDecode`
  **Scenario**: fuzz `Decode` seeded with all fixtures.
  **Expected behavior**: never panics; every accepted report satisfies every cap and rule when re-checked, and survives a marshal and decode round trip.
- **Test name**: `TestPackageImportsStdlibOnly`
  **Scenario**: `go list -deps` for the package.
  **Expected behavior**: no internal package, no third-party package, no `net`, `os`, or `log` package.

### Behavior Tests

- **Test name**: `TestBlockerKeepsUnevaluatedRules`
  **Scenario**: the fixture with a known blocker and an unevaluated required rule.
  **Expected behavior**: `HardGates` is `fail` and the unevaluated rule with its reason is still present for feedback.
- **Test name**: `TestUnknownIdentityIsRepresentable`
  **Scenario**: a report without `measurement_profile`.
  **Expected behavior**: decodes with a `nil` profile; this module does not turn it into an error or a default.
- **Test name**: `TestOriginNeverFilters`
  **Scenario**: a blocked fixture whose coupling-gate task has origin `unknown` and whose blocking finding has lifecycle `baseline`.
  **Expected behavior**: both are present after decoding, unchanged.
- **Test name**: `TestRepairKindsSurvive`
  **Scenario**: the fixture with one `code_change` and one `needs_owner_decision` task.
  **Expected behavior**: both tasks decode with their `RepairKind`, `Origin` and `FindingIDs` unchanged.
- **Test name**: `TestUnrecognizedDecisionTokensPassThrough`
  **Scenario**: `hard_gates: "maybe"`, `exit_code: 7`, lifecycle `"snoozed"`.
  **Expected behavior**: decodes unchanged, so the verdict can conclude `action_required`; decoding is not where the decision fails closed.

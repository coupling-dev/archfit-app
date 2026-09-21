# No-Source State Report

**Path**: internal/wire/report/
**Parent**: internal/wire/ (Wire Contracts)
**Submodules**: none (leaf)

## Purpose

An engine state report is produced in the customer's CI and uploaded to Archfit. The product promise is that no
source code reaches Archfit. This module is where that promise is enforced on the payload. It mirrors the engine
state contract `archfit.architecture-state.v1` (engine v2.3.1, `archfit.state.schema.json`) and turns it into a
no-source transport: it knows every key of that schema, keeps an allowlisted subset under length caps, drops the
rest, and refuses anything the schema does not define. Without it, "schema-valid engine JSON" would be mistaken
for "no-source payload" (plan T04): a real report carries source identifiers in `agent_tasks[].declarations`.

## Functional Responsibilities

- Define the transport types for verdict, gate decision, comparison identity, gate reference, dimensions,
  coverage, findings, repair tasks and seams.
- `Decode`: strict JSON decoding against the full v2.3.1 key set, size cap, enum checks, per-field caps, path
  rules, single-line rule, duplicate-ID rule; known but non-allowlisted keys are type-checked and dropped.
- Preserve what the gate and feedback need, unchanged: `verdict`, `decision.hard_gates`,
  `unevaluated_required_rules`, `comparison.config_hash`, `measurement_profile`, `gate_reference`, both repair
  kinds, task validation commands, empty `locations`.
- Nothing else. It does not interpret the decision, compare profiles, filter findings, or compute outcomes.

## Subdomain Classification

Published language between `archfit` (engine), `archfit-action` (producer) and the App. Supporting.
Volatility: moderate. It changes with a versioned engine release; the caps are **[default]** and may be tuned in pilots.

## Encapsulated Knowledge

- The complete key set of the v2.3.1 state schema, pinned at engine revision
  `f8877d25ba36d84d3780071580d23486e3d794d0`, and the split into kept and dropped keys below. Field names and
  enum values in the Public Contract are taken from that schema, not guessed.
- The decoding technique. `Decode` unmarshals into unexported shadow types that mirror the whole schema, dropped
  keys included, with `DisallowUnknownFields`. That gives "unknown key is an error" and the JSON type check of
  dropped keys for free. It then copies the kept fields into the public types. Dropped values never leave the function.
- `encoding/json` alone matches keys case-insensitively, keeps the last duplicate key and accepts trailing data.
  A token pre-pass runs first: every object key of a schema-defined object must match `^[a-z][a-z0-9_]*$`,
  no object repeats a key, nesting depth is at most 16 **[default]**, and input ends after the top-level object.
  Keys of the two free maps (`matched_by`, `measurement.tool_versions`) are exempt from the key pattern, not from the duplicate rule.
- The pre-pass walks the token stream next to the shadow types (by reflection), because `encoding/json` errors
  carry no list index and the unknown-key error carries no location. It requires each key to equal a shadow key
  exactly, which implies the key pattern, and also checks `null`, JSON types and the integer form, so every
  structural error names its exact location. The typed decode that follows stays strict as a second guard.
  Input-controlled names (unknown keys, map keys) are never echoed: a map key is written `*`, except the nine dimension names.
- The caps below, all unexported. Every number in the collection caps, string rules, path rules and number
  ranges is **[default]**: decisions.md fixes only the 5 MB upload cap.
- Error text policy: an error names the JSON location (for example `findings[12].why`) and the rule, never the value.

### Order of checks

Size over `MaxBytes` → valid UTF-8 → token pre-pass → typed decode with unknown fields disallowed → required
keys → `schema_version` → enums → collection caps → string rules → path rules → duplicate finding IDs.
The first failure is returned. No partial `Report` is ever returned.

### Dropped keys

Known to the decoder, checked for JSON type, never copied into a `Report`. A key not in the Public Contract types
and not in this list is a decode error.

| Location                      | Dropped keys                                                                     |
| ----------------------------- | -------------------------------------------------------------------------------- |
| root                          | `measurement` (`source_ref`, `history_depth`, `history_window`, `tool_versions`) |
| `findings[]`                  | `matched_by` (map of string to string; populated in real reports)                |
| `agent_tasks[]`               | `declarations` (list of syntax facts, below)                                     |
| `dimensions.*.metrics[]`      | `denominator` (`observed`, `total`), `provenance` (list of strings)              |
| `gate_reference`              | every comparison key except `status`, `base_ref`, `reasons`                      |
| `seams[]`                     | the seam keys listed below                                                       |

- A syntax fact has `language`, `file`, `module`, `kind`, `name`, `framework` (strings), `exported` (boolean),
  `start_line`, `end_line`, `count` (integers). `name` is a source identifier: the closest thing to source in a
  report. Declarations are always dropped, whatever they contain.
- Dropped seam keys: `scored_edges`, `abstained_edges`, `critical_edges`, `high_or_worse_edges`, `critical_share_pct`,
  `high_or_worse_share_pct` (integers); `volatility_provenance`, `quadrant`, `label_evidence_hash`, `confidence`,
  `role_expectation`, `hypothesis` (strings); `labels` (list of strings); `distributed_monolith` (boolean);
  `raw_distance` (object: `level`, `basis`, `from_owner`, `to_owner`, `from_deploy_unit`, `to_deploy_unit` strings,
  `same_owner`, `same_deploy_unit` booleans, `boundary_crossings`, `shared_ancestor` integers); `scores` (object: `n`,
  `min`, `median`, `max` integers, `p10`, `p90` integer or `null`, `mean` number).
- Dropped keys may be absent even where the engine schema requires them **[default]**, so a producer may strip them before upload.
- Dropped strings carry no length cap of their own; `MaxBytes` bounds them.

### Presence rules

- Kept keys follow the engine schema's `required` lists: every key of the Public Contract types without
  `omitempty` must be present, at every level. Dropped keys are never required, including the schema-required
  `measurement`, `matched_by` and seam counters.
- `null` is an error everywhere except `seams[].scores.p10` and `p90` **[default]**. The engine emits `[]`, never `null`, for empty lists.
- `gate_reference` and `measurement_profile` absent decode to `nil`. `nil` is meaningful: the engine emitted no
  baseline comparison; the measurement identity is unknown.
- `dimensions` has exactly the nine keys `intent`, `structure`, `modularity`, `coupling`, `change_locality`,
  `complexity`, `testability`, `operations`, `drift`; each value's `name` equals its key **[default]**.

### Enums

Values outside the set are decode errors. These are the enums the engine schema enforces:

| Field                                  | Values                                          |
| -------------------------------------- | ----------------------------------------------- |
| `verdict`                              | `healthy`, `needs_attention`, `blocked`         |
| `decision.hard_gates`                  | `pass`, `fail`, `unmeasured`                    |
| `comparison.status`                    | `not_requested`, `comparable`, `non_comparable` |
| `gate_reference.status`                | same three values                               |
| `dimensions.*.status`                  | `measured`, `partial`, `unmeasured`             |
| `agent_tasks[].repair_kind`            | `code_change`, `needs_owner_decision`           |
| `producers[].partial_basis`, if given  | `unresolved_specifiers`, `degraded_precision`   |

Other value sets named in the type comments (`Finding.Status`, `Severity`, `Kind`, `Producer.Status`, `Dimension.Gate`,
`AgentTask.Origin`) are plain strings in the engine schema. They are checked as tokens only **[default]**; a consumer
that meets a value it does not know fails closed or shows it as text.

### Collection caps

All values **[default]**.

| Collection                                                          | Maximum |
| ------------------------------------------------------------------- | ------- |
| `findings`                                                          | 10 000  |
| `agent_tasks`                                                       | 10 000  |
| `seams`                                                             | 50 000  |
| `decision.unevaluated_required_rules`                               | 1 000   |
| `locations` per finding                                             | 100     |
| `allowed_alternatives` per finding                                  | 32      |
| `constraints`, `validation` per task                                | 32 each |
| `files` per task                                                    | 1 000   |
| `metrics`, `unknown` per dimension                                  | 256     |
| `findings` (references) per dimension                               | 10 000  |
| `new_findings`, `resolved_findings` per delta                       | 10 000  |
| `producers`, `unknowns`, `coverage.tools`, every `reasons` list     | 64 each |

### String rules

Every kept string is valid UTF-8 without control characters (no CR, LF, tab, NUL). All lengths and character sets are
**[default]**. Classes:

- Text, at most 500 characters, single line: `Finding.Why`, `Finding.Constraint`, `AllowedAlternatives[]`,
  `AgentTask.Goal`, `AgentTask.Constraints[]`, `UnknownFact.Fact`, `UnknownFact.Reason`, `UnevaluatedRule.Reason`,
  `ToolCoverage.Reason`, `DimensionCoverage.Basis`, and every `Reasons[]` list (`Comparison`, `Reference`, `Delta`,
  `TaskOriginReasons`). Real engine text contains quotes, backticks and dashes, so no character set applies.
- Command, at most 512 characters, single line: `AgentTask.Validation[]`. No whitespace-separated word equals
  `--refresh` or starts with `--refresh=`. Kept verbatim and in order.
- Identifier, at most 200 characters, single line: every `ID`, `FindingID`, `RuleID`, `NewFindings[]`,
  `ResolvedFindings[]`, module names (`Endpoint.Module`, `Seam.FromModule`, `Seam.ToModule`), `Dimension.Name`,
  `Dimension.Owner`, `UnknownFact.Owner`, `Metric.Name`, `Metric.Unit`, `MetricDelta.Name`, `Tool`, `BaseRef`.
- Token, at most 64 characters from letters, digits, space and `_ . : / + -`: `Status`, `Kind`, `Severity`,
  `Confidence`, `Gate`, `Origin`, `Strength`, `Distance`, `Volatility`, `TaskOriginStatus`, `Edge.Kind`. The space
  is needed: a producer status is literally `timed out`.
- Version, token characters, at most 128: `MeasurementProfile.Version`, `SemanticsVersion`, `ToolVersion`,
  `RubricVersion`, `Unknowns[]`.
- Hash: `Comparison.ConfigHash` is `""` or `^[0-9a-f]{64}$`, bare hex without a `sha256:` prefix. `ModelHash`,
  `LabelsHash`, `SettingsHash` are token characters, at most 128.
- Path: `Location.File`, `AgentTask.Files[]`, and `Endpoint.Path` when not empty. Non-empty, at most 512
  characters, forward slashes only, relative, equal to its own `path.Clean` form, not `.`, no `..` segment, no leading
  `/`, no backslash, no drive prefix (`C:`). A directory or a glob such as `pkg/a/**` is a valid path.
- Numbers (ranges are **[default]**): `line` 0 to 10 000 000; every count ≥ 0; metric values any finite number;
  integers must be written without fraction or exponent.
- Finding `id` values are unique across the report. `finding_id` and finding references are not checked against
  existing findings **[default]**: the synthetic coupling-gate task may reference none.

## Public Contract

```go
// StateSchemaV1 is the engine state contract accepted (engine v2.3.1, archfit.state.schema.json).
const StateSchemaV1 = "archfit.architecture-state.v1"

// MaxBytes caps an uploaded payload: 5 MiB (the "5 MB" cap of decisions.md).
const MaxBytes = 5 << 20

// Report is the no-source transport form of an engine state report. The decoder knows every key of the
// v2.3.1 state schema: keys listed here are kept (length-capped), the other known keys are validated and
// dropped (measurement, findings[].matched_by, agent_tasks[].declarations, metric provenance, seam scores),
// and any key the schema does not define is a decode error.
type Report struct {
    SchemaVersion string               `json:"schema_version"` // must equal StateSchemaV1
    Verdict       string               `json:"verdict"`        // "healthy" | "needs_attention" | "blocked"
    Decision      Decision             `json:"decision"`
    Comparison    Comparison           `json:"comparison"`
    GateReference *Reference           `json:"gate_reference,omitempty"` // persisted-baseline comparison; nil = engine emitted none
    Dimensions    map[string]Dimension `json:"dimensions"`               // exactly the nine engine dimension keys
    Coverage      Coverage             `json:"coverage"`
    Findings      []Finding            `json:"findings"`
    AgentTasks    []AgentTask          `json:"agent_tasks"`
    Seams         []Seam               `json:"seams"`
}
```

```go
// Decision is the engine's structured gate decision. The process exit code is not part of a report:
// healthy, needs_attention and blocked are Report.Verdict; an engine error produces no report at all.
type Decision struct {
    HardGates                string            `json:"hard_gates"` // "pass" | "fail" | "unmeasured"
    UnevaluatedRequiredRules []UnevaluatedRule `json:"unevaluated_required_rules,omitempty"`
    ActiveBlockers           int               `json:"active_blockers"`
    AttentionDimensions      int               `json:"attention_dimensions"`
    UnknownDimensions        int               `json:"unknown_dimensions"`
}

type UnevaluatedRule struct {
    RuleID string `json:"rule_id"`
    Reason string `json:"reason"`
}
```

```go
// Comparison binds the report to the inputs the engine actually used (report-only --base comparison).
type Comparison struct {
    Status             string              `json:"status"` // "not_requested" | "comparable" | "non_comparable"
    BaseRef            string              `json:"base_ref,omitempty"`
    ConfigHash         string              `json:"config_hash,omitempty"` // bare lowercase hex SHA-256 of the raw policy bytes
    ModelHash          string              `json:"model_hash,omitempty"`
    LabelsHash         string              `json:"labels_hash,omitempty"` // canonical hash of approved labels; not reproducible from raw bytes
    RubricVersion      string              `json:"rubric_version,omitempty"`
    MeasurementProfile *MeasurementProfile `json:"measurement_profile,omitempty"` // nil = unknown identity
    Reasons            []string            `json:"reasons"`
    TaskOriginStatus   string              `json:"task_origin_status,omitempty"`
    TaskOriginReasons  []string            `json:"task_origin_reasons,omitempty"`
}

// Reference is the engine gate's comparison with the persisted baseline (top-level gate_reference).
// The engine exposes no baseline digest: only whether the baseline it loaded was comparable, and why not.
type Reference struct {
    Status  string   `json:"status"`             // "comparable" | "non_comparable" | "not_requested"
    BaseRef string   `json:"base_ref,omitempty"` // "baseline"
    Reasons []string `json:"reasons"`            // e.g. "no baseline file was loaded"
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
    Status           string `json:"status"` // "ok" | "partial" | "absent" | "disabled" | "timed out"
    ToolVersion      string `json:"tool_version,omitempty"`
    PartialBasis     string `json:"partial_basis,omitempty"` // "unresolved_specifiers" | "degraded_precision"
}
```

```go
type Finding struct {
    ID                  string     `json:"id"`        // 32-hex fingerprint, unique per report
    Kind                string     `json:"kind"`      // "gate" | "advisory"
    RuleID              string     `json:"rule_id"`
    Status              string     `json:"status"`    // "new" | "baseline" | "waived" | "expired_waiver" | "fixed"
    Severity            string     `json:"severity"`  // "critical" | "high" | "medium" | "low"
    Confidence          string     `json:"confidence"`
    Edge                Edge       `json:"edge"`
    Locations           []Location `json:"locations"` // may be empty; never invent one
    Why                 string     `json:"why"`        // engine-authored, single line, length-capped
    Constraint          string     `json:"constraint"` // engine-authored, single line, length-capped
    AllowedAlternatives []string   `json:"allowed_alternatives,omitempty"`
}

type Edge struct {
    From Endpoint `json:"from"`
    To   Endpoint `json:"to"`
    Kind string   `json:"kind"`
}

type Endpoint struct {
    Module string `json:"module"` // may be ""
    Path   string `json:"path"`   // may be ""
}

type Location struct {
    File string `json:"file"`
    Line int    `json:"line"` // 0 = whole file
}

type AgentTask struct {
    FindingID   string   `json:"finding_id"`
    RuleID      string   `json:"rule_id"`
    RepairKind  string   `json:"repair_kind"` // "code_change" | "needs_owner_decision"
    Goal        string   `json:"goal"`
    Constraints []string `json:"constraints"`
    Files       []string `json:"files"`
    Validation  []string `json:"validation"`       // shell command strings, replayed verbatim; never contain --refresh
    Origin      string   `json:"origin,omitempty"` // "introduced" | "pre_existing" | "unknown"; presentation only
}
```

```go
// Dimension mirrors the engine's per-dimension state. Completeness is Status; measurement trust is not an
// engine field: in v1 every fact is customer-attested and feedback says so.
type Dimension struct {
    Name       string            `json:"name"`
    Owner      string            `json:"owner"`
    Status     string            `json:"status"` // "measured" | "partial" | "unmeasured"
    Confidence string            `json:"confidence"`
    Gate       string            `json:"gate"` // "pass" | "warn" | "fail" | "not_applicable"
    Coverage   DimensionCoverage `json:"coverage"`
    Metrics    []Metric          `json:"metrics"`
    Findings   []FindingRef      `json:"findings"`
    Unknown    []UnknownFact     `json:"unknown"`
    Delta      *Delta            `json:"delta,omitempty"`
}

type DimensionCoverage struct {
    Basis    string `json:"basis"`
    Observed int    `json:"observed"`
    Total    int    `json:"total"`
}

type Metric struct {
    Name  string  `json:"name"`
    Value float64 `json:"value"`
    Unit  string  `json:"unit"`
}

type FindingRef struct {
    ID       string `json:"id"`
    RuleID   string `json:"rule_id"`
    Kind     string `json:"kind"`
    Severity string `json:"severity"`
    Status   string `json:"status"`
}

type UnknownFact struct {
    Fact   string `json:"fact"`
    Reason string `json:"reason"`
    Owner  string `json:"owner"`
}

type Delta struct {
    Status           string        `json:"status"`
    Reasons          []string      `json:"reasons,omitempty"`
    Metrics          []MetricDelta `json:"metrics,omitempty"`
    NewFindings      []string      `json:"new_findings,omitempty"`
    ResolvedFindings []string      `json:"resolved_findings,omitempty"`
}

type MetricDelta struct {
    Name   string  `json:"name"`
    Before float64 `json:"before"`
    After  float64 `json:"after"`
    Change float64 `json:"change"`
}

type Coverage struct {
    Measured   int            `json:"measured"`
    Partial    int            `json:"partial"`
    Unmeasured int            `json:"unmeasured"`
    Tools      []ToolCoverage `json:"tools"`
}

type ToolCoverage struct {
    Tool   string `json:"tool"`
    Status string `json:"status"`
    Reason string `json:"reason,omitempty"`
}

// Seam is an observed module-to-module relationship. Only these fields are kept.
type Seam struct {
    ID         string `json:"id"`
    FromModule string `json:"from_module"`
    ToModule   string `json:"to_module"`
    Edges      int    `json:"edges"` // observed dependency count
    Strength   string `json:"strength"`
    Distance   string `json:"distance"`
    Volatility string `json:"volatility"`
    Severity   string `json:"severity,omitempty"`
}
```

```go
// Decode strictly parses payload as the no-source transport. It fails on: size over MaxBytes, a key the
// v2.3.1 state schema does not define, wrong schema_version, an enum value outside its set, strings over
// the per-field caps, absolute or parent-escaping paths, multi-line text, or duplicate finding IDs.
func Decode(payload []byte) (Report, error)
```

`StateSchemaV1` is the only state schema version accepted. `MaxBytes` is the payload cap. Reading the "5 MB" of
decisions.md §3 as 5 MiB = 5 242 880 bytes is **[default]**; a payload of exactly `MaxBytes` is within the cap.
`HardGates` and `RepairKind` are engine-authored values passed through unchanged.

Semantics not visible in the signatures:

- `Decode` returns a zero `Report` with every error. Errors are plain `error` values; the caller maps any of them
  to one problem (`report_invalid`). The message names location and rule only.
- A decoded `Report` owns its memory; it does not alias `payload`.
- `Decode` never drops, reorders, or rewrites findings, tasks, seams, or validation commands. The only dropped
  input is the dropped-key list. Marshalling a `Report` yields a document without any dropped key.
- There is no exit code in a report. Healthy, attention and blocked are `Verdict`; an engine error yields no
  report, which the gate sees as a missing upload or an invalid payload.
- `Reference` carries no digest. The baseline and labels digests the producer used are stated in the upload
  envelope and compared by the gate; this module knows nothing about them.
- Everything in a decoded `Report` is still customer-attested. Decoding proves form, not truth.

## Integrations

This module imports only the standard library. It depends on no internal module. The entries below are its
consumers (the consumer depends on this module) and its external counterparts. Values are from the root coupling
table; the `internal/gate` row is computed (LCA root, deeper module two levels down).

| Counterpart                      | Direction                        | Strength                      | LCA / Rank / Distance     | Volatility | Balanced? |
| -------------------------------- | -------------------------------- | ----------------------------- | ------------------------- | ---------- | --------- |
| `internal/gate/ingest`           | ingest depends on report         | Contract (versioned schema)   | root / 2 / 2              | High       | Yes       |
| `internal/gate/verdict`          | verdict depends on report        | Contract (published language) | root / 2 / 2              | High       | Yes       |
| `internal/gate/feedback`         | feedback depends on report       | Contract (published language) | root / 2 / 2              | High       | Yes       |
| `internal/gate`                  | gate depends on report           | Contract                      | root / 2 / 2              | High       | Yes       |
| `internal/control/ui`            | ui depends on report             | Contract                      | root / 2 / 2              | High       | Yes       |
| `internal/control/ui/graph`      | graph depends on report          | Contract                      | root / 3 / 3              | Moderate   | Yes       |
| `archfit` engine state (v2.3.1)  | report mirrors the engine schema | Contract (versioned schema)   | outside the tree; highest | Moderate   | Yes       |
| `archfit-action` (external repo) | the Action uploads engine state  | Contract                      | outside the tree; highest | Moderate   | Yes       |

Shared knowledge, per counterpart:

- `internal/gate/ingest` holds `MaxBytes` (request size cap, `413`), `Decode` and `Report`. A decode error becomes
  the problem `report_invalid`, never a rejection.
- `internal/gate/verdict` holds `Report`, `Decision`, `UnevaluatedRule`, `Comparison`, `Reference`. It reads `Verdict`,
  `HardGates`, `UnevaluatedRequiredRules`, `ConfigHash` and `GateReference.Status`. It never filters by
  `AgentTask.Origin` or `Finding.Status`.
- `internal/gate/feedback` holds all types: findings with edges and locations for annotations, dimensions with
  status and confidence, agent tasks with `RepairKind` and `Validation`, the measurement profile, coverage.
- `internal/gate` holds `Report`, `Comparison` and `MeasurementProfile`: a `nil` profile or a version different
  from the manifest entry means the identity is not approved.
- `internal/control/ui` holds `Decode` and `MaxBytes` to read the `archfit-report` workflow artifact for the graph page.
- `internal/control/ui/graph` holds `Report`, `Seam` and `Comparison.ConfigHash`.
- `archfit` engine state: this module holds the whole v2.3.1 state schema key set, its enums and required lists,
  pinned at revision `f8877d25ba36d84d3780071580d23486e3d794d0`. It holds no engine Go types (trust-and-reporting.md §7).
- `archfit-action` uploads the engine's state file bytes as they are, or with dropped keys stripped. Contract
  coupling through a version constant, strict decoding and shared fixtures tolerates the distance of three repositories.

No counterpart contract is used by this module, so nothing is restated here.

## Change Vectors

- A new engine state version: add `StateSchemaV2` and a `DecodeV2` path; v1 stays while approved images emit it.
- The engine adds a key within v1: add it to the shadow types as dropped, or, after a privacy review, as kept with
  a string class and fixtures. Until then reports carrying it fail closed as `report_invalid`.
- Tuning caps from pilot evidence. Loosening is compatible; tightening follows the parent's versioning rule.
- Promoting a dropped key to kept (for example metric denominators) after a privacy review.
- Publishing the kept subset as a JSON Schema artifact for producers: add a `Schema` function here.

## Constraints and Invariants

- No-source transport (decisions.md §3): IDs, rule IDs, module names, file paths, line numbers, metrics and
  engine-authored messages only. No snippets, logs, prompts, or raw model output. Paths and module names are
  disclosed metadata; that residual exposure is documented, not hidden.
- Source identifiers never survive decoding: `declarations` and `matched_by` are dropped always, not when convenient.
- The kept set is closed: keeping another field is a reviewed contract change, never a decoder relaxation.
- Decoding never judges the decision. It checks the schema's enums and the no-source properties; outcome logic is the gate's.
- Enforcement is never filtered by `Origin` or finding `Status`; this module keeps every finding it accepts.
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
  with a backtick and with `timed out`; `config_hash` rows: empty, 64 hex, uppercase, 63 hex, with a `sha256:` prefix.
  **Expected behavior**: at-limit rows, `timed out`, empty and 64-hex hashes decode; every other row is an error naming the JSON location.
- **Test name**: `TestPathRules`
  **Scenario**: table for `locations[].file`, `agent_tasks[].files[]` and `edge.from.path`: `pkg/a/a.go`; `pkg/b`; `pkg/a/**`; empty;
  `.`; `/etc/passwd`; `../x`; `a/../../x`; `a//b`; `a/./b`; `a\b`; `C:/x`; 513 characters.
  **Expected behavior**: the first three decode everywhere; empty decodes only for an endpoint path; the rest are errors.
- **Test name**: `TestEnums`
  **Scenario**: one row per schema enum with each allowed value and one unknown value; plus unknown values for `Finding.Status`, `Severity` and `Origin`.
  **Expected behavior**: allowed values decode; unknown schema-enum values are errors; unknown plain-string values decode unchanged.
- **Test name**: `TestCollectionCaps`
  **Scenario**: generated reports at each cap and one element over it.
  **Expected behavior**: at-cap decodes; over-cap is an error naming the collection.
- **Test name**: `TestPresenceRules`
  **Scenario**: table: each kept required key absent, at every level; each as `null`; `gate_reference` and `measurement_profile` absent;
  `matched_by`, `measurement` and `declarations` absent; `dimensions` with eight keys, with a tenth key, with `name` different from its key.
  **Expected behavior**: required-key, `null` and dimension rows fail; optional and dropped-key rows decode; the two pointers are `nil`.
- **Test name**: `TestDroppedKeysAreTypeChecked`
  **Scenario**: `matched_by` as a list and with a numeric value; `declarations` as an object; `measurement.history_depth` as a string; a
  seam `scores.p10` as `null`, as 3, as `"3"`; an unknown key inside `raw_distance`.
  **Expected behavior**: only `p10` `null` and 3 decode.
- **Test name**: `TestDuplicateFindingIDs`
  **Scenario**: the same `id` once as `baseline` and once as `fixed`; the same `id` twice as `new`.
  **Expected behavior**: both are errors.
- **Test name**: `TestValidationCommandRules`
  **Scenario**: `archfit check -c .archfit.yaml --base main --lang go --require-tools`; a command with `--refresh`; with `--refresh=true`;
  with `--refreshed`; 513 characters; a command with LF; 33 commands.
  **Expected behavior**: the first and `--refreshed` decode verbatim; the rest are errors.

### Integration Contract Tests

- **Test name**: `TestDecodeRealEngineReport`
  **Scenario**: the fixture captured from engine v2.3.1 (`real-blocked-no-baseline`): verdict `blocked`, `hard_gates: fail`, one active
  blocker, populated `matched_by`, `gate_reference` `non_comparable` with reason `no baseline file was loaded`, nine dimensions, one seam.
  **Expected behavior**: decodes; those values, the bare-hex `config_hash`, profile version `archfit.measurement.v1` and the task's
  validation command `archfit check -c .archfit.yaml` equal the recorded expectation.
- **Test name**: `TestDecodeConformanceFixtures`
  **Scenario**: every T02 fixture: healthy; warning-only attention with passing hard gates; blocked; missing required evidence;
  unknown identity; comparable profile; incompatible profile; blocker plus unevaluated required rule; non-applicable rule;
  `locations: []`; populated `matched_by`; both repair kinds; waiver expiry and order; invalid input.
  **Expected behavior**: each decodes or fails as its expectation says; for decoded ones `Verdict`, `HardGates`, unevaluated rule IDs,
  profile presence and version, and `GateReference.Status` equal the recorded values.
- **Test name**: `TestMarshalledReportHasNoDroppedKey`
  **Scenario**: decode each valid fixture, marshal the `Report`, inspect the JSON, decode it again.
  **Expected behavior**: no `matched_by`, `declarations`, `measurement`, `provenance`, `denominator`, `hypothesis` or `scores` key anywhere;
  the second decode equals the first; counts of findings, tasks and seams equal the counts in the fixture file.
- **Test name**: `TestEngineSchemaKeySetIsCovered`
  **Scenario**: walk the pinned `archfit.state.schema.json` copy in `testdata/` and the shadow types by reflection.
  **Expected behavior**: every schema property is a shadow field, and the reverse; kept plus dropped equals the schema key set.
- **Test name**: `TestSizeCap`
  **Scenario**: a valid report padded (inside a dropped string) to exactly `MaxBytes`, and to `MaxBytes + 1`.
  **Expected behavior**: the first decodes; the second fails before any parsing.
- **Test name**: `TestWrongSchemaVersion`
  **Scenario**: `schema_version` empty, `archfit.architecture-state.v2`, and the envelope's version string.
  **Expected behavior**: errors.

### Boundary Tests

- **Test name**: `TestSeededLeakageFixturesFail` (release test, T04)
  **Scenario**: every rejecting file in `testdata/leakage/`: a `snippet` key on a finding; a top-level `logs` blob; a `raw_response` key on
  an agent task; a multi-line `why` holding code; a 5 000-character `goal`; a `prompt` key in the profile. Each contains `LEAK-7f3a`.
  **Expected behavior**: every one fails to decode; no error string contains the marker.
- **Test name**: `TestDeclarationsNeverSurvive` (release test, T04)
  **Scenario**: the leakage fixture whose task has a `declarations` entry named `LEAK-7f3a_handler`, whose finding has `matched_by` values
  with the marker, and whose seam `hypothesis` has it too.
  **Expected behavior**: decodes; the marker appears nowhere in the `Report`, in its marshalled JSON, or in its `%+v` formatting.
- **Test name**: `TestUnknownFieldAtEveryLevel`
  **Scenario**: add one unknown key to each schema object in turn, kept and dropped ones (`producers[]`, `locations[]`, `edge.from`,
  `declarations[]`, `raw_distance`, `scores`, `measurement`).
  **Expected behavior**: error for each.
- **Test name**: `TestDecodeRejectsNonStrictJSON`
  **Scenario**: table: key `Findings`; duplicate `decision` key with a passing second value; duplicate key inside `matched_by`; trailing
  data; top-level array; invalid UTF-8; nesting deeper than 16; `line` `1.0`, `1e0`, `"1"`, -1; a legacy `exit_code` key in `decision`.
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
  **Scenario**: a report without `measurement_profile`; a report without `gate_reference`; a report without `config_hash`.
  **Expected behavior**: all decode, with `nil` pointers and an empty hash; this module does not turn them into errors or defaults.
- **Test name**: `TestOriginNeverFilters`
  **Scenario**: a blocked fixture whose coupling-gate task has origin `unknown` and whose blocking finding has status `baseline`.
  **Expected behavior**: both are present after decoding, unchanged.
- **Test name**: `TestRepairKindsSurvive`
  **Scenario**: the fixture with one `code_change` and one `needs_owner_decision` task.
  **Expected behavior**: both tasks decode with `RepairKind`, `Origin`, `Files` and `Validation` unchanged and in order.
- **Test name**: `TestWaiverStatusesSurvive`
  **Scenario**: the waiver fixture with one `waived` and one `expired_waiver` finding.
  **Expected behavior**: both decode with their status; the expired one is not hidden or re-labelled.

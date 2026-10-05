# No-Source State Report

**Path**: internal/wire/report/
**Parent**: internal/wire/ (Wire Contracts)
**Submodules**: none (leaf)

## Purpose

An engine state report is produced in the customer's CI and uploaded to Archfit. The product promise is that no
source code reaches Archfit. This module is where that promise is enforced on the payload. It mirrors the engine
state contract `archfit.architecture-state.v1` (engine v2.3.1, `archfit.state.schema.json`) as each engine release
it reads emits it, and turns it into a no-source transport: it knows every key of that release's schema, keeps an
allowlisted subset under length caps, drops the rest, and refuses anything the schema does not define. Without it,
"schema-valid engine JSON" would be mistaken for "no-source payload" (plan T04): a real report carries source
identifiers in `agent_tasks[].declarations`.

## Functional Responsibilities

- Define the transport types for verdict, gate decision, comparison identity, gate reference, dimensions,
  coverage, findings, repair tasks and seams.
- `Decode`: strict JSON decoding against the key set of the engine release the caller names, size cap, enum
  checks, per-field caps, path rules, single-line rule, duplicate-ID rule; known but non-allowlisted keys are
  type-checked and dropped.
- `EngineVersions`: the engine releases `Decode` reads, so that a consumer can tie an approved identity to a key set.
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
- The release table: which engine releases `Decode` reads and which release added each key (see "Engine releases
  and key sets" below).
- The decoding technique. `Decode` unmarshals into unexported shadow types that mirror the whole schema, dropped
  keys included, with `DisallowUnknownFields`. That gives "unknown key is an error" and the JSON type check of
  dropped keys for free. It then copies the kept fields into the public types. Dropped values never leave the function.
  The shadow types are the union of the key sets of every listed release; the pre-pass, not the typed decode,
  limits a document to the key set of its own release.
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
  The engine release is input too: an unknown one is reported as `engine_version` and the rule, never echoed.

### Engine releases and key sets

`Decode` takes the engine release whose document it reads; the gate passes the `engine_version` of the upload
envelope. Each listed release has a key set, and the document must fit that key set exactly: a key the release does
not define is an unknown key, as before. A release that is not listed is a decode error, so its uploads end
`action_required` (`report_invalid`), never in a guessed key set.

| Release  | Key set                                                           |
| -------- | ----------------------------------------------------------------- |
| `v2.3.1` | the pinned v2.3.1 state schema (`internal/wire/testdata/schema/`) |
| `v2.4.0` | the v2.3.1 key set                                                |

- v2.4.0 adds rule types, finding kinds, the config key `guard` and unevaluated-rule reasons of the form
  `selector matches nothing: <side> <glob>`, but no state key (owner statement). A rule ID is an identifier, a
  finding kind a token and a reason text, none of them an enum here. Whether every v2.4.0 value also fits its
  string class is not shown by that statement: only a real v2.4.0 capture proves it.
- Verified against the release: `archfit.state.schema.json` at tag v2.4.0 (revision
  `5aefcdc2c5465c2eb7198ea91394bd8cb8ad2b22`) is byte-identical to the pinned v2.3.1 file (SHA-256
  `acd9a09e…`), and the unmodified capture `real-blocked-no-baseline-v240` has exactly the key paths of the v2.3.1
  capture and decodes as a v2.4.0 document. The manifest approves v2.4.0 with its image digests; the parent's
  `TestEveryApprovedEngineVersionHasACapture` holds that capture to it.
- A key a later release adds is a shadow field tagged `since:"<release>"`; untagged fields exist in every release.
  The pre-pass reads the tag when it looks a key up and treats a key of a later release as unknown. A tag that
  names no listed release makes the key unknown under every release. Within `archfit.architecture-state.v1` a
  release only adds optional keys, so the newest key set holds every older one.
- A key the App starts to keep is a kept field with the tag; keeping it is still a reviewed contract change.
- **Frozen decision core.** The gate decides only from fields every listed release defines: `schema_version`,
  `verdict`, `decision.hard_gates`, `decision.unevaluated_required_rules` (`rule_id`, `reason`),
  `comparison.config_hash`, `comparison.measurement_profile.version`, `gate_reference.status` and
  `gate_reference.reasons`, plus `findings[].id`, `kind` and `status` for `Active`. None of them may carry a
  `since` tag. A new way to block, or a new value in one of their enums, is a new state schema version, not a key.

### Order of checks

Engine release listed → size over `MaxBytes` → valid UTF-8 → token pre-pass under the release's key set → typed
decode with unknown fields disallowed → required keys → `schema_version` → enums → collection caps → string rules →
path rules → duplicate finding IDs. The first failure is returned. No partial `Report` is ever returned.

### Dropped keys

Known to the decoder, checked for JSON type, never copied into a `Report`. A key not in the Public Contract types
and not in this list is a decode error.

| Location                 | Dropped keys                                                                     |
| ------------------------ | -------------------------------------------------------------------------------- |
| root                     | `measurement` (`source_ref`, `history_depth`, `history_window`, `tool_versions`) |
| `findings[]`             | `matched_by` (map of string to string; populated in real reports)                |
| `agent_tasks[]`          | `declarations` (list of syntax facts, below)                                     |
| `dimensions.*.metrics[]` | `denominator` (`observed`, `total`), `provenance` (list of strings)              |
| `gate_reference`         | every comparison key except `status`, `base_ref`, `reasons`                      |
| `seams[]`                | the seam keys listed below                                                       |

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

| Field                                 | Values                                          |
| ------------------------------------- | ----------------------------------------------- |
| `verdict`                             | `healthy`, `needs_attention`, `blocked`         |
| `decision.hard_gates`                 | `pass`, `fail`, `unmeasured`                    |
| `comparison.status`                   | `not_requested`, `comparable`, `non_comparable` |
| `gate_reference.status`               | same three values                               |
| `dimensions.*.status`                 | `measured`, `partial`, `unmeasured`             |
| `agent_tasks[].repair_kind`           | `code_change`, `needs_owner_decision`           |
| `producers[].partial_basis`, if given | `unresolved_specifiers`, `degraded_precision`   |

Other value sets named in the type comments (`Finding.Status`, `Severity`, `Kind`, `Producer.Status`, `Dimension.Gate`,
`AgentTask.Origin`) are plain strings in the engine schema. They are checked as tokens only **[default]**; a consumer
that meets a value it does not know fails closed or shows it as text.

An empty `partial_basis` (`""`) is accepted and means the same as an absent key: the enum is checked only for a
non-empty value **[default]**.

### Collection caps

All values **[default]**. The rule behind them: `MaxBytes` is the limit real engine output meets first, and a list
cap only stops a pathological shape. A list the engine composes from another list is capped from that list's cap,
so a finding that decodes never yields a task that does not. Sizes below are measured on the real captures of
`internal/wire/testdata/report/`: a finding costs at least about 580 bytes with its dimension reference (461 and
125), an advisory about 920, a seam about 706, a location about 30 and more with a real path. The engine itself
caps none of these lists.

| Collection                                                      | Maximum | Why this number                                                |
| --------------------------------------------------------------- | ------- | -------------------------------------------------------------- |
| `findings`                                                      | 10 000  | about 9 000 findings of the smallest real size fill `MaxBytes` |
| `agent_tasks`                                                   | 10 000  | one per active gate finding                                    |
| `seams`                                                         | 50 000  | about 7 400 real seams fill `MaxBytes`                         |
| `decision.unevaluated_required_rules`                           | 1 000   | one per policy rule                                            |
| `locations` per finding                                         | 10 000  | one per file of a module, see below                            |
| `allowed_alternatives` per finding                              | 32      | none emitted by v2.3.1                                         |
| `constraints` per task                                          | 34      | alternatives plus 2, see below                                 |
| `validation` per task                                           | 32      | one command in real output                                     |
| `files` per task                                                | 10 002  | locations plus 2, see below                                    |
| `metrics`, `unknown` per dimension; `metrics` per delta         | 256     | 15 metrics at most in the real captures                        |
| `findings` (references) per dimension                           | 10 000  | one per finding                                                |
| `new_findings`, `resolved_findings` per delta                   | 10 000  | one per finding                                                |
| `producers`, `unknowns`, `coverage.tools`, every `reasons` list | 64 each | 13 producers with every analyzer on; 3 unknown sentences       |

- `locations`: module-scoped rules (`public_api_max`, `syntax_api_size_ceiling`) list one location per file of the
  module. Real reports had 106 and 91; the former cap of 100 turned a real blocked report into `report_invalid`.
  10 000 locations cost 0.3 to 0.8 MiB, so a finding at the cap still leaves room for the rest of the report.
- `files` per task: the engine's task files are every location file plus the two edge endpoints, so the cap is
  the locations cap plus 2. `constraints` per task: the finding constraint, one per allowed alternative and the
  target module's public surface, so the alternatives cap plus 2. The former 32 rejected a task whose finding had
  31 or 32 alternatives.
- `seams` and `findings`: a real report reaches `MaxBytes` long before either cap. 3 059 seams with one advisory
  each measured 5.71 MB, over the cap.

Above `MaxBytes` the upload is refused before any parsing, and the cap stays: `gate/ingest` answers 413
`payload_too_large` and publishes nothing. With `gate: required` the pending `archfit/gate` check then becomes
`action_required` (`missing_upload`) at the sweep, 60 minutes after it started. A report too large to read fails
closed; it is never read in part.

### String rules

Every kept string is valid UTF-8 without control characters (no CR, LF, tab, NUL) and without the Unicode line
separators U+2028 and U+2029, which are not control characters but break a single line. All lengths and character
sets are **[default]**. Classes:

- Text, at most 500 characters, single line: `Finding.Why`, `Finding.Constraint`, `AllowedAlternatives[]`,
  `UnknownFact.Fact`, `UnknownFact.Reason`, `UnevaluatedRule.Reason`, `ToolCoverage.Reason`,
  `DimensionCoverage.Basis`, and every `Reasons[]` list (`Comparison`, `Reference`, `Delta`, `TaskOriginReasons`).
  Real engine text contains quotes, backticks and dashes, so no character set applies.
- Task text, at most 4 096 characters, single line: `AgentTask.Goal`, `AgentTask.Constraints[]`. The engine
  composes them from other fields: a goal is a template with both edge paths (up to about 1 200 characters) or
  `Break the import cycle: ` and the why; a constraint is the finding constraint, `allowed alternative: ` and an
  alternative, or `public surface of module "<module>": [<every public glob>]`. That last one is unbounded: a real
  one with 21 globs was 765 characters, so the 500 of Text rejected a real blocked report. 4 096 holds every
  fixed composition and about 100 public globs at the observed 35 characters each.
- Command, at most 512 characters, single line: `AgentTask.Validation[]`. No whitespace-separated word equals
  `--refresh` or starts with `--refresh=`. Kept verbatim and in order.
- Identifier, at most 200 characters, single line: every `ID`, `FindingID`, `RuleID`, `NewFindings[]`,
  `ResolvedFindings[]`, module names (`Endpoint.Module`, `Seam.FromModule`, `Seam.ToModule`), `Dimension.Name`,
  `Dimension.Owner`, `UnknownFact.Owner`, `Metric.Name`, `Metric.Unit`, `MetricDelta.Name`, `Tool`, `BaseRef`.
- Token, at most 64 characters from letters, digits, space and `_ . : / + -`: `Status`, `Kind`, `Severity`,
  `Confidence`, `Gate`, `Origin`, `Strength`, `Distance`, `Volatility`, `TaskOriginStatus`, `Edge.Kind`. The space
  is needed: a producer status is literally `timed out`.
- Version, token characters, at most 128: `MeasurementProfile.Version`, `SemanticsVersion`, `RubricVersion`,
  `Unknowns[]`. These are engine identifiers; the three unknown sentences of v2.3.1 fit the class.
- Tool version, at most 128 characters, single line, every character printable (`unicode.IsPrint`, so no format
  character such as a bidi override or a zero-width space and no space but U+0020): `Producer.ToolVersion`. It is
  the tool's own version output, so free text: the engine emits `go1.27.1` (`go version` without the platform),
  `18.5.0` (dependency-cruiser), `grimp 3.17; python 3.14.3` (the grimp helper),
  `uv 0.12.21 (Homebrew 2026-09-29 aarch64-apple-darwin)` or `Python 3.12.4` (grimp when the helper fails),
  `cargo 1.98.0 (797e8a9bc 2026-08-05)`, `cargo-modules 0.26.0`, `rust-analyzer 1.98.0` or `scip-python 0.6.6`
  (scip, parenthetical removed by the engine), `ast-grep 0.45.3`, `jscpd 5.4.0`, `git version 2.56.0` or
  `git version 2.54.0 (Apple Git-157)`, and the comma-joined parser versions of supplied coverage. The engine's
  own profile comparison needs these strings verbatim, so they are not normalized anywhere. A multi-line version
  output is still rejected.
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
// Decode strictly parses payload as the no-source transport of the state document engineVersion emits. It
// fails on: an engineVersion outside EngineVersions, size over MaxBytes, a key that release's state schema
// does not define, wrong schema_version, an enum value outside its set, strings over the per-field caps,
// absolute or parent-escaping paths, multi-line text, or duplicate finding IDs.
func Decode(payload []byte, engineVersion string) (Report, error)
```

```go
// EngineVersions returns the engine releases whose state document Decode reads, oldest first. Each emits
// StateSchemaV1; a later release only adds optional keys, so the last key set holds every earlier one.
func EngineVersions() []string
```

```go
// Active reports whether the finding currently blocks: a gate finding that is new or whose waiver expired.
// It is the single definition of "active gate finding" for every consumer.
func (f Finding) Active() bool
```

`StateSchemaV1` is the only state schema version accepted. `MaxBytes` is the payload cap. Reading the "5 MB" of
decisions.md §3 as 5 MiB = 5 242 880 bytes is **[default]**; a payload of exactly `MaxBytes` is within the cap.
`HardGates` and `RepairKind` are engine-authored values passed through unchanged.

Semantics not visible in the signatures:

- `Decode` returns a zero `Report` with every error. Errors are plain `error` values; the caller maps any of them
  to one problem (`report_invalid`). The message names location and rule only.
- `engineVersion` is compared exactly with the listed releases: no trimming, no `v` added, no ranges. An unlisted
  release, the empty string included, is an error naming `engine_version`, before the payload is read.
- `EngineVersions` returns `v2.3.1`, `v2.4.0` today, as a fresh slice on every call. A caller without a bound
  release that only displays a report (the graph page) passes the last one; the gate passes the envelope's.
- A decoded `Report` owns its memory; it does not alias `payload`.
- `Decode` never drops, reorders, or rewrites findings, tasks, seams, or validation commands. The only dropped
  input is the dropped-key list. Marshalling a `Report` yields a document without any dropped key.
- There is no exit code in a report. Healthy, attention and blocked are `Verdict`; an engine error yields no
  report, which the gate sees as a missing upload or an invalid payload.
- `Reference` carries no digest. The baseline and labels digests the producer used are stated in the upload
  envelope and compared by the gate; this module knows nothing about them.
- Everything in a decoded `Report` is still customer-attested. Decoding proves form, not truth.
- `Active` is true exactly when `Kind` is `"gate"` and `Status` is `"new"` or `"expired_waiver"`. Every other
  pair is false, including a status or kind this module does not know. It reads those two fields only. It is a
  presentation predicate: the conclusion comes from `HardGates` and the gate, never from counting active findings.

## Integrations

This module imports only the standard library. It depends on no internal module. The entries below are its
consumers (the consumer depends on this module) and its external counterparts. Values are from the root coupling
table; the `internal/gate` row is computed (LCA root, deeper module two levels down).

| Counterpart                      | Direction                        | Strength                      | LCA / Rank / Distance     | Volatility | Balanced? |
| -------------------------------- | -------------------------------- | ----------------------------- | ------------------------- | ---------- | --------- |
| `internal/gate/ingest`           | ingest depends on report         | Contract (versioned schema)   | root / 2 / 2              | High       | Yes       |
| `internal/gate/verdict`          | verdict depends on report        | Contract (published language) | root / 2 / 2              | High       | Yes       |
| `internal/gate/feedback`         | feedback depends on report       | Contract (published language) | root / 2 / 2              | High       | Yes       |
| `internal/gate`                  | through gate/ingest, no import   | Contract                      | root / 2 / 2              | High       | Yes       |
| `internal/control/ui`            | ui depends on report             | Contract                      | root / 2 / 2              | High       | Yes       |
| `internal/control/ui/graph`      | graph depends on report          | Contract                      | root / 3 / 3              | Moderate   | Yes       |
| `archfit` engine state (v2.3.1)  | report mirrors the engine schema | Contract (versioned schema)   | outside the tree; highest | Moderate   | Yes       |
| `archfit-action` (external repo) | the Action uploads engine state  | Contract                      | outside the tree; highest | Moderate   | Yes       |

Shared knowledge, per counterpart:

- `internal/gate/ingest` holds `MaxBytes` (request size cap, `413`), `Decode` and `Report`. It decodes with the
  envelope's `engine_version`. A decode error, an unlisted release included, becomes the problem
  `report_invalid`, never a rejection.
- `internal/gate/verdict` holds `Report`, `Decision`, `UnevaluatedRule`, `Comparison`, `Reference`, `Dimension`,
  `Finding`. It decides only from the frozen decision core, which every listed release defines: `Verdict`,
  `Decision.HardGates`, `Decision.UnevaluatedRequiredRules` (`RuleID`, and `Reason` for the dead-selector prefix),
  `Comparison.ConfigHash`, `Comparison.MeasurementProfile` (nil or not), and `GateReference.Status` and `Reasons`.
  For the display-only `Detail` of `attention_only` it also reads `Dimension.Status` of the nine engine keys and
  `Finding.Kind` and `Finding.Status`, to name the dimensions not fully measured and count the active diagnostics.
  Those reads never change the conclusion or the reason code. It never reads `AgentTask` (so never its `Origin`),
  `Coverage` or `Seams`.
- `internal/gate/feedback` holds all types: findings with edges and locations for annotations, dimensions with
  status and confidence, agent tasks with `RepairKind` and `Validation`, the measurement profile, coverage. It calls
  `Finding.Active` to mark blocking findings.
- `internal/gate` does not import this module. It reads `Report`, `Comparison` and `MeasurementProfile` through the
  `Report` field of `ingest.Upload`: a `nil` profile or a version different from the manifest entry means the identity
  is not approved. A change to those fields still reaches it.
- `internal/control/ui` holds `Decode`, `EngineVersions` and `MaxBytes` to read the `archfit-report` workflow
  artifact for the graph page. The artifact names no release, so it decodes with the newest.
- `internal/control/ui/graph` holds `Report`, `Seam`, `Comparison.ConfigHash` and `Finding.Active`, which decides
  the links it marks as active gate findings.
- `archfit` engine state: this module holds the whole v2.3.1 state schema key set, its enums and required lists,
  pinned at revision `f8877d25ba36d84d3780071580d23486e3d794d0`. It holds no engine Go types (trust-and-reporting.md §7).
- `archfit-action` uploads the engine's state file bytes as they are, or with dropped keys stripped. Contract
  coupling through a version constant, strict decoding and shared fixtures tolerates the distance of three repositories.

No counterpart contract is used by this module, so nothing is restated here.

## Change Vectors

- A new engine state version: add `StateSchemaV2` and a `DecodeV2` path; v1 stays while approved images emit it.
- A new engine release within v1: append it to the release table. A release that adds a key also adds the shadow
  field with `since:"<release>"`, as dropped or, after a privacy review, as kept with a string class and
  fixtures; earlier releases keep refusing the key. Until then its reports fail closed as `report_invalid`.
  The App ships this before the engine release is approved in the manifest.
- Retiring a release: delete its row from the table once the manifest no longer approves it; its `since` tags
  stay, because later releases still emit those keys.
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
- Strict within each release: a document gets the key set of the release it names and nothing else. An unlisted
  release never falls back to another key set.
- The frozen decision core is defined by every listed release; a field the gate decides from never carries a
  `since` tag.
- Errors never contain payload values or the engine release. Callers log IDs, digests, outcomes and timings only.
- No I/O, no clock, no logging, no mutable globals: the release table is read-only. Standard library only. No other
  internal import, including wire siblings.
- Transport types only: a `Report` is never persisted. The App verifies it, publishes feedback and drops it.

## Test Specification

Fixtures live in `internal/wire/testdata/report/` and `internal/wire/testdata/leakage/`, read by relative path.
Each fixture has an `.expect.json` sibling recording its producer artifact and expected outcome (see the parent document);
this module asserts its `decode` and `fields` keys. The pinned engine schema is `internal/wire/testdata/schema/archfit.state.schema.json`.
This module has no private fixture and no `testdata` folder of its own. Every fixture is a v2.3.1 document except
the v2.4.0 capture `real-blocked-no-baseline-v240`. The two releases share one key set, so the tests decode every
fixture as v2.3.1, and `TestEveryEngineVersionReadsTheCorpus` decodes each one under every listed release.

### Unit Tests

- **Test name**: `TestStringClassRules`
  **Scenario**: table, one row per string class at its limit, one over it, one with LF, a tab, NUL, U+2028 and U+2029, the classes
  including tool version (128) and task text (`goal`, `constraints[]`, 4 096); token rows with a backtick and with `timed out`;
  `config_hash` rows: empty, 64 hex, uppercase, 63 hex, with a `sha256:` prefix; tool-version rows: every engine format listed under
  String rules, 128 and 129 multi-byte characters, a bidi override, a zero-width space, a no-break space, a CR; a `;` in
  `semantics_version` and `(` in `unknowns`.
  **Expected behavior**: at-limit rows, `timed out`, empty and 64-hex hashes and every engine tool-version format decode; every other
  row is an error naming the JSON location.
- **Test name**: `TestSinceTagSelectsTheKeySet`
  **Scenario**: the pre-pass over a probe type with an untagged key, a key tagged `since:"v2.4.0"` and a key tagged with an unlisted
  release: the new key under v2.3.1 and under v2.4.0, the unlisted one under the newest release.
  **Expected behavior**: the new key is an unknown key under v2.3.1 and accepted under v2.4.0; the unlisted one is always unknown.
- **Test name**: `TestPathRules`
  **Scenario**: table for `locations[].file`, `agent_tasks[].files[]` and `edge.from.path`: `pkg/a/a.go`; `pkg/b`; `pkg/a/**`; empty;
  `.`; `/etc/passwd`; `../x`; `a/../../x`; `a//b`; `a/./b`; `a\b`; `C:/x`; 513 characters.
  **Expected behavior**: the first three decode everywhere; empty decodes only for an endpoint path; the rest are errors.
- **Test name**: `TestEnums`
  **Scenario**: one row per schema enum with each allowed value and one unknown value; an empty `partial_basis`; plus unknown
  values for `Finding.Status`, `Severity` and `Origin`.
  **Expected behavior**: allowed values and the empty `partial_basis` decode; unknown schema-enum values are errors; unknown
  plain-string values decode unchanged.
- **Test name**: `TestCollectionCaps`
  **Scenario**: generated reports at each cap and one element over it, `delta.metrics` (256) included, `locations` at 10 000,
  `constraints` at 34 and task `files` at 10 002.
  **Expected behavior**: at-cap decodes; over-cap is an error naming the collection.
- **Test name**: `TestTaskHoldsWhatItsFindingHolds`
  **Scenario**: a finding at its caps (10 000 locations, 32 alternatives of 500 characters, a 500-character why and constraint) and
  the task the engine builds from it: every location file plus both endpoints, the constraint, one `allowed alternative: ` per
  alternative and a public surface of 100 globs, and the goal `Break the import cycle: ` plus the why.
  **Expected behavior**: decodes, with 10 002 files and 34 constraints.
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
- **Test name**: `TestFindingActive`
  **Scenario**: table over `Kind` and `Status`: `gate` with `new`, `expired_waiver`, `baseline`, `waived`, `fixed`, an unknown status
  and an empty status; `advisory` with `new` and with `expired_waiver`; an unknown kind and an empty kind with `new`.
  **Expected behavior**: true only for `gate` with `new` and `gate` with `expired_waiver`.
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
- **Test name**: `TestProducerToolVersionsSurvive`
  **Scenario**: the unmodified polyglot capture `real-polyglot-blocked` (cargo, grimp, dependency-cruiser, cargo-modules, scip, jscpd,
  ast-grep/syntax rows) and `producer-versions` (grimp failure with uv, macOS git, two coverage parsers).
  **Expected behavior**: both decode as recorded; each listed producer keeps its tool version and status verbatim.
- **Test name**: `TestDecodeConformanceFixtures`
  **Scenario**: every fixture in `report/`; the T02 names must be present: `healthy`, `attention-pass`, `blocked`, `missing-evidence`,
  `unknown-identity`, `profile-comparable`, `profile-incompatible`, `blocker-and-unevaluated`, `non-applicable-rule`,
  `locations-empty`, `matched-by-populated`, `repair-kinds`, `waiver-expiry-order`, `invalid-input`.
  **Expected behavior**: each decodes or fails as its expectation says; for decoded ones `Verdict`, `HardGates`, unevaluated rule IDs,
  profile presence and version, and `GateReference.Status` equal the recorded values.
- **Test name**: `TestMarshalledReportHasNoDroppedKey`
  **Scenario**: decode each valid fixture, marshal the `Report`, inspect the JSON, decode it again.
  **Expected behavior**: no `matched_by`, `declarations`, `measurement`, `provenance`, `denominator`, `hypothesis` or `scores` key anywhere;
  the second decode equals the first; counts of findings, tasks and seams equal the counts in the fixture file.
- **Test name**: `TestEngineSchemaKeySetIsCovered`
  **Scenario**: walk the pinned `internal/wire/testdata/schema/archfit.state.schema.json` and the shadow types by reflection.
  **Expected behavior**: every schema property is a shadow field, and the reverse; kept plus dropped equals the schema key set. A
  missing schema file fails the test; it never skips.
- **Test name**: `TestEngineVersions`
  **Scenario**: call `EngineVersions`, change the returned slice, call it again.
  **Expected behavior**: `v2.3.1`, `v2.4.0` both times: the caller gets a copy.
- **Test name**: `TestEveryEngineVersionReadsTheCorpus`
  **Scenario**: decode every report and leakage fixture once per listed release.
  **Expected behavior**: the same `Report`, or an error, as under v2.3.1, for every release.
- **Test name**: `TestUnknownEngineVersionIsRefused`
  **Scenario**: the real capture with the release `""`, `v2.3.0`, `v2.5.0`, `2.3.1`, `v2.3.1 ` (trailing space), `V2.3.1` and one
  carrying `LEAK-7f3a`.
  **Expected behavior**: each is an error naming `engine_version`, with a zero `Report`; no error contains the marker.
- **Test name**: `TestSinceTagsNameListedReleases`
  **Scenario**: walk every shadow field by reflection.
  **Expected behavior**: every `since` tag names a release of `EngineVersions`.
- **Test name**: `TestDecisionCoreIsInEveryVersion`
  **Scenario**: walk the shadow fields of the frozen decision core.
  **Expected behavior**: each one exists and carries no `since` tag.
- **Test name**: `TestSizeCap`
  **Scenario**: a valid report padded (inside a dropped string) to exactly `MaxBytes`, and to `MaxBytes + 1`.
  **Expected behavior**: the first decodes; the second fails before any parsing.
- **Test name**: `TestWrongSchemaVersion`
  **Scenario**: `schema_version` empty, `archfit.architecture-state.v2`, and the envelope's version string.
  **Expected behavior**: errors.
- **Test name**: `TestReportDoesNotAliasPayload`
  **Scenario**: decode the all-keys fixture, marshal the `Report`, overwrite every byte of the payload, marshal again.
  **Expected behavior**: both marshalled documents are equal: the `Report` owns its memory.

### Boundary Tests

- **Test name**: `TestSeededLeakageFixturesFail` (release test, T04)
  **Scenario**: every rejecting file in `internal/wire/testdata/leakage/`: a `snippet` key on a finding; a top-level `logs` blob; a `raw_response` key on
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

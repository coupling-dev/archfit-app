# No-Source State Report

**Path**: internal/wire/report/
**Parent**: internal/wire/ (Wire Contracts)
**Submodules**: none (leaf)

## Purpose

An engine state report is produced in the customer's CI and uploaded to Archfit. The product promise is that no
source code reaches Archfit. This module is where that promise is enforced on the payload. It mirrors the engine
state contract `archfit.architecture-state.v1` (engines v3.0.0 and v3.0.1, one `archfit.state.schema.json`) and turns it into a
no-source transport: it knows every key of that schema, keeps an allowlisted subset under length caps, drops the
rest, and refuses anything the schema does not define. Without it,
"schema-valid engine JSON" would be mistaken for "no-source payload" (plan T04): a real report carries source
identifiers in `agent_tasks[].declarations`.

## Functional Responsibilities

- Define the transport types for verdict, gate decision, comparison identity, gate reference, dimensions,
  coverage, findings, repair tasks and seams.
- `Decode`: strict JSON decoding against the v3.0.0/v3.0.1 key set, size cap, enum checks, per-field caps, path rules,
  single-line rule, duplicate-ID rule; known but non-allowlisted keys are type-checked and dropped.
- `EngineVersions`: the engine releases `Decode` reads (`v3.0.0`, `v3.0.1`), so that a consumer can tie an approved
  identity to the key set.
- Preserve what the gate and feedback need, unchanged: `verdict`, `decision.hard_gates`,
  `unevaluated_required_rules`, `comparison.config_hash`, `measurement_profile`, `gate_reference`, both repair
  kinds, task validation commands, empty `locations`.
- The display rules its consumers share (`Group`, `Cycles`, `PairOrigin`, `EngineRuleNames`, `DistanceWords`, `BalancingMoves`, `BalancingMove`, `MetricRulePrefix`,
  `ReadableWhy`, `ShownPerGroup`), so the PR comment, the App's pull request page and its graph group and word findings the same way.
- Nothing else. It does not interpret the decision, compare profiles, filter findings, or compute outcomes.

## Subdomain Classification

Published language between `archfit` (engine), `archfit-action` (producer) and the App. Supporting.
Volatility: moderate. It changes with a versioned engine release; the caps are **[default]** and may be tuned in pilots.

## Encapsulated Knowledge

- The complete key set of the state schema, pinned at engine v3.0.1 (revision
  `0649e504a6761f565086b342d315110feeb27d77`, `internal/wire/testdata/schema/`; the v3.0.0 file at
  `bbd658f008bcc4bc83e063cf305d20dc4508da2e` is byte-identical), and the split into kept and
  dropped keys below. Field names and enum values in the Public Contract are taken from that schema, not guessed.
- The engine releases `Decode` reads (see "Engine release and key set" below).
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
  The engine release is input too: an unknown one is reported as `engine_version` and the rule, never echoed.

### Engine release and key set

`Decode` takes the engine release of the upload envelope. It reads two releases, `v3.0.0` and `v3.0.1`, which emit
the same state schema byte for byte. Any other value, the empty string included, is a decode error, so the upload
ends `action_required` (`report_invalid`), never in a guessed key set. The document must fit that key set exactly: a
key that the schema does not define is an unknown key. Reading is not approval: the manifest approves `v3.0.1` only,
with its image digests for `linux/amd64` and `linux/arm64`. `v3.0.0` stays readable so that a workflow still pinned
to it ends `unknown_engine_identity`, which tells the owner to move the pin, and not `report_invalid`. Remove it
when a release changes the key set, or when no installed workflow pins it.

- The schema has these keys, among others. Kept: `comparison.classification_hash`, `comparison.origin_status`,
  `comparison.origin_reasons`, `gate_reference.drift`, `gate_reference.baseline_present`, `findings[].origin`,
  `agent_tasks[].origin`, the optional `seams[].policy`, and the seam's balance reading: `seams[].hypothesis`,
  `quadrant`, `distributed_monolith` and `raw_distance.basis`. Dropped, and type-checked: `comparison.introduced_finding_ids`
  and `comparison.resolved_finding_ids`, with the same two keys of `gate_reference`. The engine writes both lists
  only with `--base`.
- `origin` of a finding or a task (`introduced`, `pre_existing`, `unknown`) exists only in a report made with
  `--base`. `comparison.origin_status` (`comparable`, `unknown`) says whether the origin is known. They are display
  facts: no decision reads them.
- `gate_reference.drift` holds the input classes that made the reference not comparable. Its values are a closed set,
  `DriftClasses`: `classification_hash`, `model_hash`, `labels_hash`, `rubric_version`, `measurement_profile`,
  `reference_incomplete`. `gate_reference.baseline_present` says whether the engine loaded a baseline file.
- `seams[].policy` has the closed value set `violation`, `accepted`, `advisory`, `allowed`, `observed` (the engine's
  policy status of the seam). `Seam.Policy` keeps the value, an enum checked only when the key is present (absent
  decodes to `""`); the graph shows it, the gate never reads it.
- The seam's balance reading is display only. `hypothesis` is a token kept as written (see "Kept from a seam" below).
  The other value sets are closed here, from the engine's constants (the schema gives no enum): `quadrant` `cohesive`,
  `loose`, `low_cohesion`, `tight`; `raw_distance.basis` `<crossed>@<container>`, `<crossed>` one of `module_boundary`,
  `ownership`, `deploy_unit`, `declared_external` and `<container>` an identifier (a module name or `system`). A value
  outside its set decodes to `""` and never fails the report **[default]**: a value a later engine adds shows nothing.
  `distributed_monolith` is a boolean; absent reads `false`. The other `raw_distance` keys stay dropped: owners and
  deploy units name people and units.
- A key the App starts to keep is a kept field; keeping it is still a reviewed contract change.
- **Frozen decision core.** The gate decides only from these fields: `schema_version`, `verdict`,
  `decision.hard_gates`, `decision.unevaluated_required_rules` (`rule_id`, `reason`), `comparison.config_hash`,
  `comparison.measurement_profile.version`, `gate_reference.status`, `gate_reference.reasons`,
  `gate_reference.drift` and `gate_reference.baseline_present`, plus `findings[].id`, `kind` and `status` for
  `Active`. A new way to block, or a new value in one of their enums, is a new state schema version, not a key.

### Order of checks

Engine release is `v3.0.0` or `v3.0.1` → size over `MaxBytes` → valid UTF-8 → token pre-pass → typed
decode with unknown fields disallowed → required keys → `schema_version` → enums → collection caps → string rules →
path rules → duplicate finding IDs. The first failure is returned. No partial `Report` is ever returned.

### Dropped keys

Known to the decoder, checked for JSON type, never copied into a `Report`. A key not in the Public Contract types
and not in this list is a decode error.

| Location                 | Dropped keys                                                                     |
| ------------------------ | -------------------------------------------------------------------------------- |
| root                     | `measurement` (`source_ref`, `history_depth`, `history_window`, `tool_versions`) |
| `findings[]`             | `matched_by` (map of string to string; populated in real reports), except the four keys below |
| `agent_tasks[]`          | `declarations` (list of syntax facts, below)                                     |
| `dimensions.*.metrics[]` | `denominator` (`observed`, `total`), `provenance` (list of strings)              |
| `comparison`             | `drift`, `baseline_present`, `introduced_finding_ids`, `resolved_finding_ids` (`drift` and `baseline_present` are kept on `gate_reference` only) |
| `gate_reference`         | every comparison key except `status`, `base_ref`, `reasons`, `drift`, `baseline_present` |
| `seams[]`                | the seam keys listed below                                                       |

- A syntax fact has `language`, `file`, `module`, `kind`, `name`, `framework` (strings), `exported` (boolean),
  `start_line`, `end_line`, `count` (integers). `name` is a source identifier: the closest thing to source in a
  report. Declarations are always dropped, whatever they contain.
- Dropped seam keys: `scored_edges`, `abstained_edges`, `critical_edges`, `high_or_worse_edges`, `critical_share_pct`,
  `high_or_worse_share_pct` (integers); `label_evidence_hash`, `confidence`, `role_expectation` (strings); `labels`
  (list of strings); `raw_distance` but its `basis` (object: `level`, `from_owner`, `to_owner`, `from_deploy_unit`, `to_deploy_unit` strings,
  `same_owner`, `same_deploy_unit` booleans, `boundary_crossings`, `shared_ancestor` integers); `scores` (object: `n`,
  `min`, `median`, `max` integers, `p10`, `p90` integer or `null`, `mean` number).
- Kept from `matched_by`, as text (`Finding.MatchedBy`, `MatchedByKeys`): `metric`, `before`, `after`, `threshold`. A metric
  ratchet finding (`metric/<name>`) has no module edge; these four keys are the only place that names its numbers, and
  the PR comment shows them as "metric <name>: before → after (threshold)". They hold a metric name and numbers the engine
  computed, never source. Every other `matched_by` key (globs, group members, cycle data) stays dropped, unchecked.
  The allow-list is closed: another key is a reviewed contract change, and is kept only when something renders it.
- Kept from a seam: `hypothesis` (`Seam.Hypothesis`, a token, optional): the engine's balancing move, which the PR comment
  shows as the "Suggested move" of an advisory.
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
| `gate_reference.drift[]`              | `classification_hash`, `model_hash`, `labels_hash`, `rubric_version`, `measurement_profile`, `reference_incomplete` |
| `comparison.origin_status`, if given  | `comparable`, `unknown`                         |
| `findings[].origin`, if given         | `introduced`, `pre_existing`, `unknown`         |
| `agent_tasks[].origin`, if given      | `introduced`, `pre_existing`, `unknown`         |
| `dimensions.*.status`                 | `measured`, `partial`, `unmeasured`             |
| `agent_tasks[].repair_kind`           | `code_change`, `needs_owner_decision`           |
| `producers[].partial_basis`, if given | `unresolved_specifiers`, `degraded_precision`   |
| `seams[].policy`, if given            | `violation`, `accepted`, `advisory`, `allowed`, `observed` |

Other value sets named in the type comments (`Finding.Status`, `Severity`, `Kind`, `Producer.Status`, `Dimension.Gate`)
are plain strings in the engine schema. They are checked as tokens only **[default]**; a consumer
that meets a value it does not know fails closed or shows it as text.

An empty `partial_basis` (`""`) is accepted and means the same as an absent key: the enum is checked only for a
non-empty value **[default]**. `seams[].policy` is stricter, as the engine schema is: a present key must hold one of
its five values, the empty string included in the errors.

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
| `allowed_alternatives` per finding                              | 32      | one in the real captures                                       |
| `constraints` per task                                          | 35      | alternatives plus 3, see below                                 |
| `validation` per task                                           | 32      | one command in real output                                     |
| `files` per task                                                | 10 002  | locations plus 2, see below                                    |
| `metrics`, `unknown` per dimension; `metrics` per delta         | 256     | 15 metrics at most in the real captures                        |
| `findings` (references) per dimension                           | 10 000  | one per finding                                                |
| `gate_reference.drift`                                          | 6       | one per drift class                                            |
| `producers`, `unknowns`, `coverage.tools`, every `reasons` list | 64 each | 13 producers in the polyglot capture (every analyzer on); no unknown sentence in any v3.0.0 capture |

- `locations`: module-scoped rules (`public_api_max`, `syntax_api_size_ceiling`) list one location per file of the
  module. Real reports had 106 and 91; the former cap of 100 turned a real blocked report into `report_invalid`.
  10 000 locations cost 0.3 to 0.8 MiB, so a finding at the cap still leaves room for the rest of the report.
- `files` per task: the engine's task files are every location file plus the two edge endpoints, so the cap is
  the locations cap plus 2. `constraints` per task: the finding constraint, the rule's rationale
  (`rationale: <text>`), one per allowed alternative and the target module's public surface, so the alternatives cap
  plus 3.
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

- Finding text, at most 2 000 characters, single line: `Finding.Why`, `Finding.Constraint`. The engine ends the why with
  ` — <rationale>` and both with ` (see <docs>)`, texts the policy author writes and the engine does not bound; a real `module_dependencies` why is about 110 characters before them, so the 500 of Text
  would reject a two-sentence rationale with an ADR path. 2 000 keeps a task goal (`Break the import cycle: ` and
  the why) and the task's `rationale: ` constraint under the 4 096 of Task text. A rationale or docs value over the
  cap, or on more than one line (a YAML `|` or `>` block scalar ends with a line break), makes the report
  `report_invalid`; the user guide says so.
- Text, at most 500 characters, single line: `AllowedAlternatives[]`,
  `UnknownFact.Fact`, `UnknownFact.Reason`, `UnevaluatedRule.Reason`, `ToolCoverage.Reason`,
  `DimensionCoverage.Basis`, and every `Reasons[]` list (`Comparison`, `Reference`, `Delta`, `OriginReasons`).
  Real engine text contains quotes, backticks and dashes, so no character set applies.
- Task text, at most 4 096 characters, single line: `AgentTask.Goal`, `AgentTask.Constraints[]`. The engine
  composes them from other fields: a goal is a template with both edge paths (up to about 1 200 characters) or
  `Break the import cycle: ` and the why; a constraint is the finding constraint, `rationale: ` and the rule's
  rationale, `allowed alternative: ` and an alternative, or `public surface of module "<module>": [<every public glob>]`. That last one is unbounded: a real
  one with 21 globs was 765 characters, so the 500 of Text rejected a real blocked report. 4 096 holds every
  fixed composition and about 100 public globs at the observed 35 characters each.
- Command, at most 512 characters, single line: `AgentTask.Validation[]`. No whitespace-separated word equals
  `--refresh` or starts with `--refresh=`. Kept verbatim and in order.
- Identifier, at most 200 characters, single line: every `ID`, `FindingID`, `RuleID`, module names (`Endpoint.Module`, `Seam.FromModule`, `Seam.ToModule`), `Dimension.Name`,
  `Dimension.Owner`, `UnknownFact.Owner`, `Metric.Name`, `Metric.Unit`, `MetricDelta.Name`, `Tool`, `BaseRef`.
- Token, at most 64 characters from letters, digits, space and `_ . : / + -`: `Status`, `Kind`, `Severity`,
  `Confidence`, `Gate`, `Strength`, `Distance`, `Volatility`, `Drift[]`, `Edge.Kind`. The space
  is needed: a producer status is literally `timed out`.
- Version, token characters, at most 128: `MeasurementProfile.Version`, `SemanticsVersion`, `RubricVersion`,
  `Unknowns[]`. These are engine identifiers.
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
- Hash: `Comparison.ConfigHash` and `Comparison.ClassificationHash` are `""` or `^[0-9a-f]{64}$`, bare hex without a
  `sha256:` prefix. `ModelHash`,
  `LabelsHash`, `SettingsHash` are token characters, at most 128.
- Path: `Location.File`, `AgentTask.Files[]`, and `Endpoint.Path` when not empty. Non-empty, at most 512
  characters, forward slashes only, relative, equal to its own `path.Clean` form, no `..` segment, no leading
  `/`, no backslash, no drive prefix (`C:`). A directory or a glob such as `pkg/a/**` is a valid path, and so is `.`,
  the repository root: the engine names an importer that no module owns by its package directory, `.` for a
  root package, in `edge.from.path` and in the task's `files` (`real-root-importer`).
- Numbers (ranges are **[default]**): `line` 0 to 10 000 000; every count ≥ 0; metric values any finite number;
  integers must be written without fraction or exponent.
- Finding `id` values are unique across the report. `finding_id` and finding references are not checked against
  existing findings **[default]**: the synthetic coupling-gate task may reference none.

## Public Contract

```go
// StateSchemaV1 is the engine state contract accepted (archfit.state.schema.json, engine v3.0.0 and v3.0.1: one file).
const StateSchemaV1 = "archfit.architecture-state.v1"

// MaxBytes caps an uploaded payload: 5 MiB (the "5 MB" cap of decisions.md).
const MaxBytes = 5 << 20

// Report is the no-source transport form of an engine state report. The decoder knows every key of the engine
// state schema of v3.0.0 and v3.0.1: keys listed here are kept (length-capped), the other known keys are validated and dropped
// (measurement, findings[].matched_by except MatchedByKeys, agent_tasks[].declarations, metric provenance, seam
// scores), and any key the schema does not define is a decode error.
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
    ConfigHash         string              `json:"config_hash,omitempty"`         // bare lowercase hex SHA-256 of the raw policy bytes
    ClassificationHash string              `json:"classification_hash,omitempty"` // bare lowercase hex SHA-256 of the policy leaves that change measured facts
    ModelHash          string              `json:"model_hash,omitempty"`
    LabelsHash         string              `json:"labels_hash,omitempty"` // canonical hash of approved labels; not reproducible from raw bytes
    RubricVersion      string              `json:"rubric_version,omitempty"`
    MeasurementProfile *MeasurementProfile `json:"measurement_profile,omitempty"` // nil = unknown identity
    Reasons            []string            `json:"reasons"`
    OriginStatus       string              `json:"origin_status,omitempty"` // "comparable" | "unknown"; presentation only
    OriginReasons      []string            `json:"origin_reasons,omitempty"`
}

// Reference is the engine gate's comparison with the persisted baseline (top-level gate_reference).
// The engine exposes no baseline digest: only whether it loaded a baseline, whether that one was comparable, and
// the classes of input that made it not comparable. Reasons are engine text for display; decisions read Status,
// BaselinePresent and Drift.
type Reference struct {
    Status          string   `json:"status"`             // "comparable" | "non_comparable" | "not_requested"
    BaseRef         string   `json:"base_ref,omitempty"` // "baseline"
    Reasons         []string `json:"reasons"`            // e.g. "no baseline file was loaded"
    Drift           []string `json:"drift,omitempty"`    // closed set DriftClasses; empty when comparable
    BaselinePresent bool     `json:"baseline_present"`   // the engine loaded a baseline file
}

// DriftClasses are the input classes that can make a reference not comparable (engine decision.DriftClass).
var DriftClasses = []string{
    "classification_hash", "model_hash", "labels_hash", "rubric_version", "measurement_profile", "reference_incomplete",
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
    ID                  string     `json:"id"`   // 32-hex fingerprint, unique per report
    Kind                string     `json:"kind"` // "gate" | "advisory"
    RuleID              string     `json:"rule_id"`
    Status              string     `json:"status"`   // "new" | "baseline" | "waived" | "expired_waiver" | "fixed"
    Severity            string     `json:"severity"` // "critical" | "high" | "medium" | "low"
    Confidence          string     `json:"confidence"`
    Edge                Edge       `json:"edge"`
    Locations           []Location `json:"locations"`  // may be empty; never invent one
    Why                 string     `json:"why"`        // engine-authored, single line, length-capped
    Constraint          string     `json:"constraint"` // engine-authored, single line, length-capped
    AllowedAlternatives []string   `json:"allowed_alternatives,omitempty"`
    Origin              string     `json:"origin,omitempty"` // "introduced" | "pre_existing" | "unknown", only with --base; presentation only
    // MatchedBy holds only the MatchedByKeys of the engine's matched_by; a metric ratchet names its numbers there.
    MatchedBy map[string]string `json:"matched_by,omitempty"`
}

// The matched_by keys a consumer shows. The engine writes more keys; the others stay out of the App.
const (
    MatchedByMetric    = "metric"
    MatchedByBefore    = "before"
    MatchedByAfter     = "after"
    MatchedByThreshold = "threshold"
)

var MatchedByKeys = []string{MatchedByMetric, MatchedByBefore, MatchedByAfter, MatchedByThreshold}

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
    Status  string        `json:"status"`
    Reasons []string      `json:"reasons,omitempty"`
    Metrics []MetricDelta `json:"metrics,omitempty"`
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
    // VolatilityProvenance is where the engine took Volatility from: "declared" | "inherited" | "cascade" |
    // "undeclared"; "" from an engine that does not say. "declared" covers a functional edge that takes its source's
    // volatility when that is worse than the target's.
    VolatilityProvenance string `json:"volatility_provenance,omitempty"`
    Severity             string `json:"severity,omitempty"`
    // Policy is what the engine's policy says about the seam: "violation" | "accepted" |
    // "advisory" | "allowed" | "observed", a closed set. "" = the key is absent.
    Policy string `json:"policy,omitempty"`
    // Hypothesis is the engine's balancing move for the seam: "balanced" | "accept_low_volatility" |
    // "introduce_contract" | "move_functionality" | "declare_volatility" | "expected_by_role" | "follow_rule".
    // "" = the engine gave no move.
    Hypothesis string `json:"hypothesis,omitempty"`
    // The rest of the balance reading, display only, each from a closed set ("" outside it): the quadrant, the
    // distributed-monolith flag, and what the distance crosses.
    Quadrant            string       `json:"quadrant,omitempty"`
    DistributedMonolith bool         `json:"distributed_monolith,omitempty"`
    RawDistance         SeamDistance `json:"raw_distance"` // only its basis is kept
}

// SeamDistance is what the engine measured the distance on.
type SeamDistance struct {
    // Basis is "<crossed>@<container>": the boundary the distance crosses ("module_boundary" | "ownership" |
    // "deploy_unit" | "declared_external") and the module that holds both ends, or "system". "" when no edge was
    // scored, the crossing is outside that set, or the container is not an identifier.
    Basis string `json:"basis,omitempty"`
}
```

```go
// Decode strictly parses payload as the no-source transport of the state document engineVersion emits. It
// fails on: an engineVersion outside EngineVersions, size over MaxBytes, a key the state schema
// does not define, wrong schema_version, an enum value outside its set, strings over the per-field caps,
// absolute or parent-escaping paths, multi-line text, or duplicate finding IDs.
func Decode(payload []byte, engineVersion string) (Report, error)
```

```go
// EngineVersions returns the engine releases whose state document Decode reads: v3.0.0 and v3.0.1, one key set. It emits
// StateSchemaV1.
func EngineVersions() []string
```

```go
// Active reports whether the finding currently blocks: a gate finding that is new or whose waiver expired.
// It is the single definition of "active gate finding" for every consumer.
func (f Finding) Active() bool

// Reverses reports whether f and g are the two directions of one module cycle: one rule, one kind, status and severity,
// and module pairs that are reversed, non-empty and distinct. Display only: each finding keeps its ID and gate effect.
func (f Finding) Reverses(g Finding) bool

// Display rules the PR comment (gate/feedback), the pull request page and the graph (control/ui) share, so all tell
// the same story. None of them is a gate decision.

// ShownPerGroup is how many findings of one group the comment and the pull request page list; the rest are counted.
const ShownPerGroup = 50

// Group sorts a report's findings into the comment's groups: violations (active first, then unknown statuses),
// advisories, accepted, fixed; Origin cleared without a comparison; OnCycle for the first shown violations.
func Group(r Report, shown int) Groups

// Cycles returns, per finding, the index of the dependency cycle it lies on, or -1: edges of one rule, kind, status
// and severity whose target reaches their source, grouped by shared modules.
func Cycles(fs []Finding) []int

const PairAdded, PairExisting = "added", "existing"

// PairOrigin is what the report shows about one direction of a cycle on the pull request base: PairExisting when a
// finding on the same module pair is pre_existing, else PairAdded when one on the pair that lies on no cycle is
// introduced, else "".
func PairOrigin(f Finding, all []Finding, onCycle map[string]bool) string

// SplitCovered splits advisories by the violations' module pairs. An advisory on the same ordered, non-empty module
// pair as a violation is that dependency under another rule: it joins the violation's row ("also reported as") and is
// not counted among the advisories. covered holds those per pair in report order; rest is every other advisory. The
// reverse direction is another dependency and is not covered.
func SplitCovered(violations, advisories []Finding) (rest []Finding, covered map[[2]string][]Finding)

// Headlines the App writes on a check that found violations; a neutral check is read by its headline (control/ui).
const HeadlineViolations = "Architecture violations found"
const HeadlineGateFailing = "Architecture gate failing"

var EngineRuleNames map[string]string // readable names of the engine's own rules

// RuleName is a rule's readable name: an engine rule's from EngineRuleNames, a policy rule's ID as written.
func RuleName(id string) string

// FindingMeta is a finding's severity, then its status as a word ("waiver", "waiver expired", "baseline"); a new or
// fixed finding is its severity alone, as its group already says it.
func FindingMeta(f Finding) string

// EngineVerdict is the engine's decision in the comment's words: the verdict with "_" read as a space, and the rest of
// the line: "hard gates fail|pass|not measured, N active blocker(s), M required rule(s) not evaluated" or ", every
// required rule evaluated". The comment and the pull request page both print it.
func EngineVerdict(r Report) (verdict, detail string)
var DistanceWords map[string]string   // the engine's distance tokens in the words of Balanced Coupling
var BalancingMoves map[string]string  // the engine's seam hypotheses in the words of Balanced Coupling
func BalancingMove(h string) string   // BalancingMoves[h], else h as written; "" for none
const MetricRulePrefix = "metric/"    // starts the rule ID of a metric ratchet finding, which has no module edge
func ReadableWhy(why string) string   // a Why with each known "<token> distance" as "distance <words>"
```

`StateSchemaV1` is the only state schema version accepted. `MaxBytes` is the payload cap. Reading the "5 MB" of
decisions.md §3 as 5 MiB = 5 242 880 bytes is **[default]**; a payload of exactly `MaxBytes` is within the cap.
`HardGates` and `RepairKind` are engine-authored values passed through unchanged.

Semantics not visible in the signatures:

- `Decode` returns a zero `Report` with every error. Errors are plain `error` values; the caller maps any of them
  to one problem (`report_invalid`). The message names location and rule only.
- `engineVersion` is compared exactly with `v3.0.0` and `v3.0.1`: no trimming, no `v` added, no ranges. Any other value, the
  empty string included, is an error naming `engine_version`, before the payload is read.
- `EngineVersions` returns `v3.0.0`, `v3.0.1`, oldest first, as a fresh slice on every call. A caller without a
  bound release that only displays a report (the graph page) passes the last one; the gate passes the envelope's.
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
| `archfit` engine state (v3.0.1)  | report mirrors the engine schema | Contract (versioned schema)   | outside the tree; highest | Moderate   | Yes       |
| `archfit-action` (external repo) | the Action uploads engine state  | Contract                      | outside the tree; highest | Moderate   | Yes       |

Shared knowledge, per counterpart:

- `internal/gate/ingest` holds `MaxBytes` (request size cap, `413`), `Decode` and `Report`. It decodes with the
  envelope's `engine_version`. A decode error, a release other than `v3.0.0` or `v3.0.1` included, becomes the problem
  `report_invalid`, never a rejection.
- `internal/gate/verdict` holds `Report`, `Decision`, `UnevaluatedRule`, `Comparison`, `Reference`, `Dimension`,
  `Finding`. It decides only from the frozen decision core: `Verdict`,
  `Decision.HardGates`, `Decision.UnevaluatedRequiredRules` (`RuleID`, and `Reason` for the dead-selector prefix),
  `Comparison.ConfigHash`, `Comparison.MeasurementProfile` (nil or not), and `GateReference.Status`, `Reasons`,
  `Drift` and `BaselinePresent`.
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
  artifact for the graph page. The artifact names no release, so it decodes as the last release, `v3.0.1`.
- `internal/control/ui/graph` holds `Report`, `Seam` (`Seam.Policy` included, the status it shows when present),
  `Comparison.ConfigHash` and `Finding.Active`, which decides the links it marks as active gate findings in a
  report without `seams[].policy`.
- `archfit` engine state: this module holds the whole state schema key set, its enums and required lists, pinned
  at v3.0.1, revision `0649e504a6761f565086b342d315110feeb27d77`. It holds no engine Go types (trust-and-reporting.md §7).
- `archfit-action` uploads the engine's state file bytes as they are, or with dropped keys stripped. Contract
  coupling through a version constant, strict decoding and shared fixtures tolerates the distance of three repositories.

No counterpart contract is used by this module, so nothing is restated here.

## Change Vectors

- A new engine state version: add `StateSchemaV2` and a `DecodeV2` path; v1 stays while approved images emit it.
- A new engine release within v1: change the pinned schema, the shadow types and the fixtures together. A key the
  release adds is dropped or, after a privacy review, kept with a string class and fixtures. Until then its reports
  fail closed as `report_invalid`. The App ships this before the engine release is approved in the manifest.
- Tuning caps from pilot evidence. Loosening is compatible; tightening follows the parent's versioning rule.
- Promoting a dropped key to kept (for example metric denominators) after a privacy review.
- Publishing the kept subset as a JSON Schema artifact for producers: add a `Schema` function here.

## Constraints and Invariants

- No-source transport (decisions.md §3): IDs, rule IDs, module names, file paths, line numbers, metrics and
  engine-authored messages only. No snippets, logs, prompts, or raw model output. Paths and module names are
  disclosed metadata; that residual exposure is documented, not hidden.
- Source identifiers never survive decoding: `declarations` is dropped always, and so is every `matched_by` key outside the
  four of `MatchedByKeys`, which hold a metric name and numbers; not when convenient.
- The kept set is closed: keeping another field is a reviewed contract change, never a decoder relaxation.
- Decoding never judges the decision. It checks the schema's enums and the no-source properties; outcome logic is the gate's.
- Enforcement is never filtered by `Origin` or finding `Status`; this module keeps every finding it accepts.
- Empty `locations` stay empty. No consumer may invent a location.
- `Decode` is total and bounded: no input panics; work is bounded by `MaxBytes`, the depth cap and the collection caps.
- Strict: a document gets the v3.0.0/v3.0.1 key set and nothing else. Any other release is refused, never read with this
  key set.
- Errors never contain payload values or the engine release. Callers log IDs, digests, outcomes and timings only.
- No I/O, no clock, no logging, no mutable globals: the release list is read-only. Standard library only. No other
  internal import, including wire siblings.
- Transport types only: a `Report` is never persisted. The App verifies it, publishes feedback and drops it.

## Test Specification

Fixtures live in `internal/wire/testdata/report/` and `internal/wire/testdata/leakage/`, read by relative path.
Each fixture has an `.expect.json` sibling recording its producer artifact and expected outcome (see the parent document);
this module asserts its `decode` and `fields` keys. The pinned engine schema is `internal/wire/testdata/schema/archfit.state.schema.json`.
This module has no private fixture and no `testdata` folder of its own. Every fixture is a v3.0.0 or v3.0.1 document;
the tests decode each one as the release that produced it (synthetic ones as v3.0.0).

### Unit Tests

- **Test name**: `TestCycles`, `TestPairOrigin`
  **Scenario**: two directions; three modules; a path; two rules on reversed pairs; two separate cycles beside a plain
  edge; edges without a module. Then a cycle direction with a pre_existing, an introduced off-cycle, no other, and a
  reverse-pair finding.
  **Expected behavior**: the cycle index per finding, -1 off a cycle and across rules; existing, added, "", "", and
  existing over added.
- **Test name**: `TestStringClassRules`
  **Scenario**: table, one row per string class at its limit, one over it, one with LF, a tab, NUL, U+2028 and U+2029, the classes
  including finding text (`why`, `constraint`, 2 000), tool version (128) and task text (`goal`, `constraints[]`, 4 096); token rows with a backtick and with `timed out`;
  `config_hash` rows: empty, 64 hex, uppercase, 63 hex, with a `sha256:` prefix; tool-version rows: every engine format listed under
  String rules, 128 and 129 multi-byte characters, a bidi override, a zero-width space, a no-break space, a CR; a `;` in
  `semantics_version` and `(` in `unknowns`.
  **Expected behavior**: at-limit rows, `timed out`, empty and 64-hex hashes and every engine tool-version format decode; every other
  row is an error naming the JSON location.
- **Test name**: `TestSeamPolicy`
  **Scenario**: read the `seams[].policy` enum of the pinned schema; on the all-keys fixture, the key absent, each enum
  value, `""`, `permitted`, `Allowed` and `true`.
  **Expected behavior**: the schema enum is the five values; absent decodes to `""`; each value is kept; the other rows
  are errors naming `seams[0].policy`, and no error echoes the value.
- **Test name**: `TestSeamVolatilityProvenanceIsKept`
  **Scenario**: on the base document, `seams[0].volatility_provenance` absent, `declared`, a value a later engine adds,
  `<b>`, and `3`.
  **Expected behavior**: `""`; `declared`; the new value as written (a token, not a closed set, so a new provenance never
  fails a report); errors naming `seams[0].volatility_provenance` for the last two.
- **Test name**: `TestSeamBalanceReadingsKeepOnlyEngineTokens`
  **Scenario**: on the base document with the four keys removed, one at a time: `hypothesis` `move_functionality`, a
  value a later engine adds, absent; `quadrant` `tight`, `Tight`; `distributed_monolith` true, absent;
  `raw_distance.basis` `module_boundary@system`, `ownership@pkg_core`, `galaxy@system`, `module_boundary`, `""`, and a
  container with a line break.
  **Expected behavior**: each `hypothesis` token kept as written, the engine's `quadrant` and `basis` tokens kept, and
  `true`; `""` or `false` for every other row; no row fails to decode.
- **Test name**: `TestRemovedV2KeysAreUnknown`
  **Scenario**: the base document with `comparison.task_origin_status` added, and with `new_findings` added to a
  dimension `delta`.
  **Expected behavior**: both are decode errors: the schema does not define these keys.
- **Test name**: `TestPathRules`
  **Scenario**: table for `locations[].file`, `agent_tasks[].files[]` and `edge.from.path`: `pkg/a/a.go`; `pkg/b`; `pkg/a/**`; empty;
  `.`; `/etc/passwd`; `../x`; `a/../../x`; `a//b`; `a/./b`; `a\b`; `C:/x`; 513 characters.
  **Expected behavior**: the first four (`.` is the repository root) decode everywhere; empty decodes only for an endpoint
  path; the rest are errors.
- **Test name**: `TestEnums`
  **Scenario**: one row per schema enum with each allowed value and one unknown value, `origin`, `origin_status` and
  `gate_reference.drift` included; an empty `partial_basis`; plus unknown values for `Finding.Status` and `Severity`.
  **Expected behavior**: allowed values and the empty `partial_basis` decode; unknown schema-enum values are errors; unknown
  plain-string values decode unchanged.
- **Test name**: `TestCollectionCaps`
  **Scenario**: generated reports at each cap and one element over it, `delta.metrics` (256) included, `locations` at 10 000,
  `constraints` at 35 and task `files` at 10 002.
  **Expected behavior**: at-cap decodes; over-cap is an error naming the collection.
- **Test name**: `TestTaskHoldsWhatItsFindingHolds`
  **Scenario**: a finding at its caps (10 000 locations, 32 alternatives of 500 characters, a 2 000-character why and constraint) and
  the task the engine builds from it: every location file plus both endpoints, the constraint, `rationale: ` with a rationale as
  long as the why, one `allowed alternative: ` per alternative and a public surface of 100 globs, and the goal
  `Break the import cycle: ` plus the why.
  **Expected behavior**: decodes, with 10 002 files and 35 constraints.
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
- **Test name**: `TestReadableWhy`
  **Scenario**: table: a Why with `cross_module distance`, one with an owner token, two tokens in one text, an unknown
  token, "no distance" with no token, and empty.
  **Expected behavior**: each known token reads "distance <words>" of `DistanceWords`; an unknown token and any other
  text stay as written.
- **Test name**: `TestFindingActive`
  **Scenario**: table over `Kind` and `Status`: `gate` with `new`, `expired_waiver`, `baseline`, `waived`, `fixed`, an unknown status
  and an empty status; `advisory` with `new` and with `expired_waiver`; an unknown kind and an empty kind with `new`.
  **Expected behavior**: true only for `gate` with `new` and `gate` with `expired_waiver`.
- **Test name**: `TestFindingReverses`
  **Scenario**: table: `a → b` against `b → a` of the same rule, kind, status and severity; against a different rule,
  status or severity; against `a → b`; a self pair `a → a`; empty modules.
  **Expected behavior**: true only for the reversed pair that matches on rule, kind, status and severity.
- **Test name**: `TestValidationCommandRules`
  **Scenario**: `archfit check -c .archfit.yaml --base main --lang go --require-tools`; a command with `--refresh`; with `--refresh=true`;
  with `--refreshed`; 513 characters; a command with LF; 33 commands.
  **Expected behavior**: the first and `--refreshed` decode verbatim; the rest are errors.
- **Test name**: `TestSplitCovered`
  **Scenario**: table: an advisory on a violation's pair; on the reverse pair; a violation without a module; two
  advisories on one pair around another.
  **Expected behavior**: covered on that pair; rest; rest; both covered in report order and the other in rest.
- **Test name**: `TestEngineVerdictInTheCommentsWords`
  **Scenario**: table: blocked with 2 blockers; with 1; failed with none; needs_attention, hard gates unmeasured, two
  unevaluated rules; healthy.
  **Expected behavior**: "blocked" / "hard gates fail, 2 active blockers, every required rule evaluated"; "1 active
  blocker"; "0 active blockers"; "needs attention" / "hard gates not measured, 0 active blockers, 2 required rules not
  evaluated"; "healthy" / "hard gates pass, 0 active blockers, every required rule evaluated".
- **Test name**: `TestBalancingMoveWordsAKnownHypothesisOnly`
  **Scenario**: `introduce_contract`, a token this version does not know, `""`.
  **Expected behavior**: the `BalancingMoves` words; the token as written; `""`.

### Integration Contract Tests

- **Test name**: `TestDecodeRealEngineReport`
  **Scenario**: the fixture captured from engine v3.0.0 (`real-blocked-no-baseline`): verdict `blocked`, `hard_gates: fail`, one active
  blocker, populated `matched_by`, `gate_reference` `non_comparable` with reason `no baseline file was loaded`, nine dimensions, one seam.
  **Expected behavior**: decodes; those values, the bare-hex `config_hash`, profile version `archfit.measurement.v2` and the task's
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
  **Expected behavior**: no `declarations`, `measurement`, `provenance`, `denominator` or `scores` key anywhere, and no `matched_by` key
  outside `MatchedByKeys`; the second decode equals the first; counts of findings, tasks and seams equal the counts in the fixture file.
- **Test name**: `TestEngineSchemaKeySetIsCovered`
  **Scenario**: walk the pinned v3.0.1 `internal/wire/testdata/schema/archfit.state.schema.json` and the shadow types by
  reflection; decode the real capture, the all-keys fixture and `real-seam-policy`.
  **Expected behavior**: every schema property is a shadow field, and the reverse; kept plus dropped equals the schema key set. A
  missing schema file fails the test; it never skips.
- **Test name**: `TestRootImporterCapture`
  **Scenario**: the unmodified capture `real-root-importer`: a root `main.go` that no module owns imports a module whose
  `visible_to` excludes it, under a fail-gated `module_dependencies` rule with `rationale`, `alternatives` and `docs`.
  **Expected behavior**: decodes; the finding's `edge.from` is `{module: "", path: "."}`; the task files hold
  `.`; the why ends with ` — <rationale> (see <docs>)`, the constraint with ` (see <docs>)`; the task has the `rationale: ` entry.
- **Test name**: `TestEngineVersions`
  **Scenario**: call `EngineVersions`, change the returned slice, call it again.
  **Expected behavior**: `v3.0.0`, `v3.0.1` both times: the caller gets a copy.
- **Test name**: `TestUnknownEngineVersionIsRefused`
  **Scenario**: the real capture with the release `""`, `v2.5.0`, `v2.6.0`, `3.0.0`, `v3.0.0 ` (trailing space), `V3.0.0`,
  `v3.0.2`, `v3.1.0` and a `v3.0.1` carrying `LEAK-7f3a`.
  **Expected behavior**: each is an error naming `engine_version`, with a zero `Report`; no error contains the marker.
- **Test name**: `TestDecisionCoreHasShadowFields`
  **Scenario**: walk the shadow fields of the frozen decision core.
  **Expected behavior**: each one has a shadow field.
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
  with the marker, and whose seam `role_expectation` has it too.
  **Expected behavior**: decodes; the marker appears nowhere in the `Report`, in its marshalled JSON, or in its `%+v` formatting.
- **Test name**: `TestMatchedByKeepsOnlyTheAllowList`
  **Scenario**: a finding whose `matched_by` holds `metric`, `before`, `after`, `threshold`, `from_glob` and `cycle_modules`; a seam with `hypothesis` `introduce_contract`.
  **Expected behavior**: `Finding.MatchedBy` equals the four allow-listed keys; `Seam.Hypothesis` is `introduce_contract`; the marshalled `Report` holds no other key's value.
- **Test name**: `TestKeptMatchedByAndHypothesisAreChecked`
  **Scenario**: a kept `matched_by` value and a `hypothesis` with a line break; a dropped `matched_by` value with a line break; an absent `hypothesis`.
  **Expected behavior**: the first two fail naming `findings[0].matched_by.before` and `seams[0].hypothesis`; the other two decode.
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

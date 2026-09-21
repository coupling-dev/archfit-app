# UI Graph

**Path**: internal/control/ui/graph/
**Parent**: internal/control/ui/ (UI)
**Submodules**: none (leaf)

## Purpose

Graph turns one engine report plus the current policy text into a read-only picture of modules and
dependencies that the UI can draw. It keeps three product promises in one pure function: observed
dependencies are told apart from permitted ones, a module can be found in the YAML, and a picture
that no longer matches the policy text is marked stale instead of looking current. Without it this
logic would sit in templates and JavaScript where it cannot be tested.

## Functional Responsibilities

- Build one `Node` per module and one `Link` per dependency edge of the report, in a stable order.
- Mark each link as observed (seen by the engine) and as permitted (allowed by the policy the engine used).
- Find the 1-based line of each module in the policy YAML so that selecting a node jumps to its definition.
- Set `Stale` when the policy text is not the policy the report was computed from.

Graph never edits YAML and never proposes a change. Visual edits are deferred (decisions.md §6).

## Subdomain Classification

Core-adjacent product surface, but small and pure. Volatility: moderate. It changes when the engine
report's module and edge fields change (a versioned wire release) or when the view needs a new fact.
Layout, colors and interaction are not here; they are the parent's templates and scripts.

## Encapsulated Knowledge

- The line scan that finds a module in the policy text, and the two entry shapes it accepts.
- How duplicate modules, duplicate edges and edges to unknown modules are normalized.
- The ordering rule of the output.
- The meaning of `Stale`: a digest comparison, not a YAML comparison.

## Public Contract

```go
type Node struct {
    Name       string
    Layer      string
    PolicyLine int // 1-based line of the module in the policy YAML; 0 if not found
}

type Link struct {
    From, To  string
    Observed  bool // seen in the report
    Permitted bool // allowed by policy
    Count     int
}

// View is a read-only picture. Stale is true when the policy text no longer matches the report's config_hash.
type View struct {
    Nodes []Node
    Links []Link
    Stale bool
}

// Build is pure: report modules and edges plus the current policy text.
func Build(rep Report, policy []byte) View
```

### Rules of `Build`

1. Nodes: one per `rep.Modules` entry. On a duplicate `Name` the first entry wins. `Layer` is copied.
2. Links: one per distinct `(From, To)` of `rep.Edges`. Duplicates merge **[default]**: `Count` is the sum,
   `Permitted` is true only if every duplicate is permitted. `Observed = Count > 0`.
3. An edge endpoint that is not a module gets a node with that name, empty `Layer` **[default]**. An observed
   dependency is never hidden because the module list is incomplete. An edge with an empty endpoint is dropped.
4. Order: `Nodes` by `Name`, `Links` by `(From, To)`, byte-wise ascending. The same input always gives the same output.
5. `PolicyLine`: result of the line scan below for the node's `Name`; `0` when not found.
6. `Stale = Digest(policy) != rep.Comparison.ConfigHash`. An empty `ConfigHash` or empty `policy` is stale.
7. `Build` never fails and never panics. A zero `Report` gives an empty, stale `View`.

Stated limit: a relationship the policy permits but the engine did not list as an edge is not drawn. The engine's
edge list with `Permitted` and `Count` is the only source; graph does not interpret policy rules.

### Line scan

Graph does not parse YAML: only `wire/settings` and `wire/policy` may import a YAML library (root import rule).
The scan is line based **[default]**; the accepted shapes are to be pinned against engine v2.3.1 policy fixtures:

1. Find the first line that is exactly the top-level key `modules:` (column 0, optional trailing comment).
2. The block ends at the next line that starts at column 0 and is neither blank nor a comment.
3. Inside the block, at the least indentation used by entries, accept a list entry `- name: X` and a map key `X:`.
   `X` may be plain, single-quoted or double-quoted. A trailing comment is ignored.
4. The first match of a name wins. Nested keys at deeper indentation are never matched.
5. Lines are split on `\n`; a trailing `\r` is ignored. Line numbers are 1-based.

Invalid YAML needs no special case: edited text has another digest than the report's `config_hash`, so the view is
stale, and lines are still found on a best-effort basis so that the jump keeps working while the user repairs the text.

## Integrations

### internal/control/ui (parent)

- **Counterpart**: `internal/control/ui/module.md`
- **Direction**: ui depends on graph; graph never imports ui
- **Strength**: functional (composition)
- **LCA / Rank / Distance**: control/ui / 1 / 1
- **Volatility**: high (the UI), moderate on this side
- **Balanced?**: yes — parent and child, distance 1
- **Shared knowledge**: the contract above. The parent downloads and decodes the report, loads the policy text,
  serializes the `View` for its script, and shows the stale banner.

### internal/wire/report

- **Counterpart**: `internal/wire/report/module.md`
- **Direction**: graph depends on wire/report (types only; the parent calls `Decode`)
- **Strength**: contract (published language)
- **LCA / Rank / Distance**: root / 3 / 3
- **Volatility**: moderate
- **Balanced?**: yes — contract coupling tolerates the distance
- **Shared knowledge**: graph reads `Modules`, `Edges` and `Comparison.ConfigHash`, nothing else. `Decision`, `Finding`,
  `Dimension` and `AgentTask` are opaque parts of `Report` for this module. `StateSchemaV1` is the constant wire/report
  checks at decode time; `MaxBytes` is the constant that caps a payload; graph uses neither.

<!-- contract: Report, StateSchemaV1, MaxBytes — restated from internal/wire/report/module.md -->
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

<!-- contract: Comparison, MeasurementProfile, Producer, GateReference — restated from internal/wire/report/module.md -->
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

<!-- contract: Module, Edge — restated from internal/wire/report/module.md -->
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

### internal/wire/policy

- **Counterpart**: `internal/wire/policy/module.md`
- **Direction**: graph depends on wire/policy (`Digest` only)
- **Strength**: contract
- **LCA / Rank / Distance**: root / 3 / 3
- **Volatility**: moderate
- **Balanced?**: yes — contract coupling tolerates the distance
- **Shared knowledge**: `Digest` is the one definition of how `config_hash` is computed. Graph calls it instead of
  hashing on its own, so the digest format lives in one place. `PolicyPath` and `BaselinePath` are the constants naming
  the engine files; graph never reads files and uses neither.

<!-- contract: PolicyPath, BaselinePath, Digest — restated from internal/wire/policy/module.md -->
```go
// PolicyPath and BaselinePath are the engine files on the protected ref. [default]
const (
    PolicyPath   = ".archfit.yaml"
    BaselinePath = ".archfit/baseline.json"
)

// Digest returns "sha256:<hex>" of the raw file bytes. It is the value the engine reports as
// comparison.config_hash (policy) and gate_reference.digest (baseline).
func Digest(raw []byte) string
```

## Change Vectors

- The accepted `modules:` entry shapes, when the engine policy schema changes.
- More per-node or per-link facts from the report (finding counts per module, a violated flag).
- The merge rule for duplicate edges.
- Bounded visual edits later: a new function that returns a proposed text patch. `Build` stays read-only.

## Constraints and Invariants

- Pure: no I/O, no clock, no logging, no globals, no goroutines. Safe for concurrent use by construction.
- Imports: standard library, `wire/report`, `wire/policy`. No YAML library.
- Never writes or regenerates policy text, so it cannot drop comments or YAML constructs it does not understand.
- `Paths` of a module are not copied into the `View`: the picture carries names and layers only.
- No score, no aggregate health number.
- Cost is linear in modules + edges + policy lines. The report decoder caps modules at 5 000; the parent caps the
  policy text. Graph adds no cap of its own.
- Inherited: control-plane code, never on the gate path. `Stale` is a display fact and never an input to a check.

## Test Specification

All tests are plain table-driven Go tests over literal `Report` values and policy strings. Nothing is mocked.

### Unit Tests

- **Test name**: `TestPolicyLineScan`
  **Scenario**: table of policy texts: list entries `- name: api`; map keys `api:`; quoted names; trailing comments;
  CRLF line endings; `modules:` not the first key; a nested `name:` deeper in an entry; a `name:` outside the block;
  the same name twice; no `modules:` key; `modules: []`; text that is not YAML at all.
  **Expected behavior**: the 1-based line of the first entry with that name inside the block; `0` otherwise; nested and
  outside matches are ignored; no panic.
- **Test name**: `TestLinkNormalization`
  **Scenario**: table: single edge; duplicate edges with counts 2 and 3, one not permitted; `Count` 0 with `Permitted` true;
  edge to a name missing from `Modules`; edge with an empty endpoint.
  **Expected behavior**: one link per pair; sum 5 and `Permitted` false; `Observed` false for count 0; a node is added for
  the missing name with empty `Layer`; the empty-endpoint edge is dropped.
- **Test name**: `TestDuplicateModuleFirstWins`
  **Scenario**: two modules named `core` with different layers.
  **Expected behavior**: one node with the first layer.

### Integration Contract Tests

- **Test name**: `TestBuildMapsReportToView`
  **Scenario**: a report with three modules in two layers and four edges (permitted observed, forbidden observed, permitted
  unobserved, self edge), with the policy text it was computed from.
  **Expected behavior**: three nodes with correct `Layer` and `PolicyLine`; four links with the expected `Observed` and
  `Permitted` pairs; `Stale` false.
- **Test name**: `TestBuildIsDeterministic`
  **Scenario**: build twice from the same input, and once from the same modules and edges shuffled.
  **Expected behavior**: three identical views, nodes sorted by name, links by `(From, To)`.
- **Test name**: `TestStaleFollowsPolicyDigest`
  **Scenario**: table: same bytes; one added space; one added comment; empty policy; empty `ConfigHash`; `ConfigHash` in upper case.
  **Expected behavior**: `Stale` false only for the first row.

### Boundary Tests

- **Test name**: `TestBuildNeverFails`
  **Scenario**: table: zero `Report`; nil policy; 5 000 modules with 50 000 edges; policy of only blank lines; binary bytes as policy.
  **Expected behavior**: a `View` is returned without panic; the large row finishes within the test timeout.
- **Test name**: `TestViewCarriesNoPaths`
  **Scenario**: modules with recognizable `Paths` values; marshal the `View` to JSON.
  **Expected behavior**: no path string appears in the output.
- **Test name**: `TestGraphImportsNoYAMLAndDoesNoIO`
  **Scenario**: `go list -deps ./internal/control/ui/graph`.
  **Expected behavior**: only the standard library, `wire/report` and `wire/policy`; no `os`, `net`, or YAML package.

### Behavior Tests

- **Test name**: `TestInvalidYAMLMarksGraphStale` (T17)
  **Scenario**: a report and its policy; then the policy text is edited into invalid YAML (a broken indent under `modules:`).
  **Expected behavior**: `Stale` true; nodes and links still come from the report; lines that can still be found are kept,
  others are `0`; the stale flag is never cleared by anything but matching bytes.
- **Test name**: `TestUnsupportedYAMLConstructsAreUntouched` (T17)
  **Scenario**: a policy using anchors, aliases, flow style and comments; build the view.
  **Expected behavior**: the policy bytes are unchanged after `Build`; unknown constructs only ever cost a `PolicyLine` of `0`.
- **Test name**: `TestObservedIsToldApartFromPermitted`
  **Scenario**: a blocked fixture: one forbidden dependency observed twice, one permitted dependency observed once.
  **Expected behavior**: the forbidden link is `Observed` true, `Permitted` false, `Count` 2; the permitted link is true, true, 1.
- **Test name**: `TestSelectingANodeFindsItsYAML`
  **Scenario**: the healthy wire fixture with its policy file.
  **Expected behavior**: every node of a module declared in the policy has the line of its entry; opening that line in
  the policy text shows the module's name.

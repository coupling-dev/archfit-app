# UI Graph

**Path**: internal/control/ui/graph/
**Parent**: internal/control/ui/ (UI)
**Submodules**: none (leaf)

## Purpose

Graph turns one engine report plus the current policy text into a read-only picture of modules and
dependencies that the UI can draw. It keeps three product promises in one pure function: observed
dependencies that violate the policy are told apart from permitted ones, a module can be found in the
YAML, and a picture that no longer matches the policy text is marked stale instead of looking current.
Without it this logic would sit in templates and JavaScript where it cannot be tested.

## Functional Responsibilities

- Build one `Node` per module declared in the policy, with its layer and its line in the YAML.
- Build one `Link` per observed module-to-module seam of the report, in a stable order.
- Mark a link as not permitted when an active gate finding of the report names that module pair.
- Set `Stale` when the policy text is not the policy the report was computed from, or does not parse.

Graph never edits YAML and never proposes a change. Visual edits are deferred (decisions.md §6).

## Subdomain Classification

Core-adjacent product surface, but small and pure. Volatility: moderate. It changes when the engine
report's seam or finding fields change (a versioned wire release) or when the view needs a new fact.
Layout, colors and interaction are not here; they are the parent's templates and scripts.

## Encapsulated Knowledge

- Which findings make a link "not permitted": `Kind == "gate"` and `Status` `new` or `expired_waiver` **[default]**.
- How duplicate seams, seams to undeclared modules, and findings without a seam are normalized.
- The ordering rule of the output.
- The meaning of `Stale`: a digest comparison plus "the text parses", not a YAML comparison.

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

// Build is pure. Nodes come from the policy's modules, links from the report's seams; a link is not
// Permitted when a gate finding with status "new" or "expired_waiver" names that module pair.
func Build(rep Report, policy []byte) View
```

### Rules of `Build`

1. Modules: `defs, err := policy.Modules(policyText)`. One node per `ModuleDef`: `Name`, `Layer`, `PolicyLine = Line`.
   On a duplicate name the first wins.
2. Links: one per distinct `(FromModule, ToModule)` of `rep.Seams`. Duplicates merge **[default]**: `Count` is the sum
   of `Edges`. A seam with an empty endpoint is dropped. `Observed` is true for every link: a seam is an observation.
3. `Permitted` is false when a finding with `Kind == "gate"` and `Status` `new` or `expired_waiver` has
   `Edge.From.Module == From` and `Edge.To.Module == To` **[default]**; otherwise true. Findings with status `baseline`,
   `waived` or `fixed`, advisory findings, and findings with an empty module on either side do not change it.
4. Such an active gate finding whose pair has no seam still yields a link, `Count` 0, `Permitted` false **[default]**:
   a violation is never hidden because the seam list is incomplete.
5. A link endpoint that the policy does not declare gets a node with that name, empty `Layer`, `PolicyLine` 0.
6. `Stale = policy.Digest(policyText) != rep.Comparison.ConfigHash`. Both are bare lower-case hex; the comparison is
   exact. An empty `ConfigHash` or empty policy text is stale.
7. `policy.Modules` error (invalid YAML): nodes come only from rule 5, and `Stale` is true whatever the digests say.
   The caller is expected to keep showing the view of the last valid text, marked stale (the parent does).
8. Order: `Nodes` by `Name`, `Links` by `(From, To)`, byte-wise ascending. The same input always gives the same output.
9. `Build` never fails and never panics. A zero `Report` with an empty policy gives an empty, stale `View`.

Stated limits **[default]**: `Observed` is always true in v1, because a relationship the policy permits but the engine
did not observe is not in a report and graph does not interpret policy rules. Accepted debt (baseline, waived) draws
as permitted; the PR comment, not the graph, is where debt is listed.

## Integrations

### internal/control/ui (parent)

- **Counterpart**: `internal/control/ui/module.md`
- **Direction**: ui depends on graph; graph never imports ui
- **Strength**: functional (composition)
- **LCA / Rank / Distance**: control/ui / 1 / 1
- **Volatility**: high (the UI), moderate on this side
- **Balanced?**: yes — parent and child, distance 1
- **Shared knowledge**: the contract above. The parent downloads and decodes the report, loads the policy text, falls
  back to the last valid policy text when the current one does not parse, serializes the `View` for its script, and
  shows the stale banner.

### internal/wire/report

- **Counterpart**: `internal/wire/report/module.md`
- **Direction**: graph depends on wire/report (types only; the parent calls `Decode`)
- **Strength**: contract (published language)
- **LCA / Rank / Distance**: root / 3 / 3
- **Volatility**: moderate
- **Balanced?**: yes — contract coupling tolerates the distance
- **Shared knowledge**: graph reads `Seams` (`FromModule`, `ToModule`, `Edges`), `Findings` (`Kind`, `Status`,
  `Edge.From.Module`, `Edge.To.Module`) and `Comparison.ConfigHash`, nothing else. `Decision` is an opaque part of `Report`
  for this module. `Reference`, `Dimension`, `Coverage`, `AgentTask` and the other types in the fences below are restated
  only because they share a fence with a type graph reads; graph never touches them. `StateSchemaV1` is the constant
  wire/report checks at decode time; `MaxBytes` is the constant that caps a payload; graph uses neither.

<!-- contract: Report, StateSchemaV1, MaxBytes — restated from internal/wire/report/module.md -->
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

<!-- contract: Comparison, Reference, MeasurementProfile, Producer — restated from internal/wire/report/module.md -->
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

<!-- contract: Finding, Edge, Endpoint, Location, AgentTask — restated from internal/wire/report/module.md -->
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

<!-- contract: Dimension, DimensionCoverage, Metric, FindingRef, UnknownFact, Delta, MetricDelta, Coverage, ToolCoverage, Seam — restated from internal/wire/report/module.md -->
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

### internal/wire/policy

- **Counterpart**: `internal/wire/policy/module.md`
- **Direction**: graph depends on wire/policy (`Modules`, `Digest`)
- **Strength**: contract
- **LCA / Rank / Distance**: root / 3 / 3
- **Volatility**: moderate
- **Balanced?**: yes — contract coupling tolerates the distance
- **Shared knowledge**: `Modules` is the one reader of the policy's `modules:` map; graph has no YAML code of its own.
  `Digest` is the one definition of how `config_hash` is computed, so the digest format lives in one place.
  `PolicyPath`, `BaselinePath` and `LabelsPath` are the constants naming the engine files; graph never reads files and uses none.

<!-- contract: PolicyPath, BaselinePath, LabelsPath, Digest — restated from internal/wire/policy/module.md -->
```go
// The engine's three trusted input files on the protected ref. Baseline and labels sit beside the policy.
const (
    PolicyPath   = ".archfit.yaml"
    BaselinePath = ".archfit-baseline.json"
    LabelsPath   = ".archfit-labels.yaml"
)

// Digest returns the bare lowercase hex SHA-256 of the raw file bytes. For the policy file it equals the
// engine's comparison.config_hash. The engine reports no digest for baseline or labels; for those the
// producer states Digest of the bytes it used in the envelope, and the App compares it with the protected ref.
func Digest(raw []byte) string
```

<!-- contract: ModuleDef, Modules — restated from internal/wire/policy/module.md -->
```go
// ModuleDef is the subset of a policy module the App displays.
type ModuleDef struct {
    Name  string
    Layer string // may be ""
    Line  int    // 1-based line of the module key in the policy text
}

// Modules lists the modules declared under the policy's `modules:` map, in file order. It ignores every other key.
func Modules(policy []byte) ([]ModuleDef, error)
```

## Change Vectors

- The rule that makes a link "not permitted" (for example showing waived or baseline debt as its own state).
- More per-node or per-link facts from the report (finding counts per module, seam strength or severity).
- The merge rule for duplicate seams.
- Bounded visual edits later: a new function that returns a proposed text patch. `Build` stays read-only.

## Constraints and Invariants

- Pure: no I/O, no clock, no logging, no globals, no goroutines. Safe for concurrent use by construction.
- Direct imports: standard library, `wire/report`, `wire/policy`. No YAML library of its own.
- Never writes or regenerates policy text, so it cannot drop comments or YAML constructs it does not understand.
- File paths of findings and endpoints (`Endpoint.Path`, `Location.File`) are never copied into the `View`: the picture
  carries module names, layers, lines of the policy file and counts only.
- No score, no aggregate health number. Seam `Strength`, `Distance`, `Volatility` and `Severity` are not shown in v1.
- Cost is linear in modules + seams + findings. The report decoder caps findings and seams; the parent caps the policy
  text. Graph adds no cap of its own.
- Inherited: control-plane code, never on the gate path. `Stale` and `Permitted` are display facts and never an input to a check.

## Test Specification

All tests are plain table-driven Go tests over literal `Report` values and policy strings, with the real `wire/policy`.
Nothing is mocked.

### Unit Tests

- **Test name**: `TestNodesFromPolicyModules`
  **Scenario**: table of policy texts: three modules in two layers; a module without a layer; quoted names; comments and
  anchors around the map; `modules:` not the first key; no `modules:` key; `modules: {}`.
  **Expected behavior**: one node per declared module with the layer and the 1-based line `policy.Modules` reports; no nodes
  for the last two rows.
- **Test name**: `TestLinkNormalization`
  **Scenario**: table: single seam; duplicate seams with `Edges` 2 and 3; self seam; seam to a module the policy does not
  declare; seam with an empty `FromModule`.
  **Expected behavior**: one link per pair; `Count` 5 for the duplicate; the self seam is kept; a node with empty `Layer` and
  `PolicyLine` 0 is added for the undeclared module; the empty-endpoint seam is dropped; `Observed` true everywhere.
- **Test name**: `TestPermittedFollowsActiveGateFindings`
  **Scenario**: one seam `api → db` and a table of findings on that pair: gate/new; gate/expired_waiver; gate/baseline;
  gate/waived; gate/fixed; advisory/new; gate/new on `db → api`; gate/new with an empty `To.Module`; no finding.
  **Expected behavior**: `Permitted` false for the first two rows only; direction matters.
- **Test name**: `TestActiveFindingWithoutSeamStillDraws`
  **Scenario**: a gate/new finding on `ui → db` with no seam for that pair.
  **Expected behavior**: a link `ui → db`, `Count` 0, `Observed` true, `Permitted` false.

### Integration Contract Tests

- **Test name**: `TestBuildMapsReportToView`
  **Scenario**: the blocked wire fixture (engine v2.3.1 state JSON decoded by `report.Decode`) with the policy file it was computed from.
  **Expected behavior**: nodes equal the policy's modules with correct `Layer` and `PolicyLine`; links equal the fixture's
  seams; exactly the pairs named by active gate findings are not permitted; `Stale` false.
- **Test name**: `TestBuildIsDeterministic`
  **Scenario**: build twice from the same input, and once with seams and findings shuffled.
  **Expected behavior**: three identical views, nodes sorted by name, links by `(From, To)`.
- **Test name**: `TestStaleFollowsPolicyDigest`
  **Scenario**: table: same bytes; one added space; one added comment; empty policy; empty `ConfigHash`; `ConfigHash` in
  upper case; `ConfigHash` with a `sha256:` prefix.
  **Expected behavior**: `Stale` false only for the first row.

### Boundary Tests

- **Test name**: `TestBuildNeverFails`
  **Scenario**: table: zero `Report`; nil policy; 5 000 modules with 50 000 seams; policy of only blank lines; binary bytes as policy.
  **Expected behavior**: a `View` is returned without panic; the large row finishes within the test timeout.
- **Test name**: `TestViewCarriesNoPaths`
  **Scenario**: findings with recognizable `Endpoint.Path` and `Location.File` values; marshal the `View` to JSON.
  **Expected behavior**: no path string appears in the output.
- **Test name**: `TestGraphHasNoYAMLCodeAndDoesNoIO`
  **Scenario**: `go list -f '{{.Imports}}' ./internal/control/ui/graph`.
  **Expected behavior**: direct imports are the standard library, `wire/report` and `wire/policy` only; no `os`, `net`, or YAML package.

### Behavior Tests

- **Test name**: `TestInvalidYAMLMarksGraphStale` (T17)
  **Scenario**: a report and its policy; then the policy text is edited into invalid YAML (a broken indent under `modules:`).
  **Expected behavior**: `Stale` true; links still come from the report; nodes are the link endpoints with `PolicyLine` 0;
  building again with the last valid text gives the full node list, and stays stale only if its digest differs.
- **Test name**: `TestUnsupportedYAMLConstructsAreUntouched` (T17)
  **Scenario**: a policy using anchors, aliases, flow style and comments; build the view.
  **Expected behavior**: the policy bytes are unchanged after `Build`; graph returns data only and has no function that writes text.
- **Test name**: `TestViolationIsToldApartFromPermitted`
  **Scenario**: the blocked fixture: one forbidden dependency observed twice, one allowed dependency observed once.
  **Expected behavior**: the forbidden link is `Permitted` false with `Count` 2; the allowed link is `Permitted` true with `Count` 1.
- **Test name**: `TestSelectingANodeFindsItsYAML`
  **Scenario**: the healthy wire fixture with its policy file.
  **Expected behavior**: every node of a declared module has the line of its key; that line of the policy text contains the module's name.

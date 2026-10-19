# UI Graph

**Path**: internal/control/ui/graph/
**Parent**: internal/control/ui/ (UI)
**Submodules**: none (leaf)

## Purpose

Graph turns one engine report plus the current policy text into a read-only picture of modules and
dependencies that the UI can draw. It keeps three product promises in one pure function: each observed
dependency carries what the report says about it (the engine's seam policy status when the report has one, else what
its findings say: a violation, accepted debt, an advisory, or nothing), a module can be found in the YAML, and a
picture that no longer matches the policy text is marked stale instead of looking current. Graph never infers
"allowed" itself: only the engine's `seams[].policy` can say a dependency is allowed, and a baselined forbidden edge
is accepted debt, never allowed. Without it this logic would sit in templates and JavaScript where it cannot be
tested.

## Functional Responsibilities

- Build one `Node` per module declared in the policy, with its layer, its line in the YAML, and the volatility every
  seam into it agrees on.
- Build one `Link` per observed module-to-module seam of the report, and per module pair a current finding names, in
  a stable order.
- Give each link its `Status`: the engine's `seams[].policy` of the pair when a seam carries it (`violation`,
  `accepted`, `advisory`, `allowed`, or `observed`); otherwise the status the report's findings on that module pair
  give: `violation`, `accepted`, `advisory`, or `observed`.
- Set `Stale` when the policy text is not the policy the report was computed from, or does not parse.
- Set `Incomplete` when the report's hard gates did not pass or a required rule was not evaluated, so that an
  `observed` link is not read as clean.
- Give each link the coupling of its strongest seam (strength, distance in words, volatility) and the worst
  imbalance the engine reported on the pair.
- Give each link its `Origin` by the PR comment's rule (`report.PairOrigin`) when the engine compared with the base
  branch, without touching its `Status`.

Graph never edits YAML and never proposes a change. Visual edits are deferred (decisions.md §6).

## Subdomain Classification

Core-adjacent product surface, but small and pure. Volatility: moderate. It changes when the engine
report's seam or finding fields change (a versioned wire release) or when the view needs a new fact.
Layout, colors and interaction are not here; they are the web app's.

## Encapsulated Knowledge

- The rule that the engine's seam policy status, when present, is the link's status, and findings do not change it:
  the engine owns the rules and computes the status from the same findings, so a second reading here could only
  disagree with it (for example, the engine counts only an active advisory, a baselined one names nothing).
- The status a finding gives its pair, and the precedence when several findings or seams name it. Which findings are active
  (a violation) is not graph's knowledge: `report.Finding.Active()` of wire/report decides, the same rule the gate's
  feedback uses. Graph knows only the accepted statuses (`baseline`, `waived`), the `fixed` status and the `gate`
  kind, the display vocabulary feedback reads too. A `gate` finding with any other status is drawn as a violation
  (fail safe), as feedback treats an unknown status as live.
- How duplicate seams, seams to undeclared modules, and findings without a seam are normalized.
- The ordering rule of the output.
- The meaning of `Stale`: a digest comparison plus "the text parses", not a YAML comparison.
- The meaning of `Incomplete`: `Decision.HardGates` other than `pass`, or a non-empty
  `Decision.UnevaluatedRequiredRules`.

## Public Contract

```go
type Node struct {
    Name       string `json:"name"`
    Layer      string `json:"layer"`
    PolicyLine int    `json:"policy_line"` // 1-based line of the module in the policy YAML; 0 if not found
    // Volatility is the declared volatility the engine gave every seam into the module (Balanced Coupling: the
    // volatility of the module depended upon); "" when the seams disagree, say undeclared, or there are none.
    Volatility string `json:"volatility"`
}

// LinkStatus is what the report says about a module pair: the engine's seam policy status when the report carries
// one, else what the findings on the pair say. It is a display fact, never a check input.
type LinkStatus string

const (
    StatusViolation LinkStatus = "violation" // an active gate finding (wire/report Finding.Active), or a gate finding of unknown status, names the pair
    StatusAccepted  LinkStatus = "accepted"  // a gate finding accepted by the baseline or a waiver names the pair
    StatusAdvisory  LinkStatus = "advisory"  // another finding that is not fixed names the pair
    StatusAllowed   LinkStatus = "allowed"   // engine seam policy only: a fail-gated allowlist or layer rule permits the pair
    StatusObserved  LinkStatus = "observed"  // engine seam policy: no active finding names the pair and no rule permits it; without one: no current finding names the pair
)

type Link struct {
    From   string     `json:"from"`
    To     string     `json:"to"`
    Status LinkStatus `json:"status"`
    Count  int        `json:"count"` // observed dependencies of the pair's seams; 0 when only a finding names the pair
    // Origin is report.PairOrigin of the pair, the PR comment's rule: report.PairAdded when this change added the
    // dependency, report.PairExisting when the base had it, "" when the report cannot say or did not compare. Display only.
    Origin string `json:"origin"`
    // The coupling of the pair's strongest seam, as the engine measured it; all "" when no seam names the pair.
    Strength   string `json:"strength"`   // engine token: contract, model, functional, intrusive
    Distance   string `json:"distance"`   // in words (report.DistanceWords)
    Volatility string `json:"volatility"` // engine token
    Imbalance  string `json:"imbalance"`  // the highest seam severity of the pair, the engine's imbalance; "" for none
}

// View is a read-only picture. Stale is true when the policy text no longer matches the report's config_hash.
// Incomplete is true when the report's hard gates did not pass or a required rule was not evaluated: an observed
// link may then still break a rule.
type View struct {
    Nodes      []Node        `json:"nodes"`
    Links      []Link        `json:"links"`
    Findings   []LinkFinding `json:"findings"` // what names each link, for the page's inspector; display only
    Stale      bool          `json:"stale"`
    Incomplete bool          `json:"incomplete"`
}

// LinkFinding is one finding that names a drawn link's module pair, as the engine reported it. It explains a link and
// never decides its Status. It carries no origin: what a pull request added is the link's Origin, the comment's rule,
// and a cycle finding's own origin would contradict it. Why is engine text: the page must treat it as untrusted.
type LinkFinding struct {
    From   string `json:"from"`
    To     string `json:"to"`
    RuleID string `json:"rule_id"`
    Kind   string `json:"kind"`
    Status string `json:"status"`
    Why    string `json:"why"`
    More   int    `json:"more"` // on the last listed finding of a pair: how many more name the pair and are not listed
}

// Build is pure. Nodes come from the policy's modules, links from the report's seams and findings. A link's Status
// is the engine's seams[].policy when a seam of the pair carries it; otherwise it comes from the findings that name
// the pair. Graph never computes a status from the policy text.
func Build(rep Report, policy []byte) View
```

### Rules of `Build`

1. Modules: `defs, err := policy.Modules(policyText)`. One node per `ModuleDef`: `Name`, `Layer`, `PolicyLine = Line`.
   On a duplicate name the first wins.
2. Links: one per distinct `(FromModule, ToModule)` of `rep.Seams`. Duplicates merge **[default]**: `Count` is the sum
   of `Edges`. A seam with an empty endpoint is dropped. A seam alone, without `Policy`, gives `observed`.
3. Engine status: when a seam of the pair has a non-empty `Policy` (wire/report admits only `violation`, `accepted`,
   `advisory`, `allowed`, `observed`), the link's `Status` is that value, the strongest one
   when duplicate seams of the pair differ, and rule 4 does not apply to the pair. Graph shows the engine's status; it
   does not check it against the findings.
4. Finding status, for a pair no seam `Policy` decided (the `Policy` key is optional):
   `Status` comes from the findings with `Edge.From.Module == From` and `Edge.To.Module == To`. Each finding gives one
   status: `violation` when `Finding.Active()` is true (the rule wire/report owns: today a gate finding with status `new`
   or `expired_waiver`); none when its status is `fixed` (it says nothing about the current tree); `accepted` for a
   `gate` finding with status `baseline` or `waived`; `violation` for a `gate` finding with any other status, one this
   version does not know included (fail safe: feedback treats an unknown status as live); `advisory` for every other
   finding, an advisory of any status and a kind this version does not know included, so a finding is never drawn as
   a plain observation. The
   strongest wins: `violation` > `accepted` > `advisory` > `observed`. Findings with an empty module on either side do
   not change a link. Findings never give `allowed`. The policy text never sets a status: graph does not evaluate
   rules. The full order, used for duplicate seams too, is the engine's: `violation` > `accepted` > `advisory` >
   `allowed` > `observed`.
5. A finding that gives a status to a pair without a seam still yields a link, `Count` 0 **[default]**: a violation or
   accepted debt is never hidden because the seam list is incomplete. A fixed finding yields no link.
6. A link endpoint that the policy does not declare gets a node with that name, empty `Layer`, `PolicyLine` 0.
7. `Stale = policy.Digest(policyText) != rep.Comparison.ConfigHash`. Both are bare lower-case hex; the comparison is
   exact. An empty `ConfigHash` or empty policy text is stale.
8. `policy.Modules` error (invalid YAML): nodes come only from rule 6, and `Stale` is true whatever the digests say.
   The caller is expected to keep showing the view of the last valid text, marked stale (the parent does).
9. Order: `Nodes` by `Name`, `Links` by `(From, To)`, byte-wise ascending. The same input always gives the same output.
10. `Build` never fails and never panics. A zero `Report` with an empty policy gives an empty, stale, incomplete `View`.
11. `Incomplete = rep.Decision.HardGates != "pass" || len(rep.Decision.UnevaluatedRequiredRules) > 0`. `fail`,
    `unmeasured`, an empty and an unknown value are all incomplete: the engine did not show that every required rule
    was evaluated and passed, so an `observed` link may still be a forbidden edge it did not evaluate. Display only,
    like `Stale`.
12. `Findings`: every finding whose `Edge.From.Module`/`Edge.To.Module` pair is a link of the view, except `fixed`
    ones, as `LinkFinding{From, To, RuleID, Kind, Status, Why, More}`. Per pair the strongest status first (the
    order of rule 6, then report order), so the cap never drops the finding that set the link's status; at most 5 per
    pair **[default]**, the last one carrying `More`, the count not listed; `Why` clipped to 400 runes with "…"
    **[default]**; sorted by `(From, To)`, stable. It never changes a `Status`:
    an advisory on a pair whose engine seam status is `allowed` is listed and the link stays `allowed`. Never nil.
13. Origin, a projection for a pull request report: `g := report.Group(rep, report.ShownPerGroup)`; only when
    `g.Compared` (the engine compared with the base branch), each link's `Origin` is
    `report.PairOrigin` of its pair over `g.All` and `g.OnCycle`, the rule the PR comment uses: `existing` when a
    pre-existing finding names the pair, else `added` when an introduced finding off a cycle does, else `""`. A pair
    no finding names stays `""`. Without a comparison every `Origin` is `""`, so nothing claims what a change added
    without the engine's comparison. `Origin` is never inferred from a status (`new` means "not accepted", not
    "added") and never changes `Status`: an added link with a gate finding stays a `violation`.
14. Coupling: per pair, the seam with the strongest `Strength` (`contract` < `model` < `functional` < `intrusive`; a
    token outside them ranks below all), then the most `Edges`, gives `Strength`, `Volatility`, and `Distance` in
    `report.DistanceWords` (a token outside them as written). `Imbalance` is the highest `Severity` of the pair's
    seams (`low` < `medium` < `high` < `critical`). A pair only a finding names has none of them.
15. Node volatility: when every seam into a module carries the same `Volatility` and it is `low`, `medium` or
    `high`, the node shows it, an undeclared module included; otherwise `""`.

Stated limits **[default]**: only the engine says "allowed". A relationship the policy permits but the engine did
not observe is not in a report, and graph does not interpret policy rules. When a seam carries `Policy`, `observed` is
the engine's: no active finding names the pair and no blocking rule permits it; a baselined or waived advisory can
still name the pair. When no seam of the pair carries `Policy`, no link is `allowed`, and `observed` means only that
no current finding names the pair: a rule may still permit it. A pair that only a finding names (no seam) keeps the
finding status of rule 4.

## Integrations

### internal/control/ui (parent)

- **Counterpart**: `internal/control/ui/module.md`
- **Direction**: ui depends on graph; graph never imports ui
- **Strength**: functional (composition)
- **LCA / Rank / Distance**: control/ui / 1 / 1
- **Volatility**: high (the UI), moderate on this side
- **Balanced?**: yes — parent and child, distance 1
- **Shared knowledge**: the contract above. The parent downloads and decodes the report, loads the policy text, falls
  back to the last valid policy text when the current one does not parse, clears `OriginStatus` for a default-branch
  report, serializes the `View` as JSON for the web app, and says why it is stale.

### internal/wire/report

- **Counterpart**: `internal/wire/report/module.md`
- **Direction**: graph depends on wire/report (types only; the parent calls `Decode`)
- **Strength**: contract (published language)
- **LCA / Rank / Distance**: root / 3 / 3
- **Volatility**: moderate
- **Balanced?**: yes — contract coupling tolerates the distance
- **Shared knowledge**: graph reads `Seams` (`FromModule`, `ToModule`, `Edges`, `Policy`, `Strength`, `Distance`,
  `Volatility`, `Severity`), `Findings` (`Kind`, `Status`, `Edge.From.Module`, `Edge.To.Module`, `RuleID`, `Why`),
  `Comparison.ConfigHash` and `Decision.HardGates` with `Decision.UnevaluatedRequiredRules` (only whether it is
  empty), and calls `Finding.Active()`, `Group`, `PairOrigin` with `ShownPerGroup`, and `DistanceWords`, nothing else:
  the origin rule, and whether the report compared, are wire/report's. `Reference`, `Dimension`, `Coverage`, `AgentTask` and the other types in the fences below are restated
  only because they share a fence with a type graph reads; graph never touches them. `StateSchemaV1` is the constant
  wire/report checks at decode time; `MaxBytes` is the constant that caps a payload; graph uses neither.

<!-- contract: Report, StateSchemaV1, MaxBytes — restated from internal/wire/report/module.md -->
```go
// StateSchemaV1 is the engine state contract accepted (archfit.state.schema.json, engine v3.0.0 and v3.0.1: one file).
const StateSchemaV1 = "archfit.architecture-state.v1"

// MaxBytes caps an uploaded payload: 5 MiB (the "5 MB" cap of decisions.md).
const MaxBytes = 5 << 20

// Report is the no-source transport form of an engine state report. The decoder knows every key of the engine
// state schema of v3.0.0 and v3.0.1: keys listed here are kept (length-capped), the other known keys are validated and dropped
// (measurement, findings[].matched_by, agent_tasks[].declarations, metric provenance, seam scores), and any key
// the schema does not define is a decode error.
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

<!-- contract: Decision, UnevaluatedRule — restated from internal/wire/report/module.md -->
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

<!-- contract: Comparison, Reference, MeasurementProfile, Producer — restated from internal/wire/report/module.md -->
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

<!-- contract: Active, Finding — restated from internal/wire/report/module.md -->
```go
// Active reports whether the finding currently blocks: a gate finding that is new or whose waiver expired.
// It is the single definition of "active gate finding" for every consumer.
func (f Finding) Active() bool

// Reverses reports whether f and g are the two directions of one module cycle: one rule, one kind, status and severity,
// and module pairs that are reversed, non-empty and distinct. Display only: each finding keeps its ID and gate effect.
func (f Finding) Reverses(g Finding) bool
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
    Severity   string `json:"severity,omitempty"`
    // Policy is what the engine's policy says about the seam: "violation" | "accepted" |
    // "advisory" | "allowed" | "observed", a closed set. "" = the key is absent.
    Policy string `json:"policy,omitempty"`
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

- The status rule: a new `seams[].policy` value comes with a new state schema version in wire/report, then a new
  `LinkStatus` and its rank here.
- More per-node or per-link facts from the report (finding counts per module).
- The merge rule for duplicate seams.
- Bounded visual edits later: a new function that returns a proposed text patch. `Build` stays read-only.

## Constraints and Invariants

- Pure: no I/O, no clock, no logging, no globals, no goroutines. Safe for concurrent use by construction.
- Direct imports: standard library, `wire/report`, `wire/policy`. No YAML library of its own.
- Never writes or regenerates policy text, so it cannot drop comments or YAML constructs it does not understand.
- File paths of findings and endpoints (`Endpoint.Path`, `Location.File`) are never copied into the `View`. The
  picture carries module names, layers, lines of the policy file, counts, and in `Findings` the rule ID, kind, status
  and `Why` of each finding on a drawn pair. `Why` is engine text and can name a path; it is shown as text only.
- No score, no aggregate health number. Seam `Strength`, `Distance`, `Volatility` and `Severity` are shown as the
  engine's words, never combined into a number.
- Cost is linear in modules + seams + findings. The report decoder caps findings and seams; the parent caps the policy
  text. Graph's own caps are on `Findings`: 5 per pair and 400 runes of `Why`.
- Inherited: control-plane code, never on the gate path. `Stale` and `Status` are display facts and never an input to a check.
- Never "allowed" on graph's own reading: only the engine's `seams[].policy` gives `allowed`, and a pair the engine
  decided keeps the engine's status. Without it, a finding is never drawn as a plain observation, and a fixed finding
  never draws a link.

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
  `PolicyLine` 0 is added for the undeclared module; the empty-endpoint seam is dropped; `observed` everywhere.
- **Test name**: `TestFindingsExplainDrawnLinks`
  **Scenario**: a finding on a seam; a fixed finding; a finding with an empty module; an advisory on a pair whose engine seam status is `allowed`; a `Why` longer than 400 runes; 7 findings on one pair and one on a pair that sorts first.
  **Expected behavior**: one `LinkFinding`; none; none; the advisory listed while the link stays `allowed`; the text clipped to 399 runes plus "…"; 5 for the pair in report order, the other pair first, the fifth with `More` 2; a gate finding listed after 5 advisories is listed first.
- **Test name**: `TestStatusFollowsFindings`
  **Scenario**: one seam `api → db` and a table of findings on that pair: gate/new; gate/expired_waiver; gate/baseline;
  gate/waived; gate/fixed; advisory/new; advisory/baseline; advisory/fixed; an unknown kind; a gate finding with an
  unknown status; that one beside gate/baseline; gate/new on `db → api`; gate/new with an empty `To.Module`; no
  finding; advisory, baseline and new together; advisory and waived together.
  **Expected behavior**: `violation` for the first two and for the gate finding with an unknown status (also beside
  baseline); `accepted` for baseline and waived; `observed` for fixed findings, the other direction, the empty module and
  no finding; `advisory` for advisories and unknown kinds; `violation` and `accepted` win the last two rows. Every active
  finding on the pair gives `violation`: the rule is wire/report's.
- **Test name**: `TestStatusFollowsEngineSeamPolicy`
  **Scenario**: table over seams with `Policy`: each of the five values alone; `allowed` with a gate/new finding on the
  pair; `observed` with an advisory/baseline finding on the pair; duplicate seams `allowed`, `violation`, `observed`
  (`Edges` 2, 3, 1); duplicate seams `observed` and `allowed`; a seam without `Policy` and a gate/baseline finding; an
  `allowed` seam `api → db` and a gate/new finding on `ui → db` with no seam; a `violation` seam with an empty endpoint.
  **Expected behavior**: the seam's value each time; the finding does not change an engine status; `violation` with
  `Count` 6 and `allowed` with `Count` 2 for the duplicates; `accepted` without `Policy`; `api → db` allowed and
  `ui → db` violation with `Count` 0; no link for the empty endpoint.
- **Test name**: `TestLinkOriginIsTheCommentsRule`
  **Scenario**: table: an introduced finding on a pair, compared; the same not compared; a pre-existing finding; a new
  finding on a dependency the base had; a new cycle over new dependencies; the old direction of a new cycle.
  **Expected behavior**: `added`; `""`; `existing`; `existing`; `""` for both directions; `existing` for the old one.
- **Test name**: `TestOriginNeverChangesTheStatus`
  **Scenario**: an added link with an active gate finding, and without.
  **Expected behavior**: both statuses equal those of the same report without a comparison.
- **Test name**: `TestCouplingComesFromTheStrongestSeam`
  **Scenario**: table: one seam; an unknown distance token; a stronger seam with fewer dependencies beside a weaker
  one; an unknown strength beside `contract`; two equal strengths; a pair only a finding names.
  **Expected behavior**: the seam's facts, distance in words; the token as written; the stronger seam's facts and the
  worse imbalance of the two; `contract`; the seam with more dependencies; no coupling.
- **Test name**: `TestNodeShowsTheVolatilityItsSeamsAgreeOn`
  **Scenario**: seams into `db` that agree; into `api` that disagree; into `ui` only `undeclared`; into `cache`, a
  module the policy does not declare.
  **Expected behavior**: `db` high; `api` and `ui` `""`; `cache` medium.
- **Test name**: `TestFindingWithoutSeamStillDraws`
  **Scenario**: gate/new on `ui → db`, gate/baseline on `api → ui`, advisory/new on `db → api` and gate/fixed on `db → ui`,
  with no seams.
  **Expected behavior**: three links with `Count` 0: `violation`, `accepted`, `advisory`; no link for the fixed finding.

### Integration Contract Tests

- **Test name**: `TestBuildMapsReportToView`
  **Scenario**: the blocked wire fixture (engine v3.0.0 state JSON decoded by `report.Decode`) with the policy file it was computed from.
  **Expected behavior**: nodes equal the policy's modules with correct `Layer` and `PolicyLine`; links equal the fixture's
  seams; exactly the pairs named by active gate findings are violations; `Stale` false.
- **Test name**: `TestStatusFromRealReports`
  **Scenario**: engine captures of the shared corpus `internal/wire/testdata/report`: `real-blocked-forbidden-and-allowed`,
  `real-baselined-comparable`, `real-waiver-active`, `real-waiver-expired` and `real-blocked-no-baseline`
  (`seams[].policy: violation`).
  **Expected behavior**: `a → b` violation and `a → c` observed; `a → b` accepted (a baselined forbidden edge is never
  allowed); accepted; violation; violation.
- **Test name**: `TestStatusFromEngineSeamPolicy`
  **Scenario**: the capture `real-seam-policy` (the engine's hexagonal `map` scenario) with its policy file.
  **Expected behavior**: the six links with the engine's statuses, `Count` 1 each: `api → billing` accepted,
  `api → stripe`, `orders → billing` and `stripe → orders` allowed, `billing → stripe` violation, `orders → audit`
  observed; `Stale` false; `Incomplete` true (required rules not evaluated).
- **Test name**: `TestBuildIsDeterministic`
  **Scenario**: build twice from the same input, and once with seams and findings shuffled.
  **Expected behavior**: three identical views, nodes sorted by name, links by `(From, To)`.
- **Test name**: `TestStaleFollowsPolicyDigest`
  **Scenario**: table: same bytes; one added space; one added comment; empty policy; empty `ConfigHash`; `ConfigHash` in
  upper case; `ConfigHash` with a `sha256:` prefix.
  **Expected behavior**: `Stale` false only for the first row.
- **Test name**: `TestIncompleteFollowsDecision`
  **Scenario**: table of `Decision`: `pass`; `pass` with an unevaluated required rule; `unmeasured` with one; `fail`;
  `PASS`; the zero decision. Then the healthy and the blocked (`hard_gates: fail`) wire fixtures.
  **Expected behavior**: `Incomplete` false only for `pass` without unevaluated rules and for the healthy fixture.

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
- **Test name**: `TestViolationIsToldApartFromObserved`
  **Scenario**: the blocked fixture: one forbidden dependency observed twice, one dependency no finding names observed once.
  **Expected behavior**: the forbidden link is `violation` with `Count` 2; the other is `observed` with `Count` 1.
- **Test name**: `TestBaselinedViolationIsNeverPermitted`
  **Scenario**: the blocked fixture with its gate finding set to `baseline`, then to `waived`.
  **Expected behavior**: the forbidden link is `accepted` both times, never `observed`.
- **Test name**: `TestSelectingANodeFindsItsYAML`
  **Scenario**: the healthy wire fixture with its policy file.
  **Expected behavior**: every node of a declared module has the line of its key; that line of the policy text contains the module's name; every link is `observed`.

<!-- archfit:comment -->
<!-- archfit:head bbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb run 7.1 -->
## Archfit: Action required

- **violation** `no_direct_b_dependency` (medium): `a` (`pkg/a/a.go`) → `b` (`pkg/b`)
  - Locations: `pkg/a/a.go:3`
  - Why: Import from pkg/a/** to pkg/b is explicitly forbidden
  - Fix: Remove the dependency or move the code

**Next:** Refresh the baseline on the protected branch.

<details>
<summary>1 advisory · never block</summary>

| Rule | Dependency | Where | Strength | Distance | Volatility | Suggested move |
| --- | --- | --- | --- | --- | --- | --- |
| Imbalanced coupling · medium | `a` (`pkg/a/a.go`) → `b` (`pkg/b`) | `pkg/a/a.go:3` | functional | across modules, same owner | undeclared | Declare the volatility of the module in the policy: undeclared volatility counts as high |

Imbalanced coupling · **Why:** balanced coupling: functional integration strength × distance across modules, same owner × undeclared volatility → medium severity (unbalanced coupling → elevated maintenance effort)

</details>

<details>
<summary>Evidence and inputs</summary>

### Decision

- Engine verdict **blocked**: hard gates fail, 1 active blocker, every required rule evaluated
- `baseline_not_comparable` (evidence): measurement profile differs. Next: Refresh the baseline on the protected branch.

### Dimensions

| Dimension | Status | Measured | Confidence | Result |
| --- | --- | --- | --- | --- |
| `intent` | measured | 1/1 (declared rules evaluated) | high | pass |
| `structure` | measured | 1/1 (discovered dependencies resolved inside the declared module map) | high | **fail** |
| `modularity` | measured | 1/2 (declared modules with a declared public surface) | low | pass |
| `coupling` | measured | 1/1 (cross-boundary edges scored) | high | **warn** |
| `change_locality` | unmeasured | 0/0 | unrated | not applicable |
| `complexity` | measured | 2/2 (declared modules with complete dependency-chain and degree values) | high | pass |
| `testability` | partial | 3/3 (classified source files) | medium | pass |
| `operations` | partial | 0/2 (declared modules with a corroborated deploy unit and qualifying owner) | medium | pass |
| `drift` | unmeasured | 0/0 | unrated | not applicable |

### Evidence gaps

- Analyzers that did not run (absent or disabled): `dependency-cruiser`, `grimp`, `cargo`, `jscpd`, `scip`, `ast-grep/syntax`, `ast-grep`, `cargo-modules`
- Dimension `change_locality`, unknown fact: eligible commit sample — the history scan returned no eligible commit (ok), so co-change cannot be distinguished from a stable tree
- Dimension `change_locality`, unknown fact: commit-to-module attribution — the history scan is incomplete, so not every eligible commit has a complete module attribution
- Dimension `complexity`, unknown fact: function length distribution — ast-grep supplied no complete function or method extent for part or all of the out-of-claim size distribution
- Dimension `complexity`, unknown fact: cognitive complexity — no cognitive-complexity analyzer is claimed; module-graph shape is the architecture-level measure
- Dimension `testability`, unknown fact: supplied coverage units — coverage is disabled, so no supplied coverage units were observed
- Dimension `testability`, unknown fact: coverage path resolution — coverage is disabled, so no supplied coverage paths were available to resolve
- Dimension `testability`, unknown fact: coverage module attribution — coverage is disabled, so no supplied coverage was available to attribute to declared modules
- Dimension `testability`, unknown fact: coverage freshness — coverage is disabled, so no supplied coverage freshness could be established
- Dimension `operations`, unknown fact: corroborated deploy unit — one or more declared modules have no independently corroborating deploy manifest
- Dimension `operations`, unknown fact: owner provenance — one or more declared modules lack a declared or CODEOWNERS-backed owner; git-author fallback is not an ownership statement
- Dimension `operations`, unknown fact: observed runtime topology — committed manifests corroborate declared deploy units; they do not observe what is actually running
- Dimension `operations`, unknown fact: supply-chain inventory — SBOM and vulnerability facts are a separate report family and have no collector in v1
- Dimension `drift`, unknown fact: admissible persisted reference — no comparable architecture-state reference is stored
- Dimension `drift`, unknown fact: complete two-sided seam identity — two-sided seam identity cannot be compared without an admissible persisted reference
- Baseline comparison non_comparable: no baseline file was loaded

### Inputs and identity

- Policy digest: `1111111111111111111111111111111111111111111111111111111111111111`
- Baseline digest: `2222222222222222222222222222222222222222222222222222222222222222` (stated by the producer)
- Labels digest: `3333333333333333333333333333333333333333333333333333333333333333` (stated by the producer)
- Measurement profile: version `archfit.measurement.v2`, settings hash `64fb93addd39e92be19fa18ba380fa211a144697946f810bda7c483bc146f73b`
- Producer identity, claimed: engine v3.0.0, image sha256:9f86d081884c7d659a2feaa0c55ad015a3bf4f1b2b0b822cd15d6c15b0f00a08, linux/amd64

Measurement is customer-attested: the producer is authenticated, the report content is not independently checked.

### Validation commands (as run in the Archfit workflow)

```sh
archfit check -c .archfit.yaml
```

</details>

<details>
<summary>For coding agents</summary>

### Agent tasks

<!-- archfit:agent-tasks -->
```json
[
{"finding_id":"ba3803eca947bf3c1b7efa8f37854d5e","rule_id":"no_direct_b_dependency","repair_kind":"code_change","goal":"Remove the forbidden dependency from pkg/a/a.go on pkg/b; move shared behavior to a location permitted by the existing dependency rules.","constraints":["Remove the dependency or move the code","public surface of module \"b\": [pkg/b/b.go]"],"files":["pkg/a/a.go","pkg/b"],"validation":["archfit check -c .archfit.yaml"],"origin":""}
]
```
<!-- /archfit:agent-tasks -->

</details>
- **Next:** Fix the rule violations, then run archfit check again.
- Reason: `blocking_findings` (architecture)

### Findings

**1 rule violation**

- **violation** `metric/cycle_count` (high): metric `cycle_count`: 0 → 2 (max_new 0)
  - Why: metric cycle_count worsened from 0 to 2 against the accepted baseline (max_new 0)
  - Fix: do not accept this by re-running archfit baseline: restore the metric, or ask the owner to review and re-baseline

<details>
<summary>Evidence and inputs</summary>

### Decision

- Engine verdict **blocked**: hard gates fail, 1 active blocker, every required rule evaluated
- Blocking findings (architecture) · code `blocking_findings`

### Dimensions

| Dimension | Status | Measured | Confidence | Result |
| --- | --- | --- | --- | --- |
| `intent` | measured | 1/1 ("declared rules evaluated") | high | pass |
| `structure` | measured | 1/1 ("discovered dependencies resolved inside the declared module map") | high | **fail** |
| `modularity` | measured | 1/2 ("declared modules with a declared public surface") | low | pass |
| `coupling` | measured | 1/1 ("cross-boundary edges scored") | high | **warn** |
| `change_locality` | unmeasured | 0/0 | unrated | not applicable |
| `complexity` | measured | 2/2 ("declared modules with complete dependency-chain and degree values") | high | pass |
| `testability` | partial | 3/3 ("classified source files") | medium | pass |
| `operations` | partial | 0/2 ("declared modules with a corroborated deploy unit and qualifying owner") | medium | pass |
| `drift` | unmeasured | 0/0 | unrated | not applicable |

### Evidence gaps

Declared rule checks: 1/1 evaluated. Implicit accepted-reference metric checks: inventory not reported. Accepted reference: comparable.

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

### Inputs and identity

- Policy digest: `1111111111111111111111111111111111111111111111111111111111111111`
- Baseline digest: `2222222222222222222222222222222222222222222222222222222222222222` (stated by the producer)
- Labels digest: `3333333333333333333333333333333333333333333333333333333333333333` (stated by the producer)
- Measurement profile: version `archfit.measurement.v2`, settings hash `64fb93addd39e92be19fa18ba380fa211a144697946f810bda7c483bc146f73b`
- Producer identity, claimed: engine v3.0.0, image sha256:9f86d081884c7d659a2feaa0c55ad015a3bf4f1b2b0b822cd15d6c15b0f00a08, linux/amd64

Reports are customer-attested. Upload authentication does not independently verify report content.

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
{"finding_id":"2c70ed07d9d73c0bbd64113251b94561","rule_id":"metric/cycle_count","repair_kind":"code_change","goal":"Restore metric cycle_count from 2 to 0: remove the dependency cycles added since the baseline.","constraints":["do not accept this by re-running \u0060archfit baseline\u0060: restore the metric, or ask the owner to review and re-baseline"],"files":[],"validation":["archfit check -c .archfit.yaml"],"origin":""}
]
```
<!-- /archfit:agent-tasks -->

</details>

<!-- archfit:receipt {"run_id":7,"run_attempt":1,"report_digest":"4444444444444444444444444444444444444444444444444444444444444444","policy_digest":"1111111111111111111111111111111111111111111111111111111111111111","baseline_digest":"2222222222222222222222222222222222222222222222222222222222222222","labels_digest":"3333333333333333333333333333333333333333333333333333333333333333","earliest_waiver_expiry":"0001-01-01T00:00:00Z","conclusion":"failure","reason":"blocking_findings"} -->
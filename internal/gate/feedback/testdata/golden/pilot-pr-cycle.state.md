This check does not block merging: the gate is advisory for this repository.

- **Next:** Fix the blocking findings, then push.
- Reason: `blocking_findings` (architecture)
- Detail: 1 blocking finding

### Findings

**4 rule violations** · 1 dependency cycle · 2 advisories · 6 accepted · 6 introduced by this pull request

#### Dependency cycle among `pkg_chaos`, `pkg_container`, `pkg_util`

**violation** · `no-module-cycles` · high

| Dependency | This PR | Where | Strength | Distance | Volatility |
| --- | --- | --- | --- | --- | --- |
| `pkg_chaos` → `pkg_container` | existing | `pkg/chaos/cmd/builder.go:14`, `pkg/chaos/command.go:10`, `pkg/chaos/iptables/cmd/loss.go:11` and 37 more | functional | across modules | high |
| `pkg_chaos` → `pkg_util` | existing | `pkg/chaos/iptables/parse.go:12`, `pkg/chaos/netem/parse.go:11` | functional | across modules | high |
| `pkg_container` → `pkg_chaos` | added | `pkg/container/appfilter.go:4` | functional | across modules | high |
| `pkg_util` → `pkg_chaos` | added | `pkg/util/flagports.go:4` | functional | across modules | high |

**Why:** Module pkg_chaos depends on pkg_container, and the two are in a dependency cycle among 3 declared modules

**Constraint:** Remove one direction of the module cycle; routing the dependency through another module keeps the cycle

<details>
<summary>2 advisories · never block</summary>

| Rule | Dependency | This PR | Where | Strength | Distance | Volatility |
| --- | --- | --- | --- | --- | --- | --- |
| Imbalanced coupling · critical | `pkg_container` (`pkg/container/appfilter.go`) → `pkg_chaos` (`pkg/chaos/cliflags`) | added | `pkg/container/appfilter.go:4` | functional | across modules | high |
| Imbalanced coupling · critical | `pkg_util` (`pkg/util/flagports.go`) → `pkg_chaos` (`pkg/chaos/cliflags`) | added | `pkg/util/flagports.go:4` | functional | across modules | high |

Imbalanced coupling · **Why:** balanced coupling: functional integration strength × cross_module distance × high volatility → critical severity (functional coupling across a module boundary to a volatile target → a change on one side forces a change on the other)

</details>

<details>
<summary>6 accepted · baseline or waiver · display only</summary>

| Rule | Dependency | This PR | Where | Strength | Distance | Volatility |
| --- | --- | --- | --- | --- | --- | --- |
| Imbalanced coupling · critical, baseline | `cmd` (`cmd/commands.go`) → `pkg_chaos` (`pkg/chaos/iptables/cmd`) | existing | `cmd/commands.go:7`, `cmd/commands.go:8`, `cmd/commands.go:9` and 3 more | functional | across modules | high |
| Imbalanced coupling · critical, baseline | `cmd` (`cmd/runtime.go`) → `pkg_runtime` (`pkg/runtime/containerd`) | existing | `cmd/runtime.go:12`, `cmd/runtime.go:13`, `cmd/runtime.go:14` | functional | across modules | high |
| Imbalanced coupling · critical, baseline | `pkg_chaos` (`pkg/chaos/iptables/iptables.go`) → `pkg_container` (`pkg/container`) | existing | `pkg/chaos/iptables/iptables.go:8`, `pkg/chaos/netem/netem.go:9`, `pkg/chaos/netem/target.go:10` and 2 more | functional | across modules | high |
| Imbalanced coupling · critical, baseline | `pkg_chaos` (`pkg/chaos/iptables/parse.go`) → `pkg_util` (`pkg/util`) | existing | `pkg/chaos/iptables/parse.go:12`, `pkg/chaos/netem/parse.go:11` | functional | across modules | high |
| Imbalanced coupling · critical, baseline | `pkg_runtime` (`pkg/runtime/containerd/addresses.go`) → `pkg_container` (`pkg/container`) | existing | `pkg/runtime/containerd/addresses.go:15`, `pkg/runtime/containerd/client.go:12`, `pkg/runtime/containerd/iptables.go:7` and 12 more | functional | across modules | high |
| Imbalanced coupling · critical, baseline | `pkg_runtime` (`pkg/runtime/containerd/netem.go`) → `pkg_tc` (`pkg/tc`) | existing | `pkg/runtime/containerd/netem.go:7`, `pkg/runtime/docker/netem.go:7` | functional | across modules | high |

Imbalanced coupling · **Why:** balanced coupling: functional integration strength × cross_module distance × high volatility → critical severity (functional coupling across a module boundary to a volatile target → a change on one side forces a change on the other)

</details>

<details>
<summary>Evidence, inputs and validation</summary>

### Dimensions

| Dimension | Status | Coverage | Confidence | Gate |
| --- | --- | --- | --- | --- |
| `intent` | measured | 1/1 (declared rules evaluated) | high | pass |
| `structure` | measured | 167/628 (discovered dependencies resolved inside the declared module map) | high | **fail** |
| `modularity` | measured | 0/6 (declared modules with a declared public surface) | low | pass |
| `coupling` | measured | 79/79 (cross-boundary edges scored) | high | **warn** |
| `change_locality` | measured | 6/6 (declared modules touched in the scanned history window) | high | pass |
| `complexity` | measured | 6/6 (declared modules with complete dependency-chain and degree values) | high | pass |
| `testability` | partial | 158/173 (classified source files) | medium | pass |
| `operations` | partial | 1/6 (declared modules with a corroborated deploy unit and qualifying owner) | medium | pass |
| `drift` | measured | 8/8 (distributed-monolith seams compared against the stored reference) | high | pass |

### Evidence gaps

- Analyzers that did not run (absent or disabled): `dependency-cruiser`, `grimp`, `cargo`, `jscpd`, `scip`, `ast-grep/syntax`, `ast-grep`, `cargo-modules`
- Dimension `structure`, unknown fact: external dependency structure — 461 dependencies leave the declared module map (461 on libraries, 0 to or from first-party code no module owns), so their direction and layer are outside this claim
- Dimension `modularity`, unknown fact: inferred public surface — no declared module states a public surface, so inferring one is outside this claim
- Dimension `change_locality`, unknown fact: essential vs accidental volatility — commit frequency corroborates a declared volatility; it cannot establish one
- Dimension `complexity`, unknown fact: function length distribution — ast-grep supplied no complete function or method extent for part or all of the out-of-claim size distribution
- Dimension `complexity`, unknown fact: cognitive complexity — no cognitive-complexity analyzer is claimed; module-graph shape is the architecture-level measure
- Dimension `testability`, unknown fact: supplied coverage units — coverage is disabled, so no supplied coverage units were observed
- Dimension `testability`, unknown fact: coverage path resolution — coverage is disabled, so no supplied coverage paths were available to resolve
- Dimension `testability`, unknown fact: coverage module attribution — coverage is disabled, so no supplied coverage was available to attribute to declared modules
- Dimension `testability`, unknown fact: coverage freshness — coverage is disabled, so no supplied coverage freshness could be established
- Dimension `operations`, unknown fact: corroborated deploy unit — one or more declared modules have no independently corroborating deploy manifest
- Dimension `operations`, unknown fact: observed runtime topology — committed manifests corroborate declared deploy units; they do not observe what is actually running
- Dimension `operations`, unknown fact: supply-chain inventory — SBOM and vulnerability facts are a separate report family and have no collector in v1

### Inputs and identity

- Policy digest: `1111111111111111111111111111111111111111111111111111111111111111`
- Baseline digest: `2222222222222222222222222222222222222222222222222222222222222222` (stated by the producer)
- Labels digest: `3333333333333333333333333333333333333333333333333333333333333333` (stated by the producer)
- Measurement profile: version `archfit.measurement.v2`, settings hash `eb46ade8e8c37ee1f509da1f83bddfac13e4d162602d9065ccc964e165810ef6`
- Producer identity, claimed: engine v3.0.0, image sha256:9f86d081884c7d659a2feaa0c55ad015a3bf4f1b2b0b822cd15d6c15b0f00a08, linux/amd64

Measurement is customer-attested: the producer is authenticated, the report content is not independently checked.

</details>

<!-- archfit:receipt {"run_id":7,"run_attempt":1,"report_digest":"4444444444444444444444444444444444444444444444444444444444444444","policy_digest":"1111111111111111111111111111111111111111111111111111111111111111","baseline_digest":"2222222222222222222222222222222222222222222222222222222222222222","labels_digest":"3333333333333333333333333333333333333333333333333333333333333333","earliest_waiver_expiry":"0001-01-01T00:00:00Z","conclusion":"failure","reason":"blocking_findings"} -->
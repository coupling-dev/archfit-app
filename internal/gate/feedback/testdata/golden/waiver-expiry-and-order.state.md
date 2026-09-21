## Archfit: Blocked by architecture findings

- Reason: `blocking_findings` (architecture)
- Detail: 1 blocking finding
- Next: Fix the blocking findings, then push.

Also reported:
- `waiver_expired` (governance): no_direct_b_dependency. Next: Renew the waiver or fix the finding.

### Findings

**Introduced** (2)
- **blocking** `no_direct_b_dependency` (medium, expired_waiver): `a` (`pkg/a/a.go`) → `b` (`pkg/b`)
  - Locations: `pkg/a/extra.go:7`
  - Why: Import from pkg/a/** to pkg/b is explicitly forbidden
  - Constraint: Remove the dependency or move the code
- `bc/imbalanced_coupling` (medium, new): `a` (`pkg/a/a.go`) → `b` (`pkg/b`)
  - Locations: `pkg/a/a.go:3`
  - Why: balanced coupling: functional integration strength × cross_module_same_owner distance × undeclared volatility → medium severity (unbalanced coupling → elevated maintenance effort)

**Accepted debt — display only** (1)
- `no_direct_b_dependency` (medium, waived): `a` (`pkg/a/a.go`) → `b` (`pkg/b`)
  - Locations: `pkg/a/a.go:3`
  - Why: Import from pkg/a/** to pkg/b is explicitly forbidden
  - Constraint: Remove the dependency or move the code

### Dimensions

Measurement trust: customer-attested.

- `intent`: measured; coverage 1/1 (declared rules evaluated); confidence high; gate pass
- `structure`: measured; coverage 1/1 (discovered dependencies resolved inside the declared module map); confidence high; gate fail
- `modularity`: measured; coverage 1/2 (declared modules with a declared public surface); confidence low; gate pass
- `coupling`: measured; coverage 1/1 (cross-boundary edges scored); confidence high; gate warn
- `change_locality`: unmeasured; coverage 0/0; confidence unrated; gate not_applicable
- `complexity`: measured; coverage 2/2 (declared modules with complete dependency-chain and degree values); confidence high; gate pass
- `testability`: partial; coverage 3/3 (classified source files); confidence medium; gate pass
- `operations`: partial; coverage 0/2 (declared modules with a corroborated deploy unit and qualifying owner); confidence medium; gate pass
- `drift`: unmeasured; coverage 0/0; confidence unrated; gate not_applicable; change non_comparable, 0 new and 0 resolved findings

### Evidence gaps

- Tool `dependency-cruiser`: absent
- Tool `grimp`: absent
- Tool `cargo`: absent
- Tool `jscpd`: disabled (clone detection disabled by config — set analyzers.clones.enabled: true in .archfit.yaml to enable)
- Tool `scip`: disabled (opt-in: analyzers.scip.enabled)
- Tool `ast-grep/syntax`: disabled (opt-in: analyzers.syntax.enabled)
- Tool `ast-grep`: disabled
- Tool `cargo-modules`: absent
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
- Producer `ast-grep`: disabled
- Producer `ast-grep/syntax`: disabled
- Producer `cargo`: absent
- Producer `cargo-modules`: absent
- Producer `dependency-cruiser`: absent
- Producer `grimp`: absent
- Producer `jscpd`: disabled
- Producer `scip`: disabled
- Baseline comparison non_comparable: no baseline file was loaded

### Inputs and identity

- Policy digest: `1111111111111111111111111111111111111111111111111111111111111111`
- Baseline digest: `2222222222222222222222222222222222222222222222222222222222222222` (stated by the producer)
- Labels digest: `3333333333333333333333333333333333333333333333333333333333333333` (stated by the producer)
- Measurement profile: version `archfit.measurement.v1`, settings hash `64fb93addd39e92be19fa18ba380fa211a144697946f810bda7c483bc146f73b`
- Producer identity, claimed: engine v2.3.1, image sha256:9f86d081884c7d659a2feaa0c55ad015a3bf4f1b2b0b822cd15d6c15b0f00a08, linux/amd64

Measurement is customer-attested: the producer is authenticated, the report content is not independently checked.

<!-- archfit:receipt {"run_id":7,"run_attempt":1,"report_digest":"4444444444444444444444444444444444444444444444444444444444444444","policy_digest":"1111111111111111111111111111111111111111111111111111111111111111","baseline_digest":"2222222222222222222222222222222222222222222222222222222222222222","labels_digest":"3333333333333333333333333333333333333333333333333333333333333333","earliest_waiver_expiry":"2026-10-01T00:00:00Z","conclusion":"failure","reason":"blocking_findings"} -->
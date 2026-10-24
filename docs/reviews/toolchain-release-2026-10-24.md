# Coordinated toolchain release: execution record

Checkpoint: 2026-10-24, 17:42 UTC. Engine candidate PR open. No new release or deployment.

## Authority and isolation

The owner authorized `alexei-led/archfit v3.2.0` and `alexei-led/archfit-action v2.2.0` after tests, native image gates, and review.
Publish the engine only through its annotated-tag workflow. Never move an existing tag.
The App deployment requires confirmation of its exact validated image and Cloud Run destination.
Baseline migration requires review of the actual changes. No automatic reset or debt acceptance is authorized.

All source work uses isolated branches and worktrees. Main checkouts remain unchanged.
The CCGram live checkout, bridge, and protected-model PR342 belong to another session.
Do not restart, reset, or update them as part of this release.

## Current source and review

| Repository | Branch | Commit | State |
| --- | --- | --- | --- |
| Engine | `upgrade/analysis-integration` | `f804c1ecbd5a48b98ec5dfcaa8be8f5ca78f2c92` | Draft [PR86](https://github.com/alexei-led/archfit/pull/86) |
| Action | `upgrade/engine-3-2` | `fbd9a9550bef72a3ff84840ffc4ce459d47ee501` | Local guard commit, independently reviewed |
| App | `upgrade/engine-3-2` | `ae7b978832b1e9b88054e117b1d970acb5e0d487` | Local deployment-validation and rollback guard |

Engine changes provide owned dependency-cruiser/Node execution, isolated Python readers, JavaScript syntax facts, Cargo-backed SCIP attribution, and a locked complete image.
Ambiguous Rust evidence stays partial. Missing compiler or source evidence does not become an empty measured graph.
TypeScript 6.0.3 is deliberate. The tested TypeScript 7 candidate produced an unsupported empty graph.
Bundled Rust 1.99.1 tools retain compatible SCIP namespaces. The newer standalone rust-analyzer candidate did not.

The last boundary follow-up is committed as `e2da1efdd022545ce37c1a6e08128757023248ff`.
It resolves raw symlink/parent paths before normalization. Shared-helper and both Python-boundary regressions reproduce the original defect.
Independent bounded re-review closed that finding. Earlier Python, syntax, Node, Rust, and SCIP reviews are also complete.

Action commit `fbd9a955` changes only the no-endpoint report gate and its tests/docs.
Required evidence gaps, failed gates, and malformed decision evidence fail locally. Genuine optional attention still succeeds.
Endpoint uploads and discovery/baseline/reanchor behavior remain unchanged. The envelope exists before local failure.
No engine, image digest, schema, workflow, or App approval pin changed in this commit.
Independent review found no defects in the bounded change.

## Validation evidence

- Final engine Go race suite passed after the raw-path correction.
- Normal pre-push secret scan, full lint, full tests, build, and architecture hook passed. No hook was bypassed.
- Documentation checks passed: 29 Markdown files and two Mermaid diagrams. Release notes passed the content checker.
- The Node production lock audit reported zero known vulnerabilities at this checkpoint.
- Action guard regressions passed 97 assertions. Full legacy Action conformance passed 479 assertions.
  Worker results include shellcheck, shfmt, fixture byte hashes, secret scan, and a clean signed commit.
- App deployment guard passed 34 Bats tests plus actionlint, shellcheck, shfmt, and zizmor.

The final-source architecture check returned exit 2, `needs_attention`, and `next_action: ask_owner`.
It reported zero repairs and zero evidence gaps. One required rule, `metric_ratchets`, covers four unevaluated ratchets.
The stored profile uses older producer semantics. The normal architecture hook accepts exit 2, but that is not a healthy architecture claim.
No protected policy, baseline, waiver, or labels changed.

Native arm64 checkpoint `411c3eae2114a2fc932d6280760ecd7c97ccecce` passed all 133 image assertions.
Its local image was `sha256:eca83aa31c8303011f5b42311b5a054f6fadde72d33120b2e401ddef4389ad81`.
This predates the final containment corrections and is not final release acceptance.

Final candidate [CI run40117052514](https://github.com/alexei-led/archfit/actions/runs/40117052514) is running.
Locked-tool, lint, and workflow checks passed. Source tests and native amd64/arm64 images remain pending.
PR documentation checks passed separately. The existing connected App check is `action_required`, not acceptance.

Offline CCGram candidate evidence used unmodified commit `006a88885d0876f132f40cb3525c20d2986a37ac`.
Grimp 3.17/Python 3.14.9 parsed 229/229 package files with zero unresolved imports.
All six required import rules were evaluated with zero forbidden matches.
This does not prove published-image behavior, a current connected run, or complete optional semantic/cycle evidence.

## App adoption inventory

Candidate state-schema bytes match the current App pin, SHA-256 `8b5e67ff291570cd8be72b31effd66cf480dc5071747a68ac274e4d80508198c`.
This is source evidence, not release attestation. Do not invent a schema or shadow DTO change.
Keep config v2, state/envelope v1, and `archfit.measurement.v2` unless actual released bytes require otherwise.
A stable profile identifier does not make changed tool/producer measurements comparable.

After release verification, update the strict reader to v3.2.0 only, genuine captures, and schema provenance.
Replace both manifest platform rows with verified release digests and one released Action SHA.
Exactly one amd64 row is Current. Do not retain old approvals or add dual-version fallback.
Generated onboarding workflows must use that exact tuple. Keep the App image analyzer-free.

## Remaining release sequence

1. Inspect final source/native CI results and image assertion artifacts. Fix actual failures and re-review changed scope.
2. Merge the reviewed engine candidate and publish v3.2.0 through its annotated-tag workflow.
3. Verify actual release binaries, checksums, tool inventory, and both published platform manifests.
   Do not confuse an OCI index digest or local image configuration ID with a platform manifest digest.
4. Adopt the released engine in the single-engine Action. Use fresh scans and genuine release fixtures/provenance.
   Run full conformance and actual pinned-image mode tests, then publish reviewed v2.2.0.
5. Complete the App cutover described above. Run `make all` and exact-source CI before pushing/building.
6. Show the validated App image and destination to the owner. Deploy only after that confirmation.
7. Review workflow-pin PRs for the connected Go/Python repositories. Preserve their settings, owners, and gate mode.
8. Review any required baseline reanchor separately, then validate actual App checks/uploads on exact adopted commits.

The historical App deployment used source `f0213db`, run39784863202, and own service image
`sha256:c810dbd500505a67700ad90d9e392ad4e96d7e338d00b0e7aec82253dc1913e7`.
This is not a fresh traffic inventory. The destination is project `coupling-dev-510710`, region `europe-west1`.
Local cloud credentials cannot read the service. Use existing GitHub OIDC, not new IAM grants or another default project.
Rollback must restore a coherent App approval/workflow/protected-input combination, not only App traffic.

## Evidence retention and trust

New disposable logs and environments belong under `$HOME/.cache/archfit-release/2026-10-24`.
Source logs include `final-integration-race.log`, `final-source-architecture.json`, and `engine-compare-main.json`.
Action evidence is under `action-evidence-guard/`. Full reports also remain in session-managed subagent artifacts.
Older lab artifacts remain referenced. Propose consolidation only after confirming no active consumer.
Recreate environments from locks instead of relocating virtualenvs with absolute interpreter paths.

Containers are secret-free, not network-isolated. Target build scripts and indexers can execute code and exfiltrate mounted source.
Reports remain customer-attested. Passing workflows or envelopes do not prove that target code never executed.

# Coordinated toolchain release: execution record

Checkpoint: engine v3.2.0 publication started, 2026-10-25. PR86 merged. No App deployment.

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
| Engine | `upgrade/analysis-integration` | `451a52a4f56357c5e061261980dd7e21f97f7531` | Draft [PR86](https://github.com/alexei-led/archfit/pull/86) |
| Action | `upgrade/engine-3-2` | `fbd9a9550bef72a3ff84840ffc4ce459d47ee501` | Local guard commit, independently reviewed |
| App | `upgrade/engine-3-2` | `ae7b978832b1e9b88054e117b1d970acb5e0d487` | Local deployment-validation and rollback guard |

Engine changes provide owned dependency-cruiser/Node execution, isolated Python readers, JavaScript syntax facts, Cargo-backed SCIP attribution, and a locked complete image.
Ambiguous Rust evidence stays partial. Missing compiler or source evidence does not become an empty measured graph.
TypeScript 6.0.3 is deliberate. The tested TypeScript 7 candidate produced an unsupported empty graph.
Bundled Rust 1.99.1 tools retain compatible SCIP namespaces. The newer standalone rust-analyzer candidate did not.

The last boundary follow-up is committed as `e2da1efdd022545ce37c1a6e08128757023248ff`.
It resolves raw symlink/parent paths before normalization. Shared-helper and both Python-boundary regressions reproduce the original defect.
Independent bounded re-review closed that finding. Earlier Python, syntax, Node, Rust, and SCIP reviews are also complete.

CI-bootstrap correction `451a52a4` selects the manifest Python explicitly before constructing the analyzer environment.
The runner's system Python is used only to read the manifest. The selected Python bin directory is available to later offline tests.
The patch also corrects govulncheck's ignored Go-version input and disables its duplicate nested cache restore.
Independent review found no defects in this three-file change. Production, Docker, and verdict code are unchanged.

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
- CI-bootstrap regressions failed before the fix and passed afterward, including install/find failures before privileged provisioning.
  All 11 provisioning/harness unit tests passed. ShellCheck, shfmt, Ruff, actionlint, and zizmor passed for the changed scope.
- Action guard regressions passed 97 assertions. Full legacy Action conformance passed 479 assertions.
  Worker results include shellcheck, shfmt, fixture byte hashes, secret scan, and a clean signed commit.
- App deployment guard passed 34 Bats tests plus actionlint, shellcheck, shfmt, and zizmor.

The final-source architecture check returned exit 2, `needs_attention`, and `next_action: ask_owner`.
It reported zero repairs and zero evidence gaps. One required rule, `metric_ratchets`, covers four unevaluated ratchets.
The stored profile uses older producer semantics. The normal architecture hook accepts exit 2, but that is not a healthy architecture claim.
No protected policy, baseline, waiver, or labels changed.

Candidate `f804c1ec` passed 133/133 assertions on both native amd64 and arm64 in
[CI run40117052514](https://github.com/alexei-led/archfit/actions/runs/40117052514).
Downloaded assertion artifacts contained no failed or waived assertions. Image inspection confirmed both native platforms and user `65532:65532`.
These CI images use version `ci-image-contract`, not a released identity or approved platform digest.

That run failed source tests. The bootstrap selected runner Python 3.12.3 despite setup-uv's 3.14.9 selector.
Python/grimp and Rust SCIP reader failures reported the same interpreter rejection. The correction is described above.
The exact-head [CI run40118616937](https://github.com/alexei-led/archfit/actions/runs/40118616937) passed.
Source tests, lint, documentation, and all four platform binary builds passed. Both native images passed 133/133 assertions.
Provisioning logs confirm Python 3.14.9. The ignored govulncheck input warning is gone.

The existing connected App check is `action_required`, not acceptance. Its v3.0.1 image runs Go 1.26.9 against a Go 1.27 repository.
A baseline approval cannot clear that missing required evidence.

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

## Owner decision and publisher checkpoint

The owner explicitly approved engine publication while preserving the baseline and old App holds.
This does not approve a healthy architecture claim, new debt, baseline reset, or App deployment.

PR86 merged as `faa4eb4e513859567fd6c380230c91ea22fdaab6`.
Its Git tree `201033b3d079e78175a22a14cce9d0f7587c4ae6` exactly matches the tested `451a52a4` candidate.
The signed annotated v3.2.0 tag points to that merge commit. Its tag object is `0c673aa3e19e83f0dee7881d2df1faba1ff6680b`.
The tag annotation comes from the committed release-notes section. Signature verification passed.
[Release run40172977752](https://github.com/alexei-led/archfit/actions/runs/40172977752) is running.
The tag is published. Final release assets and platform digests are not yet verified or approved.

Fix the measurement environment first, then inspect fresh results and review the baseline migration.
A baseline approval cannot repair missing compiler evidence. Changed measurements do not by themselves prove new code regressions.

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

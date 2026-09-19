# Trust, measurement, and reporting

Status: retained technical requirements from the 16 September 2026 proposal,
aligned with the subsequent [product design](product.md). These describe intended
behavior, not implemented capabilities. The reporter still refuses ingestion.

This document owns the trust constraints and reporting contract. The product
design owns first-version scope; the [closed-beta plan](../plans/closed-beta.md)
owns task order, dependencies, and acceptance evidence. Engine observations below
come from the dated inspection recorded in that plan and must be rechecked before
implementation.

## 1. Execution and trusted inputs

Routine analysis runs in the customer's CI. Extractors need the repository's
toolchain and private-dependency access; this is a locality decision, not a claim
that customer execution removes sandboxing risks. The Action carries results to
the App, which owns approved inputs, baselines, and feedback.

Gate inputs come only from a trusted source, and the required check fails closed:

- Resolve policy from a protected ref, issue an exact-byte snapshot and digest,
  and reject a report whose `comparison.config_hash` does not match.
- Bind labels, baseline, and approved measurement identity independently.
- Never trust arbitrary head-tree configuration, labels, baselines, or ownership
  declarations. An owner-approved candidate policy is an exception only through
  the App's exact-head-SHA and policy-snapshot approval process.
- Resolve authority from protected configuration, not from a run's own assertions.

This prevents a contributor from disabling a gate or rewriting accepted debt.
It does not make all comparisons possible. The engine's resolved module map
includes ownership and detected deploy units: legitimate topology changes can
alter `model_hash` and make a baseline non-comparable. Review and explicit
migration handle that case; weakening comparison or silently refreshing the
baseline does not.

## 2. Provenance is necessary, not sufficient

OIDC authenticates producer claims. Eligibility still requires verification of
the approved workflow definition, run identity, analyzed revision, and artifacts.
A head-authored workflow cannot satisfy enforcement merely by obtaining a token.

A trusted workflow can run a PR install script that forges a clean report with
the expected digests. The legitimate uploader can then authenticate those bytes.
Fail-closed catches missing evidence, not complete fabricated evidence. The
initial required gate therefore discloses **customer-attested measurement**;
it does not certify protection against adversarial build scripts.

Stronger measurement needs runtime separation: target code cannot write the
analyzer, issued inputs, report path, or authentication environment. Trusted
extraction must independently derive the facts it certifies. Build-generated
files and supplied coverage remain untrusted inputs even when an uploader signs
their digest. A pinned container image freezes the starting artifact, not the
writable runtime, mounts, permissions, subprocesses, or output boundary.

### Bind the analyzed revision, not just the uploader

The envelope must bind repository, analyzed tree, PR, head/base/merge-base,
workflow revision, run attempt, measurement profile, image digest, and report
digest. In particular, `workflow_run` executes a default-branch workflow; its
own `GITHUB_SHA` is not automatically the analyzed PR head. Resolve and check
those identities authoritatively. Ambiguous PR associations are not eligible.

### Record trust per fact or rule

Keep two independent axes:

- **Measurement trust:** could target-authored code influence this fact?
- **Evidence completeness:** were the required facts observed?

Mirror the engine's per-dimension status, confidence, and coverage. A high metric
value does not prove completeness. Source-only analysis still has parser risks;
rich, properly isolated measurement is not inherently less trustworthy. State
what each enforced rule relies on. Never print a report-wide “verified” badge.

### Derive eligibility from separate facts

| Fact | Constraint |
| --- | --- |
| Approving authority | Authenticated authority from trusted policy |
| Approved producer/workflow | Scoped to installation and repository; protected definition |
| Runner environment | GitHub-hosted or approved self-hosted group; a label alone proves nothing |
| Source origin | Base-owned or fork-authored, independent of producer and runner |
| Measurement profile | Approved and compatible with the baseline |

An organization may approve its own self-hosted runners. This does not upgrade
customer-attested measurements to independently measured facts.

Fork artifacts alone cannot satisfy the required gate. A supported path needs a
base-owned dispatcher and fresh measurement through an approved workflow on
isolated compute, with reporting credentials separate from target execution.
Never run fork code in a privileged reporter or treat permission to run a fork
workflow as proof of its output. Persistent self-hosted fork execution defaults
to unsupported. Without an eligible path, show advisory evidence and block the
required gate: obtain approved measurement or use the explicit GitHub bypass.

## 3. Measurement identity and images

Two identities are needed:

| Layer | Identity | Purpose |
| --- | --- | --- |
| Engine | Versioned measurement-profile fingerprint: engine, applicable extractors/tools, rule bundle, relevant target settings | Comparison and persisted-baseline compatibility, including CLI use |
| App | Approved image digest and platform, checked against a locked release manifest | Deployment identity without treating unrelated image rebuilds as new measurements |

`measurement.tool_versions` is an input, not a complete profile definition.
Missing identities must not compare equal because both say “unknown.” The dated
engine inspection found tool-version disclosure but incomplete profile comparison
and persistence. [T01/T02](../plans/closed-beta.md#m0--resolve-contracts-and-execution-choices)
cover the remaining work; this document does not prescribe a release number.
Unknown-profile behavior, old-baseline migration, and cross-platform equivalence
need explicit decisions and tests.

On a profile change, replay the same approved source under old and new profiles,
show the measurement-induced difference, and require baseline-migration approval.
Do not silently accept newly exposed violations or discard accepted debt.

### Packaging constraints

Use one image per whole-repository run, built from one locked tool manifest.
Proposed variants are base, Go, TypeScript, Python, Rust, and polyglot; they are
packaging choices, not promises that all variants are published. Shared
extractors use the same versions. Keep cross-variant comparability conservative
until equivalence is demonstrated.

- Use a glibc-compatible base and a non-root default; verify ast-grep resolves
  as the intended executable rather than util-linux `sg`.
- Pin Go toolchain behavior (`GOTOOLCHAIN=local` for the locked profile).
- Bake and lock Python/grimp instead of resolving analyzer code at runtime.
- Include the required Rust tools before claiming Rust coverage.
- Document remaining dependency-download/egress needs rather than claiming an
  image eliminates them. Cache identity includes tool version and input tree.

Mixed repositories use a whole-repository profile, not merged per-language
reports. A sample workflow that co-locates target execution and OIDC credentials
is not a safe implementation recipe; the eligible workflow is specified and
tested in T03/T10.

## 4. One repository, one gate

`--root` is the evidence boundary. Seams and clone analysis need repository-wide
facts; separate subtree reports miss cross-subtree relationships. Concatenating
reports does not reconstruct those facts.

Use one logical repository-wide assessment, one approved baseline version, and
one authoritative gate. Route findings by owner inside the check. A filtered view
narrows presentation, not evidence. A scoped run can make a named scoped claim,
never substitute for the repository-wide gate.

Future parallel extraction may assemble compatible facts into one complete graph
before global classification, seam, and clone analysis. Any future partitioned
gate needs an architecture-owner-approved partition contract: source coverage,
cross-partition resolution, and a mandatory global assessment on the same
revision/profile. Missing analyzers or unresolved targets mean independence is
unestablished, not zero edges. Import graphs alone cannot establish clone,
external-system, or runtime independence. Check each claimed invariant on each
revision; a historical clean scan or team declaration is insufficient.

## 5. Checks and PR feedback

| Check | Required | Purpose |
| --- | --- | --- |
| `archfit/gate` | When explicitly enabled | Approved-policy findings, input integrity, evidence eligibility; always conclude, never `neutral` or `skipped` |
| `archfit/state` | Never | Dimension status, coverage, seams, drift, deltas, and `agent_tasks[]` |

`neutral` and `skipped` do not block a GitHub required check. Missing uploads
must not become the cheapest bypass.

### Gate outcomes

These mappings apply only after checking provenance, issued inputs, and evidence
eligibility; an unverified producer's verdict or exit code is not authoritative.

| Eligible run outcome | `archfit/gate` conclusion |
| --- | --- |
| Healthy (0) | `success` |
| Blocked (1) | `failure`, with blocking findings and remediation tasks |
| Non-comparable (2) | `action_required`, naming drift and required review/migration |
| Required coverage missing (2) | `action_required`, naming the missing facts |
| Error (3), absent report, or ineligible evidence | `action_required` or explicit failure; never passing/neutral |
| Independent-observation contradiction, if that future feature exists | `failure`, distinguished from architecture violations |

Policy/model changes need the relevant trusted authority's approval bound to the
exact head SHA and snapshot, plus explicit baseline handling. Re-push invalidates
approval. Missing coverage is cleared by supplying the fact, not approving around
it. An integrity failure cannot be cleared by approval.

**Erosion-only adoption is the baseline's job.** Never filter enforcement to
`agent_tasks[].origin == "introduced"`: the synthetic `bc/coupling_gate` task has
origin `unknown`. Origin sorts presentation; it never exempts a blocking finding.

### One updated comment per PR

Put verdict, reason, and next action first. Include:

- Rule, modules, source locations, approved access path, and validation command.
- Introduced findings separately from already accepted debt.
- Meaningful dimension/coupling changes and a mandatory evidence-gap section.
- Marker-delimited structured remediation data for coding agents, subject to the
  allowed-result/privacy contract; do not embed arbitrary source-bearing output.
- Policy ref/digest, measurement profile, image digest, baseline reference, and
  per-fact measurement trust/completeness. Never invent independent evidence.

Use available SARIF locations for annotations without requiring Advanced Security.
Retry or duplicate delivery updates the existing comment, and stale runs must not
overwrite feedback for a newer head. Ordinary feedback needs no LLM.

## 6. Governance and baseline migration

Architecture-owner approval is separate authority, not a mandatory separate PR.
Code and policy may land atomically with approval bound to the exact head and
policy snapshot. Resolve owner authority from protected policy. Head-edited
CODEOWNERS cannot grant approval rights. Strict, narrow, expiring exceptions
follow the [product governance rules](product.md#3-architecture-policy-and-authority)
and T14–T16, not an automatic accepted-debt refresh.

A policy diff does not prove a refactor is mechanical. A verified namespace
relocation would need to preserve each file's logical module, public/internal
classification, layer, ownership, deploy semantics, and applicable rules. Every
path-sensitive selector must undergo the same bounded substitution without
broadening permission for future paths. Equal file sets or counts prove neither.

`archfit config compare` compares two policies over one tree. After a move, the
old policy can be stale for that tree; its output is disclosure, not proof of
unchanged permission. Until a relocation proof exists, uncertain changes require
architecture-owner review. Module-owner fast paths are not assumed implemented.
Accepted debt survives only through explicit migration.

## 7. Service and wire-contract boundaries

The product's three repositories separate a tenant-bearing service from a
customer-executed analyzer, not merely code that would be inconvenient to place
in one repository:

- `archfit`: engine, CLI, images, and state/config wire contracts.
- `archfit-action`: execution/upload adapter and report-envelope contract.
- `archfit-app`: tenancy, approved inputs, baselines, governance, and feedback.

Do not publish engine-internal Go DTOs to couple the App to implementation types.
Generate transport types from versioned schemas; keep domain and storage types
separate. The state schema exists in the dated inspection; remaining integration
work is immutable artifacts, envelope schema, semantic conformance fixtures, and
a supported-version matrix. Test candidate engine artifacts against consumer
tests before promotion. Shape validation alone cannot detect semantic drift, and
installed Actions, images, and stored baselines never upgrade atomically.

The reporter is a small independent deployment. It must remain operational when
billing providers, UI, or LLM services fail. The control plane owns issuance,
approvals, entitlements, and background work. Tenant identity comes from
authenticated installation/repository context, not client-supplied IDs. Enforce
scope in database access, objects, caches, and workers; include tenant-bound report
encryption and key lifecycle. Store size-capped reports/baselines, never source
archives. The [hosting research](../research/20260918-hosting.md) describes the
selected validation target, not a deployed topology or SLA.

Fail-closed makes the reporter part of the merge path. Pre-issued tokens or
signed permits do not remove that dependency; a label-based bypass also needs the
unavailable App. Emergency recovery is a narrowly authorized GitHub ruleset
bypass, with Archfit in its own ruleset so other protections remain. Record an
override, never fabricated success. Cached entitlement/grace behavior prevents a
billing outage from being interpreted as non-payment.

## 8. Deferred designs and unresolved work

These constraints survive the original proposal but do not expand closed-beta
scope:

- **Independent measurement:** directly enforce named facts observed without
  target builds, not report-wide verification. Cross-check only a declared common
  subset with identical source SHA, policy, classification, module mapping, and
  import semantics. Compare normalized identities and source witnesses, never
  aggregate counts. A contradiction is an independent-observation discrepancy,
  not proof of tampering; report unchecked facts explicitly. Agreement promotes
  nothing outside the subset. Source-only and compiler-resolved graphs are not
  interchangeable. This requires separate design/cost review and cannot apply
  to a mode that forbids source transmission to Archfit.
- **Organization floor:** if added later, enforce obligations independently of
  repository additions; exclusions, remapping, labels, baselines, and exceptions
  must not suppress them. An unrestricted YAML merge is insufficient. A rendered
  single policy can preserve the engine's strict parsing and raw-byte hash.
- **Agent guidance:** generated constraint sections proposed by PR and an MCP
  boundary-query channel remain follow-up ideas, not required beta deliverables.
- **Other deployment/forge models:** customer-operated reporting, enterprise
  features, and GitLab/Bitbucket need explicit future scope. No pilot multi-region,
  latency, or availability promise is inherited from the old proposal.

Active unknowns—profile definition, fork acquisition, runtime separation,
privacy-safe payloads, and approval/invalidation races—are acceptance gates in
the closed-beta plan, not assumptions this cleanup resolves.

# Archfit App — closed-beta implementation plan

Status: execution plan refreshed against Archfit v2.3.1 on 20 September 2026.
The engine prerequisite has shipped; App/Action adoption and the milestone
acceptance checks below remain open. This documentation update completes none
of those integration tasks.

Inputs: the [product design](../design/product.md) and
[trust and reporting requirements](../design/trust-and-reporting.md).
The owner selected a closed beta: Pumba and Archfit first, then a few invited
teams, before public paid self-service.

The plan orders work into reviewable changes with dependencies and exit checks.
Read-only research can start now; live experiments and platform-dependent
implementation wait for their stated prerequisites. Approval of a milestone
carries through its mechanical steps and validation. Do not cross the M0–M5
milestone boundaries below without the owner's approval. This plan does not
authorize deployment, repository installation, billing changes, or invitations
to other people.

## Starting point: dated local evidence

Inspected on 20 September 2026; the published
[v2.3.1 release](https://github.com/alexei-led/archfit/releases/tag/v2.3.1)
and its downloaded binaries were verified during engine release acceptance.
This is a pinned planning snapshot, not a live status report for sibling repos.
Recheck it before starting the affected tasks. T05 has partial research evidence;
no App/Action integration task is marked complete.

| Repository | Pinned revision | Relevant state |
| --- | --- | --- |
| `archfit-app` | `1356a51446d98dcfaa5b1d68c8ad72316beee953` | Go reporter skeleton; valid-shaped reports deliberately receive 501 |
| `archfit` | `f8877d25ba36d84d3780071580d23486e3d794d0` (`v2.3.1`) | Measurement profiles, required-rule evidence states, waiver lifecycle and typed repair tasks are shipped |
| `archfit-action` | `4759dd985bff385af5c2dee2c918a0eced59f4f3` | Builds an envelope around an existing report; obtains OIDC and optionally uploads it; does not run analysis |

Material findings that affect task order:

- Engine state/config schemas and behavioral tests exist. The published binary
  passed 141 real-tool assertions across Go, TypeScript, JavaScript, Python and
  Rust. The release candidate passed contract validation on all 11 canonical
  corpus repositories, with optional evidence gaps still disclosed. These are
  engine receipts, not App conformance or proof of all four product scenarios.
- `comparison.measurement_profile` is emitted, persisted and compared using
  producer semantics, settings, status and tool identity. Unknown or incompatible
  profiles abstain with named reasons. The persisted baseline's `gate_reference`
  is separate from report-only `--base` comparison. T01 now verifies adoption;
  T02 establishes the App/Action consumer matrix.
- v2.3.1 advances deployment, Git-history and Python producer semantics to v2.
  v2.3.0 numerical snapshots become non-comparable; accepted finding fingerprints
  keep applying. Capturing a new baseline accepts current eligible debt and is
  not a migration shortcut. See the pinned
  [engine release notes](https://github.com/alexei-led/archfit/blob/f8877d25ba36d84d3780071580d23486e3d794d0/docs/guide/release-notes.md).
- Action `action.yml` runs `scripts/report.sh`, which expects an existing state
  file. Its envelope has no standalone schema artifact and does not yet express
  every approved image/profile identity required by the design.
- Action handling of `workflow_run` can select the first associated PR and lack
  base/merge-base information. Those identities must be resolved and checked,
  not accepted as unverified producer assertions.
- Action CI currently exercises offline envelope assembly using a fixture from
  mutable engine `main`; it does not prove OIDC/upload/verifier interoperability.
- Engine discovery supports Go/TypeScript/Python/Rust, and enrichment exposes
  Anthropic/OpenAI/Ollama choices. Its prompt evidence can contain package
  comments and config snippets. That output is not automatically safe to upload
  under a no-source-to-Archfit promise.

Recheck immutable artifact availability at execution time. Do not infer that a
local tag, a schema artifact, or a successful unit test proves an installed
Action/engine/App combination interoperates.

## Execution rules

- Each task produces a bounded implementation or research artifact and its
  acceptance evidence. Keep one writer per file/worktree.
- Domain, transport, and persistence types remain separate. Generate wire types
  from approved versioned schemas; do not export engine-internal Go DTOs.
- Use the owning repository's instructions and checks. In this repository run
  `make fmt`, `make test`, `make lint`, and `make build`; use `make all` before
  pushing. New behavior requires meaningful tests and manual/E2E evidence.
- A change that first accepts reports must deliberately replace the 501 refusal
  test and demonstrate rejection of invalid provenance in the same review.
- Every milestone exit records what is supported, unsupported, and unverified.
  Missing evidence is not success; an unavailable optional LLM is not a gate
  failure.
- Cross-repository tasks below identify coordination needs. Approval to work in
  this repository alone is not approval to modify all three repositories.

## M0 — Resolve contracts and execution choices

These tasks cover measurement identity, wire contracts, and the decisions needed
before App integration. Research can proceed in parallel; mutations and
implementation still follow their dependencies.

### T01 — Verify and adopt the released engine identity contract

- **Owner/repository:** App/Action integration; engine changes only for a
  separately scoped, reproduced contract gap.
- **Depends on:** the refreshed inventory and v2.3.1 release evidence.
- **Deliver:** adopt v2.3.1 as the initial integration target; pin source revision,
  binary checksums and approved image/platform digests. Record the supported
  producer/toolchain manifest and its relationship to `measurement_profile`.
  Verify the shipped persistence/comparison contract rather than reimplementing
  it in the App. Image/profile eligibility and cross-platform equivalence remain
  integration acceptance work; published images alone do not establish them.
- **Accept:** on one unchanged tree, changing a relevant analyzer/profile cannot
  be reported as code-induced erosion; unknown identity cannot compare equal
  merely because both sides are unknown. A profile-less baseline and a v2.3.0
  numerical snapshot are explicitly non-comparable with v2.3.1, while accepted
  fingerprints remain applicable. Test the persisted `gate_reference` separately
  from `--base`; reviewed replacement must not silently accept new debt.

### T02 — Pin wire contracts and conformance fixtures

- **Owner/repository:** engine + Action + App consumer tests.
- **Depends on:** T01's pinned artifact and supported-profile inventory.
- **Deliver:** immutable schema/fixture sources, envelope schema ownership and
  versioning, generated App transport types, a supported-version matrix, and
  producer/consumer contract checks. Replace mutable-branch fixture inputs.
  Produce a capability matrix for all four HLD erosion scenarios across the
  proposed supported languages/workflows, with paired forbidden and legitimate
  changes for each claimed capability. Distinguish deterministic enforcement,
  advisory-only interpretation, missing evidence, and unsupported cases.
- **Accept:** named fixtures cover healthy, warning-only attention with passing
  hard gates, blocked, missing required evidence, unknown identities, comparable
  and incompatible profiles, and invalid input. Include a known blocker together
  with an unevaluated required rule, and a proven non-applicable rule.
  Cover comparable symmetric completed partials with a recognized `partial_basis`;
  missing inputs, timeouts and opaque unknowns remain non-comparable.
  Test decision semantics as well as JSON shape. Every fixture records its
  producer artifact and expected consumer outcome under the
  [gate mapping](../design/trust-and-reporting.md#gate-outcomes).
  Pin `decision.unevaluated_required_rules`, both repair kinds, validation-command
  replay, `locations: []`, `matched_by: {}`, and unique finding lifecycle IDs.
  Cover waiver expiry/order and baseline capture excluding waived findings.
  Each claimed erosion scenario has a passing positive/negative pair; gaps
  require an owner decision on scoped engine work or explicitly limited claims,
  not an assumption that the engine already detects them.

### T03 — Specify eligible producer and revision binding

- **Owner/repository:** App + Action integration design/tests.
- **Depends on:** starting inventory; align the final envelope with T02.
- **Deliver:** a documented trusted workflow path for Pumba/Archfit, authoritative
  repository/installation/run/attempt/PR/head/base/merge-base resolution, policy
  and image/profile binding, retry/replay rules, and explicit fork support limits.
  Specify the first path end to end: PR revision, pinned analysis, separated
  uploader credentials, authenticated report receipt, then advisory feedback.
  Resolve `workflow_run` associations rather than choosing its first PR. Record
  the producer command and exit semantics; report generation is not gate success.
- **Accept:** fixture cases include PR, default-branch baseline, `workflow_run`,
  multi-PR/ambiguous association, re-push, stale result, wrong tenant, modified
  workflow, wrong image, and mismatched report digest. Self-reported envelope
  fields alone never establish eligibility. Document the remaining attested
  report-forgery gap. Test missing/cancelled runs and duplicate attempts without
  allowing stale feedback or weakening the later required-gate lifecycle.

### T04 — Define the no-source result and AI-request contracts

- **Owner/repository:** engine/Action producers + App consumer boundary.
- **Depends on:** inventory and HLD privacy modes.
- **Deliver:** allowlisted discovery and report payloads, explicit permitted
  metadata, customer-side AI request/response states, limits, and error/logging
  rules. Decide whether a separate/versioned transport is needed; do not remove
  required measurement evidence merely to make a report smaller.
  Preserve approved decision/profile/reference fields and structured repair
  tasks through that transport. File paths, rule reasons and remediation text
  require an explicit metadata policy; schema-valid engine JSON is not itself a
  no-source payload guarantee.
- **Accept:** source snippets, raw prompt packs, arbitrary logs, and raw model
  responses are absent from outbound payloads, diagnostics, artifacts, and
  telemetry in this mode. Validate with seeded source-bearing/error fixtures.
  Customer model keys are never sent to the App. Document residual metadata
  exposure and the execution/network controls needed beyond payload filtering.

### T05 — Research hosting, costs, and operating targets

Initial [hosting research and cost model](../research/20260918-hosting.md)
is available. The owner selected Cloud Run with PostgreSQL and managed GCP
services as the primary validation target. A dedicated beta project is planned
but does not exist yet. Project ID, region, database tier and the acceptance
experiment remain open; this task is not complete merely because the research
memo exists.

- **Owner/repository:** deployment design in this repository; no account changes.
- **Depends on:** HLD budget and workload categories; refine with T01–T04.
- **Deliver:** an official-source comparison including Cloudflare and at least
  one conventional Go-container option, monthly fixed/variable cost model,
  tenancy/storage/isolation fit, reporter recovery design, and a recommendation.
- **Accept:** normal and burst assumptions are explicit; infrastructure fits the
  USD 100/month pilot ceiling under those assumptions; managed LLM is budgeted
  separately. Check Go compatibility, jobs, durable state, queues, secret
  handling, discovery isolation/egress, backups, logs, billing caps, and deletion.
  Do not assume free credits or equate billing alerts with hard spend limits.
  Set measurable latency/recovery/load targets with the owner before deployment.
- **Decision gate:** confirm the Cloud Run resource design, PostgreSQL service
  and tier, region and acceptance targets before provider-specific implementation.
  Cloud SQL is the current proposal, not a confirmed tier. Preserve the current
  reporter refusal behavior in initial hosting probes; integrate verified
  ingestion only through T09. Live resource creation requires its own explicit
  authorization and cleanup plan.

### T06 — Validate one-draft-PR onboarding feasibility

- **Owner/repository:** App/Action integration experiment.
- **Depends on:** T03/T04 designs; owner-provided test App/repository authorization.
- **Deliver:** a minimal App-created same-repository draft PR that runs discovery,
  reports allowed results, receives a policy update, and reruns without loops.
  Test representative organization restrictions and bootstrap fallback.
- **Accept:** document actual events, permissions, execution identity, source
  revision, and approval requirements. No privileged execution of target scripts
  to bypass restrictions; no claim that discovery proves gate eligibility.
  `workflow_dispatch` is not assumed to bootstrap a never-run PR-only workflow.
- **Fallback:** if the one-PR path is unsupported, use bootstrap merge followed
  by policy PR. Do not block the entire beta on this UX optimization.

**M0 exit:** reviewed contracts, migration expectations, supported producer path,
privacy contract, hosting decision, and recorded onboarding experiment/fallback.
These outputs are inputs to implementation, not prose substitutes for it.

## M1 — Verified advisory reporting on the pilot repositories

Advisory App/Action integration only. No required gate yet.

### T07 — GitHub installation, access, and event boundary

- **Owner:** App GitHub integration and authenticated repository context.
- **Depends on:** T03; storage interface agreed with T05.
- **Deliver:** webhook signature verification, installation/repository lifecycle,
  scoped installation-token use, user-to-repository authorization, and event
  identity normalization. Keep private keys as file/secret references.
- **Accept:** forged/replayed events, revoked access, removed repositories, and
  cross-installation identifiers do not expose data or schedule unauthorized
  work. Uninstall prevents new work and starts the defined cleanup lifecycle.

### T08 — Durable, tenant-scoped state and work delivery

- **Owner:** App persistence, jobs/outbox, and repository authorization seams.
- **Depends on:** T05/T07 contracts.
- **Deliver:** storage for installation/repository identity, run receipts,
  versioned policy/baseline references, jobs, and check/comment reconciliation;
  migrations and bounded retry handling.
  Include tenant-bound report encryption and key-lifecycle requirements from
  the [technical design](../design/trust-and-reporting.md#7-service-and-wire-contract-boundaries);
  object-name scoping alone is not encryption or authorization.
- **Accept:** retries and duplicate webhooks/uploads are idempotent; restart does
  not lose accepted work; tenant-scoping tests cover queries, object access,
  cache keys, and workers. No provider-specific persistence type leaks into the
  report or policy domain.

### T09 — Replace deliberate refusal with provenance verification

- **Owner:** reporter ingestion, generated transport, provenance verification.
- **Depends on:** T02/T03/T07/T08.
- **Deliver:** bounded report parsing and conformance validation; token signature,
  issuer/audience/time/identity checks; authoritative run/revision and artifact
  binding; permitted retries and replay detection; safe receipt persistence.
- **Accept:** a valid approved-producer fixture is accepted and all relevant
  negative T03 cases are rejected. Unknown evidence is never promoted to
  independent verification. Change `TestReportIngestRefusesEveryReport`
  deliberately with corresponding acceptance/rejection tests in the same PR.

### T10 — Complete the customer execution/upload adapter

- **Owner/repository:** Action + engine integration, consumed by App.
- **Depends on:** T01–T03/T09.
- **Deliver:** a supported pinned execution workflow/adapter that acquires issued
  inputs, creates the report, binds it to the correct revision and identities,
  obtains OIDC, and uploads it. Correct ambiguous/incomplete workflow-run fields;
  add online transport tests against a local verifier and immutable fixtures.
  Use contract fixtures for the input-issuance interface until T12 supplies the
  real service; the M1 live advisory path does not claim trusted-policy gating.
- **Accept:** changed policy/image/profile, wrong commit, modified report bytes,
  failed upload, and reruns produce the documented outcome. Separate target
  execution from reporting credentials and retain honest attested provenance.

### T11 — Advisory check and stable PR feedback

- **Owner:** reporter decision presentation and GitHub delivery.
- **Depends on:** T09/T10.
- **Deliver:** `archfit/state`, one updated PR comment, supported annotations,
  provenance/evidence gaps, and structured remediation tasks preserving
  `repair_kind` and validation-command replay. Handle pagination,
  GitHub rate limits, retry, and stale/head-changed results.
- **Accept:** healthy/blocked/partial cases are useful without an LLM; no score
  or verified badge; repeated delivery does not spam comments; old runs cannot
  overwrite newer head feedback; missing data is explained rather than hidden.

**M1 exit:** after explicit deployment/installation approval, an eligible real
PR on Pumba and Archfit completes pinned analysis, authenticated report intake,
and one updated advisory comment. Required enforcement remains disabled until
M2 acceptance. A tenant-isolation check and reporter restart/retry drill pass.
Credentials, public URL, and actual App registration block deployment only, not
local implementation or fixtures.

## M2 — Trusted inputs and the required gate

Enforce approved policy over disclosed customer-attested evidence. The
fabricated-report gap remains open; this is not independent measurement.

### T12 — Approved inputs and baseline lifecycle

- **Owner:** App policy resolution, approved artifacts, baseline service.
- **Depends on:** T01–T03/T08/T10.
- **Deliver:** protected-ref policy resolution and issued exact-byte snapshots;
  approved labels/image/profile identity; eligible default-branch baselines and
  merge-base lookup; missing/incompatible-baseline handling.
- **Accept:** head policy/labels/baseline edits cannot weaken the gate; no missing
  profile compares as compatible; no blanket re-baseline; one repository-wide
  evidence boundary is preserved. Baseline mutation is authorized and audited.

### T13 — Required check lifecycle and recovery

- **Owner:** gate evaluator, pending/final status delivery, reconciliation.
- **Depends on:** T11/T12.
- **Deliver:** separate `archfit/gate`, explicit failure/action-required outcomes,
  missing-upload handling, supported source/producer eligibility, and recovery
  instructions. Configure the documented separate-ruleset bypass through an
  owner-reviewed setup process; never remove other protections.
- **Accept:** missing/incompatible/ineligible evidence cannot yield a passing,
  neutral, or skipped required check. Forks without a supported acquisition path
  stay explicitly blocked. Queued, failed, cancelled, timed-out, and stale runs
  do not leave misleading green results. Reporter availability limitations and
  emergency bypass are rehearsed and recorded.

**M2 exit:** owner-approved gate rehearsal on both pilot repos demonstrates a
legitimate pass, a representative violation failure, attempted input tampering,
missing evidence, stale results, and emergency recovery.

The T02 capability matrix and paired cases must also be exercised for every
claimed erosion scenario in the supported pilot matrix. Explicitly present
advisory-only and unsupported cases; the owner resolves remaining engine/scope
gaps before accepting this milestone. One representative violation does not
stand in for coverage of all four product scenarios.

Only after these checks pass may the owner enable the required gate on a
selected pilot repository. This is not independent-measurement or
adversarial-build protection certification.

## M3 — Architecture governance and strict exceptions

Implement repository-scoped governance; the organization policy floor is deferred.

### T14 — Owners and exact-version policy approval

- **Owner:** policy proposal, owner authority, approval records, GitHub reviews.
- **Depends on:** T07/T12/T13.
- **Deliver:** initial owner bootstrap, trusted owner changes, open proposal
  rights, and architecture approval bound to exact head SHA and policy snapshot.
  Support a separate policy PR and atomic code-plus-policy PRs.
- **Accept:** an ordinary code review or head-edited CODEOWNERS cannot confer
  architecture authority; any new commit invalidates approval; stale UI edits
  require reconciliation. Approved candidate policy is server-issued for that
  exact PR, not arbitrary head-tree configuration.

### T15 — Model/profile changes and debt-preserving migration

- **Owner/repository:** App governance with engine comparison support.
- **Depends on:** T01/T12/T14.
- **Deliver:** classify policy/model/profile drift; show comparable evidence or
  explain why it is unavailable; replay supported profile changes on the same
  source; explicitly approve baseline migration while retaining accepted debt.
- **Accept:** upgrades cannot silently accept newly exposed violations. A rename
  or changed module mapping cannot be declared mechanically equivalent merely
  from a config diff or matching file counts. Until a relocation proof is
  implemented, uncertain cases require owner review. Distinguish preserved
  accepted fingerprints from replaced numerical snapshots; include the concrete
  v2.3.0-to-v2.3.1 transition from T01. Baseline capture is not automatic migration.

### T16 — Versioned exceptions, expiry, and invalidation

- **Owner:** governance, scheduler, affected-check reconciliation.
- **Depends on:** T08/T13–T15; settle HLD exception-publication proposal first.
- **Deliver:** narrow exception identity, reason, expiry, approval and renewal
  history; pre-expiry warnings; scheduled invalidation of affected open PRs;
  concurrency rules for policy/approval changes versus check publication.
  Serialize and validate exceptions against the pinned
  [engine waiver contract](../design/trust-and-reporting.md#engine-waiver-contract).
- **Accept:** expiry makes the remaining violation blocking even for unrelated
  PRs without a new commit. No unrelated finding is waived; renewal requires
  owner approval; expired debt is not retained by an accidental baseline update.
  Active matching exceptions win regardless of order, and baseline capture skips
  both active and expired waived findings. Engine `approved_by` text alone never
  establishes authenticated App approval.
  Measure and document reconciliation/race windows, not an untested zero-window
  promise. Test retries and missed scheduler delivery/recovery.

**M3 exit:** demonstrate code-only, policy-only, atomic code/policy, re-push,
owner change, incompatible profile, exception grant/expiry/renewal, and attempted
unauthorized weakening on real pilot workflows or controlled integration cases.

## M4 — Complete both onboarding paths and the editing experience

### T17 — Authenticated UI, YAML validation, and graph view

- **Owner:** App UI/API and frontend assets; choose the framework explicitly.
- **Depends on:** T07/T08/T12/T14; editor feasibility research.
- **Deliver:** repository selection/status, authorized organization overview,
  YAML editor with schema diagnostics, engine validation against identified
  source, policy diff/PR publication, history, and a graph linked to YAML.
- **Accept:** no score; no cross-tenant or revoked-user access; invalid/stale
  drafts cannot publish unnoticed; graph state does not overwrite unsupported
  YAML constructs or hide stale validation. UI and GitHub-first edits reach the
  same canonical Git policy and approval process.
- **Optional:** bounded visual edits only after a tested YAML round-trip proof;
  a full diagram editor is not a beta dependency.

### T18 — Customer discovery and guided CI onboarding

- **Owner/repository:** App orchestration + Action/engine discovery transport.
- **Depends on:** T04/T06/T10/T17.
- **Deliver:** mode selection, prepared discovery PR, waiting/results/error UI,
  grouped boundary review, same-PR completion where validated, bootstrap fallback,
  and optional CLI/existing-CI path. Keep discovery suggestions separate from
  eligible required-gate evidence.
- **Accept:** an ordinary user can complete the documented flow; a restricted
  organization reaches the fallback without unsafe permission bypass. Outbound
  payload tests establish the agreed source boundary. Keys remain at the client.
  Failure/retry does not create duplicate onboarding PRs or silently publish
  unreviewed architecture.

### T19 — Managed discovery with explicit data/model consent

- **Owner:** App isolated background workers and onboarding integration.
- **Depends on:** T04/T05/T17; reviewed isolation and data lifecycle.
- **Deliver:** ephemeral source handling, bounded scan jobs, cancellation and
  cleanup, source/document reconciliation, optional managed LLM, and final
  onboarding PR. Record consent and generation provenance beside review.
- **Accept:** no persistent source copies in storage, logs, artifacts, or caches;
  no repository-script execution with reporting/model credentials; quotas bound
  concurrency and model spending. No consent means no managed LLM call. On
  failure or budget exhaustion, expose a clear resumable state.

### T20 — Explicit AI assistance in both execution modes

- **Owner:** editor requests, async jobs, proposal validation; customer adapter.
- **Depends on:** T04/T17–T19.
- **Deliver:** request a YAML completion/rule or finding explanation; send only
  permitted context to the chosen execution environment; bind response to the
  request/policy/source versions; display a proposal and its validation.
- **Accept:** results are never auto-published; stale proposals cannot overwrite
  edits; cancel/retry is bounded; customer-mode latency is visible; customer
  credentials do not enter App storage. Raw source-bearing model output cannot
  bypass the T04 transport boundary. LLM failure leaves schema editing and
  deterministic gate behavior operational.

**M4 exit:** both onboarding modes and both review surfaces complete on pilots;
private-mode leakage checks and managed-mode cleanup checks pass. New users can
understand validation failures and review proposals without knowing the internals.

## M5 — Operate a measured, invite-only beta

### T21 — Entitlements, limits, and suspension behavior

- **Owner:** control-plane entitlement state; reporter consumes cached state.
- **Depends on:** T08/T13/T16/T19/T20.
- **Deliver:** public/private classification, explicit beta grants, trial/grace
  state machine, warning and suspension reasons, managed-resource budgets, and
  audited administrative transitions. Proposal: manually provision beta grants
  before choosing/integrating public checkout; confirm beta commercial terms.
- **Accept:** confirmed expiry after grace produces a clear blocking subscription
  status, not fabricated architectural failure/success. Reporter remains
  functional. Billing-provider unavailability does not establish non-payment;
  managed LLM quota exhaustion does not stop deterministic checks. Restoring
  entitlement cannot reuse stale or otherwise ineligible evidence as success.
- **Open inputs:** trial start/length, grace, retention, repo bands, and paid
  allowance rules need explicit owner decisions before customer-facing use.

### T22 — Cost, recovery, and load evidence

- **Owner:** operations validation, telemetry, retention/backup/cleanup.
- **Depends on:** T05/T08/T13/T16/T18–T21.
- **Deliver:** per-installation resource accounting, source-safe diagnostics,
  documented restore/replay procedure, budget alarms and actual enforcement
  mechanisms, and reports for ordinary/burst/large workloads.
- **Accept:** demonstrate reporter operation during UI/LLM/billing disruption,
  tenant isolation under concurrent jobs, duplicate/out-of-order event recovery,
  backup restore, token revocation/uninstall behavior, and the selected latency
  and recovery targets. Include third-party services, backups, logs, and transfer
  in the infrastructure estimate; keep managed LLM cost separate.
- **Omni:** run only the agreed local benchmark. Measure/report workload without
  uploading its source or installing App infrastructure there. A subtree result
  cannot validate the full-repository gate.

### T23 — Invited-team beta and release evidence

- **Owner:** product validation and operator, with authorized customer contacts.
- **Depends on:** M1–M4 exit evidence, T21/T22, owner release checkpoint.
- **Deliver:** invite-only access to owner-selected teams, supported-use guide,
  onboarding and incident feedback, false-positive/remediation observations,
  costs, and evidence about value/willingness to pay. No messages or invitations
  are sent merely because this task appears in a plan.
- **Accept:** each invited team has clear data-mode consent, supported workflows,
  an owner, and documented recovery. Record issues and fixes before expansion;
  do not equate our own repository success with external usability.

**Closed-beta exit:** owner reviews technical behavior, onboarding, privacy,
operating cost, feedback quality, and external-team experience. Public checkout,
final prices/limits, subscription-provider integration, Marketplace requirements,
and public reliability/support promises belong to the subsequent commercial
launch plan. This sequencing does not change the agreed paid private-repo model.

## Coverage of owner-selected requirements

| Requirement                                        | Tasks/evidence                                                   |
| -------------------------------------------------- | ---------------------------------------------------------------- |
| Four erosion scenarios, deterministic PR guidance  | T01/T02/T11–T13; engine capability matrix and paired pilot cases |
| UI and GitHub-first policy editing                 | T14/T17–T19                                                      |
| Same-PR code/policy approval; re-push invalidation | T14/T15                                                          |
| Strict temporary exceptions                        | T16                                                              |
| Customer-held LLM keys and no source to Archfit    | T04/T18/T20                                                      |
| Managed discovery and opt-in managed LLM           | T19/T20                                                          |
| YAML/schema validation and explicit AI completion  | T17/T20                                                          |
| Repo-scoped policy; organization access/overview   | T07/T08/T17; no org policy floor                                 |
| Trial, suspension, public free model               | T21; commercial terms before public launch                       |
| USD 100 infrastructure and separate model budget   | T05/T19/T21/T22                                                  |
| Pumba/Archfit first; Omni local only               | M1/M2 exits and T22/T23                                          |

## Owner inputs at the point they are needed

- **Before implementing cross-repo prerequisites:** approval of the relevant
  engine/Action scope and milestone boundary.
- **Before any GitHub experiment or deployment:** test App identity, secret/key
  references, authorized repositories, account/region choice, and public URL.
  No inline PEMs or credentials in documentation.
- **Before required-gate activation:** architecture-owner identity, protected
  policy source, accepted baseline, successful rehearsal, and explicit bypass
  authority/process.
- **Before invited-team access:** authorized teams, data-mode consent, beta
  entitlements/expiry terms, support contact, and selected operating limits.
- **Before paid public launch:** billing provider, prices, trial/grace terms,
  service commitments, and verified unit economics.

Keep a record of completed tasks and their evidence when execution begins.
Do not mark research assumptions or unrun integration experiments as passing.

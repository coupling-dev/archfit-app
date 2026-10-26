# Gate Verdict

**Path**: internal/gate/verdict/
**Parent**: internal/gate/ (gate façade)
**Submodules**: none (leaf)

## Purpose

`verdict` turns the facts the gate collected into one outcome: a conclusion and an ordered list of
reasons. It is a pure function with a closed result vocabulary. Without it, "fail closed" would be a
habit spread over handlers; with it, a passing result exists in exactly one place and only when every
precondition holds.

## Functional Responsibilities

- `Decide`: map `Facts` to an `Outcome` in a fixed order: subscription, settings, source and producer
  eligibility, governance (owner approval), input integrity, evidence completeness, engine decision.
- `Without`: the fixed outcome for a target that has no report (missing upload, stale inputs, expired
  waiver, fork, suspension, withdrawn owner approval).
- Keep architecture, evidence, governance and subscription outcomes apart through `Category`.
- Preserve secondary reasons (for example unevaluated required rules beside a blocker) for feedback.
- Give every reason a recovery action (`Next`).

## Subdomain Classification

Core. This is the rule set a customer pays for. Volatility: high; the mapping is tuned with every pilot.
The contract (`Facts` in, `Outcome` out) is kept small so that tuning stays inside.

## Encapsulated Knowledge

- The decision order and which step wins.
- The mapping from engine decision fields (`verdict`, `hard_gates`, `unevaluated_required_rules`,
  `gate_reference.status`) to conclusions.
- The digest predicate for policy, baseline and labels, when a non-comparable reference is expected, and which owner
  action a reference needs (from its drift classes only).
- The rule that integrity failures cannot be cleared by approval.
- The category of every reason code and the recovery text of every reason.
- That task origin and finding status are never inputs of enforcement.
- What a warning-only pass names (`attention_only`): the dimensions not fully measured and the count of active
  diagnostics. That is display text; neither changes the conclusion.
- Which unevaluated required rule is a policy defect (its engine reason starts with `selector matches nothing`) and
  which lacks evidence.

## Public Contract

```go
// Conclusion is the only vocabulary the gate can publish. neutral and skipped are not representable.
type Conclusion string

const (
    Success        Conclusion = "success"
    Failure        Conclusion = "failure"
    ActionRequired Conclusion = "action_required"
)

// Category keeps architecture, evidence, governance and subscription outcomes from masquerading as each other.
type Category string

const (
    CategoryArchitecture Category = "architecture"
    CategoryEvidence     Category = "evidence"
    CategoryGovernance   Category = "governance"
    CategorySubscription Category = "subscription"
)

type ReasonCode string

const (
    ReasonHealthy             ReasonCode = "healthy"
    ReasonAttentionOnly       ReasonCode = "attention_only" // needs_attention, and nothing blocks
    ReasonBlockingFindings    ReasonCode = "blocking_findings"
    ReasonEngineError         ReasonCode = "engine_error"
    ReasonMissingCoverage     ReasonCode = "missing_required_coverage"
    ReasonBaselineNotComparable ReasonCode = "baseline_not_comparable"
    ReasonBaselineMismatch    ReasonCode = "baseline_mismatch"
    ReasonLabelsMismatch      ReasonCode = "labels_mismatch"
    ReasonPolicyMismatch      ReasonCode = "policy_mismatch"
    ReasonPolicyMissing       ReasonCode = "policy_missing" // settings merged, no policy on the protected ref yet
    ReasonPolicyDefect        ReasonCode = "policy_defect"  // a required rule's selector matches nothing
    ReasonUnknownIdentity     ReasonCode = "unknown_engine_identity"
    ReasonProfileMissing      ReasonCode = "measurement_profile_missing"
    ReasonWorkflowNotApproved ReasonCode = "workflow_not_approved"
    ReasonOwnerApprovalMissing ReasonCode = "owner_approval_missing"
    ReasonSettingsMissing     ReasonCode = "settings_missing"
    ReasonForkUnsupported     ReasonCode = "fork_unsupported"
    ReasonUnsupportedEvent    ReasonCode = "unsupported_event"
    ReasonEnvelopeMismatch    ReasonCode = "envelope_mismatch"
    ReasonReportInvalid       ReasonCode = "report_invalid"
    ReasonMissingUpload       ReasonCode = "missing_upload"
    ReasonStaleInputs         ReasonCode = "stale_inputs"
    ReasonWaiverExpired       ReasonCode = "waiver_expired"
    ReasonSuspended           ReasonCode = "subscription_suspended"
)

// ClassifyDisplayReason checks the owned reason/category vocabulary for presentation, not gate policy.
// reportDerived identifies the six assessment/recovery families; unknown or mismatched metadata is unclassified.
func ClassifyDisplayReason(code ReasonCode, category Category) (reportDerived, known bool)

type Reason struct {
    Code     ReasonCode
    Category Category
    Detail   string // IDs and digests only
    Next     string // the recovery action
}

// Outcome is the gate decision. Reasons[0] determines Conclusion; the rest are preserved for feedback.
type Outcome struct {
    Conclusion Conclusion
    Reasons    []Reason
}
```

```go
// Facts is the closed input of the decision. Report is nil when no usable report exists.
type Facts struct {
    Problems         []Problem
    BaselineDigest   string // claimed by the envelope; "" = none used
    LabelsDigest     string // claimed by the envelope; "" = none used
    Report           *Report
    IdentityApproved bool
    WorkflowApproved bool
    Trusted          Trusted
}

// Decide is a pure function. It checks, in order: subscription, settings, source and producer eligibility,
// governance (owner approval), input integrity, evidence completeness, then the engine decision.
// The step table of gate/verdict is the normative order.
func Decide(f Facts) Outcome

// Without returns the fixed outcome for a target that has no report: code is one of ReasonMissingUpload,
// ReasonStaleInputs, ReasonWaiverExpired, ReasonForkUnsupported, ReasonSuspended, ReasonOwnerApprovalMissing.
func Without(code ReasonCode) Outcome
```

```go
// Suspended is Without(ReasonSuspended) for a caller that holds the entitlement record: Next is
// e.Reason when it is not empty, exactly as step 1 of Decide sets it.
func Suspended(e Entitlement) Outcome

// ApprovalMissing is Without(ReasonOwnerApprovalMissing) for a caller that holds the trusted inputs: Detail names
// t.ProtectedPaths, exactly as step 5 of Decide sets it.
func ApprovalMissing(t Trusted) Outcome

// Unapproved is the condition of step 5: t needs an owner approval that it does not have. Only the two known passing
// values clear it, so an unknown Approval fails closed.
func Unapproved(t Trusted) bool
```

### What the two booleans mean

`gate` computes them; `Decide` only reads them.

- `IdentityApproved`: `manifest.Lookup(Upload.Identity)` hits, **and** the report carries a measurement profile,
  **and** `MeasurementProfile.Version == Entry.ProfileVersion`. Any empty identity field is a miss, so unknown never
  equals unknown. False when there is no report. The approved profile version of engine v3.0.1
  is `archfit.measurement.v2`; the value lives in the manifest, not here.
- `WorkflowApproved`: settings were found, `Binding.WorkflowPath` is not empty, and it equals
  `Trusted.Settings.Workflow`. `Binding.WorkflowPath` comes from the verified OIDC `job_workflow_ref`. A workflow
  file changed by the PR is a protected path, which `gate/inputs` turns into `ApprovalMissing` until an owner approves.

### Decision order

`Decide` evaluates every step in order and appends every reason that applies. `Reasons[0]` sets
`Conclusion`. `ReasonAttentionOnly` and `ReasonHealthy` are appended only when no other reason exists. `Reasons` is
never empty.

| Step | Condition                                                                                                       | Reason appended               | Needs `Report` |
| ---- | --------------------------------------------------------------------------------------------------------------- | ----------------------------- | -------------- |
| 1    | `Trusted.Suspended`                                                                                             | `subscription_suspended`      | no             |
| 2    | `!Trusted.SettingsFound`                                                                                        | `settings_missing`            | no             |
| 3a   | `Problems` contains `fork`                                                                                      | `fork_unsupported`            | no             |
| 3b   | `Problems` contains `unsupported_event`                                                                         | `unsupported_event`           | no             |
| 3c   | `Problems` contains `envelope_mismatch`                                                                         | `envelope_mismatch`           | no             |
| 3d   | `Problems` contains `report_invalid`, or `Report == nil` **[default]**                                          | `report_invalid`              | no             |
| 4a   | `!WorkflowApproved`                                                                                             | `workflow_not_approved`       | no             |
| 4b   | `!IdentityApproved` and `Comparison.MeasurementProfile == nil`                                                  | `measurement_profile_missing` | yes            |
| 4c   | `!IdentityApproved` and the profile is present                                                                  | `unknown_engine_identity`     | yes            |
| 5    | `Trusted.Approval` is neither `ApprovalNotRequired` nor `ApprovalGranted`                                       | `owner_approval_missing`      | no             |
| 6a   | `len(Trusted.PolicyDigests) == 0` (settings merged, no policy on the protected ref yet)                         | `policy_missing`              | no             |
| 6b   | `PolicyDigests` not empty and `!Accepts(PolicyDigests, Comparison.ConfigHash)`                                  | `policy_mismatch`             | yes            |
| 6c   | `!Accepts(Trusted.BaselineDigests, Facts.BaselineDigest)` (digest predicate, below)                             | `baseline_mismatch`           | no             |
| 6d   | `!Accepts(Trusted.LabelsDigests, Facts.LabelsDigest)`                                                           | `labels_mismatch`             | no             |
| 7a   | `ReferenceConflicts(GateReference, len(Trusted.BaselineDigests) > 0)` (below)                                   | `baseline_not_comparable`     | yes            |
| 7b   | `Verdict` or `HardGates` holds a value outside its enum                                                         | `engine_error`                | yes            |
| 7c   | `HardGates != "fail"` and a dead-selector rule exists (below)                                                   | `policy_defect`               | yes            |
| 7d   | `HardGates != "fail"` and another unevaluated rule exists, or `HardGates == "unmeasured"` and 7c did not hit    | `missing_required_coverage`   | yes            |
| 8a   | `HardGates == "fail"` or `Verdict == "blocked"`                                                                 | `blocking_findings`           | yes            |
| 8b   | step 8a hit, a dead-selector rule exists, and step 7c did not hit                                               | `policy_defect`               | yes            |
| 8c   | step 8a hit, another unevaluated rule exists, and step 7d did not hit                                           | `missing_required_coverage`   | yes            |
| 8d   | no reason so far and `Verdict == "needs_attention"` (then `HardGates == "pass"`)                                | `attention_only`              | yes            |
| 8e   | no reason so far (then `Verdict == "healthy"` and `HardGates == "pass"`)                                        | `healthy`                     | yes            |

Rules of the table:

- Steps marked "yes" are skipped when `Report == nil`.
- When step 2 hits, steps 4 to 8 are skipped **[default]**: there are no trusted inputs to compare with.
- Problems are read as a set; order and duplicates in `Facts.Problems` do not matter. An unknown `Problem` value counts as `envelope_mismatch` **[default]**.
- Step 5 fails closed: only the two known passing values of `Approval` clear it. `ApprovalMissing`, the zero value
  and any value a later version of `gate/inputs` adds all give `owner_approval_missing`.
- Digest predicate: `gate/inputs` owns it as `Accepts(trusted, claim)`; verdict has no comparison of its own. A
  mismatch is `!Accepts`. Both empty is a match: no baseline (or no labels file) on the protected ref and none used
  by the producer is allowed. `""` against a list with entries is a mismatch: a trusted baseline exists and the
  producer did not use it (no blanket re-baseline).
- The engine hashes only the policy (`Comparison.ConfigHash`). It reports no digest for the baseline or the labels
  file (`Comparison.LabelsHash` is a canonical hash, not reproducible from raw bytes, and is not compared). The
  baseline and labels digests are stated by the producer in the envelope. They are exactly as trustworthy as the
  report itself: customer-attested. They catch an honest producer running with the wrong files; they do not close
  the forged-report gap, which is unchanged.
- All digests are bare lowercase hex SHA-256 as `policy.Digest` returns them; there is no `sha256:` prefix here.
- Comparability (7a): `ReferenceConflicts(ref, baselineTrusted)` is true when the engine's gate reference contradicts
  the trusted baseline. `baselineTrusted` is true when the protected ref has a baseline. The engine must have
  compared against that baseline: `GateReference.Status == "comparable"`. A nil reference, `non_comparable` or
  `not_requested` is a conflict. The engine must also have loaded a baseline exactly when the App trusts one:
  `GateReference.BaselinePresent == baselineTrusted`. Any other pairing is a conflict, so this step fails closed.
  When the protected ref has no baseline, the engine reports `non_comparable` with "no baseline file was loaded"
  and `baseline_present: false`; that is expected and ignored **[default]** (6c already requires that the producer
  used none). Feedback then discloses "no baseline: every finding counts as new". `Comparison.Status` describes
  the report-only `--base` comparison and is never read: `--base` never substitutes the accepted-debt authority.
- Owner action (`Next` of `baseline_not_comparable`): only `GateReference.Drift` picks it, never the reason text.
  When every class is `measurement_profile` or `rubric_version`, the action is a re-anchor: run
  `archfit baseline --reanchor` and open a baseline PR that an architecture owner approves. For any other class, an
  empty `Drift` and a nil reference, the action is a full capture: run `archfit baseline` and open that PR.
  A `BaselinePresent` mismatch has no drift class to read. Its Detail names the side that holds the baseline ("the protected ref has a baseline and the
  engine loaded none", or the reverse), and its `Next` is always the full capture.
- An engine error (process exit 3) produces no report. Nothing is uploaded, the pending check stays, and the sweep
  ends it as `missing_upload`. `engine_error` therefore only names a decoded report whose `Verdict` or `HardGates`
  is outside its enum; the strict decoder of `wire/report` normally rejects that first (`report_invalid`), so this
  is the closed answer for decoder drift.
- A `ReasonCode` appears at most once in `Reasons`.
- An empty `Trusted.PolicyDigests` means the repository is between onboarding PR 1 (settings) and PR 2 (policy).
  That is `policy_missing` (6a), never `policy_mismatch`: step 6b never compares against an empty list. It sits
  after step 5 on purpose: the PR that adds the policy changes a protected path, so until an owner approves it
  `owner_approval_missing` stays `Reasons[0]` (the re-run key); after approval the head digest is in
  `PolicyDigests` and 6a no longer hits. For every other PR in that window `policy_missing` is first among steps 5 to 8.
- Digest comparison is exact string equality. An empty digest never matches.
- Governance before integrity (step 5 before step 6). In an atomic code-and-policy PR the report's `config_hash` is
  the head digest, which `gate/inputs` accepts only after an owner approved the exact head. Until then
  `owner_approval_missing` is `Reasons[0]` and the digest mismatch is appended after it, so the receipt reason is
  `owner_approval_missing` and a later owner review triggers the re-run. Once approval is granted the head digests
  are in `PolicyDigests`, `BaselineDigests` and `LabelsDigests`; a mismatch that remains is a true integrity failure,
  and approval cannot clear it. Steps 6a to 6d never read `Approval`. The comment on `Decide` lists the concerns; this table is the
  normative order of `Reasons`.
- Missing coverage is cleared by supplying the fact, never by approval.
- Dead selector (7c, 8b): an unevaluated required rule whose `Reason` starts with the exact engine prefix
  `selector matches nothing` (engine form `selector matches nothing: <side> <glob>`). Such a rule is a defect of the
  approved policy, not missing evidence: the fix is an owner-approved policy PR, or `guard: true` for a rule that
  matches nothing on purpose. Every other reason, a near miss included (other case, leading space), is missing
  evidence (7d, 8c). Both reasons are `action_required` when first, so the split never opens a way to `success`.
  `policy_defect` names only its rules and `missing_required_coverage` only the others; unmeasured hard gates that a
  dead-selector rule explains give no empty `missing_required_coverage`.
- `AgentTasks`, `Coverage`, `Seams` and task `Origin` are never read, and `Findings` and `Dimensions` are never read
  for the conclusion. Empty findings prove nothing. `Decision.ActiveBlockers`, `AttentionDimensions` and
  `UnknownDimensions` are counts for display and are not read either. `Verdict == "needs_attention"` does not change
  the conclusion: it only selects `attention_only` instead of `healthy` in the last step.
- The `attention_only` Detail is `dimensions not fully measured: <keys>; active diagnostics: <n>`. `<keys>` are those of
  the nine engine dimension keys, in the engine's order, whose `Dimensions` entry is missing or whose `Status` is not
  `measured`, or `none`; a map key outside the nine is never named. `<n>` counts findings of kind `advisory` with status
  `new` or `expired_waiver`, the engine's active diagnostics. The `Next` text says that advisories never block.

### Engine decision matrix (steps 7b to 8e)

A report carries no exit code. The inputs are `Report.Verdict` (`healthy`, `needs_attention`, `blocked`),
`Decision.HardGates` (`pass`, `fail`, `unmeasured`) and `Decision.UnevaluatedRequiredRules`. `engine_error` (7b)
comes before `missing_required_coverage` (7d). The rows below have no dead-selector rule; a dead-selector rule
adds `policy_defect` just before the place `missing_required_coverage` takes in its row, and takes that place
alone when every unevaluated rule is a dead selector.

| `Verdict`                      | `HardGates`  | Unevaluated rules | Reasons from these steps                                         | Conclusion if first |
| ------------------------------ | ------------ | ----------------- | ---------------------------------------------------------------- | ------------------- |
| `healthy`                      | `pass`       | none              | `healthy`                                                        | `success`           |
| `needs_attention`              | `pass`       | none              | `attention_only` (warning-only attention)                        | `success`           |
| `healthy` or `needs_attention` | `pass`       | some              | `missing_required_coverage`                                      | `action_required`   |
| any of the three               | `unmeasured` | any               | `missing_required_coverage` (+ `blocking_findings` if `blocked`) | `action_required`   |
| any of the three               | `fail`       | none              | `blocking_findings`                                              | `failure`           |
| any of the three               | `fail`       | some              | `blocking_findings`, `missing_required_coverage`                 | `failure`           |
| `blocked`                      | `pass`       | none              | `blocking_findings` (contradictory input, closed)                | `failure`           |
| `blocked`                      | `pass`       | some              | `missing_required_coverage`, `blocking_findings` (no repeat)     | `action_required`   |
| outside the enum               | any          | any               | `engine_error` first, then whatever else applies                 | `action_required`   |
| any                            | outside enum | any               | `engine_error` first, then whatever else applies                 | `action_required`   |

### Gate outcome mapping (trust-and-reporting.md §5)

The mapping applies only after steps 1 to 6 are clear.

| Eligible run outcome                                                                                         | Conclusion        | Reason                                                                                |
| ------------------------------------------------------------------------------------------------------------ | ----------------- | ------------------------------------------------------------------------------------- |
| Healthy: `verdict: healthy`, `hard_gates: pass`                                                              | `success`         | `healthy`                                                                             |
| Blocked: `verdict: blocked` or `hard_gates: fail`                                                            | `failure`         | `blocking_findings`; feedback adds findings and tasks                                 |
| Warning-only attention: `verdict: needs_attention`, `hard_gates: pass`, all evidence and reference rules met | `success`         | `attention_only`, naming what needs attention; `archfit/state` stays `neutral`        |
| Required gate reference non-comparable (a baseline exists on the protected ref)                              | `action_required` | `baseline_not_comparable`; recovery is an owner-approved baseline PR                  |
| A required rule's selector matches nothing (engine dead-selector reason)                                     | `action_required` | `policy_defect`, naming the rule IDs; recovery is an owner-approved policy PR         |
| Required coverage missing: `hard_gates: unmeasured` or unevaluated required rules                            | `action_required` | `missing_required_coverage`, naming the rule IDs                                      |
| Engine error (exit 3, so no report), absent report, ineligible evidence                                      | `action_required` | `missing_upload` (sweep), `report_invalid`, `engine_error`, or the eligibility reason |
| Independent-observation contradiction                                                                        | not in v1         | the feature does not exist                                                            |

### Reason catalog

Categories of integrity, comparability and `Without` codes are **[default]**. `Next` texts are **[default]**.

| Code                          | Category     | Conclusion when first | `Detail`                                                                                              | `Next`                                                                                                                |
| ----------------------------- | ------------ | --------------------- | ----------------------------------------------------------------------------------------------------- | --------------------------------------------------------------------------------------------------------------------- |
| `healthy`                     | architecture | `success`             | empty                                                                                                 | empty                                                                                                                 |
| `attention_only`              | architecture | `success`             | `dimensions not fully measured: <keys or none>; active diagnostics: <count>`                          | "Advisories never block. Review the listed dimensions and diagnostics on the archfit/state check."                    |
| `blocking_findings`           | architecture | `failure`             | empty                                                                                                 | "Fix the rule violations, then run archfit check again."                                                           |
| `engine_error`                | evidence     | `action_required`     | `verdict=<value> hard_gates=<value>`, each cut to 32 characters                                       | "Read the Archfit workflow log, use the pinned engine image, re-run the workflow."                                    |
| `missing_required_coverage`   | evidence     | `action_required`     | rule IDs, comma separated; the first 20, then `and N more` **[default]**                              | "Supply the missing evidence for the listed rules. Approval cannot clear this."                                       |
| `baseline_not_comparable`     | evidence     | `action_required`     | `GateReference.Reasons`: first 3, 200 characters in total **[default]**; `no gate reference` when nil. A `BaselinePresent` mismatch names the side that holds the baseline instead | Re-anchor or full capture, by `GateReference.Drift` (rules above).                                                    |
| `baseline_mismatch`           | evidence     | `action_required`     | claimed digest or `none`                                                                              | "Run the workflow with the baseline from the base branch."                                                            |
| `labels_mismatch`             | evidence     | `action_required`     | claimed digest or `none`                                                                              | "Run the workflow with the labels file from the base branch."                                                         |
| `policy_mismatch`             | evidence     | `action_required`     | reported `config_hash`                                                                                | "Run the workflow with the policy from the base branch; re-run if the base changed."                                  |
| `policy_missing`              | governance   | `action_required`     | empty                                                                                                 | "Merge the Archfit policy PR."                                                                                        |
| `policy_defect`               | governance   | `action_required`     | rule IDs whose selector matches nothing, comma separated; capped like `missing_required_coverage`     | "Fix the selectors of the listed rules in an owner-approved policy PR, or mark an intentional guard rule guard: true." |
| `unknown_engine_identity`     | evidence     | `action_required`     | empty                                                                                                 | "Move the Archfit workflow to the archfit-action commit and engine image the App approves now; see "Move an installed workflow to a new engine" in the Archfit user guide."                                                     |
| `measurement_profile_missing` | evidence     | `action_required`     | empty                                                                                                 | "Move the Archfit workflow to the archfit-action commit and engine image the App approves now; see "Move an installed workflow to a new engine" in the Archfit user guide. The report carries no measurement profile."          |
| `workflow_not_approved`       | evidence     | `action_required`     | empty                                                                                                 | "Upload from the workflow named in `.archfit-app.yaml`."                                                              |
| `owner_approval_missing`      | governance   | `action_required`     | changed protected paths                                                                               | "An architecture owner must authorize this exact commit: approve it on GitHub, or push it from an owner account. GitHub does not let an author approve their own pull request; use the App policy editor, where the App authors the commit."                                                               |
| `settings_missing`            | governance   | `action_required`     | empty                                                                                                 | "Merge the Archfit onboarding PR to add `.archfit-app.yaml`."                                                         |
| `fork_unsupported`            | evidence     | `action_required`     | empty                                                                                                 | "Push the branch to this repository, or use the documented ruleset bypass."                                           |
| `unsupported_event`           | evidence     | `action_required`     | empty                                                                                                 | "Run Archfit on `pull_request` or on `push` to the default branch."                                                   |
| `envelope_mismatch`           | evidence     | `action_required`     | empty                                                                                                 | "Re-run the Archfit workflow; the upload did not match the GitHub run."                                               |
| `report_invalid`              | evidence     | `action_required`     | empty                                                                                                 | "Upload the unmodified engine report; use the pinned action version."                                                 |
| `missing_upload`              | evidence     | `action_required`     | empty                                                                                                 | "No report arrived. Check the Archfit workflow run and re-run it."                                                    |
| `stale_inputs`                | evidence     | `action_required`     | empty                                                                                                 | "Policy or baseline changed on the base branch. A re-run was requested; re-run the Archfit workflow if none appears." |
| `waiver_expired`              | governance   | `action_required`     | empty                                                                                                 | "A waiver expired. A re-run was requested; renew the waiver by owner-approved PR or fix the finding."                 |
| `subscription_suspended`      | subscription | `action_required`     | empty                                                                                                 | `Entitlement.Reason` in `Decide` and `Suspended` when not empty; else "Archfit subscription inactive."                |

`Detail` carries protected path names for `owner_approval_missing`; these are the five fixed protected
paths, not customer source paths. The one engine-authored text in a `Detail` is the capped reason list of
`baseline_not_comparable` (single-line, length-capped by the decoder, no source). No `Detail` carries a finding,
a file path from the report, or a module name. The `attention_only` Detail holds only fixed dimension keys and a
count; the `policy_defect` Detail holds rule IDs, never the dead glob (feedback shows the engine reason with the
glob in the evidence-gap section). `labels_mismatch` is category evidence **[default]**.

`Without(code)` returns `ActionRequired` with the single catalog reason of `code`. A code outside the
six listed ones returns `ActionRequired` with `engine_error` **[default]**; `Without` can never return `Success`.
`Suspended(e)` and `ApprovalMissing(t)` are the two codes whose `Decide` reason carries record data; each returns
exactly the `Reasons[0]` that `Decide` gives for the same record, so a sweep correction reads like the upload
decision it replaces. `Unapproved(t)` is the condition of step 5; `gate/sweep` uses it to find a receipt decided
under an owner approval that is gone, so the step and the sweep cannot disagree about which approvals clear it.

### Why `neutral` and `skipped` cannot be represented

- `Conclusion` is a closed set of three constants. `Decide` and `Without` build the conclusion only from the
  catalog column above; there is no conversion from a string.
- `gate/feedback` writes `string(Outcome.Conclusion)` to `archfit/gate` and nothing else. The literal
  `neutral` exists only in feedback for the never-required `archfit/state` check and never passes through `Conclusion`.
- `Success` has one producer: the last step (8d `attention_only` or 8e `healthy`). It needs an empty reason list, so
  every earlier step must be clear.
- A property test holds the set closed for arbitrary `Facts`.

## Integrations

### gate/ingest

- **Counterpart**: internal/gate/ingest
- **Direction**: verdict depends on ingest (reads `Problem` values)
- **Strength**: model
- **LCA / Rank / Distance**: gate / 1 / 1
- **Volatility**: high
- **Balanced?**: yes (model strength at distance 1)
- **Shared knowledge**: the `Problem` constants. `Binding` and `Upload` are not read here. `Repo` is an
  opaque part of `Binding` for this module. `Identity` is an opaque part of `Upload` for this module.

<!-- contract: Upload, Binding, Problem — restated from internal/gate/ingest/module.md -->
```go
// Binding is the authoritative identity of an upload, resolved from OIDC claims and the GitHub API.
type Binding struct {
    Repo         Repo   // resolved by the App, never from the envelope
    Event        string // "pull_request" | "push"; discovery and baseline uploads also "workflow_dispatch"
    PullRequest  int    // 0 unless pull_request
    HeadSHA      string
    BaseRef      string // PR base branch; for other events the branch the run executed on
    BaseSHA      string
    MergeBaseSHA string
    Fork         bool
    RunID        int64
    RunAttempt   int
    WorkflowPath string
}

// Problem is an eligibility defect found after the tenant was authenticated.
// Problems never reject the request; they force the gate to action_required.
type Problem string

const (
    ProblemEnvelopeMismatch Problem = "envelope_mismatch" // envelope disagrees with authoritative data
    ProblemUnsupportedEvent Problem = "unsupported_event" // e.g. workflow_run
    ProblemFork             Problem = "fork"
    ProblemReportInvalid    Problem = "report_invalid" // payload failed no-source decoding
)

type Upload struct {
    Binding  Binding
    Identity Identity // claimed engine/image identity from the envelope
    Digest   string   // payload digest, verified against the envelope
    BaselineDigest string // claimed by the envelope; "" = the producer used no baseline
    LabelsDigest   string // claimed by the envelope; "" = the producer used no labels file
    Payload  []byte   // the exact verified bytes; the only content for KindDiscovery and KindBaseline
    Report   *Report  // nil for KindDiscovery, KindBaseline and when ProblemReportInvalid
    Problems []Problem
}
```

### gate/inputs

- **Counterpart**: internal/gate/inputs
- **Direction**: verdict depends on inputs (reads `Trusted`)
- **Strength**: model
- **LCA / Rank / Distance**: gate / 1 / 1
- **Volatility**: high
- **Balanced?**: yes
- **Shared knowledge**: `Trusted` fields `Suspended`, `SettingsFound`, `PolicyDigests`, `BaselineDigests`, `LabelsDigests`,
  `ProtectedPaths`, `Approval`, and `Entitlement.Reason`. `Target` is not used here. `Settings` is an
  opaque part of `Trusted` for this module. Steps 6b to 6d call `Accepts`; `First`, `ProtectedPaths` and
  `MayBeProtected` are not used here.

<!-- contract: Accepts, First — restated from internal/gate/inputs/module.md -->
```go
// Accepts is the digest predicate of the gate: whether claim is acceptable for one digest list of Trusted.
// An empty list accepts only the empty claim ("nothing trusted, nothing claimed"). A list with entries
// accepts only a claim that is not empty and is in the list, by exact string equality.
func Accepts(trusted []string, claim string) bool

// First returns the first digest of a list of Trusted, or "" for an empty list.
// For every list that Resolve builds, Accepts(trusted, First(trusted)) is true.
func First(trusted []string) string
```

<!-- contract: Entitlement, EntitlementKey — restated from internal/gate/inputs/module.md -->
```go
// EntitlementKey is the Store key, under the installation tenant, of the JSON-encoded Entitlement.
const EntitlementKey = "entitlement"

// Entitlement is the cached subscription state the gate reads. control/account writes it;
// the gate never calls billing code. A missing or unreadable record means "not suspended".
type Entitlement struct {
    SuspendAfter time.Time `json:"suspend_after"` // zero = never suspends
    WarnFrom     time.Time `json:"warn_from"`     // zero = no notice; feedback shows Notice from this instant
    Notice       string    `json:"notice"`        // e.g. "Trial ends 2026-10-20"
    Reason       string    `json:"reason"`        // shown when suspended, with the recovery action
}
```

<!-- contract: Target, Trusted, Approval — restated from internal/gate/inputs/module.md -->
```go
// Target is the pull request (or default-branch push) whose trusted inputs are resolved.
type Target struct {
    Repo        Repo
    PullRequest int // 0 for a default-branch push
    HeadSHA     string
    BaseRef     string // protected ref; the default branch for a push
    Fork        bool
}

type Approval string

const (
    ApprovalNotRequired Approval = "not_required" // no protected path changed
    ApprovalGranted     Approval = "granted"      // a base-version owner approved this exact head SHA
    ApprovalMissing     Approval = "missing"
)

// Trusted is everything the gate believes, all read from the protected ref or GitHub itself.
type Trusted struct {
    Settings             Settings
    SettingsFound        bool
    PolicyDigests        []string // base digest, plus the head digest when Approval == ApprovalGranted
    BaselineDigests      []string // same rule; empty = no baseline on the protected ref
    LabelsDigests        []string // same rule; empty = no labels file on the protected ref
    ProtectedPaths       []string // protected paths this PR changes
    Approval             Approval
    ApprovedBy           string
    EarliestWaiverExpiry time.Time // zero = no waivers
    Suspended            bool
    Entitlement          Entitlement
}
```

### wire/report

- **Counterpart**: internal/wire/report
- **Direction**: verdict depends on wire/report
- **Strength**: contract (published language, versioned schema)
- **LCA / Rank / Distance**: root / 2 / 2
- **Volatility**: high (verdict side); the wire contract itself is moderate
- **Balanced?**: yes
- **Shared knowledge**: `Report.Verdict`, `Decision.HardGates`, `Decision.UnevaluatedRequiredRules` (`RuleID`,
  and `Reason` for the dead-selector prefix), `Comparison.ConfigHash`, `Comparison.MeasurementProfile` (nil or not)
  and `GateReference.Status` and `Reasons` decide. For the `attention_only` Detail only, verdict also reads
  `Dimension.Status` of the nine engine keys and `Finding.Kind` and `Finding.Status`; nothing else of the report is
  read. Enum values arrive validated by the decoder; verdict still closes on any other value.
  `AgentTask` is an opaque part of `Report` for this module. `Coverage` is an opaque part of `Report` for this module.
  `Seam` is an opaque part of `Report` for this module. `Edge`, `Endpoint`, `Location`, `DimensionCoverage`,
  `Metric`, `FindingRef`, `UnknownFact`, `Delta`, `MetricDelta` and `ToolCoverage` are restated below only because
  they share a fence with `Finding` or `Dimension`; verdict never reads them.
  `StateSchemaV1` is the schema constant already enforced by the decoder; verdict does not re-check it.

<!-- contract: Report, StateSchemaV1, MaxBytes — restated from internal/wire/report/module.md -->
```go
// StateSchemaV1 is the engine state contract accepted (archfit.state.schema.json, engine v3.0.0 and v3.0.1: one file).
const StateSchemaV1 = "archfit.architecture-state.v1"

// MaxBytes caps an uploaded payload: 5 MiB (the "5 MB" cap of decisions.md).
const MaxBytes = 5 << 20

// Report is the no-source transport form of an engine state report. The decoder knows every key of the engine
// state schema of v3.0.0 and v3.0.1: keys listed here are kept (length-capped), the other known keys are validated and dropped
// (measurement, findings[].matched_by except MatchedByKeys, agent_tasks[].declarations, metric provenance, seam scores), and any key
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
    // MatchedBy holds only the MatchedByKeys of the engine's matched_by: a metric ratchet names its numbers there, and a
    // dependency finding its full location count (the engine lists at most 50 locations).
    MatchedBy map[string]string `json:"matched_by,omitempty"`
}

// The matched_by keys a consumer shows. The engine writes more keys; the others stay out of the App.
const (
    MatchedByMetric    = "metric"
    MatchedByBefore    = "before"
    MatchedByAfter     = "after"
    MatchedByThreshold = "threshold"
    // MatchedByLocationsTotal is the deduplicated location count of a dependency finding, whose Locations the engine
    // caps at 50.
    MatchedByLocationsTotal = "locations_total"
)

var MatchedByKeys = []string{MatchedByMetric, MatchedByBefore, MatchedByAfter, MatchedByThreshold, MatchedByLocationsTotal}

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
    // Hypothesis is the engine's balancing move for the seam: "balanced" | "accept_low_volatility" |
    // "introduce_contract" | "move_functionality" | "declare_volatility" | "expected_by_role" | "follow_rule".
    // "" = the engine gave no move.
    Hypothesis string `json:"hypothesis,omitempty"`
}
```

### Consumers (inbound)

- **internal/gate** (parent): functional, gate / 1 / 1, high, balanced. Builds `Facts`, calls `Decide`. The fork refusal reaches `Without` through `sweep.Without`.
- **internal/gate/feedback**: model, gate / 1 / 1, high, balanced. Renders `Outcome`; `Receipt` stores `Conclusion` and `ReasonCode`.
- **internal/gate/sweep**: contract, gate / 1 / 1, high, balanced. Calls `Without`, `Suspended`, `ApprovalMissing` and `Unapproved`; compares receipt `ReasonCode` and `Conclusion` values.

## Change Vectors

- A new reason code, category, or recovery text.
- A changed decision order or engine mapping after a pilot.
- A new engine decision field: read it here; the wire type changes in `wire/report`.
- The independent-observation outcome of trust §5, if that feature ever exists.

## Constraints and Invariants

- Pure: no I/O, no clock, no logging, no globals, no goroutines. Time facts arrive inside `Trusted`.
  Imports: standard library, `wire/report`, `gate/ingest`, `gate/inputs` only. Never `control`, `app`, LLM or UI packages.
- Deterministic: equal `Facts` give equal `Outcome`, including reason order. `Facts` is never mutated.
- Fail closed: every unrecognized value ends `action_required`. `Success` needs all of: not suspended,
  settings found, no problems, a report, approved workflow and identity, matching digests, approval not
  missing, comparable reference, complete coverage, and a passing engine decision.
- Never `neutral` or `skipped`. Never the word "verified" in any text; OIDC provenance is necessary, not sufficient.
- Never trust head-tree data: verdict reads only `Trusted` for policy, baseline, owners and settings facts.
- Never filter by task `origin` or finding `status`; erosion-only adoption is the baseline's job inside the engine.
- Suspension is category `subscription`: never an architecture failure, never a success.
- Declared restatement: the `Next` texts of `workflow_not_approved` and `settings_missing` hold the literal
  `.archfit-app.yaml`. The owner is `wire/settings` (`Path`). verdict does not import `wire/settings`, so that its
  import set stays the three packages above; `TestReasonCatalogComplete` compares the texts with `settings.Path`,
  so a renamed settings file fails that test instead of misleading a reader. Remove the literal if verdict ever
  imports `wire/settings` for another reason.
- Declared restatement: the nine engine dimension keys and their order, read for the `attention_only` Detail, are a
  literal list in `decide.go`, like the one in `gate/feedback`. The owner is `wire/report`, whose decoder accepts
  exactly these keys and exports no list of them. If the lists drift, a new engine dimension is not named in the
  Detail; the conclusion does not change. Trigger to change: a new engine dimension; remove the list when
  `wire/report` exports the keys in order.
- Declared restatement: the prefix `selector matches nothing` is the engine's dead-selector reason (engine Wave 0.3,
  the form `selector matches nothing: <side> <glob>`). The engine owns the text and publishes no code for it. If the
  engine changes it, a dead selector reads as missing evidence: still `action_required`, with the evidence recovery
  text. Trigger to change: an engine change of the reason or a new structured code.
- `Detail` holds IDs and digests only, so an `Outcome` is safe to log.
- No panic on any input, including nil `Report`, nil slices and nil pointers inside `Comparison`.

## Test Specification

### Unit Tests

- **Test name**: `TestDecideOrder`
  **Scenario**: table; each row starts from the healthy fixture facts and turns on two conditions from different steps (suspended + blocked, settings missing + fork, approval missing + policy mismatch, approval missing + baseline mismatch, baseline mismatch + labels mismatch, policy mismatch + not comparable, out-of-enum verdict + unmeasured hard gates, missing coverage + blocker).
  **Expected behavior**: the reason of the earlier step is `Reasons[0]` and sets `Conclusion`; the later reason is still present. In particular `owner_approval_missing` precedes both mismatch reasons, and `engine_error` precedes `missing_required_coverage`.
- **Test name**: `TestReasonCodesAreUnique`
  **Scenario**: `verdict: blocked` with `hard_gates: pass` and an unevaluated rule; `verdict: blocked` with `unmeasured`; fuzzed facts.
  **Expected behavior**: `[missing_required_coverage, blocking_findings]` for the first two; no `ReasonCode` ever appears twice in `Reasons`.
- **Test name**: `TestEngineDecisionMatrix`
  **Scenario**: every row of the engine decision matrix, plus `Verdict` `""`, `"BLOCKED"` and `"error"`, and `HardGates` `""` and `"PASS"`; 20, 21 and 25 unevaluated rules, once missing evidence and once dead selectors.
  **Expected behavior**: the listed reasons in the listed order; unknown values give `engine_error`. The `missing_required_coverage` and `policy_defect` Detail names 20 rule IDs, then `and 1 more` or `and 5 more`.
- **Test name**: `TestAttentionOnly`
  **Scenario**: `needs_attention` with `pass` and: two dimensions not measured (`drift` unmeasured, `testability` partial); all measured and advisories with every status plus a gate finding and a finding of an unknown kind; one dimension key missing; an extra map key and a dimension with an unknown status. Each once more with `verdict: healthy`. Then `needs_attention` beside an unevaluated rule, a dead selector, a non-comparable reference, missing approval, suspension.
  **Expected behavior**: `success` with `attention_only` alone, category architecture; Detail `dimensions not fully measured: testability, drift; active diagnostics: 0`, then `none; active diagnostics: 2` (only advisories that are `new` or `expired_waiver` count), then the missing key, then only `coupling` (an extra key is never named); `Next` says advisories never block; no report text in Detail or `Next`. With `verdict: healthy` the reason is `healthy` with an empty Detail. Beside any other reason `attention_only` never appears.
- **Test name**: `TestPolicyDefect`
  **Scenario**: dead-selector rules (`selector matches nothing: …`) alone and beside other unevaluated rules, under `unmeasured`, `pass` and `fail` hard gates, with `verdict: blocked` and with an out-of-enum verdict; unmeasured hard gates without rules; near misses (other case, leading space, `the selector matches nothing`, other reasons, empty).
  **Expected behavior**: `policy_defect` (governance, `action_required`) before `missing_required_coverage`, and after `blocking_findings` when the hard gates fail; each Detail names only its own rules and never the glob; `Next` names the owner-approved policy PR and `guard: true`; unmeasured gates explained only by dead selectors give no `missing_required_coverage`, and without rules they give it alone; every near miss is `missing_required_coverage`.
- **Test name**: `TestDigestPredicate`
  **Scenario**: table over (trusted list empty / one / two digests) × (claimed digest `""` / matching / other), once for `BaselineDigest` and once for `LabelsDigest`; a claimed digest with a `sha256:` prefix.
  **Expected behavior**: `baseline_mismatch` or `labels_mismatch` exactly when `inputs.Accepts` is false for the same pair; both empty is a match; `""` against a non-empty list is a mismatch; the prefixed digest never matches (bare hex only); neither step needs a report.
- **Test name**: `TestComparabilityRule`
  **Scenario**: a trusted baseline exists × `GateReference` nil / `comparable` / `non_comparable` / `not_requested`; no trusted baseline and no claimed baseline with `non_comparable` "no baseline file was loaded"; `Comparison.Status` set to `non_comparable` while the gate reference is comparable.
  **Expected behavior**: `baseline_not_comparable` for nil, `non_comparable` and `not_requested`, with at most three reasons and 200 characters in `Detail`; nothing for `comparable`; the no-baseline case is ignored and can be `success`; `Comparison.Status` changes nothing.
- **Test name**: `TestReferenceConflictsWithTheTrustedBaseline`
  **Scenario**: table of `ReferenceConflicts(ref, baselineTrusted)`: nil reference with and without a trusted baseline; `comparable` with `BaselinePresent` true or false; `non_comparable` with a trusted baseline and without one.
  **Expected behavior**: a conflict for a nil reference with a trusted baseline, for any status other than `comparable` with a trusted baseline, and whenever `BaselinePresent` differs from `baselineTrusted`; no conflict otherwise.
- **Test name**: `TestNextNamesTheOwnerActionFromDrift`
  **Scenario**: a `non_comparable` reference with `Drift` `measurement_profile`, `rubric_version`, both, `classification_hash`, `model_hash`, `labels_hash`, `reference_incomplete`, `measurement_profile` with `classification_hash`, and none.
  **Expected behavior**: the re-anchor `Next` when every class is `measurement_profile` or `rubric_version`; the full-capture `Next` for the others.
- **Test name**: `TestPolicyDigestSet`
  **Scenario**: `ConfigHash` equal to the base digest, to the approved head digest, to neither, and with empty `PolicyDigests`.
  **Expected behavior**: no reason for the first two; `policy_mismatch` for the third; `policy_missing` (and no `policy_mismatch`) for the empty list, also when `Report` is nil.
- **Test name**: `TestPolicyMissingWindow`
  **Scenario**: settings found, `PolicyDigests` empty: a code-only PR (`ApprovalNotRequired`) with a healthy report; the PR that adds the policy before owner approval (`ApprovalMissing`); the same after approval (`PolicyDigests` = head digest).
  **Expected behavior**: `action_required` with `policy_missing` first (category `governance`, `Next` names the policy PR); `owner_approval_missing` first with `policy_missing` second; `success`.
- **Test name**: `TestIdentityReasons`
  **Scenario**: `IdentityApproved` false with profile nil, with profile present, and with `Report` nil.
  **Expected behavior**: `measurement_profile_missing`, `unknown_engine_identity`, and only `report_invalid` respectively.
- **Test name**: `TestReasonCatalogComplete`
  **Scenario**: iterate every `ReasonCode` constant of the contract (twenty-four).
  **Expected behavior**: each has the catalog category and conclusion (`success` for `healthy` and `attention_only`); every code except `healthy` has a non-empty `Next`; the `Next` texts of `workflow_not_approved` and `settings_missing` name `settings.Path`; those of `unknown_engine_identity` and `measurement_profile_missing` name the user-guide section "Move an installed workflow to a new engine" and never the onboarding workflow, which keeps the pin it was written with.
- **Test name**: `TestWithout`
  **Scenario**: the six allowed codes, then `ReasonHealthy` and an unknown code.
  **Expected behavior**: `ActionRequired` with one reason each; the last two give `engine_error`.
- **Test name**: `TestSuspended`
  **Scenario**: an entitlement with a `Reason`; the zero entitlement; `Decide` for suspended facts with the same two records.
  **Expected behavior**: `ActionRequired`, one reason `subscription_suspended` in category `subscription`; `Next` is the record's `Reason`, or the catalog text for the zero record; `Reasons[0]` equals the one `Decide` gives.
- **Test name**: `TestApprovalMissing`
  **Scenario**: trusted inputs with a missing approval and one, two or no protected paths; `Decide` for the same inputs.
  **Expected behavior**: `ActionRequired`, one reason `owner_approval_missing`; `Reasons[0]` equals the one `Decide` gives, `Detail` included.
- **Test name**: `TestUnapproved`
  **Scenario**: `Approval` `not_required`, `granted`, `missing`, empty, and an unknown value; `Decide` for each.
  **Expected behavior**: true for the last three only, and `Decide` names `owner_approval_missing` exactly when it is true.

### Integration Contract Tests

- **Test name**: `TestDecideWireFixtures`
  **Scenario**: every report fixture of the shared corpus `internal/wire/testdata/report` (names and `.expect.json` format per `internal/wire/module.md`; verdict keeps no copy). A fixture with `decode: "ok"` is decoded with `report.Decode` as a document of its producer's release (synthetic: v3.0.0); one with `decode: "error"` gives a nil `Report`. The facts are the clean facts of that document: identity and workflow approved, settings found, no approval needed, not suspended, no problems, trusted policy digests = the report's `config_hash`, trusted baseline and labels digests = `policy.Digest` of the `.baseline.json` and `.labels.yaml` siblings (an empty list when there is no sibling), and the same digests as the envelope's claims. The fixture's `facts` object then overrides single facts; an unknown key in `facts` or in the expectation fails the test. The corpus must hold the required fixtures: `healthy`, `attention-pass`, `blocked`, `missing-evidence`, `unknown-identity`, `profile-comparable`, `profile-incompatible`, `invalid-input`, `blocker-and-unevaluated`, `non-applicable-rule`, `locations-empty`, `matched-by-populated`, `repair-kinds`, `waiver-expiry-order`.
  **Expected behavior**: `Decide` returns exactly `gate.conclusion` and the code sequence `gate.reasons` of each fixture. For the named fixtures the document also fixes the first reason: `success`/`healthy` for `healthy` and `non-applicable-rule`; `success`/`attention_only` for `attention-pass`; `failure`/`blocking_findings` for `blocked`; `action_required`/`missing_required_coverage` for `missing-evidence`; `action_required`/`measurement_profile_missing` for `unknown-identity`; `action_required`/`baseline_not_comparable` for `profile-incompatible`; `action_required`/`report_invalid` for `invalid-input`; `failure` with `missing_required_coverage` second for `blocker-and-unevaluated`.
- **Test name**: `TestReasonsZeroDeterminesConclusion`
  **Scenario**: all outcomes produced by the other tests.
  **Expected behavior**: `Conclusion` equals the catalog conclusion of `Reasons[0].Code`; `Reasons` is never empty.
- **Test name**: `TestInvalidInputFixture`
  **Scenario**: facts as the gate builds them for the invalid-input fixture: `Problems` = `report_invalid`, `Report` nil.
  **Expected behavior**: `action_required`, `report_invalid`, category `evidence`.

### Boundary Tests

- **Test name**: `TestConclusionSetIsClosed`
  **Scenario**: fuzz `Facts`: random problems (including unknown strings), nil and random reports, random booleans, random digests and approvals; also every `Without` input.
  **Expected behavior**: `Conclusion` is always one of the three constants; never `neutral`, `skipped`, or empty; no panic.
- **Test name**: `TestSuccessRequiresEverything`
  **Scenario**: start from the healthy facts; turn off one precondition at a time (suspended, settings missing, each problem, workflow, identity, policy digest, baseline digest, labels digest, approval missing, not comparable, unmeasured, unevaluated rule, dead-selector rule, `verdict: blocked`, out-of-enum verdict, nil report).
  **Expected behavior**: no variant is `Success`; the fuzz test asserts the same implication in reverse for every `Success` it sees.
- **Test name**: `TestDecideIsPure`
  **Scenario**: call `Decide` twice on deep-copied facts; inspect the package import list.
  **Expected behavior**: equal outcomes; inputs unchanged; no import of `time.Now` users, `net/http`, `os`, `log/slog`, or any internal package outside the allowed three.
- **Test name**: `TestDetailCarriesOnlyIdentifiers`
  **Scenario**: reports whose findings, messages and module names hold marker strings.
  **Expected behavior**: no marker string in any `Detail` or `Next`; no text contains "verified" or a score.

### Behavior Tests

- **Test name**: `TestUnknownIdentityNeverComparesEqual` (T01)
  **Scenario**: a report without a measurement profile whose reference also lacks one; a profile-less baseline on the protected ref (`gate_reference.status: non_comparable`); a stale numerical snapshot baseline (`non_comparable`).
  **Expected behavior**: `measurement_profile_missing` for the first; `baseline_not_comparable` for the others; none is `success`.
- **Test name**: `TestHeadEditsCannotWeakenTheGate` (T12)
  **Scenario**: report computed with a head-edited policy, baseline or labels file (the envelope states the head digests), approval missing; then the same with a clean engine decision; then a head-computed report while `inputs` saw no protected path change (`ApprovalNotRequired`).
  **Expected behavior**: `owner_approval_missing` first with `policy_mismatch`, `baseline_mismatch` or `labels_mismatch` after it; a healthy engine decision never turns it into `success`; the third case has the mismatch as `Reasons[0]`; an upload that claims no baseline (`""`) while a baseline exists is `baseline_mismatch`, and a missing or non-comparable gate reference is `baseline_not_comparable` (no blanket re-baseline).
- **Test name**: `TestAtomicPolicyPullRequestWaitsForOwner` (T14)
  **Scenario**: the engine ran with the head policy; first `Approval` missing and `PolicyDigests` = base only; then `Approval` granted and `PolicyDigests` = base and head.
  **Expected behavior**: first `action_required` with `Reasons` = `owner_approval_missing`, `policy_mismatch` (so the receipt reason is `owner_approval_missing` and a review can trigger the re-run); then `success`.
- **Test name**: `TestIntegrityFailureIsNotClearedByApproval` (T14)
  **Scenario**: `Approval` granted, but `ConfigHash` is in neither digest of `PolicyDigests`.
  **Expected behavior**: `policy_mismatch`, `action_required`.
- **Test name**: `TestMissingEvidenceNeverPasses` (T13)
  **Scenario**: unmeasured hard gates, unevaluated required rule with `verdict: needs_attention`, out-of-enum verdict, nil report, fork, unsupported event (`workflow_run`), every `Without` code.
  **Expected behavior**: `action_required` each time with its own reason.
- **Test name**: `TestNonComparableBaselineNeedsOwnerPR` (T15)
  **Scenario**: a model or profile change makes the gate reference `non_comparable` while `verdict` is `healthy` and a baseline exists on the protected ref.
  **Expected behavior**: `action_required`, `baseline_not_comparable`; `Next` names the owner-approved baseline PR.
- **Test name**: `TestBlockerKeepsUnevaluatedRules` (T02)
  **Scenario**: the blocker-plus-unevaluated fixture.
  **Expected behavior**: `failure`; reasons are `blocking_findings` then `missing_required_coverage` with the rule ID in `Detail`.
- **Test name**: `TestOriginNeverExempts`
  **Scenario**: blocked report whose only task has origin `unknown` (`bc/coupling_gate`) and whose findings all have status `baseline`.
  **Expected behavior**: `failure`; verdict follows `hard_gates`, not origin.
- **Test name**: `TestSuspensionIsItsOwnCategory` (T21)
  **Scenario**: suspended with a `healthy` report; suspended with a `blocked` report; not suspended with a zero `Entitlement` (Store outage).
  **Expected behavior**: first two are `action_required`/`subscription` with `Entitlement.Reason` as `Next`, the architecture reason kept second; the third decides normally.

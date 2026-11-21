# Onboarding

**Path**: internal/control/onboarding/
**Parent**: internal/control/ (control plane façade)
**Submodules**: none (leaf)

## Archfit 4 setup

`NewSetup(*api.Client, gate.SetupViews) Setups` provides `Setup(ctx, repo)`, a read-only view of admission,
first approval and ruleset guidance. It reads the default branch's head once, then both files at that immutable
commit: a valid `.archfit.yaml` (version 1) and `.github/workflows/archfit.yaml`. No settings file is required; no files
or records are generated. `wire/policy3` owns policy parsing/digests and `wire/workflow` owns extraction of
job/step Action calls.

An Archfit Action call names `manifest.ActionRepository`; a tag is read in that repository.
Every Archfit Action call must use a commit in manifest 4 or a public tag resolving to one. Tag reads use no
installation credential, never fall back to a branch, and fail closed on missing/unreadable/unlisted targets.
An unlisted full commit SHA is rejected without tag lookup: GitHub prefers a commit over a same-named tag.

`Setup.State` is `not_admitted`, `first_approval`, `approved`, or `outside_flow`. A policy without an approved
digest remains `first_approval`, not approved, even when the workflow is missing (admission stays false). A same-repository open PR into the default branch can supply
the policy and workflow for the first approval only when no valid policy is on the default branch; `FirstPull`
identifies it. Setup also reports whether the policy and accepted workflow are present on the default branch for the Home stepper. A default branch that already has a valid policy uses `FirstPull=0`, regardless of its workflow. The gate owns recorded state through `SetupViews`; the first-approval transactions in
`gate/records.First` and `ApproveCurrent` own the `v4/repos` entry. Setup never adds the entry itself.

`Setup.Approval` separates an exact-head signature from activation: `needed`,
`approved_pending_merge`, `active`, `stale`, or `unknown`. The gate read side shares Review's
head/policy/workflow digest predicate. It also reads outstanding proposals after a PR leaves the open
list, so an unprocessed merge stays pending rather than prompting another signature. Read failures
stay unknown. Only matching approved policy and accepted workflow on the default branch, with no
outstanding approval, become active. This summary never changes approval or gate decisions.

Ruleset guidance is separate from admission. It reads effective branch rules and inherited rulesets, reports
pull-request enforcement, warns on required GitHub reviews, and requires `archfit/gate` with this App's
`integration_id`. It warns when `current_user_can_bypass` allows the App to bypass a branch ruleset. A policy
push restriction is applicable only to organization-owned private/internal repositories and requires the App to
always bypass it so approved policy proposals can be written. Administrator emergency bypass must cover every
applicable branch ruleset; a bypass on only one does not cover the others. Unreadable bypass actors remain unknown:
an administrator bypass, **if the ruleset lists one**, is the emergency path, not a verified or named bypass.

Tests: `setup_test.go` covers admission, immutable reads, tag failure/commit precedence and approval states;
`rulesets_test.go` covers each requirement, bypass visibility and supported repository types.

## Removed flows

The setup/ruleset read side is the only onboarding service. Customer/managed discovery,
Progress stages, generated settings/workflows/CODEOWNERS, baseline capture, reanchor and the Sandbox
seam are removed. Setup never writes files or approvals. The owner action path remains in the gate
and control/answers; setup_binding_test.go checks that both interpret first-approval inputs consistently.

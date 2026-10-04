# Archfit App — first-version decisions

Status: owner brainstorm of 20 September 2026. This file closes the open items in
[product design](product.md) §12 and [trust and reporting](trust-and-reporting.md)
so that module design can start. Where it conflicts with those documents on
_how_ something is built, this file wins; their scope and trust invariants stand.

Rows marked **[default]** were chosen without an owner answer. Strike or change
them; everything else follows from the principle below.

## Principle: simple, reliable, predictable

GitHub is the database. The protected ref is the only source of truth for
anything a customer approves. The App is one small Go service that verifies
and reports; it stores almost nothing, so it has almost nothing to lose, leak,
migrate, or reconcile. When a choice is open, take the option with fewer moving
parts and behavior a GitHub user already expects.

## 1. Scope

| Decision                    | Answer                                                                           |
| --------------------------- | -------------------------------------------------------------------------------- |
| First implementable version | The whole Required list in product.md §2. Its Deferred list is unchanged.        |
| Existing closed-beta plan   | Ignored as task order. Its Accept criteria remain a source of test requirements. |

## 2. System shape

| Decision                                | Answer                                                                                                                                                                                       | Why                                                                  |
| --------------------------------------- | -------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------- | -------------------------------------------------------------------- |
| Deployment units                        | One Go binary, one container, any container host. Reporter and control plane are code modules, not separate services.                                                                        | One thing to deploy, monitor, and reason about.                      |
| Gate independence from LLM, billing, UI | Enforced by package dependencies and timeouts: gate-path packages never import or call those modules. Checked by Archfit on this repository.                                                 | Same guarantee as a separate deployment, without one.                |
| Customer-approved state                 | Files on the protected ref: engine policy (with waivers), baseline, and one App settings file.                                                                                               | Git already gives review, history, and rollback.                     |
| App settings file                       | Separate file beside the policy (owners, gate mode, workflow path, data mode). The engine policy schema stays untouched.                                                                     | The engine parses its config strictly; App fields must not break it. |
| Workflow state                          | GitHub itself: reviews, check runs, PR comments, workflow runs, installations.                                                                                                               | No second copy to drift.                                             |
| Own state                               | A small key-value store behind one `Store` interface: entitlements, consent records, LLM budget counters. Keys are prefixed by authenticated installation ID.                                | Every platform has a KV; nothing relational is needed.               |
| Reports and source                      | Never persisted. A report is verified, turned into a check and a comment, and dropped.                                                                                                       | Removes report encryption, retention, and deletion work.             |
| Queues, outbox, job tables              | None. Webhook and upload handlers do their work in the request and are idempotent.                                                                                                           | Fewer failure modes than a delivery pipeline.                        |
| Recovery from missed events             | One periodic sweep re-derives the expected state of open PRs from GitHub and repairs differences. Any external scheduler calls an authenticated endpoint; hourly **[default]**.              | Reconciliation is the single recovery mechanism.                     |
| Idempotency and replay                  | Check run `external_id` carries run ID, attempt, and report digest; the PR comment carries a hidden marker. A duplicate upload updates in place; an older run never overwrites a newer head. | GitHub holds the dedup key, so restarts lose nothing.                |
| Hosting                                 | Any container host plus any KV, within USD 100/month. Chosen in the tech-stack step. Cloud Run with PostgreSQL is no longer the selected target.                                             | Owner: do not bind to a provider.                                    |

## 3. Contracts we define as the consumer

| Decision                        | Answer                                                                                                                                                                                                           | Why                                                                 |
| ------------------------------- | ---------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------- | ------------------------------------------------------------------- |
| Envelope schema                 | Owned and versioned in this repository as JSON Schema; the Action conforms. Unknown fields are rejected.                                                                                                         | The verifier decides what it accepts.                               |
| Upload                          | One authenticated POST: envelope plus engine report bytes, GitHub Actions OIDC token, audience is the App URL. Size cap 5 MB **[default]**. The Action retries on non-2xx.                                       | One request, one answer.                                            |
| Supported triggers              | `pull_request` on same-repository branches, and `push` to the default branch. `workflow_run` is unsupported in v1.                                                                                               | Removes ambiguous PR association entirely.                          |
| Revision binding                | The App resolves repository, PR, head, base, and merge-base from the GitHub API using the run identity in the OIDC claims. Envelope fields must match; they are never trusted alone.                             | Self-reported identity proves nothing.                              |
| Trusted inputs                  | Verified by digest, not issued. The Action runs with the base-ref policy and baseline; the App reads the same files from the protected ref and rejects a report whose `config_hash` or `gate_reference` differs. | Same invariant as issued snapshots, no issuance service.            |
| Owner-approved candidate policy | If the PR changes policy and a base-version owner approved the exact head SHA, the head policy digest is also accepted for that PR only.                                                                         | Keeps atomic code-and-policy PRs.                                   |
| Approved workflow               | The OIDC workflow ref must equal the path in App settings, and that file must be unchanged between base and head unless owner-approved.                                                                          | A head-authored workflow cannot mint eligibility.                   |
| Forks                           | Unsupported for the gate in v1: explicit `action_required` with the reason. Recovery is a same-repository branch or the ruleset bypass.                                                                          | No safe fork path without isolated compute.                         |
| Engine and image identity       | v2.3.1 pinned. Approved versions, image digests, and profiles are a locked manifest shipped in the binary. Unknown identity yields `action_required`.                                                            | Unknown never compares as equal.                                    |
| No-source transport             | Strict allowlist schema: IDs, rule IDs, module names, file paths, line numbers, metrics, engine-authored messages. No snippets, logs, prompts, or raw model output. Seeded leakage fixtures are release tests.   | Paths and module names are disclosed metadata; nothing else leaves. |

## 4. Governance

| Decision                   | Answer                                                                                                                                                                                     | Why                                                       |
| -------------------------- | ------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------ | --------------------------------------------------------- |
| Architecture owners        | Explicit list of GitHub logins or teams in App settings on the protected ref.                                                                                                              | One visible list, versioned with the policy.              |
| Approval check             | A PR touching policy, baseline, App settings, or the workflow file needs an approving review from a base-version owner on the current head SHA. The gate verifies it and explains a block. | The App can say why; a bare ruleset cannot.               |
| GitHub-native layer        | The onboarding PR also adds CODEOWNERS entries for those paths and documents the ruleset, so GitHub requests the review automatically.                                                     | Expected GitHub behavior; the gate remains the authority. |
| Bootstrap                  | The first owner list arrives in the onboarding PR, merged by a repository administrator.                                                                                                   | No separate role UI.                                      |
| Re-push                    | Invalidates approval. GitHub dismisses the stale review; the gate re-checks the head SHA.                                                                                                  | Already how GitHub works.                                 |
| Exceptions                 | Engine waivers in the policy file, published through the same PR path with owner approval. The engine enforces expiry.                                                                     | Already specified by the waiver contract.                 |
| Expiry handling            | The sweep warns on the PR 7 days before expiry **[default]**. After expiry it re-runs the workflow for affected open PRs; until the new report arrives the gate is `action_required`.      | The engine re-evaluates; the App stores no report.        |
| Base policy change         | Same mechanism: open PRs evaluated against an older policy digest are re-run. The check output records the policy digest, baseline digest, and earliest waiver expiry it used.             | The check is its own receipt.                             |
| Baseline                   | File on the protected ref. Changes only through an owner-approved PR. The App never commits it to the protected ref; an upload becomes a PR from `archfit/baseline`.                       | No silent re-baseline is possible.                        |
| Profile or model migration | Non-comparable baseline yields `action_required`. Recovery is an owner-approved baseline PR. No relocation proof in v1; every uncertain case is owner review.                              | Honest and small.                                         |
| Gate enablement            | `gate: advisory` or `gate: required` in App settings, changed by owner-approved PR.                                                                                                        | Explicit owner action with history.                       |
| Missing upload             | The gate check is created `in_progress` on PR open or push. The sweep turns it into `action_required` after 60 minutes **[default]**.                                                      | Fail closed by default, with a reason.                    |
| Emergency bypass           | Archfit in its own ruleset; bypass is a documented GitHub action and is never reported as success.                                                                                         | Unchanged from the design.                                |

## 5. Onboarding

| Decision                | Answer                                                                                                                                                                          | Why                                                                          |
| ----------------------- | ------------------------------------------------------------------------------------------------------------------------------------------------------------------------------- | ---------------------------------------------------------------------------- |
| Customer-executed path  | Two PRs, always. PR 1 adds the workflow and App settings. After merge, discovery runs by `workflow_dispatch`; the App opens PR 2 with the proposed policy.                      | Works under every Actions policy; the owner already accepted the extra step. |
| One-draft-PR flow       | Deferred.                                                                                                                                                                       | An optimization that needs live experiments per organization type.           |
| CLI or existing CI path | Supported through the same upload endpoint.                                                                                                                                     | No extra design.                                                             |
| Managed discovery       | One ephemeral container job behind a `Sandbox` interface: clone with an installation token, run engine discovery, never run repository scripts, return a draft policy, destroy. | The only platform-dependent piece; isolated behind one seam.                 |
| Managed consent         | Recorded in KV per installation before any source processing or LLM call. No consent, no call.                                                                                  | Small, auditable.                                                            |
| Onboarding states       | Derived from GitHub: installed, workflow merged, report received, policy merged, gate mode.                                                                                     | Nothing to store.                                                            |

## 6. UI and editing

| Decision              | Answer                                                                                                                                              | Why                                                          |
| --------------------- | --------------------------------------------------------------------------------------------------------------------------------------------------- | ------------------------------------------------------------ |
| Frontend              | Server-rendered Go templates; Monaco with `monaco-yaml` for the editor. No SPA framework.                                                           | Least code that meets the editor requirement.                |
| Login and access      | GitHub OAuth; every request checks the user's repository permission through the GitHub API with a short cache. Signed-cookie sessions.              | GitHub permissions are the access model.                     |
| Drafts                | A branch and a draft PR. "Draft, awaiting review, published" is the PR state.                                                                       | No draft storage, no version-conflict logic of our own.      |
| Validation            | Schema validation in the browser, instantly. Engine validation is the check result on the draft PR (customer mode) or a sandbox job (managed mode). | Reuses the one validation path that is authoritative anyway. |
| Graph                 | Read-only view of engine output and policy; selecting a module jumps to its YAML. Visual edits deferred.                                            | Removes the YAML round-trip problem.                         |
| Organization overview | Repositories from the GitHub API with latest check states. No score.                                                                                | No aggregation store.                                        |

## 7. AI assistance

| Decision      | Answer                                                                                                                                                                              | Why                                          |
| ------------- | ----------------------------------------------------------------------------------------------------------------------------------------------------------------------------------- | -------------------------------------------- |
| Trigger       | Explicit request only, never on the gate path.                                                                                                                                      | Invariant.                                   |
| Managed mode  | The App calls one provider, Anthropic **[default]**, after consent, under a KV budget counter. Hard cap USD 20/month in total **[default]**.                                        | One integration to secure.                   |
| Customer mode | The request dispatches a workflow in the customer's CI; the proposal returns as a suggestion on the draft PR. Pending, failed, and stale states are the workflow run and PR states. | No agent, no async state machine in the App. |
| Acceptance    | A proposal is always a reviewable diff on the draft PR; nothing is auto-published.                                                                                                  | Same review path as a human edit.            |

## 8. Privacy, tenancy, operations

| Decision            | Answer                                                                                                                 | Why                                                  |
| ------------------- | ---------------------------------------------------------------------------------------------------------------------- | ---------------------------------------------------- |
| Tenant identity     | Installation and repository from the verified webhook, OIDC claims, or OAuth session. Never from a client-supplied ID. | Invariant.                                           |
| Logs and telemetry  | IDs, digests, outcomes, and timings only. No report bodies, paths, or messages.                                        | Keeps the no-source promise outside the payload too. |
| Uninstall           | Delete the installation's KV keys on the webhook. Nothing else exists to clean up.                                     | Follows from storing nothing.                        |
| Report history      | Whatever GitHub keeps: check runs, comments, and workflow artifacts under the customer's retention.                    | A stated limit, not a gap.                           |
| GitHub rate limits  | Conditional requests and bounded backoff. On exhaustion the check stays pending and the sweep retries.                 | Never a false green.                                 |
| Supported languages | What engine v2.3.1 supports: Go, TypeScript, JavaScript, Python, Rust. Pilots start with Go.                           | No claim beyond the engine.                          |
| Targets             | Feedback within 60 seconds of upload at p95; missed events repaired within one sweep interval; no SLA. **[default]**   | Measurable, modest.                                  |

## 9. Commercial

| Decision                                  | Answer                                                                                                                | Why                                           |
| ----------------------------------------- | --------------------------------------------------------------------------------------------------------------------- | --------------------------------------------- |
| Beta entitlements                         | Manual grants in KV. No billing provider in v1.                                                                       | Nothing to integrate until someone pays.      |
| Trial                                     | 30 days from the first private-repository installation, 7 days grace, warnings 7 days and 1 day before. **[default]** | Needed for the state machine; easy to change. |
| Suspension                                | Gate concludes `action_required` with a subscription reason. Never an architecture failure or a success.              | Invariant.                                    |
| Public repositories                       | Free, with the same size and rate limits.                                                                             | Confirmed.                                    |
| Prices, repository bands, paid allowances | Deferred to the commercial launch plan.                                                                               | Needs cost and willingness-to-pay evidence.   |

## 10. What this design costs

Stated so nobody is surprised later:

- Re-evaluation re-runs the customer's workflow. It needs the `actions: write`
  permission and spends their CI minutes.
- The organization overview and the sweep are bounded by GitHub API rate limits.
- Report history is only as long as GitHub retains it.
- Engine validation in customer mode is as fast as their CI, not instant.
- Managed discovery still needs a real isolated sandbox on the chosen platform.

## 11. Superseded statements

- Separate reporter and control-plane deployments (product.md §8, trust §7).
- Cloud Run with PostgreSQL as the selected target (product.md §8, §9).
- Stored reports, tenant-bound report encryption, and key lifecycle (trust §7).
- App-issued exact-byte input snapshots; replaced by digest verification (trust §1).
- Jobs, outbox, and durable work delivery (plan T08).
- `workflow_run` support and the one-draft-PR experiment (plan T03, T06).

## 12. Still needs the real world

Design cannot close these; they become plan tasks:

- Register a test GitHub App and confirm the OIDC claims and permissions assumed above.
- Confirm on Pumba that the Action can run with base-ref policy and baseline.
- Measure sweep cost against rate limits on a realistic number of open PRs.
- Choose the container host, KV, and sandbox when writing `docs/tech-stack.md`.

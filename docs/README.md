# Design and implementation guide

The App is a reporter skeleton, not the product described below. It checks basic
request shape, then returns 501 for valid-shaped reports because provenance
verification is absent. Malformed requests return 400. The executable and
[`TestReportIngestRefusesEveryReport`](../cmd/reporter/main_test.go) define current
behavior; design documents describe the target.

## Reading order and authority

| Document | Owns | Status |
| --- | --- | --- |
| [Product design](design/product.md) | First-version scope, user flows, governance, privacy, commercial decisions, open choices | Owner-interview design; unresolved proposals remain explicit |
| [Trust and reporting](design/trust-and-reporting.md) | Trusted inputs, measurement, producer eligibility, gate outcomes, technical boundaries | Retained technical requirements, aligned with product scope |
| [Closed-beta plan](plans/closed-beta.md) | M0–M5 milestones, T01–T23 dependencies, acceptance checks, owner inputs | Proposed execution plan; no implementation task marked complete |
| [Hosting research](research/20260918-hosting.md) | Provider comparisons, cost assumptions, bounded validation experiment | Dated research; Cloud Run/PostgreSQL selected for validation only |

Read product scope and technical constraints before choosing a plan task. The
plan sequences the design; it does not expand it. Research informs decisions;
it is neither implementation evidence nor permission to provision resources.

The plan's sibling-repository observations are pinned to its recorded revisions.
Recheck artifacts, contracts, provider features, and prices before implementation
or live experiments. Do not turn a planning snapshot into a claim about current
upstream releases.

## Cleanup of the earlier proposal

The former `docs/plans/20260916-github-app.md` mixed technical design, product
scope, research findings, and an older phase plan. Its retained requirements now
live in `design/trust-and-reporting.md`; the later product design and closed-beta
plan replace its product and execution sections. The original remains in Git
history, not as a competing active plan.

Removed superseded claims include free private advisory use, an organization
policy floor in the first version, managed-only onboarding, the missing state
schema, and assumed pilot multi-region/latency commitments. The old co-located
analysis/OIDC workflow example is not an approved execution recipe. Eligible
producer isolation remains T03/T10 work.

Use M0–M5 and T01–T23 for execution, not the former phases 0–5. Independent
measurement, organization floors, and agent channels remain deferred; their
technical constraints are retained in the technical design. No product decision,
release approval, or completed task is implied by this reorganization.

## Maintenance

- Keep product decisions in the product design and technical contracts in the
  technical design; link to them from tasks instead of copying whole sections.
- Record task completion with acceptance evidence in the plan. Keep stable task
  IDs so later reviews can cite them.
- Keep research dated and separate from deployment decisions. Replace estimates
  with measurements only when an authorized experiment supplies them.
- Update local links when moving documents. Do not retain obsolete plans as
  equally authoritative alternatives; use Git history for superseded text.

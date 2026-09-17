# archfit-app

The archfit GitHub App. Private, proprietary. Go service.

**Read `docs/plans/20260916-github-app.md` first.** It is the design authority:
seven decisions, the corrections peer review forced on them, and an explicit
record of what the design does not deliver. Do not re-litigate a decision
recorded there without new evidence; do not implement past a phase boundary
without the owner.

## Three repositories, one product

| Repo | Owns |
| --- | --- |
| [`archfit`](https://github.com/alexei-led/archfit) | engine, CLI, images, wire contracts (Apache-2.0) |
| [`archfit-action`](https://github.com/alexei-led/archfit-action) | execution/upload adapter (Apache-2.0) |
| `archfit-app` (here) | tenancy, approved policy, baselines, PR feedback |

The boundary is a **tenant-bearing service** versus an **analyzer the customer
executes**, with a narrow protocol between them. It is NOT "the self-model gate
would be annoying" — that reasoning was rejected in review.

## Invariants — these are the product

- **Gate inputs are trusted-source only, and the required check fails closed.**
  Never evaluate the head tree's `.archfit.yaml`, labels or baseline. The App
  resolves policy from a protected ref, issues its digest, and rejects a report
  whose `comparison.config_hash` does not match.
- **Provenance is necessary and insufficient.** OIDC proves which workflow
  definition ran; it does not prove the report is true, because a trusted
  workflow can faithfully run a pull request's install script that overwrites
  the report. Fail-closed catches missing evidence; fabricated evidence looks
  complete. Never print a "verified" badge over an attested report.
- **Provenance per fact, not one global trust tier.** Measurement trust ("could
  target code influence this fact?") and evidence completeness ("was it
  observed?") are independent axes. The engine already models nine dimensions
  this way; mirror it.
- **`neutral` and `skipped` do not block a required check; `action_required`
  does.** So an unverifiable report maps to failure or `action_required`, never
  to neutral — otherwise "break the upload" is the cheapest bypass. Always post
  a conclusion.
- **Erosion-only adoption is the baseline's job, never origin filtering.** The
  synthetic `bc/coupling_gate` task's origin is ALWAYS `unknown`, so filtering
  `origin == "introduced"` would make the whole coupling gate unenforceable.
- **Separate approval authority, not separate PRs.** Code and policy may land
  atomically when an architecture owner's approval is bound to the exact head
  SHA and policy snapshot. A re-push re-arms it.
- **One repository, one gate.** `--root` IS the evidence boundary (all
  extractors walk it), and seams and clone analysis are repository-wide, so
  per-scope checks would go blind to cross-subtree coupling. Team addressability
  comes from routing findings by owner inside one check.
- **LLM output is never on the gate path.** Drafts, narrative and explanations
  only, with generation provenance retained beside human approval.
- **No repository score anywhere**, dashboard included.

## Current state

The reporter's ingest endpoint refuses every report with 501, on purpose:
provenance verification is not implemented, and a report accepted without it has
no nameable producer. `TestReportIngestRefusesEveryReport` pins that. When you
implement verification, that test changes deliberately and the change is the
reviewable event.

## Conventions

- Go, stdlib-first. `net/http` with `ServeMux` patterns; no framework.
- The reporter depends on nothing else — not billing, not dashboards, not LLMs.
  It is in the merge path of every gated repository.
- Contracts it consumes are published by the engine and versioned:
  `archfit.architecture-state.v1` (`archfit.state.schema.json`) and
  `archfit.report-envelope.v1` (archfit-action). Generate transport types from
  the schema; keep domain and storage types separate.
- Secrets: file paths, never inline PEMs. See `.env.example`.
- `make all` before pushing.

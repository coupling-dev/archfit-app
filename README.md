# archfit-app

The archfit GitHub App: approved architecture policy, stored baselines, the PR
gate, and the feedback surface humans and coding agents read.

Private and proprietary. See [LICENSE](LICENSE).

## What it is

archfit answers "does this change keep the architecture healthy" from
dependency facts. The CLI answers it for whoever runs it; this service answers
it for an organisation — which means it owns the parts a pull request must not
be able to edit: the policy, the baseline, and the verdict.

**Routine PR analysis runs in the customer's CI.** archfit's facts come from `go list`,
dependency-cruiser, grimp, `cargo metadata`, ast-grep and jscpd, so measuring a
repository needs its toolchain and its private-dependency credentials. The
analysis runs in the customer's CI, inside the published image, and
[`archfit-action`](https://github.com/alexei-led/archfit-action) carries the
result here.

Three repositories, one product:

| Repo                                                             | Owns                                    | License     |
| ---------------------------------------------------------------- | --------------------------------------- | ----------- |
| [`archfit`](https://github.com/alexei-led/archfit)               | engine, CLI, images, wire contracts     | Apache-2.0  |
| [`archfit-action`](https://github.com/alexei-led/archfit-action) | execution/upload adapter                | Apache-2.0  |
| `archfit-app` (this one)                                         | tenancy, policy, baselines, PR feedback | proprietary |

## The promise, worded exactly

_archfit enforces your approved policy over disclosed evidence._

Never "your architecture is correct", and never "human-reviewed means
architecturally sound". No interface can prove a reviewer paid attention.

## Status

Engine prerequisites are partly implemented: `archfit.state.schema.json` exists
and reports include `measurement.tool_versions`. The inspected engine still
lacks the complete measurement-profile identity in comparison and persisted
baselines, so the original Phase 0 requirement is not yet satisfied. The
[closed-beta implementation plan](docs/plans/20260918-closed-beta-implementation.md)
records the inspected revisions, integration gaps, dependencies, and exit checks.

This repository holds the designs, implementation plan, and a reporter skeleton.
The ingest endpoint
**refuses every report**, deliberately: provenance verification is not
implemented, and a report accepted without it is a report whose producer cannot
be named. Accepting one now would make the first thing we ship the one thing the
design forbids.

Read [`docs/plans/20260916-github-app.md`](docs/plans/20260916-github-app.md)
before writing code here. It records seven decisions, the corrections that peer
review forced on them, and — explicitly — what the design does **not** deliver.

The [high-level product and service design](docs/design/20260918-high-level-design.md)
records the subsequent owner interview, first-version scope, onboarding modes,
governance, subscription decisions, and open research questions. It supplements
the original trust model and is not an implementation plan.

The [hosting research](docs/design/20260918-hosting-research.md) compares pilot
options against the $100/month infrastructure budget. Its cost models and
recommended experiment are not a deployment decision or measured SLA.

## Layout

```
cmd/reporter/      the report-ingest service: verify provenance, post the check
docs/plans/        the design plan and its phases
docs/design/       decisions that outlive a phase
```

The reporter is deliberately the smallest deployable piece and depends on
nothing else. It sits in the merge path of every repository that enables the
gate, so a failure in billing, dashboards or an LLM feature must not be able to
take it down.

## Develop

```sh
make test     # race tests
make lint     # golangci-lint
make build    # static binary → .bin/reporter
make run      # serve locally on :8080
```

No credentials are needed for any of that. Copy `.env.example` to `.env` when
you reach the first real deploy; the placeholders there are the inputs only the
owner can supply, and the plan's _Owner-supplied inputs_ section says which
phase each one blocks.

```sh
curl -s localhost:8080/healthz
curl -si -X POST localhost:8080/v1/reports \
  -H 'X-Archfit-Envelope: {"schema_version":"archfit.report-envelope.v1"}' \
  -H 'Content-Type: application/json' \
  -d '{"schema_version":"archfit.architecture-state.v1","verdict":"blocked"}'
```

The second call returns 501. That is the current contract, not a bug.

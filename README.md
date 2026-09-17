# archfit-app

The archfit GitHub App: approved architecture policy, stored baselines, the PR
gate, and the feedback surface humans and coding agents read.

Private and proprietary. See [LICENSE](LICENSE).

## What it is

archfit answers "does this change keep the architecture healthy" from
dependency facts. The CLI answers it for whoever runs it; this service answers
it for an organisation — which means it owns the parts a pull request must not
be able to edit: the policy, the baseline, and the verdict.

**Analysis does not run here.** archfit's facts come from `go list`,
dependency-cruiser, grimp, `cargo metadata`, ast-grep and jscpd, so measuring a
repository needs its toolchain and its private-dependency credentials. The
analysis runs in the customer's CI, inside the published image, and
[`archfit-action`](https://github.com/alexei-led/archfit-action) carries the
result here.

Three repositories, one product:

| Repo | Owns | License |
| --- | --- | --- |
| [`archfit`](https://github.com/alexei-led/archfit) | engine, CLI, images, wire contracts | Apache-2.0 |
| [`archfit-action`](https://github.com/alexei-led/archfit-action) | execution/upload adapter | Apache-2.0 |
| `archfit-app` (this one) | tenancy, policy, baselines, PR feedback | proprietary |

## The promise, worded exactly

*archfit enforces your approved policy over disclosed evidence.*

Never "your architecture is correct", and never "human-reviewed means
architecturally sound". No interface can prove a reviewer paid attention.

## Status

Phase 0 is shipped — in the engine, not here: analyzer identity is published in
`measurement.tool_versions`, and the output contract has a machine-readable
schema (`archfit.state.schema.json`, engine v2.2.0/v2.2.1).

This repository holds the plan and a reporter skeleton. The ingest endpoint
**refuses every report**, deliberately: provenance verification is not
implemented, and a report accepted without it is a report whose producer cannot
be named. Accepting one now would make the first thing we ship the one thing the
design forbids.

Read [`docs/plans/20260916-github-app.md`](docs/plans/20260916-github-app.md)
before writing code here. It records seven decisions, the corrections that peer
review forced on them, and — explicitly — what the design does **not** deliver.

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
owner can supply, and the plan's *Owner-supplied inputs* section says which
phase each one blocks.

```sh
curl -s localhost:8080/healthz
curl -si -X POST localhost:8080/v1/reports \
  -H 'X-Archfit-Envelope: {"schema_version":"archfit.report-envelope.v1"}' \
  -H 'Content-Type: application/json' \
  -d '{"schema_version":"archfit.architecture-state.v1","verdict":"blocked"}'
```

The second call returns 501. That is the current contract, not a bug.

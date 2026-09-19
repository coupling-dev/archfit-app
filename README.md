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

The [closed-beta implementation plan](docs/plans/closed-beta.md) records the
18 September 2026 inspection of engine/Action prerequisites, pinned revisions,
integration gaps, dependencies, and exit checks. That snapshot found a state
schema and tool-version disclosure but incomplete measurement-profile comparison
and baseline identity. Recheck sibling repositories before starting integration.

This repository holds the designs, implementation plan, and a reporter skeleton.
The ingest endpoint
**refuses every report**, deliberately: provenance verification is not
implemented, and a report accepted without it is a report whose producer cannot
be named. Accepting one now would make the first thing we ship the one thing the
design forbids.

## Documentation

Start with the [documentation guide](docs/README.md). The canonical documents are:

- [Product design](docs/design/product.md) — first-version scope, user flows,
  governance, privacy, subscriptions, and open decisions.
- [Trust and reporting](docs/design/trust-and-reporting.md) — technical invariants,
  measurement identity, eligibility, and gate behavior. Read before writing code.
- [Closed-beta plan](docs/plans/closed-beta.md) — M0–M5 milestones and T01–T23 tasks.
- [Hosting research](docs/research/20260918-hosting.md) — dated cost assumptions and
  the owner-selected Cloud Run/PostgreSQL validation target, not a deployment
  approval or measured SLA.

## Layout

```text
cmd/reporter/      report-ingest skeleton; refuses reports until verification exists
docs/design/       product scope and technical requirements
docs/plans/        one closed-beta implementation plan
docs/research/     dated research and validation proposals
```

The reporter is deliberately the smallest deployable piece and depends on
nothing else. It sits in the merge path of every repository that enables the
gate, so a failure in billing, dashboards or an LLM feature must not be able to
take it down.

## Develop

Enable the project hooks once per clone:

```sh
git config core.hooksPath scripts/git-hooks
```

```sh
make test     # race tests
make lint     # golangci-lint
make build    # static binary → .bin/reporter
make run      # serve locally on :8080
```

No credentials are needed for any of that. Copy `.env.example` to `.env` when
you reach the first real deploy; the placeholders there are the inputs only the
owner can supply. The plan's [owner-input checklist](docs/plans/closed-beta.md#owner-inputs-at-the-point-they-are-needed)
states when they are needed. The current binary reads only `ARCHFIT_ADDR` and
does not load `.env` automatically.

```sh
curl -s localhost:8080/healthz
curl -si -X POST localhost:8080/v1/reports \
  -H 'X-Archfit-Envelope: {"schema_version":"archfit.report-envelope.v1"}' \
  -H 'Content-Type: application/json' \
  -d '{"schema_version":"archfit.architecture-state.v1","verdict":"blocked"}'
```

The second call returns 501. That is the current contract, not a bug.

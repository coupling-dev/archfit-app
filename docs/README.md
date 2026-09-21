# Documentation

The service is complete as code, but it is not deployed.
The [release blockers](architecture.md#release-blockers) list what is missing.

## Read first

| Document                        | Content                                                           |
| ------------------------------- | ----------------------------------------------------------------- |
| [Architecture](architecture.md) | How the service works: diagrams, packages, flows, configuration   |
| [User guide](user-guide.md)     | How to onboard a repository, read the feedback, change the policy |

## For developers

| Document                                       | Content                                                                       |
| ---------------------------------------------- | ----------------------------------------------------------------------------- |
| [Module specifications](../internal/module.md) | The root of the design tree. Each package below `internal/` has a `module.md` |
| [Tech stack](tech-stack.md)                    | Technology choices, test rules, and the open platform seams                   |
| [`AGENTS.md`](../AGENTS.md)                    | The short rules for coding agents                                             |

## Design history

These documents record why the service is built this way.
For the current behavior, read [Architecture](architecture.md) and the `module.md` files.

| Document                                                         | Content                                                                    |
| ---------------------------------------------------------------- | -------------------------------------------------------------------------- |
| [Product design](design/product.md)                              | Scope, user flows, governance, privacy, and commercial decisions           |
| [Trust and reporting](design/trust-and-reporting.md)             | Trusted inputs, producer eligibility, and gate behavior                    |
| [First-version decisions](design/decisions.md)                   | Closes the open design items. It overrides the two documents above         |
| [Implementation plan](plans/2026-09-21-implement-archfit-app.md) | One task for each module, with open findings                               |
| [Closed-beta plan](plans/closed-beta.md)                         | Milestones M0–M5 and tasks T01–T23                                         |
| [Alignment review](reviews/2026-09-21-fractal-align.md)          | Differences between design and code on 21 September 2026, with resolutions |
| [Hosting research](research/20260918-hosting.md)                 | Provider and cost research. It is not a deployment approval                |

## Rules for these documents

- Product design owns the scope. Trust and reporting owns the technical invariants.
- A plan sequences the work. It does not add scope.
- Research has a date. Do the research again before you use it.
- Keep the links current. Use the Git history for replaced text.

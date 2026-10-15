# Documentation

This index helps each reader choose a short path through the repository.
The [README](../README.md) owns release status and release blockers.

## Choose a path

| Reader                           | Read next                                                         |
| -------------------------------- | ----------------------------------------------------------------- |
| New developer                    | [Architecture](architecture.md), then [tech stack](tech-stack.md) |
| Repository admin or policy owner | [User guide](user-guide.md)                                       |
| Operator                         | [User guide operator section](user-guide.md#for-the-operator)     |
| Owner deploying to Google Cloud  | [Visual cloud architecture](cloud-architecture.html), then [infrastructure runbook](../infra/README.md) |
| Design reader                    | [Design records](#design-records)                                 |

## Current behavior

| Document                                       | Use it for                                                                            |
| ---------------------------------------------- | ------------------------------------------------------------------------------------- |
| [Architecture](architecture.md)                | Product boundary, request flows, package boundaries, routes, and runtime requirements |
| [User guide](user-guide.md)                    | Onboarding, policy changes, feedback, plans, and operator requests                    |
| [Tech stack](tech-stack.md)                    | Language, dependencies, tests, frontend, and platform seams                           |
| [Module specifications](../internal/module.md) | Package contracts, invariants, and test specifications                                |
| [`AGENTS.md`](../AGENTS.md)                    | Repository rules for contributors and coding agents                                   |

## Design records

These files explain intended scope and decisions. They are not a second source of
current behavior. For current behavior, read [Architecture](architecture.md),
[User guide](user-guide.md), and the `module.md` files.

| Document                                             | Use it for                                                               |
| ---------------------------------------------------- | ------------------------------------------------------------------------ |
| [Product design](design/product.md)                  | Product scope, user flows, governance, privacy, and commercial decisions |
| [Trust and reporting](design/trust-and-reporting.md) | Trusted inputs, producer eligibility, and reporting constraints          |
| [First-version decisions](design/decisions.md)       | Decisions that close open design items                                   |
| [Cloud architecture](design/cloud/architecture.md)   | Google Cloud direction: components, flows, state, cost, open decisions   |
| [Cloud security](design/cloud/security.md)           | Assets, boundaries, identities, secrets, supply chain, incident response |
| [Cloud operations](design/cloud/operations.md)       | Delivery, rollback, observability, and routine work                      |

## Archived records

Read the [archive index](archive/README.md) for implemented plans, dated reviews, and hosting research.
These records preserve history. They are not current status or operating instructions.

## Reading rules

- Code and the `module.md` contracts own current behavior.
- Design files record decisions and scope. They do not grant release or deployment approval.
- Dated research needs a new review before use.
- Use Git history for replaced text.

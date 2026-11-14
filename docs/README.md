# Documentation

This index helps each reader choose a short path through the repository.
The [README](../README.md) is the front page. This index does not track release status: the Deploy workflow runs do.

## Choose a path

| Reader                           | Read next                                                                                               |
| -------------------------------- | ------------------------------------------------------------------------------------------------------- |
| New developer                    | [Architecture](architecture.md), then [tech stack](tech-stack.md)                                       |
| Repository admin or policy owner | [User guide](user-guide.md)                                                                             |
| Operator                         | [Cloud operations](design/cloud/operations.md)                                                          |
| Owner deploying to Google Cloud  | [Visual cloud architecture](cloud-architecture.html), then [infrastructure runbook](../infra/README.md) |
| Design reader                    | [Design records](#design-records)                                                                       |

## Current behavior

The [module specifications](../internal/module.md) describe the Archfit 4 implementation.
The architecture and user guide describe the current Archfit 4 implementation and its release limitations.

| Document                                       | Use it for                                                                            |
| ---------------------------------------------- | ------------------------------------------------------------------------------------- |
| [Architecture](architecture.md)                | Product boundary, request flows, package boundaries, routes, and runtime requirements |
| [User guide](user-guide.md)                    | Setup, Home and the map, requests, policy edits, pull requests, expiry, and drift                                    |
| [Tech stack](tech-stack.md)                    | Language, dependencies, tests, frontend, and platform seams                           |
| [Module specifications](../internal/module.md) | Package contracts, invariants, and test specifications                                |
| [`AGENTS.md`](../AGENTS.md)                    | Repository rules for contributors and coding agents                                   |

## Design records

The current design for the engine, the Action, and this App is the Archfit 4 design:
[Archfit 4 design](https://github.com/alexei-led/archfit/blob/main/docs/plans/20261110-archfit-4-architecture-change-control.md) in the archfit repository.
New work follows it.

The files below explain intended scope and decisions. They are not a second source of
current behavior. For current behavior, read [Architecture](architecture.md),
[User guide](user-guide.md), and the `module.md` files.

| Document                                             | Use it for                                                               |
| ---------------------------------------------------- | ------------------------------------------------------------------------ |
| [Cloud architecture](design/cloud/architecture.md)   | Google Cloud direction: components, flows, state, cost, open decisions   |
| [Cloud security](design/cloud/security.md)           | Assets, boundaries, identities, secrets, supply chain, incident response |
| [Cloud operations](design/cloud/operations.md)       | Delivery, rollback, observability, and routine work                      |

## Archived records

Read the [archive index](archive/README.md) for implemented plans, dated reviews, hosting research, and the
[design records of the first version](archive/design/README.md): product design, trust and reporting,
first-version decisions, and the UI redesign. The Archfit 4 design replaces them.
These records preserve history. They are not current status or operating instructions.

## Reading rules

- Code and the `module.md` contracts own current behavior.
- Design files record decisions and scope. They do not grant release or deployment approval.
- Dated research needs a new review before use.
- Use Git history for replaced text.

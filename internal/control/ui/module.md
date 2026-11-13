# UI

**Path**: internal/control/ui/
**Parent**: [control](../module.md)
**Submodules**: session

## Purpose and boundaries

Serve the embedded web app, authentication routes and repository-scoped JSON APIs. A signed-in repository page
receives the same shell regardless of repository visibility; each API read independently authorizes the repository.
The gate facade supplies policy/request views, answers and owner actions; UI imports no gate child.

## Retained Archfit 4 APIs

- Setup returns schema 3 admission, first-policy approval state and ruleset findings.
- Policy reads schema 3 YAML, modules, rules, exceptions and approved/outside-flow edit state.
- Requests expose their record, modules, earlier answers and answer previews.
- Answer digest/answer routes pass the authenticated owner and passkey token to control/answers.
- Owner digest/action routes support first approval, workflow approval and confirm/revert.
  First approval re-reads GitHub administrator permission without the session cache; the gate also
  checks policy ownership. Other actions require ownership, not merely GitHub write/admin permission.
- Passkeys list, register, step up and remove the signed-in user's credentials.
- Installation admin routes require configured operator user IDs. Account admission is checked before
  repository data access. Data-sharing consent routes remain administrator-scoped.
- /api/repos retains the accessible-repository listing and historical check summaries.

## Legacy input removal

All existing page shells stay mounted, including repository, pull, graph and Data sharing pages.
The legacy-only overview/findings/graph/pull-data APIs and onboarding/discovery POST APIs remain mounted,
authorized and CSRF-protected, but return an explicit 409 instead of reading deleted contracts or dispatching
discovery. Invalid/missing pull numbers still return 404. The v2 policy schema URL returns 404.

The old graph builder, report artifact decoder/cache, prefetch hints and v3 onboarding adapter are removed.
No legacy report, baseline or settings fixture is read. Archfit 4 page-data implementation is separate work;
this removal does not remove frontend pages.

## Security and checks

The chain recovers panics, adds CSP/security headers, bounds request bodies, authenticates sessions,
checks CSRF for writes and authorizes/admit-checks each repository API. Tokens stay server-side.
A revoked user loses API access; an unapproved installation gets the Waiting response.
Page errors use the shell with a fresh nonce; JSON errors never echo upstream text.

Route/permission, tenant isolation, CSRF, admission, session revocation, CSP and log privacy tests remain.
Setup, owner/admin/permission, policy, request, answer and passkey suites cover the retained v4 APIs.
The route table tests expect 409 only for legacy-only data routes, not removal of their page shells.

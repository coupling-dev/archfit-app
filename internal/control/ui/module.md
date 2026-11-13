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
- /api/repos lists accessible repositories in name order, at most 100, with state from the gate RepoStates façade and no historical check reads.

## Legacy input removal

All existing page shells stay mounted, including repository, pull, graph and Data sharing pages.
The legacy-only overview/findings/graph/pull-graph APIs and onboarding/discovery POST APIs remain mounted,
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

## Home API (M3 task 4)

`GET /api/repos/{o}/{n}/home` uses the existing `apiRepo(permRead, ...)` authorization and admission chain.
`Deps.HomeViews` is `gate.HomeViews`; `control` imports no gate child. The constructor receives it from
`control.New`, which calls `gate.NewHome`. The handler reuses `Setups.Setup` through the existing setup API.
Before first approval it returns `status` and `setup` (the missing steps, first pull and ruleset settings),
without loading Home data or a map.

For an approved repository the response has `status`, optional `needs_you` rows, and `map`. Each row has
`kind`, `text`, `href`, and the applicable request, pull, rule, modules, expiry date, days left or request record.
Requests link to `/r/{o}/{n}/requests/{number}`. Drift and expiry link to their GitHub issue, falling back to
Policy before the sweep opens one. Owner actions link to `/r/{o}/{n}/owner/{workflow|confirm|revert}`;
workflow links include `?pull={number}`. These links do not authorize an action; the owner API does that again.

The map has `layers` (outer first, an empty-string band last for modules without a layer), `modules`
(name, layer, effective volatility), and `lines` (kind, from, to, optional rule, request or expiry date).
It carries no layout positions. Kinds are `allowed`, `requested`, `deny`, `exception`, `drift`.
A deny rule is visible only when a request or current stored error touches its module pair.
Dates use the injected clock's UTC day. Exceptions enter Needs you at seven days, stay there on their last
day, and are expired the next day. An absent, stale or partial repository result is never called clean.

`home_test.go` runs the real façade over memory storage and GitHub fakes through the authenticated HTTP
handler. JSON goldens live in `testdata/home/`; generate deliberately with
`UPDATE_GOLDEN=1 go test ./internal/control/ui -run '^TestHomeAPIGoldens$'` from the repository root.
The same tests call `wording.NotUsedIn` and cover the UTC date window, failures and authorization.

If the outside-flow policy is missing or invalid, Home omits the map and confirm action but retains the revert link.

## Pull API (M3 task 3)

`GET /api/repos/{o}/{n}/pulls/{number}` authorizes repository read and admission before calling the gate pull
read façade. It returns the current pull record, check state, waiting requests and shared feedback page form,
or a single no-result, untrusted-result or pending-publication line. The browser uses only text and constructs
request links from the current route and request numbers; the pull graph endpoint remains unchanged.
## Owner action views

`GET /api/repos/{o}/{n}/owner/{first|workflow|confirm|revert}/view` returns the read-only owner
view through repository-read authorization and admission. `?pull=n` is required for workflow,
optional for first approval, and not allowed for confirm or revert. It returns actual file
bytes, immutable refs, SHA-256s, rendered diffs, the M2 action digest and the separate view digest.

The server registers `/r/{o}/{n}/owner/{first|workflow|confirm|revert}` as signed-in shell routes.
The client validates the action, pull query and owner-view JSON before showing an action.
Each view has one submit button, which starts the passkey ceremony for the displayed view digest.

The owner action POST accepts `view_digest` beside the original M2 `digest`. It forwards both
unchanged to control/answers, which checks the view twice and binds the passkey to the view.

## Repositories page

`GET /api/repos` keeps installation admission separate from repository state. Unadmitted installations retain the
waiting marker without a repository read. For admitted repositories, `gate.NewRepoStates` reads only the stored
`v4/repo` value: no approved policy means `not set up`, then an outside-flow marker takes precedence, then the
stored `expired`, `drift` or `clean` state. Missing, partial or unrecognised evidence is `unknown`; read errors fail
the request. The page shows a dot and a word, with fail colour only for `expired`.

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
  repository data access.
- /api/repos lists accessible repositories in name order, at most 100, with state from the gate RepoStates façade and no historical check reads.

## Security and checks

The chain recovers panics, adds CSP/security headers, bounds request bodies, authenticates sessions,
checks CSRF for writes and authorizes/admit-checks each repository API. Tokens stay server-side.
A revoked user loses API access; an unapproved installation gets the Waiting response.
Page errors use the shell with a fresh nonce; JSON errors never echo upstream text.

Route/permission, tenant isolation, CSRF, admission, session revocation, CSP and log privacy tests remain.
Setup, owner/admin/permission, policy, request, answer and passkey suites cover the retained v4 APIs.
`removed_test.go` checks that Graph, pull Graph, Decisions, Data sharing, consent and their old APIs return 404,
not redirects or 409, for signed-in and anonymous clients with no data dependencies wired.
`web/src/route-parity.test.ts` reads the real page registrations and Route kinds and requires each to have a
server route and a case in the words-checked browser spec. Mutation checks reject missing routes, kinds and words checks.

## Removed surfaces

Graph, pull Graph, Decisions, Data sharing and consent pages are not mounted. Legacy overview/findings/graph,
pull-graph and Data sharing APIs, onboarding/discover POSTs and the consent redirect are gone; unmatched routes
return 404 before repository authorization or data access. The v2 policy schema URL also returns 404.
No report cache, old graph builder, Archfit 3 renderer or consent reader remains. Stored consent records stay unread.
`overview.go` remains because it implements the retained `/api/repos` list, not the removed Overview page.

## YAML edit (M3 task 10)

- `POST /api/repos/{o}/{n}/policy/edit/preview`: whole YAML, `answer: edit`, starting `base_digest`.
  Returns all line errors, or the new digest, protected target tip and an App-rendered diff against the target policy.
- `POST /api/repos/{o}/{n}/policy/edit/reload` explicitly restores only recorded approvals under the proposal lease.
  GET and preview never repair a branch. A recovery flag offers reload before editing an unrecorded/moved head.
- `POST /api/repos/{o}/{n}/policy/edit`: the same file and base, preview target, one-use passkey token.
  Both routes use repository-read authorization, admission, CSRF and strict JSON (including no trailing values).
  YAML is capped at 32 KiB before parsing. The gate rechecks owners and every input before redemption.
- The edit passkey begin also takes the YAML and revalidates the preview before opening the ceremony.
  Its token binds the NEW byte digest, starting base and exact target; the YAML is not stored in the token.
- `/r/{o}/{n}/policy?edit=1` starts from the proposal file, otherwise the target file, and renders text only.
  Text changes drop the preview. All edits need a passkey; local glob checking uses `archfit check --policy <file>`.
- A changed base or target is `the policy changed; reload`. Any target-tip move, code-only included, voids an edit.

## Home API (M3 task 4)

`GET /api/repos/{o}/{n}/home` uses the existing `apiRepo(permRead, ...)` authorization and admission chain.
`Deps.HomeViews` is `gate.HomeViews`; `control` imports no gate child. The constructor receives it from
`control.New`, which calls `gate.NewHome`. The handler reuses `Setups.Setup` through the existing setup API.
Before first approval it returns `status` and `setup` (the missing steps, first pull and ruleset settings),
without loading Home data or a map.

Home reads the protected tip once per authorized request and shares its immutable policy (including its
byte digest and decode result) and workflow between Setup and Home. The read-only approval summary also
reuses the protected file bytes and tip, without changing the fresh inputs of Review or Act. It never caches repository approval
records, candidate PR heads or owner eligibility. Passkey inventory runs alongside Setup; policy and
workflow reads run concurrently at that same tip. The approval summary runs alongside rules, using the
same exact-head/digest predicate as Review. Ruleset details and each Home record batch have at
most four concurrent reads. Error priority follows input order, not completion order; unreadable rules
remain unknown and inventory errors return both `passkey: false` and `passkey_known: false`. HTTP client timeouts,
request cancellation and rate-limit backoff remain in effect.

The structured `ui request` log includes `stages`: `authorization`, `setup`, `rules`, `passkeys`,
`home_records`, `owner_review`, `approval_summary` and `snapshot` when reached. Each records `duration_ms`, `count` and
`cache_hits`; durations overlap and must not be summed as request latency. Counts mean authorization/
setup/approval-summary/snapshot invocations, rules API reads, returned passkeys, Home request/touched-pair/workflow items,
and owner eligibility probes respectively. Cache hits count session authorization reuse or request-local
protected-input reuse, not approval reuse. Stages contain no tokens, file bytes or upstream error text.
Existing `gh_calls` and `gh_ms` still measure real GitHub round trips, including retries.

Every response carries the same numbers in a `Server-Timing` header for browser devtools: `total` (time to the
header), `gh` (summed GitHub time, call count in `desc`), one metric per stage finished before the header, and `cold`
on the first request the instance serves. Only numbers and the fixed stage names go out.

The map imports its Diagram component only while the diagram disclosure is open; closing it aborts
layout work. The list view never starts the ELK worker. Desktop still starts with the diagram selected;
phone starts with the list. Browser tests assert zero worker requests before phone diagram selection.

For an approved repository the response has `status`, optional `needs_you` rows, and `map`. Each row has
`kind`, `text`, `href`, and the applicable request, pull, rule, modules, expiry date, days left or request record.
Requests link to `/r/{o}/{n}/requests/{number}`. Drift and expiry link to their GitHub issue, falling back to
Policy before the sweep opens one. Owner actions link to `/r/{o}/{n}/owner/{workflow|confirm|revert}`;
workflow links include `?pull={number}`. First-policy and workflow links are resolved with
`Owners.OwnerReview`: first approval requires an administrator also named in the proposed policy;
workflow approval requires an owner of the approved policy. Setup includes an optional `owner_action`
with `label` and eligible `href`; an ineligible viewer gets a named-role handoff without `href`.
A passkey is required to approve, not to follow the review link. These links do not authorize an action;
the owner API checks again on submission. Setup progress excludes `not_applicable` rules but never
counts `unknown` as complete. Policy and workflow presence on a PR is distinct from the default branch.

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
request links from the current route and request numbers. Unknown results also resolve an optional
`owner_action` using the same first-policy/workflow eligibility check as Home, not the report's prose.
It contains a plain review label and link, or a role handoff without a link. Failed authority reads
never produce a link, and the displayed verdict remains unchanged. There is no pull graph endpoint.
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

`GET /api/repos` lists at most 100 accessible repositories in name order. Unadmitted installations retain
the waiting marker without a repository read. `gate.NewRepoStates` supplies the retained `state` field from
stored `v4/repo` evidence; the SPA does not treat that field as readiness.

The existing read-only `Setup` probe now covers the first 25 admitted rows regardless of stored state,
with at most 8 concurrent probes. This is the only GitHub read path added to the cheap stored-state read;
no Home, owner-view, or additional pull lookup runs. Expanding eligibility can increase actual reads on
pages containing approved repositories, but the 25-row/8-parallel bounds are unchanged.

Each row separates:
- `setup_stage`: `not_started`, `first_approval`, `workflow_review`, or `policy_approved`.
  `FirstPull` identifies a first policy even when the default branch has none. `OpenPull` with policy
  present and workflow absent identifies workflow review, before first-approval state. Both files and
  `State == "approved"` are needed for Policy approved. Outside-flow or inconsistent approval facts
  omit the stage and explain Check in `setup_reason`; they never become Policy approved.
- `enforcement`: `required` only for the Setup rules report's `gate_check: ok`;
  `not_required` for `gate_check: warning` plus `pull_requests: ok`; otherwise `check` with
  `enforcement_reason`. Policy mode and unrelated rules findings do not determine this field.
- Optional `attention`: only Setup's existing `FirstPull` / `OpenPull` PR number or first-approval state.
  Request counts are **not read**: RepoStates and Setup expose none, and Home adds GitHub reads.
  The UI says those counts are not checked here, never invents zero, and directs readers to Home.
- `next_step`: a server-selected text/href pair to Home or a PR in the same repository.
  First-policy and workflow PRs link to Pull, which resolves owner-action eligibility. First approval
  without a PR links to Home, whose setup step resolves eligibility. The list never links directly
  to an owner action page. The SPA accepts only same-repository Home and positive-number Pull routes.
  Enforcement reasons stay in the API, but the page explains the Check chip once below the list.
  Row-specific setup reasons remain inline.

A probe error or a row beyond the probe limit retains stored evidence, omits the setup stage, and shows
Check with a reason; stored-state failures still fail the request. Waiting installations show their own
approval status, not repository readiness. The desktop table has Repository, Setup, Needs attention,
and Next step columns; mobile has labelled cards with one destination each.

`overview_test.go` covers stages/destinations and the rules-state matrix; `repos_test.go` covers admission,
bounds, errors and JSON fixtures consumed by Playwright. `web/src/lib/repos.test.ts` tests vocabulary and
destination restrictions. `web/e2e/repos.spec.ts` checks both widths, accessibility and CSP. The existing
`stay.spec.ts` text goldens cover both widths without OS-specific pixel comparisons. Set
`ARCHFIT_DOCS_SHOTS=1` when running the Repos spec to regenerate the reference images in `docs/images/`.
The audit explicitly requires “Needs attention”, “Workflow review” and “Review …” navigation labels;
wording checks allow only those exact phrases on this page, retaining the legacy check elsewhere.

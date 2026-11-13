# UI

**Path**: internal/control/ui/
**Parent**: internal/control/ (Control plane)
**Submodules**: session, graph

## Purpose

UI is the browser surface of Archfit: sign-in, the organization overview, repository status and
onboarding actions, the request page where an owner answers with a passkey, the passkey page, the read-only graph,
the Data sharing page and the entitlement banner. It holds no state of its own: every
page is derived from GitHub and the control-plane modules at request time. Without it customers
could still work GitHub-first, but onboarding, answers to requests and consent would have no home.

## Functional Responsibilities

- Serve the web app (`web/`, Svelte 5 built by Vite, embedded from `spa/`) for the overview, the repository page,
  the pull request page, both graph pages, the request page, the passkey page and Data sharing, and its JSON API under
  `/api/`. ui
  renders no HTML of its own: an error page is the same shell with the error's status and sentence.
- Authenticate every request through `session` and authorize every repository API call at the needed permission. A
  repository page is the shell, the same for every repository, and needs only a session.
- Overview: the user's repositories with their latest default-branch `archfit/state` check conclusion. No score. A
  repository of an account no operator approved is listed as "waiting for approval", with no lookup.
- Waiting: answer every repository API of an unapproved account `403` with the account's login, so the web app shows the
  Waiting page instead of an empty repository.
- Admin: for an operator only, list the App's installations, waiting first, and approve or revoke one through
  `Account.Admit` and `Account.Unadmit`.
- Repository status: onboarding `Progress`, actions to start onboarding or dispatch discovery, entitlement banner; once
  a report came in, a watch line on the default branch and the pull requests that wait for an update after the target
  moved.
- Request page: one request with its record, balance, modules, the preview of each answer and earlier answers
  (`requests.go`), and the owner's answer with the passkey step-up (`answers.go`, `passkey.go`). The passkey page lists,
  registers and removes the user's own passkeys.
- Graph: fetch the latest report artifact, decode it, build the view through `graph`, answer it as JSON, mark it stale.
  The web app draws it.
- Pull request graph: the same admitted report as the pull request page. Link origin aggregates current finding
  evidence; it does not establish newly added pairs or source sites.
- Repository findings: the latest default-branch report's findings in the groups and words of the PR comment, for the
  overview's counters and list.
- Pull request page: the result/report of one head, with the first screen of the PR comment. It has no owner action: an owner
  answers on the request page.
- Data sharing: per managed feature, what leaves the customer's environment, who allowed it and when, and the
  preconditions it still needs; allow and revoke `source_processing` consent (repository administrators only).

## Subdomain Classification

Core-adjacent product surface. Volatility: high. Page flow and wording change with every
pilot; product.md leaves usability targets open.

## Encapsulated Knowledge

- Routes, the JSON API, the web app, templates, static assets, page wording, the HTTP status mapping of every error.
- The handler chain: recover → security headers → body limit → session → CSRF → authorize (API) → admission (repository API) → handler.
- Who is an operator: a signed-in user whose GitHub user ID is in `Deps.Operators`, never by login. Any other
  signed-in user gets the same `404` as for an unknown path. A signed-out request gets what every route gives it: sign-in
  (`303` for a page, `401` for the API), and a write without the JSON type or the CSRF token is refused (`415`, `403`)
  before the operator check. Nothing here is secret: the routes are in the public docs; the check protects the data.
- How a page loads (`web/src/lib/early.ts`): its first reads (`firstPaths`, those with no input but the path) and its
  lazy chunk start beside `/api/session`, not after it; each is authorized on its own, the page takes the answer once
  when it mounts, and a read no page takes is dropped quietly. Until an answer comes, the frame and a grey skeleton of
  each section (`components/Skeleton.svelte`, CSS only, still under reduced motion). A `404` from the read that fills a
  page replaces the page with the error page's "This page cannot be shown" and no repository tabs (`explainLoad`,
  `lib/gate.ts`); a write's `404` stays a sentence on the page, so a form keeps what the user typed.
- The CSRF scheme: `hex(HMAC-SHA256(key = User.Token, "archfit-csrf"))` **[default]**, stateless and per session. The
  web app reads it from `/api/session` and sends it as the `X-CSRF-Token` header; the one form left, Sign out, sends
  it as the `csrf` field.
- The shell's style nonce: 16 random bytes, base64, fresh on every shell response, in its CSP and its
  `<meta name="csp-nonce">` (see Security, item 2).
- How check conclusions, draft states and plans are worded for people.
- The drawing of the graph (`web/src/pages/Graph.svelte` with Svelte Flow, one lazy chunk the first load never
  fetches; ELK places it in a worker, `web/src/lib/layout.worker.ts`, a hashed file of its own started on the first
  layout, so a large graph never blocks the page; a worker that fails to load or throws fails the layout with "The
  layout failed: the layout worker stopped", and the next layout starts a fresh one): colour is the status, line width the integration strength (contract thinnest, intrusive thickest), a dashed
  line a strength no seam measured, the label the dependency count, a dashed module one outside the policy, a halo a
  dependency the pull request added. Status chips that filter, a problem list (one row per dependency: the violations, then the advisories, and on a pull
  request graph the other dependencies it added; a direction whose reverse is a problem too says "cycle"), a folded
  "Accepted earlier" group (accepted debt this pull request did not add, drawn thin and faint),
  an ELK layered layout (declared layers as partitions, in policy order), focus on problem links above 15 modules
  **[default]**, a matrix view, an inspector with strength, distance, volatility and balance, then the engine's
  balance reading of the dependency (below), a one-line legend (status colours, line width = integration strength, the rarer marks folded under "More"), and
  filterable tables, which are the only view above 300 modules. Report strings reach the page only as Svelte text and
  attributes.
- Bounds, each **[default]**: 100 repositories on the overview, 8 parallel lookups, 512 KB request body,
  drawing up to 300 nodes and a filterable list above that.

## Public Contract

```go
type Deps struct {
    OAuth      OAuth  // ui builds its own session child from OAuth, CookieKey and BaseURL
    CookieKey  []byte
    BaseURL    string
    GitHub     *Client
    Account    Account
    Consents   Consents
    Onboarding Onboarding
    Admissions *Admissions // which installations the App serves; the others see the Waiting page
    Passkeys   Passkeys    // the signed-in user's passkeys and the step-up of an answer (control/passkey)
    RequestViews RequestViews // the request page: a request and the preview of each answer, read only (gate)
    Answers    Answers     // an owner's answer to a request, with the passkey (control/answers)
    Operators  []int64     // GitHub user IDs that approve installations on /admin
    Now        func() time.Time
}

// New returns the UI mounted at "/". It serves the web app's build from the binary.
func New(d Deps) (http.Handler, error)
```

### Routes

Minimum repository permission in brackets. `{o}/{n}` is only a lookup key for `Sessions.Authorize`. A repository page
(`/r/…`) needs only a session: it is the shell, the same bytes for every repository, one that exists or not, visible or
not, apart from the nonce, and it makes no GitHub call **[decided, g3]**. The repository's data and every permission
check are in the API, which authorizes on every request; a repository the user cannot see answers its `404` exactly
as one that does not exist, and the app shows that answer as the error page, with no repository tabs.

- `GET /` — the web app's shell; the app shows the overview, or a landing page with a sign-in link when
  `/api/session` answers `401`. The shell holds no tenant data.
- `GET /auth/login`, `GET /auth/callback`, `POST /auth/logout` — handlers of `session`.
- `GET /schema/policy.json` — `policy.Schema()`; no sign-in needed, no tenant data.
- `GET /licenses.txt` — the build's `assets/licenses-<hash>.txt` (the licence of every npm package in the bundle, the
  layout worker's included) at a stable address for the app's footer link, `Cache-Control: no-cache`; `404` without a
  build. No sign-in needed, no tenant data.
- `GET /assets/…` — the web app's hashed build files, `Cache-Control: public, max-age=31536000, immutable`; no
  sign-in needed, no tenant data. A folder or a missing file is `404`; nothing else of the build is served.
- `GET /r/{o}/{n}` [session] — the shell again. The app fills it from the overview API.
- `GET /r/{o}/{n}/pull/{number}` [session] — the shell for the pull request page; a number that is not a positive integer
  answers `404` here, a number that is no pull request of the repository answers `404` from the API.
- `GET /r/{o}/{n}/graph` [session] — the shell for the repository graph.
- `GET /r/{o}/{n}/pull/{number}/graph` [session] — the shell for the pull request graph; the same `404` rule for the number.
- `GET /r/{o}/{n}/data-sharing` [session] — the shell, for the Data sharing page; its API needs admin.
- `GET /r/{o}/{n}/requests/{number}` [session] — the shell for the request page; the same `404` rule for the number as
  the pull request page.
- `GET /r/{o}/{n}/passkeys` [session] — the shell for the passkey page.
- `GET /admin` [session, operator] — the shell, for the admin page; `404` for any signed-in user who is not an operator.
- `GET /r/{o}/{n}/consent` [session] — `308` to `/r/{o}/{n}/data-sharing`, from the path alone: the same answer for
  every repository, so it confirms nothing.

JSON API: every response is `application/json` with `Cache-Control: no-store` and a body of an explicit type of this
package, never a stored record. Errors are `{"error": "<sentence>"}` with the status of the mapping below. A `POST`
needs `Content-Type: application/json` (else `415`) and the `X-CSRF-Token` header (else `403`); a body field this
package does not know is `422`, so a body cannot name a repository, a user or an installation.

- `GET /api/session` — `{login, csrf, build}` of the signed-in user. `build` is an explicit allowlisted DTO of compiled
  source/build-time metadata, the embedded Go main-module version and the runtime's validated `K_REVISION`; absent metadata remains empty. About
  displays it only after sign-in. No checkout, credentials, environment inventory or cloud metadata is read. For an operator also `operator: true` and `waiting`,
  the number of installations no one approved yet (`-1` when the list could not be read); both are left out for
  everyone else.
- `GET /api/repos` — `{repos: [{full_name, waiting?, state, tone, reason}], truncated}`: the overview, first 100 by
  name. A repository whose installation is not admitted has `waiting: true`, state "waiting for approval", tone
  `attention`, and no lookup.
- `GET /api/admin/installations` [operator] — `{waiting, approved, unreadable}`; each row is `{id, account, type,
  installed_at, repos: [{name, private}], repos_error, auto, approved_by?, approved_at?}`. `waiting` is newest first,
  `approved` by account name. `auto` marks an account approved by the App's configuration. An installation whose
  approval cannot be read is listed as waiting and counted in `unreadable`, never shown as approved. `repos_error`
  means its repositories could not be read; the row stays.
- `POST /api/admin/installations/{id}/approve` [operator] — body `{}`; `Account.Admit` with the account login from
  GitHub, the operator's login and user ID and the server clock; answers `{notice}`.
- `POST /api/admin/installations/{id}/revoke` [operator] — body `{}`; `Account.Unadmit`; answers `{notice}`.
  For both, `{id}` must be one of the App's own installations (`ListInstallations`), else `404`; an account approved by
  configuration is `409` "This account is approved by the App's configuration. Change it by pull request."
- Every `/api/repos/{o}/{n}/…` route of a repository whose installation is not admitted answers `403` `{error,
  waiting}` with `waiting` the account login, after the user is authorized for it.
- `GET /api/repos/{o}/{n}/pulls/{number}` [read] — the pull request page: header, current result and report, and
  `first_screen`, the first screen of the PR comment, which stays report/check-derived text and can be absent.
- `GET /api/repos/{o}/{n}/pulls/{number}/graph` [read] — `{unavailable, view, stale_reason, policy_href, branch, pull,
  added}`: the pull request page's header and the `graph.View` of the report its check names, each link's `origin` by
  the PR comment's rule; `added` counts the links whose origin is `added`. `policy_href` is the policy file on GitHub
  at the ref whose text is the report's policy (the head, else the base branch). The same `404` rule.
- `GET /api/repos/{o}/{n}/graph` [read] — `{unavailable, view, stale_reason, policy_href, branch}`: the latest
  default-branch report against the policy on that branch. `policy_href` is the policy file on GitHub at the default
  branch. No link carries an origin.
- `GET /api/repos/{o}/{n}/findings` [read] — `{unavailable, branch, no_baseline, attention_reason, …}`: the latest
  default-branch report's findings in the pull request page's groups, without origins. `attention_reason`, only for
  the verdict `needs_attention`, follows the engine's rule in its order: active advisories, required rules not
  evaluated (or `hard_gates: unmeasured`), then the dimensions partially measured and those not measured. The overview
  shows it under the default-branch state only when that state reads "attention".
- `GET /api/repos/{o}/{n}/requests/{number}` [read] — the request page (design 12.3, M2 task 17), from
  `RequestViews.RequestView`: `{number, state, reason, rule, path?, from?, to?, expires?, record, verified, balance?:
  {value, max, min}, policy, modules: [{name, layer?, volatility?, subdomain?, depends_on}], allowed, earlier: [{kind,
  rule, from, to?, why?, instead?, expires?}]}`. `record` is the record the request issue shows, as the check prints it
  or as the CLI reported it (`verified` false: "as reported"). `modules` and `earlier` come from the policy at the
  default branch's tip (`policy` false when it has none of version 3): the two modules and what the policy already says
  about them (`deny` rules and exceptions, either way). Reason, record and policy texts are data. A missing request is
  `404`.
- `GET /api/repos/{o}/{n}/requests/{number}/preview?answer=approve|approve-until|reject&until=YYYY-MM-DD&instead=…`
  [read] — what one answer to an open request would write and change, from `RequestViews.RequestPreview`: `{answer,
  until?, diff, policy_digest, loosens, pulls: [{number, status, waits, clears, left}]}`. `diff` is the policy change as
  a unified diff against the policy at `policy_digest`, `""` when the answer writes no line; `loosens` says the answer
  needs the passkey; `pulls` are the open pull requests waiting on the request or holding an error it clears (`left` 0:
  the answer leaves none). `until` defaults to the date the worker asked for. An incomplete answer is `422`, a request
  that is not open, no policy, or an answer the writer cannot place is `409`.
- `POST /api/repos/{o}/{n}/requests/{number}/answer/digest` [read] — body `{answer, until?, instead?}`; answers
  `{digest}`, the change digest of that answer (`control/answers.Digest`). The page makes the passkey step-up
  (`POST …/passkeys/step-up`) with `action` the answer, `request` the number and that `digest`, for an answer that
  loosens the policy (approve, approve-until).
- `POST /api/repos/{o}/{n}/requests/{number}/answer` [read; the owner rule of the policy on the default branch decides]
  — body `{answer, until?, instead?, merge_later?, token?}`, the token of that step-up for approve and approve-until;
  answers `{pull_request?, state?, merge?, notice}` (M2 task 19, `control/answers.Answer`). `state` is `merged`, `later`
  or `refused` (GitHub did not merge; the pull request stays open); no `pull_request` when the answer writes no policy
  line. The signed-in user is the owner; a body names no user or repository. Refusals: a login the policy does not name
  `403 not_policy_owner`; a loosening answer without a step-up `403 needs_step_up`; a token used, expired or for another
  change `403 token`; another answer of the repository being written `409 answer_busy`; a policy pull request
  that holds 9 answers `409 proposal_full`; and the request page's (`404`,
  `409 request_closed`, `422 answer`, `409 by_hand`, `409 no_policy`). After any refusal the answer was not recorded.
- `GET /api/repos/{o}/{n}/overview` [read] — default-branch state, open pull requests with their result, setup steps,
  onboarding progress, entitlement banner, history links, and whether the user may start onboarding or discovery.
  After a report: `main_sha` (short) and `main_at` (RFC 3339) of the check the state is read from, `pulls_to_update`
  (listed pull requests whose row has `rerun`), each pull request row's `rerun` (the recorded `target_moved` next step in
  the comment's words, naming the modules; absent for any other result).
  While the owner still has to add the workflow, `workflow` is `{path, branch, content, add_url}`: `add_url` is GitHub's
  new-file page on `branch` with `filename` and `value` filled in. GitHub does not document those two parameters
  (checked by hand on 2026-10-22 with a branch name holding a `/`); the page also offers the content to copy and to
  read, so a change on GitHub's side costs one paste. `can_start` is false while `workflow` is set.
  `next` is the next setup step in one sentence and `next_how` its details, both from the onboarding stage.
  `tone` (`success` | `attention` | `danger` | `accent` | `neutral`) colours a state word; the word is always shown.
- `POST /api/repos/{o}/{n}/onboarding` [admin] — body `{mode: "customer" | "managed", owners}` (owners one per line);
  answers `{notice}`, or `{location}` of the Data sharing page when managed mode needs consent first.
- `POST /api/repos/{o}/{n}/discover` [admin] — body `{}`; dispatch the discovery run of the Archfit workflow; answers
  `{notice}`.
- `GET /api/repos/{o}/{n}/data-sharing` [admin] — `{owner, features: [{kind, title, leaves, note?, offered, allowed, by?, at?,
  checks: [{label, state}]}]}`. `owner` is the account whose repositories a consent covers; `by` is the login as it was
  when the consent was recorded (`Record` has always required one; records older than `by_id` lack only the ID); `at` is RFC 3339 UTC. `state` is `met` |
  `not_met` | `unknown`. A consent that cannot be read answers `502`, never "not allowed".
- `POST /api/repos/{o}/{n}/data-sharing` [admin] — body `{kind, action: "allow" | "revoke"}`; a kind not in the
  server's list or another action is `400`. Answers the `GET` body plus `notice`. Both actions are idempotent: allowing an
  allowed feature keeps its `by` and `at` and writes nothing; revoking nothing is `200`. `by`, the user ID and `at`
  come from the session and the server clock; the installation from the authorized repository.
- `GET /r/{o}/{n}/passkeys` [session] — the shell, for the passkey page (M2 task 24).
- The passkey routes (M2 task 18) [read] act on the signed-in user's own passkeys in the repository's installation.
  Who may register is `control/passkey`'s rule (an owner named in the policy, or a repository administrator before
  the first policy); ui hands it the user's id, login and administrator bit (`Authorize` at admin, answered from the
  permission the read authorization cached). A body never names a user or a repository; an unknown field is `422`.
  - `GET /api/repos/{o}/{n}/passkeys` — `{passkeys: [{id, created, last_used?}]}`, oldest first.
  - `POST /api/repos/{o}/{n}/passkeys/register` — body `{token?}` (a step-up token for `register` when the user has a
    passkey); `{options}`, the JSON for `navigator.credentials.create`.
  - `POST /api/repos/{o}/{n}/passkeys/register/finish` — body `{credential}`, the browser's `PublicKeyCredential`;
    `{passkey, notice}`. The registration issue is opened before the passkey is stored.
  - `POST /api/repos/{o}/{n}/passkeys/step-up` — body `{action, request?, digest?, passkey?}`; `{options}` for
    `navigator.credentials.get`.
  - `POST /api/repos/{o}/{n}/passkeys/step-up/finish` — body `{credential}`; `{token}`, usable once within 5 minutes
    for that purpose.
  - `POST /api/repos/{o}/{n}/passkeys/{id}/remove` — body `{token}` (a step-up token for `remove` and that passkey);
    `{notice}`.
  - Errors: not an owner `403`; a second passkey without a token, no passkey, too many, or a policy that does not
    decode `409`; a failed or expired ceremony `400`; a counter that went back or a token not valid `403`; an unknown
    passkey `404`; a malformed purpose `422`.
- Any other `/api/…` path, or a method an API path does not take (the paths of the policy editor, assist, owner approvals and
  decision requests included): `404` JSON, outcome `not_found`. A panic on an
  `/api/` path answers `500` JSON.

### Status mapping

- Not signed in: `303` to `/auth/login?return=<path>` for `GET`, `401` for `POST`, `401` JSON on every `/api/` path. A GitHub `401` on the user's token
  (`api.ErrUserTokenRejected`, from `Authorize` or `UserRepos`) is treated the same way after `Sessions.End`: the cookie
  is deleted, the answer is the not-signed-in one, and the log outcome is `signed_out`. It is never a `502`.
- On a page route every error answers the shell as an error page (`fail`): the status below, the shell's CSP with a
  fresh nonce, `Cache-Control: no-store`, and the sentence of the wording table, HTML-escaped, in place of the
  `__APP_ERROR__` of `<meta name="app-error">`; the app then shows that sentence instead of the route, with no
  repository tabs. An error's own text never reaches a page or an API answer. A path no route takes, or a route's path
  with a method it does not take (`GET /auth/logout`, `POST /r/o/n`), is the same page with `404` "Not found.": one
  catch-all `/` keeps every non-API answer the shell, and `ServeMux` refuses a `GET /` catch-all beside the method-less
  `/api/` one, so the mux's own `405` is not kept. A panic answers `500` "internal error" as plain text under the handler chain's CSP.
- Repository API of an installation that is not admitted (after `Authorize` succeeded): `403` "This account is waiting
  for approval." with `waiting` set in the JSON, log outcome `not_admitted`. The check reads the admission on every
  request. An approval that cannot be read is not "waiting": it is a generic error (`502` "try again"), so an approved
  account never sees the Waiting page during a Store outage.
- `/admin` and `/api/admin/…` for a user who is not an operator, and an installation ID that is not the App's: `404`
  "Not found."
- `ErrForbidden` from `Authorize`: `404` **[default]**, so that repository names are not confirmed to outsiders.
  `api.ErrNotFound` from any GitHub call a page does not handle itself: `404` with the same "Not found." wording.
- Missing or wrong CSRF token: `403`. Body over 512 KB **[default]**: `413`. An `/api/` `POST` that is not JSON: `415`.
- A Data sharing `POST` naming a kind not in the server's list, or an action other than `allow` or `revoke`: `400`.
  A Data sharing change that was written but cannot be read back: `502` "The change was saved, but it could not be read
  back. Reload the page."; the page then reads the state again.
- Onboarding states, each `409` with fixed wording: `onboarding.ErrNoSandbox` "Managed discovery is not available.",
  `onboarding.ErrAlreadyOnboarded` "This repository is already onboarded. Reload the page to see its state.",
  `onboarding.ErrBusy` "Managed discovery is busy with other repositories. Try again in a few minutes."
- `ErrRateLimited`: `503` with `Retry-After: 60` **[default]**. Any other upstream error: `502`. Panic: `500`.
- Every successful `/api/` call answers `200` with its body; the Sign out form answers `303`.

## Integrations

### internal/control/ui/session (child)

- **Counterpart**: `internal/control/ui/session/module.md`
- **Direction**: ui depends on session
- **Strength**: functional (composition)
- **LCA / Rank / Distance**: control/ui / 1 / 1
- **Volatility**: high
- **Balanced?**: yes — parent and child
- **Shared knowledge**: ui mounts the three handlers at `/auth/login`, `/auth/callback`, `/auth/logout` (session builds the
  OAuth redirect URL from the callback path) and wraps logout in the CSRF check. Every other handler calls `User`, then
  `Authorize`. `ErrForbidden` is the sentinel `Authorize` returns for "not permitted". `ui.New` constructs the child itself:
  `session.New(session.Config{CookieKey, BaseURL}, d.OAuth, d.GitHub, d.GitHub, d.Now)`, with the github/api `Client` serving as
  `Users` and `Repos`. A `session.New` error is returned from `ui.New` unchanged. ui hands the cookie key over and never uses it.

<!-- contract: Sessions, User — restated from internal/control/ui/session/module.md -->
```go
type User struct {
    Login  string
    UserID int64
    Token  string // OAuth access token; lives only in the encrypted, signed cookie
}

type Sessions interface {
    // LoginHandler and CallbackHandler run the GitHub OAuth flow with a state cookie.
    LoginHandler() http.Handler
    CallbackHandler() http.Handler
    LogoutHandler() http.Handler
    // User returns the signed-in user, or false.
    User(r *http.Request) (User, bool)
    // End deletes the session cookie from the response.
    End(w http.ResponseWriter)
    // Authorize returns the repository if the user has at least minPermission ("read" | "write" | "admin")
    // on it right now (cached for 60 seconds). The Repo carries the authoritative installation ID.
    // A user token that GitHub rejects gives an error that wraps api.ErrUserTokenRejected: the caller ends the session.
    Authorize(ctx context.Context, u User, fullName, minPermission string) (Repo, error)
}
```

<!-- contract: New, Config, ErrForbidden — restated from internal/control/ui/session/module.md -->
```go
var ErrForbidden = errors.New("session: not permitted")

type Config struct {
    CookieKey []byte // 32 bytes, from a secret reference
    BaseURL   string
}

// New fails unless CookieKey is 32 bytes and BaseURL is an absolute https URL (http allowed for localhost).
func New(c Config, o OAuth, u Users, repos Repos, now func() time.Time) (Sessions, error)
```

### internal/control/ui/graph (child)

- **Counterpart**: `internal/control/ui/graph/module.md`
- **Direction**: ui depends on graph
- **Strength**: functional (composition)
- **LCA / Rank / Distance**: control/ui / 1 / 1
- **Volatility**: high
- **Balanced?**: yes — parent and child
- **Shared knowledge**: ui supplies a decoded `Report` and the policy text, gets a `View`, serializes it as JSON into
  the page, links each node to the policy file on GitHub (`policy_href#L<PolicyLine>`) when the line is not 0, shows the stale banner
  when `Stale`, and the incomplete banner (`id="incomplete"`) when `Incomplete`. The web app uses a link's `Status`
  as its CSS class and prints it, an accepted one with its `Accepted` ("accepted violation", "accepted advisory").

<!-- contract: View, Node, Link, LinkStatus, Build — restated from internal/control/ui/graph/module.md -->
```go
type Node struct {
    Name       string `json:"name"`
    Layer      string `json:"layer"`
    PolicyLine int    `json:"policy_line"` // 1-based line of the module in the policy YAML; 0 if not found
    // Volatility and Subdomain are what the policy the report was computed from declares for the module. Volatility is
    // VolatilityUndeclared when it declares neither. Both are "" when the policy text is not the report's (View.Stale)
    // or does not name the module: the node then claims nothing. A seam's volatility is no module fact: on a
    // functional dependency the engine takes the worse of its two ends.
    Volatility string `json:"volatility"`
    Subdomain  string `json:"subdomain"`
}

// VolatilityUndeclared is Node.Volatility for a module whose policy declares neither a volatility nor a subdomain.
const VolatilityUndeclared = "undeclared"

// LinkStatus is what the report says about a module pair: the engine's seam policy status when the report carries
// one, raised to advisory or accepted by the findings on the pair; else what the findings on the pair say. It is a
// display fact, never a check input.
type LinkStatus string

const (
    StatusViolation LinkStatus = "violation" // an active gate finding (wire/report Finding.Active), or a gate finding of unknown status, names the pair
    StatusAccepted  LinkStatus = "accepted"  // a finding accepted by the baseline or a waiver names the pair (report.Group's Accepted)
    StatusAdvisory  LinkStatus = "advisory"  // another finding that is not fixed and not a violation names the pair
    StatusAllowed   LinkStatus = "allowed"   // engine seam policy only: a fail-gated allowlist or layer rule permits the pair
    StatusObserved  LinkStatus = "observed"  // engine seam policy: no active finding names the pair and no rule permits it; without one: no current finding names the pair
)

type Link struct {
    From   string     `json:"from"`
    To     string     `json:"to"`
    Status LinkStatus `json:"status"`
    Count  int        `json:"count"` // observed dependencies of the pair's seams; 0 when only a finding names the pair
    // Origin aggregates finding evidence: "added" contains introduced findings, "existing" contains pre-existing
    // findings, "" has no comparable evidence. It never dates the pair or every source site. Display only.
    Origin string `json:"origin"`
    // The coupling of the pair's strongest seam, as the engine measured it; all "" when no seam names the pair.
    Strength   string `json:"strength"`   // engine token: contract, model, functional, symmetric, intrusive, unknown
    Distance   string `json:"distance"`   // in words (report.DistanceWords)
    Volatility string `json:"volatility"` // engine token, the seam's: on a functional dependency the worse end's
    // VolatilitySource is the engine's volatility_provenance of that seam: declared, inherited, cascade, undeclared.
    VolatilitySource string `json:"volatility_source"`
    Imbalance        string `json:"imbalance"` // the highest seam severity of the pair, the engine's imbalance; "" for none
    // Accepted is what the accepted findings on the pair accept: AcceptedViolation when one is a gate finding, else
    // AcceptedAdvisory; "" when none names the pair. The page names it, so gate debt never reads as an advisory.
    Accepted string `json:"accepted"`
    // The balance reading of the pair's strongest seam: the engine's hypothesis in the PR comment's words
    // (report.BalancingMove: a move, or why none is needed), quadrant, and raw_distance.basis split at "@" into the
    // boundary it crosses and the module holding both ends, or "system". All "" when no seam names the pair or the
    // engine gives none.
    Move      string `json:"move"`
    Quadrant  string `json:"quadrant"`
    Crossing   string `json:"crossing"`  // module_boundary, ownership, deploy_unit, declared_external
    Container  string `json:"container"` // a module name or "system"
    // DistributedMonolith is the engine's distributed_monolith flag on any seam of the pair: a scored dependency of
    // intrusive strength, or functional or symmetric from a role that is not cohesive, across a module boundary at high
    // volatility. Only a deploy-unit Crossing is called a distributed monolith on the page, as in the engine's coupling
    // gate reason.
    DistributedMonolith bool `json:"distributed_monolith"`
}

// Link.Accepted values.
const (
    AcceptedViolation = "violation"
    AcceptedAdvisory  = "advisory"
)

// View is a read-only picture. Stale is true when the policy text no longer matches the report's config_hash.
// Incomplete is true when a hard gate or required rule was not evaluated or a dimension the gate applies to measured
// nothing: an observed link may then still break a rule. A failed hard gate is not incomplete.
type View struct {
    Nodes      []Node        `json:"nodes"`
    Links      []Link        `json:"links"`
    Findings   []LinkFinding `json:"findings"` // what names each link, for the page's inspector; display only
    Stale      bool          `json:"stale"`
    Incomplete bool          `json:"incomplete"`
}

// LinkFinding is one finding that names a drawn link's module pair, as the engine reported it. It explains a link and
// never decides its Status. Link.Origin aggregates finding evidence on the pair, not pair/site novelty.
// Why is engine text: the page must treat it as untrusted.
type LinkFinding struct {
    From   string `json:"from"`
    To     string `json:"to"`
    RuleID string `json:"rule_id"`
    Rule   string `json:"rule"` // report.RuleName: the readable name, as on the pull request page and the comment
    Meta   string `json:"meta"` // report.FindingMeta: severity and status, as there
    Kind   string `json:"kind"`
    Status string `json:"status"`
    Why    string `json:"why"`
    More   int    `json:"more"` // on the last listed finding of a pair: how many more name the pair and are not listed
}

// Build is pure. Nodes come from the policy's modules, links from the report's seams and findings. A link's Status
// starts at the engine's seams[].policy when a seam of the pair carries it. The findings that name the pair raise it:
// on a pair the engine gave a policy, only to advisory or accepted, since the engine's "observed" means no active
// finding and says nothing about advisories and accepted debt; the engine decides its violations. Graph never
// computes a status from the policy text.
func Build(rep Report, policy []byte) View
```

### internal/control/account

- **Counterpart**: `internal/control/account/module.md`
- **Direction**: ui depends on account
- **Strength**: contract
- **LCA / Rank / Distance**: control / 1 / 1
- **Volatility**: high
- **Balanced?**: yes
- **Shared knowledge**: `Account.Status` for the banner, and `Account.Admit` and `Account.Unadmit` for the operator's
  approval (ui calls no other `Account` method); the operator check is ui's, `account` trusts it; `Consents.Record`, `Revoke`, `Has`, `Get`.

<!-- contract: Account, Status, Plan — restated from internal/control/account/module.md -->
```go
type Plan string

const (
    PlanPublic    Plan = "public"    // no private repositories: free
    PlanTrial     Plan = "trial"     // 30 days from the first private-repository installation
    PlanGrace     Plan = "grace"     // 7 days after the trial
    PlanGranted   Plan = "granted"   // manual beta grant
    PlanSuspended Plan = "suspended"
)

type Status struct {
    Plan         Plan
    TrialEnds    time.Time
    SuspendAfter time.Time
    GrantUntil   time.Time
}

type Account interface {
    // Observe records that the installation has (or no longer has) private repositories and
    // starts the trial on the first private one. It rewrites the gate's Entitlement record.
    Observe(ctx context.Context, installationID int64, hasPrivate bool, now time.Time) error
    Status(ctx context.Context, installationID int64, now time.Time) (Status, error)
    // Grant sets a manual beta grant. Operator-only; audited in the log by installation ID.
    Grant(ctx context.Context, installationID int64, until time.Time, now time.Time) error
    // Refresh recomputes the Entitlement record of one installation (called by the sweep).
    Refresh(ctx context.Context, installationID int64, now time.Time) error
    // Forget deletes every key of the installation (uninstall), its admission included.
    Forget(ctx context.Context, installationID int64) error
    // Admit records an operator's approval of the installation: the gate serves it from the next event on.
    // Operator-only; audited in the log by installation ID and operator.
    Admit(ctx context.Context, installationID int64, a gate.Admission) error
    // Unadmit deletes the approval: the gate serves nothing for the installation from the next event on.
    // Unadmitting an installation without an approval is not an error.
    Unadmit(ctx context.Context, installationID int64, byID int64) error
}
```

<!-- contract: Consents, Consent, ConsentKind, ErrNoConsent — restated from internal/control/account/module.md -->
```go
type ConsentKind string

const (
    ConsentSourceProcessing ConsentKind = "source_processing" // managed discovery
)

type Consent struct {
    Kind ConsentKind
    By   string // GitHub login of the administrator, as it was when the consent was recorded
    ByID int64  // GitHub user ID of the administrator; 0 on a record written before the App stored it
    At   time.Time
}

// ErrNoConsent is returned by callers' guards when Has is false.
var ErrNoConsent = errors.New("account: consent not recorded")

// ErrConsentExists is returned by Record when a consent of that kind is already recorded; the record is unchanged.
var ErrConsentExists = errors.New("account: consent already recorded")

type Consents interface {
    // Record creates the consent atomically; ErrConsentExists if one is recorded, which keeps who and when.
    Record(ctx context.Context, installationID int64, c Consent) error
    Revoke(ctx context.Context, installationID int64, k ConsentKind) error
    // Has is false on any error: no consent, no call.
    Has(ctx context.Context, installationID int64, k ConsentKind) bool
    // Get answers who recorded the consent and when. Unlike Has it tells "not recorded" (false, nil) from "cannot
    // tell" (an error: the Store failed or the record is corrupt), so a page never shows an outage as "not allowed".
    Get(ctx context.Context, installationID int64, k ConsentKind) (Consent, bool, error)
}
```

### internal/control/onboarding

- **Counterpart**: `internal/control/onboarding/module.md`
- **Direction**: ui depends on onboarding
- **Strength**: contract
- **LCA / Rank / Distance**: control / 1 / 1
- **Volatility**: high
- **Balanced?**: yes
- **Shared knowledge**: `Progress`, `StartCustomer`, `StartManaged`. ui never mounts `DiscoveryHandler` or `BaselineHandler` (the façade does).
  Before `StartManaged` ui checks `Consents.Has(ConsentSourceProcessing)` and sends the administrator to the Data sharing page
  when it is false **[default]**. `ErrNoSandbox` is the sentinel onboarding returns when managed discovery is not
  configured; ui shows "managed discovery is not available". `ErrAlreadyOnboarded` and `ErrBusy` are the sentinels
  of a refused start; ui maps all three to `409` (Status mapping), never to the `502` of an upstream failure.

<!-- contract: Onboarding, Stage, Progress — restated from internal/control/onboarding/module.md -->
```go
type Stage string

const (
    StageInstalled      Stage = "installed"
    StageWorkflowMerged Stage = "workflow_merged"
    StageReportReceived Stage = "report_received"
    StagePolicyMerged   Stage = "policy_merged"
    StageAdvisory       Stage = "gate_advisory"
    StageRequired       Stage = "gate_required"
)

// Progress is derived from GitHub on every call; nothing is stored.
type Progress struct {
    Stage      Stage
    Diagnostic string // what to do next
    OpenDraft  *Draft
}

type Onboarding interface {
    Progress(ctx context.Context, r Repo) (Progress, error)
    // StartCustomer opens PR 1: the settings file with owners and CODEOWNERS entries. The owner adds the workflow.
    StartCustomer(ctx context.Context, r Repo, owners []string) (Draft, error)
    // StartManaged requires ConsentSourceProcessing, opens the onboarding PR and runs the sandbox job.
    StartManaged(ctx context.Context, r Repo, owners []string) (Draft, error)
    // DiscoveryHandler serves POST /v1/discoveries and opens PR 2 with the proposed policy.
    DiscoveryHandler() http.Handler
    // BaselineHandler serves POST /v1/baselines and opens or updates the baseline PR. It never writes the default branch.
    BaselineHandler() http.Handler
}
```

<!-- contract: ErrAlreadyOnboarded, ErrBusy — restated from internal/control/onboarding/module.md -->
```go
// ErrAlreadyOnboarded is returned by StartCustomer and StartManaged when Progress is past StageInstalled.
var ErrAlreadyOnboarded = errors.New("onboarding: already onboarded")

// ErrBusy is returned by StartManaged when this process already runs its maximum of managed jobs.
var ErrBusy = errors.New("onboarding: busy, retry later")
```

### internal/control/passkey

- **Counterpart**: `internal/control/passkey/module.md`
- **Direction**: ui depends on passkey
- **Strength**: contract
- **LCA / Rank / Distance**: control / 1 / 1
- **Volatility**: high (this side), low (passkey)
- **Balanced?**: yes
- **Shared knowledge**: `Passkeys` and its sentinels, `User`, `Purpose`. ui supplies the user from the session and the
  repository from `Authorize`; passkey supplies the browser's options as JSON and never sees a request or a cookie.
  The answers of M2 task 19 redeem the token; ui never calls `Redeem`.

### internal/control/proposal

- **Counterpart**: `internal/control/proposal/module.md`
- **Direction**: ui depends on proposal
- **Strength**: contract
- **LCA / Rank / Distance**: control / 1 / 1
- **Volatility**: high (this side), low (proposal)
- **Balanced?**: yes
- **Shared knowledge**: only the `DraftState` words (draft, awaiting review, published, closed), to word the onboarding
  draft that `Progress` reports. ui calls no `Proposals` method and builds no `Change`.

<!-- contract: DraftState — restated from internal/control/proposal/module.md -->
```go
type DraftState string

const (
    DraftNone     DraftState = "none"
    DraftOpen     DraftState = "draft"           // draft PR
    DraftInReview DraftState = "awaiting_review" // ready for review, not merged
    DraftMerged   DraftState = "published"
    DraftClosed   DraftState = "closed"
)
```

### internal/github/api

- **Counterpart**: `internal/github/api/module.md`
- **Direction**: ui depends on github/api
- **Strength**: contract
- **LCA / Rank / Distance**: root / 2 / 2
- **Volatility**: high (this side), low (github/api)
- **Balanced?**: yes — contract coupling
- **Shared knowledge**: `Deps.GitHub` is used as `Repos` (`File`, `BranchHead`; and whole, with `Users`, as the
  argument of `session.New`), `Checks` (`CheckRuns`, read only), `Pulls` (`OpenPulls`), `Actions` (`LatestRun`,
  `Dispatch`), `Users` (`UserRepos`), and the `*Client` readers outside the role interfaces `PullSummary`, `ArtifactID`
  and `ArtifactByID`, which ui declares as its own small interfaces. `ErrNotFound` is the sentinel for a
  missing file, run or artifact; `ErrRateLimited` is the sentinel after bounded backoff. `TokenSource` is an opaque part
  of `New`'s signature for this module. `Pull` is the github/api pull-request record; ui reads it without the merge
base. `WithCallStats` counts the GitHub round trips of one request for its log line. The check names
  `archfit/state` and `archfit/gate` and the artifact name `archfit-report` are GitHub objects of the root contract.

<!-- contract: RepoRef, Repo, ErrNotFound, ErrRateLimited, ErrUserTokenRejected — restated from internal/github/api/module.md -->
```go
// RepoRef addresses a repository inside an authenticated installation.
// Callers build it only from a verified webhook, verified OIDC claims, or an authorized session.
type RepoRef struct {
    InstallationID int64
    FullName       string // "owner/name"
}

type Repo struct {
    RepoRef
    ID            int64
    Private       bool
    DefaultBranch string
}

var (
    ErrNotFound    = errors.New("github: not found")
    ErrRateLimited = errors.New("github: rate limit exhausted") // returned after bounded backoff

    // ErrUserTokenRejected is GitHub's 401 on a user's OAuth token: it expired or was revoked. The token cannot
    // recover, so the call is not repeated and the user must sign in again.
    ErrUserTokenRejected = errors.New("github: user token rejected")
)
```

<!-- contract: Repos, Installation — restated from internal/github/api/module.md -->
```go
type Installation struct {
    ID          int64
    Account     string    // organization or user login
    AccountType string    // "Organization" or "User"
    CreatedAt   time.Time // when the App was installed
}

// Repos reads installations, repositories and file contents.
type Repos interface {
    ListInstallations(ctx context.Context) ([]Installation, error)
    ListRepos(ctx context.Context, installationID int64) ([]Repo, error)
    // ResolveRepo finds the installation that covers fullName using the App JWT.
    ResolveRepo(ctx context.Context, fullName string) (Repo, error)
    // File returns the raw bytes and git blob SHA of path at ref. ErrNotFound if absent.
    File(ctx context.Context, r RepoRef, ref, path string) (content []byte, blobSHA string, err error)
    // BranchHead returns the head commit SHA of branch. ErrNotFound if the branch does not exist.
    BranchHead(ctx context.Context, r RepoRef, branch string) (string, error)
}
```

<!-- contract: Checks, CheckRun, Annotation — restated from internal/github/api/module.md -->
```go
type Annotation struct {
    Path    string
    Line    int
    Level   string // "failure" | "warning" | "notice"
    Message string
}

type CheckRun struct {
    ID          int64 // 0 on create
    Name        string
    HeadSHA     string
    ExternalID  string
    DetailsURL  string // the "Details" link GitHub shows on the check; "" = GitHub's default
    Status      string // "in_progress" | "completed"
    Conclusion  string // "" unless completed
    Title       string
    Summary     string // markdown, max 65535 bytes
    Annotations []Annotation
    StartedAt   time.Time
    // AnnotationCount is read-only: the annotations GitHub holds for the run, set by CheckRuns.
    // Create and update never send it; GitHub appends the Annotations of each request.
    AnnotationCount int
}

type Checks interface {
    // CheckRuns returns only check runs created by this App.
    CheckRuns(ctx context.Context, r RepoRef, headSHA, name string) ([]CheckRun, error)
    CreateCheckRun(ctx context.Context, r RepoRef, c CheckRun) (CheckRun, error)
    UpdateCheckRun(ctx context.Context, r RepoRef, c CheckRun) error
}
```

<!-- contract: Actions, WorkflowRun — restated from internal/github/api/module.md -->
```go
type WorkflowRun struct {
    ID           int64
    Attempt      int
    Event        string
    HeadSHA      string
    HeadRepo     string
    WorkflowPath string // e.g. ".github/workflows/archfit.yaml"
    Status       string
    Conclusion   string
    PullRequests []int // PR numbers GitHub associates with the run
}

// RunFilter narrows LatestRun. Each non-empty field is sent as the GitHub query parameter of the same name.
type RunFilter struct {
    Branch string
    Event  string // e.g. "push", "workflow_dispatch"
    Status string // a run status or conclusion, e.g. "completed", "success"
}

type Actions interface {
    Run(ctx context.Context, r RepoRef, runID int64) (WorkflowRun, error)
    Rerun(ctx context.Context, r RepoRef, runID int64) error
    Dispatch(ctx context.Context, r RepoRef, workflowPath, ref string, inputs map[string]string) error
    // LatestRun is the newest run of the workflow that matches every non-empty field of f.
    LatestRun(ctx context.Context, r RepoRef, workflowPath string, f RunFilter) (WorkflowRun, error)
    // Artifact downloads the named artifact of a run, capped at maxBytes.
    Artifact(ctx context.Context, r RepoRef, runID int64, name string, maxBytes int64) ([]byte, error)
}
```

<!-- contract: Users — restated from internal/github/api/module.md -->
```go
// Users answers questions with a user's OAuth token; nothing here uses App credentials.
type Users interface {
    // Permission returns "admin" | "write" | "read" | "none" for the user on the repository.
    Permission(ctx context.Context, userToken, fullName string) (string, error)
    // UserRepos lists repositories the user can access through App installations.
    UserRepos(ctx context.Context, userToken string) ([]Repo, error)
}
```

<!-- contract: Client, New — restated from internal/github/api/module.md -->
```go
// Client implements every role interface. Consumers depend on the role interfaces they need.
type Client struct{ /* unexported */ }

func New(ts TokenSource, hc *http.Client) *Client

// Readers outside the role interfaces, for the UI only; the gate and its children never call them.
func (c *Client) PullSummary(ctx context.Context, r RepoRef, number int) (Pull, error) // Pull without the merge base
func (c *Client) ArtifactID(ctx context.Context, r RepoRef, runID int64, name string) (int64, error)
func (c *Client) ArtifactByID(ctx context.Context, r RepoRef, id int64, maxBytes int64) ([]byte, error)

// CallStats counts the round trips sent under one context and their summed time; concurrent calls add up past
// the wall-clock time. Token minting is not counted. Safe for concurrent use.
type CallStats struct{ /* unexported */ }

func WithCallStats(ctx context.Context) (context.Context, *CallStats)
func (s *CallStats) Calls() int64
func (s *CallStats) Duration() time.Duration
```

### internal/wire/policy

- **Counterpart**: `internal/wire/policy/module.md`
- **Direction**: ui depends on wire/policy
- **Strength**: contract
- **LCA / Rank / Distance**: root / 2 / 2
- **Volatility**: high (this side), moderate (wire/policy)
- **Balanced?**: yes — contract coupling
- **Shared knowledge**: `PolicyPath` is the constant naming the file the graph reads. `Schema()` is served
  unchanged at `/schema/policy.json` for editors outside the App; no page of the App reads it. Only `wire/*` may import a
  YAML library. `Digest` is used to find the policy text that is the report's. `BaselinePath` and `LabelsPath` are the constants naming `.archfit-baseline.json` and
  `.archfit-labels.yaml`; ui never reads or writes either.

<!-- contract: PolicyPath, BaselinePath, LabelsPath, Digest — restated from internal/wire/policy/module.md -->
```go
// The engine's three trusted input files on the protected ref. Baseline and labels sit beside the policy.
const (
    PolicyPath   = ".archfit.yaml"
    BaselinePath = ".archfit-baseline.json"
    LabelsPath   = ".archfit-labels.yaml"
)

// Digest returns the bare lowercase hex SHA-256 of the raw file bytes. For the policy file it equals the
// engine's comparison.config_hash. The engine reports no digest for baseline or labels; for those the
// producer states Digest of the bytes it used in the envelope, and the App compares it with the protected ref.
func Digest(raw []byte) string
```

<!-- contract: Schema — restated from internal/wire/policy/module.md -->
```go
// Schema returns the engine v3.0.1 config JSON Schema (byte-identical to v3.0.0), embedded unchanged, for the editor.
func Schema() []byte
```

### internal/wire/settings

- **Counterpart**: `internal/wire/settings/module.md`
- **Direction**: ui depends on wire/settings
- **Strength**: contract
- **LCA / Rank / Distance**: root / 2 / 2
- **Volatility**: high (this side), moderate (wire/settings)
- **Balanced?**: yes — contract coupling
- **Shared knowledge**: ui reads `Path` at the default branch and calls `Parse` to learn `Workflow` (which run holds the
  report, which workflow to dispatch for discovery) and `Gate` (required or advisory mode). ui does not edit
  the settings file: wire/settings publishes no schema, and settings change through the onboarding PR or GitHub **[default]**.
  wire/settings owns the owner-entry rule (login or `org/team-slug`, the cap, no duplicate ignoring case): ui validates
  the submitted owners with `Parse(Render(Settings{…}))`, the way onboarding does, and holds no owner pattern or cap.

<!-- contract: Settings, GateMode, DataMode, Path — restated from internal/wire/settings/module.md -->
```go
// Path is the App settings file on the protected ref, beside the engine policy.
const Path = ".archfit-app.yaml"

type GateMode string

const (
    GateAdvisory GateMode = "advisory" // publish archfit/state and the comment only
    GateRequired GateMode = "required" // also publish archfit/gate, fail closed
)

type DataMode string

const (
    DataCustomer DataMode = "customer" // source never reaches Archfit
    DataManaged  DataMode = "managed"  // managed discovery and managed LLM allowed after consent
)

// Settings is the App-owned file. The engine policy schema is untouched by it.
type Settings struct {
    Version  int      `yaml:"version"`          // must be 1
    Owners   []string `yaml:"owners"`           // GitHub logins, or "org/team-slug"; at least one
    Agents   []string `yaml:"agents,omitempty"` // logins or App slugs of coding agents, which never approve; nil when none
    Gate     GateMode `yaml:"gate"`             // default advisory
    Workflow string   `yaml:"workflow"`         // e.g. ".github/workflows/archfit.yaml"
    DataMode DataMode `yaml:"data_mode"`        // default customer
}
```

<!-- contract: Parse, Render — restated from internal/wire/settings/module.md -->
```go
// Parse strictly decodes and validates a settings file. Unknown keys are errors.
func Parse(raw []byte) (Settings, error)

// Render returns canonical YAML for s. For any s returned by Parse, Parse(Render(s)) == s.
func Render(s Settings) []byte
```

### internal/wire/report

- **Counterpart**: `internal/wire/report/module.md`
- **Direction**: ui depends on wire/report
- **Strength**: contract (published language)
- **LCA / Rank / Distance**: root / 2 / 2
- **Volatility**: high (this side), moderate (wire/report)
- **Balanced?**: yes — contract coupling
- **Shared knowledge**: ui downloads the artifact with `MaxBytes` as the cap, calls `Decode`, and hands the `Report` to
  `graph.Build` unread. The artifact holds the raw engine state JSON; `Decode` validates it against the engine v3.0.0/v3.0.1 state
  schema and reduces it to the no-source form, so fields the decoder drops (measurement, `matched_by` but its
  `MatchedByKeys`, task declarations, metric provenance, seam scores) never reach a template and ui never renders them.
  Of `Finding.MatchedBy` ui reads the metric ratchet's `metric`, `before`, `after` and `threshold` (`metricSubject`)
  and a dependency finding's `locations_total` (through `report.LocationsTotal`). ui parses the artifact
  in no other way. The artifact comes without the envelope that names its release, so ui passes the last release of
  `EngineVersions`, `v3.0.1`; both releases share one key set. The pull request page also reads the decision
  (`HardGates`, `ActiveBlockers`, `UnevaluatedRequiredRules`), `Comparison.OriginReasons`, each seam's `Hypothesis`
  and each agent task's `FindingID` and `RepairKind`.
  `StateSchemaV1` is the constant `Decode` checks. For this module each nested type is opaque:
  - `Decision` is an opaque part of `Report` for this module.
  - `Comparison` is an opaque part of `Report` for this module.
  - `Reference` is an opaque part of `Report` for this module.
  - `Dimension` is an opaque part of `Report` for this module.
  - `Coverage` is an opaque part of `Report` for this module.
  - `Finding` is an opaque part of `Report` for this module.
  - `AgentTask` is an opaque part of `Report` for this module.
  - `Seam` is an opaque part of `Report` for this module.

<!-- contract: Report, StateSchemaV1, MaxBytes — restated from internal/wire/report/module.md -->
```go
// StateSchemaV1 is the engine state contract accepted (archfit.state.schema.json, engine v3.0.0 and v3.0.1: one file).
const StateSchemaV1 = "archfit.architecture-state.v1"

// MaxBytes caps an uploaded payload: 5 MiB (the "5 MB" cap of decisions.md).
const MaxBytes = 5 << 20

// Report is the no-source transport form of an engine state report. The decoder knows every key of the engine
// state schema of v3.0.0 and v3.0.1: keys listed here are kept (length-capped), the other known keys are validated and dropped
// (measurement, findings[].matched_by except MatchedByKeys, agent_tasks[].declarations, metric provenance, seam scores),
// and any key the schema does not define is a decode error.
type Report struct {
    SchemaVersion string               `json:"schema_version"` // must equal StateSchemaV1
    Verdict       string               `json:"verdict"`        // "healthy" | "needs_attention" | "blocked"
    Decision      Decision             `json:"decision"`
    Comparison    Comparison           `json:"comparison"`
    GateReference *Reference           `json:"gate_reference,omitempty"` // persisted-baseline comparison; nil = engine emitted none
    Dimensions    map[string]Dimension `json:"dimensions"`               // exactly the nine engine dimension keys
    Coverage      Coverage             `json:"coverage"`
    Findings      []Finding            `json:"findings"`
    AgentTasks    []AgentTask          `json:"agent_tasks"`
    Seams         []Seam               `json:"seams"`
}
```

<!-- contract: Decode — restated from internal/wire/report/module.md -->
```go
// Decode strictly parses payload as the no-source transport of the state document engineVersion emits. It
// fails on: an engineVersion outside EngineVersions, size over MaxBytes, a key the state schema
// does not define, wrong schema_version, an enum value outside its set, strings over the per-field caps,
// absolute or parent-escaping paths, multi-line text, or duplicate finding IDs.
func Decode(payload []byte, engineVersion string) (Report, error)
```

<!-- contract: EngineVersions — restated from internal/wire/report/module.md -->
```go
// EngineVersions returns the engine releases whose state document Decode reads: v3.0.0 and v3.0.1, one key set. It emits
// StateSchemaV1.
func EngineVersions() []string
```

### internal/control (parent façade)

- **Counterpart**: `internal/control/module.md`
- **Direction**: control depends on ui (calls `New`, exposes the handler as `UIHandler`)
- **Strength**: functional (composition)
- **LCA / Rank / Distance**: control / 1 / 1
- **Volatility**: high
- **Balanced?**: yes — parent and child
- **Shared knowledge**: `Deps` and `New` above. `control` passes its `OAuth`, `CookieKey`, `BaseURL`, `Admissions`,
  `RequestViews` and `Operators` through, adds `Passkeys` and `Answers` it builds, and returns a
  `ui.New` error from its own constructor.

### internal/gate (façade)

- **Counterpart**: `internal/gate/module.md`
- **Direction**: ui depends on the gate façade (`Admissions`, `RequestViews`, `Target`, the answer outcomes and refusals,
  `ClassifyDisplayReason`, `FirstScreen`, `RecoveryStep`); never on a gate child
- **Strength**: contract
- **LCA / Rank / Distance**: root / 2 / 2
- **Volatility**: high
- **Balanced?**: yes
- **Shared knowledge**: `RequestViews` for the request page and its answer preview, always with the `Target` of the
  authorized repository; the refusals are mapped to wording here. Who may answer, what the digests are and when an
  answer holds are the gate's rules: ui shows them and sends back what it was shown. `FirstScreen` is fed what the
  check recorded and answers lines of plain text: ui adds no wording of its own to the first screen.

### internal/github/auth

- **Counterpart**: `internal/github/auth/module.md`
- **Direction**: ui depends on github/auth (type only)
- **Strength**: contract
- **LCA / Rank / Distance**: root / 2 / 2
- **Volatility**: high (this side), low (github/auth)
- **Balanced?**: yes — contract coupling
- **Shared knowledge**: ui receives an `OAuth` in `Deps` and hands it to `session.New`. ui calls none of its methods.

<!-- contract: OAuth, UserToken — restated from internal/github/auth/module.md -->
```go
type UserToken struct {
    AccessToken string
    Login       string
    UserID      int64
}

// OAuth is the GitHub user login flow.
type OAuth interface {
    AuthorizeURL(state, redirectURL string) string
    Exchange(ctx context.Context, code string) (UserToken, error)
}
```

## Internal Design

### Composition

- `ui.New` builds `session` first and fails if it fails; nothing else in `New` can fail.
- `session` turns a request into a `User` and a repository name into an authorized `Repo`. It is the only source of
  tenant identity in the UI.
- `graph` turns a `Report` and policy text into a `View`. Pure.
- ui's own code is handlers, the JSON API (`api.go`), the web app's server side (`spa.go`: the shell, its error pages,
  the build's assets and the licence file, embedded with `go:embed`; no vendored library) and the handler chain. The two
  children do not know each other.
- The web app's source is `web/` at the repository root: Svelte 5 and TypeScript, built by plain Vite (not SvelteKit,
  whose bootstrap is an inline script). `npm run build` writes `spa/` (`index.html` and `assets/`), embedded with
  `//go:embed all:spa`. The build is not committed: `spa/` holds only its `.gitignore`, and then the shell answers a
  page that says to run `make web`, so `go build` and `go test` need no Node. The release image builds it
  (`Dockerfile`, stage `web`). Styles are one stylesheet, `web/src/app.css`, the tokens and components of
  `docs/archive/design/prototypes/archfit.css`; no CSS framework and no runtime style injection (no Svelte transitions).
- The web app has no policy editor. The `@codemirror/*` packages stay in `web/package.json` for the YAML edit of M3
  task 10, and the shell keeps its style nonce for it; nothing in the app reads either today.

### Handler chain

1. Recover panics → `500`; log route pattern and request ID only.
2. Security headers: `Content-Security-Policy: default-src 'none'; script-src 'self'; style-src 'self';
   connect-src 'self'; img-src 'self' data:; font-src 'self'; base-uri 'none'; form-action 'self';
   frame-ancestors 'none'; object-src 'none'; worker-src 'self'` (no inline style, no inline script, no eval, no
   worker but a same-origin file, no other origin: every style is a class in a stylesheet, Vite's modulepreload
   polyfill, an inline script, is off, and the one worker is the graph's layout, a hashed build file, never `blob:`),
   `X-Content-Type-Options: nosniff`, `Referrer-Policy: same-origin` **[default]**. The shell replaces the CSP with the
   same string plus `'nonce-<n>'` in `style-src` alone, where `<n>` is 16 bytes from `crypto/rand` in base64, new on every
   response; it writes `<n>` over the `__CSP_NONCE__` of `<meta name="csp-nonce">` in `index.html` and answers with
   `Cache-Control: no-store`, so no cache keeps a nonce. `script-src` takes no nonce, and there is no
   `'unsafe-inline'` or `'unsafe-hashes'`. `web/csp.txt` is the shell's policy with the placeholder nonce, for the web
   app's preview server and smoke test, which serve `index.html` as built.
3. `http.MaxBytesReader` at 512 KB **[default]** on every `POST`.
4. `Sessions.User`. Missing → login redirect, `401`, or `401` JSON on `/api/`. A later `ErrUserTokenRejected` from `Authorize` or `UserRepos`
   calls `Sessions.End` and answers the same way.
5. `POST` only: constant-time compare of form field `csrf` (Sign out) or header `X-CSRF-Token` (`/api/`, after the
   `application/json` check) with the token derived from `User.Token`.
6. `/api/repos/{o}/{n}/…`: `Sessions.Authorize(user, "{o}/{n}", min)` on every request. The returned `Repo` is handed
   to the handler as an argument and is the only repository identity it may use. A repository page stops at step 4.
7. `/api/repos/{o}/{n}/…`, after step 6: `Admissions.Admitted(repo.InstallationID, owner of repo.FullName)`; false stops
   the request with the waiting `403`, an error stops it with the generic `502`. Authorization comes first, so a user who cannot see the repository still gets
   the `404`, and only a user who can see it learns that its account waits.
8. `/api/admin/…`: the user's ID must be in `Operators`, else `404`; the handler then looks the path's installation up among
   `ListInstallations`. Steps 4 and 5 apply as for any `POST`.

Every response carries an `X-Request-Id` header with the request ID of its log line **[default]**. That line, `ui request`
at `INFO`, one per request, also carries `gh_calls` and `gh_ms`: the GitHub round trips the request made (session's
included, token minting not) and their time summed, so calls that ran side by side add up past `duration_ms`. Numbers
only: never a URL, a route of GitHub's or a token.

### Report cache

- The report artifacts this instance downloaded, kept by bytes: at most 32 MiB **[default]**, least recently used out
  first, of a 512 MiB instance (`infra/runtime/services.tf`). An artifact alone over the bound is never kept. An
  artifact never changes, so an entry never goes stale and nothing expires it.
- Key: installation ID, repository full name and artifact ID. One repository never reads another's artifact, whatever
  IDs GitHub hands out.
- It sits behind the per-request authorization: a handler reads it only with the `Repo` its route authorized. Every
  request still lists the run's artifacts (`ArtifactID`) as the installation, so a removed repository or an expired
  artifact is GitHub's answer, never the cache's; only the download (`ArtifactByID`) is saved.
- Payloads are shared and never modified. The pull request page checks the SHA-256 of the bytes against its check
  on every request, hit or miss.

### Reads side by side

Independent GitHub reads of one request run concurrently, and each part fails the answer exactly as when they ran in
turn: the results are judged in the old order. A panic in a part is raised again on the request's goroutine once the
others are done, so the handler chain's recover still answers `500` and the process keeps serving.

- Pull request page and graph: after `PullSummary`, the settings on the base and the report (check, artifact, digest,
  decode) at once (with a hint, settings and check reads begin beside `PullSummary`, but artifact reads wait for
  the fresh check: Reads made ahead); the graph reads the policy at the head and at the base beside them. A settings error other than
  none fails the answer first, then the report's own state. The base's policy text or failure counts only when the
  head's text is not the report's, and neither counts without a report.
- Repository graph: the report and the policy text on the default branch at once; the text
  counts only once there is a report.
- Status: `Onboarding.Progress`, the `admin` check, the default-branch state, the open
  pull requests and `Account.Status` at once. The default-branch state and the pull requests count only once the
  progress says a report came in: before one they are read on the chance and dropped, up to 23 calls (the branch head
  and its check, the open pull requests and the check of each of up to 20) spent so the page waits for its slowest
  read, not their sum. Only the progress fails the page.
- The artifact listing (`ArtifactID`) runs on every request: it is GitHub's answer to whether the report still
  exists (it expires, or its owner deletes it), so the report cache only ever replaces the download, never the
  listing **[decided, g3]**.
- Tests prove the overlap without timing: the fake GitHub holds the named reads until all of them are in flight, so
  reads made in turn never meet (`TestIndependentReadsMeet`).

### PR diagnostics and serving build

Both PR API views share optional `check` context on `pullHead`. This is display-only context from the App-authored
check, independent of report admission. The 65535-byte-bounded parser reads only the recovery prelude and Inputs and
identity section, not findings or folds. Text is HTML-unescaped then rendered as escaped Svelte text, never raw
Markdown or HTML. Current approved manifest pins are recovery guidance for producer/workflow rejection only.

Context run identity, attempt, full head revision and report/input digests remain available under action-required.
Missing historical Action SHA, analysis time and base revision are explicit in the UI, never inferred from today's
manifest or App build. Check-start time is labelled separately. Report schema comes only from an admitted artifact.
`pullBody.RunID` remains the admitted report identity; context RunID cannot populate artifact hints. An
action-required result returns a report only when every reason of its check's summary is a known, report-derived one
(`verdict.ClassifyDisplayReason`, the rule the comment's `presentation` uses); otherwise it returns nil report. Findings and Graph have distinct unavailable capability states;
admitted partial evidence is marked, not described as passing.

### Reads made ahead

A chain whose reads each take the last one's answer as a parameter starts them all at once with the parameters the
previous request of the same chain found, and counts each only when the fresh read it depends on names the same
**[decided, g4]**. Independent hinted reads overlap; a stale hint costs the reads made ahead and then the chain in
turn, never a wrong page. PR artifact reads deliberately wait for the fresh check: a saved accepted run may have
been withdrawn since the previous request.

- Pull request page and graph: `PullSummary` beside the settings at the hinted base, `CheckRuns` at the hinted head,
  and the graph's policy texts at the hinted head and base. Each counts only when the fresh pull request names the
  same base and head. Only a fresh non-action-required check can start the PR artifact read, never a diagnostic run ID.
- Findings and repository graph: the settings beside `LatestRun` of the hinted workflow and `ArtifactID` of the hinted
  run, each counted only when the fresh settings name the same workflow and its fresh latest run is the same run.
- Every fresh read runs on every request, `ArtifactID` included: the hint never replaces a read, it only lets one
  start early. An artifact GitHub no longer lists reads as gone with the hint and the report cache warm.
- Hints hold parameters only (a base branch, a head SHA, a workflow path, a run ID), never an answer: at most 4096
  **[default]**, least recently used out first, keyed by installation ID, repository full name and pull request number
  (0 for the default branch). They sit behind the per-request authorization, as the report cache does, and one
  repository's hints never become another's reads. A chain the fresh reads say is gone (no settings, no pull request)
  drops its hint, and one without a run keeps only the workflow, so it stops costing reads made ahead.
- Status: `onboarding.Progress` makes its own reads ahead (onboarding module, Progress), so the overview waits for
  its slowest part, the default-branch state or the open pull requests: two reads in turn each.
- Tests: the warm chains meet at the fake GitHub's barrier (`TestWarmReadsMeet`), a hint the fresh reads contradict
  is read again (`TestStaleHintIsReadAgain`), hints stay with their repository (`TestHintsStayWithTheirRepository`).

### Pages

- Overview: `Users.UserRepos(user.Token)`; the list is already scoped by GitHub to what the user can access, so no
  per-repository `Authorize` is needed. Each installation is asked `Admitted` once per request; a repository of one
  that is not admitted gets the row "waiting for approval" and no `BranchHead` or `CheckRuns` call. At most 100 repositories **[default]**, sorted by name, 8 lookups in parallel **[default]**. Per repository:
  `Repos.BranchHead(repo.RepoRef, repo.DefaultBranch)` → `Checks.CheckRuns(head, "archfit/state")`.
  A default-branch upload publishes `archfit/state` only; `archfit/gate` lives on pull requests. No check run on the head: "no state yet". A failed lookup: "unknown" for that
  row only. Conclusions are shown as words with their reason title. No score, no totals, no ranking.
- Status: `Onboarding.Progress(repo)` (stage, diagnostic, open draft link), `Account.Status(repo.InstallationID, now)` banner:
  trial end date, grace, suspended with the recovery sentence; nothing for `public` and `granted`. A `Status` error hides the
  banner and is logged; it never fails the page. History is a link to the repository's pull requests from `archfit/` branches
  and to its check runs **[default]**: report history is what GitHub keeps. The route needs `read`; the page calls
  `Sessions.Authorize(user, "{o}/{n}", "admin")` a second time and shows the onboarding and discover controls
  only when it succeeds. Its error is not an error of the page; the `POST` routes enforce `admin` themselves.
  Setup steps: the stages of the onboarding path with the current one marked ("First report" is done only once a
  report arrived: `policy_merged` can come without one), and fixed advice at `gate_advisory` (rehearse, then the
  settings pull request and the ruleset). At `gate_required` GitHub enforces the gate and there is no step left; a gate
  that settings call required but GitHub does not require (or the App cannot confirm) stays at `gate_advisory` and shows
  the `Enforcement` wording with the ruleset to add. At `report_received`, `gate_advisory`
  and `gate_required` the page also reads `archfit/state` on the
  default-branch head (the overview's word and title) and `Pulls.OpenPulls`, newest first:
  at most 20 **[default]**, the rest counted, one `CheckRuns(head, "archfit/state")` each, at most 8 at a time; each
  row links to `/r/{o}/{n}/pull/{number}`. A failed list shows fixed wording and never fails the page; a failed
  lookup is "unknown" for its row only. The same check run gives the watch line (the run's start time and the tip's
  short SHA; none without a run) and the re-run state: a run that concludes `action_required` whose summary records
  `target_moved` gives the row `rerun`, `gate.RecoveryStep("target_moved", recorded next step)` (the comment's role
  prefix and sanitized words, so the modules are named without parsing the detail; a fixed sentence when no step was
  recorded).
- Onboarding start: `owners` is split into lines, each trimmed, blank lines dropped: that is all ui decides. The list
  is valid when `settings.Parse(settings.Render(Settings{Version: 1, Owners, Gate: GateAdvisory, Workflow, DataMode:
  DataCustomer}))` succeeds; `Gate`, `Workflow` (a fixed placeholder path) and `DataMode` only make the probe document
  complete and are never written. Any error, an empty list included, is `422` with the fixed wording "Name at least one
  owner. Owners are GitHub logins or org/team-slug, one per line, each named once." **[default]**, never `Parse`'s error
  text, and onboarding is not called. Then `mode`: `customer` → `StartCustomer`; `managed` → consent check →
  `StartManaged`; anything else `422`.
- Discover: requires stage `workflow_merged` or later; `Actions.Dispatch(Settings.Workflow, DefaultBranch, {"mode": "discovery"})`.
- Pull request page: `PullSummary(number)` → the settings on its base branch (`gateRequired`: the gate is required only
  when they say so), then `CheckRuns(head, "archfit/state")` and the latest result.
  When the run is report-derived and a report is shown, `gate.FirstScreen` words the first screen from the recorded
  reasons, the policy digest and producer engine of the summary's inputs section, the run and attempt of the external ID,
  the check run ID, the gate mode and the policy change card section of the summary (bounded to 8 KiB), with the check's
  title as the headline. The page has no owner action; an owner answers on the request page.
  A run that concludes `action_required` shows no finding unless every reason its summary records is report-derived
  (missing required coverage, a baseline that is not comparable, a policy defect): the sweep keeps the old run in a
  withdrawn result's `external_id`, so its old report would still match, and in required mode it rewrites the summary with
  the reason of the withdrawal, which is not report-derived (a report-derived action_required counts as decided, so
  approval loss withdraws it as it does a success; advisory mode withdraws nothing, as for a neutral result before). An unknown reason, an unparsed summary or a mix keeps the report out. Its `external_id` is
  `v1:<run_id>:<attempt>:sha256:<hex>`; run 0 or an unreadable id is "no upload". Then
  `ArtifactID(run_id, "archfit-report")` and the download from the report cache or `ArtifactByID(id,
  report.MaxBytes)`, and the page shows it only when `report.DecodeArtifact` finds the SHA-256 of the bytes equal to
  the digest in the id (`report.ErrDigestMismatch` otherwise): a report is never shown beside a check it did not produce.
  The sweep (rule 5c) proves a report with the same function. States, each with fixed wording and a
  link to the checks on GitHub: no check yet, action required, no upload, report expired (`ErrNotFound`, GitHub retention), report does
  not match its check, report unavailable (decode error). With a report, in the groups and words of the PR comment:
  rule violations open (`report.Group`: active gate findings first, then any status this version does not know, which
  is shown but not labelled a violation), each dependency cycle among
  them (`report.Cycles`, the comment's grouping) as one card with a drawing of the cycle (one box per module, one
  arrow per direction labelled with its supplied location count and finding-identity origin), a row per direction with
  "This PR", its locations and the seam's strength, distance (`report.DistanceWords`) and volatility, then the reason
  once and each distinct repair once; every other violation as its own card. An advisory on a violation's dependency joins that violation's row as
  "Also reported as" (`report.SplitCovered`, the comment's rule) and is not counted among the advisories; the advisory
  group then reads "Advisories on other dependencies · never block" (`advisories_elsewhere`: any advisory covered,
  over all violations), as the comment does. Advisories, "Known issues
  accepted earlier" each have a folded table with seam facts and distinct reasons. `fixed` lifecycle entries instead
  state that an accepted identity is no longer observed and resolution is not established, without invented old
  endpoints or severity. The seam's unselected hypothesis is shown only for
  the advisories, never for accepted or fixed debt, in the PR comment's words
  (`report.BalancingMove`; a token this version does not know as written). A finding whose agent task the engine marks
  `needs_owner_decision` carries an "owner decision" label; the path the engine suggests is not shown: wire/report
  does not keep `matched_by.suggested_path`. A finding that names no dependency (a metric ratchet `metric/<name>`)
  reads as the comment's subject, "metric <name>: before → after (threshold)" from its kept `matched_by` keys, never an
  empty pair. Under the header, on this page and the pull request graph, the engine's
  decision, always, in the comment's words (`report.EngineVerdict`, `decision.verdict` and `decision.detail`): "Engine
  verdict blocked: hard gates fail, N active blockers, every required rule evaluated"; then each required rule it could
  not evaluate with its reason, and `comparison.origin_reasons` as attribution notes. With the hard gates failing and no
  violation listed, the page never says "No rule violations": it points to the verdict. At most 50 per group with the rest counted (group counts are of
  findings), 3 locations per finding with the rest folded. "This PR" only when `origin_status` is `comparable`: a
  cycle direction and every other finding read their own finding-identity `origin`, not a pair/site origin.
  The existing transport tokens `added`/`existing` are worded as introduced/pre-existing finding. Badges for the gate
  ("Required gate" or "Advisory: does not block merging"), "No baseline" (`gate_reference.baseline_present`),
  "Compared with base branch" or "Not compared with base branch", and the run as a link with "customer-attested",
  each explained in a tooltip;
  the exact nonempty `agent_tasks` array with a copy button, or an explicit no-source-repair-task explanation; the nine dimensions with readable names (the engine key as a
  tooltip) and every unknown fact in one folded "To measure more" group. Engine strings are JSON data that the web
  app renders as text. Two tabs above the result, **Findings** (this page) and **Graph** (the pull request graph).
  Links in API answers are shown only when they point into the App or to `https://github.com/` (`safeHref`).
- Pull request graph: the lookup of the pull request page, unchanged (one function serves both, so they always show
  the same report and the same states with the same wording); never `LatestRun` or the default branch's report. The
  policy text for module names, layers and lines: `.archfit.yaml` at the head SHA, then at the base branch, the first
  whose `policy.Digest` is the report's `comparison.config_hash` (the Action reads the head's file only when the pull
  request changes it); with neither, the head's text (else the base's) and the stale banner "Neither the head nor the
  base branch holds the policy this report was computed from." Module lines link to that file on GitHub at that ref.
  Display only: the head's text is untrusted and decides nothing. Header: the pull request page's header and result,
  with the Graph tab current. Without comparable Git-base finding-origin evidence, every link origin stays empty.
  Otherwise `report.PairOrigin(pair, g.All)` aggregates non-fixed findings. Retained `added` means contains
  introduced findings, including cycles and mixed pairs; `existing` means contains pre-existing findings. Neither
  dates the pair or each source site. The “N with introduced findings” chip emphasizes those pairs without changing
  status/permission. They remain problem rows even when another finding on the pair is accepted. A focused link
  from a finding selects only a pair already present in the admitted graph; query parameters supply no graph facts.
- Balance reading (both graphs, `web/src/lib/seam.ts`), each sentence the engine's own meaning
  (archfit `internal/relationship/seam.go`, `score/gate.go`): the suggested move in the PR comment's words
  (`report.BalancingMove`, sent worded as `Link.Move`), marked as a hypothesis; the quadrant; the boundary crossed and the module holding both ends. A dependency the engine
  flags (`distributed_monolith`) reads as the engine's coupling gate reason, "functional coupling across the module
  boundary at high volatility (container system)": only a deploy-unit crossing is called a distributed monolith. A
  line above the graph counts the flagged dependencies the same way. No mark on the canvas: in the pilot 8 of 9
  dependencies carry the flag.
- Graph: settings → `LatestRun(Settings.Workflow, {Branch: DefaultBranch, Event: "push", Status: "success"})` → `ArtifactID(run.ID, "archfit-report")` → the report cache or `ArtifactByID(id, report.MaxBytes)` →
  `report.Decode` (with the last release of `report.EngineVersions`, `v3.0.1`: the artifact names none) → policy text on the default branch (none is an empty text) → `graph.Build`. The artifact is the raw engine
  state JSON; only the decoded no-source `Report` is used. Only a push run uploads `archfit-report`; a dispatched run (discovery, baseline,
  re-anchor) or a run in progress must not hide the last report. No run or artifact (GitHub retention): "no report available". Decode error:
  "report unavailable"; never a partial graph. The repository graph never claims "added": ui clears
  `origin_status` before `graph.Build`. Each link is drawn and listed with its `Status` (`violation`, `accepted`,
  `advisory`, `allowed`, `observed`), which is also its CSS class. `allowed` comes only from the engine's `seams[].policy`; the page explains that
  accepted debt is never allowed. When `View.Incomplete`, a banner says a required rule was not evaluated or a hard gate could not be measured (a
  dimension the gate applies to measured nothing), so an observed link can still break a rule. A failed hard gate shows no banner: its violations are
  the message. The web app counts one unit on the graph pages, a dependency (an ordered pair of modules, so a cycle is
  two): chips ("N with a violation", "N with an advisory", then the other statuses), the Problems list and the
  inspector add up; an import count reads "N imports" in the tables, the inspector and the matrix. Balance reads
  "balanced" for the engine's none and low bands (no balancing move below medium), else "imbalanced · <band>". Problems are the violations and advisories (and in a pull
  request what it added); accepted debt sits in a collapsed "Accepted earlier" group and is drawn thin and faint. The
  number on a line is its import count.
- Data sharing: `GET` reads each kind with `Consents.Get` and says what leaves the customer's environment, who allowed it and
  when, and the preconditions; managed discovery carries the note that the App does not start it yet (owner decision A1:
  nothing is removed, and a recorded consent can still be revoked). Each feature says whether the page offers it
  (`offered`): Allow shows only for an offered feature, Revoke for any allowed one. The `POST` still accepts `allow`
  for every kind: a build with a sandbox driver grants managed discovery through it. `allow` → `Consents.Record(repo.InstallationID,
  Consent{Kind, By: login, ByID: user ID, At: now})`, atomic create-only: `ErrConsentExists` is `consent_kept`; `revoke` → `Consents.Revoke`. Each accepted
  action writes one `Info` audit line `ui consent` with `outcome` (`consent_recorded` | `consent_kept` | `consent_revoked`),
  `kind` (the server's constant), `installation_id` and `user_id`; nothing of the request body. Consent is per installation
  while the permission check is per repository: a repository administrator consents for every repository of the owner the
  App can access, and the page says so **[default]**.

Historical check recovery uses only the public gate facade's verdict-owned code/category classifier. It scans
primary and all secondary prelude reasons before presenting guidance; rejected/unknown source claims and actions
are withheld, independent authority/input diagnostics and recorded identity/links remain. Supported incomplete
reference/coverage/selector recovery is preserved. The original CheckRun, outcome and artifact admission are not
changed; no private gate child or duplicate TypeScript policy is imported.

## Change Vectors

- Page flow, wording, styling, templates, the graph drawing.
- New pages that read existing contracts (check details, waiver list).
- The CSRF scheme or security headers.
- Bounded visual edits in the graph: a new handler proposing a text patch through `proposal`.
- Another way to approve an account: the operator list and the configured accounts are inputs of `app`; ui only asks.
- Splitting handlers into more child modules when a second contributor works here.

## Constraints and Invariants

- Tenant identity only from the authorized session: the installation ID and repository used by any page come from the `Repo`
  that `Sessions.Authorize` returned (or from `Users.UserRepos` on the overview). IDs in URLs, queries, forms, JSON bodies
  or headers are never read. The `{o}/{n}` path segment is a lookup key and proves nothing.
- Operators are recognized by GitHub user ID only. A login can be renamed and re-taken; an ID cannot. Operator routes
  answer a non-operator exactly like an unknown path.
- ui never takes an installation ID or an account login for an approval from the request: the ID in the path must be
  one of the App's own installations, and the account login comes from GitHub. A configured account cannot be approved
  or revoked here.
- An approval that cannot be read is never shown as approved: the admin list shows the installation as waiting and
  counts it in `unreadable`. The overview list shows such a repository as waiting too (it reads no state for it either
  way), but a repository API answers the generic error, never the Waiting page, and the operator's waiting count is `-1`.
- Every repository handler authorizes on every request, `GET` included. A revoked user loses access within session's 60 second cache **[default]**.
- No repository score, no aggregate, no ranking, anywhere.
- Draft, awaiting review and published are pull-request states read from GitHub. ui stores no draft, no version, no lock.
- ui writes no policy text. The graph never writes.
- ui writes into the shell only its nonce and an error page's fixed sentence, HTML-escaped. The web app renders text
  only through Svelte's escaped interpolation: no `{@html}`, no `innerHTML`.
- No secrets in the browser: the OAuth token stays in the session cookie (HttpOnly), and API bodies are explicit
  response types. The CSRF token is the only per-session value the app holds.
- Static assets, the web app's build and the schema carry no tenant data; everything else requires a session.
- Logs: request ID, route pattern, status, duration, user ID, repository ID, installation ID, outcome, GitHub call
  count and time. No form content, no
  policy text, no report content, no cookie or token.
- Inherited: control-plane code. The gate never calls ui; a panic or outage here changes no check. Standard library only
  on the Go side. Browser code comes only from `web/`, built at build time from the npm lockfile and served by the App:
  no runtime CDN, which the CSP would block anyway. First load of a page: at most 120 KB gzip of JavaScript and 20 KB
  of CSS; the graph chunk at most 120 KB gzip, and its layout worker at most 480 KB **[default]**, checked in CI (`web/scripts/size.mjs`).
- Declared restatement: two kinds of link are built from the literal host `https://github.com/`: the pull-request
  search and the default-branch commits page. `internal/github` owns the
  knowledge of GitHub hosts and publishes no URL builder; `control/onboarding`
  (`CloneURL`) restates the same host. On drift a link opens the wrong host; no write and no check depends on it.
  Removal trigger: GitHub Enterprise host support, when `internal/github` publishes the web base URL.
- Declared restatement: the check names `archfit/state` and `archfit/gate` are literals here. `internal/gate`
  (feedback) owns and publishes them but exports no constant, and control imports only the gate façade;
  `control/onboarding` restates `archfit/state` too. On drift the overview shows "no state yet" for every
  repository; no gate result changes. Removal trigger: a rename of a check, at which point gate exports the names.
- Declared restatement: the shape of the `archfit/state` summary that `gate/feedback` writes: the `### Policy change`
  heading, the producer line `engine <version>, image …` and the baseline word `no baseline` are read back by literal
  here (the recovery prelude and the inputs section already are). `FirstScreen` receives them as plain fields. On drift the
  page shows no first screen (an unread reason or card), or the comment's assessment line instead of "No required
  architecture change" (an unread baseline word); the root test of the first screen against the comment fails first.
  Removal trigger: feedback exports a parser of its own summary.
- The first screen is never worded here: every line is `gate.FirstScreen`'s, the page adds no sentence.
- Bounded work per request, each **[default]** except the last: 100 repositories, 8 parallel GitHub lookups, 512 KB bodies;
  5 MB artifact (`report.MaxBytes`).

## Test Specification

Harness: `ui.New` behind `httptest` with a fixed 32-byte cookie key and a fake `OAuth`, so the real `session` and `graph` run, the real `account` over a memory Store.
GitHub is faked at the HTTP boundary behind a real github/api `Client`. `Onboarding`, `Passkeys` and the gate's
`RequestViews` and `Answers` are faked at their interfaces except where a test says otherwise; the rules of who may answer are the
gate's and tested there. The clock is injected.

### Unit Tests

- **Test name**: `TestCSRFToken`
  **Scenario**: table: token of user A checked for A; for B; empty; truncated; A after re-login with a new OAuth token.
  **Expected behavior**: only the first row passes; comparison is constant-time.
- **Test name**: `TestStatusMapping`
  **Scenario**: table of every error in the Status mapping list, injected through the fakes.
  **Expected behavior**: the documented status as JSON `{"error": …}`; `Retry-After` on `503` for rate limits.
  The rows include `api.ErrNotFound` → `404` and the three onboarding sentinels `ErrNoSandbox`, `ErrAlreadyOnboarded`,
  `ErrBusy` → `409`, none of them `502`.
- **Test name**: `TestWording`
  **Scenario**: table of `DraftState`, `Plan`, check conclusions and their tones.
  **Expected behavior**: draft / awaiting review / published / closed; a banner only for trial, grace, suspended; failure
  reads "violations found" with tone `danger`, never "blocked"; a neutral check whose title starts with
  `report.HeadlineViolations` or `report.HeadlineGateFailing` reads "violations found" too (`stateWord`), every other
  neutral one "attention"; every coloured word is a word some state reads.
- **Test name**: `TestMetricSubjectReadsAsTheComment`
  **Scenario**: table: a metric finding with the metric, before, after and threshold; without the metric and
  threshold; without after; a finding that is not a metric.
  **Expected behavior**: "metric dependency_cycles: 0 → 1 (must not increase)"; the rule's name with "0 → 1"; the
  name alone; `""`.

### Integration Contract Tests

- **Test name**: `TestRoutesAndMinimumPermissions`
  **Scenario**: table of every route × user permission `none`, `read`, `write`, `admin`, and not signed in.
  **Expected behavior**: `303` to login or `401` without a session (`401` JSON on `/api/`); `404` below the minimum;
  success at and above it (`200` for a page and for every `/api/` call); a repository page `200` for any signed-in user;
  `/schema/policy.json`, `/assets/<hashed file>` and `/licenses.txt` answer without a session, the asset with the
  immutable cache header and the licences with `no-cache`; `/static/app.css`, `/assets/`, a missing asset,
  `/index.html` and an unknown `/api/` path are `404`.
- **Test name**: `TestExpiredGitHubTokenSignsOutInsteadOfBadGateway`
  **Scenario**: a signed-in user whose GitHub token is rejected (`ErrUserTokenRejected`) on the pull request API,
  the overview and the repository overview; then any other GitHub error on the repository overview and the overview.
  **Expected behavior**: the rejected token gives `401` JSON, the session cookie
  deleted, outcome `signed_out`, and the next request is a plain signed-out one. Any other error stays `502` and keeps the cookie.
- **Test name**: `TestPullPageStatesTheEngineDecision`
  **Scenario**: the pilot's PR report as captured; then with an unevaluated required rule, an attribution note and an
  agent task `needs_owner_decision` on one advisory moved to a dependency no violation names (pkg_cmd → pkg_util).
  **Expected behavior**: decision `blocked` / "hard gates fail, 4 active blockers, every required rule evaluated", `fail` with 4 blockers and empty lists; no advisory listed or counted, both new ones
  "Imbalanced coupling · critical" in the `also` of their cycle directions; then the rule with its reason, the note,
  one advisory with the words of `follow_rule` as its move, and exactly one owner decision.
- **Test name**: `TestPullPageFirstScreenIsWordedByTheComment`, `TestPullPageFirstScreenNeedsAReport`, `TestScreenInputReadsTheRecordedCheck`
  **Scenario**: the state summary feedback wrote for the healthy fixture, required mode, with and without a policy change
  card after its prelude; a title with markup; a reason that is not report-derived; a summary without a reason; no check
  on the head; the input built from a recorded check with a producer line of each shape.
  **Expected behavior**: `first_screen` holds the comment's lines (headline from the check title, the engine and policy
  digest, the next step, the identity line with the check ID, the card's heading and items) as plain text, no Markdown
  mark; none for the withheld cases; the engine version read from `engine <v>, …` only, an empty one for no upload.
  The same fixtures through the whole App, against the comment itself, are in the root `TestPullPageFirstScreenEqualsTheCommentsFirstScreen`.
- **Test name**: `TestOverviewNamesTheLastCheckedMergeOfTheDefaultBranch`, `TestOverviewCountsPullRequestsToUpdateAfterTheTargetMoved`
  **Scenario**: two runs of `archfit/state` on the default-branch tip; none; `target_moved` with a recorded step, without
  one, another reason, a success that still holds the summary, no summary, escaped repository text in the step.
  **Expected behavior**: the latest run's word, short SHA and start time, none without a check; `rerun` with the role
  prefix and the modules, a fixed sentence without a recorded step, empty for the others, and `pulls_to_update` counting
  the rows.
- **Test name**: `TestPullPageListsTheKnownIssuesFixedAsTheCommentDoes`
  **Scenario**: a new, a baseline and a fixed finding.
  **Expected behavior**: one fixed row (`old → gone`, meta "high"); the fixed finding counts as neither a violation
  nor accepted debt.
- **Test name**: `TestAttentionReasonFollowsTheEngine`
  **Scenario**: table: healthy; blocked; needs_attention with two partial dimensions; with advisories, an unevaluated
  rule and one partial and one unmeasured dimension; with `hard_gates: unmeasured` and three unmeasured dimensions.
  **Expected behavior**: `""`; `""`; "testability and operations are partially measured"; the four reasons in the
  engine's order; "a hard gate could not be measured; intent, structure and drift are not measured".
- **Test name**: `TestPullPageShowsTheReportOfItsCheck`
  **Scenario**: no check on the head; a check whose external_id names a run whose artifact matches its digest; one whose digest does not match; an expired artifact; run 0; an unknown pull request.
  **Expected behavior**: "no result yet"; the check title as headline, advisory mode, the violation, the no-baseline and no-attribution line, the run; the mismatch wording and no finding; the expired wording; the no-upload wording; `404`. Also: an `action_required` run over a matching old report shows the action-required wording and no finding; an undecodable report shows "report unavailable"; of two check runs the higher ID wins; introduced-finding evidence appears only with `origin_status: comparable` and `origin: introduced`, never as a claim that each source site was added.
- **Test name**: `TestStatusPageShowsBranchPullsAndSetup`
  **Scenario**: stage `gate_advisory` with a default-branch check and one open pull request; stage `workflow_merged`; `OpenPulls` failing; for an administrator, `CheckRuns`, `OpenPulls` and the entitlement all failing at once; 23 more open pull requests.
  **Expected behavior**: through `/api/repos/{o}/{n}/overview`: the default branch, its title and tone, the pull request linked to its page with its check title, the current and done steps, the rehearse advice; no pull request or state shown before a report, also at `policy_merged`, where "First report" is not done; the ruleset advice at `gate_required`; the fixed wording and a `200`; a `200` with admin true, the state `unknown`, the pulls error, no banner and the steps; "4 more" and 21 `CheckRuns` calls.
- **Test name**: `TestPolicyReadFailsNothingWithoutAReport`
  **Scenario**: the repository graph with no report and its policy read rate limited; the pull request graph with no check run
  and both policy reads rate limited.
  **Expected behavior**: `200` saying there is no report, and no result on the head, each without a view.
- **Test name**: `TestPullPartsFailAsWhenReadInTurn`
  **Scenario**: a pull request on base `release`, rate limited in turn on: the settings at the base; the report download; both, the download failing with a plain error; the policy at the head (graph); the policy at the base while the head's is the report's (graph).
  **Expected behavior**: `503`; `503`; `503` (the settings' error, not the report's `502`); `503`; `200`.
- **Test name**: `TestTogetherRaisesAPanicOnTheCallersGoroutine`
  **Scenario**: two parts side by side, one panics.
  **Expected behavior**: the panic reaches the caller after the other part ran.
- **Test name**: `TestReportCacheBoundsItsBytes`, `TestReportCacheIsPerRepository`
  **Scenario**: a 10-byte cache: two 4-byte payloads, the first read again, a third, then one of 12 bytes; the same artifact ID read twice for one repository, then for another repository of the same installation, and for the same name under another installation.
  **Expected behavior**: the first and third kept, the second evicted, the 12-byte one never kept, 8 bytes held; one download for the first repository, a second download for the other, which gets its own bytes, in both cases.
- **Test name**: `TestPullPageAndGraphDownloadTheReportOnce`, `TestReportCacheHitStillAuthorizes`
  **Scenario**: the pull request page then its graph; a user without access after the report is cached.
  **Expected behavior**: two artifact listings, one download; `404` and no listing for the refused request.
- **Test name**: `TestCachedReportOfAnUnlistedArtifactIsGone`
  **Scenario**: the pull request page and the findings read once, so the cache holds both reports; then GitHub lists no
  artifact any more.
  **Expected behavior**: the pull request page says the report expired, the findings say there is no report, neither with a
  finding.
- **Test name**: `TestIndependentReadsMeet`
  **Scenario**: the repository graph, the pull request graph and the overview, each with the fake GitHub holding its named
  reads until all are in flight: the artifact listing and the policy text; the artifact listing and the head's policy;
  the onboarding progress, the branch head and the open pull requests.
  **Expected behavior**: `200`, and each set met; reads made in turn would wait out the timeout and fail.
- **Test name**: `TestPullPageNumberMustBeAPull`, `TestPullPageReadsTheGateModeOfTheBase`
  **Scenario**: numbers `0`, `-4`, `x`, `4x`, and `5`, no pull request, on the page, the API and the graph; a pull request whose base branch requires the gate while the default branch is advisory.
  **Expected behavior**: `404` each (`5` on the page only from the API); `required` true.
- **Test name**: `TestPullPageHidesOriginsWithoutAComparisonAndShowsActiveFirst`
  **Scenario**: no comparison; a cycle and plain findings with origins; a status this version does not know first in the report.
  **Expected behavior**: no origin anywhere; the active finding before the unknown one, which is not a violation.
- **Test name**: `TestPullPageGroupsCyclesAsTheCommentDoes`, `TestPullPageCapsEachGroup`
  **Scenario**: a cycle whose second direction another rule's `pre_existing` finding names, a plain violation, an
  accepted pair, a seam on one direction, two dimensions; then 57 advisories.
  **Expected behavior**: one cycle with both directions, each repair once, origins "" and existing (`report.PairOrigin`),
  the seam's words on its direction only; the rest as plain violations with their own origin; dimensions in engine
  order with readable names; 50 rows, a count of 57 and 7 omitted.
- **Test name**: `TestParseExternalID`
  **Scenario**: a pilot external_id, a bare digest, run 0, another version, a non-numeric run, empty.
  **Expected behavior**: run and bare hex digest for v1 ids; not ok otherwise.
- **Test name**: `TestOverviewReadsStateOnDefaultBranchHead`
  **Scenario**: table of repositories: `archfit/state` success on the default-branch head; neutral; no check run; `BranchHead` fails.
  **Expected behavior**: one `BranchHead` and one `CheckRuns(head, "archfit/state")` per repository; the rows read healthy,
  attention, "no state yet", "unknown"; `archfit/gate` is never looked up here; one failed row does not fail the page.
- **Test name**: `TestGraphPageBuildsFromArtifact`
  **Scenario**: the blocked wire fixture (`hard_gates: fail`, no baseline) as the `archfit-report` artifact of the latest default-branch run.
  **Expected behavior**: the shell answers `200`; the graph API's `view` equals `graph.Build(report, policy)`;
  `policy_href` is the policy file on GitHub at the default branch; not stale; `incomplete`; marker strings placed in fields the decoder drops
  (a `matched_by` key outside `MatchedByKeys`, measurement, task declarations) appear nowhere in the answer.
- **Test name**: `TestGraphPageShowsTheEngineSeamPolicy`
  **Scenario**: the unmodified capture `real-seam-policy` of the shared wire corpus as the `archfit-report` artifact.
  **Expected behavior**: the API decodes it; its `view` equals `graph.Build(report, policy)`, with links of status
  `violation`, `accepted`, `allowed` and `observed`.
- **Test name**: `TestPullGraphDrawsTheReportOfItsCheck`, `TestPullGraphStates`
  **Scenario**: a pull request whose head's `archfit/state` check names a run, beside a newer default-branch report;
  then no check, a withdrawn result, an expired report, a run that did not compare, a policy only the base holds, and
  no policy of the report anywhere.
  **Expected behavior**: the view is built from the check's run, never the default branch's; the header is the pull
  request page's; a state without a report answers its fixed wording and no view; without a comparison no link is
  added; the policy link is the file at the ref that holds the report's policy, else the view is stale.
- **Test name**: `TestPullGraphShowsWhatThePullRequestAdded`
  **Scenario**: a pumba-pilot#11-shaped report: two new cycles over old dependencies, compared with base.
  **Expected behavior**: both cycles stay violations; exactly one direction of each is `added`, every other link
  `existing`, as the PR comment says; `added` is 2.
- **Test name**: `TestDefaultGraphClaimsNothingNew`
  **Scenario**: a default-branch report that says it compared with a base.
  **Expected behavior**: no graph link and no finding of the findings API carries an origin.
- **Test name**: `TestDataSharingAllowAndRevoke`
  **Scenario**: an administrator reads Data sharing, allows `source_processing`, another administrator allows it again, it is revoked
  twice; then bodies with an unknown kind, an unknown action, no action and no kind.
  **Expected behavior**: before: not allowed, the consent check unmet, discovery with its note, not offered, and one
  check; a consent given earlier for a feature the page does not offer can be revoked; after allow: the session login, user ID and clock under the authorized installation, and the answer says who and
  when; allowing again writes nothing and keeps both; both revokes `200`; the refused bodies `400` and record nothing.
- **Test name**: `TestDataSharingNeverShowsAnOutageAsNotAllowed`
  **Scenario**: a recorded consent, then `Consents.Get` fails.
  **Expected behavior**: `GET` and `POST allow` answer `502` and write nothing.
  A write that succeeds but cannot be read back answers `502` with "The change was saved", never "Nothing was changed".
- **Test name**: `TestOldConsentPageMovedToDataSharing`
  **Scenario**: `GET /r/{o}/{n}/consent` with no, read and admin permission, on the user's repository, another tenant's and one
  that does not exist.
  **Expected behavior**: `308` to that repository's `/data-sharing` every time, with no permission lookup.
- **Test name**: `TestOwnersRuleIsSettings`
  **Scenario**: table of submitted owners: `-alice` (a leading hyphen, which wire/settings accepts); a team; `alice` and
  `Alice`; `not a login!`; 101 owners; only blank lines. Each row is also run through `settings.Parse(settings.Render(…))`.
  **Expected behavior**: ui starts onboarding with the trimmed owners exactly when settings accepts them; every other
  row is `422` with ui's fixed wording, without settings' error text, and with no onboarding call.
- **Test name**: `TestOnboardingAndDiscoverActions`
  **Scenario**: start customer onboarding with two owners; start managed without consent; dispatch discovery before and after `workflow_merged`.
  **Expected behavior**: through the JSON API: `StartCustomer` gets the parsed owners and the answer is the onboarding
  notice; managed without consent answers the Data sharing page as `location` and never calls `StartManaged`; discovery dispatch is refused before the stage and sends input `mode=discovery` on the settings workflow after it.

- **Test name**: `TestRepositoryOfWaitingAccountAnswersWaiting`
  **Scenario**: a user with `admin` on a repository of an installation no one approved; its overview, findings and Data sharing APIs, another repository of an approved account, and `/api/repos`.
  **Expected behavior**: `403` with `waiting` set to the account for the three APIs; `200` for the approved repository; in the list only the unapproved repository has `waiting: true` and the state "waiting for approval".
- **Test name**: `TestOperatorApprovesThenRevokes`
  **Scenario**: an operator opens `/admin`, reads the session and the installation list, approves the waiting installation, then revokes it an hour later; the account's user reads its overview after each step.
  **Expected behavior**: `200` for the operator; session `operator` with 1 waiting; the list shows the waiting installation with its repository and the configured accounts as approved by configuration; after approve it is approved by the operator at the server clock and the user's overview is `200`; after revoke the overview is `403` and the installation is waiting again.
- **Test name**: `TestOperatorIsTheUserIDNotTheLogin`
  **Scenario**: another GitHub user, with a different user ID, signs in with the operator's login; `/admin`, approve and `/api/session`.
  **Expected behavior**: `404` for `/admin` and for approve; the session has no `operator`.
- **Test name**: `TestOperatorCannotChangeConfiguredOrUnknownInstallations`
  **Scenario**: an operator approves and revokes a configured account's installation, an unknown ID, `x` and `-3`; then approves with no CSRF token.
  **Expected behavior**: `409` for the configured account, `404` for the others, `403` without the token; the installation is not admitted afterwards.

### Boundary Tests

- **Test name**: `TestPasskeyBodiesNameNoUserAndNoRepository`
  **Scenario**: every passkey write with a body that names a user id and another repository.
  **Expected behavior**: `422` for each; nothing reaches `control/passkey`.
- **Test name**: `TestPasskeyRegistrationGetsTheUserAndWhetherTheyAreAnAdministrator`
  **Scenario**: registration begin and finish at read, write and admin.
  **Expected behavior**: passkey gets the session's user id and login, the authorized repository and installation, and
  `Admin` true only at admin.
- **Test name**: `TestPasskeyAnswersCarryTheOptionsTokenAndList`, `TestPasskeyErrorsAreWorded`
  **Scenario**: step-up, token, list and remove; every passkey sentinel and an upstream error.
  **Expected behavior**: the purpose of the body reaches passkey without a repository; each sentinel answers its
  status and sentence; no error's own text reaches the answer.
- The passkey routes are rows of the route table, so `TestRoutesAndMinimumPermissions`, `TestCSRFRequiredOnEveryPost`
  and the cross-installation tests cover them. So are the two answer routes.
- **Test name**: `TestAnswerDigestAPI`, `TestAnswerAPIPassesTheAnswerTheUserAndTheToken`, `TestAnswerAPIRefusals`
  **Scenario**: a digest; an answer with each outcome (merged, later, refused, no line); each refusal of the gate and
  the passkey; no permission; no CSRF token; a request that is not a number.
  **Expected behavior**: the answer, the token and the session's user reach `control/answers`; the notice for each
  outcome uses no "Not used" word; each refusal answers its status; nothing is answered without access or the token.

- **Test name**: `TestAdminIsNotFoundForNonOperators`
  **Scenario**: a signed-in user who is not an operator calls `/admin`, the list API, approve and revoke, and reads `/api/session`.
  **Expected behavior**: `404` for the four routes; the session body has no `operator` or `waiting`; the installation was not admitted.
- **Test name**: `TestRepositoryPagesAreOneShell`
  **Scenario**: every repository page (overview, graph, pull request, its graph, Data sharing) for the user's
  repository, another tenant's and one that does not exist; then the overview API of the other tenant's and the missing one.
  **Expected behavior**: `200` and the same status, headers and bytes for all three but the nonce and the request ID; no
  GitHub permission lookup; the two API answers identical `404`s.

- **Test name**: `TestTenantIdentityOnlyFromSession` (T07, T17)
  **Scenario**: a user of installation A sends every route with installation B's ID and repository ID in query, form, JSON body and headers, and
  with `{o}/{n}` of a repository in B.
  **Expected behavior**: supplied IDs have no effect; every API route of B's repository answers `404` (its pages are the shell, the
  same for every repository); no Store key of B is read or written; no GitHub
  call uses B's installation.
- **Test name**: `TestOverviewShowsOnlyAccessibleRepositories` (T17)
  **Scenario**: GitHub lists three repositories for the user; the fake holds five more in the same and other installations.
  **Expected behavior**: exactly three rows; no lookup is made for the others.
- **Test name**: `TestNewRejectsBadSessionConfig`
  **Scenario**: table: 16-byte `CookieKey`; `BaseURL` `http://app.example`; relative `BaseURL`; then a valid config.
  **Expected behavior**: `ui.New` returns the `session.New` error and a nil handler for the first three; no panic; a handler for the last.
- **Test name**: `TestCSRFRequiredOnEveryPost`
  **Scenario**: every `POST` route, logout included, without a token and with another user's
  token; each `/api/` `POST` also with the right token in a form field instead of the header.
  **Expected behavior**: `403`, and `415` for the form body; no sibling module or GitHub write is called.
- **Test name**: `TestOversizedAndInvalidInput`
  **Scenario**: table: 600 KB body; 50 owners lines with one malformed; an empty owners list; an unknown onboarding
  mode; a JSON body with an unknown field (a repository named in an onboarding or discovery request), and a body that
  is not JSON on the onboarding API.
  **Expected behavior**: `413` for the oversized body, `422` for the rest; nothing is written.
- **Test name**: `TestNoScoreAnywhere` (T17)
  **Scenario**: render every page with healthy, blocked and attention fixtures.
  **Expected behavior**: no number that aggregates findings or dimensions, no grade, no percentage, no sort by health.
- **Test name**: `TestHostileStringsAreEscaped`
  **Scenario**: repository names, module names and check titles containing HTML and script text.
  **Expected behavior**: escaped in HTML, in the embedded JSON and in API bodies (`<` as `\u003c`); every page carries exactly the CSP of the handler chain, or the shell's with its nonce, with no
  `'unsafe-inline'` and `worker-src 'self'` only.
- **Test name**: `TestNoAPIResponseHoldsASecret`
  **Scenario**: every API route, writes included, and an unknown API path, for a signed-in admin.
  **Expected behavior**: no answer holds the user's OAuth token or the session cookie.
- **Test name**: `TestWebPreviewUsesTheProductionCSP`
  **Scenario**: read `web/csp.txt` and `web/index.html`.
  **Expected behavior**: `csp.txt` is the shell's policy with the `__CSP_NONCE__` placeholder, byte for byte, and
  `index.html` carries that placeholder in `<meta name="csp-nonce">` and `__APP_ERROR__` in `<meta name="app-error">`,
  so the web app's smoke test runs under the production policy.
- **Test name**: `TestErrorPagesAreTheShell`
  **Scenario**: a pull request number that is not one, a path of no page, a route's path with a method it does not take
  (`GET /auth/logout`).
  **Expected behavior**: `404` each; each the shell with its CSP and a nonce, `no-store`, and the
  wording table's sentence in the app-error meta tag; no internal text and no placeholder in the page. A page that
  succeeds has an empty app-error meta tag.
- **Test name**: `TestShellNonceIsFreshPerResponse`
  **Scenario**: the shell on the overview, the repository page twice and Data sharing; then an API
  answer, an asset and the schema.
  **Expected behavior**: every shell has a 16-byte base64 nonce in `style-src` only, the rest of the policy unchanged;
  no nonce twice; the page's meta tag holds the same nonce and no placeholder is left; `Cache-Control: no-store`.
  Every other response has the CSP without a nonce.
- **Test name**: `TestLogsCarryOnlyIdentifiers`
  **Scenario**: run Data sharing (refused bodies with marker kind and action, allow twice, revoke), graph and the
  overview APIs with marker strings in the report and the entitlement error.
  **Expected behavior**: no marker, token or cookie value in any log line; exactly three `ui consent` audit lines at `INFO`
  (`consent_recorded`, `consent_kept`, `consent_revoked`), each with kind, installation ID and user ID.
- **Test name**: `TestRequestLineCountsGitHubCalls`
  **Scenario**: one overview request.
  **Expected behavior**: one `ui request` line for its route with numeric `gh_calls` and `gh_ms`; no URL in the logs.

### Behavior Tests

- **Test name**: `TestRevokedUserLosesEveryPage` (T07, T17)
  **Scenario**: a signed-in user with `write` loses repository access at GitHub; 61 seconds pass.
  **Expected behavior**: every API route of the repository, `POST` included, answers `404`; the overview no longer lists the
  repository.
- **Test name**: `TestRemovedRepositoryDisappears` (T07)
  **Scenario**: the repository is removed from the installation while the user keeps `admin` on it.
  **Expected behavior**: `404` on every API route of it; no write is attempted.
- **Test name**: `TestArchfit3RoutesAreGone`
  **Scenario**: the routes of the policy editor, assist, owner approvals and overrides, decision requests and the decisions
  page, as a signed-in administrator; then the Archfit 4 repository routes beside them.
  **Expected behavior**: `404` for each removed route and no write; every Archfit 4 route still answers `200`.
- **Test name**: `TestRepositoryGraphReadsThePolicyOnTheDefaultBranch`
  **Scenario**: a healthy report; the policy file on the default branch edited to declare a third module.
  **Expected behavior**: the graph API's `view` equals `graph.Build(report, edited policy)`; `policy_href` is the file on
  the default branch; not incomplete.
- **Test name**: `TestGraphWithoutAReport`
  **Scenario**: the graph page with no workflow run; then with an `archfit-report` artifact that `report.Decode` refuses.
  **Expected behavior**: `200` "No report available"; then `200` "Report unavailable" and no view: never a partial graph.
- **Test name**: `TestGraphReadsTheLatestSuccessfulPushRun`
  **Scenario**: a successful push run with the report, then newer runs: a failed push, a successful dispatch, a push in progress.
  **Expected behavior**: the graph is drawn; the only `Artifact` read is the successful push run's.

### Web app tests (`web/`, `make web-check`, CI job "Web app")

- `svelte-check --fail-on-warnings`: types of every component and module.
- ESLint (`web/eslint.config.js`): the recommended JavaScript, TypeScript and Svelte rules; `{@html}` is an error.
- Vitest (`web/src/lib/*.test.ts`): routes (a `.` or `..` owner or name is no route, a pull request number is a
  positive integer), `safeHref` (only App paths and `https://github.com/`), `serverError` (the app-error meta tag, the
  placeholder read as none), the cycle drawing's layout and arrow styles,
  escaped links and API paths, the sign-in return link, the early reads of each route taken once then read again and a failed one
  handed to its page, a page read's `404` replacing the page and a write's never, the theme with and without storage, the API client's CSRF
  header, error sentences and fallbacks; the balance words: every quadrant worded, the crossings as the engine gate
  names them (a distributed monolith only in a flagged deploy-unit dependency's reason, never in where the distance
  was measured), the flagged dependency's reason and the count line, the decision sentences for `fail` with and
  without blockers, `unmeasured`, `pass` and an unknown value; the age words (`lib/age.ts`: just now, minutes, hours,
  days, a time that cannot be read, a time ahead of now); the request and passkey routes.
- `web/src/lib/request.ts` and `web/src/lib/webauthn.ts` (vitest): which answers need a passkey, the date and
  `instead` rules, the status line, balance and preview words; base64url, the passkey options and the credential JSON.
- `web/scripts/size.mjs`: the first-load, graph chunk and layout worker budgets above; no lazy chunk in the first load.
- Playwright `web/e2e/request.spec.ts` and `web/e2e/passkey.spec.ts`: the request page and the passkey page with the
  API mocked and a Chromium virtual authenticator; each answer's writes (digest, step-up, answer with its token; a
  reject with none), registration with and without a step-up, removal, refusals, axe, phone width and the "Not used"
  words of `web/e2e/not-used.json`, which must equal `internal/wire/wording/wording.go.txt`.
- Playwright smoke (`web/e2e/smoke.spec.ts`): the production build behind `vite preview` with `web/csp.txt` as its
  header and `/api` mocked in the browser. Landing, repositories, overview, starting onboarding and discovery, and the
  theme toggle; the overview's watch line, its two counts (a count that could not be read shown as "–" with its reason),
  the pull request that waits for an update
  with the modules named; the pull request page (first screen, cycle and evidence, no report); the graph pages, laid out by
  the worker, the inspector's balance reading of a flagged dependency and the count line, and a worker that fails to load reported instead of a layout that never ends; a server error page (its sentence, no repository tabs) and the footer's licences link; a repository the user cannot see and one
  that does not exist shown the same, as the API's "Not found." with no repository tabs; a page asking its data beside
  `/api/session`, its skeleton up until both answer; every page keeps its content and footer at most 1280px wide and centred, and has no sideways scroll at 390px; on a phone a two-module cycle stacks its modules in a column and its labels render at 11px or more (a longer cycle keeps its size and scrolls in its box); at 390px the findings and evidence tables show one block per row, every cell labelled, with no table scrolling sideways and no row taller than its cells; a graph page with no banner renders no empty block in the page's flow; the agents fold draws no rule when it is first in its box; a dimension that warns or fails stands out; the pull request page states the engine verdict, the known issues accepted earlier with their coupling and no suggested move, and the known issues fixed; every page has no
  `securitypolicyviolation` event and no axe violation in light and in dark; the onboarding write carries the CSRF
  header and a JSON body.

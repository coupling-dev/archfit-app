# GitHub Integration

**Path**: internal/github/
**Parent**: internal/ (Archfit App root)
**Submodules**: auth, api, webhook, oidc

## Purpose

`github` groups everything that knows GitHub's shapes: credentials, the REST API, webhook deliveries and
Actions OIDC tokens. GitHub is the App's database, identity provider and user interface, so this knowledge
is large; keeping it in one branch means `gate` and `control` see only small Go contracts. Without the branch,
URL shapes, headers, pagination and rate-limit rules would leak into the core.

## Functional Responsibilities

Delivered entirely by the children; the parent has no code beyond `doc.go`.

- `auth`: App JWT, cached installation tokens, OAuth web flow.
- `api`: REST client by role: repositories, pulls, checks, comments, Actions, changes, users.
- `webhook`: signature verification and normalized events.
- `oidc`: Actions OIDC token verification and workflow path extraction.

## Subdomain Classification

Generic. Every part wraps a documented GitHub protocol; none differentiates the product. Functional volatility
is low. The branch changes when GitHub deprecates something or a consumer needs one more endpoint or event.

## Encapsulated Knowledge

Owned by the branch as a whole; no module outside `internal/github/` may hold any of it:

- GitHub URL shapes, hosts, API version header, media types.
- Pagination, ETag and rate-limit semantics, including the rule that exhaustion is an error and never data.
- JWT and JWKS formats, the webhook signature scheme, payload and claim field names and encodings.
- Which credential each endpoint needs.
- The App permissions and event subscriptions the children rely on.

One published exception: the three webhook delivery header names. `app` owns the `http.Request`, and `VerifyAny`
and `Parse` take header values, so `app` must read those headers. `webhook` exports the names as the constants
`HeaderEvent`, `HeaderDelivery`, `HeaderSignature`; `app` uses the constants and no module outside the branch
spells the strings. Nothing else in the list above is published.

The parent itself owns only the decomposition: four leaves, one internal edge (`api` → `auth`).

## Public Contract

`github` publishes its child packages. Go cannot re-export a package without alias boilerplate, and these
children hold no hidden internals, so consumers import the child and cite the child's `module.md` as the
owner of what they restate. The parent package exports no identifier.

### Published children

- `internal/github/auth`: `Credentials`, `TokenSource`, `OAuth`, `UserToken`, `New`.
- `internal/github/api`: `RepoRef`, `Repo`, `Installation`, the role interfaces `Repos`, `Pulls`, `Checks`,
  `Comments`, `Actions`, `Changes`, `Users` with their data types, `Client`, `New`, and the sentinels
  `ErrNotFound`, `ErrRateLimited`.
- `internal/github/webhook`: `Event`, `EventKind` and its constants, `Verify`, `VerifyAny`, `Parse`, and the header name
  constants `HeaderEvent`, `HeaderDelivery`, `HeaderSignature`.
- `internal/github/oidc`: `Claims`, `Verifier`, `New`, `WorkflowPath`, and the sentinel `ErrUnavailable`.

### Rules that hold for every child

- No domain decision lives here. The branch reports what GitHub says and authenticates what GitHub signs. What
  that means for a gate, an approval, a trial or a fork is decided by the consumers.
- Identity passed in is trusted by the caller's construction: a `RepoRef` or installation ID must come from a
  verified webhook, verified OIDC claims resolved through the API, or an authorized session. The branch does
  not re-derive tenancy; GitHub's own access control answers `404` to a wrong pairing.
- Identity handed out comes only from signed or authenticated sources: the signed webhook body, a verified
  token, or an API response fetched with App or user credentials.
- Failure is an error. Rate-limit exhaustion, missing access, an unknown key, or an oversized list never turn
  into an empty or partial result.
- Children import the standard library only; `api` additionally imports `auth`. No GitHub SDK.
- No child logs. Errors carry IDs, status codes and check names: no tokens, keys, payload text, or paths.

## Integrations

The parent has no code, so it integrates only through its children. Each child's `module.md` lists its own
consumers with restated contracts. Summary, with values from the root coupling table:

Distances here are measured to the `github` module itself (depth 1). A child document measures to that child
(depth 2), so a sibling of `github` shows rank 2 there; both follow the same rule: levels from the LCA to the
deeper module.

### Sibling consumers

- **Counterpart**: internal/gate/, internal/control/, internal/app/
- **Direction**: they depend on `github/*`; nothing under `github` depends on them
- **Strength**: Contract
- **LCA / Rank / Distance**: root / 1 / 1
- **Volatility**: High (`gate`), Moderate (`control`), Low (`app`)
- **Balanced?**: Yes
- **Shared knowledge**: `gate`: `api.Client`, `Actions`, `oidc.Verifier`, `webhook.Event`. `control`: `api.Client`,
  `Repos`, `auth.OAuth`, `auth.TokenSource`, `webhook.Event`. `app`: the four constructors, `webhook.VerifyAny`, `webhook.Parse`,
  and the three `webhook.Header*` constants.

### Consumers below the siblings

- **Counterpart**: internal/gate/ingest/, internal/gate/inputs/, internal/gate/feedback/, internal/gate/sweep/,
  internal/control/proposal/, internal/control/onboarding/, internal/control/assist/, internal/control/ui/ (depth 2);
  internal/control/ui/session/ (depth 3)
- **Direction**: they depend on `github/*`; nothing under `github` depends on them
- **Strength**: Contract
- **LCA / Rank / Distance**: root / 2 / 2 for the depth-2 modules; root / 3 / 3 for `control/ui/session`
- **Volatility**: High (`gate/*`, `control/onboarding`, `control/assist`, `control/ui`), Low (`control/proposal`,
  `control/ui/session`)
- **Balanced?**: Yes: contract strength tolerates the distance
- **Shared knowledge**: the identifiers listed under Published children, by way of each child's Public Contract.
  By child: `auth` ← `api`, `control/onboarding`, `control/ui/session`, `control`, `app`. `api` ← `gate/ingest`, `gate/inputs`,
  `gate/feedback`, `gate/sweep`, `gate`, `control/proposal`, `control/onboarding`, `control/assist`, `control/ui`,
  `control/ui/session`, `control`, `app`. `webhook` ← `app`, `gate`, `control`. `oidc` ← `gate/ingest`, `gate`, `app`.

### Children

- **Counterpart**: internal/github/auth/, internal/github/api/, internal/github/webhook/, internal/github/oidc/
- **Direction**: the parent publishes them; it does not compose or import them
- **Strength**: Contract (publication only; the parent package imports no child)
- **LCA / Rank / Distance**: github / 1 / 1
- **Volatility**: Low
- **Balanced?**: Yes
- **Shared knowledge**: the rules above, which each child restates in its own Constraints.

### GitHub (external)

- **Counterpart**: GitHub REST API and one GraphQL mutation (`api`, `auth`), the OAuth web flow (`auth`), webhook
  deliveries (`webhook`), the Actions OIDC provider (`oidc`)
- **Direction**: the branch depends on GitHub
- **Strength**: Contract (public, documented, versioned where GitHub versions it)
- **LCA / Rank / Distance**: outside the tree; the highest distance the system has
- **Volatility**: Low
- **Balanced?**: Yes: contract strength tolerates the distance
- **Shared knowledge**: held by the children. App registration the branch assumes: permissions `checks: write`,
  `pull_requests: write`, `contents: write`, `actions: write`, `workflows: write`, `metadata: read`,
  `members: read`; events `pull_request`, `pull_request_review`, `push`, `installation`,
  `installation_repositories`; user authorization (OAuth) enabled.

## Internal Design

Four leaves, one edge.

```text
github/
├── auth/      mints credentials            (stdlib)
├── api/       REST by role                 (stdlib + auth: TokenSource)
├── webhook/   inbound, GitHub → App        (stdlib)
└── oidc/      inbound, workflow → App      (stdlib)
```

- `api` → `auth` is the only internal integration: contract strength, `TokenSource` only, distance 1, low
  volatility, balanced. `api` asks for an App JWT or an installation token per request; `auth` caches. On a `401`, `api` calls
  `Invalidate` and retries once with a fresh token.
- `webhook` and `oidc` are pure inbound verifiers. They share nothing with each other or with `api`: a webhook
  secret, a JWKS and an App key are three different trust roots, and no child sees another's.
- `app` wires the branch: `auth.New` → `api.New(tokenSource, client)`, `oidc.New(client)`, and calls
  `webhook.VerifyAny` and `webhook.Parse` in its webhook handler, reading the headers by `webhook.HeaderEvent`,
  `webhook.HeaderDelivery` and `webhook.HeaderSignature`. `OAuth` from `auth.New` goes to `control`.
- Typical inbound flow: a delivery is verified and parsed by `webhook`; the consumer builds a `RepoRef` from
  the event's installation ID and repository; `api` fetches an installation token through `auth` and calls GitHub.
- Typical upload flow: `oidc` verifies the token; the consumer resolves `Claims.Repository` with
  `api.ResolveRepo` (App JWT), which yields the authoritative installation.

Why four and not one: the key material differs per child, three of the four are pure or nearly so and test
without HTTP fakes for the others, and `api` alone is already wide. Why not more: splitting `api` by role would
multiply transport, cache and backoff wiring for no consumer benefit; the role interfaces give the same isolation.

## Change Vectors

- A new inbound GitHub mechanism (for example Marketplace billing events): a new child or a new `EventKind`.
- GitHub Enterprise Server support: base URLs and issuers across the children, nothing outside the branch.
- Replacing the hand-written client with an SDK: inside `api`, contracts unchanged.
- A change in App permissions or subscribed events: this document and the affected child.

## Constraints and Invariants

- No code in the parent package beyond `doc.go`. No child imports the parent; no child imports a sibling except
  `api` → `auth`.
- Nothing under `github` imports `wire`, `store`, `gate`, `control`, or `app`.
- No domain decisions (see Public Contract). Provenance is necessary, not sufficient: `oidc` and `webhook`
  authenticate a sender; they never make content true, and nothing here uses the word "verified" for a report.
- Fail closed at this layer means: error out. Never an empty list, a default value, or a skipped check.
- A JWKS outage surfaces as `ErrUnavailable` from `oidc` (the caller answers `503`); it never weakens verification.
- GitHub rate-limit exhaustion surfaces as `ErrRateLimited` from `api`; consumers leave checks pending and the
  sweep retries. It never produces green.
- Secrets (App key, webhook secret, OAuth secret, tokens) are passed in as bytes or strings by `app`, held by
  exactly one child each, and never logged or placed in errors.
- Standard library first; this branch needs nothing else.

## Test Specification

Child documents specify their own tests. These exercise the branch as a whole, with one fake GitHub
(`http.RoundTripper`) shared by the children.

### Unit Tests

- **Test name**: `TestGithubParentExportsNothing`
  **Scenario**: parse `internal/github/*.go`.
  **Expected behavior**: only `doc.go` and `_test.go` files exist (tests use package `github_test`); the package
  declares no exported identifier.
- **Test name**: `TestGithubImportGraph`
  **Scenario**: `go list -deps` for each of the four child packages.
  **Expected behavior**: standard library only, plus `internal/github/auth` from `api`; no `wire`, `store`, `gate`,
  `control`, `app`, or third-party package.

### Integration Contract Tests

- **Test name**: `TestCredentialChain`
  **Scenario**: `auth.New` with a generated key → `api.New` → `ResolveRepo` then `File` against the fake.
  **Expected behavior**: `ResolveRepo` carries a JWT that verifies against the key; `File` carries the installation
  token the fake issued for that installation; one token mint for repeated calls.
- **Test name**: `TestRevokedTokenIsReplacedOnce`
  **Scenario**: real `auth` and `api` against the fake; the fake starts answering `401` for the cached token and
  issues a new one on the next mint; then a fake that answers `401` for every token.
  **Expected behavior**: the call succeeds after one invalidate, one mint and one retry; in the second case an error
  after exactly two API requests.
- **Test name**: `TestWebhookEventAddressesAPI`
  **Scenario**: a signed `pull_request` delivery → `VerifyAny` → `Parse` → `RepoRef` from the event → `Pulls.Pull`.
  **Expected behavior**: the pull is fetched with the token of the event's installation; `HeadSHA` of event and
  API agree in the fixture.
- **Test name**: `TestOIDCClaimsResolveToInstallation`
  **Scenario**: a signed Actions token → `oidc.Verify` → `ResolveRepo(Claims.Repository)` → `Actions.Run(Claims.RunID)`.
  **Expected behavior**: the run is read with the resolved installation's token; `WorkflowPath` of the claims equals
  the run's `WorkflowPath` in the fixture.

### Boundary Tests

- **Test name**: `TestTrustRootsAreSeparate`
  **Scenario**: sign a webhook with the OAuth secret, an OIDC token with the App key, and an App JWT with a JWKS key.
  **Expected behavior**: all three are refused by the receiving side.
- **Test name**: `TestForgedAndCrossTenantIdentity` (T07)
  **Scenario**: a delivery with a bad signature naming installation B; a valid delivery of A followed by a
  `RepoRef` that pairs A's installation with B's repository.
  **Expected behavior**: the first never yields an `Event`; the second ends in `ErrNotFound`; B's token is never minted.
- **Test name**: `TestNoSecretInAnyError`
  **Scenario**: drive every child into each error path with marker secrets, tokens and payload strings.
  **Expected behavior**: no marker appears in any error text.

### Behavior Tests

- **Test name**: `TestUninstallAndRepositoryRemoval` (T07)
  **Scenario**: `installation_repositories` removed, then `installation` deleted; afterwards `api` calls for the
  removed repository and for the deleted installation.
  **Expected behavior**: `ReposRemoved` then `InstallationRemoved`; `ErrNotFound` for the repository; an error from
  the token mint for the installation; no cached data served.
- **Test name**: `TestRateLimitedBranchNeverReturnsData` (T11)
  **Scenario**: the fake rate-limits everything for 10 minutes while a consumer-like sequence runs
  (`OpenPulls`, `CheckRuns`, `CreateCheckRun`).
  **Expected behavior**: each call returns `ErrRateLimited` within its 30 s budget; no partial list; no check written.
- **Test name**: `TestUserLoginToAuthorizedRepository`
  **Scenario**: `AuthorizeURL` → `Exchange` → `Users.Permission` and `Repos.ResolveRepo` for the same repository.
  **Expected behavior**: permission comes from the user token, the installation ID from the App JWT path; neither
  value is taken from caller input.

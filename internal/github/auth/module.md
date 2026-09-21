# GitHub Auth

**Path**: internal/github/auth/
**Parent**: internal/github/ (GitHub integration, publishes its children)
**Submodules**: none (leaf)

## Purpose

`auth` mints every credential the App presents to GitHub: the App JWT, installation access tokens, and
user tokens from the OAuth web flow. Without it, each caller would sign JWTs and cache tokens on its own,
and a private key or token would pass through many packages instead of one.

## Functional Responsibilities

- Parse the App private key once, from bytes, and sign short-lived RS256 App JWTs with the standard library.
- Exchange the App JWT for an installation access token; cache it per installation until 5 minutes before expiry.
- Deduplicate concurrent mints for the same installation.
- Build the OAuth authorize URL with a caller-supplied `state`, exchange the code, and identify the user.
- Keep every secret out of logs and error texts.

Not here: reading key files (that is `app`), sessions and cookies (`control/ui/session`), any REST call other
than the three credential endpoints (`github/api`), any decision about what a token may be used for.

## Subdomain Classification

Generic. GitHub App authentication is a documented, stable protocol. Volatility is low; change comes only from
GitHub deprecations. It is kept as its own leaf because it is the only package that holds the private key.

## Encapsulated Knowledge

- App JWT: header `{"alg":"RS256","typ":"JWT"}`; claims `iss` = App ID as a decimal string, `iat` = now − 60 s
  **[default]** (clock drift), `exp` = `iat` + 10 min **[default: stays under GitHub's 10-minute limit]**. Signature is
  `rsa.SignPKCS1v15` over SHA-256 of `base64url(header).base64url(claims)`, unpadded base64url. A JWT is minted
  on every `AppJWT` call; none is cached **[default]**.
- Key parsing: PEM block `RSA PRIVATE KEY` (PKCS#1, what GitHub issues) or `PRIVATE KEY` (PKCS#8 holding RSA)
  **[default]**. Anything else, or a modulus under 2048 bits **[default]**, is a constructor error.
- Installation token endpoint: `POST https://api.github.com/app/installations/{id}/access_tokens`,
  `Authorization: Bearer <App JWT>`, `Accept: application/vnd.github+json`, `X-GitHub-Api-Version: 2022-11-28`,
  empty body. `201` → JSON `{"token": "...", "expires_at": "<RFC 3339>"}`. These tokens carry the
  installation's full permissions and all its repositories.
- Repository read token: the same endpoint with the JSON body
  `{"repositories": ["<name>"], "permissions": {"contents": "read"}}`, `<name>` being the part of `fullName` after
  the slash. The response must list exactly one repository whose `full_name` equals `fullName` (ASCII
  case-insensitive) and permissions that include `contents: read` and are no wider than it plus the
  `metadata: read` GitHub always adds;
  otherwise the token is discarded and the call fails **[default]**. Never cached, never stored.
- Cache: `map[int64]*entry` under one mutex; each entry has its own mutex held while minting
  **[default: mutex per installation]**. A cached token is served while `now < expires_at − 5 min` **[default: 5 min]**. A failed mint
  is never cached. Entries leave the cache only through `Invalidate`; a mint in flight at that moment puts its
  entry back when it completes: one small entry per installation is the accepted ceiling; revisit
  above ~100 000 installations.
- OAuth endpoints: authorize `https://github.com/login/oauth/authorize` with `client_id`, `redirect_uri`, `state`;
  no `scope` (a GitHub App's user token is limited by App permissions). Exchange
  `POST https://github.com/login/oauth/access_token`, form body `client_id`, `client_secret`, `code`,
  `Accept: application/json`. GitHub answers `200` both for success (`access_token`) and failure (`error`);
  the body decides. Then `GET https://api.github.com/user` with the user token fills `Login` and `UserID`.
- Clock: an unexported `now func() time.Time`, `time.Now` in `New`. Same-package tests use the unexported
  `newWithClock` **[default]**; the public constructor has no clock parameter.
- Hosts are fixed to github.com. Tests replace the transport of the `*http.Client`, not the URLs **[default]**.
  GitHub Enterprise Server is out of scope.

## Public Contract

```go
// Credentials are loaded by the composition root from secret references, never inline.
type Credentials struct {
    AppID             int64
    PrivateKeyPEM     []byte
    OAuthClientID     string
    OAuthClientSecret string
}

// TokenSource mints GitHub App credentials. Installation tokens are cached until 5 minutes before expiry.
type TokenSource interface {
    AppJWT() (string, error)
    InstallationToken(ctx context.Context, installationID int64) (string, error)
    // Invalidate drops the cached installation token (called after a 401).
    Invalidate(installationID int64)
    // RepoReadToken mints an uncached token limited to contents:read on one repository.
    RepoReadToken(ctx context.Context, installationID int64, fullName string) (string, error)
}
```

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

```go
func New(c Credentials, hc *http.Client) (TokenSource, OAuth, error)
```

`New` is the only constructor. Both returned values share one parsed key and one HTTP client.

### Behavior rules

- `New` fails when `AppID` ≤ 0, the key does not parse, or either OAuth field is empty **[default: all four
  are required; the App always runs with the UI]**. A `nil` client means a client with a 10 s timeout **[default]**.
  `New` makes no network call.
- `AppJWT` never touches the network. It fails only if signing fails.
- `InstallationToken`: `installationID` ≤ 0 → error without a request. Non-`201` → error carrying the status
  code only. A `404` (uninstalled) or `403` (suspended installation) is such an error; nothing is cached, so
  access revoked at GitHub is noticed at the next mint, or at once when `api` gets a `401` and invalidates.
- `Invalidate` drops the cached token of one installation and nothing else. It never fails, makes no request,
  and is a no-op for an unknown or non-positive ID. The next `InstallationToken` mints. A mint already in flight
  for that installation still completes and its token is cached: it is newer than the one that drew the `401`.
- `RepoReadToken`: `installationID` ≤ 0, a `fullName` not matching `^[A-Za-z0-9_.-]+/[A-Za-z0-9_.-]+$`, or an owner
  or name that is `.` or `..` (the pattern alone admits `../x`) → error without a request. Every call mints;
  nothing is cached and the installation cache is neither read nor changed.
  Non-`201` (a `422` when the repository is not in the installation) → error with the status only. A token wider
  than asked for is never returned.
- `AuthorizeURL` is pure. It query-escapes `state` and `redirectURL`. The caller generates, stores and compares
  `state`; this package never sees the callback request.
- `Exchange`: empty `code` → error without a request. An `error` field in the body, a missing `access_token`,
  or a failing `GET /user` → error. No refresh token is kept: an expired user token means signing in again.
- Both interfaces are safe for concurrent use.
- Errors start with `github/auth:` and hold status codes and installation IDs only: never a token, JWT, code,
  client secret, key material, or response body.
- A transport error (no response) is wrapped by its cause only. The `*url.Error` that `http.Client` returns
  carries the method and the full URL; it is stripped to its `Err`, exactly as `github/api` does, so no error
  text holds a URL or a host name. The cause stays in the chain: `errors.Is(err, context.DeadlineExceeded)` and
  `errors.Is(err, context.Canceled)` work.

## Integrations

`auth` imports only the standard library. Its contract above is the shared knowledge with every consumer;
this file is its normative home, so nothing is restated for them.

### github/api

- **Counterpart**: internal/github/api/
- **Direction**: `github/api` depends on `auth`
- **Strength**: Contract
- **LCA / Rank / Distance**: github / 1 / 1
- **Volatility**: Low
- **Balanced?**: Yes
- **Shared knowledge**: `TokenSource`. `api` calls `AppJWT` for App-level endpoints and `InstallationToken` for
  everything addressed by an installation ID. On a `401` it calls `Invalidate` and retries once with a fresh
  token. It treats errors as opaque. It never calls `RepoReadToken`.

### control/onboarding

- **Counterpart**: internal/control/onboarding/ (wired through the `control` façade's `Deps`)
- **Direction**: `onboarding` depends on `auth`
- **Strength**: Contract
- **LCA / Rank / Distance**: root / 2 / 2
- **Volatility**: High (the onboarding side)
- **Balanced?**: Yes
- **Shared knowledge**: `TokenSource.RepoReadToken` only: the one credential the managed-discovery sandbox gets
  for its clone. `auth` knows nothing about sandboxes or consent.

### control/ui/session

- **Counterpart**: internal/control/ui/session/
- **Direction**: `session` depends on `auth`
- **Strength**: Contract
- **LCA / Rank / Distance**: root / 3 / 3
- **Volatility**: Low
- **Balanced?**: Yes
- **Shared knowledge**: `OAuth`, `UserToken`. `session` owns `state`, the redirect URL, and the cookie.

### app and the control façade

- **Counterpart**: internal/app/, internal/control/
- **Direction**: `app` depends on `auth` (calls `New` with `Credentials` built from secret files); `control`
  depends on `auth` (an `OAuth` field in its `Deps`, handed to `session`)
- **Strength**: Contract
- **LCA / Rank / Distance**: root / 2 / 2
- **Volatility**: Low (`app`), Moderate (`control`)
- **Balanced?**: Yes
- **Shared knowledge**: `Credentials`, `New`, `OAuth`. `app` reads the key and secret files; `auth` takes bytes.

### GitHub (external)

- **Counterpart**: GitHub REST API (`/app/installations/{id}/access_tokens`, `/user`) and the OAuth web flow
- **Direction**: `auth` depends on GitHub
- **Strength**: Contract (public, versioned API; header `X-GitHub-Api-Version: 2022-11-28`)
- **LCA / Rank / Distance**: outside the tree; the highest distance the system has
- **Volatility**: Low
- **Balanced?**: Yes: contract strength tolerates the distance
- **Shared knowledge**: the endpoints, headers, claims and response fields listed under Encapsulated Knowledge.
  Response JSON is decoded leniently: only the named fields are read, unknown fields are ignored.

## Change Vectors

- GitHub changes JWT limits, the API version header, or token lifetimes.
- A different dedupe or cache structure.
- Another narrowed token shape (other permissions, several repositories): a new method beside `RepoReadToken`.
- Refresh tokens for expiring user tokens.
- GitHub Enterprise Server base URLs.

## Constraints and Invariants

- The private key exists only inside this package, parsed; `PrivateKeyPEM` is not retained after `New`.
- Never log. This package has no logger. Secrets never appear in an error.
- A token is returned only if GitHub issued it for exactly the requested installation ID; the cache key is that
  ID. No token of installation A can be returned for B.
- The installation ID is trusted by the caller's construction (verified webhook, verified OIDC claims resolved
  through the API, or an authorized session). `auth` makes no tenancy decision.
- A cached token is never served within 5 minutes **[default]** of its expiry.
- Every network call honors `ctx` and the client timeout. Waiting on another caller's mint is bounded by that
  caller's request timeout; the lock wait itself is not cancellable (accepted ceiling).
- No retries here. Rate limits and backoff belong to `github/api`; a failed mint is simply an error.
- No domain decision lives here: nothing about gates, entitlements, or permissions.
- Standard library only: `crypto`, `crypto/rsa`, `crypto/sha256`, `crypto/x509`, `encoding/pem`, `encoding/base64`,
  `encoding/json`, `net/http`, `net/url`, `sync`, `time`, and the plain helpers `bytes`, `context`, `errors`,
  `fmt`, `io`, `regexp`, `strconv`, `strings`. No `log`, `log/slog` or `os`.

## Test Specification

GitHub is faked with an `http.RoundTripper` inside the `*http.Client`; the clock with `newWithClock`.

### Unit Tests

- **Test name**: `TestAppJWTClaimsAndSignature`
  **Scenario**: fixed clock, generated 2048-bit key, App ID 12345.
  **Expected behavior**: three unpadded base64url parts; header `RS256`/`JWT`; `iss` is `"12345"`; `iat` = now − 60 s;
  `exp` = `iat` + 600 s; the signature verifies with the public key.
- **Test name**: `TestParsePrivateKey`
  **Scenario**: table: PKCS#1 PEM; PKCS#8 RSA PEM; PKCS#8 EC key; 1024-bit RSA; garbage; empty; PEM with trailing data.
  **Expected behavior**: ok; ok; error; error; error; error; ok (first block used).
- **Test name**: `TestTokenCacheWindow`
  **Scenario**: token expiring at T+60 min; calls at T, T+54 min, T+55 min, T+56 min.
  **Expected behavior**: one mint for the first two calls; a second mint at T+55 min; the call at T+56 min is served from the new token.
- **Test name**: `TestFailedMintIsNotCached`
  **Scenario**: GitHub answers `500`, then `201`.
  **Expected behavior**: first call errors; second call mints and returns the token.
- **Test name**: `TestAuthorizeURLEscapes`
  **Scenario**: table of `state` and redirect values with `&`, `=`, space, and unicode.
  **Expected behavior**: host and path are the GitHub authorize endpoint; parsing the query returns the inputs exactly; no `scope` parameter.

### Integration Contract Tests

- **Test name**: `TestInstallationTokenRequestShape`
  **Scenario**: capture the mint request for installation 77.
  **Expected behavior**: `POST /app/installations/77/access_tokens`; bearer is a JWT that verifies; the Accept and
  API version headers are set; empty body.
- **Test name**: `TestInvalidate`
  **Scenario**: table: invalidate a cached installation; an unknown one; ID 0; invalidate 77 while 78 is cached;
  invalidate during an in-flight mint (run with `-race`).
  **Expected behavior**: next call mints; no-op; no-op; 78 still served from cache; no race, the in-flight token is cached.
- **Test name**: `TestRepoReadTokenRequestShape`
  **Scenario**: capture the request for installation 77 and `octo/repo`; call twice.
  **Expected behavior**: `POST /app/installations/77/access_tokens` with body repositories `["repo"]` and permissions
  `{"contents":"read"}`; two requests for two calls (no cache).
- **Test name**: `TestConcurrentMintsAreDeduplicated`
  **Scenario**: 50 goroutines call `InstallationToken(77)` while the fake delays 50 ms; 10 more call it for 78; run with `-race`.
  **Expected behavior**: exactly one request per installation; all callers of one installation get the same token.
- **Test name**: `TestExchangeReturnsIdentifiedUser`
  **Scenario**: the fake returns an access token, then `{"login":"octocat","id":583231}` for `GET /user`.
  **Expected behavior**: `UserToken{AccessToken, "octocat", 583231}`; the exchange request was a form POST with
  `Accept: application/json`; `GET /user` carried the user token.
- **Test name**: `TestNewValidatesCredentials`
  **Scenario**: table: valid; `AppID` 0; bad key; empty client ID; empty client secret; `nil` client.
  **Expected behavior**: ok; four errors; ok with the default client. No request is made in any row.

### Boundary Tests

- **Test name**: `TestMintErrorStatuses` (T07: revoked access)
  **Scenario**: table of mint responses: `401`, `403` (suspended), `404` (uninstalled), `422`, `500`, `201` with
  empty token, `201` with unparsable `expires_at`, malformed JSON.
  **Expected behavior**: an error for each, naming the status; nothing cached; the next call requests again.
- **Test name**: `TestRevokedInstallationAfterCacheExpiry`
  **Scenario**: mint succeeds; advance the clock 56 min; GitHub now answers `404`.
  **Expected behavior**: error; the old token is not returned.
- **Test name**: `TestNoCrossInstallationToken` (T07: cross-installation identifiers)
  **Scenario**: the fake issues a distinct token per installation path; interleave calls for IDs 1 and 2; call with 0 and −1.
  **Expected behavior**: each ID only ever gets its own token; 0 and −1 error without a request.
- **Test name**: `TestRepoReadTokenRefusesWiderOrWrongScope`
  **Scenario**: table of `201` responses: exact scope (with `metadata: read`); extra `contents: write`; extra
  `actions: read`; two repositories; another repository's `full_name`; no `repositories` field; plus `422`, `404`,
  bad `fullName` values (`a`, `a/b/c`, `../x`), ID 0.
  **Expected behavior**: token returned for the first row only; an error for every other; no request for bad input.
- **Test name**: `TestExchangeFailures`
  **Scenario**: table: body `{"error":"bad_verification_code"}` with `200`; no `access_token`; `GET /user` `401`;
  empty code; cancelled context.
  **Expected behavior**: an error for each; no `UserToken` with a non-empty `AccessToken` is returned.
- **Test name**: `TestErrorsAndStringsCarryNoSecrets`
  **Scenario**: trigger every error path with marker strings as key bytes, client secret, code, token, and response body;
  also format the returned values with `%v` and `%+v`.
  **Expected behavior**: no marker appears in any error text or formatted `TokenSource`/`OAuth` value.
- **Test name**: `TestTransportErrorsCarryNoURL`
  **Scenario**: table over `InstallationToken`, `RepoReadToken`, `Exchange` (failing at the code exchange and at
  `GET /user`), each with a transport that fails with a plain error and with one that fails with
  `context.DeadlineExceeded`.
  **Expected behavior**: an error starting with `github/auth:` for each; no `https://`, `github.com` or request
  path in any text; `errors.Is(err, context.DeadlineExceeded)` holds for the timeout rows and the plain cause is
  reachable with `errors.Is` for the others.
- **Test name**: `TestAuthImportsOnlyStdlib`
  **Scenario**: `go list -deps ./internal/github/auth`.
  **Expected behavior**: no internal or third-party package; `log`, `log/slog` and `os` are not direct imports.

### Behavior Tests

- **Test name**: `TestLongRunningProcessKeepsWorkingTokens`
  **Scenario**: simulate 3 hours of calls every 5 minutes for two installations with 60-minute tokens.
  **Expected behavior**: no returned token is within 5 minutes of expiry; about one mint per installation per 55 minutes.
- **Test name**: `TestUserLoginRoundTrip`
  **Scenario**: build the authorize URL with a state, then exchange the code the fake hands back.
  **Expected behavior**: the URL carries the state unchanged; the exchange yields login and user ID; `auth` kept no state between the two calls.
- **Test name**: `TestInvalidateAfter401Recovers`
  **Scenario**: the caller pattern of `github/api`: token T1 cached; GitHub revoked it; the caller invalidates and asks again.
  **Expected behavior**: the second `InstallationToken` mints T2 well before T1's cache window ends; T1 is never returned again.
- **Test name**: `TestSandboxGetsOnlyARepoReadToken`
  **Scenario**: `RepoReadToken(9, "o/r")` while a full installation token for 9 is cached.
  **Expected behavior**: a new, distinct token; the request asked for `contents: read` on `r` only; the cached full
  token is neither returned nor replaced.
- **Test name**: `TestUninstallStopsNewWork` (T07)
  **Scenario**: installation 9 is deleted at GitHub; the cache entry has expired.
  **Expected behavior**: every later `InstallationToken(9)` is an error; other installations are unaffected.

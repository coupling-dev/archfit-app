# UI Session

**Path**: internal/control/ui/session/
**Parent**: internal/control/ui/ (UI)
**Submodules**: none (leaf)

## Purpose

Session answers two questions for every UI request: who is the signed-in GitHub user, and may that
user touch this repository right now. It is the only place where a browser request becomes a tenant
identity. Without it the UI would have to trust repository or installation IDs from URLs and forms,
and a user whose GitHub access was revoked would keep seeing a tenant's data.

## Functional Responsibilities

- Run the GitHub OAuth web flow: login redirect with a state cookie, callback with state check and code exchange, logout.
- Keep the session in one encrypted, authenticated cookie. There is no server-side session table.
- Return the signed-in `User` for a request, or "not signed in".
- End a session on request (`End`), so the UI can drop a session whose GitHub token GitHub no longer accepts.
- Authorize a user for a repository at a minimum permission, using the user's own OAuth token, and
  return the `Repo` with the authoritative installation ID.
- Cache authorization results for 60 seconds **[default]** so that a page load does not cost one GitHub call per handler.

## Subdomain Classification

Generic (login, cookies, permission lookup are solved problems), placed inside a core-adjacent parent.
Volatility: low. GitHub OAuth and the repository permission model are stable. The cookie format is
private and can change with a forced re-login.

## Encapsulated Knowledge

- Cookie names, layout and cryptography: AES-256-GCM, 12-byte random nonce, the cookie name as
  additional authenticated data, base64url without padding.
- Session payload (JSON, inside the ciphertext only): login, user ID, OAuth access token, expiry (Unix seconds).
- Lifetimes: session 8 hours, the lifetime of an expiring GitHub user access token (no refresh token is kept), OAuth state 10 minutes **[default]**, no sliding renewal **[default]**.
- The OAuth state value (32 random bytes, hex) and the rule for a safe return path.
- The permission order `none < read < write < admin`, and that any unknown permission string counts as `none`.
- The authorization cache: key, lifetime, size bound, what is cached and what never is.

## Public Contract

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

```go
var ErrForbidden = errors.New("session: not permitted")

type Config struct {
    CookieKey []byte // 32 bytes, from a secret reference
    BaseURL   string
}

// New fails unless CookieKey is 32 bytes and BaseURL is an absolute https URL (http allowed for localhost).
func New(c Config, o OAuth, u Users, repos Repos, now func() time.Time) (Sessions, error)
```

### Handler behavior

The parent mounts the handlers at fixed paths **[default]**. Session depends on the callback path
because it builds the OAuth redirect URL as `BaseURL + "/auth/callback"`.

- `GET /auth/login` → `LoginHandler`. Reads the optional `return` query (a local path). Sets the state cookie.
  `303` to `OAuth.AuthorizeURL(state, BaseURL+"/auth/callback")`.
- `GET /auth/callback` → `CallbackHandler`. Requires `code` and `state`. `state` must equal the state cookie
  (constant-time compare) and the cookie must not be expired. Then `OAuth.Exchange`. Success: set the session cookie,
  delete the state cookie, `303` to the return path. Bad or missing state or code: `400`, no session cookie.
  Exchange error: `502`, no session cookie.
- `POST /auth/logout` → `LogoutHandler`. Deletes the session cookie, `303` to `/`. Any other method: `405`.
  The GitHub token is not revoked at GitHub **[default]**.

### Rules of the two methods

- `User` returns false for a missing, malformed, tampered, wrong-key, or expired cookie. It never returns an error and never writes a response.
- `Authorize` returns `ErrForbidden` when: the permission is below `minPermission`; the permission is `none` or unknown;
  `minPermission` is not one of the three values; `fullName` is not `owner/name`; GitHub answers "not found" for the
  permission or for `Repos.ResolveRepo` (App not installed on it). `ErrForbidden` is the sentinel callers test with `errors.Is`.
- `Permission` answers `api.ErrUserTokenRejected` (the user's token expired or was revoked): `Authorize` returns an error that
  wraps it, caches nothing, and logs outcome `token_rejected` at Info. The session cookie can outlive the token, for
  example after a revocation on GitHub or a cookie sealed by an older build, so this is an expected end of a session, not a fault.
- Any other GitHub error (including rate limit exhaustion) is returned wrapped and is never cached. No error ever yields a `Repo`.
  It is logged as `github_error` with the failing `step` (`permission` or `resolve repository`) and the HTTP `status`
  (`0` when GitHub gave no answer); a rate limit is logged as `rate_limited` with neither.
- `End` writes the same cookie deletion as logout and nothing else. It does not revoke the token at GitHub **[default]**.
- `New` returns an error when `CookieKey` is not exactly 32 bytes, or `BaseURL` is not an absolute `https` URL (`http` is
  accepted only for host `localhost`, `127.0.0.1` or `[::1]`). The error names the field, never its value. A bad key
  stops the process at start-up, not at the first login.

## Integrations

### internal/control/ui (parent)

- **Counterpart**: `internal/control/ui/module.md`
- **Direction**: ui depends on session (composition; `ui.New` calls `New`, mounts the handlers, and calls `User` and `Authorize` in every handler). Session never imports ui.
- **Strength**: functional (composition)
- **LCA / Rank / Distance**: control/ui / 1 / 1
- **Volatility**: high (the UI), low on this side
- **Balanced?**: yes — parent and child, distance 1
- **Shared knowledge**: the contract above and the three mount paths. Session knows nothing else about pages.

### internal/github/auth

- **Counterpart**: `internal/github/auth/module.md`
- **Direction**: session depends on github/auth
- **Strength**: contract
- **LCA / Rank / Distance**: root / 3 / 3
- **Volatility**: low
- **Balanced?**: yes — contract coupling tolerates the distance
- **Shared knowledge**: the OAuth flow interface. The `state` value is supplied by session; github/auth never stores it.

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

### internal/github/api

- **Counterpart**: `internal/github/api/module.md`
- **Direction**: session depends on github/api (`Users`, `Repos`, `Repo`)
- **Strength**: contract
- **LCA / Rank / Distance**: root / 3 / 3
- **Volatility**: low
- **Balanced?**: yes — contract coupling tolerates the distance
- **Shared knowledge**: `Users.Permission` with the user's token, `Repos.ResolveRepo` with the App JWT. Session calls no
  other method of `Repos`. `ErrNotFound` is the sentinel github/api returns for an unknown or uncovered repository;
  `ErrRateLimited` is the sentinel it returns after bounded backoff; `ErrUserTokenRejected` is the sentinel it returns for
  a `401` on the user's token. `api.Status(err)` gives the HTTP status of an answer that maps to no sentinel, for the log.

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

// Status returns the HTTP status of a GitHub answer that mapped to no sentinel, or 0 for any other error.
func Status(err error) int
```

<!-- contract: Repos, Installation — restated from internal/github/api/module.md -->
```go
type Installation struct {
    ID      int64
    Account string // organization or user login
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

## Change Vectors

- Session lifetime, sliding renewal, cookie names or cookie cryptography (forces a re-login, nothing else).
- Cache lifetime, size, or eviction.
- Key rotation with a second accepted key.
- Revoking the GitHub token at logout.
- A finer permission order if GitHub's `triage` or `maintain` ever need to be told apart.

## Constraints and Invariants

- Tenant identity comes only from `Authorize`: the installation ID is the one `Repos.ResolveRepo` returns. Session
  reads no installation or repository ID from the URL, query, form, header, or cookie payload.
- Authorization uses the user's own token. App credentials are used only to find the covering installation, and only
  after the user's permission was established.
- Fail closed: every error path ends without a `Repo` and without a session cookie.
- Cookies: `HttpOnly`, `SameSite=Lax`, `Path=/`, `Secure` when `BaseURL` is `https`. Names `archfit_session` and
  `archfit_oauth` **[default]**. Expiry is inside the ciphertext and checked against the injected clock; the browser's
  `Max-Age` is a convenience only.
- The state cookie payload is `{state, return path, expiry}`, encrypted the same way under its own cookie name as
  additional data, so a session cookie can never be replayed as a state cookie.
- Return path rule: must start with `/`, must not start with `//` or `/\`, must not contain a scheme or a control
  character. Anything else becomes `/`. No open redirect.
- Cache: key `(UserID, lower-case fullName)`; value `(permission, Repo)`; lifetime 60 seconds **[default]** from the GitHub answer;
  only complete successful answers and the answer `none` are cached. At most 10 000 entries **[default]**: when full,
  expired entries are dropped, then the whole cache is cleared. A revoked user loses access within 60 seconds.
- Safe for concurrent use. One mutex around the cache is enough **[default]**; GitHub calls run outside the lock.
- One key, no rotation: changing `CookieKey` signs everyone out. Stated limit.
- Logs carry user ID, repository ID, installation ID, outcome, timing and, for `github_error`, the step and HTTP status only. Never the token, the code, the state,
  the cookie value, or the login flow's query string.
- Standard library only: `crypto/aes`, `crypto/cipher`, `crypto/rand`, `crypto/subtle`, `encoding/base64`, `net/http`.
- Inherited: session is control-plane code. Nothing on the gate path calls it; its failure never changes a check.

## Test Specification

GitHub is faked at the `OAuth`, `Users` and `Repos` interfaces. The clock is injected. Nothing else is mocked.

### Unit Tests

- **Test name**: `TestCookieRoundTrip`
  **Scenario**: seal a `User` with a fixed key and clock, then open it.
  **Expected behavior**: the same `User` is returned; two seals of the same user differ (random nonce).
- **Test name**: `TestCookieRejectsTamperingAndExpiry`
  **Scenario**: table: one flipped bit; truncated value; invalid base64; empty value; sealed with another key; session
  cookie value presented under the state cookie name; clock 8 h + 1 s later.
  **Expected behavior**: every row opens as "not signed in"; no panic; nothing is logged but an outcome.
- **Test name**: `TestPermissionOrder`
  **Scenario**: table of `(have, min)` over `none`, `read`, `write`, `admin`, plus `triage`, `""`, `ADMIN`.
  **Expected behavior**: allowed only when `have >= min`; unknown `have` counts as `none`; unknown `min` is never allowed.
- **Test name**: `TestReturnPathIsLocal`
  **Scenario**: table: `/r/o/n`, `/`, empty, `//evil.example`, `/\evil.example`, `https://evil.example`, `javascript:x`, a path with `\r\n`.
  **Expected behavior**: the first two are kept; every other row becomes `/`.
- **Test name**: `TestCacheExpiryAndBound`
  **Scenario**: authorize, advance the clock 59 s and 61 s; then fill the cache past 10 000 entries.
  **Expected behavior**: one GitHub call before 60 s, a second after; the entry count never exceeds the bound.

### Integration Contract Tests

- **Test name**: `TestLoginRedirectsWithStateCookie`
  **Scenario**: `GET /auth/login?return=/r/o/n`.
  **Expected behavior**: `303` to the URL `OAuth.AuthorizeURL` returned for the cookie's state and `BaseURL+"/auth/callback"`;
  state cookie is `HttpOnly`, `SameSite=Lax`.
- **Test name**: `TestCallbackCreatesSession`
  **Scenario**: callback with the matching state and a code the fake exchanges for a `UserToken`.
  **Expected behavior**: `303` to `/r/o/n`; state cookie deleted; a following request yields that `User` from `User(r)`.
- **Test name**: `TestCallbackRejectsBadState`
  **Scenario**: table: no state cookie; different state; expired state cookie; missing code; exchange error.
  **Expected behavior**: `400` for the first four, `502` for the last; no session cookie in any row; `Exchange` is not called in the first four.
- **Test name**: `TestLogoutClearsSession`
  **Scenario**: `POST /auth/logout` with a session; then `GET /auth/logout`.
  **Expected behavior**: cookie deleted and `303` to `/`; the `GET` answers `405` and keeps the cookie.
- **Test name**: `TestAuthorizeReturnsResolvedRepo`
  **Scenario**: user has `write`; `ResolveRepo` returns installation 42.
  **Expected behavior**: `Authorize(..., "write")` returns the `Repo` with `InstallationID == 42`; `Permission` was called with the user's token.
- **Test name**: `TestAuthorizeMinimumPermission`
  **Scenario**: table of user permission against `read`, `write`, `admin`.
  **Expected behavior**: `ErrForbidden` below the minimum, the `Repo` otherwise.

### Boundary Tests

- **Test name**: `TestAuthorizeFailsClosedOnGitHubErrors`
  **Scenario**: table: `Permission` returns `ErrNotFound`, `ErrRateLimited`, `ErrUserTokenRejected`, a transport error; `ResolveRepo` returns the same three.
  **Expected behavior**: `ErrNotFound` rows give `ErrForbidden`; the others give a wrapped error (the rejected token wraps `ErrUserTokenRejected`); no row returns a `Repo`; no row is cached.
- **Test name**: `TestAuthorizeRejectsMalformedFullName`
  **Scenario**: table: empty, `owner`, `a/b/c`, `../x`, a name with a space or a query string.
  **Expected behavior**: `ErrForbidden` and no GitHub call.
- **Test name**: `TestSecretsNeverLeave`
  **Scenario**: run login, callback, authorize and failures with a capturing logger and a recognizable token, code and state.
  **Expected behavior**: none of the three strings appears in a log line, a response body, or a redirect URL other than the state in the authorize URL.
- **Test name**: `TestNewRejectsBadConfig`
  **Scenario**: table: 31-byte key; 33-byte key; nil key; relative `BaseURL`; `http://app.example`; `ftp://x`; then valid:
  `https://app.example`, `http://localhost:8080`, `http://127.0.0.1:8080`.
  **Expected behavior**: error and nil `Sessions` for the first six, naming the field and not its value; no panic; the
  valid rows return `Sessions` and nil error.

### Behavior Tests

- **Test name**: `TestRejectedUserTokenIsAnInfoOutcomeAndNeverCached`
  **Scenario**: `Permission` returns an error wrapping `ErrUserTokenRejected`.
  **Expected behavior**: `Authorize` returns an error that wraps it and is not `ErrForbidden`; no `Repo`; nothing cached; one log line
  with outcome `token_rejected` at Info.
- **Test name**: `TestGitHubErrorLogsStepAndStatusOnly`
  **Scenario**: a real `api.Client` whose transport answers `403` with a recognizable body.
  **Expected behavior**: the log line has outcome `github_error`, step `permission`, status `403`; it holds no token, body or URL.
- **Test name**: `TestEndDeletesTheSessionCookie`
  **Scenario**: `End` on a recorder.
  **Expected behavior**: the response deletes `archfit_session`.
- **Test name**: `TestRevokedUserLosesAccessWithinCacheWindow` (T07, T17)
  **Scenario**: user has `write`, is authorized, then GitHub answers `none`; clock advances 61 s.
  **Expected behavior**: the next `Authorize` returns `ErrForbidden`; no later call returns the `Repo`.
- **Test name**: `TestRemovedRepositoryIsForbidden` (T07)
  **Scenario**: the repository is removed from the installation: `ResolveRepo` now returns `ErrNotFound`; cache expired.
  **Expected behavior**: `ErrForbidden`, even though the user still has `admin` on the repository.
- **Test name**: `TestCrossInstallationIdentifierHasNoEffect` (T07, T17)
  **Scenario**: a user of installation A requests `fullName` of installation B where GitHub gives them `none`; the request
  also carries installation A's ID in query, form and header.
  **Expected behavior**: `ErrForbidden`; the supplied IDs are never read; `ResolveRepo` is not called.
- **Test name**: `TestSessionEndsAfterTwelveHours`
  **Scenario**: sign in, advance the clock 8 h + 1 s.
  **Expected behavior**: `User` returns false; a protected page redirects to login; no GitHub call is made with the old token.

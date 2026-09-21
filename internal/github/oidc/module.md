# GitHub Actions OIDC

**Path**: internal/github/oidc/
**Parent**: internal/github/ (GitHub integration, publishes its children)
**Submodules**: none (leaf)

## Purpose

`oidc` verifies the GitHub Actions OIDC token that accompanies every upload and returns the claims the App
uses. It is the only package that knows the issuer, the JWKS endpoint and the claim names. Without it, an
upload would be anonymous: nothing would tie a report to a repository, a workflow run, or an attempt.

## Functional Responsibilities

- Verify the RS256 signature of a token against the issuer's JWKS, fetched and cached here.
- Validate issuer, exact audience, and the time claims with 60 s leeway **[default]**.
- Return typed `Claims`; turn GitHub's string-encoded numbers into integers.
- Extract the workflow file path from `job_workflow_ref` for a given repository.

Not here: reading the `Authorization` header, comparing claims with the envelope or the GitHub API, deciding
eligibility (all `gate/ingest`); any GitHub REST call.

## Subdomain Classification

Generic. OIDC token verification is a standard; GitHub's claim set is documented and stable. Volatility is low.

## Encapsulated Knowledge

- Issuer: `https://token.actions.githubusercontent.com`. JWKS URL: the issuer plus `/.well-known/jwks`, fetched
  directly; the discovery document is not read **[default]**.
- Token form: three unpadded base64url parts. Header `alg` must be exactly `RS256`; `kid` is required. `none`,
  `HS256` and every other algorithm are rejected before any key lookup. Tokens over 16 KiB are rejected **[default]**.
- JWK use: `kty` `RSA`; `use` absent or `sig`; `alg` absent or `RS256`; `n`, `e` base64url; modulus at least
  2048 bits **[default]**. Other keys in the set are skipped. Verification is `rsa.VerifyPKCS1v15` with SHA-256 over
  `header.payload`.
- Key cache: the key set is kept for 1 hour **[default]**. An unknown `kid` triggers one refetch, at most once per minute
  across all callers **[default]**, so a flood of random `kid` values cannot become a fetch flood. Fetches are
  single-flight under one mutex. If a refetch fails and a cached set exists, the cached set stays in use for up
  to 24 hours **[default]**; with no usable set, every token is rejected with `ErrUnavailable`. A JWKS response that is non-`200`, malformed,
  or over 1 MiB **[default]** counts as a failed fetch.
- Claims read: `iss`, `aud`, `exp`, `nbf`, `iat`, `repository`, `repository_id`, `run_id`, `run_attempt`, `sha`,
  `ref`, `event_name`, `job_workflow_ref`, `runner_environment`. GitHub sends `repository_id`, `run_id` and
  `run_attempt` as JSON strings; a JSON number is accepted too **[default]**. Non-numeric or non-positive → error.
- `aud` may be a JSON string or an array; the required audience must be one of its values, compared exactly
  (case and trailing slash matter).
- Time rules with leeway L = 60 s **[default]**: `exp` required, `now ≤ exp + L`; `iat` required, `iat ≤ now + L`; `nbf`
  optional, `now ≥ nbf − L`.
- Required non-empty claims **[default]**: all of the above except `nbf` and `runner_environment`.
  `RunnerEnvironment` is informative (logs only); self-hosted runners are accepted.
- Unknown claims are ignored: GitHub adds claims over time.
- `job_workflow_ref` has the form `owner/name/<path>@<ref>`. `WorkflowPath` needs the prefix `fullName + "/"`
  (ASCII case-insensitive, as GitHub repository names are), cuts at the last `@`, and returns `""` if the remaining
  path is empty or still contains `@` **[default]**. Cutting at the last `@` can only lengthen a path, never
  shorten it, so a file named `x.yml@evil.yml` can never read as `x.yml`.
- Clock: an unexported `now func() time.Time`; same-package tests use the unexported `newWithClock` **[default]**.
  The public constructor has no clock parameter.

## Public Contract

```go
// Claims are the verified GitHub Actions OIDC claims the App uses.
type Claims struct {
    Repository        string // "owner/name"
    RepositoryID      int64
    RunID             int64
    RunAttempt        int
    SHA               string
    Ref               string
    EventName         string
    JobWorkflowRef    string // "owner/name/<path>@<ref>"
    RunnerEnvironment string // "github-hosted" | "self-hosted"
}

// ErrUnavailable means the signing keys could not be fetched; the caller answers 503 so the producer retries.
var ErrUnavailable = errors.New("oidc: signing keys unavailable")

// Verifier validates signature (cached JWKS), issuer, audience, and time claims.
// A verified token authenticates the producer; it never proves the payload is true.
type Verifier interface {
    Verify(ctx context.Context, token, audience string) (Claims, error)
}
```

```go
func New(hc *http.Client) Verifier

// WorkflowPath extracts "<path>" from a job_workflow_ref for repository fullName; "" if it points elsewhere.
func WorkflowPath(jobWorkflowRef, fullName string) string
```

### Behavior rules

- `New` makes no network call; the first `Verify` fetches the keys. A `nil` client means a client with a 10 s
  timeout **[default]**. The fetch host is fixed; tests replace the client's transport.
- `Verify` returns an error and zero `Claims` on any failed check. There is no partial result.
- Empty `token` or empty `audience` → error without a fetch. An empty audience never matches.
- Check order: size and shape → header (`alg`, `kid`) → key lookup (with the bounded refetch) → signature →
  `iss` → `aud` → time → required claims. Claims of an unverified token are never returned or used, except
  `kid` for the lookup.
- Errors start with `github/oidc:` and name the failed check only (`signature`, `issuer`, `audience`, `expired`,
  `not yet valid`, `claim run_id`). Never the token, a claim value, or a key.
- `ErrUnavailable` is the sentinel for exactly one situation: no cached key matches the token's `kid` and the most
  recent JWKS fetch attempt failed (cold cache, a failed refetch for an unknown `kid`, or a cached set past
  24 hours). Callers test it with `errors.Is` and answer `503` so the producer retries. Every other failure is an
  ordinary verification error (`401` at the caller): a bad token never becomes `ErrUnavailable` while keys are
  available, and an unknown `kid` after a successful fetch is an ordinary error. Shape, size and `alg` checks run
  before any key lookup, so a malformed token is never `ErrUnavailable`.
- A `Verifier` is safe for concurrent use.
- `WorkflowPath` is pure. It returns `""` for: a ref of another repository (a reusable workflow elsewhere),
  no `@`, an empty path, an empty argument. Stated limits: a branch name containing `@` yields `""` (pull
  request runs use `refs/pull/<n>/merge`, so this only affects such a default branch); the upload job must be defined in the approved
  workflow file itself; a job inside a reusable workflow reports that workflow's path.

## Integrations

`oidc` imports only the standard library and depends on no internal module. This file is the normative home
of its contract, so nothing is restated for consumers.

### gate/ingest

- **Counterpart**: internal/gate/ingest/
- **Direction**: `gate/ingest` depends on `oidc`
- **Strength**: Contract
- **LCA / Rank / Distance**: root / 2 / 2
- **Volatility**: High (the gate side)
- **Balanced?**: Yes
- **Shared knowledge**: `Verifier`, `Claims`, `WorkflowPath`. `ingest` passes the bearer token and the App URL as
  audience; `ErrUnavailable` is its `503`, any other `Verify` error its `401`. It then compares `Claims.Repository` with the envelope, looks the run
  up by `RunID`, checks `RunAttempt`, and derives the workflow path. `oidc` knows none of these rules.

### gate façade and app

- **Counterpart**: internal/gate/, internal/app/
- **Direction**: both depend on `oidc`: `gate` holds a `Verifier` in its `Deps`; `app` calls `New`
- **Strength**: Contract
- **LCA / Rank / Distance**: root / 2 / 2
- **Volatility**: High (`gate`), Low (`app`)
- **Balanced?**: Yes
- **Shared knowledge**: the `Verifier` type and `New`. The same `Verifier` serves `/v1/reports` and, through the
  gate's receiver, `/v1/discoveries`.

### GitHub Actions OIDC provider (external)

- **Counterpart**: `https://token.actions.githubusercontent.com` (tokens minted in customer workflows; JWKS endpoint)
- **Direction**: `oidc` depends on the provider
- **Strength**: Contract (OIDC, RFC 7515/7517/7519, documented claims)
- **LCA / Rank / Distance**: outside the tree; the highest distance the system has
- **Volatility**: Low
- **Balanced?**: Yes: contract strength tolerates the distance
- **Shared knowledge**: the issuer, JWKS URL, algorithm, and claim names and encodings listed under Encapsulated
  Knowledge. The producer requests the token with `audience` set to the App base URL and needs
  `permissions: id-token: write`.

## Change Vectors

- GitHub rotates keys faster, adds algorithms, or changes a claim's encoding.
- The App needs another claim (for example `workflow_sha` or `environment`): a field in `Claims`.
- Different cache lifetimes, leeway, or refetch bound.
- GitHub Enterprise Server issuers: an issuer parameter on the constructor.

## Constraints and Invariants

- A verified token authenticates the producer. It never proves the report is true. Nothing here or downstream may
  present the result as "verified"; feedback says "customer-attested".
- Fail closed: unknown key, unreachable JWKS with no cached set, unknown algorithm, or any missing required claim
  is an error. No check is skipped when another fails to run.
- The audience is compared exactly. A token minted for another service is never accepted.
- Only RS256. The algorithm comes from this package's rule, never from trusting the token header.
- Keys come only from the fixed issuer host over HTTPS. No `jku`, `x5u`, or embedded `jwk` header is honored.
- Tenant identity later derived from `Claims` is only as strong as these checks; `RepositoryID` and `RunID` are
  what `gate/ingest` binds to GitHub API data.
- No logging here. Errors carry check names only. Callers log IDs, outcomes and timings.
- Network calls honor `ctx` and the client timeout. A slow JWKS fetch delays, never weakens, verification.
- Standard library only: `crypto/rsa`, `crypto/sha256`, `encoding/base64`, `encoding/json`, `math/big`, `net/http`.

## Test Specification

Tokens are signed in tests with generated RSA keys; the JWKS endpoint is a fake `http.RoundTripper`; the clock
comes from `newWithClock`.

### Unit Tests

- **Test name**: `TestWorkflowPath`
  **Scenario**: table (ref, fullName): `o/r/.github/workflows/archfit.yaml@refs/pull/7/merge`, `o/r`; same with
  `O/R`; ref `refs/heads/a@b`; `o/other/.github/workflows/x.yml@main`, `o/r`; `o/r2/...`, `o/r`; no `@`;
  `o/r/@main`; `o/r/.github/workflows/x.yml@evil.yml@refs/heads/main`; empty inputs.
  **Expected behavior**: the path; the path; `""` (the path part still contains `@`); `""`; `""`; `""`; `""`; `""`; `""`.
- **Test name**: `TestTimeClaimsLeeway`
  **Scenario**: table around now: `exp` = now − 59 s, − 61 s; `nbf` = now + 59 s, + 61 s; `iat` = now + 59 s, + 61 s;
  missing `exp`; missing `iat`; missing `nbf`.
  **Expected behavior**: ok, error; ok, error; ok, error; error; error; ok.
- **Test name**: `TestAudienceForms`
  **Scenario**: table: string equal; string with trailing slash; different case; array containing it; array without
  it; empty array; missing `aud`; number.
  **Expected behavior**: ok; error; error; ok; error; error; error; error.
- **Test name**: `TestNumericClaimEncodings`
  **Scenario**: `run_id` as `"123"`, `123`, `"12a"`, `""`, `"0"`, `"-1"`, `1.5`; same for `run_attempt`, `repository_id`.
  **Expected behavior**: first two ok with value 123; error for the rest.
- **Test name**: `TestJWKSelection`
  **Scenario**: key set with an EC key, an RSA key with `use: enc`, a 1024-bit RSA key, and a valid RSA key, all
  with distinct `kid`.
  **Expected behavior**: only the token signed by the valid key verifies.

### Integration Contract Tests

- **Test name**: `TestVerifyReturnsClaims`
  **Scenario**: a token with the full GitHub claim set (plus unknown claims), correct issuer and audience.
  **Expected behavior**: every `Claims` field equals the token's claim; unknown claims cause no error.
- **Test name**: `TestKeysAreCachedForOneHour`
  **Scenario**: 100 verifications over 59 minutes, then one at 61 minutes.
  **Expected behavior**: one fetch, then a second.
- **Test name**: `TestKeyRotationRefetchesOnce`
  **Scenario**: the issuer rotates to a new `kid` inside the cache hour; a token signed with it arrives.
  **Expected behavior**: one refetch; the token verifies.
- **Test name**: `TestConcurrentVerifySingleFetch`
  **Scenario**: 50 goroutines verify on a cold cache while the fake delays 50 ms; run with `-race`.
  **Expected behavior**: one fetch; all succeed.

### Boundary Tests

- **Test name**: `TestVerifyRejects` (T03/T09 negatives)
  **Scenario**: table: bad signature; payload altered after signing; `alg: none`; `alg: HS256` signed with the
  public key as secret; `alg: RS384`; missing `kid`; unknown `kid`; wrong issuer; issuer with a trailing slash;
  wrong audience; expired; two parts; four parts; padded base64; 17 KiB token; empty token; empty audience;
  each required claim missing in turn.
  **Expected behavior**: an error naming the check for each; zero `Claims`.
- **Test name**: `TestUnknownKidFloodIsBounded`
  **Scenario**: 1 000 tokens with random `kid` values within one minute.
  **Expected behavior**: at most one refetch in that minute; all rejected; a valid token still verifies.
- **Test name**: `TestJWKSFailureModes`
  **Scenario**: table: cold cache and fetch `500`; cold cache and malformed JSON; oversized response; warm cache and
  refetch failing at 61 minutes; warm cache, fetch failing, clock at 25 hours; warm cache, unknown `kid`, refetch
  failing; warm cache, unknown `kid`, refetch succeeding without that key.
  **Expected behavior**: `ErrUnavailable` by `errors.Is` for the first three; ok from the cached set;
  `ErrUnavailable`; `ErrUnavailable`; an ordinary error that is not `ErrUnavailable`.
- **Test name**: `TestHeaderKeyHintsAreIgnored`
  **Scenario**: a token signed by an attacker key that carries `jwk`, `jku` and `x5u` headers pointing at it.
  **Expected behavior**: rejected; no request leaves the fixed issuer host.
- **Test name**: `TestErrorsCarryNoTokenMaterial`
  **Scenario**: trigger every error with marker strings in claims and in the token.
  **Expected behavior**: no marker in any error text.
- **Test name**: `TestOIDCImportsOnlyStdlib`
  **Scenario**: `go list -deps ./internal/github/oidc`.
  **Expected behavior**: no internal or third-party package.

### Behavior Tests

- **Test name**: `TestTokenForAnotherServiceIsRefused`
  **Scenario**: a genuine, correctly signed Actions token whose audience is another URL.
  **Expected behavior**: error `audience`; nothing else about the token matters.
- **Test name**: `TestRerunAttemptIsVisible`
  **Scenario**: tokens for attempt 1 and attempt 2 of the same run.
  **Expected behavior**: same `RunID`, `RunAttempt` 1 and 2; the caller can tell them apart.
- **Test name**: `TestSelfHostedRunnerIsAccepted`
  **Scenario**: `runner_environment` `self-hosted`, and a token without that claim.
  **Expected behavior**: both verify; the field is `self-hosted` and `""`.
- **Test name**: `TestOutageIsDistinguishableFromForgery`
  **Scenario**: during a JWKS outage on a cold cache: a genuine token, and a token with `alg: none`; with keys
  available: a token signed by an unknown key.
  **Expected behavior**: `ErrUnavailable`; an ordinary error (checked before key lookup); an ordinary error, not
  `ErrUnavailable`. No case returns `Claims`.
- **Test name**: `TestIssuerOutageFailsClosedThenRecovers`
  **Scenario**: cold start with the JWKS endpoint down, then up.
  **Expected behavior**: `ErrUnavailable` while down; success after recovery without a restart.

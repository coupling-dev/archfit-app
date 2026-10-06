# Google Cloud Token Source

**Path**: internal/gcptoken/
**Parent**: internal/ (Archfit App root)
**Submodules**: none (leaf)

## Purpose

`gcptoken` gives Google Cloud API clients an OAuth access token of the service account that the workload runs
as. On Cloud Run the metadata server issues it. No key file exists. Without this module, every Google API driver
(the Firestore `Store` driver, later the Cloud Tasks queue driver) would hold its own copy of the metadata protocol
and token cache.

## Functional Responsibilities

- Read an access token from the metadata server of the runtime.
- Cache the token until 1 minute **[default]** before it expires. Use a token that lives less than that once,
  without caching it.
- Run one refresh at a time. A caller that waits for a refresh leaves when its context ends.
- Send no request before the first `Token` call.

## Subdomain Classification

Generic. The metadata protocol is stable and small. Volatility: low.

## Encapsulated Knowledge

- Endpoint `http://metadata.google.internal/computeMetadata/v1/instance/service-accounts/default/token`, header
  `Metadata-Flavor: Google`.
- Response JSON: `access_token`, `expires_in` (seconds), `token_type` (`Bearer`). Any other status, a missing
  token, a non-positive lifetime, another token type, or a body over 64 KiB is an error.
- The token is held in memory only. Errors and logs never contain it.

## Public Contract

```go
// Source returns the access token of the service account the workload runs as.
// It reads no key file. It caches the token until refreshBefore ahead of its expiry.
// Safe for concurrent use; one refresh runs at a time.
type Source struct{ /* unexported */ }

// New returns a Source that asks the metadata server through hc. It sends no request until the first Token call.
func New(hc *http.Client) *Source

// Token returns a valid access token. Errors never contain the token.
func (s *Source) Token(ctx context.Context) (string, error)
```

A consumer declares its own one-method interface, `Token(ctx context.Context) (string, error)`, and takes the
`*Source` through it. It never imports this package. Only `app` does.

## Integrations

### internal/app

- **Counterpart**: `internal/app/module.md`
- **Direction**: app depends on gcptoken (constructor only)
- **Strength**: contract
- **LCA / Rank / Distance**: internal (root) / 1 / 1
- **Volatility**: low
- **Balanced?**: yes
- **Shared knowledge**: `New` and `Source`. `app` builds one `Source` with the shared `http.Client` when
  `ARCHFIT_STORE=firestore` and no emulator is set, and passes it to `store/firestore`.

### Google API drivers (method shape only)

- **Counterpart**: `internal/store/firestore/` (its `TokenSource`), and later the Cloud Tasks queue driver
- **Direction**: none in code; the drivers declare the method shape
- **Strength**: contract (one method signature)
- **Volatility**: low
- **Balanced?**: yes
- **Shared knowledge**: `Token(ctx context.Context) (string, error)` returns a bearer token for Google APIs.

### Metadata server (external)

- **Counterpart**: the Google Cloud metadata server of Cloud Run
- **Direction**: gcptoken depends on it
- **Strength**: contract (published API)
- **Distance**: highest (outside the tree)
- **Volatility**: low
- **Balanced?**: yes

## Change Vectors

- An audience-bound identity token for a service-to-service call: a second endpoint, a second cache.
- A runtime without a metadata server (local runs against real Google APIs). Not supported: local runs use the
  memory store or the emulator.

## Constraints and Invariants

- Standard library only. `TestAllowedImportMatrix` checks it.
- No key file, no environment credential, no token in an error or a log line.

## Test Specification

### Integration Contract Tests

- **Test name**: `TestTokenIsCachedUntilShortlyBeforeExpiry`
  **Scenario**: an `httptest` metadata server that answers only `GET` with `Metadata-Flavor: Google`; a 3600-second
  token; ask again 1 second before the margin, then at the margin.
  **Expected behavior**: one request before the margin, a new token after it.

### Unit Tests

- **Test name**: `TestShortLivedTokenIsNotCached`
  **Scenario**: a 30-second token, asked twice.
  **Expected behavior**: two requests.
- **Test name**: `TestFailedRefreshKeepsNoStaleToken`
  **Scenario**: an expired cached token and a failing metadata server.
  **Expected behavior**: an error, not the old token.

### Boundary Tests

- **Test name**: `TestInvalidResponsesAreErrorsWithoutTheToken`
  **Scenario**: table: 500, 404, not JSON, empty token, lifetime 0 and −5, token type `MAC`, a body over 64 KiB; a marker in the token.
  **Expected behavior**: an error each time; no error contains the marker.

### Behavior Tests

- **Test name**: `TestConcurrentCallersShareOneRefresh`
  **Scenario**: 10 callers while the metadata server holds its answer.
  **Expected behavior**: one request; every caller gets the token.
- **Test name**: `TestWaitingCallerLeavesWhenItsContextEnds`
  **Scenario**: a refresh in progress; a second caller with a cancelled context.
  **Expected behavior**: the second caller gets `context.Canceled` at once; the first gets the token.

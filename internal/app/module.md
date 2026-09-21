# App (composition root)

**Path**: internal/app/
**Parent**: internal/ (Archfit App, root)
**Submodules**: none (leaf)

## Purpose

`app` turns configuration into one running process: it loads secrets from file references, constructs every
module, chooses the platform implementations of `Store`, `Sandbox` and the LLM `Provider`, mounts the HTTP
routes, verifies and fans out webhooks, and guards the operator endpoints. Without it `cmd/reporter` would
have to know every module, and the rule "the gate keeps working when the control plane fails" would have no
single place where it is enforced at runtime.

## Functional Responsibilities

- `Load`: read `Config` from the environment; fail with a message naming the variable; never read a secret value from the environment itself, only a file path.
- `Run`: build the dependency graph, start one `http.Server`, shut down gracefully on context cancellation.
- Route table (normative, below), one `http.ServeMux`.
- Webhook endpoint: read the body (25 MB cap, GitHub's own maximum **[default]**), `webhook.Verify`, `webhook.Parse`, then call
  `gate.HandleEvent` and `control.HandleEvent` independently. Respond `202` when the signature is valid, whatever the handlers return; handler errors are logged by delivery ID. GitHub redelivery plus the sweep are the recovery path.
- Operator endpoints: constant-time bearer comparison with the operator token. `POST /internal/sweep` runs
  `control.Sweep` then `gate.Sweep`, each isolated; responds with the gate `Summary` counts as JSON.
  `POST /internal/grants` takes JSON `{"installation_id": <int>, "until": "<RFC 3339>"}` and calls `control.Grant`.
- Isolation at runtime: every `control` handler is wrapped with panic recovery and a per-request timeout; a panic
  or timeout there never reaches the gate routes. The gate upload route has its own timeout of 55 seconds **[default]** (target: feedback within 60 seconds).
- Platform selection: `StoreDriver` `"memory"` → `store.NewMemory()`; `SandboxDriver` `"none"` → nil `Sandbox`
  (managed discovery reports "not available"); `AnthropicKeyFile` empty → nil `Provider`. `control.New` and `anthropic.New` return errors; both are startup errors (a configured but invalid LLM key must not start silently). `app` passes the same `TokenSource` to `api.New` and to `control.Deps.Tokens`. Unknown driver names are a startup error. New drivers are added here and nowhere else.
- Structured logging with `log/slog`, JSON to stdout: request ID, route, status, duration, installation ID, outcome. Never bodies, paths, messages, tokens.

## Subdomain Classification

Supporting. Wiring has no business rules and changes only when a module or platform driver is added. Volatility: low.

## Encapsulated Knowledge

- Environment variable names, secret-file conventions, defaults, timeouts, body caps.
- Which concrete implementation backs each seam, and the order of construction.
- The route table and which module owns each route.
- The webhook fan-out order (gate first) and the rule that handler errors do not change the HTTP answer.
- The operator token scheme for `/internal/*`.
- Server settings: read timeout 15 s, write timeout 60 s, idle 60 s, shutdown grace 10 s **[default]**.

## Public Contract

```go
type Config struct {
    Addr             string // ARCHFIT_ADDR, default ":8080"
    BaseURL          string // ARCHFIT_BASE_URL; also the OIDC audience
    AppID            int64  // ARCHFIT_GITHUB_APP_ID
    PrivateKeyFile   string // ARCHFIT_GITHUB_PRIVATE_KEY_FILE
    WebhookSecretFile string // ARCHFIT_GITHUB_WEBHOOK_SECRET_FILE
    OAuthClientID    string // ARCHFIT_GITHUB_OAUTH_CLIENT_ID
    OAuthSecretFile  string // ARCHFIT_GITHUB_OAUTH_SECRET_FILE
    CookieKeyFile    string // ARCHFIT_COOKIE_KEY_FILE
    OperatorTokenFile string // ARCHFIT_OPERATOR_TOKEN_FILE; authenticates /internal/*
    AnthropicKeyFile string // ARCHFIT_ANTHROPIC_KEY_FILE; empty disables managed LLM
    StoreDriver      string // ARCHFIT_STORE, "memory" until docs/tech-stack.md picks a KV
    SandboxDriver    string // ARCHFIT_SANDBOX, "none" until docs/tech-stack.md picks one
}

// Load reads Config from the environment. Secrets are file references only.
func Load(getenv func(string) string) (Config, error)

// Run wires every module and serves until ctx is cancelled.
func Run(ctx context.Context, c Config, log *slog.Logger) error
```

### Routes

| Method and path         | Handler                                           | Authentication done by                      |
| ----------------------- | ------------------------------------------------- | ------------------------------------------- |
| `GET /healthz`          | `app`: `{"status":"ok"}`; no dependency is probed | none                                        |
| `POST /v1/reports`      | `gate.UploadHandler()`                            | gate (OIDC)                                 |
| `POST /v1/discoveries`  | `control.DiscoveryHandler()`                      | control, through the gate `Receiver` (OIDC) |
| `POST /webhooks/github` | `app` fan-out                                     | `app` (`webhook.Verify`)                    |
| `POST /internal/sweep`  | `app`                                             | `app` (operator bearer token)               |
| `POST /internal/grants` | `app`                                             | `app` (operator bearer token)               |
| `/` and everything else | `control.UIHandler()`                             | control (session)                           |

`cmd/reporter` builds a JSON `slog.Logger`, calls `Load(os.Getenv)`, then `Run` with a context cancelled on
SIGINT or SIGTERM, and exits non-zero on error. It contains nothing else. The skeleton's handler code and its
`TestReportIngestRefusesEveryReport` are removed in the same change that introduces verified ingestion (plan T09).

## Integrations

All integrations are constructor or handler calls: contract strength. `app` holds no domain types of its own.

### internal/gate

- **Counterpart**: internal/gate/
- **Direction**: app depends on gate
- **Strength**: contract
- **LCA / Rank / Distance**: internal (root) / 1 / 1
- **Volatility**: high on the gate side; the façade is kept small on purpose
- **Balanced?**: yes
- **Shared knowledge**: the façade, the sweep summary that `/internal/sweep` returns, and `NewReceiver`, which `app` passes to `control`.
  In these blocks `Config` means `gate.Config` (the sweep thresholds), not this module's `Config`. `app` sets
  `MissingUploadAfter` to 60 minutes and `WaiverWarnBefore` to 7 days. `Upload` is an opaque result of
  `Receiver` for this module; `app` never calls `Receive`.

<!-- contract: Gate, Deps, New — restated from internal/gate/module.md -->
```go
// Deps are the only things the gate path may touch: GitHub, OIDC, the Store (read-only) and a clock.
type Deps struct {
    GitHub   *Client
    OIDC     Verifier
    Store    Store
    Audience string // the App URL; OIDC audience
    Sweep    Config
    Now      func() time.Time
}

type Gate struct{ /* unexported */ }

func New(d Deps) *Gate

// UploadHandler serves POST /v1/reports.
func (g *Gate) UploadHandler() http.Handler

// HandleEvent reacts to a verified webhook event: pending check on a new head, fork refusal,
// re-run on a review change when the last receipt was owner_approval_missing, and a one-repository
// sweep when a default-branch push changes a protected path.
func (g *Gate) HandleEvent(ctx context.Context, e Event) error

// Sweep runs one reconciliation pass.
func (g *Gate) Sweep(ctx context.Context) (Summary, error)
```

<!-- contract: Sweeper, Summary, Config — restated from internal/gate/module.md -->
```go
type Config struct {
    MissingUploadAfter time.Duration // 60 minutes
    WaiverWarnBefore   time.Duration // 7 days
}

// Summary holds counts only; it is safe to log.
type Summary struct {
    Repos, Pulls                         int
    Pended, TimedOut, Rerun, Warned      int
    Suspended                            int
    Errors                               int  // repositories or pulls skipped after an error; details are in the log
    RateLimited                          bool // sweep stopped early; the next sweep continues
}

type Sweeper interface {
    // Run re-derives the expected check state of every open PR from GitHub and repairs differences.
    Run(ctx context.Context, now time.Time) (Summary, error)
    // RunRepo is the same pass limited to one repository (used when the protected ref changes).
    RunRepo(ctx context.Context, r Repo, now time.Time) (Summary, error)
}
```

<!-- contract: NewReceiver — restated from internal/gate/module.md -->
```go
// NewReceiver exposes the upload authentication and binding step so that the discovery
// upload endpoint reuses exactly the same provenance checks as the report endpoint.
func NewReceiver(gh *Client, v Verifier, audience string) Receiver
```

<!-- contract: Receiver, Rejection — restated from internal/gate/module.md -->
```go
// Rejection is returned when no tenant could be authenticated or the upload is stale.
// Nothing is published for a Rejection; the HTTP status is the whole answer.
type Rejection struct {
    Status int    // 400, 401, 403, 409, 413, 429, 503
    Code   string // stable machine-readable reason
}

func (r *Rejection) Error() string

type Receiver interface {
    // Receive authenticates and binds one upload of the given kind. A stale upload
    // (head is no longer the PR head, or an older attempt of the same run) is a 409 Rejection.
    Receive(ctx context.Context, req *http.Request, kind Kind) (Upload, error)
}
```

### internal/control

- **Counterpart**: internal/control/
- **Direction**: app depends on control
- **Strength**: contract
- **LCA / Rank / Distance**: internal (root) / 1 / 1
- **Volatility**: high inside control; the façade is stable
- **Balanced?**: yes
- **Shared knowledge**: the façade and the two platform seams `app` fills. In this block `Deps` means `control.Deps`.

<!-- contract: Control, Deps, New — restated from internal/control/module.md -->
```go
// Deps are wired by internal/app. Sandbox and Provider may be nil: managed features then report "not available".
type Deps struct {
    GitHub    *Client
    Tokens    TokenSource
    OAuth     OAuth
    Store     Store
    Receiver  Receiver
    Sandbox   Sandbox
    Provider  Provider
    CookieKey []byte
    BaseURL   string
    Now       func() time.Time
}

type Control struct{ /* unexported */ }

func New(d Deps) (*Control, error)

func (c *Control) UIHandler() http.Handler        // "/"
func (c *Control) DiscoveryHandler() http.Handler // POST /v1/discoveries
// HandleEvent maintains entitlements on installation events and forgets a tenant on uninstall.
func (c *Control) HandleEvent(ctx context.Context, e Event) error
// Sweep refreshes every installation's Entitlement record and forgets Store tenants
// whose installation no longer exists.
func (c *Control) Sweep(ctx context.Context) error
// Grant is the operator-only manual beta grant.
func (c *Control) Grant(ctx context.Context, installationID int64, until time.Time) error
```

<!-- contract: Sandbox, DiscoveryJob, DiscoveryResult — restated from internal/control/module.md -->
```go
// Sandbox is the only platform-dependent seam of managed discovery: one ephemeral, isolated
// container job. Implementations must destroy the container and its disk on every exit path,
// must not execute repository scripts, and must give the job no credential except Token.
type Sandbox interface {
    Discover(ctx context.Context, job DiscoveryJob) (DiscoveryResult, error)
}

type DiscoveryJob struct {
    CloneURL    string
    Token       string // short-lived installation token, contents:read
    Ref         string
    ImageDigest string // engine image from the locked manifest
    Timeout     time.Duration
}

type DiscoveryResult struct {
    DraftPolicy []byte   // engine-generated policy YAML
    Notes       []string // engine-authored, no-source
}

// ErrNoSandbox is returned by the default Sandbox until a platform implementation is configured.
var ErrNoSandbox = errors.New("onboarding: managed discovery is not available")
```

<!-- contract: Provider, Prompt, Completion — restated from internal/control/module.md -->
```go
// Provider is the LLM seam. Implementations own endpoint, model choice, and pricing.
type Provider interface {
    // MaxCostMicroUSD is the worst-case cost of one Complete call for p; used to reserve budget.
    MaxCostMicroUSD(p Prompt) int64
    Complete(ctx context.Context, p Prompt) (Completion, error)
}

type Prompt struct {
    System    string
    User      string
    MaxTokens int
}

type Completion struct {
    Text         string
    CostMicroUSD int64
}
```

### internal/control/assist/anthropic

- **Counterpart**: internal/control/assist/anthropic/
- **Direction**: app depends on it (constructor only); `control` publishes this package for that purpose
- **Strength**: contract
- **LCA / Rank / Distance**: internal (root) / 3 / 3
- **Volatility**: moderate (provider may change)
- **Balanced?**: yes — contract coupling tolerates any distance
- **Shared knowledge**: `app` reads the key file and leaves `Model` empty for the default. In this block `Config` means `anthropic.Config`.

<!-- contract: New, Config — restated from internal/control/assist/anthropic/module.md -->
```go
type Config struct {
    APIKey string // from a secret reference
    Model  string // default "claude-sonnet-5"
}

// New returns a Provider backed by the Anthropic Messages API over net/http.
// New fails on an empty key or a model without an embedded price.
func New(c Config, hc *http.Client) (Provider, error)
```

### internal/store

- **Counterpart**: internal/store/
- **Direction**: app depends on store
- **Strength**: contract
- **LCA / Rank / Distance**: internal (root) / 1 / 1
- **Volatility**: low functional, moderate implementation (KV backend unchosen)
- **Balanced?**: yes
- **Shared knowledge**: `app` constructs one `Store` and hands the same value to `gate` (which only reads) and `control`.
  `ErrNotFound` is the sentinel returned by `Get`; `app` never inspects it.

<!-- contract: Tenant, SystemTenant — restated from internal/store/module.md -->
```go
// Tenant is the authenticated GitHub installation ID that owns a key.
// Every key is stored under its tenant prefix; no operation crosses tenants.
type Tenant int64

// SystemTenant holds App-wide keys (the managed-LLM budget counter). It is never a GitHub installation ID.
const SystemTenant Tenant = 0
```

<!-- contract: Store, ErrNotFound — restated from internal/store/module.md -->
```go
// ErrNotFound is returned by Get when the key does not exist for the tenant.
var ErrNotFound = errors.New("store: key not found")

// Store is the only persistence seam of the App. Values are opaque bytes.
// Implementations must be safe for concurrent use.
type Store interface {
    Get(ctx context.Context, t Tenant, key string) ([]byte, error)
    Put(ctx context.Context, t Tenant, key string, value []byte) error
    Delete(ctx context.Context, t Tenant, key string) error // deleting a missing key is not an error
    // Add atomically adds delta to the integer at key (missing = 0) and returns the new value.
    Add(ctx context.Context, t Tenant, key string, delta int64) (int64, error)
    // DeleteTenant removes every key of the tenant. Idempotent.
    DeleteTenant(ctx context.Context, t Tenant) error
    // Tenants lists every tenant that owns at least one key, SystemTenant excluded.
    // Used only by the sweep to forget installations whose uninstall webhook was missed.
    Tenants(ctx context.Context) ([]Tenant, error)
}
```

<!-- contract: NewMemory — restated from internal/store/module.md -->
```go
// NewMemory returns an in-process Store for tests and local runs. Data is lost on exit.
func NewMemory() Store
```

### internal/github/auth, api, webhook, oidc

- **Counterpart**: internal/github/auth/, internal/github/api/, internal/github/webhook/, internal/github/oidc/
- **Direction**: app depends on them
- **Strength**: contract
- **LCA / Rank / Distance**: internal (root) / 2 / 2
- **Volatility**: low
- **Balanced?**: yes
- **Shared knowledge**: constructors, plus the webhook functions and event type used by the fan-out. `app` uses one
  shared `http.Client` with a 30-second timeout **[default]**. `Claims` is an opaque result of `Verifier` for this module.

<!-- contract: Credentials, TokenSource — restated from internal/github/auth/module.md -->
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

<!-- contract: New — restated from internal/github/auth/module.md -->
```go
func New(c Credentials, hc *http.Client) (TokenSource, OAuth, error)
```

<!-- contract: Client, New — restated from internal/github/api/module.md -->
```go
// Client implements every role interface. Consumers depend on the role interfaces they need.
type Client struct{ /* unexported */ }

func New(ts TokenSource, hc *http.Client) *Client
```

<!-- contract: Event, EventKind — restated from internal/github/webhook/module.md -->
```go
type EventKind string

const (
    PullOpened         EventKind = "pull_opened"         // opened, reopened, ready_for_review
    PullSynchronized   EventKind = "pull_synchronized"   // new head SHA
    PullClosed         EventKind = "pull_closed"
    ReviewChanged      EventKind = "review_changed"      // submitted, edited, dismissed
    DefaultBranchPush  EventKind = "default_branch_push"
    InstallationAdded  EventKind = "installation_added"  // created, or repositories added
    InstallationRemoved EventKind = "installation_removed" // deleted
    ReposRemoved       EventKind = "repos_removed"
    Ignored            EventKind = "ignored"
)

// Event is the normalized form of a verified delivery. Identity fields come from the signed payload only.
type Event struct {
    Kind           EventKind
    DeliveryID     string
    InstallationID int64
    Repo           string // "owner/name", "" for installation events
    RepoPrivate    bool
    PullRequest    int
    HeadSHA        string
    HeadRepo       string // differs from Repo for forks
    BaseRef        string
    ChangedPaths   []string // default_branch_push only
}
```

<!-- contract: Verify, Parse — restated from internal/github/webhook/module.md -->
```go
// Verify checks X-Hub-Signature-256 over body in constant time.
func Verify(secret []byte, signatureHeader string, body []byte) error

// Parse normalizes a verified delivery. Unknown event types yield Kind == Ignored, not an error.
func Parse(eventType, deliveryID string, body []byte) (Event, error)
```

<!-- contract: Claims, Verifier, ErrUnavailable — restated from internal/github/oidc/module.md -->
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

<!-- contract: New, WorkflowPath — restated from internal/github/oidc/module.md -->
```go
func New(hc *http.Client) Verifier

// WorkflowPath extracts "<path>" from a job_workflow_ref for repository fullName; "" if it points elsewhere.
func WorkflowPath(jobWorkflowRef, fullName string) string
```

## Change Vectors

- A new `Store`, `Sandbox`, or `Provider` driver: one constructor call and one driver name.
- A new route or operator endpoint.
- New configuration variables, timeouts, or log fields.
- Moving the sweep trigger from an external scheduler to an in-process ticker.

## Constraints and Invariants

- `app` is the only package that imports both `gate` and `control`, and the only one that selects platform implementations.
- Gate first: webhook events reach `gate.HandleEvent` before `control.HandleEvent`; the two results are independent. `gate.Sweep` runs even when `control.Sweep` fails or panics.
- A failing or missing optional dependency (LLM key, sandbox driver) never prevents startup of the gate routes. A missing GitHub App credential, webhook secret, base URL, or operator token is a startup error.
- Secrets arrive as file paths; their contents are never logged, never placed in `Config` string form, never echoed in errors.
- Tenant identity is never taken from a request here. `/internal/grants` is operator-authenticated and is the single place an installation ID arrives in a body; it is logged with the operator action.
- Webhook body cap 25 MB **[default]**; bodies are never logged. Unknown events answer `202` and do nothing.
- No queues, no background job tables. The only goroutines `app` starts are the HTTP server and its shutdown watcher.
- Standard library only.
- Logs: IDs, digests, outcomes, timings.

## Test Specification

### Unit Tests

- **Test name**: `TestLoadDefaultsAndRequired`
  **Scenario**: table over environments: empty; only required variables; every variable set; non-numeric app ID; unknown `ARCHFIT_STORE`; unknown `ARCHFIT_SANDBOX`.
  **Expected behavior**: defaults `:8080`, `memory`, `none`; an error naming the first missing or invalid variable; no secret content in any error.
- **Test name**: `TestLoadNeverReadsSecretValuesFromEnv`
  **Scenario**: set `ARCHFIT_GITHUB_PRIVATE_KEY` style variables holding inline PEM.
  **Expected behavior**: ignored; only `*_FILE` variables are honored.
- **Test name**: `TestOperatorTokenComparison`
  **Scenario**: missing header, wrong scheme, wrong token, token with different length, correct token.
  **Expected behavior**: `401` for all but the last; comparison is constant time (`subtle.ConstantTimeCompare`).

### Integration Contract Tests

- **Test name**: `TestRouteTable`
  **Scenario**: start `Run` with fakes behind every seam; request each row of the Routes table.
  **Expected behavior**: each path reaches its owner; unknown paths reach the UI handler; `/healthz` answers without touching any dependency.
- **Test name**: `TestWebhookFanOut`
  **Scenario**: deliver a signed `pull_request` event; then an event with a bad signature; then an unknown event type.
  **Expected behavior**: gate then control receive the same parsed `Event`; bad signature → `401`, no handler called; unknown type → `202`, handlers see `Ignored` or nothing.
- **Test name**: `TestSweepEndpointReturnsSummary`
  **Scenario**: authorized `POST /internal/sweep` with a gate fake returning counts.
  **Expected behavior**: `200` with the counts as JSON; control sweep was called first.
- **Test name**: `TestGrantEndpoint`
  **Scenario**: authorized valid body; malformed JSON; `until` in the past; user session cookie instead of the operator token.
  **Expected behavior**: `204` and `control.Grant` called; `400`; `400`; `401`.

### Boundary Tests

- **Test name**: `TestWebhookBodyCap`
  **Scenario**: a delivery one byte over the 25 MB cap.
  **Expected behavior**: `413`; nothing parsed; nothing logged except size and delivery ID.
- **Test name**: `TestLogsNeverContainSecretsOrBodies`
  **Scenario**: run every route with marker strings in bodies, tokens, and secret files; capture logs.
  **Expected behavior**: no marker appears.
- **Test name**: `TestOptionalDependenciesAreOptional`
  **Scenario**: no Anthropic key, sandbox `none`.
  **Expected behavior**: startup succeeds; `control.Deps.Provider` and `Sandbox` are nil; gate routes serve.
- **Test name**: `TestRequiredDependenciesFailStartup`
  **Scenario**: unreadable private key file; empty webhook secret file; missing base URL.
  **Expected behavior**: `Run` returns an error before listening.

### Behavior Tests

- **Test name**: `TestGateServesWhileControlFails`
  **Scenario**: control façade fake panics in `UIHandler`, returns errors from `HandleEvent` and `Sweep`.
  **Expected behavior**: `/v1/reports` and the gate half of webhook and sweep complete normally; the UI answers `500`; the process stays up.
- **Test name**: `TestGracefulShutdown`
  **Scenario**: cancel the context during an in-flight upload.
  **Expected behavior**: the request completes within the grace period; `Run` returns nil.
- **Test name**: `TestDuplicateWebhookDelivery`
  **Scenario**: deliver the same signed event twice.
  **Expected behavior**: both answer `202`; handlers are called twice and, being idempotent, leave one pending check (asserted with the gate fake's call log).

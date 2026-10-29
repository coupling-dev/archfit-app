# App (composition root)

**Path**: internal/app/
**Parent**: internal/ (Archfit App, root)
**Submodules**: none (leaf)

## Purpose

`app` turns configuration into one running process: it loads secrets from file references, constructs every
module, chooses the platform implementations of `Store`, `Sandbox`, `Queue` and the LLM `Provider`, mounts the
HTTP routes of the process role, verifies and queues webhooks, processes queued webhooks, and guards the operator
and worker endpoints. Without it `cmd/reporter` would
have to know every module, and the rule "the gate keeps working when the control plane fails" would have no
single place where it is enforced at runtime.

## Functional Responsibilities

- `Load`: read `Config` from the environment; fail with a message naming the variable; never read a secret value from the environment itself, only a file path.
- Admission: build one `gate.Admissions` from the configured accounts and the Store, and hand the same value to the
  gate, the upload receiver and `control`, with the operator IDs. The App is public on GitHub, so nothing runs for an
  account that is neither configured nor approved by an operator.
- `Run`: build the dependency graph, start one `http.Server`, shut down gracefully on context cancellation.
- Roles (`ARCHFIT_ROLE`): `all` **[default]**, `public` or `worker`. The role selects the route table (normative,
  below) and the secrets the process reads. One `http.ServeMux`.
- Webhook intake (`POST /webhooks/github`, roles `all` and `public`): read the body (25 MB cap, GitHub's own maximum
  **[default]**), read the three delivery headers under `webhook.HeaderEvent`, `webhook.HeaderDelivery` and
  `webhook.HeaderSignature`, `webhook.VerifyAny` with the accepted webhook secrets, `webhook.Parse`. Then queue two tasks, the gate half first:
  `<delivery ID>_gate` and `<delivery ID>_control`. Each task carries the three delivery headers, `Content-Type`,
  `X-Archfit-Half` (`gate` or `control`) and the unchanged body, and nothing else. A task that the queue refuses
  (any `Enqueue` error, for example a body over the task limit, a delivery ID that is no task name, or an
  unavailable queue) makes public intake respond `503`, logged at error level with outcome `enqueue_failed`.
  Both halves are attempted, but no refused task runs inline. Respond `202` only when both tasks are accepted.
  An `Ignored` event queues nothing and answers `202`. The local `all` role retains its inline driver.
  A delivery with a valid signature that `webhook.Parse` refuses answers `400` and queues nothing.
- Queued webhook processing (the inline driver, and `POST /internal/webhooks` in the `worker` role): verify the
  signature again, parse, and call the handler that `X-Archfit-Half` names, under its own deadline. Error classes:
  - Done, or an `Ignored` event: `204`.
  - Transient (every handler error that `api.Permanent` does not mark, a panic, a deadline): `503`, so the queue
    retries. Logged with `retry: true`.
  - Permanent (`api.Permanent`: a gone resource, or a final GitHub `4xx` such as `400` or `422`), a bad signature, an unparsable body, an
    unknown or missing half, or a body over the cap: `204`, logged at error level, never retried.
  - The inline driver never retries. Its errors are logged, and the intake request line has the outcome `handler_error`.
- Worker identity (`worker` role): every `/internal/*` route requires a Google ID token in
  `X-Serverless-Authorization`, else in `Authorization`, with `iss` `https://accounts.google.com` (or
  `accounts.google.com`), `aud` equal to `ARCHFIT_WORKER_URL`, `email` equal to `ARCHFIT_INVOKER`,
  `email_verified` true, and `exp` in the future. Anything else answers `401` and reaches no handler. Cloud Run's
  invoker check verifies the signature before the request reaches the container and can remove it
  (`SIGNATURE_REMOVED_BY_GOOGLE`), so `app` reads the claims only. This check refuses a caller with another
  identity; it does not replace the invoker check, internal ingress or the queue routing.
- Operator endpoints: constant-time bearer comparison with the operator token, in `Authorization` in the `all` role
  and in `X-Archfit-Operator` in the `worker` role, where `Authorization` carries the invoker's ID token.
  `POST /internal/sweep` runs `control.Sweep` then `gate.Sweep`, each isolated; responds with the gate `Summary`
  counts as JSON (rules under Operator endpoints below). On the worker the sweep needs the invoker identity only,
  because Cloud Scheduler carries no secret. `POST /internal/grants` sets or clears the manual beta grant through
  `control.Grant` (body rules under Operator endpoints below); on the worker it needs both.
- Isolation at runtime: the two `control` HTTP handlers are wrapped with panic recovery and a per-request timeout; a panic
  or timeout there never reaches the gate routes. The deadline of every route is in the Timeouts table below: 55 seconds
  **[default]** for the control routes, 300 seconds **[default]** for the managed assist route only, and the gate upload route
  has its own 55 seconds **[default]** (target: feedback within 60 seconds).
- Base URL: `ARCHFIT_BASE_URL` must not end in `/`. It is the OIDC audience, compared byte for byte with the token's
  `aud`, and the Action refuses an endpoint with a trailing slash; a slash here would refuse every upload.
- Secrets by role: a role reads only the secrets it uses, and never reads another one, even when its variable is set.

  | Role     | Reads                                                                                      | Never reads                                   |
  | -------- | ------------------------------------------------------------------------------------------ | --------------------------------------------- |
  | `all`    | every secret                                                                               | nothing                                       |
  | `public` | App key, webhook secrets, OAuth secret, cookie key, Anthropic key                          | operator token                                |
  | `worker` | App key, webhook secrets, operator token                                                   | OAuth secret, cookie key, Anthropic key       |

  The worker builds its control façade with `control.NewWorker`, which takes no UI dependency, and passes no OAuth
  fields to `auth.New`, so it holds no OAuth client. `ARCHFIT_GITHUB_OAUTH_CLIENT_ID`, `ARCHFIT_GITHUB_OAUTH_SECRET_FILE`
  and `ARCHFIT_COOKIE_KEY_FILE` are required in the `all` and `public` roles only. A missing secret of the role's own
  set is a startup error that names its variable.
- Webhook secrets: `ARCHFIT_GITHUB_WEBHOOK_SECRET_FILE` is the current secret and is required.
  `ARCHFIT_GITHUB_WEBHOOK_SECRET_PREVIOUS_FILE` is optional; it is set only during a rotation. While it is set, intake
  and the worker accept a signature made with either secret (`webhook.VerifyAny`). A configured previous file that is
  empty or unreadable is a startup error. Rotation: deploy both services with the new current secret and the old one
  as previous; change the secret in GitHub; after the queue holds no task signed with the old secret (about 2 hours),
  deploy without the previous file.
- Secret files: every `*_FILE` variable names a text file. `app` reads it, trims leading and trailing white space, and
  refuses an empty result at startup. The cookie key is the trimmed raw bytes of its file, not an encoding of them, and
  `control.New` requires exactly 32 of them.
- Platform selection: `QueueDriver` `"inline"` → the local `all` role only; the public role requires `"cloudtasks"`; `"cloudtasks"` → `cloudtasks.New` with
  `ARCHFIT_TASKS_QUEUE`, the URL `ARCHFIT_WORKER_URL` + `/internal/webhooks`, the audience `ARCHFIT_WORKER_URL` and
  the invoker `ARCHFIT_INVOKER`. The worker role never enqueues and ignores `QueueDriver`. The `all` role refuses
  `"cloudtasks"` at startup: it mounts no worker task route, so its tasks would never run. The cloudtasks driver gets
  its own `gcptoken.Source` over the shared client; startup sends no request to Google.
- Platform selection: `StoreDriver` `"memory"` → `store.NewMemory()`; `"firestore"` → `firestore.New` with
  `FirestoreProject`, `FirestoreDatabase` and `FirestoreEmulator`, and a `gcptoken.Source` over the shared client
  (nil, and one warning log line, when the emulator is set). `ARCHFIT_FIRESTORE_PROJECT` is required with
  `"firestore"`; an invalid project or database ID is a startup error. Startup sends no request to Google. `SandboxDriver` `"none"` → nil `Sandbox`
  (managed discovery reports "not available"); `AnthropicKeyFile` empty → nil `Provider`. `control.New` and `anthropic.New` return errors; both are startup errors (a configured but invalid LLM key must not start silently). `app` passes the same `TokenSource` to `api.New` and to `control.Deps.Tokens`. Unknown driver names are a startup error. New drivers are added here and nowhere else.
- Readiness (`GET /ready`, every role, no credential): the check of the release canary and of a post-deploy check.
  It is not a startup probe: Cloud Run runs that on every new instance, and a GitHub API outage would then stop new
  instances, intake included, although intake does not need GitHub. The startup probe is `GET /health`. It runs two probes at the same time, under one 5-second deadline **[default]** that includes the GitHub
  client's own retries and rate-limit waits:
  - `github`: `api.Client.CheckApp`, `GET /app` with a fresh App JWT. It proves that the App key works with GitHub
    and reads no installation.
  - `store`: `Store.Get(SystemTenant, "app/readiness")`. The key is reserved and never written, so `ErrNotFound` is
    the usual answer, and a value passes too. Any other error fails. The Firestore driver reports a
    missing database as an error, never as a missing key.

  Every role uses the App key and the store, so every role runs both probes. The queue is not probed: the public
  service account may only create tasks, and a refused task falls back to inline processing. All probes pass →
  `200` `{"status":"ok"}`, outcome `ready`. Else `503` `{"status":"unavailable","failed":["github","store"]}` with
  the failed probe names only, outcome `not_ready`; each probe error goes to an error log line. The answer has
  `Cache-Control: no-store`. One check runs at a time, and concurrent callers share its result. A pass answers for
  30 seconds and a failure for 5 seconds **[default]**, so a probe flood or a public caller makes at most one
  GitHub call in each window. A caller that hangs up does not cancel the check. The path does not end in `z`:
  Cloud Run reserves some such paths, so a check through the service URL could not reach `/readyz`.
- Structured logging with `log/slog`, JSON to stdout: request ID, route, status, duration, installation ID, outcome. Never bodies, paths, messages, tokens.
  `NewLogger` writes the field names that Cloud Logging reads: `severity` (`DEBUG`, `INFO`, `WARNING`, `ERROR`)
  instead of `level`, and `message` instead of `msg`; `time` stays. Keys inside a group keep their names. `Run` adds
  `role` and `revision` (`K_REVISION`) to every line. When `GOOGLE_CLOUD_PROJECT` is set, the request middleware
  reads the trace from `traceparent`, else from `X-Cloud-Trace-Context` (its decimal span ID becomes 16 hex digits),
  and every line logged with that request's context carries `logging.googleapis.com/trace`
  (`projects/<project>/traces/<trace ID>`), `logging.googleapis.com/spanId` and
  `logging.googleapis.com/trace_sampled`. Only hex digits of the right length pass, so a caller cannot write into a
  line. A `traceparent` needs a hex version other than `ff`, exactly four fields for version `00`, and non-zero IDs;
  a malformed one falls back to `X-Cloud-Trace-Context`. A malformed header, an all-zero ID or no project logs no
  trace. Limit: a module line logged without a
  context (`slog.Info`, not `slog.InfoContext`) carries no trace; it still carries the role, the revision and its
  own IDs. Upgrade trigger: a module line that must be found by trace.

## Subdomain Classification

Supporting. Wiring has no business rules and changes only when a module or platform driver is added. Volatility: low.

## Encapsulated Knowledge

- Environment variable names, secret-file conventions, defaults, timeouts, body caps.
- The built-in approved accounts (`alexei-led`, `vladikk`, `coupling-dev`) and operator user IDs (`1898375`, `1788671`),
  and that `ARCHFIT_APPROVED_ACCOUNTS` and `ARCHFIT_OPERATORS` replace them whole.
- Which concrete implementation backs each seam, and the order of construction.
- The route table of each role and which module owns each route.
- The task layout (names, headers, the half) and the order of the halves (gate first). Intake's answer does not
  depend on the handlers; the worker's answer depends only on the error class.
- The error classes of webhook processing.
- The operator token scheme for `/internal/*`, and the invoker identity check of the worker.
- Enqueue deadline: 4 s per task **[default]**, so both tasks fit inside GitHub's 10-second delivery timeout.
- Server settings: read timeout 15 s, write timeout 310 s, idle 60 s, shutdown grace 10 s **[default]**. The write timeout
  is above the longest route deadline (300 s), so a route's own deadline always answers before the server cuts the connection.

## Public Contract

```go
type Config struct {
    Addr             string // ARCHFIT_ADDR, default ":8080"
    BaseURL          string // ARCHFIT_BASE_URL; also the OIDC audience
    AppID            int64  // ARCHFIT_GITHUB_APP_ID
    PrivateKeyFile   string // ARCHFIT_GITHUB_PRIVATE_KEY_FILE
    WebhookSecretFile string // ARCHFIT_GITHUB_WEBHOOK_SECRET_FILE
    WebhookPreviousFile string // ARCHFIT_GITHUB_WEBHOOK_SECRET_PREVIOUS_FILE; optional, set only during a rotation
    OAuthClientID    string // ARCHFIT_GITHUB_OAUTH_CLIENT_ID; not used by the worker role
    OAuthSecretFile  string // ARCHFIT_GITHUB_OAUTH_SECRET_FILE; never read by the worker role
    CookieKeyFile    string // ARCHFIT_COOKIE_KEY_FILE; never read by the worker role
    OperatorTokenFile string // ARCHFIT_OPERATOR_TOKEN_FILE; authenticates the operator routes; never read by the public role
    AnthropicKeyFile string // ARCHFIT_ANTHROPIC_KEY_FILE; empty disables managed LLM; never read by the worker role
    StoreDriver      string // ARCHFIT_STORE, "memory" (default) or "firestore"
    FirestoreProject string // ARCHFIT_FIRESTORE_PROJECT; required by "firestore"
    FirestoreDatabase string // ARCHFIT_FIRESTORE_DATABASE; empty selects "(default)"
    FirestoreEmulator string // FIRESTORE_EMULATOR_HOST; host:port, plain HTTP and no credentials
    SandboxDriver    string // ARCHFIT_SANDBOX, "none" until docs/tech-stack.md picks one
    Role             string // ARCHFIT_ROLE: "all" (default), "public" or "worker"
    QueueDriver      string // ARCHFIT_QUEUE: "inline" (default) or "cloudtasks"
    TasksQueue       string // ARCHFIT_TASKS_QUEUE: "projects/<p>/locations/<l>/queues/<q>"; cloudtasks only
    WorkerURL        string // ARCHFIT_WORKER_URL: the worker service URL; the dispatch OIDC audience
    Invoker          string // ARCHFIT_INVOKER: the service account e-mail of the queue and Scheduler dispatches
    TraceProject     string // GOOGLE_CLOUD_PROJECT; optional; names the trace of a log line; empty logs no trace
    Revision         string // K_REVISION; set by Cloud Run; logged on every line
    ApprovedAccounts []string // ARCHFIT_APPROVED_ACCOUNTS: comma-separated logins served without an operator's approval; default alexei-led, vladikk, coupling-dev
    Operators        []int64  // ARCHFIT_OPERATORS: comma-separated GitHub user IDs that may approve installations; default 1898375, 1788671
}

// Load reads Config from the environment. Secrets are file references only.
func Load(getenv func(string) string) (Config, error)

// Run wires every module and serves until ctx is cancelled.
func Run(ctx context.Context, c Config, log *slog.Logger) error

// NewLogger returns the process logger: one JSON line per event on w, with the field names Cloud Logging reads.
// A line logged with a request context also carries that request's trace.
func NewLogger(w io.Writer) *slog.Logger
```

### Routes

| Method and path                         | Roles           | Handler                                           | Authentication done by                             |
| --------------------------------------- | --------------- | ------------------------------------------------- | -------------------------------------------------- |
| `GET /health`                           | every role      | `app`: `{"status":"ok"}`; no dependency is probed | none                                               |
| `GET /ready`                            | every role      | `app` readiness: GitHub App key and store probed  | none                                               |
| `POST /v1/reports`                      | `all`, `public` | `gate.UploadHandler()`                            | gate (OIDC)                                        |
| `POST /v1/discoveries`                  | `all`, `public` | `control.DiscoveryHandler()`                      | control, through the gate `Receiver` (OIDC)        |
| `POST /v1/baselines`                    | `all`, `public` | `control.BaselineHandler()`                       | control, through the gate `Receiver` (OIDC)        |
| `POST /v1/reanchors`                    | `all`, `public` | `control.ReanchorHandler()`                       | control, through the gate `Receiver` (OIDC)        |
| `POST /webhooks/github`                 | `all`, `public` | `app` intake                                      | `app` (`webhook.VerifyAny`)                        |
| `POST /internal/webhooks`               | `worker`        | `app` queued webhook processing                   | `app` (invoker ID token, then `webhook.VerifyAny`) |
| `POST /internal/sweep`                  | `all`           | `app`                                             | `app` (operator bearer token)                      |
| `POST /internal/sweep`                  | `worker`        | `app`                                             | `app` (invoker ID token)                           |
| `POST /internal/grants`                 | `all`           | `app`                                             | `app` (operator bearer token)                      |
| `POST /internal/grants`                 | `worker`        | `app`                                             | `app` (invoker ID token and operator token)        |
| any other `/internal/*`                 | every role      | `404`                                             | none                                               |
| `POST /api/repos/{owner}/{name}/assist` | `all`, `public` | `control.UIHandler()`, under the long deadline    | control (session)                                  |
| `/` and everything else                 | `all`, `public` | `control.UIHandler()`                             | control (session)                                  |

The `worker` role answers `404` for every route it does not mount, the UI included.

### Timeouts

The deadline is the one the handler sees on its request context. `http.TimeoutHandler` answers `503` when it passes.

| Route                                   | Deadline | Why                                                                                                          |
| --------------------------------------- | -------- | ------------------------------------------------------------------------------------------------------------ |
| `GET /health`                           | none     | answers from memory                                                                                          |
| `POST /v1/reports`                      | 55 s     | gate upload; target: feedback within 60 seconds                                                              |
| `POST /v1/discoveries`                  | 55 s     | control per-request timeout                                                                                  |
| `POST /v1/baselines`                    | 55 s     | control per-request timeout                                                                                  |
| `POST /v1/reanchors`                    | 55 s     | control per-request timeout                                                                                  |
| `POST /webhooks/github`                 | none     | each enqueue runs detached from the request for 4 s; inline work runs detached: gate 55 s, then control 55 s |
| `POST /internal/webhooks`               | none     | the half runs under its own deadline (gate 55 s, control 55 s), attached to the dispatch request             |
| `POST /internal/sweep`                  | none     | operator call; no deadline of its own                                                                        |
| `POST /internal/grants`                 | none     | operator call; no deadline of its own                                                                        |
| `POST /api/repos/{owner}/{name}/assist` | 300 s    | managed assist; the sum below                                                                                |
| `/` and everything else                 | 55 s     | control per-request timeout                                                                                  |

`control` measured the managed assist route end to end at about 285 seconds worst case: ui save 60 s, assist pre-checks
30 s, provider 150 s, settle 5 s (detached), propose 40 s. A shorter route deadline cancels the provider call and the
full budget reservation is settled for nothing, so this route, and only this route, gets 300 seconds.

The shared outbound `http.Client` timeout of 30 seconds does not bound the provider call: `anthropic.New` copies
the client and clears `Timeout` on its copy, so the 150 second deadline of `control/assist` is the one that applies.

### Operator endpoints

- `POST /internal/grants` takes JSON `{"installation_id": <int>, "until": "<RFC 3339>"}`; unknown fields and bodies over
  4 KB are refused. A future `until` sets the grant. `"until": null` or an omitted `until` clears the grant: `app` calls
  `control.Grant` with the zero `time.Time`. An `until` that is present and not after now answers `400` and calls
  nothing: a past time is far more likely a typo than a wish to clear, and clearing has its own spelling.
  Success answers `204`, logged with the installation ID and the outcome `granted` or `grant_cleared`; an error from
  `control.Grant` answers `500`.
- `POST /internal/sweep` answers `200` with the gate `Summary` when `gate.Sweep` succeeds, also when `control.Sweep`
  failed or panicked (that failure is only logged). When `gate.Sweep` fails it answers `500` with the partial `Summary`.
  The `Summary` is encoded by `encoding/json` with the Go field names (`Repos`, `Pulls`, `Pended`, `TimedOut`, `Rerun`,
  `Warned`, `Suspended`, `NotAdmitted`, `Errors`, `RateLimited`); there are no JSON tags. A pass that a GitHub rate limit stopped
  still answers `200`, because a retry before the limit resets cannot help and the next hourly pass continues the
  work; its request line has the outcome `sweep_rate_limited` instead of `swept`, so a log-based counter sees it.

`cmd/reporter` builds the logger with `NewLogger(os.Stdout)`, calls `Load(os.Getenv)`, then `Run` with a context cancelled on
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
- **Shared knowledge**: the façade, the sweep summary that `/internal/sweep` returns, and `NewReceiver`, which `app` passes to `control`; `app` also passes the `*Gate` itself to `control.Deps.Approvals`, the seam of the pull request page's Approve button, and to `control.Deps.DecisionQueue`, the read-only list of the decision requests that wait for an owner. `app` builds one `Admissions` with `NewAdmissions(Store,
  ApprovedAccounts)` and passes that same value to `Deps.Admissions`, `NewReceiver` and `control.Deps.Admissions`, so the
  three agree on who is served. The worker role builds it too, for the gate.
  In these blocks `Config` means `gate.Config` (the sweep thresholds), not this module's `Config`. `app` sets
  `MissingUploadAfter` to 60 minutes and `WaiverWarnBefore` to 7 days. `Upload` is an opaque result of
  `Receiver` for this module; `app` never calls `Receive`.

<!-- contract: Gate, Deps, New — restated from internal/gate/module.md -->
```go
// Deps are the only things the gate path may touch: GitHub, OIDC, the Store and a clock. On the Store the gate
// path reads the entitlement and the admission, and reads and writes the pusher and approval records of gate/inputs;
// its only other write is gate/feedback's publication leases.
type Deps struct {
    GitHub     *Client
    OIDC       Verifier
    Store      Store
    Admissions *Admissions // which installations the App serves; nothing runs for any other
    Audience   string      // the App URL; OIDC audience
    Sweep      Config
    Now        func() time.Time
}

type Gate struct{ /* unexported */ }

func New(d Deps) *Gate

// UploadHandler serves POST /v1/reports.
func (g *Gate) UploadHandler() http.Handler

// HandleEvent reacts to a verified webhook event: pending check on a new head, fork refusal,
// owner_approval_missing on a review change that withdraws the approval a receipt relied on, re-run on a
// review change when the last receipt was owner_approval_missing, and a one-repository sweep when a
// default-branch push changes a protected path. An event for a repository whose owner is not admitted gets
// the reaction "not_admitted" and nothing else: no read, no write. An approval that cannot be read is returned as an
// error, so the delivery is retried.
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
    NotAdmitted                          int  // installations skipped: no operator approved them
    Errors                               int  // repositories or pulls skipped after an error; details are in the log
    RateLimited                          bool // the pass stopped early on a GitHub rate limit
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
func NewReceiver(gh *Client, v Verifier, audience string, adm *Admissions) Receiver
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
  `app` also passes `Admissions` (the value the gate uses), `Approvals` and `DecisionQueue` (the `*Gate`) and `Operators`
  (`Config.Operators`); `New` refuses a nil `Admissions` or `Approvals`. `NewWorker` takes `Admissions` too (required), for installation events and the sweep, and no `Operators`.

<!-- contract: Control, Deps, New, WorkerDeps, NewWorker — restated from internal/control/module.md -->
```go
// Deps are wired by internal/app. Sandbox and Provider may be nil: managed features then report "not available".
type Deps struct {
    GitHub    *Client
    Tokens    TokenSource
    OAuth     OAuth
    Store     Store
    Receiver  Receiver
    // Admissions says which installations the App serves; Operators (GitHub user IDs) approve the others on /admin.
    Admissions *Admissions
    // Approvals reads and records owner approvals of pull request heads for the pull request page: the gate façade.
    Approvals Approvals
    Operators []int64
    Sandbox    Sandbox
    Provider   Provider
    CookieKey  []byte
    BaseURL    string
    Now        func() time.Time
}

// WorkerDeps are the dependencies of the worker façade: it handles events, the sweep and grants, and serves no UI.
// So it takes no OAuth, no cookie key, no Receiver, no Sandbox and no Provider.
type WorkerDeps struct {
    GitHub     *Client
    Store      Store
    Admissions *Admissions // the sweep and installation events do nothing for an account no operator approved
    Now        func() time.Time
}

type Control struct{ /* unexported */ }

func New(d Deps) (*Control, error)

// NewWorker builds the façade of the worker role. Only HandleEvent, Sweep and Grant work on it; the handler
// methods return nil, and the worker mounts no route that needs them.
func NewWorker(d WorkerDeps) (*Control, error)

func (c *Control) UIHandler() http.Handler        // "/"
func (c *Control) DiscoveryHandler() http.Handler // POST /v1/discoveries
func (c *Control) BaselineHandler() http.Handler  // POST /v1/baselines
func (c *Control) ReanchorHandler() http.Handler  // POST /v1/reanchors
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

### internal/queue, internal/queue/cloudtasks

- **Counterpart**: internal/queue/, internal/queue/cloudtasks/
- **Direction**: app depends on them (`cloudtasks`: constructor only)
- **Strength**: contract
- **LCA / Rank / Distance**: internal (root) / 1 / 1 for `queue`, 2 / 2 for `cloudtasks`
- **Volatility**: low
- **Balanced?**: yes
- **Shared knowledge**: the seam, the inline driver, and the Cloud Tasks constructor. `app` builds every task and is
  the handler of the inline driver. `app` passes the shared `http.Client` to `cloudtasks.New`.

<!-- contract: Task, Queue, ErrInvalidTask, ValidName, Handler, NewInline — restated from internal/queue/module.md -->
```go
// Task is one unit of queued work: the target's own headers and body, and the name that deduplicates it.
// The queue never reads the body; the receiver verifies it again.
type Task struct {
    Name   string // see ValidName; unique per unit of work
    Header http.Header
    Body   []byte
}

// Queue hands a Task to the worker. Implementations must be safe for concurrent use.
type Queue interface {
    // Enqueue returns nil once the task will be delivered at least once. A name that was enqueued before is
    // not an error: the task is then delivered at least once in all. An error means the task was not
    // accepted, and the caller must process it some other way.
    Enqueue(ctx context.Context, t Task) error
}

// ErrInvalidTask is returned for a task whose name or size a driver cannot carry. Nothing was sent.
var ErrInvalidTask = errors.New("queue: invalid task")

// ValidName reports whether name is a task name that every driver accepts: 1 to 500 of [A-Za-z0-9_-].
func ValidName(name string) bool

// Handler processes one delivered task.
type Handler func(ctx context.Context, t Task)

// NewInline returns a Queue that runs every task at once, in the caller's goroutine, through h.
// It never fails and never retries: h reports its own errors. It ignores the name, so it accepts any task.
func NewInline(h Handler) Queue
```

<!-- contract: Config, TokenSource, New — restated from internal/queue/cloudtasks/module.md -->
```go
// Config names the queue and the dispatch target. Every value is a resource name, not a secret.
type Config struct {
    Queue    string // "projects/<project>/locations/<location>/queues/<queue>"
    URL      string // the worker route of every task; the queue-level uriOverride replaces it when set
    Audience string // OIDC audience of the dispatch token: the worker service URL
    Invoker  string // service account e-mail that Cloud Tasks signs the dispatch token as
    Endpoint string // API base URL; empty means https://cloudtasks.googleapis.com
}

// TokenSource returns an OAuth 2.0 access token for the Cloud Tasks API.
type TokenSource interface {
    Token(ctx context.Context) (string, error)
}

// New returns a Queue that creates one Cloud Tasks HTTP task per Enqueue. It makes no network call.
// A nil client means http.DefaultClient.
func New(c Config, ts TokenSource, hc *http.Client) (queue.Queue, error)
```

### internal/control/assist/anthropic

- **Counterpart**: internal/control/assist/anthropic/
- **Direction**: app depends on it (constructor only); `control` publishes this package for that purpose
- **Strength**: contract
- **LCA / Rank / Distance**: internal (root) / 3 / 3
- **Volatility**: moderate (provider may change)
- **Balanced?**: yes — contract coupling tolerates any distance
- **Shared knowledge**: `app` reads the key file and leaves `Model` empty for the default. In this block `Config` means `anthropic.Config`.
  `app` passes the shared `http.Client`; `anthropic.New` clears `Timeout` on its own copy, so the 30-second value never cuts a provider call.

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
- **Volatility**: low functional, moderate implementation
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
    // CompareAndSwap atomically sets key to value when it currently holds exactly old, and reports whether it did.
    // A nil old matches only a missing key; a nil value deletes the key. A false result changes nothing.
    CompareAndSwap(ctx context.Context, t Tenant, key string, old, value []byte) (bool, error)
    // CompareAndSwapAll applies every swap in one atomic step when every key currently holds exactly its Old,
    // and reports whether it did. Each swap follows the rules of CompareAndSwap. A false result changes nothing.
    CompareAndSwapAll(ctx context.Context, t Tenant, swaps []Swap) (bool, error)
    // DeleteTenant removes every key of the tenant. Idempotent.
    DeleteTenant(ctx context.Context, t Tenant) error
    // Tenants lists every tenant that owns at least one key, SystemTenant excluded.
    // Used only by the sweep to forget installations whose uninstall webhook was missed.
    Tenants(ctx context.Context) ([]Tenant, error)
}

// Swap is one key of CompareAndSwapAll. A nil Old matches only a missing key; a nil Value deletes the key.
// A swap whose Value equals its Old only checks the key.
type Swap struct {
    Key        string
    Old, Value []byte
}
```

<!-- contract: NewMemory — restated from internal/store/module.md -->
```go
// NewMemory returns an in-process Store for tests and local runs. Data is lost on exit.
func NewMemory() Store
```

### internal/store/firestore

- **Counterpart**: internal/store/firestore/
- **Direction**: app depends on it (constructor only)
- **Strength**: contract
- **LCA / Rank / Distance**: internal (root) / 2 / 2
- **Volatility**: low
- **Balanced?**: yes
- **Shared knowledge**: `app` calls `New` when `ARCHFIT_STORE=firestore` and passes the shared `http.Client`.
  The 30-second client timeout bounds every Firestore request.

<!-- contract: New, Config, TokenSource, DefaultDatabase — restated from internal/store/firestore/module.md -->
```go
// DefaultDatabase is the database ID used when Config.DatabaseID is empty.
const DefaultDatabase = "(default)"

// TokenSource returns an OAuth access token for the Firestore API.
type TokenSource interface {
    Token(ctx context.Context) (string, error)
}

// Config selects the database.
type Config struct {
    ProjectID  string // Google Cloud project ID
    DatabaseID string // empty selects DefaultDatabase
    // EmulatorHost is host:port of a Firestore emulator. When set, the driver uses plain HTTP and no credentials.
    EmulatorHost string
}

// New returns a Store over the database in c. It validates c and contacts no server.
// tokens may be nil only in emulator mode.
func New(c Config, tokens TokenSource, hc *http.Client) (store.Store, error)
```

### internal/gcptoken

- **Counterpart**: internal/gcptoken/
- **Direction**: app depends on it (constructor only)
- **Strength**: contract
- **LCA / Rank / Distance**: internal (root) / 1 / 1
- **Volatility**: low
- **Balanced?**: yes
- **Shared knowledge**: `app` builds a `Source` for each Google API driver: the Firestore `TokenSource`, and in
  `Run` the `cloudtasks.TokenSource`.

<!-- contract: Source, New — restated from internal/gcptoken/module.md -->
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

### internal/github/auth, api, webhook, oidc

- **Counterpart**: internal/github/auth/, internal/github/api/, internal/github/webhook/, internal/github/oidc/
- **Direction**: app depends on them
- **Strength**: contract
- **LCA / Rank / Distance**: internal (root) / 2 / 2
- **Volatility**: low
- **Balanced?**: yes
- **Shared knowledge**: constructors, plus the webhook functions, header names and event type used by intake and
  queued processing, and `api.Permanent`, which decides the error class of a handler error. `app` uses one
  shared `http.Client` with a 30-second timeout **[default]**; it bounds the GitHub and OIDC calls. `Claims` is an opaque result of `Verifier` for this module.

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
// New returns the App's token source and, when both OAuth fields are set, the login flow. A process without a UI
// (the worker role) passes neither and gets a nil OAuth, so it never holds the client secret.
func New(c Credentials, hc *http.Client) (TokenSource, OAuth, error)
```

<!-- contract: Permanent — restated from internal/github/api/module.md -->
```go
// Permanent reports whether err is a GitHub answer that the same call cannot change when it is repeated:
// ErrNotFound, or a 4xx status other than 401, 403, 408, 409 and 429. A 403 stays retryable because a secondary
// rate limit can arrive without the headers that mark it. Rate limits, 5xx, transport and every other error are
// not permanent.
func Permanent(err error) bool
```

<!-- contract: Client, New, CheckApp — restated from internal/github/api/module.md -->
```go
// Client implements every role interface. Consumers depend on the role interfaces they need.
type Client struct{ /* unexported */ }

func New(ts TokenSource, hc *http.Client) *Client

// CheckApp proves that the App key works with GitHub: GET /app with a fresh App JWT, on every call. It reads no
// installation. Bound it with ctx: on a rate limit or a 5xx the client waits and retries within its own budget.
func (c *Client) CheckApp(ctx context.Context) error
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
    Account        string // installation kinds only: the account login from the payload; "" when it has none
    RepoPrivate    bool
    PullRequest    int
    HeadSHA        string
    HeadRepo       string // differs from Repo for forks
    BaseRef        string
    ChangedPaths   []string // default_branch_push only
}
```

<!-- contract: VerifyAny, Parse — restated from internal/github/webhook/module.md -->
```go
// VerifyAny accepts a body signed with any of secrets: the current secret and, during a rotation, the previous one.
// It checks every secret, so the time taken does not show which one matched. An empty list or an empty secret
// fails closed.
func VerifyAny(secrets [][]byte, signatureHeader string, body []byte) error

// Parse normalizes a verified delivery. Unknown event types yield Kind == Ignored, not an error.
func Parse(eventType, deliveryID string, body []byte) (Event, error)
```

<!-- contract: HeaderEvent, HeaderDelivery, HeaderSignature — restated from internal/github/webhook/module.md -->
```go
// Header names of a GitHub webhook delivery. Callers read these headers and pass their values to VerifyAny and Parse.
const (
    HeaderEvent     = "X-GitHub-Event"
    HeaderDelivery  = "X-GitHub-Delivery"
    HeaderSignature = "X-Hub-Signature-256"
)
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

- A new `Store`, `Sandbox`, `Queue`, or `Provider` driver: one constructor call and one driver name.
- A new route or operator endpoint.
- New configuration variables, timeouts, or log fields.
- Another source for the approved accounts or operators (a Store list, a secret): only `Load` changes. The lists are
  in `Config`, so a change today is a pull request and a release.
- Moving the sweep trigger from an external scheduler to an in-process ticker.

## Constraints and Invariants

- `app` is the only package that imports both `gate` and `control`, and the only one that selects platform implementations.
- Gate first: the gate task is queued before the control task, and inline work runs the gate half first. The two
  halves are separate tasks with independent results: a control failure never blocks or repeats gate work.
  `gate.Sweep` runs even when `control.Sweep` fails or panics.
- The public role never serves `/internal/*` and never reads the operator token. The worker role never serves the UI,
  the uploads or intake, and never reads the OAuth secret, the cookie key or the Anthropic key.
- A task is GitHub's own delivery. The worker verifies the signature again, so the queue is not trusted. The half
  header is not signed; it only chooses between two handlers of the same verified delivery.
- A failing or missing optional dependency (LLM key, sandbox driver) never prevents startup of the gate routes. A missing GitHub App credential, webhook secret, or base URL is a startup error, and so is a missing operator token outside the public role, a missing OAuth client ID, OAuth secret or cookie key outside the worker role, a configured previous webhook secret file that is empty or unreadable, and a missing worker URL or invoker in the worker role or with the cloudtasks driver. An empty `ARCHFIT_OPERATORS`
  uses the default; a list with an element that is not a positive integer is a startup error naming the variable.
- Secrets arrive as file paths; their contents are never logged, never placed in `Config` string form, never echoed in errors.
- The approved accounts are logins and compare case-insensitively; the operators are user IDs, never logins, because a
  released login can be taken by someone else. The lists are configuration, not secrets.
- An owner approval of a pull request head is reachable only behind `control/ui`'s browser session: no `/v1/` route
  (OIDC uploads) and no CLI reaches `gate.Approvals`. `app` hands the `*Gate` to `control.Deps.Approvals` and to nothing
  else; `TestOwnerApprovalIsReachableOnlyFromTheBrowserSession` pins it.
- Declared restatement: that test names `control/ui`'s route `POST /api/repos/{o}/{n}/pulls/{number}/approve` and
  `gate/inputs`' record key prefix `approval/` to prove where they are mounted and written. Owner of each: those two
  modules. On drift the test fails; it never changes a route.
- Tenant identity is never taken from a request here. `/internal/grants` is operator-authenticated and is the single place an installation ID arrives in a body; it is logged with the operator action.
- Webhook body cap 25 MB **[default]**; bodies are never logged. Unknown events answer `202` and do nothing.
- The server write timeout is greater than every route deadline. Raising a route deadline means raising the write timeout with it.
- Declared restatement: the mux pattern `POST /api/repos/{owner}/{name}/assist` repeats one row of the route table of
  `internal/control/ui` (there `POST /api/repos/{o}/{n}/assist`), because `control.UIHandler()` is one opaque handler and only
  this route needs the long deadline. Owner of the rule: `control/ui`. If ui renames or moves the route, requests still
  reach ui through `/`, but under 55 seconds: the provider call is cancelled and the full reservation is settled for
  nothing. Remove the restatement when `control` publishes the assist route as its own handler or publishes the pattern.
- Known ceiling: the shutdown grace of 10 seconds is shorter than the assist deadline, so a managed assist request in
  flight at shutdown is cancelled. Upgrade trigger: managed assist traffic that makes deploy-time cancellations visible.
- No background job tables and no queue state: the queue product holds queued tasks. The only goroutines `app`
  starts are the HTTP server and its shutdown watcher.
- Standard library only.
- Logs: IDs, digests, outcomes, timings.

## Test Specification

### Unit Tests

- **Test name**: `TestLoadDefaultsAndRequired`
  **Scenario**: table over environments: empty; only required variables; every variable set; non-numeric app ID; unknown `ARCHFIT_STORE`; unknown `ARCHFIT_SANDBOX`;
  `ARCHFIT_BASE_URL` ending in `/` (host only, with a path, `/` alone); a base URL with a path and no trailing slash;
  unknown `ARCHFIT_ROLE`; unknown `ARCHFIT_QUEUE`; the public role without the operator token; the worker role with
  and without the operator token, the worker URL and the invoker; the worker role with `ARCHFIT_QUEUE=cloudtasks`;
  `cloudtasks` in the `all` role; `cloudtasks` without the queue name, the worker URL or the invoker; an invoker that
  is not an e-mail; a worker URL with a trailing slash or over plain `http`.
  `ARCHFIT_STORE=firestore` without a project, with a project and a named database, and with `FIRESTORE_EMULATOR_HOST`.
  `ARCHFIT_APPROVED_ACCOUNTS=acme,octo-labs`; `ARCHFIT_OPERATORS=7, 8`, `7,<a login>` and `0`.
  **Expected behavior**: defaults `:8080`, `memory`, `none`, `all`, `inline`, the three approved accounts and the two operators; an error naming the first missing or
  invalid variable; the public role loads without the operator token, the worker role does not; the worker role loads
  without the OAuth client ID, the OAuth secret and the cookie key; the previous webhook secret file is loaded when set;
  the worker ignores the queue driver; each list variable replaces its default whole; an operator list with a non-ID or `0` is an error naming `ARCHFIT_OPERATORS`; no secret content in any error.
- **Test name**: `TestLoadNeverReadsSecretValuesFromEnv`
  **Scenario**: set `ARCHFIT_GITHUB_PRIVATE_KEY` style variables holding inline PEM.
  **Expected behavior**: ignored; only `*_FILE` variables are honored.
- **Test name**: `TestOperatorTokenComparison`
  **Scenario**: missing header, wrong scheme, wrong token, token with different length, correct token.
  **Expected behavior**: `401` for all but the last; comparison is constant time (`subtle.ConstantTimeCompare`).

### Integration Contract Tests

- **Test name**: `TestRouteTable`
  **Scenario**: start `Run` with fakes behind every seam; request each row of the Routes table.
  **Expected behavior**: each path reaches its owner; unknown paths reach the UI handler; `/health` answers without touching any dependency.
- **Test name**: `TestWebhookFanOut`
  **Scenario**: the inline driver: deliver a signed `pull_request` event; then the same event signed with the previous secret; then an event with a bad signature; then an unknown event type.
  **Expected behavior**: gate then control receive the same parsed `Event`; the previous secret → `202` and both halves; bad signature → `401`, no handler called; unknown type → `202`, handlers see `Ignored` or nothing.
- **Test name**: `TestRoleRouteTables`
  **Scenario**: table over the three roles and twelve requests: health, the three uploads, intake, two UI paths, sweep
  and grant with the operator token, a task and a sweep with the invoker ID token, an unknown `/internal/*` path.
  **Expected behavior**: each request reaches the owner that the Routes table names for that role, or answers the
  status of an unmounted route (`404`) or a refused credential (`401`) and reaches no handler. The public role has no
  `/internal/*` route; the worker has no upload, intake or UI route.
- **Test name**: `TestReadyAnswersFromTheProbes`
  **Scenario**: table over the two probes: both pass; the GitHub probe fails; the store probe fails; both fail. Each
  failure quotes a marker.
  **Expected behavior**: `200` `{"status":"ok"}` with outcome `ready`; otherwise `503` with the failed names in probe
  order and outcome `not_ready`; `Cache-Control: no-store`; the marker is never in the body; both probes run once.
- **Test name**: `TestReadyRemembersAResultBriefly`
  **Scenario**: a passing and a failing probe, checked again just before and at the end of 30 and 5 seconds.
  **Expected behavior**: one probe call inside the window, a fresh check at its end.
- **Test name**: `TestReadyIsBounded`
  **Scenario**: a probe that waits for its context, with a short timeout; ten concurrent callers of a slow probe; a
  caller whose context is already cancelled.
  **Expected behavior**: the hanging probe fails at the timeout; the ten callers cause one probe call; the probe sees
  a live context.
- **Test name**: `TestReadyNeedsNoCredentialOnAnyRole`
  **Scenario**: `GET /ready` with no header on each role.
  **Expected behavior**: `200` on every role.
- **Test name**: `TestStoreProbe`
  **Scenario**: the reserved key missing in the memory store; a store whose `Get` fails; the Firestore driver against
  an emulator address that answers `404 NOT_FOUND` (no database); a value under the reserved key.
  **Expected behavior**: ready for the first and the last; not ready for the failing store and the missing database.
- **Test name**: `TestReadyIsWiredToGitHubAndTheStore`
  **Scenario**: `run` with the memory store and a transport that answers `404` to every request; then `GET /ready`.
  **Expected behavior**: `503`, and exactly one outbound request (`GET /app`).
- **Test name**: `TestWorkerRequiresTheInvokerIdentity`
  **Scenario**: for each worker route, a table of credentials: none; the operator token; an ID token of another
  service account, another audience, another issuer, with `email_verified` false, expired, with a list audience; no
  bearer scheme; two segments; a payload that is not base64 or not JSON; the invoker token in `Authorization`; in
  `X-Serverless-Authorization`; with the issuer without scheme. Then a worker without a configured invoker.
  **Expected behavior**: `401` and no handler for every refused credential and for the worker without an invoker; the
  three invoker tokens reach the handler.
- **Test name**: `TestWorkerGrantNeedsTheOperatorToken`
  **Scenario**: the invoker ID token with the operator token in `X-Archfit-Operator`; without it; with a wrong one.
  **Expected behavior**: `204` and one grant; `401` and no grant twice.
- **Test name**: `TestWorkerTaskErrorClasses`
  **Scenario**: table over task dispatches: gate done; control done; a delivery signed with the previous secret; a Store error; `ErrRateLimited`; a deadline; a
  control panic; a wrapped `ErrNotFound` from each half; a bad signature; an unparsable body; an unknown half; no half;
  an ignored event type. Then a gate dispatch whose request context is cancelled.
  **Expected behavior**: `204` with outcome `done`; `503` with `transient_error` for the four retryable cases; `204`
  with `permanent_error`, `bad_signature`, `unparsable`, `unknown_half` or `ignored` otherwise; only the named half
  runs, and none for a refused task; no header value or secret in the logs. The half sees a 55-second deadline, and a
  cancelled dispatch answers `503`: the worker does not detach from the queue's request.
- **Test name**: `TestIntakeQueuesOneTaskPerHalf`
  **Scenario**: the public role over a recording queue: both halves accepted; the control task refused with a `503`
  error; both refused with `ErrInvalidTask`; a delivery ID that is no task name; an ignored event. Then the headers
  and body of the tasks for a delivery with extra request headers.
  **Expected behavior**: `202` when both tasks are accepted or the event is ignored; otherwise `503` with
  `enqueue_failed`. Tasks are named `<delivery>_gate` then `<delivery>_control`; public intake never runs handlers inline,
  including when no queue is configured. Both halves are attempted; an ignored event queues nothing. Every enqueue has a
  deadline; a task holds exactly the three delivery headers, `Content-Type` and `X-Archfit-Half`, and the unchanged body.
- **Test name**: `TestQueuedTaskReachesTheWorker`
  **Scenario**: a public intake and a worker joined by a queue that dispatches each task to the worker route with the
  invoker token.
  **Expected behavior**: intake runs no handler; the worker runs the gate half, then the control half, with the
  delivery ID and installation ID of the delivery.
- **Test name**: `TestSweepEndpointReturnsSummary`
  **Scenario**: authorized `POST /internal/sweep` with a gate fake returning counts.
  **Expected behavior**: `200` with the counts as JSON; control sweep was called first. A `RateLimited` summary still answers `200`, and its request line has the outcome `sweep_rate_limited`; otherwise the outcome is `swept`.
- **Test name**: `TestGrantEndpoint`
  **Scenario**: authorized body with a future `until`; `"until": null`; `until` omitted; `until` in the past or equal to now; malformed JSON,
  unknown field, missing or negative installation ID, oversized body; user session cookie instead of the operator token.
  **Expected behavior**: `204` and `control.Grant` called with that time; `204` and `control.Grant` called with the zero time, twice;
  `400` and no call; `400` and no call; `401`.
- **Test name**: `TestRouteDeadlines`
  **Scenario**: table over the route classes with fake handlers that record `ctx.Deadline()` and return at once: `POST /v1/reports`,
  `POST /v1/discoveries`, `POST /v1/baselines`, `GET /`, `POST /api/repos/o/n/policy`, `GET /api/repos/o/n/assist`, `POST /api/repos/o/n/assist`, `POST /api/repos/o/n/assist/x`, the old `POST /r/o/n/assist`; the two webhook halves;
  the operator routes. No sleeping.
  **Expected behavior**: 55 s for every row except `POST /api/repos/o/n/assist`, which sees 300 s; the inline webhook halves each see 55 s although the
  request has no deadline; the operator routes see no deadline; the server write timeout is greater than every one of them.

- **Test name**: `TestImportsStayInsideTheDesign`
  **Scenario**: list the package's imports with `go list`.
  **Expected behavior**: every import outside the standard library is a module listed under Integrations; anything else fails the test.
- **Test name**: `TestOwnerApprovalIsReachableOnlyFromTheBrowserSession`
  **Scenario**: parse every non-test Go file under `cmd/` and `internal/`; read the route tables of `control/ui` and of
  this package; `go list -deps` of `gate/ingest` and `github/oidc`; `POST` the approve path, a `/v1/reports/approve`
  path and `/v1/reports` with an Actions OIDC bearer token on every role.
  **Expected behavior**: `Approve` is used only in `gate.(*Gate).Approve` and `ui.(*server).apiApprovePull`; the
  handler only in `control/ui`'s route table, at the approve route, as `s.apiRepo(permWrite, s.apiApprovePull)`
  (session, CSRF token, write access); the record key only in `gate/inputs`; the four `/v1/` routes are served by no
  UI handler; the upload authentication links neither `gate/inputs` nor `store`; the approve paths reach only
  `control.UI` (nothing on the worker) and `/v1/reports` only `gate.Upload`.

### Boundary Tests

- **Test name**: `TestWebhookBodyCap`
  **Scenario**: a delivery one byte over the 25 MB cap.
  **Expected behavior**: `413`; nothing parsed; nothing logged except size and delivery ID.
- **Test name**: `TestLogsNeverContainSecretsOrBodies`
  **Scenario**: run every route with marker strings in bodies, tokens, and secret files; capture logs.
  **Expected behavior**: no marker appears.
- **Test name**: `TestLogLinesUseCloudLoggingFieldNames`
  **Scenario**: `run` with `NewLogger`, a trace project and a revision; `GET /health` with a sampled `traceparent`.
  **Expected behavior**: no line has `level` or `msg`; every line has `role` and `revision`; the request line has
  exactly `time`, `severity`, `message`, `role`, `revision`, the three `logging.googleapis.com/` trace fields,
  `request_id`, `method`, `route`, `status` and `duration_ms`, with severity `INFO` and the trace of the header.
- **Test name**: `TestSeverityNames`
  **Scenario**: each slog level and levels between them; a warning and an error through `NewLogger`, the error with a
  group that has its own `level` and `msg` keys.
  **Expected behavior**: `DEBUG`, `INFO`, `WARNING`, `ERROR`; the group keys keep their names.
- **Test name**: `TestRequestTrace`
  **Scenario**: table: `traceparent` sampled, not sampled, in upper case; `X-Cloud-Trace-Context` with a decimal span,
  with and without options; both headers; a malformed `traceparent` next to a valid `X-Cloud-Trace-Context`; no
  project; no header; a quote in the trace ID; a span that is not a number; the all-zero trace ID; a later version
  with an extra field; version `ff` and a non-hex version next to a valid `X-Cloud-Trace-Context`; version `00` with
  an extra field; the all-zero span ID, in both headers.
  **Expected behavior**: the trace name, a 16-digit lower-case span and the sampled flag for the valid rows, with
  `traceparent` first; no trace for the rest.
- **Test name**: `TestModuleLinesCarryTheRequestTrace`
  **Scenario**: one line with a request context that holds a trace, one without a context.
  **Expected behavior**: the first carries the trace fields and its attributes; the second has no trace field.
- **Test name**: `TestOptionalDependenciesAreOptional`
  **Scenario**: no Anthropic key, sandbox `none`; then `ARCHFIT_STORE=firestore` with a project.
  **Expected behavior**: startup succeeds; `control.Deps.Provider` and `Sandbox` are nil; gate routes serve. With
  Firestore, startup sends no outbound request. With a previous webhook secret file, intake answers `202` for a
  delivery signed with the current or the previous secret and `401` for one signed with neither.
- **Test name**: `TestEachRoleStartsWithOnlyItsSecrets`
  **Scenario**: for each role, `run` with only the secret settings of that role (`all`: every one; `public`: App key,
  webhook secret, OAuth client ID and secret, cookie key; `worker`: App key, webhook secret, operator token). Then
  each foreign secret file set to a path that does not exist. Then each own setting removed, and each own file set to
  a path that does not exist. Then the public role answers the operator and task routes.
  **Expected behavior**: each role starts, `/health` answers and no request leaves the process; a foreign path does
  not stop the startup, because it is never read; every missing or unreadable own secret fails before listening and
  names its variable; the public role answers `404` on every `/internal/*` route.
- **Test name**: `TestCloudTasksQueueWiring`
  **Scenario**: `run` in the public role with `ARCHFIT_QUEUE=cloudtasks`, a fake Google token source and a transport that
  answers `tasks.create`; one signed delivery. Then no token source; then an invalid queue name.
  **Expected behavior**: `202` and two task creations in the configured queue, named after the delivery and aimed at
  the worker route; the two other cases fail at startup and name `ARCHFIT_QUEUE` and `ARCHFIT_TASKS_QUEUE`.
- **Test name**: `TestRequiredDependenciesFailStartup`
  **Scenario**: unreadable private key file; empty webhook secret file; empty and unreadable previous webhook secret
  file; missing base URL; Firestore without a project, with an invalid project ID, with an invalid database ID.
  **Expected behavior**: `Run` returns an error before listening.

### Behavior Tests

- **Test name**: `TestUnapprovedAccountIsInertUntilAnOperatorApproves`
  **Scenario**: the real gate, control and ui over one memory Store, with only `acme` approved by configuration; an `opened` pull request delivery, an upload, a sweep, the owner's overview API and a non-operator's approve for the installation; then an operator approves it and the upload and the overview are tried again.
  **Expected behavior**: before approval the upload answers 403 `installation_not_approved`, the sweep reports `NotAdmitted == 1`, no check and no comment exist, the overview is 403 with `waiting` set to the account, and the non-operator's approve is 404; after the operator's approve the upload is accepted with `success` and the overview is 200.
- **Test name**: `TestGateServesWhileControlFails`
  **Scenario**: control façade fake panics in `UIHandler`, returns errors from `HandleEvent` and `Sweep`.
  **Expected behavior**: `/v1/reports` and the gate half of webhook and sweep complete normally; the UI answers `500`; the process stays up.
- **Test name**: `TestGracefulShutdown`
  **Scenario**: cancel the context during an in-flight upload.
  **Expected behavior**: the request completes within the grace period; `Run` returns nil.
- **Test name**: `TestDuplicateWebhookDelivery`
  **Scenario**: deliver the same signed event twice.
  **Expected behavior**: both answer `202`; handlers are called twice and, being idempotent, leave one pending check (asserted with the gate fake's call log).

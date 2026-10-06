# Cloud Tasks driver

**Path**: internal/queue/cloudtasks/
**Parent**: internal/queue/ (Queue seam)
**Submodules**: none (leaf)

## Purpose

`cloudtasks` implements `queue.Queue` on Google Cloud Tasks: one `tasks.create` call with an HTTP target per
`Enqueue`. It speaks the REST API over `net/http`, in line with the no-SDK rule of
[tech stack](../../../docs/tech-stack.md). Without it, intake could only process webhooks inline, and a failed
webhook would wait for the hourly sweep.

## Functional Responsibilities

- Validate the configuration in `New`, with no network call.
- Refuse an invalid task name or a body over the task limit with `queue.ErrInvalidTask`, before any request.
- Get an access token from the `TokenSource` for each call.
- Create the task: `POST {endpoint}/v2/{queue}/tasks` with the task name `{queue}/tasks/{name}` and an
  `httpRequest` that holds the worker URL, `POST`, the task headers, the body, and an `oidcToken` with the invoker
  service account and the audience.
- Map the answer: `2xx` is success; `409` with the status `ALREADY_EXISTS` is success, because a repeated name is
  the deduplication; every other answer is an error.

Not here: the queue configuration (rate, retries, `uriOverride`), which Terraform owns; the token source, which
the composition root wires.

## Subdomain Classification

Generic. A thin REST client for one documented call. Volatility: low.

## Encapsulated Knowledge

- Endpoint `https://cloudtasks.googleapis.com` **[default]**; `Config.Endpoint` replaces it in tests.
- Queue name format `projects/<project>/locations/<location>/queues/<queue>`, checked with a pattern.
- Body limit: 900 KiB **[default]**. The Cloud Tasks quotas page sets 1 MiB per task; the rest is room for the
  headers and the URL. The `tasks.create` reference names 100 KB. If that limit applies, the API refuses the task,
  `Enqueue` returns an error, and intake processes the webhook inline: a wrong limit costs only the retries.
  The JSON field `body` is base64, the API's bytes encoding.
- `409` also carries `ABORTED`. Only the error status `ALREADY_EXISTS` means a duplicate. The driver reads the
  error status word only, never the message, so no text from the API reaches an error.
- The task URL is the worker route. The queue-level `uriOverride` (enforced) replaces it, so a task creator cannot
  aim a task at another route. The OIDC token is minted by Cloud Tasks at dispatch, not by the App.
- Errors name the status and the API status word. They never hold the token, the body, or the API message.
  A transport error drops the URL.

## Public Contract

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

## Integrations

### internal/queue

- **Counterpart**: internal/queue/
- **Direction**: `cloudtasks` depends on `queue`
- **Strength**: Contract
- **LCA / Rank / Distance**: queue / 1 / 1
- **Volatility**: Low
- **Balanced?**: Yes
- **Shared knowledge**: the seam.

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

### internal/app

- **Counterpart**: internal/app/
- **Direction**: `app` depends on `cloudtasks` (constructor only)
- **Strength**: Contract
- **LCA / Rank / Distance**: internal (root) / 2 / 2
- **Volatility**: Low
- **Balanced?**: Yes
- **Shared knowledge**: `Config`, `TokenSource`, `New`. `app` sets `URL` to `ARCHFIT_WORKER_URL` plus
  `/internal/webhooks` and `Audience` to `ARCHFIT_WORKER_URL`.

### Cloud Tasks REST API (external)

- **Counterpart**: `cloudtasks.googleapis.com`, v2
- **Direction**: `cloudtasks` calls it
- **Strength**: Contract (a documented, versioned API)
- **Volatility**: Low
- **Shared knowledge**: `tasks.create`, the task name rule, the `ALREADY_EXISTS` status, and the task size limit.

## Change Vectors

- A schedule time or a dispatch deadline per task: one field in the request.
- A second queue (operator grants): a second `New` with another `Config`.

## Constraints and Invariants

- Standard library only, and `queue`.
- A repeated name is never an error. Otherwise intake would process a GitHub redelivery inline and the
  deduplication would not work.
- No request for a task that the driver refuses.
- No secret in an error or a log line.

## Test Specification

GitHub and Google are not reached. The API is an `httptest` server that refuses a repeated name with
`ALREADY_EXISTS` and passes each new task to the handler of the conformance suite.

### Integration Contract Tests

- **Test name**: `TestCloudTasksConformance`
  **Scenario**: `queuetest.Run` against the fake API.
  **Expected behavior**: as in `internal/queue`.
- **Test name**: `TestCreateRequestShape`
  **Scenario**: enqueue one task with two headers.
  **Expected behavior**: the path, the bearer token, the full task name, the URL, `POST`, the body, the OIDC
  e-mail and audience, and the headers in canonical form.
- **Test name**: `TestDuplicateNameIsDeduplicated`
  **Scenario**: enqueue one name three times.
  **Expected behavior**: three nil results; one delivery.

### Unit Tests

None beyond the cases below: the driver has no logic apart from the request and the answer mapping.

### Boundary Tests

- **Test name**: `TestEnqueueFailures`
  **Scenario**: table: `409 ABORTED`; `409` without a body; `503`; `429`; `403` with a message; `400
  FAILED_PRECONDITION`; a failing token source; an invalid name; an empty name; a body 1 byte over the limit;
  then a body at the limit; then an API that refuses connections.
  **Expected behavior**: an error for each failure, `ErrInvalidTask` only for the name and the size, no request for
  the token, name and size cases, and no token or API message in any error; the body at the limit is sent; the
  refused connection is an error that is not `ErrInvalidTask`.
- **Test name**: `TestNewValidatesConfig`
  **Scenario**: table: a queue without a location, with a trailing slash, empty; a plain `http` URL; a relative URL;
  an empty audience; an invoker without a domain; a nil token source; then the valid config.
  **Expected behavior**: an error for each bad config; nil for the valid one.

### Behavior Tests

- **Test name**: `TestCloudTasksQueueWiring` in `internal/app`
  **Scenario**: the public role with `ARCHFIT_QUEUE=cloudtasks` and a fake token source receives one signed delivery.
  **Expected behavior**: two `tasks.create` calls, named `<delivery>_gate` and `<delivery>_control`, both aimed at
  the worker route.

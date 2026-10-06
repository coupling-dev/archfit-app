# Queue

**Path**: internal/queue/ (includes the transparent folder `queuetest/`, the exported conformance suite)
**Parent**: internal/ (Archfit App root)
**Submodules**: cloudtasks (publishes its children)

## Purpose

`queue` is the hand-off seam between webhook intake and webhook processing. Intake must answer GitHub within
10 seconds, and GitHub does not deliver again on its own. A queue takes the work out of the request and repeats
it when it fails. Without this seam, `app` would bind to one queue product, and local runs and tests would need
that product to process a webhook.

## Functional Responsibilities

- Define `Task`, `Queue`, `Handler`, `ErrInvalidTask` and `ValidName`.
- Carry the target's own headers and body unchanged. A task is not an App-defined message: the receiver
  verifies the body again, so the queue is not trusted.
- Deduplicate by name: a name that was enqueued before is not an error.
- Provide `NewInline`, the driver that runs a task at once in the caller. It is the default
  (`ARCHFIT_QUEUE=inline`), the local driver, the test driver, and the fallback of intake when a real queue
  refuses a task.
- Export the conformance suite `queuetest.Run` **[default]**, so every driver passes the same cases.

Not provided, on purpose: delays, priorities, task deletion, listing, acknowledgement. The receiver's HTTP answer
is the acknowledgement of a real queue.

## Subdomain Classification

Generic. Durable hand-off with retries is a solved problem; the module fixes the smallest interface intake needs.
Functional volatility is low. Implementation volatility is moderate: the product is Cloud Tasks today.

## Encapsulated Knowledge

- Task name rule: 1 to 500 characters of `[A-Za-z0-9_-]`, the Cloud Tasks task ID rule, so a name valid here is
  valid for every driver.
- Delivery semantics: at least once. A driver can deliver a task twice, late, or out of order. Receivers must be
  idempotent.
- The inline driver ignores the name, never fails, and never retries: its handler reports its own errors.

## Public Contract

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

```go
// package queuetest

// New builds the queue under test. Every task the queue delivers must reach h.
type New func(t *testing.T, h queue.Handler) queue.Queue

// Run runs every conformance case against a fresh queue per case.
func Run(t *testing.T, newQueue New)
```

## Integrations

### internal/app

- **Counterpart**: internal/app/
- **Direction**: `app` depends on `queue`
- **Strength**: Contract
- **LCA / Rank / Distance**: internal (root) / 1 / 1
- **Volatility**: Low
- **Balanced?**: Yes
- **Shared knowledge**: the contract above. `app` builds the tasks, chooses the driver, and is the inline handler.

### internal/queue/cloudtasks

- **Counterpart**: internal/queue/cloudtasks/
- **Direction**: `cloudtasks` depends on `queue` (seam implementation); `queue` never imports it
- **Strength**: Contract
- **LCA / Rank / Distance**: queue / 1 / 1
- **Volatility**: Moderate (Cloud Tasks API)
- **Balanced?**: Yes
- **Shared knowledge**: `Task`, `Queue`, `ErrInvalidTask`, `ValidName`, and `queuetest.Run`.

## Internal Design

```
queue/          Task, Queue, ErrInvalidTask, ValidName, NewInline (the inline driver)
├── queuetest/  conformance suite (transparent folder)
└── cloudtasks/ Cloud Tasks driver over REST
```

`queue` publishes `cloudtasks` for its constructor only, as `store` publishes its drivers: `app` calls
`cloudtasks.New`, and nothing else imports a driver. The inline driver lives in `queue` itself, because intake
always needs it as the fallback.

## Change Vectors

- A new driver: a package under `queue/` that passes `queuetest.Run`, and one driver name in `app`.
- A delayed task (for example a retry after a known time): one optional field on `Task`.

## Constraints and Invariants

- Standard library only. `queue` imports no other internal package; `queuetest` imports only `queue`.
- A task holds no secret of the App. It carries the delivery signature, which proves the body, not the sender.
- The inline driver accepts every task, so intake can always fall back to it.

## Test Specification

### Integration Contract Tests

- **Test name**: `TestInlineConformance`, and `TestCloudTasksConformance` in `cloudtasks`
  **Scenario**: `queuetest.Run`: a task with binary body bytes and three headers; one name enqueued twice;
  16 concurrent enqueues with distinct names.
  **Expected behavior**: the body and the headers arrive intact; both enqueues of the repeated name return nil and
  the task arrives once or twice; every concurrent task arrives and no enqueue fails.

### Unit Tests

- **Test name**: `TestValidName`
  **Scenario**: table: a GUID with a suffix; one character; 500 and 501 characters; empty; a slash, a space, a dot,
  non-ASCII letters.
  **Expected behavior**: true for the first three; false for the rest.
- **Test name**: `TestInlineAcceptsAnyName`
  **Scenario**: empty name, a name with a slash, a name of 501 characters.
  **Expected behavior**: each enqueue returns nil and the handler runs each time.

### Boundary Tests

- **Test name**: `TestIntakeQueuesOneTaskPerHalf` in `internal/app`
  **Scenario**: a queue that refuses a task, with a transport error or with `ErrInvalidTask`.
  **Expected behavior**: intake processes that task through the inline driver.

### Behavior Tests

- **Test name**: `TestQueuedTaskReachesTheWorker` in `internal/app`
  **Scenario**: intake and worker joined by a queue that dispatches each task to the worker route.
  **Expected behavior**: intake runs no handler; the worker runs the gate half, then the control half.

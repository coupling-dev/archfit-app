// Package queue is the hand-off seam between webhook intake and webhook processing.
package queue

import (
	"context"
	"errors"
	"net/http"
	"regexp"
)

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

var namePattern = regexp.MustCompile(`^[A-Za-z0-9_-]{1,500}$`)

// ValidName reports whether name is a task name that every driver accepts: 1 to 500 of [A-Za-z0-9_-].
func ValidName(name string) bool { return namePattern.MatchString(name) }

// Handler processes one delivered task.
type Handler func(ctx context.Context, t Task)

// NewInline returns a Queue that runs every task at once, in the caller's goroutine, through h.
// It never fails and never retries: h reports its own errors. It ignores the name, so it accepts any task.
func NewInline(h Handler) Queue { return inline{h} }

type inline struct{ h Handler }

func (q inline) Enqueue(ctx context.Context, t Task) error {
	q.h(ctx, t)
	return nil
}

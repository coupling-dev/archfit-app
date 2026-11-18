package ui

import (
	"sync"
	"testing"
	"time"
)

// meet returns the start hooks of two reads that must run at the same time: each returns once the other has started,
// and reports a failure when it has not after a second of virtual time, which is what a read that waits for the other
// looks like.
func meet(t *testing.T) (first, second func()) {
	t.Helper()
	started := [2]chan struct{}{make(chan struct{}), make(chan struct{})}
	side := func(i int) func() {
		var once sync.Once
		return func() {
			once.Do(func() { close(started[i]) })
			select {
			case <-started[1-i]:
			case <-time.After(time.Second):
				t.Error("the two reads did not run at the same time")
			}
		}
	}
	return side(0), side(1)
}

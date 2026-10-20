package api

import (
	"context"
	"sync/atomic"
	"time"
)

// CallStats counts the GitHub round trips made under one context and the time they took, summed: calls that ran
// concurrently add up past the wall-clock time. Safe for concurrent use.
type CallStats struct {
	calls atomic.Int64
	nanos atomic.Int64
}

type statsKey struct{}

// WithCallStats returns a context whose GitHub round trips the returned CallStats counts. Token minting is not one.
func WithCallStats(ctx context.Context) (context.Context, *CallStats) {
	s := &CallStats{}
	return context.WithValue(ctx, statsKey{}, s), s
}

func (s *CallStats) Calls() int64 { return s.calls.Load() }

func (s *CallStats) Duration() time.Duration { return time.Duration(s.nanos.Load()) }

// count records one round trip that started at start, when ctx carries a CallStats.
func count(ctx context.Context, start time.Time) {
	if s, ok := ctx.Value(statsKey{}).(*CallStats); ok {
		s.calls.Add(1)
		s.nanos.Add(int64(time.Since(start)))
	}
}

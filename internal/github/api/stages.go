package api

import (
	"context"
	"sync"
	"time"
)

type stagesKey struct{}

// StageStats aggregates read-side timings for one request, including overlapping stages.
type StageStats struct {
	mu     sync.Mutex
	stages map[string]Stage
}

// Stage contains only counts and elapsed time, never upstream data or errors.
type Stage struct {
	DurationMS float64 `json:"duration_ms"`
	Count      int     `json:"count"`
	CacheHits  int     `json:"cache_hits"`
}

// WithStageStats enables optional request stage measurements.
func WithStageStats(ctx context.Context) (context.Context, *StageStats) {
	s := &StageStats{stages: make(map[string]Stage)}
	return context.WithValue(ctx, stagesKey{}, s), s
}

// StartStage returns a completion function; without instrumentation it is a no-op.
func StartStage(ctx context.Context, name string) func(count, cacheHits int) {
	s, ok := ctx.Value(stagesKey{}).(*StageStats)
	if !ok {
		return func(int, int) {}
	}
	start := time.Now()
	return func(count, hits int) {
		s.mu.Lock()
		defer s.mu.Unlock()
		stage := s.stages[name]
		stage.DurationMS += float64(time.Since(start)) / float64(time.Millisecond)
		stage.Count += count
		stage.CacheHits += hits
		s.stages[name] = stage
	}
}

// Snapshot returns a copy safe to log while stages finish.
func (s *StageStats) Snapshot() map[string]Stage {
	s.mu.Lock()
	defer s.mu.Unlock()
	out := make(map[string]Stage, len(s.stages))
	for k, v := range s.stages {
		out[k] = v
	}
	return out
}

package anthropic

import (
	"context"
	"net/http"
	"strconv"
	"time"
)

const (
	attemptTimeout    = 60 * time.Second
	maxRetries        = 1
	defaultRetryDelay = 2 * time.Second
	maxRetryDelay     = 10 * time.Second

	statusOverloaded = 529
)

func retryable(status int) bool {
	return status == http.StatusTooManyRequests || status == statusOverloaded
}

// retryDelay reads a retry-after header given in seconds.
func retryDelay(header string) time.Duration {
	seconds, err := strconv.Atoi(header)
	if err != nil || seconds < 0 {
		return defaultRetryDelay
	}
	return time.Duration(min(seconds, int(maxRetryDelay/time.Second))) * time.Second
}

// sleepContext waits for d or until ctx ends, whichever is first.
func sleepContext(ctx context.Context, d time.Duration) error {
	timer := time.NewTimer(d)
	defer timer.Stop()
	select {
	case <-ctx.Done():
		return ctx.Err()
	case <-timer.C:
		return nil
	}
}

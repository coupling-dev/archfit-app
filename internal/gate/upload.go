package gate

import (
	"time"
)

const (
	uploadDeadline    = 50 * time.Second // inside the 60 second feedback target
	retryAfterSeconds = 60

	codeRateLimited = "rate_limited"
	codeUnavailable = "unavailable"

	// the GitHub event of a run on a pushed branch
)

type refused struct {
	Error string `json:"error"`
}

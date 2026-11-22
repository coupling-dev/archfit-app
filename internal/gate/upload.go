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

const BetaAccessMessage = "Archfit is in a closed beta. Ask for access: https://github.com/coupling-dev/archfit/issues/new?template=beta-access.yml"

type refused struct {
	Error   string `json:"error"`
	Message string `json:"message,omitempty"`
}

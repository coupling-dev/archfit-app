// Package oidc verifies GitHub Actions OIDC tokens.
package oidc

import (
	"context"
	"errors"
	"net/http"
)

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

func New(hc *http.Client) Verifier {
	return nil
}

// WorkflowPath extracts "<path>" from a job_workflow_ref for repository fullName; "" if it points elsewhere.
func WorkflowPath(jobWorkflowRef, fullName string) string {
	return ""
}

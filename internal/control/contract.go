// Package control is the façade of the control plane: UI, onboarding, entitlements and assist.
// It re-publishes the platform seams that platform packages and app may use.
package control

import (
	"context"
	"errors"
	"net/http"
	"time"

	"github.com/alexei-led/archfit-app/internal/control/assist"
	"github.com/alexei-led/archfit-app/internal/control/onboarding"
	"github.com/alexei-led/archfit-app/internal/gate"
	"github.com/alexei-led/archfit-app/internal/github/api"
	"github.com/alexei-led/archfit-app/internal/github/auth"
	"github.com/alexei-led/archfit-app/internal/github/webhook"
	"github.com/alexei-led/archfit-app/internal/store"
)

var errNotImplemented = errors.New("control: not implemented")

// Deps are wired by internal/app. Sandbox and Provider may be nil: managed features then report "not available".
type Deps struct {
	GitHub    *api.Client
	Tokens    auth.TokenSource
	OAuth     auth.OAuth
	Store     store.Store
	Receiver  gate.Receiver
	Sandbox   Sandbox
	Provider  Provider
	CookieKey []byte
	BaseURL   string
	Now       func() time.Time
}

type Control struct{}

func New(d Deps) (*Control, error) {
	return nil, errNotImplemented
}

func (c *Control) UIHandler() http.Handler { // "/"
	return nil
}

func (c *Control) DiscoveryHandler() http.Handler { // POST /v1/discoveries
	return nil
}

// HandleEvent maintains entitlements on installation events and forgets a tenant on uninstall.
func (c *Control) HandleEvent(ctx context.Context, e webhook.Event) error {
	return errNotImplemented
}

// Sweep refreshes every installation's Entitlement record and forgets Store tenants
// whose installation no longer exists.
func (c *Control) Sweep(ctx context.Context) error {
	return errNotImplemented
}

// Grant is the operator-only manual beta grant.
func (c *Control) Grant(ctx context.Context, installationID int64, until time.Time) error {
	return errNotImplemented
}

// Re-published platform seams. The owning child's module.md is normative.

// Sandbox, DiscoveryJob and DiscoveryResult are owned by control/onboarding.
type (
	Sandbox         = onboarding.Sandbox
	DiscoveryJob    = onboarding.DiscoveryJob
	DiscoveryResult = onboarding.DiscoveryResult
)

// ErrNoSandbox is owned by control/onboarding.
var ErrNoSandbox = onboarding.ErrNoSandbox

// Provider, Prompt and Completion are owned by control/assist.
type (
	Provider   = assist.Provider
	Prompt     = assist.Prompt
	Completion = assist.Completion
)

// Package control is the façade of the control plane: UI, onboarding, entitlements and assist.
// It re-publishes the platform seams that platform packages and app may use.
package control

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"time"

	"github.com/alexei-led/archfit-app/internal/control/account"
	"github.com/alexei-led/archfit-app/internal/control/assist"
	"github.com/alexei-led/archfit-app/internal/control/onboarding"
	"github.com/alexei-led/archfit-app/internal/control/proposal"
	"github.com/alexei-led/archfit-app/internal/control/ui"
	"github.com/alexei-led/archfit-app/internal/gate"
	"github.com/alexei-led/archfit-app/internal/github/api"
	"github.com/alexei-led/archfit-app/internal/github/auth"
	"github.com/alexei-led/archfit-app/internal/store"
)

const cookieKeyLen = 32

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

// tenantLister is the only Store method the façade calls itself.
type tenantLister interface {
	Tenants(ctx context.Context) ([]store.Tenant, error)
}

type Control struct {
	account   account.Account
	repos     api.Repos
	tenants   tenantLister
	ui        http.Handler
	discovery http.Handler
	baseline  http.Handler
	now       func() time.Time

	// Held for the composition only; the façade never calls them.
	consents   account.Consents
	budget     account.Budget
	onboarding onboarding.Onboarding
	assist     assist.Assist
}

func New(d Deps) (*Control, error) {
	if err := d.validate(); err != nil {
		return nil, err
	}
	if d.Now == nil {
		d.Now = time.Now
	}
	acct, consents, budget := account.New(d.Store)
	proposals := proposal.New(d.GitHub, d.GitHub)
	onb := onboarding.New(onboarding.Deps{
		GitHub: d.GitHub, Tokens: d.Tokens, Receiver: d.Receiver, Proposals: proposals,
		Consents: consents, Sandbox: d.Sandbox, BaseURL: d.BaseURL, Now: d.Now,
	})
	asst := assist.New(assist.Deps{
		GitHub: d.GitHub, Provider: d.Provider, Consents: consents, Budget: budget,
		Proposals: proposals, Now: d.Now,
	})
	page, err := ui.New(ui.Deps{
		OAuth: d.OAuth, CookieKey: d.CookieKey, BaseURL: d.BaseURL, GitHub: d.GitHub,
		Account: acct, Consents: consents, Onboarding: onb, Assist: asst, Proposals: proposals, Now: d.Now,
	})
	if err != nil {
		return nil, fmt.Errorf("control: ui: %w", err)
	}
	c := newControl(acct, d.GitHub, d.Store, page, onb.DiscoveryHandler(), d.Now)
	c.baseline = onb.BaselineHandler()
	c.consents, c.budget, c.onboarding, c.assist = consents, budget, onb, asst
	return c, nil
}

func (d Deps) validate() error {
	switch {
	case d.GitHub == nil:
		return errors.New("control: nil GitHub client")
	case d.OAuth == nil:
		return errors.New("control: nil OAuth")
	case d.Store == nil:
		return errors.New("control: nil Store")
	case d.Receiver == nil:
		return errors.New("control: nil Receiver")
	case len(d.CookieKey) != cookieKeyLen:
		return fmt.Errorf("control: CookieKey must be %d bytes", cookieKeyLen)
	case d.BaseURL == "":
		return errors.New("control: empty BaseURL")
	case d.Sandbox != nil && d.Tokens == nil:
		return errors.New("control: Sandbox needs Tokens")
	}
	return nil
}

// newControl assembles the façade from already-built parts; tests pass fakes.
func newControl(a account.Account, repos api.Repos, tenants tenantLister, page, discovery http.Handler, now func() time.Time) *Control {
	return &Control{account: a, repos: repos, tenants: tenants, ui: recovered(page), discovery: discovery, now: now}
}

func (c *Control) UIHandler() http.Handler { // "/"
	return c.ui
}

func (c *Control) DiscoveryHandler() http.Handler { // POST /v1/discoveries
	return c.discovery
}

func (c *Control) BaselineHandler() http.Handler { // POST /v1/baselines
	return c.baseline
}

// recovered contains a UI panic: 500 for this request, the process and every other handler keep working.
func recovered(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		defer func() {
			v := recover()
			if v == nil {
				return
			}
			if v == http.ErrAbortHandler { //nolint:errorlint // the sentinel is panicked by identity
				panic(v)
			}
			// The pattern only: a path may carry repository names.
			slog.Error("control: ui panic", "pattern", r.Pattern)
			w.WriteHeader(http.StatusInternalServerError)
		}()
		next.ServeHTTP(w, r)
	})
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

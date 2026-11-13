// Package control is the façade of the control plane: UI, onboarding and entitlements.
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
	"github.com/alexei-led/archfit-app/internal/control/answers"
	"github.com/alexei-led/archfit-app/internal/control/onboarding"
	"github.com/alexei-led/archfit-app/internal/control/passkey"
	"github.com/alexei-led/archfit-app/internal/control/ui"
	"github.com/alexei-led/archfit-app/internal/gate"
	"github.com/alexei-led/archfit-app/internal/github/api"
	"github.com/alexei-led/archfit-app/internal/github/auth"
	"github.com/alexei-led/archfit-app/internal/store"
)

const cookieKeyLen = 32

// Deps are wired by internal/app.
type Deps struct {
	GitHub *api.Client
	OAuth  auth.OAuth
	Store  store.Store
	// Admissions says which installations the App serves; Operators (GitHub user IDs) approve the others on /admin.
	Admissions *gate.Admissions
	// RequestViews reads a request and the preview of each answer for the request page: the gate façade. It reads only.
	RequestViews gate.RequestViews
	PolicyViews  gate.PolicyViews
	// Answers records an owner's answer and carries it to the default branch: the gate façade. control/answers adds
	// the passkey.
	OwnerActions gate.OwnerActions
	SetupViews   gate.SetupViews
	Answers      gate.Answers
	Operators    []int64
	CookieKey    []byte
	BaseURL      string
	Now          func() time.Time
}

// tenantLister is the only Store method the façade calls itself.
type tenantLister interface {
	Tenants(ctx context.Context) ([]store.Tenant, error)
}

type Control struct {
	account account.Account
	repos   api.Repos
	tenants tenantLister
	admit   admitter
	ui      http.Handler
	now     func() time.Time

	// Held for the composition only; the façade never calls them.
	consents account.Consents
}

func New(d Deps) (*Control, error) {
	if err := d.validate(); err != nil {
		return nil, err
	}
	if d.Now == nil {
		d.Now = time.Now
	}
	acct, consents := account.New(d.Store)
	passkeys, err := passkey.New(passkey.Config{BaseURL: d.BaseURL}, d.Store,
		passkey.NewOwners(d.GitHub), passkey.NewIssues(d.GitHub, d.GitHub), d.Now)
	if err != nil {
		return nil, fmt.Errorf("control: passkey: %w", err)
	}
	page, err := ui.New(ui.Deps{
		Answers: answers.New(d.Answers, passkeys),
		Owners:  answers.NewOwners(d.OwnerActions, passkeys), Setups: onboarding.NewSetup(d.GitHub, d.SetupViews),
		OAuth: d.OAuth, CookieKey: d.CookieKey, BaseURL: d.BaseURL, GitHub: d.GitHub,
		Account: acct, Consents: consents,
		Admissions: d.Admissions, PolicyViews: d.PolicyViews, RequestViews: d.RequestViews, Passkeys: passkeys, Operators: d.Operators, Now: d.Now,
	})
	if err != nil {
		return nil, fmt.Errorf("control: ui: %w", err)
	}
	c := newControl(acct, d.GitHub, d.Store, d.Admissions, page, d.Now)
	c.consents = consents
	return c, nil
}

// WorkerDeps are the dependencies of the worker façade: it handles events, the sweep and grants, and serves no UI.
// It takes no UI credentials.
type WorkerDeps struct {
	GitHub     *api.Client
	Store      store.Store
	Admissions *gate.Admissions // the sweep and installation events do nothing for an account no operator approved
	Now        func() time.Time
}

// NewWorker builds the façade of the worker role. Only HandleEvent, Sweep and Grant work on it; the handler
// methods return nil, and the worker mounts no route that needs them.
func NewWorker(d WorkerDeps) (*Control, error) {
	switch {
	case d.GitHub == nil:
		return nil, errors.New("control: nil GitHub client")
	case d.Store == nil:
		return nil, errors.New("control: nil Store")
	case d.Admissions == nil:
		return nil, errors.New("control: nil Admissions")
	}
	if d.Now == nil {
		d.Now = time.Now
	}
	acct, _ := account.New(d.Store)
	return &Control{account: acct, repos: d.GitHub, tenants: d.Store, admit: d.Admissions, now: d.Now}, nil
}

func (d Deps) validate() error {
	switch {
	case d.GitHub == nil:
		return errors.New("control: nil GitHub client")
	case d.OAuth == nil:
		return errors.New("control: nil OAuth")
	case d.Store == nil:
		return errors.New("control: nil Store")
	case d.Admissions == nil:
		return errors.New("control: nil Admissions")
	case d.PolicyViews == nil:
		return errors.New("control: nil PolicyViews")
	case d.RequestViews == nil:
		return errors.New("control: nil RequestViews")
	case d.Answers == nil:
		return errors.New("control: nil Answers")
	case len(d.CookieKey) != cookieKeyLen:
		return fmt.Errorf("control: CookieKey must be %d bytes", cookieKeyLen)
	case d.BaseURL == "":
		return errors.New("control: empty BaseURL")
	}
	return nil
}

// newControl assembles the façade from already-built parts; tests pass fakes.
func newControl(a account.Account, repos api.Repos, tenants tenantLister, admit admitter, page http.Handler, now func() time.Time) *Control {
	return &Control{account: a, repos: repos, tenants: tenants, admit: admit, ui: recovered(page), now: now}
}

// admitter answers whether an installation's account is approved: gate.Admissions.
type admitter interface {
	Admitted(ctx context.Context, installationID int64, owner string) (bool, error)
}

func (c *Control) UIHandler() http.Handler { // "/"
	return c.ui
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

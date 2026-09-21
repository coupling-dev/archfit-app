// Package ui is the server-rendered control-plane web UI.
package ui

import (
	"errors"
	"net/http"
	"time"

	"github.com/alexei-led/archfit-app/internal/control/account"
	"github.com/alexei-led/archfit-app/internal/control/assist"
	"github.com/alexei-led/archfit-app/internal/control/onboarding"
	"github.com/alexei-led/archfit-app/internal/control/proposal"
	"github.com/alexei-led/archfit-app/internal/github/api"
	"github.com/alexei-led/archfit-app/internal/github/auth"
)

var errNotImplemented = errors.New("ui: not implemented")

type Deps struct {
	OAuth      auth.OAuth // ui builds its own session child from OAuth, CookieKey and BaseURL
	CookieKey  []byte
	BaseURL    string
	GitHub     *api.Client
	Account    account.Account
	Consents   account.Consents
	Onboarding onboarding.Onboarding
	Assist     assist.Assist
	Proposals  proposal.Proposals
	Now        func() time.Time
}

// New returns the server-rendered UI mounted at "/". It serves its own static assets.
func New(d Deps) (http.Handler, error) {
	return nil, errNotImplemented
}

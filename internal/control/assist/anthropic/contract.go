// Package anthropic is the Anthropic implementation of assist.Provider over net/http.
package anthropic

import (
	"errors"
	"net/http"

	"github.com/alexei-led/archfit-app/internal/control/assist"
)

var errNotImplemented = errors.New("anthropic: not implemented")

type Config struct {
	APIKey string // from a secret reference
	Model  string // default "claude-sonnet-5"
}

// New returns a Provider backed by the Anthropic Messages API over net/http.
// New fails on an empty key or a model without an embedded price.
func New(c Config, hc *http.Client) (assist.Provider, error) {
	return nil, errNotImplemented
}

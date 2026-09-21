// Package anthropic is the Anthropic implementation of assist.Provider over net/http.
package anthropic

import (
	"context"
	"errors"
	"net/http"
	"time"

	"github.com/alexei-led/archfit-app/internal/control/assist"
)

const defaultModel = "claude-sonnet-5"

type Config struct {
	APIKey string // from a secret reference
	Model  string // default "claude-sonnet-5"
}

// New returns a Provider backed by the Anthropic Messages API over net/http.
// New fails on an empty key or a model without an embedded price.
func New(c Config, hc *http.Client) (assist.Provider, error) {
	if c.APIKey == "" {
		return nil, errors.New("anthropic: Config.APIKey is empty")
	}
	if c.Model == "" {
		c.Model = defaultModel
	}
	prices, err := loadPrices()
	if err != nil {
		return nil, err
	}
	p, ok := prices[c.Model]
	if !ok {
		// An unpriced model would make the budget meaningless.
		return nil, errors.New("anthropic: Config.Model has no embedded price")
	}

	// A copy, so the caller's client keeps its own redirect policy.
	client := http.Client{}
	if hc != nil {
		client = *hc
	}
	// The key must never follow a redirect to another host.
	client.CheckRedirect = func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }

	return &provider{
		apiKey:         c.APIKey,
		model:          c.Model,
		price:          p,
		hc:             &client,
		attemptContext: context.WithTimeout,
		sleep:          sleepContext,
		now:            time.Now,
	}, nil
}

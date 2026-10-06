// Package gcptoken reads OAuth access tokens of the runtime service account from the Google Cloud metadata server.
package gcptoken

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"
)

const (
	metadataURL = "http://metadata.google.internal/computeMetadata/v1/instance/service-accounts/default/token"
	// refreshBefore makes a cached token count as expired this long before its real expiry, so a token
	// never runs out between Token and the call that uses it.
	refreshBefore    = time.Minute
	maxResponseBytes = 64 << 10
)

var errInvalidResponse = errors.New("gcptoken: metadata server response is not a valid token")

// Source returns the access token of the service account the workload runs as.
// It reads no key file. It caches the token until refreshBefore ahead of its expiry.
// Safe for concurrent use; one refresh runs at a time.
type Source struct {
	url string
	hc  *http.Client
	now func() time.Time

	sem    chan struct{} // a one-slot lock that a waiting caller can leave when its context ends
	token  string
	expiry time.Time
}

// New returns a Source that asks the metadata server through hc. It sends no request until the first Token call.
func New(hc *http.Client) *Source {
	return &Source{url: metadataURL, hc: hc, now: time.Now, sem: make(chan struct{}, 1)}
}

type tokenResponse struct {
	AccessToken string `json:"access_token"`
	ExpiresIn   int64  `json:"expires_in"`
	TokenType   string `json:"token_type"`
}

// Token returns a valid access token. Errors never contain the token.
func (s *Source) Token(ctx context.Context) (string, error) {
	select {
	case s.sem <- struct{}{}:
	case <-ctx.Done():
		return "", fmt.Errorf("gcptoken: %w", ctx.Err())
	}
	defer func() { <-s.sem }()

	if s.token != "" && s.now().Before(s.expiry) {
		return s.token, nil
	}
	tok, err := s.fetch(ctx)
	if err != nil {
		return "", err
	}
	// A token that lives less than the margin is used once and not cached.
	s.token, s.expiry = tok.AccessToken, s.now().Add(time.Duration(tok.ExpiresIn)*time.Second-refreshBefore)
	return tok.AccessToken, nil
}

func (s *Source) fetch(ctx context.Context) (tokenResponse, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, s.url, nil)
	if err != nil {
		return tokenResponse{}, fmt.Errorf("gcptoken: building request: %w", err)
	}
	req.Header.Set("Metadata-Flavor", "Google")
	resp, err := s.hc.Do(req)
	if err != nil {
		return tokenResponse{}, fmt.Errorf("gcptoken: request failed: %w", err)
	}
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode != http.StatusOK {
		return tokenResponse{}, fmt.Errorf("gcptoken: metadata server status %d", resp.StatusCode)
	}
	raw, err := io.ReadAll(io.LimitReader(resp.Body, maxResponseBytes+1))
	if err != nil {
		return tokenResponse{}, fmt.Errorf("gcptoken: reading response: %w", err)
	}
	var tok tokenResponse
	// The decode error is dropped: it can quote bytes of the token.
	if len(raw) > maxResponseBytes || json.Unmarshal(raw, &tok) != nil ||
		tok.AccessToken == "" || tok.ExpiresIn <= 0 || !strings.EqualFold(tok.TokenType, "Bearer") {
		return tokenResponse{}, errInvalidResponse
	}
	return tok, nil
}

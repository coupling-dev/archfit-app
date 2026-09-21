package auth

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"regexp"
	"strconv"
	"strings"
	"sync"
	"time"
)

const (
	apiBaseURL      = "https://api.github.com"
	acceptGitHub    = "application/vnd.github+json"
	apiVersion      = "2022-11-28"
	expiryMargin    = 5 * time.Minute
	maxResponseSize = 1 << 20
	permissionRead  = "read"
)

var fullNamePattern = regexp.MustCompile(`^[A-Za-z0-9_.-]+/[A-Za-z0-9_.-]+$`)

// entry is one installation's cached token. mint is held while minting, so concurrent
// callers wait for one request; it also guards token and expires.
type entry struct {
	mint    sync.Mutex
	token   string
	expires time.Time
}

type mintResponse struct {
	Token        string            `json:"token"`
	ExpiresAt    time.Time         `json:"expires_at"`
	Permissions  map[string]string `json:"permissions"`
	Repositories []struct {
		FullName string `json:"full_name"`
	} `json:"repositories"`
}

func (s *source) InstallationToken(ctx context.Context, installationID int64) (string, error) {
	if installationID <= 0 {
		return "", errInstallationID(installationID)
	}
	s.mu.Lock()
	e := s.entries[installationID]
	if e == nil {
		e = &entry{}
		s.entries[installationID] = e
	}
	s.mu.Unlock()

	// The lock wait is not cancellable; it is bounded by the minting caller's request timeout.
	e.mint.Lock()
	defer e.mint.Unlock()
	if e.token != "" && s.now().Before(e.expires.Add(-expiryMargin)) {
		return e.token, nil
	}
	res, err := s.mint(ctx, installationID, nil)
	if err != nil {
		return "", err
	}
	if res.ExpiresAt.IsZero() {
		return "", errMalformed(installationID)
	}
	e.token, e.expires = res.Token, res.ExpiresAt
	// Invalidate may have dropped e while the request was in flight: this token is newer, cache it.
	s.mu.Lock()
	s.entries[installationID] = e
	s.mu.Unlock()
	return e.token, nil
}

func (s *source) Invalidate(installationID int64) {
	s.mu.Lock()
	delete(s.entries, installationID)
	s.mu.Unlock()
}

func (s *source) RepoReadToken(ctx context.Context, installationID int64, fullName string) (string, error) {
	if installationID <= 0 {
		return "", errInstallationID(installationID)
	}
	owner, name, _ := strings.Cut(fullName, "/")
	if !fullNamePattern.MatchString(fullName) || isDotSegment(owner) || isDotSegment(name) {
		return "", errors.New(errPrefix + "repository full name must be owner/name")
	}
	body, err := json.Marshal(struct {
		Repositories []string          `json:"repositories"`
		Permissions  map[string]string `json:"permissions"`
	}{[]string{name}, map[string]string{"contents": permissionRead}})
	if err != nil {
		return "", errors.New(errPrefix + "encode repository token request")
	}
	res, err := s.mint(ctx, installationID, body)
	if err != nil {
		return "", err
	}
	if !narrowedTo(res, fullName) {
		return "", fmt.Errorf(errPrefix+"installation %d: issued token is not limited to contents:read on the one repository", installationID)
	}
	return res.Token, nil
}

// mint posts to the access token endpoint. Decode errors are dropped: they may quote the response.
func (s *source) mint(ctx context.Context, installationID int64, body []byte) (mintResponse, error) {
	jwt, err := s.AppJWT()
	if err != nil {
		return mintResponse{}, err
	}
	endpoint := apiBaseURL + "/app/installations/" + strconv.FormatInt(installationID, 10) + "/access_tokens"
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, endpoint, bytes.NewReader(body))
	if err != nil {
		return mintResponse{}, fmt.Errorf(errPrefix+"installation %d: build token request", installationID)
	}
	req.Header.Set("Authorization", "Bearer "+jwt)
	req.Header.Set("Accept", acceptGitHub)
	req.Header.Set("X-GitHub-Api-Version", apiVersion)
	if len(body) > 0 {
		req.Header.Set("Content-Type", "application/json")
	}
	resp, err := s.hc.Do(req)
	if err != nil {
		return mintResponse{}, fmt.Errorf(errPrefix+"installation %d: token request: %w", installationID, transportCause(err))
	}
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode != http.StatusCreated {
		return mintResponse{}, fmt.Errorf(errPrefix+"installation %d: token request: status %d", installationID, resp.StatusCode)
	}
	var res mintResponse
	if err := json.NewDecoder(io.LimitReader(resp.Body, maxResponseSize)).Decode(&res); err != nil || res.Token == "" {
		return mintResponse{}, errMalformed(installationID)
	}
	return res, nil
}

// narrowedTo reports whether GitHub limited the token to contents:read (plus the metadata:read
// it always adds) on exactly the repository fullName.
func narrowedTo(res mintResponse, fullName string) bool {
	if len(res.Repositories) != 1 {
		return false
	}
	// fullName is ASCII; equal byte length keeps EqualFold from matching non-ASCII folds (K, ſ).
	got := res.Repositories[0].FullName
	if len(got) != len(fullName) || !strings.EqualFold(got, fullName) {
		return false
	}
	// An absent permissions field proves nothing: fail closed.
	if res.Permissions["contents"] != permissionRead {
		return false
	}
	for permission, level := range res.Permissions {
		if (permission != "contents" && permission != "metadata") || level != permissionRead {
			return false
		}
	}
	return true
}

func isDotSegment(s string) bool { return s == "." || s == ".." }

func errInstallationID(installationID int64) error {
	return fmt.Errorf(errPrefix+"installation ID %d is not positive", installationID)
}

func errMalformed(installationID int64) error {
	return fmt.Errorf(errPrefix+"installation %d: token request: status %d with a malformed body", installationID, http.StatusCreated)
}

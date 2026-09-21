package auth

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
)

const (
	authorizeURL = "https://github.com/login/oauth/authorize"
	exchangeURL  = "https://github.com/login/oauth/access_token"
	userURL      = apiBaseURL + "/user"
)

// AuthorizeURL sends no scope: a GitHub App's user token is limited by the App's permissions.
func (s *source) AuthorizeURL(state, redirectURL string) string {
	q := url.Values{"client_id": {s.clientID}, "redirect_uri": {redirectURL}, "state": {state}}
	return authorizeURL + "?" + q.Encode()
}

func (s *source) Exchange(ctx context.Context, code string) (UserToken, error) {
	if code == "" {
		return UserToken{}, errors.New(errPrefix + "OAuth code is empty")
	}
	form := url.Values{"client_id": {s.clientID}, "client_secret": {s.clientSecret}, "code": {code}}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, exchangeURL, strings.NewReader(form.Encode()))
	if err != nil {
		return UserToken{}, errors.New(errPrefix + "build OAuth exchange request")
	}
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	req.Header.Set("Accept", "application/json")
	// GitHub answers 200 for a refused code too; the body decides.
	var granted struct {
		AccessToken string `json:"access_token"`
		Error       string `json:"error"`
	}
	if err := s.doJSON(req, "OAuth exchange", &granted); err != nil {
		return UserToken{}, err
	}
	if granted.Error != "" || granted.AccessToken == "" {
		return UserToken{}, errors.New(errPrefix + "OAuth exchange: GitHub issued no access token")
	}

	req, err = http.NewRequestWithContext(ctx, http.MethodGet, userURL, nil)
	if err != nil {
		return UserToken{}, errors.New(errPrefix + "build user request")
	}
	req.Header.Set("Authorization", "Bearer "+granted.AccessToken)
	req.Header.Set("Accept", acceptGitHub)
	req.Header.Set("X-GitHub-Api-Version", apiVersion)
	var user struct {
		Login string `json:"login"`
		ID    int64  `json:"id"`
	}
	if err := s.doJSON(req, "user lookup", &user); err != nil {
		return UserToken{}, err
	}
	if user.Login == "" || user.ID <= 0 {
		return UserToken{}, errors.New(errPrefix + "user lookup: response names no user")
	}
	return UserToken{AccessToken: granted.AccessToken, Login: user.Login, UserID: user.ID}, nil
}

// doJSON expects a 200 and decodes it into v. Decode errors are dropped: they may quote the response.
func (s *source) doJSON(req *http.Request, what string, v any) error {
	resp, err := s.hc.Do(req)
	if err != nil {
		return fmt.Errorf(errPrefix+"%s: %w", what, transportCause(err))
	}
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf(errPrefix+"%s: status %d", what, resp.StatusCode)
	}
	if err := json.NewDecoder(io.LimitReader(resp.Body, maxResponseSize)).Decode(v); err != nil {
		return fmt.Errorf(errPrefix+"%s: status %d with a malformed body", what, resp.StatusCode)
	}
	return nil
}

// transportCause strips the *url.Error of http.Client.Do: it carries the full URL; only its cause is kept.
func transportCause(err error) error {
	var ue *url.Error
	if errors.As(err, &ue) {
		return ue.Err
	}
	return err
}

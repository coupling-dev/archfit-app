package ui

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"net/http"

	"github.com/coupling-dev/archfit-app/internal/control/passkey"
	"github.com/coupling-dev/archfit-app/internal/github/api"
)

type confirmationRequestBody struct {
	Installation int64  `json:"installation"`
	Attempt      string `json:"attempt"`
}
type confirmationAttemptJSON struct {
	Attempt      string `json:"attempt"`
	Installation int64  `json:"installation"`
}
type confirmationIssueJSON struct {
	Token string `json:"token"`
}

func (confirmationIssueJSON) carriesToken() {}

func sessionBinding(r *http.Request) string {
	cookie, err := r.Cookie("archfit_session")
	if err != nil || cookie.Value == "" {
		return ""
	}
	sum := sha256.Sum256([]byte("first-enrolment\x00" + cookie.Value))
	return hex.EncodeToString(sum[:])
}
func (s *server) apiConfirmationRequest(r *http.Request, sc scope) (any, error) {
	if err := decodeJSON(r, &struct{}{}); err != nil {
		return nil, err
	}
	u, err := s.passkeyUser(r, sc)
	if err != nil {
		return nil, err
	}
	p, ok := s.passkeys.(interface {
		RequestConfirmation(context.Context, api.Repo, passkey.User) (string, error)
	})
	if !ok {
		return nil, invalid("Confirmation is unavailable.")
	}
	id, err := p.RequestConfirmation(r.Context(), sc.repo, u)
	if err != nil {
		return nil, err
	}
	return confirmationAttemptJSON{Attempt: id, Installation: sc.repo.InstallationID}, nil
}
func (s *server) confirmationInput(r *http.Request) (confirmationRequestBody, error) {
	var in confirmationRequestBody
	if err := decodeJSON(r, &in); err != nil {
		return in, err
	}
	if s.confirmations == nil {
		return in, invalid("Confirmation is unavailable.")
	}
	return in, nil
}
func (s *server) apiConfirmationLookup(r *http.Request, _ scope) (any, error) {
	in, err := s.confirmationInput(r)
	if err != nil {
		return nil, err
	}
	return s.confirmations.Describe(r.Context(), in.Installation, in.Attempt)
}
func (s *server) apiConfirmationIssue(r *http.Request, sc scope) (any, error) {
	in, err := s.confirmationInput(r)
	if err != nil {
		return nil, err
	}
	token, err := s.confirmations.Issue(r.Context(), in.Installation, in.Attempt, sc.user.UserID)
	if err != nil {
		return nil, err
	}
	return confirmationIssueJSON{Token: token}, nil
}
func (s *server) apiConfirmationRevoke(r *http.Request, sc scope) (any, error) {
	in, err := s.confirmationInput(r)
	if err != nil {
		return nil, err
	}
	if err = s.confirmations.Revoke(r.Context(), in.Installation, in.Attempt, sc.user.UserID); err != nil {
		return nil, err
	}
	return noticeJSON{Notice: "Confirmation revoked."}, nil
}

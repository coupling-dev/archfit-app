package passkey

import (
	"context"
	"encoding/json"

	"github.com/coupling-dev/archfit-app/internal/github/api"
	"github.com/coupling-dev/archfit-app/internal/store"
)

// ConfirmationMetadata is safe operator-visible context; it contains no secret, hash or session binding.
type ConfirmationMetadata struct {
	UserID       int64
	RepositoryID int64
	AttemptID    string
}

// Describe is called only after operator authorization.
func (s *OperatorConfirmations) Describe(ctx context.Context, tenant int64, id string) (ConfirmationMetadata, error) {
	if !s.ready() || tenant <= 0 || !confirmationID(id) {
		return ConfirmationMetadata{}, ErrConfirmation
	}
	raw, err := s.store.Get(ctx, store.Tenant(tenant), confirmationAttemptKey(id))
	if err != nil {
		return ConfirmationMetadata{}, ErrConfirmation
	}
	var user int64
	if json.Unmarshal(raw, &user) != nil || user <= 0 {
		return ConfirmationMetadata{}, ErrConfirmation
	}
	raw, err = s.store.Get(ctx, store.Tenant(tenant), confirmationUserKey(user))
	if err != nil {
		return ConfirmationMetadata{}, err
	}
	r, err := readConfirmation(raw)
	if err != nil {
		return ConfirmationMetadata{}, err
	}
	if r.AttemptID != id || r.Used || r.Purpose != confirmationPurpose {
		return ConfirmationMetadata{}, ErrConfirmation
	}
	return ConfirmationMetadata{UserID: r.UserID, RepositoryID: r.RepositoryID, AttemptID: id}, nil
}

// RequestConfirmation enforces registration eligibility before creating a pending attempt.
func (s *service) RequestConfirmation(ctx context.Context, r api.Repo, u User) (string, error) {
	if _, err := s.mayRegister(ctx, r, u); err != nil {
		return "", err
	}
	list, err := s.List(ctx, r, u.ID)
	if err != nil {
		return "", err
	}
	if len(list) != 0 {
		return "", ErrNeedsPasskey
	}
	return NewOperatorConfirmations(s.store, s.now).Request(ctx, ConfirmationScope{InstallationID: r.InstallationID, RepositoryID: r.ID, UserID: u.ID, SessionBinding: u.SessionBinding})
}

package ui

import (
	"errors"
	"net/http"

	"github.com/coupling-dev/archfit-app/internal/gate"
	"github.com/coupling-dev/archfit-app/internal/github/api"
	"github.com/coupling-dev/archfit-app/internal/wire/result"
)

type pullJSON struct {
	gate.PullPage
	Review *reviewJSON `json:"owner_action,omitempty"`
}

func (s *server) apiPull(r *http.Request, sc scope) (any, error) {
	number, ok := pullNumber(r)
	if !ok {
		return nil, api.ErrNotFound
	}
	if s.pullViews == nil {
		return nil, errors.New("pull: reader unavailable")
	}
	page, err := s.pullViews.PullPage(r.Context(), sc.repo, number)
	if err != nil {
		return nil, err
	}
	body := pullJSON{PullPage: page}
	if page.View != nil && page.View.Status == result.StatusUnknown {
		body.Review = s.ownerReview(r, sc, number)
	}
	return body, nil
}

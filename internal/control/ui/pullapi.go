package ui

import (
	"errors"
	"net/http"

	"github.com/alexei-led/archfit-app/internal/github/api"
)

func (s *server) apiPull(r *http.Request, sc scope) (any, error) {
	number, ok := pullNumber(r)
	if !ok {
		return nil, api.ErrNotFound
	}
	if s.pullViews == nil {
		return nil, errors.New("pull: reader unavailable")
	}
	return s.pullViews.PullPage(r.Context(), sc.repo, number)
}

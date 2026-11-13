package ui

import (
	"net/http"

	"github.com/alexei-led/archfit-app/internal/github/api"
)

// The page routes remain mounted while the Archfit 4 pages replace their Archfit 3 data sources.
func (s *server) apiLegacyUnavailable(r *http.Request, sc scope) (any, error) {
	if r.PathValue("number") != "" {
		number, ok := pullNumber(r)
		if !ok {
			return nil, api.ErrNotFound
		}
		if _, err := s.gh.pulls.PullSummary(r.Context(), sc.repo.RepoRef, number); err != nil {
			return nil, err
		}
	}
	return nil, conflict("Archfit 3 reports and discovery are no longer available. Use Archfit 4 setup and requests.")
}

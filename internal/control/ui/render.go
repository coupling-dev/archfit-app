package ui

import "net/http"

// fail answers a page route with the documented status: the shell as an error page with the error's fixed sentence.
// The page keeps the shell's CSP, and no error's own text reaches it.
func (s *server) fail(w http.ResponseWriter, r *http.Request, err error) {
	status, outcome, sentence := explain(err)
	logOf(r).outcome = outcome
	if outcome == "rate_limited" {
		w.Header().Set("Retry-After", retryAfterSeconds)
	}
	s.shell.serve(w, status, sentence)
}

func repoPath(sc scope, suffix string) string {
	return "/r/" + sc.repo.FullName + suffix
}

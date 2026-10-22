package ui

import (
	"encoding/json"
	"errors"
	"log/slog"
	"mime"
	"net/http"
)

const (
	apiPrefix  = "/api/"
	csrfHeader = "X-CSRF-Token"
	mediaJSON  = "application/json"
)

var errMediaType = &userError{http.StatusUnsupportedMediaType, "media_type", "Send the request as JSON."}

// apiFunc is a JSON endpoint: it gets the chain's scope and returns the response body or an error. Every response is
// an explicit type of this package, never a stored record, so no token can reach the browser.
type apiFunc func(r *http.Request, sc scope) (any, error)

// apiSignedIn is signedIn for /api: 401 JSON without a session, and on POST a JSON body with the session's CSRF token
// in the X-CSRF-Token header.
func (s *server) apiSignedIn(next apiFunc) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		u, ok := s.sessions.User(r)
		if !ok {
			signedOut(w, r)
			return
		}
		logOf(r).userID = u.UserID
		sc := scope{user: u}
		if r.Method == http.MethodPost {
			if mt, _, err := mime.ParseMediaType(r.Header.Get("Content-Type")); err != nil || mt != mediaJSON {
				s.failJSON(w, r, errMediaType)
				return
			}
			if !validCSRF(u, r.Header.Get(csrfHeader)) {
				s.failJSON(w, r, errCSRF)
				return
			}
		}
		s.answer(w, r, sc, next)
	})
}

// apiRepo is the repository authorization of the App: the {o}/{n} of the path is authorized at minPermission on every
// request, and the Repo is the only repository identity the endpoint gets. A body or query never names the repository.
// The repository pages are the shell alone and authorize nothing, so this is the only step that does.
func (s *server) apiRepo(minPermission string, next apiFunc) http.Handler {
	return s.apiSignedIn(func(r *http.Request, sc scope) (any, error) {
		repo, err := s.sessions.Authorize(r.Context(), sc.user, r.PathValue("o")+"/"+r.PathValue("n"), minPermission)
		if err != nil {
			return nil, err
		}
		l := logOf(r)
		l.repoID, l.installationID = repo.ID, repo.InstallationID
		sc.repo = repo
		if err := s.admit(r.Context(), repo); err != nil {
			return nil, err
		}
		return next(r, sc)
	})
}

func (s *server) answer(w http.ResponseWriter, r *http.Request, sc scope, next apiFunc) {
	body, err := next(r, sc)
	if err != nil {
		if !s.endIfTokenRejected(w, r, err) {
			s.failJSON(w, r, err)
		}
		return
	}
	writeJSON(w, r, http.StatusOK, body)
}

type errorJSON struct {
	Error       string           `json:"error"`
	Diagnostics []diagnosticJSON `json:"diagnostics,omitempty"` // the problems of a refused policy text
	Waiting     string           `json:"waiting,omitempty"`     // the account waiting for approval: the Waiting page
}

// failJSON is fail for /api: the documented status and sentence, as JSON, and the problems of a refused policy.
func (s *server) failJSON(w http.ResponseWriter, r *http.Request, err error) {
	status, outcome, sentence := explain(err)
	logOf(r).outcome = outcome
	if outcome == "rate_limited" {
		w.Header().Set("Retry-After", retryAfterSeconds)
	}
	body := errorJSON{Error: sentence}
	body.Waiting, _ = errorsAsWaiting(err)
	var problems *policyProblems
	if errors.As(err, &problems) {
		body.Diagnostics = diagnosticsJSON(problems.list)
	}
	writeJSON(w, r, status, body)
}

func writeJSON(w http.ResponseWriter, r *http.Request, status int, body any) {
	buf, err := json.Marshal(body)
	if err != nil {
		logOf(r).outcome = "encode"
		slog.Error("ui response encoding failed", "route", r.Pattern)
		status, buf = http.StatusInternalServerError, []byte(`{"error":"internal error"}`)
	}
	w.Header().Set("Content-Type", mediaJSON)
	w.Header().Set("Cache-Control", "no-store")
	w.WriteHeader(status)
	_, _ = w.Write(buf)
}

// decodeJSON reads a POST body into dst. Unknown fields are refused, so a body cannot smuggle a repository or a user.
func decodeJSON(r *http.Request, dst any) error {
	dec := json.NewDecoder(r.Body)
	dec.DisallowUnknownFields()
	if err := dec.Decode(dst); err != nil {
		var tooLarge *http.MaxBytesError
		if errors.As(err, &tooLarge) {
			return err
		}
		return invalid("The request could not be read.")
	}
	return nil
}

type sessionJSON struct {
	Login    string `json:"login"`
	CSRF     string `json:"csrf"`
	Operator bool   `json:"operator,omitempty"` // may approve installations on /admin
	Waiting  int    `json:"waiting,omitempty"`  // operators only: installations waiting; -1 when it could not be read
}

// apiSession is the signed-in login and the CSRF token the app sends with every write.
func (s *server) apiSession(r *http.Request, sc scope) (any, error) {
	body := sessionJSON{Login: sc.user.Login, CSRF: csrfToken(sc.user)}
	if s.isOperator(sc.user) {
		body.Operator, body.Waiting = true, s.waitingCount(r.Context())
	}
	return body, nil
}

// noticeJSON answers a write: the sentence to show and, when the next step is another page, its path.
type noticeJSON struct {
	Notice   string `json:"notice,omitempty"`
	Location string `json:"location,omitempty"`
}

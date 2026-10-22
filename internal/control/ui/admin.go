package ui

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"slices"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/alexei-led/archfit-app/internal/control/ui/session"
	"github.com/alexei-led/archfit-app/internal/gate"
	"github.com/alexei-led/archfit-app/internal/github/api"
)

// waitingError answers a repository API of an installation no operator approved: the web app shows the Waiting page.
type waitingError struct{ account string }

func (e *waitingError) Error() string { return "ui: installation not approved" }

const waitingSentence = "This account is waiting for approval."

// admit answers a repository's admission. The gate refuses the same installation, so the page tells the user why
// nothing happens instead of showing an empty repository.
func (s *server) admit(ctx context.Context, repo api.Repo) error {
	owner := ownerOf(repo.FullName)
	admitted, err := s.admissions.Admitted(ctx, repo.InstallationID, owner)
	switch {
	case err != nil:
		return fmt.Errorf("ui: read admission: %w", err) // "try again", never the Waiting page of an approved account
	case !admitted:
		return &waitingError{account: owner}
	}
	return nil
}

func ownerOf(fullName string) string {
	owner, _, _ := strings.Cut(fullName, "/")
	return owner
}

// isOperator reports whether the signed-in user may approve installations: by GitHub user ID, never by login.
func (s *server) isOperator(u session.User) bool {
	return u.UserID > 0 && slices.Contains(s.operators, u.UserID)
}

// apiOperator serves an operator-only API. Anyone else gets the same "Not found" as an unknown path.
func (s *server) apiOperator(next apiFunc) http.Handler {
	return s.apiSignedIn(func(r *http.Request, sc scope) (any, error) {
		if !s.isOperator(sc.user) {
			return nil, api.ErrNotFound
		}
		return next(r, sc)
	})
}

type installRepoJSON struct {
	Name    string `json:"name"`
	Private bool   `json:"private"`
}

type installationJSON struct {
	ID          int64             `json:"id"`
	Account     string            `json:"account"`
	Type        string            `json:"type"` // "Organization" or "User"
	InstalledAt time.Time         `json:"installed_at"`
	Repos       []installRepoJSON `json:"repos"`
	ReposError  bool              `json:"repos_error"` // the repositories could not be read; the row stands
	Auto        bool              `json:"auto"`        // approved by the App's configuration: no revoke here
	ApprovedBy  string            `json:"approved_by,omitempty"`
	ApprovedAt  *time.Time        `json:"approved_at,omitempty"`
}

type adminBody struct {
	Waiting  []installationJSON `json:"waiting"`
	Approved []installationJSON `json:"approved"`
	// Unreadable counts installations whose approval could not be read: they are listed as waiting, never as
	// approved, so a Store outage never looks like an approval.
	Unreadable int `json:"unreadable"`
}

// apiAdmin lists every installation of the App: the waiting ones first, newest first, then the approved ones by name.
func (s *server) apiAdmin(r *http.Request, _ scope) (any, error) {
	ctx := r.Context()
	installs, err := s.gh.installs.ListInstallations(ctx)
	if err != nil {
		return nil, err
	}
	rows := make([]installationJSON, len(installs))
	unreadable := make([]bool, len(installs))
	slots := make(chan struct{}, parallelLookups)
	var wg sync.WaitGroup
	for i, in := range installs {
		wg.Add(1)
		slots <- struct{}{}
		go func() {
			defer wg.Done()
			defer func() { <-slots }()
			rows[i], unreadable[i] = s.installationRow(ctx, in)
		}()
	}
	wg.Wait()

	body := adminBody{Waiting: []installationJSON{}, Approved: []installationJSON{}}
	for i, row := range rows {
		if unreadable[i] {
			body.Unreadable++
		}
		if row.Auto || row.ApprovedAt != nil {
			body.Approved = append(body.Approved, row)
		} else {
			body.Waiting = append(body.Waiting, row)
		}
	}
	sort.SliceStable(body.Waiting, func(i, j int) bool { return body.Waiting[i].InstalledAt.After(body.Waiting[j].InstalledAt) })
	sort.SliceStable(body.Approved, func(i, j int) bool {
		return strings.ToLower(body.Approved[i].Account) < strings.ToLower(body.Approved[j].Account)
	})
	return body, nil
}

// installationRow reads one installation's repositories and approval. unreadable is true when the approval could not
// be read; the row is then not approved.
func (s *server) installationRow(ctx context.Context, in api.Installation) (row installationJSON, unreadable bool) {
	row = installationJSON{ID: in.ID, Account: in.Account, Type: in.AccountType, InstalledAt: in.CreatedAt,
		Repos: []installRepoJSON{}, Auto: s.admissions.Auto(in.Account)}
	repos, err := s.gh.installs.ListRepos(ctx, in.ID)
	if err != nil {
		slog.Warn("ui admin: repositories unavailable", "installation_id", in.ID)
		row.ReposError = true
	}
	for _, repo := range repos {
		_, name, _ := strings.Cut(repo.FullName, "/")
		row.Repos = append(row.Repos, installRepoJSON{Name: name, Private: repo.Private})
	}
	sort.Slice(row.Repos, func(i, j int) bool { return row.Repos[i].Name < row.Repos[j].Name })
	if row.Auto {
		return row, false
	}
	rec, found, err := s.admissions.Record(ctx, in.ID)
	if err != nil {
		slog.Warn("ui admin: approval unreadable", "installation_id", in.ID)
		return row, true
	}
	if found {
		at := rec.At
		row.ApprovedBy, row.ApprovedAt = rec.By, &at
	}
	return row, false
}

var errAutoApproved = conflict("This account is approved by the App's configuration. Change it by pull request.")

// installation finds the installation of the path's ID among the App's own: an ID that is not one of them is
// "Not found", and the account login always comes from GitHub, never from the request.
func (s *server) installation(r *http.Request) (api.Installation, error) {
	id, err := strconv.ParseInt(r.PathValue("id"), 10, 64)
	if err != nil || id <= 0 {
		return api.Installation{}, api.ErrNotFound
	}
	installs, err := s.gh.installs.ListInstallations(r.Context())
	if err != nil {
		return api.Installation{}, err
	}
	i := slices.IndexFunc(installs, func(in api.Installation) bool { return in.ID == id })
	if i < 0 {
		return api.Installation{}, api.ErrNotFound
	}
	logOf(r).installationID = id
	return installs[i], nil
}

func (s *server) apiApprove(r *http.Request, sc scope) (any, error) {
	if err := decodeJSON(r, &struct{}{}); err != nil {
		return nil, err
	}
	in, err := s.installation(r)
	if err != nil {
		return nil, err
	}
	if s.admissions.Auto(in.Account) {
		return nil, errAutoApproved
	}
	adm := gate.Admission{Account: in.Account, By: sc.user.Login, ByID: sc.user.UserID, At: s.now().UTC()}
	if err := s.account.Admit(r.Context(), in.ID, adm); err != nil {
		return nil, err
	}
	return noticeJSON{Notice: "Approved " + in.Account + ". Archfit runs for it from its next event."}, nil
}

func (s *server) apiRevoke(r *http.Request, sc scope) (any, error) {
	if err := decodeJSON(r, &struct{}{}); err != nil {
		return nil, err
	}
	in, err := s.installation(r)
	if err != nil {
		return nil, err
	}
	if s.admissions.Auto(in.Account) {
		return nil, errAutoApproved
	}
	if err := s.account.Unadmit(r.Context(), in.ID, sc.user.UserID); err != nil {
		return nil, err
	}
	return noticeJSON{Notice: "Revoked " + in.Account + ". Archfit stops for it from its next event."}, nil
}

// waitingCount is the number of installations no one approved yet, for the operator's header. -1 when it could not be
// read: the header then shows no count rather than a wrong one.
func (s *server) waitingCount(ctx context.Context) int {
	installs, err := s.gh.installs.ListInstallations(ctx)
	if err != nil {
		return -1
	}
	n := 0
	for _, in := range installs {
		admitted, err := s.admissions.Admitted(ctx, in.ID, in.Account)
		if err != nil {
			return -1
		}
		if !admitted {
			n++
		}
	}
	return n
}

// admittedSet answers each installation of repos once: a list of a hundred repositories is a few installations. An
// approval that cannot be read lists the repository as waiting: the list reads no state for it either way.
func (s *server) admittedSet(ctx context.Context, repos []api.Repo) map[int64]bool {
	out := map[int64]bool{}
	for _, r := range repos {
		if _, seen := out[r.InstallationID]; !seen {
			out[r.InstallationID], _ = s.admissions.Admitted(ctx, r.InstallationID, ownerOf(r.FullName))
		}
	}
	return out
}

// errorsAsWaiting extracts the account of a waitingError.
func errorsAsWaiting(err error) (string, bool) {
	var w *waitingError
	if errors.As(err, &w) {
		return w.account, true
	}
	return "", false
}

// Package ui is the server-rendered control-plane web UI.
package ui

import (
	"context"
	"embed"
	"html/template"
	"io/fs"
	"net/http"
	"strings"
	"time"

	"github.com/alexei-led/archfit-app/internal/control/account"
	"github.com/alexei-led/archfit-app/internal/control/assist"
	"github.com/alexei-led/archfit-app/internal/control/onboarding"
	"github.com/alexei-led/archfit-app/internal/control/proposal"
	"github.com/alexei-led/archfit-app/internal/control/ui/session"
	"github.com/alexei-led/archfit-app/internal/github/api"
	"github.com/alexei-led/archfit-app/internal/github/auth"
	"github.com/alexei-led/archfit-app/internal/wire/policy"
)

//go:embed templates
var templatesFS embed.FS

//go:embed static
var staticFS embed.FS

type Deps struct {
	OAuth      auth.OAuth // ui builds its own session child from OAuth, CookieKey and BaseURL
	CookieKey  []byte
	BaseURL    string
	GitHub     *api.Client
	Account    account.Account
	Consents   account.Consents
	Onboarding onboarding.Onboarding
	Assist     assist.Assist
	Proposals  proposal.Proposals
	Now        func() time.Time
}

// The parts of the github/api role interfaces ui calls.
type (
	fileReader interface {
		File(ctx context.Context, r api.RepoRef, ref, path string) (content []byte, blobSHA string, err error)
		BranchHead(ctx context.Context, r api.RepoRef, branch string) (string, error)
	}
	checkReader interface {
		CheckRuns(ctx context.Context, r api.RepoRef, headSHA, name string) ([]api.CheckRun, error)
	}
	workflows interface {
		Dispatch(ctx context.Context, r api.RepoRef, workflowPath, ref string, inputs map[string]string) error
		LatestRun(ctx context.Context, r api.RepoRef, workflowPath string, f api.RunFilter) (api.WorkflowRun, error)
		Artifact(ctx context.Context, r api.RepoRef, runID int64, name string, maxBytes int64) ([]byte, error)
	}
	pullReader interface {
		Pull(ctx context.Context, r api.RepoRef, number int) (api.Pull, error)
		OpenPulls(ctx context.Context, r api.RepoRef) ([]api.Pull, error)
	}
	repoLister interface {
		UserRepos(ctx context.Context, userToken string) ([]api.Repo, error)
	}
)

type github struct {
	files   fileReader
	checks  checkReader
	actions workflows
	pulls   pullReader
	users   repoLister
}

type server struct {
	gh         github
	sessions   session.Sessions
	account    account.Account
	consents   account.Consents
	onboarding onboarding.Onboarding
	assist     assist.Assist
	proposals  proposal.Proposals
	now        func() time.Time
	pages      map[string]*template.Template
}

// New returns the server-rendered UI mounted at "/". It serves its own static assets.
func New(d Deps) (http.Handler, error) {
	sessions, err := session.New(session.Config{CookieKey: d.CookieKey, BaseURL: d.BaseURL}, d.OAuth, d.GitHub, d.GitHub, d.Now)
	if err != nil {
		return nil, err
	}
	return newHandler(d, github{files: d.GitHub, checks: d.GitHub, actions: d.GitHub, pulls: d.GitHub, users: d.GitHub}, sessions, builtWeb()), nil
}

// newHandler mounts every route. web is the build of the web app; tests hand in a small one.
func newHandler(d Deps, gh github, sessions session.Sessions, web fs.FS) http.Handler {
	static, err := fs.Sub(staticFS, "static")
	if err != nil {
		panic(err) // the embedded folder name is a compile-time constant
	}
	s := &server{
		gh:         gh,
		sessions:   sessions,
		account:    d.Account,
		consents:   d.Consents,
		onboarding: d.Onboarding,
		assist:     d.Assist,
		proposals:  d.Proposals,
		now:        d.Now,
		pages:      parsePages(),
	}

	mux := http.NewServeMux()
	files := http.StripPrefix("/static/", http.FileServerFS(static))
	mux.HandleFunc("GET /static/", func(w http.ResponseWriter, r *http.Request) {
		if strings.HasSuffix(r.URL.Path, "/") { // no directory listings
			http.NotFound(w, r)
			return
		}
		files.ServeHTTP(w, r)
	})
	mux.HandleFunc("GET /schema/policy.json", func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/schema+json")
		_, _ = w.Write(policy.Schema())
	})
	mux.Handle("GET /auth/login", sessions.LoginHandler())
	mux.Handle("GET /auth/callback", sessions.CallbackHandler())
	mux.Handle("POST /auth/logout", s.signedIn(func(w http.ResponseWriter, r *http.Request, _ session.User) {
		sessions.LogoutHandler().ServeHTTP(w, r)
	}))
	// Pages of the web app: the shell, which the app fills from /api. The repository page still authorizes, so a
	// repository the user cannot see is "Not found" before any script runs.
	index := shell(web)
	mux.HandleFunc("GET /assets/", assets(web))
	mux.HandleFunc("GET /{$}", index)
	page := func(w http.ResponseWriter, r *http.Request, _ scope) { index(w, r) }
	pullPage := func(w http.ResponseWriter, r *http.Request, sc scope) {
		if _, ok := pullNumber(r); !ok {
			s.fail(w, r, sc, api.ErrNotFound)
			return
		}
		index(w, r)
	}
	mux.Handle("GET /r/{o}/{n}", s.repo(permRead, page))
	mux.Handle("GET /r/{o}/{n}/graph", s.repo(permRead, page))
	mux.Handle("GET /r/{o}/{n}/pull/{number}", s.repo(permRead, pullPage))
	mux.Handle("GET /r/{o}/{n}/pull/{number}/graph", s.repo(permRead, pullPage))

	mux.Handle("GET /api/session", s.apiSignedIn(apiSession))
	mux.Handle("GET /api/repos", s.apiSignedIn(s.apiRepos))
	mux.Handle("GET /api/repos/{o}/{n}/overview", s.apiRepo(permRead, s.apiStatus))
	mux.Handle("GET /api/repos/{o}/{n}/findings", s.apiRepo(permRead, s.apiFindings))
	mux.Handle("GET /api/repos/{o}/{n}/graph", s.apiRepo(permRead, s.apiGraph))
	mux.Handle("GET /api/repos/{o}/{n}/pulls/{number}", s.apiRepo(permRead, s.apiPull))
	mux.Handle("GET /api/repos/{o}/{n}/pulls/{number}/graph", s.apiRepo(permRead, s.apiPullGraph))
	mux.Handle("POST /api/repos/{o}/{n}/onboarding", s.apiRepo(permAdmin, s.apiStartOnboarding))
	mux.Handle("POST /api/repos/{o}/{n}/discover", s.apiRepo(permAdmin, s.apiDiscover))
	mux.HandleFunc("/api/", func(w http.ResponseWriter, r *http.Request) {
		s.failJSON(w, r, api.ErrNotFound)
	})

	// Pages still rendered on the server; each moves to the web app in a later phase.
	mux.Handle("GET /r/{o}/{n}/policy", s.repo(permRead, s.editor))
	mux.Handle("POST /r/{o}/{n}/policy", s.repo(permWrite, s.save))
	mux.Handle("POST /r/{o}/{n}/policy/check", s.repo(permRead, s.check))
	mux.Handle("POST /r/{o}/{n}/policy/ready", s.repo(permWrite, s.ready))
	mux.Handle("POST /r/{o}/{n}/assist", s.repo(permWrite, s.propose))
	mux.Handle("GET /r/{o}/{n}/consent", s.repo(permAdmin, s.consentPage))
	mux.Handle("POST /r/{o}/{n}/consent", s.repo(permAdmin, s.consent))
	return chain(mux)
}

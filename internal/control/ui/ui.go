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

// Names of the prebuilt editor bundle; see static/vendor/README.md.
const (
	monacoScript = "vendor/monaco.bundle.js"
	monacoStyle  = "vendor/monaco.bundle.css"
)

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
		LatestRun(ctx context.Context, r api.RepoRef, workflowPath, branch string) (api.WorkflowRun, error)
		Artifact(ctx context.Context, r api.RepoRef, runID int64, name string, maxBytes int64) ([]byte, error)
	}
	repoLister interface {
		UserRepos(ctx context.Context, userToken string) ([]api.Repo, error)
	}
)

type github struct {
	files   fileReader
	checks  checkReader
	actions workflows
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
	monaco     bool
}

// New returns the server-rendered UI mounted at "/". It serves its own static assets.
func New(d Deps) (http.Handler, error) {
	sessions, err := session.New(session.Config{CookieKey: d.CookieKey, BaseURL: d.BaseURL}, d.OAuth, d.GitHub, d.GitHub, d.Now)
	if err != nil {
		return nil, err
	}
	return newHandler(d, github{files: d.GitHub, checks: d.GitHub, actions: d.GitHub, users: d.GitHub}, sessions), nil
}

func newHandler(d Deps, gh github, sessions session.Sessions) http.Handler {
	static, err := fs.Sub(staticFS, "static")
	if err != nil {
		panic(err) // the embedded folder name is a compile-time constant
	}
	_, monacoErr := fs.Stat(static, monacoScript)
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
		monaco:     monacoErr == nil,
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
	mux.HandleFunc("GET /{$}", s.overview)
	mux.Handle("GET /r/{o}/{n}", s.repo(permRead, s.status))
	mux.Handle("POST /r/{o}/{n}/onboarding", s.repo(permAdmin, s.startOnboarding))
	mux.Handle("POST /r/{o}/{n}/discover", s.repo(permAdmin, s.discover))
	mux.Handle("GET /r/{o}/{n}/policy", s.repo(permRead, s.editor))
	mux.Handle("POST /r/{o}/{n}/policy", s.repo(permWrite, s.save))
	mux.Handle("POST /r/{o}/{n}/policy/ready", s.repo(permWrite, s.ready))
	mux.Handle("GET /r/{o}/{n}/graph", s.repo(permRead, s.graphPage))
	mux.Handle("POST /r/{o}/{n}/assist", s.repo(permWrite, s.propose))
	mux.Handle("GET /r/{o}/{n}/consent", s.repo(permAdmin, s.consentPage))
	mux.Handle("POST /r/{o}/{n}/consent", s.repo(permAdmin, s.consent))
	return chain(mux)
}

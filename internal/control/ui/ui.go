// Package ui is the control-plane web UI: the embedded web app, its JSON API and the sign-in routes.
package ui

import (
	"context"
	"io/fs"
	"net/http"
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

// The parts of github/api ui calls: role-interface methods and the *Client readers kept for the UI.
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
		ArtifactID(ctx context.Context, r api.RepoRef, runID int64, name string) (int64, error)
		ArtifactByID(ctx context.Context, r api.RepoRef, id int64, maxBytes int64) ([]byte, error)
	}
	pullReader interface {
		PullSummary(ctx context.Context, r api.RepoRef, number int) (api.Pull, error)
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
	shell      shellPage
	reports    *reportCache
}

// New returns the UI mounted at "/". It serves the web app's build from the binary.
func New(d Deps) (http.Handler, error) {
	sessions, err := session.New(session.Config{CookieKey: d.CookieKey, BaseURL: d.BaseURL}, d.OAuth, d.GitHub, d.GitHub, d.Now)
	if err != nil {
		return nil, err
	}
	return newHandler(d, github{files: d.GitHub, checks: d.GitHub, actions: d.GitHub, pulls: d.GitHub, users: d.GitHub}, sessions, builtWeb()), nil
}

// newHandler mounts every route. web is the build of the web app; tests hand in a small one.
func newHandler(d Deps, gh github, sessions session.Sessions, web fs.FS) http.Handler {
	s := &server{
		gh:         gh,
		sessions:   sessions,
		account:    d.Account,
		consents:   d.Consents,
		onboarding: d.Onboarding,
		assist:     d.Assist,
		proposals:  d.Proposals,
		now:        d.Now,
		shell:      newShell(web),
		reports:    newReportCache(reportCacheBytes),
	}

	mux := http.NewServeMux()
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
	index := func(w http.ResponseWriter, _ *http.Request) { s.shell.serve(w, http.StatusOK, "") }
	mux.HandleFunc("GET /assets/", assets(web))
	mux.HandleFunc("GET "+licensesRoute, licenses(web))
	mux.HandleFunc("GET /{$}", index)
	page := func(w http.ResponseWriter, r *http.Request, _ scope) { index(w, r) }
	pullPage := func(w http.ResponseWriter, r *http.Request, _ scope) {
		if _, ok := pullNumber(r); !ok {
			s.fail(w, r, api.ErrNotFound)
			return
		}
		index(w, r)
	}
	mux.Handle("GET /r/{o}/{n}", s.repo(permRead, page))
	mux.Handle("GET /r/{o}/{n}/graph", s.repo(permRead, page))
	mux.Handle("GET /r/{o}/{n}/pull/{number}", s.repo(permRead, pullPage))
	mux.Handle("GET /r/{o}/{n}/pull/{number}/graph", s.repo(permRead, pullPage))
	mux.Handle("GET /r/{o}/{n}/data-sharing", s.repo(permAdmin, page))
	mux.Handle("GET /r/{o}/{n}/policy", s.repo(permRead, page))
	mux.Handle("GET /r/{o}/{n}/consent", s.repo(permAdmin, consentMoved))

	mux.Handle("GET /api/session", s.apiSignedIn(apiSession))
	mux.Handle("GET /api/repos", s.apiSignedIn(s.apiRepos))
	mux.Handle("GET /api/repos/{o}/{n}/overview", s.apiRepo(permRead, s.apiStatus))
	mux.Handle("GET /api/repos/{o}/{n}/findings", s.apiRepo(permRead, s.apiFindings))
	mux.Handle("GET /api/repos/{o}/{n}/graph", s.apiRepo(permRead, s.apiGraph))
	mux.Handle("GET /api/repos/{o}/{n}/pulls/{number}", s.apiRepo(permRead, s.apiPull))
	mux.Handle("GET /api/repos/{o}/{n}/pulls/{number}/graph", s.apiRepo(permRead, s.apiPullGraph))
	mux.Handle("POST /api/repos/{o}/{n}/onboarding", s.apiRepo(permAdmin, s.apiStartOnboarding))
	mux.Handle("POST /api/repos/{o}/{n}/discover", s.apiRepo(permAdmin, s.apiDiscover))
	mux.Handle("GET /api/repos/{o}/{n}/data-sharing", s.apiRepo(permAdmin, s.apiDataSharing))
	mux.Handle("POST /api/repos/{o}/{n}/data-sharing", s.apiRepo(permAdmin, s.apiShareData))
	mux.Handle("GET /api/repos/{o}/{n}/policy", s.apiRepo(permRead, s.apiPolicy))
	mux.Handle("POST /api/repos/{o}/{n}/policy", s.apiRepo(permWrite, s.apiSavePolicy))
	mux.Handle("POST /api/repos/{o}/{n}/policy/check", s.apiRepo(permRead, s.apiCheckPolicy))
	mux.Handle("POST /api/repos/{o}/{n}/policy/ready", s.apiRepo(permWrite, s.apiReady))
	mux.Handle("POST /api/repos/{o}/{n}/assist", s.apiRepo(permWrite, s.apiAssist))
	mux.HandleFunc("/api/", func(w http.ResponseWriter, r *http.Request) {
		s.failJSON(w, r, api.ErrNotFound)
	})
	// Any other path is the app's "Not found" page, under the shell's CSP.
	mux.HandleFunc("/", func(w http.ResponseWriter, r *http.Request) {
		s.fail(w, r, api.ErrNotFound)
	})

	return chain(mux)
}

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
	"github.com/alexei-led/archfit-app/internal/gate"
	"github.com/alexei-led/archfit-app/internal/github/api"
	"github.com/alexei-led/archfit-app/internal/github/auth"
	"github.com/alexei-led/archfit-app/internal/wire/policy"
)

type Deps struct {
	OAuth         auth.OAuth // ui builds its own session child from OAuth, CookieKey and BaseURL
	CookieKey     []byte
	BaseURL       string
	GitHub        *api.Client
	Account       account.Account
	Consents      account.Consents
	Onboarding    onboarding.Onboarding
	Assist        assist.Assist
	Proposals     proposal.Proposals
	Admissions    *gate.Admissions   // which installations the App serves; the others see the Waiting page
	Approvals     gate.Approvals     // owner approvals of pull request heads: read, and recorded by the Approve button
	DecisionQueue gate.DecisionQueue // decision requests of unattended agents that wait for an owner: read only
	Operators     []int64            // GitHub user IDs that approve installations on /admin
	Now           func() time.Time
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
	installLister interface {
		ListInstallations(ctx context.Context) ([]api.Installation, error)
		ListRepos(ctx context.Context, installationID int64) ([]api.Repo, error)
	}
)

type github struct {
	files    fileReader
	checks   checkReader
	actions  workflows
	pulls    pullReader
	users    repoLister
	installs installLister
}

type server struct {
	gh            github
	sessions      session.Sessions
	account       account.Account
	consents      account.Consents
	onboarding    onboarding.Onboarding
	assist        assist.Assist
	proposals     proposal.Proposals
	admissions    *gate.Admissions
	approvals     gate.Approvals
	decisionQueue gate.DecisionQueue
	operators     []int64
	now           func() time.Time
	shell         shellPage
	reports       *reportCache
	hints         *hints
}

// New returns the UI mounted at "/". It serves the web app's build from the binary.
func New(d Deps) (http.Handler, error) {
	sessions, err := session.New(session.Config{CookieKey: d.CookieKey, BaseURL: d.BaseURL}, d.OAuth, d.GitHub, d.GitHub, d.Now)
	if err != nil {
		return nil, err
	}
	gh := github{files: d.GitHub, checks: d.GitHub, actions: d.GitHub, pulls: d.GitHub, users: d.GitHub, installs: d.GitHub}
	return newHandler(d, gh, sessions, builtWeb()), nil
}

// newHandler mounts every route. web is the build of the web app; tests hand in a small one.
func newHandler(d Deps, gh github, sessions session.Sessions, web fs.FS) http.Handler {
	s := &server{
		gh:            gh,
		sessions:      sessions,
		account:       d.Account,
		consents:      d.Consents,
		onboarding:    d.Onboarding,
		assist:        d.Assist,
		proposals:     d.Proposals,
		admissions:    d.Admissions,
		approvals:     d.Approvals,
		decisionQueue: d.DecisionQueue,
		operators:     d.Operators,
		now:           d.Now,
		shell:         newShell(web),
		reports:       newReportCache(reportCacheBytes),
		hints:         newHints(),
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
	// Pages of the web app: the shell, which the app fills from /api. A repository page asks only for a session, no
	// GitHub call: the shell is the same bytes for every repository, one that exists or not, visible or not (the nonce
	// aside), and carries none of its data. Every /api/repos route authorizes per request, and the app shows its "Not
	// found." the same way for a repository that does not exist and one the user cannot see.
	index := func(w http.ResponseWriter, _ *http.Request) { s.shell.serve(w, http.StatusOK, "") }
	mux.HandleFunc("GET /assets/", assets(web))
	mux.HandleFunc("GET "+licensesRoute, licenses(web))
	mux.HandleFunc("GET /{$}", index)
	page := s.signedIn(func(w http.ResponseWriter, r *http.Request, _ session.User) { index(w, r) })
	pullPage := s.signedIn(func(w http.ResponseWriter, r *http.Request, _ session.User) {
		if _, ok := pullNumber(r); !ok {
			s.fail(w, r, api.ErrNotFound)
			return
		}
		index(w, r)
	})
	mux.Handle("GET /r/{o}/{n}", page)
	mux.Handle("GET /r/{o}/{n}/graph", page)
	mux.Handle("GET /r/{o}/{n}/pull/{number}", pullPage)
	mux.Handle("GET /r/{o}/{n}/pull/{number}/graph", pullPage)
	mux.Handle("GET /r/{o}/{n}/data-sharing", page)
	mux.Handle("GET /r/{o}/{n}/policy", page)
	mux.Handle("GET /r/{o}/{n}/decisions", page)
	mux.Handle("GET /r/{o}/{n}/consent", s.signedIn(consentMoved))
	mux.Handle("GET /admin", s.signedIn(func(w http.ResponseWriter, r *http.Request, u session.User) {
		if !s.isOperator(u) {
			s.fail(w, r, api.ErrNotFound)
			return
		}
		index(w, r)
	}))

	mux.Handle("GET /api/session", s.apiSignedIn(s.apiSession))
	mux.Handle("GET /api/admin/installations", s.apiOperator(s.apiAdmin))
	mux.Handle("POST /api/admin/installations/{id}/approve", s.apiOperator(s.apiApprove))
	mux.Handle("POST /api/admin/installations/{id}/revoke", s.apiOperator(s.apiRevoke))
	mux.Handle("GET /api/repos", s.apiSignedIn(s.apiRepos))
	mux.Handle("GET /api/repos/{o}/{n}/overview", s.apiRepo(permRead, s.apiStatus))
	mux.Handle("GET /api/repos/{o}/{n}/findings", s.apiRepo(permRead, s.apiFindings))
	mux.Handle("GET /api/repos/{o}/{n}/decisions", s.apiRepo(permRead, s.apiDecisions))
	mux.Handle("GET /api/repos/{o}/{n}/graph", s.apiRepo(permRead, s.apiGraph))
	mux.Handle("GET /api/repos/{o}/{n}/pulls/{number}", s.apiRepo(permRead, s.apiPull))
	mux.Handle("GET /api/repos/{o}/{n}/pulls/{number}/graph", s.apiRepo(permRead, s.apiPullGraph))
	mux.Handle("POST /api/repos/{o}/{n}/pulls/{number}/approve", s.apiRepo(permWrite, s.apiApprovePull))
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

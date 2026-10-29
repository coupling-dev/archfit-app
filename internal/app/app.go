package app

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"net"
	"net/http"
	"time"

	"github.com/alexei-led/archfit-app/internal/control"
	"github.com/alexei-led/archfit-app/internal/control/assist/anthropic"
	"github.com/alexei-led/archfit-app/internal/gate"
	"github.com/alexei-led/archfit-app/internal/gcptoken"
	"github.com/alexei-led/archfit-app/internal/github/api"
	"github.com/alexei-led/archfit-app/internal/github/auth"
	"github.com/alexei-led/archfit-app/internal/github/oidc"
	"github.com/alexei-led/archfit-app/internal/queue"
	"github.com/alexei-led/archfit-app/internal/queue/cloudtasks"
	"github.com/alexei-led/archfit-app/internal/store"
	"github.com/alexei-led/archfit-app/internal/store/firestore"
)

const (
	readTimeout = 15 * time.Second
	// writeTimeout stays above the longest route deadline (assistTimeout), so a route's own
	// deadline answers before the server cuts the connection.
	writeTimeout  = 310 * time.Second
	idleTimeout   = 60 * time.Second
	shutdownGrace = 10 * time.Second
	clientTimeout = 30 * time.Second

	missingUploadAfter = 60 * time.Minute
	waiverWarnBefore   = 7 * 24 * time.Hour
)

// Run wires every module and serves until ctx is cancelled.
func Run(ctx context.Context, c Config, log *slog.Logger) error {
	hc := &http.Client{Timeout: clientTimeout}
	// The Cloud Tasks API token comes from the metadata server; the source sends nothing until the first enqueue.
	return run(ctx, c, log, hc, gcptoken.New(hc), nil)
}

// run is Run with its test seams: the shared outbound client, the Google access token source of the
// cloudtasks driver, and ready, which is called with the bound address once the listener accepts
// connections. Startup contacts no remote service.
func run(ctx context.Context, c Config, log *slog.Logger, hc *http.Client, google cloudtasks.TokenSource, ready func(net.Addr)) error {
	if err := c.validate(); err != nil {
		return err
	}
	sec, err := loadSecrets(c)
	if err != nil {
		return err
	}
	if log == nil {
		log = slog.Default()
	}
	// Every line names the role and the revision, so one request can be followed from intake to the worker.
	log = log.With("role", c.Role, "revision", c.Revision)
	// Modules bind slog.Default() when they are constructed, so install the logger first.
	slog.SetDefault(log)

	creds := auth.Credentials{AppID: c.AppID, PrivateKeyPEM: sec.privateKeyPEM}
	if c.Role != roleWorker {
		creds.OAuthClientID, creds.OAuthClientSecret = c.OAuthClientID, string(sec.oauthSecret)
	}
	tokens, oauth, err := auth.New(creds, hc)
	if err != nil {
		return fmt.Errorf("app: GitHub App credentials: %w", err)
	}
	gh := api.New(tokens, hc)
	verifier := oidc.New(hc)
	st, sandbox, provider, err := platform(c, sec.anthropicKey, hc)
	if err != nil {
		return err
	}
	q, err := newQueue(c, google, hc)
	if err != nil {
		return err
	}

	admissions := gate.NewAdmissions(st, c.ApprovedAccounts)
	g := gate.New(gate.Deps{
		GitHub:     gh,
		OIDC:       verifier,
		Store:      st,
		Admissions: admissions,
		Audience:   c.BaseURL,
		Sweep:      gate.Config{MissingUploadAfter: missingUploadAfter, WaiverWarnBefore: waiverWarnBefore},
		Now:        time.Now,
	})
	var ctl *control.Control
	if c.Role == roleWorker {
		ctl, err = control.NewWorker(control.WorkerDeps{GitHub: gh, Store: st, Admissions: admissions, Now: time.Now})
	} else {
		ctl, err = control.New(control.Deps{
			GitHub:     gh,
			Tokens:     tokens,
			OAuth:      oauth,
			Store:      st,
			Receiver:   gate.NewReceiver(gh, verifier, c.BaseURL, admissions),
			Admissions: admissions,
			Approvals:  g,
			Decisions:  g,
			Operators:  c.Operators,
			Sandbox:    sandbox,
			Provider:   provider,
			CookieKey:  sec.cookieKey,
			BaseURL:    c.BaseURL,
			Now:        time.Now,
		})
	}
	if err != nil {
		return fmt.Errorf("app: control: %w", err)
	}

	return serve(ctx, c.Addr, newHandler(handlerDeps{
		role:           c.Role,
		gate:           g,
		control:        ctl,
		queue:          q,
		webhookSecrets: sec.webhookSecrets(),
		operatorToken:  sec.operatorToken,
		workerURL:      c.WorkerURL,
		invoker:        c.Invoker,
		traceProject:   c.TraceProject,
		// Every role uses the App key and the store, so every role probes both.
		probes: []probe{githubProbe(gh.CheckApp), storeProbe(st)},
		now:    time.Now,
		log:    log,
	}), log, ready)
}

// newQueue selects the queue driver of intake. Nil means the inline driver; the worker never enqueues.
func newQueue(c Config, google cloudtasks.TokenSource, hc *http.Client) (queue.Queue, error) {
	if c.QueueDriver != queueCloudTasks || c.Role == roleWorker {
		return nil, nil
	}
	if google == nil {
		return nil, fmt.Errorf("app: %s=%s needs a Google token source", envQueue, queueCloudTasks)
	}
	q, err := cloudtasks.New(cloudtasks.Config{
		Queue: c.TasksQueue, URL: c.WorkerURL + taskRoute, Audience: c.WorkerURL, Invoker: c.Invoker,
	}, google, hc)
	if err != nil {
		return nil, fmt.Errorf("app: %s: %w", envTasksQueue, err)
	}
	return q, nil
}

// The driver tables are the single place a platform driver is registered; validate checks names against them.
var (
	storeDrivers = map[string]func(Config, *http.Client) (store.Store, error){
		storeMemory:    func(Config, *http.Client) (store.Store, error) { return store.NewMemory(), nil },
		storeFirestore: newFirestore,
	}
	sandboxDrivers = map[string]func() control.Sandbox{sandboxNone: func() control.Sandbox { return nil }}
)

// newFirestore reads the access token of the Cloud Run service account from the metadata server, except
// against the emulator, which takes no credentials.
func newFirestore(c Config, hc *http.Client) (store.Store, error) {
	var tokens firestore.TokenSource
	if c.FirestoreEmulator == "" {
		tokens = gcptoken.New(hc)
	} else {
		slog.Warn("store: Firestore emulator selected: plain HTTP, no credentials", "host", c.FirestoreEmulator)
	}
	st, err := firestore.New(firestore.Config{
		ProjectID:    c.FirestoreProject,
		DatabaseID:   c.FirestoreDatabase,
		EmulatorHost: c.FirestoreEmulator,
	}, tokens, hc)
	if err != nil {
		return nil, fmt.Errorf("app: Firestore store: %w", err)
	}
	return st, nil
}

// platform selects the implementation behind each platform seam.
// A nil Sandbox or Provider is valid: the managed feature then reports "not available".
func platform(c Config, anthropicKey []byte, hc *http.Client) (store.Store, control.Sandbox, control.Provider, error) {
	newStore, newSandbox := storeDrivers[c.StoreDriver], sandboxDrivers[c.SandboxDriver]
	if newStore == nil || newSandbox == nil {
		return nil, nil, nil, c.validate()
	}
	var provider control.Provider
	if len(anthropicKey) > 0 {
		p, err := anthropic.New(anthropic.Config{APIKey: string(anthropicKey)}, hc)
		if err != nil {
			return nil, nil, nil, fmt.Errorf("app: Anthropic provider: %w", err)
		}
		provider = p
	}
	st, err := newStore(c, hc)
	if err != nil {
		return nil, nil, nil, err
	}
	return st, newSandbox(), provider, nil
}

// serve runs one http.Server until ctx is cancelled, then lets in-flight requests finish within shutdownGrace.
func serve(ctx context.Context, addr string, h http.Handler, log *slog.Logger, ready func(net.Addr)) error {
	ln, err := (&net.ListenConfig{}).Listen(ctx, "tcp", addr)
	if err != nil {
		return fmt.Errorf("app: listen: %w", err)
	}
	srv := &http.Server{
		Handler:      h,
		ReadTimeout:  readTimeout,
		WriteTimeout: writeTimeout,
		IdleTimeout:  idleTimeout,
		ErrorLog:     slog.NewLogLogger(log.Handler(), slog.LevelError),
	}
	errc := make(chan error, 1)
	go func() { errc <- srv.Serve(ln) }()
	log.Info("listening", "addr", ln.Addr().String())
	if ready != nil {
		ready(ln.Addr())
	}

	select {
	case err := <-errc:
		return fmt.Errorf("app: serve: %w", err)
	case <-ctx.Done():
	}
	shutdownCtx, cancel := context.WithTimeout(context.WithoutCancel(ctx), shutdownGrace)
	defer cancel()
	err = srv.Shutdown(shutdownCtx)
	if serveErr := <-errc; err == nil && !errors.Is(serveErr, http.ErrServerClosed) {
		err = serveErr
	}
	if err != nil {
		return fmt.Errorf("app: shutdown: %w", err)
	}
	log.Info("stopped")
	return nil
}

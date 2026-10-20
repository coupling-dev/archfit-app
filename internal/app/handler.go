package app

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"crypto/subtle"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"runtime/debug"
	"strings"
	"time"

	"github.com/alexei-led/archfit-app/internal/gate"
	"github.com/alexei-led/archfit-app/internal/github/api"
	"github.com/alexei-led/archfit-app/internal/github/webhook"
	"github.com/alexei-led/archfit-app/internal/queue"
)

const (
	maxWebhookBody = 25 << 20 // GitHub's own maximum delivery size
	maxGrantBody   = 4 << 10

	uploadTimeout  = 55 * time.Second // target: feedback within 60 seconds
	controlTimeout = 55 * time.Second
	// assistTimeout covers control's measured worst case of the managed assist route, about 285 s
	// (the provider call alone may take 150 s). A shorter deadline cancels the provider call and
	// the full budget reservation is settled for nothing.
	assistTimeout = 300 * time.Second

	// assistRoute restates one row of control/ui's route table: UIHandler is one opaque handler
	// and only this route needs the long deadline.
	assistRoute = "POST /api/repos/{owner}/{name}/assist"

	headerRequestID = "X-Request-Id"
	bearerPrefix    = "Bearer "

	// enqueueTimeout bounds one task creation. Two of them fit inside GitHub's 10-second delivery timeout.
	enqueueTimeout = 4 * time.Second

	// taskRoute is the worker route of queued webhooks. The webhook queue pins every task to it (uriOverride).
	taskRoute = "/internal/webhooks"
	// headerHalf names the half of a delivery that a task carries: halfGate or halfControl. It is not signed;
	// it only chooses between two handlers that both verify the signed body.
	headerHalf  = "X-Archfit-Half"
	halfGate    = "gate"
	halfControl = "control"

	// headerOperator carries the operator token on the worker, where Authorization carries the invoker's ID token.
	headerOperator = "X-Archfit-Operator"
	// headerServerlessAuth is the other header Cloud Run reads an invoker ID token from.
	headerServerlessAuth = "X-Serverless-Authorization"

	maxLoggedIDLen = 64 // the delivery ID is logged before the signature is checked
)

// gateFacade is the part of *gate.Gate the HTTP assembly uses.
type gateFacade interface {
	UploadHandler() http.Handler
	HandleEvent(ctx context.Context, e webhook.Event) error
	Sweep(ctx context.Context) (gate.Summary, error)
}

// controlFacade is the part of *control.Control the HTTP assembly uses.
type controlFacade interface {
	UIHandler() http.Handler
	DiscoveryHandler() http.Handler
	BaselineHandler() http.Handler
	ReanchorHandler() http.Handler
	HandleEvent(ctx context.Context, e webhook.Event) error
	Sweep(ctx context.Context) error
	Grant(ctx context.Context, installationID int64, until time.Time) error
}

type handlerDeps struct {
	role           string
	gate           gateFacade
	control        controlFacade
	queue          queue.Queue // required for public intake; nil uses the local all-role driver
	webhookSecrets [][]byte    // the current secret first, then the previous one during a rotation
	operatorToken  []byte      // nil in the public role
	workerURL      string      // the audience of invoker ID tokens; worker role only
	invoker        string      // the e-mail of invoker ID tokens; worker role only
	traceProject   string      // the project of request traces in log lines; empty logs no trace
	probes         []probe     // the dependencies of the role that GET /ready checks
	now            func() time.Time
	log            *slog.Logger
}

// newHandler mounts the route table of the role. Every control handler runs behind its own timeout, and the
// outer middleware turns any panic into a 500 for that request only.
func newHandler(d handlerDeps) http.Handler {
	s := &server{handlerDeps: d, operatorSum: sha256.Sum256(d.operatorToken),
		readiness: &readiness{probes: d.probes, timeout: readyTimeout, now: d.now, log: d.log}}
	s.inline = queue.NewInline(s.runInline)
	mux := http.NewServeMux()
	// Not /healthz: Cloud Run reserves some paths that end in "z", and its frontend answers /healthz itself.
	mux.HandleFunc("GET /health", health)
	// Readiness has no credential on any role: a deploy check through the service URL sends none.
	mux.HandleFunc(readyRoute, s.ready)
	if d.role != roleWorker {
		mux.Handle("POST /v1/reports", http.TimeoutHandler(d.gate.UploadHandler(), uploadTimeout, ""))
		mux.Handle("POST /v1/discoveries", http.TimeoutHandler(d.control.DiscoveryHandler(), controlTimeout, ""))
		mux.Handle("POST /v1/baselines", http.TimeoutHandler(d.control.BaselineHandler(), controlTimeout, ""))
		mux.Handle("POST /v1/reanchors", http.TimeoutHandler(d.control.ReanchorHandler(), controlTimeout, ""))
		mux.HandleFunc("POST /webhooks/github", s.webhook)
		ui := d.control.UIHandler()
		mux.Handle(assistRoute, http.TimeoutHandler(ui, assistTimeout, ""))
		mux.Handle("/", http.TimeoutHandler(ui, controlTimeout, ""))
		// Only the routes mounted below serve /internal/*; the UI never sees the prefix.
		mux.Handle("/internal/", http.NotFoundHandler())
	}
	switch d.role {
	case roleAll:
		mux.Handle("POST /internal/sweep", s.operator("Authorization", s.sweep))
		mux.Handle("POST /internal/grants", s.operator("Authorization", s.grant))
	case roleWorker:
		// Each route sits behind the invoker identity; grants also need the operator token.
		mux.Handle("POST "+taskRoute, s.invokerOnly(http.HandlerFunc(s.task)))
		mux.Handle("POST /internal/sweep", s.invokerOnly(http.HandlerFunc(s.sweep)))
		mux.Handle("POST /internal/grants", s.invokerOnly(s.operator(headerOperator, s.grant)))
	}
	return s.logged(mux)
}

type server struct {
	handlerDeps
	operatorSum [sha256.Size]byte
	inline      queue.Queue // local all-role driver; never a public-service fallback
	readiness   *readiness
}

// requestInfo collects the log fields only a route handler knows.
type requestInfo struct {
	deliveryID     string
	event          string
	installationID int64
	size           int64
	outcome        string
}

type requestInfoKey struct{}

func info(r *http.Request) *requestInfo {
	if ri, ok := r.Context().Value(requestInfoKey{}).(*requestInfo); ok {
		return ri
	}
	return &requestInfo{}
}

type statusWriter struct {
	http.ResponseWriter
	status int
}

func (w *statusWriter) WriteHeader(status int) {
	if w.status == 0 {
		w.status = status
	}
	w.ResponseWriter.WriteHeader(status)
}

func (w *statusWriter) Write(b []byte) (int, error) {
	if w.status == 0 {
		w.status = http.StatusOK
	}
	return w.ResponseWriter.Write(b)
}

func (w *statusWriter) Unwrap() http.ResponseWriter { return w.ResponseWriter }

// logged writes one line per request: IDs, route pattern, status, timing, outcome.
// It never logs the URL path, headers or body.
func (s *server) logged(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		start := time.Now()
		id := make([]byte, 8)
		_, _ = rand.Read(id)
		requestID := hex.EncodeToString(id)
		w.Header().Set(headerRequestID, requestID)

		ri := &requestInfo{}
		sw := &statusWriter{ResponseWriter: w}
		ctx := context.WithValue(r.Context(), requestInfoKey{}, ri)
		if t, ok := requestTrace(r.Header, s.traceProject); ok {
			ctx = context.WithValue(ctx, traceKey{}, t)
		}
		r = r.WithContext(ctx)
		defer func() {
			if v := recover(); v != nil {
				ri.outcome = "panic"
				s.log.ErrorContext(r.Context(), "handler panicked", "request_id", requestID, "panic", fmt.Sprint(v), "stack", string(debug.Stack()))
				if sw.status == 0 {
					http.Error(sw, "internal error", http.StatusInternalServerError)
				}
			}
			attrs := []any{
				"request_id", requestID,
				"method", r.Method,
				"route", r.Pattern,
				"status", sw.status,
				"duration_ms", time.Since(start).Milliseconds(),
			}
			if ri.deliveryID != "" {
				attrs = append(attrs, "delivery_id", ri.deliveryID)
			}
			if ri.event != "" {
				attrs = append(attrs, "event", ri.event)
			}
			if ri.installationID != 0 {
				attrs = append(attrs, "installation_id", ri.installationID)
			}
			if ri.size != 0 {
				attrs = append(attrs, "size", ri.size)
			}
			if ri.outcome != "" {
				attrs = append(attrs, "outcome", ri.outcome)
			}
			s.log.InfoContext(r.Context(), "request", attrs...)
		}()
		next.ServeHTTP(sw, r)
	})
}

func health(w http.ResponseWriter, _ *http.Request) {
	w.Header().Set("Content-Type", "application/json")
	_, _ = io.WriteString(w, `{"status":"ok"}`)
}

// isolate turns a panic into an error so one half of a fan-out never stops the other.
func isolate(f func() error) (err error) {
	defer func() {
		if v := recover(); v != nil {
			err = fmt.Errorf("panic: %v", v)
		}
	}()
	return f()
}

// webhook verifies a delivery and attempts both tasks, gate first. Public intake answers 202 only when both
// are accepted, or the event is ignored. Enqueue failure answers 503 without an inline fallback. Only the local
// all role uses the inline driver. Queue retries and sweep reconciliation handle recovery; GitHub does not
// automatically redeliver failed webhooks.
func (s *server) webhook(w http.ResponseWriter, r *http.Request) {
	ri := info(r)
	ri.deliveryID = loggedID(r.Header.Get(webhook.HeaderDelivery))

	body, err := io.ReadAll(http.MaxBytesReader(w, r.Body, maxWebhookBody))
	if tooLarge := (*http.MaxBytesError)(nil); errors.As(err, &tooLarge) {
		ri.size, ri.outcome = r.ContentLength, "body_too_large"
		http.Error(w, "body too large", http.StatusRequestEntityTooLarge)
		return
	}
	if err != nil {
		ri.outcome = "body_unreadable"
		http.Error(w, "bad request", http.StatusBadRequest)
		return
	}
	if err := webhook.VerifyAny(s.webhookSecrets, r.Header.Get(webhook.HeaderSignature), body); err != nil {
		ri.outcome = "bad_signature"
		http.Error(w, "unauthorized", http.StatusUnauthorized)
		return
	}
	e, err := webhook.Parse(r.Header.Get(webhook.HeaderEvent), r.Header.Get(webhook.HeaderDelivery), body)
	if err != nil {
		ri.outcome = "unparsable"
		http.Error(w, "bad request", http.StatusBadRequest)
		return
	}
	ri.event, ri.installationID, ri.outcome = string(e.Kind), e.InstallationID, "accepted"

	if e.Kind != webhook.Ignored {
		// GitHub drops the connection after 10 seconds; queueing and inline work keep their own deadlines instead.
		ctx := context.WithoutCancel(r.Context())
		failed := false
		for _, half := range []string{halfGate, halfControl} {
			t := deliveryTask(r.Header, half, body)
			if err := s.enqueue(ctx, t); err != nil {
				failed = true
				ri.outcome = outcomeEnqueueFailed
				s.log.ErrorContext(ctx, "webhook enqueue failed", "delivery_id", ri.deliveryID, "handler", half, "error", err.Error())
			}
		}
		if failed {
			http.Error(w, "webhook enqueue failed", http.StatusServiceUnavailable)
			return
		}
	}
	w.WriteHeader(http.StatusAccepted)
}

// enqueue hands t to the queue. The inline driver gets no enqueue deadline: it does the work itself, under the
// handlers' own deadlines.
func (s *server) enqueue(ctx context.Context, t queue.Task) error {
	if s.queue == nil {
		if s.role != roleAll {
			return errors.New("webhook queue is not configured")
		}
		return s.inline.Enqueue(ctx, t)
	}
	ctx, cancel := context.WithTimeout(ctx, enqueueTimeout)
	defer cancel()
	return s.queue.Enqueue(ctx, t)
}

// deliveryTask is one half of a delivery: GitHub's own headers and body, named after the delivery ID.
// No App-defined message is involved, so the worker verifies the signature exactly as intake did.
func deliveryTask(in http.Header, half string, body []byte) queue.Task {
	h := http.Header{}
	for _, k := range []string{webhook.HeaderEvent, webhook.HeaderDelivery, webhook.HeaderSignature} {
		if v := in.Get(k); v != "" {
			h.Set(k, v)
		}
	}
	h.Set("Content-Type", "application/json")
	h.Set(headerHalf, half)
	return queue.Task{Name: in.Get(webhook.HeaderDelivery) + "_" + half, Header: h, Body: body}
}

func loggedID(id string) string {
	if len(id) > maxLoggedIDLen {
		return id[:maxLoggedIDLen]
	}
	return id
}

// Outcomes of one processed task.
const (
	outcomeDone      = "done"
	outcomeIgnored   = "ignored"
	outcomePermanent = "permanent_error"
	outcomeTransient = "transient_error"

	outcomeEnqueueFailed = "enqueue_failed" // intake: at least one task was not accepted
)

// process verifies and handles one half of a delivery. It returns an error only when a repeat may succeed:
// the queue then retries. Work that can never succeed (a bad signature, an unparsable body, an unknown half,
// a permanent GitHub answer) is logged and returns nil, so it is never retried.
func (s *server) process(ctx context.Context, t queue.Task) (webhook.Event, string, error) {
	deliveryID := loggedID(t.Header.Get(webhook.HeaderDelivery))
	half := t.Header.Get(headerHalf)
	permanent := func(outcome string) (webhook.Event, string, error) {
		// The half is logged by the switch below only; an unknown value is never echoed.
		s.log.ErrorContext(ctx, "webhook task dropped", "delivery_id", deliveryID, "outcome", outcome)
		return webhook.Event{}, outcome, nil
	}
	if err := webhook.VerifyAny(s.webhookSecrets, t.Header.Get(webhook.HeaderSignature), t.Body); err != nil {
		return permanent("bad_signature")
	}
	e, err := webhook.Parse(t.Header.Get(webhook.HeaderEvent), t.Header.Get(webhook.HeaderDelivery), t.Body)
	if err != nil {
		return permanent("unparsable")
	}
	var handle func(context.Context, webhook.Event) error
	var timeout time.Duration
	switch half {
	case halfGate:
		handle, timeout = s.gate.HandleEvent, uploadTimeout
	case halfControl:
		handle, timeout = s.control.HandleEvent, controlTimeout
	default:
		return permanent("unknown_half")
	}
	if e.Kind == webhook.Ignored {
		return e, outcomeIgnored, nil
	}
	err = isolate(func() error {
		ctx, cancel := context.WithTimeout(ctx, timeout)
		defer cancel()
		return handle(ctx, e)
	})
	switch {
	case err == nil:
		return e, outcomeDone, nil
	case api.Permanent(err):
		s.log.ErrorContext(ctx, "webhook handler failed", "delivery_id", deliveryID, "handler", half, "retry", false, "error", err.Error())
		return e, outcomePermanent, nil
	default:
		s.log.ErrorContext(ctx, "webhook handler failed", "delivery_id", deliveryID, "handler", half, "retry", true, "error", err.Error())
		return e, outcomeTransient, err
	}
}

// runInline processes local all-role work in the request, which logs the outcome.
func (s *server) runInline(ctx context.Context, t queue.Task) {
	if _, outcome, _ := s.process(ctx, t); outcome != outcomeDone && outcome != outcomeIgnored {
		if ri, ok := ctx.Value(requestInfoKey{}).(*requestInfo); ok {
			ri.outcome = "handler_error"
		}
	}
}

// task is the worker route of queued webhooks. It answers 5xx only for a transient error, so the queue
// retries exactly the work that a repeat may finish; every other answer is 204.
func (s *server) task(w http.ResponseWriter, r *http.Request) {
	ri := info(r)
	ri.deliveryID = loggedID(r.Header.Get(webhook.HeaderDelivery))
	body, err := io.ReadAll(http.MaxBytesReader(w, r.Body, maxWebhookBody))
	if tooLarge := (*http.MaxBytesError)(nil); errors.As(err, &tooLarge) {
		ri.size, ri.outcome = r.ContentLength, "body_too_large"
		s.log.ErrorContext(r.Context(), "webhook task dropped", "delivery_id", ri.deliveryID, "outcome", ri.outcome)
		w.WriteHeader(http.StatusNoContent)
		return
	}
	if err != nil {
		ri.outcome = "body_unreadable"
		http.Error(w, "retry", http.StatusServiceUnavailable)
		return
	}
	e, outcome, err := s.process(r.Context(), queue.Task{Header: r.Header, Body: body})
	ri.event, ri.installationID, ri.outcome = string(e.Kind), e.InstallationID, outcome
	if err != nil {
		http.Error(w, "retry", http.StatusServiceUnavailable)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

// operator guards an operator route with the operator bearer token in header. Both sides are hashed first,
// so the constant-time comparison does not reveal the token length either.
func (s *server) operator(header string, next http.HandlerFunc) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		token, ok := strings.CutPrefix(r.Header.Get(header), bearerPrefix)
		sum := sha256.Sum256([]byte(token))
		if !ok || len(s.operatorToken) == 0 || subtle.ConstantTimeCompare(sum[:], s.operatorSum[:]) != 1 {
			info(r).outcome = "unauthorized"
			http.Error(w, "unauthorized", http.StatusUnauthorized)
			return
		}
		next(w, r)
	})
}

// sweep runs control.Sweep then gate.Sweep. The gate half runs and answers whatever control does.
func (s *server) sweep(w http.ResponseWriter, r *http.Request) {
	ri := info(r)
	ri.outcome = "swept"
	if err := isolate(func() error { return s.control.Sweep(r.Context()) }); err != nil {
		ri.outcome = "control_sweep_error"
		s.log.ErrorContext(r.Context(), "control sweep failed", "error", err.Error())
	}
	var sum gate.Summary
	err := isolate(func() (err error) {
		sum, err = s.gate.Sweep(r.Context())
		return err
	})
	status := http.StatusOK
	if sum.RateLimited && ri.outcome == "swept" {
		// Still 200: a retry before the limit resets cannot help, and the next hourly pass continues the work.
		// The outcome makes the stopped pass countable.
		ri.outcome = "sweep_rate_limited"
	}
	if err != nil {
		ri.outcome = "gate_sweep_error"
		s.log.ErrorContext(r.Context(), "gate sweep failed", "error", err.Error())
		status = http.StatusInternalServerError
	}
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(sum)
}

// grant is the single place an installation ID arrives in a request body; the operator token authorizes it.
func (s *server) grant(w http.ResponseWriter, r *http.Request) {
	ri := info(r)
	var req struct {
		InstallationID int64      `json:"installation_id"`
		Until          *time.Time `json:"until"` // null or omitted: clear the grant
	}
	dec := json.NewDecoder(http.MaxBytesReader(w, r.Body, maxGrantBody))
	dec.DisallowUnknownFields()
	if err := dec.Decode(&req); err != nil || req.InstallationID <= 0 || (req.Until != nil && !req.Until.After(s.now())) {
		ri.outcome = "invalid_grant"
		http.Error(w, "bad request", http.StatusBadRequest)
		return
	}
	ri.installationID = req.InstallationID
	var until time.Time // the zero time clears the grant
	if req.Until != nil {
		until = *req.Until
	}
	if err := s.control.Grant(r.Context(), req.InstallationID, until); err != nil {
		ri.outcome = "grant_error"
		s.log.ErrorContext(r.Context(), "operator grant failed", "installation_id", req.InstallationID, "error", err.Error())
		http.Error(w, "internal error", http.StatusInternalServerError)
		return
	}
	ri.outcome = "granted"
	if req.Until == nil {
		ri.outcome = "grant_cleared"
	}
	w.WriteHeader(http.StatusNoContent)
}

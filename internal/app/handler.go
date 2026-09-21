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
	"github.com/alexei-led/archfit-app/internal/github/webhook"
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
	assistRoute = "POST /r/{owner}/{name}/assist"

	headerRequestID = "X-Request-Id"
	bearerPrefix    = "Bearer "

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
	HandleEvent(ctx context.Context, e webhook.Event) error
	Sweep(ctx context.Context) error
	Grant(ctx context.Context, installationID int64, until time.Time) error
}

type handlerDeps struct {
	gate          gateFacade
	control       controlFacade
	webhookSecret []byte
	operatorToken []byte
	now           func() time.Time
	log           *slog.Logger
}

// newHandler mounts the route table. Every control handler runs behind its own timeout, and the
// outer middleware turns any panic into a 500 for that request only.
func newHandler(d handlerDeps) http.Handler {
	s := &server{handlerDeps: d, operatorSum: sha256.Sum256(d.operatorToken)}
	mux := http.NewServeMux()
	mux.HandleFunc("GET /healthz", healthz)
	mux.Handle("POST /v1/reports", http.TimeoutHandler(d.gate.UploadHandler(), uploadTimeout, ""))
	mux.Handle("POST /v1/discoveries", http.TimeoutHandler(d.control.DiscoveryHandler(), controlTimeout, ""))
	mux.HandleFunc("POST /webhooks/github", s.webhook)
	mux.Handle("POST /internal/sweep", s.operator(s.sweep))
	mux.Handle("POST /internal/grants", s.operator(s.grant))
	ui := d.control.UIHandler()
	mux.Handle(assistRoute, http.TimeoutHandler(ui, assistTimeout, ""))
	mux.Handle("/", http.TimeoutHandler(ui, controlTimeout, ""))
	return s.logged(mux)
}

type server struct {
	handlerDeps
	operatorSum [sha256.Size]byte
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
		r = r.WithContext(context.WithValue(r.Context(), requestInfoKey{}, ri))
		defer func() {
			if v := recover(); v != nil {
				ri.outcome = "panic"
				s.log.Error("handler panicked", "request_id", requestID, "panic", fmt.Sprint(v), "stack", string(debug.Stack()))
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
			s.log.Info("request", attrs...)
		}()
		next.ServeHTTP(sw, r)
	})
}

func healthz(w http.ResponseWriter, _ *http.Request) {
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

// webhook verifies a delivery and fans it out, gate first. Once the signature is valid the answer
// is 202 whatever the handlers return: GitHub redelivery and the sweep are the recovery path.
func (s *server) webhook(w http.ResponseWriter, r *http.Request) {
	ri := info(r)
	ri.deliveryID = r.Header.Get(webhook.HeaderDelivery)
	if len(ri.deliveryID) > maxLoggedIDLen {
		ri.deliveryID = ri.deliveryID[:maxLoggedIDLen]
	}

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
	if err := webhook.Verify(s.webhookSecret, r.Header.Get(webhook.HeaderSignature), body); err != nil {
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
		// GitHub drops the connection after 10 seconds; the handlers keep their own deadlines instead.
		ctx := context.WithoutCancel(r.Context())
		halves := []struct {
			name    string
			timeout time.Duration
			handle  func(context.Context, webhook.Event) error
		}{
			{"gate", uploadTimeout, s.gate.HandleEvent},
			{"control", controlTimeout, s.control.HandleEvent},
		}
		for _, h := range halves {
			err := isolate(func() error {
				ctx, cancel := context.WithTimeout(ctx, h.timeout)
				defer cancel()
				return h.handle(ctx, e)
			})
			if err != nil {
				ri.outcome = "handler_error"
				s.log.Error("webhook handler failed", "delivery_id", ri.deliveryID, "handler", h.name, "error", err.Error())
			}
		}
	}
	w.WriteHeader(http.StatusAccepted)
}

// operator guards /internal/* with the operator bearer token. Both sides are hashed first, so the
// constant-time comparison does not reveal the token length either.
func (s *server) operator(next http.HandlerFunc) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		token, ok := strings.CutPrefix(r.Header.Get("Authorization"), bearerPrefix)
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
		s.log.Error("control sweep failed", "error", err.Error())
	}
	var sum gate.Summary
	err := isolate(func() (err error) {
		sum, err = s.gate.Sweep(r.Context())
		return err
	})
	status := http.StatusOK
	if err != nil {
		ri.outcome = "gate_sweep_error"
		s.log.Error("gate sweep failed", "error", err.Error())
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
		s.log.Error("operator grant failed", "installation_id", req.InstallationID, "error", err.Error())
		http.Error(w, "internal error", http.StatusInternalServerError)
		return
	}
	ri.outcome = "granted"
	if req.Until == nil {
		ri.outcome = "grant_cleared"
	}
	w.WriteHeader(http.StatusNoContent)
}

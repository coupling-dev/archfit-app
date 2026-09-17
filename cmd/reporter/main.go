// Command reporter is the archfit App's report-ingest service.
//
// It is deliberately the smallest deployable piece of the App: it verifies a
// report's provenance and posts the resulting check and comment, and it depends
// on nothing else. Billing, dashboards, policy composition and LLM features do
// not belong here. The reason is availability: this service sits in the merge
// path of every repository that enables the gate, so a failure in something a
// developer is not waiting for must not be able to take it down.
package main

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"os"
	"os/signal"
	"syscall"
	"time"
)

const (
	// stateSchema is the only report contract this service accepts. It is
	// published by the engine at archfit.state.schema.json.
	stateSchema = "archfit.architecture-state.v1"
	// envelopeSchema is the contract archfit-action sends alongside a report.
	envelopeSchema = "archfit.report-envelope.v1"

	envelopeHeader = "X-Archfit-Envelope"

	defaultAddr     = ":8080"
	readTimeout     = 15 * time.Second
	writeTimeout    = 15 * time.Second
	idleTimeout     = 60 * time.Second
	shutdownTimeout = 10 * time.Second

	// maxReportBytes caps an ingested document. A state report for a large
	// monorepo is measured in megabytes, and an unbounded body is a trivial way
	// to exhaust a service that sits in the merge path.
	maxReportBytes = 32 << 20
)

func main() {
	log := slog.New(slog.NewJSONHandler(os.Stdout, nil))
	if err := run(log); err != nil {
		log.Error("exit", "err", err)
		os.Exit(1)
	}
}

func run(log *slog.Logger) error {
	addr := os.Getenv("ARCHFIT_ADDR")
	if addr == "" {
		addr = defaultAddr
	}

	mux := http.NewServeMux()
	mux.HandleFunc("GET /healthz", handleHealth)
	mux.Handle("POST /v1/reports", &reportHandler{log: log})

	srv := &http.Server{
		Addr:         addr,
		Handler:      mux,
		ReadTimeout:  readTimeout,
		WriteTimeout: writeTimeout,
		IdleTimeout:  idleTimeout,
	}

	ctx, stop := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer stop()

	errs := make(chan error, 1)
	go func() {
		log.Info("reporter listening", "addr", addr)
		if err := srv.ListenAndServe(); err != nil && !errors.Is(err, http.ErrServerClosed) {
			errs <- err
			return
		}
		errs <- nil
	}()

	select {
	case err := <-errs:
		return err
	case <-ctx.Done():
		log.Info("shutting down")
		shutdownCtx, cancel := context.WithTimeout(context.Background(), shutdownTimeout)
		defer cancel()
		return srv.Shutdown(shutdownCtx)
	}
}

func handleHealth(w http.ResponseWriter, _ *http.Request) {
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(map[string]string{"status": "ok"})
}

// envelope is what archfit-action states about a report: which repository and
// which commits it analysed, and the report's digest.
//
// None of it is proof. The action runs code from the pull request, so every
// field here is self-reported. What can be verified is the OIDC token's claims,
// and verifying them is what this service must do before a report may move a
// check — see reportHandler.
type envelope struct {
	SchemaVersion string `json:"schema_version"`
	Repository    string `json:"repository"`
	Analysed      struct {
		PullRequest string `json:"pull_request"`
		HeadSHA     string `json:"head_sha"`
		BaseSHA     string `json:"base_sha"`
		MergeBase   string `json:"merge_base"`
	} `json:"analysed"`
	Report struct {
		Digest  string `json:"digest"`
		Verdict string `json:"verdict"`
	} `json:"report"`
}

type reportHandler struct {
	log *slog.Logger
}

// ServeHTTP validates the request's shape and then refuses it.
//
// The refusal is the honest state of this service, not an oversight: provenance
// verification is not implemented, and a report accepted without it is a report
// whose producer we cannot name. Since the point of the App is that a gate
// decides from evidence whose origin is known, accepting one now would make the
// first thing we ship the one thing the design forbids. The shape checks run
// first anyway so the wire contract is exercised from day one.
func (h *reportHandler) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	raw := r.Header.Get(envelopeHeader)
	if raw == "" {
		writeError(w, http.StatusBadRequest, fmt.Sprintf("missing %s header", envelopeHeader))
		return
	}
	var env envelope
	if err := json.Unmarshal([]byte(raw), &env); err != nil {
		writeError(w, http.StatusBadRequest, "envelope is not valid JSON")
		return
	}
	if env.SchemaVersion != envelopeSchema {
		writeError(w, http.StatusBadRequest,
			fmt.Sprintf("envelope declares %q, this service accepts %q", env.SchemaVersion, envelopeSchema))
		return
	}

	var report struct {
		SchemaVersion string `json:"schema_version"`
		Verdict       string `json:"verdict"`
	}
	if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, maxReportBytes)).Decode(&report); err != nil {
		writeError(w, http.StatusBadRequest, "report body is not valid JSON")
		return
	}
	if report.SchemaVersion != stateSchema {
		writeError(w, http.StatusBadRequest,
			fmt.Sprintf("report declares %q, this service accepts %q", report.SchemaVersion, stateSchema))
		return
	}

	h.log.Info("report refused: provenance verification is not implemented",
		"repository", env.Repository, "pull_request", env.Analysed.PullRequest,
		"head_sha", env.Analysed.HeadSHA, "verdict", report.Verdict)

	writeError(w, http.StatusNotImplemented,
		"provenance verification is not implemented, so no report can be accepted yet; "+
			"run the action without an endpoint to keep the envelope as a build artifact")
}

func writeError(w http.ResponseWriter, status int, message string) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(map[string]string{"error": message})
}

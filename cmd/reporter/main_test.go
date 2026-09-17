package main

import (
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

// TestReportIngestRefusesEveryReport pins the state this service ships in: the
// wire contract is checked, and then the report is refused because its producer
// cannot be verified yet. A test that accepted one would be pinning the
// behaviour the design forbids.
func TestReportIngestRefusesEveryReport(t *testing.T) {
	t.Parallel()
	const validEnvelope = `{"schema_version":"archfit.report-envelope.v1","repository":"o/r"}`
	const validReport = `{"schema_version":"archfit.architecture-state.v1","verdict":"blocked"}`

	tests := []struct {
		name       string
		envelope   string
		body       string
		wantStatus int
		wantIn     string
	}{
		{
			name:       "a well-formed report is refused, not accepted",
			envelope:   validEnvelope,
			body:       validReport,
			wantStatus: http.StatusNotImplemented,
			wantIn:     "provenance verification is not implemented",
		},
		{
			name:       "no envelope",
			body:       validReport,
			wantStatus: http.StatusBadRequest,
			wantIn:     "missing X-Archfit-Envelope",
		},
		{
			name:       "envelope from another contract",
			envelope:   `{"schema_version":"archfit.report-envelope.v99"}`,
			body:       validReport,
			wantStatus: http.StatusBadRequest,
			wantIn:     "this service accepts",
		},
		{
			name:       "report from another contract",
			envelope:   validEnvelope,
			body:       `{"schema_version":"archfit.architecture-state.v9"}`,
			wantStatus: http.StatusBadRequest,
			wantIn:     "this service accepts",
		},
		{
			name:       "unparseable envelope",
			envelope:   `{`,
			body:       validReport,
			wantStatus: http.StatusBadRequest,
			wantIn:     "not valid JSON",
		},
	}
	h := &reportHandler{log: slog.New(slog.NewJSONHandler(io.Discard, nil))}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			req := httptest.NewRequest(http.MethodPost, "/v1/reports", strings.NewReader(tc.body))
			if tc.envelope != "" {
				req.Header.Set(envelopeHeader, tc.envelope)
			}
			rec := httptest.NewRecorder()
			h.ServeHTTP(rec, req)

			if rec.Code != tc.wantStatus {
				t.Errorf("status = %d, want %d (body: %s)", rec.Code, tc.wantStatus, rec.Body.String())
			}
			if !strings.Contains(rec.Body.String(), tc.wantIn) {
				t.Errorf("body = %s, want it to mention %q", rec.Body.String(), tc.wantIn)
			}
		})
	}
}

func TestHealth(t *testing.T) {
	t.Parallel()
	rec := httptest.NewRecorder()
	handleHealth(rec, httptest.NewRequest(http.MethodGet, "/healthz", nil))
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200", rec.Code)
	}
	if !strings.Contains(rec.Body.String(), `"ok"`) {
		t.Errorf("body = %s, want status ok", rec.Body.String())
	}
}

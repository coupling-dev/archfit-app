package app

import (
	"context"
	"encoding/hex"
	"io"
	"log/slog"
	"net/http"
	"strconv"
	"strings"
)

// Field names that Cloud Logging reads from a JSON log line. It reads severity and message only under its own
// names, and links a line to its request trace only through the trace fields.
const (
	logSeverity     = "severity"
	logMessage      = "message"
	logTrace        = "logging.googleapis.com/trace"
	logSpanID       = "logging.googleapis.com/spanId"
	logTraceSampled = "logging.googleapis.com/trace_sampled"

	headerTraceParent   = "traceparent"
	headerCloudTrace    = "X-Cloud-Trace-Context"
	traceIDLen, spanLen = 32, 16
)

// NewLogger returns the process logger: one JSON line per event on w, with the field names Cloud Logging reads.
// A line logged with a request context also carries that request's trace.
func NewLogger(w io.Writer) *slog.Logger {
	return slog.New(traceHandler{slog.NewJSONHandler(w, &slog.HandlerOptions{ReplaceAttr: cloudFields})})
}

// cloudFields renames the level and message keys and writes the level as a Cloud Logging severity.
func cloudFields(groups []string, a slog.Attr) slog.Attr {
	if len(groups) > 0 {
		return a
	}
	switch a.Key {
	case slog.MessageKey:
		a.Key = logMessage
	case slog.LevelKey:
		a = slog.String(logSeverity, severity(a.Value.Any().(slog.Level)))
	}
	return a
}

func severity(l slog.Level) string {
	switch {
	case l >= slog.LevelError:
		return "ERROR"
	case l >= slog.LevelWarn:
		return "WARNING" // Cloud Logging has no "WARN"
	case l >= slog.LevelInfo:
		return "INFO"
	}
	return "DEBUG"
}

// trace is the request trace that a log line names. A zero trace adds nothing.
type trace struct {
	name    string // projects/<project>/traces/<trace ID>
	spanID  string // 16 hex digits
	sampled bool
}

type traceKey struct{}

// traceHandler adds the trace of the record's context. Limit: the trace fields land in the group a caller opened
// with WithGroup; no caller of this logger opens one.
type traceHandler struct{ slog.Handler }

func (h traceHandler) Handle(ctx context.Context, r slog.Record) error {
	if t, ok := ctx.Value(traceKey{}).(trace); ok {
		r.AddAttrs(slog.String(logTrace, t.name), slog.String(logSpanID, t.spanID), slog.Bool(logTraceSampled, t.sampled))
	}
	return h.Handler.Handle(ctx, r)
}

func (h traceHandler) WithAttrs(attrs []slog.Attr) slog.Handler {
	return traceHandler{h.Handler.WithAttrs(attrs)}
}

func (h traceHandler) WithGroup(name string) slog.Handler {
	return traceHandler{h.Handler.WithGroup(name)}
}

// requestTrace reads the trace that Cloud Run passes in, the W3C traceparent first, then X-Cloud-Trace-Context.
// A malformed header, or no project, gives no trace. Only hex digits pass, so a caller cannot write into a log line.
func requestTrace(h http.Header, project string) (trace, bool) {
	if project == "" {
		return trace{}, false
	}
	// traceparent: <2 hex version>-<32 hex trace ID>-<16 hex span ID>-<2 hex flags>. Version ff is invalid, version
	// 00 has exactly four fields, and a later version may append fields after them (W3C Trace Context).
	if parts := strings.Split(h.Get(headerTraceParent), "-"); len(parts) >= 4 &&
		isHex(parts[0], 2) && !strings.EqualFold(parts[0], "ff") && (parts[0] != "00" || len(parts) == 4) &&
		isHex(parts[1], traceIDLen) && isHex(parts[2], spanLen) && isHex(parts[3], 2) {
		flags, _ := strconv.ParseUint(parts[3], 16, 8)
		return newTrace(project, parts[1], parts[2], flags&1 == 1)
	}
	// X-Cloud-Trace-Context: <32 hex trace ID>/<decimal span ID>;o=<0 or 1>
	id, rest, ok := strings.Cut(h.Get(headerCloudTrace), "/")
	span, options, _ := strings.Cut(rest, ";")
	n, err := strconv.ParseUint(span, 10, 64)
	if !ok || !isHex(id, traceIDLen) || err != nil {
		return trace{}, false
	}
	return newTrace(project, id, strconv.FormatUint(n, 16), options == "o=1")
}

func newTrace(project, id, span string, sampled bool) (trace, bool) {
	if strings.Trim(id, "0") == "" || strings.Trim(span, "0") == "" { // all-zero IDs are invalid
		return trace{}, false
	}
	span = strings.Repeat("0", spanLen-len(span)) + strings.ToLower(span)
	return trace{name: "projects/" + project + "/traces/" + strings.ToLower(id), spanID: span, sampled: sampled}, true
}

func isHex(s string, n int) bool {
	_, err := hex.DecodeString(s) // every length checked here is even
	return len(s) == n && err == nil
}

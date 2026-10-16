package app

import (
	"bufio"
	"context"
	"encoding/json"
	"log/slog"
	"maps"
	"net/http"
	"net/http/httptest"
	"slices"
	"strings"
	"testing"
)

const (
	testTraceID = "4bf92f3577b34da6a3ce929d0e0e4736"
	testProject = "coupling-dev-510710"
)

// logLines parses every JSON line of out.
func logLines(t *testing.T, out string) []map[string]any {
	t.Helper()
	var lines []map[string]any
	sc := bufio.NewScanner(strings.NewReader(out))
	for sc.Scan() {
		var line map[string]any
		if err := json.Unmarshal(sc.Bytes(), &line); err != nil {
			t.Fatalf("log line %q: %v", sc.Text(), err)
		}
		lines = append(lines, line)
	}
	return lines
}

func TestLogLinesUseCloudLoggingFieldNames(t *testing.T) {
	logs := &lockedBuffer{}
	c := validConfig(t)
	c.TraceProject, c.Revision = testProject, "reporter-00042-abc"
	base, _, stop := startRun(t, c, NewLogger(logs))
	r := httptest.NewRequest(http.MethodGet, base+"/health", nil)
	r.Header.Set(headerTraceParent, "00-"+testTraceID+"-00f067aa0ba902b7-01")
	send(t, r)
	if err := stop(); err != nil {
		t.Fatalf("run = %v", err)
	}

	var request map[string]any
	for _, line := range logLines(t, logs.String()) {
		for _, slogKey := range []string{"level", "msg"} {
			if _, ok := line[slogKey]; ok {
				t.Errorf("line %v has %q: Cloud Logging does not read it", line, slogKey)
			}
		}
		if line["role"] != roleAll || line["revision"] != "reporter-00042-abc" {
			t.Errorf("line %v lacks the role or the revision", line)
		}
		if line[logMessage] == "request" {
			request = line
		}
	}
	if request == nil {
		t.Fatalf("no request line in %s", logs)
	}
	want := []string{
		"time", logSeverity, logMessage, "role", "revision",
		logTrace, logSpanID, logTraceSampled,
		"request_id", "method", "route", "status", "duration_ms",
	}
	if got := slices.Sorted(maps.Keys(request)); !slices.Equal(got, slices.Sorted(slices.Values(want))) {
		t.Errorf("request line fields = %v, want %v", got, slices.Sorted(slices.Values(want)))
	}
	wantValues := map[string]any{
		logSeverity:     "INFO",
		logTrace:        "projects/" + testProject + "/traces/" + testTraceID,
		logSpanID:       "00f067aa0ba902b7",
		logTraceSampled: true,
	}
	for k, v := range wantValues {
		if request[k] != v {
			t.Errorf("%s = %v, want %v", k, request[k], v)
		}
	}
}

func TestSeverityNames(t *testing.T) {
	tests := []struct {
		level slog.Level
		want  string
	}{
		{slog.LevelDebug, "DEBUG"},
		{slog.LevelInfo, "INFO"},
		{slog.LevelInfo + 2, "INFO"},
		{slog.LevelWarn, "WARNING"},
		{slog.LevelError, "ERROR"},
		{slog.LevelError + 4, "ERROR"},
	}
	for _, tt := range tests {
		if got := severity(tt.level); got != tt.want {
			t.Errorf("severity(%s) = %s, want %s", tt.level, got, tt.want)
		}
	}

	var out strings.Builder
	log := NewLogger(&out)
	log.Warn("w")
	log.Error("e", slog.Group("g", slog.String("level", "kept"), slog.String("msg", "kept")))
	lines := logLines(t, out.String())
	if len(lines) != 2 || lines[0][logSeverity] != "WARNING" || lines[1][logSeverity] != "ERROR" {
		t.Fatalf("lines = %v", lines)
	}
	if g, _ := lines[1]["g"].(map[string]any); g["level"] != "kept" || g["msg"] != "kept" {
		t.Errorf("a group's own keys were renamed: %v", lines[1])
	}
}

func TestRequestTrace(t *testing.T) {
	span := "00f067aa0ba902b7"
	traceName := "projects/" + testProject + "/traces/" + testTraceID
	tests := []struct {
		name    string
		parent  string
		cloud   string
		project string
		want    trace
		ok      bool
	}{
		{name: "traceparent, sampled", parent: "00-" + testTraceID + "-" + span + "-01", project: testProject,
			want: trace{traceName, span, true}, ok: true},
		{name: "traceparent, not sampled", parent: "00-" + testTraceID + "-" + span + "-00", project: testProject,
			want: trace{traceName, span, false}, ok: true},
		{name: "traceparent in upper case", parent: "00-" + strings.ToUpper(testTraceID) + "-" + strings.ToUpper(span) + "-01",
			project: testProject, want: trace{traceName, span, true}, ok: true},
		{name: "X-Cloud-Trace-Context with a decimal span", cloud: testTraceID + "/1;o=1", project: testProject,
			want: trace{traceName, "0000000000000001", true}, ok: true},
		{name: "X-Cloud-Trace-Context without options", cloud: testTraceID + "/18446744073709551615", project: testProject,
			want: trace{traceName, "ffffffffffffffff", false}, ok: true},
		{name: "traceparent wins", parent: "00-" + testTraceID + "-" + span + "-01", cloud: strings.Repeat("1", 32) + "/1;o=1",
			project: testProject, want: trace{traceName, span, true}, ok: true},
		{name: "a malformed traceparent falls back", parent: "00-short-" + span + "-01", cloud: testTraceID + "/1;o=1",
			project: testProject, want: trace{traceName, "0000000000000001", true}, ok: true},
		{name: "a later version with an extra field", parent: "01-" + testTraceID + "-" + span + "-01-extra", project: testProject,
			want: trace{traceName, span, true}, ok: true},
		{name: "version ff falls back", parent: "ff-" + testTraceID + "-" + span + "-01", cloud: testTraceID + "/1;o=1",
			project: testProject, want: trace{traceName, "0000000000000001", true}, ok: true},
		{name: "a version that is not hex falls back", parent: "zz-" + testTraceID + "-" + span + "-01", cloud: testTraceID + "/1;o=1",
			project: testProject, want: trace{traceName, "0000000000000001", true}, ok: true},
		{name: "version 00 with an extra field", parent: "00-" + testTraceID + "-" + span + "-01-extra", project: testProject},
		{name: "the all-zero span ID", parent: "00-" + testTraceID + "-" + strings.Repeat("0", 16) + "-01", project: testProject},
		{name: "X-Cloud-Trace-Context with span 0", cloud: testTraceID + "/0;o=1", project: testProject},
		{name: "no project", parent: "00-" + testTraceID + "-" + span + "-01"},
		{name: "no header", project: testProject},
		{name: "a trace ID with a quote", cloud: strings.Repeat("a", 31) + `"` + "/1", project: testProject},
		{name: "a span that is not a number", cloud: testTraceID + "/x1;o=1", project: testProject},
		{name: "the all-zero trace ID", parent: "00-" + strings.Repeat("0", 32) + "-" + span + "-01", project: testProject},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			h := http.Header{}
			if tt.parent != "" {
				h.Set(headerTraceParent, tt.parent)
			}
			if tt.cloud != "" {
				h.Set(headerCloudTrace, tt.cloud)
			}
			got, ok := requestTrace(h, tt.project)
			if ok != tt.ok || got != tt.want {
				t.Errorf("requestTrace = %+v, %v; want %+v, %v", got, ok, tt.want, tt.ok)
			}
		})
	}
}

func TestModuleLinesCarryTheRequestTrace(t *testing.T) {
	var out strings.Builder
	log := NewLogger(&out)
	ctx := context.WithValue(context.Background(), traceKey{}, trace{name: "projects/p/traces/t", spanID: "s", sampled: true})
	log.With("role", roleWorker).InfoContext(ctx, "gate event")
	log.Info("no request")
	lines := logLines(t, out.String())
	if lines[0][logTrace] != "projects/p/traces/t" || lines[0]["role"] != roleWorker {
		t.Errorf("a line logged with a request context lacks its trace: %v", lines[0])
	}
	if _, ok := lines[1][logTrace]; ok {
		t.Errorf("a line without a request context names a trace: %v", lines[1])
	}
}

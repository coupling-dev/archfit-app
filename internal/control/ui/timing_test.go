package ui

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/alexei-led/archfit-app/internal/github/api"
)

func TestOnlyTheInstancesFirstResponseIsCold(t *testing.T) {
	h := chain(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte("ok"))
	}))
	var got []string
	for range 2 {
		res := httptest.NewRecorder()
		h.ServeHTTP(res, httptest.NewRequest(http.MethodGet, "/health", nil))
		got = append(got, res.Header().Get("Server-Timing"))
	}
	if !strings.HasPrefix(got[0], "total;dur=") || !strings.HasSuffix(got[0], ", cold") {
		t.Errorf("first Server-Timing = %q, want total first and cold last", got[0])
	}
	if strings.Contains(got[1], "cold") || !strings.HasPrefix(got[1], "total;dur=") {
		t.Errorf("second Server-Timing = %q, want total and no cold", got[1])
	}
}

func TestAPIResponsesCarryStageTimingsWithoutTenantData(t *testing.T) {
	e := newEnv(t)
	c := e.as("alice", permAdmin)

	repo := c.get("/api/repos/" + repoName + "/policy")
	wantStatus(t, repo, http.StatusOK)
	got := repo.Header().Get("Server-Timing")
	wantContains(t, got, "total;dur=", "authorization;dur=")
	for _, private := range []string{"alice", repoName, strings.Split(repoName, "/")[0]} {
		if strings.Contains(got, private) {
			t.Errorf("Server-Timing %q holds %q", got, private)
		}
	}

	page := c.get("/r/" + repoName)
	if page.Header().Get("Server-Timing") == "" {
		t.Error("the shell has no Server-Timing")
	}
}

func TestServerTimingKeepsOnlyMetricNames(t *testing.T) {
	_, gh := api.WithCallStats(context.Background())
	stages := map[string]api.Stage{
		"rules":            {DurationMS: 12.34},
		"authorization":    {DurationMS: 1},
		`bad;dur=1, x="y"`: {DurationMS: 5},
		"Upper":            {DurationMS: 5},
	}
	got := serverTiming(1500*time.Microsecond, gh, stages, false)
	want := "total;dur=1.5, authorization;dur=1.0, rules;dur=12.3"
	if got != want {
		t.Errorf("serverTiming = %q, want %q", got, want)
	}
}

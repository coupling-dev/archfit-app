package ui

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/alexei-led/archfit-app/internal/control/onboarding"
	"github.com/alexei-led/archfit-app/internal/control/passkey"
	"github.com/alexei-led/archfit-app/internal/github/api"
)

type blockedSetup struct {
	started chan struct{}
	release chan struct{}
	err     error
}

func (s blockedSetup) Setup(ctx context.Context, _ api.Repo) (onboarding.Setup, error) {
	close(s.started)
	select {
	case <-s.release:
	case <-ctx.Done():
		return onboarding.Setup{}, ctx.Err()
	}
	return onboarding.Setup{State: "first_approval"}, s.err
}

type concurrentPasskeys struct {
	*fakePasskeys
	setupStarted chan struct{}
	release      chan struct{}
	err          error
}

func (p concurrentPasskeys) List(ctx context.Context, _ api.Repo, _ int64) ([]passkey.Passkey, error) {
	select {
	case <-p.setupStarted:
	case <-ctx.Done():
		return nil, ctx.Err()
	}
	close(p.release)
	return []passkey.Passkey{{ID: fakePasskeyID}}, p.err
}

func TestHomeSetupAndPasskeysOverlapWithoutHidingErrors(t *testing.T) {
	for _, tc := range []struct {
		name             string
		setupErr, keyErr error
		hasKey           bool
	}{
		{"success", nil, nil, true},
		{"inventory error remains false", nil, api.ErrRateLimited, false},
		{"setup rate limit preserved", api.ErrRateLimited, nil, false},
		{"setup timeout preserved", context.DeadlineExceeded, nil, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			ctx, cancel := context.WithTimeout(t.Context(), 2*time.Second)
			defer cancel()
			started, release := make(chan struct{}), make(chan struct{})
			s := &server{setups: blockedSetup{started, release, tc.setupErr},
				passkeys: concurrentPasskeys{fakePasskeys: &fakePasskeys{}, setupStarted: started, release: release, err: tc.keyErr}}
			out, err := s.apiSetup(httptest.NewRequest("GET", "/", nil).WithContext(ctx), scope{})
			if !errors.Is(err, tc.setupErr) {
				t.Fatalf("error = %v; want %v", err, tc.setupErr)
			}
			if ctx.Err() != nil {
				t.Fatal("setup and passkeys did not overlap")
			}
			if err == nil && (out.(setupJSON).Passkey != tc.hasKey || out.(setupJSON).PasskeyKnown != (tc.keyErr == nil)) {
				t.Fatalf("Setup = %+v", out)
			}
		})
	}
}

func TestHomeStagesCountParallelWorkAndCacheHits(t *testing.T) {
	ctx, stats := api.WithStageStats(t.Context())
	var wg sync.WaitGroup
	for range 4 {
		wg.Go(func() { done := api.StartStage(ctx, "rules"); done(1, 0) })
	}
	wg.Wait()
	done := api.StartStage(ctx, "authorization")
	done(1, 1)
	got := stats.Snapshot()
	if got["rules"].Count != 4 || got["rules"].DurationMS <= 0 || got["authorization"].CacheHits != 1 {
		t.Fatalf("stages = %+v", got)
	}
	delete(got, "rules")
	if stats.Snapshot()["rules"].Count != 4 {
		t.Fatal("snapshot mutated measurements")
	}
}

func TestHomeRequestLogsReachedStagesAndWarmAuthorization(t *testing.T) {
	e := newHomeEnv(t, homePolicy)
	client := e.as("alice", permAdmin)
	logs := captureLogs(t)
	for range 2 {
		wantStatus(t, client.get(homeAPI), http.StatusOK)
	}
	var requests []map[string]api.Stage
	for line := range strings.SplitSeq(strings.TrimSpace(logs.String()), "\n") {
		var row struct {
			Message string               `json:"msg"`
			Stages  map[string]api.Stage `json:"stages"`
		}
		if err := json.Unmarshal([]byte(line), &row); err != nil {
			t.Fatal(err)
		}
		if row.Message == "ui request" {
			requests = append(requests, row.Stages)
		}
	}
	if len(requests) != 2 {
		t.Fatalf("request stage logs = %d", len(requests))
	}
	for _, stages := range requests {
		for _, name := range []string{"authorization", "setup", "passkeys", "home_records", "snapshot"} {
			stage, ok := stages[name]
			if !ok || stage.DurationMS <= 0 {
				t.Fatalf("missing stage %s: %+v", name, stages)
			}
		}
	}
	if requests[1]["authorization"].CacheHits != 1 {
		t.Fatalf("warm authorization = %+v", requests[1])
	}
	if strings.Contains(logs.String(), homePolicy) {
		t.Fatal("policy contents leaked to timing logs")
	}
}

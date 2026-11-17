package ui

import (
	"encoding/json"
	"errors"
	"net/http"
	"testing"
	"testing/synctest"
	"time"

	"github.com/alexei-led/archfit-app/internal/github/api"
)

// Setup and the Home reads each take latency of virtual time. They are independent, so the response takes one of
// them, where the serial code took both.
func TestHomeReadsRunBesideSetup(t *testing.T) {
	const latency = 250 * time.Millisecond
	synctest.Test(t, func(t *testing.T) {
		e := newHomeEnv(t, homePolicy)
		e.setups.delay, e.gh.openPullsDelay = latency, latency
		client := e.as("alice", permAdmin)
		wantStatus(t, client.get(homeAPI), http.StatusOK) // warms the authorization cache
		start := time.Now()
		response := client.get(homeAPI)
		elapsed := time.Since(start)
		wantStatus(t, response, http.StatusOK)
		t.Logf("response time = %v", elapsed)
		var body homeJSON
		if err := json.Unmarshal(response.Body.Bytes(), &body); err != nil {
			t.Fatal(err)
		}
		if body.Map == nil || body.Status != "clean" {
			t.Fatalf("body = %s", response.Body.String())
		}
		if elapsed != latency {
			t.Fatalf("response took %v of virtual time, want one read (%v)", elapsed, latency)
		}
	})
}

// A Home read error counts only for an approved repository. For any other state the answer is the Setup one.
func TestHomeReadErrorsCountOnlyWhenTheRepositoryIsApproved(t *testing.T) {
	for _, tt := range []struct {
		name      string
		state     string
		setupErr  error
		homeErr   error
		wantCode  int
		wantSetup bool
	}{
		{"approved, Home read fails", "approved", nil, errors.New("read failed"), http.StatusBadGateway, false},
		{"outside the flow, Home read fails", "outside_flow", nil, errors.New("read failed"), http.StatusBadGateway, false},
		{"not set up, Home read fails", "first_approval", nil, errors.New("read failed"), http.StatusOK, true},
		{"setup fails, Home read fails", "approved", api.ErrRateLimited, errors.New("read failed"), http.StatusServiceUnavailable, false},
	} {
		t.Run(tt.name, func(t *testing.T) {
			e := newHomeEnv(t, homePolicy)
			e.setups.out.State = tt.state
			if tt.setupErr != nil {
				e.setups.errors = map[string]error{repoName: tt.setupErr}
			}
			e.gh.fail["OpenPulls"] = tt.homeErr
			response := e.as("alice", permAdmin).get(homeAPI)
			wantStatus(t, response, tt.wantCode)
			if tt.wantCode != http.StatusOK {
				return
			}
			var body homeJSON
			if err := json.Unmarshal(response.Body.Bytes(), &body); err != nil {
				t.Fatal(err)
			}
			if (body.Setup != nil) != tt.wantSetup || body.Setup != nil && body.Setup.State != tt.state || body.Map != nil {
				t.Fatalf("body = %s", response.Body.String())
			}
		})
	}
}

package oidc

import (
	"context"
	"net/http"
	"testing"
	"time"
)

func TestTokenForAnotherServiceIsRefused(t *testing.T) {
	h := newHarness(t)
	token := rs256Token(t, h.keys.valid, testKid, with(goodClaims(testStart), "aud", "https://other-service.example.com"))
	got, err := h.verify(token)
	wantOrdinaryError(t, got, err)
	if err.Error() != "github/oidc: audience" {
		t.Errorf("error = %q, want the audience check", err)
	}
}

func TestRerunAttemptIsVisible(t *testing.T) {
	h := newHarness(t)
	first, err := h.verify(rs256Token(t, h.keys.valid, testKid, with(goodClaims(testStart), "run_attempt", "1")))
	if err != nil {
		t.Fatalf("attempt 1: %v", err)
	}
	second, err := h.verify(rs256Token(t, h.keys.valid, testKid, with(goodClaims(testStart), "run_attempt", "2")))
	if err != nil {
		t.Fatalf("attempt 2: %v", err)
	}
	if first.RunID != second.RunID {
		t.Errorf("RunID differs between attempts: %d and %d", first.RunID, second.RunID)
	}
	if first.RunAttempt != 1 || second.RunAttempt != 2 {
		t.Errorf("RunAttempt = %d and %d, want 1 and 2", first.RunAttempt, second.RunAttempt)
	}
}

func TestSelfHostedRunnerIsAccepted(t *testing.T) {
	base := goodClaims(testStart)
	tests := []struct {
		name   string
		claims map[string]any
		want   string
	}{
		{"self-hosted", with(base, "runner_environment", "self-hosted"), "self-hosted"},
		{"claim absent", without(base, "runner_environment"), ""},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			h := newHarness(t)
			got, err := h.verify(rs256Token(t, h.keys.valid, testKid, tt.claims))
			if err != nil {
				t.Fatalf("Verify: %v", err)
			}
			if got.RunnerEnvironment != tt.want {
				t.Errorf("RunnerEnvironment = %q, want %q", got.RunnerEnvironment, tt.want)
			}
		})
	}
}

func TestOutageIsDistinguishableFromForgery(t *testing.T) {
	t.Run("outage on a cold cache", func(t *testing.T) {
		h := newHarness(t)
		h.issuer.down()

		got, err := h.verify(h.goodToken(t))
		wantUnavailable(t, got, err)

		none := b64(mustJSON(t, map[string]any{"alg": "none", "kid": testKid})) + "." +
			b64(mustJSON(t, goodClaims(testStart))) + "."
		got, err = h.verify(none)
		wantOrdinaryError(t, got, err)
	})
	t.Run("keys available", func(t *testing.T) {
		h := newHarness(t)
		got, err := h.verify(rs256Token(t, h.keys.attacker, "attacker", goodClaims(testStart)))
		wantOrdinaryError(t, got, err)

		got, err = h.verify(rs256Token(t, h.keys.attacker, testKid, goodClaims(testStart)))
		wantOrdinaryError(t, got, err)
	})
}

func TestIssuerOutageFailsClosedThenRecovers(t *testing.T) {
	h := newHarness(t)
	h.issuer.down()
	for range 3 {
		got, err := h.verify(h.goodToken(t))
		wantUnavailable(t, got, err)
	}

	h.issuer.serve(http.StatusOK, jwksBody(t, rsaJWK(testKid, &h.keys.valid.PublicKey)))
	h.clock.Advance(61 * time.Second)
	if _, err := h.v.Verify(context.Background(), h.goodToken(t), testAudience); err != nil {
		t.Errorf("after recovery: %v", err)
	}
}

func TestNewUsesTenSecondClientByDefault(t *testing.T) {
	v, ok := New(nil).(*verifier)
	if !ok {
		t.Fatal("New(nil) did not return a verifier")
	}
	if got := v.keys.hc.Timeout; got != 10*time.Second {
		t.Errorf("default client timeout = %v, want 10s", got)
	}
	if _, err := v.Verify(context.Background(), "", testAudience); err == nil {
		t.Error("empty token: want an error without a fetch")
	}
}

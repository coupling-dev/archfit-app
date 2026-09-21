package oidc

import (
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"crypto/x509"
	"encoding/base64"
	"errors"
	"fmt"
	"net/http"
	"os/exec"
	"strings"
	"testing"
	"time"
)

func hs256Token(t *testing.T, h *harness) string {
	t.Helper()
	input := b64(mustJSON(t, map[string]any{"alg": "HS256", "kid": testKid})) + "." + b64(mustJSON(t, goodClaims(testStart)))
	mac := hmac.New(sha256.New, x509.MarshalPKCS1PublicKey(&h.keys.valid.PublicKey))
	mac.Write([]byte(input))
	return input + "." + b64(mac.Sum(nil))
}

func paddedToken(t *testing.T, h *harness) string {
	t.Helper()
	header := base64.URLEncoding.EncodeToString([]byte(`{"alg":"RS256","kid":"k1"}`))
	if !strings.HasSuffix(header, "=") {
		t.Fatal("test header must need base64 padding")
	}
	input := header + "." + b64(mustJSON(t, goodClaims(testStart)))
	return input + "." + b64(rsaSign(t, h.keys.valid, input))
}

type rejectCase struct {
	name     string
	token    func(*testing.T, *harness) string
	audience string
	wantErr  string
}

func TestVerifyRejects(t *testing.T) {
	base := goodClaims(testStart)
	good := func(t *testing.T, h *harness) string { return h.goodToken(t) }
	withClaims := func(claims map[string]any) func(*testing.T, *harness) string {
		return func(t *testing.T, h *harness) string { return rs256Token(t, h.keys.valid, testKid, claims) }
	}
	withHeader := func(header map[string]any) func(*testing.T, *harness) string {
		return func(t *testing.T, h *harness) string {
			return signedToken(t, h.keys.valid, header, mustJSON(t, base))
		}
	}
	tests := []rejectCase{
		{"bad signature", func(t *testing.T, h *harness) string {
			return rs256Token(t, h.keys.attacker, testKid, base)
		}, testAudience, "github/oidc: signature"},
		{"payload altered after signing", func(t *testing.T, h *harness) string {
			parts := strings.Split(h.goodToken(t), ".")
			parts[1] = b64(mustJSON(t, with(base, "repository", "o/victim")))
			return strings.Join(parts, ".")
		}, testAudience, "github/oidc: signature"},
		{"alg none", func(t *testing.T, _ *harness) string {
			return b64(mustJSON(t, map[string]any{"alg": "none", "kid": testKid})) + "." + b64(mustJSON(t, base)) + "."
		}, testAudience, "github/oidc: algorithm"},
		{"alg HS256 signed with the public key", hs256Token, testAudience, "github/oidc: algorithm"},
		{"alg RS384", withHeader(map[string]any{"alg": "RS384", "kid": testKid}), testAudience, "github/oidc: algorithm"},
		{"missing kid", withHeader(map[string]any{"alg": "RS256"}), testAudience, "github/oidc: kid"},
		{"unknown kid", func(t *testing.T, h *harness) string {
			return rs256Token(t, h.keys.valid, "other", base)
		}, testAudience, "github/oidc: unknown key"},
		{"wrong issuer", withClaims(with(base, "iss", "https://issuer.example.com")), testAudience, "github/oidc: issuer"},
		{"issuer with a trailing slash", withClaims(with(base, "iss", "https://token.actions.githubusercontent.com/")),
			testAudience, "github/oidc: issuer"},
		{"wrong audience", good, "https://other.example.com", "github/oidc: audience"},
		{"expired", withClaims(with(base, "exp", testStart.Add(-time.Hour).Unix())), testAudience, "github/oidc: expired"},
		{"two parts", func(t *testing.T, h *harness) string {
			return strings.Join(strings.Split(h.goodToken(t), ".")[:2], ".")
		}, testAudience, "github/oidc: malformed token"},
		{"four parts", func(t *testing.T, h *harness) string { return h.goodToken(t) + ".x" },
			testAudience, "github/oidc: malformed token"},
		{"padded base64", paddedToken, testAudience, "github/oidc: malformed token"},
		{"17 KiB token", func(*testing.T, *harness) string { return strings.Repeat("a", 17*1024) },
			testAudience, "github/oidc: token size"},
		{"empty token", func(*testing.T, *harness) string { return "" }, testAudience, "github/oidc: empty token"},
		{"empty audience", good, "", "github/oidc: audience"},
		{"token minted with an empty audience", withClaims(with(base, "aud", "")), "", "github/oidc: audience"},
		{"missing iss", withClaims(without(base, "iss")), testAudience, "github/oidc: issuer"},
		{"missing aud", withClaims(without(base, "aud")), testAudience, "github/oidc: audience"},
		{"missing exp", withClaims(without(base, "exp")), testAudience, "github/oidc: claim exp"},
		{"missing iat", withClaims(without(base, "iat")), testAudience, "github/oidc: claim iat"},
	}
	for _, claim := range []string{
		"repository", "repository_id", "run_id", "run_attempt", "sha", "ref", "event_name", "job_workflow_ref",
	} {
		tests = append(tests, rejectCase{"missing " + claim, withClaims(without(base, claim)), testAudience, "github/oidc: claim " + claim})
	}
	for _, claim := range []string{"repository", "sha", "ref", "event_name", "job_workflow_ref"} {
		tests = append(tests, rejectCase{"empty " + claim, withClaims(with(base, claim, "")), testAudience, "github/oidc: claim " + claim})
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			h := newHarness(t)
			got, err := h.v.Verify(context.Background(), tt.token(t, h), tt.audience)
			wantOrdinaryError(t, got, err)
			if err.Error() != tt.wantErr {
				t.Errorf("error = %q, want %q", err, tt.wantErr)
			}
		})
	}

	t.Run("empty arguments cause no fetch", func(t *testing.T) {
		h := newHarness(t)
		token := h.goodToken(t)
		_, _ = h.v.Verify(context.Background(), "", testAudience)
		_, _ = h.v.Verify(context.Background(), token, "")
		if got := h.issuer.fetches(); got != 0 {
			t.Errorf("fetches = %d, want 0", got)
		}
	})
}

func TestUnknownKidFloodIsBounded(t *testing.T) {
	h := newHarness(t)
	h.warm(t)
	h.clock.Advance(2 * time.Minute)
	before := h.issuer.fetches()

	payload := b64(mustJSON(t, goodClaims(h.clock.Now())))
	for i := range 1000 {
		header := b64(mustJSON(t, map[string]any{"alg": "RS256", "kid": fmt.Sprintf("random-%d", i)}))
		got, err := h.verify(header + "." + payload + "." + b64([]byte("not a signature")))
		wantOrdinaryError(t, got, err)
		h.clock.Advance(59 * time.Millisecond)
	}
	if got := h.issuer.fetches() - before; got > 1 {
		t.Errorf("refetches within one minute = %d, want at most 1", got)
	}
	if _, err := h.verify(h.goodToken(t)); err != nil {
		t.Errorf("valid token after the flood: %v", err)
	}
}

func TestJWKSFailureModes(t *testing.T) {
	unknownKid := func(t *testing.T, h *harness) string {
		return rs256Token(t, h.keys.rotated, "k2", goodClaims(h.clock.Now()))
	}
	good := func(t *testing.T, h *harness) string { return h.goodToken(t) }
	tests := []struct {
		name  string
		setup func(*testing.T, *harness)
		token func(*testing.T, *harness) string
		want  string // "ok", "unavailable" or "ordinary"
	}{
		{"cold cache and fetch 500", func(_ *testing.T, h *harness) {
			h.issuer.serve(http.StatusInternalServerError, "")
		}, good, "unavailable"},
		{"cold cache and malformed JSON", func(_ *testing.T, h *harness) {
			h.issuer.serve(http.StatusOK, `{"keys":[`)
		}, good, "unavailable"},
		{"oversized response", func(t *testing.T, h *harness) {
			body := jwksBody(t, rsaJWK(testKid, &h.keys.valid.PublicKey))
			h.issuer.serve(http.StatusOK, strings.TrimSuffix(body, "}")+`,"pad":"`+strings.Repeat("a", 1<<20)+`"}`)
		}, good, "unavailable"},
		{"cold cache and a key set with no usable key", func(_ *testing.T, h *harness) {
			h.issuer.serve(http.StatusOK, `{"keys":[{"kty":"EC","kid":"`+testKid+`","crv":"P-256"}]}`)
		}, good, "unavailable"},
		{"warm cache and refetch at 61 minutes without a usable key", func(t *testing.T, h *harness) {
			h.warm(t)
			h.issuer.serve(http.StatusOK, `{"keys":[]}`)
			h.clock.Set(testStart.Add(61 * time.Minute))
		}, good, "ok"},
		{"warm cache and refetch failing at 61 minutes", func(t *testing.T, h *harness) {
			h.warm(t)
			h.issuer.serve(http.StatusInternalServerError, "")
			h.clock.Set(testStart.Add(61 * time.Minute))
		}, good, "ok"},
		{"warm cache, fetch failing, clock at 25 hours", func(t *testing.T, h *harness) {
			h.warm(t)
			h.issuer.down()
			h.clock.Set(testStart.Add(25 * time.Hour))
		}, good, "unavailable"},
		{"warm cache, unknown kid, refetch failing", func(t *testing.T, h *harness) {
			h.warm(t)
			h.issuer.serve(http.StatusServiceUnavailable, "")
			h.clock.Advance(2 * time.Minute)
		}, unknownKid, "unavailable"},
		{"warm cache, unknown kid, refetch succeeding without that key", func(t *testing.T, h *harness) {
			h.warm(t)
			h.clock.Advance(2 * time.Minute)
		}, unknownKid, "ordinary"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			h := newHarness(t)
			tt.setup(t, h)
			before := h.issuer.fetches()
			got, err := h.verify(tt.token(t, h))
			switch tt.want {
			case "ok":
				if err != nil {
					t.Fatalf("want ok from the cached set, got %v", err)
				}
			case "unavailable":
				wantUnavailable(t, got, err)
			default:
				wantOrdinaryError(t, got, err)
			}
			if h.issuer.fetches() != before+1 {
				t.Errorf("fetch attempts = %d, want 1", h.issuer.fetches()-before)
			}
		})
	}
}

func TestFetchBoundCoversEveryFetch(t *testing.T) {
	h := newHarness(t)
	h.issuer.down()
	got, err := h.verify(h.goodToken(t))
	wantUnavailable(t, got, err)

	// The issuer is back, but the failed cold fetch used this minute's slot.
	h.issuer.serve(http.StatusOK, jwksBody(t, rsaJWK(testKid, &h.keys.valid.PublicKey)))
	h.clock.Advance(59 * time.Second)
	got, err = h.verify(h.goodToken(t))
	wantUnavailable(t, got, err)
	if n := h.issuer.fetches(); n != 1 {
		t.Errorf("fetch attempts inside the minute = %d, want 1", n)
	}

	h.clock.Advance(time.Second)
	if _, err := h.verify(h.goodToken(t)); err != nil {
		t.Errorf("after the minute: %v", err)
	}
}

func TestCancelledFetchDoesNotUseTheFetchSlot(t *testing.T) {
	h := newHarness(t)
	h.issuer.down()
	cancelled, cancel := context.WithCancel(context.Background())
	cancel()
	got, err := h.v.Verify(cancelled, h.goodToken(t), testAudience)
	wantUnavailable(t, got, err)

	// No clock advance: a failed fetch would block this one for a minute, a cancelled one must not.
	h.issuer.serve(http.StatusOK, jwksBody(t, rsaJWK(testKid, &h.keys.valid.PublicKey)))
	if _, err := h.verify(h.goodToken(t)); err != nil {
		t.Errorf("next caller: %v", err)
	}
}

func TestHeaderKeyHintsAreIgnored(t *testing.T) {
	h := newHarness(t)
	h.warm(t)
	h.clock.Advance(2 * time.Minute)
	for _, kid := range []string{testKid, "attacker"} {
		header := map[string]any{
			"alg": "RS256", "kid": kid,
			"jwk": rsaJWK(kid, &h.keys.attacker.PublicKey),
			"jku": "https://attacker.example.com/.well-known/jwks",
			"x5u": "https://attacker.example.com/cert.pem",
		}
		got, err := h.verify(signedToken(t, h.keys.attacker, header, mustJSON(t, goodClaims(h.clock.Now()))))
		wantOrdinaryError(t, got, err)
	}
	for _, url := range h.issuer.requested() {
		if url != fixedJWKSURL {
			t.Errorf("request left the fixed issuer host: %s", url)
		}
	}
}

func TestErrorsCarryNoTokenMaterial(t *testing.T) {
	const marker = "MARKER"
	base := goodClaims(testStart)
	marked := func(claim string) func(*testing.T, *harness) string {
		return func(t *testing.T, h *harness) string {
			return rs256Token(t, h.keys.valid, testKid, with(base, claim, marker+"-12a"))
		}
	}
	tests := []struct {
		name  string
		setup func(*harness)
		token func(*testing.T, *harness) string
	}{
		{"two parts", nil, func(*testing.T, *harness) string { return marker + "." + marker }},
		{"header not base64", nil, func(*testing.T, *harness) string { return marker + "!." + marker + "." + marker }},
		{"header not JSON", nil, func(*testing.T, *harness) string { return b64([]byte(marker)) + ".e30.c2ln" }},
		{"algorithm", nil, func(t *testing.T, h *harness) string {
			return signedToken(t, h.keys.valid, map[string]any{"alg": marker, "kid": testKid}, mustJSON(t, base))
		}},
		{"unknown kid", nil, func(t *testing.T, h *harness) string {
			return rs256Token(t, h.keys.valid, marker+"-kid", base)
		}},
		{"keys unavailable", func(h *harness) { h.issuer.serve(http.StatusOK, marker) }, func(t *testing.T, h *harness) string {
			return rs256Token(t, h.keys.valid, marker+"-kid", base)
		}},
		{"keys unreachable", func(h *harness) { h.issuer.err = errors.New(marker) }, func(t *testing.T, h *harness) string {
			return h.goodToken(t)
		}},
		{"signature", nil, func(t *testing.T, h *harness) string {
			return rs256Token(t, h.keys.attacker, testKid, with(base, "repository", marker))
		}},
		{"signature not base64", nil, func(t *testing.T, h *harness) string {
			return strings.Join(strings.Split(h.goodToken(t), ".")[:2], ".") + "." + marker + "!"
		}},
		{"payload not JSON", nil, func(t *testing.T, h *harness) string {
			return signedToken(t, h.keys.valid, map[string]any{"alg": "RS256", "kid": testKid}, []byte(marker))
		}},
		{"payload claim of the wrong type", nil, func(t *testing.T, h *harness) string {
			return rs256Token(t, h.keys.valid, testKid, with(base, "sha", []any{marker}))
		}},
		{"issuer", nil, marked("iss")},
		{"audience", nil, marked("aud")},
		{"expired", nil, func(t *testing.T, h *harness) string {
			return rs256Token(t, h.keys.valid, testKid, with(with(base, "exp", 1), "repository", marker))
		}},
		{"not yet valid", nil, func(t *testing.T, h *harness) string {
			claims := with(base, "nbf", testStart.Add(time.Hour).Unix())
			return rs256Token(t, h.keys.valid, testKid, with(claims, "repository", marker))
		}},
		{"claim exp", nil, func(t *testing.T, h *harness) string {
			return rs256Token(t, h.keys.valid, testKid, with(base, "exp", marker))
		}},
		{"claim repository_id", nil, marked("repository_id")},
		{"claim run_id", nil, marked("run_id")},
		{"claim run_attempt", nil, marked("run_attempt")},
		{"claim sha", nil, func(t *testing.T, h *harness) string {
			return rs256Token(t, h.keys.valid, testKid, with(without(base, "sha"), "ref", marker))
		}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			h := newHarness(t)
			if tt.setup != nil {
				tt.setup(h)
			}
			token := tt.token(t, h)
			got, err := h.verify(token)
			if err == nil {
				t.Fatal("want an error, got nil")
			}
			if got != (Claims{}) {
				t.Errorf("want zero Claims, got %+v", got)
			}
			text := err.Error()
			if !strings.HasPrefix(text, "github/oidc: ") {
				t.Errorf("error %q does not start with github/oidc:", text)
			}
			for _, secret := range append(strings.Split(token, "."), marker) {
				if secret != "" && strings.Contains(text, secret) {
					t.Errorf("error %q carries token material %q", text, secret)
				}
			}
		})
	}
}

func TestOIDCImportsOnlyStdlib(t *testing.T) {
	out, err := exec.Command("go", "list", "-deps", "-f", "{{if not .Standard}}{{.ImportPath}}{{end}}", ".").Output()
	if err != nil {
		t.Fatalf("go list: %v", err)
	}
	const self = "github.com/alexei-led/archfit-app/internal/github/oidc"
	for _, pkg := range strings.Fields(string(out)) {
		if pkg != self {
			t.Errorf("non-standard dependency: %s", pkg)
		}
	}
}

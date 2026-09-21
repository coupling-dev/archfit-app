package oidc

import (
	"net/http"
	"strings"
	"sync"
	"testing"
	"time"
)

func TestWorkflowPath(t *testing.T) {
	const path = ".github/workflows/archfit.yaml"
	tests := []struct {
		name, ref, fullName, want string
	}{
		{"same repository", "o/r/" + path + "@refs/pull/7/merge", "o/r", path},
		{"repository name in another case", "o/r/" + path + "@refs/pull/7/merge", "O/R", path},
		{"ref containing @", "o/r/" + path + "@refs/heads/a@b", "o/r", ""},
		{"reusable workflow elsewhere", "o/other/.github/workflows/x.yml@main", "o/r", ""},
		{"repository name is only a prefix", "o/r2/" + path + "@main", "o/r", ""},
		{"no @", "o/r/" + path, "o/r", ""},
		{"empty path", "o/r/@main", "o/r", ""},
		{"file name containing @", "o/r/.github/workflows/x.yml@evil.yml@refs/heads/main", "o/r", ""},
		{"empty ref", "", "o/r", ""},
		{"empty repository", "o/r/" + path + "@main", "", ""},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := WorkflowPath(tt.ref, tt.fullName); got != tt.want {
				t.Errorf("WorkflowPath(%q, %q) = %q, want %q", tt.ref, tt.fullName, got, tt.want)
			}
		})
	}
}

func TestTimeClaimsLeeway(t *testing.T) {
	at := func(d time.Duration) int64 { return testStart.Add(d).Unix() }
	base := goodClaims(testStart)
	tests := []struct {
		name    string
		claims  map[string]any
		wantErr string
	}{
		{"exp 59 s ago", with(base, "exp", at(-59*time.Second)), ""},
		{"exp 61 s ago", with(base, "exp", at(-61*time.Second)), "github/oidc: expired"},
		{"nbf in 59 s", with(base, "nbf", at(59*time.Second)), ""},
		{"nbf in 61 s", with(base, "nbf", at(61*time.Second)), "github/oidc: not yet valid"},
		{"iat in 59 s", with(base, "iat", at(59*time.Second)), ""},
		{"iat in 61 s", with(base, "iat", at(61*time.Second)), "github/oidc: not yet valid"},
		{"missing exp", without(base, "exp"), "github/oidc: claim exp"},
		{"missing iat", without(base, "iat"), "github/oidc: claim iat"},
		{"missing nbf", without(base, "nbf"), ""},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			h := newHarness(t)
			got, err := h.verify(rs256Token(t, h.keys.valid, testKid, tt.claims))
			if tt.wantErr == "" {
				if err != nil {
					t.Fatalf("want ok, got %v", err)
				}
				return
			}
			wantOrdinaryError(t, got, err)
			if err.Error() != tt.wantErr {
				t.Errorf("error = %q, want %q", err, tt.wantErr)
			}
		})
	}
}

func TestAudienceForms(t *testing.T) {
	base := goodClaims(testStart)
	tests := []struct {
		name   string
		claims map[string]any
		ok     bool
	}{
		{"string equal", with(base, "aud", testAudience), true},
		{"string with trailing slash", with(base, "aud", testAudience+"/"), false},
		{"different case", with(base, "aud", strings.ToUpper(testAudience)), false},
		{"array containing it", with(base, "aud", []any{"https://other.example.com", testAudience}), true},
		{"array without it", with(base, "aud", []any{"https://other.example.com"}), false},
		{"empty array", with(base, "aud", []any{}), false},
		{"missing", without(base, "aud"), false},
		{"number", with(base, "aud", 42), false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			h := newHarness(t)
			got, err := h.verify(rs256Token(t, h.keys.valid, testKid, tt.claims))
			if tt.ok {
				if err != nil {
					t.Fatalf("want ok, got %v", err)
				}
				return
			}
			wantOrdinaryError(t, got, err)
			if err.Error() != "github/oidc: audience" {
				t.Errorf("error = %q, want the audience check", err)
			}
		})
	}
}

func TestNumericClaimEncodings(t *testing.T) {
	fields := map[string]func(Claims) int64{
		"run_id":        func(c Claims) int64 { return c.RunID },
		"run_attempt":   func(c Claims) int64 { return int64(c.RunAttempt) },
		"repository_id": func(c Claims) int64 { return c.RepositoryID },
	}
	values := []struct {
		name  string
		value any
		ok    bool
	}{
		{"string 123", "123", true},
		{"number 123", 123, true},
		{"string 12a", "12a", false},
		{"empty string", "", false},
		{"string 0", "0", false},
		{"string -1", "-1", false},
		{"number 1.5", 1.5, false},
	}
	for claim, field := range fields {
		for _, tt := range values {
			t.Run(claim+" "+tt.name, func(t *testing.T) {
				h := newHarness(t)
				got, err := h.verify(rs256Token(t, h.keys.valid, testKid, with(goodClaims(testStart), claim, tt.value)))
				if tt.ok {
					if err != nil {
						t.Fatalf("want ok, got %v", err)
					}
					if field(got) != 123 {
						t.Errorf("%s = %d, want 123", claim, field(got))
					}
					return
				}
				wantOrdinaryError(t, got, err)
				if want := "github/oidc: claim " + claim; err.Error() != want {
					t.Errorf("error = %q, want %q", err, want)
				}
			})
		}
	}
}

func TestJWKSelection(t *testing.T) {
	h := newHarness(t)
	enc := rsaJWK("enc", &h.keys.rotated.PublicKey)
	enc["use"] = "enc"
	ec := map[string]any{
		"kty": "EC", "kid": "ec", "use": "sig", "crv": "P-256",
		"x": "f83OJ3D2xF1Bg8vub9tLe1gHMzV76e8Tus9uPHvRVEU", "y": "x_FEzRu9m36HLN_tue659LNpXW6pCyStikYjKIWI5a0",
	}
	h.issuer.serve(http.StatusOK, jwksBody(t,
		ec, enc, rsaJWK("weak", &h.keys.weak.PublicKey), rsaJWK(testKid, &h.keys.valid.PublicKey)))

	claims := goodClaims(testStart)
	if _, err := h.verify(rs256Token(t, h.keys.valid, testKid, claims)); err != nil {
		t.Fatalf("token signed by the valid key: %v", err)
	}
	rejected := map[string]string{
		"ec":   rs256Token(t, h.keys.attacker, "ec", claims),
		"enc":  rs256Token(t, h.keys.rotated, "enc", claims),
		"weak": rs256Token(t, h.keys.weak, "weak", claims),
	}
	for kid, token := range rejected {
		t.Run(kid, func(t *testing.T) {
			got, err := h.verify(token)
			wantOrdinaryError(t, got, err)
		})
	}
}

func TestVerifyReturnsClaims(t *testing.T) {
	h := newHarness(t)
	claims := goodClaims(testStart)
	for k, v := range map[string]any{
		"actor":        "octocat",
		"workflow_sha": "fedcba9876543210fedcba9876543210fedcba98",
		"environment":  map[string]any{"nested": []any{1, "two"}},
		"jti":          "5f0c2b1e",
		"sub":          "repo:o/r:pull_request",
	} {
		claims[k] = v
	}
	got, err := h.verify(rs256Token(t, h.keys.valid, testKid, claims))
	if err != nil {
		t.Fatalf("Verify: %v", err)
	}
	want := Claims{
		Repository:        "o/r",
		RepositoryID:      4242,
		RunID:             987654321,
		RunAttempt:        1,
		SHA:               "0123456789abcdef0123456789abcdef01234567",
		Ref:               "refs/pull/7/merge",
		EventName:         "pull_request",
		JobWorkflowRef:    "o/r/.github/workflows/archfit.yaml@refs/pull/7/merge",
		RunnerEnvironment: "github-hosted",
	}
	if got != want {
		t.Errorf("Claims = %+v, want %+v", got, want)
	}
}

func TestKeysAreCachedForOneHour(t *testing.T) {
	h := newHarness(t)
	for i := range 100 {
		h.clock.Set(testStart.Add(59 * time.Minute * time.Duration(i) / 99))
		if _, err := h.verify(h.goodToken(t)); err != nil {
			t.Fatalf("verification %d: %v", i, err)
		}
	}
	if got := h.issuer.fetches(); got != 1 {
		t.Fatalf("fetches in 59 minutes = %d, want 1", got)
	}
	h.clock.Set(testStart.Add(61 * time.Minute))
	if _, err := h.verify(h.goodToken(t)); err != nil {
		t.Fatalf("verification at 61 minutes: %v", err)
	}
	if got := h.issuer.fetches(); got != 2 {
		t.Errorf("fetches after 61 minutes = %d, want 2", got)
	}
}

func TestKeyRotationRefetchesOnce(t *testing.T) {
	h := newHarness(t)
	h.warm(t)
	h.clock.Advance(10 * time.Minute)
	h.issuer.serve(http.StatusOK, jwksBody(t, rsaJWK("k2", &h.keys.rotated.PublicKey)))

	for i := range 3 {
		if _, err := h.verify(rs256Token(t, h.keys.rotated, "k2", goodClaims(h.clock.Now()))); err != nil {
			t.Fatalf("rotated token, verification %d: %v", i, err)
		}
	}
	if got := h.issuer.fetches(); got != 2 {
		t.Errorf("fetches = %d, want 2 (initial and one refetch)", got)
	}
}

func TestConcurrentVerifySingleFetch(t *testing.T) {
	h := newHarness(t)
	h.issuer.delay = 50 * time.Millisecond
	token := h.goodToken(t)

	errs := make(chan error, 50)
	var wg sync.WaitGroup
	for range 50 {
		wg.Add(1)
		go func() {
			defer wg.Done()
			_, err := h.verify(token)
			errs <- err
		}()
	}
	wg.Wait()
	close(errs)
	for err := range errs {
		if err != nil {
			t.Errorf("concurrent Verify: %v", err)
		}
	}
	if got := h.issuer.fetches(); got != 1 {
		t.Errorf("fetches = %d, want 1", got)
	}
}

package auth

import (
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/rsa"
	"crypto/x509"
	"encoding/pem"
	"net/http"
	"net/url"
	"testing"
	"time"
)

func TestAppJWTClaimsAndSignature(t *testing.T) {
	clock := newFakeClock()
	s := newTestSource(t, clock, nil)

	jwt, err := s.AppJWT()
	if err != nil {
		t.Fatalf("AppJWT: %v", err)
	}
	header, claims := verifyJWT(t, jwt, &testKey().PublicKey)

	if header["alg"] != "RS256" || header["typ"] != "JWT" {
		t.Errorf("header = %v, want RS256/JWT", header)
	}
	if claims["iss"] != "12345" {
		t.Errorf("iss = %#v, want \"12345\"", claims["iss"])
	}
	wantIat := float64(clock.now().Unix() - 60)
	if claims["iat"] != wantIat {
		t.Errorf("iat = %v, want %v", claims["iat"], wantIat)
	}
	if claims["exp"] != wantIat+600 {
		t.Errorf("exp = %v, want %v", claims["exp"], wantIat+600)
	}
}

func TestParsePrivateKey(t *testing.T) {
	pkcs1 := testKeyPEM()
	pkcs8DER, err := x509.MarshalPKCS8PrivateKey(testKey())
	if err != nil {
		t.Fatal(err)
	}
	ecKey, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	ecDER, err := x509.MarshalPKCS8PrivateKey(ecKey)
	if err != nil {
		t.Fatal(err)
	}
	smallKey, err := rsa.GenerateKey(rand.Reader, 1024)
	if err != nil {
		t.Fatal(err)
	}
	small := pem.EncodeToMemory(&pem.Block{Type: "RSA PRIVATE KEY", Bytes: x509.MarshalPKCS1PrivateKey(smallKey)})

	tests := []struct {
		name   string
		pem    []byte
		wantOK bool
	}{
		{"PKCS#1", pkcs1, true},
		{"PKCS#8 RSA", pem.EncodeToMemory(&pem.Block{Type: "PRIVATE KEY", Bytes: pkcs8DER}), true},
		{"PKCS#8 EC", pem.EncodeToMemory(&pem.Block{Type: "PRIVATE KEY", Bytes: ecDER}), false},
		{"1024-bit RSA", small, false},
		{"garbage", []byte("not a key"), false},
		{"empty", nil, false},
		{"trailing data", append(append([]byte(nil), pkcs1...), "trailing garbage\n"...), true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			key, err := parsePrivateKey(tt.pem)
			if (err == nil) != tt.wantOK {
				t.Fatalf("parsePrivateKey error = %v, wantOK %v", err, tt.wantOK)
			}
			if tt.wantOK && !key.Equal(testKey()) {
				t.Error("parsed key differs from the encoded key")
			}
		})
	}
}

func TestTokenCacheWindow(t *testing.T) {
	clock := newFakeClock()
	gh := newFakeGitHub(t, newIssuer(clock).handler)
	s := newTestSource(t, clock, gh.client())

	first := mustToken(t, s, 77)
	clock.advance(54 * time.Minute)
	if got := mustToken(t, s, 77); got != first {
		t.Errorf("T+54m token = %q, want cached %q", got, first)
	}
	if n := gh.count(); n != 1 {
		t.Fatalf("mints after T and T+54m = %d, want 1", n)
	}

	clock.advance(time.Minute)
	second := mustToken(t, s, 77)
	if second == first {
		t.Error("T+55m served the token that is 5 minutes from expiry")
	}
	if n := gh.count(); n != 2 {
		t.Fatalf("mints after T+55m = %d, want 2", n)
	}

	clock.advance(time.Minute)
	if got := mustToken(t, s, 77); got != second {
		t.Errorf("T+56m token = %q, want new cached %q", got, second)
	}
	if n := gh.count(); n != 2 {
		t.Errorf("mints after T+56m = %d, want 2", n)
	}
}

func TestFailedMintIsNotCached(t *testing.T) {
	clock := newFakeClock()
	iss := newIssuer(clock)
	var gh *fakeGitHub
	gh = newFakeGitHub(t, func(w http.ResponseWriter, r *http.Request) {
		if gh.count() == 1 {
			writeRaw(w, http.StatusInternalServerError, "boom")
			return
		}
		iss.handler(w, r)
	})
	s := newTestSource(t, clock, gh.client())

	if token, err := s.InstallationToken(t.Context(), 77); err == nil || token != "" {
		t.Fatalf("first call = %q, %v; want an error and no token", token, err)
	}
	mustToken(t, s, 77)
	if n := gh.count(); n != 2 {
		t.Errorf("requests = %d, want 2", n)
	}
}

func TestAuthorizeURLEscapes(t *testing.T) {
	tests := []struct {
		name, state, redirect string
	}{
		{"plain", "abc123", "https://app.example/callback"},
		{"ampersand and equals", "a&b=c", "https://app.example/cb?next=/x&y=1"},
		{"space and plus", "a b+c", "https://app.example/c b"},
		{"unicode", "stäte-✓", "https://app.example/колбэк"},
		{"injection attempt", "x&scope=repo&client_id=evil", "https://app.example/cb#frag"},
	}
	_, oa, err := New(validCredentials(), nil)
	if err != nil {
		t.Fatal(err)
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			u, err := url.Parse(oa.AuthorizeURL(tt.state, tt.redirect))
			if err != nil {
				t.Fatalf("AuthorizeURL is not a URL: %v", err)
			}
			if u.Scheme != "https" || u.Host != webHost || u.Path != "/login/oauth/authorize" {
				t.Errorf("endpoint = %s://%s%s", u.Scheme, u.Host, u.Path)
			}
			q, err := url.ParseQuery(u.RawQuery)
			if err != nil {
				t.Fatalf("query: %v", err)
			}
			want := url.Values{"client_id": {"client-id"}, "state": {tt.state}, "redirect_uri": {tt.redirect}}
			if len(q) != len(want) {
				t.Errorf("query = %v, want exactly %v", q, want)
			}
			for k, v := range want {
				if got := q[k]; len(got) != 1 || got[0] != v[0] {
					t.Errorf("%s = %q, want %q", k, got, v)
				}
			}
			if q.Has("scope") {
				t.Error("scope parameter present")
			}
		})
	}
}

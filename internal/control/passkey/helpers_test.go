package passkey

import (
	"context"
	"crypto"
	"crypto/ecdsa"
	"crypto/ed25519"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/rsa"
	"crypto/sha256"
	"encoding/base64"
	"encoding/binary"
	"encoding/json"
	"errors"
	"log/slog"
	"math/big"
	"os"
	"slices"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/go-webauthn/webauthn/protocol"
	"github.com/go-webauthn/webauthn/protocol/webauthncbor"
	"github.com/go-webauthn/webauthn/protocol/webauthncose"

	"github.com/alexei-led/archfit-app/internal/github/api"
	"github.com/alexei-led/archfit-app/internal/store"
)

const (
	appURL    = "https://app.example"
	appHost   = "app.example"
	appOrigin = "https://app.example"
)

var (
	ctx = context.Background()

	// repo is the repository every test authorizes; its installation is the tenant.
	repo = api.Repo{RepoRef: api.RepoRef{InstallationID: 7, FullName: "Acme/App"}, ID: 70, DefaultBranch: "main"}

	alice = User{ID: 101, Login: "alice"} // an owner named in the target policy
	bob   = User{ID: 102, Login: "bob"}   // not an owner

	approve = Purpose{Action: "approve", Request: 431, Digest: "sha256:" + strings.Repeat("a", 64)}
)

func TestMain(m *testing.M) {
	slog.SetDefault(slog.New(slog.DiscardHandler))
	os.Exit(m.Run())
}

// authenticator is a software authenticator: it makes the bytes a browser and a security key make, with knobs that
// break one property at a time. CBOR and COSE come from go-webauthn's own encoders.
type authenticator struct {
	t      *testing.T
	alg    webauthncose.COSEAlgorithmIdentifier
	signer crypto.Signer
	cose   []byte
	id     []byte
	handle []byte // learned from the creation options

	flags  protocol.AuthenticatorFlags
	origin string
	rpID   string
	count  uint32
	step   uint32 // added to count before each assertion
}

func newAuthenticator(t *testing.T, alg webauthncose.COSEAlgorithmIdentifier) *authenticator {
	t.Helper()
	a := &authenticator{
		t: t, alg: alg, id: randomBytes(t, 16), origin: appOrigin, rpID: appHost, step: 1,
		flags: protocol.FlagUserPresent | protocol.FlagUserVerified,
	}
	var key any
	var err error
	switch alg {
	case webauthncose.AlgES256:
		k, gerr := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
		if gerr != nil {
			t.Fatal(gerr)
		}
		point, perr := k.PublicKey.Bytes()
		if perr != nil {
			t.Fatal(perr)
		}
		a.signer = k
		key = webauthncose.EC2PublicKeyData{
			PublicKeyData: webauthncose.PublicKeyData{KeyType: int64(webauthncose.EllipticKey), Algorithm: int64(alg)},
			Curve:         int64(webauthncose.P256), XCoord: point[1:33], YCoord: point[33:],
		}
	case webauthncose.AlgEd25519, webauthncose.AlgEdDSA:
		pub, priv, gerr := ed25519.GenerateKey(rand.Reader)
		if gerr != nil {
			t.Fatal(gerr)
		}
		a.signer = priv
		key = webauthncose.OKPPublicKeyData{
			PublicKeyData: webauthncose.PublicKeyData{KeyType: int64(webauthncose.OctetKey), Algorithm: int64(alg)},
			Curve:         int64(webauthncose.Ed25519), XCoord: pub,
		}
	case webauthncose.AlgRS256:
		k, gerr := rsa.GenerateKey(rand.Reader, 2048)
		if gerr != nil {
			t.Fatal(gerr)
		}
		a.signer = k
		key = webauthncose.RSAPublicKeyData{
			PublicKeyData: webauthncose.PublicKeyData{KeyType: int64(webauthncose.RSAKey), Algorithm: int64(alg)},
			Modulus:       k.N.Bytes(), Exponent: big.NewInt(int64(k.E)).Bytes(),
		}
	default:
		t.Fatalf("no software key for algorithm %d", alg)
	}
	if a.cose, err = webauthncbor.Marshal(key); err != nil {
		t.Fatal(err)
	}
	return a
}

func randomBytes(t *testing.T, n int) []byte {
	t.Helper()
	b := make([]byte, n)
	if _, err := rand.Read(b); err != nil {
		t.Fatal(err)
	}
	return b
}

func b64(b []byte) string { return base64.RawURLEncoding.EncodeToString(b) }

func (a *authenticator) authData(attested bool) []byte {
	rpIDHash := sha256.Sum256([]byte(a.rpID))
	flags := a.flags
	if attested {
		flags |= protocol.FlagAttestedCredentialData
	}
	b := append(rpIDHash[:], byte(flags))
	b = binary.BigEndian.AppendUint32(b, a.count)
	if attested {
		b = append(b, make([]byte, 16)...) // AAGUID: none
		b = binary.BigEndian.AppendUint16(b, uint16(len(a.id)))
		b = append(b, a.id...)
		b = append(b, a.cose...)
	}
	return b
}

func (a *authenticator) clientData(typ, challenge string) []byte {
	b, err := json.Marshal(map[string]any{"type": typ, "challenge": challenge, "origin": a.origin, "crossOrigin": false})
	if err != nil {
		a.t.Fatal(err)
	}
	return b
}

// create answers navigator.credentials.create with attestation "none".
func (a *authenticator) create(options json.RawMessage) []byte {
	a.t.Helper()
	var o struct {
		PublicKey struct {
			Challenge string `json:"challenge"`
			User      struct {
				ID string `json:"id"`
			} `json:"user"`
		} `json:"publicKey"`
	}
	if err := json.Unmarshal(options, &o); err != nil {
		a.t.Fatalf("creation options: %v", err)
	}
	handle, err := base64.RawURLEncoding.DecodeString(o.PublicKey.User.ID)
	if err != nil {
		a.t.Fatalf("user handle: %v", err)
	}
	a.handle = handle
	attestation, err := webauthncbor.Marshal(struct {
		Fmt      string         `cbor:"fmt"`
		AttStmt  map[string]any `cbor:"attStmt"`
		AuthData []byte         `cbor:"authData"`
	}{"none", map[string]any{}, a.authData(true)})
	if err != nil {
		a.t.Fatal(err)
	}
	return a.respond(map[string]any{
		"clientDataJSON":    b64(a.clientData("webauthn.create", o.PublicKey.Challenge)),
		"attestationObject": b64(attestation),
		"transports":        []string{"internal"},
	})
}

// get answers navigator.credentials.get.
func (a *authenticator) get(options json.RawMessage) []byte {
	a.t.Helper()
	var o struct {
		PublicKey struct {
			Challenge string `json:"challenge"`
		} `json:"publicKey"`
	}
	if err := json.Unmarshal(options, &o); err != nil {
		a.t.Fatalf("request options: %v", err)
	}
	a.count += a.step
	authData := a.authData(false)
	clientData := a.clientData("webauthn.get", o.PublicKey.Challenge)
	clientHash := sha256.Sum256(clientData)
	signed := append(slices.Clone(authData), clientHash[:]...)
	var sig []byte
	var err error
	switch k := a.signer.(type) {
	case ed25519.PrivateKey:
		sig = ed25519.Sign(k, signed)
	default:
		digest := sha256.Sum256(signed)
		sig, err = a.signer.Sign(rand.Reader, digest[:], crypto.SHA256)
	}
	if err != nil {
		a.t.Fatal(err)
	}
	return a.respond(map[string]any{
		"clientDataJSON":    b64(clientData),
		"authenticatorData": b64(authData),
		"signature":         b64(sig),
		"userHandle":        b64(a.handle),
	})
}

func (a *authenticator) respond(response map[string]any) []byte {
	b, err := json.Marshal(map[string]any{
		"id": b64(a.id), "rawId": b64(a.id), "type": "public-key", "response": response,
		"clientExtensionResults": map[string]any{},
	})
	if err != nil {
		a.t.Fatal(err)
	}
	return b
}

type fakeOwners struct {
	mu        sync.Mutex
	logins    []string
	hasPolicy bool
	err       error
}

func (f *fakeOwners) Owners(_ context.Context, r api.Repo) ([]string, bool, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if r.FullName == "" {
		return nil, false, errors.New("fake owners: no repository")
	}
	return slices.Clone(f.logins), f.hasPolicy, f.err
}

type openedIssue struct {
	repo        api.RepoRef
	title, body string
	assignees   []string
}

type fakeIssues struct {
	mu     sync.Mutex
	opened []openedIssue
	err    error
}

func (f *fakeIssues) Open(_ context.Context, r api.RepoRef, title, body string, assignees []string) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.err != nil {
		return f.err
	}
	f.opened = append(f.opened, openedIssue{r, title, body, slices.Clone(assignees)})
	return nil
}

func (f *fakeIssues) all() []openedIssue {
	f.mu.Lock()
	defer f.mu.Unlock()
	return slices.Clone(f.opened)
}

// env is the package behind a memory Store, a fake clock and the two ports.
type env struct {
	t      *testing.T
	store  store.Store
	owners *fakeOwners
	issues *fakeIssues
	now    time.Time
	pk     Passkeys
}

func newEnv(t *testing.T) *env {
	t.Helper()
	return newEnvAt(t, appURL)
}

// newEnvAt is newEnv for the App at baseURL.
func newEnvAt(t *testing.T, baseURL string) *env {
	t.Helper()
	e := &env{
		t: t, store: store.NewMemory(), issues: &fakeIssues{},
		owners: &fakeOwners{logins: []string{"Alice", "carol"}, hasPolicy: true},
		now:    time.Date(2026, 11, 12, 10, 0, 0, 0, time.UTC),
	}
	pk, err := New(Config{BaseURL: baseURL}, e.store, e.owners, e.issues, func() time.Time { return e.now })
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	e.pk = pk
	return e
}

func (e *env) register(u User, a *authenticator, token string) (Passkey, error) {
	options, err := e.pk.BeginRegistration(ctx, repo, u, token)
	if err != nil {
		return Passkey{}, err
	}
	return e.pk.FinishRegistration(ctx, repo, u, a.create(options))
}

func (e *env) mustRegister(u User, a *authenticator, token string) Passkey {
	e.t.Helper()
	p, err := e.register(u, a, token)
	if err != nil {
		e.t.Fatalf("register: %v", err)
	}
	return p
}

func (e *env) stepUp(u User, a *authenticator, p Purpose) (string, error) {
	options, err := e.pk.BeginStepUp(ctx, repo, u, p)
	if err != nil {
		return "", err
	}
	return e.pk.FinishStepUp(ctx, repo, u, a.get(options))
}

func (e *env) mustStepUp(u User, a *authenticator, p Purpose) string {
	e.t.Helper()
	token, err := e.stepUp(u, a, p)
	if err != nil {
		e.t.Fatalf("step-up: %v", err)
	}
	return token
}

func (e *env) list(u User) []Passkey {
	e.t.Helper()
	got, err := e.pk.List(ctx, repo, u.ID)
	if err != nil {
		e.t.Fatalf("List: %v", err)
	}
	return got
}

func wantErr(t *testing.T, err, want error) {
	t.Helper()
	if !errors.Is(err, want) {
		t.Fatalf("error = %v, want %v", err, want)
	}
}

// wantNoUserVerification wants a ceremony go-webauthn refused because the authenticator did not verify the user.
func wantNoUserVerification(t *testing.T, err error) {
	t.Helper()
	wantErr(t, err, ErrCeremony)
	var pe *protocol.Error
	if !errors.As(err, &pe) || !strings.Contains(pe.DevInfo, "User verification required") {
		t.Fatalf("refused for another reason: %v (%+v)", err, pe)
	}
}

package passkey

import (
	"bytes"
	"context"
	"crypto/rand"
	"crypto/sha256"
	"crypto/subtle"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"net/url"
	"regexp"
	"slices"
	"strings"
	"time"

	"github.com/go-webauthn/webauthn/protocol"
	"github.com/go-webauthn/webauthn/protocol/webauthncose"
	"github.com/go-webauthn/webauthn/webauthn"

	"github.com/alexei-led/archfit-app/internal/github/api"
	"github.com/alexei-led/archfit-app/internal/store"
)

const (
	handleSize = 32 // bytes of a user handle
	tokenSize  = 32 // bytes of a step-up token

	// maxAssignees is GitHub's limit of assignees on one issue.
	maxAssignees = 10
)

// credentialParameters are the algorithms of M2 D9: ES256, Ed25519 (under its own id and under EdDSA, the id
// authenticators still send for it), RS256.
var credentialParameters = []protocol.CredentialParameter{
	{Type: protocol.PublicKeyCredentialType, Algorithm: webauthncose.AlgES256},
	{Type: protocol.PublicKeyCredentialType, Algorithm: webauthncose.AlgEd25519},
	{Type: protocol.PublicKeyCredentialType, Algorithm: webauthncose.AlgEdDSA},
	{Type: protocol.PublicKeyCredentialType, Algorithm: webauthncose.AlgRS256},
}

var (
	editSHA        = regexp.MustCompile("^[0-9a-f]{40}$")
	editDigest     = regexp.MustCompile("^sha256:[0-9a-f]{64}$")
	actionPattern  = regexp.MustCompile(`^[a-z][a-z-]{0,31}$`)
	digestPattern  = regexp.MustCompile(`^[\x21-\x7e]{0,128}$`)
	passkeyPattern = regexp.MustCompile(`^[A-Za-z0-9_-]*$`)
)

// maxPasskeyID is the longest passkey id in base64url: WebAuthn ids are at most 1023 bytes.
const maxPasskeyID = 1364

type service struct {
	wa     *webauthn.WebAuthn
	store  store.Store
	owners Owners
	issues Issues
	now    func() time.Time
}

var _ Passkeys = (*service)(nil)

// New fails unless BaseURL is an absolute https URL (http only for localhost) and every dependency is given.
func New(c Config, s store.Store, owners Owners, issues Issues, now func() time.Time) (Passkeys, error) {
	if s == nil || owners == nil || issues == nil || now == nil {
		return nil, errors.New("passkey: Store, Owners, Issues and the clock are required")
	}
	rpID, origin, err := relyingParty(c.BaseURL)
	if err != nil {
		return nil, err
	}
	wa, err := webauthn.New(&webauthn.Config{
		RPID:                  rpID,
		RPDisplayName:         "Archfit",
		RPOrigins:             []string{origin},
		AttestationPreference: protocol.PreferNoAttestation,
		AuthenticatorSelection: protocol.AuthenticatorSelection{
			RequireResidentKey: protocol.ResidentKeyNotRequired(),
			ResidentKey:        protocol.ResidentKeyRequirementPreferred,
			UserVerification:   protocol.VerificationRequired,
		},
		Timeouts: webauthn.TimeoutsConfig{
			Login:        webauthn.TimeoutConfig{Timeout: TTL, TimeoutUVD: TTL},
			Registration: webauthn.TimeoutConfig{Timeout: TTL, TimeoutUVD: TTL},
		},
	})
	if err != nil {
		return nil, fmt.Errorf("passkey: relying party: %w", err)
	}
	return &service{wa: wa, store: s, owners: owners, issues: issues, now: now}, nil
}

// relyingParty derives the relying party id (the host) and the one origin (scheme and host) from the App's URL.
func relyingParty(baseURL string) (id, origin string, err error) {
	u, err := url.Parse(baseURL)
	if err != nil || u.Hostname() == "" || u.User != nil {
		return "", "", errors.New("passkey: BaseURL must be an absolute https URL")
	}
	switch host := u.Hostname(); {
	case u.Scheme == "https":
	case u.Scheme == "http" && (host == "localhost" || host == "127.0.0.1" || host == "::1"):
	default:
		return "", "", errors.New("passkey: BaseURL must use https (http only for localhost)")
	}
	return u.Hostname(), u.Scheme + "://" + u.Host, nil
}

func (s *service) BeginRegistration(ctx context.Context, r api.Repo, u User, token string) (json.RawMessage, error) {
	t, err := tenantOf(r, u.ID)
	if err != nil {
		return nil, err
	}
	if _, err := s.mayRegister(ctx, r, u); err != nil {
		return nil, err
	}
	handle, err := random(handleSize)
	if err != nil {
		return nil, err
	}
	var options []byte
	err = store.Update(ctx, s.store, t, []string{passkeyKey(u.ID), challengeKey(u.ID)}, func(cur [][]byte) ([][]byte, error) {
		rec, err := readRecord(cur[0])
		if err != nil {
			return nil, err
		}
		if rec != nil {
			if len(rec.Passkeys) >= MaxPasskeys {
				return nil, ErrTooMany
			}
			if token == "" {
				return nil, ErrNeedsPasskey
			}
			if _, err := s.spend(cur[1], token, Purpose{Repo: repoOf(r), Action: ActionRegister}); err != nil {
				return nil, err
			}
			handle = rec.Handle
		}
		creds := rec.credentials()
		exclusions := make([]protocol.CredentialDescriptor, 0, len(creds))
		for i := range creds {
			exclusions = append(exclusions, creds[i].Descriptor())
		}
		creation, session, err := s.wa.BeginRegistration(waUser{handle: handle, login: u.Login, creds: creds},
			webauthn.WithCredentialParameters(credentialParameters),
			webauthn.WithExclusions(exclusions),
			webauthn.WithConveyancePreference(protocol.PreferNoAttestation),
		)
		if err != nil {
			return nil, fmt.Errorf("passkey: begin registration: %w", err)
		}
		next, err := s.ceremony(kindRegister, r, session, nil)
		if err != nil {
			return nil, err
		}
		if options, err = json.Marshal(creation); err != nil {
			return nil, fmt.Errorf("passkey: encode the creation options: %w", err)
		}
		return [][]byte{cur[0], next}, nil
	})
	if err != nil {
		return nil, err
	}
	return options, nil
}

func (s *service) FinishRegistration(ctx context.Context, r api.Repo, u User, credential []byte) (p Passkey, err error) {
	defer func() { logOutcome(ctx, "register", r, u.ID, err) }()
	t, err := tenantOf(r, u.ID)
	if err != nil {
		return Passkey{}, err
	}
	owners, err := s.mayRegister(ctx, r, u)
	if err != nil {
		return Passkey{}, err
	}
	parsed, err := protocol.ParseCredentialCreationResponseBytes(credential)
	if err != nil {
		return Passkey{}, fmt.Errorf("%w: %w", ErrCeremony, err)
	}
	keys := []string{passkeyKey(u.ID), challengeKey(u.ID)}
	cur, err := s.read(ctx, t, keys)
	if err != nil {
		return Passkey{}, err
	}
	rec, err := readRecord(cur[0])
	if err != nil {
		return Passkey{}, err
	}
	_, session, err := s.open(cur[1], kindRegister, r)
	if err != nil {
		return Passkey{}, err
	}
	switch {
	case rec != nil && !bytes.Equal(rec.Handle, session.UserID):
		return Passkey{}, ErrCeremony // the record changed since the ceremony began
	case rec != nil && len(rec.Passkeys) >= MaxPasskeys:
		return Passkey{}, ErrTooMany
	}
	created, err := s.wa.CreateCredential(waUser{handle: session.UserID, login: u.Login, creds: rec.credentials()}, session, parsed)
	if err != nil {
		return Passkey{}, fmt.Errorf("%w: %w", ErrCeremony, err)
	}
	if !created.Flags.UserVerified || rec.index(created.ID) >= 0 {
		return Passkey{}, ErrCeremony
	}
	next := record{Handle: session.UserID}
	if rec != nil {
		next = *rec
	}
	added := storedOf(created, s.now())
	next.Passkeys = append(slices.Clone(next.Passkeys), added)
	value, err := next.encode()
	if err != nil {
		return Passkey{}, err
	}
	// The issue comes before the write: a passkey nobody was told about must not exist. A write that fails after it
	// leaves an issue about a passkey that is not there, which is harmless.
	title, body := registeredIssue(u.Login, r, added, s.now())
	if err := s.issues.Open(ctx, r.RepoRef, title, body, assignees(u.Login, owners)); err != nil {
		return Passkey{}, fmt.Errorf("passkey: open the registration issue: %w", err)
	}
	if err := s.swap(ctx, t, keys, cur, [][]byte{value, nil}); err != nil {
		return Passkey{}, err
	}
	return passkeyOf(added), nil
}

func (s *service) BeginStepUp(ctx context.Context, r api.Repo, u User, p Purpose) (json.RawMessage, error) {
	t, err := tenantOf(r, u.ID)
	if err != nil {
		return nil, err
	}
	if p.Action == "edit" && (p.Request != 0 || !editSHA.MatchString(p.Target) || !editDigest.MatchString(p.BaseDigest) || !editDigest.MatchString(p.Digest)) {
		return nil, ErrInvalid
	}
	if len(p.Target) > 40 || !digestPattern.MatchString(p.BaseDigest) {
		return nil, ErrInvalid
	}
	if !actionPattern.MatchString(p.Action) || p.Request < 0 || !digestPattern.MatchString(p.Digest) ||
		len(p.Passkey) > maxPasskeyID || !passkeyPattern.MatchString(p.Passkey) {
		return nil, ErrInvalid
	}
	p.Repo = repoOf(r)
	var options []byte
	err = store.Update(ctx, s.store, t, []string{passkeyKey(u.ID), challengeKey(u.ID)}, func(cur [][]byte) ([][]byte, error) {
		rec, err := readRecord(cur[0])
		if err != nil {
			return nil, err
		}
		if rec == nil {
			return nil, ErrNoPasskey
		}
		assertion, session, err := s.wa.BeginLogin(waUser{handle: rec.Handle, login: u.Login, creds: rec.credentials()},
			webauthn.WithUserVerification(protocol.VerificationRequired))
		if err != nil {
			return nil, fmt.Errorf("passkey: begin step-up: %w", err)
		}
		next, err := s.ceremony(kindAssert, r, session, &p)
		if err != nil {
			return nil, err
		}
		if options, err = json.Marshal(assertion); err != nil {
			return nil, fmt.Errorf("passkey: encode the request options: %w", err)
		}
		return [][]byte{cur[0], next}, nil
	})
	if err != nil {
		return nil, err
	}
	return options, nil
}

func (s *service) FinishStepUp(ctx context.Context, r api.Repo, u User, credential []byte) (token string, err error) {
	defer func() { logOutcome(ctx, "step-up", r, u.ID, err) }()
	t, err := tenantOf(r, u.ID)
	if err != nil {
		return "", err
	}
	parsed, err := protocol.ParseCredentialRequestResponseBytes(credential)
	if err != nil {
		return "", fmt.Errorf("%w: %w", ErrCeremony, err)
	}
	raw, err := random(tokenSize)
	if err != nil {
		return "", err
	}
	token = base64.RawURLEncoding.EncodeToString(raw)
	hash := sha256.Sum256([]byte(token))
	err = store.Update(ctx, s.store, t, []string{passkeyKey(u.ID), challengeKey(u.ID)}, func(cur [][]byte) ([][]byte, error) {
		rec, err := readRecord(cur[0])
		if err != nil {
			return nil, err
		}
		if rec == nil {
			return nil, ErrNoPasskey
		}
		c, session, err := s.open(cur[1], kindAssert, r)
		if err != nil {
			return nil, err
		}
		used, err := s.wa.ValidateLogin(waUser{handle: rec.Handle, login: u.Login, creds: rec.credentials()}, session, parsed)
		if err != nil {
			return nil, fmt.Errorf("%w: %w", ErrCeremony, err)
		}
		if used.Authenticator.CloneWarning {
			return nil, ErrCounter
		}
		i := rec.index(used.ID)
		if i < 0 || !parsed.Response.AuthenticatorData.Flags.HasUserVerified() {
			return nil, ErrCeremony
		}
		rec.Passkeys[i].SignCount = used.Authenticator.SignCount
		rec.Passkeys[i].Flags = used.Flags.MsgpByte()
		rec.Passkeys[i].LastUsed = s.now().UTC()
		value, err := rec.encode()
		if err != nil {
			return nil, err
		}
		next, err := (&challenge{
			Kind: kindToken, Repo: c.Repo, At: s.now().UTC(), Purpose: c.Purpose, TokenHash: hash[:], Credential: used.ID,
		}).encode()
		if err != nil {
			return nil, err
		}
		return [][]byte{value, next}, nil
	})
	if err != nil {
		return "", err
	}
	return token, nil
}

func (s *service) Redeem(ctx context.Context, r api.Repo, userID int64, token string, p Purpose) (id string, err error) {
	defer func() { logOutcome(ctx, "redeem", r, userID, err) }()
	t, err := tenantOf(r, userID)
	if err != nil {
		return "", err
	}
	p.Repo = repoOf(r)
	var credential []byte
	err = store.Update(ctx, s.store, t, []string{challengeKey(userID)}, func(cur [][]byte) ([][]byte, error) {
		var err error
		credential, err = s.spend(cur[0], token, p)
		return [][]byte{nil}, err
	})
	if err != nil {
		return "", err
	}
	return base64.RawURLEncoding.EncodeToString(credential), nil
}

func (s *service) List(ctx context.Context, r api.Repo, userID int64) ([]Passkey, error) {
	t, err := tenantOf(r, userID)
	if err != nil {
		return nil, err
	}
	raw, err := s.store.Get(ctx, t, passkeyKey(userID))
	if errors.Is(err, store.ErrNotFound) {
		return []Passkey{}, nil
	}
	if err != nil {
		return nil, err
	}
	rec, err := readRecord(raw)
	if err != nil {
		return nil, err
	}
	out := make([]Passkey, 0, len(rec.Passkeys))
	for _, p := range rec.Passkeys {
		out = append(out, passkeyOf(p))
	}
	return out, nil
}

func (s *service) Remove(ctx context.Context, r api.Repo, u User, passkeyID, token string) (err error) {
	defer func() { logOutcome(ctx, "remove", r, u.ID, err) }()
	t, err := tenantOf(r, u.ID)
	if err != nil {
		return err
	}
	id, err := base64.RawURLEncoding.DecodeString(passkeyID)
	if err != nil {
		return ErrUnknown
	}
	keys := []string{passkeyKey(u.ID), challengeKey(u.ID)}
	cur, err := s.read(ctx, t, keys)
	if err != nil {
		return err
	}
	rec, err := readRecord(cur[0])
	if err != nil {
		return err
	}
	i := rec.index(id)
	if i < 0 {
		return ErrUnknown
	}
	if _, err := s.spend(cur[1], token, Purpose{Repo: repoOf(r), Action: ActionRemove, Passkey: passkeyID}); err != nil {
		return err
	}
	var value []byte // nil: the last passkey goes, and the record with it
	if len(rec.Passkeys) > 1 {
		rec.Passkeys = slices.Delete(slices.Clone(rec.Passkeys), i, i+1)
		if value, err = rec.encode(); err != nil {
			return err
		}
	} else {
		owners, _, err := s.owners.Owners(ctx, r)
		if err != nil {
			return fmt.Errorf("passkey: read the owners: %w", err)
		}
		title, body := removedIssue(u.Login, r, rec.Passkeys[i], s.now())
		if err := s.issues.Open(ctx, r.RepoRef, title, body, assignees(u.Login, owners)); err != nil {
			return fmt.Errorf("passkey: open the removal issue: %w", err)
		}
	}
	return s.swap(ctx, t, keys, cur, [][]byte{value, nil})
}

// mayRegister applies "who registers" (M2 task 18): an owner named in the target policy, or, before the first
// policy, a repository administrator. It returns the owners for the registration issue.
func (s *service) mayRegister(ctx context.Context, r api.Repo, u User) ([]string, error) {
	owners, hasPolicy, err := s.owners.Owners(ctx, r)
	if err != nil {
		return nil, fmt.Errorf("passkey: read the owners: %w", err)
	}
	switch {
	case u.Login == "":
		return nil, ErrNotOwner
	case hasPolicy && slices.ContainsFunc(owners, func(o string) bool { return strings.EqualFold(o, u.Login) }):
		return owners, nil
	case !hasPolicy && u.Admin:
		return nil, nil
	}
	return nil, ErrNotOwner
}

// ceremony encodes a new challenge for a ceremony go-webauthn began. Its expiry is this package's, against the
// injected clock: the library's own check reads the wall clock, so its Expires stays zero.
func (s *service) ceremony(kind string, r api.Repo, session *webauthn.SessionData, p *Purpose) ([]byte, error) {
	session.Expires = time.Time{}
	raw, err := json.Marshal(session)
	if err != nil {
		return nil, fmt.Errorf("passkey: encode the session: %w", err)
	}
	return (&challenge{Kind: kind, Repo: repoOf(r), At: s.now().UTC(), Session: raw, Purpose: p}).encode()
}

// open returns the session of the open ceremony of kind for r, or ErrCeremony when there is none: never begun,
// replaced by a newer ceremony, consumed, begun for another repository, or older than TTL.
func (s *service) open(raw []byte, kind string, r api.Repo) (*challenge, webauthn.SessionData, error) {
	var session webauthn.SessionData
	c := readChallenge(raw)
	if c == nil || c.Kind != kind || c.Repo != repoOf(r) || s.expired(c.At) {
		return nil, session, ErrCeremony
	}
	if err := json.Unmarshal(c.Session, &session); err != nil {
		return nil, session, ErrCeremony
	}
	return c, session, nil
}

// spend checks a step-up token against the challenge bytes and returns the passkey that made its assertion. The
// caller writes the challenge key in the same swap, which consumes the token.
func (s *service) spend(raw []byte, token string, want Purpose) ([]byte, error) {
	c := readChallenge(raw)
	if c == nil || c.Kind != kindToken || c.Purpose == nil || token == "" {
		return nil, ErrToken
	}
	sum := sha256.Sum256([]byte(token))
	if subtle.ConstantTimeCompare(sum[:], c.TokenHash) != 1 || s.expired(c.At) || *c.Purpose != want {
		return nil, ErrToken
	}
	return c.Credential, nil
}

func (s *service) expired(at time.Time) bool { return !s.now().Before(at.Add(TTL)) }

// read gets the keys; a missing key is nil.
func (s *service) read(ctx context.Context, t store.Tenant, keys []string) ([][]byte, error) {
	cur := make([][]byte, len(keys))
	for i, k := range keys {
		v, err := s.store.Get(ctx, t, k)
		switch {
		case errors.Is(err, store.ErrNotFound):
		case err != nil:
			return nil, err
		default:
			cur[i] = v
		}
	}
	return cur, nil
}

// swap writes next over the bytes read, once. A key that moved in between means another ceremony began or this one
// was used: the step fails and the user starts again, so a response is never applied twice.
func (s *service) swap(ctx context.Context, t store.Tenant, keys []string, cur, next [][]byte) error {
	swaps := make([]store.Swap, len(keys))
	for i, k := range keys {
		swaps[i] = store.Swap{Key: k, Old: cur[i], Value: next[i]}
	}
	ok, err := s.store.CompareAndSwapAll(ctx, t, swaps)
	if err != nil {
		return err
	}
	if !ok {
		return ErrCeremony
	}
	return nil
}

func tenantOf(r api.Repo, userID int64) (store.Tenant, error) {
	if r.InstallationID <= 0 || userID <= 0 {
		return 0, errors.New("passkey: an installation and a user are required")
	}
	return store.Tenant(r.InstallationID), nil
}

// repoOf is the canonical repository of a ceremony and a Purpose: lower case, as GitHub compares names.
func repoOf(r api.Repo) string { return strings.ToLower(r.FullName) }

func random(n int) ([]byte, error) {
	b := make([]byte, n)
	if _, err := rand.Read(b); err != nil {
		return nil, fmt.Errorf("passkey: random: %w", err)
	}
	return b, nil
}

func passkeyOf(p stored) Passkey {
	return Passkey{ID: base64.RawURLEncoding.EncodeToString(p.ID), Created: p.Created, LastUsed: p.LastUsed}
}

// assignees are the user first, then the owners, without repeats, at most GitHub's limit.
func assignees(login string, owners []string) []string {
	out := []string{login}
	for _, o := range owners {
		if !slices.ContainsFunc(out, func(x string) bool { return strings.EqualFold(x, o) }) {
			out = append(out, o)
		}
	}
	if len(out) > maxAssignees {
		out = out[:maxAssignees]
	}
	return out
}

func logOutcome(ctx context.Context, step string, r api.Repo, userID int64, err error) {
	outcome := "ok"
	for _, o := range []struct {
		err  error
		name string
	}{
		{ErrNotOwner, "not_owner"}, {ErrNeedsPasskey, "needs_passkey"}, {ErrNoPasskey, "no_passkey"},
		{ErrCeremony, "ceremony"}, {ErrCounter, "counter"}, {ErrToken, "token"}, {ErrTooMany, "too_many"},
		{ErrUnknown, "unknown"}, {ErrInvalid, "invalid"}, {ErrPolicy, "policy"},
	} {
		if errors.Is(err, o.err) {
			outcome = o.name
			break
		}
	}
	if err != nil && outcome == "ok" {
		outcome = "error"
	}
	level := slog.LevelInfo
	if outcome == "counter" || outcome == "error" {
		level = slog.LevelWarn
	}
	slog.Default().Log(ctx, level, "passkey: "+step,
		"user_id", userID, "repository_id", r.ID, "installation_id", r.InstallationID, "outcome", outcome)
}

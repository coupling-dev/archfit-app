package passkey

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"strconv"
	"time"

	"github.com/go-webauthn/webauthn/protocol"
	"github.com/go-webauthn/webauthn/webauthn"
)

// Schema names of the two values. A read refuses any other name.
const (
	schemaRecord    = "archfit-app.passkey.v1"
	schemaChallenge = "archfit-app.challenge.v1"
)

// maxValueBytes is the Store's value limit (store/module.md).
const maxValueBytes = 64 << 10

// Kinds of the challenge record: a ceremony waiting for its finish, or a token waiting for its redeem.
const (
	kindRegister = "register"
	kindAssert   = "assert"
	kindToken    = "token"
)

// passkeyKey holds the user's passkeys in the installation (record).
func passkeyKey(userID int64) string { return "v4/passkey/" + strconv.FormatInt(userID, 10) }

// challengeKey holds the user's one open ceremony or token (challenge). A new ceremony replaces the old one, so the
// key count stays bounded although the Store has no expiry: in two tabs at once, the older ceremony fails.
func challengeKey(userID int64) string { return "v4/challenge/" + strconv.FormatInt(userID, 10) }

// record is a user's passkeys in one installation. It exists only while it holds at least one passkey.
type record struct {
	Schema   string   `json:"schema"`
	Handle   []byte   `json:"handle"` // the WebAuthn user handle: random, one per user in an installation
	Passkeys []stored `json:"passkeys"`
}

// stored is one passkey: the App's own type, so a library upgrade never changes what is stored.
type stored struct {
	ID         []byte    `json:"id"`
	PublicKey  []byte    `json:"public_key"` // COSE key, as the authenticator gave it
	Flags      uint8     `json:"flags"`      // authenticator data flags, as go-webauthn keeps them
	AAGUID     []byte    `json:"aaguid,omitempty"`
	SignCount  uint32    `json:"sign_count"`
	Transports []string  `json:"transports,omitempty"`
	Created    time.Time `json:"created"`
	LastUsed   time.Time `json:"last_used,omitzero"`
}

// challenge is the user's open ceremony (register, assert) or the token one assertion gave (token).
type challenge struct {
	Schema string    `json:"schema"`
	Kind   string    `json:"kind"`
	Repo   string    `json:"repo"` // lower-case owner/name the ceremony was begun for
	At     time.Time `json:"at"`   // when the ceremony began; for a token, when the assertion was verified
	// Session is go-webauthn's SessionData, kept whole as the library asks. It lives at most TTL.
	Session    json.RawMessage `json:"session,omitempty"`
	Purpose    *Purpose        `json:"purpose,omitempty"`    // assert and token
	TokenHash  []byte          `json:"token_hash,omitempty"` // token: SHA-256 of the token
	Credential []byte          `json:"credential,omitempty"` // token: the passkey that made the assertion
}

func encode(v any) ([]byte, error) {
	var b bytes.Buffer
	enc := json.NewEncoder(&b)
	enc.SetEscapeHTML(false)
	if err := enc.Encode(v); err != nil {
		return nil, fmt.Errorf("passkey: encode: %w", err)
	}
	raw := bytes.TrimSuffix(b.Bytes(), []byte("\n"))
	if len(raw) > maxValueBytes {
		return nil, errors.New("passkey: value over the 64 KiB limit")
	}
	return raw, nil
}

// decode reads raw into v strictly: the schema must be want, unknown fields and trailing data are refused. Errors
// never quote the value.
func decode(raw []byte, want string, v any) error {
	var head struct {
		Schema string `json:"schema"`
	}
	if err := json.Unmarshal(raw, &head); err != nil || head.Schema != want {
		return fmt.Errorf("passkey: not a %s value", want)
	}
	dec := json.NewDecoder(bytes.NewReader(raw))
	dec.DisallowUnknownFields()
	if err := dec.Decode(v); err != nil {
		return fmt.Errorf("passkey: unreadable %s value", want)
	}
	if _, err := dec.Token(); !errors.Is(err, io.EOF) {
		return fmt.Errorf("passkey: trailing data after the %s value", want)
	}
	return nil
}

// readRecord decodes a passkey record; nil bytes are no record.
func readRecord(raw []byte) (*record, error) {
	if raw == nil {
		return nil, nil
	}
	var r record
	if err := decode(raw, schemaRecord, &r); err != nil {
		return nil, err
	}
	if len(r.Handle) == 0 || len(r.Passkeys) == 0 {
		return nil, fmt.Errorf("passkey: an empty %s value", schemaRecord)
	}
	return &r, nil
}

// readChallenge decodes a challenge; nil bytes, and bytes that do not decode, are no challenge. A challenge that
// cannot be read is never a reason to refuse a new ceremony, which replaces it.
func readChallenge(raw []byte) *challenge {
	if raw == nil {
		return nil
	}
	var c challenge
	if decode(raw, schemaChallenge, &c) != nil {
		return nil
	}
	return &c
}

func (r *record) encode() ([]byte, error) {
	r.Schema = schemaRecord
	return encode(r)
}

func (c *challenge) encode() ([]byte, error) {
	c.Schema = schemaChallenge
	return encode(c)
}

// index returns the position of the passkey with id, or -1.
func (r *record) index(id []byte) int {
	if r == nil {
		return -1
	}
	for i, p := range r.Passkeys {
		if bytes.Equal(p.ID, id) {
			return i
		}
	}
	return -1
}

// credentials are the record's passkeys as go-webauthn reads them.
func (r *record) credentials() []webauthn.Credential {
	if r == nil {
		return nil
	}
	out := make([]webauthn.Credential, 0, len(r.Passkeys))
	for _, p := range r.Passkeys {
		transports := make([]protocol.AuthenticatorTransport, 0, len(p.Transports))
		for _, t := range p.Transports {
			transports = append(transports, protocol.AuthenticatorTransport(t))
		}
		out = append(out, webauthn.Credential{
			ID:              p.ID,
			PublicKey:       p.PublicKey,
			AttestationType: string(protocol.PreferNoAttestation),
			Transport:       transports,
			Flags:           webauthn.CredentialFlagsFromMsgpByte(p.Flags),
			Authenticator:   webauthn.Authenticator{AAGUID: p.AAGUID, SignCount: p.SignCount},
		})
	}
	return out
}

// storedOf turns a credential go-webauthn created into the App's stored passkey.
func storedOf(c *webauthn.Credential, now time.Time) stored {
	transports := make([]string, 0, len(c.Transport))
	for _, t := range c.Transport {
		transports = append(transports, string(t))
	}
	return stored{
		ID:         c.ID,
		PublicKey:  c.PublicKey,
		Flags:      c.Flags.MsgpByte(),
		AAGUID:     c.Authenticator.AAGUID,
		SignCount:  c.Authenticator.SignCount,
		Transports: transports,
		Created:    now.UTC(),
	}
}

// waUser is a user as go-webauthn reads one: the handle, the login and the passkeys.
type waUser struct {
	handle []byte
	login  string
	creds  []webauthn.Credential
}

func (u waUser) WebAuthnID() []byte                         { return u.handle }
func (u waUser) WebAuthnName() string                       { return u.login }
func (u waUser) WebAuthnDisplayName() string                { return u.login }
func (u waUser) WebAuthnCredentials() []webauthn.Credential { return u.creds }

// Package passkey is the App's WebAuthn relying party. It registers the passkeys of owners and turns one assertion
// with user verification into a one-use token for one action (design 9.2, M2 D9). Verification is go-webauthn's; this
// package keeps the records and the rules around it.
package passkey

import (
	"context"
	"encoding/json"
	"errors"
	"time"

	"github.com/alexei-led/archfit-app/internal/github/api"
)

// Actions of a step-up that this package consumes itself. control/answers redeems the answer kinds.
const (
	ActionRegister = "register" // a second passkey
	ActionRemove   = "remove"   // removing a passkey; Purpose.Passkey names it
)

// TTL is how long a challenge and a step-up token stay usable (M2 plan, "App keys").
const TTL = 5 * time.Minute

// MaxPasskeys is the most passkeys one user keeps in one installation **[default]**. It keeps a record far below the
// Store's 64 KiB value limit.
const MaxPasskeys = 10

// Config is the relying party: its id is the host of BaseURL, its origin BaseURL's scheme and host.
type Config struct {
	BaseURL string
}

// User is the signed-in GitHub user, as the caller authorized them for one repository.
type User struct {
	ID    int64
	Login string
	Admin bool // a repository administrator, from session.Authorize(…, "admin")
}

// Purpose is what one step-up token allows. A token is redeemed only for an equal Purpose. Repo is always set from
// the repository the call names; a value the caller puts there is replaced.
type Purpose struct {
	Repo       string `json:"repo"`                  // lower-case owner/name
	Action     string `json:"action"`                // ActionRegister, ActionRemove, or an answer kind of control/answers
	Request    int    `json:"request,omitempty"`     // the request issue; 0 when none
	Digest     string `json:"digest,omitempty"`      // the policy change digest; "" when none
	Target     string `json:"target,omitempty"`      // edit: exact protected tip of the preview
	BaseDigest string `json:"base_digest,omitempty"` // edit: exact bytes the editor started from
	Passkey    string `json:"passkey,omitempty"`     // ActionRemove: the id of the passkey to remove
}

// Passkey is one registered passkey as its user sees it.
type Passkey struct {
	ID       string // the credential id, base64url without padding
	Created  time.Time
	LastUsed time.Time // zero until its first step-up
}

// Owners is the port that reads the owners of the target policy.
type Owners interface {
	// Owners returns the logins named in owners of the policy on the default branch of r, and whether that branch has
	// a schema 3 policy. An error refuses the step that asked, so a policy that does not decode refuses it too.
	Owners(ctx context.Context, r api.Repo) (logins []string, hasPolicy bool, err error)
}

// Issues is the port that opens the issue owners must see.
type Issues interface {
	// Open opens an issue in r, assigned to the logins.
	Open(ctx context.Context, r api.RepoRef, title, body string, assignees []string) error
}

// Passkeys registers, lists and removes the passkeys of a user in one installation, and runs the step-up. Every
// method takes the repository the caller authorized: its installation is the tenant, so an uninstall deletes every
// passkey and challenge with the rest of the installation's keys.
type Passkeys interface {
	// BeginRegistration returns the creation options for the browser. A user who already has a passkey needs a
	// step-up token for ActionRegister; it is consumed here.
	BeginRegistration(ctx context.Context, r api.Repo, u User, token string) (json.RawMessage, error)
	// FinishRegistration verifies the browser's credential, opens the registration issue and stores the passkey.
	FinishRegistration(ctx context.Context, r api.Repo, u User, credential []byte) (Passkey, error)
	// BeginStepUp returns the request options for an assertion that will allow p once.
	BeginStepUp(ctx context.Context, r api.Repo, u User, p Purpose) (json.RawMessage, error)
	// FinishStepUp verifies the assertion and returns the token for the Purpose given at begin.
	FinishStepUp(ctx context.Context, r api.Repo, u User, credential []byte) (token string, err error)
	// Redeem consumes the token for p and returns the id of the passkey that made the assertion.
	Redeem(ctx context.Context, r api.Repo, userID int64, token string, p Purpose) (passkeyID string, err error)
	// List returns the user's passkeys, oldest first.
	List(ctx context.Context, r api.Repo, userID int64) ([]Passkey, error)
	// Remove deletes one passkey with a step-up token for ActionRemove and that passkey. Removing the last one opens a
	// removal issue, assigned like the registration issue to the user and the owners; the user's next registration
	// then needs no passkey.
	Remove(ctx context.Context, r api.Repo, u User, passkeyID, token string) error
}

var (
	// ErrNotOwner refuses a registration by a user who is neither an owner named in the target policy nor, before the
	// first policy, a repository administrator.
	ErrNotOwner = errors.New("passkey: not an owner")
	// ErrNeedsPasskey refuses a second registration without a step-up token.
	ErrNeedsPasskey = errors.New("passkey: a second passkey needs an existing one")
	// ErrNoPasskey refuses a step-up by a user with no passkey.
	ErrNoPasskey = errors.New("passkey: no passkey registered")
	// ErrCeremony is a ceremony that failed: no, another, an expired or a used challenge, or a credential that does
	// not verify (user verification, origin, relying party id, signature).
	ErrCeremony = errors.New("passkey: the ceremony failed")
	// ErrCounter is an assertion whose sign counter did not grow: the passkey may have been copied.
	ErrCounter = errors.New("passkey: the sign counter did not grow")
	// ErrToken is a token that is unknown, used, expired, or for another Purpose.
	ErrToken = errors.New("passkey: the token is not valid")
	// ErrTooMany refuses a registration past MaxPasskeys.
	ErrTooMany = errors.New("passkey: too many passkeys")
	// ErrUnknown names a passkey the user does not have.
	ErrUnknown = errors.New("passkey: no such passkey")
	// ErrInvalid is a Purpose that is not well formed.
	ErrInvalid = errors.New("passkey: invalid purpose")
	// ErrPolicy is a policy on the default branch that is neither schema 3 nor an Archfit 3 policy: who registers
	// cannot be known, so nobody does.
	ErrPolicy = errors.New("passkey: the policy on the default branch does not decode")
)

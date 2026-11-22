package passkey

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"crypto/subtle"
	"encoding/base64"
	"encoding/json"
	"errors"
	"log/slog"
	"strconv"
	"time"

	"github.com/coupling-dev/archfit-app/internal/store"
)

const confirmationSchema = "archfit-app.operator-confirmation.v1"
const confirmationPurpose = "first-passkey"
const confirmationTTL = 10 * time.Minute
const confirmationRequestLifetime = 30 * time.Minute
const confirmationLock = 15 * time.Minute

var ErrConfirmation = errors.New("passkey: confirmation cannot be used")
var ErrConfirmationExpired = errors.New("passkey: confirmation expired")

// ConfirmationLocked reports the server's lock expiry.
type ConfirmationLocked struct{ Until time.Time }

func (e *ConfirmationLocked) Error() string { return "passkey: confirmation locked" }

// ConfirmationScope is authenticated by the caller, never populated from owner claims.
type ConfirmationScope struct {
	InstallationID, RepositoryID, UserID int64
	SessionBinding                       string
	AttemptID                            string
}

// OperatorConfirmations stores one current first-enrolment attempt per user and installation.
// Its callers enforce owner eligibility and operator authorization before calling it.
type OperatorConfirmations struct {
	store     store.Store
	now       func() time.Time
	readToken func([]byte) (int, error)
}

type confirmationRecord struct {
	Schema               string
	AttemptID            string
	RepositoryID, UserID int64
	Binding              [32]byte
	Purpose              string
	RequestedAt          time.Time
	IssuedAt, ExpiresAt  time.Time
	Hash                 []byte
	Used, Revoked        bool
	Failures             int
	LockedUntil          time.Time
	OperatorID           int64
}

// NewOperatorConfirmations creates the state service; nil dependencies fail closed.
func NewOperatorConfirmations(s store.Store, now func() time.Time) *OperatorConfirmations {
	return &OperatorConfirmations{store: s, now: now, readToken: rand.Read}
}

func confirmationUserKey(id int64) string     { return "v4/confirmation-user/" + strconv.FormatInt(id, 10) }
func confirmationAttemptKey(id string) string { return "v4/confirmation-attempt/" + id }
func validConfirmationScope(s ConfirmationScope) bool {
	return s.InstallationID > 0 && s.RepositoryID > 0 && s.UserID > 0 && s.SessionBinding != ""
}
func confirmationID(id string) bool {
	raw, err := base64.RawURLEncoding.DecodeString(id)
	return err == nil && len(raw) == 16 && len(id) == 22
}
func readConfirmation(raw []byte) (confirmationRecord, error) {
	var r confirmationRecord
	if raw == nil {
		return r, nil
	}
	err := decode(raw, confirmationSchema, &r)
	return r, err
}
func (s *OperatorConfirmations) ready() bool {
	return s != nil && s.store != nil && s.now != nil && s.readToken != nil
}
func confirmationAudit(ctx context.Context, event string, tenant, user, operator int64, err error) {
	outcome := "ok"
	if err != nil {
		outcome = "refused"
	}
	slog.InfoContext(ctx, "passkey confirmation", "event", event, "installation_id", tenant, "user_id", user, "operator_id", operator, "outcome", outcome)
}

// Request returns a pending attempt ID, not a confirmation token. Repeated requests from
// the same session reuse the attempt; another session cannot replace a live or locked attempt.
func (s *OperatorConfirmations) Request(ctx context.Context, scope ConfirmationScope) (string, error) {
	if !s.ready() || !validConfirmationScope(scope) || scope.AttemptID != "" {
		return "", ErrConfirmation
	}
	var entropy [16]byte
	if _, err := rand.Read(entropy[:]); err != nil {
		return "", err
	}
	id := base64.RawURLEncoding.EncodeToString(entropy[:])
	t := store.Tenant(scope.InstallationID)
	key := confirmationUserKey(scope.UserID)
	binding := sha256.Sum256([]byte(scope.SessionBinding))
	for range 5 {
		raw, err := s.store.Get(ctx, t, key)
		if err != nil && !errors.Is(err, store.ErrNotFound) {
			return "", err
		}
		old, err := readConfirmation(raw)
		if err != nil {
			return "", err
		}
		keys := []string{key, confirmationAttemptKey(id)}
		if old.AttemptID != "" {
			keys = append(keys, confirmationAttemptKey(old.AttemptID))
		}
		chosen := id
		err = store.Update(ctx, s.store, t, keys, func(cur [][]byte) ([][]byte, error) {
			r, e := readConfirmation(cur[0])
			if e != nil {
				return nil, e
			}
			if r.AttemptID != old.AttemptID {
				return nil, store.ErrConflict
			}
			now := s.now().UTC()
			if now.Before(r.LockedUntil) {
				return nil, &ConfirmationLocked{r.LockedUntil}
			}
			until := r.RequestedAt.Add(confirmationRequestLifetime)
			if r.ExpiresAt.After(until) {
				until = r.ExpiresAt
			}
			if r.AttemptID != "" && !r.Used && now.Before(until) {
				if r.RepositoryID != scope.RepositoryID || r.Binding != binding {
					return nil, ErrConfirmation
				}
				chosen = r.AttemptID
				return cur, nil
			}
			if cur[1] != nil {
				return nil, ErrConfirmation
			}
			next := confirmationRecord{Schema: confirmationSchema, AttemptID: id, RepositoryID: scope.RepositoryID, UserID: scope.UserID, Binding: binding, Purpose: confirmationPurpose, RequestedAt: now}
			value, e := encode(next)
			if e != nil {
				return nil, e
			}
			alias, e := json.Marshal(scope.UserID)
			if e != nil {
				return nil, e
			}
			out := [][]byte{value, alias}
			if len(keys) == 3 {
				out = append(out, nil)
			}
			return out, nil
		})
		if errors.Is(err, store.ErrConflict) {
			continue
		}
		if err != nil {
			return "", err
		}
		return chosen, nil
	}
	return "", store.ErrConflict
}

func (s *OperatorConfirmations) update(ctx context.Context, tenant int64, id string, change func(*confirmationRecord) error) error {
	if !s.ready() || tenant <= 0 || !confirmationID(id) {
		return ErrConfirmation
	}
	raw, err := s.store.Get(ctx, store.Tenant(tenant), confirmationAttemptKey(id))
	if errors.Is(err, store.ErrNotFound) {
		return ErrConfirmation
	}
	if err != nil {
		return err
	}
	var user int64
	if json.Unmarshal(raw, &user) != nil || user <= 0 {
		return ErrConfirmation
	}
	return store.Update(ctx, s.store, store.Tenant(tenant), []string{confirmationUserKey(user)}, func(cur [][]byte) ([][]byte, error) {
		r, e := readConfirmation(cur[0])
		if e != nil {
			return nil, e
		}
		if r.AttemptID != id || r.UserID != user || r.Purpose != confirmationPurpose {
			return nil, ErrConfirmation
		}
		if e = change(&r); e != nil {
			return nil, e
		}
		value, e := encode(r)
		return [][]byte{value}, e
	})
}

// Issue returns the token once to an authenticated operator. Reissue invalidates its predecessor.
func (s *OperatorConfirmations) Issue(ctx context.Context, tenant int64, id string, operator int64) (token string, err error) {
	var user int64
	replaced := false
	defer func() {
		if err == nil && replaced {
			confirmationAudit(ctx, "revocation", tenant, user, operator, nil)
		}
		confirmationAudit(ctx, "issue", tenant, user, operator, err)
	}()
	if operator <= 0 || !s.ready() {
		return "", ErrConfirmation
	}
	var raw [16]byte
	n, entropyErr := s.readToken(raw[:])
	if entropyErr != nil {
		return "", entropyErr
	}
	if n != len(raw) {
		return "", ErrConfirmation
	}
	token = base64.RawURLEncoding.EncodeToString(raw[:])
	hash := sha256.Sum256([]byte(token))
	err = s.update(ctx, tenant, id, func(r *confirmationRecord) error {
		user = r.UserID
		replaced = len(r.Hash) != 0 && !r.Revoked
		now := s.now().UTC()
		if r.Used || !now.Before(r.RequestedAt.Add(confirmationRequestLifetime)) {
			return ErrConfirmation
		}
		if now.Before(r.LockedUntil) {
			return &ConfirmationLocked{r.LockedUntil}
		}
		if !r.LockedUntil.IsZero() {
			r.Failures = 0
			r.LockedUntil = time.Time{}
		}
		r.Hash = hash[:]
		r.Revoked = false
		r.OperatorID = operator
		r.IssuedAt = now
		r.ExpiresAt = now.Add(confirmationTTL)
		return nil
	})
	if err != nil {
		return "", err
	}
	return token, nil
}

// Verify atomically consumes a matching issued token for the authenticated attempt.
func (s *OperatorConfirmations) Verify(ctx context.Context, scope ConfirmationScope, token string) (err error) {
	defer func() { confirmationAudit(ctx, "verify", scope.InstallationID, scope.UserID, 0, err) }()
	if !s.ready() || !validConfirmationScope(scope) || token == "" {
		return ErrConfirmation
	}
	binding := sha256.Sum256([]byte(scope.SessionBinding))
	hash := sha256.Sum256([]byte(token))
	var refused error
	locked := false
	err = s.update(ctx, scope.InstallationID, scope.AttemptID, func(r *confirmationRecord) error {
		refused = nil
		locked = false
		if r.UserID != scope.UserID || r.RepositoryID != scope.RepositoryID || r.Binding != binding {
			return ErrConfirmation
		}
		now := s.now().UTC()
		if now.Before(r.LockedUntil) {
			return &ConfirmationLocked{r.LockedUntil}
		}
		if r.Used || r.Revoked || len(r.Hash) != sha256.Size {
			return ErrConfirmation
		}
		if !now.Before(r.ExpiresAt) {
			return ErrConfirmationExpired
		}
		if subtle.ConstantTimeCompare(r.Hash, hash[:]) != 1 {
			r.Failures++
			refused = ErrConfirmation
			if r.Failures >= 5 {
				r.LockedUntil = now.Add(confirmationLock)
				r.Hash = nil
				r.Revoked = true
				locked = true
				refused = &ConfirmationLocked{r.LockedUntil}
			}
			return nil
		}
		r.Used = true
		r.Hash = nil
		return nil
	})
	persisted := err == nil
	if persisted {
		err = refused
	}
	if locked && persisted {
		confirmationAudit(ctx, "lockout", scope.InstallationID, scope.UserID, 0, err)
	}
	return err
}

// Revoke invalidates an open token. The operator may issue a fresh token afterward.
func (s *OperatorConfirmations) Revoke(ctx context.Context, tenant int64, id string, operator int64) (err error) {
	var user int64
	defer func() { confirmationAudit(ctx, "revocation", tenant, user, operator, err) }()
	if operator <= 0 {
		return ErrConfirmation
	}
	return s.update(ctx, tenant, id, func(r *confirmationRecord) error {
		user = r.UserID
		if s.now().UTC().Before(r.LockedUntil) {
			return &ConfirmationLocked{r.LockedUntil}
		}
		if r.Used {
			return ErrConfirmation
		}
		r.Hash = nil
		r.Revoked = true
		r.OperatorID = operator
		return nil
	})
}

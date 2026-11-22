package passkey

import (
	"context"
	"crypto/hmac"
	"crypto/rand"
	"crypto/sha256"
	"crypto/subtle"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"math/big"
	"strconv"
	"strings"
	"time"

	"github.com/coupling-dev/archfit-app/internal/store"
)

var (
	ErrCodeCooldown = errors.New("passkey: email code cooldown")
	ErrCodeExpired  = errors.New("passkey: email code expired")
	ErrCodeWrong    = errors.New("passkey: incorrect email code")
	ErrCodeLocked   = errors.New("passkey: email code locked")
	ErrCodeSend     = errors.New("passkey: email code send failed")
)

type CodeSender interface {
	SendCode(context.Context, string, string) error
}
type EmailCodeScope struct {
	UserID, InstallationID, RepositoryID int64
	SessionBinding                       string
}
type EmailCodes struct {
	store  store.Store
	sender CodeSender
	key    []byte
	now    func() time.Time
}

type emailCodeRecord struct {
	Schema      string    `json:"schema"`
	Sent        time.Time `json:"sent"`
	LockedUntil time.Time `json:"locked_until"`
	Guesses     int       `json:"guesses"`
	Active      bool      `json:"active"`
	Digest      []byte    `json:"digest,omitempty"`
	Binding     []byte    `json:"binding,omitempty"`
}

func NewEmailCodes(s store.Store, sender CodeSender, key []byte, now func() time.Time) (*EmailCodes, error) {
	if s == nil || sender == nil || len(key) < 32 || now == nil {
		return nil, errors.New("passkey: email code dependencies required")
	}
	return &EmailCodes{s, sender, append([]byte(nil), key...), now}, nil
}
func (s *EmailCodes) mac(domain, value string) []byte {
	h := hmac.New(sha256.New, s.key)
	h.Write([]byte(domain))
	h.Write([]byte{0})
	h.Write([]byte(value))
	return h.Sum(nil)
}
func (s *EmailCodes) binding(scope EmailCodeScope) []byte {
	return s.mac("email-code-scope", fmt.Sprintf("%d:%d:%d:%s", scope.UserID, scope.InstallationID, scope.RepositoryID, scope.SessionBinding))
}
func validCodeScope(scope EmailCodeScope) bool {
	return scope.UserID > 0 && scope.InstallationID > 0 && scope.RepositoryID > 0 && scope.SessionBinding != ""
}
func codeKey(id int64) string { return "v4/email-code/" + strconv.FormatInt(id, 10) }
func readEmailCode(raw []byte) (emailCodeRecord, error) {
	if raw == nil {
		return emailCodeRecord{Schema: "archfit-app.email-code.v1"}, nil
	}
	var r emailCodeRecord
	if err := decode(raw, "archfit-app.email-code.v1", &r); err != nil {
		return r, err
	}
	return r, nil
}
func (s *EmailCodes) update(ctx context.Context, id int64, f func(*emailCodeRecord) error) error {
	return store.Update(ctx, s.store, store.SystemTenant, []string{codeKey(id)}, func(cur [][]byte) ([][]byte, error) {
		r, err := readEmailCode(cur[0])
		if err != nil {
			return nil, err
		}
		if err = f(&r); err != nil {
			return nil, err
		}
		b, err := json.Marshal(r)
		return [][]byte{b}, err
	})
}
func (s *EmailCodes) Send(ctx context.Context, scope EmailCodeScope, recipient string) (masked string, err error) {
	defer func() {
		outcome := "ok"
		if err != nil {
			outcome = "error"
		}
		slog.InfoContext(ctx, "passkey: email code send", "user_id", scope.UserID, "repository_id", scope.RepositoryID, "installation_id", scope.InstallationID, "outcome", outcome)
	}()
	if !validCodeScope(scope) || recipient == "" {
		return "", ErrInvalid
	}
	n, err := rand.Int(rand.Reader, big.NewInt(1000000))
	if err != nil {
		return "", err
	}
	code := fmt.Sprintf("%06d", n.Int64())
	// A random nonce ensures failure cleanup cannot invalidate a later attempt, even if codes repeat.
	nonce := make([]byte, 32)
	if _, err = rand.Read(nonce); err != nil {
		return "", err
	}
	digest := s.mac("email-code", fmt.Sprintf("%x:%s", nonce, code))
	stored := append(nonce, digest...)
	binding := s.binding(scope)
	now := s.now().UTC()
	err = s.update(ctx, scope.UserID, func(r *emailCodeRecord) error {
		if now.Before(r.LockedUntil) {
			return ErrCodeLocked
		}
		if !r.Sent.IsZero() && now.Before(r.Sent.Add(time.Minute)) {
			return ErrCodeCooldown
		}
		if !r.LockedUntil.IsZero() {
			r.Guesses = 0
			r.LockedUntil = time.Time{}
		}
		r.Sent = now
		r.Active = false
		r.Digest = stored
		r.Binding = binding
		return nil
	})
	if err != nil {
		return "", err
	}
	if sendErr := s.sender.SendCode(ctx, recipient, code); sendErr != nil {
		cleanupErr := s.update(ctx, scope.UserID, func(r *emailCodeRecord) error {
			if subtle.ConstantTimeCompare(r.Digest, stored) == 1 {
				r.Digest = nil
				r.Binding = nil
			}
			return nil
		})
		if cleanupErr != nil {
			return "", fmt.Errorf("%w: cleanup failed", ErrCodeSend)
		}
		return "", ErrCodeSend
	}

	err = s.update(ctx, scope.UserID, func(r *emailCodeRecord) error {
		if subtle.ConstantTimeCompare(r.Digest, stored) != 1 {
			return ErrCodeExpired
		}
		r.Active = true
		return nil
	})
	if err != nil {
		return "", err
	}
	parts := strings.SplitN(recipient, "@", 2)
	if len(parts) != 2 || len(parts[0]) == 0 {
		return "***", nil
	}
	return parts[0][:1] + "***@" + parts[1], nil
}
func (s *EmailCodes) Verify(ctx context.Context, scope EmailCodeScope, code string) error {
	if !validCodeScope(scope) {
		return ErrInvalid
	}
	now := s.now().UTC()
	var outcome error
	err := s.update(ctx, scope.UserID, func(r *emailCodeRecord) error {
		outcome = nil
		if now.Before(r.LockedUntil) {
			outcome = ErrCodeLocked
			return nil
		}
		if !r.LockedUntil.IsZero() {
			r.Guesses = 0
			r.LockedUntil = time.Time{}
		}
		if !r.Active || len(r.Digest) != 64 || !now.Before(r.Sent.Add(10*time.Minute)) {
			outcome = ErrCodeExpired
			return nil
		}
		want := s.mac("email-code", fmt.Sprintf("%x:%s", r.Digest[:32], code))
		if subtle.ConstantTimeCompare(r.Binding, s.binding(scope)) != 1 || subtle.ConstantTimeCompare(r.Digest[32:], want) != 1 {
			r.Guesses++
			outcome = ErrCodeWrong
			if r.Guesses >= 5 {
				r.LockedUntil = now.Add(15 * time.Minute)
				r.Digest = nil
				r.Binding = nil
			}
			return nil
		}
		r.Digest = nil
		r.Binding = nil
		r.Guesses = 0
		return nil
	})
	if err != nil {
		return err
	}
	return outcome
}

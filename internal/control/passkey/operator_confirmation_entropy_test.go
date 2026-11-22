package passkey

import (
	"bytes"
	"encoding/base64"
	"errors"
	"testing"
	"time"

	"github.com/coupling-dev/archfit-app/internal/store"
)

func TestConfirmationTokenEntropyBoundary(t *testing.T) {
	s, _, scope, _, _ := confirmationFixture(t)
	calls := 0
	s.readToken = func(b []byte) (int, error) {
		calls++
		if len(b) != 16 {
			t.Fatal("entropy request not 16 bytes")
		}
		copy(b, bytes.Repeat([]byte{0xad}, 16))
		return len(b), nil
	}
	token, err := s.Issue(t.Context(), 1, scope.AttemptID, 4)
	want := base64.RawURLEncoding.EncodeToString(bytes.Repeat([]byte{0xad}, 16))
	if err != nil || token != want || calls != 1 {
		t.Fatal("wrong entropy/encoding boundary")
	}
	for _, short := range []bool{false, true} {
		s.readToken = func([]byte) (int, error) {
			if short {
				return 15, nil
			}
			return 0, errors.New("entropy unavailable")
		}
		if token, err = s.Issue(t.Context(), 1, scope.AttemptID, 4); err == nil || token != "" {
			t.Fatal("entropy failure issued token")
		}
	}
}

func TestConfirmationCannotReplaceLateIssuedToken(t *testing.T) {
	s, _, scope, _, now := confirmationFixture(t)
	*now = now.Add(9 * time.Minute)
	token, err := s.Issue(t.Context(), 1, scope.AttemptID, 4)
	if err != nil {
		t.Fatal(err)
	}
	*now = now.Add(2 * time.Minute)
	req := scope
	req.AttemptID = ""
	req.SessionBinding = "other"
	if _, err = s.Request(t.Context(), req); err == nil {
		t.Fatal("replaced still-valid issued attempt")
	}
	if err = s.Verify(t.Context(), scope, token); err != nil {
		t.Fatal(err)
	}
}

func TestConfirmationPurposeAndInvalidOperators(t *testing.T) {
	s, mem, scope, token, _ := confirmationFixture(t)
	if _, err := s.Issue(t.Context(), 1, scope.AttemptID, 0); err == nil {
		t.Fatal("issued without trusted operator ID")
	}
	if err := s.Revoke(t.Context(), 1, scope.AttemptID, 0); err == nil {
		t.Fatal("revoked without trusted operator ID")
	}
	key := confirmationUserKey(scope.UserID)
	raw, err := mem.Get(t.Context(), 1, key)
	if err != nil {
		t.Fatal(err)
	}
	r, err := readConfirmation(raw)
	if err != nil {
		t.Fatal(err)
	}
	r.Purpose = "other"
	raw, err = encode(r)
	if err != nil {
		t.Fatal(err)
	}
	if err = mem.Put(t.Context(), 1, key, raw); err != nil {
		t.Fatal(err)
	}
	if s.Verify(t.Context(), scope, token) == nil {
		t.Fatal("accepted different purpose")
	}
	if _, err = mem.Get(t.Context(), store.Tenant(2), key); !errors.Is(err, store.ErrNotFound) {
		t.Fatal("cross-tenant write")
	}
}

package passkey

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/base64"
	"errors"
	"log/slog"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/coupling-dev/archfit-app/internal/store"
)

func confirmationFixture(t *testing.T) (*OperatorConfirmations, store.Store, ConfirmationScope, string, *time.Time) {
	t.Helper()
	mem := store.NewMemory()
	now := time.Date(2026, 11, 22, 12, 0, 0, 0, time.UTC)
	svc := NewOperatorConfirmations(mem, func() time.Time { return now })
	scope := ConfirmationScope{InstallationID: 1, RepositoryID: 2, UserID: 3, SessionBinding: "private-session-binding"}
	id, err := svc.Request(t.Context(), scope)
	if err != nil {
		t.Fatal(err)
	}
	scope.AttemptID = id
	token, err := svc.Issue(t.Context(), 1, id, 4)
	if err != nil {
		t.Fatal(err)
	}
	return svc, mem, scope, token, &now
}

func TestConfirmationScopeAndExpiry(t *testing.T) {
	for _, tc := range []struct {
		name   string
		change func(*ConfirmationScope)
	}{
		{"user", func(s *ConfirmationScope) { s.UserID++ }},
		{"repository", func(s *ConfirmationScope) { s.RepositoryID++ }},
		{"installation", func(s *ConfirmationScope) { s.InstallationID++ }},
		{"session", func(s *ConfirmationScope) { s.SessionBinding = "other" }},
		{"attempt", func(s *ConfirmationScope) { s.AttemptID = base64.RawURLEncoding.EncodeToString(make([]byte, 16)) }},
	} {
		t.Run(tc.name, func(t *testing.T) {
			s, _, scope, token, _ := confirmationFixture(t)
			wrong := scope
			tc.change(&wrong)
			if err := s.Verify(t.Context(), wrong, token); err == nil {
				t.Fatal("accepted mismatched scope")
			}
			if err := s.Verify(t.Context(), scope, token); err != nil {
				t.Fatal(err)
			}
		})
	}
	for _, elapsed := range []time.Duration{confirmationTTL - time.Nanosecond, confirmationTTL, confirmationTTL + time.Second} {
		t.Run(elapsed.String(), func(t *testing.T) {
			s, _, scope, token, now := confirmationFixture(t)
			*now = now.Add(elapsed)
			err := s.Verify(t.Context(), scope, token)
			if (err == nil) != (elapsed < confirmationTTL) {
				t.Fatalf("elapsed %v accepted %v", elapsed, err == nil)
			}
		})
	}
}

func TestConfirmationReissueAndRevoke(t *testing.T) {
	s, _, scope, old, _ := confirmationFixture(t)
	fresh, err := s.Issue(t.Context(), 1, scope.AttemptID, 4)
	if err != nil {
		t.Fatal(err)
	}
	if fresh == old {
		t.Fatal("reissue reused token")
	}
	if s.Verify(t.Context(), scope, old) == nil {
		t.Fatal("accepted replaced token")
	}
	if err = s.Revoke(t.Context(), 1, scope.AttemptID, 4); err != nil {
		t.Fatal(err)
	}
	if s.Verify(t.Context(), scope, fresh) == nil {
		t.Fatal("accepted revoked token")
	}
	fresh, err = s.Issue(t.Context(), 1, scope.AttemptID, 4)
	if err != nil {
		t.Fatal(err)
	}
	if err = s.Verify(t.Context(), scope, fresh); err != nil {
		t.Fatal(err)
	}
	if _, err = s.Issue(t.Context(), 1, scope.AttemptID, 4); err == nil {
		t.Fatal("reissued used attempt")
	}
}

func TestConfirmationLockSurvivesReissueAndRequest(t *testing.T) {
	s, _, scope, _, now := confirmationFixture(t)
	for i := 0; i < 5; i++ {
		err := s.Verify(t.Context(), scope, "wrong")
		if err == nil {
			t.Fatal("accepted wrong token")
		}
		var locked *ConfirmationLocked
		if errors.As(err, &locked) != (i == 4) {
			t.Fatalf("attempt %d: %v", i, err)
		}
	}
	if _, err := s.Issue(t.Context(), 1, scope.AttemptID, 4); err == nil {
		t.Fatal("issue bypassed lock")
	}
	req := scope
	req.AttemptID = ""
	req.SessionBinding = "new-session"
	if _, err := s.Request(t.Context(), req); err == nil {
		t.Fatal("request bypassed lock")
	}
	*now = now.Add(confirmationLock)
	token, err := s.Issue(t.Context(), 1, scope.AttemptID, 4)
	if err != nil {
		t.Fatal(err)
	}
	var locked *ConfirmationLocked
	if err = s.Verify(t.Context(), scope, "wrong-again"); err == nil || errors.As(err, &locked) {
		t.Fatalf("expired lock didn't reset guesses: %v", err)
	}
	if err = s.Verify(t.Context(), scope, token); err != nil {
		t.Fatal(err)
	}
}

func TestConfirmationConcurrentConsumption(t *testing.T) {
	s, _, scope, token, _ := confirmationFixture(t)
	var wins atomic.Int32
	var wg sync.WaitGroup
	for range 20 {
		wg.Go(func() {
			if s.Verify(context.Background(), scope, token) == nil {
				wins.Add(1)
			}
		})
	}
	wg.Wait()
	if wins.Load() != 1 {
		t.Fatalf("successful uses %d", wins.Load())
	}
}

func TestConfirmationNoPlaintextPersistenceOrLogs(t *testing.T) {
	var logs bytes.Buffer
	prev := slog.Default()
	slog.SetDefault(slog.New(slog.NewTextHandler(&logs, nil)))
	t.Cleanup(func() { slog.SetDefault(prev) })
	s, mem, scope, token, _ := confirmationFixture(t)
	raw, err := mem.Get(t.Context(), 1, confirmationUserKey(scope.UserID))
	if err != nil {
		t.Fatal(err)
	}
	if bytes.Contains(raw, []byte(token)) || bytes.Contains(raw, []byte(scope.SessionBinding)) {
		t.Fatal("plaintext secret persisted")
	}
	r, err := readConfirmation(raw)
	if err != nil {
		t.Fatal(err)
	}
	hash := sha256.Sum256([]byte(token))
	if !bytes.Equal(r.Hash, hash[:]) {
		t.Fatal("stored value is not token SHA-256")
	}
	if err = s.Verify(t.Context(), scope, token); err != nil {
		t.Fatal(err)
	}
	if strings.Contains(logs.String(), token) || strings.Contains(logs.String(), scope.SessionBinding) {
		t.Fatal("secret in logs")
	}
	if !strings.Contains(logs.String(), "issue") || !strings.Contains(logs.String(), "verify") {
		t.Fatal("missing audit events")
	}
}

func TestConfirmationRequestBindingAndReplacement(t *testing.T) {
	s, mem, scope, token, now := confirmationFixture(t)
	req := scope
	req.AttemptID = ""
	again, err := s.Request(t.Context(), req)
	if err != nil || again != scope.AttemptID {
		t.Fatal("same pending session not reused")
	}
	other := req
	other.SessionBinding = "another"
	if _, err = s.Request(t.Context(), other); err == nil {
		t.Fatal("replaced live attempt")
	}
	if err = s.Verify(t.Context(), scope, token); err != nil {
		t.Fatal(err)
	}
	*now = now.Add(time.Second)
	next, err := s.Request(t.Context(), other)
	if err != nil || next == scope.AttemptID {
		t.Fatal("failed new request after consumption")
	}
	if _, err = mem.Get(t.Context(), 1, confirmationAttemptKey(scope.AttemptID)); !errors.Is(err, store.ErrNotFound) {
		t.Fatal("old attempt index retained")
	}
	if s.Verify(t.Context(), scope, token) == nil {
		t.Fatal("old token worked after replacement")
	}
}

package passkey

import (
	"bytes"
	"context"
	"errors"
	"log/slog"
	"strings"
	"testing"
	"time"

	"github.com/coupling-dev/archfit-app/internal/store"
)

type confirmationWriteFailure struct{ store.Store }

func (confirmationWriteFailure) CompareAndSwapAll(context.Context, store.Tenant, []store.Swap) (bool, error) {
	return false, errors.New("test storage failure")
}

func TestConfirmationRejectedRequestHasNoID(t *testing.T) {
	s, _, scope, _, _ := confirmationFixture(t)
	scope.AttemptID = ""
	scope.SessionBinding = "other"
	id, err := s.Request(t.Context(), scope)
	if err == nil || id != "" {
		t.Fatal("rejected request exposed unpersisted ID")
	}
}
func TestConfirmationFailedWriteDoesNotAuditLock(t *testing.T) {
	s, mem, scope, _, _ := confirmationFixture(t)
	for range 4 {
		if s.Verify(t.Context(), scope, "wrong") == nil {
			t.Fatal("accepted wrong token")
		}
	}
	var logs bytes.Buffer
	prev := slog.Default()
	slog.SetDefault(slog.New(slog.NewTextHandler(&logs, nil)))
	defer slog.SetDefault(prev)
	s.store = confirmationWriteFailure{mem}
	if s.Verify(t.Context(), scope, "wrong") == nil {
		t.Fatal("accepted failed write")
	}
	if strings.Contains(logs.String(), "lockout") {
		t.Fatal("audited unpersisted lock")
	}
}
func TestConfirmationAttemptLifetimeAndRevocationLock(t *testing.T) {
	s, _, scope, _, now := confirmationFixture(t)
	*now = now.Add(30 * time.Minute)
	if _, err := s.Issue(t.Context(), 1, scope.AttemptID, 4); err == nil {
		t.Fatal("issued abandoned request")
	}
	s, _, scope, _, _ = confirmationFixture(t)
	for range 5 {
		_ = s.Verify(t.Context(), scope, "wrong")
	}
	var locked *ConfirmationLocked
	if err := s.Revoke(t.Context(), 1, scope.AttemptID, 4); !errors.As(err, &locked) {
		t.Fatalf("revoke during lock: %v", err)
	}
	if _, err := s.Issue(t.Context(), 1, scope.AttemptID, 4); !errors.As(err, &locked) {
		t.Fatalf("issue during lock: %v", err)
	}
}

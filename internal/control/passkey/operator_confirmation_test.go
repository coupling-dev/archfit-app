package passkey

import (
	"context"
	"encoding/base64"
	"testing"
	"time"

	"github.com/coupling-dev/archfit-app/internal/store"
)

func TestOperatorConfirmationOneUse(t *testing.T) {
	ctx := context.Background()
	s := NewOperatorConfirmations(store.NewMemory(), time.Now)
	scope := ConfirmationScope{InstallationID: 1, RepositoryID: 2, UserID: 3, SessionBinding: "session-binding", AttemptID: "attempt-1"}
	if err := s.Request(ctx, scope); err != nil {
		t.Fatal(err)
	}
	if err := s.Verify(ctx, scope, ""); err == nil {
		t.Fatal("pending request accepted without an issued token")
	}
	token, err := s.Issue(ctx, 1, scope.AttemptID, 4)
	if err != nil {
		t.Fatal(err)
	}
	raw, err := base64.RawURLEncoding.DecodeString(token)
	if err != nil || len(token) != 22 || len(raw) != 16 {
		t.Fatalf("invalid issued token: length %d, decoded length %d, error %v", len(token), len(raw), err)
	}
	if err := s.Verify(ctx, scope, token); err != nil {
		t.Fatal(err)
	}
	if err := s.Verify(ctx, scope, token); err == nil {
		t.Fatal("accepted a consumed token")
	}
}

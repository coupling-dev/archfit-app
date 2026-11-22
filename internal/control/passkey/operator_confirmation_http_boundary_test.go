package passkey

import (
	"testing"
	"time"
)

func TestConfirmationEmptyTokenDoesNotLock(t *testing.T) {
	s, _, scope, token, _ := confirmationFixture(t)
	for range 6 {
		if s.Verify(t.Context(), scope, "") == nil {
			t.Fatal("accepted missing token")
		}
	}
	if err := s.Verify(t.Context(), scope, token); err != nil {
		t.Fatal(err)
	}
}
func TestConfirmationTokenCanOutliveRequestIssuanceDeadline(t *testing.T) {
	s, _, scope, _, now := confirmationFixture(t)
	*now = now.Add(29 * time.Minute)
	token, err := s.Issue(t.Context(), 1, scope.AttemptID, 4)
	if err != nil {
		t.Fatal(err)
	}
	*now = now.Add(2 * time.Minute)
	if _, err = s.Issue(t.Context(), 1, scope.AttemptID, 4); err == nil {
		t.Fatal("issued after request deadline")
	}
	if err = s.Verify(t.Context(), scope, token); err != nil {
		t.Fatal(err)
	}
}

package passkey

import (
	"bytes"
	"context"
	"errors"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/coupling-dev/archfit-app/internal/store"
)

type codeSink struct{ code string }

func (s *codeSink) SendCode(_ context.Context, _, code string) error { s.code = code; return nil }

func TestEmailCodeBoundaries(t *testing.T) {
	for _, tc := range []struct {
		name string
		run  func(*testing.T, *EmailCodes, EmailCodeScope, *codeSink, *time.Time)
	}{
		{"cooldown", func(t *testing.T, svc *EmailCodes, scope EmailCodeScope, sink *codeSink, now *time.Time) {
			if _, err := svc.Send(ctx, scope, "owner@example.invalid"); err != nil {
				t.Fatal(err)
			}
			if _, err := svc.Send(ctx, scope, "owner@example.invalid"); !errors.Is(err, ErrCodeCooldown) {
				t.Fatalf("cooldown: %v", err)
			}
			*now = now.Add(time.Minute)
			if _, err := svc.Send(ctx, scope, "owner@example.invalid"); err != nil {
				t.Fatal(err)
			}
		}},
		{"expiry", func(t *testing.T, svc *EmailCodes, scope EmailCodeScope, sink *codeSink, now *time.Time) {
			if _, err := svc.Send(ctx, scope, "owner@example.invalid"); err != nil {
				t.Fatal(err)
			}
			*now = now.Add(10 * time.Minute)
			if err := svc.Verify(ctx, scope, sink.code); !errors.Is(err, ErrCodeExpired) {
				t.Fatalf("expiry: %v", err)
			}
		}},
		{"lock", func(t *testing.T, svc *EmailCodes, scope EmailCodeScope, sink *codeSink, now *time.Time) {
			if _, err := svc.Send(ctx, scope, "owner@example.invalid"); err != nil {
				t.Fatal(err)
			}
			wrong := "000000"
			if sink.code == wrong {
				wrong = "000001"
			}
			for i := 0; i < 5; i++ {
				if err := svc.Verify(ctx, scope, wrong); err == nil {
					t.Fatal("wrong code accepted")
				}
			}
			if err := svc.Verify(ctx, scope, sink.code); !errors.Is(err, ErrCodeLocked) {
				t.Fatalf("lock: %v", err)
			}
		}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			now := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
			sink := &codeSink{}
			svc, err := NewEmailCodes(store.NewMemory(), sink, []byte("01234567890123456789012345678901"), func() time.Time { return now })
			if err != nil {
				t.Fatal(err)
			}
			tc.run(t, svc, EmailCodeScope{UserID: 101, InstallationID: 7, RepositoryID: 70, SessionBinding: "binding"}, sink, &now)
		})
	}
}

func TestEmailCodeConcurrentUse(t *testing.T) {
	now := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	sink := &safeCodeSink{}
	svc, err := NewEmailCodes(store.NewMemory(), sink, []byte("01234567890123456789012345678901"), func() time.Time { return now })
	if err != nil {
		t.Fatal(err)
	}
	scope := EmailCodeScope{UserID: 101, InstallationID: 7, RepositoryID: 70, SessionBinding: "binding"}
	const n = 20
	var wg sync.WaitGroup
	results := make(chan error, n)
	for i := 0; i < n; i++ {
		wg.Add(1)
		go func() { defer wg.Done(); _, err := svc.Send(ctx, scope, "owner@example.invalid"); results <- err }()
	}
	wg.Wait()
	close(results)
	successes := 0
	for err := range results {
		if err == nil {
			successes++
		}
	}
	if successes != 1 || sink.count != 1 {
		t.Fatalf("sends %d emails %d", successes, sink.count)
	}
	results = make(chan error, n)
	for i := 0; i < n; i++ {
		wg.Add(1)
		go func() { defer wg.Done(); results <- svc.Verify(ctx, scope, sink.code) }()
	}
	wg.Wait()
	close(results)
	successes = 0
	for err := range results {
		if err == nil {
			successes++
		}
	}
	if successes != 1 {
		t.Fatalf("verifications %d", successes)
	}
}

type safeCodeSink struct {
	mu    sync.Mutex
	code  string
	count int
}

func (s *safeCodeSink) SendCode(_ context.Context, _, code string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.code = code
	s.count++
	return nil
}

type failingCodeSink struct{ code string }

func (s *failingCodeSink) SendCode(_ context.Context, _, code string) error {
	s.code = code
	return errors.New("private provider error")
}
func TestEmailCodeFailedSendAndPrivacy(t *testing.T) {
	now := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	sink := &failingCodeSink{}
	db := store.NewMemory()
	svc, err := NewEmailCodes(db, sink, []byte("01234567890123456789012345678901"), func() time.Time { return now })
	if err != nil {
		t.Fatal(err)
	}
	scope := EmailCodeScope{UserID: 101, InstallationID: 7, RepositoryID: 70, SessionBinding: "private-session"}
	_, err = svc.Send(ctx, scope, "owner@example.invalid")
	if !errors.Is(err, ErrCodeSend) || strings.Contains(err.Error(), "private provider") {
		t.Fatalf("send error: %v", err)
	}
	if err = svc.Verify(ctx, scope, sink.code); err == nil {
		t.Fatal("failed send code accepted")
	}
	raw, err := db.Get(ctx, store.SystemTenant, codeKey(scope.UserID))
	if err != nil {
		t.Fatal(err)
	}
	for _, secret := range []string{"owner@example.invalid", sink.code, scope.SessionBinding} {
		if bytes.Contains(raw, []byte(secret)) {
			t.Fatalf("stored private value")
		}
	}
	if _, err = svc.Send(ctx, scope, "owner@example.invalid"); !errors.Is(err, ErrCodeCooldown) {
		t.Fatalf("cooldown: %v", err)
	}
}

func TestEmailCodeAuthorizationScopeAndExpiry(t *testing.T) {
	now := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	sink := &codeSink{}
	svc, err := NewEmailCodes(store.NewMemory(), sink, []byte("01234567890123456789012345678901"), func() time.Time { return now })
	if err != nil {
		t.Fatal(err)
	}
	scope := EmailCodeScope{UserID: 101, InstallationID: 7, RepositoryID: 70, SessionBinding: "session"}
	if _, err = svc.Send(ctx, scope, "owner@example.invalid"); err != nil {
		t.Fatal(err)
	}
	if err = svc.Verify(ctx, scope, sink.code); err != nil {
		t.Fatal(err)
	}
	other := scope
	other.SessionBinding = "other"
	if err = svc.Spend(ctx, other); err == nil {
		t.Fatal("other session spent authorization")
	}
	if err = svc.Spend(ctx, scope); err != nil {
		t.Fatal(err)
	}
	if err = svc.Spend(ctx, scope); err == nil {
		t.Fatal("authorization reused")
	}
	if _, err = svc.Send(ctx, scope, "owner@example.invalid"); !errors.Is(err, ErrCodeCooldown) {
		t.Fatalf("cooldown after spend: %v", err)
	}
	now = now.Add(time.Minute)
	if _, err = svc.Send(ctx, scope, "owner@example.invalid"); err != nil {
		t.Fatal(err)
	}
	if err = svc.Verify(ctx, scope, sink.code); err != nil {
		t.Fatal(err)
	}
	now = now.Add(10 * time.Minute)
	if err = svc.Spend(ctx, scope); err == nil {
		t.Fatal("expired authorization spent")
	}
}

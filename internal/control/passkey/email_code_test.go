package passkey

import (
	"bytes"
	"context"
	"errors"
	"strings"
	"sync"
	"sync/atomic"
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

type delayedFailureSink struct {
	entered chan string
	release chan struct{}
	mu      sync.Mutex
	calls   int
	latest  string
}

func (s *delayedFailureSink) SendCode(_ context.Context, _, code string) error {
	s.mu.Lock()
	s.calls++
	n := s.calls
	s.latest = code
	s.mu.Unlock()
	if n == 1 {
		s.entered <- code
		<-s.release
		return errors.New("private error")
	}
	return nil
}
func TestEmailCodeLateSendFailureDoesNotInvalidateResend(t *testing.T) {
	var seconds atomic.Int64
	start := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	sink := &delayedFailureSink{entered: make(chan string, 1), release: make(chan struct{})}
	svc, err := NewEmailCodes(store.NewMemory(), sink, []byte("01234567890123456789012345678901"), func() time.Time { return start.Add(time.Duration(seconds.Load()) * time.Second) })
	if err != nil {
		t.Fatal(err)
	}
	scope := EmailCodeScope{UserID: 101, InstallationID: 7, RepositoryID: 70, SessionBinding: "binding"}
	result := make(chan error, 1)
	go func() { _, err := svc.Send(ctx, scope, "owner@example.invalid"); result <- err }()
	<-sink.entered
	seconds.Store(60)
	if _, err = svc.Send(ctx, scope, "owner@example.invalid"); err != nil {
		t.Fatal(err)
	}
	close(sink.release)
	if err = <-result; !errors.Is(err, ErrCodeSend) {
		t.Fatalf("first send: %v", err)
	}
	sink.mu.Lock()
	code := sink.latest
	sink.mu.Unlock()
	if err = svc.Verify(ctx, scope, code); err != nil {
		t.Fatalf("resend invalidated: %v", err)
	}
}

func TestEmailCodeResendPreservesAttemptsAcrossScopes(t *testing.T) {
	now := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	sink := &codeSink{}
	svc, err := NewEmailCodes(store.NewMemory(), sink, []byte("01234567890123456789012345678901"), func() time.Time { return now })
	if err != nil {
		t.Fatal(err)
	}
	scope := EmailCodeScope{UserID: 101, InstallationID: 7, RepositoryID: 70, SessionBinding: "one"}
	if _, err = svc.Send(ctx, scope, "owner@example.invalid"); err != nil {
		t.Fatal(err)
	}
	old := sink.code
	wrong := "000000"
	if old == wrong {
		wrong = "000001"
	}
	for i := 0; i < 4; i++ {
		if err = svc.Verify(ctx, scope, wrong); !errors.Is(err, ErrCodeWrong) {
			t.Fatalf("guess: %v", err)
		}
	}
	other := scope
	other.RepositoryID++
	other.SessionBinding = "two"
	if _, err = svc.Send(ctx, other, "owner@example.invalid"); !errors.Is(err, ErrCodeCooldown) {
		t.Fatalf("cross scope cooldown: %v", err)
	}
	now = now.Add(time.Minute)
	if _, err = svc.Send(ctx, other, "owner@example.invalid"); err != nil {
		t.Fatal(err)
	}
	if err = svc.Verify(ctx, scope, old); !errors.Is(err, ErrCodeLocked) {
		t.Fatalf("old code: %v", err)
	}
	if err = svc.Verify(ctx, other, sink.code); !errors.Is(err, ErrCodeLocked) {
		t.Fatalf("cross scope lock: %v", err)
	}
	now = now.Add(15 * time.Minute)
	if _, err = svc.Send(ctx, other, "owner@example.invalid"); err != nil {
		t.Fatalf("lock boundary: %v", err)
	}
}

func TestEmailCodeMaskAndLockDeadline(t *testing.T) {
	now := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	sink := &codeSink{}
	svc, err := NewEmailCodes(store.NewMemory(), sink, []byte("01234567890123456789012345678901"), func() time.Time { return now })
	if err != nil {
		t.Fatal(err)
	}
	scope := EmailCodeScope{UserID: 101, InstallationID: 7, RepositoryID: 70, SessionBinding: "binding"}
	masked, err := svc.Send(ctx, scope, "éclair@example.invalid")
	if err != nil {
		t.Fatal(err)
	}
	if masked != "é•••@example.invalid" {
		t.Fatalf("mask: %q", masked)
	}
	wrong := "000000"
	if sink.code == wrong {
		wrong = "000001"
	}
	for i := 0; i < 5; i++ {
		err = svc.Verify(ctx, scope, wrong)
	}
	var locked *CodeLockedError
	if !errors.As(err, &locked) || !errors.Is(err, ErrCodeLocked) || !locked.LockedUntil.Equal(now.Add(15*time.Minute)) || locked.LockedUntil.Location() != time.UTC {
		t.Fatalf("lock: %v", err)
	}
	_, err = svc.Send(ctx, scope, "éclair@example.invalid")
	if !errors.As(err, &locked) || !locked.LockedUntil.Equal(now.Add(15*time.Minute)) {
		t.Fatalf("send lock: %v", err)
	}
}

func TestEmailCodeActivePrivacyAndScope(t *testing.T) {
	now := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	sink := &codeSink{}
	db := store.NewMemory()
	svc, err := NewEmailCodes(db, sink, []byte("01234567890123456789012345678901"), func() time.Time { return now })
	if err != nil {
		t.Fatal(err)
	}
	scope := EmailCodeScope{UserID: 101, InstallationID: 7, RepositoryID: 70, SessionBinding: "sensitive-session-binding"}
	if _, err = svc.Send(ctx, scope, "owner@example.invalid"); err != nil {
		t.Fatal(err)
	}
	raw, err := db.Get(ctx, store.SystemTenant, codeKey(scope.UserID))
	if err != nil {
		t.Fatal(err)
	}
	for _, secret := range []string{"owner@example.invalid", sink.code, scope.SessionBinding} {
		if bytes.Contains(raw, []byte(secret)) {
			t.Fatal("active record contains private value")
		}
	}
	for _, other := range []EmailCodeScope{
		{UserID: 101, InstallationID: 8, RepositoryID: 70, SessionBinding: scope.SessionBinding},
		{UserID: 101, InstallationID: 7, RepositoryID: 71, SessionBinding: scope.SessionBinding},
		{UserID: 101, InstallationID: 7, RepositoryID: 70, SessionBinding: "other-session"},
	} {
		if err = svc.Verify(ctx, other, sink.code); !errors.Is(err, ErrCodeWrong) {
			t.Fatalf("other scope accepted: %v", err)
		}
	}
	if err = svc.Verify(ctx, scope, sink.code); err != nil {
		t.Fatalf("valid scope: %v", err)
	}
}

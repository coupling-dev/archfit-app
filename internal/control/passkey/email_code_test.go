package passkey

import (
	"context"
	"errors"
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

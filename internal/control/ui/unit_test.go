package ui

import (
	"os"
	"strings"
	"sync/atomic"
	"testing"

	"github.com/alexei-led/archfit-app/internal/control/ui/session"
)

func TestCSRFToken(t *testing.T) {
	a := session.User{Login: "a", UserID: 1, Token: "tok-a"}
	b := session.User{Login: "b", UserID: 2, Token: "tok-b"}
	relogin := session.User{Login: "a", UserID: 1, Token: "tok-a-2"}
	token := csrfToken(a)
	tests := []struct {
		name string
		user session.User
		got  string
		want bool
	}{
		{"own token", a, token, true},
		{"token of A sent by B", b, token, false},
		{"empty", a, "", false},
		{"truncated", a, token[:len(token)-1], false},
		{"A after re-login with a new OAuth token", relogin, token, false},
		{"no session token", session.User{}, csrfToken(session.User{}), false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := validCSRF(tt.user, tt.got); got != tt.want {
				t.Errorf("validCSRF = %v, want %v", got, tt.want)
			}
		})
	}
	if len(token) != 64 {
		t.Errorf("token length = %d, want hex SHA-256", len(token))
	}
	// The comparison must stay constant-time.
	src, err := os.ReadFile("csrf.go")
	if err != nil || !strings.Contains(string(src), "hmac.Equal(") {
		t.Errorf("csrf.go does not compare with hmac.Equal (%v)", err)
	}
}

func TestTogetherRaisesAPanicOnTheCallersGoroutine(t *testing.T) {
	var ran atomic.Bool
	defer func() {
		if p := recover(); p != "boom" || !ran.Load() {
			t.Errorf("recovered %v, other part ran %v; want boom after the other part", p, ran.Load())
		}
	}()
	together(func() { ran.Store(true) }, func() { panic("boom") })
	t.Error("together returned after a panic")
}

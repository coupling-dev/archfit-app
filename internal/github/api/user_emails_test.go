package api

import (
	"context"
	"errors"
	"net/http"
	"strings"
	"testing"
)

func TestVerifiedPrimaryEmail(t *testing.T) {
	const token = "fake-user-token"
	const primary = "primary@example.invalid"
	const secondary = "secondary@example.invalid"
	cases := []struct {
		name     string
		replies  []reply
		want     string
		status   int
		rejected bool
	}{
		{"primary after secondary", []reply{{status: 200, body: `[{"email":"secondary@example.invalid","verified":true,"primary":false},{"email":"primary@example.invalid","verified":true,"primary":true}]`}}, primary, 0, false},
		{"unverified primary", []reply{{status: 200, body: `[{"email":"primary@example.invalid","verified":false,"primary":true},{"email":"secondary@example.invalid","verified":true,"primary":false}]`}}, "", 0, false},
		{"no primary", []reply{{status: 200, body: `[{"email":"secondary@example.invalid","verified":true}]`}}, "", 0, false},
		{"empty", []reply{{status: 200, body: `[]`}}, "", 0, false},
		{"later page", []reply{{status: 200, header: map[string]string{"Link": linkHeader("/user/emails", 2)}, body: `[]`}, {status: 200, body: `[{"email":"primary@example.invalid","verified":true,"primary":true}]`}}, primary, 0, false},
		{"expired", []reply{{status: 401, body: primary + token}}, "", 0, true},
		{"forbidden", []reply{{status: 403, body: primary + token}}, "", 403, false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			e := newEnv(t, script(tc.replies...))
			got, err := e.c.VerifiedPrimaryEmail(context.Background(), token)
			if got != tc.want || errors.Is(err, ErrUserTokenRejected) != tc.rejected || Status(err) != tc.status {
				t.Fatalf("got %q, err %v", got, err)
			}
			if err != nil && (strings.Contains(err.Error(), token) || strings.Contains(err.Error(), primary) || strings.Contains(err.Error(), secondary)) {
				t.Fatal("sensitive data in error")
			}
			for _, r := range e.gh.requests() {
				if r.Method != http.MethodGet || r.Path != "/user/emails" || r.Auth != bearer(token) {
					t.Fatal("unexpected request or credentials")
				}
			}
			if e.ts.jwtCalls != 0 || len(e.ts.minted) != 0 {
				t.Fatal("app or installation token used")
			}
		})
	}
	t.Run("not cached", func(t *testing.T) {
		e := newEnv(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			etagJSON(w, r, `"etag"`, []map[string]any{{"email": primary, "primary": true, "verified": true}})
		}))
		for range 2 {
			got, err := e.c.VerifiedPrimaryEmail(context.Background(), token)
			if err != nil || got != primary {
				t.Fatalf("unexpected result: %v", err)
			}
		}
		reqs := e.gh.requests()
		if len(reqs) != 2 || reqs[1].IfNoneMatch != "" {
			t.Fatal("user response cached")
		}
	})
}

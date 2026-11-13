package control

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"net/url"
	"regexp"
	"strings"
	"testing"
)

// browser is a cookie-keeping client of the UI handler.
type browser struct {
	t       *testing.T
	handler http.Handler
	cookies map[string]*http.Cookie
}

func newBrowser(t *testing.T, handler http.Handler) *browser {
	return &browser{t: t, handler: handler, cookies: map[string]*http.Cookie{}}
}

func (b *browser) do(method, target string, form url.Values) *httptest.ResponseRecorder {
	b.t.Helper()
	var req *http.Request
	if form != nil {
		req = httptest.NewRequest(method, target, strings.NewReader(form.Encode()))
		req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	} else {
		req = httptest.NewRequest(method, target, nil)
	}
	return b.send(req)
}

// postJSON is a write of the web app: a JSON body and the session's CSRF token, read from /api/session.
func (b *browser) postJSON(target string, body any) *httptest.ResponseRecorder {
	b.t.Helper()
	var sess struct{ CSRF string }
	if err := json.Unmarshal(b.get("/api/session").Body.Bytes(), &sess); err != nil || sess.CSRF == "" {
		b.t.Fatalf("no session token: %v", err)
	}
	payload, err := json.Marshal(body)
	if err != nil {
		b.t.Fatal(err)
	}
	req := httptest.NewRequest(http.MethodPost, target, bytes.NewReader(payload))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("X-CSRF-Token", sess.CSRF)
	return b.send(req)
}

func (b *browser) send(req *http.Request) *httptest.ResponseRecorder {
	for _, c := range b.cookies {
		req.AddCookie(c)
	}
	rec := httptest.NewRecorder()
	b.handler.ServeHTTP(rec, req)
	for _, c := range rec.Result().Cookies() {
		if c.MaxAge < 0 {
			delete(b.cookies, c.Name)
			continue
		}
		b.cookies[c.Name] = c
	}
	return rec
}

func (b *browser) get(target string) *httptest.ResponseRecorder {
	return b.do(http.MethodGet, target, nil)
}

// signIn walks the OAuth flow of the fake OAuth: the code is the login.
func (b *browser) signIn(login string) {
	b.t.Helper()
	rec := b.get("/auth/login?return=/")
	loc, err := url.Parse(rec.Header().Get("Location"))
	if err != nil || rec.Code != http.StatusSeeOther && rec.Code != http.StatusFound {
		b.t.Fatalf("GET /auth/login = %d to %q", rec.Code, rec.Header().Get("Location"))
	}
	state := loc.Query().Get("state")
	rec = b.get("/auth/callback?" + url.Values{"code": {login}, "state": {state}}.Encode())
	if rec.Code != http.StatusSeeOther && rec.Code != http.StatusFound {
		b.t.Fatalf("GET /auth/callback = %d: %s", rec.Code, rec.Body.String())
	}
}

var hiddenInput = regexp.MustCompile(`<input[^>]*type="hidden"[^>]*>`)
var attr = regexp.MustCompile(`(name|value)="([^"]*)"`)

// hiddenFields returns the hidden inputs of a page, as a browser would post them back.
func hiddenFields(page string) url.Values {
	out := url.Values{}
	for _, tag := range hiddenInput.FindAllString(page, -1) {
		var name, value string
		for _, m := range attr.FindAllStringSubmatch(tag, -1) {
			if m[1] == "name" {
				name = m[2]
			} else {
				value = m[2]
			}
		}
		if name != "" && out.Get(name) == "" {
			out.Set(name, value)
		}
	}
	return out
}

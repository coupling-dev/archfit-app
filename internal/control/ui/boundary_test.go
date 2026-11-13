package ui

import (
	"bytes"
	"encoding/json"
	"fmt"
	"html"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"regexp"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/alexei-led/archfit-app/internal/control/ui/session"
	"github.com/alexei-led/archfit-app/internal/github/api"
)

// noWrites fails the test if any sibling module or GitHub write was reached.
func (e *env) noWrites(t *testing.T) {
	t.Helper()
	if e.consents.writes != 0 {
		t.Errorf("consent writes = %d", e.consents.writes)
	}
}

func TestTenantIdentityOnlyFromSession(t *testing.T) {
	// Installation B's IDs, supplied everywhere a client can put them.
	foreign := url.Values{"installation_id": {"2"}, "installation": {"2"}, "repository_id": {"20"}, "repo_id": {"20"}, "repo": {otherRepo}}
	send := func(c *client, rt route) int {
		form := url.Values{}
		for k, v := range rt.form {
			form[k] = v
		}
		for k, v := range foreign {
			form[k] = v
		}
		var req *http.Request
		switch {
		case rt.method == http.MethodGet:
			req = httptest.NewRequest(rt.method, baseURL+rt.path+"?"+foreign.Encode(), nil)
		case rt.api(): // the body is the endpoint's own type: a foreign field in it is refused (TestOversizedAndInvalidInput)
			raw, _ := json.Marshal(rt.body)
			req = httptest.NewRequest(rt.method, baseURL+rt.path+"?"+foreign.Encode(), bytes.NewReader(raw))
			req.Header.Set("Content-Type", "application/json")
			req.Header.Set(csrfHeader, c.csrf())
		default:
			form.Set("csrf", c.csrf())
			req = httptest.NewRequest(rt.method, baseURL+rt.path+"?"+foreign.Encode(), strings.NewReader(form.Encode()))
			req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
		}
		req.Header.Set("X-Installation-Id", "2")
		req.Header.Set("X-GitHub-Hook-Installation-Target-Id", "2")
		req.Header.Set("X-Repository-Id", "20")
		return c.do(req).Code
	}

	e := newEnv(t)
	c := e.as("alice", permAdmin)

	for _, rt := range repoRoutes(otherRepo) {
		if !rt.authorized() {
			continue
		}
		if got := send(c, rt); got != http.StatusNotFound {
			t.Errorf("%s %s = %d, want 404", rt.method, rt.path, got)
		}
	}
	e.noWrites(t)

	for _, rt := range repoRoutes(repoName) {
		if got := send(c, rt); got != rt.expectedStatus() {
			t.Errorf("%s %s = %d, want success", rt.method, rt.path, got)
		}
	}

	e.gh.mu.Lock()
	for _, call := range e.gh.calls {
		// The user's own permission lookup, with the user's token, is how B's repository is refused: no App call.
		if call.method != "Permission" && (call.installationID == installB || call.fullName == otherRepo) {
			t.Errorf("GitHub call for installation B: %+v", call)
		}
	}
	e.gh.mu.Unlock()
	for _, id := range append(e.consents.touched, e.acct.installations...) {
		if id != installA {
			t.Errorf("a key of installation %d was touched", id)
		}
	}
}

func TestOverviewShowsOnlyAccessibleRepositories(t *testing.T) {
	e := newEnv(t)
	for i, name := range []string{"acme/one", "acme/two", "acme/three"} {
		e.addRepo(name, installA, int64(40+i))
		e.grant("alice", name, permRead)
	}
	for i, name := range []string{"acme/concealed-1", "acme/concealed-2", "rival/concealed-3", "rival/concealed-4", "other/concealed-5"} {
		e.addRepo(name, int64(1+i%3), int64(50+i))
	}
	res := e.signIn("alice").get("/api/repos")
	wantStatus(t, res, http.StatusOK)
	body := res.Body.String()
	if got := strings.Count(body, `"full_name"`); got != 3 {
		t.Errorf("rows = %d, want 3", got)
	}
	if strings.Contains(body, "concealed") || strings.Contains(body, otherRepo) {
		t.Error("a repository GitHub did not list is on the page")
	}
	for _, call := range e.gh.called("BranchHead") {
		if strings.Contains(call.fullName, "concealed") || call.fullName == otherRepo || call.fullName == repoName {
			t.Errorf("lookup for %s", call.fullName)
		}
	}
	if got := len(e.gh.called("BranchHead")); got != 3 {
		t.Errorf("BranchHead calls = %d, want 3", got)
	}
}

func TestNewRejectsBadSessionConfig(t *testing.T) {
	tests := []struct {
		name    string
		key     []byte
		baseURL string
		wantErr bool
	}{
		{"16-byte key", cookieKey[:16], baseURL, true},
		{"http off localhost", cookieKey, "http://app.example", true},
		{"relative base URL", cookieKey, "/app", true},
		{"valid", cookieKey, baseURL, false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			e := newEnv(t)
			gh := api.New(nil, nil)
			clock := func() time.Time { return e.now }
			_, wantErr := session.New(session.Config{CookieKey: tt.key, BaseURL: tt.baseURL}, e.oauth, gh, gh, clock)
			h, err := New(Deps{
				OAuth: e.oauth, CookieKey: tt.key, BaseURL: tt.baseURL, GitHub: gh, Account: e.acct, Consents: e.consents,
				Now: clock,
			})
			if (wantErr != nil) != tt.wantErr {
				t.Fatalf("session.New error = %v, want error: %v", wantErr, tt.wantErr)
			}
			if tt.wantErr {
				if h != nil || err == nil || err.Error() != wantErr.Error() {
					t.Errorf("New = %v, %v; want nil and %v", h, err, wantErr)
				}
				return
			}
			if h == nil || err != nil {
				t.Fatalf("New = %v, %v", h, err)
			}
			rec := httptest.NewRecorder()
			h.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, baseURL+"/", nil))
			wantStatus(t, rec, http.StatusOK)
		})
	}
}

func TestCSRFRequiredOnEveryPost(t *testing.T) {
	routes := append(repoRoutes(repoName), route{http.MethodPost, "/auth/logout", "", nil, nil})
	e := newEnv(t)
	c := e.as("alice", permAdmin)
	bob := csrfToken(session.User{Token: e.user("bob").AccessToken})

	for _, rt := range routes {
		if rt.method != http.MethodPost {
			continue
		}
		for name, token := range map[string]string{"no token": "", "token of another user": bob} {
			var got int
			if rt.api() {
				got = c.postJSONWith(rt.path, rt.body, token).Code
			} else {
				form := url.Values{"csrf": {token}}
				for k, v := range rt.form {
					form[k] = v
				}
				got = c.post(rt.path, form).Code
			}
			if got != http.StatusForbidden {
				t.Errorf("%s with %s = %d, want 403", rt.path, name, got)
			}
		}
		if !rt.api() {
			continue
		}
		// An /api write takes the token only from the header: a form field or a form body is not enough.
		form := url.Values{"csrf": {c.csrf()}}
		req := httptest.NewRequest(http.MethodPost, baseURL+rt.path, strings.NewReader(form.Encode()))
		req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
		if got := c.do(req).Code; got != http.StatusUnsupportedMediaType {
			t.Errorf("%s as a form = %d, want 415", rt.path, got)
		}
	}
	e.noWrites(t)
	if got := c.get("/api/session").Code; got != http.StatusOK {
		t.Errorf("a logout without a CSRF token ended the session: /api/session = %d", got)
	}
}

// wantCSP allows no inline style or script, no eval, no worker but a same-origin one (the graph's layout) and no other
// origin: nothing on any page needs more.
// wantShellCSP is the web app's shell: the same policy, plus the one nonce its editor's <style> element carries.
const (
	wantCSP = "default-src 'none'; script-src 'self'; style-src 'self'; connect-src 'self'; img-src 'self' data:; " +
		"font-src 'self'; base-uri 'none'; form-action 'self'; frame-ancestors 'none'; object-src 'none'; worker-src 'self'"
	wantShellCSP = "default-src 'none'; script-src 'self'; style-src 'self' 'nonce-__CSP_NONCE__'; connect-src 'self'; " +
		"img-src 'self' data:; font-src 'self'; base-uri 'none'; form-action 'self'; frame-ancestors 'none'; object-src 'none'; worker-src 'self'"
)

var shellNonce = regexp.MustCompile(`'nonce-([A-Za-z0-9+/]{22}==)'`)

// cspOf is the CSP of a response with the shell's nonce, if any, back to its placeholder.
func cspOf(res *httptest.ResponseRecorder) string {
	return shellNonce.ReplaceAllString(res.Header().Get("Content-Security-Policy"), "'nonce-"+noncePlaceholder+"'")
}

// The web app's preview server and smoke test run under web/csp.txt, so they prove nothing unless it is the shell's
// policy with the placeholder nonce that web/index.html carries.
func TestWebPreviewUsesTheProductionCSP(t *testing.T) {
	raw, err := os.ReadFile("../../../web/csp.txt")
	if err != nil {
		t.Fatal(err)
	}
	if got := strings.TrimSpace(string(raw)); got != wantShellCSP || got != shellPolicy(noncePlaceholder) {
		t.Errorf("web/csp.txt = %q, want the server's %q", got, shellPolicy(noncePlaceholder))
	}
	index, err := os.ReadFile("../../../web/index.html")
	if err != nil {
		t.Fatal(err)
	}
	wantContains(t, string(index), `<meta name="csp-nonce" content="`+noncePlaceholder+`">`)
	wantContains(t, string(index), `<meta name="app-error" content="`+errorPlaceholder+`">`)
}

// Every shell gets its own nonce, in its CSP and in the meta tag the editor reads, and is never stored: a nonce an
// attacker has seen once must not stay valid.
func TestShellNonceIsFreshPerResponse(t *testing.T) {
	e := newEnv(t)
	c := e.as("alice", permAdmin)
	seen := map[string]bool{}
	for _, path := range []string{"/", "/r/" + repoName, "/r/" + repoName + "/graph", "/r/" + repoName + "/graph", "/r/" + repoName + "/data-sharing"} {
		res := c.get(path)
		wantStatus(t, res, http.StatusOK)
		m := shellNonce.FindStringSubmatch(res.Header().Get("Content-Security-Policy"))
		if m == nil || cspOf(res) != wantShellCSP {
			t.Fatalf("%s: CSP = %q, want %q with a 16-byte nonce", path, res.Header().Get("Content-Security-Policy"), wantShellCSP)
		}
		if seen[m[1]] {
			t.Errorf("%s: nonce %q used twice", path, m[1])
		}
		seen[m[1]] = true
		wantContains(t, res.Body.String(), `<meta name="csp-nonce" content="`+m[1]+`">`)
		if strings.Contains(res.Body.String(), noncePlaceholder) {
			t.Errorf("%s: the placeholder is left in the page", path)
		}
		if got := res.Header().Get("Cache-Control"); got != "no-store" {
			t.Errorf("%s: Cache-Control = %q, want no-store", path, got)
		}
	}
	for _, path := range []string{"/api/repos/" + repoName + "/overview", "/assets/app-1a2b.js", "/schema/policy.json"} {
		if got := c.get(path).Header().Get("Content-Security-Policy"); got != wantCSP {
			t.Errorf("%s: CSP = %q, want the policy without a nonce", path, got)
		}
	}
}

// The OAuth token stays on the server: no API answer carries it or the session cookie, whatever the route.
func TestNoAPIResponseHoldsASecret(t *testing.T) {
	e := newEnv(t)
	c := e.as("alice", permAdmin)
	api := "/api/repos/" + repoName
	answers := []*httptest.ResponseRecorder{
		c.get("/api/session"),
		c.get("/api/repos"),
		c.get(api + "/overview"),
		c.get(api + "/pulls/4"),
		c.get(api + "/pulls/4/graph"),
		c.get(api + "/graph"),
		c.get(api + "/findings"),
		c.postJSON(api+"/onboarding", map[string]string{"mode": "customer", "owners": "alice"}),
		c.postJSON(api+"/discover", struct{}{}),
		c.get("/api/nothing"),
	}
	secrets := []string{c.token}
	for _, value := range c.cookies {
		secrets = append(secrets, value)
	}
	for _, res := range answers {
		for _, secret := range secrets {
			if strings.Contains(res.Body.String(), secret) {
				t.Errorf("an API answer (%d) holds %.12q…: %.200s", res.Code, secret, res.Body.String())
			}
		}
	}
}

func TestLogsCarryOnlyIdentifiers(t *testing.T) {
	logs := captureLogs(t)
	e := newEnv(t)
	c := e.as("alice", permAdmin)
	e.acct.err = fmt.Errorf("MARKER-error")
	base := "/r/" + repoName

	sharing := "/api/repos/" + repoName + "/data-sharing"
	wantStatus(t, c.postJSON(sharing, dataSharingJSON{Kind: "MARKER-kind", Action: actionAllow}), http.StatusBadRequest)
	wantStatus(t, c.postJSON(sharing, dataSharingJSON{Kind: "source_processing", Action: "MARKER-action"}), http.StatusBadRequest)
	wantStatus(t, c.postJSON(sharing, dataSharingJSON{Kind: "source_processing", Action: actionAllow}), http.StatusOK)
	wantStatus(t, c.postJSON(sharing, dataSharingJSON{Kind: "source_processing", Action: actionAllow}), http.StatusOK)
	wantStatus(t, c.postJSON(sharing, dataSharingJSON{Kind: "source_processing", Action: actionRevoke}), http.StatusOK)
	wantStatus(t, c.get(base+"/graph"), http.StatusOK)
	wantStatus(t, c.get(base), http.StatusOK)
	wantStatus(t, c.get("/api/repos"), http.StatusOK)
	wantStatus(t, c.get("/api/repos/"+repoName+"/overview"), http.StatusConflict) // reads account.Status, which fails with the marker

	out := logs.String()
	if !strings.Contains(out, `"route":"POST /api/repos/{o}/{n}/data-sharing"`) || !strings.Contains(out, `"installation_id":1`) {
		t.Fatalf("no request log line: %.300s", out)
	}
	var audit []string
	for line := range strings.Lines(out) {
		if strings.Contains(line, `"msg":"ui consent"`) {
			audit = append(audit, line)
		}
	}
	alice := strconv.FormatInt(e.user("alice").UserID, 10)
	if len(audit) != 3 {
		t.Fatalf("consent audit lines = %d, want one per allow or revoke that was accepted:\n%s", len(audit), strings.Join(audit, ""))
	}
	for i, outcome := range []string{"consent_recorded", "consent_kept", "consent_revoked"} {
		for _, want := range []string{`"level":"INFO"`, `"outcome":"` + outcome + `"`, `"kind":"source_processing"`, `"installation_id":1`, `"user_id":` + alice} {
			if !strings.Contains(audit[i], want) {
				t.Errorf("audit line %d lacks %s: %s", i, want, audit[i])
			}
		}
	}
	secrets := []string{"MARKER-", c.token, c.csrf()}
	for _, value := range c.cookies {
		secrets = append(secrets, value)
	}
	for _, secret := range secrets {
		if strings.Contains(out, secret) {
			t.Errorf("logs contain %.20q", secret)
		}
	}
}

// An error page is the shell with the documented status: the shell's CSP with a fresh nonce, and the app shows the
// sentence of explain from the app-error meta tag. An error's own text never reaches the page.
func TestErrorPagesAreTheShell(t *testing.T) {
	tests := []struct {
		name     string
		setup    func(e *env)
		path     string
		status   int
		sentence string
	}{
		{"a pull request number that is not one", func(*env) {}, "/r/" + repoName + "/pull/0", http.StatusNotFound, "Not found."},
		{"a request number that is not one", func(*env) {}, "/r/" + repoName + "/requests/0", http.StatusNotFound, "Not found."},
		{"a path of no page", func(*env) {}, "/static/app.css", http.StatusNotFound, "Not found."},
		{"a route's path with a method it does not take", func(*env) {}, "/auth/logout", http.StatusNotFound, "Not found."},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			e := newEnv(t)
			c := e.as("alice", permRead)
			tt.setup(e)
			res := c.get(tt.path)
			wantStatus(t, res, tt.status)
			if cspOf(res) != wantShellCSP || !shellNonce.MatchString(res.Header().Get("Content-Security-Policy")) {
				t.Errorf("CSP = %q, want the shell's with a nonce", res.Header().Get("Content-Security-Policy"))
			}
			body := res.Body.String()
			wantContains(t, body, `<meta name="app-error" content="`+html.EscapeString(tt.sentence)+`">`)
			if strings.Contains(body, "secret-host") || strings.Contains(body, errorPlaceholder) {
				t.Errorf("the page leaks the error's text or keeps the placeholder: %s", body)
			}
			if got := res.Header().Get("Cache-Control"); got != "no-store" {
				t.Errorf("Cache-Control = %q, want no-store", got)
			}
		})
	}

	ok := newEnv(t).as("alice", permRead).get("/r/" + repoName)
	wantContains(t, ok.Body.String(), `<meta name="app-error" content="">`)
}

// A repository page is one shell for every repository: one the user reads, one of another tenant, one that does not
// exist. The bytes and headers are the same but for the nonce, and serving it asks GitHub nothing. The API then answers
// a repository the user cannot see exactly as one that does not exist.
func TestRepositoryPagesAreOneShell(t *testing.T) {
	e := newEnv(t)
	c := e.as("alice", permRead)
	unnonced := func(res *httptest.ResponseRecorder) string {
		m := shellNonce.FindStringSubmatch(res.Header().Get("Content-Security-Policy"))
		if m == nil {
			t.Fatalf("no nonce in %q", res.Header().Get("Content-Security-Policy"))
		}
		h := res.Header().Clone()
		h.Del("X-Request-Id") // per request, as the nonce
		return strings.ReplaceAll(fmt.Sprint(res.Code, h, res.Body.String()), m[1], "NONCE")
	}
	for _, suffix := range []string{"", "/graph", "/pull/4", "/pull/4/graph", "/data-sharing", "/passkeys", "/policy", "/policy?edit=1"} {
		var want string
		for _, repo := range []string{repoName, otherRepo, "acme/missing"} {
			res := c.get("/r/" + repo + suffix)
			wantStatus(t, res, http.StatusOK)
			got := unnonced(res)
			if want == "" {
				want = got
			} else if got != want {
				t.Errorf("/r/%s%s differs from the shell of %s%s:\n%s\nwant\n%s", repo, suffix, repoName, suffix, got, want)
			}
		}
	}
	if n := len(e.gh.called("Permission")); n != 0 {
		t.Errorf("serving the pages asked GitHub %d times, want none", n)
	}

	hidden, missing := c.get("/api/repos/"+otherRepo+"/overview"), c.get("/api/repos/acme/missing/overview")
	wantStatus(t, hidden, http.StatusNotFound)
	if hidden.Code != missing.Code || hidden.Body.String() != missing.Body.String() {
		t.Errorf("hidden = %d %q, missing = %d %q: want the same answer", hidden.Code, hidden.Body, missing.Code, missing.Body)
	}
}

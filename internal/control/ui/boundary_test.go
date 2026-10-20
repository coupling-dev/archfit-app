package ui

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"html"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"reflect"
	"regexp"
	"slices"
	"sort"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/alexei-led/archfit-app/internal/control/account"
	"github.com/alexei-led/archfit-app/internal/control/assist"
	"github.com/alexei-led/archfit-app/internal/control/onboarding"
	"github.com/alexei-led/archfit-app/internal/control/ui/session"
	"github.com/alexei-led/archfit-app/internal/github/api"
	"github.com/alexei-led/archfit-app/internal/wire/settings"
)

// noWrites fails the test if any sibling module or GitHub write was reached.
func (e *env) noWrites(t *testing.T) {
	t.Helper()
	if n := e.props.writes(); n != 0 {
		t.Errorf("proposal writes = %d", n)
	}
	if e.consents.writes != 0 {
		t.Errorf("consent writes = %d", e.consents.writes)
	}
	if len(e.onb.customer)+len(e.onb.managed) != 0 {
		t.Error("onboarding was started")
	}
	if len(e.assist.calls) != 0 {
		t.Error("assist was called")
	}
	if len(e.gh.called("Dispatch")) != 0 {
		t.Error("a workflow was dispatched")
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
	e.props.push(repoName, "archfit/edit-alice", policyText+"# draft\n")
	e.publishReport(repoName, artifact(t, "healthy"))

	for _, rt := range repoRoutes(otherRepo) {
		if got := send(c, rt); got != http.StatusNotFound {
			t.Errorf("%s %s = %d, want 404", rt.method, rt.path, got)
		}
	}
	e.noWrites(t)

	for _, rt := range repoRoutes(repoName) {
		rt = rt.withHead(e.props.drafts[repoName+"@archfit/edit-alice"].HeadSHA)
		if got := send(c, rt); got != http.StatusOK {
			t.Errorf("%s %s = %d, want success", rt.method, rt.path, got)
		}
	}

	e.gh.mu.Lock()
	for _, call := range e.gh.calls {
		if call.installationID == installB || call.fullName == otherRepo {
			t.Errorf("GitHub call for installation B: %+v", call)
		}
	}
	e.gh.mu.Unlock()
	for _, id := range append(e.consents.touched, e.acct.installations...) {
		if id != installA {
			t.Errorf("a key of installation %d was touched", id)
		}
	}
	for _, call := range append(e.onb.customer, e.onb.managed...) {
		if call.repo.InstallationID != installA {
			t.Errorf("onboarding got %+v", call.repo)
		}
	}
	for _, call := range e.assist.calls {
		if call.req.Repo.InstallationID != installA || call.req.Repo.FullName != repoName {
			t.Errorf("assist got %+v", call.req.Repo)
		}
	}
	for _, call := range e.props.proposes {
		if call.change.Repo != (api.RepoRef{InstallationID: installA, FullName: repoName}) {
			t.Errorf("proposal got %+v", call.change.Repo)
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
				Onboarding: e.onb, Assist: e.assist, Proposals: e.props, Now: clock,
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
	e.props.push(repoName, "archfit/edit-alice", policyText+"# draft\n")
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

func TestOversizedAndInvalidInput(t *testing.T) {
	owners := make([]string, 50)
	for i := range owners {
		owners[i] = fmt.Sprintf("owner-%d", i)
	}
	owners[31] = "not a login!"
	tests := []struct {
		name, path string
		body       any
		want       int
	}{
		{"600 KB body", "/policy", editOf("", "", "#"+strings.Repeat("a", 600<<10)), http.StatusRequestEntityTooLarge},
		{"600 KB JSON body", "/onboarding", onboardingJSON{Mode: "customer", Owners: strings.Repeat("a", 600<<10)}, http.StatusRequestEntityTooLarge},
		{"300 KB policy", "/policy", editOf("", "", "#"+strings.Repeat("a", 300<<10)), http.StatusUnprocessableEntity},
		{"300 KB policy through assist", "/assist", assistOf("", "#"+strings.Repeat("a", 300<<10)), http.StatusUnprocessableEntity},
		{"300 KB policy to check", "/policy/check", checkPolicyJSON{Content: "#" + strings.Repeat("a", 300<<10)}, http.StatusUnprocessableEntity},
		{"invalid UTF-8", "/policy", editOf("", "", "modules: {}\n# \xff\xfe\n"), http.StatusUnprocessableEntity},
		{"tab indent", "/policy", editOf("", "", "modules:\n\ta: {}\n"), http.StatusUnprocessableEntity},
		{"malformed owner", "/onboarding", onboardingJSON{Mode: "customer", Owners: strings.Join(owners, "\n")}, http.StatusUnprocessableEntity},
		{"no owner", "/onboarding", onboardingJSON{Mode: "customer", Owners: " \n"}, http.StatusUnprocessableEntity},
		{"unknown mode", "/onboarding", onboardingJSON{Mode: "magic", Owners: "alice"}, http.StatusUnprocessableEntity},
		{"a field the endpoint does not take", "/onboarding",
			map[string]string{"mode": "customer", "owners": "alice", "repo": otherRepo}, http.StatusUnprocessableEntity},
		{"not JSON", "/onboarding", json.RawMessage(`"mode=customer"`), http.StatusUnprocessableEntity},
		{"a field discovery does not take", "/discover", map[string]string{"repo": otherRepo}, http.StatusUnprocessableEntity},
		{"a field save does not take", "/policy",
			map[string]string{"content": policyText + "# x\n", "repo": otherRepo}, http.StatusUnprocessableEntity},
		{"malformed head_sha", "/policy", editOf("", "HEAD~1", policyText+"# x\n"), http.StatusUnprocessableEntity},
		{"7-character head_sha", "/policy", editOf("", "abc1234", policyText+"# x\n"), http.StatusUnprocessableEntity},
		{"malformed head_sha to ready", "/policy/ready", readyJSON{HeadSHA: "HEAD~1"}, http.StatusUnprocessableEntity},
		{"a branch outside archfit/", "/policy", editOf("main", "", policyText+"# x\n"), http.StatusUnprocessableEntity},
		{"a branch that climbs out of archfit/", "/assist",
			assistJSON{editJSON: editOf("archfit/../main", "", policyText), Instruction: "x", Mode: "customer"}, http.StatusUnprocessableEntity},
		{"unknown assist mode", "/assist", assistJSON{editJSON: editOf("", "", policyText), Mode: "magic"}, http.StatusUnprocessableEntity},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			e := newEnv(t)
			c := e.as("alice", permAdmin)
			wantStatus(t, c.postJSON("/api/repos/"+repoName+tt.path, tt.body), tt.want)
			e.noWrites(t)
		})
	}
}

func TestOwnersRuleIsSettings(t *testing.T) {
	const wording = "Name at least one owner. Owners are GitHub logins or org/team-slug, one per line, each named once."
	many := make([]string, 101)
	for i := range many {
		many[i] = fmt.Sprintf("owner-%d", i)
	}
	tests := []struct {
		name   string
		owners []string
	}{
		{"leading hyphen", []string{"-alice"}},
		{"team", []string{"acme/platform-team"}},
		{"duplicate ignoring case", []string{"alice", "Alice"}},
		{"not a login", []string{"not a login!"}},
		{"101 owners", many},
		{"only blank lines", nil},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			_, settingsErr := settings.Parse(settings.Render(settings.Settings{
				Version: 1, Owners: tt.owners, Gate: settings.GateAdvisory, Workflow: workflow, DataMode: settings.DataCustomer,
			}))
			e := newEnv(t)
			e.onb.progress = onboarding.Progress{Stage: onboarding.StageInstalled}
			body := onboardingJSON{Mode: "customer", Owners: "  " + strings.Join(tt.owners, " \r\n\r\n ") + "\n"}

			res := e.as("alice", permAdmin).postJSON("/api/repos/"+repoName+"/onboarding", body)

			if settingsErr == nil {
				wantStatus(t, res, http.StatusOK)
				if len(e.onb.customer) != 1 || !reflect.DeepEqual(e.onb.customer[0].owners, tt.owners) {
					t.Errorf("StartCustomer calls = %+v, want one with %v", e.onb.customer, tt.owners)
				}
				return
			}
			wantStatus(t, res, http.StatusUnprocessableEntity)
			wantContains(t, res.Body.String(), wording)
			if strings.Contains(res.Body.String(), "settings:") {
				t.Error("the page shows settings' error text")
			}
			if len(e.onb.customer) != 0 {
				t.Error("onboarding was called with owners that settings refuses")
			}
		})
	}
}

func TestSaveRejectsForeignDocuments(t *testing.T) {
	tests := []struct {
		name, content string
		want          int
		problem       diagnosticJSON
	}{
		{"prose with a colon", "Reminder: ask Bob about the layers", http.StatusUnprocessableEntity,
			diagnosticJSON{Line: 1, Column: 1, Message: "Unknown key `Reminder`."}},
		{"JSON with foreign keys", `{"schema_version": 3, "findings": []}`, http.StatusUnprocessableEntity,
			diagnosticJSON{Line: 1, Column: 2, Message: "Unknown key `schema_version`."}},
		{"mapping without version", "modules:\n  a:\n    layer: core\n", http.StatusUnprocessableEntity,
			diagnosticJSON{Line: 1, Column: 1, Message: "Missing required key `version`."}},
		{"one unknown top-level key", policyText + "reminder: true\n", http.StatusUnprocessableEntity,
			diagnosticJSON{Line: 9, Column: 1, Message: "Unknown key `reminder`."}},
		{"valid policy", policyText + "# edit\n", http.StatusOK, diagnosticJSON{}},
	}
	for _, tt := range tests {
		for _, route := range []struct {
			path string
			body any
		}{{policyAPI, editOf("", "", tt.content)}, {assistAPI, assistOf("", tt.content)}} {
			t.Run(tt.name+" "+route.path, func(t *testing.T) {
				e := newEnv(t)
				res := e.as("alice", permAdmin).postJSON(route.path, route.body)
				wantStatus(t, res, tt.want)
				if tt.want == http.StatusOK {
					if n := len(e.props.proposes); n != 1 {
						t.Errorf("Propose calls = %d, want 1", n)
					}
					return
				}
				e.noWrites(t)
				refusal := decodeAs[errorJSON](t, res)
				if refusal.Error != policyProblemsSentence || !slices.Contains(refusal.Diagnostics, tt.problem) {
					t.Errorf("refusal = %+v, want %q with %+v", refusal, policyProblemsSentence, tt.problem)
				}
				for _, leak := range []string{"engine config key", "is absent", "document is empty"} {
					if strings.Contains(res.Body.String(), leak) {
						t.Errorf("answer echoes Validate's error text %q", leak)
					}
				}
			})
		}
	}
}

var customerData = regexp.MustCompile(`(?s)"content":"(?:[^"\\]|\\.)*"|<script type="application/json".*?</script>`)

// randomNonce is the shell's per-response CSP nonce (base64: "6/0" can occur in it), random data and no wording.
var randomNonce = regexp.MustCompile(`[A-Za-z0-9+/]{22}==`)

var scorePattern = regexp.MustCompile(`(?i)\bscor(e|es|ing)\b|\bgrades?\b|\brank(ed|ing)?\b|\btotals?\b|\d\s*%|\d+\s*/\s*\d+|\b\d+ (of|out of) \d+\b`)

func TestNoScoreAnywhere(t *testing.T) {
	for _, fixture := range []string{"healthy", "real-blocked-no-baseline"} {
		t.Run(fixture, func(t *testing.T) {
			e := newEnv(t)
			// Alphabetical order is the opposite of health order here.
			names := map[string]string{"acme/a-blocked": "failure", "acme/b-attention": "neutral", "acme/c-healthy": "success"}
			i := 0
			for name, conclusion := range names {
				e.addRepo(name, installA, int64(60+i))
				e.grant("alice", name, permRead)
				e.gh.checks[name+"@"+sha(60+i)+"#"+checkState] = []api.CheckRun{{Conclusion: conclusion, Title: "Reason for " + conclusion}}
				i++
			}
			c := e.as("alice", permAdmin)
			draft := e.props.push(repoName, "archfit/edit-alice", policyText+"# draft\n")
			e.gh.checks[repoName+"@"+draft.HeadSHA+"#"+checkGate] = []api.CheckRun{{Conclusion: "failure", Title: "New violations"}}
			e.publishReport(repoName, artifact(t, fixture))
			e.acct.status = account.Status{Plan: account.PlanTrial, TrialEnds: e.now.AddDate(0, 0, 9)}

			for _, path := range []string{"/api/repos", "/api/repos/" + repoName + "/overview", "/api/repos/" + repoName + "/pulls/4", "/api/repos/" + repoName + "/graph", "/api/repos/" + repoName + "/findings", policyAPI, "/r/" + repoName + "/graph", "/r/" + repoName + "/data-sharing", "/api/repos/" + repoName + "/data-sharing"} {
				res := c.get(path)
				wantStatus(t, res, http.StatusOK)
				// Policy text and the view model are the customer's own data, not something ui computes.
				body := randomNonce.ReplaceAllString(customerData.ReplaceAllString(res.Body.String(), ""), "")
				if m := scorePattern.FindString(body); m != "" {
					t.Errorf("%s contains %q", path, m)
				}
			}

			overview := c.get("/api/repos").Body.String()
			byName := []int{strings.Index(overview, "acme/a-blocked"), strings.Index(overview, "acme/b-attention"), strings.Index(overview, "acme/c-healthy")}
			if byName[0] < 0 || !sort.IntsAreSorted(byName) {
				t.Errorf("overview is not sorted by name: %v", byName)
			}
		})
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
	for _, path := range []string{"/", "/r/" + repoName, "/r/" + repoName + "/policy", "/r/" + repoName + "/policy", "/r/" + repoName + "/data-sharing"} {
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
	for _, path := range []string{policyAPI, "/assets/app-1a2b.js", "/schema/policy.json"} {
		if got := c.get(path).Header().Get("Content-Security-Policy"); got != wantCSP {
			t.Errorf("%s: CSP = %q, want the policy without a nonce", path, got)
		}
	}
}

func TestHostileStringsAreEscaped(t *testing.T) {
	const (
		hostileRepo   = `acme/<script>alert("repo")</script>`
		hostileModule = `<img src=x onerror=alert(1)>`
		hostileText   = `</script><script>alert("x")</script>`
	)
	e := newEnv(t)
	e.addRepo(hostileRepo, installA, 70)
	e.grant("alice", hostileRepo, permRead)
	e.gh.checks[hostileRepo+"@"+sha(70)+"#"+checkState] = []api.CheckRun{{Conclusion: "neutral", Title: hostileText}}
	e.onb.progress = onboarding.Progress{Stage: onboarding.StageInstalled, Diagnostic: hostileText}
	c := e.as("alice", permAdmin)

	hostilePolicy := policyText + "  \"" + hostileModule + "\":\n    layer: '" + hostileText + "'\n"
	draft := e.props.push(repoName, "archfit/edit-alice", hostilePolicy)
	e.gh.checks[repoName+"@"+draft.HeadSHA+"#"+checkState] = []api.CheckRun{{Conclusion: "failure", Title: hostileText}}
	e.publishReport(repoName, artifact(t, "healthy"))
	pull := e.gh.pulls[repoName+"#4"]
	pull.Title, pull.Author, pull.HeadRef = hostileText, hostileModule, hostileText
	e.gh.pulls[repoName+"#4"] = pull
	e.gh.checks[repoName+"@"+sha(4)+"#"+checkState] = []api.CheckRun{{ID: 1, Conclusion: "neutral", Title: hostileText, ExternalID: "v1:0:0:"}}

	for _, path := range []string{"/", "/api/repos", "/r/" + repoName, "/api/repos/" + repoName + "/overview", "/r/" + repoName + "/policy",
		policyAPI, "/r/" + repoName + "/graph", "/r/" + repoName + "/data-sharing", "/r/" + repoName + "/pull/4", "/api/repos/" + repoName + "/pulls/4", "/r/" + repoName + "/pull/4/graph",
		"/api/repos/" + repoName + "/graph", "/api/repos/" + repoName + "/pulls/4/graph"} {
		res := c.get(path)
		wantStatus(t, res, http.StatusOK)
		body := res.Body.String()
		for _, raw := range []string{`<script>alert(`, `<img src=x`} {
			if strings.Contains(body, raw) {
				t.Errorf("%s contains unescaped %q", path, raw)
			}
		}
		if strings.HasPrefix(path, apiPrefix) && (strings.Contains(body, "<") || !strings.Contains(body, `\u003c`)) {
			t.Errorf("%s: JSON does not escape markup: %.200s", path, body)
		}
		if csp := cspOf(res); csp != wantCSP && csp != wantShellCSP {
			t.Errorf("%s: CSP = %q", path, csp)
		}
	}
	for _, path := range []string{"/nowhere", "/licenses.txt", "/schema/policy.json"} {
		if e.anonymous().get(path).Header().Get("Content-Security-Policy") == "" {
			t.Errorf("%s has no CSP header", path)
		}
	}
}

// The OAuth token stays on the server: no API answer carries it or the session cookie, whatever the route.
func TestNoAPIResponseHoldsASecret(t *testing.T) {
	e := newEnv(t)
	c := e.as("alice", permAdmin)
	e.publishReport(repoName, artifact(t, "healthy"))
	api := "/api/repos/" + repoName
	answers := []*httptest.ResponseRecorder{
		c.get("/api/session"),
		c.get("/api/repos"),
		c.get(api + "/overview"),
		c.get(api + "/pulls/4"),
		c.get(api + "/pulls/4/graph"),
		c.get(api + "/graph"),
		c.get(api + "/findings"),
		c.postJSON(api+"/onboarding", onboardingJSON{Mode: "customer", Owners: "alice"}),
		c.postJSON(api+"/discover", struct{}{}),
		c.get(api + "/policy"),
		c.postJSON(api+"/policy/check", checkPolicyJSON{Content: policyText}),
		c.postJSON(api+"/policy", editOf("", "", policyText+"# edit\n")),
		c.postJSON(api+"/assist", assistOf("", policyText+"# edit\n")),
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
	payload := bytes.ReplaceAll(artifact(t, "healthy"), []byte(`"go/packages":"go1.27.1"`), []byte(`"go/packages":"MARKER-report"`))
	e.publishReport(repoName, payload)
	e.acct.err = fmt.Errorf("MARKER-error")
	base := "/r/" + repoName

	wantStatus(t, c.postJSON(policyAPI, editOf("", "", policyText+"# MARKER-content\n")), http.StatusOK)
	wantStatus(t, c.postJSON(policyAPI, editOf("", "", "MARKER-broken: [")), http.StatusUnprocessableEntity)
	wantStatus(t, c.postJSON(policyAPI+"/check", checkPolicyJSON{Content: "MARKER-broken: ["}), http.StatusOK)
	req := assistOf(e.props.drafts[repoName+"@archfit/edit-alice"].HeadSHA, policyText+"# MARKER-content two\n")
	req.Instruction = "MARKER-instruction"
	wantStatus(t, c.postJSON(assistAPI, req), http.StatusOK)
	e.assist.propose = failingAssist(fmt.Errorf("MARKER-assist: %w", assist.ErrProvider))
	req.HeadSHA = e.props.drafts[repoName+"@archfit/edit-alice"].HeadSHA
	wantStatus(t, c.postJSON(assistAPI, req), http.StatusServiceUnavailable)
	sharing := "/api/repos/" + repoName + "/data-sharing"
	wantStatus(t, c.postJSON(sharing, dataSharingJSON{Kind: "MARKER-kind", Action: actionAllow}), http.StatusBadRequest)
	wantStatus(t, c.postJSON(sharing, dataSharingJSON{Kind: "managed_llm", Action: "MARKER-action"}), http.StatusBadRequest)
	wantStatus(t, c.postJSON(sharing, dataSharingJSON{Kind: "managed_llm", Action: actionAllow}), http.StatusOK)
	wantStatus(t, c.postJSON(sharing, dataSharingJSON{Kind: "managed_llm", Action: actionAllow}), http.StatusOK)
	wantStatus(t, c.postJSON(sharing, dataSharingJSON{Kind: "managed_llm", Action: actionRevoke}), http.StatusOK)
	wantStatus(t, c.get(base+"/graph"), http.StatusOK)
	wantStatus(t, c.get(base), http.StatusOK)
	wantStatus(t, c.get("/api/repos"), http.StatusOK)
	wantStatus(t, c.get("/api/repos/"+repoName+"/overview"), http.StatusOK) // reads account.Status, which fails with the marker

	out := logs.String()
	if !strings.Contains(out, `"route":"POST /api/repos/{o}/{n}/policy"`) || !strings.Contains(out, `"installation_id":1`) {
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
		for _, want := range []string{`"level":"INFO"`, `"outcome":"` + outcome + `"`, `"kind":"managed_llm"`, `"installation_id":1`, `"user_id":` + alice} {
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
	const internal = "dial tcp 10.0.0.7:443: secret-host refused"
	tests := []struct {
		name     string
		setup    func(e *env)
		path     string
		status   int
		sentence string
	}{
		{"a repository the user cannot see", func(*env) {}, "/r/acme/hidden", http.StatusNotFound, "Not found."},
		{"a pull request number that is not one", func(*env) {}, "/r/" + repoName + "/pull/0", http.StatusNotFound, "Not found."},
		{"a path of no page", func(*env) {}, "/static/app.css", http.StatusNotFound, "Not found."},
		{"a route's path with a method it does not take", func(*env) {}, "/auth/logout", http.StatusNotFound, "Not found."},
		{"GitHub fails with its own text", func(e *env) { e.gh.fail["Permission"] = errors.New(internal) }, "/r/" + repoName,
			http.StatusBadGateway, "GitHub or an Archfit service failed. Nothing was changed. Try again."},
		{"GitHub rate limits", func(e *env) { e.gh.fail["Permission"] = fmt.Errorf("%w: %s", api.ErrRateLimited, internal) }, "/r/" + repoName,
			http.StatusServiceUnavailable, "GitHub is rate limiting Archfit. Try again in a minute."},
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

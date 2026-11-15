package ui

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"
)

type route struct {
	method, path, min string
	form              url.Values // a form body: the only form route left is the logout
	body              any        // a JSON body: an /api write
}

func (rt route) api() bool { return strings.HasPrefix(rt.path, apiPrefix) }

// signedInOnly is the minimum of a repository page: the shell, the same for every repository, needs only a session.
const signedInOnly = ""

// authorized is true for a route that authorizes its repository: every /api/repos route, no page.
func (rt route) authorized() bool { return rt.min != signedInOnly }

// repoRoutes is every repository route with its minimum permission and a body that succeeds.
func repoRoutes(fullName string) []route {
	base, api := "/r/"+fullName, "/api/repos/"+fullName
	return []route{
		{http.MethodGet, base, signedInOnly, nil, nil},
		{http.MethodGet, base + "/policy", signedInOnly, nil, nil},
		{http.MethodGet, base + "/owner/first", signedInOnly, nil, nil},
		{http.MethodGet, api + "/owner/first/view", permRead, nil, nil},
		{http.MethodGet, base + "/owner/first?pull=7", signedInOnly, nil, nil},
		{http.MethodGet, api + "/owner/first/view?pull=7", permRead, nil, nil},
		{http.MethodGet, base + "/owner/workflow?pull=7", signedInOnly, nil, nil},
		{http.MethodGet, api + "/owner/workflow/view?pull=7", permRead, nil, nil},
		{http.MethodGet, base + "/owner/confirm", signedInOnly, nil, nil},
		{http.MethodGet, api + "/owner/confirm/view", permRead, nil, nil},
		{http.MethodGet, base + "/owner/revert", signedInOnly, nil, nil},
		{http.MethodGet, api + "/owner/revert/view", permRead, nil, nil},
		{http.MethodGet, api + "/setup", permRead, nil, nil},
		{http.MethodGet, api + "/policy", permRead, nil, nil},
		{http.MethodGet, api + "/requests/431", permRead, nil, nil},
		{http.MethodGet, api + "/requests/431/preview", permRead, nil, nil},
		{http.MethodGet, base + "/pull/4", signedInOnly, nil, nil},
		{http.MethodGet, api + "/pulls/4", permRead, nil, nil},
		{http.MethodGet, base + "/passkeys", signedInOnly, nil, nil},
		{http.MethodGet, api + "/passkeys", permRead, nil, nil},
		{http.MethodPost, api + "/passkeys/register", permRead, nil, passkeyTokenJSON{}},
		{http.MethodPost, api + "/passkeys/register/finish", permRead, nil, passkeyCredentialJSON{Credential: json.RawMessage(`{"id":"x"}`)}},
		{http.MethodPost, api + "/passkeys/step-up", permRead, nil, stepUpJSON{Action: "approve", Request: 431}},
		{http.MethodPost, api + "/passkeys/step-up/finish", permRead, nil, passkeyCredentialJSON{Credential: json.RawMessage(`{"id":"x"}`)}},
		{http.MethodPost, api + "/passkeys/" + fakePasskeyID + "/remove", permRead, nil, passkeyTokenJSON{Token: fakeToken}},
		{http.MethodGet, base + "/requests/431", signedInOnly, nil, nil},
		{http.MethodPost, api + "/requests/431/answer/digest", permRead, nil, answerJSON{Answer: "approve"}},
		{http.MethodPost, api + "/requests/431/answer", permRead, nil, answerJSON{Answer: "reject", Instead: "Use the port."}},
	}
}

var permissionRank = map[string]int{"none": 0, permRead: 1, permWrite: 2, permAdmin: 3}

func (c *client) send(rt route) int {
	if rt.method == http.MethodGet {
		return c.get(rt.path).Code
	}
	if rt.api() {
		return c.postJSON(rt.path, rt.body).Code
	}
	form := url.Values{}
	for k, v := range rt.form {
		form[k] = v
	}
	return c.post(rt.path, form).Code
}

// archfit3Routes are the routes of the Archfit 3 decision requests, owner approvals and overrides, policy editor and
// AI assist. Archfit 4 asks with an issue and answers on the request page; their old handlers are gone.
var archfit3Routes = []struct{ method, path string }{
	{http.MethodGet, "/r/" + repoName + "/decisions"},
	{http.MethodGet, "/api/repos/" + repoName + "/decisions"},
	{http.MethodPost, "/api/repos/" + repoName + "/pulls/4/approve"},
	{http.MethodPost, "/api/repos/" + repoName + "/pulls/4/request-changes"},
	{http.MethodPost, "/api/repos/" + repoName + "/pulls/4/withdraw"},
	{http.MethodPost, "/api/repos/" + repoName + "/pulls/4/override"},
	{http.MethodPost, "/api/repos/" + repoName + "/pulls/4/refresh-decision"},
	{http.MethodPost, "/api/repos/" + repoName + "/pulls/4/decisions/" + strings.Repeat("f", 16) + "/" + sha(9) + "/approve"},
	{http.MethodPost, "/api/repos/" + repoName + "/pulls/4/decisions/" + strings.Repeat("f", 16) + "/" + sha(9) + "/decline"},
	{http.MethodPost, "/api/repos/" + repoName + "/policy"},
	{http.MethodPost, "/api/repos/" + repoName + "/policy/check"},
	{http.MethodPost, "/api/repos/" + repoName + "/policy/ready"},
	{http.MethodPost, "/api/repos/" + repoName + "/assist"},
}

func TestArchfit3RoutesAreGone(t *testing.T) {
	e := newEnv(t)
	c := e.as("alice", permAdmin)
	for _, rt := range archfit3Routes {
		var res *httptest.ResponseRecorder
		if rt.method == http.MethodGet {
			res = c.get(rt.path)
		} else {
			res = c.postJSON(rt.path, map[string]string{"content": policyText, "head_sha": sha(4)})
		}
		if res.Code != http.StatusNotFound {
			t.Errorf("%s %s = %d, want 404", rt.method, rt.path, res.Code)
		}
	}
	// The Archfit 4 routes beside them still answer.
	for _, rt := range repoRoutes(repoName) {
		if got := c.send(rt); got != http.StatusOK {
			t.Errorf("%s %s = %d, want 200", rt.method, rt.path, got)
		}
	}
}

func TestRoutesAndMinimumPermissions(t *testing.T) {
	for _, rt := range repoRoutes(repoName) {
		t.Run(rt.method+" "+rt.path, func(t *testing.T) {
			e := newEnv(t)
			res := e.anonymous().do(requestOf(rt))
			if rt.method == http.MethodGet && !rt.api() {
				wantStatus(t, res, http.StatusSeeOther)
				if got, want := res.Header().Get("Location"), "/auth/login?return="+url.QueryEscape(rt.path); got != want {
					t.Errorf("Location = %q, want %q", got, want)
				}
			} else {
				wantStatus(t, res, http.StatusUnauthorized)
			}

			for _, perm := range []string{"none", permRead, permWrite, permAdmin} {
				e := newEnv(t)
				c := e.as("alice", perm)
				got := c.send(rt)
				switch {
				case permissionRank[perm] < permissionRank[rt.min]:
					if got != http.StatusNotFound {
						t.Errorf("%s: status = %d, want 404", perm, got)
					}
				case got != http.StatusOK:
					t.Errorf("%s: status = %d, want 200", perm, got)
				}
			}
		})
	}

	t.Run("overview", func(t *testing.T) {
		e := newEnv(t)
		res := e.anonymous().get("/")
		wantStatus(t, res, http.StatusOK)
		wantContains(t, res.Body.String(), `src="/assets/app-1a2b.js"`)
		for _, path := range []string{"/api/session", "/api/repos"} {
			wantStatus(t, e.anonymous().get(path), http.StatusUnauthorized)
			wantStatus(t, e.as("alice", permRead).get(path), http.StatusOK)
		}
	})
	t.Run("logout", func(t *testing.T) {
		e := newEnv(t)
		wantStatus(t, e.anonymous().post("/auth/logout", nil), http.StatusUnauthorized)
		c := e.as("alice", "none")
		wantStatus(t, c.get("/api/session"), http.StatusOK)
		wantStatus(t, c.post("/auth/logout", nil), http.StatusSeeOther)
		wantStatus(t, c.get("/api/session"), http.StatusUnauthorized)
	})
	t.Run("public assets", func(t *testing.T) {
		e := newEnv(t)
		for _, path := range []string{"/assets/app-1a2b.js", licensesRoute} {
			res := e.anonymous().get(path)
			wantStatus(t, res, http.StatusOK)
			if res.Body.Len() == 0 {
				t.Errorf("%s is empty", path)
			}
		}
		if got := e.anonymous().get("/assets/app-1a2b.js").Header().Get("Cache-Control"); got != immutableCache {
			t.Errorf("asset Cache-Control = %q, want %q", got, immutableCache)
		}
		licences := e.anonymous().get(licensesRoute)
		if got := licences.Header().Get("Cache-Control"); got != "no-cache" || licences.Body.String() != string(testWeb["assets/licenses-9f8e.txt"].Data) {
			t.Errorf("licences: Cache-Control %q, body %q; want no-cache and the build's licence file", got, licences.Body.String())
		}
		for _, path := range []string{"/schema/policy.json", "/static/app.css", "/assets/", "/assets/missing.js", "/index.html", "/api/nothing"} {
			wantStatus(t, e.anonymous().get(path), http.StatusNotFound)
		}
	})
}

func requestOf(rt route) *http.Request {
	req, _ := http.NewRequest(rt.method, baseURL+rt.path, strings.NewReader(rt.form.Encode()))
	if rt.method == http.MethodPost {
		req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	}
	return req
}

func TestPullPageNumberMustBeAPull(t *testing.T) {
	e := newEnv(t)
	c := e.as("alice", permRead)
	for _, n := range []string{"0", "-4", "x", "4x", "5"} {
		if n != "5" { // a number that is no pull request is found only by the API's GitHub call
			wantStatus(t, c.get("/r/"+repoName+"/pull/"+n), http.StatusNotFound)
		}
		wantStatus(t, c.get("/api/repos/"+repoName+"/pulls/"+n), http.StatusNotFound)
		if n != "5" {
			wantStatus(t, c.get("/r/"+repoName+"/pull/"+n+"/graph"), http.StatusNotFound)
		}
		wantStatus(t, c.get("/api/repos/"+repoName+"/pulls/"+n+"/graph"), http.StatusNotFound)
	}
}

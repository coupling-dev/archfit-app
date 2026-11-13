package ui

import (
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"net/url"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/alexei-led/archfit-app/internal/control/account"
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
		{http.MethodGet, api + "/overview", permRead, nil, nil},
		{http.MethodGet, api + "/setup", permRead, nil, nil},
		{http.MethodGet, api + "/policy", permRead, nil, nil},
		{http.MethodGet, api + "/requests/431", permRead, nil, nil},
		{http.MethodGet, api + "/requests/431/preview", permRead, nil, nil},
		{http.MethodPost, api + "/onboarding", permAdmin, nil, map[string]string{"mode": "customer", "owners": "alice"}},
		{http.MethodPost, api + "/discover", permAdmin, nil, struct{}{}},
		{http.MethodGet, base + "/graph", signedInOnly, nil, nil},
		{http.MethodGet, base + "/pull/4", signedInOnly, nil, nil},
		{http.MethodGet, api + "/pulls/4", permRead, nil, nil},
		{http.MethodGet, api + "/pulls/4/graph", permRead, nil, nil},
		{http.MethodGet, api + "/graph", permRead, nil, nil},
		{http.MethodGet, api + "/findings", permRead, nil, nil},
		{http.MethodGet, base + "/pull/4/graph", signedInOnly, nil, nil},
		{http.MethodGet, base + "/data-sharing", signedInOnly, nil, nil},
		{http.MethodGet, api + "/data-sharing", permAdmin, nil, nil},
		{http.MethodPost, api + "/data-sharing", permAdmin, nil, dataSharingJSON{Kind: "source_processing", Action: actionAllow}},
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
	e.noWrites(t)
	// The Archfit 4 routes beside them still answer.
	for _, rt := range repoRoutes(repoName) {
		if got := c.send(rt); got != rt.expectedStatus() {
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
				case got != rt.expectedStatus():
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

func TestDataSharingAllowAndRevoke(t *testing.T) {
	e := newEnv(t)
	c := e.as("alice", permAdmin)
	path := "/api/repos/" + repoName + "/data-sharing"
	feature := func(t *testing.T, body dataSharingBody, kind account.ConsentKind) featureJSON {
		t.Helper()
		for _, f := range body.Features {
			if f.Kind == string(kind) {
				return f
			}
		}
		t.Fatalf("no %s in %+v", kind, body)
		return featureJSON{}
	}
	kind := account.ConsentSourceProcessing
	allow := dataSharingJSON{Kind: string(kind), Action: actionAllow}
	revoke := dataSharingJSON{Kind: string(kind), Action: actionRevoke}

	var before dataSharingBody
	c.getJSON(path, &before)
	discovery := feature(t, before, kind)
	owner, _, _ := strings.Cut(repoName, "/")
	if before.Owner != owner || discovery.Allowed || discovery.By != "" || discovery.At != "" || len(before.Features) != len(sharedFeatures) {
		t.Errorf("before = %+v", before)
	}
	if want := []precondition{{discovery.Checks[0].Label, checkNotMet}}; !reflect.DeepEqual(discovery.Checks, want) {
		t.Errorf("checks = %+v; want the consent unmet", discovery.Checks)
	}
	if discovery.Note == "" || discovery.Offered {
		t.Errorf("managed discovery = %+v; want the note that the App does not start it, not offered", discovery)
	}

	t.Run("a consent given earlier for a feature the page does not offer can be revoked", func(t *testing.T) {
		_ = e.consents.Record(t.Context(), installA, account.Consent{Kind: account.ConsentSourceProcessing})
		discovery := dataSharingJSON{Kind: string(account.ConsentSourceProcessing), Action: actionRevoke}
		wantStatus(t, c.postJSON(path, discovery), http.StatusOK)
		if _, ok := e.consents.recorded[installA][account.ConsentSourceProcessing]; ok {
			t.Error("the earlier consent was not revoked")
		}
	})

	res := c.postJSON(path, allow)
	wantStatus(t, res, http.StatusOK)
	got := e.consents.recorded[installA][kind]
	alice := e.user("alice")
	if got.By != "alice" || got.ByID != alice.UserID || !got.At.Equal(e.now) || got.Kind != kind {
		t.Errorf("consent = %+v; want alice (%d) at %v", got, alice.UserID, e.now)
	}
	var after dataSharingBody
	if err := json.Unmarshal(res.Body.Bytes(), &after); err != nil {
		t.Fatal(err)
	}
	if f := feature(t, after, kind); !f.Allowed || f.By != "alice" || f.At != e.now.UTC().Format(time.RFC3339) ||
		f.Checks[0].State != checkMet || after.Notice == "" {
		t.Errorf("after allow = %+v", after)
	}

	t.Run("allowing again keeps who allowed it and when", func(t *testing.T) {
		writes := e.consents.writes
		e.now = e.now.Add(time.Hour)
		wantStatus(t, e.as("bob", permAdmin).postJSON(path, allow), http.StatusOK)
		if again := e.consents.recorded[installA][kind]; again != got || e.consents.writes != writes {
			t.Errorf("consent = %+v after %d writes; want %+v unchanged", again, e.consents.writes-writes, got)
		}
	})

	wantStatus(t, c.postJSON(path, revoke), http.StatusOK)
	if e.consents.Has(t.Context(), installA, kind) {
		t.Error("consent still recorded after revoke")
	}
	wantStatus(t, c.postJSON(path, revoke), http.StatusOK) // revoking nothing is not an error

	for _, body := range []any{
		dataSharingJSON{Kind: "everything", Action: actionAllow},
		dataSharingJSON{Kind: "managed_llm", Action: actionAllow},
		dataSharingJSON{Kind: "source_processing", Action: "record"},
		dataSharingJSON{Kind: "source_processing"},
		dataSharingJSON{Action: actionAllow},
	} {
		wantStatus(t, c.postJSON(path, body), http.StatusBadRequest)
	}
	if e.consents.Has(t.Context(), installA, kind) || e.consents.Has(t.Context(), installA, "managed_llm") {
		t.Error("a refused write recorded a consent")
	}
}

func TestDataSharingNeverShowsAnOutageAsNotAllowed(t *testing.T) {
	e := newEnv(t)
	c := e.as("alice", permAdmin)
	_ = e.consents.Record(t.Context(), installA, account.Consent{Kind: account.ConsentSourceProcessing, By: "alice", ByID: 1, At: e.now})
	e.consents.readErr = errors.New("store down")
	writes := e.consents.writes

	path := "/api/repos/" + repoName + "/data-sharing"
	wantStatus(t, c.get(path), http.StatusBadGateway)
	wantStatus(t, c.postJSON(path, dataSharingJSON{Kind: string(account.ConsentSourceProcessing), Action: actionAllow}), http.StatusBadGateway)
	if e.consents.writes != writes {
		t.Error("allow wrote a consent it could not read first")
	}

	t.Run("a change saved but not read back does not say nothing changed", func(t *testing.T) {
		e := newEnv(t)
		e.consents.readErrAfterWrite = errors.New("store down")
		res := e.as("alice", permAdmin).postJSON(path, dataSharingJSON{Kind: string(account.ConsentSourceProcessing), Action: actionAllow})
		wantStatus(t, res, http.StatusBadGateway)
		wantContains(t, res.Body.String(), "The change was saved")
		if _, ok := e.consents.recorded[installA][account.ConsentSourceProcessing]; !ok {
			t.Error("the consent was not recorded")
		}
	})
}

// The redirect reads only the path: the same answer for every user and every repository, so it confirms nothing.
func TestOldConsentPageMovedToDataSharing(t *testing.T) {
	for _, perm := range []string{"none", permRead, permAdmin} {
		for _, repo := range []string{repoName, otherRepo, "acme/missing"} {
			e := newEnv(t)
			res := e.as("alice", perm).get("/r/" + repo + "/consent")
			wantStatus(t, res, http.StatusPermanentRedirect)
			if loc := res.Header().Get("Location"); loc != "/r/"+repo+"/data-sharing" {
				t.Errorf("%s on %s: Location = %q", perm, repo, loc)
			}
			if n := len(e.gh.called("Permission")); n != 0 {
				t.Errorf("%s on %s: %d permission lookups, want none", perm, repo, n)
			}
		}
	}
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

func (rt route) expectedStatus() int {
	for _, suffix := range []string{"/overview", "/onboarding", "/discover", "/pulls/4/graph", "/graph", "/findings"} {
		if rt.api() && strings.HasSuffix(rt.path, suffix) {
			return http.StatusConflict
		}
	}
	return http.StatusOK
}

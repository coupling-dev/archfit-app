package internal_test

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

func TestUploadProtocolAcceptsApprovedProducer(t *testing.T) {
	f := loadFixture(t, "real-healthy")
	w := newWorld(t)
	w.seedDemo(f, "required", true)
	a := startApp(t, w)

	got := readAnswer(t, a.do(demoUpload(f).request(t, "/v1/reports")))
	if got.Status/100 != 2 || got.Conclusion != "success" {
		t.Fatalf("upload answered %+v; want 2xx success", got)
	}

	gateCheck := w.check(demoRepo, demoHead, checkGate)
	if gateCheck == nil || gateCheck.Status != "completed" || gateCheck.Conclusion != "success" {
		t.Fatalf("archfit/gate = %+v; want completed success", gateCheck)
	}
	if state := w.check(demoRepo, demoHead, checkState); state == nil || state.Status != "completed" {
		t.Fatalf("archfit/state = %+v; want a completed check", state)
	}
	rc := gateCheck.receipt(t)
	want := receipt{
		RunID: demoRun, RunAttempt: 1, ReportDigest: "sha256:" + digest(f.report), PolicyDigest: digest(f.policy),
		BaselineDigest: digest(f.baseline), Conclusion: "success", Reason: "healthy",
	}
	if rc != want {
		t.Errorf("receipt = %+v\nwant      %+v", rc, want)
	}
	comments := w.commentsOf(demoRepo, demoPull)
	if len(comments) != 1 || !strings.HasPrefix(comments[0].Body, commentMarker) {
		t.Fatalf("comments = %+v; want one carrying the marker", comments)
	}
	if published := w.everythingPublished(); !strings.Contains(published, "customer-attested") || strings.Contains(strings.ToLower(published), "verified") {
		t.Error(`feedback must say "customer-attested" and never "verified"`)
	}
	if http.DefaultTransport != router {
		t.Fatal("the network guard was replaced")
	}
}

func TestUploadProtocolRejectsInvalidProvenance(t *testing.T) {
	f := loadFixture(t, "real-healthy")
	w := newWorld(t)
	w.seedDemo(f, "required", true)
	w.install(7)
	w.addRepo(7, 7007, "mallory/other", true)
	a := startApp(t, w)

	claim := func(u upload, k string, v any) upload {
		claims := map[string]any{}
		for ck, cv := range u.claims {
			claims[ck] = cv
		}
		claims[k] = v
		u.claims = claims
		return u
	}
	cases := []struct {
		name   string
		status int
		mutate func(u upload) upload
	}{
		{"bad signature", http.StatusUnauthorized, func(u upload) upload { u.key = attackerKey(); return u }},
		{"wrong audience", http.StatusUnauthorized, func(u upload) upload { return claim(u, "aud", "https://other.example") }},
		{"expired token", http.StatusUnauthorized, func(u upload) upload {
			u = claim(u, "iat", time.Now().Add(-2*time.Hour).Unix())
			return claim(u, "exp", time.Now().Add(-time.Hour).Unix())
		}},
		{"no token", http.StatusUnauthorized, func(u upload) upload { u.noToken = true; return u }},
		{"wrong tenant: token of another repository", http.StatusForbidden, func(u upload) upload {
			u = claim(u, "repository", "mallory/other")
			return claim(u, "repository_id", "7007")
		}},
		{"wrong tenant: repository ID of another repository", http.StatusForbidden, func(u upload) upload {
			return claim(u, "repository_id", "7007")
		}},
		{"wrong tenant: App not installed", http.StatusForbidden, func(u upload) upload {
			u.env.Repository = "nobody/nothing"
			return claim(u, "repository", "nobody/nothing")
		}},
		{"modified report bytes", http.StatusBadRequest, func(u upload) upload {
			u.body = append([]byte(" "), u.body...)
			return u
		}},
		{"oversized body", http.StatusRequestEntityTooLarge, func(u upload) upload {
			u.body = append(u.body, make([]byte, 5<<20)...)
			return u
		}},
		{"unknown envelope field", http.StatusBadRequest, func(u upload) upload {
			raw, _ := json.Marshal(u.env)
			u.rawEnvelope = strings.TrimSuffix(string(raw), "}") + `,"installation_id":42}`
			return u
		}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := readAnswer(t, a.do(tc.mutate(demoUpload(f)).request(t, "/v1/reports")))
			if got.Status != tc.status || got.Error == "" || got.Conclusion != "" {
				t.Errorf("answer = %+v; want status %d with an error code", got, tc.status)
			}
			if writes := w.publishingWrites(); len(writes) != 0 {
				t.Errorf("a rejected upload published: %v", writes)
			}
			if runs := w.checkRuns(demoRepo); len(runs) != 0 {
				t.Errorf("a rejected upload left check runs: %+v", runs)
			}
		})
	}
}

func TestRoutesRequireTheirAuthentication(t *testing.T) {
	f := loadFixture(t, "real-healthy")
	w := newWorld(t)
	w.seedDemo(f, "required", true)
	w.allow("alice", demoRepo, "admin")
	a := startApp(t, w)

	if resp := a.do(httptest.NewRequest(http.MethodGet, baseURL+"/health", nil)); drain(resp) != http.StatusOK {
		t.Errorf("GET /health = %d; want 200 without credentials", resp.StatusCode)
	}

	user := newBrowser(t, a.do)
	user.signIn("alice")
	if resp, _ := user.get("/r/" + demoRepo); resp.StatusCode != http.StatusOK {
		t.Fatalf("a signed-in admin got %d on the repository page; the session does not work", resp.StatusCode)
	}
	session := "archfit_session=" + user.cookies["archfit_session"]

	routes := []struct{ method, path string }{
		{http.MethodPost, "/v1/reports"},
		{http.MethodPost, "/v1/discoveries"},
		{http.MethodPost, "/v1/baselines"},
		{http.MethodPost, "/webhooks/github"},
		{http.MethodPost, "/internal/sweep"},
		{http.MethodPost, "/internal/grants"},
		{http.MethodGet, "/r/" + demoRepo},
		{http.MethodGet, "/r/" + demoRepo + "/policy"},
		{http.MethodGet, "/r/" + demoRepo + "/data-sharing"},
		{http.MethodGet, "/r/" + demoRepo + "/consent"},
		{http.MethodGet, "/api/repos/" + demoRepo + "/data-sharing"},
		{http.MethodGet, "/api/repos/" + demoRepo + "/overview"},
		{http.MethodPost, "/api/repos/" + demoRepo + "/onboarding"},
		{http.MethodPost, "/api/repos/" + demoRepo + "/discover"},
		{http.MethodPost, "/r/" + demoRepo + "/policy"},
		{http.MethodPost, "/api/repos/" + demoRepo + "/data-sharing"},
		{http.MethodPost, "/auth/logout"},
	}
	credentials := []struct {
		name string
		set  func(r *http.Request)
	}{
		{"none", func(*http.Request) {}},
		{"wrong bearer", func(r *http.Request) { r.Header.Set("Authorization", "Bearer wrong") }},
		{"wrong signature", func(r *http.Request) { r.Header.Set("X-Hub-Signature-256", sign("wrong", nil)) }},
		{"forged session", func(r *http.Request) { r.Header.Set("Cookie", "archfit_session=forged") }},
	}
	for _, rt := range routes {
		for _, cred := range credentials {
			t.Run(rt.method+" "+rt.path+" "+cred.name, func(t *testing.T) {
				r := httptest.NewRequest(rt.method, baseURL+rt.path, strings.NewReader(`{"installation_id":42,"until":"2099-01-01T00:00:00Z"}`))
				switch rt.path {
				case "/v1/reports": // a well-formed upload, so that the answer is about the credential
					r = noToken(demoUpload(f)).request(t, rt.path)
				case "/v1/discoveries":
					r = noToken(discoveryUpload(demoRepo, demoRepoID, 555, demoBase, f.policy)).request(t, rt.path)
				case "/v1/baselines":
					r = noToken(baselineUpload(demoRepo, demoRepoID, 555, demoBase, f.baseline, f.labels)).request(t, rt.path)
				}
				r.Header.Set("X-GitHub-Event", "installation")
				r.Header.Set("X-GitHub-Delivery", "d-1")
				cred.set(r)
				resp := a.do(r)
				status := drain(resp)
				toLogin := status == http.StatusSeeOther && strings.HasPrefix(resp.Header.Get("Location"), "/auth/login")
				if status != http.StatusUnauthorized && status != http.StatusForbidden && !toLogin {
					t.Errorf("status %d (Location %q); want 401, 403 or the login redirect", status, resp.Header.Get("Location"))
				}
			})
		}
	}

	for _, path := range []string{"/internal/sweep", "/internal/grants"} {
		t.Run(path+" refuses a user session", func(t *testing.T) {
			r := httptest.NewRequest(http.MethodPost, baseURL+path, strings.NewReader(`{"installation_id":42,"until":"2099-01-01T00:00:00Z"}`))
			r.Header.Set("Cookie", session)
			if status := drain(a.do(r)); status != http.StatusUnauthorized && status != http.StatusForbidden {
				t.Errorf("status %d; want 401 or 403", status)
			}
		})
	}
	if writes := w.publishingWrites(); len(writes) != 0 {
		t.Errorf("unauthenticated requests wrote to GitHub: %v", writes)
	}
	if resp := a.operator("/internal/sweep", ""); drain(resp) != http.StatusOK {
		t.Errorf("the operator token got %d on /internal/sweep; want 200", resp.StatusCode)
	}
}

func noToken(u upload) upload {
	u.noToken = true
	return u
}

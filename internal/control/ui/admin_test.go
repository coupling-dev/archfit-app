package ui

import (
	"net/http"
	"strconv"
	"testing"
	"time"

	"github.com/alexei-led/archfit-app/internal/github/auth"
)

const (
	strangerRepo = "stranger/tool"
	installC     = int64(3)
)

func approvePath(id int64, action string) string {
	return "/api/admin/installations/" + strconv.FormatInt(id, 10) + "/" + action
}

func strangerEnv(t *testing.T) *env {
	e := newEnv(t)
	e.addRepo(strangerRepo, installC, 30)
	return e
}

func TestRepositoryOfWaitingAccountAnswersWaiting(t *testing.T) {
	e := strangerEnv(t)
	e.grant("alice", strangerRepo, "admin")
	c := e.as("alice", "admin")

	for _, path := range []string{"overview", "findings", "policy", "data-sharing"} {
		res := c.get("/api/repos/" + strangerRepo + "/" + path)
		wantStatus(t, res, http.StatusForbidden)
		if got := decodeAs[errorJSON](t, res); got.Waiting != "stranger" {
			t.Errorf("%s: waiting = %q; want stranger", path, got.Waiting)
		}
	}
	wantStatus(t, c.get("/api/repos/"+repoName+"/overview"), http.StatusOK)

	var list overviewBody
	c.getJSON("/api/repos", &list)
	for _, r := range list.Repos {
		if want := r.FullName == strangerRepo; r.Waiting != want {
			t.Errorf("%s: waiting = %v; want %v", r.FullName, r.Waiting, want)
		}
		if r.Waiting && r.State != stateWaiting {
			t.Errorf("%s: state = %q; want %q", r.FullName, r.State, stateWaiting)
		}
	}
}

func TestAdminIsNotFoundForNonOperators(t *testing.T) {
	e := strangerEnv(t)
	c := e.as("alice", "admin")

	wantStatus(t, c.get("/admin"), http.StatusNotFound)
	wantStatus(t, c.get("/api/admin/installations"), http.StatusNotFound)
	wantStatus(t, c.postJSON(approvePath(installC, "approve"), struct{}{}), http.StatusNotFound)
	wantStatus(t, c.postJSON(approvePath(installC, "revoke"), struct{}{}), http.StatusNotFound)
	if got := decodeAs[sessionJSON](t, c.get("/api/session")); got.Operator || got.Waiting != 0 {
		t.Errorf("session = %+v; want no operator fields", got)
	}
	if ok, _ := e.admit.Admitted(t.Context(), installC, "stranger"); ok {
		t.Fatal("a non-operator approved an installation")
	}
}

func TestOperatorApprovesThenRevokes(t *testing.T) {
	e := strangerEnv(t)
	e.grant("alice", strangerRepo, "admin")
	alice := e.as("alice", "admin")
	ops := e.signIn(operatorLogin)

	wantStatus(t, ops.get("/admin"), http.StatusOK)
	if got := decodeAs[sessionJSON](t, ops.get("/api/session")); !got.Operator || got.Waiting != 1 {
		t.Fatalf("session = %+v; want operator with 1 waiting", got)
	}
	var before adminBody
	ops.getJSON("/api/admin/installations", &before)
	if len(before.Waiting) != 1 || before.Waiting[0].Account != "stranger" || len(before.Waiting[0].Repos) != 1 {
		t.Fatalf("waiting = %+v; want stranger with its one repository", before.Waiting)
	}
	if len(before.Approved) != 2 || !before.Approved[0].Auto || !before.Approved[1].Auto {
		t.Fatalf("approved = %+v; want acme and rival, by configuration", before.Approved)
	}

	wantStatus(t, ops.postJSON(approvePath(installC, "approve"), struct{}{}), http.StatusOK)
	var after adminBody
	ops.getJSON("/api/admin/installations", &after)
	if len(after.Waiting) != 0 || len(after.Approved) != 3 {
		t.Fatalf("after approve: %+v", after)
	}
	row := after.Approved[2]
	if row.Account != "stranger" || row.Auto || row.ApprovedBy != operatorLogin || row.ApprovedAt == nil || !row.ApprovedAt.Equal(e.now) {
		t.Errorf("approved row = %+v; want stranger by %s at %v", row, operatorLogin, e.now)
	}
	wantStatus(t, alice.get("/api/repos/"+strangerRepo+"/overview"), http.StatusOK)

	e.now = e.now.Add(time.Hour)
	wantStatus(t, ops.postJSON(approvePath(installC, "revoke"), struct{}{}), http.StatusOK)
	wantStatus(t, alice.get("/api/repos/"+strangerRepo+"/overview"), http.StatusForbidden)
	var revoked adminBody
	ops.getJSON("/api/admin/installations", &revoked)
	if len(revoked.Waiting) != 1 || revoked.Waiting[0].ApprovedAt != nil {
		t.Errorf("after revoke: waiting = %+v; want stranger, no approval", revoked.Waiting)
	}
}

func TestOperatorCannotChangeConfiguredOrUnknownInstallations(t *testing.T) {
	e := strangerEnv(t)
	ops := e.signIn(operatorLogin)

	cases := []struct {
		path string
		want int
	}{
		{approvePath(installA, "approve"), http.StatusConflict},
		{approvePath(installA, "revoke"), http.StatusConflict},
		{approvePath(99, "approve"), http.StatusNotFound},
		{"/api/admin/installations/x/approve", http.StatusNotFound},
		{"/api/admin/installations/-3/approve", http.StatusNotFound},
	}
	for _, c := range cases {
		wantStatus(t, ops.postJSON(c.path, struct{}{}), c.want)
	}
	wantStatus(t, ops.postJSONWith(approvePath(installC, "approve"), struct{}{}, ""), http.StatusForbidden)
	if ok, _ := e.admit.Admitted(t.Context(), installC, "stranger"); ok {
		t.Fatal("an approve without the CSRF token went through")
	}
}

func TestOperatorIsTheUserIDNotTheLogin(t *testing.T) {
	e := strangerEnv(t)
	// A renamed operator's login, registered again by someone else.
	e.oauth.tokens["code-impostor"] = auth.UserToken{AccessToken: "tok-impostor", Login: operatorLogin, UserID: 4242}
	c := e.signIn("impostor")

	wantStatus(t, c.get("/admin"), http.StatusNotFound)
	wantStatus(t, c.postJSON(approvePath(installC, "approve"), struct{}{}), http.StatusNotFound)
	if got := decodeAs[sessionJSON](t, c.get("/api/session")); got.Operator {
		t.Errorf("session = %+v; want no operator", got)
	}
}

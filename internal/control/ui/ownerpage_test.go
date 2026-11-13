package ui

import (
	"net/http"
	"testing"
)

func TestOwnerPageDeepLinksServeTheShellWithoutRepositoryReads(t *testing.T) {
	for _, suffix := range []string{"first", "first?pull=7", "workflow?pull=7", "confirm", "revert"} {
		for _, repo := range []string{repoName, "missing/private"} {
			t.Run(repo+"/"+suffix, func(t *testing.T) {
				e := newEnv(t)
				res := e.signIn("alice").get("/r/" + repo + "/owner/" + suffix)
				wantStatus(t, res, http.StatusOK)
				wantContains(t, res.Body.String(), "app-error")
				if len(e.owners.actions) > 0 || len(e.gh.called("Permission")) > 0 {
					t.Fatal("shell loaded repository data")
				}
			})
		}
	}
}

func TestOwnerPageRefusesMalformedDeepLinks(t *testing.T) {
	for _, suffix := range []string{"workflow", "first?pull=x", "first?pull=", "first?pull=0", "first?pull=-1", "first?pull=1&pull=2",
		"first?pull=10000000000", "first?pull=%ZZ", "confirm?pull=7", "revert?pull=7", "unknown"} {
		t.Run(suffix, func(t *testing.T) {
			e := newEnv(t)
			wantStatus(t, e.signIn("alice").get("/r/"+repoName+"/owner/"+suffix), http.StatusNotFound)
		})
	}
}

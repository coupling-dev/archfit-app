package ui

import (
	"bytes"
	"context"
	"net/http"
	"testing"

	"github.com/alexei-led/archfit-app/internal/github/api"
)

func TestReportCacheBoundsItsBytes(t *testing.T) {
	key := func(id int64) artifactKey { return artifactKey{1, "acme/app", id} }
	c := newReportCache(10)
	c.put(key(1), []byte("aaaa"))
	c.put(key(2), []byte("bbbb"))
	c.get(key(1))                         // 1 is now the most recently used
	c.put(key(3), []byte("cccc"))         // 12 bytes: 2 goes
	c.put(key(4), []byte("dddddddddddd")) // alone over the bound: never kept

	for id, want := range map[int64]bool{1: true, 2: false, 3: true, 4: false} {
		if _, ok := c.get(key(id)); ok != want {
			t.Errorf("artifact %d cached = %v, want %v", id, ok, want)
		}
	}
	if c.size != 8 {
		t.Errorf("size = %d, want the 8 bytes of 1 and 3", c.size)
	}
}

// One repository never reads another's artifact from the cache, even under the same artifact ID.
func TestReportCacheIsPerRepository(t *testing.T) {
	gh := newFakeGH()
	gh.artifacts[5] = []byte("report of acme/app")
	s := &server{gh: github{actions: gh}, reports: newReportCache(reportCacheBytes)}
	a := api.RepoRef{InstallationID: 1, FullName: "acme/app"}
	b := api.RepoRef{InstallationID: 2, FullName: "other/app"}

	for _, r := range []api.RepoRef{a, a} {
		if _, err := s.reportPayload(context.Background(), r, 5); err != nil {
			t.Fatal(err)
		}
	}
	if n := len(gh.called("ArtifactByID")); n != 1 {
		t.Fatalf("downloads = %d, want the second read of acme/app from the cache", n)
	}

	gh.artifacts[5] = []byte("report of other/app")
	got, err := s.reportPayload(context.Background(), b, 5)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(got, []byte("report of other/app")) || len(gh.called("ArtifactByID")) != 2 {
		t.Errorf("other/app read %q after %d downloads, want its own artifact, downloaded", got, len(gh.called("ArtifactByID")))
	}
}

// The pull request page and its graph read one report: the second request lists the run's artifacts again but never
// downloads it twice.
func TestPullPageAndGraphDownloadTheReportOnce(t *testing.T) {
	e := newEnv(t)
	c := e.as("alice", permRead)
	e.checkPull(prArtifact(t, prPolicy))

	wantStatus(t, c.get("/api/repos/"+repoName+"/pulls/4"), http.StatusOK)
	wantStatus(t, c.get("/api/repos/"+repoName+"/pulls/4/graph"), http.StatusOK)
	if n := len(e.gh.called("ArtifactID")); n != 2 {
		t.Errorf("artifact listings = %d, want one per request", n)
	}
	if n := len(e.gh.called("ArtifactByID")); n != 1 {
		t.Errorf("downloads = %d, want one", n)
	}
}

// A hit never skips the per-request authorization: a user who loses access gets "Not found", cache or not.
func TestReportCacheHitStillAuthorizes(t *testing.T) {
	e := newEnv(t)
	c := e.as("alice", permRead)
	e.checkPull(prArtifact(t, prPolicy))
	wantStatus(t, c.get("/api/repos/"+repoName+"/pulls/4"), http.StatusOK)

	mallory := e.as("mallory", "")
	wantStatus(t, mallory.get("/api/repos/"+repoName+"/pulls/4"), http.StatusNotFound)
	if n := len(e.gh.called("ArtifactID")); n != 1 {
		t.Errorf("artifact listings = %d: a refused request reached the report", n)
	}
}

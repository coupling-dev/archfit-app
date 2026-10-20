package ui

import (
	"bytes"
	"context"
	"net/http"
	"strconv"
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

// One repository never reads another's artifact from the cache, even under the same artifact ID: each part of the
// key keeps them apart on its own.
func TestReportCacheIsPerRepository(t *testing.T) {
	cached := api.RepoRef{InstallationID: 1, FullName: "acme/app"}
	for _, other := range []api.RepoRef{
		{InstallationID: 1, FullName: "acme/other"}, // the same installation
		{InstallationID: 2, FullName: "acme/app"},   // the same name, as another installation sees it
	} {
		t.Run(other.FullName+"@"+strconv.FormatInt(other.InstallationID, 10), func(t *testing.T) {
			gh := newFakeGH()
			gh.artifacts[5] = []byte("report of the first")
			s := &server{gh: github{actions: gh}, reports: newReportCache(reportCacheBytes)}
			for range 2 {
				if _, err := s.reportByID(context.Background(), cached, 5); err != nil {
					t.Fatal(err)
				}
			}
			if n := len(gh.called("ArtifactByID")); n != 1 {
				t.Fatalf("downloads = %d, want the second read from the cache", n)
			}

			gh.artifacts[5] = []byte("report of the other")
			got, err := s.reportByID(context.Background(), other, 5)
			if err != nil {
				t.Fatal(err)
			}
			if !bytes.Equal(got, []byte("report of the other")) || len(gh.called("ArtifactByID")) != 2 {
				t.Errorf("read %q after %d downloads, want its own artifact, downloaded", got, len(gh.called("ArtifactByID")))
			}
		})
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

// The artifact listing is GitHub's answer to whether the report still exists, so it runs on every request: once the
// artifact is gone (expired, or deleted by its owner), the page says so though the cache still holds the report and
// the first reads left their hints.
func TestCachedReportOfAnUnlistedArtifactIsGone(t *testing.T) {
	e := newEnv(t)
	c := e.as("alice", permRead)
	e.checkPull(prArtifact(t, prPolicy))
	e.publishReport(repoName, artifact(t, "healthy"))
	var pull pullJSON
	var findings repoFindingsJSON
	c.getJSON("/api/repos/"+repoName+"/pulls/4", &pull)
	c.getJSON("/api/repos/"+repoName+"/findings", &findings)
	if pull.Unavailable != "" || findings.Unavailable != "" {
		t.Fatalf("first reads: %q, %q; want both reports", pull.Unavailable, findings.Unavailable)
	}

	e.gh.mu.Lock()
	clear(e.gh.artifacts)
	e.gh.mu.Unlock()
	pull, findings = pullJSON{}, repoFindingsJSON{}
	c.getJSON("/api/repos/"+repoName+"/pulls/4", &pull)
	c.getJSON("/api/repos/"+repoName+"/findings", &findings)
	if pull.Unavailable != reportExpired || pull.ViolationCount != 0 {
		t.Errorf("pull request: unavailable %q with %d violations, want %q", pull.Unavailable, pull.ViolationCount, reportExpired)
	}
	if findings.Unavailable != noReport || findings.ViolationCount != 0 {
		t.Errorf("findings: unavailable %q with %d violations, want %q", findings.Unavailable, findings.ViolationCount, noReport)
	}
}

package ui

import (
	"crypto/sha256"
	"encoding/hex"
	"net/http"
	"strconv"
	"strings"
	"testing"

	"github.com/alexei-led/archfit-app/internal/github/api"
	"github.com/alexei-led/archfit-app/internal/wire/policy"
	"github.com/alexei-led/archfit-app/internal/wire/settings"
)

// nameRun makes payload the report of run, which the archfit/state check on head names for pull request #4.
func (e *env) nameRun(head string, run int64, payload []byte) {
	sum := sha256.Sum256(payload)
	e.gh.mu.Lock()
	defer e.gh.mu.Unlock()
	e.gh.checks[repoName+"@"+head+"#"+checkState] = []api.CheckRun{{
		ID: 10, Name: checkState, HeadSHA: head, Status: "completed", Conclusion: "neutral",
		ExternalID: "v1:" + strconv.FormatInt(run, 10) + ":1:sha256:" + hex.EncodeToString(sum[:]),
	}}
	e.gh.artifacts[run] = payload
}

// Once a page was read, its next read starts every GitHub read at once: the hinted ones beside the one that names
// their parameter. A chain read in turn never meets.
func TestWarmReadsMeet(t *testing.T) {
	settingsAtMain := "File " + mainBranch + ":" + settings.Path
	pull := func(e *env) { e.checkPull(prArtifact(t, prPolicy)) }
	published := func(e *env) { e.publishReport(repoName, artifact(t, "healthy")) }
	tests := []struct {
		name  string
		path  string
		setup func(e *env)
		keys  []string
	}{
		{"pull request page", "/pulls/4", pull, []string{"PullSummary", "CheckRuns", "ArtifactID", settingsAtMain}},
		{"pull request graph", "/pulls/4/graph", pull,
			[]string{"PullSummary", "CheckRuns", "ArtifactID", settingsAtMain, "File " + sha(4) + ":" + policy.PolicyPath}},
		{"findings", "/findings", published, []string{settingsAtMain, "LatestRun", "ArtifactID"}},
		{"repository graph", "/graph", published, []string{settingsAtMain, "LatestRun", "ArtifactID"}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			e := newEnv(t)
			c := e.as("alice", permRead)
			tt.setup(e)
			wantStatus(t, c.get("/api/repos/"+repoName+tt.path), http.StatusOK)
			m := newMeeting(tt.keys...)
			e.gh.meet = m
			before := len(e.gh.calls)
			wantStatus(t, c.get("/api/repos/"+repoName+tt.path), http.StatusOK)
			if !m.met() {
				t.Errorf("%q were not in flight together", tt.keys)
			}
			// The reads made ahead are the answers: none is made again in turn.
			for _, key := range tt.keys {
				if n := countKey(e.gh.calls[before:], key); n != 1 {
					t.Errorf("%s read %d times, want once", key, n)
				}
			}
		})
	}
}

// countKey counts the calls a meeting key names: a method, or a method and its detail.
func countKey(calls []ghCall, key string) int {
	method, detail, withDetail := strings.Cut(key, " ")
	n := 0
	for _, c := range calls {
		if c.method == method && (!withDetail || c.detail == detail) {
			n++
		}
	}
	return n
}

// A chain the fresh reads say is gone (no settings, no run, no pull request) leaves no hint to read ahead with.
func TestGoneChainDropsItsHint(t *testing.T) {
	tests := []struct {
		name    string
		path    string
		setup   func(e *env)
		gone    func(e *env)
		status  int
		notRead string
	}{
		{"settings removed", "/findings", func(e *env) { e.publishReport(repoName, artifact(t, "healthy")) },
			func(e *env) { e.gh.deleteFile(repoName, mainBranch, settings.Path) }, http.StatusOK, "LatestRun"},
		{"no run left", "/findings", func(e *env) { e.publishReport(repoName, artifact(t, "healthy")) },
			func(e *env) { e.gh.mu.Lock(); clear(e.gh.runs); e.gh.mu.Unlock() }, http.StatusOK, "ArtifactID"},
		{"pull request gone", "/pulls/4", func(e *env) { e.checkPull(prArtifact(t, prPolicy)) },
			func(e *env) { e.gh.mu.Lock(); delete(e.gh.pulls, repoName+"#4"); e.gh.mu.Unlock() }, http.StatusNotFound, "CheckRuns"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			e := newEnv(t)
			c := e.as("alice", permRead)
			tt.setup(e)
			wantStatus(t, c.get("/api/repos/"+repoName+tt.path), http.StatusOK)
			tt.gone(e)
			wantStatus(t, c.get("/api/repos/"+repoName+tt.path), tt.status)

			before := len(e.gh.calls)
			wantStatus(t, c.get("/api/repos/"+repoName+tt.path), tt.status)
			if n := countKey(e.gh.calls[before:], tt.notRead); n != 0 {
				t.Errorf("%s read %d times after the chain was gone, want none", tt.notRead, n)
			}
		})
	}
}

// A hint the fresh reads no longer confirm does not count: the page reads again with what GitHub says now.
func TestStaleHintIsReadAgain(t *testing.T) {
	t.Run("pull request", func(t *testing.T) {
		required := strings.Replace(settingsText, "gate: advisory", "gate: required", 1)
		tests := []struct {
			name         string
			change       func(e *env)
			wantRun      int64
			wantRequired bool
		}{
			{"the head moved", func(e *env) {
				e.gh.pulls[repoName+"#4"] = withHead(e.gh.pulls[repoName+"#4"], sha(5))
				e.nameRun(sha(5), 89, prArtifact(t, policyText))
			}, 89, false},
			{"the check names another run", func(e *env) { e.nameRun(sha(4), 89, prArtifact(t, policyText)) }, 89, false},
			// A read made ahead with a stale parameter fails: its error is not the page's.
			{"the head moved, its old check fails", func(e *env) {
				e.gh.pulls[repoName+"#4"] = withHead(e.gh.pulls[repoName+"#4"], sha(5))
				e.nameRun(sha(5), 89, prArtifact(t, policyText))
				e.gh.fail["CheckRuns "+sha(4)+"#"+checkState] = api.ErrRateLimited
			}, 89, false},
			{"the check names another run, the old run's listing fails", func(e *env) {
				e.nameRun(sha(4), 89, prArtifact(t, policyText))
				e.gh.fail["ArtifactID 88 "+reportArtifact] = api.ErrRateLimited
			}, 89, false},
			{"the base moved", func(e *env) {
				p := e.gh.pulls[repoName+"#4"]
				p.BaseRef = "develop"
				e.gh.pulls[repoName+"#4"] = p
				e.gh.setFile(repoName, "develop", settings.Path, required)
			}, 88, true},
			{"the base moved, the old base's settings fail", func(e *env) {
				p := e.gh.pulls[repoName+"#4"]
				p.BaseRef = "develop"
				e.gh.pulls[repoName+"#4"] = p
				e.gh.setFile(repoName, "develop", settings.Path, required)
				e.gh.fail["File "+mainBranch+":"+settings.Path] = api.ErrRateLimited
			}, 88, true},
		}
		for _, tt := range tests {
			t.Run(tt.name, func(t *testing.T) {
				e := newEnv(t)
				c := e.as("alice", permRead)
				e.checkPull(prArtifact(t, prPolicy))
				wantStatus(t, c.get("/api/repos/"+repoName+"/pulls/4"), http.StatusOK)

				tt.change(e)
				var got pullJSON
				c.getJSON("/api/repos/"+repoName+"/pulls/4", &got)
				if got.Unavailable != "" || got.RunID != tt.wantRun || got.Required != tt.wantRequired {
					t.Errorf("unavailable %q, run %d, required %v; want the report of run %d, required %v",
						got.Unavailable, got.RunID, got.Required, tt.wantRun, tt.wantRequired)
				}
			})
		}
	})

	t.Run("default branch", func(t *testing.T) {
		other := ".github/workflows/other.yaml"
		newer := api.WorkflowRun{ID: 78, Event: "push", Status: "completed", Conclusion: "success"}
		tests := []struct {
			name   string
			change func(e *env)
		}{
			{"a newer run", func(e *env) { e.gh.addRun(repoName, workflow, mainBranch, newer) }},
			{"the settings name another workflow", func(e *env) {
				e.gh.setFile(repoName, mainBranch, settings.Path, strings.Replace(settingsText, workflow, other, 1))
				e.gh.addRun(repoName, other, mainBranch, newer)
			}},
			{"a newer run, the old run's listing fails", func(e *env) {
				e.gh.addRun(repoName, workflow, mainBranch, newer)
				e.gh.fail["ArtifactID 77 "+reportArtifact] = api.ErrRateLimited
			}},
			{"another workflow, the old one's latest run fails", func(e *env) {
				e.gh.setFile(repoName, mainBranch, settings.Path, strings.Replace(settingsText, workflow, other, 1))
				e.gh.addRun(repoName, other, mainBranch, newer)
				e.gh.fail["LatestRun "+workflow+"@"+mainBranch+" event=push status=success"] = api.ErrRateLimited
			}},
		}
		for _, tt := range tests {
			t.Run(tt.name, func(t *testing.T) {
				e := newEnv(t)
				c := e.as("alice", permRead)
				e.publishReport(repoName, artifact(t, "healthy"))
				var got repoFindingsJSON
				c.getJSON("/api/repos/"+repoName+"/findings", &got)
				if got.ViolationCount != 0 {
					t.Fatalf("violations = %d, want none in the healthy fixture", got.ViolationCount)
				}

				e.gh.mu.Lock()
				e.gh.artifacts[78] = artifact(t, "real-blocked-no-baseline")
				e.gh.mu.Unlock()
				tt.change(e)
				got = repoFindingsJSON{}
				c.getJSON("/api/repos/"+repoName+"/findings", &got)
				if got.Unavailable != "" || got.ViolationCount == 0 {
					t.Errorf("unavailable %q, %d violations; want the report of run 78", got.Unavailable, got.ViolationCount)
				}
			})
		}
	})
}

// The pull request graph reads the policy texts again at the fresh head: the hinted head's text is not this report's.
func TestStaleHintReadsThePolicyAgain(t *testing.T) {
	e := newEnv(t)
	c := e.as("alice", permRead)
	e.checkPull(prArtifact(t, prPolicy))
	e.gh.setFile(repoName, sha(4), policy.PolicyPath, prPolicy)
	wantStatus(t, c.get("/api/repos/"+repoName+"/pulls/4/graph"), http.StatusOK)

	e.gh.pulls[repoName+"#4"] = withHead(e.gh.pulls[repoName+"#4"], sha(5))
	e.nameRun(sha(5), 89, prArtifact(t, policyText))
	e.gh.setFile(repoName, sha(5), policy.PolicyPath, policyText)
	var got graphJSON
	c.getJSON("/api/repos/"+repoName+"/pulls/4/graph", &got)
	if !strings.Contains(got.PolicyHref, "/blob/"+sha(5)+"/") || got.StaleReason != "" {
		t.Errorf("policy %q, stale %q; want the text at the new head, current", got.PolicyHref, got.StaleReason)
	}
}

// Warm, every read is made ahead, and each fails the page as it did when read in turn.
func TestWarmLatestReportFailsAsInTurn(t *testing.T) {
	for _, fail := range []string{"File " + mainBranch + ":" + settings.Path, "LatestRun", "ArtifactID"} {
		for _, path := range []string{"/findings", "/graph"} {
			t.Run(fail+" "+path, func(t *testing.T) {
				e := newEnv(t)
				c := e.as("alice", permRead)
				e.publishReport(repoName, artifact(t, "healthy"))
				wantStatus(t, c.get("/api/repos/"+repoName+path), http.StatusOK)
				e.gh.mu.Lock()
				e.gh.fail[fail] = api.ErrRateLimited
				e.gh.mu.Unlock()
				wantStatus(t, c.get("/api/repos/"+repoName+path), http.StatusServiceUnavailable)
			})
		}
	}
}

func withHead(p api.Pull, head string) api.Pull {
	p.HeadSHA = head
	return p
}

// One repository's hints never become another's reads, though both have a pull request #4 and a default branch.
func TestHintsStayWithTheirRepository(t *testing.T) {
	e := newEnv(t)
	c := e.as("alice", permRead)
	e.grant("alice", otherRepo, permRead)
	e.checkPull(prArtifact(t, prPolicy))
	e.publishReport(repoName, artifact(t, "healthy"))
	e.gh.pulls[otherRepo+"#4"] = withHead(e.gh.pulls[otherRepo+"#4"], sha(6))
	for _, path := range []string{"/pulls/4", "/findings"} {
		wantStatus(t, c.get("/api/repos/"+repoName+path), http.StatusOK)
	}

	for _, path := range []string{"/pulls/4", "/findings"} {
		wantStatus(t, c.get("/api/repos/"+otherRepo+path), http.StatusOK)
	}
	for _, call := range append(e.gh.called("CheckRuns"), e.gh.called("ArtifactID")...) {
		if call.fullName == otherRepo && (call.method == "ArtifactID" || !strings.HasPrefix(call.detail, sha(6))) {
			t.Errorf("%s of %s with %q: read with another repository's hint", call.method, otherRepo, call.detail)
		}
	}
	if n := len(e.gh.called("LatestRun")); n != 2 {
		t.Errorf("latest-run reads = %d, want one per repository", n)
	}
}

func TestHintKeyParts(t *testing.T) {
	h := newHints()
	kept := hintKey{installA, repoName, 4}
	h.put(kept, hint{head: sha(4), run: 88})
	for _, other := range []hintKey{
		{installB, repoName, 4},  // the same name, as another installation sees it
		{installA, otherRepo, 4}, // another repository of the installation
		{installA, repoName, 5},  // another pull request
		{installA, repoName, 0},  // the default branch
	} {
		if _, ok := h.get(other); ok {
			t.Errorf("%+v reads the hint of %+v", other, kept)
		}
	}
	if got, ok := h.get(kept); !ok || got.head != sha(4) || got.run != 88 {
		t.Errorf("get = %+v, %v; want the hint kept", got, ok)
	}
}

func TestHintsBoundTheirCount(t *testing.T) {
	h := newHints()
	key := func(n int) hintKey { return hintKey{installA, repoName, n} }
	for n := range hintsMax {
		h.put(key(n), hint{run: int64(n)})
	}
	h.get(key(0))                // 0 is now the most recently used
	h.put(key(hintsMax), hint{}) // one over the bound: 1 goes
	h.put(key(0), hint{run: 7})  // a new hint replaces the old, the count stays
	if _, ok := h.get(key(1)); ok {
		t.Error("the least recently used hint was kept")
	}
	if got, ok := h.get(key(0)); !ok || got.run != 7 {
		t.Errorf("hint 0 = %+v, %v; want the newest", got, ok)
	}
	if n := h.order.Len(); n != hintsMax {
		t.Errorf("hints = %d, want %d", n, hintsMax)
	}
}

func TestGuessCountsOnlyForItsParameter(t *testing.T) {
	reads := 0
	read := func(p string) (string, error) { reads++; return "fresh " + p, nil }
	var g guess[string, string]
	if got, _ := g.at("a", read); got != "fresh a" || reads != 1 {
		t.Errorf("no guess: %q after %d reads, want one read now", got, reads)
	}
	g.ahead("a", read)()
	if got, _ := g.at("a", read); got != "fresh a" || reads != 2 {
		t.Errorf("guessed a, asked a: %q after %d reads, want the guess", got, reads)
	}
	if got, _ := g.at("b", read); got != "fresh b" || reads != 3 {
		t.Errorf("guessed a, asked b: %q after %d reads, want a read of b", got, reads)
	}
}

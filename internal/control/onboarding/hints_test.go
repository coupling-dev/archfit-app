package onboarding

import (
	"context"
	"errors"
	"slices"
	"strings"
	"testing"

	"github.com/alexei-led/archfit-app/internal/github/api"
	"github.com/alexei-led/archfit-app/internal/wire/policy"
	"github.com/alexei-led/archfit-app/internal/wire/settings"
)

const otherWorkflow = ".github/workflows/other.yaml"

// advisory is a repository past onboarding: settings, workflow, policy and an accepted report on the default branch.
func (f *fixture) advisory(r api.Repo) {
	f.onboard(r, settings.GateAdvisory)
	f.hub.put(r, r.DefaultBranch, policy.PolicyPath, []byte(validPolicy))
	f.report(r)
}

func (f *fixture) progress(t *testing.T, r api.Repo) Stage {
	t.Helper()
	p, err := f.svc.Progress(context.Background(), r)
	if err != nil {
		t.Fatalf("Progress: %v", err)
	}
	return p.Stage
}

// Once a repository's progress was read, the next read starts every GitHub read at once: the hinted ones beside the
// settings that name their parameter. Reads made in turn never meet.
func TestWarmProgressReadsMeet(t *testing.T) {
	f := newFixture(t)
	f.advisory(f.repo)
	f.progress(t, f.repo)

	m := newMeeting("File "+settings.Path, "File "+workflowPath, "File "+policy.PolicyPath, "LatestRun", "CheckRuns")
	f.hub.meet = m
	before := len(f.hub.readsOf(f.repo.FullName))
	if got := f.progress(t, f.repo); got != StageAdvisory {
		t.Errorf("stage = %q, want %q", got, StageAdvisory)
	}
	if !m.met() {
		t.Error("the warm reads were not in flight together")
	}
	// The reads made ahead are the answers: none is made again in turn.
	want := []string{"CheckRuns " + runHeadSHA + "#" + stateCheck, "File " + policy.PolicyPath, "File " + settings.Path,
		"File " + workflowPath, "LatestRun " + workflowPath + "@" + f.repo.DefaultBranch}
	slices.Sort(want)
	if got := slices.Sorted(slices.Values(f.hub.readsOf(f.repo.FullName)[before:])); !slices.Equal(got, want) {
		t.Errorf("warm reads = %q, want each once: %q", got, want)
	}
}

// Without settings there is nothing to read ahead: the hint goes, and the next derive reads only the settings' way.
func TestGoneSettingsDropTheProgressHint(t *testing.T) {
	f := newFixture(t)
	f.advisory(f.repo)
	f.progress(t, f.repo)
	f.hub.remove(f.repo, f.repo.DefaultBranch, settings.Path)
	f.progress(t, f.repo)

	before := len(f.hub.readsOf(f.repo.FullName))
	if got := f.progress(t, f.repo); got != StageInstalled {
		t.Errorf("stage = %q, want %q", got, StageInstalled)
	}
	for _, read := range f.hub.readsOf(f.repo.FullName)[before:] {
		if strings.HasPrefix(read, "LatestRun") || strings.HasPrefix(read, "CheckRuns") || read == "File "+workflowPath {
			t.Errorf("%s: read ahead with the hint of settings that are gone", read)
		}
	}
}

// A hint the fresh reads no longer confirm does not count: the progress reads again with what GitHub says now, and a read
// made ahead with the stale parameter never fails it.
func TestStaleProgressHintIsReadAgain(t *testing.T) {
	newer := api.WorkflowRun{ID: 10, Event: "push", Status: "completed", HeadSHA: strings.Repeat("2", 40), WorkflowPath: workflowPath}
	otherRun := api.WorkflowRun{ID: 11, Event: "push", Status: "completed", HeadSHA: strings.Repeat("3", 40), WorkflowPath: otherWorkflow}
	moveWorkflow := func(f *fixture) {
		f.hub.put(f.repo, f.repo.DefaultBranch, settings.Path, renderSettings(settings.GateAdvisory, otherWorkflow))
	}
	failOld := func(f *fixture, op, detail string) {
		f.hub.failWhen(func(o, d string) error {
			if o == op && d == detail {
				return api.ErrRateLimited
			}
			return nil
		})
	}
	tests := []struct {
		name   string
		change func(f *fixture)
		want   Stage
	}{
		{"a newer run, its check not published yet", func(f *fixture) { f.hub.addRun(f.repo, newer) }, StagePolicyMerged},
		{"a newer run, the old head's check fails", func(f *fixture) {
			f.hub.addRun(f.repo, newer)
			failOld(f, "CheckRuns", runHeadSHA+"#"+stateCheck)
		}, StagePolicyMerged},
		{"the settings name another workflow, not run yet", func(f *fixture) {
			moveWorkflow(f)
			f.hub.put(f.repo, f.repo.DefaultBranch, otherWorkflow, []byte("name: archfit\n"))
		}, StagePolicyMerged},
		{"the settings name another workflow, its file absent", moveWorkflow, StageInstalled},
		{"another workflow, the old one's latest run fails", func(f *fixture) {
			moveWorkflow(f)
			f.hub.put(f.repo, f.repo.DefaultBranch, otherWorkflow, []byte("name: archfit\n"))
			f.hub.addRun(f.repo, otherRun)
			failOld(f, "LatestRun", workflowPath+"@"+f.repo.DefaultBranch)
		}, StagePolicyMerged},
		{"another workflow, the old one's file fails", func(f *fixture) {
			moveWorkflow(f)
			f.hub.put(f.repo, f.repo.DefaultBranch, otherWorkflow, []byte("name: archfit\n"))
			failOld(f, "File", workflowPath)
		}, StagePolicyMerged},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			f := newFixture(t)
			f.advisory(f.repo)
			if got := f.progress(t, f.repo); got != StageAdvisory {
				t.Fatalf("first stage = %q, want %q", got, StageAdvisory)
			}
			tc.change(f)
			if got := f.progress(t, f.repo); got != tc.want {
				t.Errorf("stage = %q, want %q", got, tc.want)
			}
		})
	}
}

// One repository's hint never becomes another's reads.
func TestProgressHintsStayWithTheirRepository(t *testing.T) {
	f := newFixture(t)
	f.advisory(f.repo)
	f.progress(t, f.repo)
	f.progress(t, f.repo)

	other := testRepo("acme/other")
	f.hub.put(other, other.DefaultBranch, settings.Path, renderSettings(settings.GateAdvisory, otherWorkflow))
	f.hub.put(other, other.DefaultBranch, otherWorkflow, []byte("name: archfit\n"))
	if got := f.progress(t, other); got != StageWorkflowMerged {
		t.Fatalf("stage = %q, want %q", got, StageWorkflowMerged)
	}
	for _, read := range f.hub.readsOf(other.FullName) {
		if strings.Contains(read, workflowPath) || strings.Contains(read, runHeadSHA) {
			t.Errorf("%s of %s: read with another repository's hint", read, other.FullName)
		}
	}
}

// Warm, the reads are made ahead, and each failure fails the progress as it did when the reads ran in turn.
func TestWarmProgressFailsAsInTurn(t *testing.T) {
	errWorkflow, errPolicy, errRun := errors.New("workflow unreadable"), errors.New("policy unreadable"), errors.New("runs unreadable")
	tests := []struct {
		name      string
		fail      func(op, detail string) error
		wantErr   error
		wantStage Stage
	}{
		{"settings", func(op, d string) error { return when(op == "File" && d == settings.Path, api.ErrRateLimited) }, api.ErrRateLimited, ""},
		{"the latest run", func(op, _ string) error { return when(op == "LatestRun", errRun) }, errRun, ""},
		{"the state check", func(op, _ string) error { return when(op == "CheckRuns", errRun) }, errRun, ""},
		{"the latest run not found", func(op, _ string) error { return when(op == "LatestRun", api.ErrNotFound) }, nil, StagePolicyMerged},
		{"without a workflow the other failures do not count", func(op, d string) error {
			switch {
			case op == "File" && d == workflowPath:
				return api.ErrNotFound
			case op == "File" && d == policy.PolicyPath:
				return errPolicy
			}
			return when(op == "LatestRun", errRun)
		}, nil, StageInstalled},
		{"the workflow's failure comes first", func(op, d string) error {
			switch {
			case op == "File" && d == workflowPath:
				return errWorkflow
			case op == "File" && d == policy.PolicyPath:
				return errPolicy
			}
			return when(op == "LatestRun", errRun)
		}, errWorkflow, ""},
		{"then the policy's", func(op, d string) error {
			if op == "File" && d == policy.PolicyPath {
				return errPolicy
			}
			return when(op == "LatestRun", errRun)
		}, errPolicy, ""},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			f := newFixture(t)
			f.advisory(f.repo)
			f.progress(t, f.repo)
			f.hub.failWhen(tc.fail)
			p, err := f.svc.Progress(context.Background(), f.repo)
			if !errors.Is(err, tc.wantErr) || p.Stage != tc.wantStage {
				t.Errorf("got %q, %v; want %q, %v", p.Stage, err, tc.wantStage, tc.wantErr)
			}
		})
	}
}

func when(cond bool, err error) error {
	if cond {
		return err
	}
	return nil
}

func TestProgressHintKeyParts(t *testing.T) {
	h := newHints()
	kept := hintKey{7, "acme/shop"}
	h.put(kept, hint{workflow: workflowPath, head: runHeadSHA})
	for _, other := range []hintKey{{8, "acme/shop"}, {7, "acme/other"}} {
		if _, ok := h.get(other); ok {
			t.Errorf("%+v reads the hint of %+v", other, kept)
		}
	}
	if got, ok := h.get(kept); !ok || got.head != runHeadSHA {
		t.Errorf("get = %+v, %v; want the hint kept", got, ok)
	}
}

func TestProgressHintsBoundTheirCount(t *testing.T) {
	h := newHints()
	key := func(n int) hintKey { return hintKey{int64(n), "acme/shop"} }
	for n := range hintsMax {
		h.put(key(n), hint{})
	}
	h.get(key(0))
	h.put(key(hintsMax), hint{})
	h.put(key(0), hint{head: "x"})
	if _, ok := h.get(key(1)); ok {
		t.Error("the least recently used hint was kept")
	}
	if got, ok := h.get(key(0)); !ok || got.head != "x" || h.order.Len() != hintsMax {
		t.Errorf("hint 0 = %+v, %v with %d kept; want the newest of %d", got, ok, h.order.Len(), hintsMax)
	}
}

func TestProgressGuessCountsOnlyForItsParameter(t *testing.T) {
	var asked []string
	read := func(p string) (string, error) { asked = append(asked, p); return p, nil }
	var g guess[string, string]
	g.ahead("a", read)()
	_, _ = g.at("a", read)
	_, _ = g.at("b", read)
	if !slices.Equal(asked, []string{"a", "b"}) {
		t.Errorf("reads = %q, want the guess for a, a read for b", asked)
	}
}

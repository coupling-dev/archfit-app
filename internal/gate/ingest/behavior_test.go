package ingest

import (
	"fmt"
	"reflect"
	"strings"
	"testing"

	"github.com/alexei-led/archfit-app/internal/github/api"
	"github.com/alexei-led/archfit-app/internal/wire/envelope"
	"github.com/alexei-led/archfit-app/internal/wire/manifest"
)

func movePullHead(s *scenario) {
	p := s.pulls[prNumber]
	p.HeadSHA = movedSHA
	s.pulls[prNumber] = p
}

func TestRevisionBindingCases(t *testing.T) {
	rows := []struct {
		name     string
		base     func(*testing.T) *scenario
		mutate   func(*scenario)
		status   int // 0 = bound
		code     string
		problems []Problem
	}{
		{name: "PR", base: prScenario},
		{name: "default-branch push", base: pushScenario},
		{name: "workflow_run", base: prScenario,
			mutate:   func(s *scenario) { s.toEvent("workflow_run", "refs/heads/"+defaultBranch) },
			problems: []Problem{ProblemUnsupportedEvent}},
		{name: "multi-PR ambiguous association", base: prScenario,
			mutate: func(s *scenario) {
				s.run.PullRequests = []int{prNumber, 8}
				s.pulls[8] = openPull(8)
			},
			problems: []Problem{ProblemEnvelopeMismatch}},
		{name: "re-push before upload", base: prScenario, mutate: movePullHead, status: 409, code: "stale_head"},
		{name: "stale result of a fork run", base: prScenario,
			mutate: func(s *scenario) {
				s.run.PullRequests = nil
				movePullHead(s)
			}, status: 409, code: "stale_head"},
		{name: "wrong tenant", base: prScenario,
			mutate: func(s *scenario) { s.env.Repository = otherTenant }, status: 403, code: "repository_mismatch"},
		{name: "re-created repository", base: prScenario,
			mutate: func(s *scenario) { s.claims.RepositoryID = repoID + 1 }, status: 403, code: "repository_mismatch"},
		{name: "mismatched report digest", base: prScenario,
			mutate: func(s *scenario) { s.env.PayloadDigest = envelope.PayloadDigest([]byte("other")) },
			status: 400, code: "digest_mismatch"},
		{name: "older attempt after a re-run", base: prScenario,
			mutate: func(s *scenario) { s.run.Attempt = 2 }, status: 409, code: "stale_attempt"},
		{name: "cancelled run that did upload", base: prScenario,
			mutate: func(s *scenario) { s.run.Status, s.run.Conclusion = "completed", "cancelled" }},
		{name: "missing run", base: prScenario,
			mutate: func(s *scenario) { s.runErr = fmt.Errorf("fake: %w", api.ErrNotFound) },
			status: 403, code: "run_not_found"},
	}
	for _, row := range rows {
		t.Run(row.name, func(t *testing.T) {
			s := row.base(t)
			if row.mutate != nil {
				row.mutate(s)
			}
			up, err := s.receive(t)
			if row.status != 0 {
				wantRejection(t, up, err, row.status, row.code)
				return
			}
			wantProblems(t, up, err, row.problems...)
			if up.Binding.Repo != s.repo || up.Binding.HeadSHA != headSHA || up.Binding.RunID != runID {
				t.Errorf("binding = %+v, want the run of %s", up.Binding, repoName)
			}
		})
	}

	t.Run("duplicate attempt", func(t *testing.T) {
		s := prScenario(t)
		first, err := s.receive(t)
		wantProblems(t, first, err)
		second, err := s.receive(t)
		wantProblems(t, second, err)
		if !reflect.DeepEqual(first, second) {
			t.Error("the same upload gave two different results")
		}
	})
}

func TestEnvelopeAloneEstablishesNothing(t *testing.T) {
	const tokenRun, tokenPR = int64(2002), 9
	tokenHead := strings.Repeat("e", 40)

	// The envelope and report describe run 1001 of PR 7; the token belongs to run 2002 of PR 9.
	s := prScenario(t)
	s.claims.RunID = tokenRun
	s.run.ID = tokenRun
	s.run.HeadSHA = tokenHead
	s.run.PullRequests = []int{tokenPR}
	other := openPull(tokenPR)
	other.HeadSHA = tokenHead
	other.BaseRef = defaultBranch
	other.BaseSHA = strings.Repeat("1", 40)
	other.MergeBaseSHA = strings.Repeat("2", 40)
	s.pulls[tokenPR] = other

	up, err := s.receive(t)
	wantProblems(t, up, err, ProblemEnvelopeMismatch)
	want := healthyBinding(s)
	want.RunID, want.PullRequest, want.HeadSHA = tokenRun, tokenPR, tokenHead
	want.BaseRef, want.BaseSHA, want.MergeBaseSHA = other.BaseRef, other.BaseSHA, other.MergeBaseSHA
	if !reflect.DeepEqual(up.Binding, want) {
		t.Errorf("binding = %+v, want the token's run %+v", up.Binding, want)
	}
}

func TestModifiedWorkflowAndWrongImageAreBoundNotJudged(t *testing.T) {
	t.Run("another workflow file", func(t *testing.T) {
		s := prScenario(t)
		s.claims.JobWorkflowRef = repoName + "/.github/workflows/custom.yaml@refs/pull/7/merge"
		up, err := s.receive(t)
		wantProblems(t, up, err)
		if up.Binding.WorkflowPath != ".github/workflows/custom.yaml" {
			t.Errorf("WorkflowPath = %q, want the file that ran", up.Binding.WorkflowPath)
		}
	})

	t.Run("unknown image digest", func(t *testing.T) {
		s := prScenario(t)
		s.env.ImageDigest = "sha256:" + strings.Repeat("0", 64)
		up, err := s.receive(t)
		wantProblems(t, up, err)
		if up.Identity.ImageDigest != s.env.ImageDigest {
			t.Errorf("identity = %+v, want the claimed image digest", up.Identity)
		}
		if _, approved := manifest.Lookup(up.Identity); approved {
			t.Fatal("the test image digest is in the manifest")
		}
	})
}

func TestApprovedProducerIsAccepted(t *testing.T) {
	approved := manifest.Current().Identity
	s := prScenario(t)
	s.env.EngineVersion, s.env.ImageDigest, s.env.Platform = approved.EngineVersion, approved.ImageDigest, approved.Platform
	up, err := s.receive(t)
	wantProblems(t, up, err)
	if up.Identity != approved {
		t.Errorf("identity = %+v, want %+v", up.Identity, approved)
	}
	if _, ok := manifest.Lookup(up.Identity); !ok {
		t.Error("the approved identity does not pass the manifest lookup")
	}
	if up.Report == nil || up.Binding != healthyBinding(s) {
		t.Errorf("upload = %+v, want a bound report", up.Binding)
	}
}

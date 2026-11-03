package ingest

import (
	"errors"
	"net/http"
	"reflect"
	"strings"
	"testing"

	"github.com/alexei-led/archfit-app/internal/github/api"
	"github.com/alexei-led/archfit-app/internal/wire/envelope"
)

func TestMergeGroupUploadBindsTheQueuedGroupCommit(t *testing.T) {
	s := mergeGroupScenario(t)
	up, err := s.receive(t)
	wantProblems(t, up, err)
	want := Binding{
		Repo: s.repo, Event: eventMergeGroup, HeadSHA: groupSHA, AnalyzedSHA: groupSHA, BaseRef: defaultBranch,
		RunID: runID, RunAttempt: 1, WorkflowPath: workflowPath, MergeGroup: true,
	}
	if !reflect.DeepEqual(up.Binding, want) {
		t.Errorf("binding = %+v\nwant      %+v", up.Binding, want)
	}
	// The queue is read for the default branch, never for the branch the run's ref names.
	if !reflect.DeepEqual(s.queueAsked, []string{defaultBranch}) {
		t.Errorf("queue read for %v, want [%s]", s.queueAsked, defaultBranch)
	}
	wantCalls(t, s, "verify", "resolve", "run", "queue")
}

func TestMergeGroupUploadRefusals(t *testing.T) {
	tests := []struct {
		name    string
		mutate  func(*scenario)
		status  int
		code    string
		problem Problem // "" for a Rejection
	}{
		{"group no longer queued", func(s *scenario) { s.queue = s.queue[:1] }, http.StatusConflict, codeStaleGroup, ""},
		{"empty queue", func(s *scenario) { s.queue = nil }, http.StatusConflict, codeStaleGroup, ""},
		{"head is a member's pull head, not a group commit", func(s *scenario) {
			s.queue[1].HeadSHA, s.queue[1].PullHeadSHA = strings.Repeat("2", 40), groupSHA
		}, http.StatusConflict, codeStaleGroup, ""},
		{"queue rate limited", func(s *scenario) { s.queueErr = api.ErrRateLimited }, http.StatusTooManyRequests, codeRateLimited, ""},
		{"run head differs from the token's commit", func(s *scenario) { s.claims.SHA = movedSHA }, http.StatusForbidden, codeRunMismatch, ""},
		{"older attempt", func(s *scenario) { s.claims.RunAttempt, s.env.RunAttempt = 1, 1; s.run.Attempt = 2 }, http.StatusConflict, codeStaleAttempt, ""},
		{"envelope claims a base", func(s *scenario) { s.env.BaseSHA = baseSHA }, http.StatusBadRequest, codeEnvelopeInvalid, ""},
		{"analyzed commit is not the group commit", func(s *scenario) { s.env.AnalyzedSHA = headSHA }, 0, "", ProblemEnvelopeMismatch},
		{"envelope names a pull request", func(s *scenario) { s.env.PullRequest = prNumber }, http.StatusBadRequest, codeEnvelopeInvalid, ""},
		{"run in a fork", func(s *scenario) { s.run.HeadRepo = otherTenant + "/shop" }, 0, "", ProblemFork},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			s := mergeGroupScenario(t)
			tt.mutate(s)
			up, err := s.receive(t)
			if tt.code != "" {
				wantRejection(t, up, err, tt.status, tt.code)
				return
			}
			wantProblems(t, up, err, tt.problem)
		})
	}
}

func TestMergeGroupQueueFailureIsNeverABinding(t *testing.T) {
	s := mergeGroupScenario(t)
	s.queueErr = errors.New("graphql: FORBIDDEN")
	up, err := s.receive(t)
	var rej *Rejection
	if err == nil || errors.As(err, &rej) {
		t.Fatalf("got %+v, %v; want a plain error (503)", up.Binding, err)
	}
}

func TestOnlyAMergeGroupReportReadsTheQueue(t *testing.T) {
	for _, s := range []*scenario{prScenario(t), pushScenario(t)} {
		if _, err := s.receive(t); err != nil {
			t.Fatal(err)
		}
		if len(s.queueAsked) != 0 {
			t.Errorf("%s run read the merge queue", s.run.Event)
		}
	}
	s := mergeGroupScenario(t)
	s.toDiscovery()
	s.kind, s.env.Kind = envelope.KindDiscovery, envelope.KindDiscovery
	up, err := s.receive(t)
	wantProblems(t, up, err, ProblemUnsupportedEvent)
	if len(s.queueAsked) != 0 {
		t.Error("a merge_group discovery read the merge queue")
	}
}

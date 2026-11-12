package ingest

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"os"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/alexei-led/archfit-app/internal/github/api"
	"github.com/alexei-led/archfit-app/internal/wire/envelope"
	"github.com/alexei-led/archfit-app/internal/wire/result"
)

var (
	targetSHA    = strings.Repeat("7", 40)
	policyDigest = "sha256:" + strings.Repeat("5", 63) + "a"
	runCreated   = time.Date(2026, 11, 12, 10, 0, 0, 0, time.UTC)
)

// v2Scenario is a scenario whose upload is envelope 2 with a result JSON v1 payload.
type v2Scenario struct {
	*scenario
	env2    envelope.EnvelopeV2
	result  map[string]any // the payload, as JSON
	runs    []api.WorkflowRun
	runsErr error
}

// passResult is the vendored engine golden of a pass, read as JSON so a test can change any key.
func passResult(t *testing.T) map[string]any {
	t.Helper()
	raw, err := os.ReadFile("../../wire/result/testdata/result/pass.json")
	if err != nil {
		t.Fatal(err)
	}
	var m map[string]any
	if err := json.Unmarshal(raw, &m); err != nil {
		t.Fatal(err)
	}
	m["head"] = headSHA
	m["engine"] = "4.0.0"
	m["policy"].(map[string]any)["digest"] = policyDigest
	return m
}

// prScenarioV2 is the healthy envelope 2 upload of a pull request into the default branch.
func prScenarioV2(t *testing.T) *v2Scenario {
	t.Helper()
	s := &v2Scenario{scenario: prScenario(t), result: passResult(t)}
	s.run.CreatedAt = runCreated
	s.pulls[prNumber] = func(p api.Pull) api.Pull { p.BaseRef = defaultBranch; return p }(s.pulls[prNumber])
	s.env2 = envelope.EnvelopeV2{
		SchemaVersion: envelope.SchemaV2,
		Repository:    repoName,
		Event:         eventPullRequest,
		PullRequest:   prNumber,
		HeadSHA:       headSHA,
		MergeBaseSHA:  mergeBaseSHA,
		TargetRef:     "refs/heads/" + defaultBranch,
		TargetSHA:     targetSHA,
		PolicyDigest:  policyDigest,
		RunID:         runID,
		RunAttempt:    1,
		EngineVersion: "v4.0.0",
		ImageDigest:   imageDigest,
		Platform:      "linux/amd64",
	}
	s.runs = []api.WorkflowRun{s.run}
	return s
}

func pushScenarioV2(t *testing.T) *v2Scenario {
	t.Helper()
	s := prScenarioV2(t)
	s.toEvent(eventPush, "refs/heads/"+defaultBranch)
	s.env2.Event, s.env2.PullRequest = eventPush, 0
	s.runs = []api.WorkflowRun{s.run}
	return s
}

func mergeGroupScenarioV2(t *testing.T) *v2Scenario {
	t.Helper()
	s := prScenarioV2(t)
	group := mergeGroupScenario(t)
	s.run.HeadSHA, s.queue = groupSHA, group.queue
	s.toEvent(eventMergeGroup, group.claims.Ref)
	s.env2.Event, s.env2.PullRequest, s.env2.HeadSHA = eventMergeGroup, 0, groupSHA
	s.result["head"] = groupSHA
	s.runs = []api.WorkflowRun{s.run}
	return s
}

func (s *v2Scenario) receiveV2(t *testing.T) (Upload4, error) {
	t.Helper()
	body, err := json.Marshal(s.result)
	if err != nil {
		t.Fatal(err)
	}
	s.body = body
	if s.env2.PayloadDigest == "" {
		s.env2.PayloadDigest = envelope.PayloadDigest(body)
	}
	if s.rawEnvelope == "" {
		raw, err := json.Marshal(s.env2)
		if err != nil {
			t.Fatal(err)
		}
		s.rawEnvelope = string(raw)
	}
	s.calls, s.resolved, s.refs = nil, nil, nil
	r := NewV2(fakeRepos{s: s.scenario}, fakePulls{s: s.scenario}, fakeActions{s: s.scenario}, fakeRuns{s: s},
		fakeQueues{s: s.scenario}, fakeVerifier{s: s.scenario}, testAudience, fakeAdmitter{s: s.scenario})
	return r.ReceiveV2(context.Background(), s.request(t))
}

type fakeRuns struct{ s *v2Scenario }

func (f fakeRuns) HeadRuns(_ context.Context, r api.RepoRef, workflow, head string) ([]api.WorkflowRun, error) {
	f.s.calls = append(f.s.calls, "runs")
	f.s.refs = append(f.s.refs, r)
	if workflow != WorkflowPath {
		return nil, errors.New("fake: unexpected workflow " + workflow)
	}
	var out []api.WorkflowRun
	for _, run := range f.s.runs {
		if run.HeadSHA == head {
			out = append(out, run)
		}
	}
	return out, f.s.runsErr
}

func wantRejectionV2(t *testing.T, up Upload4, err error, status int, code string) {
	t.Helper()
	var rej *Rejection
	if !errors.As(err, &rej) {
		t.Fatalf("error = %v, want *Rejection %d %s", err, status, code)
	}
	if rej.Status != status || rej.Code != code {
		t.Errorf("rejection = %d %s, want %d %s", rej.Status, rej.Code, status, code)
	}
	if !reflect.DeepEqual(up, Upload4{}) {
		t.Errorf("upload = %+v, want zero", up.Binding)
	}
}

func TestReceiveV2AcceptsEachEvent(t *testing.T) {
	tests := []struct {
		name  string
		build func(*testing.T) *v2Scenario
		want  func(*v2Scenario) Binding4
		calls []string
	}{
		{"pull request", prScenarioV2, func(s *v2Scenario) Binding4 {
			return Binding4{Repo: s.repo, Event: eventPullRequest, PullRequest: prNumber, HeadSHA: headSHA,
				RunSHA: s.claims.SHA, RunID: runID, RunAttempt: 1}
		}, []string{"verify", "resolve", "run", "pull", "runs"}},
		{"push", pushScenarioV2, func(s *v2Scenario) Binding4 {
			return Binding4{Repo: s.repo, Event: eventPush, HeadSHA: headSHA, RunSHA: headSHA, RunID: runID, RunAttempt: 1}
		}, []string{"verify", "resolve", "run", "runs"}},
		{"merge group", mergeGroupScenarioV2, func(s *v2Scenario) Binding4 {
			return Binding4{Repo: s.repo, Event: eventMergeGroup, HeadSHA: groupSHA, RunSHA: groupSHA, RunID: runID, RunAttempt: 1}
		}, []string{"verify", "resolve", "run", "queue", "runs"}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			s := tt.build(t)
			up, err := s.receiveV2(t)
			if err != nil || len(up.Problems) != 0 {
				t.Fatalf("err = %v, problems = %v; want an accepted upload", err, up.Problems)
			}
			if up.Binding != tt.want(s) {
				t.Errorf("binding = %+v, want %+v", up.Binding, tt.want(s))
			}
			if up.Envelope != s.env2 || string(up.Payload) != string(s.body) {
				t.Error("the upload does not carry the decoded envelope and the exact payload")
			}
			if up.Result == nil || up.Result.Status != result.StatusPass {
				t.Errorf("result = %+v, want the decoded pass", up.Result)
			}
			wantCalls(t, s.scenario, tt.calls...)
		})
	}
}

func TestReceiveV2RefusesStaleRuns(t *testing.T) {
	later := runCreated.Add(time.Minute)
	tests := []struct {
		name   string
		mutate func(*v2Scenario)
		status int
		code   string
	}{
		{"an older attempt", func(s *v2Scenario) { s.run.Attempt = 2 }, http.StatusConflict, codeStaleAttempt},
		{"a newer run of the head", func(s *v2Scenario) {
			s.runs = append(s.runs, api.WorkflowRun{ID: runID + 5, Event: eventPullRequest, HeadSHA: headSHA, CreatedAt: later, PullRequests: []int{prNumber}})
		}, http.StatusConflict, codeStaleRun},
		{"a newer run without a pull request association", func(s *v2Scenario) {
			s.runs = append(s.runs, api.WorkflowRun{ID: runID + 5, Event: eventPullRequest, HeadSHA: headSHA, CreatedAt: later})
		}, http.StatusConflict, codeStaleRun},
		{"a run created at the same time with a higher id", func(s *v2Scenario) {
			s.runs = append(s.runs, api.WorkflowRun{ID: runID + 1, Event: eventPullRequest, HeadSHA: headSHA, CreatedAt: runCreated})
		}, http.StatusConflict, codeStaleRun},
		{"a moved pull request head", func(s *v2Scenario) {
			p := s.pulls[prNumber]
			p.HeadSHA = movedSHA
			s.pulls[prNumber] = p
		}, http.StatusConflict, codeStaleHead},
		{"a workflow file other than archfit.yaml", func(s *v2Scenario) {
			s.claims.JobWorkflowRef = repoName + "/.github/workflows/other.yaml@refs/pull/7/merge"
		}, http.StatusForbidden, codeWorkflowPath},
		{"a reusable workflow of another repository", func(s *v2Scenario) {
			s.claims.JobWorkflowRef = otherTenant + "/" + workflowPath + "@refs/heads/main"
		}, http.StatusForbidden, codeWorkflowPath},
		{"the run list cannot be read", func(s *v2Scenario) { s.runsErr = api.ErrRateLimited }, http.StatusTooManyRequests, codeRateLimited},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			s := prScenarioV2(t)
			tt.mutate(s)
			up, err := s.receiveV2(t)
			wantRejectionV2(t, up, err, tt.status, tt.code)
		})
	}
}

func TestReceiveV2IgnoresRunsThatAreNotNewer(t *testing.T) {
	earlier := runCreated.Add(-time.Minute)
	s := prScenarioV2(t)
	s.runs = append(s.runs,
		api.WorkflowRun{ID: runID - 1, Event: eventPullRequest, HeadSHA: headSHA, CreatedAt: earlier},
		api.WorkflowRun{ID: runID + 9, Event: eventPush, HeadSHA: headSHA, CreatedAt: runCreated.Add(time.Hour)},
		api.WorkflowRun{ID: runID + 8, Event: eventPullRequest, HeadSHA: headSHA, CreatedAt: runCreated.Add(time.Hour), PullRequests: []int{prNumber + 1}},
	)
	if up, err := s.receiveV2(t); err != nil || len(up.Problems) != 0 {
		t.Fatalf("err = %v, problems = %v; want accepted", err, up.Problems)
	}
}

func TestReceiveV2RefusesTheEnvelopeBeforeTheToken(t *testing.T) {
	tests := []struct {
		name   string
		mutate func(*v2Scenario)
		status int
		code   string
	}{
		{"a payload digest mismatch", func(s *v2Scenario) { s.env2.PayloadDigest = "sha256:" + strings.Repeat("0", 64) }, http.StatusBadRequest, codeEnvelopeInvalid},
		{"an envelope 1", func(s *v2Scenario) {
			raw, _ := json.Marshal(s.env)
			s.rawEnvelope = string(raw)
		}, http.StatusBadRequest, codeEnvelopeInvalid},
		{"no envelope", func(s *v2Scenario) { s.noEnvelope = true }, http.StatusBadRequest, codeEnvelopeMissing},
		{"a GET", func(s *v2Scenario) { s.method = http.MethodGet }, http.StatusBadRequest, codeMethodNotAllowed},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			s := prScenarioV2(t)
			tt.mutate(s)
			up, err := s.receiveV2(t)
			wantRejectionV2(t, up, err, tt.status, tt.code)
			wantCalls(t, s.scenario)
		})
	}
}

func TestReceiveV2Problems(t *testing.T) {
	tests := []struct {
		name   string
		build  func(*testing.T) *v2Scenario
		mutate func(*v2Scenario)
		want   []Problem
	}{
		{"envelope head", prScenarioV2, func(s *v2Scenario) { s.env2.HeadSHA = movedSHA; s.result["head"] = movedSHA }, []Problem{ProblemEnvelopeMismatch}},
		{"envelope pull request", prScenarioV2, func(s *v2Scenario) { s.env2.PullRequest = prNumber + 1 }, []Problem{ProblemEnvelopeMismatch}},
		{"envelope event", pushScenarioV2, func(s *v2Scenario) { s.env2.Event = eventMergeGroup }, []Problem{ProblemEnvelopeMismatch}},
		{"envelope run id", prScenarioV2, func(s *v2Scenario) { s.env2.RunID = runID + 1 }, []Problem{ProblemEnvelopeMismatch}},
		{"envelope attempt", prScenarioV2, func(s *v2Scenario) { s.env2.RunAttempt = 2 }, []Problem{ProblemEnvelopeMismatch}},
		{"envelope target branch", prScenarioV2, func(s *v2Scenario) { s.env2.TargetRef = "refs/heads/release" }, []Problem{ProblemEnvelopeMismatch}},
		{"result policy digest", prScenarioV2, func(s *v2Scenario) {
			s.result["policy"].(map[string]any)["digest"] = "sha256:" + strings.Repeat("6", 64)
		}, []Problem{ProblemEnvelopeMismatch}},
		{"result head", prScenarioV2, func(s *v2Scenario) { s.result["head"] = movedSHA }, []Problem{ProblemEnvelopeMismatch}},
		{"result engine", prScenarioV2, func(s *v2Scenario) { s.result["engine"] = "4.0.1" }, []Problem{ProblemEnvelopeMismatch}},
		{"engine version without the v", prScenarioV2, func(s *v2Scenario) { s.env2.EngineVersion = "4.0.0" }, []Problem{ProblemEnvelopeMismatch}},
		{"result unreadable", prScenarioV2, func(s *v2Scenario) { s.result["schema"] = "archfit.can.v1" }, []Problem{ProblemReportInvalid}},
		{"result claims waiting", prScenarioV2, func(s *v2Scenario) { s.result["status"] = "waiting" }, []Problem{ProblemReportInvalid}},
		{"closed pull request", prScenarioV2, func(s *v2Scenario) {
			p := s.pulls[prNumber]
			p.State = "closed"
			s.pulls[prNumber] = p
		}, []Problem{ProblemEnvelopeMismatch}},
		{"pull request into another base", prScenarioV2, func(s *v2Scenario) {
			p := s.pulls[prNumber]
			p.BaseRef = "release"
			s.pulls[prNumber] = p
		}, []Problem{ProblemUnsupportedEvent}},
		{"no pull request for the head", prScenarioV2, func(s *v2Scenario) { s.run.PullRequests = nil; s.pulls = nil }, []Problem{ProblemEnvelopeMismatch}},
		{"fork", prScenarioV2, func(s *v2Scenario) {
			p := s.pulls[prNumber]
			p.HeadRepo = "mallory/shop"
			s.pulls[prNumber] = p
		}, []Problem{ProblemFork}},
		{"push to another branch", pushScenarioV2, func(s *v2Scenario) { s.claims.Ref = "refs/heads/release" }, []Problem{ProblemUnsupportedEvent}},
		{"a workflow_dispatch run", pushScenarioV2, func(s *v2Scenario) {
			s.run.Event, s.claims.EventName = eventDispatch, eventDispatch
			s.runs = []api.WorkflowRun{s.run}
		}, []Problem{ProblemEnvelopeMismatch, ProblemUnsupportedEvent}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			s := tt.build(t)
			tt.mutate(s)
			up, err := s.receiveV2(t)
			if err != nil {
				t.Fatalf("error = %v, want problems %v", err, tt.want)
			}
			if !reflect.DeepEqual(up.Problems, tt.want) {
				t.Errorf("problems = %v, want %v", up.Problems, tt.want)
			}
			if tt.want[0] == ProblemReportInvalid && up.Result != nil {
				t.Errorf("result = %+v, want nil for an invalid result", up.Result)
			}
		})
	}
}

func TestReceiveV2RefusesAMergeGroupTheQueueDoesNotList(t *testing.T) {
	s := mergeGroupScenarioV2(t)
	s.queue = s.queue[:1]
	up, err := s.receiveV2(t)
	wantRejectionV2(t, up, err, http.StatusConflict, codeStaleGroup)
}

func TestReceiveV2LogsIdentifiersOnly(t *testing.T) {
	s := prScenarioV2(t)
	s.result["summary"] = "archfit: pass LEAK-RESULT"
	if _, err := s.receiveV2(t); err != nil {
		t.Fatal(err)
	}
	out := logs.String()
	for _, leak := range []string{"LEAK-RESULT", testToken, s.rawEnvelope} {
		if strings.Contains(out, leak) {
			t.Errorf("the log carries %q", leak)
		}
	}
	if !strings.Contains(out, `"envelope":2`) {
		t.Error("the log does not mark the envelope 2 upload")
	}
}

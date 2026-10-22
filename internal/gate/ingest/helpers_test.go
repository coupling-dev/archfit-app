package ingest

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"os"
	"reflect"
	"strings"
	"sync"
	"testing"

	"github.com/alexei-led/archfit-app/internal/github/api"
	"github.com/alexei-led/archfit-app/internal/github/oidc"
	"github.com/alexei-led/archfit-app/internal/wire/envelope"
)

const (
	testAudience   = "https://archfit.test"
	testToken      = "LEAK-TOKEN-header.claims.signature"
	repoName       = "acme/shop"
	repoID         = int64(501)
	installationID = int64(11)
	otherTenant    = "evil/corp"
	otherInstallID = "99"
	defaultBranch  = "main"
	runID          = int64(1001)
	prNumber       = 7
	workflowPath   = ".github/workflows/archfit.yaml"
	discoveryYAML  = "# LEAK-DISCOVERY-DRAFT\nmodules:\n  - name: shop\n    path: pkg/shop\n"
	baselineJSON   = `{"schema_version":"archfit.baseline.v3","accepted":[]}`
)

var (
	headSHA      = strings.Repeat("a", 40)
	baseSHA      = strings.Repeat("b", 40)
	mergeBaseSHA = strings.Repeat("c", 40)
	movedSHA     = strings.Repeat("f", 40)
	imageDigest  = "sha256:" + strings.Repeat("d", 64)
)

// logs captures the default slog logger for the whole package run.
var logs = &syncBuffer{}

func TestMain(m *testing.M) {
	slog.SetDefault(slog.New(slog.NewJSONHandler(logs, &slog.HandlerOptions{Level: slog.LevelDebug})))
	os.Exit(m.Run())
}

type syncBuffer struct {
	mu  sync.Mutex
	buf bytes.Buffer
}

func (b *syncBuffer) Write(p []byte) (int, error) {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.buf.Write(p)
}

func (b *syncBuffer) String() string {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.buf.String()
}

// scenario is one upload with the GitHub state the fakes answer with.
type scenario struct {
	kind        envelope.Kind
	notAdmitted bool     // no operator approved the installation
	admitDown   bool     // the approval cannot be read
	admitAsked  []string // "<installation ID> <owner>" per Admitted call
	method      string
	target      string
	body        []byte
	reader      io.Reader // replaces body when set
	env         envelope.Envelope
	rawEnvelope string // replaces the marshalled env when set
	noEnvelope  bool
	auth        string
	headers     map[string]string

	claims     oidc.Claims
	verifyErr  error
	repo       api.Repo
	resolveErr error
	run        api.WorkflowRun
	runErr     error
	pulls      map[int]api.Pull
	pullErr    error

	calls    []string
	resolved []string
	refs     []api.RepoRef
}

func readFixture(t *testing.T, name string) []byte {
	t.Helper()
	raw, err := os.ReadFile("testdata/" + name)
	if err != nil {
		t.Fatal(err)
	}
	return raw
}

// prScenario is the healthy pull_request report upload.
func prScenario(t *testing.T) *scenario {
	t.Helper()
	s := &scenario{
		kind:   envelope.KindReport,
		method: http.MethodPost,
		target: "/v1/reports",
		auth:   "Bearer " + testToken,
		env: envelope.Envelope{
			SchemaVersion: envelope.SchemaV1,
			Kind:          envelope.KindReport,
			Repository:    repoName,
			Event:         eventPullRequest,
			PullRequest:   prNumber,
			HeadSHA:       headSHA,
			BaseSHA:       baseSHA,
			MergeBaseSHA:  mergeBaseSHA,
			RunID:         runID,
			RunAttempt:    1,
			EngineVersion: "v3.0.1",
			ImageDigest:   imageDigest,
			Platform:      "linux/amd64",
		},
		claims: oidc.Claims{
			Repository:        repoName,
			RepositoryID:      repoID,
			RunID:             runID,
			RunAttempt:        1,
			SHA:               strings.Repeat("9", 40), // the merge commit, never compared for pull_request
			Ref:               "refs/pull/7/merge",
			EventName:         eventPullRequest,
			JobWorkflowRef:    repoName + "/" + workflowPath + "@refs/pull/7/merge",
			RunnerEnvironment: "github-hosted",
		},
		repo: api.Repo{
			RepoRef:       api.RepoRef{InstallationID: installationID, FullName: repoName},
			ID:            repoID,
			DefaultBranch: defaultBranch,
		},
		run: api.WorkflowRun{
			ID:           runID,
			Attempt:      1,
			Event:        eventPullRequest,
			HeadSHA:      headSHA,
			HeadRepo:     repoName,
			WorkflowPath: workflowPath,
			Status:       "in_progress",
			PullRequests: []int{prNumber},
		},
		pulls: map[int]api.Pull{prNumber: openPull(prNumber)},
	}
	s.setBody(readFixture(t, "healthy.json"))
	return s
}

func openPull(number int) api.Pull {
	return api.Pull{
		Number:       number,
		State:        "open",
		HeadSHA:      headSHA,
		HeadRef:      "feature/x",
		HeadRepo:     repoName,
		BaseRef:      "release",
		BaseSHA:      baseSHA,
		MergeBaseSHA: mergeBaseSHA,
	}
}

// pushScenario is the healthy report upload from a push to the default branch.
func pushScenario(t *testing.T) *scenario {
	t.Helper()
	s := prScenario(t)
	s.toEvent(eventPush, "refs/heads/"+defaultBranch)
	return s
}

// discoveryScenario is the healthy discovery upload from a workflow_dispatch run.
func discoveryScenario(t *testing.T) *scenario {
	t.Helper()
	s := prScenario(t)
	s.toEvent(eventDispatch, "refs/heads/"+defaultBranch)
	s.toDiscovery()
	return s
}

func (s *scenario) toDiscovery() {
	s.kind = envelope.KindDiscovery
	s.target = "/v1/discoveries"
	s.env.Kind = envelope.KindDiscovery
	s.setBody([]byte(discoveryYAML))
}

// toBaseline turns the scenario into a baseline upload; the payload is opaque to this module.
func (s *scenario) toBaseline() {
	s.kind = envelope.KindBaseline
	s.target = "/v1/baselines"
	s.env.Kind = envelope.KindBaseline
	s.setBody([]byte(baselineJSON))
}

// toEvent turns the scenario into a consistent non-PR run of the given event.
func (s *scenario) toEvent(event, ref string) {
	s.run.Event = event
	s.run.PullRequests = nil
	s.claims.EventName = event
	s.claims.Ref = ref
	s.claims.SHA = s.run.HeadSHA
	s.claims.JobWorkflowRef = repoName + "/" + workflowPath + "@" + ref
	s.env.Event = event
	s.env.PullRequest = 0
	s.env.BaseSHA = ""
	s.env.MergeBaseSHA = ""
}

func (s *scenario) setBody(body []byte) {
	s.body = body
	s.env.PayloadDigest = envelope.PayloadDigest(body)
}

func (s *scenario) request(t *testing.T) *http.Request {
	t.Helper()
	body := s.reader
	if body == nil {
		body = bytes.NewReader(s.body)
	}
	req := httptest.NewRequest(s.method, s.target, body)
	switch {
	case s.noEnvelope:
	case s.rawEnvelope != "":
		req.Header.Set("X-Archfit-Envelope", s.rawEnvelope)
	default:
		raw, err := json.Marshal(s.env)
		if err != nil {
			t.Fatal(err)
		}
		req.Header.Set("X-Archfit-Envelope", string(raw))
	}
	if s.auth != "" {
		req.Header.Set("Authorization", s.auth)
	}
	for k, v := range s.headers {
		req.Header.Set(k, v)
	}
	return req
}

func (s *scenario) receive(t *testing.T) (Upload, error) {
	t.Helper()
	s.calls, s.resolved, s.refs = nil, nil, nil
	r := New(fakeRepos{s: s}, fakePulls{s: s}, fakeActions{s: s}, fakeVerifier{s: s}, testAudience, fakeAdmitter{s: s})
	return r.Receive(context.Background(), s.request(t), s.kind)
}

type fakeAdmitter struct{ s *scenario }

func (a fakeAdmitter) Admitted(_ context.Context, installationID int64, owner string) (bool, error) {
	a.s.admitAsked = append(a.s.admitAsked, fmt.Sprintf("%d %s", installationID, owner))
	if a.s.admitDown {
		return false, errors.New("fake: store down")
	}
	return !a.s.notAdmitted, nil
}

// healthyBinding is what GitHub says about prScenario.
func healthyBinding(s *scenario) Binding {
	return Binding{
		Repo:         s.repo,
		Event:        eventPullRequest,
		PullRequest:  prNumber,
		HeadSHA:      headSHA,
		BaseRef:      "release",
		BaseSHA:      baseSHA,
		MergeBaseSHA: mergeBaseSHA,
		RunID:        runID,
		RunAttempt:   1,
		WorkflowPath: workflowPath,
	}
}

type fakeVerifier struct{ s *scenario }

func (f fakeVerifier) Verify(_ context.Context, token, audience string) (oidc.Claims, error) {
	f.s.calls = append(f.s.calls, "verify")
	if f.s.verifyErr != nil {
		return oidc.Claims{}, f.s.verifyErr
	}
	if token != testToken || audience != testAudience {
		return oidc.Claims{}, errors.New("fake verifier: unexpected token or audience")
	}
	return f.s.claims, nil
}

// The fakes embed the role interface: an unexpected method call panics.
type fakeRepos struct {
	api.Repos
	s *scenario
}

func (f fakeRepos) ResolveRepo(_ context.Context, fullName string) (api.Repo, error) {
	f.s.calls = append(f.s.calls, "resolve")
	f.s.resolved = append(f.s.resolved, fullName)
	if f.s.resolveErr != nil {
		return api.Repo{}, f.s.resolveErr
	}
	if fullName != f.s.repo.FullName {
		return api.Repo{}, api.ErrNotFound
	}
	return f.s.repo, nil
}

type fakeActions struct {
	api.Actions
	s *scenario
}

func (f fakeActions) Run(_ context.Context, r api.RepoRef, id int64) (api.WorkflowRun, error) {
	f.s.calls = append(f.s.calls, "run")
	f.s.refs = append(f.s.refs, r)
	if f.s.runErr != nil {
		return api.WorkflowRun{}, f.s.runErr
	}
	if id != f.s.run.ID {
		return api.WorkflowRun{}, api.ErrNotFound
	}
	return f.s.run, nil
}

type fakePulls struct {
	api.Pulls
	s *scenario
}

func (f fakePulls) Pull(_ context.Context, r api.RepoRef, number int) (api.Pull, error) {
	f.s.calls = append(f.s.calls, "pull")
	f.s.refs = append(f.s.refs, r)
	if f.s.pullErr != nil {
		return api.Pull{}, f.s.pullErr
	}
	p, ok := f.s.pulls[number]
	if !ok {
		return api.Pull{}, api.ErrNotFound
	}
	return p, nil
}

func wantRejection(t *testing.T, up Upload, err error, status int, code string) {
	t.Helper()
	var rej *Rejection
	if !errors.As(err, &rej) {
		t.Fatalf("error = %v, want *Rejection %d %s", err, status, code)
	}
	if rej.Status != status || rej.Code != code {
		t.Errorf("rejection = %d %s, want %d %s", rej.Status, rej.Code, status, code)
	}
	if !reflect.DeepEqual(up, Upload{}) {
		t.Errorf("upload = %+v, want zero", up.Binding)
	}
}

func wantProblems(t *testing.T, up Upload, err error, want ...Problem) {
	t.Helper()
	if err != nil {
		t.Fatalf("error = %v, want nil", err)
	}
	if len(up.Problems) != len(want) || (len(want) > 0 && !reflect.DeepEqual(up.Problems, want)) {
		t.Errorf("problems = %v, want %v", up.Problems, want)
	}
}

func wantCalls(t *testing.T, s *scenario, want ...string) {
	t.Helper()
	if strings.Join(s.calls, ",") != strings.Join(want, ",") {
		t.Errorf("calls = %v, want %v", s.calls, want)
	}
}

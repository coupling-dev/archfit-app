package onboarding

import (
	"bytes"
	"context"
	"encoding/json"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/alexei-led/archfit-app/internal/gate"
	"github.com/alexei-led/archfit-app/internal/github/api"
	"github.com/alexei-led/archfit-app/internal/wire/policy"
	"github.com/alexei-led/archfit-app/internal/wire/settings"
)

const (
	testBaseURL  = "https://archfit.example"
	validPolicy  = "version: 1\nmodules:\n  core:\n    paths:\n      - internal/core/**\n"
	waiverPolicy = "version: 1\nwaivers:\n  - rule: no-cycles\n    from: core\n    to: ui\n    reason: legacy\n    approved_by: alice\n    expires: 2027-01-01\n"
	stateCheck   = "archfit/state"
	runHeadSHA   = "1111111111111111111111111111111111111111"
)

var (
	testStart = time.Date(2026, 9, 21, 10, 0, 0, 0, time.UTC)
	owners    = []string{"alice", "acme/architects"}
)

func testRepo(name string) api.Repo {
	return api.Repo{RepoRef: api.RepoRef{InstallationID: 7, FullName: name}, ID: 42, DefaultBranch: "main"}
}

type fixture struct {
	hub      *hub
	clock    *clock
	receiver *fakeReceiver
	consents *fakeConsents
	tokens   *fakeTokens
	sandbox  *fakeSandbox
	repo     api.Repo
	svc      *service
}

func newFixture(t *testing.T) *fixture {
	t.Helper()
	f := &fixture{
		hub:      newHub(),
		clock:    &clock{t: testStart},
		receiver: &fakeReceiver{},
		consents: &fakeConsents{has: true},
		tokens:   &fakeTokens{},
		repo:     testRepo("acme/shop"),
	}
	f.sandbox = newSandbox(func(context.Context, DiscoveryJob) (DiscoveryResult, error) {
		return DiscoveryResult{DraftPolicy: []byte(validPolicy)}, nil
	})
	f.svc = f.newService()
	return f
}

// newService builds another Onboarding value over the same fakes: a second instance or a restarted process.
func (f *fixture) newService() *service {
	return newService(Deps{
		Tokens:    f.tokens,
		Receiver:  f.receiver,
		Proposals: fakeProposals{f.hub},
		Consents:  f.consents,
		Sandbox:   f.sandbox,
		BaseURL:   testBaseURL,
		Now:       f.clock.now,
	}, roles{repos: f.hub, actions: f.hub, checks: f.hub, pulls: f.hub})
}

func renderSettings(gateMode settings.GateMode, workflow string) []byte {
	return settings.Render(settings.Settings{Version: 1, Owners: owners, Gate: gateMode, Workflow: workflow, DataMode: settings.DataCustomer})
}

// addWorkflow does what the owner does: commits the workflow Progress hands out, where Progress says it goes.
func (f *fixture) addWorkflow(t *testing.T, r api.Repo) {
	t.Helper()
	p := mustProgress(t, f.svc, r)
	if p.Workflow == nil {
		t.Fatalf("Progress asks for no workflow at stage %q (%q)", p.Stage, p.Diagnostic)
	}
	f.hub.put(r, p.Workflow.Branch, p.Workflow.Path, p.Workflow.Content)
}

// onboard puts the settings and the workflow on the default branch.
func (f *fixture) onboard(r api.Repo, gateMode settings.GateMode) {
	f.hub.put(r, r.DefaultBranch, settings.Path, renderSettings(gateMode, workflowPath))
	f.hub.put(r, r.DefaultBranch, workflowPath, []byte("name: archfit\n"))
}

func (f *fixture) report(r api.Repo) {
	f.hub.setReported(r,
		api.WorkflowRun{ID: 9, Event: "push", Status: "completed", HeadSHA: runHeadSHA, WorkflowPath: workflowPath},
		api.CheckRun{ID: 1, Name: stateCheck, HeadSHA: runHeadSHA})
}

func discoveryUpload(r api.Repo, event string, payload []byte) gate.Upload {
	return gate.Upload{
		Binding: gate.Binding{
			Repo: r, Event: event, HeadSHA: runHeadSHA, BaseRef: r.DefaultBranch,
			RunID: 9001, RunAttempt: 1, WorkflowPath: workflowPath,
		},
		Digest:  "sha256:" + strings.Repeat("ab", 32),
		Payload: payload,
	}
}

type uploadResponse struct {
	status    int
	raw       string
	Error     string `json:"error"`
	Number    int    `json:"pull_request"`
	URL       string `json:"url"`
	Unchanged bool   `json:"unchanged"`
}

func (f *fixture) post(t *testing.T) uploadResponse {
	t.Helper()
	req := httptest.NewRequest(http.MethodPost, "/v1/discoveries", strings.NewReader("ignored: the fake Receiver supplies the upload"))
	rec := httptest.NewRecorder()
	f.svc.DiscoveryHandler().ServeHTTP(rec, req)
	out := uploadResponse{status: rec.Code, raw: rec.Body.String()}
	if err := json.Unmarshal(rec.Body.Bytes(), &out); err != nil {
		t.Fatalf("response is not JSON: %q", out.raw)
	}
	return out
}

func mustProgress(t *testing.T, o Onboarding, r api.Repo) Progress {
	t.Helper()
	p, err := o.Progress(context.Background(), r)
	if err != nil {
		t.Fatalf("Progress: %v", err)
	}
	return p
}

func wantStage(t *testing.T, p Progress, stage Stage, diagnostic string) {
	t.Helper()
	if p.Stage != stage {
		t.Errorf("stage = %q, want %q", p.Stage, stage)
	}
	if !strings.Contains(p.Diagnostic, diagnostic) {
		t.Errorf("diagnostic = %q, want it to contain %q", p.Diagnostic, diagnostic)
	}
}

func receive(t *testing.T, ch <-chan DiscoveryJob) DiscoveryJob {
	t.Helper()
	select {
	case job := <-ch:
		return job
	case <-time.After(10 * time.Second):
		t.Fatal("the sandbox job did not start")
		return DiscoveryJob{}
	}
}

func checkFixtures(t *testing.T) {
	t.Helper()
	for name, fixture := range map[string]string{"validPolicy": validPolicy, "waiverPolicy": waiverPolicy} {
		if err := policy.Validate([]byte(fixture)); err != nil {
			t.Fatalf("%s fixture: %v", name, err)
		}
	}
	if w, err := policy.Waivers([]byte(validPolicy)); err != nil || len(w) != 0 {
		t.Fatalf("validPolicy fixture: %v, %v", w, err)
	}
	if w, err := policy.Waivers([]byte(waiverPolicy)); err != nil || len(w) != 1 {
		t.Fatalf("waiverPolicy fixture: %v, %v", w, err)
	}
}

type lockedBuffer struct {
	mu sync.Mutex
	b  bytes.Buffer
}

func (l *lockedBuffer) Write(p []byte) (int, error) {
	l.mu.Lock()
	defer l.mu.Unlock()
	return l.b.Write(p)
}

func (l *lockedBuffer) String() string {
	l.mu.Lock()
	defer l.mu.Unlock()
	return l.b.String()
}

// captureLogs swaps the default logger; tests using it must not run in parallel.
func captureLogs(t *testing.T) *lockedBuffer {
	t.Helper()
	buf := &lockedBuffer{}
	prev := slog.Default()
	slog.SetDefault(slog.New(slog.NewJSONHandler(buf, &slog.HandlerOptions{Level: slog.LevelDebug})))
	t.Cleanup(func() { slog.SetDefault(prev) })
	return buf
}

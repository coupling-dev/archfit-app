package gate

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/alexei-led/archfit-app/internal/gate/feedback"
	"github.com/alexei-led/archfit-app/internal/gate/inputs"
	"github.com/alexei-led/archfit-app/internal/github/api"
	"github.com/alexei-led/archfit-app/internal/github/oidc"
	"github.com/alexei-led/archfit-app/internal/github/webhook"
	"github.com/alexei-led/archfit-app/internal/store"
	"github.com/alexei-led/archfit-app/internal/wire/envelope"
	"github.com/alexei-led/archfit-app/internal/wire/manifest"
	"github.com/alexei-led/archfit-app/internal/wire/policy"
	"github.com/alexei-led/archfit-app/internal/wire/settings"
)

const (
	testAudience = "https://app.archfit.test"
	testWorkflow = ".github/workflows/archfit.yaml"
	testBranch   = "main"
	ownerLogin   = "olivia"

	shopInstallation = 11
	shopRepoID       = 101
	shopName         = "acme/shop"

	fixtureHealthy   = "healthy"
	fixtureBlocked   = "blocked"
	fixtureAttention = "attention-warning-only"
	fixtureNoProfile = "unknown-identity"

	basePolicy = "version: 1\nmodules:\n  a:\n    paths: [\"pkg/a/**\"]\n"
)

var testStart = time.Date(2026, 9, 21, 12, 0, 0, 0, time.UTC)

// world is the real children wired over the in-memory GitHub, a stub verifier, a guarded memory Store
// and a fixed clock. It is the composition New builds, minus the HTTP client.
type world struct {
	t     *testing.T
	gh    *fakeGitHub
	oidc  *fakeVerifier
	mem   store.Store
	store *guardStore
	now   time.Time
	gate  *Gate
	shop  api.Repo

	nextRun int64
	nextSHA int
}

func newWorld(t *testing.T) *world {
	t.Helper()
	w := &world{
		t:       t,
		gh:      newFakeGitHub(),
		oidc:    &fakeVerifier{audience: testAudience, tokens: map[string]fakeToken{}},
		mem:     store.NewMemory(),
		now:     testStart,
		nextRun: 5000,
	}
	w.store = &guardStore{inner: w.mem}
	w.gate = compose(w.gh, w.oidc, w.store, testAudience,
		Config{MissingUploadAfter: 60 * time.Minute, WaiverWarnBefore: 7 * 24 * time.Hour}, w.clock)
	w.shop = w.addRepo(shopName, shopInstallation, shopRepoID, false)
	return w
}

func (w *world) clock() time.Time { return w.now }

// addRepo installs the App on an onboarded repository: required mode, one owner, a policy on the default branch.
func (w *world) addRepo(fullName string, installationID, id int64, private bool) api.Repo {
	r := api.Repo{
		RepoRef: api.RepoRef{InstallationID: installationID, FullName: fullName},
		ID:      id, Private: private, DefaultBranch: testBranch,
	}
	w.gh.repos[fullName] = r
	known := false
	for _, inst := range w.gh.installations {
		known = known || inst.ID == installationID
	}
	if !known {
		w.gh.installations = append(w.gh.installations, api.Installation{ID: installationID, Account: "acme"})
	}
	w.setSettings(r, settings.GateRequired)
	w.setFile(r, testBranch, policy.PolicyPath, []byte(basePolicy))
	return r
}

func (w *world) setSettings(r api.Repo, mode settings.GateMode) {
	w.setFile(r, testBranch, settings.Path, settings.Render(settings.Settings{
		Version: 1, Owners: []string{ownerLogin}, Gate: mode, Workflow: testWorkflow, DataMode: settings.DataCustomer,
	}))
}

func (w *world) setFile(r api.Repo, ref, path string, raw []byte) {
	w.gh.files[fileKey(r.FullName, ref, path)] = raw
}

func (w *world) removeFile(r api.Repo, ref, path string) {
	delete(w.gh.files, fileKey(r.FullName, ref, path))
}

func (w *world) digestOf(r api.Repo, ref, path string) string {
	raw, ok := w.gh.files[fileKey(r.FullName, ref, path)]
	if !ok {
		w.t.Fatalf("no file %s at %s", path, ref)
	}
	return policy.Digest(raw)
}

func (w *world) sha() string {
	w.nextSHA++
	return fmt.Sprintf("%040x", w.nextSHA)
}

// pull is a handle on one open pull request and the workflow run of its head.
type pull struct {
	repo   api.Repo
	number int
	head   string
	runID  int64
}

func (w *world) openPull(r api.Repo, number int) *pull {
	p := &pull{repo: r, number: number}
	w.gh.pulls[pullKey(r.FullName, number)] = api.Pull{
		Number: number, State: "open", HeadRef: fmt.Sprintf("feature-%d", number), HeadRepo: r.FullName,
		BaseRef: testBranch, BaseSHA: w.sha(), MergeBaseSHA: w.sha(),
	}
	w.push(p)
	return p
}

// push moves the pull to a new head commit with a new workflow run.
func (w *world) push(p *pull) {
	key := pullKey(p.repo.FullName, p.number)
	pl := w.gh.pulls[key]
	pl.HeadSHA = w.sha()
	w.gh.pulls[key] = pl
	w.nextRun++
	p.head, p.runID = pl.HeadSHA, w.nextRun
	w.gh.runs[p.runID] = api.WorkflowRun{
		ID: p.runID, Attempt: 1, Event: "pull_request", HeadSHA: p.head, HeadRepo: pl.HeadRepo,
		WorkflowPath: testWorkflow, Status: runCompleted, PullRequests: []int{p.number},
	}
}

func (w *world) editPull(p *pull, edit func(*api.Pull)) {
	key := pullKey(p.repo.FullName, p.number)
	pl := w.gh.pulls[key]
	edit(&pl)
	w.gh.pulls[key] = pl
}

func (w *world) editRun(p *pull, edit func(*api.WorkflowRun)) {
	run := w.gh.runs[p.runID]
	edit(&run)
	w.gh.runs[p.runID] = run
}

// finishRun completes the attempt a re-run started, as the workflow does after its upload.
func (w *world) finishRun(p *pull) {
	w.editRun(p, func(r *api.WorkflowRun) { r.Status = runCompleted })
}

func (w *world) changes(p *pull, paths ...string) {
	w.gh.changed[pullKey(p.repo.FullName, p.number)] = paths
}

func (w *world) review(p *pull, login, state, sha string) {
	key := pullKey(p.repo.FullName, p.number)
	w.gh.reviews[key] = append(w.gh.reviews[key], api.Review{Reviewer: login, State: state, CommitSHA: sha})
}

// uploadSpec is one POST /v1/reports. spec returns a fully consistent one; tests break single parts of it.
type uploadSpec struct {
	body        []byte
	env         envelope.Envelope
	rawEnvelope []byte // overrides env when set
	token       string
	claims      oidc.Claims
	url         string
	headers     map[string]string
}

func (w *world) spec(p *pull, body []byte) uploadSpec {
	run := w.gh.runs[p.runID]
	pl := w.gh.pulls[pullKey(p.repo.FullName, p.number)]
	id := manifest.Current().Identity
	s := uploadSpec{
		body: body,
		env: envelope.Envelope{
			SchemaVersion: envelope.SchemaV1, Kind: envelope.KindReport, Repository: p.repo.FullName,
			Event: "pull_request", PullRequest: p.number, HeadSHA: p.head, BaseSHA: pl.BaseSHA, MergeBaseSHA: pl.MergeBaseSHA,
			RunID: p.runID, RunAttempt: run.Attempt,
			EngineVersion: id.EngineVersion, ImageDigest: id.ImageDigest, Platform: id.Platform,
			PayloadDigest: envelope.PayloadDigest(body),
		},
		token: fmt.Sprintf("token-%d-%d", p.runID, run.Attempt),
		claims: oidc.Claims{
			Repository: p.repo.FullName, RepositoryID: p.repo.ID, RunID: p.runID, RunAttempt: run.Attempt,
			SHA: w.sha(), Ref: fmt.Sprintf("refs/pull/%d/merge", p.number), EventName: "pull_request",
			JobWorkflowRef:    p.repo.FullName + "/" + testWorkflow + "@" + fmt.Sprintf("refs/pull/%d/merge", p.number),
			RunnerEnvironment: "github-hosted",
		},
		url: "/v1/reports",
	}
	return s
}

func (w *world) request(s uploadSpec) *http.Request {
	w.t.Helper()
	raw := s.rawEnvelope
	if raw == nil {
		var err error
		if raw, err = json.Marshal(s.env); err != nil {
			w.t.Fatal(err)
		}
	}
	if s.claims != (oidc.Claims{}) { // zero claims: the token is one the test registered itself, or none
		w.oidc.tokens[s.token] = fakeToken{claims: s.claims}
	}
	req := httptest.NewRequest(http.MethodPost, s.url, bytes.NewReader(s.body))
	req.Header.Set("X-Archfit-Envelope", string(raw))
	if s.token != "" {
		req.Header.Set("Authorization", "Bearer "+s.token)
	}
	for k, v := range s.headers {
		req.Header.Set(k, v)
	}
	return req
}

type answer struct {
	status     int
	retryAfter string
	raw        string
	Conclusion string `json:"conclusion"`
	Reason     string `json:"reason"`
	Error      string `json:"error"`
}

func (w *world) send(s uploadSpec) answer {
	w.t.Helper()
	return serve(w.t, w.gate, w.request(s))
}

func serve(t *testing.T, g *Gate, req *http.Request) answer {
	t.Helper()
	rec := httptest.NewRecorder()
	g.UploadHandler().ServeHTTP(rec, req)
	a := answer{status: rec.Code, retryAfter: rec.Header().Get("Retry-After"), raw: rec.Body.String()}
	if ct := rec.Header().Get("Content-Type"); ct != "application/json" {
		t.Errorf("Content-Type = %q, want application/json", ct)
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &a); err != nil {
		t.Fatalf("answer %q is not JSON: %v", a.raw, err)
	}
	return a
}

// upload sends a consistent upload of the fixture for the pull and returns the answer.
func (w *world) upload(p *pull, fixture string, muts ...func(map[string]any)) answer {
	w.t.Helper()
	return w.send(w.spec(p, w.report(p.repo, fixture, muts...)))
}

func (w *world) wantAnswer(a answer, status int, conclusion, reason string) {
	w.t.Helper()
	if a.status != status || a.Conclusion != conclusion || a.Reason != reason {
		w.t.Fatalf("answer = %d %s, want %d conclusion=%s reason=%s", a.status, a.raw, status, conclusion, reason)
	}
}

// report loads a fixture and binds it to the world: the config hash of the policy on the default branch and the
// profile version of the locked manifest. muts run after that.
func (w *world) report(r api.Repo, fixture string, muts ...func(map[string]any)) []byte {
	w.t.Helper()
	raw, err := os.ReadFile(filepath.Join("testdata", "report", fixture+".json"))
	if err != nil {
		w.t.Fatal(err)
	}
	var doc map[string]any
	if err := json.Unmarshal(raw, &doc); err != nil {
		w.t.Fatal(err)
	}
	cmp := doc["comparison"].(map[string]any)
	if _, ok := w.gh.files[fileKey(r.FullName, testBranch, policy.PolicyPath)]; ok {
		cmp["config_hash"] = w.digestOf(r, testBranch, policy.PolicyPath)
	}
	if profile, ok := cmp["measurement_profile"].(map[string]any); ok {
		profile["version"] = manifest.Current().ProfileVersion
	}
	for _, mut := range muts {
		mut(doc)
	}
	out, err := json.Marshal(doc)
	if err != nil {
		w.t.Fatal(err)
	}
	return out
}

func configHash(h string) func(map[string]any) {
	return func(doc map[string]any) { doc["comparison"].(map[string]any)["config_hash"] = h }
}

func gateReference(status string, reasons ...string) func(map[string]any) {
	return func(doc map[string]any) {
		list := make([]any, len(reasons))
		for i, r := range reasons {
			list[i] = r
		}
		doc["gate_reference"] = map[string]any{"status": status, "base_ref": "baseline", "reasons": list}
	}
}

func noGateReference(doc map[string]any) { delete(doc, "gate_reference") }

func (w *world) event(kind webhook.EventKind, p *pull) webhook.Event {
	pl := w.gh.pulls[pullKey(p.repo.FullName, p.number)]
	return webhook.Event{
		Kind: kind, DeliveryID: "delivery-1", InstallationID: p.repo.InstallationID, Repo: p.repo.FullName,
		RepoPrivate: p.repo.Private, PullRequest: p.number, HeadSHA: pl.HeadSHA, HeadRepo: pl.HeadRepo, BaseRef: pl.BaseRef,
	}
}

func (w *world) pushEvent(r api.Repo, paths ...string) webhook.Event {
	return webhook.Event{
		Kind: webhook.DefaultBranchPush, DeliveryID: "delivery-push", InstallationID: r.InstallationID, Repo: r.FullName,
		RepoPrivate: r.Private, HeadSHA: w.sha(), HeadRepo: r.FullName, BaseRef: r.DefaultBranch, ChangedPaths: paths,
	}
}

func (w *world) handle(e webhook.Event) {
	w.t.Helper()
	if err := w.gate.HandleEvent(context.Background(), e); err != nil {
		w.t.Fatalf("HandleEvent(%s): %v", e.Kind, err)
	}
}

// check returns the one check of that name on the head; more than one is a failure.
func (w *world) check(p *pull, head, name string) (api.CheckRun, bool) {
	w.t.Helper()
	runs := w.gh.checkRuns(p.repo.FullName, head, name)
	if len(runs) > 1 {
		w.t.Fatalf("%d %s checks on %s, want at most one", len(runs), name, head)
	}
	if len(runs) == 0 {
		return api.CheckRun{}, false
	}
	return runs[0], true
}

func (w *world) wantGate(p *pull, status, conclusion string) api.CheckRun {
	w.t.Helper()
	c, ok := w.check(p, p.head, gateCheckName)
	if !ok {
		w.t.Fatalf("no %s check on PR %d", gateCheckName, p.number)
	}
	if c.Status != status || c.Conclusion != conclusion {
		w.t.Fatalf("%s on PR %d = %s/%q, want %s/%q", gateCheckName, p.number, c.Status, c.Conclusion, status, conclusion)
	}
	return c
}

// receipt reads the gate check back through the Publisher contract.
func (w *world) receipt(p *pull) feedback.Receipt {
	w.t.Helper()
	state, err := w.gate.publisher.State(context.Background(), w.target(p))
	if err != nil {
		w.t.Fatal(err)
	}
	return state.Receipt
}

func (w *world) target(p *pull) inputs.Target {
	return inputs.Target{Repo: p.repo, PullRequest: p.number, HeadSHA: p.head, BaseRef: testBranch}
}

func (w *world) comments(p *pull) []api.Comment {
	return w.gh.comments[pullKey(p.repo.FullName, p.number)]
}

// feedbackText is everything the App wrote on the pull: check titles, summaries, annotations and comments.
func (w *world) feedbackText(p *pull) string {
	var b strings.Builder
	for _, c := range w.gh.checks[p.repo.FullName] {
		if c.HeadSHA != p.head {
			continue
		}
		b.WriteString(c.Title + "\n" + c.Summary + "\n")
		for _, a := range c.Annotations {
			fmt.Fprintf(&b, "%s:%d %s\n", a.Path, a.Line, a.Message)
		}
	}
	for _, c := range w.comments(p) {
		b.WriteString(c.Body + "\n")
	}
	return b.String()
}

// noSuccess fails when any gate check of the world concluded success.
func (w *world) noSuccess() {
	w.t.Helper()
	for _, c := range w.gh.checkWrites {
		if c.Name == gateCheckName && c.Conclusion == "success" {
			w.t.Fatalf("%s concluded success on %s", gateCheckName, c.HeadSHA)
		}
	}
}

func (w *world) entitle(installationID int64, e Entitlement) {
	w.t.Helper()
	raw, err := json.Marshal(e)
	if err != nil {
		w.t.Fatal(err)
	}
	if err := w.mem.Put(context.Background(), store.Tenant(installationID), EntitlementKey, raw); err != nil {
		w.t.Fatal(err)
	}
}

// captureLogs swaps the default logger. Tests that use it do not run in parallel.
func captureLogs(t *testing.T) *bytes.Buffer {
	t.Helper()
	var buf bytes.Buffer
	old := slog.Default()
	slog.SetDefault(slog.New(slog.NewJSONHandler(&buf, &slog.HandlerOptions{Level: slog.LevelDebug})))
	t.Cleanup(func() { slog.SetDefault(old) })
	return &buf
}

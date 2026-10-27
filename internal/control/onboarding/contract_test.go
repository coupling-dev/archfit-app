package onboarding

import (
	"bytes"
	"context"
	"errors"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/alexei-led/archfit-app/internal/control/proposal"
	"github.com/alexei-led/archfit-app/internal/gate"
	"github.com/alexei-led/archfit-app/internal/github/api"
	"github.com/alexei-led/archfit-app/internal/wire/envelope"
	"github.com/alexei-led/archfit-app/internal/wire/manifest"
	"github.com/alexei-led/archfit-app/internal/wire/policy"
	"github.com/alexei-led/archfit-app/internal/wire/settings"
)

func TestStartCustomerOpensPROne(t *testing.T) {
	f := newFixture(t)
	draft, err := f.svc.StartCustomer(context.Background(), f.repo, owners)
	if err != nil {
		t.Fatal(err)
	}

	if n := f.hub.count("Propose"); n != 1 {
		t.Errorf("%d Propose calls, want 1", n)
	}
	if n := f.hub.prCount(f.repo, ""); n != 1 {
		t.Errorf("%d pull requests, want 1", n)
	}
	want := []string{".archfit-app.yaml", ".github/CODEOWNERS"} // never a workflow: the App has no workflows permission
	if got := f.hub.paths(f.repo, onboardingBranch); !reflect.DeepEqual(got, want) {
		t.Errorf("files = %v, want %v", got, want)
	}
	pull, err := f.hub.PullByHead(context.Background(), f.repo.RepoRef, onboardingBranch)
	if err != nil || pull.BaseRef != f.repo.DefaultBranch {
		t.Errorf("pull = %+v, %v; want base %q", pull, err, f.repo.DefaultBranch)
	}

	raw, _ := f.hub.file(f.repo, onboardingBranch, settings.Path)
	s, err := settings.Parse(raw)
	if err != nil {
		t.Fatal(err)
	}
	wantSettings := settings.Settings{Version: 1, Owners: owners, Gate: settings.GateAdvisory, Workflow: workflowPath, DataMode: settings.DataCustomer}
	if !reflect.DeepEqual(s, wantSettings) {
		t.Errorf("settings = %+v, want %+v", s, wantSettings)
	}

	// The owner adds the workflow to the onboarding branch; Progress hands out the file and where it goes.
	wf := mustProgress(t, f.svc, f.repo).Workflow
	wantWorkflow, _ := renderWorkflow(manifest.Current(), f.repo.DefaultBranch, testBaseURL)
	if wf == nil || wf.Branch != onboardingBranch || wf.Path != workflowPath || !bytes.Equal(wf.Content, wantWorkflow) {
		t.Errorf("Workflow = %+v; want the template rendered with manifest.Current() for %s on %s", wf, workflowPath, onboardingBranch)
	}

	if got, _ := (fakeProposals{f.hub}).Get(context.Background(), f.repo.RepoRef, onboardingBranch); got != draft {
		t.Errorf("draft = %+v, want the proposal's %+v", draft, got)
	}
	if draft.State != proposal.DraftOpen {
		t.Errorf("draft state = %q", draft.State)
	}

	body := f.hub.body(f.repo, onboardingBranch)
	for _, want := range []string{
		"/.archfit.yaml", "/.archfit-baseline.json", "/.archfit-labels.yaml", "/.archfit-app.yaml", "/" + workflowPath,
		"exact head SHA", "ruleset", "bypass", "never reported as success", "automatic deletion of head branches",
		"discover", "baseline_digest", "labels_digest", "never writes `.github/workflows/`",
	} {
		if !strings.Contains(body, want) {
			t.Errorf("PR body does not mention %q", want)
		}
	}
}

func TestDiscoveryUploadOpensPRTwo(t *testing.T) {
	f := newFixture(t)
	f.onboard(f.repo, settings.GateAdvisory)
	f.receiver.upload = discoveryUpload(f.repo, "workflow_dispatch", []byte(validPolicy))

	res := f.post(t)
	if res.status != 200 || res.Number != 1 || !strings.HasSuffix(res.URL, "/pull/1") {
		t.Fatalf("response = %d %s", res.status, res.raw)
	}
	if !reflect.DeepEqual(f.receiver.kinds, []envelope.Kind{envelope.KindDiscovery}) {
		t.Errorf("Receive kinds = %v", f.receiver.kinds)
	}
	if n := f.hub.count("Propose"); n != 1 {
		t.Errorf("%d Propose calls, want 1", n)
	}
	if got := f.hub.paths(f.repo, policyBranch); !reflect.DeepEqual(got, []string{policy.PolicyPath}) {
		t.Errorf("files = %v, want only the policy", got)
	}
	if got, _ := f.hub.file(f.repo, policyBranch, policy.PolicyPath); string(got) != validPolicy {
		t.Errorf("policy bytes differ from the payload:\n%s", got)
	}
	body := f.hub.body(f.repo, policyBranch)
	for _, want := range []string{"9001", f.receiver.upload.Digest, "not measurement evidence"} {
		if !strings.Contains(body, want) {
			t.Errorf("PR body does not mention %q:\n%s", want, body)
		}
	}
	if strings.Contains(strings.ToLower(body), "verified") {
		t.Errorf("PR body claims verification:\n%s", body)
	}
	if n := f.hub.forbiddenCalls(); n != 0 {
		t.Errorf("%d check or Actions write calls", n)
	}
}

func TestDiscoveryStatusMapping(t *testing.T) {
	big := paddedPolicy(1<<20 + 1)
	tests := []struct {
		name       string
		receiveErr error
		mutate     func(f *fixture, u *gate.Upload)
		status     int
		code       string
		proposes   int
	}{
		{name: "rejection 401", receiveErr: &gate.Rejection{Status: 401, Code: "token_invalid"}, status: 401, code: "token_invalid"},
		{name: "rejection 403", receiveErr: &gate.Rejection{Status: 403, Code: "not_installed"}, status: 403, code: "not_installed"},
		{name: "rejection 409", receiveErr: &gate.Rejection{Status: 409, Code: "stale"}, status: 409, code: "stale"},
		{name: "rejection 413", receiveErr: &gate.Rejection{Status: 413, Code: "too_large"}, status: 413, code: "too_large"},
		{name: "rate limited", receiveErr: api.ErrRateLimited, status: 503, code: "rate_limited"},
		{name: "generic error", receiveErr: errors.New("boom"), status: 500, code: "internal"},
		{name: "envelope mismatch", mutate: func(_ *fixture, u *gate.Upload) { u.Problems = []gate.Problem{gate.ProblemEnvelopeMismatch} }, status: 400, code: "envelope_mismatch"},
		{name: "pull_request discovery", mutate: func(_ *fixture, u *gate.Upload) {
			u.Binding.Event = "pull_request"
			u.Problems = []gate.Problem{gate.ProblemUnsupportedEvent, gate.ProblemFork}
		}, status: 400, code: "unsupported_event"},
		{name: "fork", mutate: func(_ *fixture, u *gate.Upload) { u.Problems = []gate.Problem{gate.ProblemFork} }, status: 400, code: "fork"},
		{name: "report invalid", mutate: func(_ *fixture, u *gate.Upload) { u.Problems = []gate.Problem{gate.ProblemReportInvalid} }, status: 400, code: "report_invalid"},
		{name: "non-default branch", mutate: func(_ *fixture, u *gate.Upload) { u.Binding.BaseRef = "feature" }, status: 403, code: "not_default_branch"},
		{name: "settings missing", mutate: func(f *fixture, _ *gate.Upload) {
			f.hub.put(f.repo, "main", settings.Path, []byte("owners: nobody knows\n"))
		}, status: 403, code: "settings_missing"},
		{name: "settings read rate limited", mutate: func(f *fixture, _ *gate.Upload) { f.hub.failOn("File", api.ErrRateLimited) }, status: 503, code: "rate_limited"},
		{name: "other workflow path", mutate: func(_ *fixture, u *gate.Upload) { u.Binding.WorkflowPath = ".github/workflows/ci.yaml" }, status: 403, code: "workflow_not_approved"},
		{name: "oversized", mutate: func(_ *fixture, u *gate.Upload) { u.Payload = big }, status: 413, code: "discovery_too_large"},
		{name: "invalid draft", mutate: func(_ *fixture, u *gate.Upload) { u.Payload = []byte(waiverPolicy) }, status: 400, code: "discovery_invalid"},
		{name: "Propose rate limited", mutate: func(f *fixture, _ *gate.Upload) { f.hub.failOn("Propose", api.ErrRateLimited) }, status: 503, code: "rate_limited", proposes: 1},
		{name: "Propose fails", mutate: func(f *fixture, _ *gate.Upload) { f.hub.failOn("Propose", errors.New("boom")) }, status: 500, code: "internal", proposes: 1},
	}
	permanent := map[int]bool{400: true, 401: true, 403: true, 409: true, 413: true}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			f := newFixture(t)
			f.onboard(f.repo, settings.GateAdvisory)
			f.receiver.upload = discoveryUpload(f.repo, "workflow_dispatch", []byte(validPolicy))
			f.receiver.err = tc.receiveErr
			if tc.mutate != nil {
				tc.mutate(f, &f.receiver.upload)
			}
			res := f.post(t)
			if res.status != tc.status || res.Error != tc.code {
				t.Errorf("got %d %q, want %d %q", res.status, res.Error, tc.status, tc.code)
			}
			if !permanent[res.status] && res.status != 503 && res.status != 500 {
				t.Errorf("status %d is outside the producer's sets", res.status)
			}
			if n := f.hub.count("Propose"); n != tc.proposes {
				t.Errorf("%d Propose calls, want %d", n, tc.proposes)
			}
			if n := f.hub.prCount(f.repo, ""); n != 0 {
				t.Errorf("%d pull requests opened", n)
			}
		})
	}

	t.Run("settings absent", func(t *testing.T) {
		f := newFixture(t)
		f.receiver.upload = discoveryUpload(f.repo, "push", []byte(validPolicy))
		if res := f.post(t); res.status != 403 || res.Error != "settings_missing" {
			t.Errorf("got %d %q", res.status, res.Error)
		}
	})
}

func TestStartManagedHappyPath(t *testing.T) {
	f := newFixture(t)
	release := make(chan struct{})
	f.sandbox.fn = func(context.Context, DiscoveryJob) (DiscoveryResult, error) {
		<-release
		return DiscoveryResult{DraftPolicy: []byte(validPolicy), Notes: []string{"12 modules found", "2 layers proposed"}}, nil
	}

	draft, err := f.svc.StartManaged(context.Background(), f.repo, owners)
	if err != nil {
		t.Fatal(err)
	}
	job := receive(t, f.sandbox.started)

	// While the job runs: PR 1 with managed settings and a running body.
	if draft.Number != 1 || draft.State != proposal.DraftOpen {
		t.Errorf("draft = %+v", draft)
	}
	raw, _ := f.hub.file(f.repo, onboardingBranch, settings.Path)
	if s, err := settings.Parse(raw); err != nil || s.DataMode != settings.DataManaged || s.Gate != settings.GateAdvisory {
		t.Errorf("settings = %+v, %v", s, err)
	}
	body := f.hub.body(f.repo, onboardingBranch)
	if !strings.Contains(body, "Managed discovery running since 2026-09-21T10:00:00Z") ||
		!strings.Contains(body, "<!-- archfit:managed state=running started=2026-09-21T10:00:00Z -->") {
		t.Errorf("running body:\n%s", body)
	}
	wantStage(t, mustProgress(t, f.svc, f.repo), StageInstalled, "Managed discovery running since 2026-09-21T10:00:00Z")

	wantJob := DiscoveryJob{
		CloneURL:    "https://github.com/acme/shop.git",
		Token:       "token-for-acme/shop",
		Ref:         "main",
		ImageDigest: manifest.Current().ImageDigest,
		Timeout:     10 * time.Minute,
	}
	if job != wantJob {
		t.Errorf("job = %+v, want %+v", job, wantJob)
	}
	if got := f.tokens.requested(); !reflect.DeepEqual(got, []tokenCall{{7, "acme/shop"}}) || f.tokens.other != 0 {
		t.Errorf("token calls = %v, other = %d", got, f.tokens.other)
	}

	close(release)
	f.svc.jobs.Wait()

	if got, ok := f.hub.file(f.repo, onboardingBranch, policy.PolicyPath); !ok || string(got) != validPolicy {
		t.Errorf("policy on the onboarding branch = %q, %v", got, ok)
	}
	if n := f.hub.prCount(f.repo, ""); n != 1 {
		t.Errorf("%d pull requests, want 1", n)
	}
	body = f.hub.body(f.repo, onboardingBranch)
	for _, want := range []string{"<!-- archfit:managed state=complete -->", "discovery complete", "source_processing", "2026-09-21T10:00:00Z", "12 modules found", "2 layers proposed"} {
		if !strings.Contains(body, want) {
			t.Errorf("complete body does not contain %q:\n%s", want, body)
		}
	}
	if strings.Contains(body, "state=running") {
		t.Errorf("complete body still carries the running marker:\n%s", body)
	}
	p := mustProgress(t, f.svc, f.repo)
	wantStage(t, p, StageInstalled, "Review and merge PR #1")
	if p.OpenDraft == nil || p.OpenDraft.Number != 1 {
		t.Errorf("OpenDraft = %+v", p.OpenDraft)
	}
}

func TestNilSandboxIsNotAvailable(t *testing.T) {
	for _, tc := range []struct {
		name   string
		mutate func(d *Deps)
	}{
		{"nil Sandbox", func(d *Deps) { d.Sandbox = nil }},
		{"nil Tokens", func(d *Deps) { d.Tokens = nil }},
	} {
		t.Run(tc.name, func(t *testing.T) {
			f := newFixture(t)
			d := Deps{Tokens: f.tokens, Proposals: fakeProposals{f.hub}, Consents: f.consents, Sandbox: f.sandbox, Now: f.clock.now}
			tc.mutate(&d)
			svc := newService(d, roles{repos: f.hub, actions: f.hub, checks: f.hub, pulls: f.hub, rules: f.hub})

			_, err := svc.StartManaged(context.Background(), f.repo, owners)
			if !errors.Is(err, ErrNoSandbox) {
				t.Errorf("err = %v, want ErrNoSandbox", err)
			}
			if f.hub.count("Propose") != 0 || f.hub.prCount(f.repo, "") != 0 {
				t.Error("something was opened")
			}
			if len(f.tokens.requested()) != 0 || len(f.sandbox.calls()) != 0 {
				t.Error("a token or a sandbox job was requested")
			}
		})
	}

	t.Run("New wires the default", func(t *testing.T) {
		f := newFixture(t)
		o := New(Deps{Proposals: fakeProposals{f.hub}, Consents: f.consents, Now: f.clock.now})
		if _, err := o.StartManaged(context.Background(), f.repo, owners); !errors.Is(err, ErrNoSandbox) {
			t.Errorf("err = %v, want ErrNoSandbox", err)
		}
	})
}

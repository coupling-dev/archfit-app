package onboarding

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/alexei-led/archfit-app/internal/control/account"
	"github.com/alexei-led/archfit-app/internal/gate"
	"github.com/alexei-led/archfit-app/internal/github/api"
	"github.com/alexei-led/archfit-app/internal/wire/policy"
	"github.com/alexei-led/archfit-app/internal/wire/settings"
)

func TestNoConsentNoJob(t *testing.T) {
	f := newFixture(t)
	ctx := context.Background()

	// Has is false for "no consent" and for a failing Store alike: the contract folds both into false.
	f.consents.set(false)
	for range 2 {
		if _, err := f.svc.StartManaged(ctx, f.repo, owners); !errors.Is(err, account.ErrNoConsent) {
			t.Fatalf("err = %v, want ErrNoConsent", err)
		}
	}

	// Consent recorded, one job, then revoked: the next call reads consent again.
	f.consents.set(true)
	if _, err := f.svc.StartManaged(ctx, f.repo, owners); err != nil {
		t.Fatal(err)
	}
	f.svc.jobs.Wait()
	f.consents.set(false)
	if _, err := f.svc.StartManaged(ctx, f.repo, owners); !errors.Is(err, account.ErrNoConsent) {
		t.Fatalf("after revoke: err = %v, want ErrNoConsent", err)
	}

	if n := len(f.sandbox.calls()); n != 1 {
		t.Errorf("%d sandbox calls, want 1 (the consented one)", n)
	}
	if n := len(f.tokens.requested()); n != 1 {
		t.Errorf("%d tokens requested, want 1", n)
	}
	if n := f.hub.count("Propose"); n != 2 { // PR 1 and the policy of the consented job
		t.Errorf("%d Propose calls, want 2", n)
	}
	for _, k := range f.consents.asked {
		if k != account.ConsentSourceProcessing {
			t.Errorf("asked for consent %q", k)
		}
	}
	if f.consents.writes != 0 {
		t.Error("onboarding wrote a consent")
	}

	t.Run("refused calls touch nothing", func(t *testing.T) {
		f := newFixture(t)
		f.consents.set(false)
		if _, err := f.svc.StartManaged(ctx, f.repo, owners); !errors.Is(err, account.ErrNoConsent) {
			t.Fatalf("err = %v", err)
		}
		if len(f.hub.calls) != 0 || len(f.sandbox.calls()) != 0 || len(f.tokens.requested()) != 0 {
			t.Errorf("calls = %v", f.hub.calls)
		}
	})
}

func TestManagedFailureStates(t *testing.T) {
	const seeded = "func seededSourceLine() { secretSauce() }"
	tests := []struct {
		name  string
		setup func(f *fixture)
		code  string
	}{
		{"token fails", func(f *fixture) { f.tokens.err = errors.New("mint failed: " + seeded) }, "token_error"},
		{"sandbox not available", func(f *fixture) {
			f.sandbox.fn = func(context.Context, DiscoveryJob) (DiscoveryResult, error) { return DiscoveryResult{}, ErrNoSandbox }
		}, "sandbox_unavailable"},
		{"sandbox fails", func(f *fixture) {
			f.sandbox.fn = func(context.Context, DiscoveryJob) (DiscoveryResult, error) {
				return DiscoveryResult{}, errors.New("engine crashed at: " + seeded)
			}
		}, "sandbox_failed"},
		{"sandbox blocks past the timeout", func(f *fixture) {
			f.svc.jobTimeout = 20 * time.Millisecond
			f.sandbox.fn = func(ctx context.Context, _ DiscoveryJob) (DiscoveryResult, error) {
				<-ctx.Done() // returns only when the job context is cancelled
				return DiscoveryResult{}, ctx.Err()
			}
		}, "timeout"},
		{"invalid draft", func(f *fixture) {
			f.sandbox.fn = func(context.Context, DiscoveryJob) (DiscoveryResult, error) {
				return DiscoveryResult{DraftPolicy: []byte("notes: |\n\t" + seeded + "\n")}, nil
			}
		}, "invalid_draft"},
		{"foreign document as draft", func(f *fixture) {
			f.sandbox.fn = func(context.Context, DiscoveryJob) (DiscoveryResult, error) {
				return DiscoveryResult{DraftPolicy: []byte(`{"verdict": "blocked", "logs": "` + seeded + `"}` + "\n")}, nil
			}
		}, "invalid_draft"},
		{"Propose of the policy fails", func(f *fixture) {
			f.hub.failWhen(func(op, detail string) error {
				if op == "Propose" && strings.HasSuffix(detail, " "+policy.PolicyPath) {
					return errors.New("422: " + seeded)
				}
				return nil
			})
		}, "github_error"},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			logs := captureLogs(t)
			f := newFixture(t)
			tc.setup(f)
			ctx := context.Background()

			draft, err := f.svc.StartManaged(ctx, f.repo, owners)
			if err != nil {
				t.Fatal(err)
			}
			f.svc.jobs.Wait()

			body := f.hub.body(f.repo, onboardingBranch)
			if !strings.Contains(body, "<!-- archfit:managed state=failed code="+tc.code+" -->") {
				t.Errorf("body has no failed marker with %q:\n%s", tc.code, body)
			}
			if !strings.Contains(body, "retry from the Archfit UI") {
				t.Errorf("body does not say how to retry:\n%s", body)
			}
			wantStage(t, mustProgress(t, f.svc, f.repo), StageInstalled, "Managed discovery failed (`"+tc.code+"`) — retry")
			if _, ok := f.hub.file(f.repo, onboardingBranch, policy.PolicyPath); ok {
				t.Error("a policy was committed")
			}
			for _, text := range append(f.hub.bodies(), logs.String()) {
				if strings.Contains(text, "seeded") || strings.Contains(text, "secretSauce") {
					t.Errorf("seeded source text leaked into:\n%s", text)
				}
			}

			// Retry: the failure is resumable on the same pull request.
			f.tokens.err = nil
			f.hub.failWhen(nil)
			f.svc.jobTimeout = 10 * time.Minute
			f.sandbox.fn = func(context.Context, DiscoveryJob) (DiscoveryResult, error) {
				return DiscoveryResult{DraftPolicy: []byte(validPolicy)}, nil
			}
			again, err := f.svc.StartManaged(ctx, f.repo, owners)
			if err != nil {
				t.Fatal(err)
			}
			f.svc.jobs.Wait()
			if again.Number != draft.Number || f.hub.prCount(f.repo, "") != 1 {
				t.Errorf("retry used PR #%d, first was #%d; %d PRs", again.Number, draft.Number, f.hub.prCount(f.repo, ""))
			}
			if body := f.hub.body(f.repo, onboardingBranch); !strings.Contains(body, "state=complete") || strings.Contains(body, "state=failed") {
				t.Errorf("body after the retry:\n%s", body)
			}
		})
	}
}

func TestManagedJobLimits(t *testing.T) {
	f := newFixture(t)
	release := make(chan struct{})
	f.sandbox.fn = func(context.Context, DiscoveryJob) (DiscoveryResult, error) {
		<-release
		return DiscoveryResult{DraftPolicy: []byte(validPolicy)}, nil
	}
	second := f.newService() // another instance over the same GitHub
	repoB, repoC := testRepo("acme/billing"), testRepo("acme/search")

	// The request context is cancelled right after the call; the job must survive it.
	reqCtx, cancel := context.WithCancel(context.Background())
	first, err := f.svc.StartManaged(reqCtx, f.repo, owners)
	cancel()
	if err != nil {
		t.Fatal(err)
	}
	receive(t, f.sandbox.started)

	for _, o := range []Onboarding{f.svc, second} {
		again, err := o.StartManaged(context.Background(), f.repo, owners)
		if err != nil || again != first {
			t.Errorf("second start = %+v, %v; want the running draft %+v", again, err, first)
		}
	}

	if _, err := f.svc.StartManaged(context.Background(), repoB, owners); err != nil {
		t.Fatal(err)
	}
	receive(t, f.sandbox.started)

	_, err = f.svc.StartManaged(context.Background(), repoC, owners)
	if err == nil || !strings.Contains(err.Error(), "busy") {
		t.Errorf("third repository: err = %v, want busy", err)
	}
	if n := f.hub.prCount(repoC, ""); n != 0 {
		t.Errorf("busy start opened %d pull requests", n)
	}

	close(release)
	f.svc.jobs.Wait()
	second.jobs.Wait()

	perRepo := map[string]int{}
	for _, job := range f.sandbox.calls() {
		perRepo[job.CloneURL]++
	}
	if len(perRepo) != 2 || perRepo["https://github.com/acme/shop.git"] != 1 || perRepo["https://github.com/acme/billing.git"] != 1 {
		t.Errorf("sandbox calls = %v, want one per started repository", perRepo)
	}
	for _, r := range []api.Repo{f.repo, repoB} {
		if body := f.hub.body(r, onboardingBranch); !strings.Contains(body, "state=complete") {
			t.Errorf("%s: job did not complete:\n%s", r.FullName, body)
		}
	}

	// The slots are free again.
	if _, err := f.svc.StartManaged(context.Background(), repoC, owners); err != nil {
		t.Errorf("after the jobs ended: %v", err)
	}
	f.svc.jobs.Wait()
}

func TestAlreadyOnboardedRefusesStart(t *testing.T) {
	f := newFixture(t)
	f.onboard(f.repo, settings.GateAdvisory)
	ctx := context.Background()

	_, errCustomer := f.svc.StartCustomer(ctx, f.repo, owners)
	_, errManaged := f.svc.StartManaged(ctx, f.repo, owners)
	f.svc.jobs.Wait()
	for _, err := range []error{errCustomer, errManaged} {
		if err == nil || !strings.Contains(err.Error(), "already onboarded") {
			t.Errorf("err = %v, want already onboarded", err)
		}
	}
	if f.hub.count("Propose") != 0 || f.hub.count("SetBody") != 0 || len(f.sandbox.calls()) != 0 {
		t.Error("something was written or run")
	}
}

// handlerPaths drives every branch of the discovery handler.
func handlerPaths() map[string]func(f *fixture) {
	upload := func(f *fixture) *gate.Upload { return &f.receiver.upload }
	return map[string]func(f *fixture){
		"accepted":           func(*fixture) {},
		"rejection":          func(f *fixture) { f.receiver.err = &gate.Rejection{Status: 401, Code: "token_invalid"} },
		"rate limited":       func(f *fixture) { f.receiver.err = api.ErrRateLimited },
		"receiver error":     func(f *fixture) { f.receiver.err = errors.New("boom") },
		"problem":            func(f *fixture) { upload(f).Problems = []gate.Problem{gate.ProblemFork} },
		"non-default branch": func(f *fixture) { upload(f).Binding.BaseRef = "feature" },
		"settings missing":   func(f *fixture) { f.hub.failOn("File", api.ErrNotFound) },
		"settings error":     func(f *fixture) { f.hub.failOn("File", errors.New("boom")) },
		"workflow path":      func(f *fixture) { upload(f).Binding.WorkflowPath = ".github/workflows/ci.yaml" },
		"too large":          func(f *fixture) { upload(f).Payload = paddedPolicy(1<<20 + 1) },
		"invalid":            func(f *fixture) { upload(f).Payload = []byte(waiverPolicy) },
		"propose fails":      func(f *fixture) { f.hub.failOn("Propose", errors.New("boom")) },
	}
}

func TestDiscoveryNeverTouchesChecks(t *testing.T) {
	for name, setup := range handlerPaths() {
		t.Run(name, func(t *testing.T) {
			f := newFixture(t)
			f.onboard(f.repo, settings.GateAdvisory)
			f.receiver.upload = discoveryUpload(f.repo, "workflow_dispatch", []byte(validPolicy))
			setup(f)
			f.post(t)
			if n := f.hub.forbiddenCalls(); n != 0 {
				t.Errorf("%d calls to CreateCheckRun, UpdateCheckRun, Rerun or Dispatch", n)
			}
			for op := range f.hub.calls {
				switch op {
				case "File", "Propose":
				default:
					t.Errorf("unexpected GitHub call %s", op)
				}
			}
		})
	}
}

func TestHeadSettingsAreIgnored(t *testing.T) {
	f := newFixture(t)
	f.onboard(f.repo, settings.GateAdvisory)
	// The run's branch rewrote the settings to approve its own workflow.
	const rogue = ".github/workflows/rogue.yaml"
	f.hub.put(f.repo, "feature", settings.Path, renderSettings(settings.GateAdvisory, rogue))
	f.hub.put(f.repo, runHeadSHA, settings.Path, renderSettings(settings.GateAdvisory, rogue))
	f.receiver.upload = discoveryUpload(f.repo, "push", []byte(validPolicy))
	f.receiver.upload.Binding.WorkflowPath = rogue

	if res := f.post(t); res.status != 403 || res.Error != "workflow_not_approved" {
		t.Errorf("got %d %q, want 403 workflow_not_approved", res.status, res.Error)
	}
	if f.hub.count("Propose") != 0 {
		t.Error("a policy PR was proposed")
	}
}

func TestLogsAndBodiesCarryNoPayload(t *testing.T) {
	const (
		draftMark = "MARK-DRAFT-7f3a"
		noteMark  = "MARK-NOTE-91bc"
		badNote   = "MARK-BADNOTE-55d0"
		errMark   = "MARK-ERROR-c2e8"
	)
	logs := captureLogs(t)
	markedPolicy := validPolicy + "# " + draftMark + "\n"
	var responses []string

	// Customer flow: start, an accepted upload, a rejected draft, and failing dependencies.
	f := newFixture(t)
	if _, err := f.svc.StartCustomer(context.Background(), f.repo, owners); err != nil {
		t.Fatal(err)
	}
	f.hub.merge(f.repo, onboardingBranch)
	f.receiver.upload = discoveryUpload(f.repo, "workflow_dispatch", []byte(markedPolicy))
	responses = append(responses, f.post(t).raw)
	f.receiver.upload.Payload = []byte("bad: [" + draftMark + "\n")
	responses = append(responses, f.post(t).raw)
	f.receiver.err = errors.New("receiver: " + errMark)
	responses = append(responses, f.post(t).raw)
	f.receiver.err = nil
	f.receiver.upload.Payload = []byte(markedPolicy)
	f.hub.failOn("Propose", errors.New("propose: "+errMark))
	responses = append(responses, f.post(t).raw)

	// Managed flow: a marked draft, one accepted and two refused notes, then a failing sandbox.
	m := newFixture(t)
	m.sandbox.fn = func(context.Context, DiscoveryJob) (DiscoveryResult, error) {
		return DiscoveryResult{
			DraftPolicy: []byte(markedPolicy),
			Notes:       []string{noteMark, badNote + "\twith a tab", badNote + " <!-- archfit:managed state=failed code=timeout -->", badNote + strings.Repeat("x", 500)},
		}, nil
	}
	if _, err := m.svc.StartManaged(context.Background(), m.repo, owners); err != nil {
		t.Fatal(err)
	}
	m.svc.jobs.Wait()
	m.sandbox.fn = func(context.Context, DiscoveryJob) (DiscoveryResult, error) {
		return DiscoveryResult{}, errors.New("sandbox: " + errMark)
	}
	m.clock.advance(time.Hour)
	if _, err := m.svc.StartManaged(context.Background(), m.repo, owners); err != nil {
		t.Fatal(err)
	}
	m.svc.jobs.Wait()

	if logs.String() == "" {
		t.Fatal("nothing was logged; the capture does not work")
	}
	for _, text := range append(responses, logs.String()) {
		for _, mark := range []string{draftMark, noteMark, badNote, errMark} {
			if strings.Contains(text, mark) {
				t.Errorf("%s leaked into a log or response:\n%s", mark, text)
			}
		}
	}
	if strings.Contains(logs.String(), "token-for-") {
		t.Error("the sandbox token was logged")
	}
	for _, body := range append(f.hub.bodies(), m.hub.bodies()...) {
		for _, mark := range []string{draftMark, badNote, errMark} {
			if strings.Contains(body, mark) {
				t.Errorf("%s leaked into a PR body:\n%s", mark, body)
			}
		}
	}

	// The markers live only where they belong: the committed policy and the accepted note.
	if got, _ := f.hub.file(f.repo, policyBranch, policy.PolicyPath); !strings.Contains(string(got), draftMark) {
		t.Error("the accepted draft was not committed")
	}
	if got, _ := m.hub.file(m.repo, onboardingBranch, policy.PolicyPath); !strings.Contains(string(got), draftMark) {
		t.Error("the managed draft was not committed")
	}
}

func TestDiscoveryRejectsForeignDocuments(t *testing.T) {
	const seeded = "LEAK-7f3a"
	// An engine state report with a seeded source string, pretty-printed so every line passes the shape checks.
	report := `{
  "schema_version": "archfit.architecture-state.v1",
  "verdict": "blocked",
  "decision": {
    "hard_gates": "fail",
    "active_blockers": 1
  },
  "findings": [
    {
      "rule": "no_direct_b_dependency",
      "snippet": "func Pay(c Card) error { return ` + seeded + ` }"
    }
  ],
  "logs": "2026-09-21 build log ` + seeded + `"
}
`
	// The text of internal/wire/testdata/report/real-blocked-no-baseline.policy.yaml.
	realPolicy := `version: 2
modules:
  a:
    paths:
      - pkg/a/**
  b:
    paths:
      - pkg/b/**
    public:
      - pkg/b/b.go
    internal:
      - pkg/b/internal/**
rules:
  - id: no_direct_b_dependency
    type: forbidden_dependency
    from: pkg/a/**
    to: pkg/b
    gate: fail
`
	tests := []struct {
		name     string
		payload  string
		accepted bool
	}{
		{name: "engine state report", payload: report},
		{name: "one-line prose", payload: "note: the card check in " + seeded + " looks fine\n"},
		{name: "sequence", payload: "- version: 2\n- " + seeded + "\n"},
		{name: "mapping without version", payload: "modules:\n  core:\n    paths:\n      - " + seeded + "/**\n"},
		{name: "unknown top-level key", payload: validPolicy + "source:\n  - " + seeded + "\n"},
		{name: "real policy", payload: realPolicy, accepted: true},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			for line := range strings.SplitSeq(tc.payload, "\n") {
				if !cleanLine(line) {
					t.Fatalf("the row must pass the shape checks, line %q does not", line)
				}
			}
			logs := captureLogs(t)
			f := newFixture(t)
			f.onboard(f.repo, settings.GateAdvisory)
			f.receiver.upload = discoveryUpload(f.repo, "workflow_dispatch", []byte(tc.payload))
			res := f.post(t)

			if tc.accepted {
				if res.status != 200 || f.hub.count("Propose") != 1 {
					t.Fatalf("got %d %q with %d Propose calls, want 200 and one", res.status, res.Error, f.hub.count("Propose"))
				}
				if got, _ := f.hub.file(f.repo, policyBranch, policy.PolicyPath); string(got) != tc.payload {
					t.Errorf("committed policy:\n%s", got)
				}
				return
			}
			if res.status != 400 || res.Error != "discovery_invalid" {
				t.Errorf("got %d %q, want 400 discovery_invalid", res.status, res.Error)
			}
			if n := f.hub.count("Propose"); n != 0 {
				t.Errorf("%d Propose calls, want 0", n)
			}
			if n := f.hub.prCount(f.repo, ""); n != 0 {
				t.Errorf("%d pull requests opened", n)
			}
			if logs.String() == "" {
				t.Error("the refusal was not logged")
			}
			if strings.Contains(res.raw, seeded) || strings.Contains(logs.String(), seeded) {
				t.Errorf("%s leaked:\nresponse: %s\nlogs: %s", seeded, res.raw, logs.String())
			}
		})
	}
}

func TestProgressErrors(t *testing.T) {
	tests := []struct {
		name      string
		setup     func(f *fixture)
		wantErr   error
		wantStage Stage
	}{
		{"File rate limited", func(f *fixture) { f.hub.failOn("File", api.ErrRateLimited) }, api.ErrRateLimited, ""},
		{"LatestRun not found", func(f *fixture) { f.hub.failOn("LatestRun", api.ErrNotFound) }, nil, StageWorkflowMerged},
		{"LatestRun fails", func(f *fixture) { f.hub.failOn("LatestRun", api.ErrRateLimited) }, api.ErrRateLimited, ""},
		{"CheckRuns fails", func(f *fixture) { f.hub.failOn("CheckRuns", api.ErrRateLimited) }, api.ErrRateLimited, ""},
		{"Get fails", func(f *fixture) { f.hub.failOn("Get", api.ErrRateLimited) }, api.ErrRateLimited, ""},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			f := newFixture(t)
			f.onboard(f.repo, settings.GateAdvisory)
			f.report(f.repo)
			tc.setup(f)
			p, err := f.svc.Progress(context.Background(), f.repo)
			if !errors.Is(err, tc.wantErr) {
				t.Fatalf("err = %v, want %v", err, tc.wantErr)
			}
			if p.Stage != tc.wantStage {
				t.Errorf("stage = %q, want %q", p.Stage, tc.wantStage)
			}
		})
	}

	t.Run("PullByHead fails", func(t *testing.T) {
		f := newFixture(t)
		proposeOn(t, f, onboardingBranch)
		f.hub.failOn("PullByHead", api.ErrRateLimited)
		if p, err := f.svc.Progress(context.Background(), f.repo); !errors.Is(err, api.ErrRateLimited) || p.Stage != "" {
			t.Errorf("got %+v, %v", p, err)
		}
	})
}

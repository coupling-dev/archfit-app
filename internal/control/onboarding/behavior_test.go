package onboarding

import (
	"context"
	"errors"
	"go/parser"
	"go/token"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/alexei-led/archfit-app/internal/wire/policy"
	"github.com/alexei-led/archfit-app/internal/wire/settings"
)

func TestCustomerFlowEndToEnd(t *testing.T) {
	f := newFixture(t)
	r := f.repo
	ctx := context.Background()

	wantStage(t, mustProgress(t, f.svc, r), StageInstalled, "Start onboarding")

	draft, err := f.svc.StartCustomer(ctx, r, owners)
	if err != nil {
		t.Fatal(err)
	}
	p := mustProgress(t, f.svc, r)
	wantStage(t, p, StageInstalled, "Review and merge PR #1")
	if p.OpenDraft == nil || *p.OpenDraft != draft {
		t.Errorf("OpenDraft = %+v, want %+v", p.OpenDraft, draft)
	}

	f.hub.merge(r, onboardingBranch)
	p = mustProgress(t, f.svc, r)
	wantStage(t, p, StageWorkflowMerged, "Run discovery")
	if p.OpenDraft != nil {
		t.Errorf("OpenDraft = %+v after the merge", p.OpenDraft)
	}

	f.receiver.upload = discoveryUpload(r, "workflow_dispatch", []byte(validPolicy))
	res := f.post(t)
	if res.status != 200 || res.Number != 2 {
		t.Fatalf("upload = %d %s", res.status, res.raw)
	}
	p = mustProgress(t, f.svc, r)
	wantStage(t, p, StageWorkflowMerged, "Review and merge the policy PR #2")
	if p.OpenDraft == nil || p.OpenDraft.Number != 2 {
		t.Errorf("OpenDraft = %+v, want the policy PR", p.OpenDraft)
	}

	f.hub.merge(r, policyBranch)
	wantStage(t, mustProgress(t, f.svc, r), StagePolicyMerged, "Waiting for the first run")

	f.report(r)
	wantStage(t, mustProgress(t, f.svc, r), StageAdvisory, "Advisory feedback is active")

	// An owner switches the gate by PR; onboarding never writes "required" itself.
	f.hub.put(r, r.DefaultBranch, settings.Path, renderSettings(settings.GateRequired, workflowPath))
	wantStage(t, mustProgress(t, f.svc, r), StageRequired, "Required gate is active")

	if n := f.hub.prCount(r, ""); n != 2 {
		t.Errorf("%d pull requests, want exactly two", n)
	}
	if n := f.hub.forbiddenCalls(); n != 0 {
		t.Errorf("%d check or Actions write calls", n)
	}
}

func TestRetriesNeverDuplicatePRs(t *testing.T) {
	ctx := context.Background()

	t.Run("StartCustomer", func(t *testing.T) {
		f := newFixture(t)
		for i := range 5 {
			if _, err := f.svc.StartCustomer(ctx, f.repo, []string{"alice", "owner-" + strconv.Itoa(i)}); err != nil {
				t.Fatal(err)
			}
		}
		if n := f.hub.prCount(f.repo, ""); n != 1 {
			t.Errorf("%d pull requests, want 1", n)
		}
		raw, _ := f.hub.file(f.repo, onboardingBranch, settings.Path)
		if s, err := settings.Parse(raw); err != nil || s.Owners[1] != "owner-4" {
			t.Errorf("settings on the branch = %+v, %v; want the newest owners", s, err)
		}
	})

	t.Run("discovery upload", func(t *testing.T) {
		f := newFixture(t)
		f.onboard(f.repo, settings.GateAdvisory)
		for i := range 3 {
			f.receiver.upload = discoveryUpload(f.repo, "workflow_dispatch", []byte(validPolicy+"# attempt "+strconv.Itoa(i)+"\n"))
			if res := f.post(t); res.status != 200 || res.Number != 1 {
				t.Fatalf("upload %d = %d %s", i, res.status, res.raw)
			}
		}
		if n := f.hub.prCount(f.repo, ""); n != 1 {
			t.Errorf("%d pull requests, want 1", n)
		}
		if got, _ := f.hub.file(f.repo, policyBranch, policy.PolicyPath); !strings.HasSuffix(string(got), "# attempt 2\n") {
			t.Errorf("branch content is not the newest upload:\n%s", got)
		}
	})

	t.Run("StartManaged after a failure", func(t *testing.T) {
		f := newFixture(t)
		f.sandbox.fn = func(context.Context, DiscoveryJob) (DiscoveryResult, error) {
			return DiscoveryResult{}, errors.New("boom")
		}
		first, err := f.svc.StartManaged(ctx, f.repo, owners)
		if err != nil {
			t.Fatal(err)
		}
		f.svc.jobs.Wait()
		f.sandbox.fn = func(context.Context, DiscoveryJob) (DiscoveryResult, error) {
			return DiscoveryResult{DraftPolicy: []byte(validPolicy + "# retry\n")}, nil
		}
		second, err := f.svc.StartManaged(ctx, f.repo, owners)
		if err != nil {
			t.Fatal(err)
		}
		f.svc.jobs.Wait()
		if first.Number != second.Number || f.hub.prCount(f.repo, "") != 1 {
			t.Errorf("PRs #%d and #%d, %d in total; want one", first.Number, second.Number, f.hub.prCount(f.repo, ""))
		}
		if got, _ := f.hub.file(f.repo, onboardingBranch, policy.PolicyPath); !strings.HasSuffix(string(got), "# retry\n") {
			t.Errorf("policy on the branch:\n%s", got)
		}
	})
}

func TestRestrictedOrganizationFallback(t *testing.T) {
	f := newFixture(t)
	if _, err := f.svc.StartCustomer(context.Background(), f.repo, owners); err != nil {
		t.Fatal(err)
	}
	f.hub.merge(f.repo, onboardingBranch)

	// workflow_dispatch is not available: the CLI runs in the existing CI on a default-branch push.
	f.receiver.upload = discoveryUpload(f.repo, "push", []byte(validPolicy))
	res := f.post(t)
	if res.status != 200 || res.Number != 2 {
		t.Fatalf("upload = %d %s", res.status, res.raw)
	}
	if got, _ := f.hub.file(f.repo, policyBranch, policy.PolicyPath); string(got) != validPolicy {
		t.Errorf("policy on the branch:\n%s", got)
	}

	// The same checks still apply to a push upload.
	f.receiver.upload.Binding.BaseRef = "feature"
	if res := f.post(t); res.status != 403 || res.Error != "not_default_branch" {
		t.Errorf("push from another branch = %d %q", res.status, res.Error)
	}

	// No elevated permission: the workflow asks for the same two scopes and nothing was dispatched or minted.
	workflow, _ := f.hub.file(f.repo, f.repo.DefaultBranch, workflowPath)
	if !strings.Contains(string(workflow), "permissions:\n  contents: read\n  id-token: write\n\n") {
		t.Errorf("workflow permissions changed:\n%s", workflow)
	}
	if f.hub.forbiddenCalls() != 0 || len(f.tokens.requested()) != 0 || f.tokens.other != 0 {
		t.Error("a dispatch, check write or token was requested")
	}
}

func TestManagedRestartShowsStalled(t *testing.T) {
	f := newFixture(t)
	release := make(chan struct{})
	f.sandbox.fn = func(context.Context, DiscoveryJob) (DiscoveryResult, error) {
		<-release // the first job never returns while the test observes it
		return DiscoveryResult{}, errors.New("lost with its process")
	}
	ctx := context.Background()

	first, err := f.svc.StartManaged(ctx, f.repo, owners)
	if err != nil {
		t.Fatal(err)
	}
	receive(t, f.sandbox.started)

	restarted := f.newService() // the process restarted: no in-memory job state survives

	f.clock.advance(5 * time.Minute)
	wantStage(t, mustProgress(t, restarted, f.repo), StageInstalled, "Managed discovery running since 2026-09-21T10:00:00Z")
	if again, err := restarted.StartManaged(ctx, f.repo, owners); err != nil || again != first {
		t.Errorf("start at +5 min = %+v, %v; want the running draft", again, err)
	}
	if n := len(f.sandbox.calls()); n != 1 {
		t.Errorf("%d sandbox calls at +5 min, want 1", n)
	}

	f.clock.advance(7 * time.Minute)
	wantStage(t, mustProgress(t, restarted, f.repo), StageInstalled, "Managed discovery stalled — retry")

	retried := make(chan struct{})
	f.sandbox.mu.Lock()
	f.sandbox.fn = func(context.Context, DiscoveryJob) (DiscoveryResult, error) {
		<-retried
		return DiscoveryResult{DraftPolicy: []byte(validPolicy)}, nil
	}
	f.sandbox.mu.Unlock()
	again, err := restarted.StartManaged(ctx, f.repo, owners)
	if err != nil {
		t.Fatal(err)
	}
	receive(t, f.sandbox.started)
	if again.Number != first.Number || f.hub.prCount(f.repo, "") != 1 {
		t.Errorf("retry used PR #%d, first was #%d", again.Number, first.Number)
	}
	if body := f.hub.body(f.repo, onboardingBranch); !strings.Contains(body, "state=running started=2026-09-21T10:12:00Z") {
		t.Errorf("the marker was not rewritten:\n%s", body)
	}
	close(retried)
	restarted.jobs.Wait()
	if n := len(f.sandbox.calls()); n != 2 {
		t.Errorf("%d sandbox calls, want 2", n)
	}
	wantStage(t, mustProgress(t, restarted, f.repo), StageInstalled, "Review and merge PR #1")

	// Only now let the goroutine of the "dead" process go, so the test leaves nothing behind.
	close(release)
	f.svc.jobs.Wait()
}

func TestOnboardingFailureLeavesGateAlone(t *testing.T) {
	boom := errors.New("boom")
	ctx := context.Background()
	ops := []string{"File", "LatestRun", "CheckRuns", "PullByHead", "Get", "Propose", "SetBody"}

	for _, op := range ops {
		t.Run(op, func(t *testing.T) {
			// Fresh repository: both start methods and Progress.
			f := newFixture(t)
			proposeOn(t, f, onboardingBranch) // an open PR, so PullByHead is reached
			f.hub.failOn(op, boom)
			_, errProgress := f.svc.Progress(ctx, f.repo)
			_, errCustomer := f.svc.StartCustomer(ctx, f.repo, owners)
			_, errManaged := f.svc.StartManaged(ctx, f.repo, owners)
			f.svc.jobs.Wait()

			// Onboarded repository: Progress and the upload.
			g := newFixture(t)
			g.onboard(g.repo, settings.GateAdvisory)
			g.report(g.repo)
			g.receiver.upload = discoveryUpload(g.repo, "push", []byte(validPolicy))
			g.hub.failOn(op, boom)
			_, errOnboarded := g.svc.Progress(ctx, g.repo)
			res := g.post(t)

			surfaced := map[string]bool{
				"File":       errProgress != nil && errCustomer != nil && errManaged != nil && errOnboarded != nil && res.status == 500,
				"LatestRun":  errOnboarded != nil,
				"CheckRuns":  errOnboarded != nil,
				"PullByHead": errProgress != nil && errCustomer != nil && errManaged != nil,
				"Get":        errProgress != nil && errCustomer != nil && errManaged != nil && errOnboarded != nil,
				"Propose":    errCustomer != nil && res.status == 500,
				"SetBody":    errManaged != nil,
			}[op]
			if !surfaced {
				t.Errorf("failure did not surface: progress=%v customer=%v managed=%v onboarded=%v upload=%d",
					errProgress, errCustomer, errManaged, errOnboarded, res.status)
			}
			for _, h := range []*hub{f.hub, g.hub} {
				if n := h.forbiddenCalls(); n != 0 {
					t.Errorf("%d check or Actions write calls", n)
				}
			}
		})
	}

	t.Run("other dependencies", func(t *testing.T) {
		f := newFixture(t)
		f.tokens.err = boom
		f.receiver.err = boom
		if _, err := f.svc.StartManaged(ctx, f.repo, owners); err != nil {
			t.Fatal(err)
		}
		f.svc.jobs.Wait()
		if res := f.post(t); res.status != 500 {
			t.Errorf("upload = %d", res.status)
		}
		if n := f.hub.forbiddenCalls(); n != 0 {
			t.Errorf("%d check or Actions write calls", n)
		}
	})

	// No comment role and no Store: the module cannot write either, by construction.
	t.Run("imports", func(t *testing.T) {
		const module = "github.com/alexei-led/archfit-app/internal/"
		allowed := map[string]bool{
			"gate": true, "control/proposal": true, "control/account": true, "github/api": true, "github/auth": true,
			"wire/envelope": true, "wire/settings": true, "wire/policy": true, "wire/manifest": true,
		}
		files, err := filepath.Glob("*.go")
		if err != nil {
			t.Fatal(err)
		}
		for _, name := range files {
			if strings.HasSuffix(name, "_test.go") {
				continue
			}
			src, err := os.ReadFile(name)
			if err != nil {
				t.Fatal(err)
			}
			parsed, err := parser.ParseFile(token.NewFileSet(), name, src, parser.ImportsOnly)
			if err != nil {
				t.Fatal(err)
			}
			for _, imp := range parsed.Imports {
				path, _ := strconv.Unquote(imp.Path.Value)
				switch rest, internal := strings.CutPrefix(path, module); {
				case internal && !allowed[rest]:
					t.Errorf("%s imports %s, which is not an integration of this module", name, path)
				case !internal && strings.Contains(path, "."):
					t.Errorf("%s imports %s; this module is standard library only", name, path)
				}
			}
		}
	})
}

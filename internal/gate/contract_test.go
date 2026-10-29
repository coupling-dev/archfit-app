package gate

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/alexei-led/archfit-app/internal/gate/feedback"
	"github.com/alexei-led/archfit-app/internal/gate/verdict"
	"github.com/alexei-led/archfit-app/internal/github/api"
	"github.com/alexei-led/archfit-app/internal/github/oidc"
	"github.com/alexei-led/archfit-app/internal/github/webhook"
	"github.com/alexei-led/archfit-app/internal/wire/envelope"
	"github.com/alexei-led/archfit-app/internal/wire/policy"
	"github.com/alexei-led/archfit-app/internal/wire/settings"
)

func TestUploadPublishesApprovedProducer(t *testing.T) {
	w := newWorld(t)
	w.setFile(w.shop, testBranch, policy.BaselinePath, []byte(`{"baseline":1}`))
	w.setFile(w.shop, testBranch, policy.LabelsPath, []byte("labels: {}\n"))
	p := w.openPull(w.shop, 7)
	w.handle(w.event(webhook.PullOpened, p))
	pending := w.wantGate(p, runInProgress, "")

	s := w.spec(p, w.report(w.shop, fixtureHealthy, gateReference("comparable")))
	s.env.BaselineDigest = w.digestOf(w.shop, testBranch, policy.BaselinePath)
	s.env.LabelsDigest = w.digestOf(w.shop, testBranch, policy.LabelsPath)
	w.wantAnswer(w.send(s), 200, "success", "healthy")

	done := w.wantGate(p, runCompleted, "success")
	if done.ID != pending.ID {
		t.Errorf("gate check %d was not completed in place (pending was %d)", done.ID, pending.ID)
	}
	if state, ok := w.check(p, p.head, stateCheckName); !ok || state.Conclusion != "success" {
		t.Errorf("%s = %+v, found %v", stateCheckName, state.Conclusion, ok)
	}
	if n := len(w.comments(p)); n != 1 {
		t.Errorf("%d comments, want 1", n)
	}
	want := feedback.Receipt{
		RunID: p.runID, RunAttempt: 1, ReportDigest: s.env.PayloadDigest, BaseSHA: s.env.BaseSHA,
		PolicyDigest:   w.digestOf(w.shop, testBranch, policy.PolicyPath),
		BaselineDigest: s.env.BaselineDigest, LabelsDigest: s.env.LabelsDigest,
		Conclusion: verdict.Success, Reason: verdict.ReasonHealthy,
	}
	if got := w.receipt(p); got != want {
		t.Errorf("receipt =\n %+v, want\n %+v", got, want)
	}
}

func TestUploadNegativeProvenance(t *testing.T) {
	for _, row := range negativeRows() {
		t.Run(row.name, func(t *testing.T) {
			w := newWorld(t)
			p := w.openPull(w.shop, 7)
			s := w.spec(p, w.report(w.shop, fixtureHealthy))
			row.damage(w, p, &s)

			a := w.send(s)
			if a.status != row.status || a.Error == "" || (row.code != "" && a.Error != row.code) {
				t.Errorf("answer = %d %s, want %d %s", a.status, a.raw, row.status, row.code)
			}
			if a.Conclusion != "" {
				t.Errorf("a rejection carries a conclusion: %s", a.raw)
			}
			if n := w.gh.writes(); n != 0 {
				t.Errorf("%d checks or comments written for a rejection", n)
			}
		})
	}
}

func TestUploadProblemsArePublished(t *testing.T) {
	for _, row := range problemRows() {
		t.Run(row.name, func(t *testing.T) {
			w := newWorld(t)
			p := w.openPull(w.shop, 7)
			w.handle(w.event(webhook.PullOpened, p))

			w.wantAnswer(w.send(row.spec(w, p)), 200, "action_required", row.reason)
			w.noSuccess()
			if _, ok := w.check(p, p.head, stateCheckName); !ok {
				t.Errorf("no %s check was published", stateCheckName)
			}
		})
	}
}

func TestHandleEventReactions(t *testing.T) {
	// ownerMissing leaves an owner_approval_missing receipt on a PR that edits the policy.
	ownerMissing := func(w *world) *pull {
		p := w.openPull(w.shop, 7)
		w.changes(p, policy.PolicyPath)
		w.setFile(w.shop, p.head, policy.PolicyPath, []byte(basePolicy+"# edited\n"))
		w.handle(w.event(webhook.PullOpened, p))
		a := w.upload(p, fixtureHealthy, configHash(w.digestOf(w.shop, p.head, policy.PolicyPath)))
		w.wantAnswer(a, 200, "action_required", "owner_approval_missing")
		return p
	}

	t.Run("PullOpened in required mode", func(t *testing.T) {
		w := newWorld(t)
		p := w.openPull(w.shop, 7)
		w.handle(w.event(webhook.PullOpened, p))
		w.wantGate(p, runInProgress, "")
		if n := w.gh.writes(); n != 1 {
			t.Errorf("%d writes, want only the pending check", n)
		}
	})
	t.Run("PullOpened in advisory mode", func(t *testing.T) {
		w := newWorld(t)
		w.setSettings(w.shop, settings.GateAdvisory)
		w.handle(w.event(webhook.PullOpened, w.openPull(w.shop, 7)))
		if n := w.gh.writes(); n != 0 {
			t.Errorf("%d writes, want none", n)
		}
	})
	t.Run("PullOpened without settings", func(t *testing.T) {
		w := newWorld(t)
		w.removeFile(w.shop, testBranch, settings.Path)
		w.handle(w.event(webhook.PullOpened, w.openPull(w.shop, 7)))
		if n := w.gh.writes(); n != 0 {
			t.Errorf("%d writes, want none", n)
		}
	})
	t.Run("PullOpened from a fork", func(t *testing.T) {
		w := newWorld(t)
		p := w.openPull(w.shop, 7)
		w.editPull(p, func(pl *api.Pull) { pl.HeadRepo = "mallory/shop" })
		w.handle(w.event(webhook.PullOpened, p))
		w.wantGate(p, runCompleted, "action_required")
		want := feedback.Receipt{
			PolicyDigest: w.digestOf(w.shop, testBranch, policy.PolicyPath),
			Conclusion:   verdict.ActionRequired, Reason: verdict.ReasonForkUnsupported,
		}
		if got := w.receipt(p); got != want {
			t.Errorf("receipt = %+v, want %+v", got, want)
		}
	})
	t.Run("PullSynchronized", func(t *testing.T) {
		w := newWorld(t)
		p := w.openPull(w.shop, 7)
		w.handle(w.event(webhook.PullOpened, p))
		old := p.head
		w.push(p)
		w.handle(w.event(webhook.PullSynchronized, p))
		w.wantGate(p, runInProgress, "")
		if c, ok := w.check(p, old, gateCheckName); !ok || c.Status != runInProgress {
			t.Errorf("the old head's check changed: %+v", c)
		}
	})
	t.Run("ReviewChanged with the receipt and a granting review", func(t *testing.T) {
		w := newWorld(t)
		p := ownerMissing(w)
		w.review(p, ownerLogin, "APPROVED", p.head)
		w.handle(w.event(webhook.ReviewChanged, p))
		if len(w.gh.reruns) != 1 || w.gh.reruns[0] != p.runID {
			t.Errorf("reruns = %v, want [%d]", w.gh.reruns, p.runID)
		}
		w.wantGate(p, runCompleted, "action_required")
	})
	t.Run("ReviewChanged with the receipt and a review that grants nothing", func(t *testing.T) {
		w := newWorld(t)
		p := ownerMissing(w)
		w.review(p, "bob", "APPROVED", p.head)
		w.review(p, ownerLogin, "COMMENTED", p.head)
		w.handle(w.event(webhook.ReviewChanged, p))
		if len(w.gh.reruns) != 0 {
			t.Errorf("reruns = %v, want none", w.gh.reruns)
		}
	})
	t.Run("ReviewChanged without an owner_approval_missing receipt", func(t *testing.T) {
		w := newWorld(t)
		p := w.openPull(w.shop, 7)
		w.wantAnswer(w.upload(p, fixtureBlocked), 200, "failure", "blocking_findings")
		w.review(p, ownerLogin, "APPROVED", p.head)
		before := w.gh.writes()
		w.handle(w.event(webhook.ReviewChanged, p))
		if len(w.gh.reruns) != 0 || w.gh.writes() != before {
			t.Errorf("reruns = %v, writes %d → %d", w.gh.reruns, before, w.gh.writes())
		}
	})

	nothing := map[string]func(*world, *pull) webhook.Event{
		"PullClosed": func(w *world, p *pull) webhook.Event { return w.event(webhook.PullClosed, p) },
		"InstallationAdded": func(*world, *pull) webhook.Event {
			return webhook.Event{Kind: webhook.InstallationAdded, InstallationID: shopInstallation}
		},
		"InstallationRemoved": func(*world, *pull) webhook.Event {
			return webhook.Event{Kind: webhook.InstallationRemoved, InstallationID: shopInstallation}
		},
		"ReposRemoved": func(*world, *pull) webhook.Event {
			return webhook.Event{Kind: webhook.ReposRemoved, InstallationID: shopInstallation}
		},
		"Ignored":                       func(*world, *pull) webhook.Event { return webhook.Event{Kind: webhook.Ignored} },
		"a kind the gate does not know": func(*world, *pull) webhook.Event { return webhook.Event{Kind: "brand_new"} },
	}
	for name, event := range nothing {
		t.Run(name+" does nothing", func(t *testing.T) {
			w := newWorld(t)
			p := w.openPull(w.shop, 7)
			w.handle(event(w, p))
			if len(w.gh.calls) != 0 {
				t.Errorf("GitHub calls = %+v, want none", w.gh.calls)
			}
		})
	}

	t.Run("the same delivery twice", func(t *testing.T) {
		w := newWorld(t)
		p := w.openPull(w.shop, 7)
		w.handle(w.event(webhook.PullOpened, p))
		before := w.gh.writes()
		w.handle(w.event(webhook.PullOpened, p))
		if w.gh.writes() != before {
			t.Errorf("redelivered PullOpened wrote %d more times", w.gh.writes()-before)
		}

		fork := w.openPull(w.shop, 8)
		w.editPull(fork, func(pl *api.Pull) { pl.HeadRepo = "" }) // the fork was deleted
		w.handle(w.event(webhook.PullOpened, fork))
		first := w.wantGate(fork, runCompleted, "action_required")
		comment := w.comments(fork)[0].Body
		w.handle(w.event(webhook.PullOpened, fork))
		again := w.wantGate(fork, runCompleted, "action_required")
		if again.ID != first.ID || again.ExternalID != "v1:0:0:" || len(w.comments(fork)) != 1 || w.comments(fork)[0].Body != comment {
			t.Errorf("redelivered fork refusal changed feedback: %+v", again)
		}
	})
}

func TestProtectedPushTriggersRepoSweep(t *testing.T) {
	tests := []struct {
		name  string
		paths []string
		swept bool
	}{
		{"policy", []string{"pkg/a/a.go", policy.PolicyPath}, true},
		{"baseline", []string{policy.BaselinePath}, true},
		{"labels", []string{policy.LabelsPath}, true},
		{"settings", []string{settings.Path}, true},
		{"the archfit workflow", []string{testWorkflow}, true},
		{"another workflow file", []string{".github/workflows/release.yaml"}, true},
		{"only source files", []string{"pkg/a/a.go", "docs/.archfit.yaml", "github/workflows/x.yaml"}, false},
		{"empty list (unknown)", nil, true},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			w := newWorld(t)
			web := w.addRepo("acme/web", shopInstallation, 102, false)
			stale := map[*pull]bool{w.openPull(w.shop, 7): tc.swept, w.openPull(web, 3): false}
			for p := range stale {
				w.wantAnswer(w.upload(p, fixtureHealthy), 200, "success", "healthy")
				w.setFile(p.repo, testBranch, policy.PolicyPath, []byte(basePolicy+"# merged change\n"))
			}
			w.gh.calls = nil

			w.handle(w.pushEvent(w.shop, tc.paths...))
			writes := w.gh.writes()

			for p, swept := range stale {
				want := verdict.ReasonHealthy
				if swept {
					want = verdict.ReasonStaleInputs
				}
				if got := w.receipt(p).Reason; got != want {
					t.Errorf("%s PR %d: receipt reason = %s, want %s", p.repo.FullName, p.number, got, want)
				}
			}
			wantReruns := 0
			if tc.swept {
				wantReruns = 1
			}
			if len(w.gh.reruns) != wantReruns {
				t.Errorf("reruns = %v, want %d", w.gh.reruns, wantReruns)
			}
			if !tc.swept && w.gh.writes() != writes {
				t.Errorf("a source-only push wrote to GitHub")
			}
		})
	}

	t.Run("the other repository is never touched", func(t *testing.T) {
		w := newWorld(t)
		web := w.addRepo("acme/web", shopInstallation, 102, false)
		w.openPull(web, 3)
		w.openPull(w.shop, 7)
		w.handle(w.pushEvent(w.shop, policy.PolicyPath))
		if len(w.gh.calls) == 0 {
			t.Fatal("the repository pass made no call")
		}
		for _, c := range w.gh.calls {
			if c.fullName != shopName || c.installationID != shopInstallation {
				t.Errorf("call outside the pushed repository: %+v", c)
			}
		}
	})

	t.Run("a rate-limited pass is a retryable error", func(t *testing.T) {
		w := newWorld(t)
		w.openPull(w.shop, 7)
		w.gh.fail["OpenPulls"] = api.ErrRateLimited
		err := w.gate.HandleEvent(context.Background(), w.pushEvent(w.shop, policy.PolicyPath))
		if !errors.Is(err, api.ErrRateLimited) || api.Permanent(err) {
			t.Errorf("HandleEvent = %v, want a retryable api.ErrRateLimited", err)
		}
		if n := w.gh.writes(); n != 0 {
			t.Errorf("%d writes after a rate limit", n)
		}
	})
}

func TestNewReceiverIsTheSameReceiver(t *testing.T) {
	for _, row := range negativeRows() {
		t.Run(row.name, func(t *testing.T) {
			// The report route, through the gate.
			w := newWorld(t)
			p := w.openPull(w.shop, 7)
			s := w.spec(p, w.report(w.shop, fixtureHealthy))
			row.damage(w, p, &s)
			viaGate := w.send(s)

			// The discovery route, through NewReceiver's composition.
			w = newWorld(t)
			p = w.openPull(w.shop, 7)
			s = w.spec(p, []byte("version: 1\n"))
			s.env.Kind = envelope.KindDiscovery
			row.damage(w, p, &s)
			_, err := newReceiver(w.gh, w.oidc, testAudience, w.admit).Receive(context.Background(), w.request(s), envelope.KindDiscovery)

			var rejection *Rejection
			if !errors.As(err, &rejection) {
				t.Fatalf("err = %v, want a *Rejection", err)
			}
			if rejection.Status != viaGate.status || rejection.Code != viaGate.Error {
				t.Errorf("discovery rejection = %d %s, report route answered %d %s",
					rejection.Status, rejection.Code, viaGate.status, viaGate.Error)
			}
			if n := w.gh.writes(); n != 0 {
				t.Errorf("%d writes", n)
			}
		})
	}

	t.Run("a discovery upload carries the payload and no report", func(t *testing.T) {
		w := newWorld(t)
		p := w.openPull(w.shop, 7)
		w.editRun(p, func(r *api.WorkflowRun) { r.Event, r.PullRequests = "workflow_dispatch", nil })
		draft := []byte("version: 1\nmodules: {}\n")
		s := w.spec(p, draft)
		s.env.Kind, s.env.Event, s.env.PullRequest, s.env.BaseSHA, s.env.MergeBaseSHA = envelope.KindDiscovery, "workflow_dispatch", 0, "", ""
		s.env.AnalyzedSHA, s.env.Integration = s.env.HeadSHA, "" // a dispatched run measures its own head
		s.claims.EventName, s.claims.Ref = "workflow_dispatch", "refs/heads/"+testBranch

		u, err := newReceiver(w.gh, w.oidc, testAudience, w.admit).Receive(context.Background(), w.request(s), envelope.KindDiscovery)
		if err != nil {
			t.Fatal(err)
		}
		if string(u.Payload) != string(draft) || u.Report != nil || len(u.Problems) != 0 {
			t.Errorf("upload = payload %q, report %v, problems %v", u.Payload, u.Report, u.Problems)
		}
		if u.Binding.Repo != w.shop || u.Binding.Event != "workflow_dispatch" || u.Binding.RunID != p.runID {
			t.Errorf("binding = %+v", u.Binding)
		}
		if n := w.gh.writes(); n != 0 {
			t.Errorf("%d writes for a discovery upload", n)
		}
	})

	t.Run("NewReceiver rejects before it needs GitHub", func(t *testing.T) {
		w := newWorld(t)
		p := w.openPull(w.shop, 7)
		s := w.spec(p, []byte("version: 1\n"))
		s.env.Kind = envelope.KindDiscovery
		s.token, s.claims = "forged", oidc.Claims{}
		req := w.request(s)
		_, err := NewReceiver(api.New(nil, nil), w.oidc, testAudience, NewAdmissions(w.mem, nil)).Receive(context.Background(), req, envelope.KindDiscovery)
		var rejection *Rejection
		if !errors.As(err, &rejection) || rejection.Status != 401 {
			t.Errorf("err = %v, want a 401 rejection", err)
		}
	})
}

func TestEntitlementContract(t *testing.T) {
	const reason = "Trial ended. Choose a plan under Account to restore the gate."
	w := newWorld(t)
	vault := w.addRepo("acme/vault", 12, 201, true)
	w.entitle(12, Entitlement{
		SuspendAfter: w.now.Add(-time.Hour), WarnFrom: w.now.Add(-48 * time.Hour),
		Notice: "Trial ends 2026-09-21", Reason: reason,
	})
	p := w.openPull(vault, 7)
	w.handle(w.event(webhook.PullOpened, p))

	w.wantAnswer(w.upload(p, fixtureHealthy), 200, "action_required", "subscription_suspended")
	w.wantGate(p, runCompleted, "action_required")
	if text := w.feedbackText(p); !strings.Contains(text, reason) {
		t.Errorf("feedback does not show the record's Reason as the next action:\n%s", text)
	}

	t.Run("a public repository is never suspended", func(t *testing.T) {
		w := newWorld(t)
		w.entitle(shopInstallation, Entitlement{SuspendAfter: w.now.Add(-time.Hour), Reason: reason})
		w.wantAnswer(w.upload(w.openPull(w.shop, 7), fixtureHealthy), 200, "success", "healthy")
	})
}

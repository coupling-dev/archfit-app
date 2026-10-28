package gate

import (
	"context"
	"errors"
	"maps"
	"slices"
	"strconv"
	"testing"

	"github.com/alexei-led/archfit-app/internal/github/api"
	"github.com/alexei-led/archfit-app/internal/github/webhook"
	"github.com/alexei-led/archfit-app/internal/store"
	"github.com/alexei-led/archfit-app/internal/wire/policy"
)

// pullApproval is what the pull request page reads for login.
func (w *world) pullApproval(p *pull, login string) PullApproval {
	w.t.Helper()
	pl := w.gh.pulls[pullKey(p.repo.FullName, p.number)]
	got, err := w.gate.PullApproval(context.Background(), w.target(p), login, pl.Author)
	if err != nil {
		w.t.Fatalf("PullApproval: %v", err)
	}
	return got
}

// approve is the page's Approve button: what login was shown, recorded, then the pull request re-decided.
func (w *world) approve(p *pull, login string) {
	w.t.Helper()
	seen := w.pullApproval(p, login).Digests
	if err := w.gate.Approve(context.Background(), w.target(p), login, seen); err != nil {
		w.t.Fatalf("Approve: %v", err)
	}
	if err := w.gate.Reevaluate(context.Background(), w.target(p)); err != nil {
		w.t.Fatalf("Reevaluate: %v", err)
	}
}

func TestAppApprovalReDecidesThePullRequest(t *testing.T) {
	w := newWorld(t)
	p, digest := w.missingApproval(7)

	before := w.pullApproval(p, ownerLogin)
	if before.Granted || before.Refusal != nil || !slices.Equal(before.Paths, []string{policy.BaselinePath}) ||
		!maps.Equal(before.Digests, map[string]string{policy.BaselinePath: digest}) {
		t.Fatalf("before = %+v", before)
	}

	w.approve(p, ownerLogin)
	if !slices.Equal(w.gh.reruns, []int64{p.runID}) {
		t.Fatalf("reruns = %v, want [%d]: an approval re-runs the workflow as a review does", w.gh.reruns, p.runID)
	}
	w.wantGate(p, runCompleted, "action_required") // the approval alone turns nothing green
	w.wantAnswer(w.send(w.baselineUpload(p, digest)), 200, "success", "healthy")
	if got := w.receipt(p).BaselineDigest; got != digest {
		t.Errorf("receipt baseline digest = %s, want the approved head digest", got)
	}
	if after := w.pullApproval(p, ownerLogin); !after.Granted || after.ApprovedBy != ownerLogin {
		t.Errorf("after = %+v, want granted by %s", after, ownerLogin)
	}

	t.Run("a new commit needs a new approval", func(t *testing.T) {
		w.finishRun(p)
		w.push(p)
		digest := w.editsBaseline(p)
		w.handle(w.event(webhook.PullSynchronized, p))
		w.wantAnswer(w.send(w.baselineUpload(p, digest)), 200, "action_required", "owner_approval_missing")
		if got := w.pullApproval(p, ownerLogin); got.Granted {
			t.Errorf("the new head reads %+v", got)
		}
	})
}

func TestAppApprovalRefusesAgentsAndOthers(t *testing.T) {
	tests := []struct {
		name   string
		owners []string
		agents []string
		login  string
		want   error
	}{
		{"an owner listed as an agent", []string{ownerLogin}, []string{ownerLogin}, ownerLogin, ErrAgentIdentity},
		{"the app account of an agent", []string{ownerLogin}, []string{"claude"}, "claude[bot]", ErrAgentIdentity},
		{"a team member listed as an agent", []string{"acme/architects"}, []string{"carol"}, "carol", ErrAgentIdentity},
		{"not an owner", []string{ownerLogin}, nil, "bob", ErrNotOwner},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			w := newWorld(t)
			w.setOwners(w.shop, testBranch, tt.owners, tt.agents...)
			w.gh.teams["acme/architects|carol"] = true
			p, digest := w.missingApproval(7)
			w.review(p, tt.login, "APPROVED", p.head)
			w.handle(w.event(webhook.ReviewChanged, p))

			got := w.pullApproval(p, tt.login)
			if got.Granted || !errors.Is(got.Refusal, tt.want) {
				t.Errorf("PullApproval = %+v, want refusal %v", got, tt.want)
			}
			if err := w.gate.Approve(context.Background(), w.target(p), tt.login, got.Digests); !errors.Is(err, tt.want) {
				t.Errorf("Approve = %v, want %v", err, tt.want)
			}
			if err := w.gate.Reevaluate(context.Background(), w.target(p)); err != nil {
				t.Fatal(err)
			}
			if len(w.gh.reruns) != 0 {
				t.Errorf("reruns = %v, want none", w.gh.reruns)
			}
			w.wantAnswer(w.send(w.baselineUpload(p, digest)), 200, "action_required", "owner_approval_missing")
		})
	}
}

func TestPullApprovalNamesAnAgentAuthor(t *testing.T) {
	w := newWorld(t)
	w.setOwners(w.shop, testBranch, []string{ownerLogin}, "copilot-swe-agent")
	p := w.openPull(w.shop, 7)
	w.editsBaseline(p)
	w.editPull(p, func(pl *api.Pull) { pl.Author = "Copilot-SWE-Agent[bot]" })

	if got := w.pullApproval(p, ownerLogin); !got.AuthorIsAgent || got.Refusal != nil {
		t.Errorf("PullApproval = %+v, want an agent author and an owner who may approve", got)
	}
	w.editPull(p, func(pl *api.Pull) { pl.Author = ownerLogin })
	if got := w.pullApproval(p, ownerLogin); got.AuthorIsAgent {
		t.Errorf("PullApproval = %+v, want a person as author", got)
	}
}

func TestPullApprovalWithoutAProtectedChangeIsEmpty(t *testing.T) {
	w := newWorld(t)
	p := w.openPull(w.shop, 7)
	w.changes(p, "pkg/a/a.go")

	got := w.pullApproval(p, ownerLogin)
	if len(got.Paths) != 0 || got.Granted || got.Refusal != nil || got.AuthorIsAgent {
		t.Errorf("PullApproval = %+v, want the zero value", got)
	}
	if err := w.gate.Approve(context.Background(), w.target(p), ownerLogin, nil); !errors.Is(err, ErrNothingToApprove) {
		t.Errorf("Approve = %v, want ErrNothingToApprove", err)
	}
}

func TestApprovalNeedsAnAdmittedInstallation(t *testing.T) {
	w := newWorld(t)
	p, _ := w.missingApproval(7)
	seen := w.pullApproval(p, ownerLogin).Digests
	w.admit.refused[shopInstallation] = true
	before := len(w.gh.calls)

	if _, err := w.gate.PullApproval(context.Background(), w.target(p), ownerLogin, ""); !errors.Is(err, errNotAdmitted) {
		t.Errorf("PullApproval = %v, want errNotAdmitted", err)
	}
	if err := w.gate.Approve(context.Background(), w.target(p), ownerLogin, seen); !errors.Is(err, errNotAdmitted) {
		t.Errorf("Approve = %v, want errNotAdmitted", err)
	}
	if err := w.gate.Reevaluate(context.Background(), w.target(p)); !errors.Is(err, errNotAdmitted) {
		t.Errorf("Reevaluate = %v, want errNotAdmitted", err)
	}
	if len(w.gh.calls) != before {
		t.Errorf("GitHub calls for an installation no operator approved: %v", w.gh.calls[before:])
	}
	if _, err := w.mem.Get(context.Background(), store.Tenant(shopInstallation), approvalKeyPrefix+shopName+"/"+strconv.Itoa(p.number)+"/"+p.head); !errors.Is(err, store.ErrNotFound) {
		t.Errorf("approval record = %v, want none", err)
	}
}

package gate

import (
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/alexei-led/archfit-app/internal/gate/inputs"
	"github.com/alexei-led/archfit-app/internal/github/api"
	"github.com/alexei-led/archfit-app/internal/wire/decision"
	"github.com/alexei-led/archfit-app/internal/wire/policy"
)

// pageTarget is all the page knows of a pull request when it decides: the repository and the number.
func (w *world) pageTarget(p *pull) inputs.Target {
	return inputs.Target{Repo: p.repo, PullRequest: p.number}
}

// requested is a pull request with the agent's request filed on its current head.
func (w *world) requested() (*pull, string) {
	p := w.openPull(w.shop, 7)
	w.handle(w.askFor(p))
	return p, w.digestOf(p.repo, testBranch, policy.PolicyPath)
}

// proposal is the draft pull request control/ui opens for an approved case: number, from the case's branch, into the
// default branch, changing .archfit.yaml alone.
func (w *world) proposal(number int, c DecisionCase) *pull {
	pp := w.openPull(w.shop, number)
	w.editPull(pp, func(pl *api.Pull) { pl.Draft, pl.HeadRef = true, c.Branch })
	w.changes(pp, policy.PolicyPath)
	return pp
}

func (w *world) wantStatusOf(p *pull, digest, line string) {
	w.t.Helper()
	want := decision.StatusMarker(findingA, digest) + "\n" + line + "\nOnly an architecture owner can approve this request."
	if got := w.statusComments(p); len(got) != 1 || got[0] != want {
		w.t.Fatalf("status comments = %q, want exactly %q", got, want)
	}
}

func (w *world) wantRecord(p *pull, digest string, status decision.Status, reason string, proposalPR int) {
	w.t.Helper()
	rec, err := w.requests().Get(context.Background(), w.target(p), findingA, digest)
	if err != nil || rec.Status != status || rec.Reason != reason || rec.ProposalPR != proposalPR {
		w.t.Fatalf("record = %+v, %v; want %s (%s), proposal %d", rec, err, status, reason, proposalPR)
	}
}

func TestDecisionRequestsAreListedForAnyoneAndDecidableByAnOwnerWhoDidNotFileThem(t *testing.T) {
	tests := []struct {
		name      string
		login     string
		requester string // who files the request; the agent when empty
		agent     bool   // login is an agent identity of the settings, and an owner by name
		want      bool
	}{
		{name: "an owner", login: ownerLogin, want: true},
		{name: "a reader", login: "bob"},
		{name: "the agent that filed it", login: agentLogin},
		{name: "an owner who filed it", login: ownerLogin, requester: ownerLogin},
		{name: "an agent identity of the settings", login: "agent-9", agent: true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			w := newWorld(t)
			if tt.agent {
				w.setOwners(w.shop, testBranch, []string{ownerLogin, tt.login}, tt.login)
			}
			p := w.openPull(w.shop, 7)
			e := w.askFor(p)
			if tt.requester != "" {
				e.Author = tt.requester
			}
			w.handle(e)
			got, err := w.gate.DecisionRequests(context.Background(), w.target(p), tt.login)
			if err != nil || len(got) != 1 {
				t.Fatalf("DecisionRequests = %+v, %v; want one request", got, err)
			}
			if got[0].CanDecide != tt.want || got[0].FindingID != findingA || got[0].Status != decision.StatusPending {
				t.Errorf("request = %+v, want CanDecide %v", got[0], tt.want)
			}
		})
	}
}

func TestDecisionRequestsOfASettledRequestAreNotDecidable(t *testing.T) {
	w := newWorld(t)
	p, digest := w.requested()
	w.handle(w.askFor(p, func(doc map[string]any) { doc["finding_id"] = findingB }))
	if err := w.gate.DeclineDecision(context.Background(), w.pageTarget(p), ownerLogin, findingA, digest); err != nil {
		t.Fatal(err)
	}
	got, err := w.gate.DecisionRequests(context.Background(), w.target(p), ownerLogin)
	if err != nil || len(got) != 2 {
		t.Fatalf("DecisionRequests = %+v, %v", got, err)
	}
	for _, r := range got {
		settled := r.FindingID == findingA
		if settled != (r.Status == decision.StatusDeclined) || r.CanDecide == settled {
			t.Errorf("request %s = %s, CanDecide %v: only the pending one may be decided", r.FindingID[:4], r.Status, r.CanDecide)
		}
	}
}

func TestOwnerApprovesAfterTheRequestStillHolds(t *testing.T) {
	w := newWorld(t)
	p, digest := w.requested()
	ctx := context.Background()

	c, err := w.gate.ApproveDecision(ctx, w.pageTarget(p), ownerLogin, findingA, digest)
	if err != nil || c.Policy != candidate || c.Record.FindingID != findingA || c.Record.HeadSHA != p.head {
		t.Fatalf("ApproveDecision = %+v, %v", c, err)
	}
	w.wantRecord(p, digest, decision.StatusPending, "", 0) // nothing moved until the proposal exists
	w.proposal(31, c)
	if err := w.gate.ApprovedDecision(ctx, w.pageTarget(p), findingA, digest, p.head, 31); err != nil {
		t.Fatal(err)
	}
	w.wantRecord(p, digest, decision.StatusApproved, "", 31)
	w.wantStatusOf(p, digest, "approved (proposal PR #31)")
	w.proposal(32, c)
	if err := w.gate.ApprovedDecision(ctx, w.pageTarget(p), findingA, digest, p.head, 32); !errors.Is(err, ErrDecisionSettled) {
		t.Errorf("a second ApprovedDecision = %v, want ErrDecisionSettled", err)
	}
	w.wantRecord(p, digest, decision.StatusApproved, "", 31)
	if len(w.gh.checkWrites) != 0 {
		t.Errorf("an owner decision wrote a check: %+v", w.gh.checkWrites)
	}
}

func TestOwnerDeclinesAndTheRequestStaysDeclined(t *testing.T) {
	w := newWorld(t)
	p, digest := w.requested()
	ctx := context.Background()
	if err := w.gate.DeclineDecision(ctx, w.pageTarget(p), ownerLogin, findingA, digest); err != nil {
		t.Fatal(err)
	}
	w.wantRecord(p, digest, decision.StatusDeclined, decision.ReasonOwnerDeclined, 0)
	w.wantStatusOf(p, digest, "declined (by an owner)")
	if err := w.gate.DeclineDecision(ctx, w.pageTarget(p), ownerLogin, findingA, digest); !errors.Is(err, ErrDecisionSettled) {
		t.Errorf("a second decline = %v, want ErrDecisionSettled", err)
	}
	if _, err := w.gate.ApproveDecision(ctx, w.pageTarget(p), ownerLogin, findingA, digest); !errors.Is(err, ErrDecisionSettled) {
		t.Errorf("approving a declined request = %v, want ErrDecisionSettled", err)
	}
	w.push(p)
	w.handle(w.askFor(p)) // the agent files it again
	w.wantRecord(p, digest, decision.StatusDeclined, decision.ReasonOwnerDeclined, 0)
}

func TestDecisionRefusalsChangeNothing(t *testing.T) {
	tests := []struct {
		name    string
		login   string
		finding string
		edit    func(w *world, p *pull)
		want    error
	}{
		{name: "a non-owner", login: "bob", finding: findingA, want: ErrNotOwner},
		{name: "no login", login: "", finding: findingA, want: ErrNotOwner},
		{name: "an agent identity named an owner", login: "agent-9", finding: findingA, edit: func(w *world, _ *pull) {
			w.setOwners(w.shop, testBranch, []string{ownerLogin, "agent-9"}, "agent-9")
		}, want: ErrAgentIdentity},
		{name: "an unknown request", login: ownerLogin, finding: findingB, want: ErrDecisionNotFound},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			w := newWorld(t)
			p, digest := w.requested()
			if tt.edit != nil {
				tt.edit(w, p)
			}
			ctx := context.Background()
			_, approveErr := w.gate.ApproveDecision(ctx, w.pageTarget(p), tt.login, tt.finding, digest)
			declineErr := w.gate.DeclineDecision(ctx, w.pageTarget(p), tt.login, tt.finding, digest)
			if !errors.Is(approveErr, tt.want) || !errors.Is(declineErr, tt.want) {
				t.Errorf("approve = %v, decline = %v; want %v", approveErr, declineErr, tt.want)
			}
			w.wantRecord(p, digest, decision.StatusPending, "", 0)
			w.wantStatusOf(p, digest, "pending")
		})
	}
}

// The requester can never decide its own request, even when it is an owner of the protected settings.
func TestTheRequesterNeverDecidesItsOwnRequest(t *testing.T) {
	w := newWorld(t)
	w.setOwners(w.shop, testBranch, []string{ownerLogin, agentLogin})
	p, digest := w.requested()
	ctx := context.Background()
	if _, err := w.gate.ApproveDecision(ctx, w.pageTarget(p), agentLogin, findingA, digest); !errors.Is(err, ErrDecisionRequester) {
		t.Errorf("ApproveDecision = %v, want ErrDecisionRequester", err)
	}
	if err := w.gate.DeclineDecision(ctx, w.pageTarget(p), "AGENT-7", findingA, digest); !errors.Is(err, ErrDecisionRequester) {
		t.Errorf("DeclineDecision = %v, want ErrDecisionRequester", err)
	}
	w.wantRecord(p, digest, decision.StatusPending, "", 0)
}

func TestApprovingARequestThatNoLongerHoldsMarksItStale(t *testing.T) {
	tests := []struct {
		name   string
		change func(w *world, p *pull)
		reason string
	}{
		{name: "the head moved", change: func(w *world, p *pull) { w.push(p) }, reason: decision.ReasonHeadMoved},
		{name: "the policy changed on the protected ref", change: func(w *world, _ *pull) {
			w.setFile(w.shop, testBranch, policy.PolicyPath, []byte(basePolicy+"# changed\n"))
		}, reason: decision.ReasonPolicyChanged},
		{name: "the pull request closed", change: func(w *world, p *pull) {
			w.editPull(p, func(pl *api.Pull) { pl.State = "closed" })
		}, reason: decision.ReasonPullClosed},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			w := newWorld(t)
			p, digest := w.requested()
			tt.change(w, p) // no webhook: the decision itself must notice
			_, err := w.gate.ApproveDecision(context.Background(), w.pageTarget(p), ownerLogin, findingA, digest)
			if !errors.Is(err, ErrDecisionStale) {
				t.Fatalf("ApproveDecision = %v, want ErrDecisionStale", err)
			}
			w.wantRecord(p, digest, decision.StatusStale, tt.reason, 0)
			w.wantStatusOf(p, digest, "stale ("+tt.reason+")")
		})
	}
}

// A decision goes through the status lease and the stored record like any settle: the record says what it published,
// and a request refiled on another head since the owner looked is not approved.
func TestDecisionsUseThePublishPathAndTheHeadTheOwnerSaw(t *testing.T) {
	w := newWorld(t)
	p, digest := w.requested()
	ctx := context.Background()
	c, err := w.gate.ApproveDecision(ctx, w.pageTarget(p), ownerLogin, findingA, digest)
	if err != nil {
		t.Fatal(err)
	}
	w.proposal(31, c)
	oldHead := p.head
	w.push(p)
	w.handle(w.askFor(p)) // the agent files again on the new head
	if err := w.gate.ApprovedDecision(ctx, w.pageTarget(p), findingA, digest, oldHead, 31); !errors.Is(err, ErrDecisionSettled) {
		t.Fatalf("ApprovedDecision on the old head = %v, want ErrDecisionSettled", err)
	}
	w.wantRecord(p, digest, decision.StatusPending, "", 0)

	c2, err := w.gate.ApproveDecision(ctx, w.pageTarget(p), ownerLogin, findingA, digest)
	if err != nil {
		t.Fatal(err)
	}
	w.proposal(32, c2)
	if err := w.gate.ApprovedDecision(ctx, w.pageTarget(p), findingA, digest, p.head, 32); err != nil {
		t.Fatal(err)
	}
	if rec := w.record(p, findingA); rec.Published != "approved (proposal PR #32)" {
		t.Errorf("Published = %q, want the line written to the thread", rec.Published)
	}
}

func TestProposalBranchIsSecretStablePerRequestAndPerHead(t *testing.T) {
	w := newWorld(t)
	p, digest := w.requested()
	q := w.openPull(w.shop, 8)
	w.handle(w.askFor(q)) // another pull request, the same finding and policy digest
	ctx := context.Background()
	branch := func(pull *pull) string {
		c, err := w.gate.ApproveDecision(ctx, w.pageTarget(pull), ownerLogin, findingA, digest)
		if err != nil {
			t.Fatal(err)
		}
		return c.Branch
	}
	first, again, other := branch(p), branch(p), branch(q)
	if first != again {
		t.Errorf("a retry names %s, the first approval named %s: the branch must be stable", again, first)
	}
	if first == other || !strings.HasPrefix(first, "archfit/decision-7-") || !strings.HasPrefix(other, "archfit/decision-8-") {
		t.Errorf("branches %q and %q: two pull requests must not share one", first, other)
	}
	suffix := strings.TrimPrefix(first, "archfit/decision-7-")
	if len(suffix) < 24 || strings.Contains(suffix, findingA[:8]) || strings.Contains(first, digest[:8]) {
		t.Errorf("suffix %q is short or derived from what the agent states", suffix)
	}
	if old := "archfit/decision-" + findingA[:8] + "-" + digest[:8]; first == old || other == old {
		t.Error("the branch is the old, guessable name")
	}
	w.push(p)
	w.handle(w.askFor(p))
	if refiled := branch(p); refiled == first {
		t.Error("a new head reuses the branch of the old one")
	}
}

func TestApprovalIsRefusedForAPullRequestThatIsNotTheExpectedDraft(t *testing.T) {
	tests := []struct {
		name string
		edit func(w *world, pp *pull, c DecisionCase)
	}{
		{name: "an agent's own pull request from a guessed branch", edit: func(w *world, pp *pull, _ DecisionCase) {
			w.editPull(pp, func(pl *api.Pull) {
				pl.HeadRef = "archfit/decision-" + findingA[:8] + "-" + w.digestOf(w.shop, testBranch, policy.PolicyPath)[:8]
			})
		}},
		{name: "not a draft", edit: func(w *world, pp *pull, _ DecisionCase) { w.editPull(pp, func(pl *api.Pull) { pl.Draft = false }) }},
		{name: "closed", edit: func(w *world, pp *pull, _ DecisionCase) { w.editPull(pp, func(pl *api.Pull) { pl.State = "closed" }) }},
		{name: "a fork's branch", edit: func(w *world, pp *pull, _ DecisionCase) {
			w.editPull(pp, func(pl *api.Pull) { pl.HeadRepo = "mallory/shop" })
		}},
		{name: "another base", edit: func(w *world, pp *pull, _ DecisionCase) {
			w.editPull(pp, func(pl *api.Pull) { pl.BaseRef = "release" })
		}},
		{name: "more files than the policy", edit: func(w *world, pp *pull, _ DecisionCase) { w.changes(pp, policy.PolicyPath, "pkg/a/a.go") }},
		{name: "another file", edit: func(w *world, pp *pull, _ DecisionCase) { w.changes(pp, ".github/workflows/x.yaml") }},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			w := newWorld(t)
			p, digest := w.requested()
			ctx := context.Background()
			c, err := w.gate.ApproveDecision(ctx, w.pageTarget(p), ownerLogin, findingA, digest)
			if err != nil {
				t.Fatal(err)
			}
			pp := w.proposal(31, c)
			tt.edit(w, pp, c)
			if err := w.gate.ApprovedDecision(ctx, w.pageTarget(p), findingA, digest, p.head, 31); !errors.Is(err, ErrDecisionProposal) {
				t.Fatalf("ApprovedDecision = %v, want ErrDecisionProposal", err)
			}
			w.wantRecord(p, digest, decision.StatusPending, "", 0)
			w.wantStatusOf(p, digest, "pending")
		})
	}
}

// The protected ref is the default branch. A pull request retargeted to a branch its author controls decides nothing.
func TestARetargetedPullRequestDecidesNothing(t *testing.T) {
	retarget := func(w *world, p *pull) {
		w.setOwners(w.shop, "release", []string{ownerLogin, "mallory"}) // the author's branch names a new owner
		w.setFile(w.shop, "release", policy.PolicyPath, []byte(basePolicy))
		w.editPull(p, func(pl *api.Pull) { pl.BaseRef = "release" })
	}
	ctx := context.Background()
	t.Run("a writer who owns only the retarget base is refused", func(t *testing.T) {
		w := newWorld(t)
		p, digest := w.requested()
		retarget(w, p)
		_, approveErr := w.gate.ApproveDecision(ctx, w.pageTarget(p), "mallory", findingA, digest)
		declineErr := w.gate.DeclineDecision(ctx, w.pageTarget(p), "mallory", findingA, digest)
		if !errors.Is(approveErr, ErrNotOwner) || !errors.Is(declineErr, ErrNotOwner) {
			t.Errorf("approve = %v, decline = %v; want ErrNotOwner", approveErr, declineErr)
		}
		w.wantRecord(p, digest, decision.StatusPending, "", 0)
	})
	t.Run("an owner is refused and the request goes stale", func(t *testing.T) {
		for _, decide := range []string{"approve", "decline"} {
			w := newWorld(t)
			p, digest := w.requested()
			retarget(w, p)
			var err error
			if decide == "approve" {
				_, err = w.gate.ApproveDecision(ctx, w.pageTarget(p), ownerLogin, findingA, digest)
			} else {
				err = w.gate.DeclineDecision(ctx, w.pageTarget(p), ownerLogin, findingA, digest)
			}
			if !errors.Is(err, ErrDecisionStale) {
				t.Fatalf("%s = %v, want ErrDecisionStale", decide, err)
			}
			w.wantRecord(p, digest, decision.StatusStale, decision.ReasonBaseChanged, 0)
			w.wantStatusOf(p, digest, "stale (base branch changed)")
		}
	})
	t.Run("the page offers nothing", func(t *testing.T) {
		w := newWorld(t)
		p, _ := w.requested()
		retarget(w, p)
		target := w.target(p)
		target.BaseRef = "release" // the page's target carries the live base
		for _, login := range []string{ownerLogin, "mallory"} {
			got, err := w.gate.DecisionRequests(ctx, target, login)
			if err != nil || len(got) != 1 || got[0].CanDecide {
				t.Errorf("DecisionRequests(%s) = %+v, %v; want one row, not decidable", login, got, err)
			}
		}
	})
}

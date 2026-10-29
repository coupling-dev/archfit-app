package gate

import (
	"context"
	"errors"
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
	if err := w.gate.ApprovedDecision(ctx, w.pageTarget(p), findingA, digest, p.head, 31); err != nil {
		t.Fatal(err)
	}
	w.wantRecord(p, digest, decision.StatusApproved, "", 31)
	w.wantStatusOf(p, digest, "approved (proposal PR #31)")
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
	if err := w.gate.ApprovedDecision(ctx, w.pageTarget(p), findingA, digest, "not-the-head", 31); !errors.Is(err, ErrDecisionSettled) {
		t.Fatalf("ApprovedDecision on another head = %v, want ErrDecisionSettled", err)
	}
	w.wantRecord(p, digest, decision.StatusPending, "", 0)

	if err := w.gate.ApprovedDecision(ctx, w.pageTarget(p), findingA, digest, p.head, 31); err != nil {
		t.Fatal(err)
	}
	if rec := w.record(p, findingA); rec.Published != "approved (proposal PR #31)" {
		t.Errorf("Published = %q, want the line written to the thread", rec.Published)
	}
}

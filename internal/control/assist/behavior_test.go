package assist

import (
	"context"
	"errors"
	"testing"

	"github.com/alexei-led/archfit-app/internal/control/account"
	"github.com/alexei-led/archfit-app/internal/control/proposal"
	"github.com/alexei-led/archfit-app/internal/github/api"
	"github.com/alexei-led/archfit-app/internal/wire/policy"
)

func TestStaleProposalCannotOverwriteEdits(t *testing.T) {
	t.Parallel()
	// duringCall changes the draft as another actor would, while the provider is working.
	duringCall := func(fx *fixture, d proposal.Draft) {
		fx.provider.complete = func(context.Context, Prompt) (Completion, error) {
			fx.proposals.setDraft(testBranch, d)
			return Completion{Text: editedYAML, CostMicroUSD: testActualCost}, nil
		}
	}
	tests := []struct {
		name        string
		setup       func(*fixture)
		wantDraft   proposal.Draft
		wantReserve int
	}{
		{"head differs at request time", func(fx *fixture) {
			fx.proposals.setDraft(testBranch, proposal.Draft{Number: 7, State: proposal.DraftOpen, HeadSHA: headC})
		}, proposal.Draft{Number: 7, State: proposal.DraftOpen, HeadSHA: headC}, 0},
		{"head moves while the provider is working", func(fx *fixture) {
			duringCall(fx, proposal.Draft{Number: 7, State: proposal.DraftOpen, HeadSHA: headC})
		}, proposal.Draft{Number: 7, State: proposal.DraftOpen, HeadSHA: headC}, 1},
		{"draft merged while the provider is working", func(fx *fixture) {
			duringCall(fx, proposal.Draft{Number: 7, State: proposal.DraftMerged, HeadSHA: headA})
		}, proposal.Draft{Number: 7, State: proposal.DraftMerged, HeadSHA: headA}, 1},
		{"draft closed while the provider is working", func(fx *fixture) {
			duringCall(fx, proposal.Draft{Number: 7, State: proposal.DraftClosed, HeadSHA: headA})
		}, proposal.Draft{Number: 7, State: proposal.DraftClosed, HeadSHA: headA}, 1},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			fx := newFixture()
			tt.setup(fx)

			_, err := fx.assist().Propose(context.Background(), ModeManaged, validRequest())

			if !errors.Is(err, proposal.ErrStale) {
				t.Fatalf("Propose() error = %v, want ErrStale", err)
			}
			if got := fx.budget.reserveCount(); got != tt.wantReserve {
				t.Errorf("reserves = %d, want %d", got, tt.wantReserve)
			}
			if tt.wantReserve == 0 {
				if fx.provider.callCount() != 0 || len(fx.budget.settles) != 0 {
					t.Errorf("money moved for a request that was stale from the start")
				}
			} else if len(fx.budget.settles) != 1 || fx.budget.settles[0].actual != testActualCost {
				t.Errorf("settles = %+v, want one with the actual cost %d", fx.budget.settles, testActualCost)
			}
			if len(fx.proposals.commits) != 0 {
				t.Errorf("commits = %d, want 0", len(fx.proposals.commits))
			}
			if got := fx.proposals.draft(testBranch); got != tt.wantDraft {
				t.Errorf("draft = %+v, want the newer edit intact %+v", got, tt.wantDraft)
			}
		})
	}
}

func TestProposalIsNeverAutoPublished(t *testing.T) {
	t.Parallel()
	for _, state := range []proposal.DraftState{proposal.DraftOpen, proposal.DraftInReview} {
		t.Run(string(state), func(t *testing.T) {
			t.Parallel()
			fx := newFixture()
			fx.proposals.setDraft(testBranch, proposal.Draft{Number: 7, State: state, HeadSHA: headA})

			res, err := fx.assist().Propose(context.Background(), ModeManaged, validRequest())

			if err != nil {
				t.Fatalf("Propose() error = %v", err)
			}
			if res.Draft.State != state || fx.proposals.draft(testBranch).State != state {
				t.Errorf("state = %q, want unchanged %q", res.Draft.State, state)
			}
			if len(fx.proposals.commits) != 1 || fx.proposals.attemptCount() != 1 {
				t.Fatalf("commits = %d, attempts = %d, want one new commit", len(fx.proposals.commits), fx.proposals.attemptCount())
			}
			if c := fx.proposals.commits[0].change; c.Branch != testBranch || c.Branch == c.Base {
				t.Errorf("commit on %q (base %q), want only the draft branch", c.Branch, c.Base)
			}
			// The fakes panic on any github/api call other than File and Dispatch: no merge exists to call.
			if fx.proposals.otherCalls != 0 || len(fx.actions.calls) != 0 {
				t.Errorf("ready/body calls = %d, dispatches = %d, want none", fx.proposals.otherCalls, len(fx.actions.calls))
			}
		})
	}
}

func TestLLMFailureLeavesEverythingElseWorking(t *testing.T) {
	t.Parallel()
	fx := newFixture()
	a := fx.assist()
	editorSave := func(step string) {
		t.Helper()
		head := fx.proposals.draft(testBranch).HeadSHA
		_, err := fx.proposals.Propose(context.Background(), proposal.Change{
			Repo: testRepo.RepoRef, Branch: testBranch, Base: mainBranch, Message: "edit",
			Files: []api.FileChange{{Path: policy.PolicyPath, Content: []byte(draftYAML)}},
		}, head)
		if err != nil {
			t.Fatalf("editor save after %s: %v", step, err)
		}
		// The next assist request in this test looks at headA again.
		fx.proposals.setDraft(testBranch, proposal.Draft{Number: 7, State: proposal.DraftOpen, HeadSHA: headA})
	}

	fx.provider.complete = func(context.Context, Prompt) (Completion, error) {
		return Completion{}, errors.New("always down")
	}
	if _, err := a.Propose(context.Background(), ModeManaged, validRequest()); !errors.Is(err, ErrProvider) {
		t.Fatalf("provider down: error = %v, want ErrProvider", err)
	}
	editorSave("provider failure")

	fx.budget.reserveErr = account.ErrBudgetExhausted
	if _, err := a.Propose(context.Background(), ModeManaged, validRequest()); !errors.Is(err, account.ErrBudgetExhausted) {
		t.Fatalf("budget exhausted: error = %v, want ErrBudgetExhausted", err)
	}
	editorSave("budget exhaustion")

	fx.consents.down = true
	if _, err := a.Propose(context.Background(), ModeManaged, validRequest()); !errors.Is(err, account.ErrNoConsent) {
		t.Fatalf("store down: error = %v, want ErrNoConsent", err)
	}
	editorSave("store outage")

	if got := len(fx.proposals.commits); got != 3 {
		t.Errorf("commits = %d, want the 3 editor saves and nothing from assist", got)
	}
	// Check runs and the gate are not dependencies of assist; the embedded nil github/api roles
	// panic on anything but File and Dispatch.
	if len(fx.actions.calls) != 0 {
		t.Errorf("dispatches = %d, want 0", len(fx.actions.calls))
	}
}

func TestCustomerModeKeepsCredentialsAndSourceOut(t *testing.T) {
	t.Parallel()
	fx := newFixture()
	fx.repos.files[fileKey{headA, "main.go"}] = []byte("package main")
	req := validRequest()

	res, err := fx.assist().Propose(context.Background(), ModeCustomer, req)

	if err != nil {
		t.Fatalf("Propose() error = %v", err)
	}
	// Consents and Budget are assist's only Store-backed dependencies.
	if fx.consents.calls != 0 || fx.consents.writes != 0 || fx.budget.reserveCount() != 0 || len(fx.budget.settles) != 0 {
		t.Errorf("customer mode touched the Store-backed dependencies")
	}
	if fx.provider.callCount() != 0 {
		t.Errorf("provider calls = %d, want 0", fx.provider.callCount())
	}
	if len(fx.repos.reads) != 0 {
		t.Errorf("customer mode read repository files: %+v", fx.repos.reads)
	}
	if fx.proposals.attemptCount() != 0 {
		t.Errorf("customer mode wrote a commit")
	}
	if len(fx.actions.calls) != 1 {
		t.Fatalf("dispatches = %d, want 1", len(fx.actions.calls))
	}
	inputs := fx.actions.calls[0].inputs
	if len(inputs) != 3 || inputs["branch"] != testBranch || inputs["head_sha"] != headA || inputs["instruction"] != req.Instruction {
		t.Errorf("dispatch inputs = %v, want exactly branch, head_sha, instruction", inputs)
	}
	if res.RunURL != "https://github.com/acme/shop/actions/workflows/archfit-assist.yaml" {
		t.Errorf("RunURL = %q", res.RunURL)
	}
}

func TestManagedModeWithoutConsentMakesNoCall(t *testing.T) {
	t.Parallel()
	fx := newFixture()
	delete(fx.consents.granted, account.ConsentManagedLLM)
	a := fx.assist()
	ctx := context.Background()

	if err := fx.consents.Record(ctx, testRepo.InstallationID, account.Consent{Kind: account.ConsentManagedLLM, By: "admin", ByID: 1, At: testNow}); err != nil {
		t.Fatal(err)
	}
	if _, err := a.Propose(ctx, ModeManaged, validRequest()); err != nil {
		t.Fatalf("with consent: error = %v", err)
	}
	if fx.provider.callCount() != 1 {
		t.Fatalf("provider calls with consent = %d, want 1", fx.provider.callCount())
	}

	if err := fx.consents.Revoke(ctx, testRepo.InstallationID, account.ConsentManagedLLM); err != nil {
		t.Fatal(err)
	}
	fx.proposals.setDraft(testBranch, proposal.Draft{Number: 7, State: proposal.DraftOpen, HeadSHA: headA})
	if _, err := a.Propose(ctx, ModeManaged, validRequest()); !errors.Is(err, account.ErrNoConsent) {
		t.Fatalf("after revocation: error = %v, want ErrNoConsent", err)
	}
	if fx.provider.callCount() != 1 || fx.budget.reserveCount() != 1 {
		t.Errorf("provider calls = %d, reserves = %d after revocation; want no new call", fx.provider.callCount(), fx.budget.reserveCount())
	}
}

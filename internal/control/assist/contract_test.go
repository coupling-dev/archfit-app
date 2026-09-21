package assist

import (
	"context"
	"errors"
	"fmt"
	"reflect"
	"strings"
	"testing"

	"github.com/alexei-led/archfit-app/internal/control/account"
	"github.com/alexei-led/archfit-app/internal/control/proposal"
	"github.com/alexei-led/archfit-app/internal/github/api"
	"github.com/alexei-led/archfit-app/internal/wire/policy"
	"github.com/alexei-led/archfit-app/internal/wire/settings"
)

func TestManagedProposeCommitsToDraft(t *testing.T) {
	t.Parallel()
	fx := newFixture()
	var order []string
	fx.provider.complete = func(context.Context, Prompt) (Completion, error) {
		order = append(order, fmt.Sprintf("complete after %d reserve, %d settle", len(fx.budget.reserves), len(fx.budget.settles)))
		return Completion{Text: editedYAML, CostMicroUSD: testActualCost}, nil
	}

	res, err := fx.assist().Propose(context.Background(), ModeManaged, validRequest())
	if err != nil {
		t.Fatalf("Propose() error = %v", err)
	}

	if want := []string{"complete after 1 reserve, 0 settle"}; !reflect.DeepEqual(order, want) {
		t.Errorf("call order = %v, want %v", order, want)
	}
	if want := []int64{testMaxCost}; !reflect.DeepEqual(fx.budget.reserves, want) {
		t.Errorf("reserves = %v, want %v", fx.budget.reserves, want)
	}
	if len(fx.budget.settles) != 1 || fx.budget.settles[0].reserved != testMaxCost || fx.budget.settles[0].actual != testActualCost {
		t.Errorf("settles = %+v, want one Settle(%d, %d)", fx.budget.settles, testMaxCost, testActualCost)
	}
	for _, at := range fx.budget.times {
		if !at.Equal(testNow) {
			t.Errorf("budget called with time %v, want the injected clock %v", at, testNow)
		}
	}
	if len(fx.proposals.commits) != 1 {
		t.Fatalf("commits = %d, want 1", len(fx.proposals.commits))
	}
	got := fx.proposals.commits[0]
	if got.expectHead != headA {
		t.Errorf("expectHeadSHA = %q, want %q", got.expectHead, headA)
	}
	wantFiles := []api.FileChange{{Path: policy.PolicyPath, Content: []byte(editedYAML)}}
	if !reflect.DeepEqual(got.change.Files, wantFiles) {
		t.Errorf("files = %+v, want %+v", got.change.Files, wantFiles)
	}
	if got.change.Repo != testRepo.RepoRef || got.change.Branch != testBranch || got.change.Base != mainBranch {
		t.Errorf("change targets %+v %q base %q", got.change.Repo, got.change.Branch, got.change.Base)
	}
	if want := "archfit: AI proposal requested by @alice"; got.change.Message != want {
		t.Errorf("message = %q, want %q", got.change.Message, want)
	}
	if res.Mode != ModeManaged || res.Draft.HeadSHA != headB || res.Draft.Number != 7 || res.RunURL != "" {
		t.Errorf("result = %+v, want managed with the new draft head", res)
	}
}

func TestManagedPreconditions(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name  string
		setup func(*fixture)
		want  error
	}{
		{"provider nil", func(fx *fixture) { fx.noProvider = true }, ErrManagedUnavailable},
		{"consent missing", func(fx *fixture) { delete(fx.consents.granted, account.ConsentManagedLLM) }, account.ErrNoConsent},
		{"Has false because the Store fails", func(fx *fixture) { fx.consents.down = true }, account.ErrNoConsent},
		{"settings file missing", func(fx *fixture) { delete(fx.repos.files, fileKey{mainBranch, settings.Path}) }, ErrNotManaged},
		{"settings invalid", func(fx *fixture) {
			fx.repos.files[fileKey{mainBranch, settings.Path}] = []byte(managedSettings + "surprise: true\n")
		}, ErrNotManaged},
		{"data_mode customer", func(fx *fixture) {
			fx.repos.files[fileKey{mainBranch, settings.Path}] = []byte(customerSettings)
		}, ErrNotManaged},
		{"draft branch says managed, default branch says customer", func(fx *fixture) {
			fx.repos.files[fileKey{mainBranch, settings.Path}] = []byte(customerSettings)
			fx.repos.files[fileKey{testBranch, settings.Path}] = []byte(managedSettings)
			fx.repos.files[fileKey{headA, settings.Path}] = []byte(managedSettings)
		}, ErrNotManaged},
		{"no draft", func(fx *fixture) { delete(fx.proposals.drafts, testBranch) }, ErrNoDraft},
		{"merged draft", func(fx *fixture) {
			fx.proposals.drafts[testBranch] = proposal.Draft{Number: 7, State: proposal.DraftMerged, HeadSHA: headA}
		}, ErrNoDraft},
		{"policy missing", func(fx *fixture) { delete(fx.repos.files, fileKey{headA, policy.PolicyPath}) }, ErrNoDraft},
		{"policy of 17 KB", func(fx *fixture) {
			fx.repos.files[fileKey{headA, policy.PolicyPath}] = []byte("version: 1\n# " + strings.Repeat("x", 17<<10) + "\n")
		}, ErrPolicyTooLarge},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			fx := newFixture()
			tt.setup(fx)
			_, err := fx.assist().Propose(context.Background(), ModeManaged, validRequest())
			if !errors.Is(err, tt.want) {
				t.Fatalf("Propose() error = %v, want %v", err, tt.want)
			}
			if fx.budget.reserveCount() != 0 || fx.provider.callCount() != 0 || fx.proposals.attemptCount() != 0 {
				t.Errorf("reserve = %d, complete = %d, propose = %d; want none",
					fx.budget.reserveCount(), fx.provider.callCount(), fx.proposals.attemptCount())
			}
			for _, read := range fx.repos.reads {
				if read.path == settings.Path && read.ref != mainBranch {
					t.Errorf("settings read at %q, want only the default branch", read.ref)
				}
			}
		})
	}
}

func TestManagedPolicyAtTheLimitIsAccepted(t *testing.T) {
	t.Parallel()
	fx := newFixture()
	head := "version: 1\n# "
	fx.repos.files[fileKey{headA, policy.PolicyPath}] = []byte(head + strings.Repeat("x", 16<<10-len(head)-1) + "\n")
	if _, err := fx.assist().Propose(context.Background(), ModeManaged, validRequest()); err != nil {
		t.Fatalf("Propose() with a 16 KB policy error = %v", err)
	}
}

func TestCustomerProposeDispatchesWorkflow(t *testing.T) {
	t.Parallel()
	t.Run("dispatches", func(t *testing.T) {
		t.Parallel()
		fx := newFixture()
		fx.noProvider = true
		req := validRequest()
		res, err := fx.assist().Propose(context.Background(), ModeCustomer, req)
		if err != nil {
			t.Fatalf("Propose() error = %v", err)
		}
		want := []dispatchCall{{
			repo:         testRepo.RepoRef,
			workflowPath: ".github/workflows/archfit-assist.yaml",
			ref:          testBranch,
			inputs:       map[string]string{"branch": testBranch, "head_sha": headA, "instruction": req.Instruction},
		}}
		if !reflect.DeepEqual(fx.actions.calls, want) {
			t.Errorf("dispatch calls = %+v, want %+v", fx.actions.calls, want)
		}
		wantRes := Result{
			Mode:   ModeCustomer,
			Draft:  fx.proposals.draft(testBranch),
			RunURL: "https://github.com/acme/shop/actions/workflows/archfit-assist.yaml",
		}
		if res != wantRes {
			t.Errorf("result = %+v, want %+v", res, wantRes)
		}
		if fx.consents.calls != 0 || fx.budget.reserveCount() != 0 || len(fx.budget.settles) != 0 ||
			fx.provider.callCount() != 0 || len(fx.repos.reads) != 0 || fx.proposals.attemptCount() != 0 {
			t.Errorf("customer mode touched consent, budget, provider, files or wrote a commit")
		}
	})
	t.Run("workflow missing", func(t *testing.T) {
		t.Parallel()
		fx := newFixture()
		fx.actions.err = fmt.Errorf("dispatch: %w", api.ErrNotFound)
		_, err := fx.assist().Propose(context.Background(), ModeCustomer, validRequest())
		if !errors.Is(err, ErrNoAssistWorkflow) {
			t.Fatalf("Propose() error = %v, want ErrNoAssistWorkflow", err)
		}
	})
}

func TestErrorsAreDistinguishable(t *testing.T) {
	t.Parallel()
	sentinels := []error{
		ErrInvalidRequest, ErrNoDraft, proposal.ErrStale, ErrManagedUnavailable, account.ErrNoConsent, ErrNotManaged,
		ErrPolicyTooLarge, ErrBusy, account.ErrBudgetExhausted, ErrProvider, ErrInvalidProposal, ErrNoAssistWorkflow,
		api.ErrRateLimited,
	}
	tests := []struct {
		name  string
		mode  Mode
		setup func(*fixture, *Request)
		want  error
	}{
		{"invalid request", ModeManaged, func(_ *fixture, r *Request) { r.By = "" }, ErrInvalidRequest},
		{"no draft", ModeCustomer, func(fx *fixture, _ *Request) { delete(fx.proposals.drafts, testBranch) }, ErrNoDraft},
		{"closed draft", ModeCustomer, func(fx *fixture, _ *Request) {
			fx.proposals.drafts[testBranch] = proposal.Draft{State: proposal.DraftClosed, HeadSHA: headA}
		}, ErrNoDraft},
		{"branch not found", ModeCustomer, func(fx *fixture, _ *Request) {
			fx.proposals.getErr = fmt.Errorf("get: %w", api.ErrNotFound)
		}, ErrNoDraft},
		{"stale at request time", ModeCustomer, func(_ *fixture, r *Request) { r.HeadSHA = headC }, proposal.ErrStale},
		{"stale at commit time", ModeManaged, func(fx *fixture, _ *Request) {
			fx.proposals.proposeErr = fmt.Errorf("propose: %w", proposal.ErrStale)
		}, proposal.ErrStale},
		{"managed unavailable", ModeManaged, func(fx *fixture, _ *Request) { fx.noProvider = true }, ErrManagedUnavailable},
		{"no consent", ModeManaged, func(fx *fixture, _ *Request) { fx.consents.down = true }, account.ErrNoConsent},
		{"not managed", ModeManaged, func(fx *fixture, _ *Request) {
			delete(fx.repos.files, fileKey{mainBranch, settings.Path})
		}, ErrNotManaged},
		{"policy too large", ModeManaged, func(fx *fixture, _ *Request) {
			fx.repos.files[fileKey{headA, policy.PolicyPath}] = []byte(strings.Repeat("# x\n", 5<<10))
		}, ErrPolicyTooLarge},
		{"busy", ModeManaged, nil, ErrBusy},
		{"budget exhausted", ModeManaged, func(fx *fixture, _ *Request) {
			fx.budget.reserveErr = fmt.Errorf("reserve: %w", account.ErrBudgetExhausted)
		}, account.ErrBudgetExhausted},
		{"provider failed", ModeManaged, func(fx *fixture, _ *Request) {
			fx.provider.complete = func(context.Context, Prompt) (Completion, error) {
				return Completion{}, errors.New("vendor said 529")
			}
		}, ErrProvider},
		{"provider timed out", ModeManaged, func(fx *fixture, _ *Request) {
			fx.provider.complete = func(context.Context, Prompt) (Completion, error) {
				return Completion{CostMicroUSD: testMaxCost}, context.DeadlineExceeded
			}
		}, ErrProvider},
		{"invalid proposal", ModeManaged, func(fx *fixture, _ *Request) {
			fx.provider.complete = answers(draftYAML, testActualCost)
		}, ErrInvalidProposal},
		{"no assist workflow", ModeCustomer, func(fx *fixture, _ *Request) { fx.actions.err = api.ErrNotFound }, ErrNoAssistWorkflow},
		{"rate limited reading settings", ModeManaged, func(fx *fixture, _ *Request) {
			fx.repos.errs[fileKey{mainBranch, settings.Path}] = fmt.Errorf("file: %w", api.ErrRateLimited)
		}, api.ErrRateLimited},
		{"rate limited reading the policy", ModeManaged, func(fx *fixture, _ *Request) {
			fx.repos.errs[fileKey{headA, policy.PolicyPath}] = fmt.Errorf("file: %w", api.ErrRateLimited)
		}, api.ErrRateLimited},
		{"rate limited reading the draft", ModeCustomer, func(fx *fixture, _ *Request) {
			fx.proposals.getErr = fmt.Errorf("get: %w", api.ErrRateLimited)
		}, api.ErrRateLimited},
		{"rate limited dispatching", ModeCustomer, func(fx *fixture, _ *Request) {
			fx.actions.err = fmt.Errorf("dispatch: %w", api.ErrRateLimited)
		}, api.ErrRateLimited},
		{"rate limited committing", ModeManaged, func(fx *fixture, _ *Request) {
			fx.proposals.proposeErr = fmt.Errorf("propose: %w", api.ErrRateLimited)
		}, api.ErrRateLimited},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			fx := newFixture()
			req := validRequest()
			var err error
			if tt.name == "busy" {
				err = busyError(t, fx)
			} else {
				tt.setup(fx, &req)
				_, err = fx.assist().Propose(context.Background(), tt.mode, req)
			}
			if err == nil {
				t.Fatalf("Propose() error = nil, want %v", tt.want)
			}
			for _, s := range sentinels {
				if got, want := errors.Is(err, s), s == tt.want; got != want {
					t.Errorf("errors.Is(%q, %q) = %v, want %v", err, s, got, want)
				}
			}
			if errors.Is(err, api.ErrNotFound) {
				t.Errorf("error %q still matches api.ErrNotFound", err)
			}
		})
	}
}

// busyError returns what a second request gets while the first one waits for the provider.
func busyError(t *testing.T, fx *fixture) error {
	t.Helper()
	started, release := make(chan struct{}), make(chan struct{})
	fx.provider.complete = func(context.Context, Prompt) (Completion, error) {
		close(started)
		<-release
		return Completion{Text: editedYAML, CostMicroUSD: testActualCost}, nil
	}
	a := fx.assist()
	done := make(chan error, 1)
	go func() {
		_, err := a.Propose(context.Background(), ModeManaged, validRequest())
		done <- err
	}()
	<-started
	_, busy := a.Propose(context.Background(), ModeManaged, validRequest())
	close(release)
	if err := <-done; err != nil {
		t.Errorf("first request error = %v", err)
	}
	return busy
}

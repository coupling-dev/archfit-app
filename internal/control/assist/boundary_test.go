package assist

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/alexei-led/archfit-app/internal/control/account"
	"github.com/alexei-led/archfit-app/internal/wire/policy"
)

func TestBudgetProtocol(t *testing.T) {
	storeDown := errors.New("store: unavailable")
	tests := []struct {
		name        string
		setup       func(fx *fixture, cancel context.CancelFunc)
		want        error
		wantCalls   int
		wantSettled int64 // actual cost of the one Settle; ignored when wantCalls is 0
		wantLog     bool
	}{
		{"reserve returns ErrBudgetExhausted", func(fx *fixture, _ context.CancelFunc) {
			fx.budget.reserveErr = account.ErrBudgetExhausted
		}, account.ErrBudgetExhausted, 0, 0, false},
		{"reserve returns a Store error", func(fx *fixture, _ context.CancelFunc) {
			fx.budget.reserveErr = storeDown
		}, storeDown, 0, 0, false},
		{"provider fails with cost 0", func(fx *fixture, _ context.CancelFunc) {
			fx.provider.complete = func(context.Context, Prompt) (Completion, error) {
				return Completion{}, errors.New("rejected")
			}
		}, ErrProvider, 1, 0, false},
		{"provider times out with cost = max", func(fx *fixture, _ context.CancelFunc) {
			fx.provider.complete = func(context.Context, Prompt) (Completion, error) {
				return Completion{CostMicroUSD: testMaxCost}, context.DeadlineExceeded
			}
		}, ErrProvider, 1, testMaxCost, false},
		{"provider succeeds but the answer is invalid", func(fx *fixture, _ context.CancelFunc) {
			fx.provider.complete = answers("I cannot help with that.", testActualCost)
		}, ErrInvalidProposal, 1, testActualCost, false},
		{"request context cancelled during Complete", func(fx *fixture, cancel context.CancelFunc) {
			fx.provider.complete = func(ctx context.Context, _ Prompt) (Completion, error) {
				cancel()
				<-ctx.Done()
				return Completion{CostMicroUSD: testMaxCost}, ctx.Err()
			}
		}, ErrProvider, 1, testMaxCost, false},
		{"settle fails", func(fx *fixture, _ context.CancelFunc) {
			fx.budget.settleErr = storeDown
			fx.provider.complete = answers("I cannot help with that.", testActualCost)
		}, ErrInvalidProposal, 1, testActualCost, true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			logs := captureLogs(t)
			fx := newFixture()
			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()
			tt.setup(fx, cancel)

			_, err := fx.assist().Propose(ctx, ModeManaged, validRequest())

			if !errors.Is(err, tt.want) {
				t.Fatalf("Propose() error = %v, want %v", err, tt.want)
			}
			if errors.Is(err, storeDown) != (tt.want == storeDown) {
				t.Errorf("error %q must not carry the settle failure", err)
			}
			if got := fx.provider.callCount(); got != tt.wantCalls {
				t.Errorf("provider calls = %d, want %d", got, tt.wantCalls)
			}
			if len(fx.budget.settles) != tt.wantCalls {
				t.Fatalf("settles = %+v, want %d", fx.budget.settles, tt.wantCalls)
			}
			if tt.wantCalls == 1 {
				s := fx.budget.settles[0]
				if s.reserved != testMaxCost || s.actual != tt.wantSettled {
					t.Errorf("Settle(%d, %d), want Settle(%d, %d)", s.reserved, s.actual, testMaxCost, tt.wantSettled)
				}
				if s.ctxErr != nil || !s.hasDeadline {
					t.Errorf("settle context: err = %v, deadline = %v; want live with its own timeout", s.ctxErr, s.hasDeadline)
				}
			}
			if got := strings.Contains(logs.String(), "settle budget failed"); got != tt.wantLog {
				t.Errorf("settle failure logged = %v, want %v", got, tt.wantLog)
			}
			if fx.proposals.attemptCount() != 0 {
				t.Errorf("Proposals.Propose called %d times, want 0", fx.proposals.attemptCount())
			}
		})
	}
}

func TestSettleUsesTheReserveInstant(t *testing.T) {
	t.Parallel()
	var mu sync.Mutex
	clock := time.Date(2026, 9, 30, 23, 59, 30, 0, time.UTC)
	fx := newFixture()
	fx.now = func() time.Time {
		mu.Lock()
		defer mu.Unlock()
		return clock
	}
	fx.provider.complete = func(context.Context, Prompt) (Completion, error) {
		mu.Lock()
		clock = clock.Add(time.Minute)
		mu.Unlock()
		return Completion{Text: editedYAML, CostMicroUSD: testActualCost}, nil
	}

	if _, err := fx.assist().Propose(context.Background(), ModeManaged, validRequest()); err != nil {
		t.Fatalf("Propose() error = %v", err)
	}

	if len(fx.budget.times) != 2 || !fx.budget.times[0].Equal(fx.budget.times[1]) {
		t.Fatalf("budget instants = %v, want Reserve and Settle at the same instant", fx.budget.times)
	}
	if got := fx.budget.months["2026-09"]; got != testActualCost {
		t.Errorf("September counter = %d, want the actual cost %d", got, testActualCost)
	}
	if got, touched := fx.budget.months["2026-10"]; touched {
		t.Errorf("October counter = %d, want it untouched", got)
	}
}

func TestRawOutputNeverLeaves(t *testing.T) {
	const marker = "RAW-OUTPUT-MARKER-c41d"
	validAnswer := editedYAML + "# " + marker + "\n"
	tests := []struct {
		name      string
		text      string
		want      error
		wantFiles string
	}{
		{"valid YAML", "```yaml\n" + validAnswer + "```", nil, validAnswer},
		{"prose", "I will not do that, " + marker + " is what I think instead.", ErrInvalidProposal, ""},
		{"oversize blob", "version: 1\n# " + strings.Repeat(marker, 70<<10/len(marker)) + "\n", ErrInvalidProposal, ""},
		{"provider error quoting the output", marker, ErrProvider, ""},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			logs := captureLogs(t)
			fx := newFixture()
			fx.provider.complete = answers(tt.text, testActualCost)
			if tt.want == ErrProvider {
				fx.provider.complete = func(context.Context, Prompt) (Completion, error) {
					return Completion{Text: tt.text, CostMicroUSD: testActualCost}, errors.New("unusable answer: " + tt.text)
				}
			}

			_, err := fx.assist().Propose(context.Background(), ModeManaged, validRequest())

			if !errors.Is(err, tt.want) {
				t.Fatalf("Propose() error = %v, want %v", err, tt.want)
			}
			if err != nil && strings.Contains(err.Error(), marker) {
				t.Errorf("error carries raw model output: %q", err)
			}
			if strings.Contains(logs.String(), marker) {
				t.Errorf("log carries raw model output")
			}
			if len(fx.actions.calls) != 0 {
				t.Errorf("dispatch calls = %d, want 0", len(fx.actions.calls))
			}
			if tt.wantFiles == "" {
				if fx.proposals.attemptCount() != 0 {
					t.Errorf("Proposals.Propose called for a rejected answer")
				}
				return
			}
			if len(fx.proposals.commits) != 1 {
				t.Fatalf("commits = %d, want 1", len(fx.proposals.commits))
			}
			c := fx.proposals.commits[0].change
			if len(c.Files) != 1 || c.Files[0].Path != policy.PolicyPath || string(c.Files[0].Content) != tt.wantFiles {
				t.Errorf("files = %+v, want only %s with the normalized answer", c.Files, policy.PolicyPath)
			}
			if strings.Contains(c.Message+c.Title+c.Body+c.Branch+c.Base, marker) {
				t.Errorf("raw model output outside the file content: %+v", c)
			}
		})
	}
}

func TestForeignAnswerIsNotAProposal(t *testing.T) {
	const marker = "FOREIGN-ANSWER-MARKER-9f3a"
	validPolicy := "version: 2\nmodules:\n  core:\n    path: internal/core\n# " + marker + "\n"
	tests := []struct {
		name      string
		text      string
		want      error
		wantFiles string
	}{
		{"one-line prose with a colon", "Sure! Here is the file: " + marker, ErrInvalidProposal, ""},
		{"JSON object with foreign keys", `{"answer": "` + marker + `", "ok": true}`, ErrInvalidProposal, ""},
		{"mapping without version", "modules:\n  core:\n    path: " + marker + "\n", ErrInvalidProposal, ""},
		{"one unknown top-level key", "version: 2\nmodules: {}\nextra: " + marker + "\n", ErrInvalidProposal, ""},
		{"valid edited policy", validPolicy, nil, validPolicy},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			logs := captureLogs(t)
			fx := newFixture()
			fx.provider.complete = answers(tt.text, testActualCost)

			_, err := fx.assist().Propose(context.Background(), ModeManaged, validRequest())

			if !errors.Is(err, tt.want) {
				t.Fatalf("Propose() error = %v, want %v", err, tt.want)
			}
			if err != nil && strings.Contains(err.Error(), marker) {
				t.Errorf("error carries raw model output: %q", err)
			}
			if strings.Contains(logs.String(), marker) {
				t.Errorf("log carries raw model output")
			}
			if len(fx.budget.settles) != 1 {
				t.Fatalf("settles = %+v, want 1", fx.budget.settles)
			}
			if s := fx.budget.settles[0]; s.reserved != testMaxCost || s.actual != testActualCost {
				t.Errorf("Settle(%d, %d), want Settle(%d, %d)", s.reserved, s.actual, testMaxCost, testActualCost)
			}
			if tt.wantFiles == "" {
				if got := fx.proposals.attemptCount(); got != 0 {
					t.Errorf("Proposals.Propose called %d times, want 0", got)
				}
				return
			}
			if len(fx.proposals.commits) != 1 {
				t.Fatalf("commits = %d, want 1", len(fx.proposals.commits))
			}
			c := fx.proposals.commits[0].change
			if len(c.Files) != 1 || c.Files[0].Path != policy.PolicyPath || string(c.Files[0].Content) != tt.wantFiles {
				t.Errorf("files = %+v, want only %s with the answer", c.Files, policy.PolicyPath)
			}
		})
	}
}

func TestInstructionIsNotLogged(t *testing.T) {
	const marker = "INSTRUCTION-MARKER-7be2"
	for _, mode := range []Mode{ModeManaged, ModeCustomer} {
		t.Run(string(mode), func(t *testing.T) {
			logs := captureLogs(t)
			fx := newFixture()
			req := validRequest()
			req.Instruction = "forbid cycles " + marker

			if _, err := fx.assist().Propose(context.Background(), mode, req); err != nil {
				t.Fatalf("Propose() error = %v", err)
			}

			if strings.Contains(logs.String(), marker) {
				t.Errorf("log carries the instruction text")
			}
			var line struct {
				Mode             string `json:"mode"`
				Outcome          string `json:"outcome"`
				InstructionBytes int    `json:"instruction_bytes"`
				InstallationID   int64  `json:"installation_id"`
				RepoID           int64  `json:"repo_id"`
			}
			if err := json.Unmarshal(bytes.TrimSpace(logs.Bytes()), &line); err != nil {
				t.Fatalf("want exactly one JSON log line, got %q: %v", logs.String(), err)
			}
			if line.InstructionBytes != len(req.Instruction) || line.Mode != string(mode) || line.Outcome != "ok" ||
				line.InstallationID != testRepo.InstallationID || line.RepoID != testRepo.ID {
				t.Errorf("log line = %+v", line)
			}
			if mode == ModeManaged {
				c := fx.proposals.commits[0].change
				if strings.Contains(c.Message+c.Title+c.Body, marker) {
					t.Errorf("instruction text in the commit or pull request text")
				}
				return
			}
			if len(fx.actions.calls) != 1 || fx.actions.calls[0].inputs["instruction"] != req.Instruction {
				t.Errorf("dispatch calls = %+v, want the instruction as an input", fx.actions.calls)
			}
			if fx.provider.callCount() != 0 || fx.proposals.attemptCount() != 0 {
				t.Errorf("customer mode sent the instruction elsewhere")
			}
		})
	}
}

func TestOneRequestPerBranch(t *testing.T) {
	t.Parallel()
	fx := newFixture()
	started, release := make(chan struct{}), make(chan struct{})
	fx.provider.complete = func(context.Context, Prompt) (Completion, error) {
		started <- struct{}{}
		<-release
		return Completion{Text: editedYAML, CostMicroUSD: testActualCost}, nil
	}
	a := fx.assist()
	other := validRequest()
	other.Branch = otherBranch
	run := func(r Request) <-chan error {
		done := make(chan error, 1)
		go func() {
			_, err := a.Propose(context.Background(), ModeManaged, r)
			done <- err
		}()
		return done
	}

	first := run(validRequest())
	<-started
	if _, err := a.Propose(context.Background(), ModeManaged, validRequest()); !errors.Is(err, ErrBusy) {
		t.Fatalf("second request error = %v, want ErrBusy", err)
	}
	if got := fx.budget.reserveCount(); got != 1 {
		t.Errorf("reserves after the busy request = %d, want 1", got)
	}
	third := run(other)
	<-started
	close(release)
	if err := <-first; err != nil {
		t.Errorf("first request error = %v", err)
	}
	if err := <-third; err != nil {
		t.Errorf("third request (other branch) error = %v", err)
	}

	again := validRequest()
	again.HeadSHA = fx.proposals.draft(testBranch).HeadSHA
	fx.repos.mu.Lock()
	fx.repos.files[fileKey{again.HeadSHA, policy.PolicyPath}] = []byte(draftYAML)
	fx.repos.mu.Unlock()
	fx.provider.complete = answers(editedYAML, testActualCost)
	if _, err := a.Propose(context.Background(), ModeManaged, again); err != nil {
		t.Errorf("request after the first ended error = %v, want accepted", err)
	}
}

func TestNeverWritesOutsideDraftBranches(t *testing.T) {
	t.Parallel()
	fx := newFixture()
	a := fx.assist()
	onDefault := validRequest()
	onDefault.Branch = mainBranch
	onFeature := validRequest()
	onFeature.Branch = "feature/limits"
	lookalike := validRequest()
	lookalike.Branch = "archfit"

	for _, mode := range []Mode{ModeManaged, ModeCustomer} {
		for _, r := range []Request{onDefault, onFeature, lookalike} {
			if _, err := a.Propose(context.Background(), mode, r); !errors.Is(err, ErrInvalidRequest) {
				t.Errorf("%s on %q: error = %v, want ErrInvalidRequest", mode, r.Branch, err)
			}
		}
	}
	if fx.proposals.attemptCount() != 0 || len(fx.actions.calls) != 0 {
		t.Fatalf("a rejected branch reached GitHub")
	}

	if _, err := a.Propose(context.Background(), ModeCustomer, validRequest()); err != nil {
		t.Fatalf("customer Propose() error = %v", err)
	}
	if _, err := a.Propose(context.Background(), ModeManaged, validRequest()); err != nil {
		t.Fatalf("managed Propose() error = %v", err)
	}

	if len(fx.proposals.commits) != 1 {
		t.Fatalf("commits = %d, want 1 (managed only)", len(fx.proposals.commits))
	}
	for _, c := range fx.proposals.commits {
		if !strings.HasPrefix(c.change.Branch, "archfit/") || c.change.Branch == mainBranch || c.expectHead == "" {
			t.Errorf("write to %q with expected head %q", c.change.Branch, c.expectHead)
		}
	}
	for _, d := range fx.actions.calls {
		if !strings.HasPrefix(d.ref, "archfit/") {
			t.Errorf("dispatch on %q", d.ref)
		}
	}
	if fx.proposals.otherCalls != 0 {
		t.Errorf("Ready or SetBody called %d times, want 0", fx.proposals.otherCalls)
	}
}

// Package assist proposes policy edits through a managed LLM provider or the customer's CI.
package assist

import (
	"context"
	"time"

	"github.com/alexei-led/archfit-app/internal/control/account"
	"github.com/alexei-led/archfit-app/internal/control/proposal"
	"github.com/alexei-led/archfit-app/internal/github/api"
)

// Provider is the LLM seam. Implementations own endpoint, model choice, and pricing.
type Provider interface {
	// MaxCostMicroUSD is the worst-case cost of one Complete call for p; used to reserve budget.
	MaxCostMicroUSD(p Prompt) int64
	Complete(ctx context.Context, p Prompt) (Completion, error)
}

type Prompt struct {
	System    string
	User      string
	MaxTokens int
}

type Completion struct {
	Text         string
	CostMicroUSD int64
}

type Mode string

const (
	ModeManaged  Mode = "managed"  // App calls the Provider after consent, under the budget
	ModeCustomer Mode = "customer" // App dispatches the assist workflow in the customer's CI
)

// Request is an explicit user request. Context is limited to the draft policy and the instruction.
type Request struct {
	Repo        api.Repo
	Branch      string // the draft branch being edited
	HeadSHA     string // branch head the user was looking at; binds the proposal
	Instruction string
	By          string // login
}

type Result struct {
	Mode   Mode
	Draft  proposal.Draft // managed: the draft PR now carrying the proposal commit
	RunURL string         // customer: the dispatched workflow's runs page
}

type Assist interface {
	// Propose never publishes: the result is always a reviewable diff on the draft PR.
	Propose(ctx context.Context, mode Mode, r Request) (Result, error)
}

// AssistWorkflowPath is the customer-mode workflow dispatched with inputs {branch, head_sha, instruction}.
const AssistWorkflowPath = ".github/workflows/archfit-assist.yaml"

type Deps struct {
	GitHub    *api.Client
	Provider  Provider // nil disables managed mode
	Consents  account.Consents
	Budget    account.Budget
	Proposals proposal.Proposals
	Now       func() time.Time
}

func New(d Deps) Assist {
	return nil
}

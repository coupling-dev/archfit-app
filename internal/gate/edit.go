package gate

import (
	"context"

	"github.com/coupling-dev/archfit-app/internal/gate/proposals"
)

// Edits carries a whole-file owner answer through the same policy proposal.
type Edits interface {
	Editor(context.Context, Target) (EditBase, error)
	ReloadEditor(context.Context, Target, AnswerOwner) (EditBase, error)
	EditPreview(context.Context, Target, EditInput) (EditView, error)
	Edit(context.Context, Target, EditInput) (AnswerOutcome, error)
	// MakeRequiredPreview and MakeRequired set gate to required in the policy the App reads
	// itself: no client bytes name the policy, and the passkey binds the digest the App computed.
	MakeRequiredPreview(context.Context, Target, AnswerOwner) (EditView, error)
	MakeRequired(context.Context, Target, RequiredInput) (AnswerOutcome, error)
}

type (
	EditInput     = proposals.EditInput
	EditView      = proposals.EditView
	EditBase      = proposals.EditorBase
	RequiredInput = proposals.RequiredInput
)

// ActionRequired is the passkey step-up action of MakeRequired.
const ActionRequired = proposals.ActionMakeRequired

const MaxEditBytes = proposals.MaxEditBytes

var (
	ErrEditChanged = proposals.ErrEditChanged
	ErrEditPolicy  = proposals.ErrEditPolicy
	ErrEditSize    = proposals.ErrEditSize
	// ErrEditUnchanged refuses an edit that equals the open proposal's file.
	ErrEditUnchanged = proposals.ErrEditUnchanged
)

func (g *Gate) Editor(ctx context.Context, t Target) (EditBase, error) {
	if err := g.admitted(ctx, t); err != nil {
		return EditBase{}, err
	}
	return g.proposals.Editor(ctx, t.Repo)
}

func (g *Gate) ReloadEditor(ctx context.Context, t Target, owner AnswerOwner) (EditBase, error) {
	if err := g.admitted(ctx, t); err != nil {
		return EditBase{}, err
	}
	return g.proposals.ReloadEditor(ctx, t.Repo, owner)
}

func (g *Gate) EditPreview(ctx context.Context, t Target, in EditInput) (EditView, error) {
	if err := g.admitted(ctx, t); err != nil {
		return EditView{}, err
	}
	return g.proposals.EditPreview(ctx, t.Repo, in)
}

func (g *Gate) Edit(ctx context.Context, t Target, in EditInput) (AnswerOutcome, error) {
	if err := g.admitted(ctx, t); err != nil {
		return AnswerOutcome{}, err
	}
	return g.proposals.Edit(ctx, t.Repo, in)
}

func (g *Gate) MakeRequiredPreview(ctx context.Context, t Target, owner AnswerOwner) (EditView, error) {
	if err := g.admitted(ctx, t); err != nil {
		return EditView{}, err
	}
	return g.proposals.MakeRequiredPreview(ctx, t.Repo, owner)
}

func (g *Gate) MakeRequired(ctx context.Context, t Target, in RequiredInput) (AnswerOutcome, error) {
	if err := g.admitted(ctx, t); err != nil {
		return AnswerOutcome{}, err
	}
	return g.proposals.MakeRequired(ctx, t.Repo, in.Owner, in.Redeem)
}

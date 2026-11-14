package gate

import (
	"context"

	"github.com/alexei-led/archfit-app/internal/gate/proposals"
)

// Edits carries a whole-file owner answer through the same policy proposal.
type Edits interface {
	Editor(context.Context, Target) (EditBase, error)
	ReloadEditor(context.Context, Target, AnswerOwner) (EditBase, error)
	EditPreview(context.Context, Target, EditInput) (EditView, error)
	Edit(context.Context, Target, EditInput) (AnswerOutcome, error)
}

type (
	EditInput = proposals.EditInput
	EditView  = proposals.EditView
	EditBase  = proposals.EditorBase
)

const MaxEditBytes = proposals.MaxEditBytes

var (
	ErrEditChanged = proposals.ErrEditChanged
	ErrEditPolicy  = proposals.ErrEditPolicy
	ErrEditSize    = proposals.ErrEditSize
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

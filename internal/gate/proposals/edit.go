package proposals

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"slices"
	"strings"

	"github.com/coupling-dev/archfit-app/internal/gate/records"
	"github.com/coupling-dev/archfit-app/internal/gate/requests"
	"github.com/coupling-dev/archfit-app/internal/github/api"
	"github.com/coupling-dev/archfit-app/internal/wire/policy3"
)

// MaxEditBytes leaves room for base64 content and metadata in one 64 KiB approval.
const MaxEditBytes = 32 << 10

var (
	ErrEditChanged = errors.New("the policy changed; reload")
	ErrEditPolicy  = errors.New("the policy has errors")
	ErrEditSize    = errors.New("the policy exceeds the 32 KiB edit limit")
	// ErrEditUnchanged refuses an edit whose file is the open proposal's: it would record an approval and a note
	// with no commit.
	ErrEditUnchanged = errors.New("Nothing changed against the open policy pull request.") //nolint:staticcheck // shown as is
)

// What a preview's diff is against.
const (
	AgainstProposal      = "the open policy pull request"
	AgainstDefaultBranch = "the default branch"
)

// EditInput is an owner's whole-file answer. Base binds the bytes it started
// from; Target binds the preview's protected tip; Redeem binds the NEW digest.
type EditInput struct {
	File         []byte
	Base, Target string
	Owner        Owner
	Later        bool
	// AsIs approves the open proposal's file as it is: File must be that file, and the approval replaces the ones a
	// move of the default branch voided, or merges a proposal GitHub refused.
	AsIs   bool
	Redeem func(context.Context, string) (string, error)
}

// EditView is a read-only preview; errors prevent the passkey step.
type EditView struct {
	Digest, Target, Diff string
	Against              string // AgainstProposal or AgainstDefaultBranch: what Diff compares the file with
	Errors               []string
}

// EditPreview validates before the passkey step, without cleanup or any writes.
func (s *Service) EditPreview(ctx context.Context, r api.Repo, in EditInput) (EditView, error) {
	if len(in.File) > MaxEditBytes {
		return EditView{}, ErrEditSize
	}
	problems := policy3.Diagnostics(in.File)
	if len(problems) > 0 {
		return EditView{Errors: problems}, nil
	}
	tip, base, cur, err := s.editBase(ctx, r, in)
	if err != nil {
		return EditView{}, err
	}
	against := AgainstDefaultBranch
	if cur != nil {
		against = AgainstProposal
	}
	return EditView{Digest: policy3.Digest(in.File), Target: tip, Diff: requests.Diff(policyPath, base, in.File),
		Against: against, Errors: []string{}}, nil
}

// editBase reads only recorded proposal inputs and protected target owners.
func (s *Service) editBase(ctx context.Context, r api.Repo, in EditInput) (string, []byte, *proposal, error) {
	tip, err := s.gh.BranchHead(ctx, r.RepoRef, r.DefaultBranch)
	if err != nil {
		return "", nil, nil, err
	}
	raw, pol, err := s.policyAt(ctx, r.RepoRef, tip)
	if err != nil {
		return "", nil, nil, err
	}
	cur, err := s.readCurrent(ctx, r, false)
	if err != nil {
		return "", nil, nil, err
	}
	base := raw
	if cur != nil {
		pull, err := s.gh.Pull(ctx, r.RepoRef, cur.rec.PullRequest)
		if err != nil {
			return "", nil, nil, err
		}
		head, err := s.gh.BranchHead(ctx, r.RepoRef, Branch)
		if err != nil {
			return "", nil, nil, err
		}
		if head != cur.rec.HeadSHA || pull.HeadSHA != head || pull.HeadRef != Branch ||
			!strings.EqualFold(pull.HeadRepo, r.FullName) || pull.BaseRef != r.DefaultBranch {
			return "", nil, nil, ErrEditChanged
		}
		base, _, err = s.gh.File(ctx, r.RepoRef, head, policyPath)
		if err != nil {
			return "", nil, nil, err
		}
		if policy3.Digest(base) != cur.rec.ExpectedDigest {
			return "", nil, nil, ErrEditChanged
		}
	}
	if in.Base != policy3.Digest(base) || in.Target != "" && in.Target != tip {
		return "", nil, nil, ErrEditChanged
	}
	if err := s.controlled(ctx, r, raw); err != nil {
		return "", nil, nil, err
	}
	if !slices.ContainsFunc(pol.Owners, func(o string) bool { return in.Owner.Login != "" && strings.EqualFold(o, in.Owner.Login) }) {
		return "", nil, nil, ErrNotOwner
	}
	if cur != nil && len(cur.approvals) >= records.MaxApprovals {
		return "", nil, nil, ErrFull
	}
	return tip, base, cur, nil
}

// Edit appends the replacement last to the ordinary proposal and approval store.
func (s *Service) Edit(ctx context.Context, r api.Repo, in EditInput) (Outcome, error) {
	if len(in.File) > MaxEditBytes {
		return Outcome{}, ErrEditSize
	}
	if problems := policy3.Diagnostics(in.File); len(problems) > 0 {
		return Outcome{}, fmt.Errorf("%w:\n%s", ErrEditPolicy, strings.Join(problems, "\n"))
	}
	release, err := s.hold(ctx, r)
	if err != nil {
		return Outcome{}, err
	}
	defer release()
	ctx, cancel := context.WithTimeout(ctx, recordTimeout)
	defer cancel()
	if in.Target == "" {
		return Outcome{}, ErrEditChanged
	}
	_, base, cur, err := s.editBase(ctx, r, in)
	if err != nil {
		return Outcome{}, err
	}
	switch same := cur != nil && bytes.Equal(in.File, base); {
	case in.AsIs && !same:
		return Outcome{}, ErrEditChanged
	case !in.AsIs && same:
		return Outcome{}, ErrEditUnchanged
	}
	if in.Redeem == nil {
		return Outcome{}, ErrNeedsPasskey
	}
	credential, err := in.Redeem(ctx, policy3.Digest(in.File))
	if err != nil {
		return Outcome{}, err
	}
	// A valid token precedes cleanup: an invalid or stale submission writes nothing.
	// Closed proposals still need the same request reopening as ordinary answers.
	if _, err := s.current(ctx, r); err != nil {
		return Outcome{}, err
	}
	tip, _, cur, err := s.editBase(ctx, r, in)
	if err != nil {
		return Outcome{}, err
	}
	if cur != nil {
		if err := s.voidAndNote(ctx, r, cur.rec.PullRequest, cur.stale, false); err != nil {
			return Outcome{}, err
		}
	}
	edit := records.Edit{File: in.File, Base: in.Base, Target: tip}
	a := records.Approval{Kind: records.KindEdit, Owner: in.Owner.ID, Login: in.Owner.Login,
		Credential: credential, At: s.now().UTC()}
	id := records.NewID()
	var listed []approved
	if cur != nil {
		listed = slices.Clone(cur.approvals)
	}
	listed = append(listed, approved{id: id, rec: a, edit: &edit})
	pin, err := s.write(ctx, r, cur, tip, in.File, listed)
	if err != nil {
		return Outcome{}, err
	}
	if err := records.EditPolicy(ctx, s.store, tenant(r), r.FullName, id, a, edit, *pin); err != nil {
		return Outcome{}, err
	}
	after, done := context.WithTimeout(context.WithoutCancel(ctx), afterTimeout)
	defer done()
	if cur != nil && cur.rec.HeadSHA != pin.HeadSHA {
		s.retire(after, r, cur.rec.HeadSHA, cur.rec.PullRequest, cur.id, RetiredSuperseded)
	}
	out := Outcome{Approval: id, PullRequest: pin.PullRequest}
	out.State, out.Merge, out.Reason = s.publish(after, r, tip, *pin, listed, in.Later)
	note := editNote(in.Owner.Login, credential, in.AsIs)
	if out.State == records.StateVoid {
		note += "the policy changed; reload. The edit's approval is void.\n"
	}
	if _, err := s.gh.CreateComment(after, r.RepoRef, out.PullRequest, note); err != nil {
		logFailure(after, r, "note the edit", err)
	}
	return out, nil
}

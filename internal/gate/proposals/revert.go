package proposals

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"slices"
	"strings"

	"github.com/alexei-led/archfit-app/internal/gate/records"
	"github.com/alexei-led/archfit-app/internal/github/api"
	"github.com/alexei-led/archfit-app/internal/wire/policy3"
)

var (
	// ErrNotOutside refuses a revert while the policy did not change outside the flow: there is nothing to revert.
	ErrNotOutside = errors.New("proposals: the policy did not change outside the flow")
	// ErrNothingToRevert refuses a revert when the default branch holds what the revert would write: an owner confirms
	// it instead.
	ErrNothingToRevert = errors.New("proposals: the default branch already holds the approved policy; confirm it")
	// ErrMoved refuses a revert whose change is not the one the owner's step-up was bound to: the state moved since the
	// owner looked.
	ErrMoved = errors.New("proposals: the revert moved since the owner looked")
)

// RevertInput is an owner's revert of the policy changed outside the flow (design 7.1, 9.2; M2 task 23).
type RevertInput struct {
	Owner Owner
	// Digest is the change digest the owner's step-up is bound to (RevertDigest): the revert the owner saw.
	Digest string
	Later  bool // merge later: the policy pull request stays open
	// Redeem consumes the step-up token for the digest and returns the passkey id. It is called after every other
	// check passed and before anything is written.
	Redeem func(ctx context.Context, digest string) (passkeyID string, err error)
}

// RevertDigest is the change digest of a revert of r's policy now: what the owner's passkey step-up is bound to.
func (s *Service) RevertDigest(ctx context.Context, r api.Repo) (string, error) {
	rev, err := s.revertOf(ctx, r)
	if err != nil {
		return "", err
	}
	return revertDigest(rev), nil
}

// Revert carries an owner's revert of the policy changed outside the flow to the default branch: the App's policy
// pull request restores the last approved policy, with the open answers it holds written on it again, and its merge
// push ends the state (records.Consume). Only an owner the approved policy names reverts: the changed policy could
// name anyone. As for an answer, an error means the revert was not recorded, and once it is recorded the check, the
// merge and the note on the issue of the state are done as far as GitHub allows.
func (s *Service) Revert(ctx context.Context, r api.Repo, in RevertInput) (Outcome, error) {
	release, err := s.hold(ctx, r)
	if err != nil {
		return Outcome{}, err
	}
	defer release()
	ctx, cancel := context.WithTimeout(ctx, recordTimeout)
	defer cancel()
	return s.revert(ctx, r, in)
}

func (s *Service) revert(ctx context.Context, r api.Repo, in RevertInput) (Outcome, error) {
	ref, t := r.RepoRef, tenant(r)
	rev, err := s.revertOf(ctx, r)
	if err != nil {
		return Outcome{}, err
	}
	if revertDigest(rev) != in.Digest {
		return Outcome{}, ErrMoved
	}
	approvedFile, err := s.approvedPolicy(ctx, ref, rev)
	if err != nil {
		return Outcome{}, err
	}
	pol, err := policy3.Decode(approvedFile)
	if err != nil {
		return Outcome{}, fmt.Errorf("proposals: the approved policy at %s does not decode: %w", rev.Commit, err)
	}
	if !slices.ContainsFunc(pol.Owners, func(o string) bool {
		return in.Owner.Login != "" && strings.EqualFold(strings.TrimPrefix(o, "@"), in.Owner.Login)
	}) {
		return Outcome{}, ErrNotOwner
	}
	tip, err := s.gh.BranchHead(ctx, ref, r.DefaultBranch)
	if err != nil {
		return Outcome{}, fmt.Errorf("proposals: read the default branch head: %w", err)
	}
	tipFile, _, err := s.gh.File(ctx, ref, tip, policyPath)
	if err != nil && !errors.Is(err, api.ErrNotFound) {
		return Outcome{}, fmt.Errorf("proposals: read the policy: %w", err)
	}
	// The revert replaces the change the owner saw: a later push, not judged yet, is another one.
	if digestOf(tipFile) != rev.Outside {
		return Outcome{}, ErrMoved
	}
	cur, err := s.current(ctx, r)
	if err != nil {
		return Outcome{}, err
	}
	file, kept, voided := approvedFile, []approved(nil), []approved(nil)
	if cur != nil {
		cur.base = approvedFile
		if file, kept, voided, err = apply(tipFile, cur); err != nil {
			return Outcome{}, err
		}
		if len(kept) >= records.MaxApprovals {
			return Outcome{}, ErrFull
		}
	}
	if bytes.Equal(file, tipFile) {
		return Outcome{}, ErrNothingToRevert
	}
	if in.Redeem == nil {
		return Outcome{}, ErrNeedsPasskey
	}
	passkey, err := in.Redeem(ctx, in.Digest)
	if err != nil {
		return Outcome{}, err
	}
	// RevertTo writes the kind and the change.
	approval := records.Approval{Owner: in.Owner.ID, Login: in.Owner.Login, Credential: passkey, At: s.now().UTC()}
	if cur != nil {
		if err := s.voidAndNote(ctx, r, cur.rec.PullRequest, voided, false); err != nil {
			return Outcome{}, err
		}
	}

	id := records.NewID()
	listed := append(kept, approved{id: id, rec: approval, revert: &rev})
	pin, err := s.write(ctx, r, cur, tip, file, listed)
	if err != nil {
		return Outcome{}, err
	}
	switch err := records.RevertTo(ctx, s.store, t, r.FullName, id, approval, rev, *pin); {
	case errors.Is(err, records.ErrMoved):
		return Outcome{}, ErrMoved
	case err != nil:
		return Outcome{}, fmt.Errorf("proposals: record the revert: %w", err)
	}

	after, cancel := context.WithTimeout(context.WithoutCancel(ctx), afterTimeout)
	defer cancel()
	out := Outcome{Approval: id, PullRequest: pin.PullRequest}
	out.State, out.Merge = s.publish(after, r, tip, *pin, listed, in.Later)
	if rev.Issue != 0 {
		if _, err := s.gh.CreateComment(after, ref, rev.Issue, revertNote(in.Owner.Login, passkey, out)); err != nil {
			logFailure(after, r, "note the revert", err)
		}
	}
	slog.Default().InfoContext(after, "proposals revert", "installation_id", r.InstallationID, "repository_id", r.ID,
		"approval", id, "pull", out.PullRequest, "state", out.State, "voided", len(voided))
	return out, nil
}

// revertOf is the revert of r's policy now: the approved policy back in place of the one changed outside the flow.
func (s *Service) revertOf(ctx context.Context, r api.Repo) (records.Revert, error) {
	var repo records.Repo
	err := records.Load(ctx, s.store, tenant(r), records.RepoKey(r.FullName), &repo)
	switch {
	case err != nil && !errors.Is(err, records.ErrNoRecord):
		return records.Revert{}, fmt.Errorf("proposals: read the repository: %w", err)
	case repo.OutsideFlow == nil:
		return records.Revert{}, ErrNotOutside
	case repo.ApprovedCommit == "" || repo.ApprovedDigest == "":
		return records.Revert{}, errors.New("proposals: the approved policy has no commit to restore")
	}
	return records.Revert{Commit: repo.ApprovedCommit, Digest: repo.ApprovedDigest, Outside: repo.OutsideFlow.Digest,
		Issue: repo.OutsideFlow.Issue}, nil
}

// approvedPolicy reads the policy a revert restores. A file whose digest is not the approved digest restores nothing.
func (s *Service) approvedPolicy(ctx context.Context, ref api.RepoRef, rev records.Revert) ([]byte, error) {
	file, _, err := s.gh.File(ctx, ref, rev.Commit, policyPath)
	if err != nil {
		return nil, fmt.Errorf("proposals: read the approved policy at %s: %w", rev.Commit, err)
	}
	if policy3.Digest(file) != rev.Digest {
		return nil, fmt.Errorf("proposals: the policy at %s is not the approved one", rev.Commit)
	}
	return file, nil
}

// digestOf is the policy digest of file; "" for no policy, as the push judgement records a deleted one.
func digestOf(file []byte) string {
	if file == nil {
		return ""
	}
	return policy3.Digest(file)
}

// revertDigest is the digest a revert's step-up is bound to: the approved policy it restores and the changed one it
// replaces. The issue is left out: it can be opened after the owner looked.
func revertDigest(rev records.Revert) string {
	raw, _ := json.Marshal(struct {
		Kind    string `json:"kind"`
		Commit  string `json:"commit"`
		Digest  string `json:"digest"`
		Outside string `json:"outside"`
	}{records.KindRevert, rev.Commit, rev.Digest, rev.Outside})
	sum := sha256.Sum256(raw)
	return "sha256:" + hex.EncodeToString(sum[:])
}

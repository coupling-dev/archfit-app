package proposals

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"slices"
	"strings"

	"github.com/coupling-dev/archfit-app/internal/gate/records"
	"github.com/coupling-dev/archfit-app/internal/gate/requests"
	"github.com/coupling-dev/archfit-app/internal/github/api"
	"github.com/coupling-dev/archfit-app/internal/wire/policy3"
)

// BatchItem is one request and its answer.
type BatchItem struct {
	Request int
	Answer  requests.Answer
}

// BatchInput is an owner's ordered batch.
type BatchInput struct {
	Answers []BatchItem
	Owner   Owner
	Redeem  func(context.Context, string) (string, error)
}

// BatchOutcome identifies all answers and their shared policy pull request.
type BatchOutcome struct {
	Approvals            []string
	PullRequest          int
	State, Merge, Reason string
}

type batchPlan struct {
	tip          string
	cur          *proposal
	kept, voided []approved
	changes      []requests.Change
	joins        []bool
	want         []byte
	digest       string
	policy       policy3.File
}

func (s *Service) prepareBatch(ctx context.Context, r api.Repo, items []BatchItem) (batchPlan, error) {
	var p batchPlan
	if len(items) == 0 || len(items) > 20 {
		return p, errors.New("proposals: invalid batch size")
	}
	seen := map[int]bool{}
	for _, item := range items {
		if item.Request <= 0 || seen[item.Request] {
			return p, errors.New("proposals: invalid or duplicate request")
		}
		seen[item.Request] = true
	}
	if err := s.controlled(ctx, r, nil); err != nil {
		return p, err
	}
	tip, err := s.gh.BranchHead(ctx, r.RepoRef, r.DefaultBranch)
	if err != nil {
		return p, err
	}
	p.tip = tip
	base, pol, err := s.policyAt(ctx, r.RepoRef, tip)
	if err != nil {
		return p, err
	}
	p.policy = pol
	if err = s.controlled(ctx, r, base); err != nil {
		return p, err
	}
	for _, item := range items {
		req, e := s.openRequest(ctx, r, item.Request)
		if e != nil {
			return p, e
		}
		c, e := requests.ChangeOf(item.Request, req, item.Answer, s.now().UTC())
		if e != nil {
			return p, e
		}
		p.changes = append(p.changes, c)
	}
	p.cur, err = s.readCurrent(ctx, r, false)
	if err != nil {
		return p, err
	}
	p.want, p.kept, p.voided, err = apply(base, p.cur)
	if err != nil {
		return p, err
	}
	for _, c := range p.changes {
		alone, e := requests.Edit(base, c, requests.RequestComment(c.Request))
		if e != nil {
			return p, e
		}
		join := !bytes.Equal(alone, base)
		p.joins = append(p.joins, join)
		if join {
			p.want, e = requests.Edit(p.want, c, requests.RequestComment(c.Request))
			if e != nil {
				return p, e
			}
		}
	}
	count := len(p.kept)
	for _, join := range p.joins {
		if join {
			count++
		}
	}
	if count > records.MaxApprovals {
		return p, ErrFull
	}
	var current string
	if p.cur != nil {
		current = snapshotDigest(p.cur.rec)
	}
	p.digest = snapshotDigest(struct {
		Installation, Repository     int64
		Tip, Policy, Current, Result string
		Changes                      []requests.Change
	}{r.InstallationID, r.ID, tip, policy3.Digest(base), current, policy3.Digest(p.want), p.changes})
	return p, nil
}

// BatchDigest binds the ordered actions and reviewed policy state.
func (s *Service) BatchDigest(ctx context.Context, r api.Repo, items []BatchItem) (string, error) {
	p, err := s.prepareBatch(ctx, r, items)
	return p.digest, err
}

// AnswerBatch records all answers with one shared policy pull request.
func (s *Service) AnswerBatch(ctx context.Context, r api.Repo, in BatchInput) (BatchOutcome, error) {
	var out BatchOutcome
	release, err := s.hold(ctx, r)
	if err != nil {
		return out, err
	}
	defer release()
	ctx, cancel := context.WithTimeout(ctx, recordTimeout)
	defer cancel()
	p, err := s.prepareBatch(ctx, r, in.Answers)
	if err != nil {
		return out, err
	}
	if in.Owner.ID <= 0 || !slices.ContainsFunc(p.policy.Owners, func(o string) bool { return in.Owner.Login != "" && strings.EqualFold(o, in.Owner.Login) }) {
		return out, ErrNotOwner
	}
	loosens := false
	for _, c := range p.changes {
		v, e := policy3.Loosens(c.Kind)
		if e != nil {
			return out, e
		}
		loosens = loosens || v
	}
	credential := ""
	if loosens {
		if in.Redeem == nil {
			return out, ErrNeedsPasskey
		}
		credential, err = in.Redeem(ctx, p.digest)
		if err != nil {
			return out, err
		}
	}
	items := make([]records.BatchAnswer, 0, len(p.changes))
	listed := slices.Clone(p.kept)
	for i, c := range p.changes {
		raw, e := json.Marshal(c)
		if e != nil {
			return out, e
		}
		cred := ""
		if v, _ := policy3.Loosens(c.Kind); v {
			cred = credential
		}
		a := records.Approval{Kind: string(c.Kind), Request: c.Request, Change: raw, Owner: in.Owner.ID, Login: in.Owner.Login, At: s.now().UTC(), Credential: cred}
		id := records.NewID()
		out.Approvals = append(out.Approvals, id)
		items = append(items, records.BatchAnswer{ApprovalID: id, Approval: a, Join: p.joins[i]})
		if p.joins[i] {
			listed = append(listed, approved{id: id, rec: a, change: c})
		}
	}
	if p.cur != nil {
		if err = s.voidAndNote(ctx, r, p.cur.rec.PullRequest, p.voided, false); err != nil {
			return BatchOutcome{}, err
		}
	}
	var pin *records.Pin
	if len(listed) > len(p.kept) {
		pin, err = s.write(ctx, r, p.cur, p.tip, p.want, listed)
		if err != nil {
			return BatchOutcome{}, err
		}
		pin.MergeNow = true
	}
	if err = records.AnswerBatch(ctx, s.store, tenant(r), r.FullName, items, pin); err != nil {
		if errors.Is(err, records.ErrAnswered) {
			return BatchOutcome{}, requests.ErrClosedRequest
		}
		return BatchOutcome{}, fmt.Errorf("proposals: record batch: %w", err)
	}
	after, done := context.WithTimeout(context.WithoutCancel(ctx), afterTimeout)
	defer done()
	if p.cur != nil && pin != nil && pin.HeadSHA != p.cur.rec.HeadSHA {
		s.retire(after, r, p.cur.rec.HeadSHA, p.cur.rec.PullRequest, p.cur.id, RetiredSuperseded)
	}
	if pin == nil && p.cur != nil && len(p.voided) > 0 {
		pin = s.resync(after, r, p.cur, p.tip, p.want, p.kept)
	}
	if pin != nil {
		state, merge, reason := s.publish(after, r, p.tip, *pin, listed, len(listed) == len(p.kept))
		if len(listed) > len(p.kept) {
			out.PullRequest, out.State, out.Merge, out.Reason = pin.PullRequest, state, merge, reason
		}
	}
	for i, c := range p.changes {
		answerOut := Outcome{Approval: out.Approvals[i]}
		if p.joins[i] {
			answerOut.PullRequest, answerOut.State, answerOut.Merge, answerOut.Reason = out.PullRequest, out.State, out.Merge, out.Reason
		}
		s.close(after, r, Input{Request: c.Request, Answer: in.Answers[i].Answer, Owner: in.Owner}, c, items[i].Approval.Credential, answerOut)
	}
	return out, nil
}

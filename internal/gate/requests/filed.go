package requests

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/alexei-led/archfit-app/internal/gate/records"
	"github.com/alexei-led/archfit-app/internal/github/api"
	"github.com/alexei-led/archfit-app/internal/wire/request"
)

// Issue is an issue opened in a repository.
type Issue struct {
	Repo   api.Repo // RepoRef and DefaultBranch, from the event
	Number int
	Author string
	Body   string
	Today  time.Time // the App's date
}

// Filed takes in an issue the CLI opened from a `can` answer, before a pull request exists (design 10.3). An issue
// whose first line is not a request line is ReactionNone. The author needs write permission (M2 D21). The issue is
// the request: the App stores it with the reported numbers "as reported", labels it and assigns the owners. It
// closes the issue with one note when a deny rule of the policy already answers the dependency, or when the same
// request is open. Any other refusal gets one reply and leaves the issue as it is.
func (s *Service) Filed(ctx context.Context, i Issue) (string, error) {
	form, ok, lineErr := request.ParseIssue(i.Body, i.Today)
	if !ok {
		return ReactionNone, nil
	}
	ref := i.Repo.RepoRef
	allowed, err := s.writer(ctx, ref, i.Author)
	if err != nil {
		return ReactionNone, err
	}
	if !allowed {
		return s.refuse(ctx, ref, i.Number, request.NoPermission().Reply())
	}
	if rejection := (*request.Rejection)(nil); errors.As(lineErr, &rejection) {
		return s.refuse(ctx, ref, i.Number, rejection.Reply())
	}
	if form.Answer == nil {
		return s.refuse(ctx, ref, i.Number, request.NothingToAsk("").Reply())
	}
	key, err := form.Answer.Key()
	if err != nil {
		return s.refuse(ctx, ref, i.Number, request.NothingToAsk("").Reply())
	}
	pol, found, err := s.policy(ctx, i.Repo)
	if err != nil {
		return ReactionNone, err
	}
	if !found {
		return s.refuse(ctx, ref, i.Number, textNoPolicy)
	}
	if a, ok := deniedPair(pol, form.Answer.From, form.Answer.To); ok {
		return ReactionAnswered, s.closeWith(ctx, ref, i.Number, answeredReply(a))
	}

	req := records.Request{Key: key, Reason: form.Reason, Expires: form.Expires, Rule: form.Answer.Rule,
		From: form.Answer.From, To: form.Answer.To, Record: capRecord(form.Record), Numbers: records.Numbers{}}
	if b := form.Answer.Balance; b != nil {
		req.Numbers.Balance = &records.Balance{Score: b.Value, Max: 10, Min: b.Min}
	}
	other, err := put(ctx, s.store, i.Repo, i.Number, req)
	switch {
	case errors.Is(err, records.ErrExists) && other > 0 && other != i.Number:
		return ReactionAnswered, s.closeWith(ctx, ref, i.Number, duplicateReply(other))
	case err != nil:
		return ReactionNone, fmt.Errorf("requests: store request %d: %w", i.Number, err)
	}
	s.triage(ctx, ref, i.Number, pol.Owners)
	return ReactionFiled, nil
}

// closeWith posts note on an issue, then closes it.
func (s *Service) closeWith(ctx context.Context, ref api.RepoRef, issue int, note string) error {
	if _, err := s.gh.CreateComment(ctx, ref, issue, note); err != nil {
		return fmt.Errorf("requests: note on issue %d: %w", issue, err)
	}
	if err := s.gh.CloseIssue(ctx, ref, issue); err != nil {
		return fmt.Errorf("requests: close issue %d: %w", issue, err)
	}
	return nil
}

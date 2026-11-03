package inputs

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/alexei-led/archfit-app/internal/github/api"
	"github.com/alexei-led/archfit-app/internal/store"
	"github.com/alexei-led/archfit-app/internal/wire/policy"
)

type decisionComments struct {
	comments        []api.Comment
	nextID          int64
	listErr         error
	createErr       error
	createThenError bool
	updates         int
	creates         int
}

func (f *decisionComments) AppComments(context.Context, api.RepoRef, int) ([]api.Comment, error) {
	if f.listErr != nil {
		return nil, f.listErr
	}
	return append([]api.Comment(nil), f.comments...), nil
}

func (f *decisionComments) CreateComment(_ context.Context, _ api.RepoRef, _ int, body string) (api.Comment, error) {
	f.creates++
	if f.createErr != nil && !f.createThenError {
		return api.Comment{}, f.createErr
	}
	f.nextID++
	comment := api.Comment{ID: f.nextID, Body: body}
	f.comments = append(f.comments, comment)
	if f.createErr != nil {
		return api.Comment{}, f.createErr
	}
	return comment, nil
}

func (f *decisionComments) UpdateComment(_ context.Context, _ api.RepoRef, id int64, body string) error {
	f.updates++
	for i := range f.comments {
		if f.comments[i].ID == id {
			f.comments[i].Body = body
			return nil
		}
	}
	return api.ErrNotFound
}

func TestMirrorDecisionsEscapesReasonsAndRetriesUnknownCreateOutcome(t *testing.T) {
	f := newFixture()
	f.pulls.changed = []string{policy.BaselinePath}
	target := pullTarget()
	seen := f.shown(t, target)
	reason := "@someone <script>alert(1)</script>\nline two"
	if err := f.resolver().Decide(context.Background(), target, "alice", seen, DecisionRequestChanges, reason, nil, now); err != nil {
		t.Fatal(err)
	}
	comments := &decisionComments{createErr: errors.New("write outcome unknown"), createThenError: true}
	resolver := f.resolver().(*resolver)
	if err := resolver.MirrorDecisions(context.Background(), target, comments); err == nil {
		t.Fatal("unknown comment write outcome was not reported")
	}
	if len(comments.comments) != 1 || !strings.Contains(comments.comments[0].Body, "&#64;someone") ||
		!strings.Contains(comments.comments[0].Body, "&lt;script&gt;") || strings.Contains(comments.comments[0].Body, "<script>") ||
		!strings.Contains(comments.comments[0].Body, target.HeadSHA) || !strings.Contains(comments.comments[0].Body, "line two") {
		t.Fatalf("mirrored comment = %#v", comments.comments)
	}
	comments.createErr = nil
	comments.createThenError = false
	if err := resolver.MirrorDecisions(context.Background(), target, comments); err != nil {
		t.Fatalf("retry mirror: %v", err)
	}
	if comments.creates != 1 || comments.updates != 0 || len(comments.comments) != 1 {
		t.Fatalf("retry duplicated comment: creates=%d updates=%d comments=%d", comments.creates, comments.updates, len(comments.comments))
	}
	raw, err := f.store.Get(context.Background(), store.Tenant(installation), decisionKey(target))
	if err != nil {
		t.Fatal(err)
	}
	var saved decisionRecord
	if err := json.Unmarshal(raw, &saved); err != nil {
		t.Fatal(err)
	}
	if len(saved.History) != 1 || !saved.History[0].Published || saved.History[0].CommentID != comments.comments[0].ID {
		t.Fatalf("mirror record = %+v, want durable published comment identity", saved)
	}
	if err := resolver.MirrorDecisions(context.Background(), target, comments); err != nil || comments.creates != 1 {
		t.Fatalf("completed mirror repeated: err=%v creates=%d", err, comments.creates)
	}
}

func TestMirrorFailureLeavesDecisionSavedAndUnpublished(t *testing.T) {
	f := newFixture()
	f.pulls.changed = []string{policy.BaselinePath}
	target := pullTarget()
	seen := f.shown(t, target)
	if err := f.resolver().Decide(context.Background(), target, "alice", seen, DecisionWithdraw, "Reconsidered.", nil, now); err == nil {
		t.Fatal("withdraw without a prior approval should refuse")
	}
	if err := f.resolver().Decide(context.Background(), target, "alice", seen, DecisionApprove, "", nil, now); err != nil {
		t.Fatal(err)
	}
	if err := f.resolver().Decide(context.Background(), target, "alice", seen, DecisionWithdraw, "Reconsidered.", nil, now.Add(time.Minute)); err != nil {
		t.Fatal(err)
	}
	comments := &decisionComments{createErr: errors.New("GitHub unavailable")}
	resolver := f.resolver().(*resolver)
	if err := resolver.MirrorDecisions(context.Background(), target, comments); err == nil {
		t.Fatal("comment failure not reported")
	}
	got := f.mustResolve(t, target)
	wantApproval(t, got, ApprovalMissing, "")
	if len(got.DecisionHistory) != 2 {
		t.Fatalf("the authenticated decisions were lost after comment failure: %+v", got.DecisionHistory)
	}
	raw, err := f.store.Get(context.Background(), store.Tenant(installation), decisionKey(target))
	if err != nil {
		t.Fatal(err)
	}
	var record decisionRecord
	if err := json.Unmarshal(raw, &record); err != nil {
		t.Fatal(err)
	}
	if record.History[0].Published || record.History[1].Published {
		t.Fatalf("failed mirror marked published: %+v", record.History)
	}
}

package ui

import (
	"context"
	"encoding/json"
	"net/http"
	"sync"
	"testing"

	"github.com/alexei-led/archfit-app/internal/control/answers"
	"github.com/alexei-led/archfit-app/internal/control/passkey"
	"github.com/alexei-led/archfit-app/internal/gate"
	"github.com/alexei-led/archfit-app/internal/github/api"
	"github.com/alexei-led/archfit-app/internal/wire/wording"
)

// fakeAnswers answers from fixed values and records what it was asked.
type fakeAnswers struct {
	answers.Edits
	mu      sync.Mutex
	digest  string
	out     gate.AnswerOutcome
	err     error
	repos   []api.Repo
	users   []passkey.User
	numbers []int
	answers []answers.Answer
	edits   []answers.EditAnswer
	editor  gate.EditBase
}

func (f *fakeAnswers) Digest(_ context.Context, r api.Repo, number int, a answers.Answer) (string, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.repos, f.numbers, f.answers = append(f.repos, r), append(f.numbers, number), append(f.answers, a)
	return f.digest, f.err
}

func (f *fakeAnswers) Answer(_ context.Context, r api.Repo, u passkey.User, number int, a answers.Answer) (gate.AnswerOutcome, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.repos, f.users, f.numbers, f.answers = append(f.repos, r), append(f.users, u), append(f.numbers, number), append(f.answers, a)
	return f.out, f.err
}

func TestAnswerDigestAPI(t *testing.T) {
	e := newEnv(t)
	e.answers.digest = "sha256:" + sha(2)
	res := e.as("alice", permRead).postJSON(requestAPI+"/answer/digest", map[string]any{"answer": "approve-until", "until": "2026-12-10"})
	wantStatus(t, res, http.StatusOK)
	var body answerDigestJSON
	if err := json.Unmarshal(res.Body.Bytes(), &body); err != nil || body.Digest != "sha256:"+sha(2) {
		t.Errorf("body %s, %v", res.Body.String(), err)
	}
	if got := e.answers.answers[0]; got != (answers.Answer{Kind: "approve-until", Until: "2026-12-10"}) || e.answers.numbers[0] != 431 ||
		e.answers.repos[0].FullName != repoName {
		t.Errorf("asked %+v for %d of %v", got, e.answers.numbers[0], e.answers.repos)
	}
}

func TestAnswerAPIPassesTheAnswerTheUserAndTheToken(t *testing.T) {
	tests := []struct {
		name   string
		out    gate.AnswerOutcome
		notice string
	}{
		{"merged", gate.AnswerOutcome{PullRequest: 12, State: gate.AnswerMerged, Merge: sha(3)}, "Answered. The policy change merged in #12."},
		{"later", gate.AnswerOutcome{PullRequest: 12, State: gate.AnswerLater}, "Answered. The policy change is in #12; it takes effect when #12 merges."},
		{"refused", gate.AnswerOutcome{PullRequest: 12, State: gate.AnswerRefused},
			"Answered. The policy change is in #12, but GitHub did not merge it. It stays open until it merges."},
		{"edit voided", gate.AnswerOutcome{PullRequest: 12, State: gate.AnswerVoided}, "the policy changed; reload"},
		{"no line", gate.AnswerOutcome{}, "Answered. The policy needs no new line for it."},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			e := newEnv(t)
			e.answers.out = tt.out
			res := e.as("alice", permRead).postJSON(requestAPI+"/answer",
				map[string]any{"answer": "approve", "merge_later": true, "token": "tok"})
			wantStatus(t, res, http.StatusOK)
			var body answeredJSON
			if err := json.Unmarshal(res.Body.Bytes(), &body); err != nil {
				t.Fatal(err)
			}
			want := answeredJSON{PullRequest: tt.out.PullRequest, State: tt.out.State, Merge: tt.out.Merge, Notice: tt.notice}
			if body != want {
				t.Errorf("body = %+v, want %+v", body, want)
			}
			if words := wording.NotUsedIn(body.Notice); len(words) > 0 {
				t.Errorf("%q uses %v", body.Notice, words)
			}
			if got := e.answers.answers[0]; got != (answers.Answer{Kind: "approve", Later: true, Token: "tok"}) {
				t.Errorf("answer = %+v", got)
			}
			if u := e.answers.users[0]; u != (passkey.User{ID: e.user("alice").UserID, Login: "alice"}) {
				t.Errorf("user = %+v", u)
			}
		})
	}
}

func TestAnswerAPIRefusals(t *testing.T) {
	tests := []struct {
		name   string
		err    error
		status int
	}{
		{"not an owner", gate.ErrNotPolicyOwner, http.StatusForbidden},
		{"no step-up", gate.ErrNeedsPasskey, http.StatusForbidden},
		{"a used token", passkey.ErrToken, http.StatusForbidden},
		{"busy", gate.ErrAnswerBusy, http.StatusConflict},
		{"a full policy pull request", gate.ErrProposalFull, http.StatusConflict},
		{"answered already", gate.ErrClosedRequest, http.StatusConflict},
		{"an incomplete answer", gate.ErrAnswer, http.StatusUnprocessableEntity},
		{"by hand", gate.ErrByHand, http.StatusConflict},
		{"outside the flow", gate.ErrOutsideFlow, http.StatusConflict},
		{"first approval is due", gate.ErrFirstApproval, http.StatusConflict},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			e := newEnv(t)
			e.answers.err = tt.err
			res := e.as("alice", permRead).postJSON(requestAPI+"/answer", map[string]any{"answer": "approve", "token": "tok"})
			wantStatus(t, res, tt.status)
			var body errorJSON
			if err := json.Unmarshal(res.Body.Bytes(), &body); err != nil {
				t.Fatal(err)
			}
			if words := wording.NotUsedIn(body.Error); len(words) > 0 {
				t.Errorf("%q uses %v", body.Error, words)
			}
		})
	}

	t.Run("no permission", func(t *testing.T) {
		e := newEnv(t)
		wantStatus(t, e.signIn("mallory").postJSON(requestAPI+"/answer", map[string]any{"answer": "reject", "instead": "x"}),
			http.StatusNotFound)
		if len(e.answers.numbers) != 0 {
			t.Errorf("answered for a user without access: %v", e.answers.numbers)
		}
	})
	t.Run("no CSRF token", func(t *testing.T) {
		e := newEnv(t)
		res := e.as("alice", permRead).postJSONWith(requestAPI+"/answer", map[string]any{"answer": "reject", "instead": "x"}, "")
		wantStatus(t, res, http.StatusForbidden)
		if len(e.answers.numbers) != 0 {
			t.Error("answered without a CSRF token")
		}
	})
	t.Run("not a number", func(t *testing.T) {
		e := newEnv(t)
		wantStatus(t, e.as("alice", permRead).postJSON("/api/repos/"+repoName+"/requests/x/answer", map[string]any{"answer": "reject"}),
			http.StatusNotFound)
	})
}

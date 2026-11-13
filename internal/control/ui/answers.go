package ui

import (
	"net/http"
	"strconv"

	"github.com/alexei-led/archfit-app/internal/control/answers"
	"github.com/alexei-led/archfit-app/internal/control/passkey"
	"github.com/alexei-led/archfit-app/internal/gate"
	"github.com/alexei-led/archfit-app/internal/github/api"
)

// The answer routes of the request page (design 10.4; M2 task 19). An answer that loosens the policy (approve,
// approve-until) needs the owner's passkey: the page asks for the digest of the answer, makes the step-up for the
// answer kind, the request and that digest (the passkey routes), and sends the token with the answer. A reject needs
// none. The repository of the path names the installation; a body never names a user or a repository.

type answerJSON struct {
	Answer     string `json:"answer"`            // approve, approve-until or reject
	Until      string `json:"until,omitempty"`   // approve-until: YYYY-MM-DD
	Instead    string `json:"instead,omitempty"` // reject: what to do instead
	MergeLater bool   `json:"merge_later,omitempty"`
	Token      string `json:"token,omitempty"` // the passkey step-up token, for approve and approve-until
}

type answerDigestJSON struct {
	Digest string `json:"digest"` // the step-up's digest: action is the answer, request the request number
}

type answeredJSON struct {
	PullRequest int    `json:"pull_request,omitempty"` // the policy pull request; absent when no policy line changes
	State       string `json:"state,omitempty"`        // merged, later or refused
	Merge       string `json:"merge,omitempty"`        // the merge commit, when merged
	Notice      string `json:"notice"`
}

func (in answerJSON) answer() answers.Answer {
	return answers.Answer{Kind: in.Answer, Until: in.Until, Instead: in.Instead, Later: in.MergeLater, Token: in.Token}
}

// apiAnswerDigest is POST /api/repos/{o}/{n}/requests/{number}/answer/digest.
func (s *server) apiAnswerDigest(r *http.Request, sc scope) (any, error) {
	number, ok := requestNumber(r)
	if !ok {
		return nil, api.ErrNotFound
	}
	var in answerJSON
	if err := decodeJSON(r, &in); err != nil {
		return nil, err
	}
	digest, err := s.answers.Digest(r.Context(), sc.repo, number, in.answer())
	if err != nil {
		return nil, err
	}
	return answerDigestJSON{Digest: digest}, nil
}

// apiAnswer is POST /api/repos/{o}/{n}/requests/{number}/answer.
func (s *server) apiAnswer(r *http.Request, sc scope) (any, error) {
	number, ok := requestNumber(r)
	if !ok {
		return nil, api.ErrNotFound
	}
	var in answerJSON
	if err := decodeJSON(r, &in); err != nil {
		return nil, err
	}
	out, err := s.answers.Answer(r.Context(), sc.repo, passkey.User{ID: sc.user.UserID, Login: sc.user.Login}, number, in.answer())
	if err != nil {
		return nil, err
	}
	return answeredJSON{PullRequest: out.PullRequest, State: out.State, Merge: out.Merge, Notice: answeredNotice(out)}, nil
}

// answeredNotice is the sentence the page shows after an answer.
func answeredNotice(out gate.AnswerOutcome) string {
	pull := "#" + strconv.Itoa(out.PullRequest)
	switch {
	case out.State == gate.AnswerVoided:
		return "the policy changed; reload"
	case out.PullRequest == 0:
		return "Answered. The policy needs no new line for it."
	case out.State == gate.AnswerMerged:
		return "Answered. The policy change merged in " + pull + "."
	case out.State == gate.AnswerLater:
		return "Answered. The policy change is in " + pull + "; it takes effect when " + pull + " merges."
	}
	return "Answered. The policy change is in " + pull + ", but GitHub did not merge it. It stays open until it merges."
}

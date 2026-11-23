package ui

import (
	"net/http"
	"strconv"

	"github.com/coupling-dev/archfit-app/internal/control/passkey"
	"github.com/coupling-dev/archfit-app/internal/gate"
)

// The setup route that makes the check required. The App reads the policy from the default
// branch and writes the change itself; a client sends at most the passkey step-up token. The
// repository of the path names the installation and the owners, and the gate applies the owner
// rule before it writes anything.

// requiredJSON is the apply body. It has no policy bytes: the App computes the change.
type requiredJSON struct {
	Token string `json:"token,omitempty"`
}

// apiRequiredPreview is POST /api/repos/{o}/{n}/setup/required/preview.
func (s *server) apiRequiredPreview(r *http.Request, sc scope) (any, error) {
	if err := decodeJSON(r, &struct{}{}); err != nil {
		return nil, err
	}
	v, err := s.answers.MakeRequiredPreview(r.Context(), sc.repo, passkey.User{ID: sc.user.UserID, Login: sc.user.Login})
	if err != nil {
		return nil, err
	}
	return editPreviewJSON{Digest: v.Digest, Target: v.Target, Diff: v.Diff, Against: v.Against, Errors: editProblems(v.Errors)}, nil
}

// apiRequired is POST /api/repos/{o}/{n}/setup/required.
func (s *server) apiRequired(r *http.Request, sc scope) (any, error) {
	var in requiredJSON
	if err := decodeJSON(r, &in); err != nil {
		return nil, err
	}
	out, err := s.answers.MakeRequired(r.Context(), sc.repo, passkey.User{ID: sc.user.UserID, Login: sc.user.Login}, in.Token)
	if err != nil {
		return nil, err
	}
	return answeredJSON{PullRequest: out.PullRequest, State: out.State, Merge: out.Merge, Notice: requiredNotice(out)}, nil
}

// requiredNotice is the sentence the setup page shows after it makes the check required.
func requiredNotice(out gate.AnswerOutcome) string {
	pull := "#" + strconv.Itoa(out.PullRequest)
	switch {
	case out.State == gate.AnswerVoided:
		return "the policy changed; reload"
	case out.PullRequest == 0:
		return "The policy already requires the check."
	case out.State == gate.AnswerMerged:
		return "The check is required; the policy change merged in " + pull + "."
	case out.State == gate.AnswerLater:
		return "The check is required in " + pull + "; it takes effect when " + pull + " merges."
	}
	why := "."
	if out.Reason != "" {
		why = ": " + out.Reason + "."
	}
	return "The check is required in " + pull + ". GitHub has not merged it yet" + why +
		" The App merges it by itself when GitHub allows; nothing else is needed from you."
}

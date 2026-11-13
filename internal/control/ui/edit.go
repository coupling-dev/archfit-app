package ui

import (
	"errors"
	"net/http"
	"strings"

	"github.com/alexei-led/archfit-app/internal/control/answers"
	"github.com/alexei-led/archfit-app/internal/control/passkey"
	"github.com/alexei-led/archfit-app/internal/gate"
)

type editJSON struct {
	Answer string `json:"answer"`
	YAML   string `json:"yaml"`
	Base   string `json:"base_digest"`
	Target string `json:"target,omitempty"`
	Token  string `json:"token,omitempty"`
	Later  bool   `json:"merge_later,omitempty"`
}

type editPreviewJSON struct {
	Digest string   `json:"digest,omitempty"`
	Target string   `json:"target,omitempty"`
	Diff   string   `json:"diff"`
	Errors []string `json:"errors"`
}

func readEdit(r *http.Request) (answers.EditAnswer, error) {
	var in editJSON
	if err := decodeJSON(r, &in); err != nil {
		return answers.EditAnswer{}, err
	}
	if len(in.YAML) > gate.MaxEditBytes {
		return answers.EditAnswer{}, gate.ErrEditSize
	}
	if in.Answer != "edit" {
		return answers.EditAnswer{}, invalid("Choose edit for a YAML answer.")
	}
	return answers.EditAnswer{YAML: in.YAML, Base: in.Base, Target: in.Target, Token: in.Token, Later: in.Later}, nil
}

func (s *server) apiEditReload(r *http.Request, sc scope) (any, error) {
	if err := decodeJSON(r, &struct{}{}); err != nil {
		return nil, err
	}
	base, err := s.answers.ReloadEditor(r.Context(), sc.repo, passkey.User{ID: sc.user.UserID, Login: sc.user.Login})
	if err != nil {
		return nil, err
	}
	return policyEditJSON{Available: true, File: string(base.File), Digest: base.Digest, Recovery: base.Recovery}, nil
}

func (s *server) apiEditPreview(r *http.Request, sc scope) (any, error) {
	in, err := readEdit(r)
	if err != nil {
		return nil, err
	}
	v, err := s.answers.PreviewEdit(r.Context(), sc.repo, passkey.User{ID: sc.user.UserID, Login: sc.user.Login}, in)
	if err != nil {
		return nil, err
	}
	return editPreviewJSON{Digest: v.Digest, Target: v.Target, Diff: v.Diff, Errors: v.Errors}, nil
}

func (s *server) apiEdit(r *http.Request, sc scope) (any, error) {
	in, err := readEdit(r)
	if err != nil {
		return nil, err
	}
	out, err := s.answers.Edit(r.Context(), sc.repo, passkey.User{ID: sc.user.UserID, Login: sc.user.Login}, in)
	if errors.Is(err, gate.ErrEditPolicy) {
		_, message, _ := strings.Cut(err.Error(), ":\n")
		return nil, invalid(message)
	}
	if err != nil {
		return nil, err
	}
	return answeredJSON{PullRequest: out.PullRequest, State: out.State, Merge: out.Merge, Notice: answeredNotice(out)}, nil
}

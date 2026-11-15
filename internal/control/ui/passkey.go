package ui

import (
	"encoding/json"
	"errors"
	"net/http"
	"strings"
	"time"

	"github.com/alexei-led/archfit-app/internal/control/answers"
	"github.com/alexei-led/archfit-app/internal/control/passkey"
	"github.com/alexei-led/archfit-app/internal/control/ui/session"
	"github.com/alexei-led/archfit-app/internal/gate"
)

// The passkey routes (M2 task 18): the signed-in user registers, lists and removes their own passkeys, and makes the
// step-up that one answer redeems. The repository of the path names the installation and the owners; a body never
// names a user or a repository.

type passkeyTokenJSON struct {
	Token string `json:"token,omitempty"` // a step-up token: register (a second passkey) and remove
}

type passkeyCredentialJSON struct {
	Credential json.RawMessage `json:"credential"` // the browser's PublicKeyCredential, as JSON
}

type passkeyOptionsJSON struct {
	Options json.RawMessage `json:"options"` // for navigator.credentials.create or .get
}

type stepUpJSON struct {
	Action     string `json:"action"`
	Request    int    `json:"request,omitempty"`
	Digest     string `json:"digest,omitempty"`
	Passkey    string `json:"passkey,omitempty"`
	Target     string `json:"target,omitempty"`
	BaseDigest string `json:"base_digest,omitempty"`
	YAML       string `json:"yaml,omitempty"`
}

type stepUpTokenJSON struct {
	Token string `json:"token"`
}

type passkeyJSON struct {
	ID       string    `json:"id"`
	Created  time.Time `json:"created"`
	LastUsed time.Time `json:"last_used,omitzero"`
}

type passkeysJSON struct {
	Passkeys []passkeyJSON `json:"passkeys"`
}

type registeredJSON struct {
	Passkey passkeyJSON `json:"passkey"`
	Notice  string      `json:"notice"`
}

func passkeyOf(p passkey.Passkey) passkeyJSON {
	return passkeyJSON{ID: p.ID, Created: p.Created, LastUsed: p.LastUsed}
}

// passkeyUser is the signed-in user for the passkey rules: administrator or not on the path's repository. The
// administrator check reuses the permission the read authorization just cached, so it makes no GitHub call.
func (s *server) passkeyUser(r *http.Request, sc scope) (passkey.User, error) {
	u := passkey.User{ID: sc.user.UserID, Login: sc.user.Login}
	_, err := s.sessions.Authorize(r.Context(), sc.user, r.PathValue("o")+"/"+r.PathValue("n"), permAdmin)
	switch {
	case err == nil:
		u.Admin = true
	case !errors.Is(err, session.ErrForbidden):
		return passkey.User{}, err
	}
	return u, nil
}

func (s *server) apiPasskeys(r *http.Request, sc scope) (any, error) {
	list, err := s.passkeys.List(r.Context(), sc.repo, sc.user.UserID)
	if err != nil {
		return nil, err
	}
	out := passkeysJSON{Passkeys: make([]passkeyJSON, 0, len(list))}
	for _, p := range list {
		out.Passkeys = append(out.Passkeys, passkeyOf(p))
	}
	return out, nil
}

func (s *server) apiPasskeyRegister(r *http.Request, sc scope) (any, error) {
	var in passkeyTokenJSON
	if err := decodeJSON(r, &in); err != nil {
		return nil, err
	}
	u, err := s.passkeyUser(r, sc)
	if err != nil {
		return nil, err
	}
	options, err := s.passkeys.BeginRegistration(r.Context(), sc.repo, u, in.Token)
	if err != nil {
		return nil, err
	}
	return passkeyOptionsJSON{Options: options}, nil
}

func (s *server) apiPasskeyRegisterFinish(r *http.Request, sc scope) (any, error) {
	var in passkeyCredentialJSON
	if err := decodeJSON(r, &in); err != nil {
		return nil, err
	}
	u, err := s.passkeyUser(r, sc)
	if err != nil {
		return nil, err
	}
	p, err := s.passkeys.FinishRegistration(r.Context(), sc.repo, u, in.Credential)
	if err != nil {
		return nil, err
	}
	return registeredJSON{Passkey: passkeyOf(p), Notice: "Passkey registered. An issue tells you and the owners."}, nil
}

func (s *server) apiPasskeyStepUp(r *http.Request, sc scope) (any, error) {
	var in stepUpJSON
	if err := decodeJSON(r, &in); err != nil {
		return nil, err
	}
	if in.Action == "edit" {
		if len(in.YAML) > gate.MaxEditBytes {
			return nil, gate.ErrEditSize
		}
		v, err := s.answers.PreviewEdit(r.Context(), sc.repo, passkey.User{ID: sc.user.UserID, Login: sc.user.Login},
			answers.EditAnswer{YAML: in.YAML, Base: in.BaseDigest, Target: in.Target})
		if err != nil {
			return nil, err
		}
		if len(v.Errors) > 0 {
			return nil, invalid(strings.Join(v.Errors, "\n"))
		}
		if v.Digest != in.Digest || v.Target != in.Target {
			return nil, gate.ErrEditChanged
		}
	}
	p := passkey.Purpose{Action: in.Action, Request: in.Request, Digest: in.Digest, Passkey: in.Passkey, Target: in.Target, BaseDigest: in.BaseDigest}
	options, err := s.passkeys.BeginStepUp(r.Context(), sc.repo, passkey.User{ID: sc.user.UserID, Login: sc.user.Login}, p)
	if err != nil {
		return nil, err
	}
	return passkeyOptionsJSON{Options: options}, nil
}

func (s *server) apiPasskeyStepUpFinish(r *http.Request, sc scope) (any, error) {
	var in passkeyCredentialJSON
	if err := decodeJSON(r, &in); err != nil {
		return nil, err
	}
	token, err := s.passkeys.FinishStepUp(r.Context(), sc.repo, passkey.User{ID: sc.user.UserID, Login: sc.user.Login}, in.Credential)
	if err != nil {
		return nil, err
	}
	return stepUpTokenJSON{Token: token}, nil
}

func (s *server) apiPasskeyRemove(r *http.Request, sc scope) (any, error) {
	var in passkeyTokenJSON
	if err := decodeJSON(r, &in); err != nil {
		return nil, err
	}
	u := passkey.User{ID: sc.user.UserID, Login: sc.user.Login}
	if err := s.passkeys.Remove(r.Context(), sc.repo, u, r.PathValue("id"), in.Token); err != nil {
		return nil, err
	}
	return noticeJSON{Notice: "Passkey removed."}, nil
}

package ui

import (
	"net/http"

	"github.com/alexei-led/archfit-app/internal/control/answers"
	"github.com/alexei-led/archfit-app/internal/gate"
	"github.com/alexei-led/archfit-app/internal/github/api"
)

type ownerJSON struct {
	Pull       int    `json:"pull_request,omitempty"`
	Digest     string `json:"digest,omitempty"`
	ViewDigest string `json:"view_digest,omitempty"`
	MergeLater bool   `json:"merge_later,omitempty"`
	Token      string `json:"token,omitempty"`
}

type ownerOutcomeJSON struct {
	Approval string `json:"approval"`
	Pull     int    `json:"pull_request,omitempty"`
	State    string `json:"state,omitempty"`
	Merge    string `json:"merge,omitempty"`
	Issue    int    `json:"issue,omitempty"`
	Check    string `json:"check,omitempty"`
}

func ownerInput(r *http.Request) (answers.OwnerAction, error) {
	kind := r.PathValue("action")
	switch kind {
	case gate.OwnerFirst, gate.OwnerWorkflow, gate.OwnerConfirm, gate.OwnerRevert:
	default:
		return answers.OwnerAction{}, api.ErrNotFound
	}
	var in ownerJSON
	if err := decodeJSON(r, &in); err != nil {
		return answers.OwnerAction{}, err
	}
	if in.Pull < 0 || kind == gate.OwnerWorkflow && in.Pull == 0 || (kind == gate.OwnerConfirm || kind == gate.OwnerRevert) && in.Pull != 0 {
		return answers.OwnerAction{}, invalid("The action has an invalid pull request.")
	}
	return answers.OwnerAction{Kind: kind, Pull: in.Pull, Digest: in.Digest, ViewDigest: in.ViewDigest, Later: in.MergeLater, Token: in.Token}, nil
}

func (s *server) apiOwnerDigest(r *http.Request, sc scope) (any, error) {
	in, err := ownerInput(r)
	if err != nil {
		return nil, err
	}
	digest, err := s.owners.OwnerDigest(r.Context(), sc.repo, in)
	if err != nil {
		return nil, err
	}
	return answerDigestJSON{Digest: digest}, nil
}

func (s *server) apiOwnerAct(r *http.Request, sc scope) (any, error) {
	in, err := ownerInput(r)
	if err != nil {
		return nil, err
	}
	u := gate.OwnerUser{ID: sc.user.UserID, Login: sc.user.Login}
	if in.Kind == gate.OwnerFirst {
		// Do not reuse the session permission cache for an approval: admin authority must be fresh for this action.
		permission, err := s.gh.users.Permission(r.Context(), sc.user.Token, sc.repo.FullName)
		if err != nil {
			return nil, err
		}
		if permission != permAdmin {
			return nil, gate.ErrOwnerNotAdmin
		}
		u.Admin = true
	}
	out, err := s.owners.OwnerAct(r.Context(), sc.repo, u, in)
	if err != nil {
		return nil, err
	}
	return ownerOutcomeJSON{Approval: out.Approval, Pull: out.PullRequest, State: out.State, Merge: out.Merge, Issue: out.Issue, Check: out.Check}, nil
}

type setupFindingJSON struct {
	Code  string `json:"code"`
	State string `json:"state"`
	Text  string `json:"text"`
}
type setupJSON struct {
	Admitted  bool               `json:"admitted"`
	State     string             `json:"state"`
	Missing   []string           `json:"missing"`
	FirstPull int                `json:"first_pull,omitempty"`
	Issue     int                `json:"issue,omitempty"`
	Rules     []setupFindingJSON `json:"rules"`
}

func (s *server) apiSetup(r *http.Request, sc scope) (any, error) {
	out, err := s.setups.Setup(r.Context(), sc.repo)
	if err != nil {
		return nil, err
	}
	body := setupJSON{Admitted: out.Admitted, State: out.State, Missing: out.Missing, FirstPull: out.FirstPull, Issue: out.Issue, Rules: []setupFindingJSON{}}
	for _, f := range out.Rules.Findings {
		body.Rules = append(body.Rules, setupFindingJSON{Code: f.Code, State: f.State, Text: f.Text})
	}
	return body, nil
}

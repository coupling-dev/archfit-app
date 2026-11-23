package ui

import (
	"net/http"
	"strconv"
	"sync"

	"github.com/coupling-dev/archfit-app/internal/control/answers"
	"github.com/coupling-dev/archfit-app/internal/control/onboarding"
	"github.com/coupling-dev/archfit-app/internal/gate"
	"github.com/coupling-dev/archfit-app/internal/github/api"
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
type approvalJSON struct {
	Status       string `json:"status"`
	Kind         string `json:"kind"`
	Pull         int    `json:"pull_request,omitempty"`
	CurrentHead  string `json:"current_head,omitempty"`
	ApprovedHead string `json:"approved_head,omitempty"`
	Merged       bool   `json:"merged,omitempty"`
	Href         string `json:"href,omitempty"`
}
type weeklyJSON struct {
	Known   bool   `json:"known"`
	Checked *int   `json:"checked,omitempty"`
	Quiet   *int   `json:"quiet,omitempty"`
	Asked   *int   `json:"asked,omitempty"`
	Caveat  string `json:"caveat,omitempty"`
}

type setupJSON struct {
	Mode         string             `json:"mode,omitempty"`
	Weekly       weeklyJSON         `json:"weekly"`
	Approval     approvalJSON       `json:"approval"`
	PasskeyKnown bool               `json:"passkey_known"`
	Review       *reviewJSON        `json:"owner_action,omitempty"`
	Admitted     bool               `json:"admitted"`
	Policy       bool               `json:"policy"`
	Workflow     bool               `json:"workflow"`
	Passkey      bool               `json:"passkey"`
	OpenPull     int                `json:"open_pull"`
	State        string             `json:"state"`
	Missing      []string           `json:"missing"`
	FirstPull    int                `json:"first_pull,omitempty"`
	SetupPull    int                `json:"setup_pull,omitempty"`
	Issue        int                `json:"issue,omitempty"`
	Rules        []setupFindingJSON `json:"rules"`
}

func (s *server) apiSetup(r *http.Request, sc scope) (any, error) {
	var out onboarding.Setup
	var setupErr error
	var passkey, passkeyKnown bool
	var reads sync.WaitGroup
	reads.Go(func() {
		done := api.StartStage(r.Context(), "setup")
		// The row's reads of the default branch (tip, policy, workflow) are shared with the approval summary.
		ctx, _ := gate.WithHomeSnapshot(r.Context(), sc.repo)
		out, setupErr = s.setups.Setup(ctx, sc.repo)
		done(1, 0)
	})
	reads.Go(func() {
		done := api.StartStage(r.Context(), "passkeys")
		keys, err := s.passkeys.List(r.Context(), sc.repo, sc.user.UserID)
		// Unreadable inventory is not a confirmed empty inventory.
		passkeyKnown = err == nil
		passkey = passkeyKnown && len(keys) > 0
		done(len(keys), 0)
	})
	reads.Wait()
	if setupErr != nil {
		return nil, setupErr
	}
	approval := approvalJSON{Status: string(out.Approval.Status), Kind: out.Approval.Kind, Pull: out.Approval.Pull,
		CurrentHead: out.Approval.CurrentHead, ApprovedHead: out.Approval.ApprovedHead, Merged: out.Approval.Merged}
	if approval.Status == "" {
		approval.Status = string(gate.ApprovalUnknown)
	}
	if approval.Kind == "" {
		approval.Kind = gate.OwnerFirst
	}
	if approval.Pull > 0 {
		approval.Href = "https://github.com/" + sc.repo.FullName + "/pull/" + strconv.Itoa(approval.Pull)
	}
	body := setupJSON{Mode: out.Mode, Approval: approval, PasskeyKnown: passkeyKnown, Admitted: out.Admitted, Policy: out.Policy, Workflow: out.Workflow, Passkey: passkey, OpenPull: out.OpenPull,
		State: out.State, Missing: out.Missing, FirstPull: out.FirstPull, SetupPull: out.SetupPull, Issue: out.Issue, Rules: []setupFindingJSON{}}
	if s.store != nil && s.now != nil {
		summary, err := gate.ReadWeekly(r.Context(), s.store, sc.repo, s.now())
		if err == nil && summary.Known {
			body.Weekly = weeklyJSON{Known: true, Checked: &summary.Checked, Quiet: &summary.Passed, Asked: &summary.Owner, Caveat: gate.WeeklyCaveat}
		}
	}
	for _, f := range out.Rules.Findings {
		body.Rules = append(body.Rules, setupFindingJSON{Code: f.Code, State: f.State, Text: f.Text})
	}
	return body, nil
}

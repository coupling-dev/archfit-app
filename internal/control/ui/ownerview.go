package ui

import (
	"net/http"
	"strconv"

	"github.com/alexei-led/archfit-app/internal/control/answers"
	"github.com/alexei-led/archfit-app/internal/gate"
	"github.com/alexei-led/archfit-app/internal/github/api"
)

type ownerFileJSON struct {
	Role    string `json:"role"`
	Ref     string `json:"ref"`
	Path    string `json:"path"`
	Digest  string `json:"digest"`
	Text    string `json:"text"`
	Missing bool   `json:"missing"`
}
type ownerDiffJSON struct {
	Title string `json:"title"`
	Path  string `json:"path"`
	Text  string `json:"text"`
}
type ownerApprovalJSON struct {
	ID      string `json:"id"`
	Request int    `json:"request"`
	Digest  string `json:"digest"`
}
type ownerProposalJSON struct {
	ID        string              `json:"id"`
	Pull      int                 `json:"pull_request"`
	Head      string              `json:"head"`
	Digest    string              `json:"digest"`
	Approvals []ownerApprovalJSON `json:"approvals"`
}
type ownerViewJSON struct {
	Kind         string              `json:"kind"`
	Pull         int                 `json:"pull_request"`
	Status       string              `json:"status"`
	Note         string              `json:"note"`
	MainAction   string              `json:"main_action"`
	ActionDigest string              `json:"action_digest"`
	Digest       string              `json:"digest"`
	Head         string              `json:"head"`
	Base         string              `json:"base"`
	Tip          string              `json:"tip"`
	Approved     string              `json:"approved"`
	Files        []ownerFileJSON     `json:"files"`
	Diffs        []ownerDiffJSON     `json:"diffs"`
	Proposals    []ownerProposalJSON `json:"proposals"`
}

// ownerPageAction is shared by the server shell and the read-only API.
func ownerPageAction(r *http.Request) (answers.OwnerAction, error) {
	a := answers.OwnerAction{Kind: r.PathValue("action")}
	switch a.Kind {
	case gate.OwnerFirst, gate.OwnerWorkflow, gate.OwnerConfirm, gate.OwnerRevert:
	default:
		return a, api.ErrNotFound
	}
	q, err := r.URL.Query()["pull"], error(nil)
	if len(q) > 0 {
		if len(q) != 1 || q[0] == "" {
			return a, api.ErrNotFound
		}
		a.Pull, err = strconv.Atoi(q[0])
		if err != nil || a.Pull <= 0 || strconv.Itoa(a.Pull) != q[0] {
			return a, api.ErrNotFound
		}
	}
	if a.Kind == gate.OwnerWorkflow && a.Pull == 0 || (a.Kind == gate.OwnerConfirm || a.Kind == gate.OwnerRevert) && a.Pull != 0 {
		return a, api.ErrNotFound
	}
	return a, nil
}

func (s *server) apiOwnerView(r *http.Request, sc scope) (any, error) {
	a, err := ownerPageAction(r)
	if err != nil {
		return nil, err
	}
	v, err := s.owners.OwnerView(r.Context(), sc.repo, a)
	if err != nil {
		return nil, err
	}
	b := ownerViewJSON{Kind: v.Kind, Pull: v.Pull, Status: v.Status, Note: v.Note, MainAction: v.MainAction,
		ActionDigest: v.ActionDigest, Digest: v.Digest, Head: v.Head, Base: v.Base, Tip: v.Tip, Approved: v.Approved,
		Files: []ownerFileJSON{}, Diffs: []ownerDiffJSON{}, Proposals: []ownerProposalJSON{}}
	for _, f := range v.Files {
		b.Files = append(b.Files, ownerFileJSON{Role: f.Role, Ref: f.Ref, Path: f.Path, Digest: f.Digest, Text: f.Text, Missing: f.Missing})
	}
	for _, d := range v.Diffs {
		b.Diffs = append(b.Diffs, ownerDiffJSON{Title: d.Title, Path: d.Path, Text: d.Text})
	}
	for _, p := range v.Proposals {
		out := ownerProposalJSON{ID: p.ID, Pull: p.Pull, Head: p.Head, Digest: p.Digest, Approvals: []ownerApprovalJSON{}}
		for _, a := range p.Approvals {
			out.Approvals = append(out.Approvals, ownerApprovalJSON{ID: a.ID, Request: a.Request, Digest: a.Digest})
		}
		b.Proposals = append(b.Proposals, out)
	}
	return b, nil
}

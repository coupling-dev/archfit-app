package ui

import (
	"context"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"

	"github.com/alexei-led/archfit-app/internal/gate"
	"github.com/alexei-led/archfit-app/internal/github/api"
)

const (
	approvalGranted = "granted"
	approvalMissing = "missing"
)

// errApprovalNotRun is an approval that was recorded while the re-run it asks for failed: the hourly sweep re-runs it.
var errApprovalNotRun = &userError{http.StatusBadGateway, "decision_not_refreshed",
	"The decision was saved, but its reason or check could not be refreshed. Reload this page before taking another action, then use Retry check refresh."}

// approvalJSON is the owner approval of a pull request head that changes a protected file; absent when it changes none.
type approvalJSON struct {
	HeadSHA string   `json:"head_sha"`
	BaseSHA string   `json:"base_sha"`
	Paths   []string `json:"paths"`
	// Digests is each path's digest at the head, "" when deleted, null for a fork; Approve sends it back.
	Digests             map[string]string    `json:"digests"`
	ShortDigests        map[string]string    `json:"short_digests"` // the same, cut for display
	State               string               `json:"state"`
	By                  string               `json:"by,omitempty"`
	CanDecide           bool                 `json:"can_decide"`
	CanApprove          bool                 `json:"can_approve"`
	CanWithdraw         bool                 `json:"can_withdraw"`
	NotificationProblem bool                 `json:"notification_problem"`
	Owners              []string             `json:"owners"`
	SettingsRef         string               `json:"settings_ref"`
	SettingsURL         string               `json:"settings_url"`
	GateMode            string               `json:"gate_mode"`
	Required            bool                 `json:"required"`
	DecisionURL         string               `json:"decision_url"`
	Objections          []ownerObjectionJSON `json:"objections"`
	History             []ownerDecisionJSON  `json:"history"`
	Reason              string               `json:"reason,omitempty"`
	AuthorIsAgent       bool                 `json:"author_is_agent"`
}

type ownerObjectionJSON struct {
	ID         string    `json:"id"`
	Login      string    `json:"login"`
	Source     string    `json:"source"`
	Reason     string    `json:"reason"`
	HeadSHA    string    `json:"head_sha"`
	At         time.Time `json:"at"`
	Overridden bool      `json:"overridden"`
}

type ownerDecisionJSON struct {
	ID                 string               `json:"id"`
	Login              string               `json:"login"`
	Action             gate.DecisionAction  `json:"action"`
	Reason             string               `json:"reason,omitempty"`
	HeadSHA            string               `json:"head_sha"`
	BaseSHA            string               `json:"base_sha"`
	At                 time.Time            `json:"at"`
	Overrode           []string             `json:"overrode,omitempty"`
	OverrodeObjections []ownerObjectionJSON `json:"overrode_objections,omitempty"`
}

// pullTarget is the pull request as the gate addresses it. The repository is the authorized one; the head and base
// come from GitHub. A head in another repository, or in a deleted one, is a fork.
func pullTarget(sc scope, p api.Pull) gate.Target {
	return gate.Target{
		Repo: sc.repo, PullRequest: p.Number, HeadSHA: p.HeadSHA, BaseRef: p.BaseRef, BaseSHA: p.BaseSHA,
		Fork: !strings.EqualFold(p.HeadRepo, sc.repo.FullName),
	}
}

// approval reads the owner approval of p's head for the signed-in user; nil when p changes no protected file.
func (s *server) approval(ctx context.Context, sc scope, p api.Pull) (*approvalJSON, error) {
	baseSHA, err := s.gh.heads.BranchHead(ctx, sc.repo.RepoRef, p.BaseRef)
	if err != nil {
		return nil, err
	}
	t := pullTarget(sc, p)
	t.BaseSHA = baseSHA
	a, err := s.approvals.PullApproval(ctx, t, sc.user.Login, p.Author)
	if err != nil || len(a.Paths) == 0 {
		return nil, err
	}
	settingsRef := a.SettingsRef
	settingsURL := "https://github.com/" + sc.repo.FullName + "/blob/" + url.PathEscape(settingsRef) + "/.archfit-app.yaml"
	owner, name, _ := strings.Cut(sc.repo.FullName, "/")
	decisionURL := strings.TrimRight(s.baseURL, "/") + "/r/" + url.PathEscape(owner) + "/" + url.PathEscape(name) + "/pull/" + strconv.Itoa(p.Number)
	out := &approvalJSON{
		HeadSHA: p.HeadSHA, BaseSHA: baseSHA, Paths: a.Paths, Digests: a.Digests, State: approvalMissing,
		CanDecide: a.Refusal == nil, CanApprove: a.Refusal == nil, CanWithdraw: a.CanWithdraw, NotificationProblem: a.NotificationProblem,
		Owners: a.Owners, SettingsRef: settingsRef,
		SettingsURL: settingsURL, GateMode: a.GateMode, Required: a.GateMode == "required", DecisionURL: decisionURL,
		AuthorIsAgent: a.AuthorIsAgent, Objections: make([]ownerObjectionJSON, 0, len(a.Objections)),
		History: make([]ownerDecisionJSON, 0, len(a.History)),
	}
	for _, objection := range a.Objections {
		reason := objection.Reason
		if strings.TrimSpace(reason) == "" {
			reason = "No reason was supplied."
		}
		out.Objections = append(out.Objections, ownerObjectionJSON{ID: objection.ID, Login: objection.Login, Source: objection.Source,
			Reason: reason, HeadSHA: objection.HeadSHA, At: objection.At, Overridden: objection.Overridden})
	}
	for _, decision := range a.History {
		item := ownerDecisionJSON{ID: decision.ID, Login: decision.Login, Action: decision.Action,
			Reason: decision.Reason, HeadSHA: decision.HeadSHA, BaseSHA: decision.BaseSHA, At: decision.At, Overrode: decision.Overrode}
		for _, objection := range decision.OverrodeObjections {
			item.OverrodeObjections = append(item.OverrodeObjections, ownerObjectionJSON{ID: objection.ID, Login: objection.Login,
				Source: objection.Source, Reason: objection.Reason, HeadSHA: objection.HeadSHA, At: objection.At, Overridden: objection.Overridden})
		}
		out.History = append(out.History, item)
	}
	if a.Digests != nil {
		out.ShortDigests = make(map[string]string, len(a.Digests))
		for path, d := range a.Digests {
			out.ShortDigests[path] = d[:min(len(d), shortSHA)]
		}
	}
	switch {
	case a.Granted:
		out.State, out.By, out.CanApprove = approvalGranted, a.ApprovedBy, false
	}
	if a.Refusal != nil {
		_, _, out.Reason = explain(a.Refusal)
	}
	if out.CanDecide {
		if _, err := s.sessions.Authorize(ctx, sc.user, sc.repo.FullName, permWrite); err != nil {
			out.CanDecide, out.CanApprove, out.CanWithdraw = false, false, false
			out.Reason = "Your repository write access could not be confirmed. No App decision is available."
		}
	}
	return out, nil
}

type approveJSON struct {
	HeadSHA      string            `json:"head_sha"`
	BaseSHA      string            `json:"base_sha"`
	Digests      map[string]string `json:"digests"`
	Reason       string            `json:"reason,omitempty"`
	ObjectionIDs []string          `json:"objection_ids,omitempty"`
}

type approvedJSON struct {
	Approval *approvalJSON `json:"approval"`
	Notice   string        `json:"notice"`
}

func (s *server) apiRefreshDecision(r *http.Request, sc scope) (any, error) {
	number, ok := pullNumber(r)
	if !ok {
		return nil, api.ErrNotFound
	}
	p, err := s.gh.pulls.PullSummary(r.Context(), sc.repo.RepoRef, number)
	if err != nil {
		return nil, err
	}
	baseSHA, err := s.gh.heads.BranchHead(r.Context(), sc.repo.RepoRef, p.BaseRef)
	if err != nil {
		return nil, err
	}
	t := pullTarget(sc, p)
	t.BaseSHA, p.BaseSHA = baseSHA, baseSHA
	if err := s.approvals.Reevaluate(r.Context(), t); err != nil {
		return nil, errApprovalNotRun
	}
	a, err := s.approval(r.Context(), sc, p)
	if err != nil {
		return nil, errSavedUnread
	}
	return approvedJSON{Approval: a, Notice: "Archfit refreshed the decision context. Approval is not a successful check, merge or policy publication."}, nil
}

func (s *server) apiApprovePull(r *http.Request, sc scope) (any, error) {
	return s.apiOwnerDecision(r, sc, gate.DecisionApprove)
}

func (s *server) apiRequestChangesPull(r *http.Request, sc scope) (any, error) {
	return s.apiOwnerDecision(r, sc, gate.DecisionRequestChanges)
}

func (s *server) apiWithdrawPull(r *http.Request, sc scope) (any, error) {
	return s.apiOwnerDecision(r, sc, gate.DecisionWithdraw)
}

func (s *server) apiOverridePull(r *http.Request, sc scope) (any, error) {
	return s.apiOwnerDecision(r, sc, gate.DecisionOverride)
}

func (s *server) apiOwnerDecision(r *http.Request, sc scope, action gate.DecisionAction) (any, error) {
	number, ok := pullNumber(r)
	if !ok {
		return nil, api.ErrNotFound
	}
	var in approveJSON
	if err := decodeJSON(r, &in); err != nil {
		return nil, err
	}
	if action != gate.DecisionOverride && len(in.ObjectionIDs) != 0 {
		return nil, invalid("Objection IDs are only accepted for an explicit override.")
	}
	ctx := r.Context()
	p, err := s.gh.pulls.PullSummary(ctx, sc.repo.RepoRef, number)
	if err != nil {
		return nil, err
	}
	if p.State != "open" || p.Merged {
		return nil, conflict("This pull request is closed or merged. No new architecture decision was recorded.")
	}
	baseSHA, err := s.gh.heads.BranchHead(ctx, sc.repo.RepoRef, p.BaseRef)
	if err != nil {
		return nil, err
	}
	if in.HeadSHA != p.HeadSHA || in.BaseSHA == "" || in.BaseSHA != baseSHA {
		return nil, conflict("The pull request head or protected settings commit changed since this page was loaded. Nothing was saved. Reload and review the current candidate.")
	}
	p.BaseSHA = baseSHA
	if (action == gate.DecisionRequestChanges || action == gate.DecisionWithdraw || action == gate.DecisionOverride) && strings.TrimSpace(in.Reason) == "" {
		return nil, invalid("Enter a reason. It will be posted to the pull request.")
	}
	t := pullTarget(sc, p)
	t.BaseSHA = baseSHA
	if err := s.approvals.Decide(ctx, t, sc.user.Login, in.Digests, action, in.Reason, in.ObjectionIDs); err != nil {
		return nil, err
	}
	if err := s.approvals.Reevaluate(ctx, t); err != nil {
		return nil, errApprovalNotRun
	}
	a, err := s.approval(ctx, sc, p)
	if err != nil {
		return nil, errSavedUnread
	}
	notice := map[gate.DecisionAction]string{
		gate.DecisionApprove:        notices["approved"],
		gate.DecisionRequestChanges: notices["changes_requested"],
		gate.DecisionWithdraw:       notices["approval_withdrawn"],
		gate.DecisionOverride:       notices["override_approved"],
	}[action]
	return approvedJSON{Approval: a, Notice: notice}, nil
}

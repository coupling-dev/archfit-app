package gate

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"

	"github.com/alexei-led/archfit-app/internal/gate/records"
	"github.com/alexei-led/archfit-app/internal/store"
	"github.com/alexei-led/archfit-app/internal/wire/policy3"
)

// RequestView contains the request page data and the recorded answer, when available.
type RequestView struct {
	Number                                               int
	State, Reason, Rule, Path, From, To, Expires, Record string
	IssueHref                                            string
	Numbers                                              RequestNumbers
	Policy                                               bool
	Layers                                               []string
	Modules                                              []RequestModule
	Allowed                                              bool
	Earlier                                              []RequestEarlier
	Answer                                               *RequestAnswer
	Dependencies                                         []RequestDependency
}

// RequestView reads a request. Layers, modules and dependencies share one target-policy snapshot.
func (g *Gate) RequestView(ctx context.Context, t Target, number int) (RequestView, error) {
	if err := g.admitted(ctx, t); err != nil {
		return RequestView{}, err
	}
	v, err := g.asks.View(ctx, t.Repo, number)
	if err != nil {
		return RequestView{}, err
	}
	out := RequestView{Number: v.Number, State: v.State, Reason: v.Reason, Rule: v.Rule, Path: v.Path, From: v.From, To: v.To,
		Expires: v.Expires, Record: v.Record, Numbers: v.Numbers, Policy: v.Policy, Layers: v.Layers, Modules: v.Modules,
		Allowed: v.Allowed, Earlier: v.Earlier, Dependencies: v.Dependencies, IssueHref: fmt.Sprintf("https://github.com/%s/issues/%d", t.Repo.FullName, number)}
	if v.State != records.StateAnswered {
		return out, nil
	}
	tenant := store.Tenant(t.Repo.InstallationID)
	var req records.Request
	if err := records.Load(ctx, g.store, tenant, records.RequestKey(t.Repo.FullName, number), &req); err != nil {
		return RequestView{}, err
	}
	if req.State != records.StateAnswered || req.Approval == "" {
		return out, nil
	}
	if strings.Contains(req.Approval, "/") {
		return RequestView{}, fmt.Errorf("request view: invalid approval id")
	}
	var approval records.Approval
	if err := records.Load(ctx, g.store, tenant, records.ApprovalKey(t.Repo.FullName, req.Approval), &approval); err != nil {
		return RequestView{}, err
	}
	if approval.State == records.StateVoid {
		return out, nil
	}
	var change AnswerChange
	if err := json.Unmarshal(approval.Change, &change); err != nil {
		return RequestView{}, fmt.Errorf("request view: unreadable answer: %w", err)
	}
	if approval.Request != number || change.Request != number || string(change.Kind) != approval.Kind {
		return RequestView{}, fmt.Errorf("request view: answer does not match request")
	}
	switch change.Kind {
	case policy3.Approve, policy3.ApproveUntil, policy3.Reject:
		out.Answer = &RequestAnswer{Kind: change.Kind, Until: change.Until, Instead: change.Instead}
	default:
		return RequestView{}, fmt.Errorf("request view: unknown answer")
	}
	return out, nil
}

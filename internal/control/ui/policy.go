package ui

import (
	"cmp"
	"errors"
	"log/slog"
	"net/http"
	"slices"
	"strconv"
	"sync"
	"time"

	"github.com/coupling-dev/archfit-app/internal/gate"
	"github.com/coupling-dev/archfit-app/internal/github/api"
	"github.com/coupling-dev/archfit-app/internal/wire/policy3"
)

// policyMissing is the conflict of a readable repository with no .archfit.yaml on its default branch. The policy page
// matches this exact sentence to show its setup state; a repository the user cannot see stays a 404.
const policyMissing = "No policy on the default branch yet."

type policyPageJSON struct {
	Owners     []string              `json:"owners"`
	Gate       string                `json:"gate"`
	Layers     []string              `json:"layers"`
	Modules    []policyModuleJSON    `json:"modules"`
	Builtin    policyBuiltinJSON     `json:"builtin"`
	Rules      []policyRuleJSON      `json:"rules"`
	Exceptions []policyExceptionJSON `json:"exceptions"`
	YAML       string                `json:"yaml"`
	Digest     string                `json:"digest"`
	Edit       policyEditJSON        `json:"edit"`
}
type policyModuleJSON struct {
	Name       string   `json:"name"`
	Paths      []string `json:"paths"`
	Public     []string `json:"public"`
	Model      []string `json:"model"` // null when absent; version 1 only
	Volatility string   `json:"volatility"`
	Subdomain  string   `json:"subdomain"`
	Layer      string   `json:"layer"`
	DependsOn  []string `json:"depends_on"`
}
type policyBuiltinJSON struct {
	Graph       string `json:"graph"`
	Coupling    string `json:"coupling"`
	Duplication string `json:"duplication"`
	Cycle       string `json:"cycle"`
	Layers      string `json:"layers"`
	Unowned     string `json:"unowned"`
	MinBalance  *int   `json:"min_balance"`
}
type policyRuleJSON struct {
	ID         string          `json:"id"`
	Deny       *policyDenyJSON `json:"deny,omitempty"`
	Only       *policyOnlyJSON `json:"only,omitempty"`
	Note       string          `json:"note,omitempty"`
	NotChecked bool            `json:"not_checked"`
	Why        string          `json:"why"`
	Instead    string          `json:"instead,omitempty"`
}
type policyDenyJSON struct {
	From []string `json:"from"`
	To   []string `json:"to"`
}
type policyOnlyJSON struct {
	Use string   `json:"use"`
	In  []string `json:"in"`
}
type policyExceptionJSON struct {
	Rule        string `json:"rule"`
	From        string `json:"from"`
	To          string `json:"to,omitempty"`
	Expires     string `json:"expires,omitempty"`
	DaysLeft    *int   `json:"days_left,omitempty"`
	Why         string `json:"why"`
	Request     int    `json:"request,omitempty"`
	RequestHref string `json:"request_href,omitempty"`
}
type policyEditJSON struct {
	Available bool   `json:"available"`
	Reason    string `json:"reason"`
	File      string `json:"file,omitempty"`
	Digest    string `json:"digest,omitempty"`
	Recovery  bool   `json:"recovery,omitempty"`
	// The open policy pull request, shown on the page with its one next action.
	PullRequest int    `json:"pull_request,omitempty"`
	Target      string `json:"target,omitempty"` // the default branch's head the proposal is shown against
	Diff        string `json:"diff,omitempty"`   // the proposal against the default branch
	Lost        string `json:"lost,omitempty"`   // what Reload removes from the proposal
	Voided      bool   `json:"voided,omitempty"`
	Foreign     bool   `json:"foreign,omitempty"`
	MergeNow    bool   `json:"merge_now,omitempty"` // the App merges it by itself when GitHub allows
}

// editJSONOf is the editor's state for the page: the whole file only while editing or when a proposal is open.
func editJSONOf(base gate.EditBase, editing bool) policyEditJSON {
	v := policyEditJSON{Available: true, Recovery: base.Recovery, PullRequest: base.PullRequest, Target: base.Target,
		Diff: base.Diff, Lost: base.Lost, Voided: base.Voided, Foreign: base.Foreign, MergeNow: base.MergeNow}
	if editing || base.PullRequest != 0 {
		v.File, v.Digest = string(base.File), base.Digest
	}
	return v
}

func (s *server) apiPolicy(r *http.Request, sc scope) (any, error) {
	ctx := r.Context()
	// The change-control state is a Store read that does not depend on the file, so both are read at once. The file's
	// errors come first, as they did when the state was read after it.
	var (
		state    gate.PolicyState
		stateErr error
		reading  sync.WaitGroup
	)
	if s.policy != nil {
		reading.Go(func() { state, stateErr = s.policy.PolicyState(ctx, sc.repo) })
	}
	defer reading.Wait()
	raw, _, err := s.gh.files.File(ctx, sc.repo.RepoRef, sc.repo.DefaultBranch, ".archfit.yaml")
	if errors.Is(err, api.ErrNotFound) {
		return nil, conflict(policyMissing)
	}
	if err != nil {
		return nil, err
	}
	f, err := policy3.Decode(raw)
	if err != nil {
		if errors.Is(err, policy3.ErrVersion2) {
			return nil, conflict("This repository's policy is still version 2; run archfit init.")
		}
		return nil, conflict("The policy on the default branch could not be read. Fix .archfit.yaml first.")
	}
	body := policyPageJSON{Owners: f.Owners, Gate: f.Gate, Layers: f.Layers, YAML: string(raw), Digest: policy3.Digest(raw), Modules: []policyModuleJSON{}, Rules: []policyRuleJSON{}, Exceptions: []policyExceptionJSON{}}
	body.Gate = cmp.Or(f.Gate, "advisory")
	b := f.Builtin
	minBalance := 7
	if b.MinBalance != nil {
		minBalance = *b.MinBalance
	}
	body.Builtin = policyBuiltinJSON{cmp.Or(b.Graph, "ask"), cmp.Or(b.Coupling, "deny"), cmp.Or(b.Duplication, "ask"), cmp.Or(b.Cycle, "deny"), cmp.Or(b.Layers, "deny"), cmp.Or(b.Unowned, "deny"), &minBalance}
	names := make([]string, 0, len(f.Modules))
	for name := range f.Modules {
		names = append(names, name)
	}
	slices.Sort(names)
	for _, name := range names {
		m := f.Modules[name]
		volatility := m.Volatility
		if volatility == "" {
			if m.Subdomain == "supporting" || m.Subdomain == "generic" {
				volatility = "low"
			} else {
				volatility = "high"
			}
		}
		body.Modules = append(body.Modules, policyModuleJSON{name, m.Paths, m.Public, m.Model, volatility, m.Subdomain, m.Layer, append([]string{}, m.DependsOn...)})
	}
	for _, rule := range f.Rules {
		v := policyRuleJSON{ID: rule.ID, Note: rule.Note, NotChecked: rule.Note != "", Why: rule.Why, Instead: rule.Instead}
		if rule.Deny != nil {
			v.Deny = &policyDenyJSON{rule.Deny.From, rule.Deny.To}
		}
		if rule.Only != nil {
			v.Only = &policyOnlyJSON{rule.Only.Use, rule.Only.In}
		}
		body.Rules = append(body.Rules, v)
	}
	requests := policy3.ExceptionRequests(raw)
	today := s.now().UTC()
	today = time.Date(today.Year(), today.Month(), today.Day(), 0, 0, 0, 0, time.UTC)
	for i, e := range f.Exceptions {
		v := policyExceptionJSON{Rule: e.Rule, From: e.From, To: e.To, Expires: e.Expires, Why: e.Why}
		if e.Expires != "" {
			date, _ := time.Parse("2006-01-02", e.Expires)
			days := int((date.Unix() - today.Unix()) / 86400)
			v.DaysLeft = &days
		}
		if i < len(requests) && requests[i] > 0 {
			v.Request = requests[i]
			v.RequestHref = "/r/" + sc.repo.FullName + "/requests/" + strconv.Itoa(v.Request)
		}
		body.Exceptions = append(body.Exceptions, v)
	}
	body.Edit.Reason = "Change control has not started."
	if s.policy != nil {
		reading.Wait()
		if stateErr != nil {
			return nil, stateErr
		}
		switch {
		case state.OutsideFlow:
			body.Edit.Reason = "The policy changed outside the approval flow."
		case state.ApprovedDigest != "":
			body.Edit.Available = true
			body.Edit.Reason = ""
		}
	}
	if editing := r.URL.Query().Get("edit") == "1"; body.Edit.Available {
		base, err := s.answers.Editor(ctx, sc.repo)
		switch {
		case err != nil && editing:
			return nil, err
		case err != nil:
			// The read-only page never fails on the editor's state: it shows the policy without the pull request.
			slog.Default().WarnContext(ctx, "ui: read the policy pull request", "repository", sc.repo.FullName, "error", err.Error())
		default:
			body.Edit = editJSONOf(base, editing)
		}
	}
	return body, nil
}

package ui

import (
	"cmp"
	"fmt"
	"net/http"
	"slices"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/alexei-led/archfit-app/internal/gate"
	"github.com/alexei-led/archfit-app/internal/github/api"
)

const homeExpiryDays = 7

// The repository states of the Archfit 4 design, as /api/home sends them. The page chooses the words and the tone.
const (
	homeStateClean       = "clean"
	homeStateDrift       = "drift"
	homeStateExpired     = "expired"
	homeStateOutsideFlow = "outside_flow"
	homeStateNotSetUp    = "not_set_up"
	homeStateUnknown     = "unknown"
)

type homeJSON struct {
	State    string         `json:"state"`
	Status   string         `json:"status"` // the sentence for the state, then one sentence per group that waits
	NeedsYou []homeNeedJSON `json:"needs_you,omitempty"`
	Map      *homeMapJSON   `json:"map,omitempty"`
	Setup    *setupJSON     `json:"setup,omitempty"`
}
type homeNeedJSON struct {
	Kind     string `json:"kind"`
	Text     string `json:"text"`
	Href     string `json:"href"`
	Request  int    `json:"request,omitempty"`
	Pull     int    `json:"pull_request,omitempty"`
	Rule     string `json:"rule,omitempty"`
	From     string `json:"from,omitempty"`
	To       string `json:"to,omitempty"`
	Expires  string `json:"expires,omitempty"`
	DaysLeft *int   `json:"days_left,omitempty"`
	Record   string `json:"record,omitempty"`
}
type homeMapJSON struct {
	Layers  []string         `json:"layers"`
	Modules []homeModuleJSON `json:"modules"`
	Lines   []homeLineJSON   `json:"lines"`
}
type homeModuleJSON struct {
	Name       string `json:"name"`
	Layer      string `json:"layer"`
	Volatility string `json:"volatility"`
}
type homeLineJSON struct {
	Kind     string `json:"kind"`
	From     string `json:"from"`
	To       string `json:"to"`
	Rule     string `json:"rule,omitempty"`
	Request  int    `json:"request,omitempty"`
	Expires  string `json:"expires,omitempty"`
	DaysLeft *int   `json:"days_left,omitempty"`
}

func (s *server) apiHome(r *http.Request, sc scope) (any, error) {
	ctx, snapshot := gate.WithHomeSnapshot(r.Context(), sc.repo)
	ctx = gate.WithRequestMemo(withReviewPermission(ctx, sc))
	// The owner reviews share one memo, and Home's listing seeds it. Setup does not use it.
	reviewCtx := gate.WithReviewReads(ctx)
	r = r.WithContext(ctx)
	// The owner reviews need the signed-in user's admin status once per request. It is read beside Home's reads, not
	// after them, so a review does not wait for it; the memo makes the reviews wait for this read if they need it first.
	var permission sync.WaitGroup
	defer permission.Wait()
	permission.Go(func() { s.reviewAdmin(ctx, sc) })
	// The owner reviews share their reads with each other, not with Setup and Home, which read on their own.
	reviewR := r.WithContext(reviewCtx)
	snapshotDone := api.StartStage(ctx, "snapshot")
	defer func() { snapshotDone(1, int(snapshot.CacheHits())) }()
	// Home's reads do not depend on Setup, so they run beside it. Their result is used only for an approved
	// repository, as before: for any other state the Home result and its error are dropped.
	var view gate.HomeState
	var homeErr error
	var homeReads sync.WaitGroup
	homeReads.Go(func() {
		recordsDone := api.StartStage(ctx, "home_records")
		view, homeErr = s.home.Home(reviewCtx, sc.repo)
		recordsDone(len(view.Requests)+len(view.Touched)+len(view.WorkflowPulls), 0)
	})
	setup, err := s.apiSetup(r, sc)
	homeReads.Wait()
	if err != nil {
		return nil, err
	}
	state := setup.(setupJSON)
	if state.State != "approved" && state.State != "outside_flow" {
		if (state.Approval.Status == string(gate.ApprovalNeeded) || state.Approval.Status == string(gate.ApprovalStale)) &&
			(state.State == "first_approval" || state.OpenPull > 0 || state.Approval.Pull > 0) {
			pull := state.Approval.Pull
			if pull == 0 {
				pull = state.FirstPull
			}
			if pull == 0 {
				pull = state.OpenPull
			}
			state.Review = s.ownerReview(reviewR, sc, pull)
		}
		return homeJSON{State: homeStateNotSetUp, Status: "Pull requests are not checked yet. Finish the setup steps.", Setup: &state}, nil
	}
	if homeErr != nil {
		return nil, homeErr
	}
	today := s.now().UTC().Truncate(24 * time.Hour)
	var shown *setupJSON
	if !setupRulesComplete(state.Rules) || state.Approval.Status == string(gate.ApprovalPending) ||
		state.Approval.Status == string(gate.ApprovalStale) || state.Approval.Status == string(gate.ApprovalUnknown) {
		shown = &state
	}
	body := homeBody(view, state, shown, sc.repo.FullName, today)
	var workflowNeeds []int
	for i, need := range body.NeedsYou {
		if need.Kind == "workflow" {
			workflowNeeds = append(workflowNeeds, i)
		}
	}
	reviews := make([]*reviewJSON, len(workflowNeeds))
	forEachBounded(len(workflowNeeds), reviewConcurrency, func(j int) {
		reviews[j] = s.ownerReview(reviewR, sc, body.NeedsYou[workflowNeeds[j]].Pull)
	})
	for j, i := range workflowNeeds {
		if review := reviews[j]; review != nil {
			need := &body.NeedsYou[i]
			need.Text, need.Href = fmt.Sprintf("%s · pull request #%d", review.Label, need.Pull), review.Href
		}
	}
	return body, nil
}

func setupRulesComplete(rules []setupFindingJSON) bool {
	return len(rules) > 0 && !slices.ContainsFunc(rules, func(r setupFindingJSON) bool {
		return r.State != "ok" && r.State != "not_applicable"
	})
}

// homeBody builds the answer for an approved repository. shown is the setup block when setup work is still open.
func homeBody(view gate.HomeState, setup setupJSON, shown *setupJSON, repo string, today time.Time) homeJSON {
	out := homeJSON{NeedsYou: []homeNeedJSON{}, Setup: shown}
	if view.PolicyAvailable {
		out.Map = homeMap(view, today)
	}
	base := "/r/" + repo
	policyHref := base + "/policy"
	issueHref := func(n int) string {
		if n > 0 {
			return "https://github.com/" + repo + "/issues/" + strconv.Itoa(n)
		}
		return policyHref
	}
	// The state is the first that holds, in the order of how much it matters to a pull request.
	outside := view.OutsideFlow || setup.State == "outside_flow"
	if outside {
		if view.PolicyAvailable {
			out.NeedsYou = append(out.NeedsYou, homeNeedJSON{Kind: "confirm", Text: "Confirm the policy changed outside the flow.", Href: base + "/owner/confirm"})
		}
		out.NeedsYou = append(out.NeedsYou, homeNeedJSON{Kind: "revert", Text: "Revert the policy changed outside the flow.", Href: base + "/owner/revert"})
	}
	var waits []string
	if n := len(view.Requests); n > 0 {
		if n == 1 {
			waits = append(waits, "1 request waits.")
		} else {
			waits = append(waits, fmt.Sprintf("%d requests wait.", n))
		}
	}
	for _, req := range view.Requests {
		out.NeedsYou = append(out.NeedsYou, homeNeedJSON{Kind: "request", Text: fmt.Sprintf("Request #%d waits.", req.Number),
			Href: base + "/requests/" + strconv.Itoa(req.Number), Request: req.Number, Rule: req.Rule, From: req.From, To: req.To, Record: req.Record})
	}
	// An exception past its date is a row to act on. It makes the repository expired only when the stored result
	// for this head says the code still breaks the rule, which is what RepositoryState holds.
	for _, x := range view.Policy.Exceptions {
		left, ok := homeDays(x.Expires, today)
		if !ok {
			continue
		}
		text := homeExceptionStatus(left)
		if left >= 0 {
			waits = append(waits, text+".")
		}
		if left > homeExpiryDays {
			continue
		}
		key := gate.HomeException{Rule: x.Rule, From: x.From, To: x.To, Expires: x.Expires}
		out.NeedsYou = append(out.NeedsYou, homeNeedJSON{Kind: "exception", Text: text, Href: issueHref(view.ExceptionIssues[key]), Rule: x.Rule,
			From: x.From, To: x.To, Expires: x.Expires, DaysLeft: &left})
	}
	// Errors stored for an older head are not current; only an open issue, which is real on GitHub, stands without them.
	drift := view.DriftKnown && len(view.Drift) > 0 || view.DriftIssue > 0
	if drift {
		out.NeedsYou = append(out.NeedsYou, homeNeedJSON{Kind: "drift", Text: "Drift on the default branch.", Href: issueHref(view.DriftIssue)})
	}
	for _, pull := range view.WorkflowPulls {
		out.NeedsYou = append(out.NeedsYou, homeNeedJSON{Kind: "workflow", Text: fmt.Sprintf("Workflow change in pull request #%d waits for an owner.", pull),
			Pull: pull})
	}
	if n := len(view.WorkflowPulls); n > 0 {
		if n == 1 {
			waits = append(waits, "1 workflow change waits.")
		} else {
			waits = append(waits, fmt.Sprintf("%d workflow changes wait.", n))
		}
	}
	// Stale or unread evidence is unknown before it is expired or drift, as on the repositories page.
	switch {
	case outside:
		out.State, out.Status = homeStateOutsideFlow, "The policy on the default branch changed without a recorded approval, so every pull request is unknown."
	case !view.DriftKnown:
		out.State, out.Status = homeStateUnknown, "The state of the default branch is not known."
	case view.DriftUnread:
		out.State, out.Status = homeStateUnknown, "The last check could not read every file, so the state is unknown."
	case view.RepositoryState == "expired":
		out.State, out.Status = homeStateExpired, "An exception passed its date and the code still breaks the rule, so every pull request fails."
	case drift:
		out.State, out.Status = homeStateDrift, "The default branch has dependencies the policy does not allow."
		if view.DriftIssue > 0 {
			out.Status += fmt.Sprintf(" Issue #%d.", view.DriftIssue)
		}
	default:
		out.State, out.Status = homeStateClean, "The default branch follows the policy."
	}
	if len(waits) > 0 {
		out.Status += " " + strings.Join(waits, " ")
	}
	if len(out.NeedsYou) == 0 && shown == nil {
		out.Status += " Nothing needs you."
	}
	return out
}

func homeExceptionStatus(left int) string {
	switch {
	case left < 0:
		return "1 exception expired"
	case left == 0:
		return "1 exception expires today"
	case left == 1:
		return "1 exception expires in 1 day"
	default:
		return fmt.Sprintf("1 exception expires in %d days", left)
	}
}

func homeDays(date string, today time.Time) (int, bool) {
	last, err := time.Parse(time.DateOnly, date)
	if err != nil {
		return 0, false
	}
	return int(last.Sub(today).Hours() / 24), true
}

func homeMap(view gate.HomeState, today time.Time) *homeMapJSON {
	pol := view.Policy
	out := &homeMapJSON{Layers: slices.Clone(pol.Layers), Modules: []homeModuleJSON{}, Lines: []homeLineJSON{}}
	slices.Reverse(out.Layers)
	for name, m := range pol.Modules {
		volatility := m.Volatility
		if volatility == "" {
			volatility = "high"
			if m.Subdomain == "supporting" || m.Subdomain == "generic" {
				volatility = "low"
			}
		}
		out.Modules = append(out.Modules, homeModuleJSON{Name: name, Layer: m.Layer, Volatility: volatility})
	}
	// Keep undeclared bands deterministic too; the no-layer band is always last.
	var extra []string
	for _, m := range out.Modules {
		if m.Layer != "" && !slices.Contains(out.Layers, m.Layer) && !slices.Contains(extra, m.Layer) {
			extra = append(extra, m.Layer)
		}
	}
	slices.Sort(extra)
	out.Layers = append(out.Layers, extra...)
	if slices.ContainsFunc(out.Modules, func(m homeModuleJSON) bool { return m.Layer == "" }) {
		out.Layers = append(out.Layers, "")
	}
	if out.Layers == nil {
		out.Layers = []string{}
	}
	slices.SortFunc(out.Modules, func(a, b homeModuleJSON) int {
		if order := cmp.Compare(slices.Index(out.Layers, a.Layer), slices.Index(out.Layers, b.Layer)); order != 0 {
			return order
		}
		return cmp.Compare(a.Name, b.Name)
	})
	add := func(line homeLineJSON) {
		if line.From == "" || line.To == "" {
			return
		}
		if _, ok := pol.Modules[line.From]; !ok {
			return
		}
		if _, ok := pol.Modules[line.To]; !ok {
			return
		}
		if !slices.Contains(out.Lines, line) {
			out.Lines = append(out.Lines, line)
		}
	}
	var touches []gate.HomeError
	for _, m := range out.Modules {
		for _, to := range pol.Modules[m.Name].DependsOn {
			add(homeLineJSON{Kind: "allowed", From: m.Name, To: to})
		}
	}
	for _, req := range view.Requests {
		add(homeLineJSON{Kind: "requested", From: req.From, To: req.To, Rule: req.Rule, Request: req.Number})
		touches = append(touches, gate.HomeError{Rule: req.Rule, From: req.From, To: req.To})
	}
	for _, e := range view.Drift {
		add(homeLineJSON{Kind: "drift", From: e.From, To: e.To, Rule: e.Rule})
	}
	touches = append(touches, view.Drift...)
	touches = append(touches, view.Touched...)
	for _, rule := range pol.Rules {
		if rule.Deny == nil {
			continue
		}
		for _, touch := range touches {
			if touch.Rule == rule.ID || slices.Contains(rule.Deny.From, touch.From) && slices.Contains(rule.Deny.To, touch.To) {
				add(homeLineJSON{Kind: "deny", Rule: rule.ID, From: touch.From, To: touch.To})
			}
		}
	}
	for _, x := range pol.Exceptions {
		line := homeLineJSON{Kind: "exception", Rule: x.Rule, From: x.From, To: x.To, Expires: x.Expires}
		if left, ok := homeDays(x.Expires, today); ok {
			line.DaysLeft = &left
		}
		add(line)
	}
	slices.SortFunc(out.Lines, func(a, b homeLineJSON) int {
		for _, pair := range [][2]string{{a.From, b.From}, {a.To, b.To}, {a.Kind, b.Kind}, {a.Rule, b.Rule}, {a.Expires, b.Expires}} {
			if c := cmp.Compare(pair[0], pair[1]); c != 0 {
				return c
			}
		}
		return cmp.Compare(a.Request, b.Request)
	})
	return out
}

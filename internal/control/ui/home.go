package ui

import (
	"cmp"
	"fmt"
	"net/http"
	"slices"
	"strconv"
	"strings"
	"time"

	"github.com/alexei-led/archfit-app/internal/gate"
	"github.com/alexei-led/archfit-app/internal/wire/policy3"
)

const homeExpiryDays = 7

type homeJSON struct {
	Status   string         `json:"status"`
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
	setup, err := s.apiSetup(r, sc)
	if err != nil {
		return nil, err
	}
	state := setup.(setupJSON)
	if state.State != "approved" && state.State != "outside_flow" {
		return homeJSON{Status: "Set up this repository.", Setup: &state}, nil
	}
	view, err := s.home.Home(r.Context(), sc.repo)
	if err != nil {
		return nil, err
	}
	today := s.now().UTC().Truncate(24 * time.Hour)
	return homeBody(view, state, sc.repo.FullName, today), nil
}

func homeBody(view gate.HomeState, setup setupJSON, repo string, today time.Time) homeJSON {
	out := homeJSON{NeedsYou: []homeNeedJSON{}, Map: homeMap(view, today)}
	base := "/r/" + repo
	policyHref := base + "/policy"
	issueHref := func(n int) string {
		if n > 0 {
			return "https://github.com/" + repo + "/issues/" + strconv.Itoa(n)
		}
		return policyHref
	}
	var status []string
	if view.OutsideFlow || setup.State == "outside_flow" {
		status = append(status, "policy changed outside the flow")
		out.NeedsYou = append(out.NeedsYou,
			homeNeedJSON{Kind: "confirm", Text: "Confirm the policy changed outside the flow.", Href: base + "/owner/confirm"},
			homeNeedJSON{Kind: "revert", Text: "Revert the policy changed outside the flow.", Href: base + "/owner/revert"})
	}
	if n := len(view.Requests); n > 0 {
		if n == 1 {
			status = append(status, "1 request waits")
		} else {
			status = append(status, fmt.Sprintf("%d requests wait", n))
		}
	}
	for _, req := range view.Requests {
		out.NeedsYou = append(out.NeedsYou, homeNeedJSON{Kind: "request", Text: fmt.Sprintf("Request #%d waits.", req.Number),
			Href: base + "/requests/" + strconv.Itoa(req.Number), Request: req.Number, Rule: req.Rule, From: req.From, To: req.To, Record: req.Record})
	}
	for _, x := range view.Policy.Exceptions {
		left, ok := homeDays(x.Expires, today)
		if !ok {
			continue
		}
		text := homeExceptionStatus(left)
		status = append(status, text)
		if left > homeExpiryDays {
			continue
		}
		key := gate.HomeException{Rule: x.Rule, From: x.From, To: x.To, Expires: x.Expires}
		out.NeedsYou = append(out.NeedsYou, homeNeedJSON{Kind: "exception", Text: text, Href: issueHref(view.ExceptionIssues[key]), Rule: x.Rule,
			From: x.From, To: x.To, Expires: x.Expires, DaysLeft: &left})
	}
	if len(view.Drift) > 0 || view.DriftIssue > 0 {
		status = append(status, "drift")
		out.NeedsYou = append(out.NeedsYou, homeNeedJSON{Kind: "drift", Text: "Drift on the default branch.", Href: issueHref(view.DriftIssue)})
	}
	switch {
	case !view.DriftKnown:
		status = append(status, "repository state is not known")
	case view.DriftUnread:
		status = append(status, "the check could not read every place")
	case view.RepositoryState == "expired" && !slices.ContainsFunc(view.Policy.Exceptions, func(x policy3.Exception) bool { left, ok := homeDays(x.Expires, today); return ok && left < 0 }):
		status = append(status, "expired")
	}
	for _, pull := range view.WorkflowPulls {
		out.NeedsYou = append(out.NeedsYou, homeNeedJSON{Kind: "workflow", Text: fmt.Sprintf("Approve the workflow change in #%d.", pull),
			Href: base + "/owner/workflow?pull=" + strconv.Itoa(pull), Pull: pull})
	}
	if n := len(view.WorkflowPulls); n > 0 {
		if n == 1 {
			status = append(status, "1 workflow change waits")
		} else {
			status = append(status, fmt.Sprintf("%d workflow changes wait", n))
		}
	}
	out.Status = strings.Join(status, "; ")
	if out.Status == "" {
		out.Status = "clean"
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
			if slices.Contains(rule.Deny.From, touch.From) && slices.Contains(rule.Deny.To, touch.To) {
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

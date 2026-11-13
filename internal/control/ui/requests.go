package ui

import (
	"net/http"
	"strconv"

	"github.com/alexei-led/archfit-app/internal/gate"
	"github.com/alexei-led/archfit-app/internal/github/api"
	"github.com/alexei-led/archfit-app/internal/wire/policy3"
)

// The request page's read API (design 10.4, 12.3; M2 task 17). It shows the owner what a request asks and what each
// answer would do; it answers nothing. The texts are the request's and the policy's; the record is the check's.

type requestJSON struct {
	Dependencies []requestDependencyJSON `json:"dependencies"`
	IssueHref    string                  `json:"issue_href"`
	Number       int                     `json:"number"`
	State        string                  `json:"state"` // open, answered or closed
	Reason       string                  `json:"reason"`
	Rule         string                  `json:"rule"`
	Path         string                  `json:"path,omitempty"`
	From         string                  `json:"from,omitempty"`
	To           string                  `json:"to,omitempty"`
	Expires      string                  `json:"expires,omitempty"`
	Record       string                  `json:"record"`
	Verified     bool                    `json:"verified"` // false: the numbers are "as reported" by the CLI
	Balance      *balanceJSON            `json:"balance,omitempty"`
	Policy       bool                    `json:"policy"` // false: the default branch has no policy of version 3
	Layers       []string                `json:"layers"`
	Modules      []moduleJSON            `json:"modules"`
	Allowed      bool                    `json:"allowed"`
	Earlier      []earlierJSON           `json:"earlier"`
	Answer       *requestAnswerJSON      `json:"answer,omitempty"`
}

type requestDependencyJSON struct {
	Kind    string `json:"kind"`
	Rule    string `json:"rule"`
	From    string `json:"from"`
	To      string `json:"to"`
	Expires string `json:"expires,omitempty"`
}

type requestAnswerJSON struct {
	Kind    string `json:"kind"`
	Until   string `json:"until,omitempty"`
	Instead string `json:"instead,omitempty"`
}

type balanceJSON struct {
	Value int `json:"value"`
	Max   int `json:"max"`
	Min   int `json:"min"`
}

type moduleJSON struct {
	Name       string   `json:"name"`
	Layer      string   `json:"layer,omitempty"`
	Volatility string   `json:"volatility,omitempty"`
	Subdomain  string   `json:"subdomain,omitempty"`
	DependsOn  []string `json:"depends_on"`
}

type earlierJSON struct {
	Kind    string `json:"kind"` // deny or exception
	Rule    string `json:"rule"`
	From    string `json:"from"`
	To      string `json:"to,omitempty"`
	Why     string `json:"why,omitempty"`
	Instead string `json:"instead,omitempty"`
	Expires string `json:"expires,omitempty"`
}

type previewJSON struct {
	Answer       string           `json:"answer"`
	Until        string           `json:"until,omitempty"`
	Diff         string           `json:"diff"` // "" when the answer writes no policy line
	PolicyDigest string           `json:"policy_digest"`
	Loosens      bool             `json:"loosens"`
	Pulls        []pullEffectJSON `json:"pulls"`
}

type pullEffectJSON struct {
	Number int    `json:"number"`
	Status string `json:"status"`
	Waits  bool   `json:"waits"`
	Clears int    `json:"clears"`
	Left   int    `json:"left"`
}

// apiRequest is GET /api/repos/{o}/{n}/requests/{number}.
func (s *server) apiRequest(r *http.Request, sc scope) (any, error) {
	number, ok := requestNumber(r)
	if !ok {
		return nil, api.ErrNotFound
	}
	v, err := s.requests.RequestView(r.Context(), gate.Target{Repo: sc.repo}, number)
	if err != nil {
		return nil, err
	}
	body := requestJSON{IssueHref: v.IssueHref, Number: v.Number, State: v.State, Reason: v.Reason, Rule: v.Rule, Path: v.Path, From: v.From,
		To: v.To, Expires: v.Expires, Record: v.Record, Verified: v.Numbers.Verified, Policy: v.Policy, Allowed: v.Allowed,
		Dependencies: []requestDependencyJSON{}, Layers: append([]string{}, v.Layers...), Modules: []moduleJSON{}, Earlier: []earlierJSON{}}
	for _, d := range v.Dependencies {
		body.Dependencies = append(body.Dependencies, requestDependencyJSON{Kind: d.Kind, Rule: d.Rule, From: d.From, To: d.To, Expires: d.Expires})
	}
	if v.Answer != nil {
		body.Answer = &requestAnswerJSON{Kind: string(v.Answer.Kind), Until: v.Answer.Until, Instead: v.Answer.Instead}
	}
	if b := v.Numbers.Balance; b != nil {
		body.Balance = &balanceJSON{Value: b.Score, Max: b.Max, Min: b.Min}
	}
	for _, m := range v.Modules {
		body.Modules = append(body.Modules, moduleJSON{Name: m.Name, Layer: m.Layer, Volatility: m.Volatility,
			Subdomain: m.Subdomain, DependsOn: append([]string{}, m.DependsOn...)})
	}
	for _, e := range v.Earlier {
		body.Earlier = append(body.Earlier, earlierJSON{Kind: e.Kind, Rule: e.Rule, From: e.From, To: e.To, Why: e.Why,
			Instead: e.Instead, Expires: e.Expires})
	}
	return body, nil
}

// apiRequestPreview is GET /api/repos/{o}/{n}/requests/{number}/preview?answer=…&until=…&instead=….
func (s *server) apiRequestPreview(r *http.Request, sc scope) (any, error) {
	number, ok := requestNumber(r)
	if !ok {
		return nil, api.ErrNotFound
	}
	q := r.URL.Query()
	a := gate.RequestAnswer{Kind: policy3.Answer(q.Get("answer")), Until: q.Get("until"), Instead: q.Get("instead")}
	p, err := s.requests.RequestPreview(r.Context(), gate.Target{Repo: sc.repo}, number, a)
	if err != nil {
		return nil, err
	}
	body := previewJSON{Answer: string(p.Answer.Kind), Until: p.Answer.Until, Diff: p.Diff, PolicyDigest: p.PolicyDigest,
		Loosens: p.Loosens, Pulls: []pullEffectJSON{}}
	for _, e := range p.Pulls {
		body.Pulls = append(body.Pulls, pullEffectJSON{Number: e.Number, Status: e.Status, Waits: e.Waits, Clears: e.Clears,
			Left: e.Left})
	}
	return body, nil
}

func requestNumber(r *http.Request) (int, bool) {
	number, err := strconv.Atoi(r.PathValue("number"))
	return number, err == nil && number > 0
}

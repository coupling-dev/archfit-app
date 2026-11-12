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
	Number   int           `json:"number"`
	State    string        `json:"state"` // open, answered or closed
	Reason   string        `json:"reason"`
	Rule     string        `json:"rule"`
	Path     string        `json:"path,omitempty"`
	From     string        `json:"from,omitempty"`
	To       string        `json:"to,omitempty"`
	Expires  string        `json:"expires,omitempty"`
	Record   string        `json:"record"`
	Verified bool          `json:"verified"` // false: the numbers are "as reported" by the CLI
	Balance  *balanceJSON  `json:"balance,omitempty"`
	Policy   bool          `json:"policy"` // false: the default branch has no policy of version 3
	Modules  []moduleJSON  `json:"modules"`
	Allowed  bool          `json:"allowed"`
	Earlier  []earlierJSON `json:"earlier"`
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
	body := requestJSON{Number: v.Number, State: v.State, Reason: v.Reason, Rule: v.Rule, Path: v.Path, From: v.From,
		To: v.To, Expires: v.Expires, Record: v.Record, Verified: v.Numbers.Verified, Policy: v.Policy, Allowed: v.Allowed,
		Modules: []moduleJSON{}, Earlier: []earlierJSON{}}
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

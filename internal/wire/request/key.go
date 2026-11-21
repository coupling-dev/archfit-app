package request

import (
	"errors"
	"fmt"
)

// ErrNoKey means an error or an answer has no request key.
var ErrNoKey = errors.New("request: no request key")

// The rule ids the key treats apart.
const (
	ruleDuplication = "duplication"
	ruleCycle       = "cycle"
	ruleUnowned     = "unowned"
	ruleException   = "exception"
)

// Error is what a request key needs of one result error; the caller copies these fields of the result's error.
type Error struct {
	Rule string
	Path string
	From string
	To   string
	Key  string // the engine's key of the error
}

// RequestKey returns the key of the request the error asks for: errors with the same key are one request.
func (e Error) RequestKey() (string, error) {
	switch e.Rule {
	case "":
		return "", fmt.Errorf("%w: the error has no rule", ErrNoKey)
	case ruleException:
		return "", fmt.Errorf("%w: an expired exception asks nothing", ErrNoKey)
	case ruleDuplication:
		if e.Key == "" {
			return "", fmt.Errorf("%w: a copy without the engine's key", ErrNoKey)
		}
		return e.Key, nil
	case ruleUnowned:
		if e.Path == "" {
			return "", fmt.Errorf("%w: unowned code without a path", ErrNoKey)
		}
		return ruleUnowned + " " + e.Path, nil
	}
	return dependencyKey(e.Rule, e.From, e.To)
}

// Answer is the `can` answer an issue reports: the dependency, the rule that decides it, its balance and, for a use
// past the target's surface (engine 0.9.0), that surface.
type Answer struct {
	Rule    string   `json:"rule"`
	From    string   `json:"from"`
	To      string   `json:"to"`
	Balance *Balance `json:"balance,omitempty"`
	Surface *Surface `json:"surface,omitempty"`
	// Package is the package past the surface, read from the head "<pkg> is past the surface" of a coupling answer that
	// has a surface line; "" for any other answer.
	Package string `json:"package,omitempty"`
}

// Balance is the coupling number of a dependency, "Value/10 (min Min)". Engine 0.9.0 adds the inputs it was computed
// with, "Value/10 (min Min): strength, to is volatility"; engine v4.0.0 does not (until task 6.5).
type Balance struct {
	Value      int    `json:"value"`
	Min        int    `json:"min"`
	Strength   string `json:"strength,omitempty"`
	Volatility string `json:"volatility,omitempty"`
}

// Surface is the declared surface of the target a `can` answer prints on its surface line.
type Surface struct {
	Public []string `json:"public"`
	Model  []string `json:"model"`
}

// Key returns the key of the request the answer asks for. A `can` answer judges one dependency, so it never names a
// copy, a cycle, unowned code or an exception.
func (a Answer) Key() (string, error) {
	switch a.Rule {
	case "", ruleDuplication, ruleCycle, ruleUnowned, ruleException:
		return "", fmt.Errorf("%w: a can answer never asks about this rule", ErrNoKey)
	}
	return dependencyKey(a.Rule, a.From, a.To)
}

func dependencyKey(rule, from, to string) (string, error) {
	if from == "" || to == "" {
		return "", fmt.Errorf("%w: a dependency needs both modules", ErrNoKey)
	}
	return rule + " " + from + " " + to, nil
}

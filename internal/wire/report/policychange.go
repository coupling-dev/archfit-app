package report

import (
	"bytes"
	"encoding/json"
	"reflect"
	"slices"
	"strings"
	"unicode/utf8"
)

// PolicyChange is the engine's classification of a change to the protected policy (`archfit config compare`,
// archfit.config-compare.v1, engine E11): what the candidate configuration changes in what blocks and in what the
// analysis reads. It is computed from declarations only. A loosening always needs a human approver.
type PolicyChange struct {
	Direction string // "none" | "tightening" | "loosening" | "mixed", re-derived from the lists below

	// Loosening: each can let a change pass that fails today.
	RulesRemoved        []PolicyRule
	RulesLoosened       []PolicyRuleDelta
	WaiversAdded        []PolicyWaiver
	AllowlistsWidened   []PolicyAllowlist
	CoverageLost        []PolicyCoverage
	DeclarationsChanged []PolicyDeclaration // differences that cannot be ordered; they count as loosening

	// Tightening: each can fail a change that passes today.
	RulesAdded         []PolicyRule
	RulesTightened     []PolicyRuleDelta
	WaiversRemoved     []PolicyWaiver
	AllowlistsNarrowed []PolicyAllowlist
	CoverageGained     []PolicyCoverage
}

// Policy change directions.
const (
	DirectionNone       = "none"
	DirectionTightening = "tightening"
	DirectionLoosening  = "loosening"
	DirectionMixed      = "mixed"
)

type PolicyRule struct{ ID, Type, Gate string }

// PolicyRuleDelta is one rule on both sides whose Field (gate, max, selector or patterns) changed.
type PolicyRuleDelta struct{ ID, Field, From, To string }

type PolicyWaiver struct{ Rule, From, To, ApprovedBy, Expires string }

// PolicyAllowlist is one module's depends_on or visible_to; "any" is an absent key, "none" an empty list.
type PolicyAllowlist struct{ Module, Field, From, To string }

// PolicyCoverage is one declaration that changes what the analysis reads.
type PolicyCoverage struct{ Kind, Detail string }

type PolicyDeclaration struct{ Key, From, To string }

// Loosens counts the changes that can let a change pass that fails today; Tightens the mirror.
func (p PolicyChange) Loosens() int {
	return len(p.RulesRemoved) + len(p.RulesLoosened) + len(p.WaiversAdded) + len(p.AllowlistsWidened) +
		len(p.CoverageLost) + len(p.DeclarationsChanged)
}

func (p PolicyChange) Tightens() int {
	return len(p.RulesAdded) + len(p.RulesTightened) + len(p.WaiversRemoved) + len(p.AllowlistsNarrowed) +
		len(p.CoverageGained)
}

// direction is the engine's rule, applied by the App itself: the stated direction decides who may approve, so it is
// never taken from the document on trust.
func (p PolicyChange) direction() string {
	switch l, t := p.Loosens() > 0, p.Tightens() > 0; {
	case l && t:
		return DirectionMixed
	case l:
		return DirectionLoosening
	case t:
		return DirectionTightening
	}
	return DirectionNone
}

// The closed value sets of the engine (internal/assessment/decision/policy_change.go at feec08c3).
var (
	policyDirections     = []string{DirectionNone, DirectionTightening, DirectionLoosening, DirectionMixed}
	ruleDeltaFields      = []string{"gate", "max", "selector", "patterns"}
	allowlistFields      = []string{"depends_on", "visible_to"}
	coverageLostKinds    = []string{"exclude_added", "language_disabled", "language_mode_lowered", "analyzer_disabled", "module_removed", "module_paths_removed", "module_review_gate_lowered"}
	coverageGainedKinds  = []string{"exclude_removed", "language_enabled", "language_mode_raised", "analyzer_enabled", "module_review_gate_raised"}
	maxPolicyChangeItems = 1000 // per list; a policy with more changes than this is not reviewed in a card
	maxPolicyChangeBytes = 1 << 20
)

type shadowPolicyChange struct {
	Direction           *string                   `json:"direction"`
	RulesRemoved        []shadowPolicyRule        `json:"rules_removed"`
	RulesLoosened       []shadowPolicyRuleDelta   `json:"rules_loosened"`
	WaiversAdded        []shadowPolicyWaiver      `json:"waivers_added"`
	AllowlistsWidened   []shadowPolicyAllowlist   `json:"allowlists_widened"`
	CoverageLost        []shadowPolicyCoverage    `json:"coverage_lost"`
	DeclarationsChanged []shadowPolicyDeclaration `json:"declarations_changed"`
	RulesAdded          []shadowPolicyRule        `json:"rules_added"`
	RulesTightened      []shadowPolicyRuleDelta   `json:"rules_tightened"`
	WaiversRemoved      []shadowPolicyWaiver      `json:"waivers_removed"`
	AllowlistsNarrowed  []shadowPolicyAllowlist   `json:"allowlists_narrowed"`
	CoverageGained      []shadowPolicyCoverage    `json:"coverage_gained"`
}

type shadowPolicyRule struct {
	ID   *string `json:"id"`
	Type *string `json:"type"`
	Gate *string `json:"gate"`
}

type shadowPolicyRuleDelta struct {
	ID    *string `json:"id"`
	Field *string `json:"field"`
	From  *string `json:"from"`
	To    *string `json:"to"`
}

type shadowPolicyWaiver struct {
	Rule       *string `json:"rule"`
	From       string  `json:"from"`
	To         string  `json:"to"`
	ApprovedBy *string `json:"approved_by"`
	Expires    *string `json:"expires"`
}

type shadowPolicyAllowlist struct {
	Module *string `json:"module"`
	Field  *string `json:"field"`
	From   *string `json:"from"`
	To     *string `json:"to"`
}

type shadowPolicyCoverage struct {
	Kind   *string `json:"kind"`
	Detail *string `json:"detail"`
}

type shadowPolicyDeclaration struct {
	Key  *string `json:"key"`
	From *string `json:"from"`
	To   *string `json:"to"`
}

// DecodePolicyChange strictly parses the policy_change object of archfit.config-compare.v1. Every list is required
// (the engine always writes it, empty when nothing changed), every value set is closed, and the stated direction must
// be the one the lists give. Errors name the field, never the value.
func DecodePolicyChange(raw []byte) (PolicyChange, error) {
	if len(raw) > maxPolicyChangeBytes {
		return PolicyChange{}, locError("policy_change", "is too large")
	}
	if !utf8.Valid(raw) {
		return PolicyChange{}, locError("policy_change", "is not valid UTF-8")
	}
	if err := prepassAt(raw, reflect.TypeFor[shadowPolicyChange](), []seg{{"policy_change", -1}}); err != nil {
		return PolicyChange{}, err
	}
	var s shadowPolicyChange
	dec := json.NewDecoder(bytes.NewReader(raw))
	dec.DisallowUnknownFields()
	if err := dec.Decode(&s); err != nil {
		return PolicyChange{}, locError("policy_change", "invalid JSON")
	}
	c := &checker{ids: map[string]struct{}{}}
	c.path = append(c.path, seg{"policy_change", -1})
	p := c.policyChange(&s)
	if err := c.err(); err != nil {
		return PolicyChange{}, err
	}
	return p, nil
}

func (c *checker) policyChange(s *shadowPolicyChange) PolicyChange {
	rule := func(r *shadowPolicyRule) PolicyRule {
		return PolicyRule{ID: c.str("id", r.ID, classIdentifier), Type: c.str("type", r.Type, classToken), Gate: c.str("gate", r.Gate, classToken)}
	}
	delta := func(r *shadowPolicyRuleDelta) PolicyRuleDelta {
		d := PolicyRuleDelta{ID: c.str("id", r.ID, classIdentifier), Field: c.str("field", r.Field, classToken),
			From: c.str("from", r.From, classText), To: c.str("to", r.To, classText)}
		c.oneOf("field", d.Field, ruleDeltaFields)
		return d
	}
	waiver := func(w *shadowPolicyWaiver) PolicyWaiver {
		return PolicyWaiver{Rule: c.str("rule", w.Rule, classIdentifier), From: c.opt("from", w.From, classText),
			To: c.opt("to", w.To, classText), ApprovedBy: c.str("approved_by", w.ApprovedBy, classText),
			Expires: c.str("expires", w.Expires, classToken)}
	}
	allow := func(a *shadowPolicyAllowlist) PolicyAllowlist {
		l := PolicyAllowlist{Module: c.str("module", a.Module, classIdentifier), Field: c.str("field", a.Field, classToken),
			From: c.str("from", a.From, classText), To: c.str("to", a.To, classText)}
		c.oneOf("field", l.Field, allowlistFields)
		return l
	}
	coverage := func(kinds []string) func(*shadowPolicyCoverage) PolicyCoverage {
		return func(v *shadowPolicyCoverage) PolicyCoverage {
			cv := PolicyCoverage{Kind: c.str("kind", v.Kind, classToken), Detail: c.str("detail", v.Detail, classText)}
			c.oneOf("kind", cv.Kind, kinds)
			return cv
		}
	}
	decl := func(d *shadowPolicyDeclaration) PolicyDeclaration {
		return PolicyDeclaration{Key: c.str("key", d.Key, classIdentifier), From: c.str("from", d.From, classText), To: c.str("to", d.To, classText)}
	}
	n := maxPolicyChangeItems
	p := PolicyChange{
		Direction:           c.str("direction", s.Direction, classToken),
		RulesRemoved:        list(c, "rules_removed", s.RulesRemoved, n, true, rule),
		RulesLoosened:       list(c, "rules_loosened", s.RulesLoosened, n, true, delta),
		WaiversAdded:        list(c, "waivers_added", s.WaiversAdded, n, true, waiver),
		AllowlistsWidened:   list(c, "allowlists_widened", s.AllowlistsWidened, n, true, allow),
		CoverageLost:        list(c, "coverage_lost", s.CoverageLost, n, true, coverage(coverageLostKinds)),
		DeclarationsChanged: list(c, "declarations_changed", s.DeclarationsChanged, n, true, decl),
		RulesAdded:          list(c, "rules_added", s.RulesAdded, n, true, rule),
		RulesTightened:      list(c, "rules_tightened", s.RulesTightened, n, true, delta),
		WaiversRemoved:      list(c, "waivers_removed", s.WaiversRemoved, n, true, waiver),
		AllowlistsNarrowed:  list(c, "allowlists_narrowed", s.AllowlistsNarrowed, n, true, allow),
		CoverageGained:      list(c, "coverage_gained", s.CoverageGained, n, true, coverage(coverageGainedKinds)),
	}
	c.oneOf("direction", p.Direction, policyDirections)
	if s.Direction != nil && slices.Contains(policyDirections, p.Direction) && p.Direction != p.direction() {
		c.fail(phaseEnum, "direction", "must be "+p.direction()+", the direction its lists give")
	}
	return p
}

// oneOf fails when v is not one of values; v itself is never named.
func (c *checker) oneOf(key, v string, values []string) {
	if v != "" && !slices.Contains(values, v) {
		c.fail(phaseEnum, key, "must be one of "+strings.Join(values, ", "))
	}
}

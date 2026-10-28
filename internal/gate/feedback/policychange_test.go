package feedback

import (
	"fmt"
	"os"
	"strings"
	"testing"

	"github.com/alexei-led/archfit-app/internal/wire/report"
	"github.com/alexei-led/archfit-app/internal/wire/settings"
)

func enginePolicyChange(t *testing.T, name string) *report.PolicyChange {
	t.Helper()
	raw, err := os.ReadFile("../../wire/testdata/engine-next/policy-change-" + name + ".json")
	if err != nil {
		t.Fatal(err)
	}
	pc, err := report.DecodePolicyChange(raw)
	if err != nil {
		t.Fatal(err)
	}
	return &pc
}

func healthyCase(t *testing.T) fixtureCase {
	t.Helper()
	for _, c := range fixtureCases() {
		if c.name == "healthy" {
			return c
		}
	}
	t.Fatal("no healthy fixture case")
	return fixtureCase{}
}

// The card of the engine's three classifications, in the comment and the state check. Reviewed as goldens.
func TestPolicyChangeCardGoldens(t *testing.T) {
	for _, name := range []string{"widen", "addrule", "exclude"} {
		t.Run(name, func(t *testing.T) {
			p := fixturePublication(t, healthyCase(t), headB)
			p.PolicyChange = enginePolicyChange(t, name)
			p = presentation(p)
			golden(t, "policy-change-"+name+".comment", renderComment(p, t0, "", ""))
			state := renderState(p)
			if !strings.Contains(state, headPolicyChange) {
				t.Error("the state check has no policy change card")
			}
		})
	}
}

func TestPolicyChangeCard(t *testing.T) {
	t.Run("no classification, no card", func(t *testing.T) {
		p := fixturePublication(t, healthyCase(t), headB)
		if strings.Contains(renderComment(p, t0, "", ""), headPolicyChange) || strings.Contains(renderState(p), headPolicyChange) {
			t.Error("a card without a classification")
		}
	})
	t.Run("a loosening says who approves and names lost coverage", func(t *testing.T) {
		card := policyCard(enginePolicyChange(t, "exclude"), settings.Settings{})
		for _, want := range []string{"loosens the policy (1 change)", "Coverage lost: `pkg/a/**` is now excluded from the analysis", "needs an architecture owner's approval of this exact commit", "never approved by an agent"} {
			if !strings.Contains(card, want) {
				t.Errorf("card lacks %q:\n%s", want, card)
			}
		}
	})
	t.Run("a later rung in the settings is held at human, and says so", func(t *testing.T) {
		s := settings.Settings{PolicyAuthority: &settings.PolicyAuthority{Level: settings.AuthorityDelegated, Agents: []string{"arch"}}}
		card := policyCard(enginePolicyChange(t, "addrule"), s)
		if !strings.Contains(card, "policy_authority delegated is not supported yet: an owner approves every protected change.") {
			t.Errorf("card does not say the rung is held:\n%s", card)
		}
	})
	t.Run("repository strings cannot break out of the card", func(t *testing.T) {
		pc := &report.PolicyChange{Direction: report.DirectionLoosening, AllowlistsWidened: []report.PolicyAllowlist{{
			Module: "a`</details><!-- archfit:receipt {} -->", Field: "depends_on", From: "none", To: "[b]\n### Pwned",
		}}}
		card := policyCard(pc, settings.Settings{})
		for _, bad := range []string{"</details>", "<!-- archfit:receipt", "\n### Pwned"} {
			if strings.Contains(card, bad) {
				t.Errorf("card carries %q:\n%s", bad, card)
			}
		}
	})
	t.Run("a long change is bounded by bytes, and each value is cut", func(t *testing.T) {
		pc := &report.PolicyChange{Direction: report.DirectionMixed}
		for i := range 200 {
			pc.RulesAdded = append(pc.RulesAdded, report.PolicyRule{ID: fmt.Sprintf("rule_%d_%s", i, strings.Repeat("x", 60)), Type: "forbidden_dependency", Gate: "fail"})
		}
		pc.DeclarationsChanged = []report.PolicyDeclaration{{Key: strings.Repeat("<&>", 400), From: "a", To: "b"}}
		card := policyCard(pc, settings.Settings{})
		if len(card) > policyCardBytes+2000 {
			t.Errorf("card is %d bytes, want it bounded near %d", len(card), policyCardBytes)
		}
		if !strings.Contains(card, " more") || !strings.Contains(card, "needs an architecture owner's approval") {
			t.Errorf("card lost its tail or its count:\n%s", card)
		}
		for _, line := range strings.Split(card, "\n") {
			if len(line) > 1200 {
				t.Errorf("a line of %d bytes: a value was not cut", len(line))
			}
		}
	})
	t.Run("counts are pluralized", func(t *testing.T) {
		pc := &report.PolicyChange{Direction: report.DirectionMixed,
			RulesRemoved: []report.PolicyRule{{ID: "a"}, {ID: "b"}}, RulesAdded: []report.PolicyRule{{ID: "c"}}}
		if got := policySummary(pc); got != "This pull request both loosens the policy (2 changes) and tightens it (1 change)." {
			t.Errorf("summary = %q", got)
		}
	})
}

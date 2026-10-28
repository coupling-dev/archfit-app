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
		for _, want := range []string{"loosens the policy: 1 loosening", "Coverage lost: exclude_added `pkg/a/**`", "needs an architecture owner's approval of this exact commit", "never approved by an agent", "code the analysis no longer reads"} {
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
	t.Run("a long change is capped", func(t *testing.T) {
		pc := &report.PolicyChange{Direction: report.DirectionTightening}
		for i := range policyCardItems + 5 {
			pc.RulesAdded = append(pc.RulesAdded, report.PolicyRule{ID: fmt.Sprintf("rule_%d", i), Type: "forbidden_dependency", Gate: "fail"})
		}
		card := policyCard(pc, settings.Settings{})
		if strings.Count(card, "\n- Rule added") != policyCardItems || !strings.Contains(card, "- and 5 more") {
			t.Errorf("card not capped at %d:\n%s", policyCardItems, card)
		}
	})
}

package settings

import (
	"reflect"
	"strings"
	"testing"
)

// withAuthority is the base file with a policy_authority block after data_mode, where Render writes it.
func withAuthority(text string) []byte {
	return append(baseWith("", ""), []byte(text)...)
}

func TestParsePolicyAuthority(t *testing.T) {
	tests := []struct {
		name string
		text string
		want *PolicyAuthority
	}{
		{"absent means human", "", nil},
		{"human", "policy_authority:\n  level: human\n", &PolicyAuthority{Level: AuthorityHuman}},
		{"co-sign with architects", "policy_authority:\n  level: co-sign\n  agents:\n    - architect[bot]\n",
			&PolicyAuthority{Level: AuthorityCoSign, Agents: []string{"architect[bot]"}}},
		{"delegated with tighten_auto", "policy_authority:\n  level: delegated\n  agents: [arch]\n  tighten_auto: true\n",
			&PolicyAuthority{Level: AuthorityDelegated, Agents: []string{"arch"}, TightenAuto: true}},
		{"tighten_auto false", "policy_authority:\n  level: human\n  tighten_auto: false\n", &PolicyAuthority{Level: AuthorityHuman}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := Parse(withAuthority(tt.text))
			if err != nil {
				t.Fatalf("Parse: %v", err)
			}
			if !reflect.DeepEqual(got.PolicyAuthority, tt.want) {
				t.Errorf("PolicyAuthority = %+v, want %+v", got.PolicyAuthority, tt.want)
			}
			again, err := Parse(Render(got))
			if err != nil || !reflect.DeepEqual(again, got) {
				t.Errorf("round trip: %+v, %v; want %+v", again, err, got)
			}
		})
	}
}

// Only human is in force in this release: a file that names a later rung stays valid, and the rung in force is human.
func TestOnlyHumanIsInForce(t *testing.T) {
	tests := []struct {
		name     string
		text     string
		wantHeld bool
	}{
		{"absent", "", false},
		{"human", "policy_authority:\n  level: human\n", false},
		{"co-sign", "policy_authority:\n  level: co-sign\n  agents: [architect]\n", true},
		{"delegated with tighten_auto", "policy_authority:\n  level: delegated\n  agents: [architect]\n  tighten_auto: true\n", true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			s, err := Parse(withAuthority(tt.text))
			if err != nil {
				t.Fatalf("Parse: %v", err)
			}
			level, held := s.Effective()
			if level != AuthorityHuman {
				t.Errorf("rung in force = %q, want human", level)
			}
			if (held != "") != tt.wantHeld {
				t.Errorf("held = %q, want a reason: %v", held, tt.wantHeld)
			}
		})
	}
}

func TestPolicyAuthorityRules(t *testing.T) {
	tests := []struct {
		name, text, wantKey string
	}{
		{"not a mapping", "policy_authority: human\n", "policy_authority"},
		{"no level", "policy_authority:\n  agents: [a]\n", "policy_authority.level"},
		{"unknown level", "policy_authority:\n  level: agent\n", "policy_authority.level"},
		{"level in other case", "policy_authority:\n  level: Human\n", "policy_authority.level"},
		{"level not a string", "policy_authority:\n  level: 1\n", "policy_authority.level"},
		{"unknown key", "policy_authority:\n  level: human\n  owners: [a]\n", "policy_authority"},
		{"duplicate key", "policy_authority:\n  level: human\n  level: co-sign\n", "policy_authority.level"},
		{"agents not a list", "policy_authority:\n  level: co-sign\n  agents: arch\n", "policy_authority.agents"},
		{"a team as an agent", "policy_authority:\n  level: co-sign\n  agents: [org/team]\n", "policy_authority.agents"},
		{"a duplicate agent", "policy_authority:\n  level: co-sign\n  agents: [arch, 'ARCH[bot]']\n", "policy_authority.agents"},
		{"tighten_auto not a boolean", "policy_authority:\n  level: human\n  tighten_auto: yes please\n", "policy_authority.tighten_auto"},
		{"tighten_auto quoted", "policy_authority:\n  level: human\n  tighten_auto: 'true'\n", "policy_authority.tighten_auto"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			_, err := Parse(withAuthority(tt.text))
			if err == nil {
				t.Fatal("Parse accepted it")
			}
			if !strings.Contains(err.Error(), tt.wantKey) {
				t.Errorf("error %q does not name %q", err, tt.wantKey)
			}
		})
	}
}

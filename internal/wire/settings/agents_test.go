package settings

import (
	"reflect"
	"strings"
	"testing"
)

func agentsBlock(agents ...string) string {
	var b strings.Builder
	b.WriteString("agents:\n")
	for _, a := range agents {
		b.WriteString("  - " + a + "\n")
	}
	return b.String()
}

// withAgents is the base file with an agents key after owners, where Render writes it.
func withAgents(text string) []byte {
	return baseWith("owners", "owners:\n  - alice\n  - acme/architects\n"+text)
}

func TestParseAgents(t *testing.T) {
	tests := []struct {
		name string
		text string
		want []string
	}{
		{"absent", "", nil},
		{"empty list", "agents: []\n", nil},
		{"logins and app slugs", agentsBlock("alice-agent", "copilot-swe-agent[bot]", "Claude"), []string{"alice-agent", "copilot-swe-agent[bot]", "Claude"}},
		{"an owner is also an agent", agentsBlock("alice"), []string{"alice"}},
		{"39 characters before the suffix", agentsBlock(strings.Repeat("a", 39) + "[bot]"), []string{strings.Repeat("a", 39) + "[bot]"}},
		{"flow list", "agents: [bot-one, 'two[bot]']\n", []string{"bot-one", "two[bot]"}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := Parse(withAgents(tt.text))
			if err != nil {
				t.Fatalf("Parse: %v", err)
			}
			if !reflect.DeepEqual(got.Agents, tt.want) {
				t.Errorf("Agents = %#v, want %#v", got.Agents, tt.want)
			}
			if !reflect.DeepEqual(got.Owners, []string{"alice", "acme/architects"}) {
				t.Errorf("Owners = %q: agents must not change the owners", got.Owners)
			}
		})
	}
}

func TestParseAgentsRules(t *testing.T) {
	tests := []struct {
		name, text string
	}{
		{"scalar", "agents: claude\n"},
		{"null", "agents:\n"},
		{"team", agentsBlock("acme/agents")},
		{"at prefix", agentsBlock(`"@claude"`)},
		{"space", agentsBlock("claude code")},
		{"suffix only", agentsBlock(`"[bot]"`)},
		{"suffix twice", agentsBlock(`"a[bot][bot]"`)},
		{"suffix in another case", agentsBlock(`"a[BOT]"`)},
		{"another suffix", agentsBlock(`"a[app]"`)},
		{"empty string", agentsBlock(`""`)},
		{"40 characters", agentsBlock(strings.Repeat("a", 40))},
		{"integer entry", agentsBlock("123")},
		{"nested list", "agents:\n  - [claude]\n"},
		{"101 entries", agentsBlock(numberedOwners(101)...)},
		{"duplicate ignoring case", agentsBlock("Claude", "claude")},
		{"duplicate ignoring the suffix", agentsBlock("claude", `"Claude[bot]"`)},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := mustFail(t, withAgents(tt.text))
			if !strings.Contains(err.Error(), "agents") {
				t.Errorf("error %q does not name the key agents", err)
			}
		})
	}

	t.Run("100 entries", func(t *testing.T) {
		if _, err := Parse(withAgents(agentsBlock(numberedOwners(100)...))); err != nil {
			t.Errorf("Parse rejected 100 agents: %v", err)
		}
	})
	t.Run("errors never echo the value", func(t *testing.T) {
		err := mustFail(t, withAgents(agentsBlock(leakMarker+"/team")))
		if strings.Contains(err.Error(), leakMarker) {
			t.Errorf("error echoes the offending value: %q", err)
		}
	})
}

func TestIsAgent(t *testing.T) {
	s := Settings{Agents: []string{"Claude", "copilot-swe-agent[bot]"}}
	tests := []struct {
		login string
		want  bool
	}{
		{"Claude", true},
		{"claude", true},
		{"CLAUDE", true},
		{"claude[bot]", true},
		{"Claude[BOT]", true},
		{"copilot-swe-agent[bot]", true},
		{"copilot-swe-agent", true},
		{"Copilot-SWE-Agent", true},
		{"alice", false},
		{"claude-2", false},
		{"[bot]", false},
		{"", false},
	}
	for _, tt := range tests {
		t.Run(tt.login, func(t *testing.T) {
			if got := s.IsAgent(tt.login); got != tt.want {
				t.Errorf("IsAgent(%q) = %t, want %t", tt.login, got, tt.want)
			}
		})
	}
	if (Settings{}).IsAgent("claude") {
		t.Error("a file without agents names an agent")
	}
}

func TestRenderWritesAgentsAfterOwners(t *testing.T) {
	s := Settings{
		Version:  1,
		Owners:   []string{"alice"},
		Agents:   []string{"claude", "copilot-swe-agent[bot]"},
		Gate:     GateRequired,
		Workflow: ".github/workflows/archfit.yaml",
		DataMode: DataCustomer,
	}
	text := string(Render(s))
	if want := "owners:\n  - alice\nagents:\n  - claude\n  - copilot-swe-agent[bot]\ngate: required\n"; !strings.Contains(text, want) {
		t.Errorf("Render =\n%s\nwant agents as a block sequence between owners and gate", text)
	}
	checkRoundTrip(t, s)

	s.Agents = []string{}
	if strings.Contains(string(Render(s)), "agents") {
		t.Errorf("an empty agent list is written:\n%s", Render(s))
	}
}

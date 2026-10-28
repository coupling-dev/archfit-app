// Package settings is the App-owned settings file on the protected ref.
package settings

import "strings"

// Path is the App settings file on the protected ref, beside the engine policy.
const Path = ".archfit-app.yaml"

type GateMode string

const (
	GateAdvisory GateMode = "advisory" // publish archfit/state and the comment only
	GateRequired GateMode = "required" // also publish archfit/gate, fail closed
)

type DataMode string

const (
	DataCustomer DataMode = "customer" // source never reaches Archfit
	DataManaged  DataMode = "managed"  // managed discovery and managed LLM allowed after consent
)

// Settings is the App-owned file. The engine policy schema is untouched by it.
type Settings struct {
	Version  int      `yaml:"version"`          // must be 1
	Owners   []string `yaml:"owners"`           // GitHub logins, or "org/team-slug"; at least one
	Agents   []string `yaml:"agents,omitempty"` // logins or App slugs of coding agents, which never approve; nil when none
	Gate     GateMode `yaml:"gate"`             // default advisory
	Workflow string   `yaml:"workflow"`         // e.g. ".github/workflows/archfit.yaml"
	DataMode DataMode `yaml:"data_mode"`        // default customer
	// PolicyAuthority is who may approve a change to the protected files. Absent means human.
	PolicyAuthority *PolicyAuthority `yaml:"policy_authority,omitempty"`
}

// AuthorityLevel is a rung of the policy authority ladder.
type AuthorityLevel string

const (
	AuthorityHuman     AuthorityLevel = "human"     // an owner approves every protected change
	AuthorityCoSign    AuthorityLevel = "co-sign"   // an architect agent and one owner (not active in this release)
	AuthorityDelegated AuthorityLevel = "delegated" // an architect agent alone for tightening (not active in this release)
)

// PolicyAuthority is the policy_authority block. Only AuthorityHuman is in force in this release: a file may name a
// later rung, which parses so that the file stays valid, and Effective holds it at human.
type PolicyAuthority struct {
	Level       AuthorityLevel `yaml:"level"`
	Agents      []string       `yaml:"agents,omitempty"`       // architect agents a later rung lets approve
	TightenAuto bool           `yaml:"tighten_auto,omitempty"` // a later rung: tightening needs no approval
}

// Effective returns the rung in force and, when it differs from the one the file names, why. Every rung but human is
// held at human in this release, so a file cannot grant an agent approval before the App supports it.
func (s Settings) Effective() (level AuthorityLevel, held string) {
	if s.PolicyAuthority == nil || s.PolicyAuthority.Level == AuthorityHuman {
		return AuthorityHuman, ""
	}
	return AuthorityHuman, "policy_authority " + string(s.PolicyAuthority.Level) + " is not supported yet: an owner approves every protected change"
}

// botSuffix ends the login GitHub shows for a GitHub App's account: "slug[bot]".
const botSuffix = "[bot]"

// IsAgent reports whether login is one of s.Agents. Case is ignored, and "name" and "name[bot]" are one identity:
// GitHub names an App's account "slug[bot]" and a file may name either form, so a missed suffix never lets an agent
// through. "" is never an agent.
func (s Settings) IsAgent(login string) bool {
	id := agentIdentity(login)
	for _, a := range s.Agents {
		if id != "" && agentIdentity(a) == id {
			return true
		}
	}
	return false
}

// agentIdentity is the form two agent names are compared in.
func agentIdentity(login string) string {
	return strings.TrimSuffix(strings.ToLower(login), botSuffix)
}

// The YAML keys of version 1, in canonical order (the order of the Settings fields).
const (
	keyVersion   = "version"
	keyOwners    = "owners"
	keyAgents    = "agents"
	keyGate      = "gate"
	keyWorkflow  = "workflow"
	keyDataMode  = "data_mode"
	keyAuthority = "policy_authority"
)

// The keys of policy_authority.
const (
	keyLevel       = "level"
	keyArchitects  = "agents"
	keyTightenAuto = "tighten_auto"
)

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
	keyVersion  = "version"
	keyOwners   = "owners"
	keyAgents   = "agents"
	keyGate     = "gate"
	keyWorkflow = "workflow"
	keyDataMode = "data_mode"
)

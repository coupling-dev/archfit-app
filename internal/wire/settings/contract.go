// Package settings is the App-owned settings file on the protected ref.
package settings

import "errors"

var errNotImplemented = errors.New("settings: not implemented")

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
	Version  int      `yaml:"version"`   // must be 1
	Owners   []string `yaml:"owners"`    // GitHub logins, or "org/team-slug"; at least one
	Gate     GateMode `yaml:"gate"`      // default advisory
	Workflow string   `yaml:"workflow"`  // e.g. ".github/workflows/archfit.yaml"
	DataMode DataMode `yaml:"data_mode"` // default customer
}

// Parse strictly decodes and validates a settings file. Unknown keys are errors.
func Parse(raw []byte) (Settings, error) {
	return Settings{}, errNotImplemented
}

// Render returns canonical YAML for s. For any s returned by Parse, Parse(Render(s)) == s.
func Render(s Settings) []byte {
	return nil
}

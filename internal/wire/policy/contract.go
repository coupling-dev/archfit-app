// Package policy locates the engine's trusted input files and reads the few policy keys the App needs.
package policy

import (
	"errors"
	"time"
)

var errNotImplemented = errors.New("policy: not implemented")

// The engine's three trusted input files on the protected ref. Baseline and labels sit beside the policy.
const (
	PolicyPath   = ".archfit.yaml"
	BaselinePath = ".archfit-baseline.json"
	LabelsPath   = ".archfit-labels.yaml"
)

// Digest returns the bare lowercase hex SHA-256 of the raw file bytes. For the policy file it equals the
// engine's comparison.config_hash. The engine reports no digest for baseline or labels; for those the
// producer states Digest of the bytes it used in the envelope, and the App compares it with the protected ref.
func Digest(raw []byte) string {
	return ""
}

// Waiver is the subset of an engine waiver the App reads. The engine enforces it; the App only schedules around Expires.
type Waiver struct {
	Rule       string // engine rule ID
	From, To   string // endpoint selectors, may be ""
	Reason     string
	ApprovedBy string    // descriptive only, never authentication
	Expires    time.Time // end of the YYYY-MM-DD day, UTC
}

// Waivers extracts the waivers of a policy file. It ignores every other key.
func Waivers(policy []byte) ([]Waiver, error) {
	return nil, errNotImplemented
}

// Schema returns the engine v2.3.1 config JSON Schema, embedded unchanged, for the editor.
func Schema() []byte {
	return nil
}

// ModuleDef is the subset of a policy module the App displays.
type ModuleDef struct {
	Name  string
	Layer string // may be ""
	Line  int    // 1-based line of the module key in the policy text
}

// Modules lists the modules declared under the policy's `modules:` map, in file order. It ignores every other key.
func Modules(policy []byte) ([]ModuleDef, error) {
	return nil, errNotImplemented
}

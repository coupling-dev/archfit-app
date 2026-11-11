// Package result is the engine's result JSON v1 (archfit.result.v1) in the App: the JSON that `archfit check
// --format json` prints and upload envelope 2 carries unchanged as its payload. Decode reads it strictly; Record
// prints its text record with the engine's grammar, so the check, the comment and the pages show what the CLI prints
// (design 8.2).
//
// The engine owns the contract (design 11.7). archfit.result.schema.json and the engine's goldens, the JSON in
// testdata/result/ and the text in testdata/record/, are byte copies; SCHEMA_SOURCE names their repository, commit
// and SHA-256. The schema file also defines archfit.can.v1, which the App never receives; Decode refuses it.
//
// The engine never emits StatusWaiting: the App sets it when an open request covers the errors. So the App owns the
// golden of a waiting result, testdata/waiting.txt, rendered from the vendored waiting.json. It is not vendored and
// not in SCHEMA_SOURCE.
package result

// SchemaV1 identifies a result.
const SchemaV1 = "archfit.result.v1"

// Status is the status of a change (design 7.1).
type Status string

// The four statuses of a change.
const (
	StatusPass    Status = "pass"
	StatusFail    Status = "fail"
	StatusWaiting Status = "waiting" // set by the App only
	StatusUnknown Status = "unknown"
)

// The json tags name the keys; a key without omitempty is required. A rule tag names the schema's value rule for the
// key (see rules).

// Result is the verdict of `archfit check` for one change.
type Result struct {
	Schema  string    `json:"schema" rule:"schema"`
	Status  Status    `json:"status" rule:"status"`
	Summary string    `json:"summary"` // the status line, such as "archfit: fail, 1 error"
	Errors  []Error   `json:"errors"`  // in record order
	Unknown []Unknown `json:"unknown"` // the reasons for an unknown status, in record order
	Policy  Policy    `json:"policy"`
	Base    string    `json:"base,omitempty" rule:"commit"` // the merge base; absent without one
	Head    string    `json:"head,omitempty" rule:"commit"`
	Date    string    `json:"date" rule:"date"` // the date exceptions were judged on
	Engine  string    `json:"engine"`
}

// Error is one place where the change breaks a rule.
type Error struct {
	Path       string     `json:"path"`           // .archfit.yaml for an error of the policy itself
	Line       int        `json:"line,omitempty"` // absent for an error of the policy file
	Rule       string     `json:"rule"`
	What       string     `json:"what"`
	Why        string     `json:"why,omitempty"`
	Instead    string     `json:"instead,omitempty"`
	Balance    *Balance   `json:"balance,omitempty"`
	Next       Next       `json:"next"`
	From       string     `json:"from,omitempty"`
	To         string     `json:"to,omitempty"`
	Key        string     `json:"key"` // compared, never parsed
	FromChange bool       `json:"from_change"`
	Exception  *Exception `json:"exception,omitempty"` // set only when Rule is "exception"
}

// Balance is the coupling number of a dependency, printed as "Value/10 (min Min)".
type Balance struct {
	Value int `json:"value"`
	Min   int `json:"min"`
}

// Next is the one step a record names.
type Next struct {
	Kind string `json:"kind" rule:"kind"` // fix, command, wait or restore
	Text string `json:"text"`             // "fix the code", a command, or "wait for an owner: #431"
	Note string `json:"note,omitempty"`   // printed in parentheses after Text
}

// Exception names a policy exception past its date; the error's From and To hold its modules.
type Exception struct {
	Rule    string `json:"rule"`
	Expires string `json:"expires" rule:"date"`
}

// Unknown is one reason the check gave no verdict.
type Unknown struct {
	Path  string `json:"path,omitempty"`
	Line  int    `json:"line,omitempty"`
	Cause string `json:"cause" rule:"cause"` // policy, base, tool or import
	What  string `json:"what"`
	Why   string `json:"why,omitempty"`
	Next  Next   `json:"next"`
}

// Policy names the policy that judged the change.
type Policy struct {
	Path      string `json:"path"`
	Digest    string `json:"digest" rule:"digest"` // "sha256:<64 hex>" of the file bytes
	Protected bool   `json:"protected"`
	Differs   bool   `json:"differs"`
	Target    string `json:"target,omitempty"`
}

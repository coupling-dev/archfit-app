// Package policy3 is the App's reader and writer of Archfit policy schema 3, the
// `.archfit.yaml` of Archfit 4: a strict decoder that accepts and rejects the same files
// as the engine, the digest that binds an answer to the bytes it was written against, and
// a writer that adds an answer to a policy without changing any other byte.
//
// The types mirror the file. An absent key keeps its zero value (nil slice, empty string,
// nil pointer), so a caller can tell an absent key from a set one and apply the defaults
// of design section 6.1. The engine's fixtures are vendored under testdata and pinned by
// SCHEMA_SOURCE; a test fails when a vendored byte changes.
package policy3

import (
	"errors"
	"strconv"
	"strings"

	"go.yaml.in/yaml/v3"
)

// File is an Archfit policy: modules, allowed dependencies, rules and exceptions.
type File struct {
	Version    int               `yaml:"version"`
	Owners     []string          `yaml:"owners"`
	Gate       string            `yaml:"gate"`
	Layers     []string          `yaml:"layers"`
	Modules    map[string]Module `yaml:"modules"`
	Builtin    Builtin           `yaml:"builtin"`
	Rules      []Rule            `yaml:"rules"`
	Exceptions []Exception       `yaml:"exceptions"`
	Generated  []string          `yaml:"generated"`
	Tests      []string          `yaml:"tests"`
	Exclude    []string          `yaml:"exclude"`
	Languages  Languages         `yaml:"languages"`
	Analyzers  Analyzers         `yaml:"analyzers"`
	Coverage   Coverage          `yaml:"coverage"`
}

// Module is a named set of paths with a public part, a volatility and the modules it may depend on.
type Module struct {
	Paths      []string `yaml:"paths"`
	Public     []string `yaml:"public"`
	Volatility string   `yaml:"volatility"`
	Subdomain  string   `yaml:"subdomain"`
	Layer      string   `yaml:"layer"`
	DependsOn  []string `yaml:"depends_on"`
}

// Builtin holds the modes of the built-in rules and the minimum balance.
type Builtin struct {
	Graph       string `yaml:"graph"`
	Coupling    string `yaml:"coupling"`
	Duplication string `yaml:"duplication"`
	Cycle       string `yaml:"cycle"`
	Layers      string `yaml:"layers"`
	Unowned     string `yaml:"unowned"`
	MinBalance  *int   `yaml:"min_balance"`
}

// Rule is a custom rule: exactly one of deny, only or note, with its reason.
type Rule struct {
	ID      string `yaml:"id"`
	Deny    *Deny  `yaml:"deny"`
	Only    *Only  `yaml:"only"`
	Note    string `yaml:"note"`
	Why     string `yaml:"why"`
	Instead string `yaml:"instead"`
}

// Deny names the source and the target of a forbidden dependency.
type Deny struct {
	From Names `yaml:"from"`
	To   Names `yaml:"to"`
}

// Only names a library or package set and the modules that may use it.
type Only struct {
	Use string `yaml:"use"`
	In  Names  `yaml:"in"`
}

// Exception permits errors of one rule in one place, with a reason and usually a date.
type Exception struct {
	Rule    string `yaml:"rule"`
	From    string `yaml:"from"`
	To      string `yaml:"to"`
	Expires string `yaml:"expires"`
	Why     string `yaml:"why"`
}

// Languages holds one block per language extractor.
type Languages struct {
	Go         GoLanguage     `yaml:"go"`
	TypeScript Language       `yaml:"typescript"`
	Python     PythonLanguage `yaml:"python"`
	Rust       RustLanguage   `yaml:"rust"`
	DotNet     DotNetLanguage `yaml:"dotnet"`
}

// Language is a language extractor with no settings besides enabled.
type Language struct {
	Enabled Switch `yaml:"enabled"`
}

// GoLanguage configures the Go extractor.
type GoLanguage struct {
	Enabled Switch    `yaml:"enabled"`
	Modules GoModules `yaml:"modules"`
}

// GoModules selects go.work members by globs relative to the repository root.
type GoModules struct {
	Include []string `yaml:"include"`
	Exclude []string `yaml:"exclude"`
}

// PythonLanguage configures the Python extractor.
type PythonLanguage struct {
	Enabled Switch `yaml:"enabled"`
	Package string `yaml:"package"`
}

// RustLanguage configures the Rust extractor.
type RustLanguage struct {
	Enabled        Switch   `yaml:"enabled"`
	Manifest       string   `yaml:"manifest"`
	Features       []string `yaml:"features"`
	IncludeDevDeps bool     `yaml:"include_dev_deps"`
}

// DotNetLanguage configures the .NET extractor.
type DotNetLanguage struct {
	Enabled  Switch   `yaml:"enabled"`
	Projects []string `yaml:"projects"`
}

// Analyzers holds the optional analyzers.
type Analyzers struct {
	Syntax       Analyzer      `yaml:"syntax"`
	CargoModules Analyzer      `yaml:"cargo_modules"`
	Roslyn       TimedAnalyzer `yaml:"roslyn"`
}

// Analyzer is an optional analyzer with no settings besides enabled.
type Analyzer struct {
	Enabled Switch `yaml:"enabled"`
}

// TimedAnalyzer is an optional analyzer with a time limit, a Go duration such as 5m.
type TimedAnalyzer struct {
	Enabled Switch `yaml:"enabled"`
	Timeout string `yaml:"timeout"`
}

// Coverage names coverage files for the report.
type Coverage struct {
	Enabled bool             `yaml:"enabled"`
	Sources []CoverageSource `yaml:"sources"`
}

// CoverageSource is one coverage file.
type CoverageSource struct {
	Path        string `yaml:"path"`
	Format      string `yaml:"format"`
	SidecarPath string `yaml:"sidecar_path"`
	MaxBytes    *int64 `yaml:"max_bytes"`
	MaxFacts    *int   `yaml:"max_facts"`
}

// Names is one name or a list of names. A YAML string decodes to a list of one.
type Names []string

// UnmarshalYAML accepts one scalar or a sequence of scalars. Decode checks the kinds of
// the items on the raw tree, so a number or an empty string is reported with its key path.
func (n *Names) UnmarshalYAML(node *yaml.Node) error {
	switch node.Kind {
	case yaml.ScalarNode:
		*n = Names{node.Value}
		return nil
	case yaml.SequenceNode:
		var list []string
		if err := node.Decode(&list); err != nil {
			return errNames
		}
		*n = Names(list)
		if *n == nil {
			*n = Names{}
		}
		return nil
	}
	return errNames
}

var errNames = errors.New("want one name or a list of names")

// Switch is true, false or "auto". Decode keeps any other scalar in a form that
// checkSwitches rejects.
type Switch string

// UnmarshalYAML maps a YAML boolean to "true" or "false" and the string auto to "auto".
// A string that is not auto keeps its quotes, so a quoted "true" is not read as true.
func (s *Switch) UnmarshalYAML(node *yaml.Node) error {
	switch {
	case node.Kind != yaml.ScalarNode:
		return errSwitch
	case node.ShortTag() == "!!bool":
		*s = Switch(strings.ToLower(node.Value))
	case node.ShortTag() == "!!str" && node.Value == switchAuto:
		*s = switchAuto
	case node.ShortTag() == "!!str":
		*s = Switch(strconv.Quote(node.Value))
	default:
		*s = Switch(node.Value)
	}
	return nil
}

var errSwitch = errors.New(`enabled takes true, false or "auto"`)

const switchAuto = "auto"

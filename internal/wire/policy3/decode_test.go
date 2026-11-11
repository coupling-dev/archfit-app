package policy3_test

import (
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"github.com/alexei-led/archfit-app/internal/wire/policy3"
)

const (
	owner    = "alexei-led"
	modApp   = "app"
	modUtil  = "pkg_util"
	modStore = "store"
	modCLI   = "cli"
	layerCmd = "cmd"
	core     = "core"
	cmdGlob  = "cmd/**"
	low      = "low"
	high     = "high"
	deny     = "deny"
	ask      = "ask"
	off      = "off"
)

func ptr[T any](v T) *T { return &v }

func readFixture(t *testing.T, kind, name string) []byte {
	t.Helper()
	data, err := os.ReadFile(filepath.Join("testdata", kind, name+".yaml"))
	if err != nil {
		t.Fatal(err)
	}
	return data
}

func decodeFixture(t *testing.T, kind, name string) (policy3.File, error) {
	t.Helper()
	return policy3.Decode(readFixture(t, kind, name))
}

func TestDecodeValidFixtures(t *testing.T) {
	tests := map[string]policy3.File{
		"smallest": {
			Version: 3,
			Owners:  []string{owner},
			Modules: map[string]policy3.Module{modApp: {Paths: []string{cmdGlob, "pkg/**"}}},
		},
		"typical": {
			Version: 3,
			Owners:  []string{owner},
			Gate:    "required",
			Layers:  []string{core, layerCmd},
			Modules: map[string]policy3.Module{
				"pkg_chaos": {
					Paths:      []string{"pkg/chaos/**"},
					Public:     []string{"pkg/chaos", "pkg/chaos/cliflags"},
					Volatility: high,
					Layer:      core,
					DependsOn:  []string{"pkg_container", modUtil},
				},
				"pkg_tc": {Paths: []string{"pkg/tc/**"}, Public: []string{"pkg/tc"}, Volatility: high, Layer: core},
				modUtil:  {Paths: []string{"pkg/util/**"}, Volatility: low, Layer: core},
			},
			Rules: []policy3.Rule{
				{
					ID:      "no-chaos-to-runtime",
					Deny:    &policy3.Deny{From: policy3.Names{"pkg_chaos"}, To: policy3.Names{"pkg_runtime"}},
					Why:     "A direct import ties a chaos action to one runtime.",
					Instead: "Add a method to container.Client.",
				},
				{
					ID:   "docker-sdk-in-runtime",
					Only: &policy3.Only{Use: "github.com/docker/docker", In: policy3.Names{"pkg_runtime"}},
					Why:  "One adapter owns the Docker API.",
				},
				{
					ID:   "fan-out-through-runner",
					Note: "Chaos actions fan out through RunOnContainers.",
					Why:  "One place checks the target limit before injection.",
				},
			},
			Exceptions: []policy3.Exception{{
				Rule: "duplication", From: "pkg_tc", To: modUtil, Expires: "2026-11-19", Why: "Request #431 is open.",
			}},
		},
		"every-key": {
			Version: 3,
			Owners:  []string{owner, "octocat"},
			Gate:    "advisory",
			Layers:  []string{"domain", "adapter", layerCmd},
			Modules: map[string]policy3.Module{
				core: {
					Paths: []string{"internal/core/**"}, Public: []string{"internal/core"},
					Subdomain: core, Layer: "domain", DependsOn: []string{},
				},
				modStore: {
					Paths: []string{"internal/store/**"}, Volatility: "medium",
					Subdomain: "supporting", Layer: "adapter", DependsOn: []string{core},
				},
				modCLI: {Paths: []string{cmdGlob}, Layer: layerCmd, DependsOn: []string{core, modStore}},
			},
			Builtin: policy3.Builtin{
				Graph: deny, Coupling: ask, Duplication: off, Cycle: deny, Layers: ask, Unowned: off,
				MinBalance: ptr(8),
			},
			Rules: []policy3.Rule{
				{
					ID:      "no-cli-to-sql",
					Deny:    &policy3.Deny{From: policy3.Names{modCLI, "cmd/tools/**"}, To: policy3.Names{"database/sql"}},
					Why:     "SQL belongs to the store.",
					Instead: "Call a store method.",
				},
				{
					ID:      "process-in-cli",
					Only:    &policy3.Only{Use: "process", In: policy3.Names{modCLI}},
					Why:     "Side effects stay at the edge.",
					Instead: "Return a command for the CLI to run.",
				},
			},
			Exceptions: []policy3.Exception{
				{Rule: "graph", From: modStore, To: modCLI, Why: "Migration in progress."},
				{Rule: policy3.RuleUnowned, From: "scripts/**", Expires: "2027-01-31", Why: "Scripts get a module next quarter."},
			},
			Generated: []string{"**/*.pb.go"},
			Tests:     []string{"**/*_test.go", "internal/testutil/**"},
			Exclude:   []string{"third_party/**"},
			Languages: policy3.Languages{
				Go: policy3.GoLanguage{
					Enabled: "true",
					Modules: policy3.GoModules{Include: []string{"services/**"}, Exclude: []string{"services/legacy"}},
				},
				TypeScript: policy3.Language{Enabled: "false"},
				Python:     policy3.PythonLanguage{Enabled: "auto", Package: "myapp"},
				Rust: policy3.RustLanguage{
					Enabled: "auto", Manifest: "Cargo.toml", Features: []string{modCLI}, IncludeDevDeps: true,
				},
				DotNet: policy3.DotNetLanguage{Enabled: "false", Projects: []string{"App.slnx"}},
			},
			Analyzers: policy3.Analyzers{
				Syntax:       policy3.Analyzer{Enabled: "true"},
				CargoModules: policy3.Analyzer{Enabled: "false"},
				Roslyn:       policy3.TimedAnalyzer{Enabled: "true", Timeout: "1m30s"},
			},
			Coverage: policy3.Coverage{
				Enabled: true,
				Sources: []policy3.CoverageSource{{
					Path: "coverage.out", Format: "go-coverprofile", SidecarPath: "coverage.out.sidecar.json",
					MaxBytes: ptr(int64(1048576)), MaxFacts: ptr(5000),
				}},
			},
		},
	}
	files, err := filepath.Glob(filepath.Join("testdata", "valid", "*.yaml"))
	if err != nil {
		t.Fatal(err)
	}
	if len(files) != len(tests) {
		t.Errorf("testdata/valid has %d files, the table has %d rows", len(files), len(tests))
	}
	for name, want := range tests {
		t.Run(name, func(t *testing.T) {
			got, err := decodeFixture(t, "valid", name)
			if err != nil {
				t.Fatalf("Decode: %v", err)
			}
			if !reflect.DeepEqual(got, want) {
				t.Errorf("Decode =\n%+v\nwant\n%+v", got, want)
			}
		})
	}
}

// An absent key keeps its zero value, so a caller can tell it from a set one.
func TestDecodeKeepsAbsentKeysZero(t *testing.T) {
	got, err := decodeFixture(t, "valid", "smallest")
	if err != nil {
		t.Fatalf("Decode: %v", err)
	}
	app := got.Modules[modApp]
	if got.Gate != "" || got.Layers != nil || got.Builtin != (policy3.Builtin{}) ||
		app.Public != nil || app.Volatility != "" || app.Subdomain != "" || app.DependsOn != nil {
		t.Errorf("absent keys decoded to values: %+v", got)
	}
}

// invalidFixtures maps each file in testdata/invalid to the messages its error must contain.
// The messages are the engine's, except where yaml.v3 words the unknown key itself.
var invalidFixtures = map[string][]string{
	"unknown-key":        {"field owner not found", "line 6"},
	"version-missing":    {`version: missing: add "version: 3"`},
	"version-2":          {`version 2 is the Archfit 3 policy format: run "archfit init" to convert it to version 3`},
	"version-4":          {"version 4: this engine reads version 3"},
	"version-string":     {`version "3": this engine reads version 3`},
	"expires-shape":      {`exceptions[0].expires: "19-11-2026" is not a date: use YYYY-MM-DD`},
	"expires-impossible": {`exceptions[0].expires: "2026-02-30" is not a date: use YYYY-MM-DD`},
	"rule-two-forms":     {"rules[0] (two-forms): has deny and note: a rule has exactly one of deny, only or note"},
	"rule-no-form":       {"rules[0] (no-form): needs one of deny, only or note"},
	"rule-two-forms-no-instead": {
		"rules[0] (two-forms): has deny and note",
		"rules[0] (two-forms).instead: required with deny",
	},
	"gate-empty":           {"gate: empty string: give a value or remove the key"},
	"expires-empty":        {"exceptions[0].expires: empty string: give a value or remove the key"},
	"unowned-to-empty":     {"exceptions[0].to: empty string: give a value or remove the key"},
	"builtin-mode-unknown": {`builtin.graph: "warn" is not one of deny, ask, off`},
	"min-balance-low":      {"builtin.min_balance: 0 is outside 1 to 10"},
	"min-balance-high":     {"builtin.min_balance: 11 is outside 1 to 10"},
	"volatility-unknown":   {`modules.app.volatility: "frozen" is not one of low, medium, high`},
	"subdomain-unknown":    {`modules.app.subdomain: "platform" is not one of core, supporting, generic`},
	"gate-unknown":         {`gate: "fail" is not one of advisory, required`},
	"enabled-unknown":      {`languages.go.enabled: "on" is not true, false or "auto"`},
	"owners-missing":       {"owners: required: list at least one GitHub login"},
	"owners-empty":         {"owners: required: list at least one GitHub login"},
	"modules-missing":      {"modules: required: declare at least one module"},
	"paths-missing":        {"modules.app.paths: required: list at least one glob"},
	"public-empty":         {"modules.app.public: empty: list at least one glob, or remove the key"},
	"module-duplicate":     {`"app"`, "already defined"},
	"layers-duplicate":     {`layers: "core" is listed twice`},
	"why-missing":          {"rules[0] (no-why).why: required: give the reason for the rule"},
	"instead-missing":      {"rules[0] (no-instead).instead: required with deny: say what to do instead"},
	"names-empty":          {"rules[0] (empty-from).deny.from: empty: name a module or a path glob"},
	"exception-to-missing": {"exceptions[0].to: required: name the module the errors reach"},
	"exception-unowned-to": {"exceptions[0].to: unowned has no target module: remove to"},
	"timeout-bad":          {`analyzers.roslyn.timeout: "five minutes" is not a Go duration, such as 5m`},
	"coverage-no-sources":  {"coverage.sources: required when coverage is enabled: list at least one file"},
}

func TestDecodeInvalidFixtures(t *testing.T) {
	for name, wants := range invalidFixtures {
		t.Run(name, func(t *testing.T) {
			_, err := decodeFixture(t, "invalid", name)
			if err == nil {
				t.Fatal("Decode accepted an invalid policy")
			}
			for _, want := range wants {
				if !strings.Contains(err.Error(), want) {
					t.Errorf("error %q does not contain %q", err, want)
				}
			}
		})
	}
}

func TestEveryInvalidFixtureHasExpectedMessages(t *testing.T) {
	files, err := filepath.Glob(filepath.Join("testdata", "invalid", "*.yaml"))
	if err != nil {
		t.Fatal(err)
	}
	for _, file := range files {
		name := strings.TrimSuffix(filepath.Base(file), ".yaml")
		if _, ok := invalidFixtures[name]; !ok {
			t.Errorf("%s has no row in invalidFixtures", file)
		}
	}
	if len(files) != len(invalidFixtures) {
		t.Errorf("testdata/invalid has %d files, the table has %d rows", len(files), len(invalidFixtures))
	}
}

// The value checks list every problem at once, so an owner fixes the file in one pass.
func TestDecodeReportsEveryProblem(t *testing.T) {
	_, err := policy3.Decode([]byte(`version: 3
owners: []
modules:
  app:
    paths: [cmd/**]
    volatility: frozen
builtin:
  min_balance: 0
`))
	if err == nil {
		t.Fatal("Decode accepted an invalid policy")
	}
	for _, want := range []string{"owners:", "modules.app.volatility:", "builtin.min_balance:"} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("error %q does not contain %q", err, want)
		}
	}
}

const header = "owners: [o]\nmodules:\n  app:\n    paths: [cmd/**]\n"

func TestDecodeVersionNumbers(t *testing.T) {
	tests := []struct{ version, wantErr string }{
		{version: "3"},
		{version: "3.0"},
		{version: "2.0", wantErr: `version 2 is the Archfit 3 policy format: run "archfit init"`},
		{version: "3.5", wantErr: "version 3.5: this engine reads version 3"},
		{version: "-3", wantErr: "version -3: this engine reads version 3"},
	}
	for _, tt := range tests {
		t.Run(tt.version, func(t *testing.T) {
			got, err := policy3.Decode([]byte("version: " + tt.version + "\n" + header))
			if tt.wantErr != "" {
				if err == nil || !strings.Contains(err.Error(), tt.wantErr) {
					t.Fatalf("error = %v, want %q", err, tt.wantErr)
				}
				return
			}
			if err != nil {
				t.Fatalf("Decode: %v", err)
			}
			if got.Version != policy3.Version {
				t.Errorf("Version = %d, want %d", got.Version, policy3.Version)
			}
		})
	}
}

func TestDecodeSwitchForms(t *testing.T) {
	tests := []struct {
		enabled, wantErr string
		want             policy3.Switch
	}{
		{enabled: "true", want: "true"},
		{enabled: "false", want: "false"},
		{enabled: "auto", want: "auto"},
		{enabled: `"true"`, wantErr: `languages.go.enabled: "true" is not true, false or "auto"`},
		{enabled: "1", wantErr: `languages.go.enabled: 1 is not true, false or "auto"`},
		{enabled: "[true]", wantErr: `enabled takes true, false or "auto"`},
		{enabled: `""`, wantErr: "languages.go.enabled: empty string: give a value or remove the key"},
	}
	for _, tt := range tests {
		t.Run(tt.enabled, func(t *testing.T) {
			got, err := policy3.Decode([]byte("version: 3\n" + header + "languages:\n  go:\n    enabled: " + tt.enabled + "\n"))
			if tt.wantErr != "" {
				if err == nil || !strings.Contains(err.Error(), tt.wantErr) {
					t.Fatalf("error = %v, want %q", err, tt.wantErr)
				}
				return
			}
			if err != nil {
				t.Fatalf("Decode: %v", err)
			}
			if got.Languages.Go.Enabled != tt.want {
				t.Errorf("enabled = %q, want %q", got.Languages.Go.Enabled, tt.want)
			}
		})
	}
}

func TestDecodeNamesForms(t *testing.T) {
	tests := []struct {
		name, entry string
		want        policy3.Names
		wantErr     string
	}{
		{name: "absent", entry: "", wantErr: "rules[0] (r).deny.from: required: name a module or a path glob"},
		{name: "one name", entry: "from: app", want: policy3.Names{"app"}},
		{name: "a list", entry: "from: [app, cmd/**]", want: policy3.Names{"app", "cmd/**"}},
		{name: "empty string", entry: `from: ""`, wantErr: "rules[0].deny.from: empty string: give a value or remove the key"},
		{name: "empty name in a list", entry: `from: [app, ""]`, wantErr: "rules[0].deny.from[1]: empty string: give a value or remove the key"},
		{name: "a number", entry: "from: 12", wantErr: "rules[0].deny.from: 12 is not a name or a list of names"},
		{name: "null", entry: "from: ", wantErr: "rules[0].deny.from: empty value: give a value or remove the key"},
		{name: "a map", entry: "from: {a: b}", wantErr: "want one name or a list of names"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			entry := tt.entry
			if entry != "" {
				entry += ", "
			}
			got, err := policy3.Decode([]byte("version: 3\nowners: [alexei-led]\nmodules:\n  app:\n    paths: [cmd/**]\n" +
				"rules:\n  - id: r\n    deny: { " + entry + "to: lib }\n    why: w\n    instead: i\n"))
			if tt.wantErr != "" {
				if err == nil || !strings.Contains(err.Error(), tt.wantErr) {
					t.Fatalf("error = %v, want %q", err, tt.wantErr)
				}
				return
			}
			if err != nil {
				t.Fatalf("Decode: %v", err)
			}
			if from := got.Rules[0].Deny.From; !reflect.DeepEqual(from, tt.want) {
				t.Errorf("deny.from = %v, want %v", from, tt.want)
			}
		})
	}
}

// The wrong kind of a value is an error even when the YAML decoder would convert it.
func TestDecodeRejectsConvertedKinds(t *testing.T) {
	tests := map[string]struct{ yaml, wantErr string }{
		"a number for an owner":                                {"owners: [12]\nmodules:\n  app:\n    paths: [cmd/**]\n", "owners[0]: 12 is not a string"},
		"a string for min_balance (the YAML decoder words it)": {header + "builtin:\n  min_balance: \"7\"\n", "cannot unmarshal !!str `7` into int"},
		"a fraction for min_balance":                           {header + "builtin:\n  min_balance: 7.5\n", "builtin.min_balance: 7.5 is not a whole number"},
		"a string for a bool":                                  {header + "coverage:\n  enabled: \"yes\"\n", `coverage.enabled: "yes" is not true or false`},
		"a null module":                                        {"owners: [o]\nmodules:\n  app:\n", "modules.app: empty value"},
	}
	for name, tt := range tests {
		t.Run(name, func(t *testing.T) {
			_, err := policy3.Decode([]byte("version: 3\n" + tt.yaml))
			if err == nil || !strings.Contains(err.Error(), tt.wantErr) {
				t.Fatalf("error = %v, want %q", err, tt.wantErr)
			}
		})
	}
}

func TestDecodeEmptyInput(t *testing.T) {
	for _, in := range []string{"", "# only a comment\n"} {
		if _, err := policy3.Decode([]byte(in)); err == nil || !strings.Contains(err.Error(), "version: missing") {
			t.Errorf("Decode(%q) error = %v, want version: missing", in, err)
		}
	}
}

// Where the engine's YAML library and yaml.v3 read a scalar differently, the App follows the
// engine, so a file the engine accepts is not refused here, and the reverse.
func TestDecodeFollowsTheEngineScalarRules(t *testing.T) {
	tests := []struct {
		name, yaml, wantErr string
		check               func(*testing.T, policy3.File)
	}{
		{
			name: "a login that looks like a number with an exponent is a name",
			yaml: "owners: [1e3]\nmodules:\n  app:\n    paths: [cmd/**]\n",
			check: func(t *testing.T, f policy3.File) {
				if !reflect.DeepEqual(f.Owners, []string{"1e3"}) {
					t.Errorf("Owners = %v", f.Owners)
				}
			},
		},
		{
			name:    "a number with an exponent is not a whole number",
			yaml:    header + "builtin:\n  min_balance: 7e0\n",
			wantErr: `builtin.min_balance: "7e0" is not a whole number`,
		},
		{
			name:    "a fraction with an exponent is a number, so not a name",
			yaml:    "owners: [1.5e3]\nmodules:\n  app:\n    paths: [cmd/**]\n",
			wantErr: "owners[0]: 1500 is not a string",
		},
		{
			name: "a module named by a number",
			yaml: "owners: [o]\nmodules:\n  123:\n    paths: [cmd/**]\n",
			check: func(t *testing.T, f policy3.File) {
				if _, ok := f.Modules["123"]; !ok {
					t.Errorf("Modules = %v", f.Modules)
				}
			},
		},
		{
			name: "a merge key supplies keys the mapping does not set",
			yaml: "owners: [o]\n<<: {modules: {app: {paths: [cmd/**]}}}\n",
			check: func(t *testing.T, f policy3.File) {
				if _, ok := f.Modules[modApp]; !ok {
					t.Errorf("Modules = %v", f.Modules)
				}
			},
		},
		{
			name:    "a merged value is checked like any other",
			yaml:    "owners: [o]\n<<: {modules: {app: {paths: [12]}}}\n",
			wantErr: "modules.app.paths[0]: 12 is not a string",
		},
		{
			// The mapping sets owners itself, so the merged list that contains itself is never read.
			name: "a merged value that contains itself and is overridden",
			yaml: "owners: [o]\nmodules: {app: {paths: [cmd/**]}}\n<<: {owners: &loop [*loop]}\n",
			check: func(t *testing.T, f policy3.File) {
				if !reflect.DeepEqual(f.Owners, []string{"o"}) {
					t.Errorf("Owners = %v", f.Owners)
				}
			},
		},
		{
			name:    "a list that contains itself",
			yaml:    "owners: &o [a, *o]\nmodules: {app: {paths: [app/**]}}\n",
			wantErr: "cannot unmarshal",
		},
		{
			name:    "a key that is an alias is checked as the key it stands for",
			yaml:    "owners: [&k generated]\nmodules: {app: {paths: [app/**]}}\n*k : [12]\n",
			wantErr: "generated[0]: 12 is not a string",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := policy3.Decode([]byte("version: 3\n" + tt.yaml))
			if tt.wantErr != "" {
				if err == nil || !strings.Contains(err.Error(), tt.wantErr) {
					t.Fatalf("error = %v, want %q", err, tt.wantErr)
				}
				return
			}
			if err != nil {
				t.Fatalf("Decode: %v", err)
			}
			tt.check(t, got)
		})
	}
}

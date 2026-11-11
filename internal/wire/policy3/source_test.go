package policy3_test

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"os"
	"path/filepath"
	"reflect"
	"regexp"
	"slices"
	"strings"
	"testing"

	"github.com/alexei-led/archfit-app/internal/wire/policy3"
)

const (
	schemaFile   = "archfit.schema.json"
	sourceFile   = "SCHEMA_SOURCE"
	sourceRepo   = "alexei-led/archfit"
	sourceCommit = "e77ab1a43ca70be56db8d0cd696c605fbb05d135"
)

// readSource reads SCHEMA_SOURCE: key: value lines, then one "sha256  path" line per vendored file.
func readSource(t *testing.T) (meta, sums map[string]string) {
	t.Helper()
	raw, err := os.ReadFile(sourceFile)
	if err != nil {
		t.Fatal(err)
	}
	meta, sums = map[string]string{}, map[string]string{}
	for line := range strings.Lines(string(raw)) {
		line = strings.TrimSpace(line)
		switch {
		case line == "" || strings.HasPrefix(line, "#"):
		case regexp.MustCompile(`^[0-9a-f]{64}  \S+$`).MatchString(line):
			sum, path, _ := strings.Cut(line, "  ")
			sums[path] = sum
		default:
			key, value, ok := strings.Cut(line, ": ")
			if !ok {
				t.Fatalf("%s: cannot read the line %q", sourceFile, line)
			}
			meta[key] = value
		}
	}
	return meta, sums
}

// A vendored file changes only together with its SCHEMA_SOURCE line, and the line names the
// engine commit it came from (M2 plan, D4).
func TestSourcePinsEveryVendoredFile(t *testing.T) {
	meta, sums := readSource(t)
	if meta["repository"] != sourceRepo || meta["revision"] != sourceCommit {
		t.Errorf("SCHEMA_SOURCE names %s at %s, want %s at %s", meta["repository"], meta["revision"], sourceRepo, sourceCommit)
	}
	vendored := []string{schemaFile}
	for _, kind := range []string{"valid", "invalid"} {
		files, err := filepath.Glob(filepath.Join("testdata", kind, "*.yaml"))
		if err != nil {
			t.Fatal(err)
		}
		vendored = append(vendored, files...)
	}
	for _, path := range vendored {
		data, err := os.ReadFile(path)
		if err != nil {
			t.Fatal(err)
		}
		sum := sha256.Sum256(data)
		if got := sums[filepath.ToSlash(path)]; got != hex.EncodeToString(sum[:]) {
			t.Errorf("%s: SCHEMA_SOURCE says sha256 %q, the file is %x", path, got, sum)
		}
	}
	if len(sums) != len(vendored) {
		t.Errorf("SCHEMA_SOURCE has %d files, %d are vendored", len(sums), len(vendored))
	}
}

// The Go types and the vendored schema name the same keys, so a new engine key cannot be
// vendored without the types that read it.
func TestTypesHaveTheKeysOfTheSchema(t *testing.T) {
	raw, err := os.ReadFile(schemaFile)
	if err != nil {
		t.Fatal(err)
	}
	var schema struct {
		Properties map[string]json.RawMessage `json:"properties"`
		Defs       map[string]struct {
			Properties map[string]json.RawMessage `json:"properties"`
		} `json:"$defs"`
	}
	if err := json.Unmarshal(raw, &schema); err != nil {
		t.Fatal(err)
	}
	keys := func(m map[string]json.RawMessage) []string {
		out := make([]string, 0, len(m))
		for k := range m {
			out = append(out, k)
		}
		slices.Sort(out)
		return out
	}
	types := map[string]reflect.Type{
		"File": reflect.TypeFor[policy3.File](), "Module": reflect.TypeFor[policy3.Module](),
		"Builtin": reflect.TypeFor[policy3.Builtin](), "Rule": reflect.TypeFor[policy3.Rule](),
		"Deny": reflect.TypeFor[policy3.Deny](), "Only": reflect.TypeFor[policy3.Only](),
		"Exception": reflect.TypeFor[policy3.Exception](), "Languages": reflect.TypeFor[policy3.Languages](),
		"Language": reflect.TypeFor[policy3.Language](), "GoLanguage": reflect.TypeFor[policy3.GoLanguage](),
		"GoModules": reflect.TypeFor[policy3.GoModules](), "PythonLanguage": reflect.TypeFor[policy3.PythonLanguage](),
		"RustLanguage": reflect.TypeFor[policy3.RustLanguage](), "DotNetLanguage": reflect.TypeFor[policy3.DotNetLanguage](),
		"Analyzers": reflect.TypeFor[policy3.Analyzers](), "Analyzer": reflect.TypeFor[policy3.Analyzer](),
		"TimedAnalyzer": reflect.TypeFor[policy3.TimedAnalyzer](), "Coverage": reflect.TypeFor[policy3.Coverage](),
		"CoverageSource": reflect.TypeFor[policy3.CoverageSource](),
	}
	fields := func(typ reflect.Type) []string {
		var out []string
		for i := range typ.NumField() {
			name, _, _ := strings.Cut(typ.Field(i).Tag.Get("yaml"), ",")
			out = append(out, name)
		}
		slices.Sort(out)
		return out
	}
	if got, want := fields(types["File"]), keys(schema.Properties); !slices.Equal(got, want) {
		t.Errorf("File has the keys %v, the schema has %v", got, want)
	}
	for name, def := range schema.Defs {
		typ, ok := types[name]
		if !ok {
			t.Errorf("the schema defines %s, which has no Go type", name)
			continue
		}
		if got, want := fields(typ), keys(def.Properties); !slices.Equal(got, want) {
			t.Errorf("%s has the keys %v, the schema has %v", name, got, want)
		}
	}
	if len(schema.Defs) != len(types)-1 {
		t.Errorf("the schema has %d definitions, the test names %d types besides File", len(schema.Defs), len(types)-1)
	}
}

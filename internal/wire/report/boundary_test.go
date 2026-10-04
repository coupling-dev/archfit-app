package report

import (
	"fmt"
	"os/exec"
	"reflect"
	"slices"
	"strings"
	"testing"
)

func TestSeededLeakageFixturesFail(t *testing.T) {
	names := fixtures(t, "leakage")
	for _, required := range []string{
		"snippet-on-finding", "top-level-logs", "raw-response-on-task", "multi-line-why", "long-goal", "prompt-in-profile",
	} {
		if !slices.Contains(names, "leakage/"+required+".json") {
			t.Errorf("fixture %s is missing", required)
		}
	}
	for _, name := range names {
		if loadExpectation(t, name).Decode != decodeError {
			continue
		}
		t.Run(name, func(t *testing.T) {
			payload := loadFixture(t, name)
			if !strings.Contains(string(payload), leakMarker) {
				t.Fatalf("fixture does not contain %s", leakMarker)
			}
			_, err := Decode(payload, fixtureEngine)
			if err == nil {
				t.Fatal("decoded, want an error")
			}
			if strings.Contains(err.Error(), leakMarker) {
				t.Errorf("error echoes the payload: %v", err)
			}
		})
	}
}

func TestDeclarationsNeverSurvive(t *testing.T) {
	payload := loadFixture(t, "leakage/declarations-dropped.json")
	doc := parseDoc(t, payload)
	for _, steps := range [][]any{
		{"agent_tasks", 0, "declarations", 0, "name"},
		{"findings", 0, "matched_by", "from_glob"},
		{"seams", 0, "hypothesis"},
	} {
		if s, _ := at(t, doc, steps...).(string); !strings.Contains(s, leakMarker) {
			t.Fatalf("fixture has no marker at %v", steps)
		}
	}
	r := mustDecode(t, payload)
	if strings.Contains(string(encode(t, r)), leakMarker) {
		t.Error("the marker survives in the marshalled Report")
	}
	if strings.Contains(fmt.Sprintf("%+v", r), leakMarker) {
		t.Error("the marker survives in the Report")
	}
}

// objectPaths visits every object of doc with its generalized location.
func objectPaths(v any, steps []any, general string, visit func(steps []any, general string)) {
	switch v := v.(type) {
	case map[string]any:
		visit(steps, general)
		for k, child := range v {
			objectPaths(child, append(slices.Clone(steps), k), strings.TrimPrefix(general+"."+k, "."), visit)
		}
	case []any:
		for i, child := range v {
			objectPaths(child, append(slices.Clone(steps), i), general+"[]", visit)
		}
	}
}

func TestUnknownFieldAtEveryLevel(t *testing.T) {
	freeMaps := []string{"findings[].matched_by", "measurement.tool_versions"}
	var covered []string
	objectPaths(baseDoc(t), nil, "", func(steps []any, general string) {
		if slices.Contains(freeMaps, general) {
			return
		}
		covered = append(covered, general)
		t.Run(fmt.Sprint(steps...), func(t *testing.T) {
			doc := baseDoc(t)
			set(t, doc, 1, append(slices.Clone(steps), "zz_unknown")...)
			_, err := Decode(encode(t, doc), fixtureEngine)
			if general == "dimensions" {
				wantError(t, err, "dimensions")
				return
			}
			wantError(t, err, "unknown key")
		})
	})
	for _, required := range []string{
		"", "decision", "comparison", "gate_reference", "measurement", "coverage", "coverage.tools[]",
		"comparison.measurement_profile", "comparison.measurement_profile.producers[]",
		"findings[]", "findings[].edge", "findings[].edge.from", "findings[].edge.to", "findings[].locations[]",
		"agent_tasks[]", "agent_tasks[].declarations[]", "decision.unevaluated_required_rules[]",
		"dimensions.drift", "dimensions.drift.delta", "dimensions.drift.delta.metrics[]", "dimensions.drift.coverage",
		"dimensions.coupling.metrics[]", "dimensions.coupling.metrics[].denominator", "dimensions.coupling.findings[]",
		"dimensions.drift.unknown[]", "seams[]", "seams[].raw_distance", "seams[].scores",
	} {
		if !slices.Contains(covered, required) {
			t.Errorf("the base fixture has no %q object", required)
		}
	}
}

func TestDecodeRejectsNonStrictJSON(t *testing.T) {
	base := string(encode(t, baseDoc(t)))
	replace := func(old, new string) string {
		if !strings.Contains(base, old) {
			t.Fatalf("the base document has no %s", old)
		}
		return strings.Replace(base, old, new, 1)
	}
	deep := strings.Repeat("[", 17) + strings.Repeat("]", 17)
	rows := []struct{ name, payload string }{
		{"key Findings", replace(`"findings":`, `"Findings":`)},
		{"key SCHEMA_VERSION", replace(`"schema_version":`, `"SCHEMA_VERSION":`)},
		{"duplicate decision key", strings.TrimSuffix(base, "}") +
			`,"decision":{"hard_gates":"pass","active_blockers":0,"attention_dimensions":0,"unknown_dimensions":0}}`},
		{"duplicate key inside matched_by", replace(`"matched_by":{`, `"matched_by":{"from_glob":"x","from_glob":"y",`)},
		{"trailing object", base + " {}"},
		{"trailing garbage", base + "x"},
		{"top-level array", "[" + base + "]"},
		{"top-level string", `"report"`},
		{"empty", ""},
		{"invalid UTF-8", replace(`"verdict":"blocked"`, "\"verdict\":\"blocked\xff\"")},
		{"nesting deeper than 16", replace(`"tool_versions":{`, `"tool_versions":{"deep":`+deep+`,`)},
		{"line 1.0", replace(`"line":3`, `"line":1.0`)},
		{"line 1e0", replace(`"line":3`, `"line":1e0`)},
		{"line as a string", replace(`"line":3`, `"line":"1"`)},
		{"line -1", replace(`"line":3`, `"line":-1`)},
		{"line over the range", replace(`"line":3`, `"line":10000001`)},
		{"legacy exit_code", replace(`"decision":{`, `"decision":{"exit_code":2,`)},
	}
	for _, row := range rows {
		t.Run(row.name, func(t *testing.T) {
			if r, err := Decode([]byte(row.payload), fixtureEngine); err == nil || !isZero(r) {
				t.Errorf("err=%v, zero report=%v; want an error and a zero Report", err, isZero(r))
			}
		})
	}
}

func FuzzDecode(f *testing.F) {
	for _, name := range append(fixtures(f, "report"), fixtures(f, "leakage")...) {
		f.Add(loadFixture(f, name))
	}
	f.Fuzz(func(t *testing.T, payload []byte) {
		first, err := Decode(payload, fixtureEngine)
		if err != nil {
			if !isZero(first) {
				t.Fatal("an error came with a non-zero Report")
			}
			return
		}
		// Re-decoding the marshalled Report re-checks every cap and rule on what was accepted.
		second, err := Decode(encode(t, first), fixtureEngine)
		if err != nil {
			t.Fatalf("an accepted report fails its own rules: %v", err)
		}
		if !reflect.DeepEqual(first, second) {
			t.Fatal("the round trip changed the Report")
		}
	})
}

func TestPackageImportsStdlibOnly(t *testing.T) {
	goList := func(args ...string) []string {
		out, err := exec.Command("go", append([]string{"list"}, args...)...).Output()
		if err != nil {
			t.Fatalf("go list: %v", err)
		}
		return strings.Fields(string(out))
	}
	forbidden := func(pkg string, roots ...string) bool {
		for _, root := range roots {
			if pkg == root || strings.HasPrefix(pkg, root+"/") {
				return true
			}
		}
		return false
	}
	self := goList(".")[0]
	for _, dep := range goList("-deps", "-f", "{{if not .Standard}}{{.ImportPath}}{{end}}", ".") {
		if dep != self {
			t.Errorf("non-standard dependency %s", dep)
		}
	}
	// fmt and encoding/json pull in os themselves, so os is checked on the direct imports only.
	for _, dep := range goList("-deps", ".") {
		if forbidden(dep, "net", "log") {
			t.Errorf("forbidden dependency %s", dep)
		}
	}
	for _, dep := range goList("-f", `{{join .Imports " "}}`, ".") {
		if forbidden(dep, "net", "os", "log") {
			t.Errorf("forbidden import %s", dep)
		}
	}
}

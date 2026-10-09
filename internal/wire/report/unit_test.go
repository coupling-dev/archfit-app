package report

import (
	"fmt"
	"maps"
	"slices"
	"strings"
	"testing"
)

func TestStringClassRules(t *testing.T) {
	classes := []struct {
		class string
		loc   string
		limit int
		steps []any
	}{
		{"text", "decision.unevaluated_required_rules[0].reason", 500, []any{"decision", "unevaluated_required_rules", 0, "reason"}},
		{"finding why", "findings[0].why", 2_000, []any{"findings", 0, "why"}},
		{"finding constraint", "findings[0].constraint", 2_000, []any{"findings", 0, "constraint"}},
		{"command", "agent_tasks[0].validation[0]", 512, []any{"agent_tasks", 0, "validation", 0}},
		{"identifier", "findings[0].rule_id", 200, []any{"findings", 0, "rule_id"}},
		{"token", "seams[0].strength", 64, []any{"seams", 0, "strength"}},
		{"version", "comparison.rubric_version", 128, []any{"comparison", "rubric_version"}},
		{"hash token", "comparison.model_hash", 128, []any{"comparison", "model_hash"}},
		{"tool version", "producers[0].tool_version", 128, toolVersionSteps},
		{"task goal", "agent_tasks[0].goal", 4_096, []any{"agent_tasks", 0, "goal"}},
		{"task constraint", "agent_tasks[0].constraints[0]", 4_096, []any{"agent_tasks", 0, "constraints", 0}},
		{"path", "findings[0].locations[0].file", 512, []any{"findings", 0, "locations", 0, "file"}},
	}
	var rows []mutation
	for _, c := range classes {
		rows = append(rows,
			mutation{c.class + " at limit", setter(strings.Repeat("a", c.limit), c.steps...), ""},
			mutation{c.class + " over limit", setter(strings.Repeat("a", c.limit+1), c.steps...), c.loc},
			mutation{c.class + " with LF", setter("a\nb", c.steps...), c.loc},
			mutation{c.class + " with tab", setter("a\tb", c.steps...), c.loc},
			mutation{c.class + " with NUL", setter("a\x00b", c.steps...), c.loc},
			mutation{c.class + " with U+2028", setter("a\u2028b", c.steps...), c.loc},
			mutation{c.class + " with U+2029", setter("a\u2029b", c.steps...), c.loc},
		)
	}
	producerStatus := []any{"comparison", "measurement_profile", "producers", 0, "status"}
	configHash := []any{"comparison", "config_hash"}
	hex64 := strings.Repeat("0123456789abcdef", 4)
	rows = append(rows,
		mutation{"text limit counts characters, not bytes", setter(strings.Repeat("é", 500), "findings", 0, "why"), ""},
		mutation{"text with CR", setter("a\rb", "findings", 0, "why"), "findings[0].why"},
		mutation{"text with quotes, backticks and dashes", setter("set `x: \"y\"` — then retry", "findings", 0, "why"), ""},
		mutation{"token with a backtick", setter("o`k", producerStatus...), "producers[0].status"},
		mutation{"token timed out", setter("timed out", producerStatus...), ""},
		mutation{"config_hash empty", setter("", configHash...), ""},
		mutation{"config_hash 64 hex", setter(hex64, configHash...), ""},
		mutation{"config_hash uppercase", setter(strings.ToUpper(hex64), configHash...), "comparison.config_hash"},
		mutation{"config_hash 63 hex", setter(hex64[:63], configHash...), "comparison.config_hash"},
		mutation{"config_hash with sha256 prefix", setter("sha256:"+hex64, configHash...), "comparison.config_hash"},
	)
	runMutations(t, append(rows, toolVersionRows()...))
}

var toolVersionSteps = []any{"comparison", "measurement_profile", "producers", 0, "tool_version"}

// toolVersionRows: a producer's tool_version is the tool's own version output, so it is free text. Every
// string the engine emits must decode; the other version-like fields stay tokens.
func toolVersionRows() []mutation {
	const loc = "producers[0].tool_version"
	accepted := []struct{ source, version string }{
		{"go/packages", "go1.27.1"},
		{"go/packages devel toolchain", "devel go1.28-4f2a1b3c Tue Sep 30 12:00:00 2026 +0000"},
		{"dependency-cruiser", "18.5.0"},
		{"grimp helper", "grimp 3.17; python 3.14.3"},
		{"grimp failure with uv", "uv 0.12.21 (Homebrew 2026-09-29 aarch64-apple-darwin)"},
		{"grimp failure with python3", "Python 3.12.4"},
		{"cargo", "cargo 1.98.0 (797e8a9bc 2026-08-05)"},
		{"cargo-modules", "cargo-modules 0.26.0"},
		{"scip with rust-analyzer", "rust-analyzer 1.98.0"},
		{"scip with scip-python", "scip-python 0.6.6"},
		{"ast-grep", "ast-grep 0.45.3"},
		{"jscpd", "jscpd 5.4.0"},
		{"git-history", "git version 2.56.0"},
		{"git-history with macOS git", "git version 2.54.0 (Apple Git-157)"},
		{"supplied-coverage with two formats", "coverage-parser.go-coverprofile.v1,coverage-parser.lcov.v1"},
		{"limit counted in characters", strings.Repeat("é", 128)},
	}
	var rows []mutation
	for _, a := range accepted {
		rows = append(rows, mutation{"tool version of " + a.source, setter(a.version, toolVersionSteps...), ""})
	}
	semantics := []any{"comparison", "measurement_profile", "producers", 0, "semantics_version"}
	return append(rows,
		mutation{"tool version of 129 characters", setter(strings.Repeat("é", 129), toolVersionSteps...), loc},
		mutation{"tool version with a bidi override", setter("v1"+string(rune(0x202e))+"2", toolVersionSteps...), loc},
		mutation{"tool version with a zero-width space", setter("v1"+string(rune(0x200b))+"2", toolVersionSteps...), loc},
		mutation{"tool version with a no-break space", setter("v1"+string(rune(0x00a0))+"2", toolVersionSteps...), loc},
		mutation{"tool version with CR", setter("cargo 1.98.0\r", toolVersionSteps...), loc},
		mutation{"semantics_version stays a token", setter("grimp.v2; python", semantics...), "producers[0].semantics_version"},
		mutation{"unknowns stay tokens", setter([]any{"uv (unknown)"}, "comparison", "measurement_profile", "unknowns"),
			"measurement_profile.unknowns[0]"},
	)
}

func TestPathRules(t *testing.T) {
	fields := []struct {
		loc        string
		emptyValid bool
		steps      []any
	}{
		{"findings[0].locations[0].file", false, []any{"findings", 0, "locations", 0, "file"}},
		{"agent_tasks[0].files[0]", false, []any{"agent_tasks", 0, "files", 0}},
		{"findings[0].edge.from.path", true, []any{"findings", 0, "edge", "from", "path"}},
	}
	values := []struct {
		name, value string
		valid       bool
	}{
		{"file", "pkg/a/a.go", true},
		{"directory", "pkg/b", true},
		{"glob", "pkg/a/**", true},
		{"repository root", ".", true},
		{"absolute", "/etc/passwd", false},
		{"parent", "../x", false},
		{"escaping parent", "a/../../x", false},
		{"double slash", "a//b", false},
		{"dot segment", "a/./b", false},
		{"backslash", `a\b`, false},
		{"drive prefix", "C:/x", false},
		{"513 characters", strings.Repeat("a", 513), false},
	}
	var rows []mutation
	for _, f := range fields {
		for _, v := range values {
			row := mutation{f.loc + " " + v.name, setter(v.value, f.steps...), f.loc}
			if v.valid {
				row.wantLoc = ""
			}
			rows = append(rows, row)
		}
		empty := mutation{f.loc + " empty", setter("", f.steps...), f.loc}
		if f.emptyValid {
			empty.wantLoc = ""
		}
		rows = append(rows, empty)
	}
	runMutations(t, rows)
}

func TestEnums(t *testing.T) {
	comparisonStatuses := []string{"not_requested", "comparable", "non_comparable"}
	enums := []struct {
		loc     string
		allowed []string
		steps   []any
	}{
		{"verdict", []string{"healthy", "needs_attention", "blocked"}, []any{"verdict"}},
		{"decision.hard_gates", []string{"pass", "fail", "unmeasured"}, []any{"decision", "hard_gates"}},
		{"comparison.status", comparisonStatuses, []any{"comparison", "status"}},
		{"gate_reference.status", comparisonStatuses, []any{"gate_reference", "status"}},
		{"dimensions.intent.status", []string{"measured", "partial", "unmeasured"}, []any{"dimensions", "intent", "status"}},
		{"agent_tasks[0].repair_kind", []string{"code_change", "needs_owner_decision"}, []any{"agent_tasks", 0, "repair_kind"}},
		{"producers[0].partial_basis", []string{"unresolved_specifiers", "degraded_precision"},
			[]any{"comparison", "measurement_profile", "producers", 0, "partial_basis"}},
		{"comparison.origin_status", []string{"comparable", "unknown"}, []any{"comparison", "origin_status"}},
		{"findings[0].origin", []string{"introduced", "pre_existing", "unknown"}, []any{"findings", 0, "origin"}},
		{"agent_tasks[0].origin", []string{"introduced", "pre_existing", "unknown"}, []any{"agent_tasks", 0, "origin"}},
		{"gate_reference.drift", DriftClasses, []any{"gate_reference", "drift", 0}},
	}
	var rows []mutation
	for _, e := range enums {
		for _, v := range e.allowed {
			rows = append(rows, mutation{e.loc + " " + v, setter(v, e.steps...), ""})
		}
		rows = append(rows, mutation{e.loc + " unknown value", setter("sometimes", e.steps...), e.loc})
	}
	rows = append(rows, mutation{"producers[0].partial_basis empty", setter("", "comparison", "measurement_profile", "producers", 0, "partial_basis"), ""})
	runMutations(t, rows)

	t.Run("unknown plain-string values decode unchanged", func(t *testing.T) {
		doc := baseDoc(t)
		set(t, doc, "quarantined", "findings", 0, "status")
		set(t, doc, "blocker", "findings", 0, "severity")
		r := mustDecode(t, encode(t, doc))
		if f := r.Findings[0]; f.Status != "quarantined" || f.Severity != "blocker" {
			t.Errorf("finding status %q, severity %q", f.Status, f.Severity)
		}
	})
}

func minimalFinding(i int) any {
	end := map[string]any{"module": "", "path": ""}
	return map[string]any{
		"id": fmt.Sprintf("f%d", i), "kind": "gate", "rule_id": "r", "status": "new", "severity": "low", "confidence": "",
		"edge": map[string]any{"from": end, "to": end, "kind": "import"}, "locations": []any{}, "why": "", "constraint": "",
	}
}

func TestCollectionCaps(t *testing.T) {
	text := func(int) any { return "x" }
	drift := []any{"dimensions", "drift"}
	profile := []any{"comparison", "measurement_profile"}
	caps := []struct {
		loc   string
		limit int
		steps []any
		elem  func(i int) any
	}{
		{"findings", 10_000, []any{"findings"}, minimalFinding},
		{"agent_tasks", 10_000, []any{"agent_tasks"}, func(int) any {
			return map[string]any{"finding_id": "f", "rule_id": "r", "repair_kind": "code_change", "goal": "",
				"constraints": []any{}, "files": []any{}, "validation": []any{}}
		}},
		{"seams", 50_000, []any{"seams"}, func(int) any {
			return map[string]any{"id": "s", "from_module": "a", "to_module": "b", "edges": 1,
				"strength": "l", "distance": "l", "volatility": "l"}
		}},
		{"decision.unevaluated_required_rules", 1_000, []any{"decision", "unevaluated_required_rules"},
			func(int) any { return map[string]any{"rule_id": "r", "reason": "x"} }},
		{"findings[0].locations", 10_000, []any{"findings", 0, "locations"},
			func(i int) any { return map[string]any{"file": "a.go", "line": i} }},
		{"findings[0].allowed_alternatives", 32, []any{"findings", 0, "allowed_alternatives"}, text},
		{"agent_tasks[0].constraints", 35, []any{"agent_tasks", 0, "constraints"}, text},
		{"agent_tasks[0].validation", 32, []any{"agent_tasks", 0, "validation"}, text},
		{"agent_tasks[0].files", 10_002, []any{"agent_tasks", 0, "files"}, text},
		{"dimensions.drift.metrics", 256, append(drift, "metrics"),
			func(int) any { return map[string]any{"name": "m", "value": 1.5, "unit": "count"} }},
		{"dimensions.drift.unknown", 256, append(drift, "unknown"),
			func(int) any { return map[string]any{"fact": "f", "reason": "r", "owner": "o"} }},
		{"dimensions.drift.findings", 10_000, append(drift, "findings"), func(int) any {
			return map[string]any{"id": "f", "rule_id": "r", "kind": "gate", "severity": "low", "status": "new"}
		}},
		{"dimensions.drift.delta.metrics", 256, append(drift, "delta", "metrics"),
			func(int) any { return map[string]any{"name": "m", "before": 1, "after": 2, "change": 1} }},
		{"dimensions.drift.delta.reasons", 64, append(drift, "delta", "reasons"), text},
		{"producers", 64, append(profile, "producers"),
			func(int) any { return map[string]any{"tool": "t", "semantics_version": "t.v1", "status": "ok"} }},
		{"unknowns", 64, append(profile, "unknowns"), text},
		{"coverage.tools", 64, []any{"coverage", "tools"}, func(int) any { return map[string]any{"tool": "t", "status": "ok"} }},
		{"comparison.reasons", 64, []any{"comparison", "reasons"}, text},
		{"comparison.origin_reasons", 64, []any{"comparison", "origin_reasons"}, text},
		{"gate_reference.reasons", 64, []any{"gate_reference", "reasons"}, text},
		{"gate_reference.drift", 6, []any{"gate_reference", "drift"}, func(int) any { return "model_hash" }},
	}
	for _, c := range caps {
		for _, over := range []bool{false, true} {
			n, name := c.limit, c.loc+" at cap"
			if over {
				n, name = c.limit+1, c.loc+" over cap"
			}
			t.Run(name, func(t *testing.T) {
				t.Parallel()
				list := make([]any, n)
				for i := range list {
					list[i] = c.elem(i)
				}
				doc := baseDoc(t)
				set(t, doc, list, c.steps...)
				payload := encode(t, doc)
				if len(payload) > MaxBytes {
					t.Fatalf("payload of %d bytes would test the size cap instead", len(payload))
				}
				_, err := Decode(payload, fixtureEngine)
				if !over {
					if err != nil {
						t.Fatalf("Decode: %v", err)
					}
					return
				}
				wantError(t, err, c.loc)
			})
		}
	}
}

// TestTaskHoldsWhatItsFindingHolds builds a task the way the engine does (agenttask.Build) from a finding at
// its caps: files are the location files plus both edge endpoints; constraints are the finding constraint,
// "rationale: <r>", one "allowed alternative: <a>" per alternative and the target module's public surface;
// a cycle goal is "Break the import cycle: " plus the why. The why ends with the rationale, so neither is longer
// than a why at its cap. A finding that decodes must not yield a task that does not.
func TestTaskHoldsWhatItsFindingHolds(t *testing.T) {
	doc := baseDoc(t)
	text, why := strings.Repeat("w", maxTextLen), strings.Repeat("y", maxFindingLen)

	locations, files := make([]any, maxLocations), []any{"pkg/a/a.go", "pkg/b"}
	for i := range locations {
		file := fmt.Sprintf("pkg/a/f%05d.go", i)
		locations[i] = map[string]any{"file": file, "line": i + 1}
		files = append(files, file)
	}
	alternatives, constraints := make([]any, maxAlternatives), []any{why, "rationale: " + why}
	for i := range alternatives {
		alternatives[i] = text
		constraints = append(constraints, "allowed alternative: "+text)
	}
	globs := make([]string, 100)
	for i := range globs {
		globs[i] = fmt.Sprintf("pkg/b/api/v1/resources/group_%03d/**", i)
	}
	constraints = append(constraints, fmt.Sprintf("public surface of module %q: [%s]", "b", strings.Join(globs, " ")))

	set(t, doc, locations, "findings", 0, "locations")
	set(t, doc, alternatives, "findings", 0, "allowed_alternatives")
	set(t, doc, why, "findings", 0, "why")
	set(t, doc, why, "findings", 0, "constraint")
	set(t, doc, files, "agent_tasks", 0, "files")
	set(t, doc, constraints, "agent_tasks", 0, "constraints")
	set(t, doc, "Break the import cycle: "+why, "agent_tasks", 0, "goal")

	r := mustDecode(t, encode(t, doc))
	if task := r.AgentTasks[0]; len(task.Files) != maxLocations+2 || len(task.Constraints) != maxAlternatives+3 {
		t.Errorf("task with %d files and %d constraints", len(task.Files), len(task.Constraints))
	}
}

// optionalKeys are the generalized locations that may be absent: omitempty keys and dropped keys. A trailing
// dot marks a dropped subtree, where nothing is required.
func optionalKeys() []string {
	return []string{
		"gate_reference", "gate_reference.base_ref", "measurement", "measurement.",
		"gate_reference.config_hash", "gate_reference.model_hash", "gate_reference.labels_hash",
		"gate_reference.rubric_version", "gate_reference.measurement_profile", "gate_reference.measurement_profile.",
		"gate_reference.drift", "gate_reference.baseline_present",
		"decision.unevaluated_required_rules",
		"comparison.base_ref", "comparison.config_hash", "comparison.model_hash", "comparison.labels_hash",
		"comparison.rubric_version", "comparison.measurement_profile", "comparison.classification_hash",
		"comparison.drift", "comparison.baseline_present", "comparison.origin_status", "comparison.origin_reasons",
		"comparison.introduced_finding_ids", "comparison.resolved_finding_ids", "findings[].origin",
		"comparison.measurement_profile.producers[].tool_version",
		"comparison.measurement_profile.producers[].partial_basis",
		"findings[].matched_by", "findings[].matched_by.", "findings[].allowed_alternatives",
		"agent_tasks[].declarations", "agent_tasks[].declarations[].", "agent_tasks[].origin",
		"dimensions.*.delta", "dimensions.*.delta.reasons", "dimensions.*.delta.metrics",
		"dimensions.*.metrics[].denominator", "dimensions.*.metrics[].denominator.", "dimensions.*.metrics[].provenance",
		"coverage.tools[].reason",
		"seams[].severity", "seams[].scored_edges", "seams[].abstained_edges", "seams[].critical_edges",
		"seams[].high_or_worse_edges", "seams[].critical_share_pct", "seams[].high_or_worse_share_pct",
		"seams[].volatility_provenance", "seams[].quadrant", "seams[].label_evidence_hash", "seams[].confidence",
		"seams[].role_expectation", "seams[].hypothesis", "seams[].labels", "seams[].distributed_monolith",
		"seams[].raw_distance", "seams[].raw_distance.", "seams[].scores", "seams[].scores.",
	}
}

func isOptionalKey(general string) bool {
	for _, o := range optionalKeys() {
		if general == o || (strings.HasSuffix(o, ".") && strings.HasPrefix(general, o)) {
			return true
		}
	}
	return false
}

// keyPaths lists every object key of doc as steps, with its generalized location ("findings[].edge.from").
func keyPaths(v any, steps []any, general string, visit func(steps []any, general string)) {
	switch v := v.(type) {
	case map[string]any:
		for _, k := range slices.Sorted(maps.Keys(v)) {
			child, g := v[k], k
			if general == "dimensions" {
				g = "*"
			}
			if general != "" {
				g = general + "." + g
			}
			next := append(slices.Clone(steps), k)
			visit(next, g)
			keyPaths(child, next, g, visit)
		}
	case []any:
		for i, child := range v {
			keyPaths(child, append(slices.Clone(steps), i), general+"[]", visit)
		}
	}
}

func TestPresenceRules(t *testing.T) {
	visited := map[string]bool{} // one instance of each key location is enough
	keyPaths(baseDoc(t), nil, "", func(steps []any, general string) {
		if visited[general] {
			return
		}
		visited[general] = true
		name := fmt.Sprint(steps...)
		t.Run("absent "+name, func(t *testing.T) {
			doc := baseDoc(t)
			del(t, doc, steps...)
			_, err := Decode(encode(t, doc), fixtureEngine)
			if optional := isOptionalKey(general); optional != (err == nil) {
				t.Errorf("%s absent: optional=%v, err=%v", general, optional, err)
			}
		})
		t.Run("null "+name, func(t *testing.T) {
			doc := baseDoc(t)
			set(t, doc, nil, steps...)
			_, err := Decode(encode(t, doc), fixtureEngine)
			if nullable := general == "seams[].scores.p10" || general == "seams[].scores.p90"; nullable != (err == nil) {
				t.Errorf("%s null: nullable=%v, err=%v", general, nullable, err)
			}
		})
	})

	t.Run("absent pointers are nil", func(t *testing.T) {
		doc := baseDoc(t)
		del(t, doc, "gate_reference")
		del(t, doc, "comparison", "measurement_profile")
		r := mustDecode(t, encode(t, doc))
		if r.GateReference != nil || r.Comparison.MeasurementProfile != nil {
			t.Errorf("gate reference %v, profile %v, want nil", r.GateReference, r.Comparison.MeasurementProfile)
		}
	})

	runMutations(t, []mutation{
		{"dropped keys absent", func(t *testing.T, doc map[string]any) {
			del(t, doc, "measurement")
			del(t, doc, "findings", 0, "matched_by")
			del(t, doc, "agent_tasks", 0, "declarations")
		}, ""},
		{"dimensions with eight keys", deleter("dimensions", "drift"), "dimensions"},
		{"dimensions with a tenth key", func(t *testing.T, doc map[string]any) {
			set(t, doc, at(t, doc, "dimensions", "drift"), "dimensions", "security")
		}, "dimensions"},
		{"dimension name different from its key", setter("drift", "dimensions", "intent", "name"), "dimensions.intent.name"},
	})
}

func TestDroppedKeysAreTypeChecked(t *testing.T) {
	p10 := []any{"seams", 0, "scores", "p10"}
	runMutations(t, []mutation{
		{"matched_by as a list", setter([]any{"a"}, "findings", 0, "matched_by"), "findings[0].matched_by"},
		{"matched_by with a numeric value", setter(map[string]any{"score": 5}, "findings", 0, "matched_by"), "findings[0].matched_by"},
		{"declarations as an object", setter(map[string]any{}, "agent_tasks", 0, "declarations"), "agent_tasks[0].declarations"},
		{"history_depth as a string", setter("0", "measurement", "history_depth"), "measurement.history_depth"},
		{"p10 null", setter(nil, p10...), ""},
		{"p10 integer", setter(3, p10...), ""},
		{"p10 string", setter("3", p10...), "seams[0].scores.p10"},
		{"unknown key in raw_distance", setter("x", "seams", 0, "raw_distance", "hops"), "seams[0].raw_distance"},
	})
}

func TestDuplicateFindingIDs(t *testing.T) {
	for _, statuses := range [][2]string{{"baseline", "fixed"}, {"new", "new"}} {
		t.Run(statuses[0]+" and "+statuses[1], func(t *testing.T) {
			doc := baseDoc(t)
			first, second := minimalFinding(7).(map[string]any), minimalFinding(7).(map[string]any)
			first["status"], second["status"] = statuses[0], statuses[1]
			set(t, doc, []any{first, second}, "findings")
			_, err := Decode(encode(t, doc), fixtureEngine)
			wantError(t, err, "findings[1].id")
		})
	}
}

func TestFindingActive(t *testing.T) {
	tests := []struct {
		kind, status string
		want         bool
	}{
		{"gate", "new", true},
		{"gate", "expired_waiver", true},
		{"gate", "baseline", false},
		{"gate", "waived", false},
		{"gate", "fixed", false},
		{"gate", "reopened", false},
		{"gate", "", false},
		{"advisory", "new", false},
		{"advisory", "expired_waiver", false},
		{"blocker", "new", false},
		{"", "new", false},
	}
	for _, tt := range tests {
		t.Run(tt.kind+"/"+tt.status, func(t *testing.T) {
			if got := (Finding{Kind: tt.kind, Status: tt.status}).Active(); got != tt.want {
				t.Errorf("Active() = %v, want %v", got, tt.want)
			}
		})
	}
}

func TestValidationCommandRules(t *testing.T) {
	const loc = "agent_tasks[0].validation"
	valid := []string{
		"archfit check -c .archfit.yaml --base main --lang go --require-tools",
		"archfit check --refreshed",
	}
	t.Run("valid commands decode verbatim and in order", func(t *testing.T) {
		doc := baseDoc(t)
		set(t, doc, []any{valid[0], valid[1]}, "agent_tasks", 0, "validation")
		if got := mustDecode(t, encode(t, doc)).AgentTasks[0].Validation; !slices.Equal(got, valid) {
			t.Errorf("validation %q, want %q", got, valid)
		}
	})
	many := make([]any, 33)
	for i := range many {
		many[i] = valid[0]
	}
	command := func(c string) func(*testing.T, map[string]any) {
		return setter([]any{valid[0], c}, "agent_tasks", 0, "validation")
	}
	runMutations(t, []mutation{
		{"--refresh", command("archfit check --refresh -c .archfit.yaml"), loc + "[1]"},
		{"--refresh=true", command("archfit check --refresh=true"), loc + "[1]"},
		{"513 characters", command(strings.Repeat("a", 513)), loc + "[1]"},
		{"with LF", command("archfit check\nrm -rf ."), loc + "[1]"},
		{"33 commands", setter(many, "agent_tasks", 0, "validation"), loc},
	})
}

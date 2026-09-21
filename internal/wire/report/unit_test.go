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
		{"text", "findings[0].why", 500, []any{"findings", 0, "why"}},
		{"command", "agent_tasks[0].validation[0]", 512, []any{"agent_tasks", 0, "validation", 0}},
		{"identifier", "findings[0].rule_id", 200, []any{"findings", 0, "rule_id"}},
		{"token", "seams[0].strength", 64, []any{"seams", 0, "strength"}},
		{"version", "comparison.rubric_version", 128, []any{"comparison", "rubric_version"}},
		{"hash token", "comparison.model_hash", 128, []any{"comparison", "model_hash"}},
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
	runMutations(t, rows)
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
		{"dot", ".", false},
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
	}
	var rows []mutation
	for _, e := range enums {
		for _, v := range e.allowed {
			rows = append(rows, mutation{e.loc + " " + v, setter(v, e.steps...), ""})
		}
		rows = append(rows, mutation{e.loc + " unknown value", setter("sometimes", e.steps...), e.loc})
	}
	rows = append(rows, mutation{"producers[0].partial_basis empty", setter("", enums[len(enums)-1].steps...), ""})
	runMutations(t, rows)

	t.Run("unknown plain-string values decode unchanged", func(t *testing.T) {
		doc := baseDoc(t)
		set(t, doc, "quarantined", "findings", 0, "status")
		set(t, doc, "blocker", "findings", 0, "severity")
		set(t, doc, "inherited", "agent_tasks", 0, "origin")
		r := mustDecode(t, encode(t, doc))
		if f := r.Findings[0]; f.Status != "quarantined" || f.Severity != "blocker" {
			t.Errorf("finding status %q, severity %q", f.Status, f.Severity)
		}
		if got := r.AgentTasks[0].Origin; got != "inherited" {
			t.Errorf("origin %q", got)
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
		{"findings[0].locations", 100, []any{"findings", 0, "locations"},
			func(i int) any { return map[string]any{"file": "a.go", "line": i} }},
		{"findings[0].allowed_alternatives", 32, []any{"findings", 0, "allowed_alternatives"}, text},
		{"agent_tasks[0].constraints", 32, []any{"agent_tasks", 0, "constraints"}, text},
		{"agent_tasks[0].validation", 32, []any{"agent_tasks", 0, "validation"}, text},
		{"agent_tasks[0].files", 1_000, []any{"agent_tasks", 0, "files"}, text},
		{"dimensions.drift.metrics", 256, append(drift, "metrics"),
			func(int) any { return map[string]any{"name": "m", "value": 1.5, "unit": "count"} }},
		{"dimensions.drift.unknown", 256, append(drift, "unknown"),
			func(int) any { return map[string]any{"fact": "f", "reason": "r", "owner": "o"} }},
		{"dimensions.drift.findings", 10_000, append(drift, "findings"), func(int) any {
			return map[string]any{"id": "f", "rule_id": "r", "kind": "gate", "severity": "low", "status": "new"}
		}},
		{"dimensions.drift.delta.new_findings", 10_000, append(drift, "delta", "new_findings"), text},
		{"dimensions.drift.delta.resolved_findings", 10_000, append(drift, "delta", "resolved_findings"), text},
		{"dimensions.drift.delta.metrics", 256, append(drift, "delta", "metrics"),
			func(int) any { return map[string]any{"name": "m", "before": 1, "after": 2, "change": 1} }},
		{"dimensions.drift.delta.reasons", 64, append(drift, "delta", "reasons"), text},
		{"producers", 64, append(profile, "producers"),
			func(int) any { return map[string]any{"tool": "t", "semantics_version": "t.v1", "status": "ok"} }},
		{"unknowns", 64, append(profile, "unknowns"), text},
		{"coverage.tools", 64, []any{"coverage", "tools"}, func(int) any { return map[string]any{"tool": "t", "status": "ok"} }},
		{"comparison.reasons", 64, []any{"comparison", "reasons"}, text},
		{"comparison.task_origin_reasons", 64, []any{"comparison", "task_origin_reasons"}, text},
		{"gate_reference.reasons", 64, []any{"gate_reference", "reasons"}, text},
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
				_, err := Decode(payload)
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

// optionalKeys are the generalized locations that may be absent: omitempty keys and dropped keys. A trailing
// dot marks a dropped subtree, where nothing is required.
func optionalKeys() []string {
	return []string{
		"gate_reference", "gate_reference.base_ref", "measurement", "measurement.",
		"gate_reference.config_hash", "gate_reference.model_hash", "gate_reference.labels_hash",
		"gate_reference.rubric_version", "gate_reference.measurement_profile", "gate_reference.measurement_profile.",
		"gate_reference.task_origin_status", "gate_reference.task_origin_reasons",
		"decision.unevaluated_required_rules",
		"comparison.base_ref", "comparison.config_hash", "comparison.model_hash", "comparison.labels_hash",
		"comparison.rubric_version", "comparison.measurement_profile", "comparison.task_origin_status",
		"comparison.task_origin_reasons",
		"comparison.measurement_profile.producers[].tool_version",
		"comparison.measurement_profile.producers[].partial_basis",
		"findings[].matched_by", "findings[].matched_by.", "findings[].allowed_alternatives",
		"agent_tasks[].declarations", "agent_tasks[].declarations[].", "agent_tasks[].origin",
		"dimensions.*.delta", "dimensions.*.delta.reasons", "dimensions.*.delta.metrics",
		"dimensions.*.delta.new_findings", "dimensions.*.delta.resolved_findings",
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
			_, err := Decode(encode(t, doc))
			if optional := isOptionalKey(general); optional != (err == nil) {
				t.Errorf("%s absent: optional=%v, err=%v", general, optional, err)
			}
		})
		t.Run("null "+name, func(t *testing.T) {
			doc := baseDoc(t)
			set(t, doc, nil, steps...)
			_, err := Decode(encode(t, doc))
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
			_, err := Decode(encode(t, doc))
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

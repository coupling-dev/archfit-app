package report

import (
	"bytes"
	"encoding/json"
	"reflect"
	"slices"
	"strings"
	"testing"
)

func checkExpectation(t *testing.T, r Report, want expectedFields) {
	t.Helper()
	if r.Verdict != want.Verdict {
		t.Errorf("verdict %q, want %q", r.Verdict, want.Verdict)
	}
	if r.Decision.HardGates != want.HardGates {
		t.Errorf("hard_gates %q, want %q", r.Decision.HardGates, want.HardGates)
	}
	ids := []string{}
	for _, u := range r.Decision.UnevaluatedRequiredRules {
		ids = append(ids, u.RuleID)
	}
	if !slices.Equal(ids, want.UnevaluatedRuleIDs) {
		t.Errorf("unevaluated rules %q, want %q", ids, want.UnevaluatedRuleIDs)
	}
	profile := r.Comparison.MeasurementProfile
	if (profile != nil) != want.HasProfile {
		t.Errorf("profile present %v, want %v", profile != nil, want.HasProfile)
	}
	if profile != nil && profile.Version != want.ProfileVersion {
		t.Errorf("profile version %q, want %q", profile.Version, want.ProfileVersion)
	}
	status := ""
	if r.GateReference != nil {
		status = r.GateReference.Status
	}
	if status != want.GateReferenceStatus {
		t.Errorf("gate reference status %q, want %q", status, want.GateReferenceStatus)
	}
}

func TestDecodeRealEngineReport(t *testing.T) {
	const fixture = "report/real-blocked-no-baseline.json"
	r := mustDecode(t, loadFixture(t, fixture))
	checkExpectation(t, r, loadExpectation(t, fixture).Fields)

	if r.SchemaVersion != StateSchemaV1 || r.Verdict != "blocked" || r.Decision.HardGates != "fail" || r.Decision.ActiveBlockers != 1 {
		t.Errorf("schema %q, verdict %q, decision %+v", r.SchemaVersion, r.Verdict, r.Decision)
	}
	if got, want := r.Comparison.ConfigHash, "4a18013bab44d922fa67d9e8e699463b2b5284f1c2908b62d2b7c5f607c55574"; got != want {
		t.Errorf("config_hash %q, want %q", got, want)
	}
	if p := r.Comparison.MeasurementProfile; p == nil || p.Version != "archfit.measurement.v2" || len(p.Producers) != 8 {
		t.Errorf("measurement profile %+v", p)
	}
	ref := r.GateReference
	if ref == nil || ref.Status != "non_comparable" || ref.BaseRef != "baseline" || ref.BaselinePresent ||
		!slices.Equal(ref.Reasons, []string{"no baseline file was loaded"}) || !slices.Equal(ref.Drift, []string{"reference_incomplete"}) {
		t.Errorf("gate reference %+v", ref)
	}
	if len(r.Dimensions) != 9 || len(r.Seams) != 1 || len(r.Findings) != 2 {
		t.Errorf("%d dimensions, %d seams, %d findings", len(r.Dimensions), len(r.Seams), len(r.Findings))
	}
	if len(r.AgentTasks) != 1 || !slices.Equal(r.AgentTasks[0].Validation, []string{"archfit check -c .archfit.yaml"}) {
		t.Errorf("agent tasks %+v", r.AgentTasks)
	}
	wantLocation := []Location{{File: "pkg/a/a.go", Line: 3}}
	if f := r.Findings[0]; f.RuleID != "no_direct_b_dependency" || !slices.Equal(f.Locations, wantLocation) {
		t.Errorf("finding %+v", f)
	}
}

func TestProducerToolVersionsSurvive(t *testing.T) {
	tests := []struct {
		fixture string
		want    map[string]Producer // by tool; only the rows checked
	}{
		{"report/real-polyglot-blocked.json", map[string]Producer{
			"cargo":              {ToolVersion: "cargo 1.98.0 (797e8a9bc 2026-08-05)", Status: "ok"},
			"grimp":              {ToolVersion: "grimp 3.17; python 3.14.3", Status: "ok"},
			"dependency-cruiser": {ToolVersion: "18.5.0", Status: "ok"},
			"cargo-modules":      {ToolVersion: "cargo-modules 0.26.0", Status: "ok"},
			"scip":               {ToolVersion: "scip-python 0.6.6", Status: "ok"},
			"jscpd":              {ToolVersion: "jscpd 5.4.0", Status: "ok"},
			"ast-grep/syntax":    {ToolVersion: "ast-grep 0.45.3", Status: "ok"},
		}},
		{"report/producer-versions.json", map[string]Producer{
			"grimp":             {ToolVersion: "uv 0.12.21 (Homebrew 2026-09-29 aarch64-apple-darwin)", Status: "partial"},
			"git-history":       {ToolVersion: "git version 2.54.0 (Apple Git-157)", Status: "ok"},
			"supplied-coverage": {ToolVersion: "coverage-parser.go-coverprofile.v1,coverage-parser.lcov.v1", Status: "ok"},
		}},
	}
	for _, tt := range tests {
		t.Run(tt.fixture, func(t *testing.T) {
			r := mustDecode(t, loadFixture(t, tt.fixture))
			checkExpectation(t, r, loadExpectation(t, tt.fixture).Fields)
			got := map[string]Producer{}
			for _, p := range r.Comparison.MeasurementProfile.Producers {
				got[p.Tool] = Producer{ToolVersion: p.ToolVersion, Status: p.Status}
			}
			for tool, want := range tt.want {
				if got[tool] != want {
					t.Errorf("producer %s = %+v, want %+v", tool, got[tool], want)
				}
			}
		})
	}
}

func TestDecodeConformanceFixtures(t *testing.T) {
	names := fixtures(t, "report")
	for _, required := range []string{
		"healthy", "attention-pass", "blocked", "missing-evidence", "unknown-identity",
		"profile-comparable", "profile-incompatible", "blocker-and-unevaluated", "non-applicable-rule",
		"locations-empty", "matched-by-populated", "repair-kinds", "waiver-expiry-order", "invalid-input",
	} {
		if !slices.Contains(names, "report/"+required+".json") {
			t.Errorf("fixture %s is missing", required)
		}
	}
	for _, name := range names {
		t.Run(name, func(t *testing.T) {
			want := loadExpectation(t, name)
			r, err := Decode(loadFixture(t, name), fixtureRelease(t, name))
			if want.Decode == decodeError {
				if err == nil {
					t.Fatal("decoded, want an error")
				}
				return
			}
			if err != nil {
				t.Fatalf("Decode: %v", err)
			}
			checkExpectation(t, r, want.Fields)
		})
	}
}

// validFixtures lists every fixture, of both folders, that is expected to decode.
func validFixtures(t testing.TB) []string {
	t.Helper()
	var out []string
	for _, name := range append(fixtures(t, "report"), fixtures(t, "leakage")...) {
		if loadExpectation(t, name).Decode == decodeOK {
			out = append(out, name)
		}
	}
	return out
}

func collectKeys(v any, into map[string]bool) {
	switch v := v.(type) {
	case map[string]any:
		for k, child := range v {
			into[k] = true
			collectKeys(child, into)
		}
	case []any:
		for _, child := range v {
			collectKeys(child, into)
		}
	}
}

func TestMarshalledReportHasNoDroppedKey(t *testing.T) {
	for _, name := range validFixtures(t) {
		t.Run(name, func(t *testing.T) {
			payload, release := loadFixture(t, name), fixtureRelease(t, name)
			first := mustDecodeAs(t, payload, release)
			marshalled := encode(t, first)

			keys := map[string]bool{}
			collectKeys(parseDoc(t, marshalled), keys)
			for _, dropped := range []string{"matched_by", "declarations", "measurement", "provenance", "denominator", "hypothesis", "scores"} {
				if keys[dropped] {
					t.Errorf("marshalled report has a %q key", dropped)
				}
			}
			if second := mustDecodeAs(t, marshalled, release); !reflect.DeepEqual(first, second) {
				t.Error("the second decode differs from the first")
			}
			source := parseDoc(t, payload)
			for key, got := range map[string]int{"findings": len(first.Findings), "agent_tasks": len(first.AgentTasks), "seams": len(first.Seams)} {
				if want := len(source[key].([]any)); got != want {
					t.Errorf("%d %s, fixture has %d", got, key, want)
				}
			}
		})
	}
}

// jsonFields maps the JSON key of every field of a struct type to the field type.
func jsonFields(t reflect.Type) map[string]reflect.Type {
	out := map[string]reflect.Type{}
	for i := range t.NumField() {
		f := t.Field(i)
		name, _, _ := strings.Cut(f.Tag.Get("json"), ",")
		out[name] = f.Type
	}
	return out
}

func indirect(t reflect.Type) reflect.Type {
	for t.Kind() == reflect.Pointer {
		t = t.Elem()
	}
	return t
}

// checkKeptInShadow asserts that every key of a public type exists in the shadow type at the same location.
func checkKeptInShadow(t *testing.T, loc string, public, shadow reflect.Type) {
	public, shadow = indirect(public), indirect(shadow)
	switch public.Kind() {
	case reflect.Slice, reflect.Map:
		checkKeptInShadow(t, loc+"[]", public.Elem(), shadow.Elem())
	case reflect.Struct:
		shadowFields := jsonFields(shadow)
		for key, typ := range jsonFields(public) {
			shadowType, ok := shadowFields[key]
			if !ok {
				t.Errorf("%s.%s is a kept key without a shadow field", loc, key)
				continue
			}
			checkKeptInShadow(t, loc+"."+key, typ, shadowType)
		}
	}
}

type schemaWalker struct {
	t    *testing.T
	root map[string]any
}

// resolve follows local "$ref" pointers such as "#/$defs/finding".
func (w schemaWalker) resolve(node map[string]any) map[string]any {
	for {
		ref, ok := node["$ref"].(string)
		if !ok {
			return node
		}
		var cur any = w.root
		for part := range strings.SplitSeq(strings.TrimPrefix(ref, "#/"), "/") {
			cur = cur.(map[string]any)[part]
		}
		node = cur.(map[string]any)
	}
}

// check compares one schema node with the shadow type at the same location, both ways.
func (w schemaWalker) check(loc string, node map[string]any, shadow reflect.Type) {
	node, shadow = w.resolve(node), indirect(shadow)
	if items, ok := node["items"].(map[string]any); ok {
		if shadow.Kind() != reflect.Slice {
			w.t.Errorf("%s: schema list, shadow %s", loc, shadow)
			return
		}
		w.check(loc+"[]", items, shadow.Elem())
		return
	}
	props, _ := node["properties"].(map[string]any)
	if shadow.Kind() == reflect.Map {
		if extra, ok := node["additionalProperties"].(map[string]any); ok {
			w.check(loc+".*", extra, shadow.Elem())
		}
		for key, prop := range props {
			w.check(loc+"."+key, prop.(map[string]any), shadow.Elem())
		}
		return
	}
	if shadow.Kind() != reflect.Struct {
		if len(props) > 0 {
			w.t.Errorf("%s: schema object, shadow %s", loc, shadow)
		}
		return
	}
	fields := jsonFields(shadow)
	for key, prop := range props {
		typ, ok := fields[key]
		if !ok {
			w.t.Errorf("%s.%s is a schema property without a shadow field", loc, key)
			continue
		}
		w.check(loc+"."+key, prop.(map[string]any), typ)
	}
	for key := range fields {
		if _, ok := props[key]; !ok {
			w.t.Errorf("%s.%s is a shadow field the schema does not define", loc, key)
		}
	}
}

func TestEngineSchemaKeySetIsCovered(t *testing.T) {
	checkKeptInShadow(t, "report", reflect.TypeFor[Report](), reflect.TypeFor[shadowReport]())

	// Every key of a real capture and of the all-keys fixture must be a shadow field: both decode strictly.
	mustDecode(t, loadFixture(t, "report/real-blocked-no-baseline.json"))
	mustDecode(t, loadFixture(t, baseFixture))
	mustDecode(t, loadFixture(t, "report/real-seam-policy.json"))

	w := schemaWalker{t: t, root: parseDoc(t, loadFixture(t, "schema/archfit.state.schema.json"))}
	w.check("report", w.root, reflect.TypeFor[shadowReport]())
}

// shadowFields visits every field of every shadow struct reachable from t, with its JSON location.
func shadowFields(loc string, t reflect.Type, visit func(loc string, f reflect.StructField)) {
	t = indirect(t)
	switch t.Kind() {
	case reflect.Slice, reflect.Map:
		shadowFields(loc+"[]", t.Elem(), visit)
	case reflect.Struct:
		for i := range t.NumField() {
			f := t.Field(i)
			key, _, _ := strings.Cut(f.Tag.Get("json"), ",")
			visit(loc+"."+key, f)
			shadowFields(loc+"."+key, f.Type, visit)
		}
	}
}

// decisionCore is what the gate decides from (gate/verdict Decide, the identity check, the receipt) plus the two
// fields of Finding.Active.
var decisionCore = []string{
	"report.schema_version", "report.verdict", "report.decision", "report.decision.hard_gates",
	"report.decision.unevaluated_required_rules", "report.decision.unevaluated_required_rules[].rule_id",
	"report.decision.unevaluated_required_rules[].reason", "report.comparison", "report.comparison.config_hash",
	"report.comparison.measurement_profile", "report.comparison.measurement_profile.version",
	"report.gate_reference", "report.gate_reference.status", "report.gate_reference.reasons",
	"report.findings", "report.findings[].id", "report.findings[].kind", "report.findings[].status",
}

func TestDecisionCoreHasShadowFields(t *testing.T) {
	seen := map[string]bool{}
	shadowFields("report", reflect.TypeFor[shadowReport](), func(loc string, f reflect.StructField) {
		if !slices.Contains(decisionCore, loc) {
			return
		}
		seen[loc] = true
	})
	for _, loc := range decisionCore {
		if !seen[loc] {
			t.Errorf("decision core field %s has no shadow field", loc)
		}
	}
}

func TestSizeCap(t *testing.T) {
	doc := baseDoc(t)
	set(t, doc, "", "measurement", "source_ref")
	room := MaxBytes - len(encode(t, doc))
	padded := func(n int) []byte {
		set(t, doc, strings.Repeat("a", n), "measurement", "source_ref")
		return encode(t, doc)
	}

	exact := padded(room)
	if len(exact) != MaxBytes {
		t.Fatalf("payload of %d bytes, want %d", len(exact), MaxBytes)
	}
	mustDecode(t, exact)

	_, err := Decode(padded(room+1), fixtureEngine)
	wantError(t, err, "MaxBytes")

	// The size check comes before any parsing: garbage over the cap reports the size, not the syntax.
	_, err = Decode(bytes.Repeat([]byte{0xff}, MaxBytes+1), fixtureEngine)
	wantError(t, err, "MaxBytes")
}

func TestWrongSchemaVersion(t *testing.T) {
	// The envelope's version string is any wire version that is not the state schema's.
	for _, version := range []string{"", "archfit.architecture-state.v2", "archfit.upload-envelope.v1"} {
		t.Run(version, func(t *testing.T) {
			doc := baseDoc(t)
			set(t, doc, version, "schema_version")
			_, err := Decode(encode(t, doc), fixtureEngine)
			wantError(t, err, "schema_version")
		})
	}
}

func TestEngineVersions(t *testing.T) {
	got := EngineVersions()
	if want := []string{"v3.0.0"}; !slices.Equal(got, want) {
		t.Errorf("EngineVersions() = %q, want %q", got, want)
	}
	got[0] = "changed"
	if EngineVersions()[0] != "v3.0.0" {
		t.Error("EngineVersions() hands out the package's own list")
	}
}

func TestUnknownEngineVersionIsRefused(t *testing.T) {
	payload := loadFixture(t, "report/real-blocked-no-baseline.json")
	for _, version := range []string{"", "v2.5.0", "v2.6.0", "3.0.0", "v3.0.0 ", "V3.0.0", "v3.0.0-" + leakMarker} {
		t.Run(version, func(t *testing.T) {
			r, err := Decode(payload, version)
			wantError(t, err, "engine_version")
			if !isZero(r) {
				t.Error("an error must come with a zero Report")
			}
			if strings.Contains(err.Error(), leakMarker) {
				t.Errorf("error echoes the engine version: %v", err)
			}
		})
	}
}

func TestReportDoesNotAliasPayload(t *testing.T) {
	payload := loadFixture(t, baseFixture)
	first := mustDecode(t, payload)
	want, err := json.Marshal(first)
	if err != nil {
		t.Fatal(err)
	}
	for i := range payload {
		payload[i] = 'x'
	}
	if got := encode(t, first); !bytes.Equal(got, want) {
		t.Error("the Report changed when the payload was overwritten")
	}
}

// TestSeamPolicy: seams[].policy is an optional key with a closed value set, the enum of the pinned schema.
func TestSeamPolicy(t *testing.T) {
	schema := parseDoc(t, loadFixture(t, "schema/archfit.state.schema.json"))
	var values []string
	for _, v := range at(t, schema, "$defs", "Seam", "properties", "policy", "enum").([]any) {
		values = append(values, v.(string))
	}
	if want := []string{"violation", "accepted", "advisory", "allowed", "observed"}; !slices.Equal(values, want) {
		t.Fatalf("schema enum = %q, want %q", values, want)
	}

	type row struct {
		name    string
		release string
		value   any // nil: the key is absent
		want    string
		wantLoc string
	}
	rows := []row{
		{name: "absent", release: fixtureEngine},
		{name: "empty", release: fixtureEngine, value: "", wantLoc: "seams[0].policy"},
		{name: "outside the set", release: fixtureEngine, value: "permitted", wantLoc: "seams[0].policy"},
		{name: "case differs", release: fixtureEngine, value: "Allowed", wantLoc: "seams[0].policy"},
		{name: "not a string", release: fixtureEngine, value: true, wantLoc: "seams[0].policy"},
	}
	for _, v := range values {
		rows = append(rows, row{name: v, release: fixtureEngine, value: v, want: v})
	}
	for _, row := range rows {
		t.Run(row.name, func(t *testing.T) {
			doc := baseDoc(t)
			if row.value != nil {
				set(t, doc, row.value, "seams", 0, "policy")
			}
			r, err := Decode(encode(t, doc), row.release)
			if row.wantLoc != "" {
				wantError(t, err, row.wantLoc)
				if strings.Contains(err.Error(), "permitted") || strings.Contains(err.Error(), "Allowed") {
					t.Errorf("error echoes the value: %v", err)
				}
				return
			}
			if err != nil {
				t.Fatalf("Decode: %v", err)
			}
			if got := r.Seams[0].Policy; got != row.want {
				t.Errorf("Policy = %q, want %q", got, row.want)
			}
		})
	}
}

// TestRootImporterCapture: the engine names an importer that no module owns by its package directory, "." for the
// repository root, and ends the finding why and constraint with the rule's rationale and docs reference.
func TestRootImporterCapture(t *testing.T) {
	r := mustDecode(t, loadFixture(t, "report/real-root-importer.json"))
	if len(r.Findings) != 1 || len(r.AgentTasks) != 1 {
		t.Fatalf("%d findings, %d tasks, want one each", len(r.Findings), len(r.AgentTasks))
	}
	f, task := r.Findings[0], r.AgentTasks[0]
	if f.Edge.From != (Endpoint{Path: "."}) || f.Edge.To.Module != "billing" {
		t.Errorf("edge = %+v, want from the unowned root package to module billing", f.Edge)
	}
	if !slices.Contains(task.Files, ".") {
		t.Errorf("task files = %q, want the root package", task.Files)
	}
	const docs = " (see docs/adr/0001-billing.md)"
	if !strings.HasSuffix(f.Why, " — billing is reached only through the shop checkout"+docs) || !strings.HasSuffix(f.Constraint, docs) {
		t.Errorf("why %q / constraint %q do not end with the rule's rationale and docs", f.Why, f.Constraint)
	}
	if !slices.Contains(task.Constraints, "rationale: billing is reached only through the shop checkout") {
		t.Errorf("task constraints = %q, want the rationale entry", task.Constraints)
	}
}

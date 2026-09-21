package report

import (
	"bytes"
	"encoding/json"
	"errors"
	"os"
	"reflect"
	"slices"
	"strings"
	"testing"
)

func checkExpectation(t *testing.T, r Report, want expectation) {
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
	checkExpectation(t, r, loadExpectation(t, fixture))

	if r.SchemaVersion != StateSchemaV1 || r.Verdict != "blocked" || r.Decision.HardGates != "fail" || r.Decision.ActiveBlockers != 1 {
		t.Errorf("schema %q, verdict %q, decision %+v", r.SchemaVersion, r.Verdict, r.Decision)
	}
	if got, want := r.Comparison.ConfigHash, "4a18013bab44d922fa67d9e8e699463b2b5284f1c2908b62d2b7c5f607c55574"; got != want {
		t.Errorf("config_hash %q, want %q", got, want)
	}
	if p := r.Comparison.MeasurementProfile; p == nil || p.Version != "archfit.measurement.v1" || len(p.Producers) != 12 {
		t.Errorf("measurement profile %+v", p)
	}
	ref := r.GateReference
	if ref == nil || ref.Status != "non_comparable" || ref.BaseRef != "baseline" ||
		!slices.Equal(ref.Reasons, []string{"no baseline file was loaded"}) {
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

func TestDecodeConformanceFixtures(t *testing.T) {
	names := fixtures(t, "report")
	for _, required := range []string{
		"healthy", "attention-warning-only", "blocked", "missing-required-evidence", "unknown-identity",
		"comparable-profile", "incompatible-profile", "blocker-plus-unevaluated", "non-applicable-rule",
		"locations-empty", "matched-by-populated", "both-repair-kinds", "waiver-expiry-and-order", "invalid-input",
	} {
		if !slices.Contains(names, "report/"+required+".json") {
			t.Errorf("fixture %s is missing", required)
		}
	}
	for _, name := range names {
		t.Run(name, func(t *testing.T) {
			want := loadExpectation(t, name)
			r, err := Decode(loadFixture(t, name))
			if want.Outcome == "error" {
				if err == nil {
					t.Fatal("decoded, want an error")
				}
				return
			}
			if err != nil {
				t.Fatalf("Decode: %v", err)
			}
			checkExpectation(t, r, want)
		})
	}
}

// validFixtures lists every fixture, of both folders, that is expected to decode.
func validFixtures(t testing.TB) []string {
	t.Helper()
	var out []string
	for _, name := range append(fixtures(t, "report"), fixtures(t, "leakage")...) {
		if loadExpectation(t, name).Outcome == "decode" {
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
			payload := loadFixture(t, name)
			first := mustDecode(t, payload)
			marshalled := encode(t, first)

			keys := map[string]bool{}
			collectKeys(parseDoc(t, marshalled), keys)
			for _, dropped := range []string{"matched_by", "declarations", "measurement", "provenance", "denominator", "hypothesis", "scores"} {
				if keys[dropped] {
					t.Errorf("marshalled report has a %q key", dropped)
				}
			}
			if second := mustDecode(t, marshalled); !reflect.DeepEqual(first, second) {
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

	raw, err := os.ReadFile("testdata/archfit.state.schema.json")
	if errors.Is(err, os.ErrNotExist) {
		t.Skip("testdata/archfit.state.schema.json (engine revision f8877d25ba36d84d3780071580d23486e3d794d0) is not vendored yet")
	}
	if err != nil {
		t.Fatal(err)
	}
	w := schemaWalker{t: t, root: parseDoc(t, raw)}
	w.check("report", w.root, reflect.TypeFor[shadowReport]())
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

	_, err := Decode(padded(room + 1))
	wantError(t, err, "MaxBytes")

	// The size check comes before any parsing: garbage over the cap reports the size, not the syntax.
	_, err = Decode(bytes.Repeat([]byte{0xff}, MaxBytes+1))
	wantError(t, err, "MaxBytes")
}

func TestWrongSchemaVersion(t *testing.T) {
	// The envelope's version string is any wire version that is not the state schema's.
	for _, version := range []string{"", "archfit.architecture-state.v2", "archfit.upload-envelope.v1"} {
		t.Run(version, func(t *testing.T) {
			doc := baseDoc(t)
			set(t, doc, version, "schema_version")
			_, err := Decode(encode(t, doc))
			wantError(t, err, "schema_version")
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

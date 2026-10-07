package policy

import (
	"bytes"
	"fmt"
	"strings"
	"testing"
)

func TestDiagnose(t *testing.T) {
	tests := []struct {
		name   string
		policy string
		want   []Diagnostic
	}{
		{
			name:   "valid policy",
			policy: "version: 2\nmodules:\n  a:\n    layer: core\n    paths: [\"pkg/a/**\"]\nrules:\n  - id: r\n    type: cycle\n    gate: warn\n",
		},
		{
			name:   "syntax error keeps the library's line",
			policy: "version: 2\nmodules:\n  a:\n    paths: [x\n  b: {}\nrules: }\n",
			want:   []Diagnostic{{Line: 4, Message: "YAML syntax: did not find expected ',' or ']'. The problem is at line 4 or below it."}},
		},
		{
			name:   "parser error names the start of its block, not the problem line",
			policy: "# header\nversion: 2\nlayers:\n- core\nexclude: []\n- edge\n",
			want:   []Diagnostic{{Line: 2, Message: "YAML syntax: did not find expected key. The problem is at line 2 or below it."}},
		},
		{
			name:   "parser error on the first line",
			policy: "version: !e!x 2\n",
			want:   []Diagnostic{{Line: 1, Message: "YAML syntax: found undefined tag handle. The problem is at line 1 or below it."}},
		},
		{
			name:   "tab indent",
			policy: "version: 2\nmodules:\n\ta: {}\n",
			want:   []Diagnostic{{Line: 3, Message: "YAML syntax: found character that cannot start any token. The problem is at line 3 or below it."}},
		},
		{
			name:   "duplicate key",
			policy: "version: 2\nrules: []\nrules: []\n",
			want:   []Diagnostic{{Line: 3, Message: `YAML syntax: mapping key "rules" already defined at line 2.`}},
		},
		{
			name:   "unknown top-level key with a close match",
			policy: "version: 2\nmodlues: {}\n",
			want:   []Diagnostic{{Line: 2, Column: 1, Message: "Unknown key `modlues`. Did you mean `modules`?"}},
		},
		{
			name:   "unknown nested key with a close match",
			policy: "version: 2\nrules:\n  - id: r\n    type: forbidden_dependency\n    form: a/**\n",
			want:   []Diagnostic{{Line: 5, Column: 5, Message: "Unknown key `form`. Did you mean `from`?"}},
		},
		{
			name:   "unknown key with no close match",
			policy: "version: 2\nreminder: ask Bob\n",
			want:   []Diagnostic{{Line: 2, Column: 1, Message: "Unknown key `reminder`."}},
		},
		{
			name:   "nested key line and column",
			policy: "version: 2\nlanguages:\n  go:\n    modules:\n      include: pkg/**\n",
			want:   []Diagnostic{{Line: 5, Column: 16, Message: "`include` must be a list."}},
		},
		{
			name:   "wrong type: text for a boolean",
			policy: "version: 2\noutputs:\n  json: 1\n",
			want:   []Diagnostic{{Line: 3, Column: 9, Message: "`json` must be `true` or `false`."}},
		},
		{
			name:   "wrong type: list for a mapping",
			policy: "version: 2\nmodules: [a, b]\n",
			want:   []Diagnostic{{Line: 2, Column: 10, Message: "`modules` must be a mapping of keys."}},
		},
		{
			name:   "wrong type: list item",
			policy: "version: 2\nrules:\n  - cycle\n",
			want:   []Diagnostic{{Line: 3, Column: 5, Message: "Each item must be a mapping of keys."}},
		},
		{
			name:   "wrong type: text for an integer",
			policy: "version: 2\nrules:\n  - id: r\n    type: public_api_max\n    max: many\n",
			want:   []Diagnostic{{Line: 5, Column: 10, Message: "`max` must be a whole number."}},
		},
		{
			name:   "quoted number",
			policy: "version: 2\nmetrics:\n  function_loc_threshold: \"60\"\n",
			want:   []Diagnostic{{Line: 3, Column: 27, Message: "`function_loc_threshold` must be a whole number without quotes."}},
		},
		{
			name:   "a mapping or a list where a const or enum is expected",
			policy: "version: 2\nlanguages:\n  go:\n    enabled: {}\n    gate: [warn]\n",
			want: []Diagnostic{
				{Line: 4, Column: 14, Message: "`enabled` must be `true` or `false` or `auto`."},
				{Line: 5, Column: 11, Message: "`gate` must be one of `off`, `warn`, `fail`."},
			},
		},
		{
			name:   "a null required value is missing",
			policy: "version: ~\nwaivers:\n  - rule: ~\n    reason: x\n    approved_by: me\n    expires: 2026-10-20\n",
			want: []Diagnostic{
				{Line: 1, Column: 10, Message: "`version` needs a value."},
				{Line: 3, Column: 11, Message: "`rule` needs a value."},
			},
		},
		{
			name:   "minimum reads YAML integer spellings",
			policy: "version: 2\nmetrics:\n  function_loc_threshold: -0x1\ncoverage:\n  sources:\n    - {path: a, max_bytes: -1_0}\n    - {path: b, max_facts: -0o7}\n",
			want: []Diagnostic{
				{Line: 3, Column: 27, Message: "`function_loc_threshold` must be at least 1."},
				{Line: 6, Column: 28, Message: "`max_bytes` must be at least 1."},
				{Line: 7, Column: 28, Message: "`max_facts` must be at least 1."},
			},
		},
		{
			name:   "minLength names its bound",
			policy: "version: 2\nwaivers:\n  - rule: r\n    reason: \"\"\n    approved_by: me\n    expires: 2026-10-20\n",
			want:   []Diagnostic{{Line: 4, Column: 13, Message: "`reason` must be at least 1 character long."}},
		},
		{
			name:   "a problem in an aliased value is reported once per use",
			policy: "version: 2\nx-anchors: &bad {json: 1}\noutputs: *bad\n",
			want: []Diagnostic{
				{Line: 2, Column: 1, Message: "Unknown key `x-anchors`."},
				{Line: 3, Column: 10, Message: "`json` must be `true` or `false`."},
			},
		},
		{
			name:   "an alias used twice is reported at each use",
			policy: "version: 2\nlanguages:\n  go: {gate: &g maybe}\n  python: {gate: *g}\n  rust: {gate: *g}\n",
			want: []Diagnostic{
				{Line: 3, Column: 14, Message: "`gate` must be one of `off`, `warn`, `fail`."},
				{Line: 4, Column: 18, Message: "`gate` must be one of `off`, `warn`, `fail`."},
				{Line: 5, Column: 16, Message: "`gate` must be one of `off`, `warn`, `fail`."},
			},
		},
		{
			name:   "merged keys are checked against the merge site",
			policy: "version: 2\nmodules:\n  a: &d {layer: core}\nlanguages:\n  go:\n    <<: *d\n",
			want:   []Diagnostic{{Line: 6, Column: 5, Message: "Unknown key `layer`."}},
		},
		{
			name:   "a binary value",
			policy: "version: 2\nexclude:\n  - !!binary aGVsbG8=\n",
			want:   []Diagnostic{{Line: 3, Message: "YAML syntax: a !!binary value, which no engine key takes."}},
		},
		{
			name:   "NEL, LS and PS line breaks",
			policy: "version: 2\n# note\u2028here\n",
			want:   []Diagnostic{{Message: "The policy has a NEL, LS or PS line break. Use ordinary line breaks."}},
		},
		{
			name:   "wrong enum value with a close match",
			policy: "version: 2\nrules:\n  - id: r\n    type: cycle\n    gate: fial\n",
			want:   []Diagnostic{{Line: 5, Column: 11, Message: "`gate` must be one of `off`, `warn`, `fail`. Did you mean `fail`?"}},
		},
		{
			name:   "misspelled rule type",
			policy: "version: 2\nrules:\n  - id: r\n    type: forbiden_dependency\n",
			want: []Diagnostic{{Line: 4, Column: 11, Message: "`type` must be one of `cycle`, `forbidden_dependency`, " +
				"`forbidden_layer_direction`, `forbidden_pattern`, `internal_api_access`, `module_cycle`, " +
				"`module_dependencies`, `new_cross_module_dependency`, `public_api_change`, `public_api_max`, " +
				"`public_api_only`, `public_api_type_leak`. Did you mean `forbidden_dependency`?"}},
		},
		{
			name: "module allowlists and the module_dependencies rule",
			policy: "version: 2\nmodules:\n  a:\n    paths: [\"pkg/a/**\"]\n    depends_on: [b, \"layer:core\", \"role:adapter\", \"svc-*\"]\n" +
				"  b:\n    paths: [\"pkg/b/**\"]\n    visible_to: []\nrules:\n  - id: r\n    type: module_dependencies\n    gate: fail\n",
		},
		{
			// Whether an allowlist entry selects a declared module is the engine's check (config lint unknown_module).
			name:   "an allowlist entry that names no declared module",
			policy: "version: 2\nmodules:\n  a:\n    depends_on: [no_such_module]\n    visible_to: [nor_this_one]\n",
		},
		{
			name: "module selectors and rule rationale",
			policy: "version: 2\nrules:\n  - id: r\n    type: forbidden_dependency\n    from_module: \"layer:domain\"\n" +
				"    to_module: \"role:adapter\"\n    rationale: the domain stays pure\n    alternatives: [use a port]\n" +
				"    docs: docs/adr/0007.md\n    gate: fail\n",
		},
		{
			name:   "allowlist given as text",
			policy: "version: 2\nmodules:\n  a:\n    depends_on: b\n    visible_to: {b: true}\n",
			want: []Diagnostic{
				{Line: 4, Column: 17, Message: "`depends_on` must be a list."},
				{Line: 5, Column: 17, Message: "`visible_to` must be a list."},
			},
		},
		{
			name:   "allowlist entry that is not text",
			policy: "version: 2\nmodules:\n  a:\n    depends_on: [[b]]\n",
			want:   []Diagnostic{{Line: 4, Column: 18, Message: "Each item must be text."}},
		},
		{
			name:   "rule rationale keys of the wrong type",
			policy: "version: 2\nrules:\n  - id: r\n    type: cycle\n    alternatives: use a port\n    rationale: [a, b]\n    from_module: {x: 1}\n",
			want: []Diagnostic{
				{Line: 5, Column: 19, Message: "`alternatives` must be a list."},
				{Line: 6, Column: 16, Message: "`rationale` must be text."},
				{Line: 7, Column: 18, Message: "`from_module` must be text."},
			},
		},
		{
			name:   "misspelled allowlist keys",
			policy: "version: 2\nmodules:\n  a:\n    depend_on: [b]\n    visibile_to: [b]\n",
			want: []Diagnostic{
				{Line: 4, Column: 5, Message: "Unknown key `depend_on`. Did you mean `depends_on`?"},
				{Line: 5, Column: 5, Message: "Unknown key `visibile_to`. Did you mean `visible_to`?"},
			},
		},
		{
			name:   "misspelled rule keys",
			policy: "version: 2\nrules:\n  - id: r\n    type: forbidden_dependency\n    from_modul: \"layer:x\"\n    rationalle: why\n",
			want: []Diagnostic{
				{Line: 5, Column: 5, Message: "Unknown key `from_modul`. Did you mean `from_module`?"},
				{Line: 6, Column: 5, Message: "Unknown key `rationalle`. Did you mean `rationale`?"},
			},
		},
		{
			name:   "misspelled module_dependencies",
			policy: "version: 2\nrules:\n  - id: r\n    type: module_dependecies\n",
			want: []Diagnostic{{Line: 4, Column: 11, Message: "`type` must be one of `cycle`, `forbidden_dependency`, " +
				"`forbidden_layer_direction`, `forbidden_pattern`, `internal_api_access`, `module_cycle`, " +
				"`module_dependencies`, `new_cross_module_dependency`, `public_api_change`, `public_api_max`, " +
				"`public_api_only`, `public_api_type_leak`. Did you mean `module_dependencies`?"}},
		},
		{
			name:   "wrong enum value without a close match",
			policy: "version: 2\ncoupling:\n  duplicated_knowledge: ignore\n",
			want:   []Diagnostic{{Line: 3, Column: 25, Message: "`duplicated_knowledge` must be one of `score`, `advisory`."}},
		},
		{
			name:   "wrong version",
			policy: "version: 1\n",
			want:   []Diagnostic{{Line: 1, Column: 10, Message: "`version` must be `2`."}},
		},
		{
			name:   "neither boolean nor auto",
			policy: "version: 2\nlanguages:\n  go:\n    enabled: yes\n",
			want:   []Diagnostic{{Line: 4, Column: 14, Message: "`enabled` must be `true` or `false` or `auto`."}},
		},
		{
			name:   "missing required top-level key",
			policy: "modules: {}\n",
			want:   []Diagnostic{{Line: 1, Column: 1, Message: "Missing required key `version`."}},
		},
		{
			name:   "missing required nested key",
			policy: "version: 2\nrules:\n  - id: r\n    gate: warn\n",
			want:   []Diagnostic{{Line: 3, Column: 5, Message: "Missing required key `type`."}},
		},
		{
			name:   "empty text",
			policy: "# nothing yet\n",
			want:   []Diagnostic{{Message: "Missing required key `version`."}},
		},
		{
			name:   "minimum, minItems and date format",
			policy: "version: 2\nmetrics:\n  function_loc_threshold: 0\nexternal_systems:\n  pay:\n    targets: []\nwaivers:\n  - rule: r\n    reason: x\n    approved_by: me\n    expires: 2026-13-01\n",
			want: []Diagnostic{
				{Line: 3, Column: 27, Message: "`function_loc_threshold` must be at least 1."},
				{Line: 6, Column: 14, Message: "`targets` must list at least 1 item."},
				{Line: 11, Column: 14, Message: "`expires` must be a date written YYYY-MM-DD."},
			},
		},
		{
			name:   "metric entries follow the pattern schema",
			policy: "version: 2\nmetrics:\n  cycle:\n    gate: maybe\n",
			want:   []Diagnostic{{Line: 4, Column: 11, Message: "`gate` must be one of `off`, `warn`, `fail`."}},
		},
		{
			name:   "problems are listed in line order",
			policy: "version: 2\nrules:\n  - id: r\n    gate: never\noutputs: {sarif: maybe}\n",
			want: []Diagnostic{
				{Line: 3, Column: 5, Message: "Missing required key `type`."},
				{Line: 4, Column: 11, Message: "`gate` must be one of `off`, `warn`, `fail`."},
				{Line: 5, Column: 18, Message: "`sarif` must be `true` or `false`."},
			},
		},
		{
			name:   "merge key at the top level",
			policy: "version: 2\n<<: {rules: []}\n",
			want:   []Diagnostic{{Line: 2, Column: 1, Message: "The top level cannot use a merge key `<<`. Write the keys out."}},
		},
		{
			name:   "root is not a mapping",
			policy: "Sure! Here is the file\n",
			want:   []Diagnostic{{Line: 1, Message: "The top level must be a mapping of keys, such as `version: 2`."}},
		},
		{
			name:   "a quoted key is bounded",
			policy: "version: 2\n\"" + strings.Repeat("k", 100) + "\\n\": 1\n",
			want:   []Diagnostic{{Line: 2, Column: 1, Message: "Unknown key `" + strings.Repeat("k", 63) + "…`."}},
		},
		{
			name:   "a reader-only rule without a key",
			policy: "version: 2\nmodules:\n  " + strings.Repeat("m", 201) + ": {}\n",
			want:   []Diagnostic{{Message: "The App cannot read this policy: modules[0] (line 3): name is longer than 200 characters."}},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := Diagnose([]byte(tt.policy))
			if len(got) != len(tt.want) {
				t.Fatalf("Diagnose = %+v\nwant %+v", got, tt.want)
			}
			for i := range got {
				if got[i] != tt.want[i] {
					t.Errorf("diagnostic %d = %+v\nwant          %+v", i, got[i], tt.want[i])
				}
			}
		})
	}
}

// The engine's own YAML decoder is the reference: each row here is a text it reads without complaint.
func TestDiagnoseReadsScalarsLikeTheEngine(t *testing.T) {
	for name, policy := range map[string]string{
		"number for text":         "version: 2\nmodules:\n  a:\n    owner: 123\n",
		"date for date-time":      "version: 2\nmodules:\n  a:\n    reviewed_at: 2026-01-01\n",
		"unquoted waiver date":    "version: 2\nwaivers:\n  - rule: r\n    reason: x\n    approved_by: me\n    expires: 2026-10-20\n",
		"null value":              "version: 2\nmodules:\nlanguages:\n  go:\n    gate:\n    enabled: ~\ncoupling:\n  duplicated_knowledge: ~\n",
		"auto":                    "version: 2\nanalyzers:\n  scip:\n    enabled: auto\n",
		"merged required keys":    "version: 2\nrules:\n  - &base {id: r, type: cycle}\n  - <<: *base\n    gate: warn\n",
		"merge below a module":    "version: 2\nmodules:\n  a: &d\n    layer: core\n  b:\n    <<: *d\n    paths: [\"pkg/b/**\"]\n",
		"alias for a value":       "version: 2\nlayers: &l [core]\nexclude: *l\n",
		"tagged and block scalar": "version: 2\nexclude:\n  - !!str 123\n  - |\n    block text\n",
	} {
		t.Run(name, func(t *testing.T) {
			if got := Diagnose([]byte(policy)); got != nil {
				t.Errorf("Diagnose = %+v, want nil", got)
			}
		})
	}
}

// engineSelfPolicy is the engine repository's own .archfit.yaml at archfit commit 2d20da8, copied unchanged.
const engineSelfPolicy = "testdata/policy/engine-self.archfit.yaml"

func TestDiagnoseAcceptsRealPolicies(t *testing.T) {
	for _, path := range append(policyPaths(t), engineSelfPolicy) {
		t.Run(path, func(t *testing.T) {
			got := Diagnose(readFile(t, path))
			if strings.HasSuffix(path, "malformed-expiry.yaml") {
				if len(got) != 1 || !strings.Contains(got[0].Message, "`expires` must be a date") || got[0].Line == 0 {
					t.Errorf("Diagnose = %+v, want one positioned expires diagnostic", got)
				}
				return
			}
			if got != nil {
				t.Errorf("Diagnose = %+v, want nil", got)
			}
		})
	}
}

func TestDiagnoseRefusesWhatValidateRefuses(t *testing.T) {
	for name, tt := range foreignDocuments(t) {
		t.Run(name, func(t *testing.T) {
			if got := Diagnose(tt.policy); len(got) == 0 {
				t.Error("Diagnose = nil for a text Validate refuses")
			}
		})
	}
}

func TestDiagnoseIsBounded(t *testing.T) {
	var b strings.Builder
	b.WriteString("version: 2\nrules:\n")
	for range 3 * maxDiagnostics {
		b.WriteString("  - {id: r, type: cycle, gate: never}\n")
	}
	got := Diagnose([]byte(b.String()))
	if len(got) != maxDiagnostics+1 {
		t.Fatalf("got %d diagnostics, want %d and a closing note", len(got), maxDiagnostics)
	}
	if last := got[len(got)-1]; last.Line != 0 || !strings.Contains(last.Message, "More problems") {
		t.Errorf("last diagnostic = %+v", last)
	}
}

// A re-pinned schema with a keyword the walker does not know must fail closed, never check less.
func TestDiagnoseFailsClosedOnAnUnknownSchemaKeyword(t *testing.T) {
	if _, err := compileSchema(Schema()); err != nil {
		t.Fatalf("the pinned schema does not compile: %v", err)
	}
	for name, schema := range map[string]string{
		"unknown keyword":                    `{"properties": {"version": {"type": "integer", "maximum": 3}}}`,
		"type list":                          `{"properties": {"version": {"type": ["integer", "null"]}}}`,
		"unresolved ref":                     `{"properties": {"version": {"$ref": "#/$defs/Missing"}}}`,
		"unsupported pattern":                `{"patternProperties": {"^(?=a)": {}}}`,
		"unknown format":                     `{"properties": {"at": {"type": "string", "format": "email"}}}`,
		"keyword beside $ref":                `{"$defs": {"N": {"type": "integer"}}, "properties": {"x": {"$ref": "#/$defs/N", "minimum": 1}}}`,
		"keyword beside oneOf":               `{"properties": {"x": {"oneOf": [{"type": "boolean"}], "enum": ["auto"]}}}`,
		"reference cycle":                    `{"$defs": {"A": {"$ref": "#/$defs/B"}, "B": {"$ref": "#/$defs/A"}}, "properties": {"x": {"$ref": "#/$defs/A"}}}`,
		"property also matched by a pattern": `{"properties": {"ab": {}}, "patternProperties": {"^a": {}}}`,
		"not JSON":                           `version: 2`,
	} {
		t.Run(name, func(t *testing.T) {
			if _, err := compileSchema([]byte(schema)); err == nil {
				t.Error("compileSchema = nil error, want an error")
			}
		})
	}
}

func TestSchemaReferenceChainsResolve(t *testing.T) {
	root, err := compileSchema([]byte(`{"$defs": {"A": {"$ref": "#/$defs/B"}, "B": {"type": "integer"}}, "properties": {"x": {"$ref": "#/$defs/A"}}}`))
	if err != nil {
		t.Fatal(err)
	}
	if got := root.Properties["x"].resolved(); got != root.Defs["B"] {
		t.Errorf("x resolves to %+v, want $defs.B", got)
	}
}

func TestSuggest(t *testing.T) {
	keys := []string{"exclude", "external_systems", "modules", "rules", "version"}
	tests := []struct{ got, want string }{
		{"modlues", "modules"},
		{"rule", "rules"},
		{"verison", "version"},
		{"excludes", "exclude"},
		{"x", ""},
		{"owners", ""},
		{"forbiden_dependency", ""},
	}
	for _, tt := range tests {
		if got := suggest(tt.got, keys); got != tt.want {
			t.Errorf("suggest(%q) = %q, want %q", tt.got, got, tt.want)
		}
	}
	if got := suggest("forbiden_dependency", []string{"forbidden_dependency", "cycle"}); got != "forbidden_dependency" {
		t.Errorf("suggest(forbiden_dependency) = %q", got)
	}
}

func FuzzDiagnose(f *testing.F) {
	for _, policy := range policyFixtures(f) {
		f.Add(policy)
	}
	f.Add([]byte("version: 2\nmodules: {a: [\n"))
	f.Fuzz(func(t *testing.T, policy []byte) {
		got := Diagnose(policy)
		lines := bytes.Count(policy, []byte("\n")) + bytes.Count(policy, []byte("\r")) + 1
		for _, d := range got {
			if d.Line < 0 || d.Line > lines || d.Message == "" {
				t.Fatalf("diagnostic %+v out of range for %d lines", d, lines)
			}
		}
		if got != nil {
			return
		}
		if err := Validate(policy); err != nil {
			t.Errorf("Diagnose = nil, Validate = %v", err)
		}
		if _, err := Waivers(policy); err != nil {
			t.Errorf("Diagnose = nil, Waivers = %v", err)
		}
		if _, err := Modules(policy); err != nil {
			t.Errorf("Diagnose = nil, Modules = %v", err)
		}
	})
}

// Suggestions are computed only for problems that are listed: past the cap, an unknown key costs no edit distance.
func TestDiagnoseSuggestsOnlyForListedProblems(t *testing.T) {
	var b strings.Builder
	b.WriteString("version: 2\n")
	for i := range 3000 {
		fmt.Fprintf(&b, "%s%d: 1\n", strings.Repeat("x", 58), i)
	}
	policy := []byte(b.String())
	allocs := testing.AllocsPerRun(1, func() { Diagnose(policy) })
	// One edit distance allocates a matrix row per rune; 3000 keys against 16 names would be millions.
	if allocs > 1_000_000 {
		t.Errorf("Diagnose allocated %.0f times", allocs)
	}
}

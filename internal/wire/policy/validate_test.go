package policy

import (
	"bytes"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func readFile(t *testing.T, path string) []byte {
	t.Helper()
	raw, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read %s: %v", path, err)
	}
	return raw
}

func TestValidateAcceptsRealPolicies(t *testing.T) {
	for _, path := range policyPaths(t) {
		t.Run(path, func(t *testing.T) {
			if err := Validate(readFile(t, path)); err != nil {
				t.Errorf("Validate = %v, want nil", err)
			}
		})
	}

	t.Run("structural only", func(t *testing.T) {
		raw := fixture(t, malformedExpiryPolicy)
		if _, err := Waivers(raw); err == nil {
			t.Fatal("fixture no longer has a malformed expiry")
		}
		if err := Validate(raw); err != nil {
			t.Errorf("Validate = %v, want nil: the engine judges values", err)
		}
	})

	for name, policy := range map[string]string{
		"version only":       "version: 1\n",
		"null version":       "version:\n",
		"every schema key":   "version: 1\nexclude: []\nlanguages: {}\nanalyzers: {}\ncoverage: {}\nai: {}\ncoupling: {}\nlayers: {}\nmodules: {}\nexternal_systems: {}\nrules: {}\nwaivers: []\nmetrics: {}\nmodule_review: {}\nfile_class: {}\noutputs: {}\n",
		"comments and CRLF":  "# policy\r\nversion: 1\r\nrules: {}\r\n",
		"JSON spelling":      `{"version": 1, "modules": {}}`,
		"alias below a key":  "version: 1\nlayers: &l {}\nmodules: *l\n",
		"explicit start":     "---\nversion: 1\n",
		"quoted version key": "\"version\": 1\n",
	} {
		t.Run(name, func(t *testing.T) {
			if err := Validate([]byte(policy)); err != nil {
				t.Errorf("Validate = %v, want nil", err)
			}
		})
	}
}

type foreignDocument struct {
	policy []byte
	want   string // a part of Validate's error
}

// foreignDocuments are texts Validate refuses; Diagnose must refuse each of them too.
func foreignDocuments(t *testing.T) map[string]foreignDocument {
	t.Helper()
	return map[string]foreignDocument{
		"engine state report":   {readFile(t, filepath.Join(sharedReportDir, "real-healthy.json")), "key 1 is not"},
		"labels file":           {fixture(t, "report/real-baselined-with-labels.labels.yaml"), "is not an engine config key"},
		"prose with a colon":    {[]byte("Sure! Here is the file: done"), "key 1 is not"},
		"prose without a colon": {[]byte("Sure! Here is the file"), errRootNotMapping.Error()},
		"scalar root":           {[]byte("42\n"), errRootNotMapping.Error()},
		"sequence root":         {[]byte("- version: 1\n"), errRootNotMapping.Error()},
		"no version":            {[]byte("modules: {}\nrules: {}\n"), `required top-level key "version"`},
		"empty mapping":         {[]byte("{}\n"), `required top-level key "version"`},
		"one unknown key":       {[]byte("version: 1\nmodules: {}\nextra: 1\n"), "key 3 is not"},
		"unknown key first":     {[]byte("extra: 1\nversion: 1\n"), "key 1 is not"},
		"wrong case":            {[]byte("Version: 1\n"), "key 1 is not"},
		"non-scalar key":        {[]byte("version: 1\n? [a, b]\n: 1\n"), errSyntax.Error()},
		"integer key":           {[]byte("version: 1\n1: a\n"), "key 2 is not"},
		"null key":              {[]byte("version: 1\n~: a\n"), "key 2 is not"},
		"merge key":             {[]byte("version: 1\n<<: {rules: {}}\n"), "key 2 is not"},
		"version only merged":   {[]byte("<<: {version: 1}\n"), "key 1 is not"},
		"empty":                 {nil, errEmpty.Error()},
		"whitespace only":       {[]byte(" \n\n"), errEmpty.Error()},
		"comment only":          {[]byte("# version: 1\n"), errEmpty.Error()},
		"duplicate key":         {[]byte("version: 1\nrules: {}\nrules: {}\n"), errSyntax.Error()},
		"duplicate version":     {[]byte("version: 1\nversion: 2\n"), errSyntax.Error()},
		"two documents":         {[]byte("version: 1\n---\nversion: 1\n"), errNotOneDocument.Error()},
		"truncated YAML":        {[]byte("version: 1\nmodules: {a: [\n"), errSyntax.Error()},
		"invalid UTF-8":         {[]byte("version: 1\nrules: \xff\n"), errNotUTF8.Error()},
		"too large":             {append([]byte("version: 1\n#"), bytes.Repeat([]byte("x"), maxInputBytes)...), errTooLarge.Error()},
		"alias bomb":            {[]byte("version: 1\n" + aliasBomb()), errTooComplex.Error()},
	}
}

func TestValidateRejectsForeignDocuments(t *testing.T) {
	for name, tt := range foreignDocuments(t) {
		t.Run(name, func(t *testing.T) {
			err := Validate(tt.policy)
			if err == nil {
				t.Fatal("Validate = nil, want an error")
			}
			if !strings.Contains(err.Error(), tt.want) {
				t.Errorf("Validate = %q, want it to contain %q", err, tt.want)
			}
		})
	}
}

func TestValidateKeySetComesFromSchema(t *testing.T) {
	var root map[string]json.RawMessage
	if err := json.Unmarshal(Schema(), &root); err != nil {
		t.Fatal(err)
	}
	if got := string(root["additionalProperties"]); got != "false" {
		t.Fatalf("schema additionalProperties = %s: the top-level key set is no longer closed", got)
	}
	var properties map[string]json.RawMessage
	if err := json.Unmarshal(root["properties"], &properties); err != nil {
		t.Fatal(err)
	}

	policy := []byte("version: 1\nnew_engine_key: {}\n")
	pinned, err := schemaKeySet(Schema())
	if err != nil {
		t.Fatalf("schemaKeySet(pinned): %v", err)
	}
	if err := pinned.check(policy); err == nil {
		t.Fatal("the pinned schema accepts new_engine_key")
	}
	if len(pinned.allowed) != len(properties) {
		t.Errorf("allowed has %d keys, schema has %d properties", len(pinned.allowed), len(properties))
	}

	properties["new_engine_key"] = json.RawMessage(`{"type": "object"}`)
	root["properties"], _ = json.Marshal(properties)
	root["required"] = json.RawMessage(`["version", "new_engine_key"]`)
	grown, _ := json.Marshal(root)
	next, err := schemaKeySet(grown)
	if err != nil {
		t.Fatalf("schemaKeySet(grown): %v", err)
	}
	if err := next.check(policy); err != nil {
		t.Errorf("grown schema: check = %v, want nil", err)
	}
	err = next.check([]byte("version: 1\n"))
	if err == nil || !strings.Contains(err.Error(), `"new_engine_key"`) {
		t.Errorf("grown schema: check = %v, want the new required key named", err)
	}

	for name, schema := range map[string]string{
		"not JSON":           "version: 1",
		"open key set":       `{"properties": {"version": {}}, "required": ["version"]}`,
		"additional allowed": `{"properties": {"version": {}}, "additionalProperties": true}`,
		"no properties":      `{"additionalProperties": false}`,
	} {
		t.Run(name, func(t *testing.T) {
			if _, err := schemaKeySet([]byte(schema)); err == nil {
				t.Error("schemaKeySet = nil error, want an error")
			}
		})
	}
}

func TestValidateErrorsEchoNothing(t *testing.T) {
	const marker = "LEAK-7f3a"
	report := readFile(t, filepath.Join(sharedReportDir, "real-healthy.json"))
	var reportKeys map[string]json.RawMessage
	if err := json.Unmarshal(report, &reportKeys); err != nil {
		t.Fatal(err)
	}

	tests := map[string][]byte{
		"unknown key and value": []byte("version: 1\n" + marker + ": " + marker + "\n"),
		"duplicate key":         []byte("version: 1\n" + marker + ": 1\n" + marker + ": 2\n"),
		"no version":            []byte("rules: {" + marker + ": " + marker + "}\n"),
		"scalar root":           []byte(marker + "\n"),
		"prose":                 []byte("Sure! Here is " + marker + ": " + marker),
		"syntax error":          []byte("version: 1\n" + marker + ": [" + marker + "\n"),
		"anchor and alias":      []byte("version: &" + marker + " 1\n" + marker + ": *" + marker + "\n"),
		"unknown alias":         []byte("version: *" + marker + "\n"),
		"report":                report,
	}
	for name, policy := range tests {
		t.Run(name, func(t *testing.T) {
			err := Validate(policy)
			if err == nil {
				t.Fatal("Validate = nil, want an error")
			}
			if strings.Contains(err.Error(), marker) {
				t.Errorf("error echoes the marker: %q", err)
			}
			for key := range reportKeys {
				if strings.Contains(err.Error(), key) {
					t.Errorf("error echoes the report key %q: %q", key, err)
				}
			}
		})
	}
}

func FuzzValidate(f *testing.F) {
	for _, policy := range policyFixtures(f) {
		f.Add(policy)
	}
	f.Add([]byte("Sure! Here is the file: done"))
	f.Fuzz(func(t *testing.T, policy []byte) {
		if Validate(policy) != nil {
			return
		}
		if _, _, err := read(policy); err != nil {
			t.Errorf("Validate accepts what the common reader rejects: %v", err)
		}
	})
}

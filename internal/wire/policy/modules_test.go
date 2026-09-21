package policy

import (
	"bytes"
	"fmt"
	"reflect"
	"strings"
	"testing"
)

func manyModules(n int) string {
	var b strings.Builder
	b.WriteString("modules:\n")
	for i := range n {
		fmt.Fprintf(&b, "  m%d: {}\n", i)
	}
	return b.String()
}

func TestModulesShapes(t *testing.T) {
	twoModules := "version: 2\nmodules:\n  api:\n    paths: [api/**]\n    layer: app\n  db:\n    paths: [db/**]\n"
	tests := []struct {
		name    string
		in      string
		want    []ModuleDef
		wantErr bool
	}{
		{name: "no modules key", in: "version: 2\n"},
		{name: "modules null", in: "modules: null\n"},
		{name: "modules empty map", in: "modules: {}\n"},
		{name: "modules is a list", in: "modules: [a, b]\n", wantErr: true},
		{name: "modules is a string", in: "modules: none\n", wantErr: true},
		{name: "entry value is a string", in: "modules:\n  a: pkg/a\n", wantErr: true},
		{name: "entry value is null", in: "modules:\n  a:\n", wantErr: true},
		{name: "layer is a list", in: "modules:\n  a:\n    layer: [app]\n", wantErr: true},
		{name: "layer is null", in: "modules:\n  a:\n    layer:\n", want: []ModuleDef{{"a", "", 2}}},
		{
			name: "with and without layer",
			in:   twoModules,
			want: []ModuleDef{{"api", "app", 3}, {"db", "", 6}},
		},
		{
			name: "comments and blank lines",
			in:   "# policy\n\nmodules:\n\n  # first\n  api:\n    layer: app\n\n  # second\n\n  db: {}\n",
			want: []ModuleDef{{"api", "app", 6}, {"db", "", 11}},
		},
		{
			name: "CRLF line endings",
			in:   strings.ReplaceAll(twoModules, "\n", "\r\n"),
			want: []ModuleDef{{"api", "app", 3}, {"db", "", 6}},
		},
		{
			name: "BOM",
			in:   "\ufeff" + twoModules,
			want: []ModuleDef{{"api", "app", 3}, {"db", "", 6}},
		},
		{
			name: "names as written",
			in:   "modules:\n  \"pkg/a b\": {}\n  42: {}\n  'Ünï': {layer: 3}\n",
			want: []ModuleDef{{"pkg/a b", "", 2}, {"42", "", 3}, {"Ünï", "3", 4}},
		},
		{
			name: "flow mapping",
			in:   "modules: {a: {layer: x}, b: {}}\n",
			want: []ModuleDef{{"a", "x", 1}, {"b", "", 1}},
		},
		{name: "duplicate name", in: "modules:\n  a: {}\n  a: {}\n", wantErr: true},
		{
			name: "alias value keeps the line of the key",
			in:   "defaults: &d\n  layer: app\nmodules:\n  a: *d\n  b:\n    <<: *d\n    paths: [b/**]\n",
			want: []ModuleDef{{"a", "app", 4}, {"b", "app", 5}},
		},
		{
			name: "modules is an alias",
			in:   "base: &m\n  a: {}\nmodules: *m\n",
			want: []ModuleDef{{"a", "", 2}},
		},
		{
			name: "merge key under modules, explicit keys win",
			in:   "base: &m\n  a: {layer: old}\n  c: {}\nmodules:\n  <<: *m\n  a: {layer: new}\n",
			want: []ModuleDef{{"a", "new", 6}, {"c", "", 3}},
		},
		{name: "merge of itself", in: "modules: &m\n  <<: *m\n", wantErr: true},
		{name: "lone CR line breaks", in: "modules:\r  a: {}\r", wantErr: true},
		{name: "NEL line break", in: "note: \"x\u0085y\"\nmodules:\n  a: {}\n", wantErr: true},
		{name: "name of 200 characters", in: "modules:\n  " + strings.Repeat("é", 200) + ": {}\n", want: []ModuleDef{{strings.Repeat("é", 200), "", 2}}},
		{name: "name of 201 characters", in: "modules:\n  " + strings.Repeat("é", 201) + ": {}\n", wantErr: true},
		{name: "5001 modules", in: manyModules(5001), wantErr: true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := Modules([]byte(tt.in))
			if (err != nil) != tt.wantErr {
				t.Fatalf("error = %v, wantErr %v", err, tt.wantErr)
			}
			if err != nil && got != nil {
				t.Errorf("got %v with an error, want nil", got)
			}
			if !reflect.DeepEqual(got, tt.want) {
				t.Errorf("got %+v, want %+v", got, tt.want)
			}
		})
	}

	t.Run("5000 modules", func(t *testing.T) {
		got := mustModules(t, []byte(manyModules(5000)))
		if len(got) != 5000 || got[4999] != (ModuleDef{"m4999", "", 5001}) {
			t.Errorf("got %d modules, last %+v", len(got), got[len(got)-1])
		}
	})
}

func TestModulesOnEnginePolicy(t *testing.T) {
	t.Run("real capture", func(t *testing.T) {
		got := mustModules(t, fixture(t, realCapturePolicy))
		want := []ModuleDef{{"a", "", 3}, {"b", "", 6}}
		if !reflect.DeepEqual(got, want) {
			t.Errorf("got %+v, want %+v", got, want)
		}
	})

	t.Run("full policy", func(t *testing.T) {
		var expect struct {
			Fields struct {
				Modules []struct {
					Name  string `json:"name"`
					Layer string `json:"layer"`
					Line  int    `json:"line"`
				} `json:"modules"`
			} `json:"fields"`
		}
		fixtureJSON(t, fullExpectation, &expect)
		var want []ModuleDef
		for _, m := range expect.Fields.Modules {
			want = append(want, ModuleDef{m.Name, m.Layer, m.Line})
		}
		got := mustModules(t, fixture(t, fullPolicy))
		if len(want) == 0 || !reflect.DeepEqual(got, want) {
			t.Errorf("got %+v, want %+v", got, want)
		}
	})
}

func TestGraphJumpsToTheModuleLine(t *testing.T) {
	for _, policy := range policyFixtures(t) {
		lines := strings.Split(string(policy), "\n")
		for _, m := range mustModules(t, policy) {
			if m.Line < 1 || m.Line > len(lines) {
				t.Fatalf("module %q: line %d outside the text", m.Name, m.Line)
			}
			if line := strings.TrimSpace(lines[m.Line-1]); !strings.HasPrefix(line, m.Name+":") {
				t.Errorf("module %q: line %d is %q", m.Name, m.Line, line)
			}
		}
	}
}

func FuzzModules(f *testing.F) {
	for _, policy := range policyFixtures(f) {
		f.Add(policy)
	}
	f.Add([]byte("base: &m\n  a: {layer: old}\nmodules:\n  <<: *m\n  b: *m\n"))
	f.Add([]byte("modules: &m\n  <<: *m\n"))
	f.Add([]byte("modules:\r  a: {}\r"))
	f.Fuzz(func(t *testing.T, policy []byte) {
		mods, err := Modules(policy)
		if err != nil && mods != nil {
			t.Fatalf("error %v with modules %v", err, mods)
		}
		lines := bytes.Count(policy, []byte("\n")) + 1
		names := map[string]bool{}
		for _, m := range mods {
			if m.Line < 1 || m.Line > lines {
				t.Fatalf("module %q: line %d, input has %d lines", m.Name, m.Line, lines)
			}
			if names[m.Name] {
				t.Fatalf("duplicate module name %q", m.Name)
			}
			names[m.Name] = true
		}
	})
}

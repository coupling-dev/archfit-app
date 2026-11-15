package result

import (
	"encoding/json"
	"maps"
	"os"
	"reflect"
	"slices"
	"strings"
	"testing"
)

// property is what the schema says of one key.
type property struct {
	Type      string   `json:"type"`
	Enum      []string `json:"enum"`
	Pattern   string   `json:"pattern"`
	Ref       string   `json:"$ref"`
	Minimum   *int     `json:"minimum"`
	Maximum   int      `json:"maximum"`
	MinLength int      `json:"minLength"`
	Items     *struct {
		Ref string `json:"$ref"`
	} `json:"items"`
}

type definition struct {
	Properties           map[string]property `json:"properties"`
	Required             []string            `json:"required"`
	AdditionalProperties *bool               `json:"additionalProperties"`
}

const defsPrefix = "#/$defs/"

// TestTypesFollowTheVendoredSchema ties the Go types and the decoder's rules to the vendored schema: for every
// object Result reaches, the same keys, the same required keys, the same JSON types, the same enums, patterns,
// minLength and maximum, and minimum 1 on every integer. A re-vendored schema that changes the result fails here until the types follow.
func TestTypesFollowTheVendoredSchema(t *testing.T) {
	raw, err := os.ReadFile("archfit.result.schema.json")
	if err != nil {
		t.Fatal(err)
	}
	var schema struct {
		OneOf []struct {
			Ref string `json:"$ref"`
		} `json:"oneOf"`
		Defs map[string]definition `json:"$defs"`
	}
	if err := json.Unmarshal(raw, &schema); err != nil {
		t.Fatal(err)
	}
	if !slices.ContainsFunc(schema.OneOf, func(o struct {
		Ref string `json:"$ref"`
	}) bool {
		return o.Ref == defsPrefix+"Result"
	}) {
		t.Fatal("the schema's oneOf has no Result")
	}

	checked := map[string]bool{}
	var follow func(t reflect.Type)
	follow = func(typ reflect.Type) {
		name := typ.Name()
		if checked[name] {
			return
		}
		checked[name] = true
		def, ok := schema.Defs[name]
		if !ok {
			t.Errorf("the schema has no $defs.%s", name)
			return
		}
		if def.AdditionalProperties == nil || *def.AdditionalProperties {
			t.Errorf("%s: the schema allows other keys", name)
		}
		keys := keysOf(typ)
		names := make([]string, 0, len(keys))
		var required []string
		for _, k := range keys {
			names = append(names, k.name)
			if k.required {
				required = append(required, k.name)
			}
		}
		if got, want := slices.Sorted(slices.Values(names)), slices.Sorted(maps.Keys(def.Properties)); !slices.Equal(got, want) {
			t.Errorf("%s keys = %q, schema properties %q", name, got, want)
		}
		if got, want := slices.Sorted(slices.Values(required)), slices.Sorted(slices.Values(def.Required)); !slices.Equal(got, want) {
			t.Errorf("%s required = %q, schema %q", name, got, want)
		}
		for _, k := range keys {
			p, ok := def.Properties[k.name]
			if !ok {
				continue
			}
			at := name + "." + k.name
			checkRule(t, at, k.rule, p)
			if next := checkKind(t, at, k.typ, p); next != nil {
				follow(next)
			}
		}
	}
	follow(reflect.TypeFor[Result]())
}

// checkRule compares the rule tag of a key with the schema's enum or pattern.
func checkRule(t *testing.T, at, tag string, p property) {
	t.Helper()
	r, tagged := rules[tag]
	if r.minLength != p.MinLength || r.maximum != p.Maximum {
		t.Errorf("%s: rule %q has minLength %d and maximum %d, schema %d and %d", at, tag, r.minLength, r.maximum, p.MinLength, p.Maximum)
		return
	}
	switch {
	case tag != "" && !tagged:
		t.Errorf("%s: rule tag %q names no rule", at, tag)
	case len(p.Enum) > 0:
		if !slices.Equal(slices.Sorted(slices.Values(r.enum)), slices.Sorted(slices.Values(p.Enum))) {
			t.Errorf("%s: enum %q, schema %q", at, r.enum, p.Enum)
		}
	case p.Pattern != "":
		if r.pattern == nil || r.pattern.String() != p.Pattern {
			t.Errorf("%s: rule %q, schema pattern %q", at, tag, p.Pattern)
		}
	case tagged && r.minLength == 0 && r.maximum == 0:
		t.Errorf("%s: rule tag %q, the schema has no rule", at, tag)
	}
}

// checkKind compares the Go kind of a key with its JSON type and returns the struct type to follow, if any. The kinds
// accepted here are the kinds the pre-pass reads.
func checkKind(t *testing.T, at string, typ reflect.Type, p property) reflect.Type {
	t.Helper()
	if typ.Kind() == reflect.Pointer && p.Ref != "" {
		typ = typ.Elem()
	}
	switch {
	case p.Ref != "" && typ.Kind() == reflect.Struct && p.Ref == defsPrefix+typ.Name():
		return typ
	case p.Type == "array" && typ.Kind() == reflect.Slice && p.Items != nil && p.Items.Ref == defsPrefix+typ.Elem().Name():
		return typ.Elem()
	case p.Type == "string" && typ.Kind() == reflect.String,
		p.Type == "boolean" && typ.Kind() == reflect.Bool:
		return nil
	case p.Type == "integer" && typ.Kind() == reflect.Int:
		if p.Minimum == nil || *p.Minimum != 1 {
			t.Errorf("%s: the pre-pass requires >= 1, the schema minimum is %v", at, p.Minimum)
		}
		return nil
	}
	t.Errorf("%s: Go %s does not match schema %s", at, typ, strings.TrimSpace(p.Type+" "+p.Ref))
	return nil
}

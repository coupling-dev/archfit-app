package policy3

import (
	"fmt"
	"maps"
	"math"
	"reflect"
	"regexp"
	"slices"
	"strings"
	"time"

	"go.yaml.in/yaml/v3"
)

var (
	namesType  = reflect.TypeFor[Names]()
	switchType = reflect.TypeFor[Switch]()
)

// exponentOnly matches a number with an exponent and no decimal point. The engine's YAML
// library reads it as a string and yaml.v3 as a float, and a GitHub login such as 1e3 must
// stay a name, so it is a string here too.
var exponentOnly = regexp.MustCompile(`^[-+]?[0-9]+[eE][-+]?[0-9]+$`)

// toAny converts a YAML node into the plain tree that checkKinds walks, with the engine's
// two scalar rules: a mapping key is its text whatever its kind (a module may be named
// 123), and a bare number with an exponent and no decimal point is a string. An alias, a
// merge key and a mapping whose key is an alias are resolved by yaml.v3 itself, which
// detects a cycle and bounds the expansion, so a crafted file cannot exhaust the stack;
// the two rules do not reach inside what an alias or a merge brings.
func toAny(n *yaml.Node) any {
	switch n.Kind {
	case yaml.SequenceNode:
		list := make([]any, len(n.Content))
		for i, item := range n.Content {
			list[i] = toAny(item)
		}
		return list
	case yaml.MappingNode:
		m := make(map[string]any, len(n.Content)/2)
		for i := 0; i+1 < len(n.Content); i += 2 {
			key := n.Content[i]
			if key.Kind == yaml.AliasNode || key.ShortTag() == "!!merge" {
				return resolved(n)
			}
			m[key.Value] = toAny(n.Content[i+1])
		}
		return m
	case yaml.ScalarNode:
		var v any
		if err := n.Decode(&v); err != nil {
			return n.Value
		}
		if _, isFloat := v.(float64); isFloat && exponentOnly.MatchString(n.Value) {
			return n.Value
		}
		return v
	case yaml.AliasNode:
		return resolved(n)
	}
	return nil
}

// resolved lets yaml.v3 decode a node and gives the mappings string keys. A node that
// cannot be decoded reads as an empty value: the strict decode that came first would have
// refused any file whose aliases it followed.
func resolved(n *yaml.Node) any {
	var v any
	if err := n.Decode(&v); err != nil {
		return nil
	}
	return stringKeys(v)
}

func stringKeys(v any) any {
	switch v := v.(type) {
	case map[string]any:
		for k, e := range v {
			v[k] = stringKeys(e)
		}
		return v
	case map[any]any:
		m := make(map[string]any, len(v))
		for k, e := range v {
			m[fmt.Sprint(k)] = stringKeys(e)
		}
		return m
	case []any:
		for i, e := range v {
			v[i] = stringKeys(e)
		}
		return v
	}
	return v
}

// checkKinds walks the raw YAML tree beside the File type and reports a value of the
// wrong kind. The YAML decoder converts scalars silently (12 into a string field, "7"
// into an int field) and reads a null as an absent key; the published schema rejects all
// three, so the decoder must too.
func checkKinds(p *problems, v any, t reflect.Type, path string) {
	if t.Kind() == reflect.Pointer {
		t = t.Elem()
	}
	if v == nil {
		p.add(path, "empty value: give a value or remove the key")
		return
	}
	switch t {
	case namesType:
		if list, ok := v.([]any); ok {
			checkKinds(p, list, reflect.TypeFor[[]string](), path)
			return
		}
		checkString(p, path, v, "a name or a list of names")
		return
	case switchType:
		if isBool(v) {
			return
		}
		checkString(p, path, v, `true, false or "auto"`)
		return
	}
	switch t.Kind() {
	case reflect.String:
		checkString(p, path, v, "a string")
	case reflect.Int, reflect.Int64:
		expectKind(p, path, v, "a whole number", isInteger)
	case reflect.Bool:
		expectKind(p, path, v, "true or false", isBool)
	case reflect.Slice:
		list, ok := v.([]any)
		if !ok {
			p.add(path, "want a list")
			return
		}
		for i, item := range list {
			checkKinds(p, item, t.Elem(), fmt.Sprintf("%s[%d]", path, i))
		}
	case reflect.Map:
		m, ok := v.(map[string]any)
		if !ok {
			p.add(path, "want a mapping")
			return
		}
		for _, key := range slices.Sorted(maps.Keys(m)) {
			checkKinds(p, m[key], t.Elem(), joinPath(path, key))
		}
	case reflect.Struct:
		m, ok := v.(map[string]any)
		if !ok {
			p.add(path, "want a mapping")
			return
		}
		fields := yamlFields(t)
		for _, key := range slices.Sorted(maps.Keys(m)) {
			// Unknown keys already failed the strict decode.
			if field, ok := fields[key]; ok {
				checkKinds(p, m[key], field.Type, joinPath(path, key))
			}
		}
	}
}

func expectKind(p *problems, path string, v any, want string, ok func(any) bool) {
	if !ok(v) {
		p.add(path, "%v is not %s", yamlValue(v), want)
	}
}

// checkString requires a non-empty string. An empty string is never a value in the
// policy: the schema gives every string a minimum length of 1, so an explicit "" never
// reads as an absent key.
func checkString(p *problems, path string, v any, want string) {
	// The YAML decoder reads an unquoted date as a time. The typed decode keeps its text, as
	// the engine's decoder does, so a date where a string belongs is a string.
	if _, ok := v.(time.Time); ok {
		return
	}
	s, ok := v.(string)
	switch {
	case !ok:
		p.add(path, "%v is not %s", yamlValue(v), want)
	case s == "":
		p.add(path, "empty string: give a value or remove the key")
	}
}

func isBool(v any) bool { _, ok := v.(bool); return ok }

// isInteger follows JSON Schema: a number with no fraction is an integer.
func isInteger(v any) bool {
	switch n := v.(type) {
	case int, int64, uint64:
		return true
	case float64:
		return n == math.Trunc(n) && !math.IsInf(n, 0)
	}
	return false
}

// yamlFields maps each YAML key of a struct to its field.
func yamlFields(t reflect.Type) map[string]reflect.StructField {
	fields := make(map[string]reflect.StructField, t.NumField())
	for i := range t.NumField() {
		f := t.Field(i)
		name, _, _ := strings.Cut(f.Tag.Get("yaml"), ",")
		fields[name] = f
	}
	return fields
}

func joinPath(path, key string) string {
	if path == "" {
		return key
	}
	return path + "." + key
}

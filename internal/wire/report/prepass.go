package report

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"reflect"
	"strconv"
	"strings"
)

// maxDepth caps the nesting of objects and lists. [default]
const maxDepth = 16

// locError is the only error constructor of the package: a JSON location and a rule, never a payload value.
func locError(loc, rule string) error {
	return fmt.Errorf("report: %s: %s", loc, rule)
}

// seg is one step of a JSON location: an object key, a list index (index >= 0), or both ("findings[3]").
type seg struct {
	key   string
	index int
}

func formatLoc(path []seg) string {
	if len(path) == 0 {
		return "payload"
	}
	var b strings.Builder
	for _, s := range path {
		if s.key != "" {
			if b.Len() > 0 {
				b.WriteByte('.')
			}
			b.WriteString(s.key)
		}
		if s.index >= 0 {
			b.WriteByte('[')
			b.WriteString(strconv.Itoa(s.index))
			b.WriteByte(']')
		}
	}
	return b.String()
}

// scanner is the token pre-pass. encoding/json alone matches keys case-insensitively, keeps the last duplicate
// key, turns null into a zero value and drops list indexes from its errors. The pre-pass walks the token stream
// next to the shadow types instead: keys must equal a schema key exactly (which implies ^[a-z][a-z0-9_]*$) of
// the selected release's key set, no object repeats a key, null is allowed for optInt and optBool only, every value has the
// JSON type of its shadow field, and input ends after the top-level object. The keys of a map are free; its
// duplicates are not.
type scanner struct {
	dec    *json.Decoder
	path   []seg
	fields map[reflect.Type]map[string]field
}

// field is one key of a shadow struct: its type.
type field struct {
	typ reflect.Type
}

// prepass scans payload against the shadow type root.
func prepass(payload []byte, root reflect.Type) error {
	return prepassAt(payload, root, nil)
}

// prepassAt is prepass for a document that sits at base inside a larger one, so its errors name the full location.
func prepassAt(payload []byte, root reflect.Type, base []seg) error {
	s := &scanner{dec: json.NewDecoder(bytes.NewReader(payload)), fields: map[reflect.Type]map[string]field{}, path: base}
	s.dec.UseNumber()
	if tok, err := s.dec.Token(); err != nil || tok != json.Delim('{') {
		return s.fail("must be a JSON object")
	}
	if err := s.object(root, 1); err != nil {
		return err
	}
	if _, err := s.dec.Token(); err != io.EOF {
		return s.fail("data after the top-level object")
	}
	return nil
}

func (s *scanner) fail(rule string) error {
	return locError(formatLoc(s.path), rule)
}

// fieldsOf maps the JSON keys of a shadow struct to its fields.
func (s *scanner) fieldsOf(t reflect.Type) map[string]field {
	if f, ok := s.fields[t]; ok {
		return f
	}
	f := make(map[string]field, t.NumField())
	for i := range t.NumField() {
		sf := t.Field(i)
		key, _, _ := strings.Cut(sf.Tag.Get("json"), ",")
		f[key] = field{typ: sf.Type}
	}
	s.fields[t] = f
	return f
}

// object scans the members of an object whose opening brace is consumed. t is a struct or a map.
func (s *scanner) object(t reflect.Type, depth int) error {
	seen := map[string]struct{}{}
	for s.dec.More() {
		tok, err := s.dec.Token()
		key, ok := tok.(string)
		if err != nil || !ok {
			return s.fail("invalid JSON")
		}
		if _, dup := seen[key]; dup {
			return s.fail("duplicate key")
		}
		seen[key] = struct{}{}

		child, name := reflect.Type(nil), "*" // a map key is input: it is never echoed
		if t.Kind() == reflect.Map {
			child = t.Elem()
			if isDimension(key) {
				name = key
			}
		} else if f, known := s.fieldsOf(t)[key]; known {
			child, name = f.typ, key
		} else {
			return s.fail("unknown key")
		}
		s.path = append(s.path, seg{name, -1})
		if err := s.value(child, depth); err != nil {
			return err
		}
		s.path = s.path[:len(s.path)-1]
	}
	if _, err := s.dec.Token(); err != nil {
		return s.fail("invalid JSON")
	}
	return nil
}

func (s *scanner) list(elem reflect.Type, depth int) error {
	last := len(s.path) - 1
	for i := 0; s.dec.More(); i++ {
		s.path[last].index = i
		if err := s.value(elem, depth); err != nil {
			return err
		}
	}
	s.path[last].index = -1
	if _, err := s.dec.Token(); err != nil {
		return s.fail("invalid JSON")
	}
	return nil
}

// value scans one value that must have the JSON type of the shadow type t. depth is the depth of its parent.
func (s *scanner) value(t reflect.Type, depth int) error {
	for t.Kind() == reflect.Pointer {
		t = t.Elem()
	}
	tok, err := s.dec.Token()
	if err != nil {
		return s.fail("invalid JSON")
	}
	kind, ok := t.Kind(), false
	switch v := tok.(type) {
	case json.Delim:
		if depth >= maxDepth {
			return s.fail("nested deeper than " + strconv.Itoa(maxDepth))
		}
		if v == '{' && (kind == reflect.Struct || kind == reflect.Map) {
			return s.object(t, depth+1)
		}
		if v == '[' && kind == reflect.Slice {
			return s.list(t.Elem(), depth+1)
		}
	case nil:
		if t != reflect.TypeFor[optInt]() && t != reflect.TypeFor[optBool]() {
			return s.fail("null is not allowed")
		}
		ok = true
	case string:
		ok = kind == reflect.String
	case bool:
		ok = kind == reflect.Bool
	case json.Number:
		switch kind {
		case reflect.Int:
			if _, err := strconv.Atoi(v.String()); err != nil {
				return s.fail("must be an integer without fraction or exponent")
			}
			ok = true
		case reflect.Float64:
			if _, err := strconv.ParseFloat(v.String(), 64); err != nil {
				return s.fail("must be a finite number")
			}
			ok = true
		}
	}
	if !ok {
		return s.fail("must be " + jsonTypeName(kind))
	}
	return nil
}

func jsonTypeName(kind reflect.Kind) string {
	switch kind {
	case reflect.Struct, reflect.Map:
		return "an object"
	case reflect.Slice:
		return "a list"
	case reflect.String:
		return "a string"
	case reflect.Bool:
		return "a boolean"
	case reflect.Int:
		return "an integer"
	default:
		return "a number"
	}
}

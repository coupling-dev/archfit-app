package result

import (
	"bytes"
	"encoding/json"
	"errors"
	"io"
	"reflect"
	"regexp"
	"slices"
	"strconv"
	"strings"
	"unicode/utf8"
)

// rule is a value rule of the schema: an enum, a pattern, a minimum string length, or an integer maximum.
type rule struct {
	enum      []string
	pattern   *regexp.Regexp
	minLength int
	maximum   int
}

// rules holds the schema's value rules by the name a rule tag gives. The patterns are the schema's, verbatim.
var rules = map[string]rule{
	"schema": {enum: []string{SchemaV1}},
	"status": {enum: []string{string(StatusPass), string(StatusFail), string(StatusWaiting), string(StatusUnknown)}},
	"kind":   {enum: []string{"fix", "command", "wait", "restore"}},
	"cause":  {enum: []string{"policy", "base", "tool", "import"}},
	"repository": {enum: []string{
		string(RepositoryClean), string(RepositoryDrift), string(RepositoryExpired),
	}},
	"digest": {pattern: regexp.MustCompile(`^sha256:[0-9a-f]{64}$`)},
	"commit": {pattern: regexp.MustCompile(`^[0-9a-f]{40}([0-9a-f]{24})?$`)},
	"date":   {pattern: regexp.MustCompile(`^[0-9]{4}-[0-9]{2}-[0-9]{2}$`)},
	"text":   {minLength: 1},
	"scale":  {maximum: 10},
}

// violation is the rule's message for value, or "" when value conforms. It never quotes value.
func (r rule) violation(value string) string {
	if r.minLength > 0 {
		if len(value) >= r.minLength {
			return ""
		}
		return "must not be empty"
	}
	if r.pattern != nil {
		if r.pattern.MatchString(value) {
			return ""
		}
		return "must match " + r.pattern.String()
	}
	if slices.Contains(r.enum, value) {
		return ""
	}
	return "must be one of " + strings.Join(r.enum, ", ")
}

var errMalformed = errors.New("result: malformed JSON")

// locError names the JSON location and the rule, never a value of the input.
func locError(loc, violated string) error {
	if loc == "" {
		return errors.New("result: " + violated)
	}
	return errors.New("result: " + loc + ": " + violated)
}

// Decode reads raw strictly against the Result branch of archfit.result.schema.json. encoding/json alone matches
// keys case-insensitively, keeps the last of duplicate keys, turns null into a zero value and ignores absent keys,
// so a token pre-pass walks the input beside the Go types first: keys exactly as tagged, each once, a key without
// omitempty required, no null, each value of its JSON type, integers whole and >= 1 (the schema's minimum for every
// integer), the value rules of the rule tags (enums, patterns, minLength, maximum), and nothing after the object. The caller bounds the size.
func Decode(raw []byte) (Result, error) {
	// encoding/json replaces invalid UTF-8 silently.
	if !utf8.Valid(raw) {
		return Result{}, errors.New("result: invalid UTF-8")
	}
	w := walker{dec: json.NewDecoder(bytes.NewReader(raw))}
	w.dec.UseNumber()
	if err := w.value(reflect.TypeFor[Result](), "", ""); err != nil {
		return Result{}, err
	}
	if _, err := w.dec.Token(); !errors.Is(err, io.EOF) {
		return Result{}, errors.New("result: trailing data after the object")
	}

	dec := json.NewDecoder(bytes.NewReader(raw))
	dec.DisallowUnknownFields()
	var r Result
	if err := dec.Decode(&r); err != nil {
		return Result{}, errMalformed
	}
	return r, nil
}

// walker is the token pre-pass.
type walker struct {
	dec *json.Decoder
}

// value reads one value of type t at loc; ruleName is the rule tag of its key. The types above use only the kinds
// below; TestTypesFollowTheVendoredSchema fails on any other.
func (w *walker) value(t reflect.Type, loc, ruleName string) error {
	tok, err := w.dec.Token()
	if err != nil {
		return errMalformed
	}
	if tok == nil {
		return locError(loc, "must not be null")
	}
	if t.Kind() == reflect.Pointer {
		t = t.Elem()
	}
	switch t.Kind() {
	case reflect.Struct:
		if tok != json.Delim('{') {
			return locError(loc, "must be an object")
		}
		return w.object(t, loc)
	case reflect.Slice:
		if tok != json.Delim('[') {
			return locError(loc, "must be an array")
		}
		for i := 0; w.dec.More(); i++ {
			if err := w.value(t.Elem(), loc+"["+strconv.Itoa(i)+"]", ""); err != nil {
				return err
			}
		}
		if _, err := w.dec.Token(); err != nil {
			return errMalformed
		}
	case reflect.String:
		s, ok := tok.(string)
		if !ok {
			return locError(loc, "must be a string")
		}
		if r, ok := rules[ruleName]; ok {
			if violated := r.violation(s); violated != "" {
				return locError(loc, violated)
			}
		}
	case reflect.Bool:
		if _, ok := tok.(bool); !ok {
			return locError(loc, "must be a boolean")
		}
	case reflect.Int:
		n, ok := tok.(json.Number)
		if !ok {
			return locError(loc, "must be an integer")
		}
		i, err := strconv.Atoi(n.String())
		if err != nil || i < 1 {
			return locError(loc, "must be an integer >= 1")
		}
		if r, ok := rules[ruleName]; ok && r.maximum > 0 && i > r.maximum {
			return locError(loc, "must be <= "+strconv.Itoa(r.maximum))
		}
	}
	return nil
}

// object reads the keys of an object of struct type t, after its opening brace.
func (w *walker) object(t reflect.Type, loc string) error {
	fields := keysOf(t)
	seen := make(map[string]bool, len(fields))
	for w.dec.More() {
		tok, err := w.dec.Token()
		if err != nil {
			return errMalformed
		}
		name, _ := tok.(string)
		f, ok := lookup(fields, name)
		if !ok {
			return locError(loc, "unknown key")
		}
		at := join(loc, name)
		if seen[name] {
			return locError(at, "duplicate key")
		}
		seen[name] = true
		if err := w.value(f.typ, at, f.rule); err != nil {
			return err
		}
	}
	if _, err := w.dec.Token(); err != nil {
		return errMalformed
	}
	for _, f := range fields {
		if f.required && !seen[f.name] {
			return locError(join(loc, f.name), "required")
		}
	}
	return nil
}

// key is one JSON key of a struct type.
type key struct {
	name     string
	typ      reflect.Type
	required bool
	rule     string
}

func keysOf(t reflect.Type) []key {
	keys := make([]key, 0, t.NumField())
	for f := range t.Fields() {
		name, opts, _ := strings.Cut(f.Tag.Get("json"), ",")
		keys = append(keys, key{name: name, typ: f.Type, required: opts != "omitempty", rule: f.Tag.Get("rule")})
	}
	return keys
}

func lookup(keys []key, name string) (key, bool) {
	for _, k := range keys {
		if k.name == name {
			return k, true
		}
	}
	return key{}, false
}

func join(loc, name string) string {
	if loc == "" {
		return name
	}
	return loc + "." + name
}

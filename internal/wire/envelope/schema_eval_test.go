package envelope_test

import (
	"bytes"
	"encoding/json"
	"io"
	"math/big"
	"reflect"
	"regexp"
	"testing"
	"unicode/utf8"

	"github.com/alexei-led/archfit-app/internal/wire/envelope"
)

// parseJSON keeps numbers as json.Number and rejects trailing data.
func parseJSON(raw []byte) (any, error) {
	dec := json.NewDecoder(bytes.NewReader(raw))
	dec.UseNumber()
	var v any
	if err := dec.Decode(&v); err != nil {
		return nil, err
	}
	if _, err := dec.Token(); err != io.EOF {
		return nil, io.ErrUnexpectedEOF
	}
	return v, nil
}

// schemaAccepts evaluates raw against Schema() with the test-local evaluator.
func schemaAccepts(t *testing.T, raw []byte) bool {
	t.Helper()
	doc, err := parseJSON(envelope.Schema())
	if err != nil {
		t.Fatalf("Schema() is not valid JSON: %v", err)
	}
	schema, ok := doc.(map[string]any)
	if !ok {
		t.Fatalf("Schema() is not a JSON object")
	}
	inst, err := parseJSON(raw)
	if err != nil {
		return false
	}
	return evalSchema(t, schema, inst)
}

func wantSchemaAgrees(t *testing.T, raw []byte, decoded bool) {
	t.Helper()
	if got := schemaAccepts(t, raw); got != decoded {
		t.Errorf("schema accepts = %v, Decode accepts = %v", got, decoded)
	}
}

// evalSchema supports exactly the keywords the envelope schema uses and fails the test on any other.
// It visits every keyword, without short-circuit, so an unknown one cannot hide behind a failure.
func evalSchema(t *testing.T, schema map[string]any, inst any) bool {
	t.Helper()
	ok := true
	for keyword, arg := range schema {
		switch keyword {
		case "$schema", "$id", "title", "description", "then", "else":
			// Annotations; then/else are evaluated with "if".
		case "type":
			ok = hasType(t, arg, inst) && ok
		case "const":
			ok = reflect.DeepEqual(arg, inst) && ok
		case "enum":
			found := false
			for _, want := range arg.([]any) {
				found = found || reflect.DeepEqual(want, inst)
			}
			ok = found && ok
		case "pattern":
			if s, isString := inst.(string); isString {
				ok = regexp.MustCompile(arg.(string)).MatchString(s) && ok
			}
		case "maxLength":
			if s, isString := inst.(string); isString {
				ok = big.NewRat(int64(utf8.RuneCountInString(s)), 1).Cmp(rat(t, arg)) <= 0 && ok
			}
		case "minimum":
			if n, isNumber := inst.(json.Number); isNumber {
				ok = rat(t, n).Cmp(rat(t, arg)) >= 0 && ok
			}
		case "required":
			if obj, isObject := inst.(map[string]any); isObject {
				for _, name := range arg.([]any) {
					_, present := obj[name.(string)]
					ok = present && ok
				}
			}
		case "properties":
			if obj, isObject := inst.(map[string]any); isObject {
				for name, sub := range arg.(map[string]any) {
					if v, present := obj[name]; present {
						ok = evalSchema(t, sub.(map[string]any), v) && ok
					}
				}
			}
		case "additionalProperties":
			if arg != false {
				t.Fatalf("evaluator supports additionalProperties: false only")
			}
			if obj, isObject := inst.(map[string]any); isObject {
				declared, _ := schema["properties"].(map[string]any)
				for name := range obj {
					_, known := declared[name]
					ok = known && ok
				}
			}
		case "allOf":
			for _, sub := range arg.([]any) {
				ok = evalSchema(t, sub.(map[string]any), inst) && ok
			}
		case "if":
			branch := "else"
			if evalSchema(t, arg.(map[string]any), inst) {
				branch = "then"
			}
			if sub, present := schema[branch]; present {
				ok = evalSchema(t, sub.(map[string]any), inst) && ok
			}
		default:
			t.Fatalf("schema uses keyword %q, which the test evaluator does not know", keyword)
		}
	}
	return ok
}

func hasType(t *testing.T, arg, inst any) bool {
	t.Helper()
	switch arg {
	case "object":
		_, ok := inst.(map[string]any)
		return ok
	case "string":
		_, ok := inst.(string)
		return ok
	case "integer":
		n, ok := inst.(json.Number)
		return ok && rat(t, n).IsInt()
	default:
		t.Fatalf("evaluator does not know type %v", arg)
		return false
	}
}

func rat(t *testing.T, n any) *big.Rat {
	t.Helper()
	num, ok := n.(json.Number)
	if !ok {
		t.Fatalf("%v is not a number", n)
	}
	r, ok := new(big.Rat).SetString(num.String())
	if !ok {
		t.Fatalf("%v is not a number", n)
	}
	return r
}

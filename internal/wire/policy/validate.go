package policy

import (
	"encoding/json"
	"errors"
	"fmt"
	"sync"
)

var (
	errEmpty  = errors.New("policy: document is empty")
	errSchema = errors.New("policy: embedded engine config schema is not readable")
)

// keySet is the closed set of top-level keys of an engine config schema.
type keySet struct {
	allowed  map[string]struct{}
	required []string
}

// engineKeys is derived from the embedded schema, so a new engine release changes it with the schema file.
var engineKeys = sync.OnceValues(func() (keySet, error) { return schemaKeySet(schema) })

// schemaKeySet reads the root `properties` and `required` of a config JSON Schema. A schema whose root is
// not closed with `additionalProperties: false` has no key set to check against and is an error.
func schemaKeySet(schema []byte) (keySet, error) {
	var root struct {
		Properties           map[string]json.RawMessage `json:"properties"`
		Required             []string                   `json:"required"`
		AdditionalProperties json.RawMessage            `json:"additionalProperties"`
	}
	if err := json.Unmarshal(schema, &root); err != nil {
		return keySet{}, errSchema
	}
	if len(root.Properties) == 0 || string(root.AdditionalProperties) != "false" {
		return keySet{}, errSchema
	}
	keys := keySet{allowed: make(map[string]struct{}, len(root.Properties)), required: root.Required}
	for key := range root.Properties {
		keys.allowed[key] = struct{}{}
	}
	return keys, nil
}

// Validate reports whether policy is plausibly an engine policy file: a YAML document whose root is a
// mapping, whose top-level keys all belong to the engine config schema's closed key set, and which has
// the required `version` key. It is a structural check, not engine validation: the engine remains the
// authority on the file's meaning. Errors never echo keys or values.
func Validate(policy []byte) error {
	keys, err := engineKeys()
	if err != nil {
		return err
	}
	return keys.check(policy)
}

func (k keySet) check(policy []byte) error {
	top, ok, err := read(policy)
	if err != nil {
		return err
	}
	return k.checkTop(top, ok)
}

// checkTop is check over an already read text; ok is read's.
func (k keySet) checkTop(top topLevel, ok bool) error {
	if !ok {
		return errEmpty
	}
	// A merge key is not a schema key, so keys merged into the root never need a look.
	present := make(map[string]struct{}, len(k.allowed))
	for i := 0; i < len(top.root.Content); i += 2 {
		key := resolve(top.root.Content[i]).Value
		if _, ok := k.allowed[key]; !ok {
			return fmt.Errorf("policy: top-level key %d is not an engine config key", i/2+1)
		}
		present[key] = struct{}{}
	}
	// The names come from the schema, never from the input.
	for _, key := range k.required {
		if _, ok := present[key]; !ok {
			return fmt.Errorf("policy: required top-level key %q is absent", key)
		}
	}
	return nil
}

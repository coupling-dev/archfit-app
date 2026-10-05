package policy

import (
	"bytes"
	"encoding/json"
	"regexp"
	"strings"
	"sync"
)

// schemaNode is one JSON Schema object of the embedded engine schema. It knows exactly the keywords that schema
// uses. The decoder refuses any other keyword, so a re-pinned schema that needs more fails closed, in tests and
// at run time, instead of checking less than it says.
type schemaNode struct {
	SchemaURI         string                 `json:"$schema"`
	ID                string                 `json:"$id"`
	Defs              map[string]*schemaNode `json:"$defs"`
	Ref               string                 `json:"$ref"`
	Description       string                 `json:"description"`
	Default           json.RawMessage        `json:"default"`
	Type              string                 `json:"type"`
	Properties        map[string]*schemaNode `json:"properties"`
	PatternProperties map[string]*schemaNode `json:"patternProperties"`
	Additional        *additional            `json:"additionalProperties"`
	Required          []string               `json:"required"`
	Items             *schemaNode            `json:"items"`
	MinItems          *int                   `json:"minItems"`
	Enum              []json.RawMessage      `json:"enum"`
	Const             json.RawMessage        `json:"const"`
	OneOf             []*schemaNode          `json:"oneOf"`
	Minimum           *float64               `json:"minimum"`
	MinLength         *int                   `json:"minLength"`
	Format            string                 `json:"format"`

	patterns []keyPattern // compiled PatternProperties
	target   *schemaNode  // resolved Ref
}

// additional is `additionalProperties`: false closes the object, a schema checks every other key.
type additional struct {
	closed bool
	schema *schemaNode
}

func (a *additional) UnmarshalJSON(raw []byte) error {
	switch string(bytes.TrimSpace(raw)) {
	case "false":
		a.closed = true
		return nil
	case "true":
		return nil
	}
	a.schema = new(schemaNode)
	return strictJSON(raw, a.schema)
}

type keyPattern struct {
	match  func(key string) bool
	schema *schemaNode
}

// strictJSON decodes one JSON value and refuses unknown fields; a custom unmarshaler gets no decoder options,
// so every nested decode goes through here.
func strictJSON(raw []byte, into any) error {
	dec := json.NewDecoder(bytes.NewReader(raw))
	dec.DisallowUnknownFields()
	return dec.Decode(into)
}

// engineSchema is the compiled embedded schema, read once.
var engineSchema = sync.OnceValues(func() (*schemaNode, error) { return compileSchema(schema) })

func compileSchema(raw []byte) (*schemaNode, error) {
	root := new(schemaNode)
	if err := strictJSON(raw, root); err != nil {
		return nil, errSchema
	}
	if err := root.link(root, 0); err != nil {
		return nil, err
	}
	return root, nil
}

// maxSchemaDepth bounds link on a malformed schema; the engine schema nests five levels.
const maxSchemaDepth = 32

// link resolves references and compiles key patterns of n and everything below it. It refuses what the walker
// would apply only in part: validation keywords beside `$ref` or `oneOf`, a format other than date and date-time,
// a reference chain that does not end, and a property name that a key pattern also matches.
func (n *schemaNode) link(root *schemaNode, depth int) error {
	if n == nil {
		return nil
	}
	if depth > maxSchemaDepth {
		return errSchema
	}
	if n.Ref != "" {
		if n.validates() || len(n.OneOf) > 0 {
			return errSchema
		}
		target, err := follow(root, n.Ref)
		if err != nil {
			return err
		}
		n.target = target
	}
	if len(n.OneOf) > 0 && n.validates() {
		return errSchema
	}
	switch n.Format {
	case "", "date", "date-time":
	default:
		return errSchema
	}
	patterns := keysOf(n.PatternProperties)
	for _, pattern := range patterns {
		match, err := keyMatcher(pattern)
		if err != nil {
			return err
		}
		for name := range n.Properties {
			if match(name) {
				return errSchema
			}
		}
		n.patterns = append(n.patterns, keyPattern{match: match, schema: n.PatternProperties[pattern]})
	}
	children := []*schemaNode{n.Items}
	if n.Additional != nil {
		children = append(children, n.Additional.schema)
	}
	children = append(children, n.OneOf...)
	for _, m := range []map[string]*schemaNode{n.Defs, n.Properties, n.PatternProperties} {
		for _, name := range keysOf(m) {
			children = append(children, m[name])
		}
	}
	for _, sub := range children {
		if err := sub.link(root, depth+1); err != nil {
			return err
		}
	}
	return nil
}

// validates reports a keyword that checks a value, as opposed to an annotation or a reference.
func (n *schemaNode) validates() bool {
	return n.Type != "" || n.Properties != nil || n.PatternProperties != nil || n.Additional != nil ||
		n.Required != nil || n.Items != nil || n.MinItems != nil || n.Enum != nil || len(n.Const) > 0 ||
		n.Minimum != nil || n.MinLength != nil || n.Format != ""
}

// follow resolves a `#/$defs/<name>` reference through any chain of references to the definition that checks.
func follow(root *schemaNode, ref string) (*schemaNode, error) {
	for range maxSchemaDepth {
		name, ok := strings.CutPrefix(ref, "#/$defs/")
		target := root.Defs[name]
		if !ok || target == nil {
			return nil, errSchema
		}
		if target.Ref == "" {
			return target, nil
		}
		ref = target.Ref
	}
	return nil, errSchema
}

// notExactly is the one pattern shape the engine schema uses that RE2 cannot compile: any key but one.
var notExactly = regexp.MustCompile(`^\^\(\?!([A-Za-z0-9_]+)\$\)\.\+\$$`)

func keyMatcher(pattern string) (func(string) bool, error) {
	if m := notExactly.FindStringSubmatch(pattern); m != nil {
		except := m[1]
		return func(key string) bool { return key != "" && key != except }, nil
	}
	re, err := regexp.Compile(pattern)
	if err != nil {
		return nil, errSchema
	}
	return re.MatchString, nil
}

// resolved follows a reference to its definition.
func (n *schemaNode) resolved() *schemaNode {
	if n.target != nil {
		return n.target
	}
	return n
}

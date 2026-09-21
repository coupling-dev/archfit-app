package policy

import (
	"bytes"
	"errors"
	"io"
	"unicode/utf8"

	"go.yaml.in/yaml/v3"
)

const maxInputBytes = 1 << 20

// The library's own error text is never returned: it can echo keys, anchors and scalar values of the input.
var (
	errTooLarge       = errors.New("policy: input is larger than 1048576 bytes")
	errNotUTF8        = errors.New("policy: input is not valid UTF-8")
	errSyntax         = errors.New("policy: input is not valid YAML")
	errNotOneDocument = errors.New("policy: input is not exactly one YAML document")
	errRootNotMapping = errors.New("policy: document root is not a mapping")
)

// topLevel holds the only keys this module reads. Every other key is ignored. The values stay nodes because
// `expires` needs the scalar text as written and module keys need their line.
type topLevel struct {
	Waivers yaml.Node `yaml:"waivers"`
	Modules yaml.Node `yaml:"modules"`
}

// read applies the common reading rules. ok is false for empty input.
func read(policy []byte) (top topLevel, ok bool, err error) {
	if len(policy) > maxInputBytes {
		return topLevel{}, false, errTooLarge
	}
	if !utf8.Valid(policy) {
		return topLevel{}, false, errNotUTF8
	}
	// The input is untrusted; a library bug must not take the caller down.
	defer func() {
		if recover() != nil {
			top, ok, err = topLevel{}, false, errSyntax
		}
	}()

	dec := yaml.NewDecoder(bytes.NewReader(policy))
	var doc yaml.Node
	if err := dec.Decode(&doc); err != nil {
		if errors.Is(err, io.EOF) {
			return topLevel{}, false, nil
		}
		return topLevel{}, false, errSyntax
	}
	var second yaml.Node
	switch err := dec.Decode(&second); {
	case err == nil:
		return topLevel{}, false, errNotOneDocument
	case !errors.Is(err, io.EOF):
		return topLevel{}, false, errSyntax
	}
	if len(doc.Content) != 1 || doc.Content[0].Kind != yaml.MappingNode {
		return topLevel{}, false, errRootNotMapping
	}

	// Decoding into a node checks syntax only. A full decode adds the library's duplicate-key, merge,
	// alias-cycle and alias-expansion checks, so the node walks below are bounded and cycle-free.
	var whole any
	if err := doc.Decode(&whole); err != nil {
		return topLevel{}, false, errSyntax
	}
	if err := doc.Decode(&top); err != nil {
		return topLevel{}, false, errSyntax
	}
	return top, true, nil
}

// resolve follows an alias to its anchored node.
func resolve(n *yaml.Node) *yaml.Node {
	if n.Kind == yaml.AliasNode {
		return n.Alias
	}
	return n
}

// isNull reports an absent key or a YAML null.
func isNull(n *yaml.Node) bool {
	return n.Kind == 0 || n.Kind == yaml.ScalarNode && n.ShortTag() == "!!null"
}

// scalarText returns the text of a scalar as written, "" for an absent key or a null. ok is false for a
// list or a mapping.
func scalarText(n *yaml.Node) (text string, ok bool) {
	n = resolve(n)
	switch {
	case isNull(n):
		return "", true
	case n.Kind == yaml.ScalarNode:
		return n.Value, true
	}
	return "", false
}

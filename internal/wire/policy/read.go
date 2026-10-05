package policy

import (
	"bytes"
	"errors"
	"fmt"
	"io"
	"strconv"
	"strings"
	"unicode/utf8"

	"go.yaml.in/yaml/v3"
)

const (
	maxInputBytes = 1 << 20

	// Bounds of the shape check that runs before the library decode, each **[default]**. The library's
	// duplicate-key scan compares every pair of keys of a mapping, byte by byte and again for every alias that
	// expands it, and allocates a string per duplicate pair; maxDecodeWork caps that work in units of a node or
	// 64 bytes. maxNodes only bounds the walk's depth and size: a node takes at least two bytes of input.
	maxNodes       = maxInputBytes / 2
	maxMappingKeys = 10_000
	maxDecodeWork  = 50_000_000
	workBytes      = 64
)

// The library's own error text is never returned: it can echo keys, anchors and scalar values of the input.
var (
	errTooLarge       = errors.New("policy: input is larger than 1048576 bytes")
	errNotUTF8        = errors.New("policy: input is not valid UTF-8")
	errSyntax         = errors.New("policy: input is not valid YAML")
	errNotOneDocument = errors.New("policy: input is not exactly one YAML document")
	errRootNotMapping = errors.New("policy: document root is not a mapping")
	errLineBreak      = errors.New("policy: input has a line break other than LF or CRLF")
	errTooComplex     = errors.New("policy: input has too many YAML nodes, keys or alias expansions")
)

const binaryTag = "!!binary"

// syntaxError is errSyntax with the place the library reported. Its text stays errSyntax's: only Diagnose,
// whose diagnostics go back to the author of the text, reads line and problem.
type syntaxError struct {
	line    int // 0 when the library named no line
	problem string
	// exact is false for a parser or scanner error: the library then names the line where the enclosing
	// block or token starts, or the problem line when that is line 1, so the problem is at line or below it.
	exact bool
}

func (e *syntaxError) Error() string        { return errSyntax.Error() }
func (e *syntaxError) Is(target error) bool { return target == errSyntax }

// newSyntaxError reads the first "line N: problem" of a library error. The library spells a parser error
// "yaml: line N: problem" and a decode error as a TypeError list of "line N: problem".
func newSyntaxError(err error) error {
	text := err.Error()
	if strings.Contains(text, "excessive aliasing") {
		return errTooComplex
	}
	var te *yaml.TypeError
	if errors.As(err, &te) && len(te.Errors) > 0 {
		text = te.Errors[0]
	}
	text = strings.TrimPrefix(text, "yaml: ")
	e := &syntaxError{problem: text, exact: errors.As(err, &te)}
	if rest, ok := strings.CutPrefix(text, "line "); ok {
		if num, problem, ok := strings.Cut(rest, ": "); ok {
			if line, err := strconv.Atoi(num); err == nil && line > 0 {
				e.line, e.problem = line, problem
			}
		}
	}
	// The library adds one to a scanner error's line but prints a parser error's line 0-based, and leaves out
	// line 0. Measured on go.yaml.in/yaml/v3 v3.0.5; parserProblems is that version's parser wording.
	if parserProblems[e.problem] {
		e.line++
	}
	return e
}

// checkShape walks the parsed tree before the library decodes it: at most maxNodes nodes and maxMappingKeys keys
// per mapping, no duplicate key (the library's own rule: same kind and same text), no alias cycle, and at most
// maxDecodeWork for the decode with every alias expanded. Each node is visited once.
func checkShape(doc *yaml.Node) error {
	s := shapeWalk{work: map[*yaml.Node]int{}}
	if err := s.walk(doc, 0); err != nil {
		return err
	}
	work, err := s.cost(doc)
	if err != nil {
		return err
	}
	if work > maxDecodeWork {
		return errTooComplex
	}
	return nil
}

type shapeWalk struct {
	nodes int
	work  map[*yaml.Node]int // memoized decode cost; inProgress while its subtree is summed
}

const inProgress = -1

func (s *shapeWalk) walk(n *yaml.Node, depth int) error {
	if s.nodes++; s.nodes > maxNodes || depth > maxNodes {
		return errTooComplex
	}
	// The library base64-decodes a !!binary value into a new string at every alias expansion, so one aliased
	// value multiplies memory. No engine key takes binary data.
	if n.Kind == yaml.ScalarNode && n.ShortTag() == binaryTag {
		return &syntaxError{line: n.Line, exact: true, problem: "a !!binary value, which no engine key takes"}
	}
	if n.Kind == yaml.MappingNode {
		type keyID struct {
			kind  yaml.Kind
			value string
		}
		seen := make(map[keyID]*yaml.Node, len(n.Content)/2)
		for i := 0; i+1 < len(n.Content); i += 2 {
			key := n.Content[i]
			id := keyID{key.Kind, key.Value}
			if first, ok := seen[id]; ok {
				return &syntaxError{line: key.Line, exact: true,
					problem: fmt.Sprintf("mapping key %q already defined at line %d", key.Value, first.Line)}
			}
			seen[id] = key
		}
		if len(seen) > maxMappingKeys {
			return errTooComplex
		}
	}
	for _, child := range n.Content {
		if err := s.walk(child, depth+1); err != nil {
			return err
		}
	}
	return nil
}

// cost is the work the library's decode does for n, with aliases expanded: one unit per node and per 64 bytes of a
// scalar, and per pair of keys its duplicate scan compares times the 64-byte blocks of the longest key. It
// saturates just above maxDecodeWork.
func (s *shapeWalk) cost(n *yaml.Node) (int, error) {
	switch w, ok := s.work[n]; {
	case ok && w == inProgress:
		return 0, &syntaxError{line: n.Line, exact: true, problem: "an alias refers to a node that contains it"}
	case ok:
		return w, nil
	}
	s.work[n] = inProgress
	total := 1 + len(n.Value)/workBytes
	if n.Kind == yaml.MappingNode {
		keys, longest := len(n.Content)/2, 0
		for i := 0; i < len(n.Content); i += 2 {
			longest = max(longest, len(n.Content[i].Value))
		}
		total = min(total+keys*(keys-1)/2*(1+longest/workBytes), maxDecodeWork+1)
	}
	children := n.Content
	if n.Kind == yaml.AliasNode && n.Alias != nil {
		children = []*yaml.Node{n.Alias}
	}
	for _, child := range children {
		w, err := s.cost(child)
		if err != nil {
			return 0, err
		}
		total = min(total+w, maxDecodeWork+1)
	}
	s.work[n] = total
	return total, nil
}

var parserProblems = map[string]bool{
	"did not find expected <stream-start>":   true,
	"did not find expected <document start>": true,
	"did not find expected node content":     true,
	"did not find expected key":              true,
	"did not find expected '-' indicator":    true,
	"did not find expected ',' or ']'":       true,
	"did not find expected ',' or '}'":       true,
	"found duplicate %YAML directive":        true,
	"found incompatible YAML document":       true,
	"found duplicate %TAG directive":         true,
	"found undefined tag handle":             true,
}

// topLevel holds the only keys this module reads. Every other key is ignored. The values stay nodes because
// `expires` needs the scalar text as written and module keys need their line. root is the whole mapping,
// for Validate, which looks at every key but at no value.
type topLevel struct {
	Waivers yaml.Node `yaml:"waivers"`
	Modules yaml.Node `yaml:"modules"`
	root    *yaml.Node
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
			top, ok, err = topLevel{}, false, &syntaxError{}
		}
	}()

	dec := yaml.NewDecoder(bytes.NewReader(policy))
	var doc yaml.Node
	if err := dec.Decode(&doc); err != nil {
		if errors.Is(err, io.EOF) {
			return topLevel{}, false, nil
		}
		return topLevel{}, false, newSyntaxError(err)
	}
	var second yaml.Node
	switch err := dec.Decode(&second); {
	case err == nil:
		return topLevel{}, false, errNotOneDocument
	case !errors.Is(err, io.EOF):
		return topLevel{}, false, newSyntaxError(err)
	}
	if len(doc.Content) != 1 || doc.Content[0].Kind != yaml.MappingNode {
		return topLevel{}, false, errRootNotMapping
	}
	if err := checkShape(&doc); err != nil {
		return topLevel{}, false, err
	}

	// Decoding into a node checks syntax only. A full decode adds the library's duplicate-key, merge,
	// alias-cycle and alias-expansion checks, so the node walks below are bounded and cycle-free.
	var whole any
	if err := doc.Decode(&whole); err != nil {
		return topLevel{}, false, newSyntaxError(err)
	}
	if err := doc.Decode(&top); err != nil {
		return topLevel{}, false, newSyntaxError(err)
	}
	top.root = doc.Content[0]
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

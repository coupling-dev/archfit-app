package policy

import (
	"bytes"
	"errors"
	"fmt"
	"unicode/utf8"

	"go.yaml.in/yaml/v3"
)

const (
	maxModules   = 5000
	maxNameRunes = 200
)

// ModuleDef is the subset of a policy module the App displays.
type ModuleDef struct {
	Name  string
	Layer string // may be ""
	Line  int    // 1-based line of the module key in the policy text
}

// Modules lists the modules declared under the policy's `modules:` map, in file order. It ignores every other key.
func Modules(policy []byte) ([]ModuleDef, error) {
	top, ok, err := read(policy)
	if err != nil || !ok {
		return nil, err
	}
	return modulesOf(policy, top)
}

// modulesOf is Modules over an already read text.
func modulesOf(policy []byte, top topLevel) ([]ModuleDef, error) {
	modules := resolve(&top.Modules)
	if isNull(modules) {
		return nil, nil
	}
	if modules.Kind != yaml.MappingNode {
		return nil, errors.New("policy: modules is not a mapping")
	}
	// Deliberate ceiling: the library also counts a lone CR as a line break, so its lines would not match an
	// LF split of the same text. Such input is refused instead of mapped.
	if hasOtherLineBreak(policy) {
		return nil, errLineBreak
	}
	c := collector{seen: map[string]bool{}}
	if err := c.collect(modules, false); err != nil {
		return nil, err
	}
	return c.out, nil
}

// hasOtherLineBreak reports a line break the library counts and an LF split, a text area and the engine's decoder
// do not: a lone CR, NEL, LS or PS. Only readers that report lines refuse it; the others read such a text.
func hasOtherLineBreak(policy []byte) bool {
	return bytes.Count(policy, []byte("\r")) != bytes.Count(policy, []byte("\r\n")) ||
		bytes.ContainsAny(policy, "\u0085\u2028\u2029")
}

type collector struct {
	out  []ModuleDef
	seen map[string]bool
}

// collect appends the entries of one mapping: its own keys in file order, then keys merged in with `<<` that
// the mapping does not define itself, as YAML merge semantics require.
func (c *collector) collect(mapping *yaml.Node, merged bool) error {
	var merges []*yaml.Node
	for i := 0; i+1 < len(mapping.Content); i += 2 {
		key, value := mapping.Content[i], resolve(mapping.Content[i+1])
		index := len(c.out)
		switch {
		case key.Kind != yaml.ScalarNode:
			return fmt.Errorf("policy: modules[%d] (line %d): name is not a scalar", index, key.Line)
		case key.ShortTag() == "!!merge":
			merges = append(merges, value)
			continue
		case c.seen[key.Value] && merged:
			continue
		case c.seen[key.Value]:
			return fmt.Errorf("policy: modules[%d] (line %d): duplicate name", index, key.Line)
		case index == maxModules:
			return fmt.Errorf("policy: more than %d modules", maxModules)
		case utf8.RuneCountInString(key.Value) > maxNameRunes:
			return fmt.Errorf("policy: modules[%d] (line %d): name is longer than %d characters", index, key.Line, maxNameRunes)
		case value.Kind != yaml.MappingNode:
			return fmt.Errorf("policy: modules[%d] (line %d) is not a mapping", index, key.Line)
		}
		var entry struct {
			Layer yaml.Node `yaml:"layer"`
		}
		if err := value.Decode(&entry); err != nil {
			return fmt.Errorf("policy: modules[%d] (line %d) is not readable", index, key.Line)
		}
		layer, ok := scalarText(&entry.Layer)
		if !ok {
			return fmt.Errorf("policy: modules[%d].layer (line %d) is not a scalar", index, key.Line)
		}
		c.seen[key.Value] = true
		c.out = append(c.out, ModuleDef{Name: key.Value, Layer: layer, Line: key.Line})
	}

	for _, merge := range merges {
		sources := []*yaml.Node{merge}
		if merge.Kind == yaml.SequenceNode {
			sources = merge.Content
		}
		for _, source := range sources {
			source = resolve(source)
			if source.Kind != yaml.MappingNode {
				return errors.New("policy: modules merges a value that is not a mapping")
			}
			if err := c.collect(source, true); err != nil {
				return err
			}
		}
	}
	return nil
}

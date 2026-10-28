package settings

import (
	"bytes"
	"errors"
	"fmt"
	"io"
	"regexp"
	"strings"
	"unicode/utf8"

	"go.yaml.in/yaml/v3"
)

const (
	maxFileBytes   = 65536
	maxOwners      = 100
	maxLoginLen    = 39 // GitHub's login limit
	maxWorkflowLen = 255

	tagString  = "!!str"
	tagInteger = "!!int"

	// scopeDocument takes the place of the key in errors about the file as a whole.
	scopeDocument = "document"
)

var (
	loginRule    = regexp.MustCompile(`^[A-Za-z0-9-]+$`)
	agentRule    = regexp.MustCompile(`^[A-Za-z0-9-]+(\[bot\])?$`)
	teamRule     = regexp.MustCompile(`^[A-Za-z0-9-]+/[A-Za-z0-9._-]+$`)
	workflowRule = regexp.MustCompile(`^\.github/workflows/[A-Za-z0-9._-]+\.ya?ml$`)
)

// ruleError names the key and the broken rule. It never carries text from the file:
// the file is customer input and the error may reach logs and pull request comments.
func ruleError(key, rule string) error {
	return fmt.Errorf("settings: %s: %s", key, rule)
}

// Parse strictly decodes and validates a settings file. Unknown keys are errors.
func Parse(raw []byte) (s Settings, err error) {
	// The YAML library is the only code here that can panic on hostile input. Fail closed if it does.
	defer func() {
		if recover() != nil {
			s, err = Settings{}, ruleError(scopeDocument, "not valid YAML")
		}
	}()

	root, err := decodeRoot(raw)
	if err != nil {
		return Settings{}, err
	}
	fields, err := rootFields(root)
	if err != nil {
		return Settings{}, err
	}

	s.Version, err = parseVersion(fields[keyVersion])
	if err != nil {
		return Settings{}, err
	}
	s.Owners, err = parseOwners(fields[keyOwners])
	if err != nil {
		return Settings{}, err
	}
	s.Agents, err = parseAgents(fields[keyAgents])
	if err != nil {
		return Settings{}, err
	}
	gate, err := parseMode(fields[keyGate], keyGate, string(GateAdvisory), string(GateRequired))
	if err != nil {
		return Settings{}, err
	}
	s.Gate = GateMode(gate)
	s.Workflow, err = parseWorkflow(fields[keyWorkflow])
	if err != nil {
		return Settings{}, err
	}
	dataMode, err := parseMode(fields[keyDataMode], keyDataMode, string(DataCustomer), string(DataManaged))
	if err != nil {
		return Settings{}, err
	}
	s.DataMode = DataMode(dataMode)
	return s, nil
}

// decodeRoot applies the document rules and returns the root node of the one document.
// The file is decoded to nodes, not to Settings: only nodes show aliases, a key that is
// present but empty, and the difference between 1 and "1".
func decodeRoot(raw []byte) (*yaml.Node, error) {
	if len(raw) > maxFileBytes {
		return nil, ruleError(scopeDocument, fmt.Sprintf("larger than %d bytes", maxFileBytes))
	}
	if !utf8.Valid(raw) {
		return nil, ruleError(scopeDocument, "not valid UTF-8")
	}

	dec := yaml.NewDecoder(bytes.NewReader(raw))
	dec.KnownFields(true)
	var doc, extra yaml.Node
	// Library errors quote the file, so their text is dropped.
	switch err := dec.Decode(&doc); {
	case errors.Is(err, io.EOF):
		return nil, ruleError(scopeDocument, "empty file")
	case err != nil:
		return nil, ruleError(scopeDocument, "not valid YAML")
	}
	if err := dec.Decode(&extra); !errors.Is(err, io.EOF) {
		return nil, ruleError(scopeDocument, "must hold exactly one YAML document")
	}

	if doc.Kind != yaml.DocumentNode || len(doc.Content) != 1 {
		return nil, ruleError(scopeDocument, "not valid YAML")
	}
	if usesAnchors(&doc) {
		return nil, ruleError(scopeDocument, "anchors and aliases are not allowed")
	}
	return doc.Content[0], nil
}

func usesAnchors(n *yaml.Node) bool {
	if n.Kind == yaml.AliasNode || n.Anchor != "" {
		return true
	}
	for _, child := range n.Content {
		if usesAnchors(child) {
			return true
		}
	}
	return false
}

// rootFields returns the value node of every key that is present.
func rootFields(root *yaml.Node) (map[string]*yaml.Node, error) {
	if root.Kind != yaml.MappingNode {
		return nil, ruleError(scopeDocument, "the root must be a mapping")
	}
	fields := make(map[string]*yaml.Node, len(root.Content)/2)
	for i := 0; i+1 < len(root.Content); i += 2 {
		key, value := root.Content[i], root.Content[i+1]
		if key.Kind != yaml.ScalarNode || key.ShortTag() != tagString {
			return nil, ruleError(scopeDocument, "unknown key")
		}
		switch key.Value {
		case keyVersion, keyOwners, keyAgents, keyGate, keyWorkflow, keyDataMode:
		default:
			return nil, ruleError(scopeDocument, "unknown key")
		}
		if _, dup := fields[key.Value]; dup {
			return nil, ruleError(key.Value, "duplicate key")
		}
		fields[key.Value] = value
	}
	return fields, nil
}

func parseVersion(n *yaml.Node) (int, error) {
	if n == nil {
		return 0, ruleError(keyVersion, "required")
	}
	if n.Kind != yaml.ScalarNode || n.ShortTag() != tagInteger || n.Value != "1" {
		return 0, ruleError(keyVersion, "must be the integer 1")
	}
	return 1, nil
}

func parseString(n *yaml.Node, key, rule string) (string, error) {
	if n.Kind != yaml.ScalarNode || n.ShortTag() != tagString {
		return "", ruleError(key, rule)
	}
	return n.Value, nil
}

func parseOwners(n *yaml.Node) ([]string, error) {
	if n == nil {
		return nil, ruleError(keyOwners, "required")
	}
	if n.Kind != yaml.SequenceNode {
		return nil, ruleError(keyOwners, "must be a list")
	}
	if len(n.Content) == 0 {
		return nil, ruleError(keyOwners, "must name at least one owner")
	}
	if len(n.Content) > maxOwners {
		return nil, ruleError(keyOwners, fmt.Sprintf("at most %d entries", maxOwners))
	}

	const form = `each entry must be a login or "org/team-slug", as a string without "@"`
	owners := make([]string, 0, len(n.Content))
	seen := make(map[string]bool, len(n.Content))
	for _, entry := range n.Content {
		owner, err := parseString(entry, keyOwners, form)
		if err != nil {
			return nil, err
		}
		isLogin := loginRule.MatchString(owner) && len(owner) <= maxLoginLen
		if !isLogin && !teamRule.MatchString(owner) {
			return nil, ruleError(keyOwners, form)
		}
		folded := strings.ToLower(owner)
		if seen[folded] {
			return nil, ruleError(keyOwners, "duplicate entry, ignoring case")
		}
		seen[folded] = true
		owners = append(owners, owner)
	}
	return owners, nil
}

// parseAgents returns nil for an absent key and for an empty list, so that Render, which leaves an empty list out,
// keeps the round trip. A login that is also an owner is not an error: a broken settings file stops the gate for every
// pull request, and IsAgent wins over the owner list anyway.
func parseAgents(n *yaml.Node) ([]string, error) {
	if n == nil {
		return nil, nil
	}
	if n.Kind != yaml.SequenceNode {
		return nil, ruleError(keyAgents, "must be a list")
	}
	if len(n.Content) > maxOwners {
		return nil, ruleError(keyAgents, fmt.Sprintf("at most %d entries", maxOwners))
	}

	const form = `each entry must be a login or "app-slug[bot]", as a string without "@"; teams are not allowed`
	var agents []string
	seen := make(map[string]bool, len(n.Content))
	for _, entry := range n.Content {
		agent, err := parseString(entry, keyAgents, form)
		if err != nil {
			return nil, err
		}
		if !agentRule.MatchString(agent) || len(strings.TrimSuffix(agent, botSuffix)) > maxLoginLen {
			return nil, ruleError(keyAgents, form)
		}
		id := agentIdentity(agent)
		if seen[id] {
			return nil, ruleError(keyAgents, `duplicate entry, ignoring case and "[bot]"`)
		}
		seen[id] = true
		agents = append(agents, agent)
	}
	return agents, nil
}

// parseMode returns def for an absent key. A key that is present must hold def or other exactly.
func parseMode(n *yaml.Node, key, def, other string) (string, error) {
	if n == nil {
		return def, nil
	}
	rule := fmt.Sprintf("must be %q or %q", def, other)
	mode, err := parseString(n, key, rule)
	if err != nil {
		return "", err
	}
	if mode != def && mode != other {
		return "", ruleError(key, rule)
	}
	return mode, nil
}

func parseWorkflow(n *yaml.Node) (string, error) {
	if n == nil {
		return "", ruleError(keyWorkflow, "required")
	}
	rule := fmt.Sprintf("must be a .yml or .yaml file directly under .github/workflows/, at most %d characters", maxWorkflowLen)
	workflow, err := parseString(n, keyWorkflow, rule)
	if err != nil {
		return "", err
	}
	if len(workflow) > maxWorkflowLen || !workflowRule.MatchString(workflow) {
		return "", ruleError(keyWorkflow, rule)
	}
	return workflow, nil
}

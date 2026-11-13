package policy3

import (
	"fmt"
	"regexp"
	"slices"
	"strconv"
	"strings"

	"go.yaml.in/yaml/v3"
)

var diagnosticLine = regexp.MustCompile("(?:line )([0-9]+): (.*)")
var diagnosticRule = regexp.MustCompile(`(rules\[[0-9]+\]) \([^\n]*\)`)

// Diagnostics renders every decoder problem in the record's location and wording.
func Diagnostics(data []byte) []string {
	_, err := Decode(data)
	if err == nil {
		return []string{}
	}
	var doc yaml.Node
	_ = yaml.Unmarshal(data, &doc)
	lines := map[string]int{}
	if len(doc.Content) > 0 {
		diagnosticPaths(doc.Content[0], "", lines)
	}
	type problem struct {
		line int
		text string
	}
	var list []problem
	for _, message := range strings.Split(err.Error(), "\n") {
		message = strings.TrimSpace(message)
		if message == "" || message == "yaml: unmarshal errors:" {
			continue
		}
		line := 1
		if m := diagnosticLine.FindStringSubmatch(message); m != nil {
			line, _ = strconv.Atoi(m[1])
			message = m[2]
		} else {
			path, _, _ := strings.Cut(diagnosticRule.ReplaceAllString(message, "${1}"), ":")
			for path != "" {
				if n := lines[path]; n > 0 {
					line = n
					break
				}
				i := strings.LastIndexAny(path, ".[")
				if i < 0 {
					break
				}
				path = path[:i]
			}
		}
		list = append(list, problem{line, message})
	}
	slices.SortStableFunc(list, func(a, b problem) int { return a.line - b.line })
	out := []string{}
	for _, p := range list {
		line := fmt.Sprintf(".archfit.yaml:%d: policy: %s", p.line, p.text)
		if !slices.Contains(out, line) {
			out = append(out, line)
		}
	}
	return out
}

func diagnosticPaths(n *yaml.Node, path string, lines map[string]int) {
	switch n.Kind {
	case yaml.MappingNode:
		for i := 0; i+1 < len(n.Content); i += 2 {
			key, value := n.Content[i], n.Content[i+1]
			next := key.Value
			if path != "" {
				next = path + "." + next
			}
			lines[next] = key.Line
			diagnosticPaths(value, next, lines)
		}
	case yaml.SequenceNode:
		for i, value := range n.Content {
			next := fmt.Sprintf("%s[%d]", path, i)
			lines[next] = value.Line
			diagnosticPaths(value, next, lines)
		}
	}
}

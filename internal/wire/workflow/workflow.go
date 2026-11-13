// Package workflow reads the Action calls of a GitHub Actions workflow file: the `uses:` references of its jobs and
// steps. Only those count. A string under `run:`, an environment value or a comment is not a call, so a workflow that
// mentions the Action in its shell text never looks like it runs it.
package workflow

import (
	"slices"
	"strings"

	"go.yaml.in/yaml/v3"
)

// UsesRefs returns every `uses:` reference of the workflow's jobs and steps. A file that is not YAML has none.
func UsesRefs(raw []byte) []string {
	var doc struct {
		Jobs map[string]struct {
			Uses  string `yaml:"uses"`
			Steps []struct {
				Uses string `yaml:"uses"`
			} `yaml:"steps"`
		} `yaml:"jobs"`
	}
	if yaml.Unmarshal(raw, &doc) != nil {
		return nil
	}
	var out []string
	for _, job := range doc.Jobs {
		if job.Uses != "" {
			out = append(out, job.Uses)
		}
		for _, step := range job.Steps {
			if step.Uses != "" {
				out = append(out, step.Uses)
			}
		}
	}
	slices.Sort(out)
	return out
}

// ActionRef returns the ref of a `uses:` reference to the Archfit Action (`<repository>@<ref>`), compared without
// case as GitHub does, and whether the reference is one.
func ActionRef(uses, repository string) (string, bool) {
	prefix := strings.ToLower(repository) + "@"
	if !strings.HasPrefix(strings.ToLower(uses), prefix) {
		return "", false
	}
	return uses[len(prefix):], true
}

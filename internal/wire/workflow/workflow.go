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

// ActionEndpoint returns the first non-empty `endpoint` input, in job-name order, of a step that calls repository,
// and whether there is one. A step without the input sends nothing, so it does not hide a later one. A file that is
// not YAML has none.
func ActionEndpoint(raw []byte, repository string) (string, bool) {
	var doc struct {
		Jobs map[string]struct {
			Steps []struct {
				Uses string               `yaml:"uses"`
				With map[string]yaml.Node `yaml:"with"` // a non-scalar input of another step still decodes
			} `yaml:"steps"`
		} `yaml:"jobs"`
	}
	if yaml.Unmarshal(raw, &doc) != nil {
		return "", false
	}
	names := make([]string, 0, len(doc.Jobs))
	for name := range doc.Jobs {
		names = append(names, name)
	}
	slices.Sort(names)
	for _, name := range names {
		for _, step := range doc.Jobs[name].Steps {
			if _, ok := ActionRef(step.Uses, repository); !ok {
				continue
			}
			if in := step.With["endpoint"]; in.Kind == yaml.ScalarNode && in.Value != "" {
				return in.Value, true
			}
		}
	}
	return "", false
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

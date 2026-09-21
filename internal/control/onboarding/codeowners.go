package onboarding

import (
	"bytes"
	"context"
	"fmt"
	"strings"

	"github.com/alexei-led/archfit-app/internal/github/api"
	"github.com/alexei-led/archfit-app/internal/wire/policy"
	"github.com/alexei-led/archfit-app/internal/wire/settings"
)

const codeownersHeader = "# Archfit protected paths"

// GitHub reads the first CODEOWNERS it finds in this order; a new file goes to the first location.
var codeownersLocations = []string{".github/CODEOWNERS", "CODEOWNERS", "docs/CODEOWNERS"}

// protectedPaths are the five files whose change needs an architecture owner's approval.
func protectedPaths() []string {
	return []string{"/" + policy.PolicyPath, "/" + policy.BaselinePath, "/" + policy.LabelsPath, "/" + settings.Path, "/" + workflowPath}
}

// codeowners returns the CODEOWNERS file of the default branch with the protected paths appended.
func (s *service) codeowners(ctx context.Context, r api.Repo, owners []string) (string, []byte, error) {
	for _, path := range codeownersLocations {
		raw, found, err := s.file(ctx, r, path)
		if err != nil {
			return "", nil, fmt.Errorf("onboarding: read %s: %w", path, err)
		}
		if found {
			return path, appendCodeowners(raw, owners), nil
		}
	}
	return codeownersLocations[0], appendCodeowners(nil, owners), nil
}

// appendCodeowners keeps the existing bytes unchanged and adds only the lines that are missing.
// Later CODEOWNERS lines win, so appended entries take precedence over older patterns.
func appendCodeowners(existing []byte, owners []string) []byte {
	present := map[string]bool{}
	for line := range strings.Lines(string(existing)) {
		present[strings.TrimRight(line, "\r\n")] = true
	}
	var add []string
	for _, path := range protectedPaths() {
		if line := path + " @" + strings.Join(owners, " @"); !present[line] {
			add = append(add, line)
		}
	}
	if len(add) == 0 {
		return existing
	}
	if !present[codeownersHeader] {
		add = append([]string{codeownersHeader}, add...)
	}
	out := bytes.Clone(existing)
	if len(out) > 0 && out[len(out)-1] != '\n' {
		out = append(out, '\n')
	}
	return append(out, strings.Join(add, "\n")+"\n"...)
}

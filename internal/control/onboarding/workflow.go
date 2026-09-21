package onboarding

import (
	"bytes"
	_ "embed"
	"strconv"
	"text/template"

	"github.com/alexei-led/archfit-app/internal/wire/manifest"
)

//go:embed templates/archfit.yaml.tmpl
var workflowTemplate string

// renderWorkflow pins the Action commit, the engine version and the image digest of e.
func renderWorkflow(e manifest.Entry, defaultBranch, baseURL string) ([]byte, error) {
	// "[[ ]]" delimiters keep the GitHub "${{ }}" expressions of the template literal.
	// strconv.Quote yields a double-quoted scalar that YAML reads as the same string.
	t, err := template.New("workflow").Delims("[[", "]]").
		Funcs(template.FuncMap{"quote": strconv.Quote}).
		Option("missingkey=error").
		Parse(workflowTemplate)
	if err != nil {
		return nil, err
	}
	var out bytes.Buffer
	err = t.Execute(&out, struct {
		manifest.Entry
		DefaultBranch, BaseURL string
	}{e, defaultBranch, baseURL})
	return out.Bytes(), err
}

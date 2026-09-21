package github_test

import (
	"go/ast"
	"go/parser"
	"go/token"
	"os"
	"os/exec"
	"slices"
	"strings"
	"testing"
)

const (
	modulePath = "github.com/alexei-led/archfit-app"
	branchPath = modulePath + "/internal/github"
)

func TestGithubParentExportsNothing(t *testing.T) {
	entries, err := os.ReadDir(".")
	if err != nil {
		t.Fatal(err)
	}
	fset := token.NewFileSet()
	for _, e := range entries {
		name := e.Name()
		if e.IsDir() || !strings.HasSuffix(name, ".go") {
			continue
		}
		file, err := parser.ParseFile(fset, name, nil, parser.SkipObjectResolution)
		if err != nil {
			t.Fatal(err)
		}
		if strings.HasSuffix(name, "_test.go") {
			if file.Name.Name != "github_test" {
				t.Errorf("%s: package %s, want github_test", name, file.Name.Name)
			}
			continue
		}
		if name != "doc.go" {
			t.Errorf("%s: the parent holds no code beyond doc.go", name)
		}
		if len(file.Imports) != 0 {
			t.Errorf("%s: the parent imports nothing", name)
		}
		if ast.FileExports(file) {
			t.Errorf("%s: declares exported identifiers", name)
		}
	}
}

func goList(t *testing.T, args ...string) []string {
	t.Helper()
	out, err := exec.Command("go", append([]string{"list"}, args...)...).Output()
	if err != nil {
		t.Fatalf("go list %v: %v", args, err)
	}
	// One package per line: a test variant reads "path [path.test]", so fields would split it.
	var lines []string
	for line := range strings.Lines(string(out)) {
		if line = strings.TrimSpace(line); line != "" {
			lines = append(lines, line)
		}
	}
	return lines
}

func TestGithubImportGraph(t *testing.T) {
	const nonStandard = "{{if not .Standard}}{{.ImportPath}}{{end}}"
	for child, allowed := range map[string][]string{
		"auth":    {branchPath + "/auth"},
		"api":     {branchPath + "/api", branchPath + "/auth"},
		"webhook": {branchPath + "/webhook"},
		"oidc":    {branchPath + "/oidc"},
	} {
		t.Run(child, func(t *testing.T) {
			// The dependency closure outside the standard library is the child itself, plus auth under api.
			got := goList(t, "-deps", "-f", nonStandard, "./"+child)
			slices.Sort(got)
			if !slices.Equal(got, allowed) {
				t.Errorf("non-standard dependencies = %v, want %v", got, allowed)
			}
		})
	}

	// Tests included, nothing under the branch reaches outside it: no wire, store, gate, control, app, no third party.
	t.Run("branch with tests", func(t *testing.T) {
		for _, dep := range goList(t, "-deps", "-test", "-f", nonStandard, "./...") {
			if !strings.HasPrefix(dep, branchPath) {
				t.Errorf("internal/github depends on %s", dep)
			}
		}
	})
}

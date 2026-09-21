package policy

import (
	"os/exec"
	"strings"
	"testing"
)

func goList(t *testing.T, args ...string) []string {
	t.Helper()
	out, err := exec.Command("go", append([]string{"list"}, args...)...).Output()
	if err != nil {
		t.Fatalf("go list: %v", err)
	}
	return strings.Fields(string(out))
}

func TestPackageImports(t *testing.T) {
	if _, err := exec.LookPath("go"); err != nil {
		t.Skip("go tool not on PATH")
	}
	const (
		self    = "github.com/alexei-led/archfit-app/internal/wire/policy"
		yamlLib = "go.yaml.in/yaml/v3"
	)

	for _, dep := range goList(t, "-deps", ".") {
		root, _, _ := strings.Cut(dep, "/")
		thirdParty := strings.Contains(root, ".")
		switch {
		case dep == self:
		case thirdParty && dep != yamlLib:
			t.Errorf("depends on %s, want only the standard library and %s", dep, yamlLib)
		case root == "net" || root == "log":
			t.Errorf("depends on %s", dep)
		}
	}

	// `os` is reachable through fmt and the YAML library, so it is banned as a direct import only.
	for _, imp := range goList(t, "-f", `{{join .Imports "\n"}}`, ".") {
		if root, _, _ := strings.Cut(imp, "/"); root == "net" || root == "os" || root == "log" {
			t.Errorf("imports %s", imp)
		}
	}
}

package settings

import (
	"os/exec"
	"strings"
	"testing"
)

const (
	selfPackage = "github.com/alexei-led/archfit-app/internal/wire/settings"
	yamlPackage = "go.yaml.in/yaml/v3"
)

func goList(t *testing.T, args ...string) []string {
	t.Helper()
	if _, err := exec.LookPath("go"); err != nil {
		t.Skip("go tool not found")
	}
	out, err := exec.Command("go", append([]string{"list"}, args...)...).Output()
	if err != nil {
		t.Fatalf("go list %v: %v", args, err)
	}
	return strings.Fields(string(out))
}

func TestPackageImports(t *testing.T) {
	for _, dep := range goList(t, "-deps", "-f", "{{if not .Standard}}{{.ImportPath}}{{end}}", ".") {
		if dep != selfPackage && dep != yamlPackage {
			t.Errorf("dependency outside the standard library and the YAML library: %s", dep)
		}
	}

	assertNone(t, goList(t, "-deps", "."), "net", "log")
	// fmt reaches os inside the standard library, so os is banned as a direct import only.
	assertNone(t, goList(t, "-f", `{{join .Imports "\n"}}`, "."), "net", "log", "os")
}

func assertNone(t *testing.T, pkgs []string, banned ...string) {
	t.Helper()
	for _, pkg := range pkgs {
		for _, b := range banned {
			if pkg == b || strings.HasPrefix(pkg, b+"/") {
				t.Errorf("banned package %s", pkg)
			}
		}
	}
}

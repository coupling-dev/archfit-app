package wire_test

import (
	"strings"
	"testing"
)

const (
	modulePath = "github.com/alexei-led/archfit-app/"
	yamlLib    = "go.yaml.in/yaml/v3"
)

func isThirdParty(pkg string) bool {
	root, _, _ := strings.Cut(pkg, "/")
	return strings.Contains(root, ".") && !strings.HasPrefix(pkg, modulePath)
}

func TestChildrenDoNotImportEachOther(t *testing.T) {
	for _, child := range wireChildren {
		self := wirePackage + "/" + child
		for _, dep := range goList(t, "-deps", "./"+child) {
			if strings.HasPrefix(dep, modulePath) && dep != self {
				t.Errorf("%s depends on internal package %s", child, dep)
			}
		}
	}
}

func TestWireImportsNoIO(t *testing.T) {
	yamlAllowed := map[string]bool{"policy3": true, "workflow": true}
	bannedAnywhere := map[string]bool{"net": true, "net/http": true, "database/sql": true, "log": true, "log/slog": true}
	// `os` is reachable through fmt and the YAML library, so `os` and `os/exec` are banned as direct imports.
	bannedDirect := map[string]bool{"os": true, "os/exec": true}

	for _, pkg := range append([]string{"."}, wireChildren...) {
		path := "./" + pkg
		for _, dep := range goList(t, "-deps", path) {
			switch {
			case bannedAnywhere[dep]:
				t.Errorf("%s depends on %s", pkg, dep)
			case isThirdParty(dep) && (dep != yamlLib || !yamlAllowed[pkg]):
				t.Errorf("%s depends on third-party %s", pkg, dep)
			}
		}
		for _, imp := range goList(t, "-f", `{{join .Imports "\n"}}`, path) {
			if bannedDirect[imp] || bannedAnywhere[imp] {
				t.Errorf("%s imports %s", pkg, imp)
			}
		}
	}
}

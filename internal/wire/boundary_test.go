package wire_test

import (
	"encoding/json"
	"fmt"
	"strings"
	"testing"

	"github.com/alexei-led/archfit-app/internal/wire/report"
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
	yamlAllowed := map[string]bool{"settings": true, "policy": true}
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

func TestDecodersNeverEchoSeededValues(t *testing.T) {
	checked := 0
	for _, f := range loadCorpus(t) {
		if f.Folder != folderLeakage && f.Expect.Decode != decodeError {
			continue
		}
		_, _, err := decodeFixture(t, f)
		if err == nil {
			continue
		}
		checked++
		if strings.Contains(err.Error(), leakMarker) {
			t.Errorf("%s: the error text echoes the seeded marker", f.id())
		}
	}
	if checked == 0 {
		t.Error("no rejecting fixture was checked")
	}
}

func TestSourceIdentifiersNeverSurviveDecoding(t *testing.T) {
	var surviving fixture
	for _, f := range inFolder(loadCorpus(t), folderLeakage) {
		if f.Name == survivingLeakageFixture {
			surviving = f
		}
	}
	if surviving.Name == "" {
		t.Fatalf("leakage fixture %q is missing", survivingLeakageFixture)
	}
	raw := surviving.bytes(t)
	for _, carrier := range []string{`"declarations"`, `"matched_by"`, `"hypothesis"`} {
		if !strings.Contains(string(raw), carrier) {
			t.Fatalf("the fixture has no %s key to carry the marker", carrier)
		}
	}
	if got := strings.Count(string(raw), leakMarker); got < 3 {
		t.Fatalf("marker occurrences = %d, want one per carrier at least", got)
	}

	r, err := report.Decode(raw, fixtureEngine)
	if err != nil {
		t.Fatalf("the surviving fixture must decode: %v", err)
	}
	encoded, err := json.Marshal(r)
	if err != nil {
		t.Fatal(err)
	}
	for name, text := range map[string]string{
		"JSON": string(encoded), "%+v dump": fmt.Sprintf("%+v", r), "%#v dump": fmt.Sprintf("%#v", r),
	} {
		if strings.Contains(text, leakMarker) {
			t.Errorf("the marker survives in the %s of the decoded report", name)
		}
	}
}

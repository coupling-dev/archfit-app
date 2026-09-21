package graph

import (
	"encoding/json"
	"fmt"
	"os/exec"
	"strings"
	"testing"

	"github.com/alexei-led/archfit-app/internal/wire/report"
)

const wirePrefix = "github.com/alexei-led/archfit-app/internal/wire/"

func TestBuildNeverFails(t *testing.T) {
	const modules, seams = 5000, 50000
	var large strings.Builder
	large.WriteString("modules:\n")
	for i := range modules {
		fmt.Fprintf(&large, "  m%04d:\n    layer: l%d\n", i, i%7)
	}
	largeSeams := make([]report.Seam, seams)
	for i := range largeSeams {
		largeSeams[i] = seam(fmt.Sprintf("m%04d", i%modules), fmt.Sprintf("m%04d", (i*7+i/modules)%modules), 1)
	}

	tests := []struct {
		name      string
		rep       report.Report
		policy    []byte
		wantNodes int
		wantStale bool
	}{
		{name: "zero report", policy: []byte{}, wantStale: true},
		{name: "nil policy", rep: reportFor("", []report.Seam{seam("a", "b", 1)}, nil), wantNodes: 2, wantStale: true},
		{name: "5000 modules with 50000 seams", rep: reportFor(large.String(), largeSeams, nil), policy: []byte(large.String()), wantNodes: modules},
		{name: "only blank lines", policy: []byte("\n\n   \n\n"), wantStale: true},
		{name: "binary bytes", policy: []byte{0x00, 0xff, 0xfe, 0x89, 'P', 'N', 'G', 0x0d, 0x0a, 0x1a, 0x00}, wantStale: true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := Build(tt.rep, tt.policy)
			if len(got.Nodes) != tt.wantNodes {
				t.Errorf("len(Nodes) = %d, want %d", len(got.Nodes), tt.wantNodes)
			}
			if got.Stale != tt.wantStale {
				t.Errorf("Stale = %v, want %v", got.Stale, tt.wantStale)
			}
		})
	}

	zero := Build(report.Report{}, nil)
	if len(zero.Nodes) != 0 || len(zero.Links) != 0 || !zero.Stale {
		t.Errorf("zero input = %+v, want an empty, stale view", zero)
	}
}

func TestViewCarriesNoPaths(t *testing.T) {
	const fromPath, toPath, locFile = "src/secret/from_path.go", "src/secret/to_path.go", "src/secret/location_file.go"
	leaky := finding("gate", "new", "api", "db")
	leaky.Edge.From.Path = fromPath
	leaky.Edge.To.Path = toPath
	leaky.Locations = []report.Location{{File: locFile, Line: 12}}
	pathOnly := finding("gate", "new", "", "")
	pathOnly.Edge.From.Path = fromPath
	pathOnly.Edge.To.Path = toPath

	rep := reportFor(threeModules, []report.Seam{seam("api", "db", 1)}, []report.Finding{leaky, pathOnly})
	out, err := json.Marshal(Build(rep, []byte(threeModules)))
	if err != nil {
		t.Fatalf("marshal view: %v", err)
	}
	for _, p := range []string{fromPath, toPath, locFile, "src/secret"} {
		if strings.Contains(string(out), p) {
			t.Errorf("view JSON contains path %q: %s", p, out)
		}
	}
}

func TestGraphHasNoYAMLCodeAndDoesNoIO(t *testing.T) {
	out, err := exec.Command("go", "list", "-f", `{{join .Imports "\n"}}`, ".").Output()
	if err != nil {
		t.Fatalf("go list: %v", err)
	}
	imports := strings.Fields(string(out))
	if len(imports) == 0 {
		t.Fatal("go list reported no imports")
	}
	for _, imp := range imports {
		switch {
		case imp == wirePrefix+"report", imp == wirePrefix+"policy":
		case strings.Contains(imp, "yaml"):
			t.Errorf("YAML import %q", imp)
		case strings.Contains(strings.SplitN(imp, "/", 2)[0], "."):
			t.Errorf("import %q is not the standard library, wire/report or wire/policy", imp)
		case imp == "os", strings.HasPrefix(imp, "os/"), imp == "net", strings.HasPrefix(imp, "net/"),
			imp == "io/ioutil", imp == "log", strings.HasPrefix(imp, "log/"), imp == "time", imp == "syscall":
			t.Errorf("import %q does I/O, logging or reads the clock", imp)
		}
	}

	// Transitively, everything outside the standard library is a wire package or the YAML library wire/policy uses.
	deps, err := exec.Command("go", "list", "-deps", "-f", `{{if not .Standard}}{{.ImportPath}}{{end}}`, ".").Output()
	if err != nil {
		t.Fatalf("go list -deps: %v", err)
	}
	self := strings.TrimSuffix(wirePrefix, "wire/") + "control/ui/graph"
	for _, dep := range strings.Fields(string(deps)) {
		if dep != self && !strings.HasPrefix(dep, wirePrefix) && !strings.HasPrefix(dep, "go.yaml.in/yaml/") {
			t.Errorf("unexpected non-standard dependency %q", dep)
		}
	}
}

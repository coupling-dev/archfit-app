package graph

import (
	"bytes"
	"go/ast"
	"go/parser"
	"go/token"
	"os"
	"reflect"
	"slices"
	"strings"
	"testing"

	"github.com/alexei-led/archfit-app/internal/wire/policy"
	"github.com/alexei-led/archfit-app/internal/wire/report"
)

func TestInvalidYAMLMarksGraphStale(t *testing.T) {
	rep := reportFor(threeModules,
		[]report.Seam{seam("api", "db", 2), seam("ui", "api", 1)},
		[]report.Finding{finding("gate", "new", "ui", "db")})
	wantLinks := []Link{link("api", "db", 2, StatusObserved), link("ui", "api", 1, StatusObserved), link("ui", "db", 0, StatusViolation)}

	broken := strings.Replace(threeModules, "    paths: [api/**]", "   paths: [api/**]", 1)
	if _, err := policy.Modules([]byte(broken)); err == nil {
		t.Fatal("the broken indent still parses; the scenario needs invalid YAML")
	}

	got := Build(rep, []byte(broken))
	if !got.Stale {
		t.Error("Stale = false for invalid YAML")
	}
	if !reflect.DeepEqual(got.Links, wantLinks) {
		t.Errorf("Links = %+v, want %+v", got.Links, wantLinks)
	}
	if want := []Node{{"api", "", 0}, {"db", "", 0}, {"ui", "", 0}}; !reflect.DeepEqual(got.Nodes, want) {
		t.Errorf("Nodes = %+v, want the link endpoints %+v", got.Nodes, want)
	}

	// Invalid YAML is stale even when the report was computed from these exact bytes.
	if !Build(reportFor(broken, nil, nil), []byte(broken)).Stale {
		t.Error("Stale = false for invalid YAML whose digest matches")
	}

	lastValid := Build(rep, []byte(threeModules))
	if want := []Node{{"api", "app", 3}, {"db", "infra", 6}, {"ui", "app", 9}}; !reflect.DeepEqual(lastValid.Nodes, want) {
		t.Errorf("last valid Nodes = %+v, want %+v", lastValid.Nodes, want)
	}
	if lastValid.Stale {
		t.Error("last valid text with the report's digest is stale")
	}
	if !Build(rep, []byte(threeModules+"# edited since\n")).Stale {
		t.Error("last valid text with another digest is not stale")
	}
}

func TestUnsupportedYAMLConstructsAreUntouched(t *testing.T) {
	text := []byte("# owned by platform\n" +
		"defaults: &defaults\n" +
		"  layer: app # inherited\n" +
		"modules:\n" +
		"  api: {layer: app, paths: [api/**, &shared 'pkg/shared/**']}\n" +
		"  db:\n" +
		"    <<: *defaults\n" +
		"    paths: [*shared]\n" +
		"rules: [] # none yet\n")
	before := bytes.Clone(text)

	got := Build(reportFor(string(text), []report.Seam{seam("api", "db", 1)}, nil), text)

	if !bytes.Equal(text, before) {
		t.Errorf("policy bytes changed:\n%s", text)
	}
	if len(got.Nodes) != 2 || got.Nodes[0].Name != "api" || got.Nodes[1].Name != "db" {
		t.Errorf("Nodes = %+v, want api and db", got.Nodes)
	}
	if got.Stale {
		t.Error("Stale = true for unchanged policy text")
	}

	if exported := exportedFuncs(t); !reflect.DeepEqual(exported, []string{"Build"}) {
		t.Errorf("exported functions = %v, want only Build: graph returns data and writes no text", exported)
	}
}

func TestViolationIsToldApartFromObserved(t *testing.T) {
	// The blocked fixture observes its forbidden dependency a -> b once. Observe it a second time, and
	// add one dependency b -> a that no finding names, observed once.
	rep := loadReport(t, blockedFixture)
	rep.Seams = append(rep.Seams, rep.Seams[0], seam("b", "a", 1))

	got := Build(rep, []byte(fixturePolicy))

	want := []Link{link("a", "b", 2, StatusViolation), link("b", "a", 1, StatusObserved)}
	if !reflect.DeepEqual(got.Links, want) {
		t.Errorf("Links = %+v, want %+v", got.Links, want)
	}
}

func TestBaselinedViolationIsNeverPermitted(t *testing.T) {
	// The same forbidden dependency once its finding is accepted by the baseline, then by a waiver.
	for _, status := range []string{"baseline", "waived"} {
		rep := loadReport(t, blockedFixture)
		for i := range rep.Findings {
			if rep.Findings[i].Kind == "gate" {
				rep.Findings[i].Status = status
			}
		}
		got := Build(rep, []byte(fixturePolicy))
		if want := []Link{link("a", "b", 1, StatusAccepted)}; !reflect.DeepEqual(got.Links, want) {
			t.Errorf("%s: Links = %+v, want %+v", status, got.Links, want)
		}
	}
}

func TestSelectingANodeFindsItsYAML(t *testing.T) {
	got := Build(loadReport(t, healthyFixture), []byte(fixturePolicy))
	if got.Stale {
		t.Error("Stale = true for the healthy fixture with its policy")
	}
	if len(got.Nodes) != 2 {
		t.Fatalf("Nodes = %+v, want the two declared modules", got.Nodes)
	}
	lines := strings.Split(fixturePolicy, "\n")
	for _, n := range got.Nodes {
		if n.PolicyLine < 1 || n.PolicyLine > len(lines) {
			t.Errorf("node %s PolicyLine = %d, outside the policy text", n.Name, n.PolicyLine)
			continue
		}
		if line := lines[n.PolicyLine-1]; strings.TrimSpace(line) != n.Name+":" {
			t.Errorf("node %s PolicyLine %d is %q, want the module's key", n.Name, n.PolicyLine, line)
		}
	}
	for _, l := range got.Links {
		if l.Status != StatusObserved {
			t.Errorf("healthy link %s -> %s has status %q, want observed", l.From, l.To, l.Status)
		}
	}
}

func exportedFuncs(t *testing.T) []string {
	t.Helper()
	entries, err := os.ReadDir(".")
	if err != nil {
		t.Fatalf("read package dir: %v", err)
	}
	var names []string
	for _, e := range entries {
		if !strings.HasSuffix(e.Name(), ".go") || strings.HasSuffix(e.Name(), "_test.go") {
			continue
		}
		file, err := parser.ParseFile(token.NewFileSet(), e.Name(), nil, parser.SkipObjectResolution)
		if err != nil {
			t.Fatalf("parse %s: %v", e.Name(), err)
		}
		for _, decl := range file.Decls {
			if fn, ok := decl.(*ast.FuncDecl); ok && fn.Name.IsExported() {
				names = append(names, fn.Name.Name)
			}
		}
	}
	slices.Sort(names)
	return names
}

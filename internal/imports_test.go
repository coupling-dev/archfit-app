package internal_test

import (
	"os/exec"
	"slices"
	"strings"
	"sync"
	"testing"
)

const (
	modulePath = "github.com/alexei-led/archfit-app"
	internal   = modulePath + "/internal/"
	yamlModule = "go.yaml.in/yaml/"
)

// goList runs `go list` at the module root and returns its non-empty output lines.
func goList(t *testing.T, args ...string) []string {
	t.Helper()
	cmd := exec.Command("go", append([]string{"list"}, args...)...)
	cmd.Dir = ".."
	out, err := cmd.Output()
	if err != nil {
		stderr := ""
		if ee, ok := err.(*exec.ExitError); ok { //nolint:errorlint // exec returns the concrete type
			stderr = string(ee.Stderr)
		}
		t.Fatalf("go list %v: %v\n%s", args, err, stderr)
	}
	var lines []string
	for line := range strings.SplitSeq(string(out), "\n") {
		if line = strings.TrimSpace(line); line != "" {
			lines = append(lines, line)
		}
	}
	return lines
}

// closure is the non-standard dependency closure of a pattern. Standard packages are never part of
// a rule: fmt imports os, and that says nothing about this design.
func closure(t *testing.T, pattern string) []string {
	t.Helper()
	return goList(t, "-deps", "-f", "{{if not .Standard}}{{.ImportPath}}{{end}}", pattern)
}

type graph struct {
	direct     map[string][]string // production package → its direct non-standard imports
	thirdParty map[string]bool
}

var (
	graphOnce sync.Once
	theGraph  graph
)

// importGraph lists the direct production imports of every package of the module once per test binary.
func importGraph(t *testing.T) graph {
	t.Helper()
	graphOnce.Do(func() {
		g := graph{direct: map[string][]string{}, thirdParty: map[string]bool{}}
		nonStandard := map[string]bool{}
		for _, p := range closure(t, "./...") {
			nonStandard[p] = true
			if !strings.HasPrefix(p, modulePath) {
				g.thirdParty[p] = true
			}
		}
		for _, line := range goList(t, "-f", `{{.ImportPath}} {{join .Imports " "}}`, "./...") {
			fields := strings.Fields(line)
			g.direct[fields[0]] = []string{}
			for _, imp := range fields[1:] {
				if nonStandard[imp] {
					g.direct[fields[0]] = append(g.direct[fields[0]], imp)
				}
			}
		}
		theGraph = g
	})
	if len(theGraph.direct) == 0 {
		t.Fatal("go list returned no packages")
	}
	return theGraph
}

// under reports whether pkg is the internal module at path or one of its descendants.
func under(pkg, path string) bool {
	return pkg == internal+path || strings.HasPrefix(pkg, internal+path+"/")
}

func TestGatePathImportsNothingForbidden(t *testing.T) {
	deps := closure(t, "./internal/gate/...")
	if !slices.Contains(deps, internal+"gate") || !slices.Contains(deps, internal+"gate/verdict") {
		t.Fatalf("the closure of ./internal/gate/... does not contain the gate packages: %v", deps)
	}
	forbidden := []string{"control", "app"}
	for _, dep := range deps {
		for _, f := range forbidden {
			if under(dep, f) {
				t.Errorf("the gate path reaches %s", dep)
			}
		}
		for _, word := range []string{"anthropic", "openai", "llm", "/assist", "/ui"} {
			if strings.Contains(strings.ToLower(dep), word) {
				t.Errorf("the gate path reaches an LLM or UI package: %s", dep)
			}
		}
	}
}

// seamImplementations are the packages allowed to import the parent that declares their interface: the
// implementations of a seam, and its conformance suite.
var seamImplementations = map[string]string{
	internal + "control/assist/anthropic": internal + "control/assist",
	internal + "queue/cloudtasks":         internal + "queue",
	internal + "queue/queuetest":          internal + "queue",
}

// importGroup is one row of the allowed-import table: allowed answers for non-standard imports only.
type importGroup struct {
	name    string
	member  func(pkg string) bool
	allowed func(pkg, imp string) bool
}

func TestAllowedImportMatrix(t *testing.T) {
	g := importGraph(t)
	groups := []importGroup{
		{"wire", func(p string) bool { return under(p, "wire") }, func(pkg, imp string) bool {
			return strings.HasPrefix(imp, yamlModule) && (pkg == internal+"wire/settings" || pkg == internal+"wire/policy")
		}},
		{"store", func(p string) bool { return p == internal+"store" }, func(string, string) bool { return false }},
		{"queue", func(p string) bool { return under(p, "queue") }, func(pkg, imp string) bool {
			return seamImplementations[pkg] == internal+"queue" && imp == internal+"queue"
		}},
		{"github", func(p string) bool { return under(p, "github") }, func(pkg, imp string) bool {
			return pkg == internal+"github/api" && imp == internal+"github/auth"
		}},
		{"gate", func(p string) bool { return under(p, "gate") }, func(_, imp string) bool {
			return under(imp, "wire") || under(imp, "github") || imp == internal+"store" || under(imp, "gate")
		}},
		{"control", func(p string) bool { return under(p, "control") }, func(_, imp string) bool {
			return under(imp, "wire") || under(imp, "github") || imp == internal+"store" ||
				imp == internal+"gate" || under(imp, "control")
		}},
		{"app", func(p string) bool { return under(p, "app") }, func(string, string) bool { return true }},
	}

	seen := map[string]int{}
	for pkg, imports := range g.direct {
		if !strings.HasPrefix(pkg, internal) {
			continue
		}
		var group *importGroup
		for i := range groups {
			if groups[i].member(pkg) {
				group = &groups[i]
			}
		}
		if group == nil {
			// A driver package under store/ is a platform seam implementation: it may import store and its SDK.
			if under(pkg, "store") {
				continue
			}
			t.Errorf("%s belongs to no package group of the allowed-import table", pkg)
			continue
		}
		seen[group.name]++
		for _, imp := range imports {
			if !group.allowed(pkg, imp) {
				t.Errorf("%s (group %s) imports %s", pkg, group.name, imp)
			}
			// A child never imports its parent, except a seam implementation and its declaring package.
			if strings.HasPrefix(pkg, imp+"/") && seamImplementations[pkg] != imp {
				t.Errorf("%s imports its ancestor %s", pkg, imp)
			}
		}
	}
	for _, grp := range groups {
		if seen[grp.name] == 0 {
			t.Errorf("no package found in group %s: the matrix checked nothing for it", grp.name)
		}
	}

	// Nobody outside gate imports gate/*; nobody outside control imports control/*, except app calling
	// the constructor of a platform implementation.
	for pkg, imports := range g.direct {
		for _, imp := range imports {
			if strings.HasPrefix(imp, internal+"gate/") && !under(pkg, "gate") {
				t.Errorf("%s imports %s: only the gate façade is published", pkg, imp)
			}
			if strings.HasPrefix(imp, internal+"control/") && !under(pkg, "control") {
				if _, platform := seamImplementations[imp]; !platform || !under(pkg, "app") {
					t.Errorf("%s imports %s: only the control façade and platform constructors are published", pkg, imp)
				}
			}
		}
	}

	// cmd/reporter is the thin entry point: app and nothing else.
	if got := g.direct[modulePath+"/cmd/reporter"]; !slices.Equal(got, []string{internal + "app"}) {
		t.Errorf("cmd/reporter imports %v; want only %sapp", got, internal)
	}
}

func TestOnlyPlatformSeamsTouchPlatformSDKs(t *testing.T) {
	g := importGraph(t)
	for pkg := range g.thirdParty {
		if !strings.HasPrefix(pkg, yamlModule) {
			t.Errorf("third-party package %s is in the build; docs/tech-stack.md allows one YAML parser only", pkg)
		}
	}
	yamlImporters := []string{}
	for pkg, imports := range g.direct {
		for _, imp := range imports {
			if !g.thirdParty[imp] {
				continue
			}
			if strings.HasPrefix(imp, yamlModule) {
				yamlImporters = append(yamlImporters, pkg)
				continue
			}
			// Any other SDK belongs to a Store, Sandbox or Provider implementation package.
			parent := pkg[:strings.LastIndex(pkg, "/")]
			if parent != internal+"store" && parent != internal+"control/onboarding" && parent != internal+"control/assist" {
				t.Errorf("%s imports the SDK %s outside a platform seam implementation", pkg, imp)
			}
		}
	}
	slices.Sort(yamlImporters)
	if want := []string{internal + "wire/policy", internal + "wire/settings"}; !slices.Equal(yamlImporters, want) {
		t.Errorf("YAML is imported by %v; want %v", yamlImporters, want)
	}
}

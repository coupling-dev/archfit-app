package wire_test

import (
	"go/ast"
	"go/parser"
	"go/token"
	"os/exec"
	"reflect"
	"sort"
	"strings"
	"testing"
)

const (
	wirePackage = "github.com/alexei-led/archfit-app/internal/wire"
)

var wireChildren = []string{"envelope", "manifest", "policy3", "request", "result", "wording", "workflow"}

func goList(t *testing.T, args ...string) []string {
	t.Helper()
	out, err := exec.Command("go", append([]string{"list"}, args...)...).Output() //nolint:gosec // fixed tool, fixed arguments
	if err != nil {
		t.Fatalf("go list %v: %v", args, err)
	}
	return strings.Fields(string(out))
}

func TestPublishedPackagesExist(t *testing.T) {
	got := goList(t, "./...")
	want := []string{wirePackage}
	for _, child := range wireChildren {
		want = append(want, wirePackage+"/"+child)
	}
	sort.Strings(want)
	sort.Strings(got)
	if !reflect.DeepEqual(got, want) {
		t.Errorf("packages\n got %v\nwant %v", got, want)
	}

	pkgs, err := parser.ParseDir(token.NewFileSet(), ".", nil, 0) //nolint:staticcheck // one directory, no type information needed
	if err != nil {
		t.Fatal(err)
	}
	parent, ok := pkgs["wire"]
	if !ok {
		t.Fatal("package wire has no non-test file")
	}
	for name, file := range parent.Files {
		if strings.HasSuffix(name, "_test.go") {
			continue
		}
		if len(file.Decls) != 0 || ast.FileExports(file) {
			t.Errorf("%s: the parent package holds no declarations", name)
		}
	}
}

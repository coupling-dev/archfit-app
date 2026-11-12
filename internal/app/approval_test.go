package app

import (
	"go/ast"
	"go/parser"
	"go/token"
	"io/fs"
	"maps"
	"net/http"
	"net/http/httptest"
	"os/exec"
	pathpkg "path"
	"path/filepath"
	"slices"
	"strconv"
	"strings"
	"testing"
)

// ownerDecisionPatterns are control/ui's authenticated decision routes, restated for the boundary test.
var ownerDecisionPatterns = []string{
	"POST /api/repos/{o}/{n}/pulls/{number}/approve",
	"POST /api/repos/{o}/{n}/pulls/{number}/request-changes",
	"POST /api/repos/{o}/{n}/pulls/{number}/withdraw",
	"POST /api/repos/{o}/{n}/pulls/{number}/override",
	"POST /api/repos/{o}/{n}/pulls/{number}/refresh-decision",
}

// repoRoot is the module root, two levels above this package.
var repoRoot = filepath.Join("..", "..")

// parsedFiles parses every non-test Go file of the module, keyed by its slash path from the root.
func parsedFiles(t *testing.T) map[string]*ast.File {
	t.Helper()
	files := map[string]*ast.File{}
	fset := token.NewFileSet()
	for _, top := range []string{"cmd", "internal"} {
		err := filepath.WalkDir(filepath.Join(repoRoot, top), func(path string, d fs.DirEntry, err error) error {
			if err != nil || d.IsDir() || !strings.HasSuffix(path, ".go") || strings.HasSuffix(path, "_test.go") {
				return err
			}
			f, err := parser.ParseFile(fset, path, nil, 0)
			if err != nil {
				return err
			}
			rel, _ := filepath.Rel(repoRoot, path)
			files[filepath.ToSlash(rel)] = f
			return nil
		})
		if err != nil {
			t.Fatal(err)
		}
	}
	return files
}

// selectorsNamed returns "<package dir>.<enclosing function>" for every use of a selector called name: a method or a
// field, not a name another package declares (policy3.Approve is an answer kind, not a call).
func selectorsNamed(files map[string]*ast.File, name string) []string {
	var out []string
	for path, f := range files {
		imported := map[string]bool{}
		for _, spec := range f.Imports {
			local := pathpkg.Base(strings.Trim(spec.Path.Value, `"`))
			if spec.Name != nil {
				local = spec.Name.Name
			}
			imported[local] = true
		}
		for _, decl := range f.Decls {
			fn, ok := decl.(*ast.FuncDecl)
			if !ok || fn.Body == nil {
				continue
			}
			ast.Inspect(fn.Body, func(n ast.Node) bool {
				sel, ok := n.(*ast.SelectorExpr)
				if !ok || sel.Sel.Name != name {
					return true
				}
				if x, ok := sel.X.(*ast.Ident); ok && x.Obj == nil && imported[x.Name] {
					return true
				}
				out = append(out, filepath.Dir(path)+"."+fn.Name.Name)
				return true
			})
		}
	}
	slices.Sort(out)
	return out
}

// routes returns the pattern and handler expression of every mux.Handle and mux.HandleFunc call in the file.
func routes(t *testing.T, f *ast.File) map[string]ast.Expr {
	t.Helper()
	out := map[string]ast.Expr{}
	ast.Inspect(f, func(n ast.Node) bool {
		call, ok := n.(*ast.CallExpr)
		if !ok || len(call.Args) != 2 {
			return true
		}
		sel, ok := call.Fun.(*ast.SelectorExpr)
		if !ok || (sel.Sel.Name != "Handle" && sel.Sel.Name != "HandleFunc") {
			return true
		}
		if lit, ok := call.Args[0].(*ast.BasicLit); ok && lit.Kind == token.STRING {
			pattern, err := strconv.Unquote(lit.Value)
			if err != nil {
				t.Fatal(err)
			}
			out[pattern] = call.Args[1]
		}
		return true
	})
	return out
}

func names(e ast.Expr) []string {
	var out []string
	ast.Inspect(e, func(n ast.Node) bool {
		if id, ok := n.(*ast.Ident); ok {
			out = append(out, id.Name)
		}
		return true
	})
	return out
}

// No owner-decision write or credential is reachable from the Action's OIDC uploads or the CLI; every action is
// reachable only behind the browser session of control/ui.
func TestOwnerDecisionIsReachableOnlyFromTheBrowserSession(t *testing.T) {
	files := parsedFiles(t)

	t.Run("App decisions are delegated through the gate façade", func(t *testing.T) {
		if got := selectorsNamed(files, "Approve"); len(got) != 0 {
			t.Errorf("legacy Approve bypass is still called from %v", got)
		}
		if got := selectorsNamed(files, "Decide"); !slices.Contains(got, "internal/control/ui.apiOwnerDecision") ||
			!slices.Contains(got, "internal/gate.Decide") || !slices.Contains(got, "internal/gate.Approve") {
			t.Errorf("decision call graph = %v; want the browser handler and gate façade only", got)
		}
		for path, f := range files {
			ast.Inspect(f, func(n ast.Node) bool {
				lit, ok := n.(*ast.BasicLit)
				if !ok {
					return true
				}
				for _, prefix := range []string{`"approval/`, `"owner-decision/`, `"review-request/`} {
					if strings.Contains(lit.Value, prefix) && filepath.Dir(path) != "internal/gate/inputs" {
						t.Errorf("%s names gate/inputs record prefix %s", path, prefix)
					}
				}
				return true
			})
		}
	})

	t.Run("control/ui mounts every owner decision route behind write/session/CSRF", func(t *testing.T) {
		ui := routes(t, files["internal/control/ui/ui.go"])
		for _, pattern := range ownerDecisionPatterns {
			expr, ok := ui[pattern]
			if !ok {
				t.Errorf("control/ui does not mount %s", pattern)
				continue
			}
			got := names(expr)
			if !slices.Contains(got, "apiRepo") || !slices.Contains(got, "permWrite") || !slices.Contains(got, "s") {
				t.Errorf("%s is served by %v, want signed-in write route", pattern, got)
			}
		}
	})

	t.Run("no /v1/ route of the app reaches the UI", func(t *testing.T) {
		app := routes(t, files["internal/app/handler.go"])
		var v1 []string
		for pattern, handler := range app {
			if !strings.Contains(pattern, " /v1/") {
				continue
			}
			v1 = append(v1, pattern)
			if got := names(handler); slices.Contains(got, "UIHandler") || slices.Contains(got, "ui") {
				t.Errorf("%s is served by %v", pattern, got)
			}
		}
		slices.Sort(v1)
		if want := []string{"POST /v1/baselines", "POST /v1/discoveries", "POST /v1/reanchors", "POST /v1/reports"}; !slices.Equal(v1, want) {
			t.Errorf("/v1/ routes = %v, want %v", v1, want)
		}
	})

	t.Run("the OIDC upload path does not link the approval record", func(t *testing.T) {
		out, err := exec.CommandContext(t.Context(), "go", "list", "-deps", modulePath+"internal/gate/ingest", modulePath+"internal/github/oidc").Output()
		if err != nil {
			t.Fatalf("go list: %v", err)
		}
		for dep := range strings.FieldsSeq(string(out)) {
			if dep == modulePath+"internal/gate/inputs" || dep == modulePath+"internal/store" {
				t.Errorf("the upload authentication links %s, which can write an approval record", dep)
			}
		}
	})

	t.Run("owner decision paths reach only the UI handler, on every role", func(t *testing.T) {
		ownerPaths := []string{
			"/api/repos/acme/app/pulls/4/approve", "/api/repos/acme/app/pulls/4/request-changes",
			"/api/repos/acme/app/pulls/4/withdraw", "/api/repos/acme/app/pulls/4/override",
			"/api/repos/acme/app/pulls/4/refresh-decision",
		}
		paths := append(slices.Clone(ownerPaths), "/v1/reports/approve", "/v1/reports")
		want := map[string]map[string]string{roleAll: {}, rolePublic: {}, roleWorker: {}}
		for _, path := range ownerPaths {
			want[roleAll][path], want[rolePublic][path] = "control.UI", "control.UI"
		}
		want[roleAll][paths[len(paths)-2]], want[rolePublic][paths[len(paths)-2]] = "control.UI", "control.UI"
		want[roleAll][paths[len(paths)-1]], want[rolePublic][paths[len(paths)-1]] = "gate.Upload", "gate.Upload"
		for _, path := range paths {
			want[roleWorker][path] = ""
		}
		for _, role := range slices.Sorted(maps.Keys(want)) {
			for _, path := range paths {
				f := newRoleFixture(role)
				req := httptest.NewRequest(http.MethodPost, path, strings.NewReader(`{"head_sha":"x"}`))
				req.Header.Set("Authorization", "Bearer an-actions-oidc-token")
				req.Header.Set("Content-Type", "application/json")
				rec := f.do(req)
				calls := f.calls.get()
				first := ""
				if len(calls) > 0 {
					first = calls[0]
				}
				if first != want[role][path] {
					t.Errorf("%s %s reached %v (status %d), want %q", role, path, calls, rec.Code, want[role][path])
				}
			}
		}
	})
}

// A decision on an agent's request is made only behind the browser session of control/ui: no upload route and no
// credential an agent holds reaches it, and the requester's login is refused by the gate besides.
func TestDecisionsAreReachableOnlyFromTheBrowserSession(t *testing.T) {
	files := parsedFiles(t)
	const (
		approveDecision = "POST /api/repos/{o}/{n}/pulls/{number}/decisions/{finding}/{digest}/approve"
		declineDecision = "POST /api/repos/{o}/{n}/pulls/{number}/decisions/{finding}/{digest}/decline"
	)

	t.Run("one caller of each decision, in control/ui", func(t *testing.T) {
		want := map[string][]string{
			"ApproveDecision":  {"internal/control/ui.apiApproveDecision"},
			"ApprovedDecision": {"internal/control/ui.apiApproveDecision"},
			"DeclineDecision":  {"internal/control/ui.apiDeclineDecision"},
			"DecisionRequests": {"internal/control/ui.decisionRequests"},
		}
		for name, callers := range want {
			if got := selectorsNamed(files, name); !slices.Equal(got, callers) {
				t.Errorf("%s is used in %v, want only %v", name, got, callers)
			}
		}
	})

	t.Run("control/ui mounts them behind the session, CSRF and write access", func(t *testing.T) {
		ui := routes(t, files["internal/control/ui/ui.go"])
		for pattern, handler := range map[string]string{approveDecision: "apiApproveDecision", declineDecision: "apiDeclineDecision"} {
			expr, ok := ui[pattern]
			if !ok {
				t.Fatalf("control/ui does not mount %s", pattern)
			}
			if got := names(expr); !slices.Equal(got, []string{"s", "apiRepo", "permWrite", "s", handler}) {
				t.Errorf("%s is served by %v, want s.apiRepo(permWrite, s.%s)", pattern, got, handler)
			}
		}
	})

	t.Run("the decision paths reach only the UI handler, on every role", func(t *testing.T) {
		const decisionPath = "/api/repos/acme/app/pulls/4/decisions/" + "0a0a0a0a0a0a0a0a0a0a0a0a0a0a0a0a" + "/" +
			"5b5b5b5b5b5b5b5b5b5b5b5b5b5b5b5b5b5b5b5b5b5b5b5b5b5b5b5b5b5b5b5b"
		want := map[string]string{roleAll: "control.UI", rolePublic: "control.UI", roleWorker: ""}
		for _, role := range slices.Sorted(maps.Keys(want)) {
			for _, action := range []string{"/approve", "/decline"} {
				f := newRoleFixture(role)
				req := httptest.NewRequest(http.MethodPost, decisionPath+action, strings.NewReader(`{}`))
				req.Header.Set("Authorization", "Bearer an-actions-oidc-token")
				req.Header.Set("Content-Type", "application/json")
				rec := f.do(req)
				calls := f.calls.get()
				first := ""
				if len(calls) > 0 {
					first = calls[0]
				}
				if first != want[role] {
					t.Errorf("%s %s reached %v (status %d), want %q", role, action, calls, rec.Code, want[role])
				}
			}
		}
	})
}

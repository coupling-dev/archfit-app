package manifest

import (
	"encoding/json"
	"errors"
	"go/ast"
	"go/parser"
	"go/token"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"slices"
	"strings"
	"sync"
	"testing"
)

const (
	testDigestAMD = "sha256:1111111111111111111111111111111111111111111111111111111111111111"
	testDigestARM = "sha256:2222222222222222222222222222222222222222222222222222222222222222"
	testActionRef = "3333333333333333333333333333333333333333"
)

type row map[string]any

func validRows() []row {
	return []row{
		{
			"engine_version": "v2.3.1", "image_digest": testDigestAMD, "platform": platformAMD64,
			"profile_version": "p1", "action_ref": testActionRef, "current": true,
		},
		{
			"engine_version": "v2.3.1", "image_digest": testDigestARM, "platform": platformARM64,
			"profile_version": "p1", "action_ref": testActionRef,
		},
	}
}

func doc(t *testing.T, version int, rows []row) []byte {
	t.Helper()
	data, err := json.Marshal(map[string]any{"version": version, "entries": rows})
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	return data
}

func TestEmbeddedManifestIsValid(t *testing.T) {
	tbl, err := parse(manifestJSON)
	if err != nil {
		t.Fatalf("embedded manifest is invalid: %v", err)
	}
	current := 0
	for id := range tbl.rows {
		if id.EngineVersion != "v2.3.1" {
			t.Errorf("row %+v: engine version is not v2.3.1", id)
		}
		if id.ImageDigest == "sha256:"+strings.Repeat("0", 64) {
			t.Errorf("row %+v: all-zero digest", id)
		}
		if id == tbl.current.Identity {
			current++
		}
	}
	if current != 1 {
		t.Errorf("current rows = %d, want 1", current)
	}
	if tbl.current.Platform != "linux/amd64" {
		t.Errorf("current platform = %q, want linux/amd64", tbl.current.Platform)
	}
}

func TestManifestValidationRules(t *testing.T) {
	if _, err := parse(doc(t, 1, validRows())); err != nil {
		t.Fatalf("valid base: %v", err)
	}

	edit := func(i int, key string, value any) func([]row) []row {
		return func(rows []row) []row {
			rows[i][key] = value
			return rows
		}
	}
	tests := []struct {
		name    string
		version int
		mutate  func([]row) []row
		want    error // nil: any error (raised by the JSON decoder)
	}{
		{"no entries", 1, func([]row) []row { return []row{} }, errNoEntries},
		{"empty profile_version", 1, edit(0, "profile_version", ""), errEmptyField},
		{"digest with 63 hex characters", 1, edit(0, "image_digest", "sha256:"+strings.Repeat("1", 63)), errDigest},
		{"all-zero digest", 1, edit(0, "image_digest", "sha256:"+strings.Repeat("0", 64)), errZeroDigest},
		{"platform darwin/arm64", 1, edit(1, "platform", "darwin/arm64"), errPlatform},
		{"action_ref v1", 1, edit(0, "action_ref", "v1"), errActionRef},
		{"engine version v2.3.0", 1, edit(1, "engine_version", "v2.3.0"), errEngineVersion},
		{"duplicate identity", 1, func(rows []row) []row { return append(rows, rows[1]) }, errDuplicate},
		{"no current row", 1, edit(0, "current", false), errNoCurrent},
		{"two current rows", 1, func(rows []row) []row {
			extra := validRows()[0]
			extra["image_digest"] = "sha256:" + strings.Repeat("4", 64)
			return append(rows, extra)
		}, errMultipleCurrent},
		{"current row on linux/arm64", 1, func(rows []row) []row {
			rows[0]["current"], rows[1]["current"] = false, true
			return rows
		}, errCurrentPlatform},
		{"unknown key notes", 1, edit(0, "notes", "x"), nil},
		{"version 2", 2, func(rows []row) []row { return rows }, errVersion},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			_, err := parse(doc(t, tt.version, tt.mutate(validRows())))
			if err == nil {
				t.Fatal("want error, got nil")
			}
			if tt.want != nil && !errors.Is(err, tt.want) {
				t.Errorf("error = %v, want %v", err, tt.want)
			}
			if tt.want == nil && !strings.Contains(err.Error(), "notes") {
				t.Errorf("error = %v, want it to name the unknown key", err)
			}
		})
	}
}

func TestLookupExactMatch(t *testing.T) {
	known := Current()
	with := func(edit func(*Identity)) Identity {
		id := known.Identity
		edit(&id)
		return id
	}
	tests := []struct {
		name string
		id   Identity
		hit  bool
	}{
		{"exact identity", known.Identity, true},
		{"digest uppercased", with(func(id *Identity) { id.ImageDigest = strings.ToUpper(id.ImageDigest) }), false},
		{"digest hex uppercased", with(func(id *Identity) {
			id.ImageDigest = "sha256:" + strings.ToUpper(strings.TrimPrefix(id.ImageDigest, "sha256:"))
		}), false},
		{"version without the v", with(func(id *Identity) { id.EngineVersion = strings.TrimPrefix(id.EngineVersion, "v") }), false},
		{"other platform for that digest", with(func(id *Identity) { id.Platform = "linux/arm64" }), false},
		{"leading space in engine version", with(func(id *Identity) { id.EngineVersion = " " + id.EngineVersion }), false},
		{"leading space in image digest", with(func(id *Identity) { id.ImageDigest = " " + id.ImageDigest }), false},
		{"leading space in platform", with(func(id *Identity) { id.Platform = " " + id.Platform }), false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, ok := Lookup(tt.id)
			if ok != tt.hit {
				t.Fatalf("Lookup(%+v) hit = %v, want %v", tt.id, ok, tt.hit)
			}
			if tt.hit && got != known {
				t.Errorf("Lookup = %+v, want %+v", got, known)
			}
			if !tt.hit && got != (Entry{}) {
				t.Errorf("miss returned %+v, want the zero Entry", got)
			}
		})
	}
}

func TestCurrentIsApproved(t *testing.T) {
	current := Current()
	got, ok := Lookup(current.Identity)
	if !ok {
		t.Fatalf("Lookup(Current().Identity) missed: %+v", current.Identity)
	}
	if got != current {
		t.Errorf("Lookup = %+v, want %+v", got, current)
	}
	if current.Platform != "linux/amd64" {
		t.Errorf("Platform = %q, want linux/amd64", current.Platform)
	}
	if !regexp.MustCompile(`^[0-9a-f]{40}$`).MatchString(current.ActionRef) {
		t.Errorf("ActionRef = %q, want 40 lowercase hex", current.ActionRef)
	}
}

func TestLookupReturnsProfileAndActionRef(t *testing.T) {
	var raw struct {
		Entries []struct {
			EngineVersion  string `json:"engine_version"`
			ImageDigest    string `json:"image_digest"`
			Platform       string `json:"platform"`
			ProfileVersion string `json:"profile_version"`
			ActionRef      string `json:"action_ref"`
			Current        bool   `json:"current"`
		} `json:"entries"`
	}
	if err := json.Unmarshal(manifestJSON, &raw); err != nil {
		t.Fatalf("decode embedded manifest: %v", err)
	}
	if len(raw.Entries) == 0 {
		t.Fatal("embedded manifest has no entries")
	}
	for _, e := range raw.Entries {
		got, ok := Lookup(Identity{EngineVersion: e.EngineVersion, ImageDigest: e.ImageDigest, Platform: e.Platform})
		if !ok {
			t.Errorf("row %s %s missed", e.ImageDigest, e.Platform)
			continue
		}
		if got.ProfileVersion != e.ProfileVersion || got.ActionRef != e.ActionRef {
			t.Errorf("row %s: got profile %q action %q, want %q %q",
				e.ImageDigest, got.ProfileVersion, got.ActionRef, e.ProfileVersion, e.ActionRef)
		}
	}
}

func TestManifestMatchesFixtureProducers(t *testing.T) {
	paths, err := filepath.Glob(filepath.Join("testdata", "*.expect.json"))
	if err != nil {
		t.Fatalf("glob: %v", err)
	}
	approved := 0
	for _, path := range paths {
		var fixture struct {
			IdentityApproved bool `json:"identity_approved"`
			Producer         struct {
				EngineVersion string `json:"engine_version"`
				ImageDigest   string `json:"image_digest"`
				Platform      string `json:"platform"`
			} `json:"producer"`
			ProfileVersion string `json:"profile_version"`
		}
		data, err := os.ReadFile(path)
		if err != nil {
			t.Fatalf("read %s: %v", path, err)
		}
		if err := json.Unmarshal(data, &fixture); err != nil {
			t.Fatalf("decode %s: %v", path, err)
		}
		if !fixture.IdentityApproved {
			continue
		}
		approved++
		p := fixture.Producer
		got, ok := Lookup(Identity{EngineVersion: p.EngineVersion, ImageDigest: p.ImageDigest, Platform: p.Platform})
		if !ok {
			t.Errorf("%s: producer identity %+v missed", path, p)
			continue
		}
		if got.ProfileVersion != fixture.ProfileVersion {
			t.Errorf("%s: profile version %q, manifest has %q", path, fixture.ProfileVersion, got.ProfileVersion)
		}
	}
	if approved == 0 {
		t.Fatal("no approved fixture found")
	}
}

func TestUnknownNeverEqualsUnknown(t *testing.T) {
	real := Current().Identity
	tests := []struct {
		name string
		id   Identity
	}{
		{"zero identity", Identity{}},
		{"empty engine version", Identity{ImageDigest: real.ImageDigest, Platform: real.Platform}},
		{"empty image digest", Identity{EngineVersion: real.EngineVersion, Platform: real.Platform}},
		{"empty platform", Identity{EngineVersion: real.EngineVersion, ImageDigest: real.ImageDigest}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got, ok := Lookup(tt.id); ok || got != (Entry{}) {
				t.Errorf("Lookup(%+v) = %+v, %v; want a miss", tt.id, got, ok)
			}
		})
	}
}

func TestNoRuntimeOverride(t *testing.T) {
	unlisted := Identity{
		EngineVersion: "v2.3.1",
		ImageDigest:   "sha256:" + strings.Repeat("9", 64),
		Platform:      "linux/amd64",
	}
	override := doc(t, 1, []row{{
		"engine_version": unlisted.EngineVersion, "image_digest": unlisted.ImageDigest, "platform": unlisted.Platform,
		"profile_version": "p1", "action_ref": testActionRef, "current": true,
	}})
	file := filepath.Join(t.TempDir(), "manifest.json")
	if err := os.WriteFile(file, override, 0o600); err != nil {
		t.Fatalf("write override: %v", err)
	}
	t.Setenv("ARCHFIT_MANIFEST", file)
	t.Setenv("ARCHFIT_MANIFEST_JSON", string(override))
	t.Setenv("ARCHFIT_IMAGE_DIGEST", unlisted.ImageDigest)
	t.Setenv("ARCHFIT_ENGINE_VERSION", unlisted.EngineVersion)

	if got, ok := Lookup(unlisted); ok {
		t.Errorf("unlisted identity hit: %+v", got)
	}
	if Current().ImageDigest == unlisted.ImageDigest {
		t.Error("Current was overridden by the environment")
	}

	exported := exportedDecls(t)
	want := []string{"func Current", "func Lookup", "type Entry", "type Identity"}
	if strings.Join(exported, ",") != strings.Join(want, ",") {
		t.Errorf("exported API = %v, want %v", exported, want)
	}
}

// exportedDecls lists the package's exported declarations from its non-test sources, sorted.
func exportedDecls(t *testing.T) []string {
	t.Helper()
	paths, err := filepath.Glob("*.go")
	if err != nil {
		t.Fatalf("glob: %v", err)
	}
	var out []string
	add := func(kind string, name *ast.Ident) {
		if name.IsExported() {
			out = append(out, kind+" "+name.Name)
		}
	}
	for _, path := range paths {
		if strings.HasSuffix(path, "_test.go") {
			continue
		}
		file, err := parser.ParseFile(token.NewFileSet(), path, nil, 0)
		if err != nil {
			t.Fatalf("parse %s: %v", path, err)
		}
		for _, decl := range file.Decls {
			switch d := decl.(type) {
			case *ast.FuncDecl:
				add("func", d.Name)
			case *ast.GenDecl:
				for _, spec := range d.Specs {
					switch s := spec.(type) {
					case *ast.TypeSpec:
						add("type", s.Name)
					case *ast.ValueSpec:
						for _, name := range s.Names {
							add(d.Tok.String(), name)
						}
					}
				}
			}
		}
	}
	slices.Sort(out)
	return out
}

func TestPackageImportsStdlibOnly(t *testing.T) {
	goList := func(args ...string) []string {
		t.Helper()
		out, err := exec.Command("go", append([]string{"list"}, args...)...).Output()
		if err != nil {
			t.Fatalf("go list %v: %v", args, err)
		}
		return strings.Fields(string(out))
	}

	if nonStd := goList("-deps", "-f", `{{if not .Standard}}{{.ImportPath}}{{end}}`, "."); len(nonStd) != 1 {
		t.Errorf("non-standard packages in deps = %v, want only this package", nonStd)
	}
	// encoding/json pulls os in transitively, so the ban on net, os and log applies to direct imports.
	for _, imp := range goList("-f", `{{join .Imports "\n"}}`, ".") {
		root, _, _ := strings.Cut(imp, "/")
		if root == "net" || root == "os" || root == "log" {
			t.Errorf("forbidden import %q", imp)
		}
	}
}

func TestConcurrentLookup(t *testing.T) {
	wantCurrent := Current()
	wantEntry, wantOK := Lookup(wantCurrent.Identity)

	var wg sync.WaitGroup
	for range 100 {
		wg.Add(1)
		go func() {
			defer wg.Done()
			if got := Current(); got != wantCurrent {
				t.Errorf("Current = %+v, want %+v", got, wantCurrent)
			}
			if got, ok := Lookup(wantCurrent.Identity); got != wantEntry || ok != wantOK {
				t.Errorf("Lookup = %+v, %v; want %+v, %v", got, ok, wantEntry, wantOK)
			}
			if _, ok := Lookup(Identity{}); ok {
				t.Error("zero identity hit")
			}
		}()
	}
	wg.Wait()
}

func TestWrongImageIsNotApproved(t *testing.T) {
	id := Current().Identity
	last := id.ImageDigest[len(id.ImageDigest)-1]
	flipped := byte('0')
	if last == '0' {
		flipped = '1'
	}
	id.ImageDigest = id.ImageDigest[:len(id.ImageDigest)-1] + string(flipped)

	if got, ok := Lookup(id); ok {
		t.Errorf("wrong image hit: %+v", got)
	}
}

func TestOlderEngineIsNotApproved(t *testing.T) {
	id := Current().Identity
	id.EngineVersion = "v2.3.0"

	if got, ok := Lookup(id); ok {
		t.Errorf("v2.3.0 hit: %+v", got)
	}
}

func TestGeneratedWorkflowInputsArePinned(t *testing.T) {
	current := Current()
	if !regexp.MustCompile(`^[0-9a-f]{40}$`).MatchString(current.ActionRef) {
		t.Errorf("ActionRef = %q, want a commit SHA", current.ActionRef)
	}
	if !regexp.MustCompile(`^sha256:[0-9a-f]{64}$`).MatchString(current.ImageDigest) {
		t.Errorf("ImageDigest = %q, want a sha256 digest", current.ImageDigest)
	}
}

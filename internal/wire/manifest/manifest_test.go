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
	"strconv"
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
			"engine_version": "v3.0.0", "image_digest": testDigestAMD, "platform": platformAMD64,
			"profile_version": "p1", "action_ref": testActionRef, "current": true,
		},
		{
			"engine_version": "v3.0.0", "image_digest": testDigestARM, "platform": platformARM64,
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
		if !slices.Contains(tbl.versions, id.EngineVersion) {
			t.Errorf("row %+v: engine version missing from the version list", id)
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
		{"engine version without the v", 1, edit(1, "engine_version", "2.3.1"), errEngineVersion},
		{"engine version v2.3", 1, edit(1, "engine_version", "v2.3"), errEngineVersion},
		{"engine version v2.4.0-rc.1", 1, edit(1, "engine_version", "v2.4.0-rc.1"), errEngineVersion},
		{"engine version latest", 1, edit(1, "engine_version", "latest"), errEngineVersion},
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

	// Errors name the field and the rule, never the defective value.
	for field, bad := range map[string]string{
		"engine_version": "v9.9.9-LEAK",
		"image_digest":   "sha256:LEAK",
		"platform":       "LEAK/arm64",
		"action_ref":     "LEAK",
	} {
		_, err := parse(doc(t, 1, edit(0, field, bad)(validRows())))
		if err == nil || !strings.Contains(err.Error(), field) || strings.Contains(err.Error(), "LEAK") {
			t.Errorf("%s: error %q, want it to name the field and not the value", field, err)
		}
	}
}

// TestApprovedEngineVersions lists the shipped releases; change it with manifest.json.
func TestApprovedEngineVersions(t *testing.T) {
	got := EngineVersions()
	if want := []string{"v3.4.0"}; !slices.Equal(got, want) {
		t.Errorf("EngineVersions() = %q, want %q", got, want)
	}
	got[0] = "changed"
	if EngineVersions()[0] == "changed" {
		t.Error("EngineVersions() hands out the package's own list")
	}
	for _, v := range EngineVersions() {
		if !engineVersionPattern.MatchString(v) {
			t.Errorf("engine version %q is not a release tag", v)
		}
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
	const (
		reportDir      = "../testdata/report"
		envelopeSuffix = ".envelope.json"
	)
	paths, err := filepath.Glob(filepath.Join(reportDir, "*"+envelopeSuffix))
	if err != nil {
		t.Fatalf("glob: %v", err)
	}
	readJSON := func(path string, into any) {
		t.Helper()
		data, err := os.ReadFile(path)
		if err != nil {
			t.Fatalf("read %s: %v", path, err)
		}
		if err := json.Unmarshal(data, into); err != nil {
			t.Fatalf("decode %s: %v", path, err)
		}
	}
	approved, unapproved := 0, 0
	for _, path := range paths {
		name := strings.TrimSuffix(filepath.Base(path), envelopeSuffix)
		var envelope struct {
			EngineVersion string `json:"engine_version"`
			ImageDigest   string `json:"image_digest"`
			Platform      string `json:"platform"`
		}
		readJSON(path, &envelope)
		var expect struct {
			Fields struct {
				ProfileVersion string `json:"profile_version"`
			} `json:"fields"`
			Facts struct {
				IdentityApproved *bool `json:"identity_approved"`
			} `json:"facts"`
		}
		readJSON(filepath.Join(reportDir, name+".expect.json"), &expect)

		id := Identity{EngineVersion: envelope.EngineVersion, ImageDigest: envelope.ImageDigest, Platform: envelope.Platform}
		got, ok := Lookup(id)
		if a := expect.Facts.IdentityApproved; a != nil && !*a {
			unapproved++
			if ok {
				t.Errorf("%s: identity %+v hit, the expectation says it is not approved", name, id)
			}
			continue
		}
		approved++
		if !ok {
			t.Errorf("%s: envelope identity %+v missed", name, id)
			continue
		}
		if got.ProfileVersion != expect.Fields.ProfileVersion {
			t.Errorf("%s: profile version %q, manifest has %q", name, expect.Fields.ProfileVersion, got.ProfileVersion)
		}
	}
	if approved == 0 || unapproved == 0 {
		t.Fatalf("approved fixtures = %d, unapproved = %d, want at least one of each", approved, unapproved)
	}
}

func TestManifestHasNoPlaceholdersInRelease(t *testing.T) {
	// The placeholders module.md documents, as "entry index field". None remain: the image digest is the
	// published v3.0.1 one and the Action commit is the pinned archfit-action commit.
	var documented []string

	var file struct {
		Entries []fileEntry `json:"entries"`
	}
	if err := json.Unmarshal(manifestJSON, &file); err != nil {
		t.Fatalf("decode: %v", err)
	}
	var found []string
	for i, e := range file.Entries {
		for field, hex := range map[string]string{
			"image_digest": strings.TrimPrefix(e.ImageDigest, "sha256:"),
			"action_ref":   e.ActionRef,
		} {
			if strings.Trim(hex, hex[:1]) == "" {
				found = append(found, strconv.Itoa(i)+" "+field)
			}
		}
	}
	slices.Sort(found)

	if os.Getenv("ARCHFIT_RELEASE") == "1" {
		if len(found) != 0 {
			t.Fatalf("a release ships placeholder values: %q", found)
		}
		return
	}
	if !slices.Equal(found, documented) {
		t.Errorf("placeholders %q, documented %q: replace all of them, the fixture envelopes and the documented list together", found, documented)
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
		EngineVersion: "v3.0.0",
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
	want := []string{"func Current", "func EngineVersions", "func Lookup", "type Entry", "type Identity"}
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

// TestOlderEngineIsNotApproved: retired tuples stay unapproved during the v3.2 patch transition, including for still-readable older reports.
func TestOlderEngineIsNotApproved(t *testing.T) {
	for _, id := range []Identity{
		{EngineVersion: "v3.0.1", ImageDigest: "sha256:9aac269fa350e643054cbe5ced58426b4ed46000f30d60f49564aa605ece2408", Platform: "linux/amd64"},
		{EngineVersion: "v3.0.1", ImageDigest: "sha256:19ccd3cb81ea6107ee723e939b0f01d4e8da045204e0a5daa4c004c2baa4faec", Platform: "linux/arm64"},
		{EngineVersion: "v3.0.0", ImageDigest: "sha256:0eab810538fd8e75115f548154edecfe294ee70e1f13e1a158008ded43119909", Platform: "linux/amd64"},
		{EngineVersion: "v3.0.0", ImageDigest: "sha256:416cd49b73dfc1a2057ed07da9f5a65f2e1b47562cec1cdf255f40a3e6220473", Platform: "linux/arm64"},
		{EngineVersion: "v2.5.0", ImageDigest: "sha256:5752b4fe6d62f4fdd71edf33b4174228e17bd7c3d960b36a67111806ec17686a", Platform: "linux/amd64"},
		{EngineVersion: "v2.5.0", ImageDigest: "sha256:44d95cbb13519b9f85bd610488ae2dd0051b94a6447ee17087aa202c28ac605c", Platform: "linux/arm64"},
		{EngineVersion: "v2.4.1", ImageDigest: "sha256:996e044c55eec96303c344bbe327c97b2a17661b1ab626bb94d00d47045c01c7", Platform: "linux/amd64"},
	} {
		if got, ok := Lookup(id); ok {
			t.Errorf("%s %s hit: %+v", id.EngineVersion, id.Platform, got)
		}
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

func TestReleasedApprovalTuples(t *testing.T) {
	// The Action commit that vendors the envelope with analyzed_sha, integration and policy_change (archfit-action X1, X3b). Every row names
	// it: the envelope check fetches each distinct Action commit, so no older Action may stay beside it.
	const action = "fb2520cb069571d6bdc9274ac97d4df49994b6c7"
	want := []Identity{
		{EngineVersion: "v3.4.0", ImageDigest: "sha256:bdcfe29bf9def9a4f0ae544923bc3e755fdec2c2cd6c46ec353c01e60642896b", Platform: "linux/amd64"},
		{EngineVersion: "v3.4.0", ImageDigest: "sha256:0cadc39c41de4dc58bb115b22bdd0296cb81688175a6ea1839a1e16ed2c35e1f", Platform: "linux/arm64"},
	}
	// The engine releases the previous Actions carried left the manifest; their identities are no longer approved.
	retired := []Identity{
		{EngineVersion: "v3.2.3", ImageDigest: "sha256:87323e8d80d519531338ff7a36ce750d337ecf6560e48493bdeb10b864e7b772", Platform: "linux/amd64"},
		{EngineVersion: "v3.2.3", ImageDigest: "sha256:54f6bf1ec99be6f4bdc21ca59e16d3dd65fb5b31a2c2af6c932ad556c89a220c", Platform: "linux/arm64"},
		{EngineVersion: "v3.2.2", ImageDigest: "sha256:bd351ac70610555237974fac5ff914967a1100fb8ef754853151300d5d19ca35", Platform: "linux/amd64"},
		{EngineVersion: "v3.2.2", ImageDigest: "sha256:e97a200a7c26907e4d30982b06e61ac7cc27bffd1d46002ad53bbf6b6594c479", Platform: "linux/arm64"},
		{EngineVersion: "v3.2.0", ImageDigest: "sha256:5f2d4a5fff1ace10d7b08937429179631ae0c1d0e1f9e6f37c72b30954cf00db", Platform: "linux/amd64"},
		{EngineVersion: "v3.2.0", ImageDigest: "sha256:725c3a074358df8ea41881296eda06b07daed7934d7a6d40bb1ecfc3c8a8128d", Platform: "linux/arm64"},
	}
	for _, id := range retired {
		if got, ok := Lookup(id); ok {
			t.Errorf("retired identity %s %s still approved: %+v", id.EngineVersion, id.Platform, got)
		}
	}
	var file struct {
		Entries []json.RawMessage `json:"entries"`
	}
	if err := json.Unmarshal(manifestJSON, &file); err != nil {
		t.Fatal(err)
	}
	if len(file.Entries) != len(want) {
		t.Fatalf("approved entries = %d, want %d", len(file.Entries), len(want))
	}
	for _, id := range want {
		e, ok := Lookup(id)
		if !ok || e.ActionRef != action || e.ProfileVersion != "archfit.measurement.v2" {
			t.Errorf("released identity %s: entry=%+v, approved=%v", id.Platform, e, ok)
		}
	}
	if got := Current(); got.Identity != want[0] || got.ActionRef != action {
		t.Errorf("current = %+v, want the released amd64 tuple", got)
	}
	index := Identity{EngineVersion: "v3.2.0", ImageDigest: "sha256:36e8d4e2d7e3eb15246888b386cb82e7b495607f1fb536a593d2ee733391f886", Platform: "linux/amd64"}
	if _, ok := Lookup(index); ok {
		t.Error("the OCI index digest must not be approved as a platform image")
	}
}

func TestPatchTransitionRejectsMixedAndUnknownTuples(t *testing.T) {
	current := Current().Identity
	for _, id := range []Identity{
		{EngineVersion: "v3.2.2", ImageDigest: "sha256:47575b70d4602859011fecfc9c01c4d6a653f38c033cc880350f301a1ba5f07b", Platform: "linux/amd64"},
		{EngineVersion: "v3.2.2", ImageDigest: current.ImageDigest, Platform: "linux/arm64"},
		{EngineVersion: "v3.2.0", ImageDigest: current.ImageDigest, Platform: current.Platform},
		{EngineVersion: "v3.2.1", ImageDigest: current.ImageDigest, Platform: current.Platform},
		{EngineVersion: "v3.2.3", ImageDigest: "sha256:87323e8d80d519531338ff7a36ce750d337ecf6560e48493bdeb10b864e7b772", Platform: current.Platform},
		{EngineVersion: "v3.4.0", ImageDigest: "sha256:a1ada076a294482ae2b695246dc1cf965ed46be8dae30c8b77a6b94951763137", Platform: current.Platform},
		{EngineVersion: "v3.4.0", ImageDigest: current.ImageDigest, Platform: "linux/arm64"},
		{EngineVersion: "v3.4.1", ImageDigest: current.ImageDigest, Platform: current.Platform},
	} {
		if _, ok := Lookup(id); ok {
			t.Errorf("unapproved tuple hit: %+v", id)
		}
	}
}

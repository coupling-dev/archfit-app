package manifest

import (
	"encoding/json"
	"errors"
	"strings"
	"testing"
)

const (
	testEngine4AMD    = "sha256:a1b2c3d4e5f60718293a4b5c6d7e8f90a1b2c3d4e5f60718293a4b5c6d7e8f90"
	testEngine4ARM    = "sha256:0f1e2d3c4b5a69788796a5b4c3d2e1f00f1e2d3c4b5a69788796a5b4c3d2e1f0"
	testActionCommit4 = "4c1d2e3f405162738495a6b7c8d9e0f1a2b3c4d5"
	testActionCommit5 = "5d2e3f405162738495a6b7c8d9e0f1a2b3c4d5e6"
)

func validEngines() []row {
	return []row{{
		"engine_version": "v4.0.0-rc.1",
		"images":         map[string]any{platformAMD64: testEngine4AMD, platformARM64: testEngine4ARM},
		"action_commits": []any{testActionCommit4},
	}}
}

func engineDoc(t *testing.T, version int, engines []row) []byte {
	t.Helper()
	data, err := json.Marshal(map[string]any{"version": version, "engines": engines})
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	return data
}

// TestEmbeddedManifest4PinsRelease: until task 6.5 the App approves engine v4.0.0, which the onboarded repositories run,
// and engine 0.9.0 beside it. Each was read from its source: the image index's per-platform manifests and the Action's
// release tag.
func TestEmbeddedManifest4PinsRelease(t *testing.T) {
	table, err := parseEngines(manifest4JSON)
	if err != nil {
		t.Fatalf("embedded manifest4.json: %v", err)
	}
	releases := []struct {
		version, amd64, arm64, index, action string
	}{
		{"v4.0.0",
			"sha256:be1eac8f73513c6d0ef4ff17a78255629e4e4bc2d1b3e044b49afcbfdd6aac29",
			"sha256:3ac782cb5acd177ab1225ce5f68d8b77acea71d0a0cf724c8ecd429cc180fbff",
			"sha256:8ad49e766ce3e486104077df213f2badc6ed0f66d7fe88ae377efaf6a918ccd2",
			"9669571b7b25e3f37fd7f1d99d7b89741c6c95d6"},
		{"v0.9.0",
			"sha256:e841f7b519e5d4a787407887f784f121dff9801c25d0bc892695d783d7805db4",
			"sha256:3e8739e40375e711f3cecd2d44f2dad60b05b94602479d0aff5a598e1adeed9f",
			"sha256:adf05068581dce841246876dec766d931483134344439beaf12a038c213d32c0",
			"53e0c49d80627efa4262d7ee78c56dd6054a6e43"},
	}
	if len(table.identities) != 2*len(releases) || len(table.actions) != len(releases) {
		t.Errorf("manifest approves %d identities and %d Action commits, want %d and %d",
			len(table.identities), len(table.actions), 2*len(releases), len(releases))
	}
	for _, r := range releases {
		t.Run(r.version, func(t *testing.T) {
			for platform, digest := range map[string]string{platformAMD64: r.amd64, platformARM64: r.arm64} {
				if id := (Identity{EngineVersion: r.version, Platform: platform, ImageDigest: digest}); !EngineApproved(id) {
					t.Errorf("release identity not approved: %+v", id)
				}
			}
			if !ActionApproved(r.action) {
				t.Errorf("release Action commit %s not approved", r.action)
			}
			if index := (Identity{EngineVersion: r.version, Platform: platformAMD64, ImageDigest: r.index}); EngineApproved(index) {
				t.Error("image index digest must not be approved as a platform digest")
			}
			// One release's digest under the other release's version is no identity the manifest lists.
			for _, other := range releases {
				if other.version != r.version && EngineApproved(Identity{EngineVersion: other.version, Platform: platformAMD64, ImageDigest: r.amd64}) {
					t.Errorf("%s's image approved as engine %s", r.version, other.version)
				}
			}
		})
	}
}

func TestManifest4ValidationRules(t *testing.T) {
	if _, err := parseEngines(engineDoc(t, 2, validEngines())); err != nil {
		t.Fatalf("valid base: %v", err)
	}
	if _, err := parseEngines([]byte(`{"version":2,"engines":[]}`)); err != nil {
		t.Fatalf("an empty engine list approves nothing and is valid: %v", err)
	}
	edit := func(key string, value any) func([]row) []row {
		return func(rows []row) []row {
			rows[0][key] = value
			return rows
		}
	}
	image := func(platform, digest string) func([]row) []row {
		return edit("images", map[string]any{platform: digest})
	}
	tests := []struct {
		name    string
		version int
		mutate  func([]row) []row
		want    error // nil: any error (raised by the JSON decoder)
	}{
		{"version 1", 1, func(rows []row) []row { return rows }, errEngineFileVersion},
		{"engine 3", 2, edit("engine_version", "v3.4.0"), errEngineVersion},
		{"version without the v", 2, edit("engine_version", "4.0.0"), errEngineVersion},
		{"beta prerelease", 2, edit("engine_version", "v4.0.0-beta.1"), errEngineVersion},
		{"rc 0", 2, edit("engine_version", "v4.0.0-rc.0"), errEngineVersion},
		{"leading zero", 2, edit("engine_version", "v4.01.0"), errEngineVersion},
		{"engine 5", 2, edit("engine_version", "v5.0.0"), errEngineVersion},
		{"engine 1", 2, edit("engine_version", "v1.0.0"), errEngineVersion},
		{"0.x leading zero", 2, edit("engine_version", "v0.09.0"), errEngineVersion},
		{"0.x rc 0", 2, edit("engine_version", "v0.9.0-rc.0"), errEngineVersion},
		{"no images", 2, edit("images", map[string]any{}), errNoImages},
		{"platform darwin/arm64", 2, image("darwin/arm64", testEngine4AMD), errPlatform},
		{"digest with 63 hex characters", 2, image(platformAMD64, "sha256:"+strings.Repeat("a", 63)), errDigest},
		{"all-zero digest", 2, image(platformAMD64, "sha256:"+strings.Repeat("0", 64)), errPlaceholder},
		{"placeholder digest", 2, image(platformAMD64, "sha256:"+strings.Repeat("1", 64)), errPlaceholder},
		{"no Action commit", 2, edit("action_commits", []any{}), errNoActions},
		{"Action tag", 2, edit("action_commits", []any{"v4"}), errActionCommit},
		{"Action commit upper case", 2, edit("action_commits", []any{strings.ToUpper(testActionCommit4)}), errActionCommit},
		{"placeholder Action commit", 2, edit("action_commits", []any{strings.Repeat("3", 40)}), errPlaceholder},
		{"Action commit twice", 2, edit("action_commits", []any{testActionCommit4, testActionCommit4}), errDuplicateAction},
		{"engine version twice", 2, func(rows []row) []row { return append(rows, validEngines()[0]) }, errDuplicateVersion},
		{"a measurement profile", 2, edit("profile_version", "archfit.measurement.v2"), nil},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			_, err := parseEngines(engineDoc(t, tt.version, tt.mutate(validEngines())))
			if err == nil {
				t.Fatal("want error, got nil")
			}
			if tt.want != nil && !errors.Is(err, tt.want) {
				t.Errorf("error = %v, want %v", err, tt.want)
			}
			if tt.want == nil && !strings.Contains(err.Error(), "profile_version") {
				t.Errorf("error = %v, want it to name the unknown key", err)
			}
		})
	}
	if _, err := parseEngines([]byte(`{"version":2}`)); !errors.Is(err, errNoEngineList) {
		t.Errorf("no engines key: error = %v, want %v", err, errNoEngineList)
	}

	// Errors name the field and the rule, never the defective value.
	for name, mutate := range map[string]func([]row) []row{
		"engine_version": edit("engine_version", "v4.0.0-LEAK"),
		"images":         image("LEAK/arm64", testEngine4AMD),
		"action_commits": edit("action_commits", []any{"LEAK"}),
	} {
		_, err := parseEngines(engineDoc(t, 2, mutate(validEngines())))
		if err == nil || !strings.Contains(err.Error(), name) || strings.Contains(err.Error(), "LEAK") {
			t.Errorf("%s: error %q, want it to name the field and not the value", name, err)
		}
	}
}

func TestEngineApprovedExactMatch(t *testing.T) {
	withEngines(t, engineDoc(t, 2, validEngines()))
	approved := Identity{EngineVersion: "v4.0.0-rc.1", ImageDigest: testEngine4AMD, Platform: platformAMD64}
	tests := []struct {
		name string
		id   Identity
		want bool
	}{
		{"exact amd64", approved, true},
		{"exact arm64", Identity{EngineVersion: "v4.0.0-rc.1", ImageDigest: testEngine4ARM, Platform: platformARM64}, true},
		{"amd64 digest on arm64", Identity{EngineVersion: "v4.0.0-rc.1", ImageDigest: testEngine4AMD, Platform: platformARM64}, false},
		{"digest upper case", Identity{EngineVersion: "v4.0.0-rc.1", ImageDigest: strings.ToUpper(testEngine4AMD), Platform: platformAMD64}, false},
		{"version without the v", Identity{EngineVersion: "4.0.0-rc.1", ImageDigest: testEngine4AMD, Platform: platformAMD64}, false},
		{"final release of the rc", Identity{EngineVersion: "v4.0.0", ImageDigest: testEngine4AMD, Platform: platformAMD64}, false},
		{"leading space", Identity{EngineVersion: " v4.0.0-rc.1", ImageDigest: testEngine4AMD, Platform: platformAMD64}, false},
		{"no version", Identity{ImageDigest: testEngine4AMD, Platform: platformAMD64}, false},
		{"no digest", Identity{EngineVersion: "v4.0.0-rc.1", Platform: platformAMD64}, false},
		{"no platform", Identity{EngineVersion: "v4.0.0-rc.1", ImageDigest: testEngine4AMD}, false},
		{"zero", Identity{}, false},
		{"an engine 3 row", Identity{EngineVersion: "v3.4.0", ImageDigest: testEngine4AMD, Platform: platformAMD64}, false},
	}
	for _, tt := range tests {
		if got := EngineApproved(tt.id); got != tt.want {
			t.Errorf("%s: EngineApproved = %v, want %v", tt.name, got, tt.want)
		}
	}
}

// TestManifestApprovesEngine090BesideV4 uses a fake 0.9.0 row: part b of task 5.3 adds the real one.
func TestManifestApprovesEngine090BesideV4(t *testing.T) {
	for _, version := range []string{"v0.9.0", "v0.9.0-rc.1", "v0.10.2"} {
		t.Run(version, func(t *testing.T) {
			rows := validEngines()
			rows = append(rows, row{
				"engine_version": version,
				"images":         map[string]any{platformAMD64: testEngine4ARM},
				"action_commits": []any{testActionCommit5},
			})
			withEngines(t, engineDoc(t, 2, rows))
			for _, tc := range []struct {
				id   Identity
				want bool
			}{
				{Identity{EngineVersion: version, ImageDigest: testEngine4ARM, Platform: platformAMD64}, true},
				{Identity{EngineVersion: "v4.0.0-rc.1", ImageDigest: testEngine4AMD, Platform: platformAMD64}, true},
				{Identity{EngineVersion: version, ImageDigest: testEngine4AMD, Platform: platformAMD64}, false},
			} {
				if got := EngineApproved(tc.id); got != tc.want {
					t.Errorf("EngineApproved(%+v) = %v, want %v", tc.id, got, tc.want)
				}
			}
			if !ActionApproved(testActionCommit4) || !ActionApproved(testActionCommit5) {
				t.Error("the Action commits of both rows are not approved")
			}
		})
	}
}

func TestActionApproved(t *testing.T) {
	rows := validEngines()
	rows[0]["action_commits"] = []any{testActionCommit4, testActionCommit5}
	withEngines(t, engineDoc(t, 2, rows))
	for commit, want := range map[string]bool{
		testActionCommit4:                  true,
		testActionCommit5:                  true,
		strings.ToUpper(testActionCommit4): false,
		testActionCommit4[:7]:              false,
		"v4":                               false,
		"":                                 false,
		"2221f4bfece0725a657414fed751cdc090ba1bd4": false, // the Action 3 commit of manifest 1
	} {
		if got := ActionApproved(commit); got != want {
			t.Errorf("ActionApproved(%q) = %v, want %v", commit, got, want)
		}
	}
}

func TestManifest4HasNoRuntimeOverride(t *testing.T) {
	rows := engineDoc(t, 2, validEngines())
	t.Setenv("ARCHFIT_MANIFEST4", string(rows))
	t.Setenv("ARCHFIT_ACTION_COMMIT", testActionCommit4)
	if EngineApproved(Identity{EngineVersion: "v4.0.0-rc.1", ImageDigest: testEngine4AMD, Platform: platformAMD64}) {
		t.Error("an engine approved from the environment")
	}
	if ActionApproved(testActionCommit4) {
		t.Error("an Action commit approved from the environment")
	}
}

// withEngines replaces the embedded manifest 4 table for one test.
func withEngines(t *testing.T, data []byte) {
	t.Helper()
	table, err := parseEngines(data)
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	saved := engines
	engines = table
	t.Cleanup(func() { engines = saved })
}

type row map[string]any

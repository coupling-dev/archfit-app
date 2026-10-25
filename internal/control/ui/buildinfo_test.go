package ui

import (
	"net/http"
	"runtime/debug"
	"strings"
	"testing"
)

func TestBuildInfoUsesOnlyBuildAndAllowlistedRuntimeMetadata(t *testing.T) {
	info := &debug.BuildInfo{Main: debug.Module{Version: "v1.2.3"}, Settings: []debug.BuildSetting{
		{Key: "vcs.revision", Value: sha(7)}, {Key: "vcs.time", Value: "2020-01-01T00:00:00Z"},
		{Key: "vcs.modified", Value: "true"}, {Key: "secret", Value: "not-for-browser"},
	}}
	got := buildInfo(info, "", "2026-10-25T12:00:00+02:00", "archfit-public-123")
	if got.SourceRevision != sha(7) || got.Version != "v1.2.3" || got.BuiltAt != "2026-10-25T10:00:00Z" || !got.Modified || got.RuntimeRevision != "archfit-public-123" {
		t.Fatalf("build = %+v", got)
	}
	info.Main.Version = "<script>"
	got = buildInfo(info, "not-a-sha", "", "https://credentials@example.com")
	if got.Version != "" || got.SourceRevision != "" || got.BuiltAt != "" || got.RuntimeRevision != "" {
		t.Fatalf("invalid metadata must be absent, not guessed: %+v", got)
	}
	if got := buildInfo(nil, "", "", ""); got != (buildInfoJSON{}) {
		t.Fatalf("missing build metadata = %+v", got)
	}
}

func TestBuildInfoModuleVersionBoundaries(t *testing.T) {
	for _, version := range []string{"v1.2.3", "v0.0.0-20261025103112-e3cde88c42b4", "v1.2.3+dirty", "", "(devel)", "<script>", strings.Repeat("x", 129)} {
		t.Run(version, func(t *testing.T) {
			info := &debug.BuildInfo{Main: debug.Module{Version: version}}
			got := buildInfo(info, "", "", "")
			want := version
			if version == "" || version == "(devel)" || version == "<script>" || len(version) > 128 {
				want = ""
			}
			if got.Version != want {
				t.Fatalf("module version %q displayed as %q, want %q", version, got.Version, want)
			}
		})
	}
}

func TestSessionBuildInfoRequiresAuthentication(t *testing.T) {
	e := newEnv(t)
	t.Setenv("UNRELATED_SECRET", "never-send-this")
	t.Setenv("K_REVISION", "archfit-public-123")
	wantStatus(t, e.anonymous().get("/api/session"), http.StatusUnauthorized)
	res := e.as("alice", permRead).get("/api/session")
	wantStatus(t, res, http.StatusOK)
	got := decodeAs[sessionJSON](t, res)
	if got.Build.RuntimeRevision != "archfit-public-123" || strings.Contains(res.Body.String(), "never-send-this") {
		t.Fatalf("session build metadata = %s", res.Body.String())
	}
}

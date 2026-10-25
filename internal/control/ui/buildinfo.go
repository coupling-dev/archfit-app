package ui

import (
	"os"
	"regexp"
	"runtime/debug"
	"time"
)

// Release builds inject only public build metadata. Never read the working tree,
// an arbitrary environment map, or cloud metadata from a browser request.
var (
	sourceRevision string
	builtAt        string
)

type buildInfoJSON struct {
	Version         string `json:"version"`
	SourceRevision  string `json:"source_revision"`
	BuiltAt         string `json:"built_at"`
	RuntimeRevision string `json:"runtime_revision"`
	Modified        bool   `json:"modified"`
}

var (
	revisionPattern = regexp.MustCompile(`^[0-9a-f]{40}$`)
	versionPattern  = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9._+-]{0,127}$`)
)

func runningBuild() buildInfoJSON {
	info, _ := debug.ReadBuildInfo()
	return buildInfo(info, sourceRevision, builtAt, os.Getenv("K_REVISION"))
}

func buildInfo(info *debug.BuildInfo, revision, timestamp, runtimeRevision string) buildInfoJSON {
	out := buildInfoJSON{}
	if info != nil {
		if versionPattern.MatchString(info.Main.Version) {
			out.Version = info.Main.Version
		}
		for _, s := range info.Settings {
			switch s.Key {
			case "vcs.revision":
				if revision == "" {
					revision = s.Value
				}
			case "vcs.modified":
				out.Modified = s.Value == "true"
			}
		}
	}
	if revisionPattern.MatchString(revision) {
		out.SourceRevision = revision
	}
	if parsed, err := time.Parse(time.RFC3339, timestamp); err == nil {
		out.BuiltAt = parsed.UTC().Format(time.RFC3339)
	}
	if versionPattern.MatchString(runtimeRevision) {
		out.RuntimeRevision = runtimeRevision
	}
	return out
}

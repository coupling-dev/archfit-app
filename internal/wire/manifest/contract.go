// Package manifest is the locked list of approved engine and image identities shipped in the binary.
package manifest

import (
	_ "embed"
	"slices"
)

// The image_digest values in manifest.json are the published per-platform digests of
// ghcr.io/alexei-led/archfit v3.2.0, v3.2.2 and v3.2.3; action_ref is the archfit-action commit that generated workflows pin.
// TestManifestHasNoPlaceholdersInRelease fails a build tested with ARCHFIT_RELEASE=1 if a placeholder returns.
//
//go:embed manifest.json
var manifestJSON []byte

// loaded is read-only after package initialization. An invalid embedded manifest panics here, so the
// binary cannot start with a broken manifest.
var loaded = mustParse(manifestJSON)

// Identity is a producer's claimed engine and image identity.
type Identity struct {
	EngineVersion string
	ImageDigest   string // "sha256:<hex>"
	Platform      string // "linux/amd64" | "linux/arm64"
}

// Entry is one approved row of the locked manifest shipped in the binary.
type Entry struct {
	Identity
	ProfileVersion string // approved measurement profile version for this image
	ActionRef      string // pinned archfit-action commit SHA used in generated workflows
}

// Lookup reports whether id is approved. Unknown never compares as equal: any empty field is a miss.
func Lookup(id Identity) (Entry, bool) {
	if id.EngineVersion == "" || id.ImageDigest == "" || id.Platform == "" {
		return Entry{}, false
	}
	entry, ok := loaded.rows[id]
	return entry, ok
}

// Current returns the entry new workflows are generated against (linux/amd64).
func Current() Entry {
	return loaded.current
}

// EngineVersions returns the engine releases the manifest approves, each once, in the order of their first row.
func EngineVersions() []string {
	return slices.Clone(loaded.versions)
}

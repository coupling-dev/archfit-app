// Package manifest is the locked list of approved engine and image identities shipped in the binary.
package manifest

import _ "embed"

// PLACEHOLDER DATA: the image_digest, profile_version and action_ref values in manifest.json are not the
// published engine v2.3.1 release values (they were not available when this was written). Replace all
// three from the engine release evidence before any release; the repeated-character values are fake.
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

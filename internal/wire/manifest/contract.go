// Package manifest is the locked list of approved engine and image identities shipped in the binary.
package manifest

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
	return Entry{}, false
}

// Current returns the entry new workflows are generated against (linux/amd64).
func Current() Entry {
	return Entry{}
}

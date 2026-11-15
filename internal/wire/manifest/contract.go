// Package manifest is the locked list of approved engine and image identities shipped in the binary.
package manifest

import (
	_ "embed"
)

// Identity is a producer's claimed engine and image identity.
type Identity struct {
	EngineVersion string
	ImageDigest   string // "sha256:<hex>"
	Platform      string // "linux/amd64" | "linux/arm64"
}

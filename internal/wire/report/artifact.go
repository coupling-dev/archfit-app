package report

import (
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"strings"
)

// ArtifactName is the workflow artifact that carries the report bytes of a run.
const ArtifactName = "archfit-report"

// ErrDigestMismatch is returned by DecodeArtifact when the bytes are not the report the digest names.
var ErrDigestMismatch = errors.New("report: artifact does not match its digest")

// DecodeArtifact decodes a report artifact after proving it is the one a check names. digest is the report digest of
// a receipt or of a check's external_id, "sha256:<hex>" or the bare hex; "" matches nothing. The artifact names no
// engine release, so it decodes as the newest one, whose key set holds every older one.
func DecodeArtifact(payload []byte, digest string) (Report, error) {
	sum := sha256.Sum256(payload)
	if strings.TrimPrefix(digest, "sha256:") != hex.EncodeToString(sum[:]) {
		return Report{}, ErrDigestMismatch
	}
	return Decode(payload, engineVersions[len(engineVersions)-1])
}

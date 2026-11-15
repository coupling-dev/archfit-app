package manifest

import (
	"errors"
	"regexp"
)

const (
	platformAMD64 = "linux/amd64"
	platformARM64 = "linux/arm64"
)

var (
	digestPattern    = regexp.MustCompile(`^sha256:[0-9a-f]{64}$`)
	actionRefPattern = regexp.MustCompile(`^[0-9a-f]{40}$`)
)

var (
	errDigest = errors.New("image_digest must match ^sha256:[0-9a-f]{64}$")

	errPlatform = errors.New("platform must be linux/amd64 or linux/arm64")
)

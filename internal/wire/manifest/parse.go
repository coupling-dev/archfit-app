package manifest

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"regexp"
	"slices"
	"strings"
)

const (
	fileVersion   = 1
	platformAMD64 = "linux/amd64"
	platformARM64 = "linux/arm64"
)

var (
	engineVersionPattern = regexp.MustCompile(`^v[0-9]+\.[0-9]+\.[0-9]+$`)
	digestPattern        = regexp.MustCompile(`^sha256:[0-9a-f]{64}$`)
	actionRefPattern     = regexp.MustCompile(`^[0-9a-f]{40}$`)
	zeroDigest           = "sha256:" + strings.Repeat("0", 64)
)

var (
	errVersion         = errors.New("version must be 1")
	errNoEntries       = errors.New("at least one entry is required")
	errEmptyField      = errors.New("every string field must be non-empty")
	errEngineVersion   = errors.New("engine_version must be a release tag vMAJOR.MINOR.PATCH")
	errDigest          = errors.New("image_digest must match ^sha256:[0-9a-f]{64}$")
	errZeroDigest      = errors.New("image_digest must not be all zeros")
	errPlatform        = errors.New("platform must be linux/amd64 or linux/arm64")
	errActionRef       = errors.New("action_ref must be a 40-character lowercase hex commit SHA")
	errDuplicate       = errors.New("two entries share the same identity")
	errNoCurrent       = errors.New("exactly one entry must be current, found none")
	errMultipleCurrent = errors.New("exactly one entry must be current, found several")
	errCurrentPlatform = errors.New("the current entry must be on " + platformAMD64)
)

type fileEntry struct {
	EngineVersion  string `json:"engine_version"`
	ImageDigest    string `json:"image_digest"`
	Platform       string `json:"platform"`
	ProfileVersion string `json:"profile_version"`
	ActionRef      string `json:"action_ref"`
	Current        bool   `json:"current"`
}

type table struct {
	rows     map[Identity]Entry
	versions []string // the distinct engine versions of the rows, in file order
	current  Entry
}

func mustParse(data []byte) table {
	t, err := parse(data)
	if err != nil {
		panic("manifest: invalid embedded manifest.json: " + err.Error())
	}
	return t
}

// parse strictly decodes a manifest data file and checks every row validity rule.
// Validation errors name the entry, the field and the rule; they never quote a value.
func parse(data []byte) (table, error) {
	var file struct {
		Version int         `json:"version"`
		Entries []fileEntry `json:"entries"`
	}
	dec := json.NewDecoder(bytes.NewReader(data))
	dec.DisallowUnknownFields()
	if err := dec.Decode(&file); err != nil {
		return table{}, fmt.Errorf("decode: %w", err)
	}
	if file.Version != fileVersion {
		return table{}, errVersion
	}
	if len(file.Entries) == 0 {
		return table{}, errNoEntries
	}

	t := table{rows: make(map[Identity]Entry, len(file.Entries))}
	currents := 0
	for i, e := range file.Entries {
		if err := e.validate(); err != nil {
			return table{}, fmt.Errorf("entry %d: %w", i, err)
		}
		entry := Entry{
			Identity:       Identity{EngineVersion: e.EngineVersion, ImageDigest: e.ImageDigest, Platform: e.Platform},
			ProfileVersion: e.ProfileVersion,
			ActionRef:      e.ActionRef,
		}
		if _, dup := t.rows[entry.Identity]; dup {
			return table{}, fmt.Errorf("entry %d: %w", i, errDuplicate)
		}
		t.rows[entry.Identity] = entry
		if !slices.Contains(t.versions, e.EngineVersion) {
			t.versions = append(t.versions, e.EngineVersion)
		}
		if e.Current {
			currents++
			t.current = entry
		}
	}
	switch {
	case currents == 0:
		return table{}, errNoCurrent
	case currents > 1:
		return table{}, errMultipleCurrent
	case t.current.Platform != platformAMD64:
		return table{}, errCurrentPlatform
	}
	return t, nil
}

func (e fileEntry) validate() error {
	switch {
	case e.EngineVersion == "" || e.ImageDigest == "" || e.Platform == "" || e.ProfileVersion == "" || e.ActionRef == "":
		return errEmptyField
	case !engineVersionPattern.MatchString(e.EngineVersion):
		return errEngineVersion
	case !digestPattern.MatchString(e.ImageDigest):
		return errDigest
	case e.ImageDigest == zeroDigest:
		return errZeroDigest
	case e.Platform != platformAMD64 && e.Platform != platformARM64:
		return errPlatform
	case !actionRefPattern.MatchString(e.ActionRef):
		return errActionRef
	}
	return nil
}

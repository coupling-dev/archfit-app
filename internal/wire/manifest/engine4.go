package manifest

import (
	"bytes"
	_ "embed"
	"encoding/json"
	"errors"
	"fmt"
	"maps"
	"regexp"
	"slices"
	"strings"
)

// ActionRepository is the repository of the Action that runs an approved engine in a workflow:
// `uses: alexei-led/archfit-action@<commit>`.
const ActionRepository = "alexei-led/archfit-action"

// manifest4.json lists the Archfit 4 engines the App approves: per release, the image digest of each platform and
// the archfit-action commits that run it. It has no measurement profile. It ships empty until the first engine 4
// release candidate is pinned (M2 tasks 29 and 30), so until then no envelope 2 upload has an approved engine.
//
//go:embed manifest4.json
var manifest4JSON []byte

var engines = mustParseEngines(manifest4JSON)

// engineTable is manifest 4 after validation: the approved identities and Action commits.
type engineTable struct {
	identities map[Identity]bool
	actions    map[string]bool
}

const engineFileVersion = 2

var (
	engine4VersionPattern = regexp.MustCompile(`^v4\.(0|[1-9][0-9]*)\.(0|[1-9][0-9]*)(-rc\.[1-9][0-9]*)?$`)

	errEngineFileVersion = errors.New("version must be 2")
	errNoEngineList      = errors.New("engines is required")
	errEngine4Version    = errors.New("engine_version must be v4.MINOR.PATCH or v4.MINOR.PATCH-rc.N")
	errDuplicateVersion  = errors.New("two engines share the same engine_version")
	errNoImages          = errors.New("images must name at least one platform")
	errPlaceholder       = errors.New("must not be one hex character repeated")
	errNoActions         = errors.New("action_commits must name at least one commit")
	errActionCommit      = errors.New("action_commits must be 40-character lowercase hex commit SHAs")
	errDuplicateAction   = errors.New("action_commits names a commit twice")
)

type engineEntry struct {
	EngineVersion string            `json:"engine_version"`
	Images        map[string]string `json:"images"`
	ActionCommits []string          `json:"action_commits"`
}

// EngineApproved reports whether manifest 4 approves the claimed engine identity: the exact engine version, image
// digest and platform of one row. Unknown never compares as equal: any empty field is a miss.
func EngineApproved(id Identity) bool {
	return engines.identities[id] && id.EngineVersion != "" && id.ImageDigest != "" && id.Platform != ""
}

// ActionApproved reports whether commit is an archfit-action commit manifest 4 lists. Only a full commit SHA can
// match: a tag or branch name never does.
func ActionApproved(commit string) bool {
	return engines.actions[commit]
}

func mustParseEngines(data []byte) engineTable {
	t, err := parseEngines(data)
	if err != nil {
		panic("manifest: invalid embedded manifest4.json: " + err.Error())
	}
	return t
}

// parseEngines strictly decodes manifest 4 and checks every rule. Errors name the engine, the field and the rule;
// they never quote a value.
func parseEngines(data []byte) (engineTable, error) {
	var file struct {
		Version int            `json:"version"`
		Engines *[]engineEntry `json:"engines"`
	}
	dec := json.NewDecoder(bytes.NewReader(data))
	dec.DisallowUnknownFields()
	if err := dec.Decode(&file); err != nil {
		return engineTable{}, fmt.Errorf("decode: %w", err)
	}
	if file.Version != engineFileVersion {
		return engineTable{}, errEngineFileVersion
	}
	if file.Engines == nil {
		return engineTable{}, errNoEngineList
	}
	t := engineTable{identities: map[Identity]bool{}, actions: map[string]bool{}}
	var versions []string
	for i, e := range *file.Engines {
		if err := e.validate(); err != nil {
			return engineTable{}, fmt.Errorf("engine %d: %w", i, err)
		}
		if slices.Contains(versions, e.EngineVersion) {
			return engineTable{}, fmt.Errorf("engine %d: %w", i, errDuplicateVersion)
		}
		versions = append(versions, e.EngineVersion)
		for platform, digest := range e.Images {
			t.identities[Identity{EngineVersion: e.EngineVersion, ImageDigest: digest, Platform: platform}] = true
		}
		for _, c := range e.ActionCommits {
			t.actions[c] = true
		}
	}
	return t, nil
}

func (e engineEntry) validate() error {
	if !engine4VersionPattern.MatchString(e.EngineVersion) {
		return errEngine4Version
	}
	if len(e.Images) == 0 {
		return errNoImages
	}
	for _, platform := range slices.Sorted(maps.Keys(e.Images)) {
		digest := e.Images[platform]
		switch {
		case platform != platformAMD64 && platform != platformARM64:
			return fmt.Errorf("images: %w", errPlatform)
		case !digestPattern.MatchString(digest):
			return fmt.Errorf("images %s: %w", platform, errDigest)
		case placeholder(strings.TrimPrefix(digest, "sha256:")):
			return fmt.Errorf("images %s: image_digest %w", platform, errPlaceholder)
		}
	}
	if len(e.ActionCommits) == 0 {
		return errNoActions
	}
	for j, c := range e.ActionCommits {
		switch {
		case !actionRefPattern.MatchString(c):
			return errActionCommit
		case placeholder(c):
			return fmt.Errorf("action_commits: %w", errPlaceholder)
		case slices.Contains(e.ActionCommits[:j], c):
			return errDuplicateAction
		}
	}
	return nil
}

// placeholder reports a hex string of one repeated character, such as all zeros: never a real digest or commit.
func placeholder(hex string) bool {
	return hex != "" && strings.Trim(hex, hex[:1]) == ""
}

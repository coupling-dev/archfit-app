# Approved identities

**Path**: internal/wire/manifest/
**Parent**: [wire](../module.md)

## Contract

`Identity` is the claimed engine version, platform image digest and platform.
`EngineApproved` requires an exact nonempty tuple listed in embedded `manifest4.json`.
`ActionApproved` accepts only a full Action commit SHA listed in that file.
`ActionRepository` (`coupling-dev/archfit-action`) identifies the producer repository. Until task 6.5 the App also
accepts `LegacyActionRepository`, the Action's old home, which the onboarded repositories run until epic 6 moves
them; `ActionRepositories` lists both. The two share their history, so a listed commit names the same Action in both.

The manifest file has `version: 2` and an `engines` list. Each release is v0.MINOR.PATCH or v4.MINOR.PATCH, or
either with `-rc.N` (v4 until task 6.5 removes engine v4.0.0), with non-placeholder image digests for linux/amd64 or linux/arm64 and at least
one non-placeholder Action commit. Unknown fields and duplicate release/action entries are rejected.
An empty engine list approves nothing. Invalid embedded data prevents package initialization.

The embedded list approves engine v4.0.0 and Action v4.0.0. The engine image index digest is
`sha256:8ad49e766ce3e486104077df213f2badc6ed0f66d7fe88ae377efaf6a918ccd2`; uploads use the
platform-specific digests in the manifest, not the index digest. No runtime override exists.
Manifest v1, measurement profiles, `Lookup`, `Current` and `EngineVersions` have been removed.

## Checks

`engine_manifest_test.go` covers strict parsing, empty-list denial, exact matches, wrong/legacy identities
and the absence of runtime overrides.

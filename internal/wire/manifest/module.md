# Approved identities

**Path**: internal/wire/manifest/
**Parent**: [wire](../module.md)

## Contract

`Identity` is the claimed engine version, platform image digest and platform.
`EngineApproved` requires an exact nonempty tuple listed in embedded `manifest4.json`.
`ActionApproved` accepts only a full Action commit SHA listed in that file.
`ActionRepository` identifies the producer repository.

The manifest file has `version: 2` and an `engines` list. Each release is v4.MINOR.PATCH or
v4.MINOR.PATCH-rc.N, with non-placeholder image digests for linux/amd64 or linux/arm64 and at least
one non-placeholder Action commit. Unknown fields and duplicate release/action entries are rejected.
An empty engine list approves nothing. Invalid embedded data prevents package initialization.

The current embedded list is empty until release identities are pinned. No runtime override exists.
Manifest v1, measurement profiles, `Lookup`, `Current` and `EngineVersions` have been removed.

## Checks

`engine4_test.go` covers strict parsing, empty-list denial, exact matches, wrong/legacy identities
and the absence of runtime overrides.

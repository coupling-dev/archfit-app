# Approved identities

**Path**: internal/wire/manifest/
**Parent**: [wire](../module.md)

## Contract

`Identity` is the claimed engine version, platform image digest and platform.
`EngineApproved` requires an exact nonempty tuple listed in embedded `manifest4.json`.
`ActionApproved` accepts only a full Action commit SHA listed in that file.
`ActionRepository` (`coupling-dev/archfit-action`) identifies the producer repository; no other repository is accepted.

The manifest file has `version: 2` and an `engines` list. Each release is v0.MINOR.PATCH, or with `-rc.N`, with
non-placeholder image digests for linux/amd64 or linux/arm64 and at least one non-placeholder Action commit. Unknown
fields and duplicate release/action entries are rejected. A v4 release, such as engine v4.0.0, is refused.
An empty engine list approves nothing. Invalid embedded data prevents package initialization.

The embedded list approves one release:

| Engine | Image | Action commit |
| --- | --- | --- |
| v0.9.0 | `ghcr.io/coupling-dev/archfit`, index `sha256:adf05068…d32c0` | `53e0c49` (`coupling-dev/archfit-action` v0.9.0) |

Uploads use the platform-specific digests in the manifest, not the index digests. No runtime override exists.

The manifest does not pair an engine with its Action commits: `EngineApproved` and `ActionApproved` are separate
sets. With one release the pairing is moot; when a second row is added, the Action still refuses any engine but its
own before it uploads, so a mixed claim reaches the App only as a forged envelope, which gives `workflow_changed`.

Manifest v1, measurement profiles, `Lookup`, `Current` and `EngineVersions` have been removed.

## Checks

`engine_manifest_test.go` covers strict parsing, empty-list denial, exact matches, wrong/legacy identities
and the absence of runtime overrides.

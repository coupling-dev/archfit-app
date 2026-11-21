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

The embedded list approves two releases until task 6.5 removes the first:

| Engine | Image | Action commit |
| --- | --- | --- |
| v4.0.0 | the old home's image, index `sha256:8ad49e76…8ccd2` | `9669571` (Action v4.0.0) |
| v0.9.0 | `ghcr.io/coupling-dev/archfit`, index `sha256:adf05068…d32c0` | `53e0c49` (`coupling-dev/archfit-action` v0.9.0) |

Uploads use the platform-specific digests in the manifest, not the index digests. No runtime override exists.

The manifest does not pair an engine with its Action commits: `EngineApproved` and `ActionApproved` are separate
sets, so an upload claiming one release's engine beside the other release's Action commit is approved. The Action
pairs them: each release refuses any engine but its own before it uploads, so a mixed claim reaches the App only as a
forged envelope, which needs a changed workflow, which gives `workflow_changed` unless an owner approved it. The gap
closes by itself at task 6.5, when only the 0.9.0 row remains.

Manifest v1, measurement profiles, `Lookup`, `Current` and `EngineVersions` have been removed.

## Checks

`engine_manifest_test.go` covers strict parsing, empty-list denial, exact matches, wrong/legacy identities
and the absence of runtime overrides.

# Approved Identity Manifest

**Path**: internal/wire/manifest/
**Parent**: internal/wire/ (Wire Contracts)
**Submodules**: none (leaf)

## Purpose

The manifest is the locked list of engine versions, image digests, platforms and measurement-profile versions the
App accepts, shipped inside the binary. It answers one question for the gate: "is this claimed producer identity
approved?" It also tells onboarding which pinned Action commit and image to write into a generated workflow.
Without it, approval of an engine image would be a runtime setting somebody could change, and an unknown
identity could compare as equal to another unknown.

## Functional Responsibilities

- Embed the approved rows as data and validate them when the package loads.
- `Lookup`: exact match of a claimed `Identity` against the rows.
- `Current`: the row new workflows are generated against.
- Nothing else. It does not read reports, compare profiles, or decide outcomes.

## Subdomain Classification

Published language (release identity shared with `archfit` and `archfit-action`). Supporting.
Volatility: moderate. It changes only with a reviewed engine, image or Action release; v1 lists engine v2.3.1 only.

## Encapsulated Knowledge

- The data file `manifest.json`, embedded with `go:embed`, and its shape (private to this package):
  top-level `version` (must be 1) and `entries`, a list of objects with exactly these keys: `engine_version`,
  `image_digest`, `platform`, `profile_version`, `action_ref`, and an optional boolean `current`.
- Row validity rules, checked once at package initialization and again by a unit test:
  - at least one entry; every string non-empty;
  - `engine_version` is `v2.3.1` in this release;
  - `image_digest` matches `^sha256:[0-9a-f]{64}$` and is not all zeros;
  - `platform` is `linux/amd64` or `linux/arm64`;
  - `action_ref` matches `^[0-9a-f]{40}$` (a commit SHA, never a tag or branch);
  - no two entries share the same `Identity`;
  - exactly one entry has `current: true`, and its platform is `linux/amd64` **[default]**.
- An invalid embedded manifest panics at package initialization **[default]**. The binary cannot start with a
  broken manifest, and the unit test catches it before release.
- The values must come from the engine release evidence of plan task T01 (engine source revision
  `f8877d25ba36d84d3780071580d23486e3d794d0`, tag `v2.3.1`). Today only `profile_version`
  (`archfit.measurement.v1`) is real. The shipped image digests and the Action commit are placeholders: `sha256:`
  plus 64 times `a` on `linux/amd64`, `sha256:` plus 64 times `b` on `linux/arm64`, and 40 times `c` as `action_ref` of
  both rows. They wait for the published v2.3.1 image digests and the pinned `archfit-action` commit. This is a release
  blocker recorded in `docs/plans/2026-09-21-implement-archfit-app.md`. Until it is resolved every real upload misses
  `Lookup` and concludes `action_required`, and a generated workflow pins an image and a commit that do not exist.
- A placeholder is an `image_digest` or `action_ref` whose hex part is one character repeated. Placeholders are
  forbidden in a release: `TestManifestHasNoPlaceholdersInRelease` fails when the environment variable
  `ARCHFIT_RELEASE=1` is set and any row carries one. The release pipeline must run the tests with that variable. The
  variable is read by the test only; it never reaches `Lookup` or `Current`.
- The synthetic `.envelope.json` siblings in `internal/wire/testdata/report/` state the `linux/amd64` row's identity.
  Replacing the placeholders means editing `manifest.json`, those envelopes and the test's list of documented
  placeholders in one commit.
- Validation errors name the entry index, the field and the rule. They never quote a value.
- "Locked" means: no environment variable, flag, file path, Store key, or network fetch can add or change a row.
  The only way to change it is a reviewed commit to `manifest.json`.
- Decoding of the data file is strict: unknown keys are an error.

## Public Contract

```go
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
func Lookup(id Identity) (Entry, bool)

// Current returns the entry new workflows are generated against (linux/amd64).
func Current() Entry
```

Semantics not visible in the signatures:

- `Lookup` compares all three `Identity` fields by exact, case-sensitive string equality. No trimming, no
  lowercasing, no prefix match, no version ranges. A miss returns the zero `Entry` and `false`.
- An `Identity` with any empty field is a miss, even if a row could otherwise match.
- `Current` never fails: package initialization guarantees exactly one current row.
  `Lookup(Current().Identity)` is always a hit.
- `Entry` and `Identity` are plain values. Callers cannot reach or change package state through them.
- `ProfileVersion` is the value the gate compares with the report's `measurement_profile.version`. This module
  does not know the report type; the consumer passes no report in.
- `Identity` has no JSON tags: it is not a wire form. The consumer builds it from three envelope strings.

## Integrations

This module imports only the standard library. It depends on no internal module. The entries below are its
consumers (the consumer depends on this module) and its external counterparts. Values are from the root coupling table.

| Counterpart                       | Direction                          | Strength | LCA / Rank / Distance     | Volatility | Balanced? |
| --------------------------------- | ---------------------------------- | -------- | ------------------------- | ---------- | --------- |
| `internal/gate`                   | gate depends on manifest           | Contract | root / 2 / 2              | High       | Yes       |
| `internal/gate/ingest`            | ingest depends on manifest         | Contract | root / 2 / 2              | High       | Yes       |
| `internal/gate/feedback`          | feedback depends on manifest       | Contract | root / 2 / 2              | High       | Yes       |
| `internal/control/onboarding`     | onboarding depends on manifest     | Contract | root / 2 / 2              | High       | Yes       |
| `archfit` engine release (v2.3.1) | manifest records released identity | Contract | outside the tree; highest | Moderate   | Yes       |
| `archfit-action` (external repo)  | manifest pins one Action commit    | Contract | outside the tree; highest | Moderate   | Yes       |

Rank for `internal/gate`: the LCA is the root, and the deeper module (`wire/manifest`) is two levels below it. The
same holds for every depth-2 consumer.

Shared knowledge, per counterpart:

- `internal/gate` holds `Identity`, `Entry` and `Lookup`. It builds an `Identity` from the upload's envelope
  strings and treats the identity as approved only when `Lookup` hits, the report carries a measurement profile,
  and that profile's version equals `Entry.ProfileVersion`. A miss becomes `action_required`, never a rejection.
- `internal/gate/ingest` holds `Identity` only: it builds the value from the three envelope strings and publishes
  it in its `Upload`. It never calls `Lookup`.
- `internal/gate/feedback` holds `Identity` only: the claimed identity is a field of its publish request and is
  printed in the evidence block. It never calls `Lookup`.
- `internal/control/onboarding` holds `Entry` and `Current`. It writes `ActionRef` and `ImageDigest` into the
  generated workflow file, and passes `ImageDigest` to managed discovery.
- `archfit` engine release: this module holds the released version string, the published image digests per
  platform, and the profile version those images emit. It holds no engine code or schema.
- `archfit-action`: this module holds one commit SHA. Nothing else about the Action is known here.

Both external integrations are contract coupling through immutable identifiers (digest, commit SHA), which
tolerates the distance of separate repositories and release cycles.

## Change Vectors

- A new engine or image release: add rows to `manifest.json`, move `current`, relax the `v2.3.1` rule.
- Retiring an image: delete its row; reports from it then conclude `action_required`.
- Image variants per language (trust-and-reporting.md §3 packaging): more rows per platform; `current` stays explicit.
- A new pinned Action commit: edit `action_ref`.

## Constraints and Invariants

- Unknown never compares as equal (decisions.md §3, plan T01). Empty, partial, or unlisted identities miss.
- The manifest is deployment identity only. A hit does not make measurements independently verified; feedback
  still says "customer-attested". An App manifest never turns unknown measurement evidence into known evidence.
- No runtime mutation and no runtime source of rows other than the embedded file.
- No I/O after initialization, no clock, no logging. Standard library only. No other internal import.
- `Lookup` and `Current` are safe for concurrent use: state is read-only after initialization.
- Inherited from the parent: transport and identity types here are never storage or domain types.

## Test Specification

### Unit Tests

- **Test name**: `TestEmbeddedManifestIsValid`
  **Scenario**: load the embedded file through the same validation the package initialization uses.
  **Expected behavior**: passes; every row is engine `v2.3.1`; exactly one `current` row on `linux/amd64`; no all-zero digest.
- **Test name**: `TestManifestValidationRules`
  **Scenario**: table of in-memory manifests, one defect per row: no entries; empty `profile_version`; digest with 63 hex
  characters; all-zero digest; platform `darwin/arm64`; `action_ref` `v1`; duplicate identity; no current row; two current rows;
  current row on `linux/arm64`; unknown key `notes`; `version: 2`.
  **Expected behavior**: each row fails validation with an error naming the field and the rule; no error text contains the
  defective value; the valid base passes.
- **Test name**: `TestManifestHasNoPlaceholdersInRelease`
  **Scenario**: collect every placeholder value of the embedded rows, as `entry index, field`; read `ARCHFIT_RELEASE`. The
  test never skips.
  **Expected behavior**: with `ARCHFIT_RELEASE=1`, no placeholder is left. Without it, the placeholders found are exactly
  the documented ones (both image digests, both `action_ref` values), so a partial replacement fails; once all are
  replaced the documented list is emptied in the same commit.
- **Test name**: `TestLookupExactMatch`
  **Scenario**: table against a known row: exact identity; digest uppercased; version without the `v`; platform
  `linux/arm64` when only `linux/amd64` is listed for that digest; leading space in each field.
  **Expected behavior**: only the exact identity hits; every other row returns the zero `Entry` and `false`.

### Integration Contract Tests

- **Test name**: `TestCurrentIsApproved`
  **Scenario**: call `Current`, then `Lookup(Current().Identity)`.
  **Expected behavior**: hit; the returned `Entry` equals `Current()`; `Platform` is `linux/amd64`; `ActionRef` is 40 lowercase hex.
- **Test name**: `TestLookupReturnsProfileAndActionRef`
  **Scenario**: look up every embedded row by its identity.
  **Expected behavior**: each hits and returns that row's `ProfileVersion` and `ActionRef` unchanged.
- **Test name**: `TestManifestMatchesFixtureProducers`
  **Scenario**: every report fixture in `internal/wire/testdata/report/` with an `.envelope.json` sibling. The identity is the
  envelope's `engine_version`, `image_digest` and `platform`; the `producer` object of an expectation carries no image
  digest or platform. A fixture is approved unless its expectation has `facts.identity_approved: false`.
  **Expected behavior**: an approved identity hits `Lookup`, and the expectation's `fields.profile_version` equals
  `Entry.ProfileVersion`; an unapproved one (`unlisted-image`) misses. At least one fixture of each kind exists.

### Boundary Tests

- **Test name**: `TestUnknownNeverEqualsUnknown`
  **Scenario**: table: zero `Identity`; each single field empty with the other two taken from a real row.
  **Expected behavior**: all miss.
- **Test name**: `TestNoRuntimeOverride`
  **Scenario**: set environment variables such as `ARCHFIT_MANIFEST` and `ARCHFIT_IMAGE_DIGEST`, then look up an unlisted identity.
  **Expected behavior**: miss; the package API exposes no function that adds or replaces rows.
- **Test name**: `TestPackageImportsStdlibOnly`
  **Scenario**: `go list -deps` for the package.
  **Expected behavior**: no internal package, no third-party package, no `net`, `os`, or `log` package.
- **Test name**: `TestConcurrentLookup`
  **Scenario**: 100 goroutines call `Lookup` and `Current` under the race detector.
  **Expected behavior**: no race; identical results.

### Behavior Tests

- **Test name**: `TestWrongImageIsNotApproved`
  **Scenario**: the current identity with one hex character of the image digest changed (T03: wrong image).
  **Expected behavior**: miss.
- **Test name**: `TestOlderEngineIsNotApproved`
  **Scenario**: identity with `EngineVersion: "v2.3.0"` and a current image digest.
  **Expected behavior**: miss; v2.3.0 snapshots are non-comparable and v1 approves v2.3.1 only.
- **Test name**: `TestGeneratedWorkflowInputsArePinned`
  **Scenario**: read `Current()` as onboarding would.
  **Expected behavior**: `ActionRef` is a commit SHA and `ImageDigest` a digest; neither is a tag, branch, or `latest`.

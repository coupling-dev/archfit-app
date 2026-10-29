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
- `EngineVersions`: the engine releases the rows approve.
- Nothing else. It does not read reports, compare profiles, or decide outcomes.

## Subdomain Classification

Published language (release identity shared with `archfit` and `archfit-action`). Supporting.
Volatility: moderate. It changes only with a reviewed engine, image or Action release. The shipped rows are engine
v3.4.0 on `linux/amd64` (`current`) and `linux/arm64`. The v3.2.3, v3.2.2 and v3.2.0 tuples left with the Actions that carried them.

## Encapsulated Knowledge

- The data file `manifest.json`, embedded with `go:embed`, and its shape (private to this package):
  top-level `version` (must be 1) and `entries`, a list of objects with exactly these keys: `engine_version`,
  `image_digest`, `platform`, `profile_version`, `action_ref`, and an optional boolean `current`.
- Row validity rules, checked once at package initialization and again by a unit test:
  - at least one entry; every string non-empty;
  - `engine_version` is a release tag `vMAJOR.MINOR.PATCH` (no pre-release, no build suffix);
  - `image_digest` matches `^sha256:[0-9a-f]{64}$` and is not all zeros;
  - `platform` is `linux/amd64` or `linux/arm64`;
  - `action_ref` matches `^[0-9a-f]{40}$` (a commit SHA, never a tag or branch);
  - no two entries share the same `Identity`;
  - exactly one entry has `current: true`, and its platform is `linux/amd64` **[default]**.
- An invalid embedded manifest panics at package initialization **[default]**. The binary cannot start with a
  broken manifest, and the unit test catches it before release.
- What a release needs before its rows land, checked by the parent `wire` tests because the children never import
  each other: `wire/report` reads the release (its state key set is known), the corpus holds an unmodified capture of
  that release that decodes, and, when `current` moves to it, `wire/policy` embeds its config schema. The format
  rule above is all this package can check alone; it is not an approval of any version string.
- The values must come from the engine release evidence. `profile_version` (`archfit.measurement.v2`, the engine's
  `MeasurementProfileVersion`, read from the unmodified capture) and the image digests are real:
  - v3.4.0 (engine source revision `2692cd6b8aab23e559d0aa76712a192d69b4229f`):
    `sha256:bdcfe29bf9def9a4f0ae544923bc3e755fdec2c2cd6c46ec353c01e60642896b` on `linux/amd64` and
    `sha256:0cadc39c41de4dc58bb115b22bdd0296cb81688175a6ea1839a1e16ed2c35e1f` on `linux/arm64`.
    The OCI index `sha256:e4c258d0698c223cca223568a9fec1cd5393937cc6e6ce58def1f62b66205dd4` is not a platform approval.
    `profile_version` is `archfit.measurement.v2`: the engine constant `MeasurementProfileVersion` at the tag, and the
    `comparison.measurement_profile.version` of the unmodified native capture `real-v340-arm64-blocked-no-baseline`.
  - v3.2.3 (engine source revision `c7490636480a8faedddf3bff1d0a678a7ab7987c`), retired: `linux/amd64`
    `sha256:87323e8d80d519531338ff7a36ce750d337ecf6560e48493bdeb10b864e7b772`, `linux/arm64`
    `sha256:54f6bf1ec99be6f4bdc21ca59e16d3dd65fb5b31a2c2af6c932ad556c89a220c`. Its captures stay in the corpus as history.
  - v3.2.2 (engine source revision `90c0ca240143329d404eb4eb116b096f607f7dc5`):
    `sha256:bd351ac70610555237974fac5ff914967a1100fb8ef754853151300d5d19ca35` on `linux/amd64` and
    `sha256:e97a200a7c26907e4d30982b06e61ac7cc27bffd1d46002ad53bbf6b6594c479` on `linux/arm64`.
    The OCI index is not an approved platform pin.
  - v3.2.0 (engine source revision `faa4eb4e513859567fd6c380230c91ea22fdaab6`): the per-platform digests of
    `ghcr.io/alexei-led/archfit:v3.2.0` (index `sha256:36e8d4e2d7e3eb15246888b386cb82e7b495607f1fb536a593d2ee733391f886`,
    equal to the release's `image-identity.json`),
    `sha256:5f2d4a5fff1ace10d7b08937429179631ae0c1d0e1f9e6f37c72b30954cf00db` on `linux/amd64` and
    `sha256:725c3a074358df8ea41881296eda06b07daed7934d7a6d40bb1ecfc3c8a8128d` on `linux/arm64`. The native image outputs in Action CI run40175778074 carry `archfit.measurement.v2`.

  Every row pins archfit-action commit `7ade3b8fc989ec416ea5d570e7e236f47f7845fc` (Action v3.1.0) (X1: pull requests are measured as
  merged, and the envelope carries `analyzed_sha` and `integration`; X3b: the envelope may carry the engine's
  `policy_change`; vendored envelope schema sha256
  `0c2de1a3691ac85935f04887b94bc98dbc01f57f65a9c4fc3db276d5bf78b7e6`). That commit runs engine v3.4.0 only; its engine-version and image-digest inputs are optional and default to
  that engine and the digest of the runner's platform, so generated workflows pass neither. Action v3.0.0 (`fb2520cb069571d6bdc9274ac97d4df49994b6c7`)
  sends the same identity and the same envelope bytes, so a workflow still pinned to it keeps working. CI validates every distinct Action commit and compares each vendored envelope
  schema with the App contract, in the existing static required jobs: a manifest that still named an older Action would
  fail it, so the older Actions and the engine releases they carried are not in the manifest. Their uploads end
  `unknown_engine_identity`. The engine bump replaces these rows once, in the release change.
  `Lookup` does not read `action_ref`; it is what a generated workflow pins.
- A placeholder is an `image_digest` or `action_ref` whose hex part is one character repeated. Placeholders are
  forbidden in a release: `TestManifestHasNoPlaceholdersInRelease` fails when the environment variable
  `ARCHFIT_RELEASE=1` is set and any row carries one. The release pipeline must run the tests with that variable. The
  variable is read by the test only; it never reaches `Lookup` or `Current`.
- Historical report/envelope pairs retain their original bytes and now expect their old tuples to be unapproved.
  New `real-v320-*` captures use the released image and Action; their current tuples are approved.
  The explicitly synthetic baseline-tampering envelope uses the current tuple so it still tests input binding.
  Fixture producer metadata distinguishes captured reports from synthetic event/transport metadata.
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

```go
// EngineVersions returns the engine releases the manifest approves, each once, in the order of their first row.
func EngineVersions() []string
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
- `EngineVersions` returns a fresh slice on every call, today `v3.4.0`. It approves nothing by itself:
  `Lookup` on the whole identity stays the only approval. The parent `wire` tests read it to tie every approved
  release to the report decoder, to a real capture and, through `Current`, to the policy schema pin.

## Integrations

This module imports only the standard library. It depends on no internal module. The entries below are its
consumers (the consumer depends on this module) and its external counterparts. Values are from the root coupling table.

| Counterpart                       | Direction                          | Strength | LCA / Rank / Distance     | Volatility | Balanced? |
| --------------------------------- | ---------------------------------- | -------- | ------------------------- | ---------- | --------- |
| `internal/gate`                   | gate depends on manifest           | Contract | root / 2 / 2              | High       | Yes       |
| `internal/gate/ingest`            | ingest depends on manifest         | Contract | root / 2 / 2              | High       | Yes       |
| `internal/gate/feedback`          | feedback depends on manifest       | Contract | root / 2 / 2              | High       | Yes       |
| `internal/control/onboarding`     | onboarding depends on manifest     | Contract | root / 2 / 2              | High       | Yes       |
| `archfit` engine (v3.2.0)         | manifest records released identity | Contract | outside the tree; highest | Moderate   | Yes       |
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

- A new engine or image release: replace the rows in `manifest.json` and keep one `current` row. The same App change
  updates `wire/report` (with any key the release adds), adds a real capture of it to the corpus, and re-pins
  `wire/policy`'s config schema; the parent tests fail until all three are there.
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
  **Expected behavior**: passes; every row's engine version is in the version list; exactly one `current` row on `linux/amd64`;
  no all-zero digest.
- **Test name**: `TestManifestValidationRules`
  **Scenario**: table of in-memory manifests, one defect per row: no entries; empty `profile_version`; digest with 63 hex
  characters; all-zero digest; platform `darwin/arm64`; `action_ref` `v1`; engine version `2.3.1`, `v2.3`, `v2.4.0-rc.1` and
  `latest`; duplicate identity; no current row; two current rows; current row on `linux/arm64`; unknown key `notes`; `version: 2`.
  **Expected behavior**: each row fails validation with an error naming the field and the rule; no error text contains the
  defective value; the valid base passes.
- **Test name**: `TestApprovedEngineVersions`
  **Scenario**: call `EngineVersions`, change the returned slice, call it again.
  **Expected behavior**: `v3.4.0` both times, a release tag; it changes only together with `manifest.json`.
- **Test name**: `TestManifestHasNoPlaceholdersInRelease`
  **Scenario**: collect every placeholder value of the embedded rows, as `entry index, field`; read `ARCHFIT_RELEASE`. The
  test never skips.
  **Expected behavior**: with `ARCHFIT_RELEASE=1`, no placeholder is left. Without it, the placeholders found are exactly
  the documented ones, so an undocumented placeholder fails. The documented list is empty: the image digests and the
  Action commit are all real.
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
  **Expected behavior**: miss; the package API is exactly `Lookup`, `Current`, `EngineVersions`, `Entry` and `Identity`, and
  none of them adds or replaces rows.
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
  **Scenario**: the published v3.0.0 and v2.5.0 identities on both platforms and the v2.4.1 `linux/amd64` identity.
  **Expected behavior**: all miss; the manifest holds only the exact v3.4.0 rows.
- **Test name**: `TestGeneratedWorkflowInputsArePinned`
  **Scenario**: read `Current()` as onboarding would.
  **Expected behavior**: `ActionRef` is a commit SHA and `ImageDigest` a digest; neither is a tag, branch, or `latest`.

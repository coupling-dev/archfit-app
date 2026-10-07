# Wire Contracts

**Path**: internal/wire/
**Parent**: internal/ (Archfit App)
**Submodules**: envelope, report, settings, policy, manifest

## Purpose

`wire` is the published language between three repositories: `archfit` (engine), `archfit-action` (producer) and
this App, plus the files customers keep on their protected ref. It groups every versioned transport contract in one
place, separate from domain and storage types (AGENTS.md). Without it, contract types would drift into the gate
and the control plane, a version bump would touch domain logic, and nobody could say which bytes the App accepts.

## Functional Responsibilities

- Publish five child packages, each owning one contract: the upload envelope, the no-source state report, the App
  settings file, the App's view of the engine policy files, and the approved identity manifest.
- State the versioning rule all five follow.
- Hold the shared conformance fixtures in `wire/testdata/` (a transparent folder, not a module) used by the
  children's tests and by consumer tests such as the gate verdict table.
- The parent package itself has no code except `doc.go`.

## Subdomain Classification

Published language. Supporting: necessary, not differentiating. Volatility: moderate. Contracts change only with a
versioned engine or Action release, or a product decision about the settings file. Consumers are high-volatility
core modules, which is why everything here is contract-strength and strictly versioned.

## Encapsulated Knowledge

- The rule for what belongs here: transport types, schemas, strict decoding, digests. No domain decisions, no I/O.
- The fixture corpus: its layout, the named cases, and the producer artifact each one was captured from.
- The pinned external revisions the contracts were taken from: engine `archfit` v2.3.1 at
  `f8877d25ba36d84d3780071580d23486e3d794d0`, v2.4.0 at `5aefcdc2c5465c2eb7198ea91394bd8cb8ad2b22` and v2.4.1 at
  `ca1f593e69ddf41662cbebe6ccb0fb59b09f73fd` (the same state schema), and v2.5.0 at
  `59318b9eec906f54bc7a54d01db634b0c20c0647` (state schema with `seams[].policy`, config schema);
  `archfit-action` at `4759dd985bff385af5c2dee2c918a0eced59f4f3`
  (plan inventory of 20 September 2026).
- Each child's internals (decoder technique, caps, file formats) stay in that child.

## Public Contract

`wire` publishes its children as importable Go packages. Go cannot re-export a package without alias boilerplate,
and these children hold no hidden internals, so consumers import and cite the child directly. The contract of each
child is normative in the child's own `module.md`; it is not restated here.

### Published packages

| Package                  | Owns                                                                                                 |
| ------------------------ | ---------------------------------------------------------------------------------------------------- |
| `internal/wire/envelope` | report-envelope v1: `Envelope`, `Kind`, `Decode`, `PayloadDigest`, `Schema`                          |
| `internal/wire/report`   | engine state v1 as no-source transport: `Report` and its parts, `Decode`, `EngineVersions`, size cap |
| `internal/wire/settings` | `.archfit-app.yaml`: `Settings`, modes, `Parse`, `Render`, file location                             |
| `internal/wire/policy`   | engine file locations, bare-hex `Digest`, `Waivers`, `Modules`, `ReadBaseline` readers, config `Schema` |
| `internal/wire/manifest` | locked approved identities: `Identity`, `Entry`, `Lookup`, `Current`, `EngineVersions`               |

### Versioning rule

- Every contract carries a version identifier: envelope `archfit.report-envelope.v1`; report
  `archfit.architecture-state.v1`; settings file `version: 1`; manifest data `version: 1`; policy follows the
  engine release of the manifest's `current` row, v2.5.0 today; the engine baseline file `archfit.baseline.v2`.
- Unknown fields are rejected by every decoder here. Three deliberate nuances: the policy readers ignore
  engine-owned keys they do not read; the report decoder knows every key of each engine release it reads and
  drops the known keys that are not allowlisted, while a key that release's schema does not define is still an error;
  the baseline reader (`policy.ReadBaseline`) ignores baseline keys it does not read, as the engine's own decoder does.
- The envelope `kind` enum gained `baseline` inside v1: a new accepted value is additive, so the version stays.
- Engine releases evolve without a flag day. The manifest approves rows of several releases (N and N-1 during an
  upgrade window). The report decoder reads the document of the release the upload envelope claims, with that
  release's key set and nothing else; a release it does not list fails decoding, so the upload ends
  `action_required`. The gate decides only from the frozen decision core, which every listed release defines (see
  `report`). Release order: the engine tags N; one App change lists N in `report` with any key N adds, adds a real
  capture of N to the corpus, adds the manifest rows and re-pins the policy schema when `current` moves; after the
  App release, workflows move their pin; then N-1's rows go. The parent tests below hold the three together.
  Closing a window deletes manifest rows only: the decoder keeps reading the release while corpus fixtures are its
  documents (v2.3.1 and v2.4.0, whose rows are gone, are still listed in `report` because every synthetic fixture
  is a v2.3.1 document and `real-blocked-no-baseline-v240` a v2.4.0 one).
- Two digest formats exist and are never compared with each other: engine-style bare lowercase hex (`policy.Digest`,
  the report's `config_hash`, the envelope's `baseline_digest` and `labels_digest`) and the prefixed `sha256:<hex>`
  form of envelope-native values (`payload_digest`, `image_digest`) **[default]**.
- A wrong or unknown version identifier is a decode error, never a best-effort parse.
- A breaking change means a new version constant and a new package-level decode path next to the old one. The old
  path stays until no approved producer emits the old version. Breaking: removing or renaming a field, changing a
  field's meaning or format, tightening a cap, making an optional field required.
- Because unknown fields are rejected, a producer may send a new optional field only after an App release that
  allows it. App first, then Action or engine **[default]**. Adding an optional allowlisted field this way keeps the
  version. For the report, the field is allowed for the engine release that adds it and later ones only.
- A version bump never changes a consumer's domain types; consumers map from the new transport type.

### Conformance fixtures

`internal/wire/testdata/` is read by tests through relative paths. It is test data, not an import. Layout **[default]**:

| Folder      | Content                                                                             |
| ----------- | ----------------------------------------------------------------------------------- |
| `envelope/` | envelope JSON documents, valid and invalid                                          |
| `report/`   | state report payloads; optional sibling policy, baseline, labels and envelope files |
| `policy/`   | engine policy files, including waiver expiry and order cases                        |
| `leakage/`  | seeded source-bearing payloads; each contains the marker `LEAK-7f3a`                |
| `schema/`   | pinned copy of the engine's `archfit.state.schema.json` with its source revision    |

Files in `schema/` are reference data, not fixtures, and have no expectation sibling. The folder holds the schema
and `SCHEMA_SOURCE`, a `key: value` note with `repository`, `tag`, `revision`, `path` and `sha256`.

The four fixture folders are flat. A fixture is `<name>.<ext>`; `<name>` has no dot and is unique in its folder.
Every other file is a sibling of a fixture, named `<name>` plus one of these suffixes **[default]**:

| Suffix           | Content                                                                        |
| ---------------- | ------------------------------------------------------------------------------ |
| `.expect.json`   | the expectation; required for every fixture                                    |
| `.policy.yaml`   | `report/`: the policy file the engine read; its digest is `config_hash`        |
| `.baseline.json` | `report/`: the baseline file the engine loaded                                 |
| `.labels.yaml`   | `report/`: the labels file the engine loaded                                   |
| `.envelope.json` | `report/`: the envelope a producer sends with this report; synthetic           |
| `.payload`       | `envelope/`: the exact payload bytes that `payload_digest` was taken from      |

A sibling without its fixture fails the corpus test. Siblings have no expectation of their own.

Every fixture `<name>.<ext>` has a sibling `<name>.expect.json` with these keys and no others:

| Key        | Meaning                                                                                                |
| ---------- | ------------------------------------------------------------------------------------------------------ |
| `producer` | object: `repository`, `revision` (40 hex), `engine_version`, `command`; or `"synthetic"` with a reason |
| `decode`   | `"ok"` or `"error"`                                                                                    |
| `fields`   | for `ok`: the decision and identity values the child's contract test asserts                           |
| `gate`     | report fixtures only: object with `conclusion` and `reasons`, the ordered list of reason codes         |
| `facts`    | optional, report fixtures only: object of overrides to the clean facts; absent means all clean         |

A synthetic `producer` is one string, `"synthetic: <reason>"`; for a hand-edited report the reason records the
source capture and the exact `jq` edit **[default]**. `fields` by folder: `envelope/` holds the whole decoded
envelope; `report/` and `leakage/` hold `verdict`, `hard_gates`, `unevaluated_rule_ids`, `has_profile`,
`profile_version` and `gate_reference_status`; `policy/` holds `modules` (`name`, `layer`, `line`) and `waivers`
(`rule`, `from`, `to`, `reason`, `approved_by`, `expires`). A `policy/` fixture is `ok` only when both readers
succeed. `fields` is absent for `error`.

`gate.reasons` is never empty, and `reasons[0]` is the code that determines `conclusion`. Later codes are the extra
reasons the gate preserves, in order. Example: the `blocker-and-unevaluated` fixture expects `failure` with
`["blocking_findings", "missing_required_coverage"]`.

Outcomes are driven by the report's `verdict`, `decision.hard_gates` and `gate_reference.status`, together with the
facts. A report has no exit code. The expected outcome holds under clean facts except the one eligibility fact the
fixture exercises. Clean facts are: identity approved, workflow approved, settings found, no approval needed, not
suspended, no problems, and consistent digests: the trusted policy digests are exactly the report's `config_hash`;
the trusted baseline and labels digests are `policy.Digest` of the sibling files (empty list when there is no
sibling file); the envelope states those same baseline and labels digests (`""` when there is no file).

`facts` lists overrides with these keys, all optional:

- `identity_approved`, `workflow_approved`: booleans.
- `problems`: list of problem codes.
- `baseline_digest`, `labels_digest`: what the envelope states, bare hex or `""`.
- `trusted_policy_digests`, `trusted_baseline_digests`, `trusted_labels_digests`: lists of bare-hex digests the
  protected ref yields.

Examples: `unknown-identity` carries no measurement profile, so its expectation has
`"facts": {"identity_approved": false}` and `gate.reasons` `["measurement_profile_missing", "blocking_findings"]`
(its report is also blocked, and the gate keeps every reason that applies). `invalid-input` has
`decode: "error"`, `"facts": {"problems": ["report_invalid"]}` and `gate.reasons` `["report_invalid"]`. A baseline
tampering fixture overrides `baseline_digest` with a value outside `trusted_baseline_digests`. Any other key in
`facts` fails the corpus test.

Fixtures captured from the engine cite their engine release and that release's pinned revision (v2.3.1:
`f8877d25…`; v2.4.0: `5aefcdc2…`; v2.4.1: `ca1f593e…`; v2.5.0: `59318b9e…`); fixtures from a mutable branch are not
allowed. The tests, and every consumer test that reads the whole corpus, decode a capture as a document of the
release it cites, and a synthetic fixture as a v2.3.1 document, the release every one of them was derived from.
Every fixture is a v2.3.1 document except `real-blocked-no-baseline-v240`, `real-blocked-no-baseline-v241`,
`real-blocked-no-baseline-v250` and `real-seam-policy-v250`.
Required report fixtures (plan T02): `healthy`, `attention-pass` (verdict `needs_attention` with passing hard
gates), `blocked`, `missing-evidence` (`hard_gates: unmeasured`), `unknown-identity`, `profile-comparable` (symmetric
partial with a recognized `partial_basis`), `profile-incompatible` (`gate_reference.status: non_comparable`),
`invalid-input`, `blocker-and-unevaluated`, `non-applicable-rule`, `locations-empty`, `matched-by-populated`
(decodes; `matched_by` is dropped and never in any output), `repair-kinds`, `waiver-expiry-order`, and
`real-blocked-no-baseline`. The last one is captured unmodified from the real engine v2.3.1 on a two-module Go
repository: verdict `blocked`, `hard_gates: fail`, no baseline file, so `gate_reference` is `non_comparable` with
reason `no baseline file was loaded`; its policy file ships beside it and its facts have empty trusted baseline and
labels lists. Its `gate` expectation is `failure` with `["blocking_findings"]`: with no trusted baseline the gate
ignores the engine's `non_comparable`.

All required fixtures except the last are hand-edited from that capture, so they inherit its `non_comparable`
`gate_reference`. `blocked` and `profile-incompatible` exercise comparability through `facts`
(`trusted_baseline_digests` with a matching `baseline_digest`). `profile-comparable` does not yet have the symmetric
partial its name promises: its bytes are a blocked report with a `non_comparable` reference, and its expectation
records that.

Further engine captures, unmodified, each with its policy file and the baseline or labels file the engine loaded:
`real-healthy` (verdict `healthy`, comparable baseline; the engine reaches `healthy` only when all nine dimensions
are measured, so it is captured on the engine's reachability fixture), `real-attention-baseline`,
`real-attention-no-baseline`, `real-baselined-comparable` (finding status `baseline`), `real-baselined-with-labels`,
`real-blocked-forbidden-and-allowed` (one forbidden and one allowed dependency, comparable baseline, finding status
`new`), `real-unmeasured-no-inventory`, `real-waiver-expired`, `real-waiver-active`, and `real-polyglot-blocked`:
the engine fixture with a cargo crate, a Python and a TypeScript package added and every optional analyzer on, so
its producer rows carry the real tool versions of cargo, cargo-modules, grimp, dependency-cruiser, scip, jscpd and
ast-grep. Two byte copies of captures ship a synthetic envelope: `unlisted-image` (image digest outside the
manifest) and `baseline-tampered`. Every synthetic envelope states engine v2.5.0, the `current` row; the reports
beside them are v2.3.1 documents, which v2.5.0 reads too: its key set only adds the optional `seams[].policy` (see
`manifest`).

Two hand-edited fixtures cover what real engine output reached the old App limits with: `producer-versions` (the
remaining tool-version formats: grimp failing with `uv`, the macOS system git, two coverage parsers) and
`large-module` (150 locations on one finding, 152 task files and a 906-character public-surface constraint).
The captures, like the earlier ones, come from a development host, not from the pinned image.

`real-blocked-no-baseline-v240` is the v2.4.0 capture the manifest's v2.4.0 rows need: the command of
`real-blocked-no-baseline` on the engine's `testdata/fixture-go` at tag v2.4.0 (unchanged since v2.3.1, so its policy
file is byte-identical), run with the released binary `archfit-v2.4.0-darwin-arm64` from the GitHub release, checked
against the release's `SHA256SUMS`, on a development host, not the pinned image. It is unmodified. Its key paths are
those of the v2.3.1 capture; the bytes differ only in an agent-task constraint (v2.4.0 no longer lists the target's
public surface on a forbidden-dependency task) and the host's git version. Its expectation is the v2.3.1 one: blocked,
`hard_gates: fail`, `non_comparable` gate reference, `failure` with `["blocking_findings"]`.

`real-blocked-no-baseline-v241` is the v2.4.1 capture the manifest's v2.4.1 rows need, produced the same way: the
command of `real-blocked-no-baseline` on `testdata/fixture-go` at tag v2.4.1 (policy file byte-identical), run with
the released binary `archfit-v2.4.1-darwin-arm64`, checked against the release's `SHA256SUMS`, on a development host,
not the pinned image. It is unmodified, and its bytes equal the v2.4.0 capture: v2.4.1 changes only the config
schema (the rule types become an enum), and the report names no engine version. Its expectation is the v2.4.0 one.

`real-blocked-no-baseline-v250` is the v2.5.0 capture the manifest's v2.5.0 rows need, produced the same way: the
command of `real-blocked-no-baseline` on `testdata/fixture-go` at tag v2.5.0 (policy file byte-identical), run with
the released binary `archfit-v2.5.0-darwin-arm64`, checked against the release's `SHA256SUMS`, on a development host,
not the pinned image. It is unmodified, and its bytes equal the v2.4.1 capture except for `"policy":"violation"` on
its one seam, the one state key v2.5.0 adds. Its expectation is the v2.4.1 one.

`real-seam-policy-v250` is the v2.5.0 capture that carries four of the five `seams[].policy` values (no `advisory`:
the run has no advisory finding): the `hexagonalRepo` scenario of the engine's `cmd/archfit/map_test.go` at tag v2.5.0, rebuilt file
for file (five Go packages, the test's policy with layers, a fail-gated `forbidden_layer_direction` and a fail-gated
`forbidden_dependency` rule). `archfit baseline` runs first; then `billing` imports `stripe`, and
`archfit check --json -c .archfit.yaml -q --refresh` runs with the same released binary. It is unmodified, and its
seam statuses equal the engine's `map` golden file for the scenario: `api -> billing` accepted, `api -> stripe`,
`orders -> billing` and `stripe -> orders` allowed, `billing -> stripe` violation, `orders -> audit` observed. Its
policy and the baseline the engine loaded ship beside it. The added import closes a Go import cycle, so four packages
do not type-check: both required rules are unevaluated, and the baseline (go/packages `ok`) is `non_comparable` with
the report (`partial`). Its expectation records that: blocked, `hard_gates: fail`, both rule IDs unevaluated,
`action_required` with `["baseline_not_comparable", "blocking_findings", "missing_required_coverage"]`.

The `leakage/` set has two groups. Rejecting fixtures (`decode: "error"`): snippet field, log blob, raw model output
key, multi-line text, oversized text. One surviving fixture (`decode: "ok"`): a marker identifier inside
`agent_tasks[].declarations`, `matched_by` and a seam `hypothesis`; it must decode, and the marker must be gone.

## Integrations

`wire` has no code, so it has no code-level integration of its own. Its children import only the standard library,
and `settings` and `policy` one YAML library. Internal consumers integrate with a child, not with this parent;
each child document lists them with LCA, rank and distance. All of them are contract-strength across the root
(rank 2, distance 2; rank 3, distance 3 for `control/ui/graph`), balanced at any volatility.

External counterparts, reached only through these contracts:

| Counterpart                       | Direction                                       | Strength                    | LCA / Rank / Distance     | Volatility | Balanced? |
| --------------------------------- | ----------------------------------------------- | --------------------------- | ------------------------- | ---------- | --------- |
| `archfit` engine (v2.4.1, v2.5.0) | report, policy, manifest follow engine releases | Contract (versioned schema) | outside the tree; highest | Moderate   | Yes       |
| `archfit-action` (external repo)  | the Action conforms to envelope and report      | Contract (JSON Schema)      | outside the tree; highest | Moderate   | Yes       |
| customer repositories (the files) | customers write settings and policy files       | Contract (versioned file)   | outside the tree; highest | Moderate   | Yes       |

Shared knowledge: with the engine, the complete state schema key set (the v2.3.1 one, unchanged in v2.4.0 and v2.4.1,
plus `seams[].policy` in v2.5.0), its enums and decision semantics, the config schema, waiver keys, the `modules` map
shape, the three input file names, the bare-hex hashing rule and released identities, pinned at revision
`f8877d25ba36d84d3780071580d23486e3d794d0` (v2.3.1), `5aefcdc2c5465c2eb7198ea91394bd8cb8ad2b22` (v2.4.0),
`ca1f593e69ddf41662cbebe6ccb0fb59b09f73fd` (v2.4.1 identities) and, for the state schema, the config schema and the
v2.5.0 identities, `59318b9eec906f54bc7a54d01db634b0c20c0647` (v2.5.0), each verified
against a real report; with the Action, the envelope schema, both digest rules and the no-source transport; with customers, the settings field rules. Separate repositories and release cycles are the highest
distance the system has. Contract coupling with explicit versions, strict decoding and a shared fixture corpus is
what keeps it balanced. Installed Actions, images and stored baselines never upgrade atomically, so old decode
paths stay until retired.

## Internal Design

Five independent leaves. There is no flow between them and no composition in the parent.

| Child      | Contributes                                                  | Third-party imports |
| ---------- | ------------------------------------------------------------ | ------------------- |
| `envelope` | what the producer states about an upload; the payload digest | none                |
| `report`   | the no-source payload boundary                               | none                |
| `settings` | the App-owned file on the protected ref                      | one YAML library    |
| `policy`   | the App's read-only view of engine-owned files               | one YAML library    |
| `manifest` | approved engine, image, profile and Action identities        | none                |

Rules between children:

- Children never import each other and never import the parent. Shared meaning is carried by plain strings, and
  the consumer does the mapping. Examples: the envelope's three identity strings become a manifest `Identity` in the
  gate; `policy.Digest` output is compared with the report's `config_hash` and with the envelope's `baseline_digest`
  and `labels_digest` in the gate.
- Two small duplications are accepted to keep that rule **[default]**: the strict-JSON pre-pass exists in both
  `envelope` and `report`; SHA-256 hex formatting exists in `envelope` (prefixed) and `policy` (bare). Each is a few
  lines, and the two digests have different formats and meanings.
- `doc.go` in the parent states the placement rule and the versioning rule for Go readers.
- Fan-out is five, within the 4±1 guidance.

## Change Vectors

- A new engine release: `report`, `policy`, `manifest` and the fixtures change together; no consumer contract changes
  unless a version constant is added.
- A new envelope version for the Action: `envelope` only.
- A new settings field: `settings` only.
- A sixth contract (for example a versioned discovery result or an AI-request transport, plan T04): a new child here.
- Generating transport types from schemas instead of writing them by hand: inside each child.

## Constraints and Invariants

- Wire packages contain transport types, schemas, strict decoding and digests. No domain decisions, no I/O, no
  clock, no logging, no network, no Store access.
- Versioned wire contracts stay separate from domain and storage types (AGENTS.md). Nothing here is persisted.
- Imports: standard library only, plus one YAML library in `settings` and `policy`. No other internal package.
  Enforced by Archfit on this repository and by the tests below.
- Decoding proves shape, never truth. Provenance is necessary, not sufficient: everything decoded here is
  customer-attested until the gate binds it to authoritative GitHub data and protected-ref digests.
- Never trust head-tree config, labels, or baselines: these packages parse bytes; consumers must supply bytes read
  from the protected ref.
- Decoders are total and bounded: no panic on any input, explicit size limits, no partial results.
- Error text names fields and rules, never values, so callers' logs stay at IDs, digests, outcomes and timings.
- Engine-internal Go types are never imported or mirrored; only versioned schema shapes (trust-and-reporting.md §7).

## Test Specification

Parent tests live in package `wire_test` and exercise the children together and the fixture corpus.

### Unit Tests

- **Test name**: `TestFixtureCorpusIsComplete`
  **Scenario**: walk `testdata/`.
  **Expected behavior**: every required named fixture exists; every fixture has an `.expect.json` with `producer` and `decode`, no unknown keys, and no unknown `facts` key; every report fixture has `gate` with a non-empty `reasons` list; no orphan expectation files.
- **Test name**: `TestFixturesRecordPinnedProducers`
  **Scenario**: read every `producer` entry.
  **Expected behavior**: engine-captured fixtures cite a pinned release at its pinned revision (`v2.3.1` at `f8877d25ba36d84d3780071580d23486e3d794d0`, `v2.4.0` at `5aefcdc2c5465c2eb7198ea91394bd8cb8ad2b22`, `v2.4.1` at `ca1f593e69ddf41662cbebe6ccb0fb59b09f73fd`, `v2.5.0` at `59318b9eec906f54bc7a54d01db634b0c20c0647`); synthetic ones give a reason; none cites a branch name.
- **Test name**: `TestLeakageFixturesCarryTheMarker`
  **Scenario**: read every file in `testdata/leakage/`.
  **Expected behavior**: each contains `LEAK-7f3a`; all have `decode: "error"` except the one surviving declarations fixture, which has `decode: "ok"`.
- **Test name**: `TestPinnedEngineSchemaIsRecorded`
  **Scenario**: read `testdata/schema/`.
  **Expected behavior**: `archfit.state.schema.json` is present with a source note citing tag `v2.5.0`, revision `59318b9eec906f54bc7a54d01db634b0c20c0647` and its SHA-256; the hash matches.

### Integration Contract Tests

- **Test name**: `TestEveryFixtureDecodesAsRecorded`
  **Scenario**: route each fixture to its child decoder by folder (`envelope.Decode`, `report.Decode`, `policy.Waivers` and `policy.Modules`).
  **Expected behavior**: `ok` fixtures decode and match `fields`; `error` fixtures fail.
- **Test name**: `TestRealEngineCaptureDecodes`
  **Scenario**: `report/real-blocked-no-baseline` with its policy file, as captured from engine v2.3.1.
  **Expected behavior**: `report.Decode` succeeds on the unmodified bytes; verdict `blocked`; `gate_reference.status` `non_comparable`;
  `policy.Digest` of the policy equals `config_hash`; `policy.Modules` lists `a` and `b`, matching the seam's modules.
- **Test name**: `TestPublishedPackagesExist`
  **Scenario**: `go list ./internal/wire/...`.
  **Expected behavior**: exactly the parent and the five published packages; the parent exports no identifiers.
- **Test name**: `TestUnknownVersionRejectedEverywhere`
  **Scenario**: an envelope with `archfit.report-envelope.v2`, a report with `archfit.architecture-state.v2` under every listed engine
  release, the real capture as a document of engine `v2.6.0`, a settings file with `version: 2`.
  **Expected behavior**: all are errors.
- **Test name**: `TestEveryApprovedEngineVersionIsReadable`
  **Scenario**: every release of `manifest.EngineVersions`.
  **Expected behavior**: `report.EngineVersions` lists it, and the real capture decodes as its document.
- **Test name**: `TestEveryApprovedEngineVersionHasACapture`
  **Scenario**: every release of `manifest.EngineVersions` against the engine-captured report fixtures that decode.
  **Expected behavior**: each release has at least one.
- **Test name**: `TestPolicySchemaFollowsTheCurrentEngine`
  **Scenario**: read the `tag` of `policy/SCHEMA_SOURCE` and `manifest.Current`.
  **Expected behavior**: the tag equals the current row's engine version, so moving `current` forces the config schema re-pin.

### Boundary Tests

- **Test name**: `TestChildrenDoNotImportEachOther`
  **Scenario**: `go list -deps` for each child.
  **Expected behavior**: no child lists another `internal/wire/` package or any other internal package.
- **Test name**: `TestWireImportsNoIO`
  **Scenario**: `go list -deps` for all wire packages.
  **Expected behavior**: no `net`, `net/http`, `os`, `os/exec`, `database/sql`, `log`, or `log/slog`; third-party imports are the one YAML library, in `settings` and `policy` only.
- **Test name**: `TestDecodersNeverEchoSeededValues`
  **Scenario**: feed every `leakage/` fixture and every invalid fixture to its decoder and collect the error strings.
  **Expected behavior**: no error string contains `LEAK-7f3a`.
- **Test name**: `TestSourceIdentifiersNeverSurviveDecoding` (release test, T04)
  **Scenario**: decode the surviving leakage fixture (marker in `declarations`, `matched_by`, seam `hypothesis`) and marshal the result.
  **Expected behavior**: decodes; `LEAK-7f3a` is absent from the `Report`, from its JSON and from its formatted dump.

### Behavior Tests

- **Test name**: `TestFixturesCoverTheGateMapping`
  **Scenario**: group report fixtures by `gate.conclusion` and `gate.reasons[0]`.
  **Expected behavior**: at least one fixture each for `success` healthy, `success` with attention, `failure` blocking findings,
  `action_required` missing coverage, `action_required` baseline not comparable, `action_required` unknown identity or missing
  profile, `action_required` report invalid. At least one fixture has two or more ordered reasons. The fixtures for
  unknown identity and invalid input declare their `facts` override. No fixture expects `neutral` or `skipped`.
- **Test name**: `TestUploadPairIsConsistent`
  **Scenario**: for each report fixture that ships an envelope, compute `envelope.PayloadDigest` of the report bytes and look up the envelope identity in the manifest.
  **Expected behavior**: the digest equals the envelope's `payload_digest`; fixtures marked approved hit `manifest.Lookup`; a fixture with an unlisted image digest misses.
- **Test name**: `TestPolicyDigestBindsReportToPolicy`
  **Scenario**: for each report fixture with a sibling policy file, apply `policy.Digest`.
  **Expected behavior**: equals the report's bare-hex `config_hash`; after a one-byte policy edit it no longer does.
- **Test name**: `TestInputDigestsBindUploadToProtectedFiles`
  **Scenario**: for each report fixture with an envelope and sibling baseline or labels files, apply `policy.Digest` to the files.
  **Expected behavior**: equals the envelope's `baseline_digest` and `labels_digest`; a fixture without the file has `""`; no value carries a `sha256:` prefix.

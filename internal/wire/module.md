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
  `f8877d25ba36d84d3780071580d23486e3d794d0`; `archfit-action` at `4759dd985bff385af5c2dee2c918a0eced59f4f3`
  (plan inventory of 20 September 2026).
- Each child's internals (decoder technique, caps, file formats) stay in that child.

## Public Contract

`wire` publishes its children as importable Go packages. Go cannot re-export a package without alias boilerplate,
and these children hold no hidden internals, so consumers import and cite the child directly. The contract of each
child is normative in the child's own `module.md`; it is not restated here.

### Published packages

| Package                  | Owns                                                                                         |
| ------------------------ | -------------------------------------------------------------------------------------------- |
| `internal/wire/envelope` | report-envelope v1: `Envelope`, `Kind`, `Decode`, `PayloadDigest`, `Schema`                  |
| `internal/wire/report`   | engine state v1 as no-source transport: `Report` and its parts, `Decode`, size cap           |
| `internal/wire/settings` | `.archfit-app.yaml`: `Settings`, modes, `Parse`, `Render`, file location                     |
| `internal/wire/policy`   | engine file locations, bare-hex `Digest`, `Waivers` and `Modules` readers, config `Schema`   |
| `internal/wire/manifest` | locked approved identities: `Identity`, `Entry`, `Lookup`, `Current`                         |

### Versioning rule

- Every contract carries a version identifier: envelope `archfit.report-envelope.v1`; report
  `archfit.architecture-state.v1`; settings file `version: 1`; manifest data `version: 1`; policy follows the pinned
  engine release v2.3.1.
- Unknown fields are rejected by every decoder here. Two deliberate nuances: the policy readers ignore
  engine-owned keys they do not read; the report decoder knows every key of the engine's v2.3.1 state schema and
  drops the known keys that are not allowlisted, while a key the schema does not define is still an error.
- Two digest formats exist and are never compared with each other: engine-style bare lowercase hex (`policy.Digest`,
  the report's `config_hash`, the envelope's `baseline_digest` and `labels_digest`) and the prefixed `sha256:<hex>`
  form of envelope-native values (`payload_digest`, `image_digest`) **[default]**.
- A wrong or unknown version identifier is a decode error, never a best-effort parse.
- A breaking change means a new version constant and a new package-level decode path next to the old one. The old
  path stays until no approved producer emits the old version. Breaking: removing or renaming a field, changing a
  field's meaning or format, tightening a cap, making an optional field required.
- Because unknown fields are rejected, a producer may send a new optional field only after an App release that
  allows it. App first, then Action or engine **[default]**. Adding an optional allowlisted field this way keeps the version.
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

Files in `schema/` are reference data, not fixtures, and have no expectation sibling.

Every fixture `<name>.<ext>` has a sibling `<name>.expect.json` with these keys and no others:

| Key        | Meaning                                                                                                |
| ---------- | ------------------------------------------------------------------------------------------------------ |
| `producer` | object: `repository`, `revision` (40 hex), `engine_version`, `command`; or `"synthetic"` with a reason |
| `decode`   | `"ok"` or `"error"`                                                                                    |
| `fields`   | for `ok`: the decision and identity values the child's contract test asserts                           |
| `gate`     | report fixtures only: object with `conclusion` and `reasons`, the ordered list of reason codes         |
| `facts`    | optional, report fixtures only: object of overrides to the clean facts; absent means all clean         |

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
`"facts": {"identity_approved": false}` and `gate.reasons` `["measurement_profile_missing"]`. `invalid-input` has
`decode: "error"`, `"facts": {"problems": ["report_invalid"]}` and `gate.reasons` `["report_invalid"]`. A baseline
tampering fixture overrides `baseline_digest` with a value outside `trusted_baseline_digests`. Any other key in
`facts` fails the corpus test.

Fixtures captured from the engine use revision `f8877d25…`; fixtures from a mutable branch are not allowed.
Required report fixtures (plan T02): `healthy`, `attention-pass` (verdict `needs_attention` with passing hard
gates), `blocked`, `missing-evidence` (`hard_gates: unmeasured`), `unknown-identity`, `profile-comparable` (symmetric
partial with a recognized `partial_basis`), `profile-incompatible` (`gate_reference.status: non_comparable`),
`invalid-input`, `blocker-and-unevaluated`, `non-applicable-rule`, `locations-empty`, `matched-by-populated`
(decodes; `matched_by` is dropped and never in any output), `repair-kinds`, `waiver-expiry-order`, and
`real-blocked-no-baseline`. The last one is captured unmodified from the real engine v2.3.1 on a two-module Go
repository: verdict `blocked`, `hard_gates: fail`, no baseline file, so `gate_reference` is `non_comparable` with
reason `no baseline file was loaded`; its policy file ships beside it and its facts have empty trusted baseline and
labels lists. Its `gate` expectation is recorded from the gate verdict rules when the fixture is added.

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
| `archfit` engine (v2.3.1)         | report, policy, manifest follow engine releases | Contract (versioned schema) | outside the tree; highest | Moderate   | Yes       |
| `archfit-action` (external repo)  | the Action conforms to envelope and report      | Contract (JSON Schema)      | outside the tree; highest | Moderate   | Yes       |
| customer repositories (the files) | customers write settings and policy files       | Contract (versioned file)   | outside the tree; highest | Moderate   | Yes       |

Shared knowledge: with the engine, the complete v2.3.1 state schema key set, its enums and decision semantics, the
config schema, waiver keys, the `modules` map shape, the three input file names, the bare-hex hashing rule and
released identities, all pinned at revision `f8877d25ba36d84d3780071580d23486e3d794d0` and verified against a real
report; with the Action, the envelope schema, both digest rules and the no-source transport; with customers, the settings field rules. Separate repositories and release cycles are the highest
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
  **Expected behavior**: engine-captured fixtures cite revision `f8877d25ba36d84d3780071580d23486e3d794d0`; synthetic ones give a reason; none cites a branch name.
- **Test name**: `TestLeakageFixturesCarryTheMarker`
  **Scenario**: read every file in `testdata/leakage/`.
  **Expected behavior**: each contains `LEAK-7f3a`; all have `decode: "error"` except the one surviving declarations fixture, which has `decode: "ok"`.
- **Test name**: `TestPinnedEngineSchemaIsRecorded`
  **Scenario**: read `testdata/schema/`.
  **Expected behavior**: `archfit.state.schema.json` is present with a source note citing revision `f8877d25ba36d84d3780071580d23486e3d794d0` and its SHA-256; the hash matches.

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
  **Scenario**: an envelope with `archfit.report-envelope.v2`, a report with `archfit.architecture-state.v2`, a settings file with `version: 2`.
  **Expected behavior**: all three are errors.

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

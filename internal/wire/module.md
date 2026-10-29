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
- Current engine schemas are pinned to v3.4.0 at `2692cd6b8aab23e559d0aa76712a192d69b4229f` (state sha256 `1d37f7d8…`, config sha256 `edffa7db…`).
  The state schema adds to v3.0.0/v3.0.1 the keys v3.3.0 and v3.4.0 emit and requires `identity`; the config schema adds `target_branch` and `module_review.change_gate`.
  New workflows pin Action v3.0.0 at commit `fb2520cb069571d6bdc9274ac97d4df49994b6c7`, the only Action in the manifest. The App still owns the unchanged envelope schema.
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
  engine release of the manifest's `current` row, v3.4.0 today; the engine baseline file `archfit.baseline.v3`.
- Unknown fields are rejected by every decoder here. Three deliberate nuances: the policy readers ignore
  engine-owned keys they do not read; the report decoder knows every key of the pinned engine v3.4.0 state schema and
  drops the known keys that are not allowlisted, while a key that schema does not define is still an error;
  the baseline reader (`policy.ReadBaseline`) ignores baseline keys it does not read, as the engine's own decoder does.
- The envelope `kind` enum gained `baseline` inside v1: a new accepted value is additive, so the version stays.
- The manifest approves v3.4.0, with one Action SHA and two exact platform digests. Only v3.4.0 amd64 is current.
  The decoder also reads historical releases (v3.0.0 to v3.2.3) without an identity. Readability is not approval.
  Engine/Action releases provide actual identities and captures before the App replaces its approval rows.
  After App deployment, owners update installed workflows by pull request. No automatic pin, baseline, or debt change occurs.
- Two digest formats exist and are never compared with each other: engine-style bare lowercase hex (`policy.Digest`,
  the report's `config_hash`, the envelope's `baseline_digest` and `labels_digest`) and the prefixed `sha256:<hex>`
  form of envelope-native values (`payload_digest`, `image_digest`) **[default]**.
- A wrong or unknown version identifier is a decode error, never a best-effort parse.
- A breaking change means a new version constant and a new package-level decode path next to the old one. The old
  path stays until no approved producer emits the old version. Breaking: removing or renaming a field, changing a
  field's meaning or format, tightening a cap, making an optional field required.
- Because unknown fields are rejected, a producer may send a new optional field only after an App release that
  allows it. App first, then Action or engine **[default]**. Adding an optional allowlisted field this way keeps the
  version.
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

Fixtures captured from the engine cite their engine release and that release's pinned revision (v3.0.0:
`bbd658f0…`; v3.0.1: `0649e504…`; v3.2.0: `faa4eb4e…`; v3.2.2: `90c0ca24…`); fixtures from a mutable branch are not allowed. A capture is a document of its own
release; every synthetic fixture uses the current v3.4.0 key set, identity included. The tests, and every consumer test that reads the whole
corpus, decode each fixture as its release; the known releases share one key set.
Required report fixtures (plan T02): `healthy`, `attention-pass` (verdict `needs_attention` with passing hard
gates), `blocked`, `missing-evidence` (`hard_gates: unmeasured`), `unknown-identity`, `profile-comparable` (symmetric
partial with a recognized `partial_basis`), `profile-incompatible` (`gate_reference.status: non_comparable`),
`invalid-input`, `blocker-and-unevaluated`, `non-applicable-rule`, `locations-empty`, `matched-by-populated`
(decodes; `matched_by` keeps only `metric`, `before`, `after`, `threshold` and `locations_total`, and no other key is in any output), `repair-kinds`, `waiver-expiry-order`, and
`real-blocked-no-baseline`. The last one is captured unmodified from the real engine v3.0.0 on a two-module Go
repository: verdict `blocked`, `hard_gates: fail`, no baseline file, so `gate_reference` is `non_comparable` with
reason `no baseline file was loaded`; its policy file ships beside it and its facts have empty trusted baseline and
labels lists. Its old tuple is now unapproved. The new `real-v320-blocked-no-baseline` capture exercises the current blocking decision.

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
ast-grep. Three byte copies of captures ship a synthetic envelope: `unlisted-image` (image digest outside the
manifest), `retired-engine` (the former v3.0.0 `linux/amd64` row: the report decodes, the identity is not approved,
`action_required` with `["unknown_engine_identity", "blocking_findings"]`) and `baseline-tampered`. Historical envelopes keep their original identities and now expect unapproved tuples. New `real-v320-*` pairs
carry the actual released v3.2.0 identity. The synthetic tampering case uses the current tuple to keep testing the digest mismatch.

Two engine v3.0.1 captures, unmodified, with the released binary `archfit-v3.0.1-darwin-arm64` checked against the
release's `SHA256SUMS`, on a development host: `real-pr-origin` is the run the Action makes on a pull request,
`archfit check --json -c .archfit.yaml -q --refresh --base <merge base>` on two commits of `testdata/fixture-go` at
tag v3.0.1 plus a module `c` and a fail-gated rule against it; the head adds the import. Its comparison is
`comparable` against the merge base with `origin_status: comparable`, two findings `introduced` and two
`pre_existing`, and `config_hash` and the measurement profile stay the head's. Its expectation is the
`real-blocked-no-baseline` one. `real-recaptured-baseline` is the `real-baselined-comparable` recipe on v3.0.1: the
same accepted findings and policy, a baseline without `state.dimensions` and `state.hard_gate_finding_ids`, and the
same check report up to the Go toolchain version.

The current `real-v322-blocked-no-baseline` report/envelope pair was emitted by the released native arm64 image
through the qualified Action commit. The unmodified baseline/reanchor outputs and their provenance are in
[`testdata/release-v322/SOURCE.md`](testdata/release-v322/SOURCE.md). Tests bind all five capture hashes, preserve N/A
encapsulation omission and measured cycle zero, and structurally read both baseline outputs. No authenticated upload
or execution of the engine's reanchor algorithm is claimed by these App tests.

`real-v340-arm64-blocked-no-baseline` is the unmodified `tests/fixtures/state.json` of archfit-action at `fb2520cb069571d6bdc9274ac97d4df49994b6c7`
(sha256 `958a3de5…`), emitted by the released native arm64 engine v3.4.0 image. Only the report is real: the Action's
CI export of its own envelope is not committed, so the sibling envelope is built here around it (synthetic GitHub fields,
the manifest's arm64 digest, the payload digest of the report). It is the capture the manifest's v3.4.0 rows need; the
v3.2.3 captures stay as history and no longer carry an approved identity. The synthetic report fixtures carry the
`identity` block v3.4.0 states, because Decode requires it from that release; `coverage-details` and
`coverage-details-malformed` hold a `coverage.tools[].details` list that is accepted and one that is `report_invalid`.

Four v3.2.0 captures remain transition evidence: `real-v320-blocked-no-baseline`, `real-v320-baselined-comparable`,
`real-v320-pr-origin`, and `real-v320-missing-compiler`. Reports and captured baseline/envelope bytes are unmodified.
The first three use the released native arm64 image through released Action stages on controlled scratch repositories.
Their event identity is synthetic test input, not authenticated remote proof. The missing-compiler report comes from
publisher run40172977752's real negative contract. Its required evidence remains unavailable despite an approved image.
Historical report bytes and producer revisions are retained. Only their retired upload identities lose approval.

Two hand-edited fixtures cover what real engine output reached the old App limits with: `producer-versions` (the
remaining tool-version formats: grimp failing with `uv`, the macOS system git, two coverage parsers) and
`large-module` (150 locations on one finding, 152 task files and a 906-character public-surface constraint).
The captures, like the earlier ones, come from a development host, not from the pinned image.

`real-blocked-no-baseline` is the v3.0.0 capture the manifest's v3.0.0 rows needed: `archfit check --json
-c .archfit.yaml -q --refresh` on a scratch copy of the engine's `testdata/fixture-go` at tag v3.0.0, run with the
released binary `archfit-v3.0.0-darwin-arm64`, checked against the release's `SHA256SUMS`, on a development host, not
the pinned image. It is unmodified. Its expectation: blocked, `hard_gates: fail`, `non_comparable` gate reference,
`failure` with `["blocking_findings"]`.

`real-seam-policy` is the v3.0.0 capture that carries four of the five `seams[].policy` values (no `advisory`:
the run has no advisory finding): the `hexagonalRepo` scenario of the engine's `cmd/archfit/map_test.go` at tag v3.0.0, rebuilt file
for file (five Go packages, the test's policy with layers, a fail-gated `forbidden_layer_direction` and a fail-gated
`forbidden_dependency` rule). `archfit baseline` runs first; then `billing` imports `stripe`, and
`archfit check --json -c .archfit.yaml -q --refresh` runs with the same released binary. It is unmodified, and its
seam statuses equal the engine's `map` golden file for the scenario: `api -> billing` accepted, `api -> stripe`,
`orders -> billing` and `stripe -> orders` allowed, `billing -> stripe` violation, `orders -> audit` observed. Its
policy and the baseline the engine loaded ship beside it. Its expectation: blocked, `hard_gates: fail`, the rules
`api-not-into-billing`, `layers-point-inward` and `metric_ratchets` unevaluated, `non_comparable` gate reference,
`action_required` with `["baseline_not_comparable", "blocking_findings", "missing_required_coverage"]`.

`real-root-importer` is the v3.0.0 capture of a rule with `rationale`, `alternatives` and `docs`, built on a scratch Go module:
`billing/api/api.go`, `shop/shop.go`, and a root `main.go` that no module owns and imports `billing/api`. Its
policy (beside it) gives `billing` `visible_to: [shop]` and has one fail-gated `module_dependencies` rule with
`rationale`, `alternatives` and `docs`. `archfit check --json -c .archfit.yaml -q --refresh` runs with the same
released binary and no baseline. It is unmodified. The engine names the unowned importer by its package directory,
`edge.from.path: "."`, and lists `.` in the task's files; the why and the constraint end with the rationale and the
docs reference, and the task has a `rationale: ` constraint. Its expectation is the `real-blocked-no-baseline` one:
blocked, `hard_gates: fail`, `non_comparable` gate reference, `failure` with `["blocking_findings"]`.

The `leakage/` set has two groups. Rejecting fixtures (`decode: "error"`): snippet field, log blob, raw model output
key, multi-line text, oversized text. One surviving fixture (`decode: "ok"`): a marker identifier inside
`agent_tasks[].declarations`, `matched_by` and a seam `role_expectation`; it must decode, and the marker must be gone.

## Integrations

`wire` has no code, so it has no code-level integration of its own. Its children import only the standard library,
and `settings` and `policy` one YAML library. Internal consumers integrate with a child, not with this parent;
each child document lists them with LCA, rank and distance. All of them are contract-strength across the root
(rank 2, distance 2; rank 3, distance 3 for `control/ui/graph`), balanced at any volatility.

External counterparts, reached only through these contracts:

| Counterpart                       | Direction                                       | Strength                    | LCA / Rank / Distance     | Volatility | Balanced? |
| --------------------------------- | ----------------------------------------------- | --------------------------- | ------------------------- | ---------- | --------- |
| `archfit` engine (v3.4.0)         | report, policy, manifest follow engine releases | Contract (versioned schema) | outside the tree; highest | Moderate   | Yes       |
| `archfit-action` (external repo)  | the Action conforms to envelope and report      | Contract (JSON Schema)      | outside the tree; highest | Moderate   | Yes       |
| customer repositories (the files) | customers write settings and policy files       | Contract (versioned file)   | outside the tree; highest | Moderate   | Yes       |

Shared knowledge: with the engine, the complete state schema key set, its enums and decision semantics, the config
schema, waiver keys, the `modules` map shape, the three input file names, the bare-hex hashing rule and released
identities, with current schemas pinned at revision `2692cd6b8aab23e559d0aa76712a192d69b4229f` (v3.4.0) and verified against real reports; with the Action, the envelope schema, both digest rules and the no-source transport; with customers, the settings field rules. Separate repositories and release cycles are the highest
distance the system has. Contract coupling with explicit versions, strict decoding and a shared fixture corpus is
what keeps it balanced.

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
  **Expected behavior**: engine-captured fixtures cite a pinned release at its pinned revision (`v3.0.0` at `bbd658f008bcc4bc83e063cf305d20dc4508da2e`, `v3.0.1` at `0649e504a6761f565086b342d315110feeb27d77`, `v3.2.0` at `faa4eb4e513859567fd6c380230c91ea22fdaab6`); synthetic ones give a reason; none cites a branch name.
- **Test name**: `TestLeakageFixturesCarryTheMarker`
  **Scenario**: read every file in `testdata/leakage/`.
  **Expected behavior**: each contains `LEAK-7f3a`; all have `decode: "error"` except the one surviving declarations fixture, which has `decode: "ok"`.
- **Test name**: `TestPinnedEngineSchemaIsRecorded`
  **Scenario**: read `testdata/schema/`.
  **Expected behavior**: `archfit.state.schema.json` is present with a source note citing tag `v3.4.0`, revision `2692cd6b8aab23e559d0aa76712a192d69b4229f` and its SHA-256; the hash matches.

### Integration Contract Tests

- **Test name**: `TestEveryFixtureDecodesAsRecorded`
  **Scenario**: route each fixture to its child decoder by folder (`envelope.Decode`, `report.Decode`, `policy.Waivers` and `policy.Modules`).
  **Expected behavior**: `ok` fixtures decode and match `fields`; `error` fixtures fail.
- **Test name**: `TestRealEngineCaptureDecodes`
  **Scenario**: `report/real-blocked-no-baseline` with its policy file, as captured from engine v3.0.0.
  **Expected behavior**: `report.Decode` succeeds on the unmodified bytes; verdict `blocked`; `gate_reference.status` `non_comparable`;
  `policy.Digest` of the policy equals `config_hash`; `policy.Modules` lists `a` and `b`, matching the seam's modules.
- **Test name**: `TestRealPullRequestCaptureCarriesOrigin`
  **Scenario**: `report/real-pr-origin` with its policy file, as captured from engine v3.0.1 with `--base`.
  **Expected behavior**: `comparison.status` and `origin_status` are `comparable` against the merge base; `config_hash`
  is the policy's digest and the profile is `Current`'s; `no_c_dependency` and one `bc/imbalanced_coupling` finding
  are `introduced`, `no_direct_b_dependency` and the other are `pre_existing`; each task carries its finding's origin.
- **Test name**: `TestPublishedPackagesExist`
  **Scenario**: `go list ./internal/wire/...`.
  **Expected behavior**: exactly the parent and the five published packages; the parent exports no identifiers.
- **Test name**: `TestUnknownVersionRejectedEverywhere`
  **Scenario**: an envelope with `archfit.report-envelope.v2`, a report with `archfit.architecture-state.v2` as a document of each readable release,
  the real capture as a document of engine `v2.6.0`, a settings file with `version: 2`.
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
  **Scenario**: decode the surviving leakage fixture (marker in `declarations`, `matched_by`, seam `role_expectation`) and marshal the result.
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

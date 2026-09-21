# Engine Policy Files

**Path**: internal/wire/policy/
**Parent**: internal/wire/ (Wire Contracts)
**Submodules**: none (leaf)

## Purpose

The engine policy `.archfit.yaml`, the baseline `.archfit-baseline.json` and the labels file
`.archfit-labels.yaml` belong to the Archfit engine, not to the App. The App needs six small things from them:
where they live, the digest rule that binds a report to them, the waiver expiry dates to schedule around, the
declared modules for the graph view, the engine's config JSON Schema for the editor, and a structural check that
a text is plausibly a policy before the App proposes or saves it. This module owns exactly
that and deliberately nothing more. Without it, the digest rule would be re-derived in several places, and any
drift between them would turn honest reports into `policy_mismatch`.

## Functional Responsibilities

- Name the three engine files on the protected ref: `PolicyPath`, `BaselinePath`, `LabelsPath`.
- `Digest`: bare lowercase hex SHA-256 of raw file bytes. For the policy it is the value the engine reports as
  `comparison.config_hash`. For baseline and labels it is the value the producer states in the upload envelope.
- `Waivers`: read the `waivers:` list of a policy file into `Waiver` values with parsed expiry instants.
- `Modules`: list the names, layers and line numbers of the policy's `modules:` map, in file order.
- `Schema`: the engine v2.3.1 config JSON Schema (`archfit.schema.json`), embedded unchanged.
- `Validate`: a structural check that a text is plausibly an engine policy: one YAML mapping whose top-level keys
  all belong to the schema's closed key set and include the required `version`. It keeps foreign text (a report,
  a labels file, model prose) out of policy pull requests and saved drafts.
- Nothing else. It does no engine validation of policies (no value, type or rule check), does not match waivers to
  findings, parse baselines or labels, or enforce expiry.

## Subdomain Classification

Published language owned by the `archfit` repository; this module is the App's pinned, read-only view of it.
Supporting. Volatility: moderate, tied to engine releases (pinned v2.3.1).

## Encapsulated Knowledge

- The digest algorithm and its byte-exactness: SHA-256 over the file bytes as stored in git, bare lowercase hex,
  no prefix. No YAML or JSON canonicalization, no newline or BOM handling. Verified against engine v2.3.1: the
  engine's `comparison.config_hash` is exactly this value for the policy file. The envelope's `payload_digest`
  is a different format (`sha256:` prefix) owned by `wire/envelope`; the two are never compared with each other.
- What the engine does not hash. It reports no digest for the baseline or the labels file; `comparison.labels_hash`
  is a canonical hash of approved labels and cannot be reproduced from raw bytes. For those two files the binding
  is: producer states `Digest` of the bytes it used, the App compares with `Digest` of the protected-ref bytes.
- The waiver keys, pinned to the v2.3.1 config schema (`WaiverDef`): `rule`, `from`, `to`, `reason`, `approved_by`,
  `expires`; the engine requires all but `from` and `to`.
- The expiry rule: `expires` is a `YYYY-MM-DD` date, valid through the end of that day; the instant is 23:59:59
  UTC of that day **[default: UTC]**. The engine treats a malformed `expires` as already expired.
- The modules shape, pinned to the same schema: `modules:` is a map from module name to a definition with `paths`,
  `public`, `internal`, `layer` and other keys. Only the name, `layer` and the key's line are read.
- The embedded schema file and its provenance: `archfit.schema.json` copied unchanged from the engine repository
  at revision `f8877d25ba36d84d3780071580d23486e3d794d0` (tag `v2.3.1`). A sibling file `SCHEMA_SOURCE` records the
  source revision, the source path and the SHA-256 of the copy **[default]**.
- The top-level key set of a policy. It is derived from the embedded schema's root `properties` and `required`,
  read once lazily with `encoding/json`; there is never a second hand-written list. The derivation relies on the
  root's `additionalProperties: false`; a schema without it has no closed key set and `Validate` then always fails.
  In v2.3.1 the root has sixteen properties and requires only `version`; the schema file is the one source.
- The YAML library: the same single third-party dependency as `wire/settings`
  (`go.yaml.in/yaml/v3` **[default]** until `docs/tech-stack.md` names one). `Modules` uses its node API for line numbers.
- The baseline and labels files are opaque here. Only their paths and digests are known.

### Common reading rules

`Waivers`, `Modules` and `Validate` share one reader and these rules:

- Input limit 1 048 576 bytes **[default]**; larger input is an error. Invalid UTF-8 or YAML that does not parse is an error.
- Exactly one YAML document whose root is a mapping; otherwise an error. Empty input gives `nil, nil` from
  `Waivers` and `Modules`, and an error from `Validate`.
- Duplicate mapping keys, alias cycles and alias-expansion bombs are errors (the library's full decode runs once).
- `Waivers` and `Modules` read only their one top-level key. Every other key, known to the engine or not, is
  ignored. This is deliberately not strict: the engine validates its own file, and a newer engine key must not
  break these two readers. `Validate` is the strict one; see its rules below.
- Any error returns a `nil` slice. There is no partial result.

### Waivers reading rules

- `waivers` absent, `null`, or an empty list: result is `nil, nil`.
- `waivers` present but not a list of mappings: error.
- Per entry: `expires` absent, empty, or not a real calendar date in exact `YYYY-MM-DD` form is an error for the
  whole call. Quoted and unquoted YAML dates are both accepted. The other five keys are copied as strings and may be
  empty; unknown keys inside an entry are ignored **[default]**.
- Order of the result equals file order. No de-duplication, no filtering of expired entries.
- A malformed `expires` is an error here although the engine accepts the file and treats the waiver as expired.
  Nothing is lost: the engine already reports the finding as `expired_waiver`, so there is no expiry to schedule.

### Modules reading rules

- `modules` absent, `null`, or an empty map: result is `nil, nil`. `modules` present but not a mapping: error.
- One `ModuleDef` per map key, in file order. `Name` is the key text. `Line` is the 1-based line of the key in
  the input, counting LF line breaks. `Layer` is the entry's `layer` scalar, or `""` when absent.
- An entry whose value is not a mapping, or whose `layer` is not a scalar, is an error. All other keys of an entry
  are ignored. Names are at most 200 characters, at most 5 000 modules **[default]**; more is an error.
- A duplicate module name is an error (the YAML library already rejects duplicate mapping keys).
- Aliases and merge keys under `modules` are resolved by the library; `Line` is then the line of the key, not of the anchor.
  Keys merged in with `<<` directly under `modules` follow the map's own keys, and the map's own keys win.
- Input with a line break other than LF or CRLF (a lone CR, NEL, LS, PS) is an error when `modules` is a mapping:
  the library counts those as lines, an LF split does not, so `Line` would be wrong **[default]**.

### Validate rules

- The common reading rules apply first, with the same reader and the same errors.
- An empty document (no bytes, white space only, comments only) is an error: `version` is required.
- Every top-level key must be a name in the schema's root `properties`. The match is exact and case-sensitive.
  A key that is not a plain string (a number, a null, a merge key `<<`) is not in the set and is an error, so keys
  merged into the root are never considered.
- Every name in the schema's root `required` must be present as a key. Presence is enough: `version:` with a null
  value passes. No value is inspected, at any depth; a policy with a malformed waiver `expires` passes.
- The first failure wins. An unknown key is reported by its 1-based position among the top-level keys, a missing
  required key by its schema name. No key or value of the input appears in any error.
- A policy written for a newer engine with a new top-level key is rejected until the embedded schema is replaced.
  That is intended: the pinned engine would reject the file as well.

## Public Contract

```go
// The engine's three trusted input files on the protected ref. Baseline and labels sit beside the policy.
const (
    PolicyPath   = ".archfit.yaml"
    BaselinePath = ".archfit-baseline.json"
    LabelsPath   = ".archfit-labels.yaml"
)

// Digest returns the bare lowercase hex SHA-256 of the raw file bytes. For the policy file it equals the
// engine's comparison.config_hash. The engine reports no digest for baseline or labels; for those the
// producer states Digest of the bytes it used in the envelope, and the App compares it with the protected ref.
func Digest(raw []byte) string
```

```go
// Waiver is the subset of an engine waiver the App reads. The engine enforces it; the App only schedules around Expires.
type Waiver struct {
    Rule       string    // engine rule ID
    From, To   string    // endpoint selectors, may be ""
    Reason     string
    ApprovedBy string    // descriptive only, never authentication
    Expires    time.Time // end of the YYYY-MM-DD day, UTC
}

// Waivers extracts the waivers of a policy file. It ignores every other key.
func Waivers(policy []byte) ([]Waiver, error)
```

```go
// Schema returns the engine v2.3.1 config JSON Schema, embedded unchanged, for the editor.
func Schema() []byte
```

```go
// ModuleDef is the subset of a policy module the App displays.
type ModuleDef struct {
    Name  string
    Layer string // may be ""
    Line  int    // 1-based line of the module key in the policy text
}

// Modules lists the modules declared under the policy's `modules:` map, in file order. It ignores every other key.
func Modules(policy []byte) ([]ModuleDef, error)
```

`Validate` is the structural gate for text that is about to be proposed or saved as a policy:

```go
// Validate reports whether policy is plausibly an engine policy file: a YAML document whose root is a
// mapping, whose top-level keys all belong to the engine config schema's closed key set, and which has
// the required `version` key. It is a structural check, not engine validation: the engine remains the
// authority on the file's meaning. Errors never echo keys or values.
func Validate(policy []byte) error
```

`PolicyPath` is the location of the engine policy on the protected ref. `BaselinePath` is the location of the
persisted engine baseline and `LabelsPath` is the location of the approved labels file, both beside the policy.
`ApprovedBy` is descriptive text copied from the file.

Semantics not visible in the signatures:

- `Digest` of empty input is the digest of empty input, not an error. Output always matches `^[0-9a-f]{64}$`:
  bare hex, never a `sha256:` prefix. An absent file has no digest; callers represent "no file" as `""`, never as `Digest(nil)`.
- `Modules` follows the reading rules above. `Line` refers to the exact bytes passed in; a caller that shows lines
  must pass the same text it displays.
- `Waivers` follows the reading rules above. A caller that gets an error proceeds with no waiver schedule (a zero
  earliest expiry) and logs the outcome only; the engine still enforces the waivers.
- `Validate` follows the Validate rules above. It is the check control-plane callers run before they propose or
  save policy text: `nil` means one well-formed YAML mapping within the size limit, with only engine config keys
  and with `version`. It is not engine validation; the check run on the draft PR is. `Validate` does not read
  waivers: a caller that also needs readable waivers calls `Waivers` as well.
- `Waivers` alone is not a policy check: it ignores unknown keys, so prose or JSON with a colon passes it.
- `Schema` returns a fresh copy on every call. The bytes are the engine's file, not reformatted.

## Integrations

This module imports the standard library and one YAML library. It depends on no internal module. The entries
below are its consumers (the consumer depends on this module) and its external counterpart. Values are from the
root coupling table.

| Counterpart                      | Direction                       | Strength | LCA / Rank / Distance     | Volatility | Balanced? |
| -------------------------------- | ------------------------------- | -------- | ------------------------- | ---------- | --------- |
| `internal/gate/inputs`           | inputs depends on policy        | Contract | root / 2 / 2              | High       | Yes       |
| `internal/gate`                  | gate depends on policy          | Contract | root / 2 / 2              | High       | Yes       |
| `internal/control/onboarding`    | onboarding depends on policy    | Contract | root / 2 / 2              | High       | Yes       |
| `internal/control/assist`        | assist depends on policy        | Contract | root / 2 / 2              | High       | Yes       |
| `internal/control/ui`            | ui depends on policy            | Contract | root / 2 / 2              | High       | Yes       |
| `internal/control/ui/graph`      | graph depends on policy         | Contract | root / 3 / 3              | Moderate   | Yes       |
| `archfit` engine config (v2.3.1) | policy follows the engine files | Contract | outside the tree; highest | Moderate   | Yes       |

The `control/ui/graph` row exists because the graph's stale flag compares `Digest` of the policy text with the
report's `config_hash`, and its nodes come from `Modules`, so that both rules live in one place.

Shared knowledge, per counterpart:

- `internal/gate/inputs` holds `PolicyPath`, `BaselinePath`, `LabelsPath`, `Digest`, `Waiver` and `Waivers`. It reads the
  three files at the protected ref, digests them, digests an owner-approved head candidate, and takes the earliest
  `Expires`. The gate compares the policy digest with the report's `config_hash`, and the baseline and labels
  digests with the envelope's `baseline_digest` and `labels_digest`.
- `internal/gate` holds `PolicyPath`, `BaselinePath` and `LabelsPath` only: the file locations it watches in events.
  The digests and the earliest expiry reach it through `inputs.Trusted`, without a policy type.
- `internal/control/onboarding` holds `PolicyPath` to place the proposed policy of PR 2 and to derive the `policy_merged`
  stage, `BaselinePath` and `LabelsPath`, `Waivers`, and `Validate`, which it calls on an uploaded discovery draft: an
  error means no pull request is opened.
- `internal/control/assist` holds `PolicyPath`, `Schema` (rule descriptions for the prompt context) and `Validate`,
  which it calls on a provider answer: an error means the proposal is not a policy.
- `internal/control/ui` holds `PolicyPath`, `Schema` for the editor's completion and in-browser validation,
  `Validate` as the server-side check before an editor draft is saved, and `Modules` and `Waivers` for its pages.
- `internal/control/ui/graph` holds `Digest`, `ModuleDef` and `Modules`: nodes, layers and the policy line to jump to.
- `archfit` engine config: this module holds the config schema file, the three file locations, the waiver keys,
  the shape of the `modules` map, and the hashing rule. The engine owns all of them; this module follows a pinned release. Contract coupling
  through a versioned schema and pinned revision tolerates the distance of a separate repository.

No counterpart contract is used by this module, so nothing is restated here.

## Change Vectors

- A new engine release: replace the embedded schema and `SCHEMA_SOURCE`; adjust waiver keys if the waiver contract changed.
- The engine changes how it computes `config_hash`, or starts reporting baseline and labels digests: change `Digest` only.
- Configurable file locations per repository: the constants become defaults; the settings file would carry overrides.
- The App starts reading another policy key or module attribute (for example `owner`): extend `ModuleDef` or add a reader.

## Constraints and Invariants

- The engine enforces waivers, including expiry, order-independent matching, and the unwaivable coupling gate.
  The App never re-implements that; it only schedules warnings and re-runs around `Expires`.
- `ApprovedBy` is descriptive metadata, never authentication (plan T16). Authority comes from the owner approval
  the gate verifies on the pull request that changed the policy.
- This module never decides where bytes come from. Consumers read the protected ref; head-tree policy, baseline
  and labels are never trusted, except the digest of an owner-approved candidate, which is the consumer's rule.
- The App never writes or commits a baseline; this module has no baseline writer and no policy writer.
- Digests are over raw bytes. No consumer may normalize a file before hashing.
- A baseline or labels digest stated by the producer is self-reported. It proves nothing alone; it only lets the
  gate refuse a run that says it used other bytes than the protected ref holds.
- No I/O, no clock (expiry comparison with "now" is the caller's), no logging. Errors name the entry index and
  key, never values or reasons text.
- `Validate` uses the same input limit, the same reader and the same duplicate-key, alias and line-break handling
  as `Waivers`. An empty or comment-only document is an error for `Validate` only, because `version` is required.
- `Validate` is structural. It never replaces the engine's validation and never inspects a value.
- `Waivers`, `Modules` and `Validate` are total: no input panics. Work is bounded by the input limit and the library's alias limits.
- No other internal import, including `wire/settings` and `wire/report`.

## Test Specification

Fixtures come from the shared corpus and are read by relative path: the policy fixtures in
`internal/wire/testdata/policy/` (`<name>.yaml` with its `.expect.json`), and in `internal/wire/testdata/report/` the
`.policy.yaml`, `.baseline.json`, `.labels.yaml` and `.envelope.json` siblings of the report fixtures. The parent
document defines names, suffixes and the expectation format.

One fixture is private, because the shared corpus has no equivalent: `testdata/policy/malformed-expiry.report.json`,
the synthetic engine report for the shared `policy/malformed-expiry.yaml`. `TestMalformedExpiryNeedsNoSchedule` reads
it and first checks that its `config_hash` is the digest of that policy.

### Unit Tests

- **Test name**: `TestDigestKnownVectors`
  **Scenario**: table: empty input; `"abc"`; a policy fixture; the same fixture with CRLF endings, with a BOM, with a trailing newline removed.
  **Expected behavior**: `e3b0c442…b855` and `ba7816bf…15ad` for the first two; all four fixture variants differ; output matches
  `^[0-9a-f]{64}$` and never starts with `sha256:`.
- **Test name**: `TestModulesShapes`
  **Scenario**: table: no `modules` key; `modules: null`; `modules: {}`; `modules: [a, b]`; an entry whose value is a string; `layer` as
  a list; two modules with and without `layer`; comments and blank lines between entries; CRLF line endings; 5 001 modules.
  **Expected behavior**: `nil, nil` for the first three; errors for list, string value, list layer and 5 001; otherwise file order, names
  as written, `Layer` or `""`, and `Line` equal to the 1-based line of each key.
- **Test name**: `TestWaiversExpiresParsing`
  **Scenario**: table of `expires` values: `2026-10-20`; `"2026-10-20"`; `2028-02-29`; `2027-02-29`; `2026-1-5`; `20261020`;
  `2026-10-20T00:00:00Z`; `next week`; empty; absent.
  **Expected behavior**: the first three give 23:59:59 UTC of that day; every other row is an error for the whole call.
- **Test name**: `TestWaiversShapes`
  **Scenario**: table: no `waivers` key; `waivers: null`; `waivers: []`; `waivers: "none"`; `waivers: [a, b]`; entry with only `expires`;
  entry with an unknown key `ticket`; three entries in non-chronological order; two identical entries.
  **Expected behavior**: `nil, nil` for the first three; errors for rows four and five; the rest succeed with file order kept,
  duplicates kept, missing strings empty, the unknown key ignored.

- **Test name**: `TestValidateKeySetComesFromSchema`
  **Scenario**: derive the key set from the embedded schema and from a copy with one more root property, also listed as required;
  schema bytes that are not JSON, have no root properties, or lack `additionalProperties: false`.
  **Expected behavior**: the embedded schema's root is closed and rejects a policy with the new key; the copy accepts it and
  names the new required key when it is absent; the broken schemas are errors.

### Integration Contract Tests

- **Test name**: `TestWaiversIgnoresTheRestOfThePolicy`
  **Scenario**: a full engine v2.3.1 policy fixture with modules, rules and two waivers, plus a copy with an extra unknown top-level key.
  **Expected behavior**: both return the same two waivers with all six fields filled.
- **Test name**: `TestValidateAcceptsRealPolicies`
  **Scenario**: every `internal/wire/testdata/policy/*.yaml` and every
  `internal/wire/testdata/report/*.policy.yaml`, including the real engine captures and the malformed-expiry policy that
  `Waivers` rejects; table: `version` only; null `version`; all sixteen keys; CRLF with comments; JSON spelling; an alias below a key.
  **Expected behavior**: `nil` error for all.
- **Test name**: `TestDigestMatchesEngineConfigHash`
  **Scenario**: for each shared report fixture with a `.policy.yaml` sibling, digest the sibling; include the real engine capture
  `real-blocked-no-baseline`.
  **Expected behavior**: equals the fixture report's `comparison.config_hash` (for the real capture `4a18013b…c55574`).
- **Test name**: `TestDigestMatchesEnvelopeInputDigests`
  **Scenario**: for each shared report fixture with an `.envelope.json` sibling: its `.baseline.json` and `.labels.yaml` siblings, when present.
  **Expected behavior**: `Digest` of each file equals the envelope's `baseline_digest` and `labels_digest`; a fixture without the file has `""`
  there. The exception is `baseline-tampered`, whose envelope must state a baseline digest different from its file's. At least one
  baseline and one labels file are checked.
- **Test name**: `TestModulesOnEnginePolicy`
  **Scenario**: the real capture's policy (modules `a` and `b`, no layers) and a full v2.3.1 policy fixture with layers, rules and waivers.
  **Expected behavior**: `a` at line 3 and `b` at line 6 for the capture; for the full fixture every module of the recorded expectation, in order.
- **Test name**: `TestSchemaIsThePinnedEngineFile`
  **Scenario**: hash `Schema()` and parse it.
  **Expected behavior**: the SHA-256 equals the one in `SCHEMA_SOURCE`; the revision there is `f8877d25…`; valid JSON with `waivers`
  and `modules` properties and a `WaiverDef` that requires `rule`, `reason`, `approved_by`, `expires`; two calls return equal but independent slices.
- **Test name**: `TestPathConstants`
  **Scenario**: read the constants.
  **Expected behavior**: `.archfit.yaml`, `.archfit-baseline.json` and `.archfit-labels.yaml`.

### Boundary Tests

- **Test name**: `TestReadersRejectBadInput`
  **Scenario**: table, run for both `Waivers` and `Modules`: invalid YAML; invalid UTF-8; two documents; root is a list; 1 048 577 bytes; an alias-expansion bomb; empty input.
  **Expected behavior**: errors for all but empty input, which gives `nil, nil`; bounded time and memory; no panic.
- **Test name**: `TestValidateRejectsForeignDocuments`
  **Scenario**: table: the engine state report `real-healthy.json`; a labels file; `Sure! Here is the file: done`; prose without
  a colon; a scalar root; a sequence root; a mapping without `version`; `{}`; one unknown top-level key; `Version`; an integer key,
  a null key and a merge key; empty, white-space-only and comment-only input; a duplicate top-level key; two documents;
  truncated YAML; invalid UTF-8; 1 048 577 bytes; an alias-expansion bomb.
  **Expected behavior**: an error for every row; unknown keys are named by position, the missing key as `version`; no panic.
- **Test name**: `TestValidateErrorsEchoNothing`
  **Scenario**: the marker `LEAK-7f3a` as an unknown key and its value, as a duplicate key, below a valid key of a document
  without `version`, as a scalar root, in prose, in broken YAML, as an anchor and alias name; the engine state report.
  **Expected behavior**: every error is free of the marker and of every top-level key of the report.
- **Test name**: `FuzzValidate`
  **Scenario**: fuzz `Validate` seeded with the fixtures and the prose line.
  **Expected behavior**: never panics; whatever `Validate` accepts, the common reader accepts.
- **Test name**: `TestErrorsNeverEchoValues`
  **Scenario**: a waiver whose `reason` and malformed `expires` contain the marker `LEAK-7f3a`.
  **Expected behavior**: the error names entry index and key; it does not contain the marker.
- **Test name**: `FuzzWaivers`
  **Scenario**: fuzz `Waivers` seeded with the fixtures.
  **Expected behavior**: never panics; every returned `Expires` has time 23:59:59 and location UTC.
- **Test name**: `FuzzModules`
  **Scenario**: fuzz `Modules` seeded with the fixtures.
  **Expected behavior**: never panics; every `Line` is between 1 and the number of input lines; names are unique.
- **Test name**: `TestPackageImports`
  **Scenario**: `go list -deps` for the package.
  **Expected behavior**: standard library plus the one YAML library; no internal package; no `net` or `log` package. `os` is
  reachable through `fmt` and the YAML library, so it is banned as a direct import only.

### Behavior Tests

- **Test name**: `TestEarliestExpiryIsSchedulable`
  **Scenario**: the waiver expiry/order fixture (T02): an expired and an active waiver for the same rule, listed expired-first and active-first.
  **Expected behavior**: both orders return both waivers; the minimum `Expires` is the expired one in both; nothing is filtered here.
- **Test name**: `TestWaiverIsActiveThroughItsLastDay`
  **Scenario**: `expires: 2026-10-20`; compare with 2026-10-20 12:00 UTC and 2026-10-21 00:00 UTC.
  **Expected behavior**: the first instant is before `Expires`, the second is after it.
- **Test name**: `TestPolicyEditChangesDigest`
  **Scenario**: a base policy and a head policy that differs by one waiver line or by whitespace only.
  **Expected behavior**: different digests in both cases; a whitespace-only edit is still a policy change needing approval.
- **Test name**: `TestWaiversAsSyntaxCheck`
  **Scenario**: table: a valid policy without waivers; prose text that is not a mapping; truncated YAML; a policy with an unclosed quote.
  `Waivers` still rejects these, although control-plane callers now use `Validate` as their check.
  **Expected behavior**: `nil` error for the first; errors for the rest.
- **Test name**: `TestApprovedByIsNeverAuthority`
  **Scenario**: a waiver with `approved_by` naming a real owner login.
  **Expected behavior**: the value is returned as plain text; the package exposes nothing that treats it as an approval.
- **Test name**: `TestGraphJumpsToTheModuleLine`
  **Scenario**: a policy shown in the editor; take `Modules`, split the same text by LF, read the line at `Line` for each module.
  **Expected behavior**: each line, trimmed, starts with the module name followed by a colon.
- **Test name**: `TestMalformedExpiryNeedsNoSchedule`
  **Scenario**: the fixture policy with `expires: 2026-13-40`, which the engine accepts and reports as `expired_waiver`.
  **Expected behavior**: `Waivers` returns an error; the fixture's report already shows the finding as expired, so no expiry is lost.

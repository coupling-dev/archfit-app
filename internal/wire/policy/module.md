# Engine Policy Files

**Path**: internal/wire/policy/
**Parent**: internal/wire/ (Wire Contracts)
**Submodules**: none (leaf)

## Purpose

The engine policy `.archfit.yaml`, the baseline `.archfit-baseline.json` and the labels file
`.archfit-labels.yaml` belong to the Archfit engine, not to the App. The App needs five small things from them:
where they live, the digest rule that binds a report to them, the waiver expiry dates to schedule around, the
declared modules for the graph view, and the engine's config JSON Schema for the editor. This module owns exactly
that and deliberately nothing more. Without it, the digest rule would be re-derived in several places, and any
drift between them would turn honest reports into `policy_mismatch`.

## Functional Responsibilities

- Name the three engine files on the protected ref: `PolicyPath`, `BaselinePath`, `LabelsPath`.
- `Digest`: bare lowercase hex SHA-256 of raw file bytes. For the policy it is the value the engine reports as
  `comparison.config_hash`. For baseline and labels it is the value the producer states in the upload envelope.
- `Waivers`: read the `waivers:` list of a policy file into `Waiver` values with parsed expiry instants.
- `Modules`: list the names, layers and line numbers of the policy's `modules:` map, in file order.
- `Schema`: the engine v2.3.1 config JSON Schema (`archfit.schema.json`), embedded unchanged.
- Nothing else. It does not validate policies, match waivers to findings, parse baselines or labels, or enforce expiry.

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
- The YAML library: the same single third-party dependency as `wire/settings`
  (`go.yaml.in/yaml/v3` **[default]** until `docs/tech-stack.md` names one). `Modules` uses its node API for line numbers.
- The baseline and labels files are opaque here. Only their paths and digests are known.

### Common reading rules

`Waivers` and `Modules` share these:

- Input limit 1 048 576 bytes **[default]**; larger input is an error. Invalid UTF-8 or YAML that does not parse is an error.
- Exactly one YAML document whose root is a mapping; otherwise an error. Empty input gives `nil, nil`.
- Only the one top-level key is read. Every other key, known to the engine or not, is ignored. This is
  deliberately not strict: the engine validates its own file, and a newer engine key must not break the App.
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
- `Waivers` doubles as a syntax check for control-plane callers: `nil` error means one well-formed YAML mapping
  document within the size limit, with readable waivers. It is not engine validation; the check run on the draft PR is.
- `Schema` returns a fresh copy on every call. The bytes are the engine's file, not reformatted.

## Integrations

This module imports the standard library and one YAML library. It depends on no internal module. The entries
below are its consumers (the consumer depends on this module) and its external counterpart. Values are from the
root coupling table.

| Counterpart                      | Direction                       | Strength | LCA / Rank / Distance     | Volatility | Balanced? |
| -------------------------------- | ------------------------------- | -------- | ------------------------- | ---------- | --------- |
| `internal/gate/inputs`           | inputs depends on policy        | Contract | root / 2 / 2              | High       | Yes       |
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
- `internal/control/onboarding` holds `PolicyPath` to place the proposed policy of PR 2 and to derive the `policy_merged` stage.
- `internal/control/assist` holds `PolicyPath`, `Schema` (rule descriptions for the prompt context) and `Waivers`,
  which it calls as a YAML syntax check on model output: an error means the proposal is not valid policy YAML.
- `internal/control/ui` holds `PolicyPath`, `Schema` for the editor's completion and in-browser validation, and
  `Waivers` as the same server-side YAML syntax check before a draft is saved.
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
- `Waivers` and `Modules` are total: no input panics. Work is bounded by the input limit and the library's alias limits.
- No other internal import, including `wire/settings` and `wire/report`.

## Test Specification

Policy fixtures live in `internal/wire/testdata/policy/` and are read by relative path.

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

### Integration Contract Tests

- **Test name**: `TestWaiversIgnoresTheRestOfThePolicy`
  **Scenario**: a full engine v2.3.1 policy fixture with modules, rules and two waivers, plus a copy with an extra unknown top-level key.
  **Expected behavior**: both return the same two waivers with all six fields filled.
- **Test name**: `TestDigestMatchesEngineConfigHash`
  **Scenario**: for each report fixture that ships its policy file, digest it; include the real engine capture `real-blocked-no-baseline`.
  **Expected behavior**: equals the fixture report's `comparison.config_hash` (for the real capture `4a18013b…c55574`).
- **Test name**: `TestDigestMatchesEnvelopeInputDigests`
  **Scenario**: for each upload fixture that ships baseline and labels files with its envelope.
  **Expected behavior**: `Digest` of each file equals the envelope's `baseline_digest` and `labels_digest`; a fixture without the file has `""` there.
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
  **Expected behavior**: standard library plus the one YAML library; no internal package; no `net`, `os`, or `log` package.

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
  **Scenario**: table as assist and the editor would call it: a valid policy without waivers; prose text that is not a mapping; truncated YAML; a policy with an unclosed quote.
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

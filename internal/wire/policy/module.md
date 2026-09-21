# Engine Policy Files

**Path**: internal/wire/policy/
**Parent**: internal/wire/ (Wire Contracts)
**Submodules**: none (leaf)

## Purpose

The engine policy `.archfit.yaml` and the baseline `.archfit/baseline.json` belong to the Archfit engine, not to
the App. The App needs four small things from them: where they live, the digest the engine reports for them, the
waiver expiry dates to schedule around, and the engine's config JSON Schema for the editor. This module owns exactly
that and deliberately nothing more. Without it, the digest rule would be re-derived in several places, and any
drift between them would turn honest reports into `policy_mismatch`.

## Functional Responsibilities

- Name the two engine files on the protected ref: `PolicyPath`, `BaselinePath`.
- `Digest`: `sha256:<hex>` of raw file bytes, the value compared with the report's `comparison.config_hash`
  (policy) and `comparison.gate_reference.digest` (baseline).
- `Waivers`: read the `waivers:` list of a policy file into `Waiver` values with parsed expiry instants.
- `Schema`: the engine v2.3.1 config JSON Schema, embedded unchanged.
- Nothing else. It does not validate policies, match waivers to findings, parse baselines, or enforce expiry.

## Subdomain Classification

Published language owned by the `archfit` repository; this module is the App's pinned, read-only view of it.
Supporting. Volatility: moderate, tied to engine releases (pinned v2.3.1).

## Encapsulated Knowledge

- The digest algorithm and its byte-exactness: SHA-256 over the file bytes as stored in git, lowercase hex,
  prefix `sha256:`. No YAML or JSON canonicalization, no newline or BOM handling. That this equals the engine's
  `config_hash` and `gate_reference.digest` is a root-level **[default]** to be confirmed against engine v2.3.1 in
  the first plan task; if the engine hashes differently, only `Digest` changes.
- The waiver keys the App reads: `rule`, `from`, `to`, `reason`, `approved_by`, `expires`.
- The expiry rule: `expires` is a `YYYY-MM-DD` date; the instant is 23:59:59 UTC of that day **[default: UTC]**.
  The waiver is active through that whole day.
- The embedded schema file and its provenance: copied unchanged from the engine repository at revision
  `f8877d25ba36d84d3780071580d23486e3d794d0` (tag `v2.3.1`). A sibling file `SCHEMA_SOURCE` records the source
  revision, the source path and the SHA-256 of the copy **[default]**. The source path is recorded when plan
  tasks T01/T02 pin the artifact.
- The YAML library: the same single third-party dependency as `wire/settings`
  (`go.yaml.in/yaml/v3` **[default]** until `docs/tech-stack.md` names one).
- The baseline file is opaque here. Only its path and its digest are known.

### Waivers reading rules

- Input limit 1 048 576 bytes **[default]**; larger input is an error. Invalid UTF-8 or YAML that does not parse is an error.
- Exactly one YAML document whose root is a mapping; otherwise an error.
- Only the top-level key `waivers` is read. Every other key, known to the engine or not, is ignored. This is
  deliberately not strict: the engine validates its own file, and a newer engine key must not break scheduling.
- `waivers` absent, `null`, or an empty list: result is `nil, nil`.
- `waivers` present but not a list of mappings: error.
- Per entry: `expires` absent, empty, or not a real calendar date in exact `YYYY-MM-DD` form is an error for the
  whole call. Quoted and unquoted YAML dates are both accepted. The other five keys are copied as strings and may be
  empty; unknown keys inside an entry are ignored **[default]**.
- Order of the result equals file order. No de-duplication, no filtering of expired entries.
- Any error returns `nil` waivers. There is no partial result.

## Public Contract

```go
// PolicyPath and BaselinePath are the engine files on the protected ref. [default]
const (
    PolicyPath   = ".archfit.yaml"
    BaselinePath = ".archfit/baseline.json"
)

// Digest returns "sha256:<hex>" of the raw file bytes. It is the value the engine reports as
// comparison.config_hash (policy) and gate_reference.digest (baseline).
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

`PolicyPath` is the location of the engine policy on the protected ref. `BaselinePath` is the location of the
persisted engine baseline on the protected ref. `ApprovedBy` is descriptive text copied from the file.

Semantics not visible in the signatures:

- `Digest` of empty input is the digest of empty input, not an error. Output always matches `^sha256:[0-9a-f]{64}$`.
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
report's `config_hash`, so that the digest rule lives in one place.

Shared knowledge, per counterpart:

- `internal/gate/inputs` holds `PolicyPath`, `BaselinePath`, `Digest`, `Waiver` and `Waivers`. It reads both files at the
  protected ref, digests them, digests an owner-approved head candidate, and takes the earliest `Expires`.
- `internal/control/onboarding` holds `PolicyPath` to place the proposed policy of PR 2 and to derive the `policy_merged` stage.
- `internal/control/assist` holds `PolicyPath`, `Schema` (rule descriptions for the prompt context) and `Waivers`,
  which it calls as a YAML syntax check on model output: an error means the proposal is not valid policy YAML.
- `internal/control/ui` holds `PolicyPath`, `Schema` for the editor's completion and in-browser validation, and
  `Waivers` as the same server-side YAML syntax check before a draft is saved.
- `internal/control/ui/graph` holds `Digest` only.
- `archfit` engine config: this module holds the config schema file, the two default file locations, the waiver
  keys, and the hashing rule. The engine owns all of them; this module follows a pinned release. Contract coupling
  through a versioned schema and pinned revision tolerates the distance of a separate repository.

No counterpart contract is used by this module, so nothing is restated here.

## Change Vectors

- A new engine release: replace the embedded schema and `SCHEMA_SOURCE`; adjust waiver keys if the waiver contract changed.
- The engine changes how it computes `config_hash` or the baseline digest: change `Digest` only.
- Configurable file locations per repository: the constants become defaults; the settings file would carry overrides.
- The App starts reading another policy key (for example module names): add a reader next to `Waivers`.

## Constraints and Invariants

- The engine enforces waivers, including expiry, order-independent matching, and the unwaivable coupling gate.
  The App never re-implements that; it only schedules warnings and re-runs around `Expires`.
- `ApprovedBy` is descriptive metadata, never authentication (plan T16). Authority comes from the owner approval
  the gate verifies on the pull request that changed the policy.
- This module never decides where bytes come from. Consumers read the protected ref; head-tree policy and baseline
  are never trusted, except the digest of an owner-approved candidate, which is the consumer's rule.
- The App never writes or commits a baseline; this module has no baseline writer and no policy writer.
- Digests are over raw bytes. No consumer may normalize a file before hashing.
- No I/O, no clock (expiry comparison with "now" is the caller's), no logging. Errors name the entry index and
  key, never values or reasons text.
- `Waivers` is total: no input panics. Work is bounded by the input limit and the library's alias limits.
- No other internal import, including `wire/settings` and `wire/report`.

## Test Specification

Policy fixtures live in `internal/wire/testdata/policy/` and are read by relative path.

### Unit Tests

- **Test name**: `TestDigestKnownVectors`
  **Scenario**: table: empty input; `"abc"`; a policy fixture; the same fixture with CRLF endings, with a BOM, with a trailing newline removed.
  **Expected behavior**: known SHA-256 values for the first two; all four fixture variants differ; output matches `^sha256:[0-9a-f]{64}$`.
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
- **Test name**: `TestDigestMatchesEngineFixtures`
  **Scenario**: for each report fixture that ships its policy and baseline files, digest them.
  **Expected behavior**: equals the fixture report's `config_hash` and `gate_reference.digest`. This is the test that confirms or
  refutes the raw-byte **[default]** against engine v2.3.1.
- **Test name**: `TestSchemaIsThePinnedEngineFile`
  **Scenario**: hash `Schema()` and parse it.
  **Expected behavior**: the SHA-256 equals the one in `SCHEMA_SOURCE`; the revision there is `f8877d25…`; valid JSON with a `waivers`
  property; two calls return equal but independent slices.
- **Test name**: `TestPathConstants`
  **Scenario**: read the constants.
  **Expected behavior**: `.archfit.yaml` and `.archfit/baseline.json`.

### Boundary Tests

- **Test name**: `TestWaiversRejectsBadInput`
  **Scenario**: table: invalid YAML; invalid UTF-8; two documents; root is a list; 1 048 577 bytes; an alias-expansion bomb; empty input.
  **Expected behavior**: errors for all but empty input, which gives `nil, nil`; bounded time and memory; no panic.
- **Test name**: `TestErrorsNeverEchoValues`
  **Scenario**: a waiver whose `reason` and malformed `expires` contain the marker `LEAK-7f3a`.
  **Expected behavior**: the error names entry index and key; it does not contain the marker.
- **Test name**: `FuzzWaivers`
  **Scenario**: fuzz `Waivers` seeded with the fixtures.
  **Expected behavior**: never panics; every returned `Expires` has time 23:59:59 and location UTC.
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

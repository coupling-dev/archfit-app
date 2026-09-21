# Report Envelope

**Path**: internal/wire/envelope/
**Parent**: internal/wire/ (Wire Contracts)
**Submodules**: none (leaf)

## Purpose

The envelope is the producer's statement about one uploaded payload: which repository, event, pull request,
revisions, workflow run, engine, image and payload digest it claims. This module owns that contract: the
versioned JSON Schema that `archfit-action` conforms to, the strict decoder, and the payload digest function.
Without it the App and the Action would each define the upload header by habit, unknown fields would slip in,
and "the bytes are what the producer stated" could not be checked.

## Functional Responsibilities

- Publish the `archfit.report-envelope.v1` JSON Schema as an embedded artifact (`Schema`).
- Decode envelope JSON strictly into `Envelope` (`Decode`): exact keys, all keys required, formats checked.
- Compute the payload digest, `sha256:<hex>` of the exact payload bytes (`PayloadDigest`).
- Carry the producer's statement of which baseline and labels bytes the engine loaded (`baseline_digest`,
  `labels_digest`), because the engine report itself contains no digest of those files.
- Nothing else. It does not read HTTP requests, compare fields with GitHub data, or decide eligibility.

## Subdomain Classification

Published language between `archfit-app` and `archfit-action`. Supporting, not differentiating.
Volatility: moderate. The contract changes only with a versioned Action release; the App decides what it
accepts (decisions.md §3: the verifier owns the envelope schema).

## Encapsulated Knowledge

- The schema file `envelope.v1.schema.json`, embedded with `go:embed`. JSON Schema draft 2020-12 **[default]**,
  `$id` `https://archfit.dev/schemas/report-envelope.v1.json` **[default]**, `additionalProperties: false`,
  every property required.
- Validation is a hand-written decoder, not a JSON Schema library **[default]**. The schema file is the published
  artifact; the decoder is the enforcement. A test proves both agree on the fixtures.
- The strict JSON technique. `encoding/json` alone is not strict enough: it matches keys case-insensitively,
  keeps the last of duplicate keys, turns `null` into a zero value, and ignores absent keys. The decoder
  therefore runs a token pre-pass over the top-level object first: every key must be one of the 16 exact
  lowercase names, appear exactly once, and carry a non-null value. Then it decodes with
  `DisallowUnknownFields`, requires end of input after the object, and applies the field rules below.
- Size limit of the raw envelope: 8 192 bytes **[default]**, unexported.
- Error text policy: an error names the field and the rule, never the offending value.

### Field rules (v1)

The length limits and character sets below (100, 64, 32) are **[default]**; the SHA and digest shapes are GitHub's and SHA-256's.

| JSON key          | Rule                                                                                  |
| ----------------- | ------------------------------------------------------------------------------------- |
| `schema_version`  | equals `SchemaV1`                                                                     |
| `kind`            | `report` or `discovery`                                                               |
| `repository`      | `^[A-Za-z0-9_.-]{1,100}/[A-Za-z0-9_.-]{1,100}$`                                       |
| `event`           | `^[a-z_]{1,64}$` — format only **[default]**, see below                               |
| `pull_request`    | integer ≥ 0; > 0 if and only if `event` is `pull_request`                             |
| `head_sha`        | `^[0-9a-f]{40}$`                                                                      |
| `base_sha`        | `^[0-9a-f]{40}$` when `event` is `pull_request`, otherwise exactly `""`               |
| `merge_base_sha`  | same rule as `base_sha`                                                               |
| `run_id`          | integer > 0, no fraction, no exponent                                                 |
| `run_attempt`     | integer ≥ 1                                                                           |
| `engine_version`  | `^[A-Za-z0-9._+-]{1,64}$` — format only; approval is the manifest's job               |
| `image_digest`    | `^sha256:[0-9a-f]{64}$`                                                               |
| `platform`        | `^[a-z0-9]+/[a-z0-9]+(/[a-z0-9]+)?$`, at most 32 characters — format only             |
| `payload_digest`  | `^sha256:[0-9a-f]{64}$`                                                               |
| `baseline_digest` | `^[0-9a-f]{64}$` or exactly `""` (no file); bare hex; `""` when `kind` is `discovery` |
| `labels_digest`   | same rule as `baseline_digest`                                                        |

Two digest formats exist on purpose. `payload_digest` and `image_digest` keep the `sha256:` prefix **[default]**: they
are envelope-native values in OCI style. `baseline_digest` and `labels_digest` are bare hex because they are compared
with the engine-style digest of the protected-ref files, which is bare hex like the engine's `config_hash`. A prefixed
value in a bare field, or the reverse, is a decode error, so the two can never be confused silently. A discovery
upload runs before any policy exists, so both input digests must be `""` there **[default]**.

`event` is checked as a format, not as the three supported names **[default]**. Reason: an honest upload from an
unsupported trigger (`workflow_run`) must pass decoding so that the gate can publish `action_required` with
`unsupported_event` on the pull request. A `400` would hide the defect from the PR. The same reasoning keeps
`engine_version`, `image_digest` and `platform` format-only: an unknown identity is an outcome, not a parse error.

## Public Contract

```go
// SchemaV1 is the only envelope schema version accepted.
const SchemaV1 = "archfit.report-envelope.v1"

// Kind names the payload that accompanies the envelope.
type Kind string

const (
    KindReport    Kind = "report"    // engine state report, POST /v1/reports
    KindDiscovery Kind = "discovery" // discovery draft policy, POST /v1/discoveries
)

// Envelope is what the producer states about a payload. Every field is self-reported:
// it must match authoritative GitHub data and is never trusted alone.
type Envelope struct {
    SchemaVersion string `json:"schema_version"` // must equal SchemaV1
    Kind          Kind   `json:"kind"`
    Repository    string `json:"repository"`     // "owner/name"
    Event         string `json:"event"`          // "pull_request" | "push" | "workflow_dispatch"
    PullRequest   int    `json:"pull_request"`   // 0 unless Event == "pull_request"
    HeadSHA       string `json:"head_sha"`       // 40 lowercase hex
    BaseSHA       string `json:"base_sha"`       // "" unless pull_request
    MergeBaseSHA  string `json:"merge_base_sha"` // "" unless pull_request
    RunID         int64  `json:"run_id"`
    RunAttempt    int    `json:"run_attempt"`
    EngineVersion string `json:"engine_version"` // e.g. "v2.3.1"
    ImageDigest   string `json:"image_digest"`   // "sha256:<64 hex>"
    Platform      string `json:"platform"`       // e.g. "linux/amd64"
    PayloadDigest  string `json:"payload_digest"`  // "sha256:<64 hex>" of the exact payload bytes
    BaselineDigest string `json:"baseline_digest"` // bare hex SHA-256 of the baseline file the engine loaded; "" = none
    LabelsDigest   string `json:"labels_digest"`   // bare hex SHA-256 of the labels file the engine loaded; "" = none
}
```

```go
// Decode parses and validates raw against the v1 JSON Schema. Unknown fields,
// a wrong schema_version, malformed SHAs or digests are errors.
func Decode(raw []byte) (Envelope, error)

// PayloadDigest returns "sha256:<hex>" of payload.
func PayloadDigest(payload []byte) string

// Schema returns the published JSON Schema document for SchemaV1.
func Schema() []byte
```

`SchemaV1` is the schema version constant. `KindReport` is the kind of an engine state report upload and
`KindDiscovery` is the kind of a discovery draft-policy upload. `PayloadDigest` is both a function and an
envelope field; the function computes what the field states.

Semantics not visible in the signatures:

- `Decode` returns a zero `Envelope` with every error. Errors are plain `error` values; callers map any of them to
  one rejection. The message names field and rule only.
- `PayloadDigest` is lowercase hex of SHA-256 over the bytes as given. Empty input is valid and yields the digest
  of empty input. No normalization of any kind.
- `Schema` returns a fresh copy on every call; callers may not mutate embedded data.
- `BaselineDigest` and `LabelsDigest` are bare lowercase hex SHA-256 of the raw file bytes the engine loaded, the same
  rule the App applies to the protected-ref files; `""` means the engine loaded no such file. They are self-reported
  like every other field: a match with the protected ref is necessary, never sufficient. This module does not
  compute them and imports nothing to check them.
- The three fields `EngineVersion`, `ImageDigest`, `Platform` have the same meaning as the manifest's identity,
  but `Envelope` does not reference any manifest type. The consumer copies the three strings.

## Integrations

This module imports only the standard library. It depends on no internal module. The entries below are its
consumers (the consumer depends on this module) and its external counterpart. Values are from the root coupling table.

| Counterpart                      | Direction                          | Strength                    | LCA / Rank / Distance     | Volatility | Balanced? |
| -------------------------------- | ---------------------------------- | --------------------------- | ------------------------- | ---------- | --------- |
| `internal/gate/ingest`           | ingest depends on envelope         | Contract (versioned schema) | root / 2 / 2              | High       | Yes       |
| `internal/gate`                  | gate depends on envelope           | Contract                    | root / 2 / 2              | High       | Yes       |
| `internal/control/onboarding`    | onboarding depends on envelope     | Contract                    | root / 2 / 2              | High       | Yes       |
| `archfit-action` (external repo) | the Action conforms to this schema | Contract (JSON Schema)      | outside the tree; highest | Moderate   | Yes       |

Shared knowledge, per counterpart:

- `internal/gate/ingest` holds the whole Public Contract above. It calls `Decode` on the `X-Archfit-Envelope`
  header value, checks `Kind` against the route, compares every identity field with authoritative GitHub data, and
  compares `PayloadDigest(body)` with `Envelope.PayloadDigest`. It passes `BaselineDigest` and `LabelsDigest` on for
  the gate to compare with the trusted digests of the protected ref. Nothing from ingest is known here.
- `internal/gate` imports this module for `KindReport` only, to ask its receiver for a report upload. It reads
  `BaselineDigest`, `LabelsDigest` and the three identity strings through `ingest.Upload`.
- `internal/control/onboarding` holds `Kind` and `KindDiscovery` only, to ask the gate's receiver for a discovery upload.
- `archfit-action` holds the schema document returned by `Schema` and the digest rule. It must send exactly the
  16 keys, and state the baseline and labels digests of the bytes it handed to the engine. Contract coupling tolerates the distance: separate repository, separate release cycle, versioned schema,
  strict decoding. The Action ships after the App for any additive change (see the parent's versioning rule).

## Change Vectors

- A new envelope version: add `SchemaV2`, a second schema file, and a `DecodeV2`; v1 stays until producers migrate.
- Tightening or loosening a field format within v1 when GitHub changes an identifier shape (for example SHA-256 object IDs).
- Replacing the hand-written checks with a JSON Schema library, once `docs/tech-stack.md` allows one.
- Changing the raw size limit.

## Constraints and Invariants

- Every envelope field is self-reported. Decoding proves shape, never truth. No consumer may treat a decoded
  envelope as authoritative (decisions.md §3 revision binding).
- Unknown fields are rejected (decisions.md §3). So are duplicate keys, case-variant keys, `null` values, absent
  keys, and trailing data.
- No I/O, no clock, no logging, no globals besides the embedded schema. Standard library only.
- No import of any other internal package, including wire siblings.
- Transport type only: `Envelope` is never stored and never used as a domain type by this module.
- Errors never contain field values. Logs of callers stay at IDs, digests, outcomes and timings.
- `Decode` is total: no input makes it panic. Work is bounded by the 8 192-byte limit **[default]**.
- Inherited from the parent: a breaking change means a new version constant and a new decode path.

## Test Specification

Fixtures live in `internal/wire/testdata/envelope/` and are read by relative path. Each has an `.expect.json` sibling
in the parent's format: `decode`, and for `ok` the whole decoded envelope under `fields`. This module has no private
fixture and no `testdata` folder of its own. The shared format has no key for the field an invalid fixture must name,
so `TestSchemaAndDecoderAgree` states that field per fixture name in the test.

### Unit Tests

- **Test name**: `TestDecodeFieldRules`
  **Scenario**: table, one valid base envelope mutated per row: wrong `schema_version`; `kind` `"state"`; repository without slash,
  with two slashes, empty owner; `event` `"Pull Request"`; `head_sha` uppercase, 39 and 41 characters; `image_digest` without
  prefix, with `sha512:`, 63 hex; `payload_digest` uppercase; `run_id` 0 and negative; `run_attempt` 0; `engine_version` empty
  and 65 characters; `platform` `"linux"` and 33 characters.
  **Expected behavior**: every row is an error naming the field; the unmodified base decodes.
- **Test name**: `TestDecodeCrossFieldRules`
  **Scenario**: table: `pull_request` event with `pull_request: 0`; `push` with `pull_request: 7`; `push` with a non-empty
  `base_sha`; `pull_request` event with empty `merge_base_sha`; valid `push`; valid `workflow_dispatch`; valid `pull_request`.
  **Expected behavior**: the first four fail; the last three decode with the fields unchanged.
- **Test name**: `TestInputDigestFormats`
  **Scenario**: table for `baseline_digest` and `labels_digest`: 64 lowercase hex; `""`; `sha256:` plus 64 hex; uppercase hex; 63 hex;
  key absent; `null`; a non-empty value with `kind: discovery`. Plus `payload_digest` as bare 64 hex.
  **Expected behavior**: only the first two decode; a bare `payload_digest` is an error too.
- **Test name**: `TestPayloadDigestKnownVectors`
  **Scenario**: table: empty input; `"abc"`; a 5 MB buffer; the same text with LF and with CRLF.
  **Expected behavior**: empty gives `sha256:e3b0c442…b855`; `"abc"` gives `sha256:ba7816bf…15ad`; LF and CRLF differ; output always matches `^sha256:[0-9a-f]{64}$`.
- **Test name**: `TestNumbersMustBeIntegers`
  **Scenario**: `run_id` as `1.5`, `1e3`, `"123"`, and a value above the int64 range; `pull_request` as `7.0`.
  **Expected behavior**: all are errors.

### Integration Contract Tests

- **Test name**: `TestDecodeAcceptsConformanceEnvelopes`
  **Scenario**: every fixture in `internal/wire/testdata/envelope/` marked `decode: ok` (PR report, default-branch push report, discovery by `workflow_dispatch`).
  **Expected behavior**: decodes; each `Envelope` field equals the value recorded in the fixture's expectation.
- **Test name**: `TestSchemaAndDecoderAgree`
  **Scenario**: run every envelope fixture, valid and invalid, through `Decode` and through a test-local evaluator of `Schema()`
  that supports only the keywords the schema uses (`type`, `const`, `enum`, `pattern`, `minimum`, `maxLength`, `required`,
  `properties`, `additionalProperties`, `allOf`, `if`/`then`/`else`).
  **Expected behavior**: both accept or both reject each fixture; a rejection by `Decode` names the field the test records for that
  fixture; the evaluator fails the test on any keyword it does not know.
- **Test name**: `TestSchemaDocument`
  **Scenario**: parse `Schema()`.
  **Expected behavior**: valid JSON; `schema_version` is `const` `SchemaV1`; 16 required properties; `additionalProperties` is false; two calls return equal but independent slices.

### Boundary Tests

- **Test name**: `TestDecodeRejectsNonStrictJSON`
  **Scenario**: table: an extra key `installation_id`; duplicate `head_sha` (second value valid); key `Head_SHA`; `"kind": null`;
  one key absent per row; trailing `{}` after the object; a top-level array; empty input; invalid UTF-8; 8 193 bytes.
  **Expected behavior**: every row is an error; none panics.
- **Test name**: `TestErrorsNeverEchoValues`
  **Scenario**: invalid envelopes whose bad values contain the marker `LEAK-7f3a`.
  **Expected behavior**: no error string contains the marker.
- **Test name**: `FuzzDecode`
  **Scenario**: fuzz `Decode` seeded with the fixtures.
  **Expected behavior**: never panics; any accepted input re-marshals to JSON that decodes to an equal `Envelope`.
- **Test name**: `TestPackageImportsStdlibOnly`
  **Scenario**: `go list -deps` and the direct import list for the package.
  **Expected behavior**: no internal package and no third-party package among all dependencies; no `net`, `os`, or `log`
  package among the direct imports. The transitive check cannot cover `os`: `encoding/json` and `crypto/sha256` depend on it.

### Behavior Tests

- **Test name**: `TestModifiedPayloadIsDetectable`
  **Scenario**: a fixture payload and its envelope; flip one byte of the payload (T03: mismatched report digest).
  **Expected behavior**: `PayloadDigest(payload)` no longer equals `Envelope.PayloadDigest`; for the untouched payload it does.
- **Test name**: `TestUnsupportedTriggerStillDecodes`
  **Scenario**: a well-formed envelope with `event: "workflow_run"`.
  **Expected behavior**: decodes, so the gate can publish `unsupported_event` instead of answering `400`.
- **Test name**: `TestUnknownIdentityStillDecodes`
  **Scenario**: well-formed envelope with `engine_version: "v9.9.9"` and an image digest the manifest does not list (T03: wrong image).
  **Expected behavior**: decodes; this module makes no approval decision.
- **Test name**: `TestProducerStatesTheInputsItUsed`
  **Scenario**: the upload fixture of the real engine capture (no baseline, no labels) and a fixture with both files.
  **Expected behavior**: the first decodes with both digests `""`; the second with two bare-hex digests, unchanged, for the gate to
  compare with the protected ref. A run that used a head-edited baseline states a digest the protected ref does not have.

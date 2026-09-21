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
  therefore runs a token pre-pass over the top-level object first: every key must be one of the 14 exact
  lowercase names, appear exactly once, and carry a non-null value. Then it decodes with
  `DisallowUnknownFields`, requires end of input after the object, and applies the field rules below.
- Size limit of the raw envelope: 8 192 bytes **[default]**, unexported.
- Error text policy: an error names the field and the rule, never the offending value.

### Field rules (v1)

The length limits and character sets below (100, 64, 32) are **[default]**; the SHA and digest shapes are GitHub's and SHA-256's.

| JSON key          | Rule                                                                                          |
| ----------------- | --------------------------------------------------------------------------------------------- |
| `schema_version`  | equals `SchemaV1`                                                                             |
| `kind`            | `report` or `discovery`                                                                       |
| `repository`      | `^[A-Za-z0-9_.-]{1,100}/[A-Za-z0-9_.-]{1,100}$`                                               |
| `event`           | `^[a-z_]{1,64}$` — format only **[default]**, see below                                       |
| `pull_request`    | integer ≥ 0; > 0 if and only if `event` is `pull_request`                                     |
| `head_sha`        | `^[0-9a-f]{40}$`                                                                              |
| `base_sha`        | `^[0-9a-f]{40}$` when `event` is `pull_request`, otherwise exactly `""`                       |
| `merge_base_sha`  | same rule as `base_sha`                                                                       |
| `run_id`          | integer > 0, no fraction, no exponent                                                         |
| `run_attempt`     | integer ≥ 1                                                                                   |
| `engine_version`  | `^[A-Za-z0-9._+-]{1,64}$` — format only; approval is the manifest's job                       |
| `image_digest`    | `^sha256:[0-9a-f]{64}$`                                                                       |
| `platform`        | `^[a-z0-9]+/[a-z0-9]+(/[a-z0-9]+)?$`, at most 32 characters — format only                     |
| `payload_digest`  | `^sha256:[0-9a-f]{64}$`                                                                       |

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
    PayloadDigest string `json:"payload_digest"` // "sha256:<64 hex>" of the exact payload bytes
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
- The three fields `EngineVersion`, `ImageDigest`, `Platform` have the same meaning as the manifest's identity,
  but `Envelope` does not reference any manifest type. The consumer copies the three strings.

## Integrations

This module imports only the standard library. It depends on no internal module. The entries below are its
consumers (the consumer depends on this module) and its external counterpart. Values are from the root coupling table.

| Counterpart                      | Direction                          | Strength                    | LCA / Rank / Distance     | Volatility | Balanced? |
| -------------------------------- | ---------------------------------- | --------------------------- | ------------------------- | ---------- | --------- |
| `internal/gate/ingest`           | ingest depends on envelope         | Contract (versioned schema) | root / 2 / 2              | High       | Yes       |
| `internal/control/onboarding`    | onboarding depends on envelope     | Contract                    | root / 2 / 2              | High       | Yes       |
| `archfit-action` (external repo) | the Action conforms to this schema | Contract (JSON Schema)      | outside the tree; highest | Moderate   | Yes       |

Shared knowledge, per counterpart:

- `internal/gate/ingest` holds the whole Public Contract above. It calls `Decode` on the `X-Archfit-Envelope`
  header value, checks `Kind` against the route, compares every identity field with authoritative GitHub data, and
  compares `PayloadDigest(body)` with `Envelope.PayloadDigest`. Nothing from ingest is known here.
- `internal/control/onboarding` holds `Kind` and `KindDiscovery` only, to ask the gate's receiver for a discovery upload.
- `archfit-action` holds the schema document returned by `Schema` and the digest rule. It must send exactly the
  14 keys. Contract coupling tolerates the distance: separate repository, separate release cycle, versioned schema,
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

Fixtures live in `internal/wire/testdata/envelope/` and are read by relative path. Each has an `.expect.json` sibling.

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
- **Test name**: `TestPayloadDigestKnownVectors`
  **Scenario**: table: empty input; `"abc"`; a 5 MB buffer; the same text with LF and with CRLF.
  **Expected behavior**: empty gives `sha256:e3b0c442…b855`; `"abc"` gives `sha256:ba7816bf…15ad`; LF and CRLF differ; output always matches `^sha256:[0-9a-f]{64}$`.
- **Test name**: `TestNumbersMustBeIntegers`
  **Scenario**: `run_id` as `1.5`, `1e3`, `"123"`, and a value above the int64 range; `pull_request` as `7.0`.
  **Expected behavior**: all are errors.

### Integration Contract Tests

- **Test name**: `TestDecodeAcceptsConformanceEnvelopes`
  **Scenario**: every fixture in `testdata/envelope/` marked `decode: ok` (PR report, default-branch push report, discovery by `workflow_dispatch`).
  **Expected behavior**: decodes; each `Envelope` field equals the value recorded in the fixture's expectation.
- **Test name**: `TestSchemaAndDecoderAgree`
  **Scenario**: run every envelope fixture, valid and invalid, through `Decode` and through a test-local evaluator of `Schema()`
  that supports only the keywords the schema uses (`type`, `const`, `enum`, `pattern`, `minimum`, `maxLength`, `required`,
  `properties`, `additionalProperties`, `allOf`, `if`/`then`/`else`).
  **Expected behavior**: both accept or both reject each fixture; the evaluator fails the test on any keyword it does not know.
- **Test name**: `TestSchemaDocument`
  **Scenario**: parse `Schema()`.
  **Expected behavior**: valid JSON; `schema_version` is `const` `SchemaV1`; 14 required properties; `additionalProperties` is false; two calls return equal but independent slices.

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
  **Scenario**: `go list -deps` for the package.
  **Expected behavior**: no internal package, no third-party package, no `net`, `os`, or `log` package.

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

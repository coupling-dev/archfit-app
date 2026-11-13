# Upload envelope

**Path**: internal/wire/envelope/
**Parent**: [wire](../module.md)

## Contract

`SchemaV2` is `archfit.upload-envelope.v2`. `EnvelopeV2` describes an engine result v1 payload;
it does not establish the truth of that result. `PayloadDigest` hashes the exact payload bytes
as `sha256:<hex>`. `SchemaV2Document` returns a copy of the embedded JSON Schema.

`DecodeV2(raw, payload)` rejects unknown, duplicate, missing or null keys, wrong JSON types,
invalid UTF-8, trailing data, malformed identities/digests/refs and a payload digest mismatch.
The envelope header is capped at 8192 bytes; the caller caps the payload.

Only `pull_request`, `push` and `merge_group` are accepted events. The pull request number is
positive only for a pull request. GitHub authority is checked by gate/ingest, not this decoder.
Envelope 1 and its kind, baseline/labels digests, integration and policy-change object are not accepted.

## Checks

The v2 decoder and schema tests use `../testdata/envelope-v2/`. Invalid version and legacy-key
cases prove refusal; the test-local schema evaluator independently checks structural rules.

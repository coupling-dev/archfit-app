# Wire contracts

**Path**: internal/wire/
**Submodules**: envelope, manifest, policy3, request, result, wording, workflow

## Purpose and boundaries

Published transport contracts shared with the Archfit 4 engine and Action. The parent exports nothing.
Children own decoding, schemas, digests and pinned conformance fixtures; they do not perform I/O,
make gate decisions, persist data or import one another.

## Contracts

| Child | Contract |
| --- | --- |
| [envelope](envelope/module.md) | Upload envelope 2 and its payload digest |
| [manifest](manifest/module.md) | Approved Archfit 4 engine identities and Action commits |
| [policy3](policy3/module.md) | Schema 3 policy, digest and targeted answer edits |
| [request](request/module.md) | Request line, issue and answer keys |
| [result](result/module.md) | Engine result JSON v1 and text records |
| [wording](wording/module.md) | Prohibited output vocabulary, for tests |
| workflow | YAML job/step Action calls; no policy judgment |

A versioned decoder rejects unknown versions and fields. A request line is a command rather than
a versioned document; its engine-vendored goldens define it. Digests use `sha256:<hex>`.

Envelope v1, engine architecture-state reports, the schema 2 policy reader, baseline/waiver readers,
`.archfit-app.yaml` settings and manifest v1 have been removed. Detecting a version 2 policy
through `policy3.ErrVersion2` is a migration diagnostic, not acceptance or validation of that policy.

## Fixtures and checks

`testdata/envelope-v2/` holds strict-decoder/schema goldens with expectation siblings and one
`stand-in.payload`. Other children keep their own fixtures and provenance records.
No retained test reads the removed Archfit 3 report, leakage, policy, baseline, release or schema corpus.

`contract_test.go` checks the published package set. `boundary_test.go` checks dependency isolation
and the no-I/O rule. `../imports_test.go` enforces the module-wide import matrix and package removals.

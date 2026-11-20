# Result JSON and the Text Record

**Path**: internal/wire/result/
**Parent**: internal/wire/ (Wire Contracts)
**Submodules**: none (leaf)

## Purpose

The result is what `archfit check --format json` prints: the verdict for one change, as errors and unknown reasons.
Upload envelope 2 carries it unchanged as its payload (M2 plan D5). The text record is how every surface shows it:
where, which rule, what; then why, instead, balance, next; the status line last (design 8). This module reads the
result strictly and prints its record with the engine's grammar, so the check, the comment and the pages show exactly
what the CLI prints (design 8.2). The engine owns both contracts (design 11.7); the App holds byte copies.

## Functional Responsibilities

- Decode `archfit.result.v1` strictly into `Result` (`Decode`). Name the location and the rule of the first failure,
  never a value.
- Carry the optional `repository` block of a push run (M2 D18, engine task 5): the repository state (`clean`, `drift`,
  `expired`), the drift errors, the exceptions within 14 days of their date, and the places the check could not read.
  `nil` means no state, never `clean`; `State` covers only what was read, so a reader that shows it shows `Unread`
  beside it. `gate/drift` (M2 task 22) reads it.
- Render the record of a `Result` (`Record`).
- Hold the vendored schema and the engine's goldens of each engine, pinned in its `SCHEMA_SOURCE`.
- Nothing else: no I/O, no GitHub, no decision. `waiting` is derived from open requests by the gate (M2 task 16).

## Subdomain Classification

Published language between the engine and the App. Supporting. Volatility: moderate; it changes only with a versioned
engine release.

## Two engines side by side (until task 6.5)

The App reads the result of engine v4.0.0 and of engine 0.9.0 at the same time. This departs from the surfaces design
(6.6: "Old and new result versions are not accepted side by side"). The beta plan (task 5.3) chose it: the four
onboarded repositories keep engine v4.0.0 until epic 6, and the App that reads 0.9.0 is deployed before they move.

Both use the id `archfit.result.v1`. They differ only in the balance and the surface:

| Engine | `balance` | `surface` on an error |
| --- | --- | --- |
| v4.0.0 | `{value, min}` | never |
| 0.9.0 | `{value, min, strength, volatility}` | only on an error whose balance has the two inputs |

`Decode` decides by what the result holds, not by the engine version: a balance has both inputs or neither, every
balance of one result (errors and `repository.drift`) has the same shape, and a surface needs a balance with inputs.
A mix is refused. No verdict reads the balance or the surface; they feed the record, the request numbers and the
surface answers. Which engines may upload at all is the manifest's decision (`wire/manifest`).

**Temporary.** Task 6.5 removes the v4.0.0 manifest entry and with it this dual path. Delete then:

- `testdata/v4.0.0/` (schema, corpus, records, `SCHEMA_SOURCE`) and the `v400` corpus in `vendor_test.go`;
- the `testdata/v4.0.0` row of `schemaDifferences` in `schema_test.go`, and the `optional` row of `v0.9.0` (the inputs
  become required: drop `omitempty` from `Balance.Strength` and `Balance.Volatility`);
- in `decode.go`, the shape branch of `checkShape` for a balance without inputs and the mix check;
- in `record.go`, the balance line without inputs;
- in `wire/request`, the `balance:` line without inputs (`issue.go`);
- in `wire/policy3`, the reading of version 3 (`Version3`, `testdata/v3/`, the `model` refusal of a version 3 file)
  and the refusal of `declare-model` on a version 3 policy;
- the `scripts/check-names.sh` exclusions of these v4.0.0 and v3 files.

## Encapsulated Knowledge

- The vendored files, byte copies from the repository and commit in each corpus's `SCHEMA_SOURCE` (M2 plan D4). One
  corpus per engine: `testdata/v0.9.0/` (`coupling-dev/archfit`) and `testdata/v4.0.0/` (the old engine repository, tag
  v4.0.0's commit):

  | Here, under `testdata/<engine>/` | Engine                                           |
  | -------------------------------- | ------------------------------------------------ |
  | `archfit.result.schema.json`     | `archfit.result.schema.json`                     |
  | `result/*.json`                  | `internal/model/result/testdata/` (results only) |
  | `record/*.txt`                   | `internal/output/record/testdata/`               |

  The schema also defines `archfit.can.v1`; the App never receives a `can` answer, so `can.json` and `can.txt` are not
  vendored and `Decode` refuses that schema.
- The engine has no record golden for `repository_clean`, `repository_drift` and `repository_expired`: the record never
  shows the block. Their record is that of `pass` and `expired_exception`, whose results differ only by the block.
- `testdata/<engine>/waiting.txt` is the App's own golden, not vendored and not in `SCHEMA_SOURCE`: the engine never emits
  `waiting`, so the App owns its record. It is rendered from the vendored `waiting.json`.
- Hand-written strict decoder, as in `wire/report` **[default]**: a token pre-pass walks the input beside the Go types
  (keys exactly as the `json` tags, each once; a key without `omitempty` required; no `null`; each value of its JSON
  type; integers whole and >= 1, the schema's minimum for every integer; the enum, pattern, `minLength` or `maximum` a `rule` tag names; nothing
  after the object), then `DisallowUnknownFields`. The patterns are the schema's, verbatim. The caller bounds the size
  (ingest, 5 MiB).
- The grammar, from the engine's `internal/output/record` package doc. A 0.9.0 balance prints its inputs,
  `balance: 2/10 (min 7): intrusive, assessment-repair is high` (the subject is the error's `to`), and a surface prints
  `surface: <glob> (public), <glob> (model)`; a v4.0.0 balance prints `balance: 9/10 (min 7)`. `Record` prints the producer's text as given:
  the status line is `summary`, and `next` already holds `wait for an owner: #n`. It is the record without
  `--verbose`: digests and the engine version stay out of the check and the comment (design 5).

## Public Contract

```go
const SchemaV1 = "archfit.result.v1"

type Status string // StatusPass, StatusFail, StatusWaiting (set by the App only), StatusUnknown

type Result struct { Schema, Status, Summary, Errors, Unknown, Repository, Policy, Base, Head, Date, Engine }
type Error struct { Path, Line, Rule, What, Why, Instead, Balance, Surface, Next, From, To, Key, FromChange, Exception }
type Unknown struct { Path, Line, Cause, What, Why, Next }
type Next struct { Kind, Text, Note }
type Balance struct { Value, Min, Strength, Volatility } // the inputs only from 0.9.0; HasInputs
type Surface struct { Public, Model }                     // Error.Surface, only with a balance that has inputs
type Strength string   // StrengthContract, StrengthModel, StrengthIntrusive
type Volatility string // VolatilityLow, VolatilityMedium, VolatilityHigh
type Exception struct { Rule, Expires }
type Policy struct { Path, Digest, Protected, Differs, Target }
type RepositoryState string // RepositoryClean, RepositoryDrift, RepositoryExpired
type Repository struct { State, Drift, Expiring, Unread } // Result.Repository is a pointer: nil is no state
type ExpiringException struct { Rule, From, To, Expires, Why }
type UnreadPlace struct { Path, Line, Reason, Fix }

// Decode reads raw strictly against the Result branch of the vendored schema.
func Decode(raw []byte) (Result, error)

// Record returns the text record of r: its errors, its unknown reasons, then its status line.
func Record(r Result) string
```

The JSON keys are the engine's `internal/model/result`. `Decode` returns a zero `Result` with every error. Decoding
proves shape, never truth: `gate/ingest` (M2 task 14) holds the result to the envelope, the policy and GitHub.

## Re-vendor

At a new engine commit or tag (M2 task 22, and the release): copy the files of the table above from that revision into
`testdata/v0.9.0/`, set `commit:` and every `sha256` line of its `SCHEMA_SOURCE`, then run the tests. The v4.0.0
corpus is never re-vendored; task 6.5 deletes it. `TestTypesFollowTheVendoredSchema` names
every key, required key, enum or pattern the Go types must follow; read the golden diffs before you accept them.

## Constraints and Invariants

- Standard library only; no I/O; no import of any internal package.
- Unknown keys, keys in another case, duplicate keys, `null`, absent required keys, wrong types, values outside the
  schema's enums and patterns, trailing data and invalid UTF-8 are errors. Errors never contain input values.

## Test Specification

- `TestVendoredFilesMatchSchemaSource`: every vendored file has its `SCHEMA_SOURCE` line and SHA-256, and every line
  a file.
- `TestRecordMatchesTheGoldens`: every vendored result renders byte for byte to its golden (a `repository_*` result to
  the golden of the same result without the block); every golden has a result.
  `TestWaitingResultNamesTheRequest`: the waiting record says `wait for an owner: #431`.
- `TestTypesFollowTheVendoredSchema`: the Go types and the rules equal the schema.
- `TestDecodeRefusesAMixOfBalanceShapes`: one input without the other, values outside the enums, a surface without
  inputs or in a v4.0.0 result, and both shapes in one result (errors, and errors with drift).
  `TestDecodeReadsBothBalanceShapes`, `TestDecodeReadsTheSurface`.
- `TestDecodeRejects`, `TestDecodeRejectsWhatIsNotOneObject`: one mutation of a vendored result per row, with the
  exact error, which never quotes the seeded value. `TestDecodeAcceptsAbsentOptionalKeys`, `TestDecodeReadsEveryField`,
  `TestDecodeReadsTheRepositoryBlock` (each state; a result without the block has none).
- `TestRecordLines`: the parts of the grammar no golden covers.

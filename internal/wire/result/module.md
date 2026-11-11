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
- Render the record of a `Result` (`Record`).
- Hold the vendored schema and the engine's goldens, pinned in `SCHEMA_SOURCE`.
- Nothing else: no I/O, no GitHub, no decision. `waiting` is derived from open requests by the gate (M2 task 16).

## Subdomain Classification

Published language between the engine and the App. Supporting. Volatility: moderate; it changes only with a versioned
engine release, and old and new contracts are never accepted side by side (design 11.7).

## Encapsulated Knowledge

- The vendored files, byte copies from `github.com/alexei-led/archfit` at the commit in `SCHEMA_SOURCE` (M2 plan D4):

  | Here                         | Engine                                           |
  | ---------------------------- | ------------------------------------------------ |
  | `archfit.result.schema.json` | `archfit.result.schema.json`                     |
  | `testdata/result/*.json`     | `internal/model/result/testdata/` (results only) |
  | `testdata/record/*.txt`      | `internal/output/record/testdata/`               |

  The schema also defines `archfit.can.v1`; the App never receives a `can` answer, so `can.json` and `can.txt` are not
  vendored and `Decode` refuses that schema.
- `testdata/waiting.txt` is the App's own golden, not vendored and not in `SCHEMA_SOURCE`: the engine never emits
  `waiting`, so the App owns its record. It is rendered from the vendored `waiting.json`.
- Hand-written strict decoder, as in `wire/report` **[default]**: a token pre-pass walks the input beside the Go types
  (keys exactly as the `json` tags, each once; a key without `omitempty` required; no `null`; each value of its JSON
  type; integers whole and >= 1, the schema's minimum for every integer; the enum or pattern a `rule` tag names; nothing
  after the object), then `DisallowUnknownFields`. The patterns are the schema's, verbatim. The caller bounds the size
  (ingest, 5 MiB).
- The grammar, from the engine's `internal/output/record` package doc. `Record` prints the producer's text as given:
  the status line is `summary`, and `next` already holds `wait for an owner: #n`. It is the record without
  `--verbose`: digests and the engine version stay out of the check and the comment (design 5).

## Public Contract

```go
const SchemaV1 = "archfit.result.v1"

type Status string // StatusPass, StatusFail, StatusWaiting (set by the App only), StatusUnknown

type Result struct { Schema, Status, Summary, Errors, Unknown, Policy, Base, Head, Date, Engine }
type Error struct { Path, Line, Rule, What, Why, Instead, Balance, Next, From, To, Key, FromChange, Exception }
type Unknown struct { Path, Line, Cause, What, Why, Next }
type Next struct { Kind, Text, Note }
type Balance struct { Value, Min }
type Exception struct { Rule, Expires }
type Policy struct { Path, Digest, Protected, Differs, Target }

// Decode reads raw strictly against the Result branch of the vendored schema.
func Decode(raw []byte) (Result, error)

// Record returns the text record of r: its errors, its unknown reasons, then its status line.
func Record(r Result) string
```

The JSON keys are the engine's `internal/model/result`. `Decode` returns a zero `Result` with every error. Decoding
proves shape, never truth: `gate/ingest` (M2 task 14) holds the result to the envelope, the policy and GitHub.

## Re-vendor

At a new engine commit or tag (M2 task 22, and the release): copy the files of the table above from that revision, set
`commit:` and every `sha256` line of `SCHEMA_SOURCE`, then run the tests. `TestTypesFollowTheVendoredSchema` names
every key, required key, enum or pattern the Go types must follow; read the golden diffs before you accept them.

## Constraints and Invariants

- Standard library only; no I/O; no import of any internal package.
- Unknown keys, keys in another case, duplicate keys, `null`, absent required keys, wrong types, values outside the
  schema's enums and patterns, trailing data and invalid UTF-8 are errors. Errors never contain input values.

## Test Specification

- `TestVendoredFilesMatchSchemaSource`: every vendored file has its `SCHEMA_SOURCE` line and SHA-256, and every line
  a file.
- `TestRecordMatchesTheGoldens`: every vendored result renders byte for byte to its golden; every golden has a result.
  `TestWaitingResultNamesTheRequest`: the waiting record says `wait for an owner: #431`.
- `TestTypesFollowTheVendoredSchema`: the Go types and the rules equal the schema.
- `TestDecodeRejects`, `TestDecodeRejectsWhatIsNotOneObject`: one mutation of a vendored result per row, with the
  exact error, which never quotes the seeded value. `TestDecodeAcceptsAbsentOptionalKeys`, `TestDecodeReadsEveryField`.
- `TestRecordLines`: the parts of the grammar no golden covers.

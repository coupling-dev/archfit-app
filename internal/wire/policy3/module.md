# Policy Schema 3

**Path**: internal/wire/policy3/
**Parent**: internal/wire/ (Wire Contracts)
**Submodules**: none (leaf)

## Purpose

The policy `.archfit.yaml` of Archfit 4 belongs to the engine. The App reads it to check an owner's edit and to know
which policy a result was judged by, and it writes an owner's answer into it. This module owns exactly that: a strict
decoder that accepts and rejects the same files as the engine, the digest that ties an answer to the bytes it was
written against, and three writers that add an answer to a policy without changing any other byte (design 6.1, 6.3,
6.4, 9.1, 9.2, 10.4; M2 plan task 3, D10, D11). Without it the App would re-derive the digest and would rewrite owners'
files, losing their comments and order.

`wire/policy` is the reader of the Archfit 3 policy (schema 2). It stays until M2 task 25 removes it. This package
reads schema 3 only.

## Functional Responsibilities

- `Decode`: read a schema 3 file into `File`, strictly. Unknown keys, wrong kinds, a `null`, an empty string, values outside
  their list or range, bad dates, missing required keys and a rule without exactly one form are errors. The value
  checks list every problem, one per line, with its key path.
- `Digest`: `sha256:` and the lowercase hex SHA-256 of the exact file bytes. The engine's `policy.digest` is the same.
- `AddDependsOn`, `AddException`, `AddDenyRule`: the three edits of an answer. Each inserts text and nothing else.
- `Loosens`: whether an answer loosens the policy, by its kind alone (D10).
- Vendor the engine's `archfit.schema.json` and its valid and invalid policy fixtures under `testdata`, pinned by
  `SCHEMA_SOURCE`.
- Nothing else: no I/O, no GitHub, no storage, no passkey, no check that a module's paths match a file, no engine
  policy model (defaults, balance, comparison of two policies; those are milestone 3).

## Subdomain Classification

Published language between the engine and the App. Supporting. Volatility: high until the engine 4.0.0 release moves
the pin to a tag; moderate after.

## Encapsulated Knowledge

- The decode rules of the engine's `internal/config/policyfile`: the version message, the closed values, the one form
  of a rule, `to` required except for `unowned`, `expires` as `YYYY-MM-DD`. The unknown-key and duplicate-key messages
  are those of `go.yaml.in/yaml/v3`; every other message is the engine's. A date written without quotes is a string.
  The kind checks follow `yaml.v3`'s scalars with two rules of the engine's library added: a mapping key is its text
  whatever its kind (a module may be named `123`), and a bare number with an exponent and no decimal point (`1e3`) is
  a string, so a login of that form is a name. An alias, a merge key and a key that is an alias are resolved by `yaml.v3`,
  which detects a cycle and bounds the expansion; the two rules do not reach inside what they bring. Other scalar forms the
  two libraries may resolve differently have no engine fixture and are not aligned.
- Why the writer inserts text instead of encoding the document: a YAML encoder rewrites indentation, quoting and blank
  lines, so the digest would change with bytes the answer did not touch. The node tree gives the line and column of
  each key; the inserted lines copy the indentation of their neighbours.
- The layouts the writer edits: a module that is a block mapping; `depends_on` absent, a flow list on one line, or a
  block list; `exceptions` and `rules` absent (appended at the end of the file) or a block list, with its dashes
  indented or at the key's indent. Line ends (LF or CRLF) and a missing final newline stay as they are. Any other layout
  is `ErrLayout`: the owner edits the file by hand. That includes a line break other than LF or CRLF, a document end
  marker (`...`, with or without a comment) or a second document (a `---` on the first line is fine), and an anchor, an alias or a merge key. `Decode`
  still reads those files.

## Public Contract

```go
func Decode(data []byte) (File, error)
func Digest(file []byte) string
func AddDependsOn(file []byte, module, dependency string) ([]byte, error)
func AddException(file []byte, e Exception) ([]byte, error)
func AddDenyRule(file []byte, id, from, to, why, instead string) ([]byte, error)
func Loosens(kind Answer) (bool, error)
var ErrLayout error
```

- `File` and the types it holds mirror the file. An absent key keeps its zero value, so a caller tells an absent key
  from a set one.
- `AddDependsOn` needs both modules in the policy. A dependency the policy already allows returns the file unchanged,
  so its digest does not change.
- `AddException` takes `Why`; the caller words it so that it names the request, as `Request #431 is open.` does. `Expires` is
  a date or empty. The rule `unowned` has no `To`.
- `AddDenyRule` writes `deny` with one `from` and one `to`, a `why` and an `instead`.
- Each edit decodes its own output and compares it with the old policy plus the answer. A value that would not read
  back as written, or an answer the policy does not allow, is an error, never a different policy.
- `Loosens`: `approve`, `approve-until` and `confirm` loosen; `reject` tightens; any other kind is an error. The kinds
  `first`, `workflow` and `revert` belong to later tasks.

## Integrations

Imports the standard library and `go.yaml.in/yaml/v3`; no internal package. Consumers, in later tasks: ingest (task 14)
uses `Decode` and `Digest`; answers (task 19) and the outside-the-flow confirmation (task 20) use the writers and
`Loosens`. External counterpart: the engine, which owns the schema; this package vendors its file and fixtures.
Contract strength, root / 2 / 2: balanced.

## Change Vectors

- The engine pin moves: replace `archfit.schema.json`, the fixtures and the `SCHEMA_SOURCE` lines together; at the
  release the pin becomes the tag. `TestTypesHaveTheKeysOfTheSchema` fails until the types carry a new key.
- Milestone 3: a general comparison of two policies replaces `Loosens` by kind, and the YAML editor calls `Decode`.
- A new answer kind adds an edit and a row in `Loosens`.

## Constraints and Invariants

- `Decode` accepts every valid engine fixture and rejects every invalid one. A file the two disagree on is a bug here.
- The output of an edit decodes, differs from the input by one inserted piece of text, and has the digest of its own
  bytes. The digest of a file changes exactly when one of its bytes changes.
- No I/O, no clock, no logging, no globals besides the closed value lists.
- Reasons, names and dates are written on one line; a value with a line break is an error.

## Test Specification

- `TestDecodeValidFixtures`, `TestDecodeInvalidFixtures`, `TestEveryInvalidFixtureHasExpectedMessages`: the engine's
  fixtures, each with the messages its error must contain.
- `TestDecodeReportsEveryProblem`, `TestDecodeVersionNumbers`, `TestDecodeSwitchForms`, `TestDecodeNamesForms`,
  `TestDecodeRejectsConvertedKinds`, `TestDecodeEmptyInput`: forms the fixtures do not reach.
- `TestDecodeFollowsTheEngineScalarRules`: `1e3` as a login, `7e0` as a balance, a module named `123`, merge keys, an alias
  that contains itself, a key that is an alias.
- `TestDigestEqualsTheEngineDigest`: the digest of each valid fixture equals the value `shasum -a 256` gave once.
- `TestDigestChangesOnlyWhenBytesChange`, `TestWriterNoChangeKeepsTheDigest`.
- `TestWriterGoldens`: each directory under `testdata/writer` holds `in.yaml`, `answer.json` and `out.yaml`. The output
  equals the golden, decodes, is an insertion of the input, and the kind in `answer.json` has the stated `Loosens`.
  Run `go test ./internal/wire/policy3 -run TestWriterGoldens -update` only after reading the diff.
- `TestWriterKeepsLineEndingsAndFinalNewline`, `TestWriterAcceptsALeadingDocumentStart`, `TestWriterRefuses` (unknown
  module, a layout it does not edit, including a bare CR or NEL break, a merge key, an alias, a document end marker and a
  second document; a value a flow list would split, a bad date, a reason on two lines, an invalid result).
- `TestLoosens`, `TestSourcePinsEveryVendoredFile`, `TestTypesHaveTheKeysOfTheSchema`.

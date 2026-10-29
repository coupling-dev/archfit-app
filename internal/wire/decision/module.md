# Decision Request

**Path**: internal/wire/decision/
**Parent**: internal/wire/ (Wire Contracts)
**Submodules**: none (leaf)

## Purpose

A decision request is what an unattended coding agent files, as a pull request comment, when a finding blocks it and
only the architecture owners may change the policy. This module owns that contract: the versioned JSON Schema, the
extraction of the block from a comment, the strict decoder, and the text of the App's status marker. Without it the
agent and the App would each define the block by habit, and the answer could not be read by a program.
The App owns the contract (it is the receiver, like the envelope). An engine CLI (`archfit decision request|status`)
is backlog for engine v3.5.0; until then the agent posts the block with `gh pr comment --body-file`.

## Functional Responsibilities

- Publish the `archfit.decision-request.v1` JSON Schema as an embedded artifact (`Schema`).
- Find the one request block in a comment (`HasRequest`, `Extract`), telling "no block" (`ErrNoRequest`) from "a block
  that cannot be used".
- Decode the JSON strictly into `Request` (`Decode`); name the first failing field, never its value.
- Pull the two IDs out of a request that failed `Decode`, so the answer can carry a marker (`Identify`).
- State the status marker, the status values and the status line (`StatusMarker`, `Status`, `StatusLine`).
- Nothing else: no I/O, no GitHub, no storage, no decision about who may ask or what is approved.

## Subdomain Classification

Published language between `archfit-app` and agents. Supporting. Volatility: moderate; it changes with the engine CLI
and with what owners need to see.

## Encapsulated Knowledge

- The block: `<!-- archfit:decision-request -->`, a ` ```json ` fence, the JSON, a closing fence, `<!-- /archfit:decision-request -->`.
  Exactly one of each marker. At most 262 144 bytes of a comment are scanned (GitHub allows 65 536 characters).
- Hand-written strict decoder, as in `wire/envelope` **[default]**: a token pre-pass over the top-level object and the
  `edge` and `proposal` objects (exact keys, each once, no `null`, right JSON type, nothing after the object), then
  `DisallowUnknownFields`, then the field rules. The schema file is the published artifact; the decoder is the
  enforcement; a test proves they agree.
- Free text rule: no control character but newline and tab, and no U+2028 or U+2029 (a store that escapes them would
  grow a value past its limit). `blast_radius` is not in v1.
- Where the decoder is stricter than the schema (the schema file says so): duplicate keys, U+2028/2029, a policy of
  at most 65 536 *bytes* (`maxLength` counts characters), a raw block of at most 262 144 bytes.

### Field rules (v1)

The limits and the identifier charset are **[default]**; the hex shapes are the engine's and SHA-256's.

| JSON key             | Rule                                                                              |
| -------------------- | --------------------------------------------------------------------------------- |
| `schema_version`     | equals `SchemaV1`                                                                 |
| `finding_id`         | `^[0-9a-f]{32}$`                                                                  |
| `rule_id`            | `^[A-Za-z0-9_.:/@+-]{1,128}$`                                                     |
| `edge.from`, `edge.to` | `^[A-Za-z0-9_.:/@+-]{1,200}$` (module names)                                    |
| `head_sha`           | `^[0-9a-f]{40}$`                                                                  |
| `policy_digest`      | `^[0-9a-f]{64}$`                                                                  |
| `why`                | 1 to 2000 characters of free text                                                 |
| `alternatives_tried` | array of at most 20 entries, each 1 to 500 characters of free text; may be empty |
| `proposal.kind`      | equals `policy_patch`                                                             |
| `proposal.policy`    | 1 to 65 536 characters and at most 65 536 bytes of free text                      |

### Status

First line of the App's comment: `<!-- archfit:decision-status <finding_id> <policy_digest> -->`. Second line:
`pending`, `approved (proposal PR #n)`, `declined`, `declined (<reason>)`, or `stale (head moved | policy changed |
pull request closed)`. A request that failed a check and has no record is answered with `-` for an ID it did not state
in a valid form (`Unknown`). A program reads the status as the first word of line 2.

## Public Contract

```go
const SchemaV1 = "archfit.decision-request.v1"
const ProposalPolicyPatch = "policy_patch"

type Request struct {
    SchemaVersion, FindingID, RuleID string
    Edge              Edge     // From, To
    HeadSHA, PolicyDigest, Why string
    AlternativesTried []string
    Proposal          Proposal // Kind, Policy
}

const OpenMarker, CloseMarker = "<!-- archfit:decision-request -->", "<!-- /archfit:decision-request -->"
var ErrNoRequest error

func HasRequest(commentBody string) bool          // the cheap precheck: the open marker is present
func Extract(commentBody string) ([]byte, error)   // the JSON of the one block; ErrNoRequest when there is none
func Decode(raw []byte) (Request, error)           // strict; zero Request with every error
func Identify(raw []byte) (findingID, policyDigest string)
func Schema() []byte                               // a fresh copy

type Status string // StatusPending, StatusApproved, StatusDeclined, StatusStale
const ReasonHeadMoved, ReasonPolicyChanged, ReasonPullClosed
const StatusMarkerPrefix, Unknown
func StatusMarker(findingID, policyDigest string) string
func ApprovedDetail(proposalPR int) string
func StatusLine(s Status, detail string) string
```

Semantics: `Extract` returns an error that is not `ErrNoRequest` for more than one block, an unclosed block, or a block
that is not one `json` fence. Errors name a field and a rule, never a value. `Decode` is total and bounded.

## Integrations

Imports the standard library only. Consumer: `internal/gate` (intake, through `gate/decisions`), which holds the whole
Public Contract. External counterpart: agents and, later, the engine CLI, which vendor `Schema()` byte for byte.
Contract strength, root / 2 / 2, high volatility on the consumer side: balanced.

## Change Vectors

- A new field: a new schema version; v1 stays. `blast_radius` returns when the engine can compute it deterministically.
- A second proposal kind (baseline, settings).
- The engine CLI posts the block itself; the block does not change.

## Constraints and Invariants

- Every field is self-reported; decoding proves shape, never truth. The App checks head, policy digest and policy
  against GitHub and the protected ref.
- Unknown keys, duplicate keys, `null`, wrong types, trailing data and control characters are errors.
- No I/O, no clock, no logging, no globals besides the embedded schema. No import of any internal package.
- Errors never contain field values.

## Test Specification

Tests are table-driven in the package; there is no shared fixture folder (the known-bad corpus is the tables).

- `TestDecodeFieldRules`: one valid request mutated per row, every limit at, below and above its edge. Every bad row
  names its field and the schema evaluator agrees, except the rows only the decoder can see.
- `TestDecodeRejectsNonStrictJSON`: extra keys, duplicates, case variants, `null` and absence of every key, wrong
  types, nested arrays, trailing data, truncation, invalid UTF-8, an oversize block.
- `TestErrorsNeverEchoValues`, `FuzzDecode`, `FuzzExtract`, `TestIdentify`.
- `TestExtract`, `TestHasRequest`: no block, a block with prose, CRLF, two blocks, unclosed, unfenced, a block past the scan cap.
- `TestStatusMarkerAndLine`, `TestSchemaDocument`, `TestPackageImportsStdlibOnly`.

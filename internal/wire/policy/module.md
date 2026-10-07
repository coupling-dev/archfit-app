# Engine Policy Files

**Path**: internal/wire/policy/
**Parent**: internal/wire/ (Wire Contracts)
**Submodules**: none (leaf)

## Purpose

The engine policy `.archfit.yaml`, the baseline `.archfit-baseline.json` and the labels file
`.archfit-labels.yaml` belong to the Archfit engine, not to the App. The App needs eight small things from them:
where they live, the digest rule that binds a report to them, the waiver expiry dates to schedule around, the
declared modules for the graph view, the engine's config JSON Schema, a structural check that
a text is plausibly a policy before the App proposes it, positioned schema diagnostics of a text the editor
checks or saves, and the accepted finding IDs and capture policy of a baseline before the App proposes it. This module owns exactly
that and deliberately nothing more. Without it, the digest rule would be re-derived in several places, and any
drift between them would turn honest reports into `policy_mismatch`.

## Functional Responsibilities

- Name the three engine files on the protected ref: `PolicyPath`, `BaselinePath`, `LabelsPath`.
- `Digest`: bare lowercase hex SHA-256 of raw file bytes. For the policy it is the value the engine reports as
  `comparison.config_hash`. For baseline and labels it is the value the producer states in the upload envelope.
- `Waivers`: read the `waivers:` list of a policy file into `Waiver` values with parsed expiry instants.
- `Modules`: list the names, layers and line numbers of the policy's `modules:` map, in file order.
- `Schema`: the engine v2.5.0 config JSON Schema (`archfit.schema.json`), embedded unchanged.
- `Validate`: a structural check that a text is plausibly an engine policy: one YAML mapping whose top-level keys
  all belong to the schema's closed key set and include the required `version`. It keeps foreign text (a report,
  a labels file) out of discovery pull requests; `Diagnose`, which contains it, does the same for saved drafts and
  assist proposals.
- `Diagnose`: the editor's check of a policy text: YAML syntax, the embedded schema (unknown keys, types, enum values,
  required keys, the few value bounds), and the readers above, each problem with its line and, when known, column.
  It returns nothing exactly when the text is fit to save.
- `ReadBaseline`: a structural reader of an `archfit.baseline.v2` file: the accepted finding IDs with their rule IDs,
  the `state.config_hash` the capture ran under, and the measurement profile version. It keeps a foreign or
  incomplete document out of baseline pull requests.
- Nothing else. It does no engine validation of policies or baselines: no rule type, selector, ownership, layer or
  cross-key check, which only the engine can do (`archfit config lint`). It does not match waivers or accepted
  findings to findings, read metrics or dimension snapshots, parse labels, or enforce expiry.

## Subdomain Classification

Published language owned by the `archfit` repository; this module is the App's pinned, read-only view of it.
Supporting. Volatility: moderate, tied to engine releases (config schema pinned v2.5.0).

## Encapsulated Knowledge

- The digest algorithm and its byte-exactness: SHA-256 over the file bytes as stored in git, bare lowercase hex,
  no prefix. No YAML or JSON canonicalization, no newline or BOM handling. Verified against engine v2.3.1: the
  engine's `comparison.config_hash` is exactly this value for the policy file. The envelope's `payload_digest`
  is a different format (`sha256:` prefix) owned by `wire/envelope`; the two are never compared with each other.
- What the engine does not hash. It reports no digest for the baseline or the labels file; `comparison.labels_hash`
  is a canonical hash of approved labels and cannot be reproduced from raw bytes. For those two files the binding
  is: producer states `Digest` of the bytes it used, the App compares with `Digest` of the protected-ref bytes.
- The waiver keys, pinned to the v2.5.0 config schema (`WaiverDef`, unchanged since v2.3.1): `rule`, `from`, `to`,
  `reason`, `approved_by`, `expires`; the engine requires all but `from` and `to`.
- The expiry rule: `expires` is a `YYYY-MM-DD` date, valid through the end of that day; the instant is 23:59:59
  UTC of that day **[default: UTC]**. The engine refuses a malformed `expires` when it loads the config
  (`internal/config/config.go` at v2.3.1 and v2.4.0), so such a policy runs no check at all.
- The modules shape, pinned to the same schema: `modules:` is a map from module name to a definition with `paths`,
  `public`, `internal`, `layer` and other keys (v2.5.0 adds the allowlists `depends_on` and `visible_to`). Only the
  name, `layer` and the key's line are read.
- The embedded schema file and its provenance: `archfit.schema.json` copied unchanged from the engine repository
  at revision `59318b9eec906f54bc7a54d01db634b0c20c0647` (tag `v2.5.0`). A sibling file `SCHEMA_SOURCE` records the
  source revision, the source path and the SHA-256 of the copy **[default]**.
- One pinned schema, not one per engine release **[default]**. Its users are `Diagnose`, `Validate`, the assist
  prompt, and `GET /schema/policy.json` (one route for every repository, for editors outside the App). None of them knows which engine release a
  repository runs: that pin is in the repository's workflow file, which the App does not parse. So the schema is
  the one of the release new workflows pin, `manifest.Current().EngineVersion`, and the parent `wire` test
  `TestPolicySchemaFollowsTheCurrentEngine` fails as soon as `current` moves until this file and `SCHEMA_SOURCE`
  are re-pinned. The pin is v2.5.0. v2.4.0 added the rule key `guard` to v2.3.1's schema (an optional key inside
  config schema v2, no top-level key); v2.4.1 adds an `enum` of the eleven rule types to `rules[].type`, so a
  misspelled type is an enum miss with a "Did you mean" suggestion instead of passing as free text. v2.5.0 adds
  optional keys only: the module keys `depends_on` and `visible_to` (lists of module names or selectors), the
  twelfth rule type `module_dependencies`, and the rule keys `from_module`, `to_module`, `rationale`, `docs` (text)
  and `alternatives` (a list of text). `module_review.gate` is not new; only its engine behavior changed. Whether
  an allowlist entry or a module selector names a declared module is the engine's check (`archfit config lint`
  `unknown_module`), not the schema's: `Diagnose` accepts any text there. During an upgrade window a repository
  still on N-1 therefore gets a schema that also applies N's changes; its own engine decides on the draft pull
  request's check, which stays the authority. `Validate` reads only the root key set, which v2.4.0, v2.4.1 and
  v2.5.0 leave unchanged. Per-release pins need a per-repository engine release in the control plane first.
- The top-level key set of a policy. It is derived from the embedded schema's root `properties` and `required`,
  read once lazily with `encoding/json`; there is never a second hand-written list. The derivation relies on the
  root's `additionalProperties: false`; a schema without it has no closed key set and `Validate` then always fails.
  In v2.5.0, as in v2.3.1, the root has sixteen properties and requires only `version`; the schema file is the one
  source.
- The JSON Schema subset `Diagnose` walks: exactly the keywords the embedded schema uses (`$ref` to `$defs`, `type`,
  `properties`, `patternProperties`, `additionalProperties`, `required`, `items`, `minItems`, `enum`, `const`, `oneOf`,
  `minimum`, `minLength`, `format`; `$schema`, `$id`, `description` and `default` are annotations). The schema is decoded
  strictly, and `Diagnose` fails closed on anything the walker would apply only in part: any other keyword, a
  list-valued `type`, a `$ref` that does not resolve or whose chain does not end, a validation keyword beside `$ref`
  or beside `oneOf`, a `format` other than `date` and `date-time`, and a property name that a key pattern also
  matches. Key patterns are compiled in sorted order and every matching pattern applies. RE2 has no look-ahead, so the
  one `metrics` pattern `^(?!function_loc_threshold$).+$` is read as "any key but that one"; any other pattern must
  compile as Go `regexp`. No JSON Schema library: a few hundred lines of walker check the same keywords and keep the
  YAML line and column of every node.
- Scalars are judged by their YAML tag, with the engine decoder's leniency where it is uniform **[default]**. Probed
  with `archfit config lint` of engine `v2.4.0-3-g4fca82d`: it accepts `owner: 123` and a bare date for the `date-time`
  field `reviewed_at`; it rejects `enabled: yes`, `json: 1` and `metrics.function_loc_threshold: "60"`, but accepts
  `max: "3"` and `version: "2"`. So a `string` takes any scalar, `integer` and `number` take only an unquoted number
  (stricter than the engine in some places, never looser), `boolean` takes only `true` or `false`, and of the two
  formats only `date` is checked (`YYYY-MM-DD`, a real day, the engine's and the `Waivers` rule). `minimum` reads the
  number as the library decodes its tag, so `-0x1`, `-0o7` and `-1_0` count.
- A null is the engine decoder's default for a key, with one exception that the schema shows: a required key. The
  engine accepts `gate: ~`, `enabled: ~`, `duplicated_knowledge: ~` and `max_bytes: ~`, and rejects `version: ~`,
  `rules[].id: ~` and `external_systems.<name>.targets: ~`. So a null passes every check except `required`, where it
  counts as missing ("needs a value"). Known gap: the engine also rejects `metrics.function_loc_threshold: ~`, a rule of
  its own `metrics` decoder that the schema does not state; `Diagnose` accepts it and `archfit config lint` catches it.
- The YAML library names, for a parser or scanner error, the line where the enclosing block or token starts, or the
  problem line when that block starts on line 1; it prints a scanner line 1-based and a parser line 0-based, leaving
  out line 0. `read` adds one for the parser's problem wording (measured on `go.yaml.in/yaml/v3` v3.0.5), and
  `Diagnose` says "The problem is at line N or below it" rather than claim the exact line, and clamps an
  end-of-stream line to the last line of the text. A duplicate key and a decode error name their exact line.
- The YAML library: the same single third-party dependency as `wire/settings`
  (`go.yaml.in/yaml/v3` **[default]** until `docs/tech-stack.md` names one). `Modules` uses its node API for line numbers.
- The labels file is opaque here. Only its path and digest are known.
- The baseline keys the App reads, pinned to engine v2.3.1 (`internal/baseline` at the pinned revision, unchanged
  in v2.4.0): `schema_version` (`archfit.baseline.v2`), `accepted[].fingerprint` (the finding ID,
  `hex(sha256(…)[:16])`, so 32 lowercase hex), `accepted[].rule_id`, `state.config_hash` (equal to `Digest` of the
  policy the capture ran under, checked on the corpus captures) and `state.measurement_profile.version`. v2.3.1
  writes the profile on every capture; a file without it cannot be bound to an approved identity and is refused.

### Baseline reading rules

- Input limit 1 048 576 bytes **[default]**, the read cap of `github/api.File`: a larger baseline could not be read
  back from the protected ref by the gate. Invalid UTF-8 is an error.
- One JSON object (`encoding/json`; trailing data is an error). Keys the App does not read are ignored, at every depth:
  the engine adds baseline keys over time (seams, module digests), and the engine's own decoder ignores unknown keys
  too. This is the third deliberate nuance of the parent's "unknown fields are rejected" rule, after the policy
  readers and the report key sets.
- Required: `schema_version` equal to `BaselineSchemaV2`; `accepted`, a list (empty is fine, `null` is not);
  `state.config_hash`, 64 lowercase hex; `state.measurement_profile.version`, 1 to 64 token characters.
- Per accepted entry: `fingerprint` exactly 32 lowercase hex; `rule_id` 1 to 128 characters of `A-Z a-z 0-9 _ . : / + -`;
  fingerprints distinct. These are token classes, so a caller may render both in Markdown code spans unescaped.
- Any error returns the zero `Baseline`. Errors name the key and the rule, never a value.

### Common reading rules

`Waivers`, `Modules`, `Validate` and `Diagnose` share one reader and these rules:

- Input limit 1 048 576 bytes **[default]**; larger input is an error. Invalid UTF-8 or YAML that does not parse is an error.
- Exactly one YAML document whose root is a mapping; otherwise an error. Empty input gives `nil, nil` from
  `Waivers` and `Modules`, and an error from `Validate`.
- Before the library's full decode, one walk of the parsed tree bounds it **[default]**: at most 524 288 nodes (half
  the input limit; a node takes at least two bytes, so this bounds only the walk itself), 10 000 keys per mapping,
  no duplicate key (the library's rule: same kind and same text; refused at the first one, with its line), no
  `!!binary` value, no alias inside its own anchor, and at most 50 000 000 units of decode work with every alias
  expansion counted again: a node costs 1 plus 1 per 64 bytes of its text, and a mapping of k keys adds k·(k−1)/2
  pairs times 1 plus 1 per 64 bytes of its longest key. The library's duplicate-key scan compares every pair of keys
  byte by byte and allocates a string per duplicate pair, so without the walk 30 KB of identical keys exhausts
  memory; it base64-decodes a `!!binary` value into a new string at every expansion, and no engine key takes binary
  data. Breaking a bound is `errTooComplex`; a duplicate key or a `!!binary` value is a syntax error with its line.
- Then the library's full decode runs once: merge values and alias cycles. Its own "excessive aliasing" refusal is
  `errTooComplex` too.
- Line breaks other than LF and CRLF are not a reading error: the engine's decoder treats NEL, LS and PS as text, and
  a waiver reason pasted with one must not lose the waiver schedule. Only the readers that report lines refuse them
  (`Modules`, `Diagnose`).
- Each refusal on the gate path has a cost. `Waivers` (gate inputs, at every resolve and sweep) and `Validate`
  (discovery uploads) refuse: input over 1 MB, invalid UTF-8, YAML that does not parse, more than one document, a root
  that is not a mapping, a duplicate key, a `!!binary` value, an alias cycle, and a bound above. The engine refuses
  every one of these except an over-large decode, so a refused policy is one no engine run accepts either, apart from
  an unusually large or alias-heavy file. When `Waivers` refuses, the gate runs with no waiver schedule and logs it:
  no early re-run when a waiver expires, while the engine still enforces the waivers. When `Validate` refuses, no
  discovery pull request is opened.
- `Waivers` and `Modules` read only their one top-level key. Every other key, known to the engine or not, is
  ignored. This is deliberately not strict: the engine validates its own file, and a newer engine key must not
  break these two readers. `Validate` is the strict one; see its rules below.
- Any error returns a `nil` slice. There is no partial result.

### Waivers reading rules

- `waivers` absent, `null`, or an empty list: result is `nil, nil`.
- `waivers` present but not a list of mappings: error.
- Per entry: `expires` absent, empty, or not a real calendar date in exact `YYYY-MM-DD` form is an error for the
  whole call. Quoted and unquoted YAML dates are both accepted. The other five keys are copied as strings and may be
  empty; unknown keys inside an entry are ignored **[default]**.
- Order of the result equals file order. No de-duplication, no filtering of expired entries.
- A malformed `expires` is an error here, and the engine refuses the whole file at config load. Nothing is lost: a
  policy the engine cannot load produces no report, so there is no expiry to schedule.

### Modules reading rules

- `modules` absent, `null`, or an empty map: result is `nil, nil`. `modules` present but not a mapping: error.
- One `ModuleDef` per map key, in file order. `Name` is the key text. `Line` is the 1-based line of the key in
  the input, counting LF line breaks. `Layer` is the entry's `layer` scalar, or `""` when absent.
- An entry whose value is not a mapping, or whose `layer` is not a scalar, is an error. All other keys of an entry
  are ignored. Names are at most 200 characters, at most 5 000 modules **[default]**; more is an error.
- A duplicate module name is an error (the YAML library already rejects duplicate mapping keys).
- Aliases and merge keys under `modules` are resolved by the library; `Line` is then the line of the key, not of the anchor.
  Keys merged in with `<<` directly under `modules` follow the map's own keys, and the map's own keys win.
- A lone CR, NEL, LS or PS is an error when `modules` is a mapping: the library counts each as a line break, an LF
  split does not, so `Line` would be wrong **[default]**.

### Validate rules

- The common reading rules apply first, with the same reader and the same errors.
- An empty document (no bytes, white space only, comments only) is an error: `version` is required.
- Every top-level key must be a name in the schema's root `properties`. The match is exact and case-sensitive.
  A key that is not a plain string (a number, a null, a merge key `<<`) is not in the set and is an error, so keys
  merged into the root are never considered.
- Every name in the schema's root `required` must be present as a key. Presence is enough: `version:` with a null
  value passes. No value is inspected, at any depth; a policy with a malformed waiver `expires` passes.
- The first failure wins. An unknown key is reported by its 1-based position among the top-level keys, a missing
  required key by its schema name. No key or value of the input appears in any error.
- A policy written for a newer engine with a new top-level key is rejected until the embedded schema is replaced.
  That is intended: the pinned engine would reject the file as well.

### Diagnose rules

- The common reading rules apply first, on one parse that the three readers below share. A reading error is one
  diagnostic: a syntax error with the library's line and problem ("at line N or below it" unless the line is exact),
  a root that is not a mapping at line 1, and too large, not UTF-8, a NEL/LS/PS line break (checked first, since a
  text area does not show it), too complex or more than
  one document without a line.
  An empty document gives one diagnostic per required root key, without a line.
- Then the schema walk, from the root: an unknown key at the key's line and column, with "Did you mean" when a known key
  of that mapping is within edit distance 2 (a swap of neighbours costs 1) and the distance is at most a third of its
  length; a wrong type, enum or const value, a value under `minimum`, `minLength` or `minItems`, or a bad `date` at
  the value; a missing required key at the mapping, and a required key whose value is null at that value. An enum
  miss gets the same suggestion from the enum values. A mapping or list where a `const` or `enum` is expected is
  wrong. Suggestions are computed only for problems that are listed.
- A problem inside an alias is reported at the alias, once per use, not at the anchor; the check uses the anchored
  node.
- A merge key `<<` is refused at the top level, as `Validate` refuses it. Below it, the merged keys follow YAML merge
  semantics (the mapping's own keys win, then the merged mappings in order), count for `required`, and are checked
  against the schema of the mapping they are merged into, each problem reported at the merge key.
- At most 100 diagnostics in line order, then one note that more are not listed.
- Only when the walk finds nothing: `Waivers`, `Modules` and `Validate` run over the same parse, and the first error becomes one
  diagnostic without a line (for example a module name over 200 characters). So `nil` from `Diagnose` implies `nil`
  errors from all three.
- Messages may quote keys of the input and the library's syntax problem, each at most 64 characters on one line. They
  are for the person who wrote the text; no caller logs them.

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
// Schema returns the engine v2.5.0 config JSON Schema, embedded unchanged: Diagnose's schema, the assist
// prompt's context, and the file served for editors outside the App.
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

`Validate` is the structural gate for text that is about to be proposed as a policy:

```go
// Validate reports whether policy is plausibly an engine policy file: a YAML document whose root is a
// mapping, whose top-level keys all belong to the engine config schema's closed key set, and which has
// the required `version` key. It is a structural check, not engine validation: the engine remains the
// authority on the file's meaning. Errors never echo keys or values.
func Validate(policy []byte) error
```

`Diagnose` is the editor's check: the one check of a text the editor checks or saves.

```go
// Diagnostic is one problem of a policy text, worded for the person who wrote the text.
type Diagnostic struct {
    Line    int    // 1-based line in the text; 0 when the problem has no single place
    Column  int    // 1-based column; 0 when unknown
    Message string // one sentence; may quote keys of the text, at most 64 characters of each
}

// Diagnose checks a policy text against the embedded engine config schema and the App's own readers. It returns
// nil exactly when the text is fit to save: then Validate, Waivers and Modules all accept it too.
func Diagnose(policy []byte) []Diagnostic
```

`ReadBaseline` is the structural gate for a baseline that is about to be proposed:

```go
// BaselineSchemaV2 is the only engine baseline schema the App reads (engine v2.3.1 writes it).
const BaselineSchemaV2 = "archfit.baseline.v2"

// MaxBaselineBytes caps a baseline file: 1 MiB, the size github/api reads back from the protected ref.
const MaxBaselineBytes = 1 << 20

// Accepted is one accepted finding of a baseline: its engine finding ID and the rule that produced it.
type Accepted struct {
    Fingerprint string // 32 lowercase hex
    RuleID      string
}

// Baseline is the App's view of an engine baseline file: what it accepts and what it was captured under.
type Baseline struct {
    Accepted       []Accepted // file order
    ConfigHash     string     // state.config_hash: Digest of the policy the capture ran under
    ProfileVersion string     // state.measurement_profile.version
}

// ReadBaseline decodes an engine baseline file. It is a structural check, not engine validation: one JSON
// object of schema BaselineSchemaV2, at most MaxBaselineBytes, with a state block naming a 64-hex config hash
// and a measurement profile version, and accepted findings with distinct 32-hex fingerprints and token rule IDs.
// Errors never echo keys or values.
func ReadBaseline(raw []byte) (Baseline, error)
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
- `Validate` follows the Validate rules above. It is the check control-plane callers run before they propose
  a discovery draft; the editor and assist use `Diagnose`: `nil` means one well-formed YAML mapping within the size limit, with only engine config keys
  and with `version`. It is not engine validation; the check run on the draft PR is. `Validate` does not read
  waivers: a caller that also needs readable waivers calls `Waivers` as well.
- `Waivers` alone is not a policy check: it ignores unknown keys, so prose or JSON with a colon passes it.
- `Diagnose` follows the Diagnose rules above. It is stricter than `Validate` (it reads values) and looser than the
  engine (it knows no rule type, selector or module), so `nil` means "fit to save", never "the engine accepts it".
  `Line` refers to the exact bytes passed in; a browser's CRLF does not move it.
- `Schema` returns a fresh copy on every call. The bytes are the engine's file, not reformatted.
- `ReadBaseline` follows the baseline reading rules above. `Accepted` keeps file order; a caller that compares two
  baselines treats the fingerprints as a set. `ConfigHash` is the capture's own claim: the caller compares it with
  `Digest` of the protected policy, and `ProfileVersion` with the approved manifest row.

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
| `archfit` engine config (v2.5.0) | policy follows the engine files | Contract | outside the tree; highest | Moderate   | Yes       |

The `control/ui/graph` row exists because the graph's stale flag compares `Digest` of the policy text with the
report's `config_hash`, and its nodes come from `Modules`, so that both rules live in one place.

Shared knowledge, per counterpart:

- `internal/gate/inputs` holds `PolicyPath`, `BaselinePath`, `LabelsPath`, `Digest`, `Waiver` and `Waivers`. It reads the
  three files at the protected ref, digests them, digests an owner-approved head candidate, and takes the earliest
  `Expires`. The gate compares the policy digest with the report's `config_hash`, and the baseline and labels
  digests with the envelope's `baseline_digest` and `labels_digest`.
- `internal/gate` (the façade) no longer imports this module: the protected-path set is owned by `internal/gate/inputs`, which holds the three path constants.
  The digests and the earliest expiry reach it through `inputs.Trusted`, without a policy type.
- `internal/control/onboarding` holds `PolicyPath` to place the proposed policy of PR 2 and to derive the `policy_merged`
  stage, `BaselinePath` and `LabelsPath`, `Waivers`, and `Validate`, which it calls on an uploaded discovery draft: an
  error means no pull request is opened. For a baseline upload it also holds `Digest`, `MaxBaselineBytes`,
  `Accepted`, `Baseline` and `ReadBaseline`: an error means no pull request is opened, and `ConfigHash` must equal
  `Digest` of the protected policy.
- `internal/control/assist` holds `PolicyPath`, `Schema` (rule descriptions for the prompt context), `Diagnostic` and
  `Diagnose`, which it calls on a provider answer: any diagnostic means the proposal is not a policy the editor
  would save. It drops the messages.
- `internal/control/ui` holds `PolicyPath`, `Schema` (served at `/schema/policy.json`), `Diagnostic` and `Diagnose`
  as the one check of the editor's Check and of every save, and `Modules` for the graph page.
- `internal/control/ui/graph` holds `Digest`, `ModuleDef` and `Modules`: nodes, layers and the policy line to jump to.
- `archfit` engine config: this module holds the config schema file, the three file locations, the waiver keys,
  the shape of the `modules` map, and the hashing rule. The engine owns all of them; this module follows a pinned release. Contract coupling
  through a versioned schema and pinned revision tolerates the distance of a separate repository.

No counterpart contract is used by this module, so nothing is restated here.

## Change Vectors

- A new engine release: replace the embedded schema and `SCHEMA_SOURCE` in the change that moves the manifest's
  `current` to it (v2.4.0 brought the rule key `guard`, v2.4.1 the rule-type enum, v2.5.0 the module allowlists,
  module selectors and rule rationale); adjust waiver keys if the waiver contract changed.
- The control plane learns each repository's engine release: then the schema can be pinned per release.
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
- The App never writes a baseline and never commits one to the protected ref; this module has no baseline writer and
  no policy writer. A captured baseline reaches the protected ref only through an owner-approved pull request that
  `control/onboarding` proposes with the producer's exact bytes.
- Digests are over raw bytes. No consumer may normalize a file before hashing.
- A baseline or labels digest stated by the producer is self-reported. It proves nothing alone; it only lets the
  gate refuse a run that says it used other bytes than the protected ref holds.
- No I/O, no clock (expiry comparison with "now" is the caller's), no logging. Errors name the entry index and
  key, never values or reasons text. `Diagnose` is the one exception: its messages may quote keys, for the author only.
- `Validate` uses the same input limit, the same reader and the same duplicate-key, alias and line-break handling
  as `Waivers`. An empty or comment-only document is an error for `Validate` only, because `version` is required.
- `Validate` is structural. It never replaces the engine's validation and never inspects a value.
- `Waivers`, `Modules`, `Validate` and `Diagnose` are total: no input panics. Work is bounded by the input limit and the library's alias limits.
- No other internal import, including `wire/settings` and `wire/report`.

## Test Specification

Fixtures come from the shared corpus and are read by relative path: the policy fixtures in
`internal/wire/testdata/policy/` (`<name>.yaml` with its `.expect.json`), and in `internal/wire/testdata/report/` the
`.policy.yaml`, `.baseline.json`, `.labels.yaml` and `.envelope.json` siblings of the report fixtures. The parent
document defines names, suffixes and the expectation format.

One fixture is private, because the shared corpus has no equivalent: `testdata/policy/malformed-expiry.report.json`,
the synthetic engine report for the shared `policy/malformed-expiry.yaml`. `TestMalformedExpiryNeedsNoSchedule` reads
it and first checks that its `config_hash` is the digest of that policy. The report is synthetic: engine v2.3.1 and
v2.4.0 refuse that policy at config load and produce no report.

A second private fixture, `testdata/policy/engine-self.archfit.yaml`, is the engine repository's own `.archfit.yaml`
at archfit commit `2d20da8`, copied unchanged: a large real policy that `Diagnose` must accept.

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

- **Test name**: `TestDiagnose`
  **Scenario**: table: a valid policy; a syntax error four lines into the text; a parser error after a leading comment,
  worded as "at line N or below it" with N the start of its block, and one on the first line; a tab indent; a duplicate key; an unknown top-level and nested key with a close match, and one without;
  a list where a nested list is expected, read for its line and column; text for a boolean and for an integer; a list
  for a mapping and a scalar list item; an enum miss with and without a close match; a misspelled rule type
  (`forbiden_dependency`: the twelve types, "Did you mean `forbidden_dependency`?"); the v2.5.0 keys: valid
  `depends_on` and `visible_to` lists (a module name, `layer:`, `role:` and glob selectors, an empty list) with a
  `module_dependencies` rule, an allowlist entry that names no declared module (no diagnostic: the engine's check),
  valid `from_module`, `to_module`, `rationale`, `alternatives`, `docs`, an allowlist given as text and as a
  mapping, an allowlist item that is a list, `alternatives` as text, `rationale` as a list, `from_module` as a
  mapping, the typos `depend_on`, `visibile_to`, `from_modul`, `rationalle` (each with "Did you mean") and
  `module_dependecies` (an enum miss with "Did you mean `module_dependencies`?"); `version: 1`; `enabled: yes`; a
  missing top-level and nested required key; a comment-only text; `minimum`, `minItems` and `date`; a `metrics` entry;
  three problems out of order; a top-level merge key; a non-mapping root; a 100-character quoted key; a 201-character
  module name; a quoted number; a mapping and a list where a const or enum is expected; `version: ~` and a waiver
  `rule: ~`; `minimum` against `-0x1`, `-1_0` and `-0o7`; a `!!binary` value; an empty `reason` (minLength); a problem inside an alias, and one
  alias used twice; a merged key the merge site does not allow; an LS line break.
  **Expected behavior**: exactly the listed diagnostics, with line, column and message; `nil` for the valid policy;
  alias problems at each alias, a merged key's problem at the merge key.
- **Test name**: `TestSuggest`
  **Scenario**: table of misspelled keys against a key list, and `forbiden_dependency` against `forbidden_dependency`.
  **Expected behavior**: the close key, or nothing when no key is within the distance rule.
- **Test name**: `TestDiagnoseFailsClosedOnAnUnknownSchemaKeyword`
  **Scenario**: compile the embedded schema; then schemas with `maximum`, a list-valued `type`, an unresolved `$ref`, a
  look-ahead pattern other than the known one, bytes that are not JSON, `format: email`, `minimum` beside `$ref`, `enum`
  beside `oneOf`, a `$ref` cycle, and a property that a key pattern also matches.
  **Expected behavior**: the embedded schema compiles; every other one is an error.
- **Test name**: `TestSchemaReferenceChainsResolve`
  **Scenario**: a property whose `$ref` names a definition that is itself a `$ref`.
  **Expected behavior**: the property resolves to the last definition.
- **Test name**: `TestValidateKeySetComesFromSchema`
  **Scenario**: derive the key set from the embedded schema and from a copy with one more root property, also listed as required;
  schema bytes that are not JSON, have no root properties, or lack `additionalProperties: false`.
  **Expected behavior**: the embedded schema's root is closed and rejects a policy with the new key; the copy accepts it and
  names the new required key when it is absent; the broken schemas are errors.

### Integration Contract Tests

- **Test name**: `TestWaiversIgnoresTheRestOfThePolicy`
  **Scenario**: a full engine v2.3.1 policy fixture with modules, rules and two waivers, plus a copy with an extra unknown top-level key.
  **Expected behavior**: both return the same two waivers with all six fields filled.
- **Test name**: `TestValidateAcceptsRealPolicies`
  **Scenario**: every `internal/wire/testdata/policy/*.yaml` and every
  `internal/wire/testdata/report/*.policy.yaml`, including the real engine captures and the malformed-expiry policy that
  `Waivers` rejects; table: `version` only; null `version`; all sixteen keys; CRLF with comments; JSON spelling; an alias below a key.
  **Expected behavior**: `nil` error for all.
- **Test name**: `TestDiagnoseAcceptsRealPolicies`
  **Scenario**: the same corpus as `TestValidateAcceptsRealPolicies`, plus the engine's own `.archfit.yaml`.
  **Expected behavior**: `nil` for every file but the malformed-expiry policy, which gets one positioned `expires` diagnostic.
- **Test name**: `TestDiagnoseReadsScalarsLikeTheEngine`
  **Scenario**: table of texts engine v2.4.1 accepts: a number for text, a bare date for `reviewed_at`,
  an unquoted waiver date, null values of optional keys (`gate`, `enabled`, `duplicated_knowledge`), `auto`, required keys brought in by a merge, a merge below a module, an alias
  for a value, a tagged and a block scalar.
  **Expected behavior**: `nil` for every row.
- **Test name**: `TestDigestMatchesEngineConfigHash`
  **Scenario**: for each shared report fixture with a `.policy.yaml` sibling, digest the sibling; include the real engine capture
  `real-blocked-no-baseline`.
  **Expected behavior**: equals the fixture report's `comparison.config_hash` (for the real capture `4a18013b…c55574`).
- **Test name**: `TestDigestMatchesEnvelopeInputDigests`
  **Scenario**: for each shared report fixture with an `.envelope.json` sibling: its `.baseline.json` and `.labels.yaml` siblings, when present.
  **Expected behavior**: `Digest` of each file equals the envelope's `baseline_digest` and `labels_digest`; a fixture without the file has `""`
  there. The exception is `baseline-tampered`, whose envelope must state a baseline digest different from its file's. At least one
  baseline and one labels file are checked.
- **Test name**: `TestModulesOnEnginePolicy`
  **Scenario**: the real capture's policy (modules `a` and `b`, no layers) and a full v2.3.1 policy fixture with layers, rules and waivers.
  **Expected behavior**: `a` at line 3 and `b` at line 6 for the capture; for the full fixture every module of the recorded expectation, in order.
- **Test name**: `TestSchemaIsThePinnedEngineFile`
  **Scenario**: hash `Schema()` and parse it.
  **Expected behavior**: the SHA-256 equals the one in `SCHEMA_SOURCE`; the revision there is `59318b9e…`; valid JSON with `waivers`
  and `modules` properties and a `WaiverDef` that requires `rule`, `reason`, `approved_by`, `expires`; two calls return equal but independent slices.
- **Test name**: `TestPathConstants`
  **Scenario**: read the constants.
  **Expected behavior**: `.archfit.yaml`, `.archfit-baseline.json` and `.archfit-labels.yaml`.
- **Test name**: `TestReadBaselineRealCaptures`
  **Scenario**: the real v2.3.1 captures `real-baselined-comparable.baseline.json` (two accepted) and
  `real-attention-baseline.baseline.json` (none).
  **Expected behavior**: fingerprints and rule IDs in file order; `ConfigHash` equals `Digest` of the sibling policy;
  `ProfileVersion` `archfit.measurement.v1`; the empty capture has no accepted entry and the same `ConfigHash`.
- **Test name**: `TestReadBaselineIgnoresUnknownKeys`
  **Scenario**: a minimal valid baseline with unknown keys at the root, inside an accepted entry and inside `state`.
  **Expected behavior**: decodes with its one accepted entry.

### Boundary Tests

- **Test name**: `TestReadersRejectBadInput`
  **Scenario**: table, run for both `Waivers` and `Modules`: invalid YAML; invalid UTF-8; two documents; root is a list;
  1 048 577 bytes; an alias-expansion bomb; empty input.
  **Expected behavior**: errors for all but empty input, which gives `nil, nil`; bounded time and memory; no panic.
- **Test name**: `TestValidateRejectsForeignDocuments`
  **Scenario**: table: the engine state report `real-healthy.json`; a labels file; `Sure! Here is the file: done`; prose without
  a colon; a scalar root; a sequence root; a mapping without `version`; `{}`; one unknown top-level key; `Version`; an integer key,
  a null key and a merge key; empty, white-space-only and comment-only input; a duplicate top-level key; two documents;
  truncated YAML; invalid UTF-8; 1 048 577 bytes; an alias-expansion bomb (refused as too complex).
  **Expected behavior**: an error for every row; unknown keys are named by position, the missing key as `version`; no panic.
- **Test name**: `TestDiagnoseRefusesWhatValidateRefuses`
  **Scenario**: every row of `TestValidateRejectsForeignDocuments`.
  **Expected behavior**: at least one diagnostic for each.
- **Test name**: `TestDiagnoseIsBounded`
  **Scenario**: 300 rules with a bad `gate` each.
  **Expected behavior**: 100 diagnostics and one closing note without a line.
- **Test name**: `TestDiagnoseSuggestsOnlyForListedProblems`
  **Scenario**: 3 000 unknown 60-character keys.
  **Expected behavior**: fewer than a million allocations: no edit distance is computed past the hundredth problem.
- **Test name**: `TestReadBoundsTheLibraryDecode`
  **Scenario**: 50 000 identical keys; one mapping with 10 001 keys; a 2 000-key mapping expanded by 200 aliases; an alias
  inside its own anchor; a 128 KB scalar expanded by 43 000 aliases; a 96 KB `!!binary` value expanded by 4 000 aliases;
  1 500 keys of 500 bytes that differ only at the end, expanded by 8 aliases; then 5 000 modules, the `Modules` limit.
  **Expected behavior**: `Waivers` returns the syntax or too-complex error and `Diagnose` one diagnostic, within 5 seconds
  and 256 MB of allocation; the 5 000 modules read. Without the walk the first row does not finish; with node counts
  instead of byte weights the long-text rows pass the bound, and the binary row allocates over 1 GB.
- **Test name**: `TestReadersTakeAFullSizePolicy`
  **Scenario**: 5 000 modules in realistic stanzas (`owner`, `layer`, `deploy_unit`, `volatility`, `subdomain`,
  `paths`, `public`, `internal`), under 1 MB.
  **Expected behavior**: `Modules`, `Validate` and `Diagnose` accept it (about 110 000 nodes).
- **Test name**: `TestLineBreaksOnlyMatterWhereLinesAre`
  **Scenario**: a policy with modules and a waiver whose quoted reason holds a NEL, an LS or a PS.
  **Expected behavior**: `Waivers` returns the waiver and `Validate` accepts the text; `Modules` returns the
  line-break error and `Diagnose` one line-break diagnostic.
- **Test name**: `TestExcessiveAliasingIsTooComplex`
  **Scenario**: the library's "excessive aliasing" error.
  **Expected behavior**: `errTooComplex`, not a syntax error.
- **Test name**: `FuzzDiagnose`
  **Scenario**: fuzz `Diagnose` seeded with the fixtures and truncated YAML; `testdata/fuzz/FuzzDiagnose` keeps an
  end-of-stream input whose reported line was past the text.
  **Expected behavior**: never panics; every line is between 0 and the number of input lines and every message is
  non-empty; when it returns `nil`, `Validate`, `Waivers` and `Modules` all succeed.
- **Test name**: `TestValidateErrorsEchoNothing`
  **Scenario**: the marker `LEAK-7f3a` as an unknown key and its value, as a duplicate key, below a valid key of a document
  without `version`, as a scalar root, in prose, in broken YAML, as an anchor and alias name; the engine state report.
  **Expected behavior**: every error is free of the marker and of every top-level key of the report.
- **Test name**: `FuzzValidate`
  **Scenario**: fuzz `Validate` seeded with the fixtures and the prose line.
  **Expected behavior**: never panics; whatever `Validate` accepts, the common reader accepts.
- **Test name**: `TestErrorsNeverEchoValues`
  **Scenario**: a waiver whose `reason` and malformed `expires` contain the marker `LEAK-7f3a`.
  **Expected behavior**: the error names entry index and key; it does not contain the marker.
- **Test name**: `TestReadBaselineRejects`
  **Scenario**: table: empty; YAML; an array; trailing data; invalid UTF-8; over 1 MiB; schema v1; no schema; no or `null`
  `accepted`; no `state`; a prefixed, empty or uppercase config hash; no profile; an empty or spaced profile version;
  a 31-hex, uppercase or missing fingerprint; a rule ID with a space, with Markdown, empty or of 129 characters; a duplicate fingerprint.
  **Expected behavior**: an error for every row; the unedited minimal baseline decodes.
- **Test name**: `TestReadBaselineErrorsNeverEchoValues`
  **Scenario**: the marker `LEAK-BASELINE-7f3a` as a rule ID, as the schema version, and in truncated JSON.
  **Expected behavior**: every error is free of the marker.
- **Test name**: `FuzzWaivers`
  **Scenario**: fuzz `Waivers` seeded with the fixtures.
  **Expected behavior**: never panics; every returned `Expires` has time 23:59:59 and location UTC.
- **Test name**: `FuzzModules`
  **Scenario**: fuzz `Modules` seeded with the fixtures.
  **Expected behavior**: never panics; every `Line` is between 1 and the number of input lines; names are unique.
- **Test name**: `TestPackageImports`
  **Scenario**: `go list -deps` for the package.
  **Expected behavior**: standard library plus the one YAML library; no internal package; no `net` or `log` package. `os` is
  reachable through `fmt` and the YAML library, so it is banned as a direct import only.

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
  **Scenario**: table: a valid policy without waivers; prose text that is not a mapping; truncated YAML; a policy with an unclosed quote.
  `Waivers` still rejects these, although control-plane callers now use `Validate` or `Diagnose` as their check.
  **Expected behavior**: `nil` error for the first; errors for the rest.
- **Test name**: `TestApprovedByIsNeverAuthority`
  **Scenario**: a waiver with `approved_by` naming a real owner login.
  **Expected behavior**: the value is returned as plain text; the package exposes nothing that treats it as an approval.
  The exported surface is exactly the Public Contract; methods of unexported types are not part of it.
- **Test name**: `TestGraphJumpsToTheModuleLine`
  **Scenario**: a policy shown in the editor; take `Modules`, split the same text by LF, read the line at `Line` for each module.
  **Expected behavior**: each line, trimmed, starts with the module name followed by a colon.
- **Test name**: `TestMalformedExpiryNeedsNoSchedule`
  **Scenario**: the fixture policy with `expires: 2026-13-40`, which the current engine refuses at config load; the
  synthetic report models a run that reports the waiver as `expired_waiver`.
  **Expected behavior**: `Waivers` returns an error; the fixture's report already shows the finding as expired, so no expiry is lost.

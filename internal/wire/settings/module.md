# App Settings File

**Path**: internal/wire/settings/
**Parent**: internal/wire/ (Wire Contracts)
**Submodules**: none (leaf)

## Purpose

`.archfit-app.yaml` is the one App-owned file on the protected ref. It names the architecture owners, the gate
mode, the approved workflow path, and the data mode. This module owns its format: the types, strict parsing with
validation, and canonical rendering. Without it, the gate, onboarding and the UI would each read the file their
own way, and a typo such as `gate: requierd` could silently mean "advisory".

## Functional Responsibilities

- Define `Settings`, `GateMode`, `DataMode` and the file location `Path`.
- `Parse`: strict YAML decoding plus validation of every field; defaults for `gate` and `data_mode`.
- `Render`: canonical YAML for a `Settings` value, used for the onboarding PR and UI edits.
- Nothing else. It does not read repositories, decide who is an owner of what, or check approvals.

## Subdomain Classification

Published language between the App and its customers' repositories. Supporting.
Volatility: moderate. Fields follow product decisions (decisions.md §2, §4); each change is a versioned file format change.

## Encapsulated Knowledge

- The YAML keys and their order: `version`, `owners`, `gate`, `workflow`, `data_mode`.
- The YAML library. It is the single third-party dependency allowed here: the one named in `docs/tech-stack.md`;
  until then `go.yaml.in/yaml/v3` **[default]**, decoded with known-fields checking on. `wire/policy` uses the same library.
  The file is decoded to YAML nodes, not to `Settings`: only nodes show aliases, a key that is present but empty, and
  `1` versus `"1"`. The unknown-key and duplicate-key checks are therefore this module's own walk over the root mapping.
  Library error text quotes the file, so it is never passed on.
- The validation rules below and the canonical output form. All limits are unexported. Every numeric limit is
  **[default]**: 100 owners, 39-character login (GitHub's login limit), 255-character workflow path, 65 536-byte (64 KiB) file.

### Field rules (version 1)

| Key         | Required | Rule                                                                                  |
| ----------- | -------- | ------------------------------------------------------------------------------------- |
| `version`   | yes      | integer, exactly `1`                                                                  |
| `owners`    | yes      | list of 1 to 100 strings; each a login or a team (below); no duplicates ignoring case |
| `gate`      | no       | `advisory` or `required`, exact lowercase; absent means `advisory`                    |
| `workflow`  | yes      | `^\.github/workflows/[A-Za-z0-9._-]+\.ya?ml$`, at most 255 characters                 |
| `data_mode` | no       | `customer` or `managed`, exact lowercase; absent means `customer`                     |

- Owner login: `^[A-Za-z0-9-]+$`, at most 39 characters. Owner team: `^[A-Za-z0-9-]+/[A-Za-z0-9._-]+$` ("org/team-slug").
  An `@` prefix is not accepted; the file is not CODEOWNERS.
- The workflow rule implies: directly under `.github/workflows/`, no subdirectory, no `..`, no leading slash.
- Document rules: at most 65 536 bytes (64 KiB) **[default]**; valid UTF-8; exactly one YAML document; the root is a mapping; unknown
  keys, duplicate keys, anchors and aliases are errors; an empty or comment-only file is an error.
- Owner order is preserved as written. Nothing is sorted, trimmed, or lowercased by `Parse`.

### Canonical form written by Render

One header comment line (`# Archfit App settings. Changes need architecture-owner approval.`) **[default]**, then the
five keys in the order above, `owners` as a block sequence with two-space indentation, `gate` and `data_mode`
always written explicitly, LF line endings, one trailing newline. Output is deterministic: equal input, equal bytes.

## Public Contract

```go
// Path is the App settings file on the protected ref, beside the engine policy.
const Path = ".archfit-app.yaml"

type GateMode string

const (
    GateAdvisory GateMode = "advisory" // publish archfit/state and the comment only
    GateRequired GateMode = "required" // also publish archfit/gate, fail closed
)

type DataMode string

const (
    DataCustomer DataMode = "customer" // source never reaches Archfit
    DataManaged  DataMode = "managed"  // managed discovery and managed LLM allowed after consent
)

// Settings is the App-owned file. The engine policy schema is untouched by it.
type Settings struct {
    Version  int      `yaml:"version"`   // must be 1
    Owners   []string `yaml:"owners"`    // GitHub logins, or "org/team-slug"; at least one
    Gate     GateMode `yaml:"gate"`      // default advisory
    Workflow string   `yaml:"workflow"`  // e.g. ".github/workflows/archfit.yaml"
    DataMode DataMode `yaml:"data_mode"` // default customer
}
```

```go
// Parse strictly decodes and validates a settings file. Unknown keys are errors.
func Parse(raw []byte) (Settings, error)

// Render returns canonical YAML for s. For any s returned by Parse, Parse(Render(s)) == s.
func Render(s Settings) []byte
```

`GateAdvisory` is the gate mode that publishes `archfit/state` and the comment only. `GateRequired` is the gate
mode that also publishes `archfit/gate`. `DataCustomer` is the data mode in which source never reaches Archfit.
`DataManaged` is the data mode that allows managed discovery and managed LLM calls after consent.

Semantics not visible in the signatures:

- `Parse` returns a zero `Settings` with every error. A returned `Settings` always has `Gate` and `DataMode` set
  to one of their constants, never `""`.
- The round-trip law covers every `s` that `Parse` returned. It also holds for a hand-built valid `s` whose `Gate`
  and `DataMode` are set. For a valid `s` with an empty `Gate` or `DataMode`, `Render` writes the default and
  `Parse` returns the value with the default filled in.
- `Render` does not validate and cannot fail. A caller that builds a `Settings` from user input must check it with
  `Parse(Render(s))` before proposing the file.
- A settings file that does not exist is not this module's concern: the caller sees a missing file and treats the
  repository as not onboarded. A file that exists but fails `Parse` is an error for the caller to fail closed on.

## Integrations

This module imports the standard library and one YAML library. It depends on no internal module. The entries
below are its consumers (the consumer depends on this module). Values are from the root coupling table.

| Counterpart                   | Direction                      | Strength | LCA / Rank / Distance | Volatility | Balanced? |
| ----------------------------- | ------------------------------ | -------- | --------------------- | ---------- | --------- |
| `internal/gate/inputs`        | inputs depends on settings     | Contract | root / 2 / 2          | High       | Yes       |
| `internal/gate`               | gate depends on settings       | Contract | root / 2 / 2          | High       | Yes       |
| `internal/gate/feedback`      | feedback depends on settings   | Contract | root / 2 / 2          | High       | Yes       |
| `internal/gate/sweep`         | sweep depends on settings      | Contract | root / 2 / 2          | High       | Yes       |
| `internal/control/onboarding` | onboarding depends on settings | Contract | root / 2 / 2          | High       | Yes       |
| `internal/control/ui`         | ui depends on settings         | Contract | root / 2 / 2          | High       | Yes       |
| `internal/control/assist`     | assist depends on settings     | Contract | root / 2 / 2          | High       | Yes       |

Shared knowledge, per counterpart:

- `internal/gate/inputs` holds the whole Public Contract. It reads `Path` at the protected ref only, calls `Parse`,
  publishes the result inside its own `Trusted` value, and resolves `Settings.Owners`.
- `internal/gate` imports this module for `GateRequired` and `Path`. It reads `Settings.Gate` and `Settings.Workflow`
  through `inputs.Trusted`. It holds nothing of the file format.
- `internal/gate/feedback` and `internal/gate/sweep` import this module for `GateRequired` only, and compare it
  with `Settings.Gate` read through `inputs.Trusted`.
- `internal/gate/verdict` does not import this module and reads no `Settings` field: it sees only the boolean
  `SettingsFound` of `inputs.Trusted`. It is no consumer and has no row.
- `internal/control/onboarding` holds `Settings`, `Path`, `Render` and `Parse`: it renders the file for PR 1 with the
  owners the administrator entered, and reads `Gate` for the onboarding stage.
- `internal/control/ui` holds `Settings`, `Path`, `Parse` and `Render` for the settings editor and the repository page.
- `internal/control/assist` holds `Settings`, `DataMode`, `DataManaged`, `Path` and `Parse`: managed assistance is
  allowed only when the base settings say `data_mode: managed`.
- The file itself is an external surface: customers edit it by pull request. The field rules table is what they see.

No counterpart contract is used by this module, so nothing is restated here.

## Change Vectors

- A new field or mode: extend `Settings`, the rules table and `Render`; consumers that ignore it do not change.
- A breaking format change: `version: 2`, parsed by a new decode path; version 1 files keep parsing.
- Publishing a JSON Schema of the file for editor completion: add a `Schema` function here.
- Swapping the YAML library after `docs/tech-stack.md`.

## Constraints and Invariants

- The engine policy schema stays untouched: App fields live only in this file (decisions.md §2).
- This module never decides where the bytes come from. Its consumers must read the file from the protected ref;
  a head-tree settings file is never trusted. Owners in particular come from the base version only.
- Strict and fail closed: any unknown key, wrong type, wrong case, or unknown mode is an error, never a default.
  Defaults apply only to absent `gate` and `data_mode`.
- `gate` defaults to advisory: the required gate is always an explicit owner action (decisions.md §4).
- No I/O, no clock, no logging. Errors name the key and the rule, never the offending value.
- `Parse` is total: no input panics. Work is bounded by the size limit and the alias ban.
- No other internal import. Transport type only: `Settings` is not a storage type.

## Test Specification

### Unit Tests

- **Test name**: `TestParseFieldRules`
  **Scenario**: table over a valid base file, one change per row: `version` 0, 2, `"1"`, absent; `owners` absent, empty, 101 entries,
  `@alice`, `alice smith`, `org/team/extra`, `Alice` plus `alice`, a 40-character login; `gate: Required`, `gate: neutral`;
  `data_mode: hybrid`; `workflow` absent, `archfit.yaml`, `.github/workflows/sub/a.yaml`, `.github/workflows/../../x.yml`,
  `.github/workflows/a.json`, `/.github/workflows/a.yml`.
  **Expected behavior**: every row is an error naming the key; the base parses.
- **Test name**: `TestParseDefaults`
  **Scenario**: a file without `gate` and `data_mode`.
  **Expected behavior**: `Gate == GateAdvisory`, `DataMode == DataCustomer`.
- **Test name**: `TestOwnerForms`
  **Scenario**: owners `alice`, `a-b-c`, `acme/platform-team`, `acme/team.one_two`.
  **Expected behavior**: all accepted, order preserved, text unchanged.
- **Test name**: `TestRenderIsCanonical`
  **Scenario**: render one value twice and compare with a golden file.
  **Expected behavior**: identical bytes; header comment first; key order fixed; `gate` and `data_mode` present; LF; one trailing newline.
- **Test name**: `TestParseAcceptsHandEditedForms`
  **Scenario**: one hand-edited file: a BOM, the `---` and `...` document markers, double and single quotes, a trailing comment,
  a blank line, a flow list of owners holding `"123"` and `'true'`, keys out of order, and the explicit tags `!!int` and `!!str`.
  **Expected behavior**: parses to the same `Settings` as the plain spelling; the quoted owners stay the strings `123` and `true`.
- **Test name**: `TestParseAcceptsLimits`
  **Scenario**: table of values exactly at a limit: 100 owners; a 39-character login; a 255-character workflow path; a 65 536-byte file.
  **Expected behavior**: all parse.
- **Test name**: `TestRenderNeverFails`
  **Scenario**: table of invalid values: the zero `Settings`; invalid UTF-8 in an owner and the workflow; control characters, an empty
  owner, a negative version, a `gate` of `x: y` and a workflow of `# no`.
  **Expected behavior**: `Render` returns bytes that start with the header comment and never panics; `Parse` of the output is an
  error, so an invalid value never becomes a valid file.

### Integration Contract Tests

- **Test name**: `TestParseRenderRoundTrip`
  **Scenario**: property test over generated valid `Settings` with both modes set; and over every valid fixture file via `Parse`.
  **Expected behavior**: `Parse(Render(s))` equals `s`; `Render(Parse(Render(s)))` equals `Render(s)`.
- **Test name**: `TestRenderFillsDefaults`
  **Scenario**: valid `Settings` with empty `Gate` and `DataMode`.
  **Expected behavior**: the output states `gate: advisory` and `data_mode: customer`; parsing it returns the value with both set.
- **Test name**: `TestPathConstant`
  **Scenario**: read `Path`.
  **Expected behavior**: `.archfit-app.yaml`; it matches the protected file named in the root document.

### Boundary Tests

- **Test name**: `TestParseRejectsNonStrictYAML`
  **Scenario**: table: unknown key `bypass: true`; duplicate `gate` key; two documents; root is a list; empty input; comments only;
  anchors and aliases in `owners`; invalid UTF-8; 65 537 bytes; a tab-indented mapping.
  **Expected behavior**: every row is an error; none panics.
- **Test name**: `TestErrorsNeverEchoValues`
  **Scenario**: invalid files whose bad values contain the marker `LEAK-7f3a`.
  **Expected behavior**: no error string contains the marker.
- **Test name**: `FuzzParse`
  **Scenario**: fuzz `Parse` seeded with the fixtures.
  **Expected behavior**: never panics; every accepted input satisfies all field rules and survives the round trip.
- **Test name**: `TestPackageImports`
  **Scenario**: `go list -deps` and the direct imports of the package.
  **Expected behavior**: standard library plus the one YAML library; no internal package; no `net` or `log` package
  anywhere in the dependencies; no direct import of `os` (the YAML library reaches `os` through `fmt`, so a
  transitive ban cannot hold).

### Behavior Tests

- **Test name**: `TestTypoNeverWeakensTheGate`
  **Scenario**: files with `gate: requierd`, `gates: required`, and `Gate: required`.
  **Expected behavior**: all are errors; none parses as advisory.
- **Test name**: `TestOnboardingFileIsAcceptedByTheGate`
  **Scenario**: render settings with owners `alice` and `acme/architects`, workflow `.github/workflows/archfit.yaml`, then parse as the gate would.
  **Expected behavior**: parses; advisory mode; customer data mode; owners unchanged.
- **Test name**: `TestWorkflowPathCannotLeaveTheWorkflowsFolder`
  **Scenario**: workflow values that point to a script, another folder, or a traversal.
  **Expected behavior**: errors; an approved workflow path is always a file directly under `.github/workflows/`.

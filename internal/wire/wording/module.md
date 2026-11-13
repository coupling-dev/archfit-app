# The Words Archfit Output Never Uses

**Path**: internal/wire/wording/
**Parent**: internal/wire/ (Wire Contracts)
**Submodules**: none (leaf)

## Purpose

Design section 5 gives one word per thing and lists the other words in its "Not used" column. The engine checks its
output against that list; the App must check its own the same way, or the check, the comment and the pages drift to
other words. This module holds the engine's list, a byte copy, and the matcher, so every App test that renders text for
a person calls one function.

## Functional Responsibilities

- Hold `wording.go.txt`, the engine's `internal/testutil/wording.go`, pinned in `SCHEMA_SOURCE` (repository, commit,
  SHA-256).
- `NotUsedIn(text)`: the "Not used" words text uses, matched as the engine matches them.
- Nothing else: no I/O at run time, no decision. It is for tests; no production path calls it.

## Subdomain Classification

Published language between the engine and the App. Generic. Volatility: low; the list changes only with design
section 5.

## Encapsulated Knowledge

- The vendored file is Go source of the engine's `testutil` package. It is embedded and parsed with `go/parser`, never
  compiled, so the App reads the engine's `notUsedWords` and `gateNames` without a copy typed by hand.
- The matching: whole words in any case; a phrase matches across any white space; the two gate names (the check
  `archfit/gate` and the policy key `gate:`) are removed first.
- A vendored file the parser cannot read is a defect of this package: the package panics when it loads, and
  `TestNotUsedMatchesAsTheEngine` runs that load.

## Public Contract

```go
// NotUsedIn returns the words of the "Not used" column of design section 5 that text uses, in list order.
func NotUsedIn(text string) []string
```

## Re-vendor

At a new engine commit: copy `internal/testutil/wording.go` to `wording.go.txt`, set `commit:` and its `sha256` line
in `SCHEMA_SOURCE`, then run the tests.

## Constraints and Invariants

- Standard library only; no import of any internal package.
- `TestVendoredFileMatchesSchemaSource` fails when the file and its `SCHEMA_SOURCE` line disagree.
- `TestWebWordsMatchVendoredEngine` keeps `web/e2e/not-used.json` aligned with the embedded list and gate-name pattern.
- `web/e2e/wording-cases.json` supplies matcher cases to Go and vitest.

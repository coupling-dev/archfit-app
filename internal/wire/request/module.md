# The Request Line and the Request Issue

**Path**: internal/wire/request/
**Parent**: internal/wire/ (Wire Contracts)
**Submodules**: none (leaf)

## Purpose

A worker asks an owner to change the policy with one line: `archfit request -m "<why>"` on a laptop, or a reply
`/archfit request <why>` on the pull request (design 10.3). The App opens an issue for the request; its number is the
request id. This module is that contract: the grammar of the line, the issue the CLI opens before a pull request
exists, the key that makes a second request for the same thing join the first, the issue the App renders, and the
reply to a line that cannot be a request. The App owns it (design 11.7); the engine's `archfit request` (M2 task 6)
vendors the goldens of `testdata/line/` and `testdata/form/` and writes exactly those bytes.

## Functional Responsibilities

- Parse a pull-request comment (`ParseComment`) and the body of an issue the CLI opened (`ParseIssue`).
- Refuse a request line that cannot be used with one reply each: no reason, a reason over 500 characters, a bad,
  past or too distant date (`*Rejection`). Give the replies for "nothing to ask" (`NothingToAsk`) and "no permission"
  (`NoPermission`, M2 plan D21); the caller decides when they apply.
- Give the request key of a result error (`Error.RequestKey`) and of a `can` answer (`Answer.Key`).
- Render the request issue: `Issue.Title`, `Issue.Body`, the label `archfit-request`.
- Nothing else: no I/O, no GitHub, no permission check, no result lookup. Those are the gate's (M2 task 16).

## Subdomain Classification

Published language between the CLI, agents and the App. Supporting. Volatility: moderate; it changes only together
with an engine release that vendors its goldens.

## Encapsulated Knowledge

- The grammar, normative in the package doc (`doc.go`):

  ```text
  comment-line = "/archfit request" SP reason [ SP "--expires" SP date ]
  cli          = "archfit request" [ " -m " shell-quoted-reason ] [ " --expires " date ]
  reason       = 1 to 500 characters of one line, trimmed, whose last word is not "--expires"
                 and whose second-to-last word is not "--expires"
  date         = YYYY-MM-DD, after the App's date, at most 365 days ahead
  issue-form   = comment-line NL NL fence "text" NL record fence NL
  ```

- Reading a comment: the first line that starts with `/archfit request` at its first column, followed by white space
  or the end of the line, is the request; other lines are ignored. Lines end at LF, CRLF or CR, as in Markdown. The
  flag is the pair that ends the line: when the second-to-last word is `--expires`, the last word must be a date, and
  a lone `--expires` at the end is a bad date; earlier `--expires` text stays in the reason **[default]**.
  A line cannot tell a reason ending in `--expires` or `--expires <word>` from the flag, so the grammar excludes
  such a reason and the CLI refuses it before it posts; the App reads that ending as the flag.
  Characters are Unicode code points. The App's date is the caller's `today`, by its own year, month and day.
- Reading an issue: the first line that is not blank must be the request line, so the body the App renders (which
  starts with **Reason**) is never read as one. The record is the first closed `text` block after the line,
  verbatim. Before a pull request exists the CLI opens an issue only from a `can` answer (design 10.3; the manager's
  review of task 2 narrowed M2 plan D2 to this), so the answer is read from that record by the engine's grammar: the head `from -> to: rule: what`, then
  indented `why`, `instead`, `balance`, `next` lines. Anything else gives no answer. The numbers stay "as reported"
  until a pull request confirms them.
- The key, its parts joined by one space (M2 plan D3, as reviewed):

  | Rule | Key |
  | --- | --- |
  | `graph`, `coupling`, `layers`, `cycle`, a custom rule | `<rule> <from> <to>`; for `only`, `to` is the library |
  | `duplication` | the engine's key of the error: the rule, the two modules sorted, the literal's digest |
  | `unowned` | `unowned <path>` |
  | `exception` | none: an expired exception asks nothing |

  A cycle is one error per dependency, keyed in its direction, as exceptions match rule, from and to in order. The
  engine's key is compared, never parsed. A `can` answer judges one dependency, so its key is never a copy, a cycle
  or unowned code. Module names may hold spaces, so two keys can be equal for different modules; the engine's own
  keys have the same limit.
- The issue: title `Request: <rule> <from> -> <to>`, `Request: duplication <a> <-> <b>` (sorted) or
  `Request: unowned <path>`. Body: **Reason** and **Record** in `text` blocks, "as reported" while no pull request
  confirms the numbers, then the linked pull requests and the asked date when there are any. The date has no days
  left: the body is not rewritten every day. Every fence is one backtick longer than the longest run inside it, at
  least three, so nothing in a reason or a reported record (a mention, a link, Markdown) takes effect.
- Replies never repeat what the author wrote, and no line of a reply starts with `/archfit request`, so the App never
  reads its own reply as a request.
- `testdata/wording.go.txt` is the engine's `internal/testutil/wording.go`, a byte copy pinned in `SCHEMA_SOURCE`
  (repository, commit, SHA-256). The tests read the "Not used" words of design section 5 from it.

## Public Contract

```go
const Command = "/archfit request"
const Label = "archfit-request"
const MaxReason = 500
const MaxDaysAhead = 365

type Line struct{ Reason, Expires string }
type Form struct { Line; Record string; Answer *Answer }
type Answer struct { Rule, From, To string; Balance *Balance }
type Balance struct{ Value, Min int }
type Error struct{ Rule, Path, From, To, Key string } // the fields of a result error; Key is the engine's
type Issue struct { Rule, Path, From, To, Reason, Expires, Record string; AsReported bool; Pulls []int }
type Kind string // KindNoReason, KindReasonTooLong, KindDate, KindNothingToAsk, KindNoPermission
type Rejection struct{ Kind Kind }

var ErrNoKey error

// ParseComment and ParseIssue: ok is false when there is no request line; err is a *Rejection when the line
// cannot be used.
func ParseComment(body string, today time.Time) (Line, bool, error)
func ParseIssue(body string, today time.Time) (Form, bool, error)

func (e Error) RequestKey() (string, error)  // ErrNoKey when the error asks nothing
func (a Answer) Key() (string, error)        // ErrNoKey for a rule a can answer never names
func (i Issue) Title() string
func (i Issue) Body() string
func (r *Rejection) Reply() string           // the comment the App posts
func NothingToAsk(records string) *Rejection // records: the check's records, without the status line
func NoPermission() *Rejection
```

`Issue.Record` is the caller's text: the records of the asked errors as `wire/result` prints them, without the status
line, or the reported `can` answer. A child of `wire` imports no other internal package, so the gate maps a
`result.Error` to `Error` and builds the record with `wire/result`.

## Re-vendor

At a new engine commit: copy `internal/testutil/wording.go` to `testdata/wording.go.txt`, set `commit:` and its
`sha256` line in `SCHEMA_SOURCE`, then run the tests.

## Constraints and Invariants

- Standard library only; no I/O; no import of any internal package.
- Parsing is total: no panic on any input. A parsed reason is one trimmed line of 1 to 500 characters; a parsed date is
  after `today` and at most 365 days later.
- `Rejection.Error()` names the kind only.

## Test Specification

- `TestLineGoldens`, `TestFormGoldens`: each canonical line and issue body the CLI writes parses to its `.json`, and is
  the canonical form of it. `TestCommentGoldens`: comments as the App receives them. `TestRejectGoldens`: each
  rejection's reply, from a comment and from an issue. `TestIssueGoldens`: each request's title and body; the body is
  not a request. The goldens hold the design 8.1 `graph` and copy examples, the `can` answer as reported, and a reason
  with quotes, backticks, `@team` and `--expires` in it. The App's date is 2026-11-10.
- `TestEveryGoldenHasItsCase`, `TestRepliesAreNotRequests`.
- `TestGoldensUseNoNotUsedWord`: no golden uses a "Not used" word; `TestNotUsedMatchesAsTheEngine` pins the matcher.
  `TestVendoredFilesMatchSchemaSource` holds the word list to its pin.
- `TestParseComment`, `TestParseCommentIsNotARequest`, `TestParseIssue`, `TestParseIssueIsNotARequest`: every rule of
  the grammar and its edges (500 and 501 characters; today, the next day, 365 and 366 days ahead; leap days; the flag
  at the end, alone or inside the reason). `TestRejectionErrorNamesTheKindOnly`.
- `TestErrorRequestKey`, `TestErrorWithoutRequestKey`, `TestAnswerAndErrorShareTheKey`, `TestAnswerWithoutKey`,
  `TestTitle`, `TestBodyFencesWhatTheWorkerWrote`, `TestBodyOfAReasonThatIsARequestLine`.
- `FuzzParseComment`, `FuzzBody`: the invariants above, and a body always has exactly its two blocks, unchanged.

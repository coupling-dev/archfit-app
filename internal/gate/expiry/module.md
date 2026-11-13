# Gate Expiry

**Path**: internal/gate/expiry/
**Parent**: internal/gate/ (Gate)
**Submodules**: none (leaf)

## Purpose

"A date has teeth" (design 6.4): the App warns the owners before an exception's date, and after it, while the code
still breaks the rule, every pull request fails until an owner extends the exception or the code is fixed (design 7.1,
7.2, 10.8; M2 D19, task 22). The engine judges the date in every run, but a pull request that gets no new run would keep
a result judged before the date. This module holds the App's part: the warning issues, which pull requests a date
reaches, and the record the App shows when a pull request cannot be judged again.

## Functional Responsibilities

- `Approved` reads the policy at the default branch's tip. Only when it is the approved policy (`Repo.ApprovedDigest`, not
  outside the flow) does it act; otherwise it returns `nil`: a repository without an approved policy, or outside the
  flow, is `unknown`, and nothing of it may pass or run again. It writes nothing and returns the tip, dated
  exceptions and whether the policy is advisory.
- `Warn` maintains the warning issues independently of enforcement:
  - From `WarnDays` (7) days before an exception's date, and after it: one issue per exception and date, title
    `Archfit: exception <rule> <from> -> <to> expires on <date>`, label `archfit-expiry`, the owners of the policy
    assigned; recorded in `Repo.ExpiryIssues` under `<exception key> <date>`.
  - A recorded issue whose exception the policy no longer holds with that date is closed with a note: extended (the
    new date) or removed. A later date gets its own issue from 7 days before it.
- `Due` lists the exceptions past their date that a pull record's result did not judge: a result the App accepted as it
  is (`pass`, `fail`, `waiting`) whose `date` is on or before the exception's last day. A result judged after it is the
  engine's own judgement of the date and stands: a pull request whose head no longer breaks the rule passes after its
  next run. An `unknown` result keeps the record that says what to restore first.
- `Result` is the engine's record of each expired exception (`expired_exception` golden, byte for byte): path
  `.archfit.yaml`, `<rule> <from> -> <to> expired on <date>`, `next` waiting for the request the exception's `why`
  names, else "fix the code, or ask an owner to extend the exception"; status `fail`, "(not from this change)".
- `Fail` publishes `Result` on a pull request's head over the run of its pull record (`feedback.Publisher4`), only
  while both the default-branch tip and the pull record remain the ones read: a policy extension or newer upload
  that lands first wins. Both are read again inside the publication lease.

Days are UTC calendar days, as the engine counts them: an exception holds through `expires` and is past its date from
the next day.

Sweep wiring is left for the second pull request of M2 task 22. The contract is `Approved` per repository, `Warn`
as a separate notification step whose failure never blocks enforcement, and a re-run under the guard of design 10.5
(`sweep.Rerun`) for each open pull request with `Due` exceptions; `Fail` only where the guard refuses. This pull
request provides the helpers only: no runtime caller enforces expiry yet.

Not provided: the re-run (`gate/sweep`), the drift issue (`gate/drift`), the engine's judgement of the date.

## Subdomain Classification

Core: the one rule of the verdict that reaches every pull request (design 7.2). Volatility: low; the words of the
record follow the engine's golden.

## Encapsulated Knowledge

- The engine's expiry rule (archfit `internal/assessment/count`): the day after the last day, the words of the record,
  the key `exception <rule> <from> [<to>]`, and the request number read from `why`.
- Issues are written on GitHub first and recorded after (`records.ExpiryOpened`, `ExpiryClosed`); a second issue of a
  concurrent pass is closed with a note naming the first. Assigning owners and notes are notifications: a failure is
  logged with ids and the HTTP status. A permanent GitHub refusal does not stop the pass; a gone issue is forgotten.
  If recording a newly created issue fails, closing that unrecorded issue is attempted before returning the error.
- The words: the App's, clipped policy names and reason in safe code spans, dates with the days left as design 5
  writes them; no digest; no "Not used" word. The issue body and title remain bounded even for long policy strings.
- Dates are parsed from `Exception.Expires`, including for values built as literals. Missing or invalid dates do not
  expire; the policy decoder refuses malformed dated exceptions.

## Public Contract

```go
const Label = "archfit-expiry"
const WarnDays = 7

type Exception struct { Rule, From, To, Why, Expires string }
func (x Exception) Key() string
func (x Exception) Expired(today time.Time) bool
func Dated(pol policy3.File) []Exception
func Due(xs []Exception, rec records.Pull, today time.Time) []Exception
func Result(xs []Exception) result.Result

type GitHub interface { /* File, BranchHead, CreateIssue, CloseIssue, CreateComment, SetAssigneesAndLabels */ }
type Approved struct { Tip string; Exceptions []Exception; Advisory bool /* private notification state */ }
type Failure struct { Repo api.Repo; Pull int; Tip string; Record records.Pull; Expired []Exception; Advisory bool }
func New(gh GitHub, s store.Store) *Service
func (s *Service) Approved(ctx context.Context, r api.Repo, tip string) (*Approved, error)
func (s *Service) Warn(ctx context.Context, r api.Repo, a *Approved, now time.Time) error
func (s *Service) Fail(ctx context.Context, p feedback.Publisher4, f Failure) error
```

## Integrations

- **gate/sweep** (second pull request of task 22, not wired here): `Approved`, `Warn`, `Due`, `Fail`.
- **gate/markdown**: bounded, literal issue text.
- **gate/records**: the repository record (`ApprovedDigest`, `OutsideFlow`, `ExpiryIssues`), the pull record (`Date`,
  run identity, status). **gate/feedback**: `Publisher4`. **wire/policy3**: `Decode`, `Digest`. **wire/result**:
  `Result`, `Record`.
- Contract strength; LCA gate / rank 1 / distance 1; balanced.

## Constraints and Invariants

- Nothing is written for a repository whose tip policy is not the approved one.
- At most one recorded issue per exception and date.
- **Stated limit**: a crash between opening an issue and recording it, or a failed cleanup of an unrecorded or
  duplicate issue, can leave an unmanaged issue.

## Test Specification

Over a fake GitHub, the memory Store and a fixed clock:
`TestTheIssueOpensSevenDaysBeforeTheDate` (8 days before none, 7 days one, later passes none),
`TestAnExceptionAlreadyPastItsDateGetsItsIssue`, `TestAnExtendedExceptionClosesItsIssue` (the new date gets its own
issue), `TestARemovedExceptionClosesItsIssue`, `TestCheckActsOnlyOnTheApprovedPolicy` (no record, no approved policy,
outside the flow, an unapproved tip, no policy), `TestDue` (before, on and after the date; judged on the last day or
after it; waiting; unknown; no date), `TestResultIsTheEngineRecord` (golden), `TestResultOfAnExceptionWithoutARequest`,
`TestASecondPassClosesItsOwnIssue`, `TestAFailedIssueWriteIsReturned` (a failed close is tried again),
`TestFailPublishesOverThePullRecordsRun` (a newer record wins), `TestTextsUseNoNotUsedWord`. `review_test.go` covers
literal and invalid dates, notification failures independent of enforcement, gone issues, cleanup after failed
records, policy-tip freshness inside the lease, and bounded literal policy text.

# Gate Drift

**Path**: internal/gate/drift/
**Parent**: internal/gate/ (Gate)
**Submodules**: none (leaf)

## Purpose

Drift is an error on the default branch that no exception covers and no open change added (design 7.1). It never
fails a pull request: it is the owners' work, and "drift is never accepted by silence" (design 7.2). The engine reports
it in the `repository` block of a push run (M2 D18, engine task 5). This module keeps one issue per repository that
lists it, assigned to the owners (design 11.6), edited after each push and closed when none is left (M2 task 22).
Without it the drift of a push run would be uploaded and never shown.

## Functional Responsibilities

`Pushed` applies the block of one push run that the gate accepted as it is, at the default branch's tip:

| Block | Recorded issue | Action |
| --- | --- | --- |
| none (`status` unknown, a preview, a head that is not the tip, a later push overtook it) | any | nothing: no block means no state, never clean |
| drift | none | open the issue: title `Archfit: drift on <branch>`, label `archfit-drift`, the owners of the policy at the head assigned; record it |
| drift | recorded | edit the body in place and reopen it (an owner who closes it does not accept the drift) |
| no drift, unread places | recorded | edit the body: no error in what was read, the places not read and how to read them; never closed |
| no drift, every place read | recorded | close it with a note naming the commit; forget it |
| no drift | none | nothing: unread places alone open no issue |
| any | describes this head already | nothing (the Action's retry, a second delivery) |

The state `expired` follows the same rows: the drift list is what counts here, and an expired exception is the work of
`gate/expiry`.

Not provided: the expiry of exceptions (`gate/expiry`), the check of a pull request, the push judgement of the policy
(`gate/approvals`).

## Subdomain Classification

Supporting. A notification of the owners, with the record the engine wrote. Volatility: low; it follows the engine's
`repository` block.

## Encapsulated Knowledge

- The issue is written on GitHub first and recorded after (`records.DriftOpened`, `DriftUpdated`, `DriftClosed`). A
  failed write or record is returned, so the upload answers 503 and the Action's retry finishes it. A second delivery
  that opened its own issue finds the recorded one and closes its own with a note.
- `Repo.DriftHead` names the commit the body describes, so the same push again writes nothing.
- The body shows the drift errors as the check prints them (`result.Record`, no status line), at most 30,000 bytes of
  records, and at most 50 unread places; the rest is counted. Words: the App's, the branch, commit SHAs and the engine's
  records; no digest (design 5); no "Not used" word.
- Assigning owners and the notes are notifications: a failure is logged with ids and the HTTP status, never a name.
  Owners GitHub cannot assign leave the label alone, as for requests.

## Public Contract

```go
const Label = "archfit-drift"
const None, Opened, Updated, Closed = "none", "opened", "updated", "closed"

type GitHub interface { /* File, CreateIssue, UpdateIssueBody, CloseIssue, ReopenIssue, CreateComment,
    SetAssigneesAndLabels */ }
type Run struct { Repo api.Repo; Head string; State *result.Repository }
func New(gh GitHub, s store.Store) *Service
func (s *Service) Pushed(ctx context.Context, run Run) (string, error)
```

## Integrations

- **gate** (parent): `drift.go` calls `Pushed` from the envelope 2 upload (`upload4.go`) after the check is published,
  only for a `push` run (the event GitHub states) of the default branch, with no App record, at the tip the decision
  read.
- **gate/records**: the repository record (`DriftIssue`, `DriftHead`) and its helpers.
- **wire/result**: `Repository`, `Record`. **wire/policy3**: `Decode` (owners). **github/api**: the roles of `GitHub`.
- Contract strength; LCA gate / rank 1 / distance 1; balanced.

## Constraints and Invariants

- At most one recorded drift issue per repository; a run without a block never closes it.
- No log line holds a repository name, a login or a record.
- **Stated limit**: a crash between opening the issue and recording it opens a second one on the retry (as for
  requests and "policy changed outside the flow").

## Test Specification

Over a fake GitHub and the memory Store, with the engine's vendored `repository_*` goldens as input:
`TestDriftOpensOneIssueForTheOwners`, `TestDriftIsEditedInPlace` (also reopened), `TestTheSameHeadAgainChangesNothing`,
`TestDriftClosesWhenNoErrorIsLeftAndEveryPlaceWasRead` (clean and expired), `TestACleanStateWithUnreadPlacesKeepsTheIssueOpen`,
`TestNoBlockMeansNoState`, `TestNoDriftOpensNothing`, `TestASecondDeliveryClosesItsOwnIssue`,
`TestAFailedWriteIsReturnedAndRecordsNothing` (a retried close finishes), `TestOwnersGitHubCannotAssignKeepTheLabel`,
`TestTheBodyStaysWithinItsBounds`, `TestTextsUseNoNotUsedWord`.

In `internal/gate` (`drift_test.go`), through the upload path: `TestAPushRunWithDriftOpensTheDriftIssue` (the retry
writes nothing), `TestOnlyAnAcceptedPushRunAtTheTipCountsForDrift` (a late older push, App records, a pull request, a
merge group), `TestAFailedDriftIssueFailsTheUpload` (503, then the retry opens it).

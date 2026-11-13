# Gate Approvals

**Path**: internal/gate/approvals/
**Parent**: internal/gate/ (Gate)
**Submodules**: none (leaf)

## Purpose

An approval is valid once, for one step, and an older approved policy cannot be used again (design 9.2, 19). The policy
reaches the default branch by a push, so each push that changes `.archfit.yaml` must be judged: the merge of the App's
proposal with the policy its approvals wrote uses them, once; any other change is "policy changed outside the flow"
(design 7.1), and every check is `unknown` until an owner confirms or reverts it (M2 task 23). This module judges a
default-branch push (M2 task 20). Without it an approval stays open for ever, its request keeps covering its key, and a
direct push to the policy is the new rule with no owner's word.

## Functional Responsibilities

- `Pushed` judges one push to the default branch, by its own change, not against a stored digest, so deliveries late or
  out of order are judged the same way:
  1. Known changed paths without `.archfit.yaml`: `none`, nothing read.
  2. A push already judged (`records.PolicyPushKey` of its two commits, before and after): only what the judgement
     left is finished, the issue of the state when none is recorded, and the hand-off of a consume. Nothing is judged
     twice. A push that moves the branch back to a commit judged before (a force-push to an older merge) is another
     push, judged again: an older approved policy is outside the flow.
  3. The policy at the pushed commit and at the commit before (`Before`; a missing file has no digest). The same digest:
     `none`. No commit before (a created branch, a payload without one): judged.
  4. An open proposal (`records.ProposalIndexKey`) whose pull request merged in this push (`api.Pull.Merged`; its
     `MergeCommitSHA` is the pushed commit, or, when a merge queue landed several pull requests at once, on the
     history of the pushed commit and not of the commit before: `Compare`) and whose expected digest is the pushed
     policy's: `records.Consume` with the merge commit, `consumed`. The approved digest moves only when the policy at
     the default branch's tip is that digest now and the merge commit is not behind the repository's approved commit
     (with no order between them, after a history rewrite, the tip decides) (`records.Merge.AtTip`, judged over
     `Merge.Over`; a consume that lands in between is `records.ErrApprovedMoved`,
     and both are read again, at most 3 rounds). So a consume handled after a later policy push, or racing it, never
     puts an older digest back, and never approves a tip that holds a change no approval made. The outcome
     lists the requests of its approvals and the pull requests waiting on them, for the re-check after an answer
     (task 21).
  5. Such a proposal with another digest (an owner merged a head from before the last answer joined, or the default
     branch's policy changed under it) can never be consumed: a merge commit is one commit. Its approvals are voided
     (their requests open again: note, issue reopened), then the proposal. An answer that joined it meanwhile
     (`records.ErrApprovalsLeft`) is read and voided too, at most 3 rounds. Without this its requests would cover their
     keys for ever.
  6. No approved digest and not outside the flow, or a push made before the first approval (the commit before it
     strictly behind `Repo.GuardedSince` and the pushed commit on its history, or, for the push that created the
     branch, the pushed commit strictly behind it; a push that moves the branch back from a later commit is not one): `unguarded`, recorded (`records.Unguarded`), so a delivery of it again after the first
     consume is not outside the flow. Change control starts with the first approval (task 23) or
     the first consume at the tip; the envelope 2 upload says `first_approval` until then (manager, 2026-11-12).
  7. Otherwise `outside`: `records.Outside` sets the state and records the push in one swap, then one issue is opened
     to the owners of the policy before the push (the one owners stood behind; the pushed one when there is none), and
     its number recorded (`records.OutsideIssue`). Already outside the flow with an issue: a note names the new commit.
- Fail closed: the state is recorded before the issue is opened, so every upload is `unknown` at once. A failed read or
  write is returned and the delivery retried; the retry finds the judgement and finishes the issue. Until a push is
  judged, the upload compares the tip's policy with the approved digest (`policy_unapproved`), so a late judgement
  never lets a check pass.

- A proposal that admits a workflow file (a first approval, `Proposal.WorkflowDigest`) is consumed only when its merge
  commit also holds that file: one first approval never admits another workflow. A merge with another file voids it
  as a merge of another policy does; with no approved policy the push is then `unguarded`.
- A proposal approved in a state that ended is voided as such a merge is, and the push judged as any other
  (`stillApplies`, and `records.Consume` refuses it too): a `first` proposal once the repository has an approved
  policy or is outside the flow (another onboarding pull request started change control), and one holding a revert
  that no longer holds (`records.Revert.Holds`: the changed policy and the approved one it was made against), as after
  a confirm, or a later change, even when the same change is pushed again.
- Before a consume (also of a workflow proposal) and before a void, the proposal's head is retired (`proposals.Retire`,
  M2 review H2): its success is taken back, so another pull request at that commit merges on nothing. It comes first
  and a failure is returned, so the delivery is retried with nothing changed, and `Retire` repeats safely.
- An open policy proposal whose revert no longer holds (a confirm, or a later change outside the flow, ended the state
  it was made in) has its head retired too (`retireStaleReverts`, on a confirm and on every outside judgement, also on
  a delivery again): its success would restore the old policy over the new one. The proposal stays open; the next
  answer voids the revert (`gate/proposals`).
- **Stated limit**: a pull request retargeted to the default branch sends no event; the head of an ended proposal is
  retired again only when the next pull request opens at that commit (a sweep rule for it is task 22's).
- After a consume whose proposal holds a revert (M2 task 23, `gate/proposals`) ended the state, the issue of the state
  gets a note and is closed (a notification: a failure is logged, a delivery again does not repeat it).
- `WorkflowPushed` judges a default-branch push for the workflow approval it uses (M2 task 23): the open `workflow`
  proposal whose pull request merged in the push is consumed (`records.ConsumeWorkflow`) when the merge commit holds
  the approved workflow file, and voided otherwise. A push that leaves `.github/workflows/archfit.yaml` alone is
  `none`, and so is the same push again. `Pushed` passes over workflow proposals, `WorkflowPushed` over the others. A
  workflow file changed with no approval is not a state of its own: each run is compared with the default branch's
  file (`gate`, `upload4.go`).
- Owner actions outside requests (M2 task 23): `Bind` reads what an action binds to now (`Change`: the pull request's
  head, its policy digest and workflow digest; or the default branch's tip and its policy digest), and `Act` binds it
  again, refuses a change that is not the one the owner's step-up was bound to (`ErrMoved`, before the step-up is
  used), checks who acts, redeems the step-up, records the approval (`records.First`, `Workflow`, `ApproveCurrent`,
  `Confirm`) and records it on GitHub, naming the owner and the passkey (design 19):
  - **first** of an onboarding pull request: a repository administrator who is also an owner named in the policy at
    the PR's immutable head; the pull request is open, of this repository,
    into the default branch, which has no policy of version 3; its head has one and has the workflow file. Recorded on
    the pull request.
  - **first** of the policy already on the default branch (an already-converted repository with no record): a
    repository administrator who is also an owner the policy names (manager, 2026-11-13; the passkey registration rule
    is unchanged). Takes effect at once; recorded on a new issue assigned to the owners.
  - **workflow**: an owner named in the approved policy; the repository has an approved policy and is not outside the
    flow; the head changes the workflow file. Recorded on the pull request.
  - **confirm**: an owner named in the approved policy; the tip's policy is the digest the state recorded (a later
    change waits for its own judgement). Ends the state; recorded on its issue, which closes.
  Owners are read from the policy at `Repo.ApprovedCommit`, never from the default branch: a policy changed outside the
  flow could name anyone.

Not provided: the re-check of the waiting pull requests (`sweep.AfterAnswer`, called by the gate), the revert
(written by `gate/proposals` as an answer is), the re-run that gives an approved head its verdict (`gate`, `owner.go`),
traversing installations and the guarded re-check handoff (gate/sweep4.go).

## Narrow sweep recovery

`Reconcile(ctx, repo, tip)` does not change `Pushed`'s event contract and never voids a proposal. A pending
`Repo.SweepRecheck` is returned as a consumed outcome until the gate finishes its handoff. Otherwise, with an approved
policy whose tip differs, it proves a complete linear history from `ApprovedCommit` to `tip`, at most 100 commits,
using parent identities rather than list/index order. It counts exact policy-byte digest transitions: exactly one
policy-changing commit must leave the tip's digest.

An indexed policy proposal must uniquely match that commit's GitHub-recorded merge and the tip digest. Only then is
it consumed with the handoff marker in the same swap. No policy proposals means a proven direct push is recorded
outside the flow. An unmatched index entry, multiple changes, missing/cut/non-linear history or an unreadable compare
is `JudgedAmbiguous`: no consume, void or approval. The gate logs the range and records it on affected pull records.
A rate limit or cancelled context still stops the pass.

Follow-up 3 is partially satisfied: ambiguous lost pushes remain `policy_unapproved`, not repaired by guessing.
Owner action through the approval flow, or a new delivered push followed by confirm, is needed. An intermediate
policy blob never authorizes the tip.

`Recovered(ctx, push)` is the gate's narrow dispatch before `Pushed`: a delayed real webhook whose complete linear
range includes an immutable recovered merge and leaves that merge's policy digest is recorded under its actual
before/after key and returns the already-consumed outcome. The proof is written in the consume swap under
`v4/recovered-push/<repo>/<merge>`, so code-only commits before or after the merge do not break redelivery identity.
It changes no approval or repository authority. Unrecognized deliveries use `Pushed` with its unchanged contract.
Sweep outside-flow writes use `records.OutsideIf`, which checks the approved commit/digest and outside state in the
transaction: a concurrent owner confirmation wins rather than being undone by a stale sweep.

## Subdomain Classification

Core: the one-use step of design 9.2 and the state of design 7.1, and the owner actions that start change control or
end the state (M2 task 23). Volatility: high during milestone 2.

## Encapsulated Knowledge

- The judgements `none`, `unguarded`, `consumed`, `outside`, and the order of the rules above.
- "Changed" is a digest comparison of the policy before and after the push (`policy3.Digest`, exact bytes): a comment
  changes it, a push of other files or one that puts the bytes back does not.
- Why a push is judged by its own change and not against `Repo.ApprovedDigest`: two merges handled out of order, or a
  late delivery after a later consume, would otherwise read as a change outside the flow.
- The words of the issue and the notes (`texts.go`): only the App's words, the branch in a code span, issue numbers and
  commit SHAs; no digest (design 5), no worker or owner text.

## Public Contract

```go
const PolicyPath = ".archfit.yaml"
const WorkflowPath = ".github/workflows/archfit.yaml"
const JudgedNone, JudgedUnguarded, JudgedConsumed, JudgedOutside = "none", "unguarded", "consumed", "outside"

type GitHub interface { /* File, BranchHead, Pull, Compare, CreateIssue, CreateComment, CloseIssue, ReopenIssue,
    SetAssigneesAndLabels */ }
type Service struct{ /* unexported */ }
func New(gh GitHub, s store.Store) *Service

type Push struct { Repo api.Repo; Before, After string; ChangedPaths []string }
type Outcome struct {
    Judged   string
    Proposal string // consumed
    Requests []int  // consumed: the requests of its approvals
    Waiting  []int  // consumed: the pull requests waiting on them, sorted, once each
    Issue    int    // outside: the issue of the state
    Voided   string // a proposal whose pull request merged another policy
}
func (s *Service) Pushed(ctx context.Context, p Push) (Outcome, error)
func (s *Service) WorkflowPushed(ctx context.Context, p Push) (Outcome, error)

type Owner struct { ID int64; Login string; Admin bool }
type Action struct { Kind string; Pull int } // records.KindFirst, KindWorkflow or KindConfirm; Pull 0 without a pull request
type Change struct { Kind string; Pull int; HeadSHA, Commit, Policy, Workflow string }
func (c Change) Digest() string // what the owner's step-up is bound to
type Input struct {
    Action Action
    Owner  Owner
    Digest string // the digest the step-up was bound to
    Redeem func(ctx context.Context, digest string) (passkeyID string, err error)
}
type Done struct { Approval string; Change Change; Passkey string; Issue int }
func (s *Service) Bind(ctx context.Context, r api.Repo, a Action) (Change, error)
func (s *Service) Act(ctx context.Context, r api.Repo, in Input) (Done, error)

var ErrNotAdmin, ErrNotOwner, ErrNotDue, ErrNotApprovable, ErrMoved, ErrNeedsPasskey error
```

## Integrations

- **gate** (parent): builds the `Service` with the gate's GitHub client and Store; `events.go` calls `Pushed` through
  the façade file `approvals.go` on every `DefaultBranchPush`, before the repository sweep, and hands a consume's
  waiting pull requests to `sweep.AfterAnswer` (the re-check after an answer, M2 task 21); it then calls
  `WorkflowPushed` through `owner.go`. `owner.go` calls `Bind` and `Act`. `upload4.go` reads the state it sets
  (`Repo.OutsideFlow`, `ApprovedDigest`) and the open first and workflow proposals.
- **gate/records**: `Consume` (`Merge`), `Outside`, `Unguarded`, `OutsideIssue`, `Void`, `VoidProposal`, `First`,
  `Workflow`, `ConsumeWorkflow`, `ApproveCurrent`, `Confirm`, `FileDigest`, `Push`, the keys and values.
  **gate/requests**: `PasskeyShown`.
- **wire/policy3**: `Digest`, `Decode` (owners). **github/api**: the roles of `GitHub`.
- Contract strength; LCA gate / rank 1 / distance 1; balanced.

## Change Vectors

- Milestone 3 (owner action page): `Bind` is what the page shows before the step-up.

## Constraints and Invariants

- A push is judged once (`records.PolicyPush`, under its commits before and after); a proposal is consumed by exactly
  one merge commit.
- The repository's state is set before its issue exists; at most one issue per state (a second one opened by a
  concurrent delivery is closed with a note naming the first).
- A consume while outside the flow keeps the state and the approved digest (`records.Consume`), unless its proposal
  holds a revert.
- An owner action is bound to the digest of what the owner saw; it is refused before the step-up is used when that
  moved. Owners of a workflow approval, a confirm and a revert are the approved policy's.
- **Stated limit**: a workflow file changed on the default branch with no approval (a direct push, a bypass) becomes
  the file every run is compared with. A pull request cannot do it under the required check; the ruleset makes the
  rest an administrator bypass (design 10.8).
- **Stated limit**: the issue of a state a revert ended is closed after the consume; a crash in between leaves it open.
- No log line holds a repository name, a login or a policy; notification failures log the step and the HTTP status.
- **Stated limit**: a crash between opening the issue and recording it opens a second issue on the retry (as for
  requests).
- **Stated limit**: when an owner merges a pinned head on GitHub while another answer joins the proposal, the push's
  digest is not the expected one: the push is outside the flow and the proposal void (fail closed).
- **Stated limit**: two consumes handled out of order use both proposals' approvals; the approved digest is the newer
  one. A first consume after an unjudged direct push approves nothing: the repository stays without an approved policy
  (`first_approval`) until the first approval.
- **Stated limit**: a direct push that lands while the first consume of a repository is in flight (after it read the
  tip, before its swap) is judged unguarded, and the consume approves: the tip then holds a policy no approval made,
  with no state and no issue. Every upload is `unknown` (`policy_unapproved`, fail closed) until the sweep compares the
  tip with the approved digest through the narrow recovery above.
- **Stated limit**: a lost policy push is repaired only when the narrow proof above holds. Until then uploads stay
  `unknown` (`policy_unapproved`), and an unanswered lost-merge handoff leaves its requests covering their keys.
  Ambiguous ranges intentionally remain so; no valid approval is voided to force progress.

## Test Specification

Over a fake GitHub (the policy per commit, pull requests, issues) and the memory Store; records are written as
`gate/proposals` writes them (`records.Answer`, `records.Join`):

- `TestTwoJoinedAnswersAreConsumedByOnePush`: the proposal merged, the approved digest, both requests out of the
  index, the hand-off lists both requests and their waiting pull requests, no issue.
- `TestARewrittenProposalIsConsumed`: a re-pinned head and digest (a moved target) is consumed by its merge.
- `TestTheSamePushTwice`: the same outcome again, also after a later consume; an outside push twice: one issue, no
  note.
- `TestADirectPushIsOutsideTheFlow`: the state, one issue assigned to the owners of the policy before (not the pushed
  ones), the approved digest kept; a second direct push notes the issue.
- `TestAnOlderApprovedPolicyPushedAgainIsOutsideTheFlow`, `TestAnApprovalIsUsedOnce` (the same policy by another
  commit is outside the flow; the consumed request takes no answer, its approval joins no proposal).
- `TestAMergeOfAnotherPolicyVoidsItsProposal`: outside the flow, the proposal void, its requests open again with a
  note.
- `TestAPushIsJudgedOnlyWhenItChangesThePolicy`: other files are not read; unknown paths with the same policy; no
  commit before; the policy removed.
- `TestARepositoryWithoutAnApprovedPolicyIsNotJudged`, `TestAConsumeOutsideTheFlowKeepsTheState`,
  `TestAFailedIssueIsOpenedOnTheRetry`, `TestTextsUseNoNotUsedWord`.
- `TestAConsumeHandledLateKeepsTheNewerApproval`, `TestAFirstConsumeAfterAnUnjudgedDirectPushApprovesNothing` (the
  direct push again is still unguarded), `TestAPushBackToAConsumedCommitIsOutsideTheFlow`,
  `TestAMergeQueueBatchIsConsumed` (the merge commit inside a batch; a batch whose last policy no approval made is
  outside the flow and voids the proposal), `TestAnAnswerThatJoinsDuringTheVoidIsVoidedToo`,
  `TestALatePushFromBeforeTheFirstApprovalIsNotOutsideTheFlow` (a push back to the first approval's commit is judged),
  `TestAConsumeThatLosesARaceKeepsTheNewerApproval`, `TestARewrittenHistoryDoesNotStopLaterApprovals`,
  `TestALateBranchCreationFromBeforeTheFirstApprovalIsNotOutsideTheFlow`.

Owner actions and workflow pushes (`owner_test.go`, the fake also holds the workflow file per commit):
`TestTheFirstApprovalOfAnOnboardingPullRequestIsConsumedByItsMerge` (a non-administrator refused; the record on the pull
request), `TestOneFirstApprovalNeverAdmitsAnotherWorkflow`, `TestAFirstApprovalIsBoundToWhatTheAdministratorSaw` (a push
after `Bind`: `ErrMoved` before the step-up), `TestAFirstApprovalRefuses` (approved repository, fork, head without a
policy of version 3 or without the workflow, a branch with a policy, no pull request),
`TestTheFirstApprovalOfAnAlreadyConvertedRepository` (an administrator the policy does not name refused; the issue
assigned to the owners; a later direct push is outside the flow), `TestAWorkflowApprovalIsConsumedByItsMergeOnce`,
`TestAWorkflowMergedWithAnotherFileVoidsItsApproval`, `TestAWorkflowApprovalRefuses`,
`TestOnlyAnOwnerOfTheApprovedPolicyConfirms` (the owner a direct push named is refused),
`TestAConfirmWaitsForTheJudgementOfALaterChange`, `TestARevertConsumeEndsTheStateAndClosesItsIssue`,
`TestAFirstApprovalMergedAfterChangeControlStartedIsOutsideTheFlow`, `TestARevertMergedAfterAConfirmIsOutsideTheFlow`,
`TestAUsedOrVoidProposalTakesBackItsHeadsVerdict`.

In `internal/gate` (`approvals_test.go`), over the gate's fake GitHub: `TestADirectPushPutsEveryUploadOutsideTheFlow`
(one issue, the upload `unknown` with `outside_flow` naming it), `TestAnAnsweredRequestCoversItsKeyUntilItsProposalIsConsumed`,
`TestAPolicyNoOwnerApprovedGivesNoVerdict`, `TestACodePullRequestThatEditsThePolicyIsUnknown`,
`TestAnUploadBeforeItsPushIsJudgedGivesNoVerdict`.

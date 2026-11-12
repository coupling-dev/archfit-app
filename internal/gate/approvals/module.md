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
  2. A push already judged (`records.PolicyPushKey` of its commit): only what the judgement left is finished, the
     issue of the state when none is recorded, and the hand-off of a consume. Nothing is judged twice.
  3. The policy at the pushed commit and at the commit before (`Before`; a missing file has no digest). The same digest:
     `none`. No commit before (a created branch, a payload without one): judged.
  4. An open proposal (`records.ProposalIndexKey`) whose pull request is merged with the pushed commit as its merge
     commit (`api.Pull.Merged`, `MergeCommitSHA`) and whose expected digest is the pushed policy's: `records.Consume`,
     `consumed`. The outcome lists the requests of its approvals and the pull requests waiting on them, for the re-check
     after an answer (task 21).
  5. Such a proposal with another digest (an owner merged a head from before the last answer joined, or the default
     branch's policy changed under it) can never be consumed: a merge commit is one commit. Its approvals are voided
     (their requests open again: note, issue reopened), then the proposal. Without this its requests would cover their
     keys for ever.
  6. No approved digest and not outside the flow: `unguarded`. Change control starts with the first approval (task 23)
     or the first consume; the envelope 2 upload says `first_approval` until then (manager, 2026-11-12).
  7. Otherwise `outside`: `records.Outside` sets the state and records the push in one swap, then one issue is opened
     to the owners of the policy before the push (the one owners stood behind; the pushed one when there is none), and
     its number recorded (`records.OutsideIssue`). Already outside the flow with an issue: a note names the new commit.
- Fail closed: the state is recorded before the issue is opened, so every upload is `unknown` at once. A failed read or
  write is returned and the delivery retried; the retry finds the judgement and finishes the issue.

Not provided: the re-check of the waiting pull requests (task 21; the gate logs the hand-off), confirm and revert (task
23), the sweep's comparison of the tip with the approved digest when a push event is lost (task 22).

## Subdomain Classification

Core: the one-use step of design 9.2 and the state of design 7.1. Volatility: high during milestone 2; task 23 adds the
end of the state and the first approval's proposal (the onboarding pull request), which this module consumes as it is.

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
const JudgedNone, JudgedUnguarded, JudgedConsumed, JudgedOutside = "none", "unguarded", "consumed", "outside"

type GitHub interface { /* File, Pull, CreateIssue, CreateComment, CloseIssue, ReopenIssue, SetAssigneesAndLabels */ }
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
```

## Integrations

- **gate** (parent): builds the `Service` with the gate's GitHub client and Store; `events.go` calls it through the
  façade file `approvals.go` on every `DefaultBranchPush`, before the repository sweep, and logs the hand-off of a
  consume (`gate recheck handed`). `upload4.go` reads the state it sets (`Repo.OutsideFlow`, `ApprovedDigest`).
- **gate/records**: `Consume`, `Outside`, `OutsideIssue`, `Void`, `VoidProposal`, the keys and values.
- **wire/policy3**: `Digest`, `Decode` (owners). **github/api**: the roles of `GitHub`.
- Contract strength; LCA gate / rank 1 / distance 1; balanced.

## Change Vectors

- Task 23: confirm and revert end the state; the first approval and the workflow approval are proposals on their own
  pull requests, consumed here unchanged.
- Task 21 replaces the logged hand-off with the re-check.

## Constraints and Invariants

- A push is judged once (`records.PolicyPush`); a proposal is consumed by exactly one commit.
- The repository's state is set before its issue exists; at most one issue per state (a second one opened by a
  concurrent delivery is closed with a note naming the first).
- A consume while outside the flow keeps the state and the approved digest (`records.Consume`).
- No log line holds a repository name, a login or a policy; notification failures log the step and the HTTP status.
- **Stated limit**: a crash between opening the issue and recording it opens a second issue on the retry (as for
  requests).
- **Stated limit**: when an owner merges a pinned head on GitHub while another answer joins the proposal, the push's
  digest is not the expected one: the push is outside the flow and the proposal void (fail closed).
- **Stated limit**: a policy push whose event is lost is not judged until the sweep compares the tip with the approved
  digest (task 22).

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

In `internal/gate` (`approvals_test.go`), over the gate's fake GitHub: `TestADirectPushPutsEveryUploadOutsideTheFlow`
(one issue, the upload `unknown` with `outside_flow` naming it), `TestAnAnsweredRequestCoversItsKeyUntilItsProposalIsConsumed`,
`TestAPolicyNoOwnerApprovedGivesNoVerdict`, `TestACodePullRequestThatEditsThePolicyIsUnknown`.

# Gate Proposals

**Path**: internal/gate/proposals/
**Parent**: internal/gate/ (Gate)
**Submodules**: none (leaf)

## Purpose

An owner answers a request in the App, and the answer must reach the policy on the default branch through a pull
request the App writes, checks and merges (design 9.2, 10.4; M2 task 19). This module carries an answer from the
approval record to the merge: it records the approval, puts its change on the App's branch `archfit/policy` with the
other open answers, sets `archfit/gate` on that pull request when M2 D13 holds, merges it ("Approve and merge"), and
notes the answer on the request issue. Without it an answer is a record nobody acts on, or a branch anyone could merge.

## Functional Responsibilities

- `Digest`: the change digest of an answer, which the owner's passkey step-up is bound to (`control/passkey` Purpose).
- `Answer`, under a per-repository lease (`records.ProposalLeaseKey`, 60 s; a second answer waits at most 10 s, then
  `ErrBusy`). One answer ends within 50 s, inside the App's 55 s UI deadline: 10 s for the lease, 15 s on the caller's
  context up to the record, then 25 s on a context the caller cannot cancel, so a closed tab or a deadline never leaves
  a recorded answer's check, merge or issue half done:
  1. Read the default branch's tip and its policy (`requests.ErrNoPolicy` without one). The user's login must be in
     `owners`, case-insensitive (`ErrNotOwner`); any owner may answer, the one who filed it too. The request must be
     open (`requests.ErrNoRequest`, `requests.ErrClosedRequest`); `requests.ChangeOf` checks the answer.
  2. Find the open proposal: the proposal index entry whose pull request is open. A proposal whose pull request merged
     waits for the push that consumes it (task 20). One whose pull request was closed without a merge is void: its
     approvals first (their requests open again), the proposal and its index entry last, so a cleanup that stops half
     way is finished by the next answer, which still finds the proposal.
  3. Write, in order, every open approval's change of that proposal and then the new one into the tip's policy
     (`requests.Edit`, comment `request #N`). An older change the policy no longer takes (`requests.ErrByHand`) voids its
     approval, which leaves the proposal's list; its request opens again (`records.Void`), its issue is reopened with a
     note. The new change must apply. It joins the proposal when the default branch does not say it yet, even when an
     open answer already writes the same line, so it travels, and is voided, with that proposal. A proposal holding 9
     answers that still apply refuses a joining answer (`ErrFull`) before the step-up.
  4. An answer that loosens the policy (`policy3.Loosens`: approve, approve-until) redeems the passkey token for the
     change digest (`Input.Redeem`), after every check above; a reject needs none. A loosening answer without `Redeem`
     is `ErrNeedsPasskey`; Redeem's error is returned as is.
  5. When the answer writes a line: put the policy on `archfit/policy`. No open proposal: one commit on the tip, the
     branch moved with force, `EnsurePull` (marked ready when it is a draft). An open proposal: when the default
     branch's `.archfit.yaml` did not change since the branch's base (`Compare(head, tip)`), one fast-forward commit on
     the recorded head; otherwise, or when the branch left the recorded head (someone pushed to it), one commit on the
     tip, with force. Every commit of the branch since the default branch is then the App's own.
  6. Record the answer in one swap (`records.Answer`): the approval, the request `answered` and out of its index, and
     the approval joining the proposal (created when new) pinned to the new head and its digest.
  7. From here the answer stands; what follows is done as far as GitHub allows and logged with identifiers when not:
     describe the pull request (every answer with its owner and passkey), set `archfit/gate` (D13), merge at the pinned
     head unless "merge later", note the answer on the request issue (the owner and the passkey, design 19) and close
     it.
- The D13 check, read again from GitHub before `archfit/gate` is set to success on the pinned head: `archfit/policy`
  is at that head; the pull request's head is that branch of this repository, its base the default branch, and it is
  open; from the tip the head changes `.archfit.yaml` and nothing else (not truncated); the policy at the head has the
  pinned digest, which is the digest of the recorded approvals written on the tip. Any condition false sets nothing.
- The merge: wait at most 10 s for the pull request to show the head (GitHub moves it after the ref), then
  `MergePull(number, head)` (squash, expected head, no bypass actor: design appendix D.2). On any refusal (405, 409,
  422) read the pull request: merged anyway (an owner, an earlier attempt; unmeasured) is `merged`, else it stays open
  for the sweep (task 22) or an owner's merge on GitHub. A failed check is never followed by a merge.

Not provided: the consume on the merge push (task 20), the re-check of waiting pull requests (task 21), the sweep's
merge retry and the 24-hour void of an approval without a pull request (task 22), the pages (task 24).

## Subdomain Classification

Core: this is the protection of design 9.2, the only way a policy change reaches the default branch with an approval.
Volatility: high during milestone 2; milestone 3 adds the YAML edit (M3 task 10) as another answer kind.

## Encapsulated Knowledge

- The branch `archfit/policy` (`Branch`, M2 D13), the check name `archfit/gate`, the check's external id
  `proposal:<id>`, the pull request's title, body, commit messages and check summary (`texts.go`).
- Why an answer is recorded after the branch write and in one swap: a crash in between leaves a branch commit that no
  record names. The proposal's expected digest still names the old head, so a merge of the new head is "policy changed
  outside the flow" (task 20) and D13 never passes it. The reverse order could leave an approval with no proposal.
- Why "the App authored every commit" needs no author read: each head the App records is a commit it made, on a parent
  it chose (its recorded head, or a default branch commit), and a commit SHA names its content.
- The lease value `archfit-app.proposal-lease.v1` (token, expiry), read leniently: an unreadable one is replaced.
- An answer the default branch already says (a dependency it allows), or one that writes no line at all (a reject of
  `duplication`, `unowned` or an `only` rule), joins no proposal and is closed at once. An answer that joins nothing never merges the proposal, even when
  its approvals were voided and the branch rewritten: its other answers chose when to merge.

## Public Contract

```go
const Branch = "archfit/policy"

type GitHub interface { /* File, BranchHead, Pull, Compare, CommitOn, EnsurePull, UpdatePullBody, MarkReady, MergePull,
    CreateCheckRun, CreateComment, CloseIssue, ReopenIssue */ }
type Service struct{ /* unexported */ }
func New(gh GitHub, s store.Store, now func() time.Time) *Service

type Owner struct { ID int64; Login string }
type Input struct {
    Request int
    Answer  requests.Answer
    Owner   Owner
    Later   bool
    Redeem  func(ctx context.Context, digest string) (passkeyID string, err error)
}
type Outcome struct { Approval string; PullRequest int; State, Merge string } // State: requests.PullMerged, PullLater, PullRefused

func (s *Service) Digest(ctx context.Context, r api.Repo, number int, a requests.Answer) (string, error)
func (s *Service) Answer(ctx context.Context, r api.Repo, in Input) (Outcome, error)
func IsProposalPull(ctx context.Context, s store.Store, r api.Repo, pull int) (bool, error) // an open proposal's pull request

var ErrNotOwner, ErrNeedsPasskey, ErrBusy, ErrFull error
```

An error from `Answer` means the answer was not recorded (the request is still open; a token may have been used). What
it found on its way (a pull request closed without a merge, an approval that no longer applies) may have been recorded
void. The digest is
`sha256:` and the hex SHA-256 of the JSON of `requests.Change`.

## Integrations

- **gate** (parent): builds the `Service` with the gate's GitHub client and Store; the façade `answers.go` serves it to
  `control` after the installation's admission, and keeps an upload for an open proposal's pull request from setting
  its verdict (D13).
- **gate/records**: `Answer`, `Join` (a pin after a rewrite that adds no answer), `Void`, `VoidProposal`, the keys.
- **gate/requests**: `ChangeOf`, `Edit`, `RequestComment`, the refusals, `AnswerNote`, `ReopenNote`, `PasskeyShown`.
- **wire/policy3**: `Decode` (owners), `Digest`, `Loosens`. **github/api**: the roles of `GitHub`.
- Contract strength; LCA gate / rank 1 / distance 1 for the gate siblings; balanced.

## Change Vectors

- The YAML edit of milestone 3 (M3 task 10): another answer kind whose change is a whole file, on the same branch.
- The consume (task 20) and the sweep (task 22) read the proposal this module writes; the sweep retries the merge.

## Constraints and Invariants

- Fail closed: `archfit/gate` is set only when every D13 condition holds, read from GitHub after the write.
- One answer per request: the request moves from `open` to `answered` in the swap that records the approval.
- A proposal holds at most 9 approvals that still apply (a void one leaves its list): an answer that would join a
  full one is `ErrFull` before the step-up and any write, so the full proposal keeps its head and check and can merge.
  Its pinned head and digest change only in one swap: with an approval joining (`records.Answer`), or, after a rewrite
  that adds no answer, pinned again through an approval it keeps (`records.Join`).
- No log line holds a token, a passkey id or owner text; errors carry GitHub's method, route and status.
- **Release-blocking follow-up for tasks 20 and 21** (manager, 2026-11-12): an answered request leaves the request
  index at once, so while a proposal waits ("merge later", or a refused merge) a pull request that fails on the same
  key does not wait on it, and a new request line opens a second issue. During "merge later" an answered request must
  still cover its key (`waiting`, no duplicate issue) until its proposal is consumed.
- **Stated limit, unmeasured**: the App has no `workflows` permission. A rewrite after the default branch's policy
  changed moves `archfit/policy` onto default-branch commits; if those changed `.github/workflows/`, GitHub may refuse
  the move. The answer then fails with that cause and nothing is recorded. The rehearsal (task 28) can measure it.

## Test Specification

Over a fake GitHub with commits, branches, pull requests that follow their branch, check runs and a merge that requires
an `archfit/gate` success on the head (design appendix D.2), and the memory Store:

- `TestEachAnswerIsRecordedWrittenCheckedMergedAndNoted`: approve a dependency (the flow list becomes a block list),
  approve another rule (an exception), approve until a date, reject with `instead` (a deny rule): the merged policy, the
  approval with owner, login and passkey, the request answered, the proposal pinned, one merge at the pinned head, the
  check, the note and the closed issue, the pull request body.
- `TestARejectWithNoPolicyLineAnswersWithoutAPullRequest`.
- `TestAnAnswerIsRefusedAndWritesNothing`: no step-up, a token for another change, a reused token, a login the policy
  does not name, an answered request, no request, a past date, a module the policy no longer has.
- `TestTheStepUpIsBoundToTheChangeDigest`, `TestARefusedMergeKeepsTheProposalOpen` (405), `TestAMergeRefusedButDoneIsMerged`,
  `TestTheMergeWaitsForThePullRequestToShowTheHead`, `TestAMergeAfterAFailedCheckIsNotTried`.
- `TestTwoAnswersJoinOnePullRequest` (merge later, then approve and merge: one pull request, a fast-forward),
  `TestATargetThatMovesBetweenTwoAnswers` (another file: a fast-forward; the policy: a rewrite from the new tip with both
  answers and a new expected digest; a push to the branch: a rewrite that drops it),
  `TestAChangeThatNoLongerAppliesIsVoidAndItsRequestOpensAgain`, `TestAPullRequestClosedWithoutAMergeVoidsItsProposal`,
  `TestAMergedProposalWaitsForItsPushAndANewOneOpens`, `TestTwoAnswersAtOnceGoIntoOnePullRequest`,
  `TestNoPolicyOnTheDefaultBranch`, `TestAFullProposalTakesNoAnswerUntilItMerges`,
  `TestAnExceptionOfAnOnlyRuleHasNoTo`, `TestARefusedAnswerStillRecordsWhatItFound`,
  `TestAnAnswerKeepsGoingAfterItsCallerLeaves` (the caller's context ends after the record),
  `TestAnAnswerJoinsWhenOnlyTheOpenProposalSaysIt`, `TestAVoidedApprovalFreesItsPlace`,
  `TestAnAnswerThatWritesNoLineResyncsWithoutMerging` (answers kept: re-pinned and checked, not merged; none kept: the
  proposal void and out of the index).
- `TestTheCheckIsSetOnlyWhenEveryD13ConditionHolds`: the branch at another commit, another file, another digest, a
  pull request from another repository, into another branch, closed.
- `TestTheLease`: a held lease is `ErrBusy` with no write; an expired one is taken over and released; a release never
  deletes another holder's lease.

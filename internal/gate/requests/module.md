# Gate Requests

**Path**: internal/gate/requests/
**Parent**: internal/gate/ (Gate)
**Submodules**: none (leaf)

## Purpose

A worker asks an owner to change the policy with one line (design 10.3). This module turns that line into a request
issue, keeps the request, and derives the status `waiting` (design 7.1): a pull request whose errors are all covered
by open requests waits for the owners instead of failing. Without it the line is a comment nobody reads, and a worker
who asked still sees `fail`.

## Functional Responsibilities

- Take in a pull-request comment with a request line (`Service.Comment`):
  1. Parse it (`wire/request`); a comment without a request line is not a request. Only an open pull request into the
     default branch is judged; any other is ignored without a word.
  2. Authorize the author (M2 D21): the login that wrote the pull request, or a login with `write` or `admin`
     permission (`api.Collaborators`). Else one reply, `NoPermission`.
  3. Refuse a line that cannot be used (no reason, too long, a bad date) with its reply.
  4. Read the errors of the pull request's latest verified result (M2 D6): the pull record of its current head, the
     run's artifact `archfit-result`, whose SHA-256 must be the recorded payload digest. No record, a record of an
     older head, a status without errors the App accepted, or an artifact that is gone or other bytes: one reply, ask
     again when the check has run.
  5. The errors that ask an owner are those whose next step is the `archfit request` command. None: one reply,
     `NothingToAsk` with the records of the other errors.
  6. Per request key, in record order: a deny rule of the default branch's policy that lists both modules answers it
     (design 6.3): the reply quotes its `instead`, no issue. An open request with the key: the pull request joins it.
     Else the App opens the issue (`wire/request`'s title and body, the pull request linked), labels it
     `archfit-request`, assigns the policy's owners, and stores the request with its index entry. One line opens at
     most `MaxNewPerComment` (10) requests; the reply says to ask again for the rest.
  7. Derive the status again and publish `waiting` when it now holds (required mode only); then post one reply. The
     reply says the check shows `waiting` only in required mode.
  A pull request that joins is shown on the issue once: the body is rendered again and the issue gets one note until
  the request's `Listed` holds it. An upload joins in the store alone (`Wait`), so its pull request reaches the issue
  with its own request line, and a failed edit is done again on a retry.
- Take in an issue the CLI opened from a `can` answer (`Service.Filed`): parse it, require `write` or `admin`
  permission, take the key from the reported answer. A deny rule that answers it, or an open request with the key:
  one note and the issue is closed. Else the issue is the request, stored "as reported" (numbers unverified), labeled
  and assigned. Any other refusal: one reply, the issue stays.
- Derive `waiting` (`Cover`): a `fail` whose errors all ask an owner and whose keys are all open requests is
  `waiting`; the status line names the requests (`archfit: waiting, request #431`) and each error's next step is
  `wait for an owner: #431`. Every other result stays as it is.
- On a verified upload the gate calls `OpenIndex`, `Cover` and `Wait`: the pull request joins the requests that cover
  it, in the store only, so the upload stays within its deadline.
- Drop a closed pull request from the pull index (`PullClosed`).
- Show a request to its owner (`Service.View`, M2 task 17, design 10.4, 12.3, 19): the reason, the record with its
  balance, verified or "as reported", and from the policy at the default branch's tip the two modules (layer,
  volatility, subdomain, `depends_on`), whether the dependency is already allowed, and the earlier answers: the `deny`
  rules and the exceptions that name both modules, either way. Every answer in effect is a policy line; its request
  link comes with the `# request #N` line comment of task 19 (milestone 3 D8).
- Preview one answer to an open request (`Service.Preview`): the policy change as a unified diff of `Edit` against the
  tip's bytes, the tip's digest, whether the answer needs the passkey (`policy3.Loosens`), and the open pull requests
  (pull index) it touches: those waiting on the request, and, for approve and approve-until, those whose stored request
  keys hold the request's key, with the count of errors it clears and leaves. Approve-until takes the worker's date
  when the owner gives none. The App runs no analysis.
- Map an answer to its policy edit, once (`ChangeOf`, `Edit`), for the preview and for the answer (task 19): approve on
  `graph` adds the dependency to `depends_on`; approve on any other rule adds an exception without a date;
  approve-until adds an exception with `expires` (`unowned`: from the path; an `only` rule: no `to`); reject adds a
  `deny` rule `request-<N>` with the owner's `instead`; a reject of a copy, of unowned code or of an `only` rule writes
  no line. A comment, such as `request #431`, ends each line the edit writes.

Not provided: the answers (task 19), the pages (task 24).

## Subdomain Classification

Core: the request is how a worker asks instead of copying code. Volatility: high during milestone 2.

## Encapsulated Knowledge

- Which errors ask (the next step's command), how errors group by request key, and in which order.
- When a request joins, when it opens an issue, when a deny rule answers it. A deny rule is matched by module name;
  **stated limit**: a rule written with path globs is not matched, and the request opens normally.
- The texts of the replies and notes. None repeats a worker's text outside a fence; owner text (`instead`) is in a
  `text` block, module and rule names are in code spans, so no `@name` or link in them notifies or links. Every
  comment goes through `inert`: line ends become LF and a line that would start with `/archfit request` starts with a
  space, so the App never reads its own comment as a request, whatever engine or owner text it quotes.
- The caps: a record at most 30,000 bytes (the issue body stays below GitHub's 65,536 characters and the stored
  request below 64 KiB), at most 10 assignees, at most 10 new requests per line.
- The artifact name `archfit-result` (M2 D6), which the Action uploads (archfit-action task 10).
- Advisory mode: `Cover` is applied only in required mode. `waiting` is `in_progress`, which blocks a merge, and an
  advisory policy never blocks; a request still opens.

## Public Contract

```go
const ArtifactName = "archfit-result"
const MaxNewPerComment = 10

type GitHub interface { /* File, BranchHead, Pull, Artifact, CreateComment, CreateIssue, UpdateIssueBody, CloseIssue,
    SetAssigneesAndLabels, CollaboratorPermission */ }
type Service struct{ /* unexported */ }
func New(gh GitHub, s store.Store, p feedback.Publisher4) *Service

type Comment struct { Repo api.Repo; Pull int; Author, Body string; Today time.Time }
type Issue struct { Repo api.Repo; Number int; Author, Body string; Today time.Time }
func IsRequest(body string) bool
func (s *Service) Comment(ctx context.Context, c Comment) (reaction string, err error)
func (s *Service) Filed(ctx context.Context, i Issue) (reaction string, err error)

func Asks(e result.Error) bool
func Key(e result.Error) (string, error)
func Cover(r result.Result, open *records.Index) (shown result.Result, ids []int)
func OpenIndex(ctx context.Context, s store.Store, r api.Repo) (*records.Index, error)
func Wait(ctx context.Context, s store.Store, r api.Repo, pull int, ids []int) error
func PullClosed(ctx context.Context, s store.Store, r api.Repo, pull int) error

type Answer struct { Kind policy3.Answer; Until, Instead string }
type Change struct { Request int; Kind policy3.Answer; Rule, Path, From, To, Until, Instead string } // JSON, kept by an approval
type View struct { /* Number, State, Reason, Rule, Path, From, To, Expires, Record, Numbers, Policy, Modules, Allowed, Earlier */ }
type Module struct { Name, Layer, Volatility, Subdomain string; DependsOn []string }
type Earlier struct { Kind /* EarlierDeny, EarlierException */, Rule, From, To, Why, Instead, Expires string }
type Preview struct { Answer Answer; Change Change; Diff, PolicyDigest string; Loosens bool; Pulls []PullEffect }
type PullEffect struct { Number int; Status string; Waits bool; Clears, Left int }
var ErrNoRequest, ErrClosedRequest, ErrAnswer, ErrNoPolicy, ErrByHand error
func (s *Service) View(ctx context.Context, r api.Repo, number int) (View, error)
func (s *Service) Preview(ctx context.Context, r api.Repo, number int, a Answer) (Preview, error)
func ChangeOf(number int, req records.Request, a Answer) (Change, error)
func Edit(file []byte, c Change, comment string) ([]byte, error)
func Diff(path string, old, edited []byte) string
```

Reactions: `none`, `request_ignored`, `request_refused`, `request_asked`, `request_answered`, `request_filed`. An error
is a failed read or write: the gate returns it so the delivery is retried. A retry joins the requests already open
without a second note; a reply may be posted again.

## Integrations

- **gate** (parent): builds the `Service` with the gate's `Publisher4`, so a waiting check takes the head's lease
  that uploads take; calls `Comment` on `PullCommented` with a request line, `Filed` on `IssueOpened`, `PullClosed`
  on `PullClosed`, and `OpenIndex`, `Cover` and `Wait` from the envelope 2 upload (`upload4.go`).
- **gate/records**: the request, its index and the pull record. **gate/feedback**: `Publisher4`, `ErrStaleInputs`.
- **wire/request**: the grammar, the key, the issue, the rejections. **wire/result**: the result and its record.
  **wire/policy3**: owners, gate mode, deny rules. **wire/envelope**: the payload digest.
- **github/api**: the roles of `GitHub`.

## Constraints and Invariants

- A request and its index entry change in one swap; a pull request joins with another. An open request is in the
  index under its key, once.
- The App publishes `waiting` only from a result whose digest matches the pull record, and only while the pull record
  is still the one read: head, run, attempt, payload digest and status (`Publication4.Current`). A newer upload, or
  the same run decided again (say `unknown` after a policy changed), makes it stale and the newest record is read
  again.
  The upload path in turn re-reads the open requests inside the head's lease, so a `fail` decided before a request
  opened is stale and the Action's retry decides again.
- Assignment and labels are notifications, never authority: a failure there does not stop the request and is logged
  as `requests triage failed` with identifiers only.
- **Stated limit**: when storing a new request fails after its issue was opened, a retried delivery opens a second
  issue for the key.

## Test Specification

- `TestCover`, `TestCoverMatchesTheWaitingGolden`, `TestRequestNames`: every status, partial coverage, a fix-the-code
  error whose key is open, two requests, the status line and record of `wire/result`'s `waiting.txt`.
- `TestNoCommentOfTheAppIsARequest` (owner and engine text with LF, CRLF and a bare CR before the command),
  `TestTheReplySaysWaitingOnlyWhenTheCheckCanWait`, `TestSameRecordComparesTheStatus`.
- `TestEveryTextUsesNoNotUsedWordAndIsNoRequest`, `TestOwnerAndWorkerTextCannotMentionOrLink`,
  `TestTheIssueBodyStaysUnderGitHubsLimit`, `TestDeniedMatchesModulesOnly`, `TestGroupsKeepRecordOrderAndSkipErrorsWithoutAKey`.
- In `internal/gate` (`requests_test.go`), over the fake GitHub and the memory Store: a line opens an issue and sets
  waiting; a second pull request joins; a deny rule already answers; each refusal (no permission, a deny-mode error, no
  result yet, no reason, a past date) gets one reply; an artifact that is not the uploaded bytes, one that is gone and
  a result of an older head are no verified result; waiting is not published over a record decided again; a failed
  triage is logged and the request still counts; a writer who did not write the pull request may ask; a comment
  without a line writes nothing; ten requests per line; a new push stays waiting and a new error fails; advisory
  never waits; an upload decided before a request is stale; a CLI issue as reported, then confirmed by a pull request;
  a CLI issue closed or refused; a closed pull request leaves the pull index.
- In `preview_test.go`, over a fake GitHub and the memory Store: a verified and an "as reported" request; a request with
  earlier answers, one already allowed, and none without a policy; the diff of approve, approve until the worker's date
  and another, and reject with `instead`; each refusal; the pull requests an approve and a reject touch; `Edit` for
  each answer of each rule; the comment on each written line (LF and CRLF); `Diff` cases. In `internal/gate`
  (`preview_test.go`): the view and the preview after a real request line and two uploads, and an installation not
  admitted.

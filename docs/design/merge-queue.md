# Merge queue: design and Action contract

Updated for M2 task 26: no approval exists; a queued member that changes a protected path blocks its group.

Status: contract revision 1, frozen for archfit-action v3.1.1. A change to this contract is a new revision that
both repositories adopt together.

This page states how the App gates a GitHub merge queue on the default branch, and what the Action must send for a
merge group. The [architecture](../architecture.md) and the [user guide](../user-guide.md#merge-queue) describe the
behaviour for users and operators.

## Why a merge group needs its own decision

GitHub tests a queued pull request as a merge group: a temporary commit, on a `gh-readonly-queue/...` branch, that
holds the default branch plus every entry ahead of it in the queue plus this pull request. The queue merges only when
the required checks pass on that commit. A success on a pull request head is not a success on the group: two pull
requests that pass alone can fail together.

When the branch rule's "Only merge non-failing pull requests" is off, a pull request whose own group failed can merge
inside a later group that passes. So each group is checked for every protected change it carries, not only the change
of its last entry.

## Wire contract (for the Action)

The envelope schema is unchanged: `archfit.report-envelope.v1`,
[`internal/wire/envelope/envelope.v1.schema.json`](../../internal/wire/envelope/envelope.v1.schema.json). A merge
group report uses these values:

| Field                                       | Value                                                                 |
| ------------------------------------------- | --------------------------------------------------------------------- |
| `kind`                                      | `report`                                                              |
| `event`                                     | `merge_group`                                                         |
| `pull_request`                              | `0`                                                                   |
| `head_sha`                                  | `merge_group.head_sha` of the event, which is `GITHUB_SHA` of the run |
| `analyzed_sha`                              | the same as `head_sha`: the group commit is the integrated tree       |
| `base_sha`, `merge_base_sha`, `integration` | `""`                                                                  |
| `policy_change`                             | absent (the schema refuses it for every event except `pull_request`)  |
| `run_id`, `run_attempt`                     | the run, as for every event                                           |
| `baseline_digest`, `labels_digest`          | bare hex SHA-256 of the blobs the engine read, or `""` without a file |
| engine identity, `payload_digest`           | as for every report                                                   |

Fixtures: [`merge-group-report.json`](../../internal/wire/testdata/envelope/merge-group-report.json) (valid) and
[`invalid-merge-group-with-base.json`](../../internal/wire/testdata/envelope/invalid-merge-group-with-base.json)
(refused: `base_sha` set).

The Action must:

1. Run only on `merge_group` with action `checks_requested`, and only when `merge_group.base_ref` is
   `refs/heads/<default branch>`.
2. Analyse exactly the group commit `merge_group.head_sha`.
3. When an endpoint is set, write `.archfit.yaml`, `.archfit-baseline.json` and `.archfit-labels.yaml` from the git
   blobs **at the group commit**, and report their digests. A blob that differs from the default branch ends the
   group (below). Without an endpoint no one checks it, so the files come from `merge_group.base_sha`, a
   default-branch commit.
4. Not create a check named `archfit/gate` or `archfit/state`. The App writes them, on the group commit.
5. Upload to `POST <endpoint>/v1/reports` as for every report.

Answers that are specific to a merge group:

| Status | Body                                     | Meaning for the Action                                                                                        |
| ------ | ---------------------------------------- | ------------------------------------------------------------------------------------------------------------- |
| 200    | `{"conclusion": "...", "reason": "..."}` | Accepted and published on the group commit. Not "passed"                                                      |
| 409    | `{"error": "stale_group"}`               | The queue has no entry with this head commit: the group was destroyed, rebuilt or merged. Final: do not retry |
| 409    | `{"error": "stale_attempt"}`             | A newer attempt of the run exists. Final                                                                      |
| 503    | `{"error": "unavailable"}`               | The App could not read the queue, the inputs, or wrote nothing because they changed. Retry                    |

All other answers are those of every report upload.

## How the App binds the upload

1. The OIDC token's `event_name` is `merge_group`. The workflow run that `run_id` names has event `merge_group`,
   the same attempt, and the head commit equals the token's `sha`, the envelope's `head_sha` and `analyzed_sha`.
   The run's head repository is the repository itself.
2. The App reads the merge queue of the default branch through GitHub's GraphQL API
   (`repository.mergeQueue(branch).entries`). An entry must have this head commit. None is `409 stale_group`. The
   App never parses the `gh-readonly-queue/...` branch name, and nothing the uploader states selects members.
3. The workflow path of the token is the one the protected settings name, as for every report.

## Members and the combined inputs

The members of a group are the queue entries from position 1 up to the entry whose head commit is the group commit:
each entry's build holds every entry ahead of it. For each member the App reads the pull request: open (or merged since the queue
was read), base is the default branch, head commit is the entry's, not a fork. An entry GitHub has not built yet has no
group commit and binds no upload. A fork member ends `fork_unsupported`; another mismatch ends
`merge_group_unverified`.

The App reads the default-branch head commit `S` once. It reads every protected file at the two commit SHAs `S` and
the group commit `X`, never by branch name, so the decision has one immutable snapshot. Settings, owners, agents, the
gate mode and the workflow path come from `S`.

For each protected path:

- Same blob at `S` and `X`: unchanged.
- Different blob: no member changes the path in its own pull request. That ends `merge_group_conflict`.

Separately, every member whose pull request changes a protected path ends the group `owner_approval_missing`, naming
the member and the path, also when the group's combined file equals the default branch's. Nothing approves such a
member: not a review, a push or an App answer.

The accepted digest of each file is its blob at `S`. The group commit itself is never approved: GitHub created it, and
queue membership, a push or an `approved_by` text approve nothing.

## Feedback and lifecycle

- `archfit/gate` is written on the group commit when the protected settings say `gate: required`, with
  `archfit/state`. No pull request comment is written for a group.
- A group commit is a branch head, so a pull request into another base can share it. As for a pull request head, the
  App never writes a `success` then: the upload ends `shared_head`, and opening such a pull request withdraws an
  existing group success, also after the group merged and the default branch moved to its commit.
- The `merge_group` webhook with `checks_requested` creates the pending `archfit/gate` on the group commit. A group
  without an upload stays pending until GitHub's status check timeout fails it.
- The webhook with `destroyed`, for a reason other than `merged`, turns a pending or successful group check into
  `action_required` `merge_group_unverified`. A later upload for the group gets `409 stale_group`.
- Inside the publication lease the upload reads the queue and the inputs again. A group destroyed, a member
  that changes a protected path, or a protected file changed since the decision writes nothing and answers `503`.

## Recovery

| Reason                   | What to do                                                                                                                                                                                                                                               |
| ------------------------ | -------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------- |
| `merge_group_conflict`   | A protected file of the group differs from the default branch, and no queued pull request changes it. Queue the pull requests again.                                                                                                                     |
| `merge_group_unverified` | The App could not confirm the group from GitHub: it was destroyed or rebuilt, or a member changed. Queue the pull request again. (A missing Merge queues permission is not this reason: the upload answers `503`, see Limits.)                           |
| `owner_approval_missing` | A queued pull request changes a protected path. Remove it from the queue and revert the change; ask for a policy change with a request (see the user guide, "Change the policy"). Queue the pull requests again.                                         |

## Limits

- Only the merge queue of the default branch. A queue on another branch gets nothing from the App.
- Forks stay refused, in a group as on their own pull request.
- The hourly sweep does not scan merge groups. A waiver that expires during a group's short life is decided at
  upload time only.
- The App needs the repository permission **Merge queues: Read** and the **Merge group** webhook event. Without them a
  merge-group upload answers `503` and no pending check is created: the group fails by timeout, never passes.
- Reports stay customer-attested. The OIDC token proves the run, not the measurement.

# Merge queue: design and Action contract

Updated for M2 task 26: no approval exists; a queued member that changes a protected path blocks its group.

Status: contract revision 2 is the Archfit 4.0 contract and Action 4 ships it. Revision 1 (the v1 envelope) is no longer accepted by the App.

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

The App accepts [`archfit.upload-envelope.v2`](../../internal/wire/envelope/envelope.v2.schema.json). Every field is self-reported. The body is result JSON v1. Action 4's schema is byte-identical to this App schema; its pin lands with archfit-action PR #16. A merge-group envelope contains:

| Field | Value |
| --- | --- |
| `schema_version` | `archfit.upload-envelope.v2` |
| `repository` | GitHub repository `owner/name` |
| `event`, `pull_request` | `merge_group`, `0` |
| `head_sha` | The merge group's checked commit |
| `merge_base_sha` | The merge base of the group head and target tip |
| `target_ref`, `target_sha` | The target branch ref and fetched tip |
| `policy_digest` | `sha256:` digest of the policy bytes used |
| `run_id`, `run_attempt` | GitHub Actions run and attempt numbers |
| `engine_version`, `image_digest`, `platform` | Engine identity and runner platform |
| `payload_digest` | `sha256:` digest of the exact result body |

The App's [merge-group fixture](../../internal/wire/testdata/envelope-v2/merge-group.json) is an example. The v1 fixtures were removed in Archfit 4.

The Action must:

1. Run only on `merge_group` with action `checks_requested` and the default branch as `base_ref`.
2. Check the group head named by the event and verify the checkout and group base.
3. Select `.archfit.yaml` using the merge base, checked head and target tip. The Action reports its digest; the App independently judges the policy against protected inputs.
4. Upload the result to `POST <endpoint>/v1/reports` when an endpoint is configured. Without one, the Action fails the job unless the result is `pass`.

The App's 200 response contains `status` and, when applicable, `reason`. Accepted does not mean passed.

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

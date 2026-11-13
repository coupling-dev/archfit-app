# User guide

This guide is for repository administrators and policy owners using Archfit 4. The App checks changes against `.archfit.yaml` on the protected default branch. It records owner answers and carries policy changes in a pull request. See [architecture](architecture.md) for the trust boundary and failure handling.

> **Current release limitation:** The checked-in manifest has no approved engines. Until a release populates it, the App cannot admit an engine identity.

## Install and set up

1. Install the GitHub App for the repository. An account not on the operator's approved list waits for operator admission.
2. Put a schema 3 `.archfit.yaml` and `.github/workflows/archfit.yaml` in the repository. Review them before the first approval. The App does not generate these files.
3. Sign in to the App and register a passkey as a repository administrator. See [Register a passkey](#register-a-passkey).
4. A repository administrator must approve the first policy with a passkey. The owner action supports a same-repository open pull request when the default branch has no schema 3 policy, or the current default-branch policy otherwise. Open `/r/<owner>/<repo>/owner/first`, or add `?pull=<number>` for the onboarding pull request.
5. Configure the branch ruleset below and verify the workflow and engine are approved in the App's release manifest.

The App's setup API reads the policy and workflow at one default-branch commit and reports admission, first approval and ruleset findings. It does not change GitHub rulesets. Home shows this setup information before the first approval.

## Home and the map

Open `/r/<owner>/<repo>` for **Home**. The status line comes first. **Needs you** lists open requests,
exceptions near their date, drift and owner actions. Each item opens its page. A request's record folds away.
Before the first policy is approved, Home shows the setup steps, what is missing and the ruleset findings
instead of the map. Owner actions open the pages below.

**The map** shows modules by layer, outer first, with each module's volatility. Its legend identifies an allowed
dependency, a requested one with its request number, one a rule denies, an exception with its date, and drift.
When several kinds apply to a pair, only one is drawn, in this order: drift, a rule denies it, requested, exception,
allowed. A dependency that is not drawn needs a request. **Show the map as a list** opens the same modules and
dependencies as text. At phone width, scroll inside the map box. Focus it and use the arrow keys to scroll by keyboard.
If the layout worker fails, Home says so and the list is still available.

The repository tabs are **Home** and **Policy**. Policy lists modules, rules, notes and exceptions from the default
branch, with links to recorded requests. **Show raw YAML** opens the file. **Edit** appears only when change control
is available, but the editor is not yet available: the link currently returns to the read view.

## Configure the ruleset

On the default branch, require pull requests and the `archfit/gate` check from this App's integration. Set required GitHub approvals to **0**. Do not make the App a bypass actor on the branch ruleset: bypass would also bypass the required check. Keep an administrator bypass covering each applicable branch ruleset as an emergency path. The App cannot always read bypass actors; an unknown result is not confirmation. Ask an administrator to check them.

For an organization-owned private or internal repository, a supported push ruleset can restrict `.archfit.yaml`. The App needs an **always** bypass on that separate push ruleset to write approved policy proposals. This does not grant it a branch-ruleset bypass. The setup API reports the rules it can read; it does not apply them.

## Merge queue

The App accepts a `merge_group` upload and binds it to GitHub queue authority. Require `archfit/gate` on the default branch ruleset for the queue as well. The workflow must run for merge-group events.

## Owner actions

Each page starts with the status, shows the App's diff of the actual files, and has one main action.
That action asks for your passkey before it submits. **Show the files and their SHA-256s** opens
all input files and their immutable commit refs. GitHub issue links and direct loads use the same pages.

| Action | Path after `/r/<owner>/<repo>` |
| --- | --- |
| Approve the first policy on the default branch | `/owner/first` |
| Approve the first policy in an onboarding pull request | `/owner/first?pull=<number>` |
| Approve a workflow change | `/owner/workflow?pull=<number>` |
| Confirm a policy changed outside the flow | `/owner/confirm` |
| Revert a policy changed outside the flow | `/owner/revert` |

The first approval has no approved baseline: every policy line is shown as an addition.
Only a repository administrator named in that policy can make it. Other actions require an owner
of the previously approved policy, not an owner added by an outside-flow change.
A revert shows the exact policy it will write, including earlier approved answers kept from the open
policy pull request. It also shows those answers relative to the approved policy and names that pull request.
An approval does not mean the check passed: the outcome says whether a new run was requested, is pending,
or could not be started.

**Known limits:** Submit re-reads the displayed files, refs and open proposal answers before the passkey is
consumed. A difference refuses the action and asks you to reload. GitHub and the Store do not provide one
atomic snapshot: a ref can still move after the last read. Proposal consumption re-checks its files and
digest, and outside-flow reconciliation surfaces a later default-branch policy change. The passkey binds
the displayed view; it does not remove this residual window or prove that the owner read the files.

## Register a passkey

Open `/r/<owner>/<repo>/passkeys` after signing in. A policy owner can register a passkey; before the first policy, a repository administrator can register one. Follow the browser's registration prompt. Registration opens a GitHub issue notifying the user and owners. A second passkey and each removal require an existing passkey. The App uses user verification for the assertion; it does not prove that the owner read the request.

## Answer a request

A worker can comment `/archfit request <reason>` on a pull request, or file a request through the CLI. The App creates or joins request issues. CLI-filed measurements are marked **as reported**, not verified.

1. Open `/r/<owner>/<repo>/requests/<number>`. Read the status, reason and record with its balance, then the two-module map and declared volatility. Numbers without a confirming pull request remain **as reported**.
2. Preview the policy change and affected pull requests.
3. Read earlier answers, then choose **Approve and merge**, **Approve until a date**, or **Reject** with what to do instead. An expiry date must be in the future and at most 365 days away.
4. Confirm an approval with your passkey. Reject does not require a passkey.
5. Check the result. The App attempts to merge the policy pull request unless you chose merge later. If GitHub refuses, the pull request remains open. A merge-later answer takes effect when that pull request merges.

An answered request shows its recorded answer and links to the GitHub issue that names the owner and passkey. Older records without an approval pointer show the issue link only, with no answer buttons.

Only an owner named in the current policy can answer. The requester may answer if also an owner. Several answers can join one policy pull request. After consumption, the App attempts to re-check affected pull requests; a fresh result is needed before they can pass.

## Expiry and drift

An exception is valid through its expiry date, in UTC. From seven days before expiry the App maintains an `archfit-expiry` issue for the exception. Fix the code or ask an owner to extend the exception. After expiry an old pull-request result is withdrawn and the App attempts a fresh check; if it cannot re-run, it can publish an expired-exception failure.

An accepted default-branch result that reports drift opens or updates an `archfit-drift` issue for owners. Fix the reported drift and run the workflow again. The App closes the issue only when the accepted result reports no drift and no unread places. Missing or unread evidence is not a clean result.

## Troubleshooting

| Situation | Action |
| --- | --- |
| The check is `unknown` | Read its reason and restore the named authority or input. Unknown is not a pass. |
| A policy changed outside the approval flow | An owner must confirm or revert it with a passkey. Review the issue and the policy before acting. |
| A workflow changed | An owner must approve the workflow change with a passkey unless only the Action ref moved to an approved commit. |
| A request shows **as reported** | Treat its measurements as a claim. Review the source and a current pull-request result. |
| The App cannot confirm a ruleset or bypass | Ask a repository administrator to inspect the ruleset in GitHub. |
| A passkey prompt fails or the policy moved | Start the action again and review the new preview. A step-up is bound to one action and expires. |

For operators, see [configuration](architecture.md#configuration) and [cloud operations](design/cloud/operations.md).

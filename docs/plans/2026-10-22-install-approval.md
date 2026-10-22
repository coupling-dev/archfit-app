# Install approval and the "Waiting for approval" page

**Goal**: make the GitHub App public, so it installs on any account, while nothing runs on our GCP for an account
that an operator (Alexei, Vladik) has not approved. GitHub has no install approval of its own: a public App installs
anywhere, and it cannot turn private again while other accounts have it. So the App enforces approval itself.

## Decisions

| #   | Question                       | Decision                                                                                                                                                                                                                                                                             |
| --- | ------------------------------ | ------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------ |
| 1   | What is approved               | The **GitHub account** (user or organization) that installed the App. Every repository of that installation follows it. A repository's owner is always its installation's account, so every path can check it.                                                                       |
| 2   | Auto-approved accounts         | `alexei-led`, `vladikk`, `coupling-dev`. Config `ARCHFIT_APPROVED_ACCOUNTS` (logins, comma-separated), defaults in `internal/app/config.go`. A change is a pull request plus a release.                                                                                              |
| 3   | Where manual approvals live    | Firestore, one record `admission` under the installation's tenant: account login, who approved (login and user ID), when. Approve writes it, revoke deletes it, uninstall deletes it with the rest of the tenant (existing `Forget`).                                                 |
| 4   | Who approves                   | Operators: config `ARCHFIT_OPERATORS`, GitHub **user IDs** `1898375` (alexei-led) and `1788671` (vladikk). IDs, not logins: a renamed login can be registered by someone else.                                                                                                       |
| 5   | Where operators approve        | A new page `/admin`, operators only (404 for any other signed-in user; signed-out requests are sent to sign in, as on every page): installations waiting, with their repositories and install date, and **Approve**; approved ones with who and when, and **Revoke**. The header shows "Admin · 2 waiting".                                                |
| 6   | What "not approved" does       | Nothing that costs. Repository webhooks are dropped and logged. Uploads get `403 installation_not_approved`, which the Action does not retry. The gate sweep skips the installation. Repository APIs answer `403 installation_not_approved`, and the web app shows the Waiting page. |
| 7   | When the record cannot be read | Fail closed and retry: nothing runs for the account, and the work is retried (a webhook is redelivered, an upload answers `503 admission_unavailable`, the sweep counts an error, the UI says "try again"). Auto-approved accounts need no read, so our own repositories keep working through a Firestore outage. |
| 8   | Revoke after approval          | Same as never approved, from the next event on. Checks already posted stay on GitHub.                                                                                                                                                                                                |
| 9   | Notification of a new install  | None: the header count tells operators. Upgrade trigger: a request waits more than a day unnoticed → add a log-based alert e-mail.                                                                                                                                                   |

Limit, stated in the docs: auto-approval is by login. If `vladikk` renamed and someone registered the old login, their
installation would be served for free. It grants nothing else: operator rights are by user ID.

## The Waiting page (also the signed-out landing page)

One page that says at once what Archfit does and why it is worth it. One component, `Pitch.svelte`, used twice: on the
landing page with **Sign in with GitHub**, and on any repository of an account that is waiting, with the status box.

1. **Hero**: "Architecture checks on every pull request." Under it: "Archfit keeps your modules apart the way you
   designed them. It flags a pull request that crosses a boundary or tightens the wrong coupling, before it merges."
2. **How it works**, a three-step diagram (inline SVG, light and dark):
   `Your CI runs the Archfit engine` → `A report with no source code` → `A check and one comment on the pull request`.
   Under it: "Your code never leaves your CI."
3. **What you get**, four cards: catches boundary and coupling drift on the pull request; the policy is a file you
   review like code; changes to the policy need an architecture owner's approval; existing debt is a baseline, so only
   new problems block.
4. **A sample check**: a static picture of the `archfit/state` check and the comment's first lines, as GitHub shows them.
5. **Status box** (Waiting page only): "Your installation on **acme** is waiting for approval. Archfit is an
   experiment, and we approve installations by hand. Until then nothing runs and nothing is posted on your pull
   requests." Then: "Nothing to do now. This page changes once it is approved."

## Code changes

- `gate/inputs`: `AdmissionKey`, the `Admission` record, and `Admissions.Admitted(ctx, installationID, owner)`: config
  list first, then the Store record; any error is "not approved". ("Approval" already names the policy owner's
  approval, so the code says admission; the UI says approval.) The gate façade re-publishes them, the way it does the
  entitlement. `control/account` writes the record (`Admit`, `Unadmit`), the way it writes the entitlement.
- Gate: `HandleEvent` returns early (`reaction: not_admitted`) for an unapproved owner. `ingest` refuses after it
  resolves the repository: `403 installation_not_approved`. The sweep skips unapproved installations.
- Control: `ui.apiRepo` checks approval after authorization and answers `403` with code `installation_not_approved`
  and the account login. New `GET /api/admin/installations`, `POST /api/admin/installations/{id}/approve` and
  `/revoke` (operators, CSRF). `GET /api/session` gains `operator` and `waiting` (count, operators only).
  `GET /api/repos` marks each waiting repository `waiting`.
- App: config `ARCHFIT_APPROVED_ACCOUNTS`, `ARCHFIT_OPERATORS`, with the defaults above in code.
- Web: `Pitch.svelte` (landing and Waiting), the Waiting page on the `installation_not_approved` answer, the Admin page,
  the "waiting" badge on the Repositories list.
- Docs: user guide (who gets served, the Waiting page), operator section (approve, revoke), `infra/README.md` (make
  the App public, the two variables), module docs.

## Tests

- Approved: config login (any case), Store record; not approved: no record, Store error, revoked.
- Gate: an unapproved PR event makes no GitHub call; an unapproved upload answers `403 installation_not_approved`; the
  sweep touches no unapproved installation.
- UI: every repository API answers 403 for an unapproved owner, and the overview of an approved one is unchanged;
  `/admin` and its API are 404 for a non-operator; approve then revoke round-trips, with who and when.
- System test: install on a stranger's account → PR event, upload and UI all inert → operator approves → the same
  upload is accepted.
- Smoke: the landing page, the Waiting page (no horizontal scroll at phone width, axe clean), the Admin page.

## Rollout

1. Release. Check that `coupling-dev` repositories still get checks (auto-approved).
2. Make the App public: App settings → Advanced → **Make public**.
3. Install it on `alexei-led`, choose only `ccgram`. Auto-approved, so onboarding starts at once.
4. Onboard `alexei-led/ccgram`. Its `.archfit.yaml` already passes the App's policy check; it stays as it is.

## Not doing

Auto-uninstall of installations left waiting, per-repository approval, a Marketplace listing, a contact form.

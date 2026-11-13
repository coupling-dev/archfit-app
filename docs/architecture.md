# Architecture

Archfit App is a GitHub App that receives engine results, publishes pull-request feedback, and controls changes to the repository policy. The engine runs in the customer's GitHub Actions workflow. The App uses GitHub and an installation-scoped store as authorities; the uploaded result remains a producer claim.

For owner procedures, see the [user guide](user-guide.md). For deployment, see [cloud operations](design/cloud/operations.md).

## Components

- `internal/app` composes the service and mounts routes. Public and worker roles separate intake from queued work; `all` runs both locally.
- `internal/github` authenticates webhooks and Actions OIDC tokens and accesses GitHub APIs.
- `internal/gate` admits uploads, decides feedback, manages requests and proposals, and reconciles state.
- `internal/control` serves authenticated pages, passkeys, owner actions and account administration.
- `internal/wire` holds versioned envelope, result, policy and request contracts. `internal/store` supplies transactional state, in memory or Firestore.

## Ingest

### Report upload

`POST /v1/reports` accepts envelope 2 (`archfit.upload-envelope.v2`) with result JSON v1 (`archfit.result.v1`), up to 5 MiB (`ingest.maxPayloadBytes`, `5 << 20` bytes). The receiver strictly decodes the envelope and checks the payload digest. It verifies the GitHub Actions OIDC token, resolves the installation, repository, workflow run and attempt through GitHub, and binds the event to a pull request, default-branch push or merge group. The accepted workflow path is `.github/workflows/archfit.yaml`.

The envelope fields and result are self-reported. The App compares them with GitHub authority, the locked manifest and protected inputs; it does not prove that the engine's analysis is correct. Invalid payloads, mismatches, unsupported events and forks become unknown, not a neutral or skipped verdict. Stale attempts and moved heads are rejected. The App reads schema 3 `.archfit.yaml` from the target/default-branch authority and checks its exact-byte digest, the approved policy state and workflow identity. It does not trust policy supplied by the pull-request head.

The checked-in [manifest](../internal/wire/manifest/manifest4.json) currently has no approved engines. The release must populate the manifest before an engine identity can be admitted. An HTTP 200 upload response means accepted, not passed.

## Feedback

The App publishes `archfit/gate`: pass is success; fail is failure, or neutral in advisory mode. Waiting is in progress. Unknown is action required, not neutral or skipped.

## Requests and answers

A pull-request author or a collaborator with write or admin permission can comment `/archfit request <reason>`. The App uses the current pull record and the `archfit-result` artifact matching its recorded digest. It creates or joins requests for eligible errors; a matching deny rule answers instead. A CLI-filed issue can also become a request; its measurements are marked **as reported**. A failed result becomes `waiting` only when all its errors are covered by open requests, in required mode.

A policy owner can preview and answer an open request. Approve adds an allowed dependency or exception; approve-until adds a dated exception; reject adds a deny rule with an alternative. The first two require a passkey assertion bound to the request, answer and change digest. Reject does not. A recorded answer can join other answers on `archfit/policy`; the App checks the proposal's head, files and digest before setting its check and attempting the merge. Merge-later leaves the proposal open. A successful answer does not guarantee GitHub merged the proposal.

## Approvals and re-check

An administrator's first approval establishes change control. Later owner approvals are recorded for a specific change and consumed once when the matching policy proposal merges. A policy push outside this flow puts the repository outside the flow and opens an issue; checks become unknown until an owner confirms or reverts. A changed workflow also requires an owner action, except for a change limited to an approved Action ref.

After a proposal is consumed, the App refreshes affected pull requests. It requests a re-run only for the latest relevant completed run of the live head, younger than 30 days, reading the target tip, when the target changed only in `.archfit.yaml` since the event base. An in-flight run is left alone. Otherwise it attempts an expected-head branch update for an eligible same-repository pull request, or explains why it cannot. Only a fresh upload supplies a new result.

## Sweep

An external scheduler calls `POST /internal/sweep` hourly. The sweep reconciles policy pushes and proposals, withdraws stale results, handles missing uploads and re-checks, and warns about or enforces expired exceptions. It acts on approved policy state, and does not turn unreadable evidence into a pass. See [cloud operations](design/cloud/operations.md) for deployment and authentication.

An exception remains valid through its expiry date, using UTC calendar days. The App opens an `archfit-expiry` issue from seven days before that date. After expiry, the sweep withdraws an old result and requests a fresh run when possible; otherwise it publishes the expired-exception failure. An accepted default-branch result can also maintain an `archfit-drift` issue. Unread evidence is not treated as clean.

## State

The store is partitioned by installation. Archfit 4 gate records use `v4/` keys, including `v4/repos`, repository, request, approval, proposal, policy-push and pull records and their indexes. Repository names in keys are lower case. Passkeys and short-lived challenges also use `v4/` keys. Multi-key compare-and-swap transactions keep related records and indexes together; schema-tagged values are decoded strictly. Archfit 4 gate records do not read or write Archfit 3 keys.

A rollback does not undo stored data. The separate key space prevents the older gate and the Archfit 4 gate from interpreting each other's gate records. It does not roll back GitHub policy changes.

## Trust rules

GitHub authentication establishes the workflow and run, not the truth of the result. A digest comparison binds bytes, not correctness. A passkey confirms an action with user verification, not that a person read or understood the request. No LLM output decides the gate. Failures to establish authority or approved inputs must not become a pass.

The App can warn about ruleset configuration, but an administrator can change or bypass rules. The branch ruleset must require the App's `archfit/gate` check and must not let the App bypass it. The separately scoped policy push ruleset, where supported, needs the App's bypass to write policy proposals. See [ruleset setup](user-guide.md#configure-the-ruleset).

## HTTP routes

The public role serves `/health`, `/ready`, GitHub webhooks, the upload, OAuth and UI routes. The worker handles queued events and internal sweep and grant routes. The local `all` role also serves internal routes with an operator bearer token. The six legacy onboarding, discovery, overview, findings, graph and pull-graph APIs return HTTP 409 with JSON `{"error":"Archfit 3 reports and discovery are no longer available. Use Archfit 4 setup and requests."}` after authorization and any pull lookup. The setup, Home, policy, pull, request, answer and passkey APIs are separate. A page route alone does not guarantee its backing API is available.

## Deployment

See [cloud architecture](design/cloud/architecture.md) and [operations](design/cloud/operations.md). Build the embedded web app with `make web` before `make build`; `make all` runs formatting, lint, available web checks, tests and build.

### Configuration

The App reads environment variables. It does not read a `.env` file.
Secret variables name files. They do not contain secret values.
See [`.env.example`](../.env.example).

| Variable                                      | Function                                                                                                |
| --------------------------------------------- | ------------------------------------------------------------------------------------------------------- |
| `ARCHFIT_ADDR`                                | Listen address. Default `:8080`                                                                         |
| `ARCHFIT_BASE_URL`                            | Public URL and OIDC audience. No trailing `/`                                                           |
| `ARCHFIT_GITHUB_APP_ID`                       | GitHub App ID                                                                                           |
| `ARCHFIT_GITHUB_PRIVATE_KEY_FILE`             | GitHub App private-key file                                                                             |
| `ARCHFIT_GITHUB_WEBHOOK_SECRET_FILE`          | Webhook secret file                                                                                     |
| `ARCHFIT_GITHUB_WEBHOOK_SECRET_PREVIOUS_FILE` | Optional. The previous webhook secret file, set only during a rotation. Both secrets are accepted       |
| `ARCHFIT_GITHUB_OAUTH_CLIENT_ID`              | OAuth client ID. Not used by the `worker` role                                                          |
| `ARCHFIT_GITHUB_OAUTH_SECRET_FILE`            | OAuth client secret file. Not read by the `worker` role                                                 |
| `ARCHFIT_COOKIE_KEY_FILE`                     | 32-byte session-cookie key file. Not read by the `worker` role                                          |
| `ARCHFIT_OPERATOR_TOKEN_FILE`                 | Bearer token file for the operator routes. Not read by the `public` role                                |
| `ARCHFIT_STORE`                               | Store driver: `memory` (default) or `firestore`                                                         |
| `ARCHFIT_FIRESTORE_PROJECT`                   | Google Cloud project ID. Required with `firestore`                                                      |
| `ARCHFIT_FIRESTORE_DATABASE`                  | Firestore database ID. Default `(default)`                                                              |
| `FIRESTORE_EMULATOR_HOST`                     | Emulator `host:port`. Plain HTTP and no credentials                                                     |
| `ARCHFIT_ROLE`                                | `all` (default), `public`, or `worker`                                                                  |
| `ARCHFIT_QUEUE`                               | Local `all` role defaults to `inline`; `public` requires `cloudtasks`; `worker` never enqueues          |
| `ARCHFIT_TASKS_QUEUE`                         | `projects/<p>/locations/<l>/queues/<q>`, for `cloudtasks`                                               |
| `ARCHFIT_WORKER_URL`                          | Worker service URL, the invoker token audience. No trailing `/`. For `cloudtasks` and the `worker` role |
| `ARCHFIT_INVOKER`                             | Service account e-mail of queue and Scheduler dispatches. For `cloudtasks` and the `worker` role        |
| `ARCHFIT_APPROVED_ACCOUNTS`                   | Accounts served without operator approval. See `.env.example` for defaults                              |
| `ARCHFIT_OPERATORS`                           | GitHub user IDs that approve installations on `/admin`, comma-separated. Default `1898375,1788671`      |
| `GOOGLE_CLOUD_PROJECT`                        | Optional. Google Cloud project ID of the log trace field. Empty: log lines name no trace                |
| `K_REVISION`                                  | Set by Cloud Run. Written as `revision` on every log line                                               |

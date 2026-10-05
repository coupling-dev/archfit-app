# Architecture

This page explains the App boundary, the main request flows, and the Go package structure.
Read the [module specifications](../internal/module.md) for package contracts and test specifications.

## The main idea

The Archfit engine runs in customer CI and writes a report. The report is customer-attested evidence.
The App does not trust the report until it verifies the sender, revision, policy, and other gate inputs.

For each report, the App:

1. Verifies the GitHub Actions OIDC token and the upload envelope.
2. Reads policy, baseline, labels, and settings from the protected ref.
3. Compares the report with those trusted inputs.
4. Publishes feedback on the pull request.

The protected ref is the base branch of a pull request. For a default-branch push, it is the default branch.

## System context

```mermaid
flowchart LR
    USER(["Repository admin<br/>or policy owner"])
    SCHED(["External scheduler"])

    subgraph CI["Customer CI · report producer"]
        ACT["archfit-action<br/>runs the engine"]
    end

    subgraph GH["GitHub · trusted source"]
        PR["Pull request<br/>checks and comment"]
        REF["Protected ref<br/>policy, baseline, labels,<br/>settings, workflow"]
        OIDC["Actions OIDC keys"]
    end

    APP["archfit-app<br/>one Go binary"]
    LLM["Anthropic API<br/>optional"]

    ACT -->|"report + OIDC token"| APP
    PR -->|"webhook"| APP
    APP -->|"read trusted inputs"| REF
    APP -->|"verify token keys"| OIDC
    APP -->|"publish checks and comment"| PR
    USER -->|"OAuth browser session"| APP
    SCHED -->|"operator request"| APP
    APP -.->|"proposal on request"| LLM

    classDef core fill:#2f855a,stroke:#22543d,color:#ffffff
    classDef control fill:#e8eef7,stroke:#4a6fa5,color:#1a202c
    classDef nottrusted fill:#fff4d6,stroke:#b7791f,color:#1a202c
    classDef optional fill:#f3e8ff,stroke:#6b46c1,color:#1a202c,stroke-dasharray:4 3
    classDef neutral fill:#edf2f7,stroke:#718096,color:#1a202c

    class APP core
    class PR,REF,OIDC control
    class ACT nottrusted
    class LLM optional
    class USER,SCHED neutral

    style CI fill:#fff4d6,stroke:#b7791f,color:#1a202c
    style GH fill:#e8eef7,stroke:#4a6fa5,color:#1a202c
```

Solid arrows show request, read, or publication flow. The dotted arrow shows an optional AI proposal.

The App stores no report or source code. GitHub stores checks, comments, workflow runs, and the no-source
report artifact that the Graph page reads. The App stores entitlements, consent records, an LLM budget
counter, and short-lived publication leases in a key-value store. A lease makes the writes for one head SHA run
one at a time, on every instance, so an older publication cannot overwrite a newer one. Inside the lease an upload
reads its inputs again, so a decision from replaced inputs is never written. Without the store, the App
publishes nothing.

The repository split is:

- `archfit` owns the engine, CLI, images, and report schemas.
- `archfit-action` runs the engine in customer CI and uploads the report.
- `archfit-app` owns approved policy, report intake, the gate, and feedback.

## Packages

The App is one Go binary. The main internal packages are shown below.

```mermaid
flowchart TB
    APP["app<br/>config, wiring, routes"]
    CONTROL["control<br/>UI, onboarding,<br/>proposals, account, assist"]
    GATE["gate<br/>upload, verdict,<br/>feedback, sweep"]
    BASE["Shared packages<br/>wire · github · store"]

    APP --> CONTROL
    APP --> GATE
    APP --> BASE
    CONTROL -->|"gate facade"| GATE
    CONTROL --> BASE
    GATE --> BASE

    classDef core fill:#2f855a,stroke:#22543d,color:#ffffff
    classDef control fill:#e8eef7,stroke:#4a6fa5,color:#1a202c
    classDef neutral fill:#edf2f7,stroke:#718096,color:#1a202c

    class GATE core
    class CONTROL control
    class APP,BASE neutral
```

Arrows show permitted import groups. The `gate` arrow from `control` is a facade call.
The shared group contains `wire`, `github`, and `store`.

| Package   | Main responsibility                                                            | Child packages                                         |
| --------- | ------------------------------------------------------------------------------ | ------------------------------------------------------ |
| `app`     | Loads environment settings, builds packages, and registers routes              | none                                                   |
| `gate`    | Verifies uploads, decides outcomes, publishes feedback, and runs the sweep     | `ingest`, `inputs`, `verdict`, `feedback`, `sweep`     |
| `control` | Serves the UI and manages onboarding, proposals, AI requests, and entitlements | `account`, `assist`, `onboarding`, `proposal`, `ui`    |
| `wire`    | Defines versioned file and message formats                                     | `envelope`, `manifest`, `policy`, `report`, `settings` |
| `github`  | Holds GitHub API, authentication, webhook, and OIDC knowledge                  | `api`, `auth`, `oidc`, `webhook`                       |
| `store`   | Defines the tenant key-value interface and the memory driver                   | none                                                   |

The import rules keep the gate independent:

- `gate` never imports `control`, `app`, UI code, or LLM code.
- `control` uses only the `gate` facade.
- `wire` and `store` import no other internal package.
- A seam implementation can import the package that defines its interface.
- Tests run `go list -deps` to enforce the allowed imports.

## Report upload

The upload path accepts a report, verifies it, and publishes feedback in one request.

```mermaid
flowchart LR
    ACTION["archfit-action<br/>customer CI"]
    INGEST["gate/ingest<br/>authenticate and bind"]
    AUTH{"Sender and revision<br/>verified?"}
    REJECT["HTTP error<br/>nothing published"]
    INPUTS["gate/inputs<br/>read protected-ref inputs"]
    VERDICT["gate/verdict<br/>pure decision"]
    FEEDBACK["gate/feedback<br/>checks and comment"]
    GITHUB["GitHub<br/>checks, comment, receipt"]

    ACTION -->|"POST /v1/reports"| INGEST
    INGEST --> AUTH
    AUTH -->|"no"| REJECT
    AUTH -->|"yes"| INPUTS
    INPUTS -->|"trusted facts"| VERDICT
    VERDICT -->|"success, failure, or action_required"| FEEDBACK
    FEEDBACK -->|"publish"| GITHUB

    classDef core fill:#2f855a,stroke:#22543d,color:#ffffff
    classDef control fill:#e8eef7,stroke:#4a6fa5,color:#1a202c
    classDef nottrusted fill:#fff4d6,stroke:#b7791f,color:#1a202c
    classDef neutral fill:#edf2f7,stroke:#718096,color:#1a202c

    class INGEST,INPUTS,VERDICT,FEEDBACK core
    class GITHUB control
    class ACTION nottrusted
    class AUTH,REJECT neutral
```

Arrows show the order of the upload request. Authentication defects return an HTTP status.
After authentication, defects become gate reasons and appear in GitHub feedback.

`gate/ingest` verifies the token, envelope, payload digest, report format, run, pull request, and revision.
It reads the report with the key set of the engine release that the envelope claims.
`gate/inputs` reads the protected-ref files and owner approval. `gate/verdict` performs no I/O and no AI call.
The upload handler has a 55-second deadline. The target is feedback within 60 seconds.

The upload body is at most 5 MiB. A larger body gets `413 payload_too_large`, and the App publishes nothing.
The App does not read part of a report.

## Baseline upload

The baseline is a file on the protected ref. It changes only through a pull request that an architecture owner
approves. The App never commits it to the default branch.

```mermaid
flowchart LR
    ACTION["archfit-action<br/>mode: baseline on main"]
    ONB["control/onboarding<br/>receive and check"]
    OK{"Approved run at the main head,<br/>approved identity, protected<br/>policy and labels?"}
    REJECT["HTTP error<br/>nothing written"]
    PR["Pull request from<br/>archfit/baseline"]
    OWNER["Owner approves<br/>the exact head"]
    MAIN["Protected ref"]

    ACTION -->|"POST /v1/baselines"| ONB
    ONB --> OK
    OK -->|"no"| REJECT
    OK -->|"yes"| PR
    PR --> OWNER
    OWNER -->|"merge"| MAIN

    classDef core fill:#2f855a,stroke:#22543d,color:#ffffff
    classDef control fill:#e8eef7,stroke:#4a6fa5,color:#1a202c
    classDef nottrusted fill:#fff4d6,stroke:#b7791f,color:#1a202c
    classDef neutral fill:#edf2f7,stroke:#718096,color:#1a202c

    class ONB core
    class PR,MAIN control
    class ACTION nottrusted
    class OK,REJECT,OWNER neutral
```

Arrows show the order of the baseline flow. Only the merge writes the protected ref.

`control/onboarding` uses the gate's `Receiver`, so the token, envelope, run and digest checks are the same as for a
report. Then it checks, in this order, and the first failure decides:

1. The run has no problem and ran on the default branch.
2. The run checked out the current head of the default branch. Every later read is at that commit.
3. The run used the workflow that `.archfit-app.yaml` names.
4. The engine identity is a row of the locked manifest.
5. The file is a structurally valid `archfit.baseline.v2` baseline of at most 1 MiB, and its measurement profile is
   the profile of that manifest row.
6. Its `state.config_hash` is the digest of the protected `.archfit.yaml`.
7. The envelope's `labels_digest` is the digest of the protected `.archfit-labels.yaml`, or empty when there is none.

A file equal to the protected baseline opens nothing. Otherwise the App opens a draft pull request from
`archfit/baseline` into the default branch that changes only `.archfit-baseline.json`, with the uploaded bytes. A new
upload updates the same branch and pull request and rewrites its body. The body counts the accepted findings added
and removed, lists them by rule ID and finding ID, names the run, engine identity and digests, and states that an
architecture owner must approve the exact head commit. The gate's protected-path rule then governs the merge.

Today the Action captures with plain `archfit baseline`, which accepts all current eligible debt. The added count in
the body is what keeps that visible to the owner. When the engine ships `archfit baseline --reanchor`, a re-capture
after an engine or profile change should use it, so that the pull request accepts no new finding by construction.

### Baseline upload contract

The Action must implement this contract in `mode: baseline` when `endpoint` is set:

- Request: `POST <endpoint>/v1/baselines`.
- Headers: `Authorization: Bearer <OIDC token>` with the endpoint as audience, `X-Archfit-Envelope` with the
  one-line `archfit.report-envelope.v1` JSON, and `Content-Type: application/json`.
- Body: the exact bytes of the `.archfit-baseline.json` that `archfit baseline` wrote, at most 1,048,576 bytes.
- Envelope: `kind` is `baseline`; `event` is `workflow_dispatch` (or `push` to the default branch); `pull_request` is
  `0`; `base_sha` and `merge_base_sha` are `""`; `head_sha` is the checked-out default-branch commit; `baseline_digest`
  is `""`; `labels_digest` is the bare hex SHA-256 of the labels file the capture read, or `""` without one;
  `payload_digest` is `sha256:<hex>` of the body; the engine identity fields are those of the pinned image.

| Status        | Body                                    | Meaning                                                                  |
| ------------- | --------------------------------------- | ------------------------------------------------------------------------ |
| 200           | `{"pull_request": <n>, "url": "<url>"}` | The baseline pull request was opened or updated                          |
| 200           | `{"unchanged": true}`                   | The capture equals the protected baseline. Nothing was written           |
| 400           | `{"error": "<code>"}`                   | Malformed or unbound upload, or `baseline_invalid`. Final                |
| 401           | `{"error": "<code>"}`                   | `token_missing` or `token_invalid`. Final                                |
| 403           | `{"error": "<code>"}`                   | Not eligible or not bound to the protected inputs (below). Final         |
| 409           | `{"error": "<code>"}`                   | `stale_head` or `stale_attempt`. Superseded: dispatch the capture again  |
| 413           | `{"error": "<code>"}`                   | `baseline_too_large` above 1 MiB, `payload_too_large` above 5 MiB. Final |
| 429, 503, 500 | `{"error": "<code>"}`                   | `rate_limited`, `oidc_unavailable` or `internal`. Retry                  |

The `400` codes are `method_not_allowed`, `envelope_missing`, `envelope_invalid`, `kind_mismatch`, `digest_mismatch`,
`unsupported_event`, `envelope_mismatch`, `fork` and `baseline_invalid`. The `403` codes are `repository_mismatch`,
`not_installed`, `run_not_found`, `run_mismatch`, `not_default_branch`, `settings_missing`, `workflow_not_approved`,
`unknown_engine_identity`, `policy_missing`, `policy_mismatch` and `labels_mismatch`.

## Gate outcomes

The `verdict` package has three conclusions. It cannot represent `neutral` or `skipped`.
The first reason sets the conclusion. Later reasons remain in feedback.

| Conclusion        | Use                                                | Categories                               |
| ----------------- | -------------------------------------------------- | ---------------------------------------- |
| `success`         | Trusted evidence has no blocking finding           | `architecture`                           |
| `failure`         | Trusted evidence has a blocking finding            | `architecture`                           |
| `action_required` | Evidence, governance, or subscription needs action | `evidence`, `governance`, `subscription` |

The App publishes two checks:

- `archfit/state` is advisory. It is published for each accepted report upload. It uses `neutral` for advisory
  attention and for findings it does not enforce, and `action_required` whenever the gate result is
  `action_required`. Errors and unverifiable evidence are never `neutral` or `skipped`.
- `archfit/gate` is the blocking check. It is published only when the settings file contains `gate: required`.
- A `needs_attention` verdict with nothing else to act on is `success` with the reason `attention_only`, never
  `healthy`. A required rule whose selector matches nothing is `action_required` with `policy_defect`.

Each published check carries a receipt with the run ID, report digest, policy digest, baseline digest, and
labels digest. The sweep compares the receipt with the protected ref. See the [reason catalog](user-guide.md#reason-codes)
for recovery actions.

## Engine releases

An engine upgrade needs no flag day. Three `wire` packages share the rule:

- `wire/manifest` approves engine images by rows. Rows of two releases can exist together during an upgrade:
  the release that new workflows pin and the release before it.
- `wire/report` reads a report with the key set of its engine release. Each release has its own key set, and a
  key of a later release is an unknown key. A release that is not in the list gives `report_invalid`.
- The gate decides only from fields that every listed release defines: the verdict, the hard gates, the
  unevaluated required rules, the config hash, the measurement-profile version, and the gate reference.
- `wire/policy` keeps one engine config schema, the schema of the release that new workflows pin.

The App reads v2.3.1 reports. It is ready for v2.4.0 reports, because v2.4.0 adds no report key. The manifest
approves v2.3.1 only, until the v2.4.0 images are published.
Tests in `internal/wire` stop a manifest row for a release that the decoder cannot read, that has no real capture
in the fixture corpus, or whose config schema is not pinned when new workflows move to it.

## Webhooks and the sweep

A webhook starts immediate work. The sweep repairs work after a lost webhook or a changed protected ref.

```mermaid
flowchart LR
    EVENT["Webhook or scheduler"]
    READ["Read receipt and<br/>protected-ref inputs"]
    FRESH{"Receipt is current?"}
    KEEP["Keep feedback"]
    ACTION["Set pending or<br/>action_required"]
    RERUN["Request workflow rerun<br/>when recovery allows"]
    UPLOAD["New report upload"]
    UPDATE["Update checks and comment"]

    EVENT --> READ
    READ --> FRESH
    FRESH -->|"yes"| KEEP
    FRESH -->|"no or missing"| ACTION
    ACTION --> RERUN
    RERUN --> UPLOAD
    UPLOAD --> UPDATE

    classDef core fill:#2f855a,stroke:#22543d,color:#ffffff
    classDef control fill:#e8eef7,stroke:#4a6fa5,color:#1a202c
    classDef nottrusted fill:#fff4d6,stroke:#b7791f,color:#1a202c
    classDef neutral fill:#edf2f7,stroke:#718096,color:#1a202c

    class READ,ACTION,RERUN core
    class UPDATE,KEEP neutral
    class EVENT,UPLOAD control
    class FRESH nottrusted
```

The arrows show recovery flow. The sweep does not compute a new report because the App stores no report.

| Event                                                    | Gate action                                                                                                                                                                  | Control action                        |
| -------------------------------------------------------- | ---------------------------------------------------------------------------------------------------------------------------------------------------------------------------- | ------------------------------------- |
| New pull request head                                    | Set a pending required check or refuse a fork                                                                                                                                | None                                  |
| Push to the default branch that changes a protected path | Sweep that repository                                                                                                                                                        | None                                  |
| Review change                                            | Set `owner_approval_missing` when a `success` or `failure` check lost its owner approval; rerun when the last reason was `owner_approval_missing` and no approval is missing | None                                  |
| Installation added or removed                            | None                                                                                                                                                                         | Start the trial or delete tenant data |

The app handler runs gate and control webhook work independently. A control error does not change gate work.

The sweep inspects open pull requests for these conditions:

- A pending required check is missing.
- No report arrived within 60 minutes.
- An owner approval that a `success` or `failure` check relied on is gone.
- The policy, baseline, or labels changed on the protected ref.
- A waiver expires within seven days or has expired.
- The subscription changed.

An owner approval can end without a new commit: the owner dismisses the review or requests changes, the owner leaves
the settings file on the protected ref, or the pull request starts to change a protected path. Edits to the settings
file and the workflow file change no digest in the receipt, so the receipt digests cannot show this. The review event,
or the sweep when the event is lost, then sets the required check to `action_required` with `owner_approval_missing`.
It requests no rerun: no run can pass until an owner approves the head again, and that approval triggers the rerun.
The rerun also follows when no approval is needed any more, for example after a base retarget.
The review event and the sweep read the check, read the inputs, decide, and write all inside the head's publication
lease. So a newer run that turned green while they waited is the result they downgrade, and an approval that
arrives meanwhile is handled by its own review event after the write. An upload decided before the withdrawal reads
its inputs again inside the lease, writes nothing, and answers `503`. Without the Store nothing is written, and the
check keeps its current conclusion until the Store is back and the next review event or sweep pass runs. In
advisory mode there is no required check, and neither path changes the advisory `archfit/state` check.

The sweep reruns the customer workflow after stale inputs, an expired waiver, late owner approval, or restored
subscription. A missing upload becomes `action_required`. A fresh report is required for a new `success` result.

## Trust rules

These invariants keep repository authors from weakening the gate:

- Read policy, baseline, labels, settings, owners, and workflow from the protected ref.
- Verify the policy digest against the report.
- Accept a head policy only when an architecture owner approved the exact head SHA.
- Treat missing, incompatible, or unverifiable evidence as not successful.
- Describe report provenance as `customer-attested`, never as `verified`.
- Keep LLM output, billing, and UI code off the gate path.
- Resolve tenant identity from a verified webhook, OIDC claim, or OAuth session.
- Never commit to the protected ref. A captured baseline arrives only as a pull request that an owner approves.
- Log IDs, digests, outcomes, and timings only.

The protected paths are:

- `.archfit.yaml`
- `.archfit-baseline.json`
- `.archfit-labels.yaml`
- `.archfit-app.yaml`
- The workflow file named by `.archfit-app.yaml`

A change to a protected path needs approval from an architecture owner on the exact head SHA.

## HTTP routes

The routes come from [`internal/app/handler.go`](../internal/app/handler.go) and
[`internal/control/ui/ui.go`](../internal/control/ui/ui.go).

### Service routes

| Method and route        | Owner     | Authentication             |
| ----------------------- | --------- | -------------------------- |
| `GET /healthz`          | `app`     | None                       |
| `POST /v1/reports`      | `gate`    | GitHub Actions OIDC token  |
| `POST /v1/discoveries`  | `control` | GitHub Actions OIDC checks |
| `POST /v1/baselines`    | `control` | GitHub Actions OIDC checks |
| `POST /webhooks/github` | `app`     | `X-Hub-Signature-256`      |
| `POST /internal/sweep`  | `app`     | Operator bearer token      |
| `POST /internal/grants` | `app`     | Operator bearer token      |

### UI routes

| Method and route                                      | Authentication                                  |
| ----------------------------------------------------- | ----------------------------------------------- |
| `GET /`                                               | None. Shows the landing page or repository list |
| `GET /auth/login` and `GET /auth/callback`            | None                                            |
| `POST /auth/logout`                                   | GitHub OAuth session and CSRF token             |
| `GET /static/{path}` and `GET /schema/policy.json`    | None                                            |
| `GET /r/{o}/{n}`                                      | Session with repository `read` permission       |
| `POST /r/{o}/{n}/onboarding`, `/discover`, `/consent` | Session with repository `admin` permission      |
| `GET /r/{o}/{n}/policy` and `/graph`                  | Session with repository `read` permission       |
| `POST /r/{o}/{n}/policy` and `/policy/ready`          | Session with repository `write` permission      |
| `POST /r/{o}/{n}/assist`                              | Session with repository `write` permission      |

## Platform seams

The App keeps platform-dependent code behind three interfaces.

| Seam       | Current driver                       | Current behavior                    |
| ---------- | ------------------------------------ | ----------------------------------- |
| `Store`    | `memory`                             | Data is lost when the process stops |
| `Sandbox`  | `none`                               | Managed discovery is not available  |
| `Provider` | `anthropic` when a key is configured | Managed AI is optional              |

A new driver changes the composition in `app`. The gate contracts do not change.

## Deployment

The App is not deployed. The target is Google Cloud. The [cloud design](design/cloud/architecture.md) owns the deployment design.
This page lists only what any host must provide.
A host must provide these runtime capabilities:

- Run one static Go binary in a container.
- Provide a key-value driver that passes the `store/storetest` suite. When more than one instance runs, its
  `CompareAndSwap` must be atomic across all of them.
- Call `POST /internal/sweep` with an external scheduler.
- Provide a sandbox driver before managed discovery is enabled.

### Configuration

The App reads environment variables. It does not read a `.env` file.
Secret variables name files. They do not contain secret values.
See [`.env.example`](../.env.example).

| Variable                             | Function                                               |
| ------------------------------------ | ------------------------------------------------------ |
| `ARCHFIT_ADDR`                       | Listen address. Default `:8080`                        |
| `ARCHFIT_BASE_URL`                   | Public URL and OIDC audience. No trailing `/`          |
| `ARCHFIT_GITHUB_APP_ID`              | GitHub App ID                                          |
| `ARCHFIT_GITHUB_PRIVATE_KEY_FILE`    | GitHub App private-key file                            |
| `ARCHFIT_GITHUB_WEBHOOK_SECRET_FILE` | Webhook secret file                                    |
| `ARCHFIT_GITHUB_OAUTH_CLIENT_ID`     | OAuth client ID                                        |
| `ARCHFIT_GITHUB_OAUTH_SECRET_FILE`   | OAuth client secret file                               |
| `ARCHFIT_COOKIE_KEY_FILE`            | 32-byte session-cookie key file                        |
| `ARCHFIT_OPERATOR_TOKEN_FILE`        | Bearer token file for `/internal/*`                    |
| `ARCHFIT_ANTHROPIC_KEY_FILE`         | Optional Anthropic key file. Empty disables managed AI |
| `ARCHFIT_STORE`                      | Store driver. Only `memory` exists                     |
| `ARCHFIT_SANDBOX`                    | Sandbox driver. Only `none` exists                     |

### Release blockers

- The locked manifest contains a placeholder Action commit (`action_ref`). Its image digests are the published
  v2.3.1 digests. A generated workflow pins an Action commit that does not exist while the placeholder remains.
  `ARCHFIT_RELEASE=1` makes the release test fail.
- `archfit-action` is not published. Its `feat/wave1-run-engine` branch sends the envelope format of this service, runs
  on `pull_request`, `push`, and `workflow_dispatch` only, and accepts `mode: report | discovery | baseline`. The
  manifest `action_ref` takes the commit of its first release.
- On that branch, from commit `04fce3f`, `mode: baseline` with an `endpoint` uploads the captured
  `.archfit-baseline.json` to `/v1/baselines` as the [baseline upload contract](#baseline-upload-contract) states:
  envelope `kind: baseline` with an empty `baseline_digest`, a 1 MiB cap, the pull request URL, `unchanged`, or the
  error code in the step summary, and a failed job with "dispatch the capture again" on `409 stale_head` or
  `stale_attempt`. It still keeps the file as a workflow artifact. Its vendored envelope schema pins revision `c0b1e97`
  of this repository and is byte-identical to `internal/wire/envelope/envelope.v1.schema.json`.
- The `action-conformance` CI job compares the Action's vendored `schema/envelope.v1.schema.json` with
  `internal/wire/envelope/envelope.v1.schema.json` at the manifest `action_ref`. While `action_ref` is the placeholder,
  the `action-pin` job reports "not verified" in a warning and the step summary, and `action-conformance` shows as
  skipped. If `archfit-action` stays private, the repository needs an `ARCHFIT_ACTION_TOKEN` secret with read access.
- No GitHub App is registered.
- The Monaco editor bundle is not built. The policy editor uses a plain text area.

# App composition root

**Path**: internal/app/
**Parent**: [internal](../module.md)

## Purpose

Load environment configuration and file-backed secrets, select Store/Queue implementations, compose gate/control,
serve role-specific routes and isolate webhook, upload, worker and operator failures.

## Configuration and composition

`Load` and `Config.validate` reject invalid settings without echoing secret values. Secret variables name files.
`Run` constructs GitHub auth/API/OIDC clients, Store, Queue and shared installation admissions.
The gate receives no control-plane dependency. Control receives gate view/answer/owner/setup ports, OAuth,
cookie configuration, Store and admission. Worker control needs no browser credentials.

Store drivers are memory and Firestore over REST. Queue drivers are inline and Cloud Tasks.
The v3 Sandbox driver, `ARCHFIT_SANDBOX`, discovery Receiver and legacy sweep configuration are removed.
The process uses standard net/http and shuts down gracefully after context cancellation.

## HTTP roles

| Route | all | public | worker |
| --- | --- | --- | --- |
| GET /health, GET /ready | yes | yes | yes |
| POST /v1/reports | envelope 2 | envelope 2 | 404 |
| POST /webhooks/github | inline halves | queued halves | 404 |
| UI, authentication and /api | yes | yes | 404 |
| POST /internal/sweep | operator bearer | 404 | worker identity |
| POST /internal/grants | operator bearer | 404 | worker identity and X-Archfit-Operator bearer |
| POST /internal/webhooks | 404 | 404 | worker identity |

`/v1/discoveries`, `/v1/baselines` and `/v1/reanchors` return 404 for every method,
including when the UI is mounted at /. The control UI also returns 404 for `/schema/policy.json`.
All other page routes remain mounted. The upload decoder itself refuses envelope 1.

Public webhook intake checks signatures and parsing before enqueueing gate/control halves.
Both halves are attempted; a refused enqueue is 503, never an inline fallback. Accepted intake is 202.
Workers verify the signature again and use `X-Archfit-Half` to select the isolated handler.
Transient errors retry (503); permanent/ignored work is acknowledged. A failing control half cannot prevent
the gate half from running.

All-role operator jobs require the configured bearer token. Worker routes verify Google ID-token audience,
issuer and invoker email. Middleware logs identifiers, outcomes, counts and durations, not payload or secrets.
Readiness is separate from liveness and probes the dependencies selected for the process role.

## Checks

`handler_test.go` and `role_test.go` verify route presence/removal, auth, queue dispatch and isolation.
`app_test.go` checks startup, real upload-route refusal, optional dependencies and graceful shutdown.
Configuration, secrets, readiness, middleware and worker identity have focused test files.
Run `make all` before pushing.

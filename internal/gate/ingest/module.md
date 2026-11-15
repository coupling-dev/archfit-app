# Upload authentication

**Path**: internal/gate/ingest/
**Parent**: [gate](../module.md)

## Contract

`NewV2` constructs a `Receiver4`; `ReceiveV2` authenticates one envelope 2 upload and returns
`Upload4` or an error. The payload cap is 5 MiB. The only accepted workflow path is
`.github/workflows/archfit.yaml`.

The receiver strictly decodes the envelope and checks its payload digest before OIDC verification.
It resolves the installation/repository and run from verified claims and GitHub, checks admission,
workflow identity and attempt, and binds the event to a pull request, default-branch push or queued
merge group. Stale attempts, moved heads/groups and newer runs are rejected. Unreadable authority
never authenticates an upload.

After authentication, envelope mismatches, unsupported events, forks and invalid result payloads
become ordered `Problem` values. The gate makes them unknown, never neutral or skipped.
The engine may not upload the App-owned waiting status. `Binding4` contains GitHub authority;
`Upload4.Envelope` and `Result` remain claims.

`Rejection` carries a stable HTTP status and code. Logs contain IDs, digests, problem codes and
durations, not repository content, tokens, envelope JSON or decoder error text.

Envelope v1 authentication/dispatch and discovery/baseline/reanchor bindings are removed.
Shared authentication, pull resolution, queue binding and strict-envelope helpers remain for v4.

## Checks

`receive4_test.go` covers valid events, workflow/OIDC binding, stale runs and attempts, payload/result
mismatches and refusal of envelope 1 before token verification.

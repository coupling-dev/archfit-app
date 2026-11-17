# Gate admission and targets

**Path**: internal/gate/inputs/
**Parent**: [gate](../module.md)

## Contract

`Target` identifies the repository and head a gate service operates on. It is shared by the
v4 publisher, request and owner paths; it contains no uploaded report or parsed policy.

`Admissions` serves accounts configured through `NewAdmissions` or carrying an installation
`Admission` record. Configured account matching is case-insensitive. An unreadable Store record
returns an error: callers must retry rather than treat an approved installation as unapproved.
`Record` exposes approval metadata to the operator UI. `AdmissionKey` is `admission`.
`WithAdmissionMemo` makes `Record` (and so `Admitted`) read each installation once for the rest of one request,
failed reads included. Install it only on a request that changes no admission; it is never shared between requests.

`Entitlement` and `EntitlementKey` remain the account-to-gate storage contract.
Control/account writes these records; this package calls no billing or control-plane code.

The Archfit 3 protected-settings resolver, baseline/labels digest lists, waiver expiry reader,
merge-group protected-file comparison and legacy protected-path helpers are removed.
The v4 upload resolves its schema 3 policy and immutable workflow inputs in gate/upload4.go.

## Checks

`admission_test.go` covers configured accounts, stored approvals, tenant keys, missing records,
case folding and Store failures. `admission_memo_test.go` covers the request memo: one read per installation, concurrent
callers, failed reads, and no sharing between Admissions or requests.

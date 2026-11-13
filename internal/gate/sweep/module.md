# Re-check helpers

**Path**: internal/gate/sweep/
**Parent**: [gate](../module.md)

## Contract

The hourly Archfit 4 reconciliation pass is in gate/sweep4.go. This child owns its shared re-check
helpers and the count-only `Summary` returned by the gate facade.

`Rerun` requests an Actions re-run only when the stored run is the latest relevant completed attempt
of the live head, is younger than 30 days, reads the policy from the target tip, and the target changed
only in `.archfit.yaml` since the event's base. A queued/in-progress run is left in flight.
`RerunResult` and `Unmet` distinguish a request, in-flight work and a condition that cannot hold.
Read/request errors are returned, not converted to clean state.

`AfterAnswer` refreshes pull requests waiting on a consumed proposal: re-run when permitted, otherwise
update a same-repository branch with an expected head, or tell its author why no update was possible.
The chosen path is recorded in the pull record and on request issues. Repeated deliveries are idempotent.

The Archfit 3 sweeper, protected-input rule table, comparability/waiver rules, module-graph overlap cache
and legacy sweep configuration are removed. No v4 result is sent through those rules.

## Checks

`unit_test.go` covers re-run boundary conditions. `recheck_test.go` covers answer handoffs, retries and
per-pull paths. The facade's sweep4 tests cover reconciliation and publication races.

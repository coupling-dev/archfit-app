# Gate feedback

**Path**: internal/gate/feedback/
**Parent**: [gate](../module.md)

## Contract

`New4` provides `Publisher4.Publish4` and `Withdraw4`. A `Publication4` writes archfit/gate on
its head and one pull request comment, using the engine result's status and records. It writes no
archfit/state check. Pass is success; fail is failure (neutral only in advisory mode); waiting is
in_progress; unknown or an unrecognized status is action_required.

Every publication holds the installation/head Store lease. `Current` rechecks live authority
inside that lease; false returns `ErrStaleInputs` without writing. `Withdraw4` accepts only unknown
and can take the live check receipt when the caller lacks a matching pull record.
Uncertain writes keep the lease until expiry; a known-unwritten call releases it. Work deadlines
end before lease expiry and releases cannot delete another holder's lease.

The external ID is `v2:<run>:<attempt>:<digest>`. Historical v1 external IDs and comment head markers
are read only to prevent overwriting newer existing publications; this is not an envelope v1 upload path.
`Receipt` retains only run and attempt for ordering and re-checks.
Annotations are batched; newer runs and comments belonging to another current head are not overwritten.
Producer text is bounded and made inert for Markdown/annotations.

Archfit 3 report rendering, comparability reasons, baseline/labels receipts, waiver warnings,
repair issues and legacy publisher methods are removed.

## Checks

`view_test.go` and `testdata/golden4/` cover result rendering. `publish4_test.go` and
`withdraw4_test.go` cover status mapping, ordering, batching and authority races.
`lease_test.go` exercises the retained lease failures, cancellation, takeover and release behavior through v4.

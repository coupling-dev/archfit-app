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

The shared result model also supplies `BuildPage(result.Result) Page`: status, status line,
ordered `Record` values, author and owner lines. The comment renders those same page records.
Each record retains its next step; `ShowNext` is false when the author line already names it.
The author line names at most ten requests. Later requests keep their next-step lines.
A fix step with a note or an additional option remains visible in both forms.
`Request` binds an error's wait step to its issue number, including an expired exception.
Records omit upload-only metadata. Page strings remain untrusted text, not HTML.
The builder renders the App's judged result; it does not verify evidence or grant authority.
External callers use the gate facade's `BuildPullView`, never this child package.

Archfit 3 report rendering, comparability reasons, baseline/labels receipts, waiver warnings,
repair issues and legacy publisher methods are removed.

## Checks

`view_test.go` uses one fixture set for comment, check title, summary, annotations and page JSON
in `testdata/golden4/`, with wording checks on every form. It covers design 8.1 and the three
12.5 comments byte for byte. The non-uploadable `can` example contributes its text record to a
fail fixture. `page_test.go` and `page_bounds_test.go` check request binding, next-step display,
text bounds and isolation from the source result. `publish4_test.go` and
`withdraw4_test.go` cover status mapping, ordering, batching and authority races.
`lease_test.go` exercises the retained lease failures, cancellation, takeover and release behavior through v4.

## Publication receipts

For a pull request, publication invalidates its old receipt with a pending marker inside the head lease.
A confirmed receipt is written only after both GitHub writes succeed and neither was skipped. It contains
only an App-generated result; successful engine results clear the marker and remain in the artifact. A
receipt failure is logged, leaving the marker pending rather than retrying GitHub. Run/attempt ordering and
a pending token prevent older or superseded writers from confirming another publication. Oversize result
bodies are omitted, not truncated. Upload also stages a pending marker atomically with the pull record,
so lease acquisition failure cannot expose a result that was not published.

An upload deliberately excluded from publication (such as the App's policy proposal or an unsupported
event) records an `unpublished` marker instead of a pending marker; its engine result never becomes a verdict.

An upload's pending marker has a token made before the atomic pull-record swap. Its publisher must claim that
exact token; a queued writer cannot replace another upload staged for the same run and attempt. An ordinary
request publication cannot claim an upload-staged marker; an authoritative App-generated withdrawal can
supersede it so an abandoned upload cannot preserve an old success. When the shared-head check or pull comment is intentionally
skipped because a newer publication exists, feedback records a superseded marker, not a perpetual pending state.

Pending request publications retain source `request` and their payload digest. A matching request retry may
resume that pending write after its authority check; it cannot resume a withdrawal or an upload-staged marker.

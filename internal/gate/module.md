# Gate

**Path**: internal/gate/
**Submodules**: approvals, drift, expiry, feedback, ingest, inputs, markdown, proposals, records, requests, sweep

## Purpose

The facade owns the Archfit 4 fail-closed merge gate, change control and reconciliation.
It never imports control, app or an LLM. Provenance authenticates the sender; result contents remain claims.

## Entry points

- `UploadHandler` serves POST /v1/reports through envelope 2 only. Authentication/binding is in ingest;
  upload4.go compares the schema 3 policy digest, immutable protected target, workflow, engine identity and
  recorded approval authority before publishing a result. Invalid/unverifiable evidence becomes unknown.
- `HandleEvent` handles request comments/issues, closed pull requests and policy/workflow pushes.
  An opened/synchronized pull into the default branch retires spent proposal-head authority.
  Legacy pending/fork, merge-group protected-settings and source-graph webhook rules are removed.
- Request views, previews and answers use requests/proposals. Approval records and policy proposals are
  separate from GitHub reviews; a push alone approves nothing.
- Owner actions provide first policy/workflow approval and confirm/revert outside-flow changes.
  The facade's owner/setup ports serve the control plane without exposing gate children.
- `Sweep` runs only sweep4.go, over admitted installations and schema 3/recorded repositories.

## Reconciliation and safety

The hourly pass repairs proven policy transitions and consumed-proposal handoffs, warns/enforces approved
exception expiry, refreshes stale accepted policy results, marks missing uploads unknown after the timeout,
refreshes ten-day waiting checks and retires closed pull indexes. Ambiguous history approves nothing.
Unverifiable refresh artifacts become unknown; transient authority failures retry.
Publication leases recheck live head, target, approval, request coverage and record authority.
A force-pushed-back head without a matching record is withdrawn using its live check receipt.

Admissions and entitlement contracts are republished from inputs. Summary is republished from sweep.
Envelope v1, legacy protected-settings resolution, baseline/labels digests, comparability decisions,
waiver rules, report repair issues and v3 sweep configuration are removed.

## Contracts and checks

Child module.md files own their contracts; wire contracts never double as domain/storage records.
The upload4, requests, answers, approvals, owner, drift and sweep4 test suites cover retained behavior.
`../imports_test.go` checks dependency direction and removed packages.

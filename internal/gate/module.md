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

## Pull-request page form

`pullview.go` republishes `PullView`, `PullRecord`, `PullNext` and `PullBalance`.
`BuildPullView(result.Result) PullView` renders the feedback model as the status line,
ordered records, author and owner lines. Each waiting error names its request number;
`ShowNext` controls the next step already covered by the author line.
Callers establish the result's authority before rendering; the builder performs no
artifact retrieval or evidence verification. Control imports this facade, not feedback.
`pullview_test.go` compares the facade's JSON with the shared feedback goldens.

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

## Home read façade

`home.go` publishes `HomeViews.Home(context.Context, api.Repo) (HomeState, error)` and
`NewHome(gh, store, admissions) HomeViews`. It checks admission before reading repository data, reads the
default-branch tip and policy at that immutable commit, and compares its digest with the recorded approval.
A recorded outside-flow change or an unmatched digest is exposed as outside-flow state, never verified policy.

`HomeState` carries the target policy, open requests and their records, the latest stored drift's rule/module
pairs, evidence completeness, drift and expiry issue numbers, touched deny-rule pairs, and workflow pull numbers.
It does not publish storage keys. Requests come from the v4 request index, not labels. Pull errors count only
for a currently open, same-repository pull into the default branch whose head matches its stored result.
Workflow changes are read from those same open pulls; a workflow already approved at that head is omitted.
Read failures are returned, not turned into a clean answer. The setup state still belongs to `SetupViews`
and `control/onboarding`; Home does not implement onboarding again.

A missing or invalid target policy with recorded outside-flow state is returned without a map, so the owner can revert it. Proposed policy files are read at pull head only for workflow-action eligibility.

## Pull read façade

`NewPullViews` reads a pull record, live request index and check state for the authorized repository. A current
App-generated publication receipt takes precedence over the artifact; pending, omitted or corrupt receipts
never produce a verdict. Otherwise the run artifact is fetched with a bound and its exact bytes are checked
against the stored digest before decoding. Missing artifacts, digest mismatches and publication pending
states remain distinct. `BuildPullView` is the only result-to-page form; only coverable required-policy or already-waiting engine
results are covered from the live request index. Advisory failures stay fail. The pull head must match the stored record.

A missing-upload receipt remains readable before the first pull record exists. References to outside-flow
issues are plain text, not request links; only the shared model's explicit request numbers become links.
## Request page view

`RequestView` reads the request and exposes declared volatility, layers outer-first and structured deny/exception dependencies from one target-policy snapshot. When an answered request has an approval pointer, the facade reads the recorded answer; older records without it provide no answer detail. The view includes the request issue link, which remains the source for the owner and passkey.

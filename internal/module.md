# Archfit App

**Path**: internal/
**Parent**: none (root)
**Submodules**: app, wire, github, store, gate, control

## Purpose

Archfit App is one Go service that turns a customer-executed Archfit engine report into
trustworthy GitHub feedback: an advisory `archfit/state` check, one updated PR comment, and
an explicitly enabled, fail-closed `archfit/gate` check. Around that gate path it provides
onboarding, policy editing, AI assistance on request, and entitlements. Without it, an
engine report is only a self-reported file: nothing binds it to the approved policy on the
protected ref, to an architecture owner's approval, or to the revision it claims to describe.

This file is also the architecture overview. Requirements: `docs/design/decisions.md`
(wins on conflict), `docs/design/product.md` §2 Required, `docs/design/trust-and-reporting.md`,
and the invariants in `AGENTS.md`. Choices made without an owner answer are marked **[default]**.

## Functional Responsibilities

- Verify and bind uploaded reports: GitHub Actions OIDC, authoritative revision binding,
  trusted inputs verified by digest against the protected ref, approved engine identity.
- Decide the gate outcome with no LLM, billing call, or UI on the path; always conclude
  `success`, `failure`, or `action_required`; never `neutral` or `skipped`.
- Publish `archfit/state`, `archfit/gate` (when `gate: required`) and one PR comment, idempotently.
- Enforce architecture-owner approval on the exact head SHA for policy, baseline, settings and
  workflow changes, including atomic code-and-policy PRs.
- Recover from missed events with one periodic sweep; schedule around waiver expiry and base policy changes
  by re-running the customer's workflow.
- Onboard repositories in customer-executed mode (two PRs) and managed mode (sandbox job after consent).
- Serve the UI: GitHub OAuth login, organization overview without a score, YAML editor with schema
  completion, drafts as draft PRs, read-only graph.
- Offer AI assistance only on explicit request: managed (one provider, consent, hard budget) or
  customer mode (workflow dispatch). Proposals are always reviewable diffs.
- Keep entitlements (manual grants, trial, grace, suspension), consent records and the LLM budget
  counter in a KV store; delete them on uninstall. Besides them only short-lived publication leases, which
  serialize the GitHub writes of one head SHA across instances. Store nothing else: no reports, no source, no queues.

## Subdomain Classification

The system as a whole is a **core** product. Per area:

| Area                                                 | Subdomain                                               | Volatility                                                            | Rationale                                                                         |
| ---------------------------------------------------- | ------------------------------------------------------- | --------------------------------------------------------------------- | --------------------------------------------------------------------------------- |
| `gate`                                               | Core — the part that makes the product worth paying for | High                                                                  | Trust rules, outcome mapping and feedback will be tuned with every pilot          |
| `control/onboarding`, `control/ui`, `control/assist` | Core-adjacent product surface                           | High                                                                  | Adoption UX; product.md leaves usability targets open                             |
| `control/account`                                    | Supporting — needed, not differentiating                | Moderate                                                              | Trial and commercial terms are **[default]** and expected to change before launch |
| `control/proposal`                                   | Supporting                                              | Low                                                                   | Branch-plus-draft-PR mechanics are stable GitHub behavior                         |
| `wire`                                               | Published language between three repositories           | Moderate                                                              | Changes only with a versioned engine or Action release                            |
| `github`, `store`                                    | Generic — solved problems                               | Low functional, moderate implementation (`store` backend is unchosen) | Wrapped behind contracts                                                          |
| `app`                                                | Supporting                                              | Low                                                                   | Wiring only                                                                       |

## Encapsulated Knowledge

- The decomposition itself: which packages exist, the allowed import directions, and the rule
  that `gate` never imports `control` or `app`.
- The single-binary shape: every module is a Go package in one process; the only platform seams
  are `Store`, `Sandbox`, and the LLM `Provider`.
- The rule that GitHub is the database and the protected ref the only source of customer-approved truth.

## Public Contract

The system's contract is its external surface. Everything else is internal.

### HTTP surface

| Route                   | Owner                     | Authentication                                            |
| ----------------------- | ------------------------- | --------------------------------------------------------- |
| `GET /healthz`          | `app`                     | none                                                      |
| `POST /v1/reports`      | `gate`                    | GitHub Actions OIDC bearer token, audience = App base URL |
| `POST /v1/discoveries`  | `control` (onboarding)    | same OIDC checks, through the gate's `Receiver`           |
| `POST /v1/baselines`    | `control` (onboarding)    | same OIDC checks, through the gate's `Receiver`           |
| `POST /webhooks/github` | `app` → `gate`, `control` | `X-Hub-Signature-256`                                     |
| `POST /internal/sweep`  | `app` → `control`, `gate` | operator bearer token **[default]**                       |
| `POST /internal/grants` | `app` → `control`         | operator bearer token **[default]**                       |
| everything else         | `control` (ui)            | GitHub OAuth session cookie                               |

### Upload protocol

Consumed by `archfit-action` and the CLI path: one POST. Header
`Authorization: Bearer <OIDC token>`. Header `X-Archfit-Envelope: <envelope JSON>` **[default: kept from the skeleton]**.
Body: the exact payload bytes, at most 5 MB. `2xx` = accepted and published; the producer retries
on any non-2xx except `400`, `401`, `403`, `409`, `413`.

### Files on the protected ref

Customer-approved state: `.archfit.yaml` (engine policy with waivers), `.archfit-baseline.json`,
`.archfit-labels.yaml` (the engine's own file names, beside the policy), and `.archfit-app.yaml` (App settings).
With the workflow file these are the five protected paths.

### GitHub objects

Check runs `archfit/gate` and `archfit/state`; one PR comment carrying the hidden
marker `<!-- archfit:comment -->`; branches under `archfit/`; workflow artifact `archfit-report`
holding the no-source report **[default]**, read only by the graph view.

GitHub App permissions: `checks: write`, `pull_requests: write`, `contents: write`, `workflows: write`
(onboarding commits a workflow file), `actions: write` (re-run and dispatch), `members: read` (team owners), `metadata: read`.

### Process entry

`cmd/reporter` calls exactly this and nothing else:

<!-- contract: Config, Load, Run — restated from internal/app/module.md -->
```go
type Config struct {
    Addr             string // ARCHFIT_ADDR, default ":8080"
    BaseURL          string // ARCHFIT_BASE_URL; also the OIDC audience
    AppID            int64  // ARCHFIT_GITHUB_APP_ID
    PrivateKeyFile   string // ARCHFIT_GITHUB_PRIVATE_KEY_FILE
    WebhookSecretFile string // ARCHFIT_GITHUB_WEBHOOK_SECRET_FILE
    OAuthClientID    string // ARCHFIT_GITHUB_OAUTH_CLIENT_ID
    OAuthSecretFile  string // ARCHFIT_GITHUB_OAUTH_SECRET_FILE
    CookieKeyFile    string // ARCHFIT_COOKIE_KEY_FILE
    OperatorTokenFile string // ARCHFIT_OPERATOR_TOKEN_FILE; authenticates /internal/*
    AnthropicKeyFile string // ARCHFIT_ANTHROPIC_KEY_FILE; empty disables managed LLM
    StoreDriver      string // ARCHFIT_STORE, "memory" until docs/tech-stack.md picks a KV
    SandboxDriver    string // ARCHFIT_SANDBOX, "none" until docs/tech-stack.md picks one
}

// Load reads Config from the environment. Secrets are file references only.
func Load(getenv func(string) string) (Config, error)

// Run wires every module and serves until ctx is cancelled.
func Run(ctx context.Context, c Config, log *slog.Logger) error
```

## Integrations

The root integrates with nothing above it. Its external counterparts are other repositories, reached only
through the contracts above: `archfit` (config schema pinned v2.4.0; state schema pinned v2.3.1, byte-identical in
v2.4.0), `archfit-action` (conforms to the envelope schema owned here), and GitHub. Distance to them is the highest
the system has (separate repositories and lifecycles), so the strength is contract by construction: versioned JSON
Schema, strict decoding, a locked manifest. All internal integrations are listed in the coupling table below.

## Internal Design

### Module map

```
internal/                         Archfit App (root)
├── app/                          composition root: config, wiring, routes, webhook fan-out, operator endpoints
├── wire/                         published language (versioned contracts, no domain logic)
│   ├── envelope/                 report-envelope v1: schema, strict decode, payload digest
│   ├── report/                   engine state v1 as no-source allowlist transport
│   ├── settings/                 .archfit-app.yaml: owners, gate mode, workflow path, data mode
│   ├── policy/                   engine file paths, raw-byte digest, waiver reader, config schema
│   └── manifest/                 locked approved engine/image/profile manifest
├── github/                       everything that knows GitHub's API shapes
│   ├── auth/                     App JWT, installation tokens, OAuth exchange
│   ├── api/                      REST client by role: repos, pulls, checks, comments, actions, changes, users
│   ├── webhook/                  signature verification, normalized events
│   └── oidc/                     Actions OIDC token verification
├── store/                        Store interface (tenant-prefixed KV) + in-memory implementation
├── gate/                         the gate path (core)
│   ├── ingest/                   authenticate and bind one upload
│   ├── inputs/                   trusted inputs from the protected ref; owner approval; entitlement read
│   ├── verdict/                  pure decision: facts → conclusion and reasons
│   ├── feedback/                 check runs, the PR comment, receipts, idempotency
│   └── sweep/                    reconciliation of open PRs
└── control/                      control plane (never on the gate path)
    ├── account/                  entitlements, consent records, LLM budget (all KV state)
    ├── proposal/                 a change as branch + draft PR, idempotent
    ├── onboarding/               two-PR customer flow, managed discovery behind Sandbox
    ├── assist/                   explicit AI requests; owns the LLM Provider seam
    │   └── anthropic/            Provider implementation
    └── ui/                       server-rendered pages, editor, overview
        ├── session/              OAuth login, signed cookies, permission checks
        └── graph/                read-only graph view model
```

Root fan-out is six, one above the 4±1 guidance; see Minor issues below.

### Go packaging rules

- A module folder is a Go package. A parent package composes its children; a child never imports its parent.
  One exception: an implementation package of a seam imports the package that declares the interface
  (`store/<driver>` → `store`, `control/assist/anthropic` → `control/assist`); the declaring package never imports its implementations.
- `wire` and `github` **publish their child packages**: Go cannot re-export a package without alias
  boilerplate, and these children hold no hidden internals. Their parents' contracts say so, and consumers
  cite the child they import. `gate` and `control` publish only their façade package; nobody outside imports
  `gate/*` or `control/*`, with one exception: `app` calls the constructor of a platform implementation
  (`control/assist/anthropic`, and later `store/<driver>`, `control/onboarding/<sandbox>`), which `control` publishes for that purpose.
- `cmd/reporter` stays a thin entry point: build the logger, `app.Load`, `app.Run`.

### Allowed imports (enforced by Archfit on this repository and by a root test)

| Package group          | May import                                                                  |
| ---------------------- | --------------------------------------------------------------------------- |
| `wire/*`               | standard library, one YAML library (in `wire/settings`, `wire/policy` only) |
| `store`                | standard library                                                            |
| `github/*`             | standard library, `github/auth` (from `github/api`)                         |
| `gate`, `gate/*`       | `wire/*`, `github/*`, `store`, gate siblings                                |
| `control`, `control/*` | `wire/*`, `github/*`, `store`, `gate` (façade only), control siblings       |
| `app`                  | everything                                                                  |

`gate` must never import `control`, `app`, any LLM package, or any UI package. This is the package-level
form of "keep LLM output off the gate path" and of decisions.md §2 gate independence.

### Key flows

**Report upload** (`POST /v1/reports`): `gate/ingest` enforces the size cap, strictly decodes the envelope,
verifies the OIDC token, resolves repository, installation, run, PR, head, base and merge-base from the GitHub
API, compares them with the envelope, checks the payload digest and decodes the no-source report. A request
whose tenant cannot be authenticated, or that is stale, is rejected with an HTTP status and nothing is
published. After authentication every defect becomes a `Problem`, not a rejection. `gate/inputs` reads
settings, policy, baseline and labels from the protected ref, computes digests, finds changed protected paths and the
owner approval on the exact head SHA, and reads the cached `Entitlement`. `gate` checks the manifest identity
and the workflow path. `gate/verdict` maps the facts to an outcome. `gate/feedback` publishes. All in the request.

**Webhook**: `app` verifies the signature, parses the event, hands it to `gate.HandleEvent` first and then to
`control.HandleEvent`. A `control` error never changes the gate's result. New head → pending gate check (required
mode) or fork refusal. A default-branch push that changes a protected path → a one-repository sweep, in the request. Review change → re-run the workflow if the last receipt was `owner_approval_missing`.
Installation events → trial start, uninstall cleanup.

**Sweep** (`POST /internal/sweep`, hourly **[default]**): `control.Sweep` refreshes entitlement records and forgets Store tenants whose installation is gone, then
`gate.Sweep` walks installations → repositories with a settings file → open PRs and repairs: missing pending
check, missing upload after 60 minutes, receipt digests that no longer match the protected ref (re-run),
waiver expiry (warn 7 days before, re-run after), suspension and restoration. The two sweeps fail independently.

**Customer onboarding**: UI → `onboarding.StartCustomer` → `proposal` opens PR 1 (workflow, settings with owners,
CODEOWNERS entries). After merge, the UI dispatches discovery; the workflow uploads a draft policy to
`POST /v1/discoveries`; `onboarding` opens PR 2. Stages are derived from GitHub on every read.

**Baseline capture**: an owner dispatches the workflow with `mode: baseline` on the default branch; the Action runs
`archfit baseline` in the pinned image and uploads the file to `POST /v1/baselines`. `onboarding` pins the run to the
default-branch head, checks the approved identity and that the capture ran under the protected policy and labels, then
opens or updates one pull request on `archfit/baseline` that changes only `.archfit-baseline.json`. It never commits to
the default branch; the gate's protected-path rule requires an owner approval of the exact head before that PR merges.

**Managed onboarding**: consent recorded in KV → onboarding PR opened with a "discovery running" body → `Sandbox.Discover`
→ policy commit pushed to the same PR. No LLM runs inside the sandbox **[default]**; optional LLM refinement
is an ordinary `assist` request on the resulting draft.

**Edit and assist**: the editor loads policy and schema, validates in the browser, and saves through
`proposal.Propose` with the head SHA it read (stale edits are refused). Engine validation is the check result
on the draft PR. `assist.Propose` adds a commit to the draft (managed) or dispatches the customer's assist workflow.

### Coupling assessment

Distance scale and thresholds are those of the Fractal Design skill. Depths: root 0; `gate` 1; `gate/ingest` 2;
`control/ui/session` 3. Volatility is that of the more volatile side.

| Integration                                                                 | Strength                          | LCA            | Rank | Distance | Volatility | Balanced? | Action                                           |
| --------------------------------------------------------------------------- | --------------------------------- | -------------- | ---- | -------- | ---------- | --------- | ------------------------------------------------ |
| gate → gate/{ingest,inputs,verdict,feedback,sweep}                          | Functional (composition)          | gate           | 1    | 1        | High       | Yes       | —                                                |
| gate/verdict → gate/ingest (`Problem`), gate/inputs (`Trusted`)             | Model                             | gate           | 1    | 1        | High       | Yes       | —                                                |
| gate/feedback → gate/verdict (`Outcome`), gate/inputs (`Target`, `Trusted`) | Model                             | gate           | 1    | 1        | High       | Yes       | —                                                |
| gate/sweep → gate/inputs, gate/feedback, gate/verdict                       | Contract                          | gate           | 1    | 1        | High       | Yes       | —                                                |
| gate/ingest → wire/envelope, wire/report, wire/manifest                     | Contract (versioned schema)       | root           | 2    | 2        | High       | Yes       | —                                                |
| gate/ingest → github/oidc, github/api                                       | Contract                          | root           | 2    | 2        | High       | Yes       | —                                                |
| gate/inputs → wire/settings, wire/policy                                    | Contract                          | root           | 2    | 2        | High       | Yes       | —                                                |
| gate/inputs → github/api                                                    | Contract                          | root           | 2    | 2        | High       | Yes       | —                                                |
| gate/inputs → store                                                         | Contract                          | root           | 2    | 2        | High       | Yes       | —                                                |
| gate/feedback → store (publication leases)                                  | Contract                          | root           | 2    | 2        | High       | Yes       | —                                                |
| gate/verdict, gate/feedback → wire/report                                   | Contract (published language)     | root           | 2    | 2        | High       | Yes       | —                                                |
| gate/feedback, gate/sweep → github/api                                      | Contract                          | root           | 2    | 2        | High       | Yes       | —                                                |
| gate → wire/{envelope,manifest,settings}, github/{api,oidc,webhook}         | Contract                          | root           | 2    | 2        | High       | Yes       | —                                                |
| gate → store (hands the Store to gate/inputs and gate/feedback)             | Contract                          | root           | 1    | 1        | High       | Yes       | —                                                |
| gate/sweep → wire/settings; gate/feedback → wire/settings, wire/manifest    | Contract                          | root           | 2    | 2        | High       | Yes       | —                                                |
| control/account → gate (`Entitlement` record)                               | Contract                          | root           | 2    | 2        | Moderate   | Yes       | gate owns the record so it never imports billing |
| control/account → store                                                     | Contract                          | root           | 2    | 2        | Moderate   | Yes       | —                                                |
| control/onboarding → gate (`Receiver`)                                      | Contract                          | root           | 2    | 2        | High       | Yes       | —                                                |
| control/onboarding → control/proposal, control/account                      | Contract                          | control        | 1    | 1        | High       | Yes       | —                                                |
| control/onboarding → wire/{envelope,settings,policy,manifest}, github/api   | Contract                          | root           | 2    | 2        | High       | Yes       | —                                                |
| control/onboarding, control/ui → github/auth (`TokenSource`, `OAuth`)       | Contract                          | root           | 2    | 2        | Low        | Yes       | —                                                |
| control/assist → control/account, control/proposal                          | Contract                          | control        | 1    | 1        | High       | Yes       | —                                                |
| control/assist → wire/policy, wire/settings, github/api                     | Contract                          | root           | 2    | 2        | High       | Yes       | —                                                |
| control/assist/anthropic → control/assist (`Provider`)                      | Contract                          | control/assist | 1    | 1        | Moderate   | Yes       | —                                                |
| control/proposal → github/api                                               | Contract                          | root           | 2    | 2        | Low        | Yes       | —                                                |
| control/ui → control/{account,onboarding,assist,proposal}                   | Contract                          | control        | 1    | 1        | High       | Yes       | —                                                |
| control/ui → control/ui/{session,graph}                                     | Functional (composition)          | control/ui     | 1    | 1        | High       | Yes       | —                                                |
| control/ui → wire/{policy,settings,report}, github/api                      | Contract                          | root           | 2    | 2        | High       | Yes       | —                                                |
| control/ui/session → github/auth, github/api                                | Contract                          | root           | 3    | 3        | Low        | Yes       | —                                                |
| control/ui/graph → wire/report, wire/policy                                 | Contract                          | root           | 3    | 3        | Moderate   | Yes       | —                                                |
| control → its children                                                      | Functional (composition)          | control        | 1    | 1        | High       | Yes       | —                                                |
| control → gate, store, github/*                                             | Contract                          | root           | 1–2  | 1–2      | Moderate   | Yes       | —                                                |
| github/api → github/auth (`TokenSource`)                                    | Contract                          | github         | 1    | 1        | Low        | Yes       | —                                                |
| app → gate, control, store                                                  | Contract (constructors, handlers) | root           | 1    | 1        | Low        | Yes       | —                                                |
| app → github/{auth,api,webhook,oidc}                                        | Contract                          | root           | 2    | 2        | Low        | Yes       | —                                                |
| control → github/webhook (`Event`)                                          | Contract                          | root           | 2    | 2        | Low        | Yes       | —                                                |
| app → control/assist/anthropic (constructor)                                | Contract                          | root           | 3    | 3        | Low        | Yes       | —                                                |

No integration is intrusive. Every model- or functional-strength integration is between siblings or parent
and child (distance 1). Everything that crosses the root is contract coupling, which tolerates any distance.

### Major decisions and trade-offs

1. **The gate owns what it must know.** `Entitlement` and its Store key are defined by `gate/inputs` and
   re-published by `gate`; `control/account` writes the record. The gate reads a cached fact and never calls
   billing code. Cost: the record shape is a contract billing must respect. Missing or unreadable record means
   "not suspended": a KV or billing outage never establishes non-payment.
2. **`verdict` is a pure function with a closed `Conclusion` type.** `neutral` and `skipped` cannot be
   expressed, so "fail closed" is a compile-time property rather than a review habit.
3. **Problems versus rejections.** Before the tenant is authenticated the only answer is an HTTP status.
   After it, every defect is published as `action_required` with a reason, so a broken producer is visible on the PR.
4. **Re-run is the single re-evaluation mechanism.** The App stores no report, so expiry, base policy change,
   owner approval arriving late, and restored entitlement all re-run the customer's workflow
   (`actions: write`, their CI minutes) and show `action_required` until the new report arrives.
5. **The check is its own receipt.** Run ID, attempt, report digest, policy and baseline digests and the earliest
   waiver expiry live in the check run; the sweep compares them with the protected ref. No second copy can drift.
6. **Consumer-owned seams.** `Sandbox` lives with `onboarding`, `Provider` with `assist`, `Store` stands alone
   because both branches use it. Platform implementations are separate packages chosen in `app`.
7. **Three upload routes, one provenance path.** `/v1/discoveries` and `/v1/baselines` reuse the gate's `Receiver`,
   so neither can weaken or fork the authentication rules. Discovery and baseline output never satisfy the gate; a
   baseline becomes trusted input only when an owner-approved pull request merges it.
8. **Advisory mode publishes no `archfit/gate` check [default].** If a ruleset requires the check while settings
   say `advisory`, the PR stays blocked as "expected": closed, never green.
9. **No LLM in the sandbox [default].** One LLM call site (`assist`), one budget counter, and source never
   reaches a model from Archfit infrastructure.
10. **Published children instead of alias façades (deliberate deviation).** The model says an integration at rank ≥ 2
    should pass through the counterpart's ancestor. For `wire` and `github` the ancestor's contract is "these child
    packages are public": Go has no re-export, and an alias façade would triple every restatement for no hidden
    knowledge. All such integrations are contract strength, so distance stays balanced. `gate` and `control`, which do
    hide knowledge, keep real façades.
11. **Trial notices ride on feedback [default].** No e-mail channel: the `Entitlement` record carries a notice
    the comment and UI display from `WarnFrom` on.

### Defaults chosen here

Beyond the **[default]** rows of decisions.md: file path `.archfit-app.yaml`;
envelope in the `X-Archfit-Envelope` header; operator bearer token for `/internal/*`; artifact name
`archfit-report`; head baseline and head labels accepted under the same owner-approval rule as head policy;
baseline and labels integrity through producer-stated envelope digests (below); `Store` tenant `0` for the App-wide budget counter; session
permission cache of 60 seconds; Anthropic model `claude-sonnet-5`.

Readings of decisions.md adopted here, to be confirmed by the owner: §3 "Supported triggers" governs report
uploads; discovery uploads (§5) and baseline uploads (§4) also accept `workflow_dispatch`. §4 "The App never
commits it" means never to the protected ref: the App commits a captured baseline only to `archfit/baseline`, and it
reaches the protected ref only by an owner-approved merge. §2 "keys are prefixed by installation ID" has
one exception, the App-wide LLM budget counter of §7. §4 "created `in_progress` on PR open or push" means a push
to the PR branch (a new head SHA); a default-branch push gets `archfit/state` only. §7 "suggestion" means a
commit on the draft PR, not a GitHub review suggestion. Supported languages are whatever engine v2.4.0 supports;
the App has no language-specific logic, so no module owns that list. "Checked by Archfit on this repository"
needs this repository's own `.archfit.yaml`, which is a plan task; until then the root import tests are the enforcement.

### Pinned against engine v2.3.1

`wire/report` mirrors `archfit.state.schema.json` at the pinned revision; the guessed field names of the first
draft are gone. What the real engine changed in this design:

- A report has no exit code. The decision reads `verdict`, `decision.hard_gates` and
  `decision.unevaluated_required_rules`. An engine error produces no report, so it surfaces as a missing upload.
- `comparison.config_hash` is the bare hex SHA-256 of the raw policy bytes, so the policy is verified by digest
  exactly as decisions.md §3 says.
- The engine hashes neither the baseline nor the labels file; `gate_reference` says only whether the baseline it
  loaded was comparable. decisions.md §3 ("rejects a report whose `gate_reference` differs") cannot be met by
  the report alone. **[default]** The envelope carries `baseline_digest` and `labels_digest`, stated by the
  producer and compared with the protected ref. That is as trustworthy as the report itself (customer-attested)
  and needs no engine change. An engine-side digest would be stronger; it is a cross-repository follow-up.
- A report lists no modules or edges. The graph takes nodes from the policy file and links from `seams[]`.
- `agent_tasks[].declarations` carries source identifiers; the no-source decoder always drops it.

### Minor issues (accepted)

- Root fan-out of six exceeds 4±1 by one. `app` is wiring only and `store` is a single interface, so the
  load is low. Folding them elsewhere would hide a platform seam or fatten `cmd/reporter`.
- `github/api` is a wide leaf. It is generic, low-volatility, and split into role interfaces so no consumer holds all of it.
- `control/ui` is the largest leaf-like module. Split further only when a second contributor works on it.

## Change Vectors

- Adding a module under `gate` or `control` without touching the other branch.
- Replacing the KV, sandbox, or LLM platform: a new implementation package plus one line in `app`.
- A new engine version: `wire/report`, `wire/policy`, `wire/manifest` and their fixtures.
- Splitting `gate` into its own deployment later: the import rule already guarantees it builds alone.
- A billing provider: inside `control/account`; the `Entitlement` record stays.

## Constraints and Invariants

- Never trust head-tree config, labels, baselines, owners, or CODEOWNERS. Gate inputs come from the protected
  ref and are verified by digest. The only exception is a head policy or baseline digest for a PR whose exact
  head SHA a base-version owner approved.
- Fail closed. The gate concludes `success`, `failure`, or `action_required`. Missing, ineligible, or
  incompatible evidence is never `success`.
- Provenance is necessary, not sufficient. Feedback says "customer-attested"; nothing prints "verified".
- No LLM, billing, or UI code on the gate path, by imports and by runtime calls.
- Wire types are read-only inputs: modules read a decoded `wire` value and pass it on, but nothing stores one, mutates one, or re-publishes one as its own type.
- Versioned wire contracts (`wire/*`) stay separate from domain types; storage holds only `account`'s records and the `Entitlement` record.
- Tenant identity comes from a verified webhook, verified OIDC claims, or an authorized OAuth session; never from a client-supplied ID.
- Nothing is persisted except KV keys under an installation prefix (and the system budget counter). No report, source, queue, or job table.
- Logs and telemetry carry IDs, digests, outcomes and timings only: no report bodies, paths, or messages.
- Handlers do their work in the request and are idempotent; GitHub holds the dedup keys.
- GitHub rate-limit exhaustion before a decision is published leaves checks pending for the sweep; it never produces
  green. A rate limit on the changed-file list, which only filters annotations, comes after the gate check is written
  with its decided conclusion; the retry adds the annotations.
- Origin is presentation only; enforcement is never filtered to introduced findings. No repository score.
- Standard library first. Expected third-party dependencies: one YAML parser, nothing else until `docs/tech-stack.md` says so.
- Targets **[default]**: feedback within 60 seconds of upload at p95; missed events repaired within one sweep interval.

## Test Specification

### Unit Tests

- **Test name**: `TestGatePathImportsNothingForbidden`
  **Scenario**: run `go list -deps ./internal/gate/...`.
  **Expected behavior**: no package under `internal/control`, `internal/app`, or any LLM provider package appears.
- **Test name**: `TestAllowedImportMatrix`
  **Scenario**: run `go list -deps` for each package group in the Allowed imports table.
  **Expected behavior**: every import is within the group's allowed set; `wire/*` and `store` import no other internal package.
- **Test name**: `TestOnlyPlatformSeamsTouchPlatformSDKs`
  **Scenario**: list third-party imports per package.
  **Expected behavior**: only `wire/settings` and `wire/policy` import YAML; cloud, KV, or LLM SDKs appear only in `Store`, `Sandbox`, or `Provider` implementation packages.

### Integration Contract Tests

- **Test name**: `TestUploadProtocolAcceptsApprovedProducer` (replaces `TestReportIngestRefusesEveryReport` from the skeleton, in the same change)
  **Scenario**: a fixture upload with a valid OIDC token, matching envelope and digest, against fake GitHub and a memory Store.
  **Expected behavior**: `2xx`; `archfit/state`, the comment, and (required mode) `archfit/gate` exist with the receipt in the check.
- **Test name**: `TestUploadProtocolRejectsInvalidProvenance`
  **Scenario**: table of T03/T09 negatives: bad signature, wrong audience, expired token, wrong tenant, modified report bytes, oversized body, unknown envelope field.
  **Expected behavior**: the documented status for each; nothing is published; no success check exists.
- **Test name**: `TestRoutesRequireTheirAuthentication`
  **Scenario**: call every route of the HTTP surface without and with wrong credentials.
  **Expected behavior**: `401`/`403` everywhere except `/healthz`; `/internal/*` refuse a user session.

### Boundary Tests

- **Test name**: `TestNoSourceLeavesThePayloadBoundary`
  **Scenario**: seeded leakage fixtures (snippet field, log blob, raw model output, multi-line message) uploaded as reports and as discovery results.
  **Expected behavior**: decode fails; the gate concludes `action_required` (`report_invalid`); seeded strings appear in no log line, check, or comment.
- **Test name**: `TestLogsCarryOnlyIdentifiers`
  **Scenario**: run the upload, webhook, sweep and UI flows with a capturing logger and fixtures containing marker paths and messages.
  **Expected behavior**: logs contain IDs, digests, outcomes, timings; no marker string.
- **Test name**: `TestClientSuppliedInstallationIDIsIgnored`
  **Scenario**: send requests carrying another tenant's installation ID in headers, query, envelope and form fields.
  **Expected behavior**: the tenant used is always the one from OIDC, webhook, or session; no cross-tenant key or repository is touched.
- **Test name**: `TestUninstallLeavesNoKeys`
  **Scenario**: install, create entitlement, consent and budget activity, then deliver `installation_removed`.
  **Expected behavior**: the tenant has no keys; the system budget counter is unchanged.

### Behavior Tests

- **Test name**: `TestLegitimateChangePassesAndViolationFails`
  **Scenario**: paired healthy and blocked fixtures for each claimed erosion scenario.
  **Expected behavior**: `success` and `failure` respectively; the failure feedback carries rule, modules, locations and the validation commands of its agent tasks (a healthy report has no task and therefore no command).
- **Test name**: `TestTamperingNeverYieldsSuccess`
  **Scenario**: head-edited policy, baseline, settings, workflow, CODEOWNERS; stale approval after re-push; unknown image; missing profile; fork PR; `workflow_run` upload.
  **Expected behavior**: each ends `action_required` with its own reason; none is `success`, `neutral`, or `skipped`.
- **Test name**: `TestGateSurvivesControlPlaneFailure`
  **Scenario**: LLM provider failing, UI handler panicking, control sweep failing; separately the Store failing together with the control sweep.
  **Expected behavior**: without the Store failure, report upload, webhook pending checks and the gate sweep still complete. With the Store failing, an unreadable entitlement would mean "not suspended", but no publication lease can be taken: the upload answers 503, the webhook and every sweep visit return errors, and no gate check is written, so nothing is green.
- **Test name**: `TestSweepRepairsMissedEvents`
  **Scenario**: drop the PR-opened webhook, drop an upload, change the base policy, let a waiver expire, suspend then restore an installation.
  **Expected behavior**: after one sweep each PR shows the expected check state; re-runs were requested; nothing turned green without a fresh report.
- **Test name**: `TestDuplicateAndOutOfOrderDelivery`
  **Scenario**: the same upload twice; an older attempt after a newer one; an upload for a superseded head.
  **Expected behavior**: one comment, updated in place; the newer result stands; the stale upload gets `409` and changes nothing.
- **Test name**: `TestSuspensionIsNeitherArchitectureFailureNorSuccess`
  **Scenario**: private repository after trial and grace with no grant; then a grant.
  **Expected behavior**: `action_required` in category `subscription` with a recovery action; after the grant the workflow is re-run and only a fresh report can produce `success`.
- **Test name**: `TestBothOnboardingModesComplete`
  **Scenario**: customer mode through PR 1, discovery upload, PR 2; managed mode with and without consent and with no sandbox configured.
  **Expected behavior**: exactly one PR per step after retries; no consent means no sandbox job and no LLM call; "not available" is a clear state.

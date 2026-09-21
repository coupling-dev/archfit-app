# Implement Archfit App

## Overview

- Archfit App is one Go service that turns a customer-executed Archfit engine report into trustworthy GitHub
  feedback: an advisory `archfit/state` check, one updated PR comment, and an explicitly enabled, fail-closed
  `archfit/gate` check, with onboarding, policy editing, AI assistance on request, and entitlements around it.
- Design tree: `internal/` — 29 modules, implemented bottom-up by height in the module tree.
- Each module task implements exactly one module; its complete specification is that module's `module.md`.
- This plan replaces the task order of [closed-beta.md](closed-beta.md); that plan's Accept criteria already live in
  the modules' Test Specifications (decisions.md §1).
- Out of scope, because nothing in the design tree needs them: choosing the container host, the KV driver, and the
  sandbox driver. The service runs with the `memory` store and no sandbox until then (docs/tech-stack.md).

## Development Approach

- **Testing approach**: TDD — for every module, write the tests from its Test Specification first, then implement
  until they pass
- **Tech stack**: defined in docs/tech-stack.md — read it before any task; every task uses it, no per-task
  deviations. New stack decisions made during implementation are recorded there immediately (it is the normative
  home; this plan does not restate it)
- Each task's spec is exactly two files — the module's module.md and docs/tech-stack.md; needing any other file to
  implement the module is a design defect: stop and report it (⚠️), do not improvise
- Implement the module's code inside its own folder; never reach into another module's folder or internals —
  consumers code against the counterpart contracts restated in their own module.md
- **Go refinement of the wave order.** A Go package does not compile until every package it imports exists, so a
  consumer cannot be built against a contract that exists only in prose. Two measures keep the bottom-up waves
  valid: Task 2 creates every package's exported contract as a compiling skeleton, and the leaf wave is split into
  0a, 0b, 0c by which leaves call real code (not just types or interfaces) of other leaves. `internal/app` is a leaf
  by height but wires the real `gate` and `control` façades, so it runs after them
- Tasks in the same wave are independent: a parallel executor may run them concurrently (e.g. one subagent per task,
  isolated worktrees); a sequential executor completes them in task order — both are correct
- Never start a parent module's task before all its submodules' tasks are complete
- **CRITICAL: every task implements the tests named in its module.md's Test Specification** — all four subsections
  (Unit, Integration Contract, Boundary, Behavior); they are deliverables, not suggestions
- **CRITICAL: all tests must pass before the task is complete** — no exceptions
- **CRITICAL: if the design proves wrong or incomplete during implementation, update the module.md first** (it is
  validated on write; a changed contract fence must be changed in its owner and in every restatement), mark the
  finding with ⚠️ in this plan, then implement to the updated design — never silently diverge from the documents
- Run `make all` before pushing (AGENTS.md)

## Progress Tracking

- Mark completed items with `[x]` immediately when done
- Add newly discovered tasks with ➕ prefix
- Document issues/blockers with ⚠️ prefix
- Update this plan if implementation deviates from the design tree — and update the affected module.md, which is
  the normative record

## Waves

| Wave | Module                             | Submodules it composes                       |
| ---- | ---------------------------------- | -------------------------------------------- |
| 0a   | internal/store/                    | — (leaf)                                     |
| 0a   | internal/wire/envelope/            | — (leaf)                                     |
| 0a   | internal/wire/report/              | — (leaf)                                     |
| 0a   | internal/wire/settings/            | — (leaf)                                     |
| 0a   | internal/wire/policy/              | — (leaf)                                     |
| 0a   | internal/wire/manifest/            | — (leaf)                                     |
| 0a   | internal/github/auth/              | — (leaf)                                     |
| 0a   | internal/github/webhook/           | — (leaf)                                     |
| 0a   | internal/github/oidc/              | — (leaf)                                     |
| 0b   | internal/github/api/               | — (leaf)                                     |
| 0b   | internal/gate/ingest/              | — (leaf)                                     |
| 0b   | internal/gate/inputs/              | — (leaf)                                     |
| 0b   | internal/gate/verdict/             | — (leaf)                                     |
| 0b   | internal/gate/feedback/            | — (leaf)                                     |
| 0b   | internal/control/account/          | — (leaf)                                     |
| 0b   | internal/control/proposal/         | — (leaf)                                     |
| 0b   | internal/control/onboarding/       | — (leaf)                                     |
| 0b   | internal/control/assist/anthropic/ | — (leaf)                                     |
| 0b   | internal/control/ui/session/       | — (leaf)                                     |
| 0b   | internal/control/ui/graph/         | — (leaf)                                     |
| 0c   | internal/gate/sweep/               | — (leaf)                                     |
| 1    | internal/wire/                     | envelope, report, settings, policy, manifest |
| 1    | internal/github/                   | auth, api, webhook, oidc                     |
| 1    | internal/gate/                     | ingest, inputs, verdict, feedback, sweep     |
| 1    | internal/control/assist/           | anthropic                                    |
| 1    | internal/control/ui/               | session, graph                               |
| 2    | internal/control/                  | account, proposal, onboarding, assist, ui    |
| 3    | internal/app/                      | — (leaf; wires gate, control, store, github) |
| 4    | internal/ (root)                   | app, wire, github, store, gate, control      |

Modules in the same wave run in parallel. Wave 0a has 9 modules, 0b has 11, 0c has 1, wave 1 has 5.

## Implementation Steps

### Task 1: Pin the engine contracts the design assumed

- [x] fetch the archfit v2.3.1 state and config JSON Schemas at the revision pinned in internal/wire/module.md, and
      one real state report produced by that release
- [x] compare them with the transport types in internal/wire/report/module.md and the waiver keys in
      internal/wire/policy/module.md; confirm that `comparison.config_hash` and `gate_reference` are what the design
      marks as **[default]** (SHA-256 of the raw policy and baseline bytes)
- [x] where they differ, update the owner module.md first, then every marked restatement of the changed fence; run
      the validator in tree mode until it reports no defects
- [x] record each difference in this plan with ⚠️, or write "none found" here

⚠️ The guessed report contract did not match the engine (local checkout at tag v2.3.1; real report generated from
its `testdata/fixture-go`). Differences and the design changes they forced are recorded in internal/module.md,
"Pinned against engine v2.3.1": no exit code in a report; `gate_reference` is top-level and has no digest; the
engine hashes only the policy file, as bare hex; real file names `.archfit-baseline.json` and
`.archfit-labels.yaml`; dimensions are an object of nine keys; findings, agent tasks and seams have different
fields; no modules or edges list; `declarations` carries source identifiers. The waiver keys were right.

⚠️ Owner decision wanted: baseline and labels integrity now rests on producer-stated envelope digests
(`baseline_digest`, `labels_digest`) because the engine exposes none. Accepted as **[default]**; the stronger
alternative is an engine change.

### Task 2: Create the compiling contract skeleton

- [x] add the dependencies docs/tech-stack.md allows to go.mod; add nothing else
- [x] for every module under internal/, create its package with exactly the exported identifiers of its
      `## Public Contract` section: types, constants, sentinel errors, interfaces, and function signatures whose
      bodies return a "not implemented" error (façades alias the child types they re-publish)
- [x] write no behavior and no tests in this task; do not touch cmd/reporter
- [x] run `go build ./...` and `go vet ./...` — every package compiles; `make all` stays green

### Task 3 [Wave 0a]: Implement internal/store/ (leaf, no internal imports)

- [x] read internal/store/module.md in full — with docs/tech-stack.md it is the complete and only spec for this task
- [x] write the tests named in its Test Specification: Unit Tests and Boundary Tests (TDD — failing first is expected)
- [x] write the tests named in its Test Specification: Integration Contract Tests and Behavior Tests
- [x] implement the module per its Functional Responsibilities, Public Contract, and Constraints and Invariants, inside internal/store/, replacing the Task 2 skeleton, until all its tests pass
- [x] run `go test -race ./internal/store/...` and `golangci-lint run ./internal/store/...` — all green before the task is complete

⚠️ internal/store: `TestStoreImportsOnlyStdlib` lists `./...`; the task that adds a `store/<driver>` package with an SDK must narrow it or allow-list the driver. Input limits are unexported, so every driver re-implements them; `storetest` is the guard. Accepted.

### Task 4 [Wave 0a]: Implement internal/wire/envelope/ (leaf, no internal imports)

- [x] read internal/wire/envelope/module.md in full — with docs/tech-stack.md it is the complete and only spec for this task
- [x] write the tests named in its Test Specification: Unit Tests and Boundary Tests (TDD — failing first is expected)
- [x] write the tests named in its Test Specification: Integration Contract Tests and Behavior Tests
- [x] implement the module per its Functional Responsibilities, Public Contract, and Constraints and Invariants, inside internal/wire/envelope/, replacing the Task 2 skeleton, until all its tests pass
- [x] run `go test -race ./internal/wire/envelope/...` and `golangci-lint run ./internal/wire/envelope/...` — all green before the task is complete

⚠️ internal/wire/envelope: an import test cannot forbid stdlib packages transitively (`go list -deps` always shows `os`); the module tests direct imports plus "no non-stdlib dependency". The root import-matrix tests must be written the same way. The decoder is stricter than the published JSON Schema can express (integer form and range, duplicate keys, raw size); the schema description documents it. Fixtures live in the module's own testdata until the wire parent task creates the shared folder. Accepted.

### Task 5 [Wave 0a]: Implement internal/wire/report/ (leaf, no internal imports)

- [x] read internal/wire/report/module.md in full — with docs/tech-stack.md it is the complete and only spec for this task
- [x] write the tests named in its Test Specification: Unit Tests and Boundary Tests (TDD — failing first is expected)
- [x] write the tests named in its Test Specification: Integration Contract Tests and Behavior Tests
- [x] implement the module per its Functional Responsibilities, Public Contract, and Constraints and Invariants, inside internal/wire/report/, replacing the Task 2 skeleton, until all its tests pass
- [x] run `go test -race ./internal/wire/report/...` and `golangci-lint run ./internal/wire/report/...` — all green before the task is complete

⚠️ internal/wire/report: the pinned engine state schema (tag v2.3.1, SHA-256 `acd9a09eefe1f30227c0e62155d641925cdfd416e9007e99fea79293c8e387c6`) was vendored by the orchestrator into the module's testdata; `TestEngineSchemaKeySetIsCovered` now runs its schema walk and passes. The test skips when the file is absent and does not pin the SHA-256 yet: the wire parent task moves the schema to `internal/wire/testdata/schema/` and adds the pin. Fixture `.expect.json` shapes in wire children are provisional until the wire parent task defines the shared format.

### Task 6 [Wave 0a]: Implement internal/wire/settings/ (leaf, no internal imports)

- [x] read internal/wire/settings/module.md in full — with docs/tech-stack.md it is the complete and only spec for this task
- [x] write the tests named in its Test Specification: Unit Tests and Boundary Tests (TDD — failing first is expected)
- [x] write the tests named in its Test Specification: Integration Contract Tests and Behavior Tests
- [x] implement the module per its Functional Responsibilities, Public Contract, and Constraints and Invariants, inside internal/wire/settings/, replacing the Task 2 skeleton, until all its tests pass
- [x] run `go test -race ./internal/wire/settings/...` and `golangci-lint run ./internal/wire/settings/...` — all green before the task is complete

⚠️ internal/wire/settings: import tests forbid `os` only as a direct import (same finding as envelope). A PostToolUse formatter rewrites `.yaml` fixtures on write, so hand-edited YAML forms are tested inline; later tasks that need exact YAML bytes must do the same. Accepted.

### Task 7 [Wave 0a]: Implement internal/wire/policy/ (leaf, no internal imports)

- [x] read internal/wire/policy/module.md in full — with docs/tech-stack.md it is the complete and only spec for this task
- [x] write the tests named in its Test Specification: Unit Tests and Boundary Tests (TDD — failing first is expected)
- [x] write the tests named in its Test Specification: Integration Contract Tests and Behavior Tests
- [x] implement the module per its Functional Responsibilities, Public Contract, and Constraints and Invariants, inside internal/wire/policy/, replacing the Task 2 skeleton, until all its tests pass
- [x] run `go test -race ./internal/wire/policy/...` and `golangci-lint run ./internal/wire/policy/...` — all green before the task is complete

⚠️ internal/wire/policy: reading goes through `yaml.Node` (needed for `Line` and `expires`), so the module adds a full-document decode to get the library's duplicate-key, merge and alias-bomb checks; a duplicate key anywhere in the policy is therefore an error. `Modules` refuses input with line breaks other than LF/CRLF when `modules` is a mapping, because the library counts CR, NEL, LS and PS as lines and `Line` would drift (added to module.md as [default]). Both accepted. The two cross-module digest tests use the module's own fixtures until the wire parent task creates the shared folder.

### Task 8 [Wave 0a]: Implement internal/wire/manifest/ (leaf, no internal imports)

- [x] read internal/wire/manifest/module.md in full — with docs/tech-stack.md it is the complete and only spec for this task
- [x] write the tests named in its Test Specification: Unit Tests and Boundary Tests (TDD — failing first is expected)
- [x] write the tests named in its Test Specification: Integration Contract Tests and Behavior Tests
- [x] implement the module per its Functional Responsibilities, Public Contract, and Constraints and Invariants, inside internal/wire/manifest/, replacing the Task 2 skeleton, until all its tests pass
- [x] run `go test -race ./internal/wire/manifest/...` and `golangci-lint run ./internal/wire/manifest/...` — all green before the task is complete

⚠️ internal/wire/manifest: `manifest.json` holds PLACEHOLDER image digests (`sha256:aaaa…`, `sha256:bbbb…`) and a PLACEHOLDER Action commit (`cccc…`); the real values need the published v2.3.1 images and the pinned archfit-action commit (decisions.md §12). `profile_version` is the real `archfit.measurement.v1`. Until they are replaced, a generated onboarding workflow pins an image and commit that do not exist: release blocker, not a build blocker. The module panics at package init on an invalid embedded manifest (module.md [default]), a deliberate exception to the tech-stack "no panics outside main" convention. Owner decision wanted on both.

### Task 9 [Wave 0a]: Implement internal/github/auth/ (leaf, no internal imports)

- [x] read internal/github/auth/module.md in full — with docs/tech-stack.md it is the complete and only spec for this task
- [x] write the tests named in its Test Specification: Unit Tests and Boundary Tests (TDD — failing first is expected)
- [x] write the tests named in its Test Specification: Integration Contract Tests and Behavior Tests
- [x] implement the module per its Functional Responsibilities, Public Contract, and Constraints and Invariants, inside internal/github/auth/, replacing the Task 2 skeleton, until all its tests pass
- [x] run `go test -race ./internal/github/auth/...` and `golangci-lint run ./internal/github/auth/...` — all green before the task is complete

⚠️ internal/github/auth: the `RepoReadToken` name pattern admitted `../x` while its test requires refusal; the module.md rule now rejects an owner or name that is `.` or `..`. The stdlib list in Constraints was completed and the import test reworded (direct imports only). A caller that fetched its cache entry just before `Invalidate` can get the old token once; the api retry-after-401 absorbs it. All internal to the module, accepted.

### Task 10 [Wave 0a]: Implement internal/github/webhook/ (leaf, no internal imports)

- [x] read internal/github/webhook/module.md in full — with docs/tech-stack.md it is the complete and only spec for this task
- [x] write the tests named in its Test Specification: Unit Tests and Boundary Tests (TDD — failing first is expected)
- [x] write the tests named in its Test Specification: Integration Contract Tests and Behavior Tests
- [x] implement the module per its Functional Responsibilities, Public Contract, and Constraints and Invariants, inside internal/github/webhook/, replacing the Task 2 skeleton, until all its tests pass
- [x] run `go test -race ./internal/github/webhook/...` and `golangci-lint run ./internal/github/webhook/...` — all green before the task is complete

### Task 11 [Wave 0a]: Implement internal/github/oidc/ (leaf, no internal imports)

- [x] read internal/github/oidc/module.md in full — with docs/tech-stack.md it is the complete and only spec for this task
- [x] write the tests named in its Test Specification: Unit Tests and Boundary Tests (TDD — failing first is expected)
- [x] write the tests named in its Test Specification: Integration Contract Tests and Behavior Tests
- [x] implement the module per its Functional Responsibilities, Public Contract, and Constraints and Invariants, inside internal/github/oidc/, replacing the Task 2 skeleton, until all its tests pass
- [x] run `go test -race ./internal/github/oidc/...` and `golangci-lint run ./internal/github/oidc/...` — all green before the task is complete

⚠️ internal/github/oidc: the contract fixes the sentinel text `oidc: signing keys unavailable` while the behavior rules want every error prefixed `github/oidc:`; reconciled by wrapping, `errors.Is` holds. The 1/min JWKS fetch bound covers every fetch attempt, so after a failed attempt `ErrUnavailable` can last up to 60 s after the issuer recovers (the producer retries on 503). A slow JWKS fetch stalls concurrent `Verify` calls up to the client timeout. Accepted.

### Task 12 [Wave 0b]: Implement internal/github/api/ (leaf, calls only wave 0a code or interfaces)

- [x] read internal/github/api/module.md in full — with docs/tech-stack.md it is the complete and only spec for this task
- [x] write the tests named in its Test Specification: Unit Tests and Boundary Tests (TDD — failing first is expected)
- [x] write the tests named in its Test Specification: Integration Contract Tests and Behavior Tests
- [x] implement the module per its Functional Responsibilities, Public Contract, and Constraints and Invariants, inside internal/github/api/, replacing the Task 2 skeleton, until all its tests pass
- [x] run `go test -race ./internal/github/api/...` and `golangci-lint run ./internal/github/api/...` — all green before the task is complete

⚠️ internal/github/api: no design findings. Notable internal rules now in its module.md: empty, `.` and `..` path segments are rejected before any request; automatic redirects are disabled and the artifact redirect is followed without credentials; consumers must fake the role interfaces, never `*Client`.

### Task 13 [Wave 0b]: Implement internal/gate/ingest/ (leaf, calls only wave 0a code or interfaces)

- [x] read internal/gate/ingest/module.md in full — with docs/tech-stack.md it is the complete and only spec for this task
- [x] write the tests named in its Test Specification: Unit Tests and Boundary Tests (TDD — failing first is expected)
- [x] write the tests named in its Test Specification: Integration Contract Tests and Behavior Tests
- [x] implement the module per its Functional Responsibilities, Public Contract, and Constraints and Invariants, inside internal/gate/ingest/, replacing the Task 2 skeleton, until all its tests pass
- [x] run `go test -race ./internal/gate/ingest/...` and `golangci-lint run ./internal/gate/ingest/...` — all green before the task is complete

⚠️ internal/gate/ingest: the `X-Archfit-Envelope` header carries the compact envelope JSON verbatim, no base64 — an obligation on archfit-action. A head repository different from the base repository sets `Fork` and `ProblemFork` for every event, not only pull_request. A PR number that GitHub does not know is `envelope_mismatch`, not a retryable error. The `TestPullRequestResolution` row "empty list and no hint" is reachable only through an envelope claiming another event, because `envelope.Decode` ties pull_request fields to the event. Accepted.

### Task 14 [Wave 0b]: Implement internal/gate/inputs/ (leaf, calls only wave 0a code or interfaces)

- [x] read internal/gate/inputs/module.md in full — with docs/tech-stack.md it is the complete and only spec for this task
- [x] write the tests named in its Test Specification: Unit Tests and Boundary Tests (TDD — failing first is expected)
- [x] write the tests named in its Test Specification: Integration Contract Tests and Behavior Tests
- [x] implement the module per its Functional Responsibilities, Public Contract, and Constraints and Invariants, inside internal/gate/inputs/, replacing the Task 2 skeleton, until all its tests pass
- [x] run `go test -race ./internal/gate/inputs/...` and `golangci-lint run ./internal/gate/inputs/...` — all green before the task is complete

⚠️ internal/gate/inputs: `policy.Waivers` is all-or-nothing, so one malformed `expires` zeroes the whole expiry schedule for that policy (the engine still enforces waivers; the App only loses its warning and re-run timing). module.md names "the client's oversize error" but github/api exposes no such sentinel; inputs treats every non-`ErrNotFound` error as fail-closed, which covers it. `entitlementTimeout` is an unexported package variable so the timeout test runs in milliseconds. Accepted.

### Task 15 [Wave 0b]: Implement internal/gate/verdict/ (leaf, calls only wave 0a code or interfaces)

- [x] read internal/gate/verdict/module.md in full — with docs/tech-stack.md it is the complete and only spec for this task
- [x] write the tests named in its Test Specification: Unit Tests and Boundary Tests (TDD — failing first is expected)
- [x] write the tests named in its Test Specification: Integration Contract Tests and Behavior Tests
- [x] implement the module per its Functional Responsibilities, Public Contract, and Constraints and Invariants, inside internal/gate/verdict/, replacing the Task 2 skeleton, until all its tests pass
- [x] run `go test -race ./internal/gate/verdict/...` and `golangci-lint run ./internal/gate/verdict/...` — all green before the task is complete

⚠️ internal/gate/verdict: the `comparable-profile` report fixture copied from wire/report is really a blocked report with a non-comparable gate reference, so its expectation is `failure`, not the `success` the spec row names; the wire parent task (Task 24) must supply healthy and comparable-baseline captures from the real engine and the shared `.expect.json` files, after which verdict's fixtures move there. `TestOriginNeverExempts` sets origin and status in memory because no fixture has them. With a suspended installation and a healthy report, Reasons holds only `subscription_suspended` (the step table is normative; the architecture reason is kept second only for a blocked report). Step 5 fails closed on any unknown Approval value. Accepted.

### Task 16 [Wave 0b]: Implement internal/gate/feedback/ (leaf, calls only wave 0a code or interfaces)

- [x] read internal/gate/feedback/module.md in full — with docs/tech-stack.md it is the complete and only spec for this task
- [x] write the tests named in its Test Specification: Unit Tests and Boundary Tests (TDD — failing first is expected)
- [x] write the tests named in its Test Specification: Integration Contract Tests and Behavior Tests
- [x] implement the module per its Functional Responsibilities, Public Contract, and Constraints and Invariants, inside internal/gate/feedback/, replacing the Task 2 skeleton, until all its tests pass
- [x] run `go test -race ./internal/gate/feedback/...` and `golangci-lint run ./internal/gate/feedback/...` — all green before the task is complete

⚠️ internal/gate/feedback: the spec's truncation order left the validation-commands block unbounded, so the module gives it its own budget (4 096 bytes, 20 commands) [default, in module.md]. Following the spec order literally, a very large findings block drops every agent task before any finding is cut; a fairer split is a design change, left for the owner. "The pending check is unchanged" cannot hold when `CreateComment` is rate-limited, because the gate check is written first; the test asserts never-green and a duplicate-free retry instead. A validation command containing marker sequences or a newline is omitted and counted, never altered. Fixtures are copies of wire/report fixtures until the wire parent task creates the shared folder. Accepted.

### Task 17 [Wave 0b]: Implement internal/control/account/ (leaf, calls only wave 0a code or interfaces)

- [x] read internal/control/account/module.md in full — with docs/tech-stack.md it is the complete and only spec for this task
- [x] write the tests named in its Test Specification: Unit Tests and Boundary Tests (TDD — failing first is expected)
- [x] write the tests named in its Test Specification: Integration Contract Tests and Behavior Tests
- [x] implement the module per its Functional Responsibilities, Public Contract, and Constraints and Invariants, inside internal/control/account/, replacing the Task 2 skeleton, until all its tests pass
- [x] run `go test -race ./internal/control/account/...` and `golangci-lint run ./internal/control/account/...` — all green before the task is complete

⚠️ internal/control/account: `Grant` audit lines reach the JSON log only if `app` calls `slog.SetDefault` before constructing control (tech-stack logging convention). `supportContact` is the PLACEHOLDER `support@archfit.example` (owner input wanted before launch). `Observe(false)` on a missing record writes only the zero Entitlement, so `TestObserveBackfillsMissingRecord` holds for `Observe(true)` only. A failed compensating budget `Add` returns `errors.Join(ErrBudgetExhausted, storeErr)`; consumers must use `errors.Is`. Accepted.

### Task 18 [Wave 0b]: Implement internal/control/proposal/ (leaf, calls only wave 0a code or interfaces)

- [x] read internal/control/proposal/module.md in full — with docs/tech-stack.md it is the complete and only spec for this task
- [x] write the tests named in its Test Specification: Unit Tests and Boundary Tests (TDD — failing first is expected)
- [x] write the tests named in its Test Specification: Integration Contract Tests and Behavior Tests
- [x] implement the module per its Functional Responsibilities, Public Contract, and Constraints and Invariants, inside internal/control/proposal/, replacing the Task 2 skeleton, until all its tests pass
- [x] run `go test -race ./internal/control/proposal/...` and `golangci-lint run ./internal/control/proposal/...` — all green before the task is complete

⚠️ internal/control/proposal: no constructor in the design takes a logger, so modules log through `slog.Default()`; `app` must install the JSON logger with `slog.SetDefault` (tests that capture logs cannot run in parallel). `PullByHead` returning not-found with `expectHeadSHA` set is `ErrStale`, not `ErrNotFound` (the Public Contract wins over the test row). With `expectHeadSHA` set on a branch that does not exist yet, `EnsureBranch` still runs first, so an empty `archfit/` branch can be left behind. No timings are logged ("no clock"). Accepted.

### Task 19 [Wave 0b]: Implement internal/control/onboarding/ (leaf, calls only wave 0a code or interfaces)

- [x] read internal/control/onboarding/module.md in full — with docs/tech-stack.md it is the complete and only spec for this task
- [x] write the tests named in its Test Specification: Unit Tests and Boundary Tests (TDD — failing first is expected)
- [x] write the tests named in its Test Specification: Integration Contract Tests and Behavior Tests
- [x] implement the module per its Functional Responsibilities, Public Contract, and Constraints and Invariants, inside internal/control/onboarding/, replacing the Task 2 skeleton, until all its tests pass
- [x] run `go test -race ./internal/control/onboarding/...` and `golangci-lint run ./internal/control/onboarding/...` — all green before the task is complete

⚠️ internal/control/onboarding: the generated workflow uses `alexei-led/archfit-action@<ActionRef>` with inputs `endpoint`, `audience`, `engine-version`, `image-digest`, `discover` — names chosen here because nothing pins them; archfit-action must adopt them (same release obligation as the placeholder manifest values). `Consents` exposes only `Has`, so the PR body shows the consent kind and the time it was checked, not the recorded consent time. `StartManaged` reuses an open customer-mode onboarding PR as-is, so `data_mode: customer` stays on the branch while a managed job runs (spec-mandated; owner decision wanted). "already onboarded" and "busy" are unexported errors the UI can match only by text; exporting sentinels is a contract change for the ui parent task to request if needed. The module's tests use hand fakes for Proposals and Consents, because the real ones would import packages outside its Integrations. Accepted.

### Task 20 [Wave 0b]: Implement internal/control/assist/anthropic/ (leaf, calls only wave 0a code or interfaces)

- [x] read internal/control/assist/anthropic/module.md in full — with docs/tech-stack.md it is the complete and only spec for this task
- [x] write the tests named in its Test Specification: Unit Tests and Boundary Tests (TDD — failing first is expected)
- [x] write the tests named in its Test Specification: Integration Contract Tests and Behavior Tests
- [x] implement the module per its Functional Responsibilities, Public Contract, and Constraints and Invariants, inside internal/control/assist/anthropic/, replacing the Task 2 skeleton, until all its tests pass
- [x] run `go test -race ./internal/control/assist/anthropic/...` and `golangci-lint run ./internal/control/assist/anthropic/...` — all green before the task is complete

⚠️ internal/control/assist/anthropic: the embedded price table (`prices.json`: sonnet 2/10, opus 5/25 micro-USD per input/output token) comes from the design document, not from a checked vendor price list; verify it and the default model ID against Anthropic's current pricing before managed LLM is enabled, because the USD 20 hard cap is computed from it. An empty `Prompt.System` is sent as `"system": ""`; if the live API rejects that, module.md changes to "omit when empty" (one-line `omitempty`). Neither blocks the build; both block enabling managed LLM.

### Task 21 [Wave 0b]: Implement internal/control/ui/session/ (leaf, calls only wave 0a code or interfaces)

- [x] read internal/control/ui/session/module.md in full — with docs/tech-stack.md it is the complete and only spec for this task
- [x] write the tests named in its Test Specification: Unit Tests and Boundary Tests (TDD — failing first is expected)
- [x] write the tests named in its Test Specification: Integration Contract Tests and Behavior Tests
- [x] implement the module per its Functional Responsibilities, Public Contract, and Constraints and Invariants, inside internal/control/ui/session/, replacing the Task 2 skeleton, until all its tests pass
- [x] run `go test -race ./internal/control/ui/session/...` and `golangci-lint run ./internal/control/ui/session/...` — all green before the task is complete

⚠️ internal/control/ui/session: no design findings. Redirects set Location and 303 by hand, because `http.Redirect` writes an HTML body that would repeat the OAuth state. Keeping the user token out of wrapped GitHub errors is github/api's responsibility (its errors carry no URL or token).

### Task 22 [Wave 0b]: Implement internal/control/ui/graph/ (leaf, calls only wave 0a code or interfaces)

- [x] read internal/control/ui/graph/module.md in full — with docs/tech-stack.md it is the complete and only spec for this task
- [x] write the tests named in its Test Specification: Unit Tests and Boundary Tests (TDD — failing first is expected)
- [x] write the tests named in its Test Specification: Integration Contract Tests and Behavior Tests
- [x] implement the module per its Functional Responsibilities, Public Contract, and Constraints and Invariants, inside internal/control/ui/graph/, replacing the Task 2 skeleton, until all its tests pass
- [x] run `go test -race ./internal/control/ui/graph/...` and `golangci-lint run ./internal/control/ui/graph/...` — all green before the task is complete

⚠️ internal/control/ui/graph: no wire fixture has the shape the spec scenarios name (one forbidden dependency observed twice plus one allowed dependency; a healthy report with its own policy file), so the tests extend the real capture in Go; the wire parent task (Task 24) should add such fixtures to the shared corpus. `Build`'s second parameter is named `policyText` because `policy` collides with the imported package; the signature is unchanged for callers. Accepted.

### Task 23 [Wave 0c]: Implement internal/gate/sweep/ (leaf, calls wave 0b code)

- [x] read internal/gate/sweep/module.md in full — with docs/tech-stack.md it is the complete and only spec for this task
- [x] write the tests named in its Test Specification: Unit Tests and Boundary Tests (TDD — failing first is expected)
- [x] write the tests named in its Test Specification: Integration Contract Tests and Behavior Tests
- [x] implement the module per its Functional Responsibilities, Public Contract, and Constraints and Invariants, inside internal/gate/sweep/, replacing the Task 2 skeleton, until all its tests pass
- [x] run `go test -race ./internal/gate/sweep/...` and `golangci-lint run ./internal/gate/sweep/...` — all green before the task is complete

⚠️ internal/gate/sweep: module.md said "the first rule that acts ends the evaluation"; read literally a fresh pending check falls through to rule 5 and every new PR is published as stale. Corrected in module.md to "the first rule whose condition holds" (as implemented). A generic error from `Actions.Run` is a skipped unit, never "no re-run possible", so a transient API error cannot publish `missing_upload`. Skip log lines carry IDs only (GitHub error text quotes names and paths), which makes a persistent skip such as a revoked `actions: write` hard to diagnose; a safe error kind from github/api would fix it (contract change, deferred). Eight hand mutations of the re-run guards were each caught by tests. Accepted.

### Task 24 [Wave 1]: Implement internal/wire/ (parent)

- [x] read internal/wire/module.md in full — with docs/tech-stack.md it is the complete and only spec for this task
- [x] write the tests named in its Test Specification: Unit Tests and Boundary Tests (TDD — failing first is expected)
- [x] write the tests named in its Test Specification: Integration Contract Tests and Behavior Tests
- [x] implement the module's own code and wire its submodules (envelope, report, settings, policy, manifest) per its Internal Design, inside internal/wire/, replacing the Task 2 skeleton, until all its tests pass
- [x] run `go test -race ./internal/wire/...` and `golangci-lint run ./internal/wire/...` — all green before the task is complete

⚠️ internal/wire: shared corpus built (127 files) with ten unmodified captures from the rebuilt engine v2.3.1, including a genuinely healthy report, a comparable persisted baseline, and forbidden-plus-allowed. Findings: (1) the engine's `fixture-go` can never be `healthy` — zero findings gives `needs_attention`, because healthy needs all nine dimensions measured; (2) outside a git repository the engine reports `hard_gates: unmeasured` ("rule scope cannot be established"), a real missing-coverage path customers can hit; (3) a `profile-comparable` capture with a recognized `partial_basis` cannot be produced from v2.3.1 on these fixtures, so that fixture records the truth (blocked, non-comparable) and none was faked; (4) the children and gate/verdict, gate/feedback keep their own fixture copies (a consumer of the shared corpus must derive clean facts from the `.baseline.json` / `.labels.yaml` sibling files, not assume an empty trusted list) under old names and an older `facts` shape — ➕ follow-up: repoint them at `internal/wire/testdata` (list in the task report); (5) the capture scripts hard-code local paths and live in the session scratchpad only; each fixture's `producer.command` is the in-repo record. The engine schema SHA-256 is pinned in a test that fails when the file is missing or changed. Accepted.

### Task 25 [Wave 1]: Implement internal/github/ (parent)

- [x] read internal/github/module.md in full — with docs/tech-stack.md it is the complete and only spec for this task
- [x] write the tests named in its Test Specification: Unit Tests and Boundary Tests (TDD — failing first is expected)
- [x] write the tests named in its Test Specification: Integration Contract Tests and Behavior Tests
- [x] implement the module's own code and wire its submodules (auth, api, webhook, oidc) per its Internal Design, inside internal/github/, replacing the Task 2 skeleton, until all its tests pass
- [x] run `go test -race ./internal/github/...` and `golangci-lint run ./internal/github/...` — all green before the task is complete

⚠️ internal/github: a rate-limited installation-token MINT surfaces as a plain error, not `api.ErrRateLimited` (auth has no rate-limit sentinel), so "rate-limit exhaustion surfaces as ErrRateLimited" holds only once a token is cached. It still fails closed and returns no data; gate callers answer 503 for any plain error, so the producer retries and the sweep repairs. A sentinel in auth is a contract change, deferred. Accepted.

### Task 26 [Wave 1]: Implement internal/gate/ (parent)

- [x] read internal/gate/module.md in full — with docs/tech-stack.md it is the complete and only spec for this task
- [x] write the tests named in its Test Specification: Unit Tests and Boundary Tests (TDD — failing first is expected)
- [x] write the tests named in its Test Specification: Integration Contract Tests and Behavior Tests
- [x] implement the module's own code and wire its submodules (ingest, inputs, verdict, feedback, sweep) per its Internal Design, inside internal/gate/, replacing the Task 2 skeleton, until all its tests pass
- [x] run `go test -race ./internal/gate/...` and `golangci-lint run ./internal/gate/...` — all green before the task is complete

⚠️ internal/gate: the façade is tested with the REAL children over role-interface fakes; the exported `New(Deps)` and `NewReceiver` are exercised only up to the first GitHub call, because a real `*api.Client` needs a `TokenSource` from github/auth, which is outside gate's Integrations — ➕ the root task (Task 31) must drive the full upload path through `app` with a fake GitHub HTTP server behind the real client. A fork is exactly `HeadRepo != Repo` (a deleted fork has an empty head repository and must still be refused). A decision with no reasons is a 503, nothing published. When a missing-upload timeout and a waiver notice apply to the same PR, the notice lands on the second sweep pass (rule 4 ends the evaluation first); from the third pass nothing changes. `app` may pass a zero `Deps.Sweep` (sweep defaults to 60 min / 7 days) and must call `gate.HandleEvent` before `control.HandleEvent`. Twelve hand mutations of the upload and event paths were killed. The non-standard dependency closure of `./internal/gate/...` is wire, github, store, gate and the YAML library only. Accepted.

### Task 27 [Wave 1]: Implement internal/control/assist/ (parent)

- [x] read internal/control/assist/module.md in full — with docs/tech-stack.md it is the complete and only spec for this task
- [x] write the tests named in its Test Specification: Unit Tests and Boundary Tests (TDD — failing first is expected)
- [x] write the tests named in its Test Specification: Integration Contract Tests and Behavior Tests
- [x] implement the module's own code and wire its submodules (anthropic) per its Internal Design, inside internal/control/assist/, replacing the Task 2 skeleton, until all its tests pass
- [x] run `go test -race ./internal/control/assist/...` and `golangci-lint run ./internal/control/assist/...` — all green before the task is complete

⚠️ internal/control/assist: `policy.Waivers` is a weak "is this a policy file" check for a provider answer — one-line prose with a colon parses as a mapping and would be committed to the draft branch. Harm is bounded (a reviewable diff on a draft PR, bound to the head SHA), but a real fix needs wire/policy to publish a validation function (known top-level keys or the schema); contract change, owner decision wanted. Nine exported sentinel errors were added with the names module.md lists as [default]. `ErrProvider` carries only a kind (timeout, cancelled, error), never provider text. Accepted.

### Task 28 [Wave 1]: Implement internal/control/ui/ (parent)

- [x] read internal/control/ui/module.md in full — with docs/tech-stack.md it is the complete and only spec for this task
- [x] write the tests named in its Test Specification: Unit Tests and Boundary Tests (TDD — failing first is expected)
- [x] write the tests named in its Test Specification: Integration Contract Tests and Behavior Tests
- [x] implement the module's own code and wire its submodules (session, graph) per its Internal Design, inside internal/control/ui/, replacing the Task 2 skeleton, until all its tests pass
- [x] run `go test -race ./internal/control/ui/...` and `golangci-lint run ./internal/control/ui/...` — all green before the task is complete

⚠️ internal/control/ui: ➕ FOLLOW-UP the Monaco + monaco-yaml bundle is NOT built (no Node or network in the build session). The editor ships as a plain `<textarea>` that saves without JavaScript, and the page loads `static/vendor/monaco.bundle.js` only when it exists; `static/vendor/README.md` has the one manual esbuild step, file names and mount contract. Schema completion (product.md §5) is therefore not delivered until that step runs. Other findings: onboarding's "already onboarded" and "busy" errors are unexported, so both answer a generic 502 — exporting two sentinels from onboarding would let ui answer 409 (contract change, owner decision). `assist.Result.RunURL` cannot survive the mandated 303 without state, so ui rebuilds the workflow runs URL from the authorized repository and redirects with `?since=<head_sha>`. The status mapping had no row for `api.ErrNotFound` (→ 404) or `onboarding.ErrNoSandbox` (→ 409); both chosen here. The spec's branch pattern accepted `archfit/../main`; tightened in module.md. Per the orchestrator's instruction the sibling modules are hand fakes in ui's tests; the composed behavior is proven in the control parent and root tasks. Accepted.

### Task 29 [Wave 2]: Implement internal/control/ (parent)

- [x] read internal/control/module.md in full — with docs/tech-stack.md it is the complete and only spec for this task
- [x] write the tests named in its Test Specification: Unit Tests and Boundary Tests (TDD — failing first is expected)
- [x] write the tests named in its Test Specification: Integration Contract Tests and Behavior Tests
- [x] implement the module's own code and wire its submodules (account, proposal, onboarding, assist, ui) per its Internal Design, inside internal/control/, replacing the Task 2 skeleton, until all its tests pass
- [x] run `go test -race ./internal/control/...` and `golangci-lint run ./internal/control/...` — all green before the task is complete

⚠️ internal/control: composition tests run the REAL children over a fake GitHub HTTP server behind a real `*api.Client` and a memory Store (the pattern `app` and root tests reuse). Findings: (1) account binds `slog.Default()` when it is constructed, so `app` must call `slog.SetDefault` BEFORE `control.New` or Grant audit lines are lost; (2) ➕ onboarding edge case: if the managed onboarding PR is merged while the sandbox goroutine is still finishing, its late `Propose` opens a second PR for the already-merged branch; (3) ui/session caches an authorization per user and repository for 60 s, so a fresh sign-in right after an uninstall still reads pages until the cache expires (reads only; the "short cache" of decisions.md §6); (4) the spec's import test (`go list -deps` with no gate child) cannot pass because the gate façade imports its children; reworded in module.md to direct imports; (5) `assist.Propose` validates the request before the nil-Provider check, so `ErrInvalidRequest` wins over `ErrManagedUnavailable`. Accepted; item 2 is a follow-up for the owner.

### Task 30 [Wave 3]: Implement internal/app/ (composition root)

- [x] read internal/app/module.md in full — with docs/tech-stack.md it is the complete and only spec for this task
- [x] write the tests named in its Test Specification: Unit Tests and Boundary Tests (TDD — failing first is expected)
- [x] write the tests named in its Test Specification: Integration Contract Tests and Behavior Tests
- [x] implement the module per its Functional Responsibilities, Public Contract, and Constraints and Invariants, inside internal/app/, wiring the real gate, control, store and github packages, until all its tests pass
- [x] run `go test -race ./internal/app/...` and `golangci-lint run ./internal/app/...` — all green before the task is complete

⚠️ internal/app: (1) DESIGN TENSION, owner decision wanted: the webhook fan-out is synchronous (decisions.md §2 "handlers do their work in the request"), but GitHub drops a delivery after 10 s while the gate and control halves may each take up to 55 s (a default-branch push triggers a one-repository sweep in the request). The work still completes on a detached context; only the 202 is lost and GitHub marks the delivery failed. Options: accept, or answer 202 first and finish in a goroutine. (2) internal/github/api retries transport errors with backoff (~7 s per call), so a sweep with GitHub unreachable took 14 s in a probe; production sweeps are slow, not wrong, when GitHub is down. (3) `/internal/sweep` encodes `gate.Summary` as-is, so JSON keys are Go field names (`Repos`, `Pulls`, …) because the contract has no JSON tags. (4) UI requests carry two request IDs in the log (app's and ui's). (5) A signed but unparsable webhook answers 400; secrets files are trimmed text; OAuth client, OAuth secret and cookie key are required at startup; `Run` contacts no remote service at startup (asserted with a counting transport). Accepted except item 1.

### Task 31 [Wave 4]: Implement internal/ (root) and the entry point

- [x] read internal/module.md and internal/app/module.md in full
- [x] write the root Test Specification tests: the import-matrix tests (`go list -deps`), the upload-protocol
      contract tests, the boundary tests, and the behavior tests, in a root-level test package under internal/
- [x] replace cmd/reporter with the thin entry point the app module describes, and replace
      `TestReportIngestRefusesEveryReport` in the same change, as the root Test Specification requires
- [x] fix whatever the composed tests expose in the owning module (update its module.md first if the design was wrong)
- [x] run `make all` — all green before the task is complete

⚠️ internal/ (root): the composed tests found one REAL module defect that every per-module suite missed: `POST /v1/discoveries` accepted any short-lined text as a draft policy (all seven shared leakage fixtures, an engine report with seeded source strings included) and committed it to a pull request. Fixed before this task was closed: `wire/policy` gained the structural `Validate` (closed top-level key set derived from the embedded engine schema, `version` required), and control/onboarding (discovery and managed drafts), control/assist (provider answers) and control/ui (editor save) now require it; this also closes the weak-check finding of Task 27. Behavior change: assist no longer rejects a malformed waiver date, because Validate inspects no values; the engine check on the draft PR stays the authority. Other findings: (1) `app.Run` has no seam for the outbound HTTP client, port, clock or Store, so the root tests drive it by swapping `http.DefaultTransport` and reading the port from its log record, and every clock- or Store-dependent root test recomposes the packages as app.go does — app's own wiring of clock and Store is not covered by them (owner decision: add an unexported-to-exported seam or accept); (2) CODEOWNERS is not a protected path — the tampering row tests that a head-edited CODEOWNERS confers no approval rights; making it a sixth protected path is an owner decision; (3) unauthenticated UI GETs answer 303 to the login page, not 401/403 as the root spec words it; (4) `measurement_profile: null` decodes as `report_invalid` while an absent key gives `measurement_profile_missing` (both action_required); (5) control "proposal" log lines carry the repository full name and "assist: propose" the login, outside the strict "IDs only" wording. ➕ The capture of "UI handler panicking" is substituted by "UI failing with 5xx" because a panic cannot be provoked from outside the package.

### Task 32: Verify acceptance criteria

- [x] run the full test suite — all modules, all four test categories, must pass (`make test`)
- [x] run the validator in tree mode over internal/ — module.md edits made during implementation must have left the
      tree defect-free
- [x] run linter — all issues fixed (`make lint`)
- [ ] build the image (`docker build`) and start it with the `memory` store and no sandbox: `/healthz` answers
- [ ] verify every ⚠️ noted during implementation is resolved or explicitly accepted by the user

⚠️ Task 32 is NOT complete. Done on 2026-09-21: `make all` green (31 packages, race tests, 0 lint issues), design
validator clean on 29 documents, and a smoke run of the built binary with the `memory` store and no sandbox
(`/healthz` answers, `/internal/sweep` refuses a missing token with 401, graceful stop on SIGTERM). Not done: the
Docker image build — the Docker daemon was not running in the build session; the Dockerfile now copies `go.sum`
with `go.mod`, because the module has a dependency. Not done: the ⚠️ audit — the items marked "owner decision" in
Tasks 1, 8, 17, 19, 20, 27, 28, 29, 30 and 31 are open until the owner accepts or changes them.

### Task 33: [Final] Update documentation

- [x] update README.md, docs/README.md and .env.example for the new configuration and the end of the 501 skeleton
- [ ] verify each implemented module.md still matches what was built (spot check; /modularity:fractal-align does this
      rigorously)

⚠️ Task 33: documentation is updated. The module.md spot check is left to `/modularity:fractal-align`, which does it
rigorously; during the build every module.md was validated on each change and every test named in a Test
Specification was checked to exist.

## Post-Completion

_No checkboxes — informational._

- Run /modularity:fractal-align to verify code ↔ design alignment rigorously.
- Add this repository's own `.archfit.yaml` so that Archfit checks the gate import rule here (decisions.md §2).
- Real-world items from decisions.md §12, each needing the owner: register a test GitHub App and confirm the OIDC
  claims and permissions; confirm on Pumba that the Action can run with the base-ref policy and baseline; measure
  sweep cost against rate limits; choose the container host, KV, and sandbox, then add their driver modules.
- The envelope schema owned here must be adopted by `archfit-action` before any live upload works.
- No deployment, App installation, or invitation is authorized by this plan.

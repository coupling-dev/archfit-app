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

| Wave | Module | Submodules it composes |
| ---- | ------ | ---------------------- |
| 0a | internal/store/ | — (leaf) |
| 0a | internal/wire/envelope/ | — (leaf) |
| 0a | internal/wire/report/ | — (leaf) |
| 0a | internal/wire/settings/ | — (leaf) |
| 0a | internal/wire/policy/ | — (leaf) |
| 0a | internal/wire/manifest/ | — (leaf) |
| 0a | internal/github/auth/ | — (leaf) |
| 0a | internal/github/webhook/ | — (leaf) |
| 0a | internal/github/oidc/ | — (leaf) |
| 0b | internal/github/api/ | — (leaf) |
| 0b | internal/gate/ingest/ | — (leaf) |
| 0b | internal/gate/inputs/ | — (leaf) |
| 0b | internal/gate/verdict/ | — (leaf) |
| 0b | internal/gate/feedback/ | — (leaf) |
| 0b | internal/control/account/ | — (leaf) |
| 0b | internal/control/proposal/ | — (leaf) |
| 0b | internal/control/onboarding/ | — (leaf) |
| 0b | internal/control/assist/anthropic/ | — (leaf) |
| 0b | internal/control/ui/session/ | — (leaf) |
| 0b | internal/control/ui/graph/ | — (leaf) |
| 0c | internal/gate/sweep/ | — (leaf) |
| 1 | internal/wire/ | envelope, report, settings, policy, manifest |
| 1 | internal/github/ | auth, api, webhook, oidc |
| 1 | internal/gate/ | ingest, inputs, verdict, feedback, sweep |
| 1 | internal/control/assist/ | anthropic |
| 1 | internal/control/ui/ | session, graph |
| 2 | internal/control/ | account, proposal, onboarding, assist, ui |
| 3 | internal/app/ | — (leaf; wires gate, control, store, github) |
| 4 | internal/ (root) | app, wire, github, store, gate, control |

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

- [ ] read internal/store/module.md in full — with docs/tech-stack.md it is the complete and only spec for this task
- [ ] write the tests named in its Test Specification: Unit Tests and Boundary Tests (TDD — failing first is expected)
- [ ] write the tests named in its Test Specification: Integration Contract Tests and Behavior Tests
- [ ] implement the module per its Functional Responsibilities, Public Contract, and Constraints and Invariants, inside internal/store/, replacing the Task 2 skeleton, until all its tests pass
- [ ] run `go test -race ./internal/store/...` and `golangci-lint run ./internal/store/...` — all green before the task is complete

### Task 4 [Wave 0a]: Implement internal/wire/envelope/ (leaf, no internal imports)

- [ ] read internal/wire/envelope/module.md in full — with docs/tech-stack.md it is the complete and only spec for this task
- [ ] write the tests named in its Test Specification: Unit Tests and Boundary Tests (TDD — failing first is expected)
- [ ] write the tests named in its Test Specification: Integration Contract Tests and Behavior Tests
- [ ] implement the module per its Functional Responsibilities, Public Contract, and Constraints and Invariants, inside internal/wire/envelope/, replacing the Task 2 skeleton, until all its tests pass
- [ ] run `go test -race ./internal/wire/envelope/...` and `golangci-lint run ./internal/wire/envelope/...` — all green before the task is complete

### Task 5 [Wave 0a]: Implement internal/wire/report/ (leaf, no internal imports)

- [ ] read internal/wire/report/module.md in full — with docs/tech-stack.md it is the complete and only spec for this task
- [ ] write the tests named in its Test Specification: Unit Tests and Boundary Tests (TDD — failing first is expected)
- [ ] write the tests named in its Test Specification: Integration Contract Tests and Behavior Tests
- [ ] implement the module per its Functional Responsibilities, Public Contract, and Constraints and Invariants, inside internal/wire/report/, replacing the Task 2 skeleton, until all its tests pass
- [ ] run `go test -race ./internal/wire/report/...` and `golangci-lint run ./internal/wire/report/...` — all green before the task is complete

### Task 6 [Wave 0a]: Implement internal/wire/settings/ (leaf, no internal imports)

- [ ] read internal/wire/settings/module.md in full — with docs/tech-stack.md it is the complete and only spec for this task
- [ ] write the tests named in its Test Specification: Unit Tests and Boundary Tests (TDD — failing first is expected)
- [ ] write the tests named in its Test Specification: Integration Contract Tests and Behavior Tests
- [ ] implement the module per its Functional Responsibilities, Public Contract, and Constraints and Invariants, inside internal/wire/settings/, replacing the Task 2 skeleton, until all its tests pass
- [ ] run `go test -race ./internal/wire/settings/...` and `golangci-lint run ./internal/wire/settings/...` — all green before the task is complete

### Task 7 [Wave 0a]: Implement internal/wire/policy/ (leaf, no internal imports)

- [ ] read internal/wire/policy/module.md in full — with docs/tech-stack.md it is the complete and only spec for this task
- [ ] write the tests named in its Test Specification: Unit Tests and Boundary Tests (TDD — failing first is expected)
- [ ] write the tests named in its Test Specification: Integration Contract Tests and Behavior Tests
- [ ] implement the module per its Functional Responsibilities, Public Contract, and Constraints and Invariants, inside internal/wire/policy/, replacing the Task 2 skeleton, until all its tests pass
- [ ] run `go test -race ./internal/wire/policy/...` and `golangci-lint run ./internal/wire/policy/...` — all green before the task is complete

### Task 8 [Wave 0a]: Implement internal/wire/manifest/ (leaf, no internal imports)

- [ ] read internal/wire/manifest/module.md in full — with docs/tech-stack.md it is the complete and only spec for this task
- [ ] write the tests named in its Test Specification: Unit Tests and Boundary Tests (TDD — failing first is expected)
- [ ] write the tests named in its Test Specification: Integration Contract Tests and Behavior Tests
- [ ] implement the module per its Functional Responsibilities, Public Contract, and Constraints and Invariants, inside internal/wire/manifest/, replacing the Task 2 skeleton, until all its tests pass
- [ ] run `go test -race ./internal/wire/manifest/...` and `golangci-lint run ./internal/wire/manifest/...` — all green before the task is complete

### Task 9 [Wave 0a]: Implement internal/github/auth/ (leaf, no internal imports)

- [ ] read internal/github/auth/module.md in full — with docs/tech-stack.md it is the complete and only spec for this task
- [ ] write the tests named in its Test Specification: Unit Tests and Boundary Tests (TDD — failing first is expected)
- [ ] write the tests named in its Test Specification: Integration Contract Tests and Behavior Tests
- [ ] implement the module per its Functional Responsibilities, Public Contract, and Constraints and Invariants, inside internal/github/auth/, replacing the Task 2 skeleton, until all its tests pass
- [ ] run `go test -race ./internal/github/auth/...` and `golangci-lint run ./internal/github/auth/...` — all green before the task is complete

### Task 10 [Wave 0a]: Implement internal/github/webhook/ (leaf, no internal imports)

- [ ] read internal/github/webhook/module.md in full — with docs/tech-stack.md it is the complete and only spec for this task
- [ ] write the tests named in its Test Specification: Unit Tests and Boundary Tests (TDD — failing first is expected)
- [ ] write the tests named in its Test Specification: Integration Contract Tests and Behavior Tests
- [ ] implement the module per its Functional Responsibilities, Public Contract, and Constraints and Invariants, inside internal/github/webhook/, replacing the Task 2 skeleton, until all its tests pass
- [ ] run `go test -race ./internal/github/webhook/...` and `golangci-lint run ./internal/github/webhook/...` — all green before the task is complete

### Task 11 [Wave 0a]: Implement internal/github/oidc/ (leaf, no internal imports)

- [ ] read internal/github/oidc/module.md in full — with docs/tech-stack.md it is the complete and only spec for this task
- [ ] write the tests named in its Test Specification: Unit Tests and Boundary Tests (TDD — failing first is expected)
- [ ] write the tests named in its Test Specification: Integration Contract Tests and Behavior Tests
- [ ] implement the module per its Functional Responsibilities, Public Contract, and Constraints and Invariants, inside internal/github/oidc/, replacing the Task 2 skeleton, until all its tests pass
- [ ] run `go test -race ./internal/github/oidc/...` and `golangci-lint run ./internal/github/oidc/...` — all green before the task is complete

### Task 12 [Wave 0b]: Implement internal/github/api/ (leaf, calls only wave 0a code or interfaces)

- [ ] read internal/github/api/module.md in full — with docs/tech-stack.md it is the complete and only spec for this task
- [ ] write the tests named in its Test Specification: Unit Tests and Boundary Tests (TDD — failing first is expected)
- [ ] write the tests named in its Test Specification: Integration Contract Tests and Behavior Tests
- [ ] implement the module per its Functional Responsibilities, Public Contract, and Constraints and Invariants, inside internal/github/api/, replacing the Task 2 skeleton, until all its tests pass
- [ ] run `go test -race ./internal/github/api/...` and `golangci-lint run ./internal/github/api/...` — all green before the task is complete

### Task 13 [Wave 0b]: Implement internal/gate/ingest/ (leaf, calls only wave 0a code or interfaces)

- [ ] read internal/gate/ingest/module.md in full — with docs/tech-stack.md it is the complete and only spec for this task
- [ ] write the tests named in its Test Specification: Unit Tests and Boundary Tests (TDD — failing first is expected)
- [ ] write the tests named in its Test Specification: Integration Contract Tests and Behavior Tests
- [ ] implement the module per its Functional Responsibilities, Public Contract, and Constraints and Invariants, inside internal/gate/ingest/, replacing the Task 2 skeleton, until all its tests pass
- [ ] run `go test -race ./internal/gate/ingest/...` and `golangci-lint run ./internal/gate/ingest/...` — all green before the task is complete

### Task 14 [Wave 0b]: Implement internal/gate/inputs/ (leaf, calls only wave 0a code or interfaces)

- [ ] read internal/gate/inputs/module.md in full — with docs/tech-stack.md it is the complete and only spec for this task
- [ ] write the tests named in its Test Specification: Unit Tests and Boundary Tests (TDD — failing first is expected)
- [ ] write the tests named in its Test Specification: Integration Contract Tests and Behavior Tests
- [ ] implement the module per its Functional Responsibilities, Public Contract, and Constraints and Invariants, inside internal/gate/inputs/, replacing the Task 2 skeleton, until all its tests pass
- [ ] run `go test -race ./internal/gate/inputs/...` and `golangci-lint run ./internal/gate/inputs/...` — all green before the task is complete

### Task 15 [Wave 0b]: Implement internal/gate/verdict/ (leaf, calls only wave 0a code or interfaces)

- [ ] read internal/gate/verdict/module.md in full — with docs/tech-stack.md it is the complete and only spec for this task
- [ ] write the tests named in its Test Specification: Unit Tests and Boundary Tests (TDD — failing first is expected)
- [ ] write the tests named in its Test Specification: Integration Contract Tests and Behavior Tests
- [ ] implement the module per its Functional Responsibilities, Public Contract, and Constraints and Invariants, inside internal/gate/verdict/, replacing the Task 2 skeleton, until all its tests pass
- [ ] run `go test -race ./internal/gate/verdict/...` and `golangci-lint run ./internal/gate/verdict/...` — all green before the task is complete

### Task 16 [Wave 0b]: Implement internal/gate/feedback/ (leaf, calls only wave 0a code or interfaces)

- [ ] read internal/gate/feedback/module.md in full — with docs/tech-stack.md it is the complete and only spec for this task
- [ ] write the tests named in its Test Specification: Unit Tests and Boundary Tests (TDD — failing first is expected)
- [ ] write the tests named in its Test Specification: Integration Contract Tests and Behavior Tests
- [ ] implement the module per its Functional Responsibilities, Public Contract, and Constraints and Invariants, inside internal/gate/feedback/, replacing the Task 2 skeleton, until all its tests pass
- [ ] run `go test -race ./internal/gate/feedback/...` and `golangci-lint run ./internal/gate/feedback/...` — all green before the task is complete

### Task 17 [Wave 0b]: Implement internal/control/account/ (leaf, calls only wave 0a code or interfaces)

- [ ] read internal/control/account/module.md in full — with docs/tech-stack.md it is the complete and only spec for this task
- [ ] write the tests named in its Test Specification: Unit Tests and Boundary Tests (TDD — failing first is expected)
- [ ] write the tests named in its Test Specification: Integration Contract Tests and Behavior Tests
- [ ] implement the module per its Functional Responsibilities, Public Contract, and Constraints and Invariants, inside internal/control/account/, replacing the Task 2 skeleton, until all its tests pass
- [ ] run `go test -race ./internal/control/account/...` and `golangci-lint run ./internal/control/account/...` — all green before the task is complete

### Task 18 [Wave 0b]: Implement internal/control/proposal/ (leaf, calls only wave 0a code or interfaces)

- [ ] read internal/control/proposal/module.md in full — with docs/tech-stack.md it is the complete and only spec for this task
- [ ] write the tests named in its Test Specification: Unit Tests and Boundary Tests (TDD — failing first is expected)
- [ ] write the tests named in its Test Specification: Integration Contract Tests and Behavior Tests
- [ ] implement the module per its Functional Responsibilities, Public Contract, and Constraints and Invariants, inside internal/control/proposal/, replacing the Task 2 skeleton, until all its tests pass
- [ ] run `go test -race ./internal/control/proposal/...` and `golangci-lint run ./internal/control/proposal/...` — all green before the task is complete

### Task 19 [Wave 0b]: Implement internal/control/onboarding/ (leaf, calls only wave 0a code or interfaces)

- [ ] read internal/control/onboarding/module.md in full — with docs/tech-stack.md it is the complete and only spec for this task
- [ ] write the tests named in its Test Specification: Unit Tests and Boundary Tests (TDD — failing first is expected)
- [ ] write the tests named in its Test Specification: Integration Contract Tests and Behavior Tests
- [ ] implement the module per its Functional Responsibilities, Public Contract, and Constraints and Invariants, inside internal/control/onboarding/, replacing the Task 2 skeleton, until all its tests pass
- [ ] run `go test -race ./internal/control/onboarding/...` and `golangci-lint run ./internal/control/onboarding/...` — all green before the task is complete

### Task 20 [Wave 0b]: Implement internal/control/assist/anthropic/ (leaf, calls only wave 0a code or interfaces)

- [ ] read internal/control/assist/anthropic/module.md in full — with docs/tech-stack.md it is the complete and only spec for this task
- [ ] write the tests named in its Test Specification: Unit Tests and Boundary Tests (TDD — failing first is expected)
- [ ] write the tests named in its Test Specification: Integration Contract Tests and Behavior Tests
- [ ] implement the module per its Functional Responsibilities, Public Contract, and Constraints and Invariants, inside internal/control/assist/anthropic/, replacing the Task 2 skeleton, until all its tests pass
- [ ] run `go test -race ./internal/control/assist/anthropic/...` and `golangci-lint run ./internal/control/assist/anthropic/...` — all green before the task is complete

### Task 21 [Wave 0b]: Implement internal/control/ui/session/ (leaf, calls only wave 0a code or interfaces)

- [ ] read internal/control/ui/session/module.md in full — with docs/tech-stack.md it is the complete and only spec for this task
- [ ] write the tests named in its Test Specification: Unit Tests and Boundary Tests (TDD — failing first is expected)
- [ ] write the tests named in its Test Specification: Integration Contract Tests and Behavior Tests
- [ ] implement the module per its Functional Responsibilities, Public Contract, and Constraints and Invariants, inside internal/control/ui/session/, replacing the Task 2 skeleton, until all its tests pass
- [ ] run `go test -race ./internal/control/ui/session/...` and `golangci-lint run ./internal/control/ui/session/...` — all green before the task is complete

### Task 22 [Wave 0b]: Implement internal/control/ui/graph/ (leaf, calls only wave 0a code or interfaces)

- [ ] read internal/control/ui/graph/module.md in full — with docs/tech-stack.md it is the complete and only spec for this task
- [ ] write the tests named in its Test Specification: Unit Tests and Boundary Tests (TDD — failing first is expected)
- [ ] write the tests named in its Test Specification: Integration Contract Tests and Behavior Tests
- [ ] implement the module per its Functional Responsibilities, Public Contract, and Constraints and Invariants, inside internal/control/ui/graph/, replacing the Task 2 skeleton, until all its tests pass
- [ ] run `go test -race ./internal/control/ui/graph/...` and `golangci-lint run ./internal/control/ui/graph/...` — all green before the task is complete

### Task 23 [Wave 0c]: Implement internal/gate/sweep/ (leaf, calls wave 0b code)

- [ ] read internal/gate/sweep/module.md in full — with docs/tech-stack.md it is the complete and only spec for this task
- [ ] write the tests named in its Test Specification: Unit Tests and Boundary Tests (TDD — failing first is expected)
- [ ] write the tests named in its Test Specification: Integration Contract Tests and Behavior Tests
- [ ] implement the module per its Functional Responsibilities, Public Contract, and Constraints and Invariants, inside internal/gate/sweep/, replacing the Task 2 skeleton, until all its tests pass
- [ ] run `go test -race ./internal/gate/sweep/...` and `golangci-lint run ./internal/gate/sweep/...` — all green before the task is complete

### Task 24 [Wave 1]: Implement internal/wire/ (parent)

- [ ] read internal/wire/module.md in full — with docs/tech-stack.md it is the complete and only spec for this task
- [ ] write the tests named in its Test Specification: Unit Tests and Boundary Tests (TDD — failing first is expected)
- [ ] write the tests named in its Test Specification: Integration Contract Tests and Behavior Tests
- [ ] implement the module's own code and wire its submodules (envelope, report, settings, policy, manifest) per its Internal Design, inside internal/wire/, replacing the Task 2 skeleton, until all its tests pass
- [ ] run `go test -race ./internal/wire/...` and `golangci-lint run ./internal/wire/...` — all green before the task is complete

### Task 25 [Wave 1]: Implement internal/github/ (parent)

- [ ] read internal/github/module.md in full — with docs/tech-stack.md it is the complete and only spec for this task
- [ ] write the tests named in its Test Specification: Unit Tests and Boundary Tests (TDD — failing first is expected)
- [ ] write the tests named in its Test Specification: Integration Contract Tests and Behavior Tests
- [ ] implement the module's own code and wire its submodules (auth, api, webhook, oidc) per its Internal Design, inside internal/github/, replacing the Task 2 skeleton, until all its tests pass
- [ ] run `go test -race ./internal/github/...` and `golangci-lint run ./internal/github/...` — all green before the task is complete

### Task 26 [Wave 1]: Implement internal/gate/ (parent)

- [ ] read internal/gate/module.md in full — with docs/tech-stack.md it is the complete and only spec for this task
- [ ] write the tests named in its Test Specification: Unit Tests and Boundary Tests (TDD — failing first is expected)
- [ ] write the tests named in its Test Specification: Integration Contract Tests and Behavior Tests
- [ ] implement the module's own code and wire its submodules (ingest, inputs, verdict, feedback, sweep) per its Internal Design, inside internal/gate/, replacing the Task 2 skeleton, until all its tests pass
- [ ] run `go test -race ./internal/gate/...` and `golangci-lint run ./internal/gate/...` — all green before the task is complete

### Task 27 [Wave 1]: Implement internal/control/assist/ (parent)

- [ ] read internal/control/assist/module.md in full — with docs/tech-stack.md it is the complete and only spec for this task
- [ ] write the tests named in its Test Specification: Unit Tests and Boundary Tests (TDD — failing first is expected)
- [ ] write the tests named in its Test Specification: Integration Contract Tests and Behavior Tests
- [ ] implement the module's own code and wire its submodules (anthropic) per its Internal Design, inside internal/control/assist/, replacing the Task 2 skeleton, until all its tests pass
- [ ] run `go test -race ./internal/control/assist/...` and `golangci-lint run ./internal/control/assist/...` — all green before the task is complete

### Task 28 [Wave 1]: Implement internal/control/ui/ (parent)

- [ ] read internal/control/ui/module.md in full — with docs/tech-stack.md it is the complete and only spec for this task
- [ ] write the tests named in its Test Specification: Unit Tests and Boundary Tests (TDD — failing first is expected)
- [ ] write the tests named in its Test Specification: Integration Contract Tests and Behavior Tests
- [ ] implement the module's own code and wire its submodules (session, graph) per its Internal Design, inside internal/control/ui/, replacing the Task 2 skeleton, until all its tests pass
- [ ] run `go test -race ./internal/control/ui/...` and `golangci-lint run ./internal/control/ui/...` — all green before the task is complete

### Task 29 [Wave 2]: Implement internal/control/ (parent)

- [ ] read internal/control/module.md in full — with docs/tech-stack.md it is the complete and only spec for this task
- [ ] write the tests named in its Test Specification: Unit Tests and Boundary Tests (TDD — failing first is expected)
- [ ] write the tests named in its Test Specification: Integration Contract Tests and Behavior Tests
- [ ] implement the module's own code and wire its submodules (account, proposal, onboarding, assist, ui) per its Internal Design, inside internal/control/, replacing the Task 2 skeleton, until all its tests pass
- [ ] run `go test -race ./internal/control/...` and `golangci-lint run ./internal/control/...` — all green before the task is complete

### Task 30 [Wave 3]: Implement internal/app/ (composition root)

- [ ] read internal/app/module.md in full — with docs/tech-stack.md it is the complete and only spec for this task
- [ ] write the tests named in its Test Specification: Unit Tests and Boundary Tests (TDD — failing first is expected)
- [ ] write the tests named in its Test Specification: Integration Contract Tests and Behavior Tests
- [ ] implement the module per its Functional Responsibilities, Public Contract, and Constraints and Invariants, inside internal/app/, wiring the real gate, control, store and github packages, until all its tests pass
- [ ] run `go test -race ./internal/app/...` and `golangci-lint run ./internal/app/...` — all green before the task is complete

### Task 31 [Wave 4]: Implement internal/ (root) and the entry point

- [ ] read internal/module.md and internal/app/module.md in full
- [ ] write the root Test Specification tests: the import-matrix tests (`go list -deps`), the upload-protocol
      contract tests, the boundary tests, and the behavior tests, in a root-level test package under internal/
- [ ] replace cmd/reporter with the thin entry point the app module describes, and replace
      `TestReportIngestRefusesEveryReport` in the same change, as the root Test Specification requires
- [ ] fix whatever the composed tests expose in the owning module (update its module.md first if the design was wrong)
- [ ] run `make all` — all green before the task is complete

### Task 32: Verify acceptance criteria

- [ ] run the full test suite — all modules, all four test categories, must pass (`make test`)
- [ ] run the validator in tree mode over internal/ — module.md edits made during implementation must have left the
      tree defect-free
- [ ] run linter — all issues fixed (`make lint`)
- [ ] build the image (`docker build`) and start it with the `memory` store and no sandbox: `/healthz` answers
- [ ] verify every ⚠️ noted during implementation is resolved or explicitly accepted by the user

### Task 33: [Final] Update documentation

- [ ] update README.md, docs/README.md and .env.example for the new configuration and the end of the 501 skeleton
- [ ] verify each implemented module.md still matches what was built (spot check; /modularity:fractal-align does this
      rigorously)

## Post-Completion

_No checkboxes — informational._

- Run /modularity:fractal-align to verify code ↔ design alignment rigorously.
- Add this repository's own `.archfit.yaml` so that Archfit checks the gate import rule here (decisions.md §2).
- Real-world items from decisions.md §12, each needing the owner: register a test GitHub App and confirm the OIDC
  claims and permissions; confirm on Pumba that the Action can run with the base-ref policy and baseline; measure
  sweep cost against rate limits; choose the container host, KV, and sandbox, then add their driver modules.
- The envelope schema owned here must be adopted by `archfit-action` before any live upload works.
- No deployment, App installation, or invitation is authorized by this plan.

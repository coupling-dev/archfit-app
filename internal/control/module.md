# Control plane

**Path**: internal/control/
**Submodules**: account, answers, onboarding, passkey, ui

## Purpose

The facade composes account lifecycle, consent records, Archfit 4 setup reads, passkeys, request answers,
owner actions and the UI. The gate remains independent of the control plane.

## Public contract

`New(Deps)` builds the public/all-role control plane. It requires GitHub, OAuth, Store, admissions,
policy/request views, answers, a 32-byte cookie key and a base URL. A nil clock uses time.Now.
The gate's owner/setup ports are passed to control/answers and onboarding.

`UIHandler` serves the UI through a panic boundary. `HandleEvent`, `Sweep` and `Grant` maintain
installation/account state. `NewWorker(WorkerDeps)` needs no UI credentials and serves no UI handler.
Admission errors are retried rather than dropping installation work.

There is no discovery receiver or Sandbox dependency. Discovery, baseline and reanchor upload handlers,
two-PR v3 onboarding and its proposal service have been removed. The app explicitly returns 404 for
their upload endpoints. Schema 3 setup and owner actions are preserved.

## Integrations and checks

The facade imports its children, the gate facade, GitHub roles and Store; external packages do not import
control children. `unit_test.go` checks wiring and worker isolation. Account lifecycle, uninstall cleanup,
failed reads, authorization and panic containment are exercised by the facade tests.
Child modules own the setup, passkey, consent and answer contracts and their tests.

## Home composition

`New` passes `HomeViews: gate.NewHome(d.GitHub, d.Store, d.Admissions)` to `ui.New`.
The read-only Home façade owns all access to gate records and uses only the already-wired dependencies;
`control.Deps` needs no new App wiring. See [the UI Home contract](ui/module.md#home-api-m3-task-4).

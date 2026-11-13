# Archfit App internals

**Path**: internal/
**Submodules**: app, control, gate, github, wire, store, queue, gcptoken

## Responsibilities and boundaries

- [app](app/module.md) loads configuration/secrets, composes services, mounts role-specific HTTP routes
  and dispatches authenticated webhooks and operator/worker jobs.
- [gate](gate/module.md) authenticates envelope 2 uploads, checks protected policy/workflow and approval
  authority, publishes results, handles requests/answers/owner actions and reconciles stored v4 state.
- [control](control/module.md) provides installation admission, account/consent lifecycle, setup reads,
  passkeys, owner/answer adapters and the authenticated browser surface.
- [wire](wire/module.md) owns versioned transport contracts, not I/O, gate decisions or persistence.
- GitHub/OIDC/auth clients, Store, Queue and Google token support are independent infrastructure boundaries.

Outside code imports the gate/control facade, never its children. The gate imports no control/app/UI/LLM.
Only policy3 and workflow import the YAML library; only control/passkey imports go-webauthn.
`imports_test.go` enforces these rules and prevents removed Archfit 3 packages from returning.

## Accepted inputs

POST /v1/reports accepts upload envelope 2 with engine result v1 only. Policy is schema 3 and identities
come from manifest 4. Provenance is necessary, not independent verification of report content.
Protected policy digests and recorded owner approval authority remain required.

Envelope 1, engine architecture-state reports, schema 2 policy parsing, App settings, manifest 1,
discovery/baseline/reanchor flows and their fixtures are removed. Their upload endpoints and the v2
policy schema endpoint return 404. Existing page shells stay mounted; legacy-only data APIs return
an explicit 409 while Archfit 4 page data replaces them.

## Verification

Run `make all` before pushing. Retained v4 behavior is tested in the gate, control, wire and app children.
Import tests and wire boundary tests enforce package removal, dependency direction and no-I/O contracts.

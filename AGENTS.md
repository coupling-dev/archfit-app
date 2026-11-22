# archfit-app

Private Go service for tenancy, approved policy, baselines, and PR feedback.

Start here: new work follows the [Archfit 4 design](https://github.com/coupling-dev/archfit/blob/main/docs/plans/20261110-archfit-4-architecture-change-control.md), the current design for the engine, the Action, and this App.
The code today implements the first version; the requirements of that shipped behavior are in [`docs/archive/design/`](docs/archive/design/README.md).

- Never trust head-tree config, labels, or baselines. Resolve gate inputs from a protected ref and verify the policy digest.
- Fail closed. Unverifiable reports must not become `neutral` or `skipped`. There are two bounded exceptions, and only these:
  - The unguarded setup pull request (no valid policy on the default branch, no approval, no protected state) gets a neutral "no verdict yet" check, because no policy is in force and the App adjudicates nothing from head files.
  - In advisory mode, an `unknown` of a verified report that has no verdict yet (the engine's own unknown, such as no base or missing evidence, the first policy, or a policy that changed since the run) is neutral and says why and the one next step.
  An unverifiable upload (binding, identity, digest or target mismatch, a changed workflow, an engine the App does not list) and any `unknown` in required mode stay `action_required`.
- Provenance is necessary, not sufficient. Never claim an attested report is verified.
- Keep LLM output off the gate path.
- Use stdlib-first Go and keep versioned wire contracts separate from domain/storage types.
- Run `make all` before pushing.

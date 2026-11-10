# archfit-app

Private Go service for tenancy, approved policy, baselines, and PR feedback.

Start here: new work follows the [Archfit 4 design](https://github.com/alexei-led/archfit/blob/main/docs/plans/20261110-archfit-4-architecture-change-control.md), the current design for the engine, the Action, and this App.
The code today implements the first version; the requirements of that shipped behavior are in [`docs/archive/design/`](docs/archive/design/README.md).

- Never trust head-tree config, labels, or baselines. Resolve gate inputs from a protected ref and verify the policy digest.
- Fail closed. Unverifiable reports must not become `neutral` or `skipped`.
- Provenance is necessary, not sufficient. Never claim an attested report is verified.
- Keep LLM output off the gate path.
- Use stdlib-first Go and keep versioned wire contracts separate from domain/storage types.
- Run `make all` before pushing.

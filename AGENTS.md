# archfit-app

Private Go service for tenancy, approved policy, baselines, and PR feedback.

- Never trust head-tree config, labels, or baselines. Resolve gate inputs from a protected ref and verify the policy digest.
- Fail closed. Unverifiable reports must not become `neutral` or `skipped`.
- Provenance is necessary, not sufficient. Never claim an attested report is verified.
- Keep LLM output off the gate path.
- Use stdlib-first Go and keep versioned wire contracts separate from domain/storage types.
- Run `make all` before pushing.

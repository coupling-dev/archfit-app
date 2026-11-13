# Workflow calls

**Path**: internal/wire/workflow/
**Parent**: internal/wire/
**Submodules**: none (leaf)

## Purpose

Extract GitHub Actions job/step `uses:` calls for onboarding admission without importing YAML parsing
into control. `UsesRefs([]byte) []string` returns sorted calls; invalid YAML returns none. Comments,
shell text, environment values and unrelated fields are not calls.

`ActionRef(uses, repository) (string, bool)` matches the repository without case and extracts its ref.
It does not resolve or approve a ref. Onboarding applies manifest 4 and public-tag resolution.

## Constraints

No filesystem, network, storage or control/gate dependencies. This parser does not validate a complete
GitHub Actions workflow or prove that a job will execute.

## Tests

`workflow_test.go` covers job/step extraction, invalid YAML, non-call text, casing and repository matching.

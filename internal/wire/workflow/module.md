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

`ActionEndpoint(raw, repository) (string, bool)` returns the `endpoint` input of the first step, in job-name order,
that calls the repository, and whether it is non-empty. The gate's request lines use it to answer only when the
workflow sends its results to this App. It returns the value as written: an expression is not evaluated.

## Constraints

No filesystem, network, storage or control/gate dependencies. This parser does not validate a complete
GitHub Actions workflow or prove that a job will execute.

## Tests

`workflow_test.go` covers job/step extraction, invalid YAML, non-call text, casing and repository matching, and the
Action's `endpoint` input: literal, quoted, expression, empty, absent, another Action, job order, `run:` text.

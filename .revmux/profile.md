# archfit-app review profile

## What this is

- A private Go GitHub App (one static binary). The archfit engine runs in customer CI
  (archfit-action); the App verifies the uploaded report against approved inputs read
  from the protected ref and publishes GitHub check runs and a PR comment.
- Two checks: `archfit/state` (advisory) and `archfit/gate` (required when enabled).
  Gate conclusions are only `success`, `failure` or `action_required`.
- Rules are in `AGENTS.md`; behaviour in `docs/architecture.md`, `docs/user-guide.md`,
  `docs/design/{decisions,trust-and-reporting}.md`; every package has a `module.md`.

## What a real defect looks like here

- Fail-open: any path where an unverifiable, malformed, forged or missing report, a
  decoder error, a GitHub API error, a stale input or an unknown engine identity ends as
  `success`, `neutral` or `skipped` on the gate, or where a required check stays pending
  forever instead of `action_required` with a reason.
- Trust breaks: gate inputs (policy, baseline, labels, settings, workflow) read from the
  PR head or a branch name instead of the protected ref at a pinned commit; a head policy
  accepted without an owner approval on the exact head SHA; envelope identity trusted
  without rebinding to the GitHub API; OIDC audience or workflow not checked; anything
  committed to the default branch by the App.
- Provenance claims: text that calls an attested report "verified".
- LLM output, billing or UI code reaching the gate decision.
- Wire contract drift: a decoder that accepts unknown keys or out-of-set enums, caps that
  allow unbounded memory, a field used by the frozen decision core that is not present in
  every supported engine release, a schema that disagrees with its decoder.
- Tenant isolation: one installation's data reachable from another's request.
- GitHub mechanics: duplicate or stale annotations, comments re-posted instead of
  edited, an older publication overwriting a newer one, more than 50 annotations per
  request, summaries over 65,535 bytes.
- Logging secrets, tokens, report bodies or source identifiers (only IDs, digests,
  outcomes and timings may be logged).
- `module.md` contracts that contradict the code they describe.

## Blast radius

- Every pilot repository's merge gate. A fail-open lets architecture erosion merge under a
  green required check; a false `action_required` blocks a team's merges.

## Reporting bar

- Report: the defects above, security (injection into GitHub markdown, path traversal,
  SSRF via configured URLs), missing tests for failure paths and boundaries, docs that
  contradict behaviour.
- Do not report: style the linters own (gofmt, goimports, golangci-lint), naming taste,
  speculative abstractions, prose polish.

## Deliberate conventions (not defects)

- The App never runs customer code; managed discovery sits behind a `Sandbox` seam.
- Provenance is "customer-attested", never "verified".
- `needs_attention` with nothing to act on is `success` with reason `attention_only`.
- The store has only an in-memory driver today (persistent storage is a known release
  blocker).
- Stdlib-first Go; wire types are separate from domain and storage types.

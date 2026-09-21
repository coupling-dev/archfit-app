# archfit-app

Private Go service for approved architecture policy, baselines, and PR feedback.
See [LICENSE](LICENSE). Analysis runs in customer CI; this service owns the
trusted policy, report intake, gate, and feedback.

The three repositories are split by responsibility:

- [`archfit`](https://github.com/alexei-led/archfit): engine, CLI, images, and
  wire contracts.
- [`archfit-action`](https://github.com/alexei-led/archfit-action): execution and
  upload adapter.
- `archfit-app`: tenancy, approved policy, baselines, and PR feedback.

## Status

The service is implemented as one Go binary (`cmd/reporter` → `internal/app`) and
covered by tests, but it is **not deployed and not release-ready**:

- It runs with the in-memory `Store` and no sandbox driver; the container host,
  KV, and sandbox are not chosen ([tech stack](docs/tech-stack.md)).
- `internal/wire/manifest` still holds placeholder image digests and a
  placeholder Action commit.
- The Monaco editor bundle is not built; the policy editor is a plain textarea.
- No GitHub App is registered and `archfit-action` has not adopted the envelope
  schema owned here.

Open findings and owner decisions are marked ⚠️ and ➕ in the
[implementation plan](docs/plans/2026-09-21-implement-archfit-app.md).

## Documentation

See the [documentation index](docs/README.md):

- [Product design](docs/design/product.md)
- [Trust and reporting](docs/design/trust-and-reporting.md)
- [Module design](internal/module.md) — every package under `internal/` has a `module.md` spec
- [Tech stack](docs/tech-stack.md)
- [Implementation plan](docs/plans/2026-09-21-implement-archfit-app.md)
- [Closed-beta plan](docs/plans/closed-beta.md)
- [Hosting research](docs/research/20260918-hosting.md)

## Development

Enable hooks once per clone:

```sh
git config core.hooksPath scripts/git-hooks
```

```sh
make test     # race tests
make lint     # golangci-lint
make build    # .bin/reporter
make run      # :8080
```

Configuration is environment variables; secrets are file paths (`*_FILE`), never
values. See [`.env.example`](.env.example). The binary does not load `.env`
itself. `make all` runs format, lint, tests, and build; run it before pushing.
Health check:

```sh
curl -s localhost:8080/healthz
```

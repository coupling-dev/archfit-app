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

The reporter is a skeleton. It validates request shape, then returns `501` for
valid reports because provenance verification is not implemented yet. See
[`cmd/reporter/main_test.go`](cmd/reporter/main_test.go).

## Documentation

See the [documentation index](docs/README.md):

- [Product design](docs/design/product.md)
- [Trust and reporting](docs/design/trust-and-reporting.md)
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

The reporter reads `ARCHFIT_ADDR` only; it does not load `.env` automatically.
Health check:

```sh
curl -s localhost:8080/healthz
```

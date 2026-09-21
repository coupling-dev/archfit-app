# archfit-app

Archfit finds pull requests that break the architecture rules of a repository.
This service is the trusted part of Archfit. It is a private Go service. See [LICENSE](LICENSE).

The analysis runs in customer CI. This service does the rest:

- It keeps the approved policy under review, like code.
- It verifies each uploaded report against the approved policy.
- It publishes the result on the pull request: one or two checks and one comment.
- It fails closed. A report that it cannot verify never passes the gate.

Archfit has three repositories:

| Repository                                                       | Responsibility                                     |
| ---------------------------------------------------------------- | -------------------------------------------------- |
| [`archfit`](https://github.com/alexei-led/archfit)               | Engine, CLI, images, and report schemas            |
| [`archfit-action`](https://github.com/alexei-led/archfit-action) | Runs the engine in CI and uploads the report       |
| `archfit-app`                                                    | Approved policy, report intake, gate, and feedback |

## Status

The service is complete as code and has tests. It is **not deployed and not ready for release**.

- Hosting is not decided. The service runs with an in-memory store.
- The engine manifest contains placeholder digests.
- No GitHub App is registered.

[Release blockers](docs/architecture.md#release-blockers) gives the full list.

## Documentation

| Document                                    | Read it to                                      |
| ------------------------------------------- | ----------------------------------------------- |
| [Architecture](docs/architecture.md)        | Understand how the service works, with diagrams |
| [User guide](docs/user-guide.md)            | Onboard a repository and read the feedback      |
| [Documentation index](docs/README.md)       | Find the design documents, plans, and research  |
| [Module specifications](internal/module.md) | Read the detailed specification of each package |

## Development

Enable the Git hooks one time for each clone:

```sh
git config core.hooksPath scripts/git-hooks
```

```sh
make all      # format, lint, test, build. Run it before you push
make test     # race tests
make lint     # golangci-lint
make build    # .bin/reporter
make run      # :8080
```

The configuration is environment variables. Each secret is a file path (`*_FILE`), never a value.
The binary does not read a `.env` file. See [`.env.example`](.env.example) and the
[configuration table](docs/architecture.md#configuration).

Health check:

```sh
curl -s localhost:8080/healthz
```

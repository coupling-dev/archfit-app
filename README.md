# archfit-app

The App is a private Go service for Archfit. The Archfit engine runs in customer CI.
The App verifies the report against approved repository inputs and publishes GitHub feedback.
A report that the App cannot verify never passes the gate.

## Status

The code is complete and tested. The App is not deployed and is not ready for release.
Hosting, the GitHub App registration, and release artifacts remain open.
See the [release blockers](docs/architecture.md#release-blockers).

## Read next

| Reader                           | Start here                                                                  |
| -------------------------------- | --------------------------------------------------------------------------- |
| New developer                    | [Architecture](docs/architecture.md), then [tech stack](docs/tech-stack.md) |
| Repository admin or policy owner | [User guide](docs/user-guide.md)                                            |
| Operator                         | [User guide operator section](docs/user-guide.md#for-the-operator)          |
| Design reader                    | [Documentation index](docs/README.md)                                       |

The three repositories have separate roles:

| Repository                                                       | Role                                               |
| ---------------------------------------------------------------- | -------------------------------------------------- |
| [`archfit`](https://github.com/alexei-led/archfit)               | Engine, CLI, images, and report schemas            |
| [`archfit-action`](https://github.com/alexei-led/archfit-action) | Runs the engine in CI and uploads the report       |
| `archfit-app`                                                    | Approved policy, report intake, gate, and feedback |

## Development

Enable the Git hooks once in each clone:

```sh
git config core.hooksPath scripts/git-hooks
```

Run the full local check before you push:

```sh
make all      # format, lint, test, build
make test     # race tests
make lint     # golangci-lint
make build    # .bin/reporter
make run      # start the service on the configured address
make deploy-check  # shellcheck, actionlint, and the release script tests (scripts/deploy)
```

Export the variables in [`.env.example`](.env.example) before `make run`.
The App reads environment variables. It does not read a `.env` file.
Each secret variable names a file with a `*_FILE` suffix.
See the [configuration table](docs/architecture.md#configuration).

Check a running local service:

```sh
curl -s localhost:8080/healthz
```

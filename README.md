# Archfit App

Archfit checks each pull request against the architecture rules in your repository and posts the result on GitHub.
This repository is the hosted GitHub App. Your CI runs the engine and sends the App a report with no source code.
The App checks the report against your approved policy, then publishes a check and a comment on the pull request.

## Archfit 4 input contract

Only upload envelope 2, result v1, schema 3 policy and manifest 4 are supported.
The discovery/baseline/reanchor upload URLs and /schema/policy.json return 404.
The App no longer reads `.archfit-app.yaml` or Archfit 3 reports. See the [user guide](docs/user-guide.md)
for the current pages and the [module contracts](internal/module.md) for implementation limits.

## Repository owners

- **Install:** [Archfit Review on GitHub](https://github.com/apps/archfit-review), then choose the repositories.
- **Read:** the [user guide](docs/user-guide.md). Start with [Install and set up](docs/user-guide.md#install-and-set-up)
  and [Configure the ruleset](docs/user-guide.md#configure-the-ruleset).
- **Daily use:** [Home and the map](docs/user-guide.md#home-and-the-map), [answer a request](docs/user-guide.md#answer-a-request),
  [edit the policy](docs/user-guide.md#edit-the-policy-yaml), and [read a pull request](docs/user-guide.md#pull-request-and-comment).

During the beta, the App serves an account once an operator approves it. Until then the App shows a waiting page and runs
nothing.

## Developers

- [Architecture](docs/architecture.md) and [tech stack](docs/tech-stack.md).
- [Design documents](docs/README.md).
- Operators: [Cloud operations](docs/design/cloud/operations.md).

Enable the Git hooks once in each clone:

```sh
git config core.hooksPath scripts/git-hooks
```

Run the full local check before you push:

```sh
make all           # format, lint, test, build
make test          # race tests
make lint          # golangci-lint
make build         # .bin/reporter
make run           # start the service on the configured address
make deploy-check  # shellcheck, actionlint, and the release script tests (scripts/deploy)
```

Export the variables in [`.env.example`](.env.example) before `make run`. The App reads environment variables, not a
`.env` file. Each secret variable names a file with a `*_FILE` suffix. See the
[configuration table](docs/architecture.md#configuration).

Check a running local service:

```sh
curl -s localhost:8080/health
```

## Repositories

The three repositories have separate roles:

| Repository                                                       | Role                                               |
| ---------------------------------------------------------------- | -------------------------------------------------- |
| [`archfit`](https://github.com/alexei-led/archfit)               | Engine, CLI, images, and report schemas            |
| [`archfit-action`](https://github.com/alexei-led/archfit-action) | Runs the engine in CI and uploads the report       |
| `archfit-app`                                                    | Approved policy, report intake, gate, and feedback |

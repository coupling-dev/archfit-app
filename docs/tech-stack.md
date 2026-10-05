# Tech stack

This page lists the technology choices for the design tree under [`internal/`](../internal/module.md).
The `module.md` files define behavior. This page defines implementation tools and conventions.

The guiding principle is simple, reliable, and predictable. The standard library comes first.
Rows marked **[default]** need an owner decision before launch.

## Language and build

| Item            | Choice                                                                                                    |
| --------------- | --------------------------------------------------------------------------------------------------------- |
| Language        | Go 1.27. Module path `github.com/alexei-led/archfit-app`                                                  |
| Packages        | One Go package per module folder under `internal/`                                                        |
| Entry point     | `cmd/reporter` calls the logger, `app.Load`, and `app.Run`                                                |
| Build           | `CGO_ENABLED=0` static binary. The existing `Dockerfile` builds a distroless image as a non-root user     |
| Commands        | `make fmt`, `make lint`, `make test`, `make build`, and `make all`                                        |
| Format and lint | `gofmt -s`, `goimports -local github.com/alexei-led/archfit-app`, and `golangci-lint` v2.14 defaults (CI) |
| Logging         | `log/slog` writes JSON to stdout. Logs contain IDs, digests, outcomes, and timings                        |
| HTTP            | `net/http` with Go 1.22+ `ServeMux` patterns                                                              |
| Crypto          | Standard library RS256 JWT, JWKS, HMAC-SHA256, AES-GCM cookies, and SHA-256 digests                       |
| Embedded data   | `go:embed` stores schemas, the locked manifest, the Anthropic price table, templates, and assets          |

## Dependencies

The only third-party module is:

| Module                      | Used in                        | Purpose                                 |
| --------------------------- | ------------------------------ | --------------------------------------- |
| `go.yaml.in/yaml/v3` v3.0.x | `wire/settings`, `wire/policy` | Strict YAML decoding with `KnownFields` |

The App has no GitHub SDK, JWT library, JSON Schema library, assertion library, or LLM SDK.
GitHub, OIDC, and Anthropic clients use `net/http`.

## Testing

The tests use the standard `testing` package and table-driven cases with `t.Run`.

| Area                  | Choice                                                                                                |
| --------------------- | ----------------------------------------------------------------------------------------------------- |
| Assertions            | Plain `if` statements with `t.Errorf` or `t.Fatalf`. Small local helpers handle structs **[default]** |
| Full run              | `go test -race -coverprofile=coverage.out ./...` through `make test`                                  |
| One module            | `go test -race ./internal/<path>/...`                                                                 |
| GitHub, OIDC, and LLM | `net/http/httptest` servers behind real clients. Consumers use hand-written role fakes                |
| Clock                 | Injected `func() time.Time`. Tests do not sleep                                                       |
| Store                 | `store.NewMemory()`. Failure cases use a failing wrapper. Drivers run `store/storetest`               |
| Fixtures              | `internal/wire/testdata/` with `.expect.json` files                                                   |
| Import rules          | Root tests run `go list -deps` against the allowed import matrix                                      |
| Fuzzing               | `testing.F` covers strict `envelope`, `report`, `settings`, and `policy` decoders **[default]**       |

## Frontend

| Item      | Choice                                                                                                                           |
| --------- | -------------------------------------------------------------------------------------------------------------------------------- |
| Rendering | Server-rendered `html/template`. No SPA framework                                                                                |
| Editor    | A plain text area. The server checks syntax and schema on Check and on every save, and lists problems by line. No editor library |
| Graph     | A small vanilla script renders plain SVG from the JSON view model **[default]**                                                  |

Static assets are the module's own files under `internal/control/ui/static/`. Node is not needed by `make`, CI, or the
image. The CSP allows no inline style and no worker.

## Platform seams

The design isolates platform-dependent code behind three interfaces.

| Seam       | Current driver                                             | Later choice                                                                                               |
| ---------- | ---------------------------------------------------------- | ---------------------------------------------------------------------------------------------------------- |
| `Store`    | `memory`. Data is lost when the process exits              | Chosen, not built: Firestore over REST. See [cloud design](design/cloud/architecture.md#state)             |
| `Sandbox`  | `none`. Managed discovery reports that it is not available | Add an ephemeral container-job driver under `internal/control/onboarding/<driver>/`                        |
| `Provider` | `internal/control/assist/anthropic` over `net/http`        | Add another provider as a sibling package                                                                  |
| Host       | Not deployed                                               | Chosen, not built: two Cloud Run services. See [cloud design](design/cloud/architecture.md)                |
| Scheduler  | Any external scheduler calls `POST /internal/sweep`        | Chosen, not built: Cloud Scheduler and Cloud Tasks. See [cloud design](design/cloud/architecture.md#flows) |

The cloud design adds `Store.Update` to the `Store` contract and a `Queue` seam. The sandbox choice is still open.
The [hosting research](archive/research/20260918-hosting.md) is dated research, not a deployment decision.

## Conventions

- Import a module by its full path, such as `.../internal/wire/report`.
- Import only the packages listed under Integrations in the module specification.
- Run `go test -race ./internal/<module path>/` and `golangci-lint run ./internal/<module path>/` for one module.
- Use sentinel errors with `errors.Is` and `errors.As`. Wrap errors with `%w`. Do not panic outside `main`.
- Put `context.Context` first in every I/O function. Give every outbound call a deadline.
- Pass constructor dependencies explicitly. Do not use package state or `init` side effects.
- Keep wire types, domain types, and storage types separate.
- Read secrets from `*_FILE` variables. Never log secret values.
- Keep generated and vendored files in `testdata/`, `static/`, or `templates/`.

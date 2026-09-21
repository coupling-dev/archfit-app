# Tech stack

Normative home of every technology choice for implementing the design tree under
[`internal/`](../internal/module.md). The `module.md` files say what to build;
this file says what with. Implementers read it before any task. A decision made
during implementation is recorded here first.

Principle: simple, reliable, predictable. Standard library first. Rows marked
**[default]** were chosen without an owner answer; strike or change them.

## Language and build

| Item          | Choice                                                                                              |
| ------------- | --------------------------------------------------------------------------------------------------- |
| Language      | Go, version from `go.mod` (1.27). Module path `github.com/alexei-led/archfit-app`.                  |
| Packages      | One package per module folder under `internal/`. Package name = folder name.                        |
| Entry point   | `cmd/reporter`: logger, `app.Load`, `app.Run`. Nothing else.                                        |
| Build         | `CGO_ENABLED=0`, static binary, existing `Dockerfile` (distroless, non-root).                       |
| Commands      | `make fmt`, `make lint`, `make test`, `make build`; `make all` before pushing.                      |
| Format, lint  | `gofmt -s`, `goimports -local github.com/alexei-led/archfit-app`, `golangci-lint` v2 defaults.      |
| Logging       | `log/slog`, JSON to stdout. IDs, digests, outcomes, timings only.                                   |
| HTTP          | `net/http` with the Go 1.22+ `ServeMux` patterns. No router library.                                |
| Crypto        | Standard library only: RS256 JWT and JWKS, HMAC-SHA256, AES-GCM cookies, SHA-256 digests.           |
| Embedded data | `go:embed` for JSON Schemas, the locked manifest, the Anthropic price table, templates, and assets. |

## Dependencies

Allowed third-party modules. Anything else needs a row here first.

| Module                        | Used only in                   | Why                                                                                             |
| ----------------------------- | ------------------------------ | ----------------------------------------------------------------------------------------------- |
| `go.yaml.in/yaml/v3` (v3.0.x) | `wire/settings`, `wire/policy` | Strict YAML decoding (`KnownFields`). Maintained successor of `gopkg.in/yaml.v3`. **[default]** |

No GitHub SDK, no JWT library, no JSON Schema library, no assertion library, no
LLM SDK. The GitHub, OIDC, and Anthropic clients are plain `net/http`.

## Testing

TDD: the tests named in a module's Test Specification are written first.

| Item              | Choice                                                                                                                             |
| ----------------- | ---------------------------------------------------------------------------------------------------------------------------------- |
| Framework         | Standard `testing` package. Table-driven tests with `t.Run`. Test names are the ones in the Test Specification.                    |
| Assertions        | Plain `if got != want` with `t.Errorf`/`t.Fatalf`; `reflect.DeepEqual` or a small local helper for structs. **[default]**          |
| Run               | `go test -race -coverprofile=coverage.out ./...` (`make test`). One module: `go test -race ./internal/<path>/...`.                 |
| GitHub, OIDC, LLM | `net/http/httptest` servers behind the real client; consumers use hand-written fakes of the role interfaces. No mocking framework. |
| Clock             | Injected `func() time.Time`; tests never sleep.                                                                                    |
| Store             | `store.NewMemory()`; failure cases use a small failing wrapper. Drivers run the shared `store/storetest` suite.                    |
| Fixtures          | `internal/wire/testdata/` with `.expect.json` files, as the `wire` module defines. Seeded leakage fixtures are release tests.      |
| Import rules      | Root tests run `go list -deps` to enforce the allowed-import matrix.                                                               |
| Fuzzing           | `testing.F` for the strict decoders (`envelope`, `report`, `settings`). **[default]**                                              |

## Frontend

| Item      | Choice                                                                                                                                                                                                |
| --------- | ----------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------- |
| Rendering | `html/template`, server-rendered. No SPA framework.                                                                                                                                                   |
| Editor    | Monaco with `monaco-yaml`.                                                                                                                                                                            |
| Assets    | One prebuilt, committed bundle under `internal/control/ui/static/`, embedded with `go:embed`. Rebuilding it is a manual `esbuild` step; Node is not needed by `make`, CI, or the image. **[default]** |
| Graph     | Plain SVG rendered by a small vanilla script from the JSON view model. No graph library until module counts prove one necessary. **[default]**                                                        |

## Platform seams

The design isolates every platform-dependent piece behind `Store`, `Sandbox`,
and the LLM `Provider`. None of them blocks implementing the design tree.

| Seam       | Now                                                       | Later                                                                                                |
| ---------- | --------------------------------------------------------- | ---------------------------------------------------------------------------------------------------- |
| `Store`    | `memory` driver (in `internal/store`). Data lost on exit. | One KV driver as `internal/store/<driver>/`, chosen with the host. Must pass `store/storetest`.      |
| `Sandbox`  | `none`: managed discovery reports "not available".        | One ephemeral container-job driver as `internal/control/onboarding/<driver>/`, chosen with the host. |
| `Provider` | `internal/control/assist/anthropic` over `net/http`.      | Another provider is a sibling package.                                                               |
| Host       | Any container host. Not chosen.                           | Within USD 100/month. See [hosting research](research/20260918-hosting.md).                          |
| Scheduler  | Any external scheduler calling `POST /internal/sweep`.    | Chosen with the host.                                                                                |

Choosing the host, KV, and sandbox is an owner decision that needs the
real-world checks in [decisions §12](design/decisions.md). Each choice adds a
driver module with its own `module.md`; it changes no existing module.

## Conventions

- Imports: a module is imported by its full path, `github.com/alexei-led/archfit-app/internal/<module path>`
  (for example `.../internal/wire/report`, `.../internal/github/api`). Only the packages a module's `module.md`
  lists under Integrations may be imported.
- One module's checks: `go test -race ./internal/<module path>/` and `golangci-lint run ./internal/<module path>/`
  (no `...` for a parent, so its children's tests are not re-run).

- Errors: sentinel errors and `errors.Is`/`errors.As`; wrap with `%w`; no panics outside `main`.
- Context first in every I/O function; every outbound call has a deadline.
- Constructors take dependencies explicitly; no package-level state, no `init` side effects.
- Wire types (`internal/wire/*`) are read-only inputs; domain and storage types stay separate.
- Secrets arrive as file paths (`*_FILE` variables); never logged.
- Generated or vendored files live in transparent folders (`testdata/`, `static/`, `templates/`).

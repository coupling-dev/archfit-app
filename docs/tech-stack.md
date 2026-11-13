# Tech stack

This page lists the technology choices for the design tree under [`internal/`](../internal/module.md).
The `module.md` files define behavior. This page defines implementation tools and conventions.

The guiding principle is simple, reliable, and predictable. The standard library comes first.
Rows marked **[default]** need an owner decision before launch.

## Language and build

| Item            | Choice                                                                                                                           |
| --------------- | -------------------------------------------------------------------------------------------------------------------------------- |
| Language        | Go 1.27. Module path `github.com/alexei-led/archfit-app`                                                                         |
| Packages        | One Go package per module folder under `internal/`                                                                               |
| Entry point     | `cmd/reporter` calls the logger, `app.Load`, and `app.Run`                                                                       |
| Build           | `CGO_ENABLED=0` static binary. The existing `Dockerfile` builds a distroless image as a non-root user                            |
| Commands        | `make fmt`, `make lint`, `make test`, `make build`, and `make all`                                                               |
| Format and lint | `gofmt -s`, `goimports -local github.com/alexei-led/archfit-app`, and `golangci-lint` v2.14 defaults (CI)                        |
| Infrastructure  | OpenTofu 1.8+ (Terraform-compatible), `google` provider pinned, stacks `data` and `runtime` under [`infra/`](../infra/README.md) |
| Logging         | `log/slog` writes JSON to stdout. Logs contain IDs, digests, outcomes, and timings                                               |
| HTTP            | `net/http` with Go 1.22+ `ServeMux` patterns                                                                                     |
| Crypto          | Standard library RS256 JWT, JWKS, HMAC-SHA256, AES-GCM cookies, and SHA-256 digests. Passkeys: go-webauthn (Dependencies)        |
| Embedded data   | `go:embed` stores schemas, the locked manifest, templates, and assets                                                            |

## Dependencies

The App imports two third-party modules:

| Module                                    | Used in                                                         | Purpose                                                                                         |
| ----------------------------------------- | --------------------------------------------------------------- | ----------------------------------------------------------------------------------------------- |
| `go.yaml.in/yaml/v3` v3.0.x               | `wire/settings`, `wire/policy`, `wire/policy3`, `wire/workflow` | Strict policy/settings decoding; policy edits by node position; job/step Action call extraction |
| `github.com/go-webauthn/webauthn` v0.18.2 | `control/passkey` only                                          | WebAuthn registration and assertion for the passkey of an answer (M2 D9)                        |

go-webauthn, because a hand-written WebAuthn, CBOR and COSE verifier would be security code the App owns forever
(M2 D9). v0.18.2 is its latest release (2026-09-19, BSD-3-Clause); `govulncheck` finds nothing the App reaches through
it. Its own imports enter the build only through `control/passkey`: CBOR (`fxamacker/cbor`, `x448/float16`), TPM
(`google/go-tpm`), MessagePack (`tinylib/msgp`, `philhofer/fwd`), `go-viper/mapstructure`, `google/uuid`,
`go-webauthn/x`, `golang.org/x/crypto`, `golang.org/x/sys`, and `golang-jwt/jwt` for its metadata service, which the
App does not configure. `internal/imports_test.go` fails when any of them is imported elsewhere.

Apart from go-webauthn's own imports, the App has no GitHub SDK, JWT library, JSON Schema library, assertion library,
or LLM SDK. GitHub, OIDC, Firestore, and metadata-server clients use `net/http`.

## Testing

The tests use the standard `testing` package and table-driven cases with `t.Run`.

| Area            | Choice                                                                                                                        |
| --------------- | ----------------------------------------------------------------------------------------------------------------------------- |
| Assertions      | Plain `if` statements with `t.Errorf` or `t.Fatalf`. Small local helpers handle structs **[default]**                         |
| Full run        | `go test -race -coverprofile=coverage.out ./...` through `make test`                                                          |
| One module      | `go test -race ./internal/<path>/...`                                                                                         |
| GitHub and OIDC | `net/http/httptest` servers behind real clients. Consumers use hand-written role fakes                                        |
| Clock           | Injected `func() time.Time`. Tests do not sleep                                                                               |
| Store           | `store.NewMemory()`. Failure cases use a failing wrapper. Drivers run `store/storetest`                                       |
| Firestore       | An `httptest` fake of the REST subset. CI also runs the driver tests against the Firestore emulator                           |
| Fixtures        | `internal/wire/testdata/` with `.expect.json` files                                                                           |
| Import rules    | Root tests run `go list -deps` against the allowed import matrix                                                              |
| Passkey         | A software authenticator on go-webauthn's CBOR and COSE encoders, and the W3C WebAuthn level 3 test vectors; never a real key |
| Fuzzing         | `testing.F` covers strict `envelope`, `report`, `settings`, and `policy` decoders **[default]**                               |

## Frontend

| Item      | Choice                                                                                                                            |
| --------- | --------------------------------------------------------------------------------------------------------------------------------- |
| Rendering | Svelte 5 and TypeScript, built by Vite into `internal/control/ui/spa` and embedded in the binary; an error page is the same shell |
| Styles    | One stylesheet, the tokens and components of `docs/archive/design/prototypes/archfit.css`, light and dark                         |
| Graph     | Svelte Flow (`@xyflow/svelte`, MIT) with the ELK layered layout (`elkjs`, EPL-2.0), lazy on the graph pages; ELK runs in a worker |
| Licences  | The build writes the licence of every bundled npm package to `assets/licenses-<hash>.txt`, linked as `/licenses.txt`              |
| Tests     | `svelte-check`, ESLint, Vitest, a bundle budget, and a Playwright smoke test with axe under the production CSP                    |

Node is needed only to build and check the web app (`make web`, `make web-check`, the CI job "Web app" and the image's
`web` stage); Go builds and tests run without it. Versions are pinned in `web/package-lock.json`. The smoke test
serves the build on `E2E_PORT`, or on a free port when it is unset, so checks in two worktrees can run at once. The CSP allows no
inline style, no inline script, no eval, no worker but a same-origin file (`worker-src 'self'`, the graph's layout) and
no other origin. The one exception is the shell's per-response nonce in `style-src`, for CodeMirror's style element. See
[UI redesign](archive/design/ui-redesign.md#frontend-stack-for-phases-b-to-f-plan-2-for-the-owner) for the migration.

## Platform seams

The design isolates platform-dependent code behind two interfaces.

| Seam      | Current driver                                             | Later choice                                                                                                         |
| --------- | ---------------------------------------------------------- | -------------------------------------------------------------------------------------------------------------------- |
| `Store`   | `memory` (default), or `firestore`: Firestore over REST    | None. `CompareAndSwapAll` covers the multi-key transaction of the [cloud design](design/cloud/architecture.md#state) |
| `Sandbox` | `none`. Managed discovery reports that it is not available | Add an ephemeral container-job driver under `internal/control/onboarding/<driver>/`                                  |
| Host      | Cloud Run, two services in `europe-west1`                  | Deployed. See [cloud design](design/cloud/architecture.md)                                                           |
| Scheduler | Any external scheduler calls `POST /internal/sweep`        | Chosen, not built: Cloud Scheduler and Cloud Tasks. See [cloud design](design/cloud/architecture.md#flows)           |

The cloud design adds a `Queue` seam. The sandbox choice is still open.
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
- Keep generated and vendored files in `testdata/`; the web app's build goes to `internal/control/ui/spa/`, not committed.

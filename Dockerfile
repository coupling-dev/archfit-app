# The reporter holds no analyzer toolchain by design: it verifies a report's
# provenance and posts a check. Analysis happens in the customer's runner.

# Node only builds the web app; the runtime image holds the one Go binary that embeds the build.
FROM node:24.15-alpine AS web
WORKDIR /src/web
COPY web/package.json web/package-lock.json ./
RUN npm ci
COPY web/ ./
COPY internal/control/ui/spa/.gitignore ../internal/control/ui/spa/
RUN npm run build

FROM golang:1.27-alpine AS build
WORKDIR /src
COPY go.mod go.sum ./
RUN go mod download
COPY . .
COPY --from=web /src/internal/control/ui/spa ./internal/control/ui/spa
ARG APP_SOURCE_REVISION
ARG APP_BUILT_AT
RUN CGO_ENABLED=0 go build -ldflags "-s -w -X github.com/alexei-led/archfit-app/internal/control/ui.sourceRevision=${APP_SOURCE_REVISION} -X github.com/alexei-led/archfit-app/internal/control/ui.builtAt=${APP_BUILT_AT}" -o /out/reporter ./cmd/reporter

FROM gcr.io/distroless/static-debian12:nonroot
ARG APP_SOURCE_REVISION
ARG APP_BUILT_AT
LABEL org.opencontainers.image.revision=${APP_SOURCE_REVISION} \
      org.opencontainers.image.created=${APP_BUILT_AT}
COPY --from=build /out/reporter /usr/local/bin/reporter
USER nonroot:nonroot
EXPOSE 8080
ENTRYPOINT ["/usr/local/bin/reporter"]

# The reporter holds no analyzer toolchain by design: it verifies a report's
# provenance and posts a check. Analysis happens in the customer's runner.
FROM golang:1.27-alpine AS build
WORKDIR /src
COPY go.mod go.sum ./
RUN go mod download
COPY . .
RUN CGO_ENABLED=0 go build -ldflags "-s -w" -o /out/reporter ./cmd/reporter

FROM gcr.io/distroless/static-debian12:nonroot
COPY --from=build /out/reporter /usr/local/bin/reporter
USER nonroot:nonroot
EXPOSE 8080
ENTRYPOINT ["/usr/local/bin/reporter"]

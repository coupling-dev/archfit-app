BINARY := reporter
BIN_DIR := .bin
MODULE := github.com/alexei-led/archfit-app
ACTIONLINT ?= actionlint
GO_FILES := $(shell find . -type f -name '*.go' -not -path './vendor/*' -not -path './web/*')

.DEFAULT_GOAL := help

.PHONY: help
help: ## show available commands
	@grep -hE '^[a-z-]+:.*?## ' $(MAKEFILE_LIST) | awk 'BEGIN{FS=":.*?## "}{printf "  \033[36m%-12s\033[0m %s\n", $$1, $$2}'

.PHONY: build
build: ## build a static binary in .bin/
	CGO_ENABLED=0 go build -o $(BIN_DIR)/$(BINARY) ./cmd/reporter

.PHONY: test
test: ## run race tests with coverage
	go test -race -coverprofile=coverage.out ./...

.PHONY: lint
lint: ## run golangci-lint
	golangci-lint run ./...

.PHONY: fmt
fmt: ## format Go files
	gofmt -s -w $(GO_FILES)
	goimports -w -local $(MODULE) $(GO_FILES)

.PHONY: fmt-check
fmt-check: ## verify Go formatting
	@test -z "$$(gofmt -l $(GO_FILES))" || { echo 'gofmt: files need formatting' >&2; gofmt -l $(GO_FILES); exit 1; }
	@test -z "$$(goimports -d -local $(MODULE) $(GO_FILES))" || { echo 'goimports: files need formatting' >&2; goimports -d -local $(MODULE) $(GO_FILES); exit 1; }

.PHONY: deploy-check
deploy-check: ## lint the workflows and test the release scripts (needs shellcheck, actionlint, bats, jq)
	shellcheck scripts/deploy/*.sh scripts/deploy/test/bin/* scripts/deploy/test/*.bats
	$(ACTIONLINT)
	bats scripts/deploy/test

.PHONY: run
run: ## run the reporter locally
	go run ./cmd/reporter

# The web app (web/) builds into internal/control/ui/spa, which the binary embeds. Without a build the App serves a
# notice page, so Go builds and tests need no Node.
.PHONY: web
web: ## build the web app into internal/control/ui/spa (needs Node)
	cd web && npm ci && npm run build

.PHONY: web-check
web-check: ## type-check, test, build, size-check and smoke-test the web app (needs Node and Playwright's Chromium)
	cd web && npm ci && npm run ci

.PHONY: web-if-node
web-if-node:
	@if command -v node >/dev/null 2>&1; then $(MAKE) web-check; else echo 'node not found: web checks skipped' >&2; fi

.PHONY: all
all: fmt lint web-if-node test build ## format, lint, check the web app (when Node is installed), test, and build

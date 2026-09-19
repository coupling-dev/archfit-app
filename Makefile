BINARY := reporter
BIN_DIR := .bin
MODULE := github.com/alexei-led/archfit-app
GO_FILES := $(shell find . -type f -name '*.go' -not -path './vendor/*')

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

.PHONY: run
run: ## run the reporter locally
	go run ./cmd/reporter

.PHONY: all
all: fmt lint test build ## format, lint, test, and build

BINARY   := reporter
BIN_DIR  := .bin
MODULE   := github.com/alexei-led/archfit-app

.DEFAULT_GOAL := help

.PHONY: help
help: ## show this help
	@grep -hE '^[a-z-]+:.*?## ' $(MAKEFILE_LIST) | awk 'BEGIN{FS=":.*?## "}{printf "  \033[36m%-12s\033[0m %s\n", $$1, $$2}'

.PHONY: build
build: ## static binary → .bin/reporter
	CGO_ENABLED=0 go build -o $(BIN_DIR)/$(BINARY) ./cmd/reporter

.PHONY: test
test: ## race tests with coverage
	go test -race -coverprofile=coverage.out ./...

.PHONY: lint
lint: ## golangci-lint
	golangci-lint run ./...

.PHONY: fmt
fmt: ## gofmt + goimports
	gofmt -s -w .
	goimports -w -local $(MODULE) .

.PHONY: run
run: ## run the reporter locally
	go run ./cmd/reporter

.PHONY: all
all: fmt lint test build ## everything

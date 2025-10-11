GO := go
CGO_ENABLED = 0
COMMIT_HASH := $(shell git --no-pager describe --tags --always --dirty)

build:
	CGO_ENABLED=$(CGO_ENABLED) go build -ldflags=$(LDFLAGS) -o bin/ ./cmd/...

fmt:
	go fmt ./...

.PHONY: lint lint-fix lint-fmt
lint:
	@echo "--> Running linter"
	@golangci-lint run

lint-fix:
	@echo "--> Running linter auto fix"
	@golangci-lint run --fix

lint-fmt:
	@echo "--> Running linter format"
	@golangci-lint fmt

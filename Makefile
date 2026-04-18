LEFTHOOK_MODULE  ?= github.com/evilmartians/lefthook/v2
LEFTHOOK_VERSION ?= v2.1.5

.PHONY: help build test test-all test-integration bench fmt lint clean coverage govulncheck hooks examples

help:
	@echo "Codex Agent SDK for Go - Development Tasks"
	@echo ""
	@echo "Available targets:"
	@echo "  make build           - Build the SDK"
	@echo "  make test            - Run unit tests (default, fast)"
	@echo "  make test-all        - Run ALL tests including integration (requires codex CLI + OPENAI_API_KEY)"
	@echo "  make test-integration - Run integration tests only"
	@echo "  make bench           - Run benchmarks"
	@echo "  make fmt             - Format code with gofmt"
	@echo "  make lint            - Run go vet and golangci-lint"
	@echo "  make coverage        - Run tests with coverage report"
	@echo "  make clean           - Clean build artifacts"
	@echo "  make examples        - Build all examples"
	@echo "  make govulncheck     - Scan for known vulnerabilities"
	@echo "  make hooks           - Install lefthook pre-commit hooks"
	@echo ""

build:
	@echo "Building SDK..."
	go build ./...
	@echo "Build complete"

test:
	@echo "Running unit tests (short mode)..."
	@echo "Note: Integration tests skipped. Use 'make test-all' to run all tests."
	go test -race -short -count=1 -p 4 ./...

test-all:
	@echo "Running ALL tests (including integration tests)..."
	@echo "WARNING: This will spawn codex CLI processes if OPENAI_API_KEY is set"
	go test -race -count=1 -p 4 -tags=integration ./...

test-integration:
	@echo "Running integration tests..."
	go test -v -tags=integration ./tests/...

bench:
	@echo "Running benchmarks..."
	go test -bench=. -benchmem ./tests/...

fmt:
	@echo "Formatting code..."
	go fmt ./...
	@echo "Format complete"

lint:
	@echo "Running linters..."
	go vet ./...
	@if command -v golangci-lint > /dev/null; then golangci-lint run ./...; else echo "golangci-lint not installed, skipping"; fi

coverage:
	@echo "Running tests with coverage (short mode)..."
	go test -race -short -count=1 -p 4 -coverprofile=coverage.out ./...
	go tool cover -func=coverage.out
	@echo "HTML report: go tool cover -html=coverage.out -o coverage.html"

govulncheck:
	@echo "Scanning for known vulnerabilities..."
	govulncheck ./...

clean:
	@echo "Cleaning build artifacts..."
	go clean -testcache
	rm -f coverage.out coverage.html
	@echo "Clean complete"

examples:
	@echo "Building examples..."
	@for dir in examples/*/; do \
		if [ -f "$$dir/main.go" ]; then \
			echo "Building $$dir..."; \
			(cd "$$dir" && go build -o app main.go && rm -f app); \
		fi; \
	done
	@echo "Examples built"

hooks:
	@mkdir -p .bin
	@GOBIN=$(CURDIR)/.bin go install $(LEFTHOOK_MODULE)@$(LEFTHOOK_VERSION)
	@PATH="$(CURDIR)/.bin:$$PATH" $(CURDIR)/.bin/lefthook install

.DEFAULT_GOAL := help

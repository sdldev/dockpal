# Makefile for Dockpal
# Perancang: Senior Software Architect

# Get version from git or default to 0.9.0-dev
VERSION ?= $(shell git describe --tags --always --dirty 2>/dev/null || echo "0.9.0-dev")
LDFLAGS = -s -w -X main.version=$(VERSION)

.PHONY: all build build-linux-amd64 dev test lint clean help

all: build

## build: Build binary for local OS/Arch
build:
	@echo "Building Dockpal version $(VERSION)..."
	go build -ldflags "$(LDFLAGS)" -o dockpal .

## build-linux-amd64: Build cross-compiled binary for Linux AMD64
build-linux-amd64:
	@echo "Building Dockpal for Linux AMD64 version $(VERSION)..."
	GOOS=linux GOARCH=amd64 go build -ldflags "$(LDFLAGS)" -o dockpal-linux-amd64 .

## dev: Build and run locally for development
dev: build
	@echo "Starting Dockpal dev server on port 3012..."
	DOCKPAL_DATA_DIR=$(CURDIR)/.data ./dockpal server

## dev-watch: Build and run with hot reload on .go file changes (requires reflex)
dev-watch:
	@echo "Starting Dockpal dev server with hot reload (reflex)..."
	@reflex -r '\.go$$' -R '_test\.go$$' -s -- sh -c 'go build -o .dockpal-dev . && DOCKPAL_DATA_DIR=$(CURDIR)/.data ./.dockpal-dev server'

## test: Run unit tests
test:
	@echo "Running tests..."
	go test -v ./...

## lint: Run static code analysis
lint:
	@echo "Running go vet..."
	go vet ./...

## install-hooks: Setup local Git pre-commit hooks for testing
install-hooks:
	@echo "Installing pre-commit hook..."
	@mkdir -p .git/hooks
	@echo '#!/bin/sh' > .git/hooks/pre-commit
	@echo 'echo "🔍 Running pre-commit verification..."' >> .git/hooks/pre-commit
	@echo 'go vet ./...' >> .git/hooks/pre-commit
	@echo 'if [ $$? -ne 0 ]; then' >> .git/hooks/pre-commit
	@echo '    echo "❌ [Pre-Commit] Go vet failed. Commit aborted."' >> .git/hooks/pre-commit
	@echo '    exit 1' >> .git/hooks/pre-commit
	@echo 'fi' >> .git/hooks/pre-commit
	@echo 'go test ./...' >> .git/hooks/pre-commit
	@echo 'if [ $$? -ne 0 ]; then' >> .git/hooks/pre-commit
	@echo '    echo "❌ [Pre-Commit] Go tests failed. Commit aborted."' >> .git/hooks/pre-commit
	@echo '    exit 1' >> .git/hooks/pre-commit
	@echo 'fi' >> .git/hooks/pre-commit
	@echo 'echo "✅ [Pre-Commit] All verification passed. Committing..."' >> .git/hooks/pre-commit
	@chmod +x .git/hooks/pre-commit
	@echo "Pre-commit hook installed successfully."

## clean: Clean build artifacts and temporary files
clean:
	@echo "Cleaning up..."
	rm -f dockpal dockpal-linux-amd64 coverage.out

## help: Show help documentation
help:
	@echo "Usage: make [target]"
	@echo ""
	@echo "Targets:"
	@grep -E '^## [a-zA-Z_-]+:.*?## .*$$' $(MAKEFILE_LIST) | sort | awk 'BEGIN {FS = ":.*?## "}; {printf "  \033[36m%-20s\033[0m %s\n", $$1, $$2}'
	@grep -E '^## [a-zA-Z_-]+:.*$$' $(MAKEFILE_LIST) | sed -e 's/## //' | awk 'BEGIN {FS = ":"}; {if (NF>1) printf "  \033[36m%-20s\033[0m %s\n", $$1, $$2; else printf "  \033[36m%-20s\033[0m\n", $$1}'

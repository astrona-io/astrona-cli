set shell := ["bash", "-uc"]

bin := "astrona"

# List available recipes
default:
    @just --list

# Build ./astrona for local testing (state: ~/.astrona-dev; nothing installed)
build:
    go build -ldflags "-X main.Version=developer" -o {{bin}} ./cmd/astrona

# Remove the built binary
clean:
    rm -f {{bin}}

# Fail if any file needs gofmt
fmt:
    @test -z "$(gofmt -l .)" || (echo "Needs gofmt:"; gofmt -l .; exit 1)

# Auto-fix formatting
fmt-fix:
    gofmt -w .

vet:
    go vet ./...

# Run golangci-lint (install: go install github.com/golangci/golangci-lint/cmd/golangci-lint@latest)
lint:
    @command -v golangci-lint >/dev/null || (echo "golangci-lint not found; install: go install github.com/golangci/golangci-lint/cmd/golangci-lint@latest"; exit 1)
    golangci-lint run ./...

# Auto-fix lint issues where possible
lint-fix:
    golangci-lint run --fix ./...

test:
    go test ./...

# Format check, vet, lint, and test — run before committing
check: fmt vet lint test

# Generate reference docs with release paths; docgen does not write user state
docs-gen-cli:
    go run -ldflags "-X main.Version=0.0.0" ./cmd/astrona docgen --output docs/reference/cli

# Serve the docs site locally with live reload (uses uv — no persistent venv to manage)
docs: docs-gen-cli
    uv run --with-requirements docs/requirements.txt mkdocs serve

# Build the docs site statically (used by CI) — fails on broken nav/links
build-docs: docs-gen-cli
    uv run --with-requirements docs/requirements.txt mkdocs build --strict

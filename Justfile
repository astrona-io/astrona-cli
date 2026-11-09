set shell := ["bash", "-uc"]

bin := "astrona"

# List available recipes
default:
    @just --list

# Build ./astrona for local testing (state: ~/.astrona-dev; nothing installed)
build: web
    go build -ldflags "-X main.Version=developer" -o {{bin}} ./cmd/astrona
    @# Embedded now; put the committed placeholder page back so git stays clean.
    @git checkout -- internal/workspace/web/dist/index.html 2>/dev/null || true
    @echo "Built ./{{bin}}. Run: ./{{bin}} --help (or just run --help)" >&2

# Build the browser workspace frontend into internal/workspace/web/dist (needs Node, see .nvmrc)
web:
    cd internal/workspace/web && npm ci --no-audit --no-fund && npm run build

# Serve the workspace frontend with a mock server on http://127.0.0.1:5173/#code=dev&lab=demo
web-dev:
    cd internal/workspace/web && npm run dev

# Build and run the local development binary, forwarding CLI arguments
[positional-arguments]
run *args: build
    @./{{bin}} "$@"

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

# Run the Robot Framework acceptance suite against ./astrona (default: smoke tier; needs kind + an engine)
[positional-arguments]
oat *args: build
    uv run --with-requirements acceptance/requirements.txt robot --include smoke --outputdir oat-report --xunit xunit.xml "$@" acceptance/suites

# Generate reference docs with release paths; docgen does not write user state
docs-gen-cli:
    go run -ldflags "-X main.Version=0.0.0" ./cmd/astrona docgen --output docs/reference/cli

# Serve the docs site locally with live reload (uses uv — no persistent venv to manage)
docs: docs-gen-cli
    uv run --with-requirements docs/requirements.txt mkdocs serve

# Build the docs site statically (used by CI) — fails on broken nav/links
build-docs: docs-gen-cli
    uv run --with-requirements docs/requirements.txt mkdocs build --strict

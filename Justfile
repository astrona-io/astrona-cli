set shell := ["bash", "-uc"]

bin := "astrona"
install_dir := env_var_or_default("ASTRONA_INSTALL_DIR", env_var("HOME") / ".local/bin")

# List available recipes
default:
    @just --list

# Build the astrona binary
build:
    go build -o {{bin}} ./cmd/astrona

# Build and move the binary onto PATH (default: ~/.local/bin, override with ASTRONA_INSTALL_DIR)
install: build
    mkdir -p {{install_dir}}
    mv {{bin}} {{install_dir}}/{{bin}}
    @echo "Installed to {{install_dir}}/{{bin}}"

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

test:
    go test ./...

# Format check, vet, and test — run before committing
check: fmt vet test

# Generate the CLI reference markdown (docs/reference/cli/) from the actual command tree
docs-gen-cli:
    go run ./cmd/astrona docgen --output docs/reference/cli

# Serve the docs site locally with live reload (uses uv — no persistent venv to manage)
docs: docs-gen-cli
    uv run --with-requirements docs/requirements.txt mkdocs serve

# Build the docs site statically (used by CI) — fails on broken nav/links
build-docs: docs-gen-cli
    uv run --with-requirements docs/requirements.txt mkdocs build --strict

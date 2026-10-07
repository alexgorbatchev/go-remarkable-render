set dotenv-load := false

default:
    @just --list

# Run unit tests with the race detector
test:
    go test -race -v ./...

# Format Go source code
fmt:
    go fmt ./...

# Check module hygiene, then run vet and golangci-lint
lint:
    go mod tidy -diff
    go vet ./...
    golangci-lint run ./...

# Alias for lint
vet: lint

# Run lint and the test suite
check: lint test

# Regenerate E2E golden reference render with Python via PEP 723
generate-golden:
    uv run testdata/generate_golden.py

# Clean build artifacts and coverage files
clean:
    rm -rf bin coverage.out coverage.html .tmp *.test

set dotenv-load := false

default:
    @just --list

# Run unit tests
test:
    go test -v ./...

# Format Go source code
fmt:
    go fmt ./...

# Run static analysis and vet
lint:
    go vet ./...

# Alias for lint
vet: lint

# Run checks
check: test

# Clean build artifacts and coverage files
clean:
    rm -rf bin coverage.out coverage.html .tmp *.test

.PHONY: help build test clean lint docker docker-build docker-up docker-down install-tools fmt vet security release

# Variables
BINARY_DIR=bin
PROXY_BINARY=$(BINARY_DIR)/proxy
API_BINARY=$(BINARY_DIR)/api
DOCKER_COMPOSE=deployments/docker-compose.yml

# Default target
help:
	@echo "Available targets:"
	@echo "  build         - Build binaries for proxy and API"
	@echo "  test          - Run all tests"
	@echo "  test-verbose  - Run tests with verbose output"
	@echo "  test-coverage - Run tests with coverage report"
	@echo "  clean         - Clean build artifacts"
	@echo "  lint          - Run golangci-lint"
	@echo "  fmt           - Format code with gofmt"
	@echo "  vet           - Run go vet"
	@echo "  security      - Run security checks"
	@echo "  install-tools - Install development tools"
	@echo "  docker-build  - Build Docker images"
	@echo "  docker-up     - Start services with Docker Compose"
	@echo "  docker-down   - Stop services with Docker Compose"
	@echo "  release       - Build release binaries for all platforms"

# Build targets
build: $(PROXY_BINARY) $(API_BINARY)

$(PROXY_BINARY):
	@mkdir -p $(BINARY_DIR)
	@echo "Building proxy binary..."
	@go build -ldflags="-w -s" -o $(PROXY_BINARY) ./cmd/proxy/main.go

$(API_BINARY):
	@mkdir -p $(BINARY_DIR)
	@echo "Building API binary..."
	@go build -ldflags="-w -s" -o $(API_BINARY) ./cmd/api/main.go

# Test targets
test:
	@echo "Running tests..."
	@go test ./...

test-verbose:
	@echo "Running tests with verbose output..."
	@go test -v ./...

test-coverage:
	@echo "Running tests with coverage..."
	@go test -v -race -coverprofile=coverage.out ./...
	@go tool cover -html=coverage.out -o coverage.html
	@echo "Coverage report generated: coverage.html"

# Code quality targets
lint:
	@echo "Running golangci-lint..."
	@golangci-lint run

fmt:
	@echo "Formatting code..."
	@gofmt -s -w .
	@goimports -w .

vet:
	@echo "Running go vet..."
	@go vet ./...

security:
	@echo "Running security checks..."
	@gosec ./...
	@govulncheck ./...

# Development tools
install-tools:
	@echo "Installing development tools..."
	@go install github.com/golangci/golangci-lint/cmd/golangci-lint@latest
	@go install golang.org/x/tools/cmd/goimports@latest
	@go install github.com/securecodewarrior/gosec/v2/cmd/gosec@latest
	@go install golang.org/x/vuln/cmd/govulncheck@latest

# Docker targets
docker-build:
	@echo "Building Docker images..."
	@docker build -t socks5-proxy-analytics-api -f build/docker/Dockerfile.api .
	@docker build -t socks5-proxy-analytics-proxy -f build/docker/Dockerfile.proxy .

docker-up:
	@echo "Starting services with Docker Compose..."
	@docker-compose -f $(DOCKER_COMPOSE) up -d

docker-down:
	@echo "Stopping services with Docker Compose..."
	@docker-compose -f $(DOCKER_COMPOSE) down

docker-logs:
	@echo "Showing Docker Compose logs..."
	@docker-compose -f $(DOCKER_COMPOSE) logs -f

# Release target
release:
	@echo "Building release binaries..."
	@mkdir -p dist
	@echo "Building for linux/amd64..."
	@GOOS=linux GOARCH=amd64 go build -ldflags="-w -s" -o dist/socks5-proxy-linux-amd64 ./cmd/proxy/main.go
	@GOOS=linux GOARCH=amd64 go build -ldflags="-w -s" -o dist/socks5-api-linux-amd64 ./cmd/api/main.go
	@echo "Building for linux/arm64..."
	@GOOS=linux GOARCH=arm64 go build -ldflags="-w -s" -o dist/socks5-proxy-linux-arm64 ./cmd/proxy/main.go
	@GOOS=linux GOARCH=arm64 go build -ldflags="-w -s" -o dist/socks5-api-linux-arm64 ./cmd/api/main.go
	@echo "Building for darwin/amd64..."
	@GOOS=darwin GOARCH=amd64 go build -ldflags="-w -s" -o dist/socks5-proxy-darwin-amd64 ./cmd/proxy/main.go
	@GOOS=darwin GOARCH=amd64 go build -ldflags="-w -s" -o dist/socks5-api-darwin-amd64 ./cmd/api/main.go
	@echo "Building for darwin/arm64..."
	@GOOS=darwin GOARCH=arm64 go build -ldflags="-w -s" -o dist/socks5-proxy-darwin-arm64 ./cmd/proxy/main.go
	@GOOS=darwin GOARCH=arm64 go build -ldflags="-w -s" -o dist/socks5-api-darwin-arm64 ./cmd/api/main.go
	@echo "Building for windows/amd64..."
	@GOOS=windows GOARCH=amd64 go build -ldflags="-w -s" -o dist/socks5-proxy-windows-amd64.exe ./cmd/proxy/main.go
	@GOOS=windows GOARCH=amd64 go build -ldflags="-w -s" -o dist/socks5-api-windows-amd64.exe ./cmd/api/main.go
	@echo "Generating checksums..."
	@cd dist && sha256sum * > checksums.txt
	@echo "Release build complete. Files in dist/ directory."

# Clean target
clean:
	@echo "Cleaning build artifacts..."
	@rm -rf $(BINARY_DIR)
	@rm -rf dist
	@rm -f coverage.out coverage.html
	@docker-compose -f $(DOCKER_COMPOSE) down -v --remove-orphans 2>/dev/null || true

# Dependencies
deps:
	@echo "Downloading dependencies..."
	@go mod download
	@go mod verify

# Pre-commit hook simulation
pre-commit: fmt vet lint test

# CI simulation (run what CI runs)
ci: deps pre-commit build

# Development workflow
dev: clean deps build test
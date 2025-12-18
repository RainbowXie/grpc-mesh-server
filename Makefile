# grpc-mesh-server Makefile

# Version information
VERSION ?= $(shell git describe --tags --always --dirty 2>/dev/null || echo "dev")
BUILD_TIME := $(shell date -u '+%Y-%m-%d_%H:%M:%S')
GIT_COMMIT := $(shell git rev-parse --short HEAD 2>/dev/null || echo "unknown")

# Build flags
LDFLAGS := -ldflags "-X main.Version=$(VERSION) -X main.BuildTime=$(BUILD_TIME) -X main.GitCommit=$(GIT_COMMIT)"

# Directories
BIN_DIR := bin
CMD_DIR := cmd/server
CONFIG_DIR := config

# Binary name
BINARY := grpc-mesh-server

.PHONY: all
all: build

.PHONY: build
build: ## Build the server binary
	@echo "Building $(BINARY) $(VERSION)..."
	@mkdir -p $(BIN_DIR)
	go build $(LDFLAGS) -o $(BIN_DIR)/$(BINARY) ./$(CMD_DIR)
	@echo "✓ Build complete: $(BIN_DIR)/$(BINARY)"

.PHONY: build-release
build-release: ## Build optimized release binary
	@echo "Building release $(BINARY) $(VERSION)..."
	@mkdir -p $(BIN_DIR)
	CGO_ENABLED=0 go build $(LDFLAGS) -trimpath -o $(BIN_DIR)/$(BINARY) ./$(CMD_DIR)
	@echo "✓ Release build complete: $(BIN_DIR)/$(BINARY)"

.PHONY: install
install: ## Install the binary to $GOPATH/bin
	@echo "Installing $(BINARY)..."
	go install $(LDFLAGS) ./$(CMD_DIR)
	@echo "✓ Installed to $(GOPATH)/bin/$(BINARY)"

.PHONY: run
run: build ## Build and run the server
	@echo "Starting $(BINARY)..."
	./$(BIN_DIR)/$(BINARY) -config $(CONFIG_DIR)/config.yaml

.PHONY: clean
clean: ## Remove build artifacts
	@echo "Cleaning..."
	rm -rf $(BIN_DIR)
	go clean
	@echo "✓ Clean complete"

.PHONY: test
test: ## Run tests
	@echo "Running tests..."
	go test -v -race ./...

.PHONY: test-coverage
test-coverage: ## Run tests with coverage
	@echo "Running tests with coverage..."
	go test -v -race -coverprofile=coverage.out ./...
	go tool cover -html=coverage.out -o coverage.html
	@echo "✓ Coverage report: coverage.html"

.PHONY: fmt
fmt: ## Format code
	@echo "Formatting code..."
	go fmt ./...
	@echo "✓ Format complete"

.PHONY: lint
lint: ## Run linter
	@echo "Running linter..."
	@which golangci-lint > /dev/null || (echo "golangci-lint not installed. Run: go install github.com/golangci/golangci-lint/cmd/golangci-lint@latest" && exit 1)
	golangci-lint run ./...
	@echo "✓ Lint complete"

.PHONY: vet
vet: ## Run go vet
	@echo "Running go vet..."
	go vet ./...
	@echo "✓ Vet complete"

.PHONY: check
check: fmt vet lint test ## Run all checks (fmt, vet, lint, test)

.PHONY: deps
deps: ## Download dependencies
	@echo "Downloading dependencies..."
	go mod download
	go mod tidy
	@echo "✓ Dependencies ready"

.PHONY: token
token: ## Generate authentication token
	@echo "Generating authentication token..."
	bash scripts/gen-token.sh
	@echo "✓ Token generated"

.PHONY: tokens
tokens: ## Generate multiple tokens (usage: make tokens COUNT=5)
	@echo "Generating authentication tokens..."
	@test -n "$(COUNT)" && bash scripts/gen-token.sh -c $(COUNT) || bash scripts/gen-token.sh -c 3
	@echo "✓ Tokens generated"

.PHONY: certs
certs: ## Generate development certificates
	@echo "Generating development certificates..."
	bash scripts/gen-dev-certs.sh
	@echo "✓ Certificates generated"

.PHONY: certs-lan
certs-lan: ## Generate certificates with LAN IP (usage: make certs-lan IP=192.168.1.100)
	@echo "Generating certificates with LAN IP..."
	@test -n "$(IP)" || (echo "Error: IP not set. Usage: make certs-lan IP=192.168.1.100" && exit 1)
	SAN_IPS="$(IP)" bash scripts/gen-dev-certs.sh
	@echo "✓ Certificates generated with IP: $(IP)"

.PHONY: certs-prod
certs-prod: ## Generate production certificates (usage: make certs-prod DOMAIN=example.com IPS=203.0.113.10)
	@echo "Generating production certificates..."
	@test -n "$(DOMAIN)" || (echo "Error: DOMAIN not set. Usage: make certs-prod DOMAIN=example.com IPS=203.0.113.10" && exit 1)
	@test -n "$(IPS)" || (echo "Error: IPS not set. Usage: make certs-prod DOMAIN=example.com IPS=203.0.113.10" && exit 1)
	DAYS=730 CN=$(DOMAIN) SAN_DNS="$(DOMAIN)" SAN_IPS="$(IPS)" bash scripts/gen-dev-certs.sh
	@echo "✓ Production certificates generated"

.PHONY: setup
setup: deps certs token ## Complete setup (dependencies + certificates + token)
	@echo ""
	@echo "✓ Setup complete! Next steps:"
	@echo "  1. Copy the generated token to config/config.yaml security.allowed_tokens"
	@echo "  2. Build: make build"
	@echo "  3. Run: make run"

.PHONY: version
version: ## Show version information
	@echo "Version:    $(VERSION)"
	@echo "Build Time: $(BUILD_TIME)"
	@echo "Git Commit: $(GIT_COMMIT)"

.PHONY: info
info: version ## Show build information
	@echo ""
	@echo "Go Version: $(shell go version)"
	@echo "GOOS:       $(shell go env GOOS)"
	@echo "GOARCH:     $(shell go env GOARCH)"

.PHONY: docker-build
docker-build: ## Build Docker image
	@echo "Building Docker image..."
	docker build -t grpc-mesh-server:$(VERSION) -t grpc-mesh-server:latest .
	@echo "✓ Docker image built: grpc-mesh-server:$(VERSION)"

.PHONY: docker-run
docker-run: ## Run Docker container
	@echo "Running Docker container..."
	docker run --rm -p 8443:8443 -p 50051:50051 -v $(PWD)/config:/config grpc-mesh-server:latest

.PHONY: proto
proto: ## Generate protobuf code (if needed)
	@echo "Generating protobuf code..."
	@which protoc > /dev/null || (echo "protoc not installed" && exit 1)
	protoc --proto_path=../grpc_mesh/rpc/v1 \
		--go_out=pkg/rpc --go_opt=paths=source_relative \
		--go-grpc_out=pkg/rpc --go-grpc_opt=paths=source_relative \
		grpc_mesh.proto
	@echo "✓ Protobuf code generated"

.PHONY: help
help: ## Show this help message
	@echo "grpc-mesh-server Makefile"
	@echo ""
	@echo "Usage:"
	@echo "  make <target>"
	@echo ""
	@echo "Targets:"
	@awk 'BEGIN {FS = ":.*##"; printf ""} /^[a-zA-Z_-]+:.*?##/ { printf "  %-20s %s\n", $$1, $$2 } /^##@/ { printf "\n%s\n", substr($$0, 5) } ' $(MAKEFILE_LIST)

# Default target
.DEFAULT_GOAL := help

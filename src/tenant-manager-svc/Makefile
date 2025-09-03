.PHONY: build run clean test deps

# Build configuration
BINARY_NAME=tenant-manager-svc
BIN_DIR=bin
MAIN_FILE=main.go

# Default target
all: deps build

# Install dependencies
deps:
	go mod tidy
	go mod download

# Build the binary
build:
	@echo "Building $(BINARY_NAME)..."
	@mkdir -p $(BIN_DIR)
	go build -o $(BIN_DIR)/$(BINARY_NAME) $(MAIN_FILE)
	@echo "Build complete: $(BIN_DIR)/$(BINARY_NAME)"

# Run the service
run: build
	@echo "Starting $(BINARY_NAME)..."
	./$(BIN_DIR)/$(BINARY_NAME)

# Run tests
test:
	go test -v ./...

# Run tests with coverage
test-coverage:
	go test -v -cover ./...

# Clean build artifacts
clean:
	@echo "Cleaning..."
	rm -rf $(BIN_DIR)
	go clean

# Development run (with hot reload using air if available)
dev:
	@if command -v air > /dev/null; then \
		air; \
	else \
		echo "Air not found. Install with: go install github.com/cosmtrek/air@latest"; \
		$(MAKE) run; \
	fi

# Format code
fmt:
	go fmt ./...

# Vet code
vet:
	go vet ./...

# Lint code (requires golangci-lint)
lint:
	@if command -v golangci-lint > /dev/null; then \
		golangci-lint run; \
	else \
		echo "golangci-lint not found. Install from https://golangci-lint.run/"; \
	fi

# Check for security issues (requires gosec)
security:
	@if command -v gosec > /dev/null; then \
		gosec ./...; \
	else \
		echo "gosec not found. Install with: go install github.com/securecodewarrior/gosec/v2/cmd/gosec@latest"; \
	fi

# Full quality check
quality: fmt vet lint security test

# Build for production
build-prod:
	@echo "Building $(BINARY_NAME) for production..."
	@mkdir -p $(BIN_DIR)
	CGO_ENABLED=0 GOOS=linux GOARCH=amd64 go build -ldflags="-w -s" -o $(BIN_DIR)/$(BINARY_NAME) $(MAIN_FILE)
	@echo "Production build complete: $(BIN_DIR)/$(BINARY_NAME)"

# Docker build (if Dockerfile exists)
docker-build:
	@if [ -f Dockerfile ]; then \
		docker build -t $(BINARY_NAME):latest .; \
	else \
		echo "Dockerfile not found"; \
	fi
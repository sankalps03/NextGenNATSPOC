# Development Workflow for Go + NATS Microservices

This document defines the standard development workflow, tooling, and practices for building and maintaining microservices.

## Development Environment Setup

### Prerequisites
- **Go 1.24+**: Latest stable Go version with generics support
- **NATS Server**: Local NATS instance or Docker container
- **Docker**: For containerization and local testing
- **Git**: Version control with branch-based workflow
- **IDE/Editor**: VS Code, GoLand, or equivalent with Go support

### Local Development Stack
```bash
# Essential tools
go install golang.org/x/tools/gopls@latest          # Language server
go install github.com/golangci/golangci-lint/cmd/golangci-lint@latest  # Linter
go install golang.org/x/tools/cmd/goimports@latest  # Import formatting
go install github.com/pressly/goose/v3/cmd/goose@latest  # Database migrations

# Testing tools  
go install github.com/onsi/ginkgo/v2/ginkgo@latest  # BDD testing framework
go install gotest.tools/gotestsum@latest            # Test output formatting

# NATS tools
go install github.com/nats-io/natscli/nats@latest   # NATS CLI
```

### Project Structure Standards
```
service-name/
├── cmd/
│   └── server/
│       └── main.go                 # Application entry point
├── internal/
│   ├── api/                        # HTTP handlers and routes
│   ├── domain/                     # Business logic and entities
│   ├── infrastructure/             # External service adapters
│   ├── messaging/                  # NATS publishers and subscribers  
│   └── storage/                    # Database and persistence
├── pkg/                            # Public libraries (if any)
├── configs/
│   ├── local.yaml                  # Local development config
│   ├── test.yaml                   # Test environment config
│   └── production.yaml             # Production config template
├── deployments/
│   ├── Dockerfile                  # Container definition
│   ├── docker-compose.yaml        # Local development stack
│   └── k8s/                        # Kubernetes manifests
├── scripts/
│   ├── build.sh                    # Build automation
│   ├── test.sh                     # Test execution
│   └── migrate.sh                  # Database migrations
├── test/
│   ├── integration/                # Integration tests
│   ├── fixtures/                   # Test data
│   └── mocks/                      # Generated mocks
├── docs/
│   ├── CLAUDE.md                   # Service-specific AI guidance
│   ├── API.md                      # API documentation
│   └── DEPLOYMENT.md               # Deployment instructions
├── go.mod                          # Go modules
├── go.sum                          # Module checksums
├── .golangci.yml                   # Linter configuration
├── Makefile                        # Build automation
└── README.md                       # Project overview
```

## Build and Test Commands

### Standard Makefile Targets
```makefile
.PHONY: help build test lint clean docker run

help:           ## Show help messages
build:          ## Build the application binary
test:           ## Run all tests with coverage
test-unit:      ## Run unit tests only
test-integration: ## Run integration tests only
lint:           ## Run code linting and formatting
clean:          ## Clean build artifacts
docker:         ## Build Docker image
run:            ## Run application locally
migrate-up:     ## Apply database migrations
migrate-down:   ## Rollback database migrations
```

### Build Commands
```bash
# Development build
make build

# Production build with optimizations
go build -ldflags="-s -w -X main.version=$(git describe --tags)" -o bin/server cmd/server/main.go

# Cross-platform build
GOOS=linux GOARCH=amd64 go build -o bin/server-linux cmd/server/main.go
```

### Testing Commands
```bash
# Run all tests with coverage
make test

# Run tests with verbose output
go test -v ./...

# Run tests with coverage report
go test -coverprofile=coverage.out ./...
go tool cover -html=coverage.out -o coverage.html

# Run specific test package
go test -v ./internal/domain/...

# Run integration tests only
go test -v -tags=integration ./test/integration/...

# Benchmark tests
go test -bench=. -benchmem ./...
```

### Code Quality Commands
```bash
# Run linter
make lint

# Format code
gofmt -s -w .
goimports -w .

# Detect race conditions
go test -race ./...

# Static analysis
go vet ./...

# Security scanning
gosec ./...
```

## Git Workflow

### Branch Strategy
- **main**: Production-ready code, protected branch
- **develop**: Integration branch for features
- **feature/**: Feature development branches
- **hotfix/**: Critical production fixes
- **release/**: Release preparation branches

### Commit Message Format
```
type(scope): brief description

Detailed explanation of changes made and why.

Breaking Change: description of breaking change if any
Closes: #123, #456
```

**Types**: feat, fix, docs, style, refactor, perf, test, chore

### Pull Request Process
1. **Create Feature Branch**: `git checkout -b feature/ticket-creation`
2. **Implement Changes**: Follow TDD approach when possible
3. **Run Quality Checks**: Ensure tests pass and linting passes
4. **Update Documentation**: Update relevant documentation
5. **Create Pull Request**: Include description and testing notes
6. **Code Review**: Address reviewer feedback
7. **Merge**: Squash and merge to maintain clean history

## Testing Strategy

### Test Types and Structure

#### Unit Tests
```go
// internal/domain/ticket_test.go
package domain_test

import (
    "testing"
    "github.com/stretchr/testify/assert"
    "github.com/stretchr/testify/require"
)

func TestTicket_Create(t *testing.T) {
    tests := []struct {
        name    string
        input   CreateTicketRequest
        want    Ticket
        wantErr bool
    }{
        {
            name: "valid ticket creation",
            input: CreateTicketRequest{
                Title: "Test Ticket",
                Priority: "high",
            },
            want: Ticket{
                Title: "Test Ticket",
                Status: "open",
                Priority: "high",
            },
            wantErr: false,
        },
    }
    
    for _, tt := range tests {
        t.Run(tt.name, func(t *testing.T) {
            got, err := CreateTicket(tt.input)
            if tt.wantErr {
                require.Error(t, err)
                return
            }
            require.NoError(t, err)
            assert.Equal(t, tt.want.Title, got.Title)
            assert.Equal(t, tt.want.Status, got.Status)
        })
    }
}
```

#### Integration Tests
```go
// test/integration/api_test.go
//go:build integration
// +build integration

package integration_test

import (
    "context"
    "testing"
    "github.com/testcontainers/testcontainers-go"
    "github.com/stretchr/testify/suite"
)

type APITestSuite struct {
    suite.Suite
    server     *http.Server
    natsConn   *nats.Conn
    dbConn     *sql.DB
    containers []testcontainers.Container
}

func (s *APITestSuite) SetupSuite() {
    // Start test containers (NATS, Database)
    // Initialize test server
    // Prepare test data
}

func (s *APITestSuite) TearDownSuite() {
    // Clean up containers and connections
}

func (s *APITestSuite) TestCreateTicket() {
    // Test API endpoint with real infrastructure
}
```

### Test Configuration
```yaml
# .golangci.yml
linters-settings:
  govet:
    check-shadowing: true
  gocyclo:
    min-complexity: 10
  dupl:
    threshold: 100

linters:
  enable:
    - govet
    - errcheck
    - staticcheck
    - unused
    - gosimple
    - structcheck
    - varcheck
    - ineffassign
    - deadcode
    - gocyclo
    - dupl
    - misspell
    - lll
    - unparam
```

## Local Development Workflow

### Environment Setup
```bash
# 1. Clone repository
git clone <repository-url>
cd service-name

# 2. Install dependencies
go mod download

# 3. Start local infrastructure
docker-compose up -d

# 4. Run database migrations
make migrate-up

# 5. Start development server
make run
```

### Development Docker Compose
```yaml
# docker-compose.yaml
version: '3.8'
services:
  nats:
    image: nats:alpine
    ports:
      - "4222:4222"
      - "8222:8222"
    command: ["-js", "-m", "8222"]
    
  postgres:
    image: postgres:15-alpine
    environment:
      POSTGRES_DB: service_db
      POSTGRES_USER: service_user
      POSTGRES_PASSWORD: service_pass
    ports:
      - "5432:5432"
    volumes:
      - postgres_data:/var/lib/postgresql/data
      
  redis:
    image: redis:alpine
    ports:
      - "6379:6379"

volumes:
  postgres_data:
```

### Hot Reload Development
```bash
# Install air for hot reload
go install github.com/cosmtrek/air@latest

# Run with hot reload
air

# Or use with custom config
air -c .air.toml
```

## Configuration Management

### Environment-Based Configuration
```go
// internal/config/config.go
type Config struct {
    Server   ServerConfig   `yaml:"server"`
    Database DatabaseConfig `yaml:"database"`
    NATS     NATSConfig     `yaml:"nats"`
    Logging  LoggingConfig  `yaml:"logging"`
}

type ServerConfig struct {
    Port         int           `yaml:"port" env:"SERVER_PORT" env-default:"8080"`
    ReadTimeout  time.Duration `yaml:"read_timeout" env:"SERVER_READ_TIMEOUT" env-default:"30s"`
    WriteTimeout time.Duration `yaml:"write_timeout" env:"SERVER_WRITE_TIMEOUT" env-default:"30s"`
}
```

### Configuration Loading
```go
func LoadConfig() (*Config, error) {
    cfg := &Config{}
    
    // Load from file
    if err := cleanenv.ReadConfig("configs/config.yaml", cfg); err != nil {
        return nil, fmt.Errorf("failed to read config: %w", err)
    }
    
    // Override with environment variables
    if err := cleanenv.ReadEnv(cfg); err != nil {
        return nil, fmt.Errorf("failed to read env: %w", err)
    }
    
    // Validate configuration
    if err := cfg.Validate(); err != nil {
        return nil, fmt.Errorf("invalid config: %w", err)
    }
    
    return cfg, nil
}
```

## Deployment Preparation

### Docker Build
```dockerfile
# Dockerfile
FROM golang:1.21-alpine AS builder
WORKDIR /app
COPY go.mod go.sum ./
RUN go mod download
COPY . .
RUN CGO_ENABLED=0 GOOS=linux go build -ldflags="-s -w" -o server cmd/server/main.go

FROM alpine:latest
RUN apk --no-cache add ca-certificates tzdata
WORKDIR /app
COPY --from=builder /app/server .
COPY --from=builder /app/configs ./configs
EXPOSE 8080
CMD ["./server"]
```

### Health Checks
```go
// internal/api/health.go
func (h *HealthHandler) CheckHealth(w http.ResponseWriter, r *http.Request) {
    ctx, cancel := context.WithTimeout(r.Context(), 5*time.Second)
    defer cancel()
    
    checks := make(map[string]string)
    
    // Database health check
    if err := h.db.PingContext(ctx); err != nil {
        checks["database"] = "unhealthy"
    } else {
        checks["database"] = "healthy"
    }
    
    // NATS health check
    if !h.nats.IsConnected() {
        checks["nats"] = "unhealthy"
    } else {
        checks["nats"] = "healthy"
    }
    
    response := HealthResponse{
        Status:    "healthy",
        Timestamp: time.Now(),
        Checks:    checks,
    }
    
    // Set status to unhealthy if any check fails
    for _, status := range checks {
        if status == "unhealthy" {
            response.Status = "unhealthy"
            w.WriteHeader(http.StatusServiceUnavailable)
            break
        }
    }
    
    json.NewEncoder(w).Encode(response)
}
```

## Performance and Monitoring

### Metrics Collection
```go
// internal/metrics/metrics.go
var (
    RequestsTotal = prometheus.NewCounterVec(
        prometheus.CounterOpts{
            Name: "http_requests_total",
            Help: "Total number of HTTP requests",
        },
        []string{"method", "endpoint", "status_code"},
    )
    
    RequestDuration = prometheus.NewHistogramVec(
        prometheus.HistogramOpts{
            Name: "http_request_duration_seconds",
            Help: "HTTP request duration in seconds",
        },
        []string{"method", "endpoint"},
    )
)
```

### Profiling Setup
```go
// Add to main.go for development
import _ "net/http/pprof"

go func() {
    log.Println(http.ListenAndServe("localhost:6060", nil))
}()
```

This development workflow ensures consistent, high-quality development practices across all microservices while maintaining flexibility for service-specific needs.
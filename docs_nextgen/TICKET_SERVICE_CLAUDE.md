# Ticket Service - Claude Code Documentation

## Service Context

**Service Name**: `ticket-svc`  
**Domain**: Ticket Management  
**Language**: Go (1.24+)  
**Runtime**: Containerized  
**Team**: Platform Team  

### Business Purpose
The Ticket Service manages support tickets within a multi-tenant SaaS platform. It provides CRUD operations for tickets and publishes domain events for downstream processing by notification and audit services.

### Key Responsibilities
- Create, read, update, and delete tickets within tenant boundaries
- Validate ticket data and enforce business rules
- Publish domain events for ticket lifecycle changes
- Trigger notification events for high-severity tickets
- Maintain tenant isolation for all operations

## Boundaries

### Service Boundaries
**Owns**: Ticket entities, ticket lifecycle events, severity-based notification triggers  
**Consumes**: None (PoC has no external dependencies)  
**Publishes**: Ticket domain events, notification requests  

### Data Boundaries
- **Primary Entities**: Ticket (id, tenant, title, description, created_by, timestamps)
- **Tenant Scoping**: All tickets are tenant-scoped; no cross-tenant operations
- **Data Storage**: In-memory per-tenant maps (PoC only, database planned)

### Integration Boundaries
- **Upstream**: Direct HTTP clients (web UI, mobile apps, APIs)
- **Downstream**: Notification service, audit service (via NATS events)
- **External**: None in PoC scope

## Domain Model

### Core Entities

#### Ticket
```go
type Ticket struct {
    ID          string    `json:"id"`
    Tenant      string    `json:"tenant"`
    Title       string    `json:"title"`
    Description string    `json:"description"`
    CreatedBy   string    `json:"created_by"`
    CreatedAt   time.Time `json:"created_at"`
    UpdatedAt   time.Time `json:"updated_at"`
}
```

### Business Rules
- **Title**: Required on creation, max 200 characters
- **CreatedBy**: Required on creation, represents user identifier
- **Description**: Optional, max 2000 characters
- **Severity Detection**: Descriptions containing "critical" or "major" (case-insensitive) trigger notification events
- **Tenant Isolation**: All operations must include X-Tenant-ID header

### Value Objects
- **Meta**: Event metadata (event_id, tenant, occurred_at, schema)
- **TicketEvent**: Domain event wrapper (meta + ticket data)

## Message Contracts

### Published Events

#### 1. Ticket Created
- **Subject**: `{tenant}.ticket.ticket.event.created`
- **When**: After successful ticket creation
- **Payload**: TicketEvent with full ticket data
- **Headers**: tenant, schema: ticket.event.created@v1, Nats-Msg-Id, Content-Type: application/x-protobuf
- **Consumers**: notification-svc, audit-svc

#### 2. Ticket Updated  
- **Subject**: `{tenant}.ticket.ticket.event.updated`
- **When**: After successful ticket modification
- **Payload**: TicketEvent with updated ticket data
- **Headers**: tenant, schema: ticket.event.updated@v1, Nats-Msg-Id, Content-Type: application/x-protobuf
- **Consumers**: notification-svc, audit-svc

#### 3. Ticket Deleted
- **Subject**: `{tenant}.ticket.ticket.event.deleted`
- **When**: After successful ticket deletion
- **Payload**: TicketEvent with deleted ticket data
- **Headers**: tenant, schema: ticket.event.deleted@v1, Nats-Msg-Id, Content-Type: application/x-protobuf
- **Consumers**: audit-svc

#### 4. Ticket Read
- **Subject**: `{tenant}.ticket.ticket.event.read`
- **When**: After successful single ticket retrieval
- **Payload**: TicketEvent with retrieved ticket data
- **Headers**: tenant, schema: ticket.event.read@v1, Nats-Msg-Id, Content-Type: application/x-protobuf
- **Consumers**: audit-svc

#### 5. Tickets Listed
- **Subject**: `{tenant}.ticket.ticket.event.listed`  
- **When**: After successful ticket list retrieval
- **Payload**: TicketEvent with count metadata
- **Headers**: tenant, schema: ticket.event.listed@v1, Nats-Msg-Id, Content-Type: application/x-protobuf
- **Consumers**: audit-svc

#### 6. Notification Requested
- **Subject**: `{tenant}.notification.notification.event.requested`
- **When**: Create/update with "critical" or "major" in description
- **Payload**: TicketEvent with ticket data triggering notification
- **Headers**: tenant, schema: notification.event.requested@v1, Nats-Msg-Id, Content-Type: application/x-protobuf
- **Consumers**: notification-svc

### Message Schema
```protobuf
syntax = "proto3";
package ticketpb;
option go_package = "internal/pb;ticketpb";

message Meta {
  string event_id    = 1;
  string tenant      = 2;
  string occurred_at = 3;
  string schema      = 4;
}

message Ticket {
  string id          = 1;
  string tenant      = 2;
  string title       = 3;
  string description = 4;
  string created_by  = 5;
  string created_at  = 6;
  string updated_at  = 7;
}

message TicketEvent {
  Meta   meta = 1;
  Ticket data = 2;
}
```

## Technical Decisions

### Data Storage
- **PoC Decision**: No database - in-memory per-tenant maps only
- **Rationale**: Simplifies PoC development and deployment
- **Future**: Database planned for production (PostgreSQL recommended)

### Message Protocol
- **Decision**: Protobuf over NATS JetStream
- **Rationale**: Schema evolution support, compact serialization, type safety
- **Alternative Considered**: JSON (rejected for schema management complexity)

### Deduplication Strategy
- **Decision**: JetStream deduplication via Nats-Msg-Id header
- **Rationale**: At-least-once delivery with automatic duplicate prevention
- **Consumer Guidance**: Implement idempotent consumers as additional safety

### Event Sourcing
- **Decision**: Domain events for all state changes
- **Rationale**: Audit trail, eventual consistency, downstream service decoupling
- **Pattern**: Publish events after successful state mutations

## Integration Patterns

### HTTP API Integration
- **Pattern**: REST with tenant-scoped resources
- **Authentication**: X-Tenant-ID header validation
- **Error Handling**: Standard HTTP status codes with JSON error responses
- **Content Negotiation**: JSON request/response bodies

### Event-Driven Integration
- **Pattern**: Publish-subscribe with JetStream persistence
- **Reliability**: At-least-once delivery with consumer acknowledgments
- **Ordering**: No ordering guarantees across tenants; FIFO within tenant subjects
- **Error Handling**: Dead letter queue for failed message processing

### Consumer Guidance
- **Acknowledgment**: Manual ack after successful processing
- **Retry Strategy**: Exponential backoff (100ms base, max 30s, max 5 retries)
- **Idempotency**: Use event_id for deduplication in consumer logic
- **Error Handling**: Log failures, send to dead letter, alert operations

## Development Patterns

### Project Structure
```
ticket-svc/
├── cmd/server/           # Application entry point
├── internal/
│   ├── api/             # HTTP handlers and routing
│   ├── domain/          # Business logic and entities
│   ├── events/          # Event publishing logic  
│   ├── pb/              # Generated protobuf code
│   └── storage/         # In-memory storage implementation
├── proto/               # Protobuf schema definitions
├── docker/             # Container configuration
└── docs/               # Service documentation
```

### Code Organization
- **Domain Layer**: Pure business logic, no infrastructure dependencies
- **API Layer**: HTTP request/response handling, validation
- **Events Layer**: Message publishing, event construction
- **Storage Layer**: Data persistence abstraction (in-memory for PoC)

### Configuration Management
- **Environment Variables**: NATS_URL, SERVICE_NAME, LOG_LEVEL, X_TENANT_HEADER
- **Validation**: Fail fast on missing required configuration
- **Defaults**: Conservative defaults for optional settings

## Testing Strategy

### Unit Tests (70%)
- **Domain Logic**: Ticket validation, business rules, severity detection
- **Event Construction**: Message payload validation, header generation
- **Utilities**: containsSeverity function, tenant extraction logic
- **Coverage Target**: 90%+ for business logic

### Integration Tests (20%)  
- **NATS Publishing**: Event publishing with test containers
- **HTTP API**: End-to-end API testing with in-memory storage
- **Protobuf Contracts**: Schema compilation and serialization tests
- **Test Environment**: Ephemeral NATS server via testcontainers

### Contract Tests (10%)
- **Message Schema**: Protobuf compatibility validation
- **API Contracts**: Request/response schema validation
- **Event Consumers**: Mock consumer validation of event structure

### Testing Tools
- **Framework**: Standard Go testing, testify assertions
- **Mocking**: Manual mocks for external dependencies
- **Test Data**: Fixture-based test data generation
- **CI Integration**: All tests run on every commit

## Operational Concerns

### Performance Requirements
- **API Latency**: p50 < 100ms, p95 < 300ms (local development)
- **Event Publishing**: < 50ms publish latency
- **Throughput**: 1000 requests/minute per tenant (PoC target)
- **Memory**: < 100MB baseline, < 500MB under load

### Scalability Considerations
- **Horizontal Scaling**: Stateless service, multiple instances supported
- **Resource Limits**: CPU: 1 core, Memory: 512MB per instance
- **Load Balancing**: Round-robin HTTP load balancing
- **Event Partitioning**: Tenant-based subject partitioning

### Monitoring and Observability
- **Structured Logging**: JSON format with tenant, correlation_id, operation fields
- **Metrics**: HTTP request duration, event publish latency, error rates
- **Health Checks**: /health endpoint with NATS connectivity validation
- **Tracing**: OpenTelemetry-ready correlation IDs

### Security Considerations
- **Tenant Isolation**: X-Tenant-ID validation, tenant-scoped subjects
- **Data Protection**: No sensitive data in logs, encrypted message payloads planned
- **Access Control**: HTTP-level tenant validation (AuthN/AuthZ out of scope)
- **Future Security**: JWT tokens, NATS NKeys authentication planned

### Error Handling
- **HTTP Errors**: Structured JSON error responses with correlation IDs
- **Event Publishing**: Retry with backoff, dead letter on persistent failure
- **Resource Exhaustion**: Graceful degradation, circuit breakers
- **Logging**: All errors logged with full context

## Development Workflow

### Local Development Setup
1. **NATS Server**: Start local NATS with JetStream enabled
   ```bash
   nats-server -js
   ```
2. **Environment**: Set NATS_URL=nats://localhost:4222
3. **Stream Creation**: Platform docker-compose creates TICKET_EVENTS stream
4. **Service Start**: `go run cmd/server/main.go`
5. **Testing**: Use curl or Postman with X-Tenant-ID header

### Development Dependencies
- **Go 1.24+**: Language runtime
- **NATS Server**: Message broker (JetStream enabled)
- **Protoc**: Protocol buffer compiler
- **Docker**: Container runtime for testing

### Code Generation
- **Protobuf**: `protoc --go_out=. --go_opt=paths=source_relative proto/*.proto`
- **Wire**: Dependency injection code generation (if adopted)

### Git Workflow
- **Feature Branches**: All work done in feature branches
- **Pull Requests**: Required for all changes to main branch
- **Conventional Commits**: Standardized commit message format

## Release Process

### Build Pipeline
1. **Lint**: golangci-lint for code quality
2. **Test**: Unit, integration, and contract tests
3. **Build**: Multi-stage Docker image creation
4. **Security**: Vulnerability scanning (gosec, docker scan)
5. **Artifact**: Push container image to registry

### Deployment Strategy
- **Blue-Green**: Zero-downtime deployments
- **Health Checks**: Kubernetes liveness/readiness probes
- **Configuration**: Environment-specific ConfigMaps/Secrets
- **Rollback**: Automated rollback on health check failures

### Environment Progression
1. **Development**: Feature branch deployment
2. **Staging**: Integration testing environment  
3. **Production**: Blue-green deployment with monitoring

## Customization Instructions

### Schema Evolution
- **Versioning**: Use semantic versioning in schema headers (@v1, @v2)
- **Backward Compatibility**: Add new fields as optional
- **Breaking Changes**: Increment major version, update subject patterns
- **Migration**: Support multiple schema versions during transition

### Subject Pattern Evolution
- **Current**: `{tenant}.ticket.ticket.event.{action}`
- **Evolution**: Add service version to subjects when needed
- **Wildcards**: Consumer subscriptions use `*>.ticket.>.event.*`

### Business Rule Customization
- **Severity Keywords**: Configurable via environment variables
- **Validation Rules**: Implement as configurable business rules
- **Event Triggers**: Plugin architecture for custom event logic
- **Tenant Overrides**: Support per-tenant business rule customization

### Performance Tuning
- **Connection Pooling**: Configure NATS connection pool size
- **Batch Publishing**: Implement message batching for high throughput
- **Caching**: Add Redis caching layer for read operations
- **Database**: Replace in-memory storage with PostgreSQL

### Monitoring Customization
- **Metrics**: Add custom business metrics via Prometheus
- **Alerting**: Configure alerts for SLA violations
- **Dashboards**: Service-specific Grafana dashboards
- **Log Aggregation**: ELK/EFK stack integration
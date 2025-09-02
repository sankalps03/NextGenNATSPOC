# NextGen NATS POC - Ticket Service

A high-performance, event-driven ticket management microservice that handles ticket lifecycle operations via NATS messaging. This service operates as a pure NATS-based microservice without HTTP API endpoints.

## 🚀 Features

- **Pure NATS Microservice** - No HTTP endpoints, communicates only via NATS
- **NATS Request-Response** - Handles API server requests via NATS subjects
- **Event-Driven Publishing** - Publishes ticket lifecycle events to NATS JetStream
- **Multi-tenant Support** - Tenant-isolated ticket management
- **CRUD Operations** - Full ticket lifecycle management (Create, Read, Update, Delete)
- **Event Sourcing** - All ticket changes generate domain events
- **In-Memory Storage** - Fast, ephemeral storage for POC (production would use database)
- **Hot Reload** - Development mode with automatic restarts
- **Structured Logging** - JSON-based logging with request tracing

## 🔧 NATS Integration

### Service Subjects
The service responds to requests on:
- `ticket.service` - API requests from API server

### Event Publishing
The service publishes domain events:
- `ticket.create` - When tickets are created
- `ticket.update` - When tickets are modified
- `ticket.delete` - When tickets are deleted

## ⚙️ Configuration

The service is configured via environment variables:

| Variable | Default | Description |
|----------|---------|-------------|
| `NATS_URL` | `nats://127.0.0.1:4222,nats://127.0.0.1:4223,nats://127.0.0.1:4224` | NATS cluster URLs (comma-separated) |
| `SERVICE_NAME` | `ticket-service` | Service identifier for NATS |
| `LOG_LEVEL` | `info` | Logging level (debug, info, warn, error) |

## Development

### Prerequisites

- Go 1.24+
- NATS Server with JetStream enabled
- Docker (optional)

### Setup

1. Clone the repository
2. Install dependencies:
   ```bash
   make deps
   ```

3. Start NATS server with JetStream (or use the cluster):
   ```bash
   # Single node
   nats-server -js
   
   # Or use the Docker cluster
   cd ../docker-cluster && make start
   ```

4. Run the service:
   ```bash
   make run
   ```

### Building

```bash
make build
```

### Testing

```bash
make test
```

### Docker

Build and run with Docker:

```bash
make docker-build
make docker-run
```

## API Operations

The service handles these operations via NATS request-response:

### Create Ticket
- **Subject**: `ticket.service`
- **Action**: `create`
- **Request**: Ticket data with title, description, priority
- **Response**: Created ticket with ID and metadata
- **Events**: Publishes `ticket.create` event

### List Tickets
- **Subject**: `ticket.service`
- **Action**: `list`
- **Request**: Optional filters and pagination
- **Response**: Array of tickets for the tenant

### Get Ticket
- **Subject**: `ticket.service`
- **Action**: `get`
- **Request**: Ticket ID
- **Response**: Ticket details or error if not found

### Update Ticket
- **Subject**: `ticket.service`
- **Action**: `update`
- **Request**: Ticket ID and updated fields
- **Response**: Updated ticket data
- **Events**: Publishes `ticket.update` event

### Delete Ticket
- **Subject**: `ticket.service`
- **Action**: `delete`
- **Request**: Ticket ID
- **Response**: Success confirmation
- **Events**: Publishes `ticket.delete` event

## Data Model

### Ticket Structure
```go
type Ticket struct {
    ID          string    `json:"id"`
    Title       string    `json:"title"`
    Description string    `json:"description"`
    Priority    string    `json:"priority"`
    Status      string    `json:"status"`
    CreatedAt   time.Time `json:"created_at"`
    UpdatedAt   time.Time `json:"updated_at"`
    TenantID    string    `json:"tenant_id"`
}
```

### Request Format
```go
type ServiceRequest struct {
    Action   string      `json:"action"`
    TenantID string      `json:"tenant_id"`
    Data     interface{} `json:"data,omitempty"`
}
```

### Response Format
```go
type ServiceResponse struct {
    Success bool        `json:"success"`
    Data    interface{} `json:"data,omitempty"`
    Error   string      `json:"error,omitempty"`
}
```

## Business Rules

### Multi-Tenancy
- All operations are scoped to the tenant specified in requests
- Tickets are isolated per tenant in memory storage
- Event publishing includes tenant context

### Data Validation
- **Title**: Required, 1-200 characters
- **Description**: Optional, max 1000 characters
- **Priority**: Must be one of: low, medium, high, critical
- **Status**: Must be one of: open, in_progress, resolved, closed

### Event Publishing
- All state changes generate corresponding domain events
- Events include full ticket data and tenant context
- Events are published to NATS JetStream for durability

## Architecture

### Component Structure
```
ticket-svc/
├── main.go              # Application entry point with full implementation
├── go.mod               # Go module definition (shared with project root)
├── go.sum               # Go module checksums (shared with project root)
├── Makefile            # Build and development tasks
├── Dockerfile          # Multi-stage container build
└── README.md           # This documentation
```

### Service Flow
1. **NATS Connection**: Connect to NATS cluster with JetStream
2. **Service Registration**: Subscribe to `ticket.service` subject
3. **Request Processing**: Handle incoming service requests
4. **Data Operations**: Perform CRUD operations on in-memory store
5. **Event Publishing**: Publish domain events for state changes
6. **Response**: Send JSON response back via NATS
7. **Error Handling**: Structured error responses with logging

### Error Handling
- **Validation Errors**: Return 400-style errors for invalid input
- **Not Found**: Return 404-style errors for missing tickets
- **Tenant Isolation**: Prevent cross-tenant data access
- **NATS Failures**: Graceful degradation with retries
- **Logging**: All errors logged with full context

## Integration with API Server

The ticket service integrates with the API server by:
1. Subscribing to the `ticket.service` NATS subject
2. Processing HTTP requests forwarded as NATS messages
3. Maintaining the same request/response patterns as HTTP APIs
4. Publishing events to the same JetStream subjects used by other services
5. Following consistent multi-tenant patterns

## Monitoring

The service provides structured JSON logging with:
- Request tracing with tenant and operation context
- Performance metrics and latency measurements
- Error details with full stack traces
- NATS connectivity status
- Event publishing confirmation

Health checks validate:
- NATS connectivity and JetStream availability
- Service subscription status
- Last request processed timestamp

## Performance Targets (PoC)

- **Request Processing**: p50 < 10ms, p95 < 50ms
- **Event Publishing**: < 5ms additional latency
- **Throughput**: 1000 requests/minute per tenant
- **Memory Usage**: < 50MB baseline, < 200MB under load
- **NATS Response Time**: < 5ms for successful operations

## Testing

### Unit Testing
```bash
make test
```

### Load Testing
```bash
make load-test  # Simulates NATS requests to ticket.service
```

### Coverage Report
```bash
make coverage
```

## Security

- **Input Validation**: All inputs sanitized and validated
- **Tenant Isolation**: Strict tenant boundary enforcement
- **No Secrets**: No sensitive data stored or logged
- **Container Security**: Runs as non-root user (UID 65534)
- **Static Binary**: No dynamic dependencies in container

## Future Enhancements

- Database persistence (PostgreSQL/MongoDB)
- Advanced querying and filtering
- Ticket assignment and workflow states
- File attachment support
- Search and indexing capabilities
- Audit trail and change history
- Performance optimizations and caching
- Horizontal scaling with sharding
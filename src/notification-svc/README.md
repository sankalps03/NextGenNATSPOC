# NextGen NATS POC - Notification Service

A high-performance, event-driven notification microservice that processes ticket events and delivers notifications via NATS messaging. This service operates as a pure NATS-based microservice without HTTP API endpoints.

## 🚀 Features

- **Pure NATS Microservice** - No HTTP endpoints, communicates only via NATS
- **Event-Driven Processing** - Subscribes to NATS JetStream ticket events
- **Multi-tenant Support** - Tenant-isolated notification processing
- **Severity-based Filtering** - Only processes critical and major tickets
- **Template-based Messaging** - Customizable notification templates
- **Log-based Delivery** - Structured JSON output with log rotation
- **NATS Request-Response** - Handles API server requests via NATS subjects
- **Retry Logic** - Exponential backoff with dead letter queue
- **Deduplication** - Prevents duplicate notifications within time window
- **Hot Reload** - Development mode with automatic restarts

## 🔧 NATS Integration

### Service Subjects
The service responds to requests on:
- `notification.service` - API requests from API server

### Event Subscriptions  
The service subscribes to:
- `ticket.create` - New ticket creation events
- `ticket.update` - Ticket modification events
- `notification.event` - Direct notification triggers

### Event Publishing
The service publishes:
- `notification.created` - When notifications are generated

## Event Processing

The service subscribes to the following NATS subjects:

### Subscribed Events
- `ticket.create` - When tickets are created
- `ticket.update` - When tickets are updated  
- `notification.event` - Direct notification triggers

### Published Events
- `notification.created` - When a notification is created
- `notification.delivered` - When a notification is delivered
- `notification.failed` - When a notification delivery fails

## ⚙️ Configuration

The service is configured via environment variables:

| Variable | Default | Description |
|----------|---------|-------------|
| `NATS_URL` | `nats://127.0.0.1:4222,nats://127.0.0.1:4223,nats://127.0.0.1:4224` | NATS cluster URLs (comma-separated) |
| `SERVICE_NAME` | `notification-service` | Service identifier for NATS |
| `LOG_LEVEL` | `info` | Logging level |
| `LOG_FILE_PATH` | `./notifications.log` | Path for notification log files |
| `X_TENANT_HEADER` | `X-Tenant-ID` | Tenant identification header |
| `NOTIFICATION_RETENTION_DAYS` | `30` | Notification data retention period |

## Development

### Prerequisites

- Go 1.24+
- NATS Server with JetStream enabled
- Docker (optional)

### Setup

1. Clone the repository
2. Install dependencies:
   ```bash
   make dev-setup
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

## Business Rules

### Notification Triggering
- **Severity Detection**: Only tickets with descriptions containing "critical" or "major" (case-insensitive) trigger notifications
- **Deduplication**: Prevents duplicate notifications for the same ticket within 5 minutes
- **Templates**: Uses Go text/template syntax with predefined templates for different severities

### Multi-Tenancy
- All operations are scoped to the tenant specified in the X-Tenant-ID header
- Notifications are stored per-tenant in memory (PoC only)
- Log files are organized by tenant in subdirectories

### Error Handling
- **Retry Logic**: Exponential backoff with max 5 retries (100ms to 3.2s)
- **Dead Letter Queue**: Failed messages after max retries (logged for PoC)
- **Graceful Degradation**: Service continues operating even if individual notifications fail

## Architecture

### Component Structure
```
notification-svc/
├── main.go              # Application entry point with full implementation
├── go.mod               # Go module definition  
├── go.sum               # Go module checksums
├── Makefile            # Build and development tasks
├── Dockerfile          # Multi-stage container build
├── README.md           # This documentation
├── proto/              # Protobuf schema definitions
│   └── notification.proto # Notification service protobuf schema
├── templates/          # Notification message templates
│   ├── critical.tmpl   # Critical severity template
│   ├── major.tmpl      # Major severity template
│   └── default.tmpl    # Default notification template
└── internal/pb/        # Generated protobuf code (optional)
```

### Log Structure
```
{LOG_FILE_PATH}/notifications/{tenant}/notifications-{YYYY-MM-DD}.log
```

Each log entry is a JSON object with:
- `timestamp` - UTC timestamp
- `tenant` - Tenant identifier
- `notification_id` - Unique notification ID
- `ticket_id` - Associated ticket ID
- `severity` - Notification severity (critical/major)
- `message` - Formatted notification message
- `status` - Delivery status
- `delivered_at` - Delivery timestamp

### Event Flow
1. **Subscription**: Subscribe to ticket events from NATS JetStream
2. **Filtering**: Check if event contains critical/major keywords
3. **Deduplication**: Prevent duplicate notifications within time window
4. **Creation**: Generate notification with appropriate template
5. **Delivery**: Write structured JSON to tenant-specific log file
6. **Publishing**: Publish notification lifecycle events
7. **Acknowledgment**: Acknowledge NATS message after successful processing

## Monitoring

The service provides structured JSON logging with:
- Request tracing with tenant context
- Event processing metrics and latency
- Error details with full context
- NATS connectivity status
- Performance metrics

Health checks validate:
- NATS connectivity
- Last event processed timestamp
- Service operational status

## Performance Targets (PoC)

- **Event Processing Latency**: p50 < 50ms, p95 < 200ms
- **Notification Generation**: < 100ms from event to creation
- **Throughput**: 500 notifications/minute per tenant
- **Memory Usage**: < 50MB baseline, < 200MB under load
- **Event Acknowledgment**: < 10ms for successful processing

## Integration with Ticket Service

The notification service integrates with the existing ticket service by:
1. Subscribing to the same NATS JetStream cluster (nats-motadata)
2. Using the existing TICKET_EVENTS stream
3. Processing ticket.create, ticket.update, and notification.event subjects
4. Maintaining the same multi-tenant architecture patterns
5. Following consistent error handling and logging practices

## Future Enhancements

- Database persistence for notification history
- Email/SMS delivery channels
- User notification preferences
- Real-time delivery via WebSocket
- Advanced escalation rules
- Analytics and reporting dashboard
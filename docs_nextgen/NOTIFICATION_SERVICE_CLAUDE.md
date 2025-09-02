# Notification Service - Claude Code Documentation

## Service Context

**Service Name**: `notification-svc`  
**Domain**: Notification Management  
**Language**: Go (1.24+)  
**Runtime**: Containerized  
**Team**: Platform Team  

### Business Purpose
The Notification Service processes ticket events and delivers notifications to users based on ticket severity and business rules. It subscribes to ticket domain events from the ticket service and generates appropriate notifications through various channels (currently log-based for PoC).

### Key Responsibilities
- Subscribe to ticket domain events from NATS JetStream
- Process and filter events based on severity and notification rules
- Generate structured notification payloads for different channels
- Log critical and major severity notifications for audit and debugging
- Maintain tenant isolation for all notification processing
- Provide notification status tracking and delivery confirmation

## Boundaries

### Service Boundaries
**Owns**: Notification entities, notification delivery logic, notification templates  
**Consumes**: Ticket events from ticket-svc via NATS JetStream  
**Publishes**: Notification status events, delivery confirmations  

### Data Boundaries
- **Primary Entities**: Notification (id, tenant, ticket_id, severity, channel, status, timestamps)
- **Tenant Scoping**: All notifications are tenant-scoped; no cross-tenant operations
- **Data Storage**: In-memory per-tenant maps (PoC only, database planned)

### Integration Boundaries
- **Upstream**: Ticket Service (via NATS events)
- **Downstream**: Log files (PoC), future email/SMS providers
- **External**: None in PoC scope (future integrations with SendGrid, Twilio, etc.)

## Domain Model

### Core Entities

#### Notification
```go
type Notification struct {
    ID          string    `json:"id"`
    Tenant      string    `json:"tenant"`
    TicketID    string    `json:"ticket_id"`
    TicketTitle string    `json:"ticket_title"`
    Severity    string    `json:"severity"`
    Message     string    `json:"message"`
    Channel     string    `json:"channel"`
    Status      string    `json:"status"`
    CreatedAt   time.Time `json:"created_at"`
    UpdatedAt   time.Time `json:"updated_at"`
    DeliveredAt *time.Time `json:"delivered_at,omitempty"`
}
```

### Business Rules
- **Severity Filtering**: Only "critical" and "major" ticket events trigger notifications
- **Message Templates**: Predefined templates based on ticket event type and severity
- **Channel Selection**: Default to "log" channel for PoC, extensible for email/SMS
- **Tenant Isolation**: All notification processing must respect tenant boundaries
- **Deduplication**: Prevent duplicate notifications for same ticket within 5-minute time window
- **Status Tracking**: Track notification lifecycle (created, processing, delivered, failed)

### Value Objects
- **NotificationChannel**: Enum for delivery channels (log, email, sms, webhook)
- **NotificationStatus**: Enum for delivery status (pending, delivered, failed, skipped)
- **SeverityLevel**: Enum for ticket severity levels (low, medium, high, critical, major)

## Message Contracts

### Subscribed Events

#### 1. Ticket Created Events
- **Subject**: `ticket.create`
- **Source**: ticket-svc
- **Payload**: TicketEvent with full ticket data
- **Processing**: Check description for critical/major keywords, create notification if match
- **Filters**: Only process if description contains severity keywords

#### 2. Ticket Updated Events
- **Subject**: `ticket.update`
- **Source**: ticket-svc
- **Payload**: TicketEvent with updated ticket data
- **Processing**: Check for severity escalation, create notification if now critical/major
- **Filters**: Only process if severity increased or new critical keywords added

#### 3. Notification Events
- **Subject**: `notification.event`
- **Source**: ticket-svc
- **Payload**: Notification trigger event with ticket context
- **Processing**: Direct notification creation from explicit notification events
- **Filters**: Process all notification events regardless of content

### Published Events

#### 1. Notification Created
- **Subject**: `notification.created`
- **When**: After successful notification creation
- **Payload**: NotificationEvent with full notification data
- **Headers**: tenant, schema: notification.created@v1, Nats-Msg-Id, Content-Type: application/json
- **Consumers**: audit-svc, analytics-svc

#### 2. Notification Delivered
- **Subject**: `notification.delivered`
- **When**: After successful notification delivery
- **Payload**: NotificationEvent with delivery confirmation
- **Headers**: tenant, schema: notification.delivered@v1, Nats-Msg-Id, Content-Type: application/json
- **Consumers**: audit-svc, reporting-svc

#### 3. Notification Failed
- **Subject**: `notification.failed`
- **When**: After notification delivery failure
- **Payload**: NotificationEvent with error details
- **Headers**: tenant, schema: notification.failed@v1, Nats-Msg-Id, Content-Type: application/json
- **Consumers**: audit-svc, alerting-svc

## API Design

### HTTP Endpoints

#### 1. List Notifications
- **Method**: GET
- **Path**: `/api/v1/notifications`
- **Headers**: X-Tenant-ID (required)
- **Query Parameters**: 
  - `page` (optional): Page number for pagination
  - `size` (optional): Page size (default: 50, max: 100)
  - `status` (optional): Filter by notification status
  - `channel` (optional): Filter by notification channel
- **Response**: 200 OK with paginated notification list
- **Error Handling**: 400 for invalid parameters, 403 for unauthorized tenant access

#### 2. Get Notification
- **Method**: GET
- **Path**: `/api/v1/notifications/{id}`
- **Headers**: X-Tenant-ID (required)
- **Response**: 200 OK with notification details, 404 if not found
- **Error Handling**: 403 for cross-tenant access attempts

#### 3. Health Check
- **Method**: GET
- **Path**: `/health`
- **Headers**: None required
- **Response**: 200 OK with service health status and NATS connectivity
- **Metrics**: Include NATS connection status, last event processed timestamp

### Event Processing Patterns

#### 1. Event Subscription
- **Pattern**: Durable consumer with manual acknowledgment
- **Consumer Group**: notification-service-consumer
- **Delivery Semantics**: At-least-once with deduplication
- **Error Handling**: Dead letter queue after 5 processing failures
- **Retry Strategy**: Exponential backoff (100ms base, 2x multiplier, max 3.2s delay, max 5 retries)
- **Stream**: TICKET_EVENTS (existing stream from ticket-svc cluster)

#### 2. Message Processing
- **Pattern**: Single-threaded per tenant to maintain ordering
- **Concurrency**: Multi-tenant parallel processing with goroutines
- **State Management**: In-memory state with periodic persistence
- **Transaction Semantics**: Process event -> Create notification -> Acknowledge message

## Technical Architecture

### Component Structure
```
notification-svc/
├── main.go              # Application entry point with full implementation
├── go.mod               # Go module definition  
├── go.sum               # Go module checksums
├── Makefile            # Build and development tasks
├── Dockerfile          # Multi-stage container build
├── README.md           # Service documentation
├── .gitignore          # Git ignore patterns
├── proto/              # Protobuf schema definitions
│   └── notification.proto # Notification service protobuf schema
├── templates/          # Notification message templates
│   ├── critical.tmpl   # Critical severity template
│   ├── major.tmpl      # Major severity template
│   └── default.tmpl    # Default notification template
└── internal/pb/        # Generated protobuf code
```

### Dependencies
- **NATS**: Message broker for event subscription
- **Gorilla Mux**: HTTP routing and middleware
- **UUID**: Unique identifier generation (google/uuid)
- **Logrus**: Structured logging with JSON formatter
- **Go Templates**: Message template rendering (text/template)
- **Lumberjack**: Log rotation and compression (gopkg.in/natefinch/lumberjack.v2)

### Configuration
Environment variables:
- `NATS_URL`: NATS server URL (default: `nats://localhost:4222`)
- `PORT`: HTTP server port (default: `8081`)
- `SERVICE_NAME`: Service identifier (default: `notification-svc`)
- `LOG_LEVEL`: Logging level (default: `info`)
- `LOG_FILE_PATH`: Path for notification log files (default: `./notifications.log`)
- `X_TENANT_HEADER`: Tenant header name (default: `X-Tenant-ID`)
- `NOTIFICATION_RETENTION_DAYS`: Notification data retention (default: `30`)

## Implementation Specifications

### Event Processing Logic
1. **Event Reception**: Subscribe to ticket.* and notification.* subjects
2. **Severity Detection**: Case-insensitive substring match for "critical" OR "major" in ticket description
3. **Notification Creation**: Generate notification entity with appropriate template
4. **Channel Routing**: Route to appropriate delivery channel (log for PoC)
5. **Status Tracking**: Update notification status through delivery lifecycle
6. **Event Publishing**: Publish notification lifecycle events

### Notification Templates
Using Go text/template syntax with {{.FieldName}} placeholders:
- **Critical Template**: "🚨 CRITICAL: Ticket #{{.TicketID}} requires immediate attention: {{.TicketTitle}}"
- **Major Template**: "⚠️ MAJOR: Ticket #{{.TicketID}} needs priority handling: {{.TicketTitle}}"
- **Default Template**: "📋 Ticket #{{.TicketID}} notification: {{.TicketTitle}}"

### Log-Based Delivery (PoC)
- **Format**: Structured JSON logs with notification details
- **Rotation**: Daily log rotation at midnight UTC with gzip compression (using lumberjack)
- **Max Size**: 100MB per file, keep 7 days of logs
- **Fields**: timestamp, tenant, notification_id, ticket_id, severity, message, status, delivered_at
- **Location**: {LOG_FILE_PATH}/notifications/{tenant}/notifications-{YYYY-MM-DD}.log

### Error Handling Strategy
- **Transient Errors**: Exponential backoff retry with jitter (max 5 retries, base delay 100ms)
- **Permanent Errors**: Dead letter queue after 5 failed attempts
- **Processing Errors**: Log error details and mark notification as failed
- **NATS Disconnection**: Automatic reconnection with state recovery (max 10 attempts, 1s base delay)

### Multi-Tenancy Implementation
- **Event Filtering**: Process only events for valid tenants
- **Data Isolation**: Separate notification storage per tenant
- **Resource Limits**: Per-tenant notification rate limiting
- **Configuration**: Tenant-specific notification preferences

## Performance Targets (PoC)

- **Event Processing Latency**: p50 < 50ms, p95 < 200ms
- **Notification Generation**: < 100ms from event to notification creation
- **Throughput**: 500 notifications/minute per tenant
- **Memory Usage**: < 50MB baseline, < 200MB under load
- **Event Acknowledgment**: < 10ms for successful processing

## Monitoring and Observability

### Metrics
- Notifications created/delivered/failed per tenant
- Event processing latency and throughput
- NATS subscription health and message lag
- Template rendering performance
- Memory usage and goroutine counts

### Logging
- Structured JSON logging with tenant context
- Notification lifecycle events with correlation IDs
- Error logging with full stack traces
- Performance metrics for event processing pipeline
- Tenant-specific notification statistics

### Health Checks
- NATS connectivity validation
- Event consumer status verification
- Template system functionality
- Log file write permissions
- Memory and goroutine health status

## Future Enhancements

### Channel Expansion
- Email notifications via SendGrid/SMTP
- SMS notifications via Twilio/AWS SNS
- Webhook notifications for external systems
- In-app notifications via WebSocket/SSE
- Mobile push notifications

### Advanced Features
- Notification preferences per user/tenant
- Escalation rules and reminder notifications
- Digest notifications for non-critical events
- Rich notification templates with attachments
- Analytics and reporting dashboard

### Scalability Improvements
- Database persistence for notification history
- Horizontal scaling with message partitioning
- Caching layer for frequently accessed data
- Batch processing for high-volume notifications
- Queue management and backpressure handling

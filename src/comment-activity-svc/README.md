# NextGen NATS POC - Comment & Activity Service

A microservice implementing the CQRS pattern that manages user comments and chronological activity timelines for tickets. This service demonstrates event-driven architecture by subscribing to ticket events and providing a comprehensive activity feed for ticket management systems.

## 🚀 Features

- **CQRS Implementation** - Separates command (write) and query (read) operations
- **Event-Driven Architecture** - Subscribes to ticket events and generates activities
- **Comment Management** - Full CRUD operations for ticket comments
- **Activity Timeline** - Chronological activity feed with automatic event processing
- **Multi-tenant Support** - Tenant-isolated data storage and operations
- **NATS Request-Response** - Handles API server requests via NATS subjects
- **Event Publishing** - Publishes comment and activity events for other services
- **Real-time Updates** - Immediate activity generation from ticket events
- **Structured Logging** - JSON-based logging with request tracing

## 🔧 NATS Integration

### Service Subjects
The service responds to requests on:
- `comment-activity.service` - API requests from API server

### Subscribed Subjects
The service listens to ticket events:
- `ticket.created` - New ticket creation events
- `ticket.updated` - Ticket modification events  
- `ticket.deleted` - Ticket deletion events

### Published Subjects
The service publishes events:
- `comment.created` - When comments are added
- `activity.created` - When activities are generated

### Stream Configuration
Uses the `ACTIVITY_EVENTS` JetStream stream with:
- File storage for durability
- 7-day retention period
- 1M message limit

## ⚙️ Configuration

The service is configured via environment variables:

| Variable | Default | Description |
|----------|---------|-------------|
| `NATS_URL` | `nats://127.0.0.1:4222,nats://127.0.0.1:4223,nats://127.0.0.1:4224` | NATS cluster URLs |
| `SERVICE_NAME` | `comment-activity-service` | Service identifier |
| `LOG_LEVEL` | `info` | Logging level |

## Development

### Prerequisites

- Go 1.24+
- NATS Server with JetStream enabled
- NATS CLI tool (for testing)
- Docker (optional)

### Setup

1. Clone the repository
2. Install dependencies:
   ```bash
   make deps
   ```

3. Start NATS server with JetStream:
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

### Add Comment
- **Action**: `add_comment`
- **Request Data**: 
  ```json
  {
    "ticket_id": "ticket-123",
    "user_id": "user1", 
    "user_name": "John Doe",
    "content": "This is a comment"
  }
  ```
- **Response**: Created comment with ID and metadata
- **Side Effects**: Creates activity entry, publishes events

### Get Comments
- **Action**: `get_comments`
- **Request Data**:
  ```json
  {
    "ticket_id": "ticket-123",
    "limit": 50,
    "offset": 0
  }
  ```
- **Response**: Paginated list of comments sorted by creation time

### Get Activities
- **Action**: `get_activities`
- **Request Data**:
  ```json
  {
    "ticket_id": "ticket-123", 
    "limit": 100,
    "offset": 0
  }
  ```
- **Response**: Paginated list of activities sorted by timestamp (newest first)

### Get Timeline
- **Action**: `get_timeline`
- **Request Data**:
  ```json
  {
    "ticket_id": "ticket-123",
    "limit": 100,
    "offset": 0
  }
  ```
- **Response**: Combined timeline of activities and comments sorted chronologically

## Data Models

### Comment Structure
```go
type Comment struct {
    ID        string    `json:"id"`
    TicketID  string    `json:"ticket_id"`
    TenantID  string    `json:"tenant_id"`
    UserID    string    `json:"user_id"`
    UserName  string    `json:"user_name"`
    Content   string    `json:"content"`
    CreatedAt time.Time `json:"created_at"`
    UpdatedAt time.Time `json:"updated_at"`
}
```

### Activity Structure
```go
type Activity struct {
    ID          string                 `json:"id"`
    TicketID    string                 `json:"ticket_id"`
    TenantID    string                 `json:"tenant_id"`
    Type        string                 `json:"type"` // comment, status_change, assignment, etc.
    Description string                 `json:"description"`
    UserID      string                 `json:"user_id"`
    UserName    string                 `json:"user_name"`
    Metadata    map[string]interface{} `json:"metadata"`
    Timestamp   time.Time              `json:"timestamp"`
}
```

### Ticket Event Structure
```go
type TicketEvent struct {
    EventID   string                 `json:"event_id"`
    EventType string                 `json:"event_type"`
    TenantID  string                 `json:"tenant_id"`
    TicketID  string                 `json:"ticket_id"`
    UserID    string                 `json:"user_id"`
    UserName  string                 `json:"user_name"`
    Changes   map[string]interface{} `json:"changes"`
    Timestamp time.Time              `json:"timestamp"`
}
```

## Activity Types

The service automatically generates activities for various ticket events:

### Creation Activities
- **Type**: `creation`
- **Description**: "Ticket created by [UserName]"
- **Trigger**: `ticket.created` events

### Update Activities  
- **Type**: `update`
- **Description**: Smart descriptions based on what changed:
  - Status: "changed status from Open to In Progress"
  - Priority: "changed priority from Low to High" 
  - Assignment: "assigned to John Doe" / "reassigned from Alice to Bob"
  - Title/Description: "updated title" / "updated description"
- **Trigger**: `ticket.updated` events

### Comment Activities
- **Type**: `comment`
- **Description**: "[UserName] added a comment"
- **Trigger**: Direct comment additions via API
- **Metadata**: Includes comment ID and content

### Deletion Activities
- **Type**: `deletion`
- **Description**: "Ticket deleted by [UserName]"
- **Trigger**: `ticket.deleted` events

## Event-Driven Architecture

### CQRS Pattern Implementation

1. **Command Side (Write)**:
   - Add comments via API requests
   - Generate activities from ticket events
   - Store data in tenant-isolated collections

2. **Query Side (Read)**:
   - Serve comment lists with pagination
   - Provide activity feeds with filtering
   - Generate combined timelines

3. **Event Sourcing**:
   - All changes generate events
   - Events published to NATS for other services
   - Immutable activity log

### Pub/Sub Integration

```
Ticket Service → ticket.created/updated/deleted → Comment & Activity Service
                                                        ↓
                                        Generate Activities
                                                        ↓
                              comment.created/activity.created → Other Services
```

### Request-Reply Integration

```
API Server → comment-activity.service → Comment & Activity Service
                                                ↓
                                        Process Request
                                                ↓
                                        Return Response
```

## Usage Examples

### Testing with NATS CLI

```bash
# Add a comment
nats request comment-activity.service '{
  "action": "add_comment",
  "tenant_id": "tenant1",
  "data": {
    "ticket_id": "ticket-123",
    "user_id": "user1",
    "user_name": "John Doe", 
    "content": "This is a test comment"
  }
}'

# Get comments for a ticket
nats request comment-activity.service '{
  "action": "get_comments",
  "tenant_id": "tenant1",
  "data": {
    "ticket_id": "ticket-123",
    "limit": 10
  }
}'

# Get activity timeline
nats request comment-activity.service '{
  "action": "get_timeline", 
  "tenant_id": "tenant1",
  "data": {
    "ticket_id": "ticket-123",
    "limit": 20
  }
}'
```

### Simulating Ticket Events

```bash
# Simulate ticket creation
nats pub ticket.created '{
  "event_id": "evt1",
  "event_type": "ticket.created",
  "tenant_id": "tenant1",
  "ticket_id": "ticket-123",
  "user_id": "user1",
  "user_name": "John Doe",
  "changes": {},
  "timestamp": "2024-01-01T12:00:00Z"
}'

# Simulate status change
nats pub ticket.updated '{
  "event_id": "evt2", 
  "event_type": "ticket.updated",
  "tenant_id": "tenant1",
  "ticket_id": "ticket-123",
  "user_id": "user2",
  "user_name": "Jane Smith",
  "changes": {
    "status": {"old": "open", "new": "in_progress"}
  },
  "timestamp": "2024-01-01T12:05:00Z"
}'
```

### Monitoring Events

```bash
# Monitor published events
nats sub activity.created comment.created
```

## Integration with Other Services

### Ticket Service Integration
- **Event Subscription**: Listens to all ticket lifecycle events
- **Activity Generation**: Automatically creates activity entries
- **Change Detection**: Analyzes event payloads to generate meaningful descriptions
- **Tenant Isolation**: Maintains same multi-tenant patterns

### API Server Integration
- **Request Routing**: Handles comment and activity API requests
- **Response Formatting**: Provides structured responses for frontend consumption
- **Error Handling**: Returns appropriate error codes and messages
- **Pagination**: Supports pagination for large datasets

### Notification Service Integration (Future)
- **Event Publishing**: Publishes comment/activity events for notifications
- **Real-time Updates**: Enables real-time UI updates via event streams
- **User Mentions**: Could trigger notifications for @mentions in comments

## Architecture

### Component Structure
```
comment-activity-svc/
├── main.go              # Application entry point with full implementation
├── go.mod               # Go module definition
├── go.sum               # Go module checksums  
├── Makefile            # Build and development tasks
├── Dockerfile          # Multi-stage container build
└── README.md           # This documentation
```

### Data Storage (POC)
- **In-Memory Maps**: Tenant-isolated storage for POC
- **Thread Safety**: RWMutex for concurrent access
- **Data Structure**:
  - `comments[tenant_id][comment_id] → Comment`
  - `activities[tenant_id] → []Activity`

### Error Handling
- **Validation Errors**: Required field validation with clear messages
- **Event Processing**: Graceful handling of malformed events
- **NATS Failures**: Automatic retry and reconnection
- **Data Consistency**: Transaction-like operations for comment+activity creation

## Monitoring

### Service Health
- NATS connectivity status
- Event processing metrics
- Comment/activity creation rates
- Memory usage tracking

### Structured Logging
```json
{
  "timestamp": "2024-01-01T12:00:00Z",
  "level": "INFO", 
  "service": "comment-activity-service",
  "tenant_id": "tenant1",
  "action": "add_comment",
  "ticket_id": "ticket-123",
  "user_id": "user1",
  "processing_time_ms": 15
}
```

### Key Metrics
- Comments created per minute
- Activities generated per event type
- API request latency (p50, p95, p99)
- Event processing lag
- Timeline query performance

## Performance Targets (POC)

- **Comment Creation**: < 50ms end-to-end
- **Activity Generation**: < 10ms from event receipt
- **Timeline Queries**: < 100ms for 100 items
- **Event Processing**: < 5ms per ticket event
- **Memory Usage**: < 100MB baseline, < 300MB under load
- **Throughput**: 500 operations/minute per tenant

## Testing

### Unit Tests
```bash
make test
```

### API Testing
```bash
make test-api
```

### Event Simulation
```bash
make simulate-ticket-events
```

### Event Monitoring
```bash
make monitor-events
```

### Coverage Report
```bash
make coverage
```

## Production Considerations

### Data Persistence
- Replace in-memory storage with database (PostgreSQL/MongoDB)
- Implement proper indexing for queries
- Add data archiving for old activities
- Consider event store for audit trail

### Scalability
- Horizontal scaling with NATS queue groups
- Database read replicas for query performance
- Caching layer for frequently accessed timelines
- Event streaming for real-time updates

### Security
- Input validation and sanitization
- Rate limiting for comment creation
- Audit logging for all operations
- Content filtering and moderation

### Monitoring
- Comprehensive metrics collection
- Distributed tracing integration
- Real-time alerting on failures
- Performance dashboards

## Future Enhancements

### Comment Features
- Comment editing and deletion
- Comment threading and replies
- Rich text and markdown support
- File attachments and media
- Comment reactions and voting

### Activity Features
- Advanced filtering and search
- Custom activity types
- Activity aggregation and summaries
- Bulk operations tracking
- Performance analytics

### Integration Features
- Webhook notifications for external systems
- Integration with chat platforms (Slack, Teams)
- Email digest generation
- Mobile push notifications
- Real-time collaboration features

### Analytics Features
- Comment sentiment analysis
- User engagement metrics
- Activity pattern analysis
- Performance trending
- Automated insights generation
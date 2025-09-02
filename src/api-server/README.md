# NextGen NATS POC - API Server

A high-performance API gateway that consolidates ticket and notification service APIs, communicating with backend services via NATS messaging.

## 🚀 Features

- **Unified API Gateway** - Single entry point for all ticket and notification operations
- **NATS-based Communication** - Microservices communicate via NATS request-response pattern
- **Multi-tenant Support** - Tenant isolation through `X-Tenant-ID` header
- **Health Monitoring** - Built-in health check endpoint
- **Graceful Shutdown** - Proper cleanup on termination signals
- **CORS Support** - Cross-origin resource sharing enabled
- **Request Logging** - Structured logging with correlation IDs
- **Container Ready** - Docker support with multi-stage builds

## 📋 API Endpoints

### Ticket Management
- `POST /api/v1/tickets` - Create a new ticket
- `GET /api/v1/tickets` - List tickets for tenant
- `GET /api/v1/tickets/{id}` - Get specific ticket
- `PUT /api/v1/tickets/{id}` - Update ticket
- `DELETE /api/v1/tickets/{id}` - Delete ticket

### Notification Management
- `GET /api/v1/notifications` - List notifications for tenant
- `GET /api/v1/notifications/{id}` - Get specific notification

### Health Check
- `GET /health` - Service health status

## 🔧 Configuration

The service is configured via environment variables:

| Variable | Default | Description |
|----------|---------|-------------|
| `NATS_URL` | `nats://127.0.0.1:4222,nats://127.0.0.1:4223,nats://127.0.0.1:4224` | NATS cluster URLs (comma-separated) |
| `PORT` | `8082` | HTTP server port |
| `SERVICE_NAME` | `api-server` | Service identifier for NATS |
| `LOG_LEVEL` | `info` | Logging level |
| `X_TENANT_HEADER` | `X-Tenant-ID` | Tenant identification header |

## 🏃‍♂️ Quick Start

### Prerequisites
- Go 1.24+
- NATS server running
- Ticket and Notification services running

### Local Development

1. **Build and run:**
   ```bash
   make run
   ```

2. **Development mode with hot reload:**
   ```bash
   make dev  # requires 'entr' tool
   ```

3. **Run with custom configuration:**
   ```bash
   NATS_URL=nats://localhost:4222 PORT=8080 make run
   ```

### Docker

1. **Build Docker image:**
   ```bash
   make docker-build
   ```

2. **Run with Docker:**
   ```bash
   make docker-run
   ```

3. **Custom Docker run:**
   ```bash
   docker run --rm -p 8082:8082 \
     -e NATS_URL=nats://host.docker.internal:4222 \
     -e LOG_LEVEL=debug \
     nextgen-api-server:latest
   ```

## 🧪 Testing

### Create a Ticket
```bash
curl -X POST http://localhost:8082/api/v1/tickets \
  -H "Content-Type: application/json" \
  -H "X-Tenant-ID: tenant1" \
  -d '{
    "title": "Critical system outage",
    "description": "Production system is down - critical issue",
    "created_by": "john.doe@company.com"
  }'
```

### List Tickets
```bash
curl http://localhost:8082/api/v1/tickets \
  -H "X-Tenant-ID: tenant1"
```

### Get Notifications
```bash
curl http://localhost:8082/api/v1/notifications \
  -H "X-Tenant-ID: tenant1"
```

### Health Check
```bash
curl http://localhost:8082/health
```

## 📊 Monitoring

### Health Check Response
```json
{
  "status": "healthy",
  "nats": "connected"
}
```

### Request Logging
Each request generates a structured log entry:
```
method=POST path=/api/v1/tickets correlation_id=uuid-here duration=15ms tenant=tenant1
```

## 🏗 Architecture

```
┌─────────────┐    HTTP     ┌─────────────┐    NATS     ┌─────────────┐
│   Client    │ ────────── │ API Server  │ ─────────── │ Ticket Svc  │
│             │            │  (Port 8082)│             │             │
└─────────────┘            └─────────────┘    NATS     └─────────────┘
                                  │        ─────────── 
                                  │                    ┌─────────────┐
                                  └────────────────── │Notification │
                                                       │    Svc      │
                                                       └─────────────┘
```

### Request Flow
1. Client sends HTTP request to API Server
2. API Server validates tenant header and request
3. API Server sends NATS request to appropriate service
4. Backend service processes request and responds via NATS
5. API Server formats response and returns to client

### Error Handling
- **400 Bad Request** - Missing tenant header or invalid JSON
- **404 Not Found** - Resource not found
- **500 Internal Server Error** - Service communication failure
- **503 Service Unavailable** - NATS connection issues

## 🔒 Security

- **Tenant Isolation** - All API operations require `X-Tenant-ID` header
- **Input Validation** - Request data is validated before processing
- **CORS Protection** - Configurable cross-origin policies
- **Container Security** - Non-root user in Docker container

## 🛠 Development

### Available Commands
```bash
make help           # Show all available commands
make build          # Build binary
make test           # Run tests
make lint           # Run linter
make fmt            # Format code
make vet            # Run go vet
make docker-build   # Build Docker image
make coverage       # Generate coverage report
```

### Project Structure
```
src/api-server/
├── main.go          # Main application code
├── Dockerfile       # Multi-stage Docker build
├── Makefile         # Build and development tasks
├── README.md        # This file
└── bin/             # Build artifacts (created)
```

## 📝 Contributing

1. Follow Go conventions and best practices
2. Ensure all tests pass: `make test`
3. Run linter: `make lint`
4. Format code: `make fmt`
5. Update documentation as needed

## 🐛 Troubleshooting

### Common Issues

**NATS Connection Failed**
```
Failed to connect to NATS: no servers available
```
- Ensure NATS server is running
- Verify NATS_URL configuration
- Check network connectivity

**Service Unavailable**
```
ERROR: Failed to communicate with ticket service
```
- Ensure backend services are running
- Verify services are connected to NATS
- Check NATS subject subscriptions

**Missing Tenant Header**
```json
{"error": "missing_tenant_id", "message": "X-Tenant-ID header is required"}
```
- Include `X-Tenant-ID` header in all API requests
- Verify header name matches configuration

### Debug Mode
Run with debug logging:
```bash
LOG_LEVEL=debug make run
```

## 📈 Performance

- **Lightweight** - Minimal dependencies, small binary size
- **Concurrent** - Handles multiple requests simultaneously  
- **Efficient** - Direct NATS communication, minimal overhead
- **Scalable** - Stateless design, horizontal scaling ready

## 🔗 Related Services

- **Ticket Service** - Manages ticket CRUD operations via NATS
- **Notification Service** - Handles notifications for critical tickets
- **NATS Server** - Message broker for service communication
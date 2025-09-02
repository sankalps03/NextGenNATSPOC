# NextGen NATS POC - Log Ingestion Service

A high-performance, multi-protocol log ingestion microservice that collects logs from various sources and forwards them to the log parser service via NATS JetStream. This service supports TCP, UDP, and NATS agent ingestion with mutual TLS authentication for secure log collection.

## 🚀 Features

- **Multi-Protocol Ingestion** - TCP, UDP, and NATS agent
- **Mutual TLS Authentication** - Secure client authentication for TCP connections
- **Multi-tenant Architecture** - Tenant isolation using client certificates and headers
- **NATS JetStream Integration** - Reliable message publishing with persistence
- **Real-time Streaming** - Live log ingestion via TCP and UDP
- **Hot Reload Development** - Automatic restarts during development
- **Structured Logging** - JSON-based logging with request tracing
- **Certificate Management** - Automatic development certificate generation

## 🔧 Protocol Support

### TCP Stream Ingestion
- **Port**: 9090 (configurable)
- **Protocol**: TLS-encrypted TCP with mutual authentication
- **Format**: Line-delimited raw logs
- **Authentication**: Client certificate validation

### UDP Datagram Ingestion  
- **Port**: 9091 (configurable)
- **Protocol**: Plain UDP (no TLS possible)
- **Format**: `TENANT:tenant_id` header + line-delimited logs
- **Authentication**: Tenant ID in message header

### NATS Agent
- **Subjects**: `log.agent.{tenant_id}`
- **Protocol**: NATS queue subscription with load balancing
- **Format**: JSON messages with content and metadata
- **Authentication**: NATS connection-level security

## ⚙️ Configuration

The service is configured via environment variables:

| Variable | Default | Description |
|----------|---------|-------------|
| `NATS_URL` | `nats://127.0.0.1:4222,nats://127.0.0.1:4223,nats://127.0.0.1:4224` | NATS cluster URLs |
| `SERVICE_NAME` | `log-ingestion-service` | Service identifier |
| `TCP_PORT` | `9090` | TLS TCP server port |
| `UDP_PORT` | `9091` | UDP server port |
| `TLS_CERT_FILE` | `./certs/server.crt` | TLS server certificate |
| `TLS_KEY_FILE` | `./certs/server.key` | TLS server private key |
| `CA_CERT_FILE` | `./certs/ca.crt` | Certificate Authority cert |
| `LOG_LEVEL` | `info` | Logging level |

## Development

### Prerequisites

- Go 1.24+
- NATS Server with JetStream enabled
- OpenSSL (for certificate generation)
- Docker (optional)

### Setup

1. Clone the repository
2. Install dependencies:
   ```bash
   make deps
   ```

3. Generate development certificates:
   ```bash
   make gen-certs
   ```

4. Start NATS server with JetStream:
   ```bash
   # Single node
   nats-server -js
   
   # Or use the Docker cluster
   cd ../docker-cluster && make start
   ```

5. Run the service:
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

## Authentication & Security

### Mutual TLS Setup

The service uses mutual TLS for secure authentication:

1. **Server Certificate**: Identifies the log ingestion service
2. **Client Certificates**: Authenticate clients and extract tenant information
3. **CA Certificate**: Validates all certificates in the chain

### Tenant Extraction

Tenant IDs are extracted from client certificates using:
- **Common Name (CN)**: Format `tenant-{id}` or direct tenant ID
- **Organizational Unit (OU)**: Fallback tenant identifier
- **Subject Alternative Names**: Additional tenant information

### Certificate Generation

Development certificates are automatically generated:

```bash
make gen-certs
```

This creates:
- `certs/ca.crt` - Certificate Authority
- `certs/ca.key` - CA private key  
- `certs/server.crt` - Server certificate
- `certs/server.key` - Server private key
- `certs/client.crt` - Sample client certificate (tenant1)
- `certs/client.key` - Client private key

## Data Flow

### Log Entry Structure
```go
type LogEntry struct {
    ID        string            `json:"id"`
    TenantID  string            `json:"tenant_id"`
    Source    string            `json:"source"`        // tcp, udp, file_upload, nats_agent
    Content   string            `json:"content"`
    Metadata  map[string]string `json:"metadata"`
    Timestamp time.Time         `json:"timestamp"`
}
```

### NATS Publishing

Logs are published to NATS JetStream subjects:
- `log.raw` - Individual log entries
- `log.batch` - Batched log entries (from file uploads)
- `log.agent` - Logs from NATS agents

### Event Flow
1. **Ingestion**: Receive logs via TCP/UDP/HTTP/NATS
2. **Authentication**: Validate client certificates or tenant headers
3. **Processing**: Parse and structure log data
4. **Publishing**: Send to NATS JetStream for parser service
5. **Acknowledgment**: Confirm successful ingestion

## Usage Examples

### TCP Stream
```bash
# Stream logs via TLS TCP
echo "Sample log entry" | openssl s_client \
  -connect localhost:9090 \
  -cert certs/client.crt \
  -key certs/client.key \
  -CAfile certs/ca.crt \
  -quiet
```

### UDP Messages
```bash
# Send UDP log with tenant header
echo -e "TENANT:tenant1\nUDP log message\nAnother log line" | \
  nc -u localhost 9091
```

### NATS Agent
```bash
# Publish via NATS client
nats pub log.agent.tenant1 \
  '{"content":"Agent log message","metadata":{"host":"server1"}}'
```

## Monitoring

### Structured Logging

The service provides JSON-structured logs with:
- Request tracing with tenant context
- Performance metrics and latency
- Error details with full context
- NATS connectivity status
- Client certificate information

### Metrics

Key performance indicators:
- Logs ingested per second
- Processing latency per protocol
- NATS publish success rate
- Client authentication failures
- Memory and CPU usage

## Architecture

### Component Structure
```
log-ingestion-svc/
├── main.go              # Application entry point with full implementation
├── go.mod               # Go module definition (shared with project root)
├── go.sum               # Go module checksums (shared with project root)
├── Makefile            # Build and development tasks
├── Dockerfile          # Multi-stage container build
├── README.md           # This documentation
└── certs/              # TLS certificates (generated by make gen-certs)
    ├── ca.crt          # Certificate Authority
    ├── ca.key          # CA private key
    ├── server.crt      # Server certificate
    ├── server.key      # Server private key
    ├── client.crt      # Sample client certificate
    └── client.key      # Client private key
```

### Service Integration

The log ingestion service integrates with the NextGen NATS POC by:
1. Publishing to the same NATS JetStream cluster
2. Using the LOG_EVENTS stream for all log data
3. Following consistent multi-tenant patterns
4. Providing structured data for downstream processing
5. Maintaining high availability with NATS clustering

### Error Handling

- **TLS Failures**: Graceful handling of certificate errors
- **NATS Failures**: Automatic reconnection and retry logic
- **Parsing Errors**: Invalid data logged but doesn't crash service
- **Resource Limits**: File size and connection limits enforced
- **Graceful Shutdown**: Clean shutdown of all protocol handlers

## Performance Targets (POC)

- **TCP/UDP Ingestion**: < 10ms per log entry
- **NATS Publishing**: < 5ms publish latency
- **Throughput**: 10,000 log entries/minute per tenant
- **Memory Usage**: < 100MB baseline, < 300MB under load
- **Connection Handling**: 1000 concurrent TCP connections

## Testing

### Unit Tests
```bash
make test
```

### Endpoint Testing
```bash
make test-endpoints
```

### Load Testing
```bash
make bench
```

### Security Scanning
```bash
make security
```

## Production Considerations

### Certificate Management
- Use proper CA-signed certificates in production
- Implement certificate rotation
- Monitor certificate expiration
- Use hardware security modules (HSM) for CA keys

### Scalability
- Deploy multiple instances behind load balancer
- Use NATS queue groups for horizontal scaling
- Implement connection pooling and limits
- Monitor resource usage and auto-scaling triggers

### Security
- Regular security audits and penetration testing
- Network isolation and firewall rules
- Log sanitization to prevent injection attacks
- Rate limiting and DDoS protection

### Monitoring
- Comprehensive metrics collection (Prometheus/Grafana)
- Distributed tracing (Jaeger/Zipkin)
- Centralized logging and alerting
- Health checks and uptime monitoring

## Future Enhancements

- Database persistence for ingestion tracking
- Advanced log filtering and routing
- WebSocket real-time streaming
- Advanced load balancing algorithms
- Machine learning-based anomaly detection
- Integration with cloud storage systems
- Advanced authentication methods (OAuth, LDAP)
- Compression support for log streams
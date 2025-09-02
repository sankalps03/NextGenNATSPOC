# NextGen NATS POC - Log Parser Service

A high-performance, intelligent log parsing microservice that processes raw logs from the ingestion service and transforms them into structured, searchable data. The service supports multiple log formats including JSON, Syslog, Apache, Nginx, and custom regex patterns.

## 🚀 Features

- **Multi-Format Parsing** - JSON, Syslog, Apache, Nginx, and custom patterns
- **Intelligent Parser Selection** - Automatic format detection and parser matching
- **Structured Output** - Consistent parsed log format with extracted fields
- **NATS JetStream Integration** - Reliable message processing with durability
- **Field Extraction** - Timestamps, log levels, messages, and custom fields
- **Error Handling** - Failed parsing stored as-is, no data loss
- **Multi-tenant Support** - Tenant-isolated log processing
- **Real-time Processing** - Low-latency log transformation
- **Batch Processing** - Efficient handling of log batches
- **Extensible Parsers** - Easy addition of new log format parsers

## 🔧 NATS Integration

### Subscribed Subjects
The service processes logs from:
- `log.raw` - Individual raw log entries from ingestion service
- `log.batch` - Batched raw log entries from file uploads

### Published Subjects
The service publishes parsed logs to:
- `log.parsed` - Successfully parsed and structured logs
- `log.failed` - Logs that couldn't be parsed (stored as-is)
- `log.structured` - Batched structured logs

### Stream Configuration
Uses the `PARSED_LOGS` JetStream stream with:
- File storage for durability
- 48-hour retention period
- 2M message limit

## ⚙️ Configuration

The service is configured via environment variables:

| Variable | Default | Description |
|----------|---------|-------------|
| `NATS_URL` | `nats://127.0.0.1:4222,nats://127.0.0.1:4223,nats://127.0.0.1:4224` | NATS cluster URLs |
| `SERVICE_NAME` | `log-parser-service` | Service identifier |
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

## Supported Log Formats

### JSON Logs
**Format**: Structured JSON objects
**Example**:
```json
{"timestamp":"2024-01-01T12:00:00Z","level":"INFO","message":"User login successful","user_id":"123"}
```
**Extracted Fields**: All JSON keys as fields, special handling for timestamp, level, message

### Syslog (RFC3164)
**Format**: `<priority>timestamp hostname process[pid]: message`
**Example**:
```
<134>Jan 1 12:00:00 server1 nginx[1234]: GET /api/status HTTP/1.1
```
**Extracted Fields**: priority, facility, severity, timestamp, hostname, process, pid, message

### Standard Timestamp-Level-Message
**Format**: `timestamp [level] message`
**Example**:
```
2024-01-01 12:00:00 [INFO] Application started successfully
```
**Extracted Fields**: timestamp, level, message

### Apache Combined Log
**Format**: Common Log Format with referrer and user agent
**Example**:
```
127.0.0.1 - - [01/Jan/2024:12:00:00 +0000] "GET /index.html HTTP/1.1" 200 1234 "http://example.com" "Mozilla/5.0"
```
**Extracted Fields**: ip, timestamp, method, path, protocol, status, size, referer, user_agent

### Nginx Access Log
**Format**: Similar to Apache with nginx-specific formatting
**Example**:
```
127.0.0.1 - - [01/Jan/2024:12:00:00 +0000] "GET /api/v1/status HTTP/1.1" 200 512 "-" "curl/7.68.0"
```
**Extracted Fields**: ip, timestamp, method, path, protocol, status, size, referer, user_agent

### Custom Regex Parsers
**Format**: Extensible regex-based parsing for custom log formats
**Configuration**: Define patterns, field names, and extraction rules

## Data Structures

### Input Log Entry
```go
type LogEntry struct {
    ID        string            `json:"id"`
    TenantID  string            `json:"tenant_id"`
    Source    string            `json:"source"`
    Content   string            `json:"content"`
    Metadata  map[string]string `json:"metadata"`
    Timestamp time.Time         `json:"timestamp"`
}
```

### Output Parsed Log
```go
type ParsedLog struct {
    ID           string            `json:"id"`
    OriginalID   string            `json:"original_id"`
    TenantID     string            `json:"tenant_id"`
    Source       string            `json:"source"`
    ParsedFields map[string]string `json:"parsed_fields"`
    ParsedTime   time.Time         `json:"parsed_time"`
    LogLevel     string            `json:"log_level"`
    Message      string            `json:"message"`
    RawContent   string            `json:"raw_content"`
    ParserUsed   string            `json:"parser_used"`
    ParseStatus  string            `json:"parse_status"` // success, partial, failed
    Metadata     map[string]string `json:"metadata"`
    Timestamp    time.Time         `json:"timestamp"`
}
```

## Parser Architecture

### Parser Interface
```go
type LogParser interface {
    Name() string
    Parse(content string) (*ParsedLog, error)
    CanParse(content string) bool
}
```

### Parser Selection Logic
1. **Format Detection**: Each parser checks if it can handle the log format
2. **Priority Order**: Parsers are tried in order of specificity (JSON → Syslog → Custom)
3. **First Match Wins**: First parser that successfully parses is used
4. **Fallback**: If no parser matches, log is stored as failed with original content

### Timestamp Parsing
Supports multiple timestamp formats:
- RFC3339: `2006-01-02T15:04:05Z07:00`
- RFC3339Nano: `2006-01-02T15:04:05.999999999Z07:00`
- ISO 8601: `2006-01-02T15:04:05`
- Standard: `2006-01-02 15:04:05`
- Syslog: `Jan 2 15:04:05`
- Apache: `02/Jan/2006:15:04:05 -0700`

### Log Level Extraction
Standardizes log levels to:
- `DEBUG`, `INFO`, `WARN`, `ERROR`, `FATAL`
- Maps from various formats (syslog severities, HTTP status codes, etc.)

## Usage Examples

### Testing Individual Parsers
```bash
# Create test log files
make test-parsers

# Publish test logs to NATS
make test-nats-publish

# Monitor parsed output
make monitor-parsed
```

### Manual Testing with NATS CLI
```bash
# Test JSON log parsing
nats pub log.raw '{
  "id": "test1",
  "tenant_id": "tenant1", 
  "source": "test",
  "content": "{\"level\":\"INFO\",\"message\":\"Test message\"}",
  "metadata": {},
  "timestamp": "2024-01-01T12:00:00Z"
}'

# Test syslog parsing
nats pub log.raw '{
  "id": "test2",
  "tenant_id": "tenant1",
  "source": "test", 
  "content": "<134>Jan 1 12:00:00 server1 app[1234]: Test syslog message",
  "metadata": {},
  "timestamp": "2024-01-01T12:00:00Z"
}'
```

### Monitoring Parsed Logs
```bash
# Subscribe to parsed logs
nats sub log.parsed

# Subscribe to failed parsing attempts
nats sub log.failed

# Subscribe to structured batches
nats sub log.structured
```

## Integration with Log Ingestion Service

The log parser service seamlessly integrates with the log ingestion service:

1. **Automatic Processing**: Subscribes to ingestion service output subjects
2. **Preservation**: Maintains all original metadata and tenant information
3. **Reliability**: Uses JetStream durability and acknowledgments
4. **Performance**: Processes logs in real-time with low latency
5. **Scalability**: Can run multiple instances with NATS queue groups

### Data Flow
```
Log Ingestion Service → NATS JetStream → Log Parser Service → NATS JetStream → Downstream Services
```

## Monitoring

### Service Health
- NATS connectivity status
- Last processed message timestamp
- Parser success/failure rates
- Processing latency metrics

### Structured Logging
```json
{
  "timestamp": "2024-01-01T12:00:00Z",
  "level": "INFO",
  "service": "log-parser-service",
  "tenant_id": "tenant1",
  "parser_used": "json",
  "parse_status": "success",
  "processing_time_ms": 5,
  "original_log_id": "abc123"
}
```

### Key Metrics
- Logs parsed per second
- Parser distribution (which parsers are used most)
- Parsing success rate
- Processing latency (p50, p95, p99)
- Memory usage and garbage collection
- Failed parsing reasons

## Architecture

### Component Structure
```
log-parser-svc/
├── main.go              # Application entry point with full implementation
├── go.mod               # Go module definition (shared with project root)
├── go.sum               # Go module checksums (shared with project root)
├── Makefile            # Build and development tasks
├── Dockerfile          # Multi-stage container build
└── README.md           # This documentation
```

### Parser Implementations
- **JSONLogParser**: Handles structured JSON logs
- **SyslogParser**: RFC3164 syslog format parsing
- **CommonLogParser**: Configurable regex-based parsing
- **NginxLogParser**: Nginx access log format
- **ApacheLogParser**: Apache combined log format
- **CustomRegexParser**: User-defined regex patterns

### Error Handling
- **Parsing Failures**: Logs stored as-is with failed status
- **NATS Failures**: Automatic retry and reconnection
- **Memory Limits**: Graceful handling of large log entries
- **Malformed Data**: Robust error handling without service crashes

## Performance Targets (POC)

- **Processing Latency**: p50 < 5ms, p95 < 20ms per log entry
- **Throughput**: 5,000 log entries/minute per instance
- **Memory Usage**: < 100MB baseline, < 300MB under load
- **Parsing Success Rate**: > 95% for common log formats
- **NATS Acknowledgment**: < 2ms for successful processing

## Testing

### Unit Tests
```bash
make test
```

### Parser Testing
```bash
make test-parsers
```

### Integration Testing
```bash
make test-nats-publish
```

### Benchmark Testing
```bash
make bench
```

### Coverage Report
```bash
make coverage
```

## Production Considerations

### Performance Optimization
- Connection pooling and reuse
- Batch processing for high throughput
- Memory pool for log objects
- Compiled regex caching
- Parallel processing with worker pools

### Scalability
- Horizontal scaling with multiple instances
- NATS queue groups for load distribution
- Auto-scaling based on message queue depth
- Resource monitoring and alerting

### Reliability
- Circuit breakers for downstream failures
- Dead letter queues for problematic logs
- Health checks and graceful degradation
- Backup parsing strategies

### Monitoring & Alerting
- Comprehensive metrics collection
- Distributed tracing integration
- Real-time alerting on failures
- Performance dashboards

## Future Enhancements

- Machine learning-based log classification
- Dynamic parser configuration and hot-reload
- Advanced field extraction with NLP
- Log correlation and event detection
- Integration with search engines (Elasticsearch)
- Custom parsing rule management API
- Performance optimization with caching layers
- Advanced error recovery and retry logic
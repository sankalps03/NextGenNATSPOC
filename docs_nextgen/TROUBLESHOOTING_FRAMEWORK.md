# Troubleshooting Framework for Go + NATS Microservices

This document provides systematic approaches to diagnose, investigate, and resolve common issues in Go + NATS microservice environments.

## Diagnostic Methodology

### Issue Classification Framework

#### Service-Level Issues
- **Application Crashes**: Service stops unexpectedly
- **Memory Leaks**: Gradual memory consumption increase
- **Performance Degradation**: Response times increase over time
- **Resource Exhaustion**: CPU/memory/connection limits reached
- **Logic Errors**: Incorrect business behavior

#### Communication Issues  
- **Message Loss**: Messages not reaching consumers
- **Message Duplication**: Same message processed multiple times
- **Connection Failures**: Unable to connect to NATS
- **Timeout Issues**: Operations taking too long to complete
- **Serialization Errors**: Message format or encoding problems

#### Infrastructure Issues
- **Network Connectivity**: Service-to-service communication failures
- **Database Problems**: Connection or query performance issues
- **External Service Failures**: Third-party API or service unavailability
- **Configuration Issues**: Incorrect environment or service configuration
- **Deployment Problems**: Issues during service deployment or updates

### Investigation Process

#### Phase 1: Immediate Assessment
1. **Check Service Status**: Is the service running and responding to health checks?
2. **Review Recent Changes**: What deployments, configurations, or code changes occurred recently?
3. **Identify Impact Scope**: Which users, tenants, or operations are affected?
4. **Check Dependencies**: Are external services and infrastructure components healthy?
5. **Review Error Rates**: What's the current error rate compared to baseline?

#### Phase 2: Data Collection
1. **Gather Logs**: Collect relevant log entries with proper correlation IDs
2. **Check Metrics**: Review performance metrics and resource utilization
3. **Analyze Traces**: Examine distributed traces for request flow analysis
4. **Review Alerts**: Check what monitoring alerts have been triggered
5. **Collect System Info**: CPU, memory, network, and disk utilization

#### Phase 3: Root Cause Analysis
1. **Timeline Construction**: Build timeline of events leading to the issue
2. **Correlation Analysis**: Correlate different data sources to identify patterns
3. **Hypothesis Formation**: Develop theories about potential root causes
4. **Testing Hypotheses**: Use targeted tests or experiments to validate theories
5. **Root Cause Identification**: Confirm the underlying cause of the issue

## Common Issue Patterns

### NATS Connection Issues

#### Symptoms
- Connection timeouts or failures
- Messages not being delivered
- High connection retry rates
- Service unable to publish or subscribe

#### Diagnostic Steps
```bash
# Check NATS server status
nats server check

# Test connectivity from service host
nats server ping

# Check connection configuration
nats server info

# Monitor connection metrics
nats server report connections
```

#### Investigation Areas
- **Network Connectivity**: Can service reach NATS server on specified port?
- **Authentication**: Are credentials correct and not expired?
- **Resource Limits**: Has NATS server hit connection or resource limits?
- **Configuration**: Is connection string and configuration correct?
- **Firewall/Security**: Are network security rules blocking connections?

#### Common Solutions
- Verify NATS server is running and accessible
- Check and update authentication credentials
- Review and adjust connection pool settings
- Implement connection retry with exponential backoff
- Monitor and scale NATS server resources if needed

### Message Processing Issues

#### Symptoms  
- Messages stuck in queues
- High processing latency
- Message processing errors
- Consumer lag increasing

#### Diagnostic Steps
```go
// Check queue depth and consumer status
stats, err := js.StreamInfo("STREAM_NAME")
if err != nil {
    log.Printf("Stream info error: %v", err)
}

// Monitor consumer status
consumer, err := js.ConsumerInfo("STREAM_NAME", "CONSUMER_NAME")
if err != nil {
    log.Printf("Consumer info error: %v", err)
}
```

#### Investigation Areas
- **Consumer Performance**: Are message handlers taking too long to process?
- **Resource Contention**: Is CPU, memory, or I/O becoming a bottleneck?
- **Error Handling**: Are failed messages being properly handled or causing blocks?
- **Concurrency**: Are too many or too few goroutines processing messages?
- **Downstream Dependencies**: Are external services causing processing delays?

#### Common Solutions
- Increase consumer concurrency with more goroutines
- Implement batch processing for improved throughput
- Add circuit breakers for external service calls
- Optimize message handlers for better performance
- Implement dead letter queues for failed message handling

### Memory and Resource Issues

#### Symptoms
- Out of memory errors
- High memory utilization
- Goroutine leaks
- File descriptor exhaustion
- CPU usage spikes

#### Diagnostic Steps
```bash
# Check current resource usage
top -p $(pgrep service-name)

# Generate memory profile
curl http://localhost:6060/debug/pprof/heap > heap.prof
go tool pprof heap.prof

# Generate CPU profile  
curl http://localhost:6060/debug/pprof/profile?seconds=30 > cpu.prof
go tool pprof cpu.prof

# Check goroutine count
curl http://localhost:6060/debug/pprof/goroutine?debug=1
```

#### Investigation Areas
- **Memory Leaks**: Are objects being properly garbage collected?
- **Goroutine Leaks**: Are goroutines being properly terminated?
- **Resource Management**: Are connections, files, and other resources being closed?
- **Large Objects**: Are large objects accumulating in memory?
- **GC Pressure**: Is garbage collection frequency appropriate?

#### Common Solutions
- Review code for proper resource cleanup using defer statements
- Implement context cancellation for goroutine lifecycle management
- Add resource pooling for frequently allocated objects
- Tune garbage collection settings for workload characteristics
- Implement memory monitoring and alerting

### Performance Degradation

#### Symptoms
- Increasing response times
- Higher CPU or memory usage
- Degraded throughput
- User-reported slowness

#### Diagnostic Steps
```go
// Add request timing middleware
func TimingMiddleware(next http.Handler) http.Handler {
    return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
        start := time.Now()
        next.ServeHTTP(w, r)
        duration := time.Since(start)
        
        log.Printf("Request %s %s took %v", r.Method, r.URL.Path, duration)
        
        // Record metrics
        RequestDuration.WithLabelValues(r.Method, r.URL.Path).Observe(duration.Seconds())
    })
}
```

#### Investigation Areas
- **Database Queries**: Are queries optimized with proper indexes?
- **External Services**: Are third-party APIs responding slowly?
- **Caching**: Is caching being used effectively?
- **Algorithm Efficiency**: Are algorithms optimal for current data sizes?
- **Concurrency**: Is parallel processing being used where appropriate?

#### Common Solutions
- Profile application to identify bottlenecks
- Optimize database queries and add appropriate indexes
- Implement caching for frequently accessed data
- Add connection pooling for external service calls
- Review algorithms for efficiency improvements

## Diagnostic Tools and Techniques

### Built-in Go Tools

#### pprof Profiling
```bash
# CPU profiling
go tool pprof http://localhost:6060/debug/pprof/profile?seconds=30

# Memory profiling
go tool pprof http://localhost:6060/debug/pprof/heap

# Goroutine analysis
go tool pprof http://localhost:6060/debug/pprof/goroutine

# Mutex contention
go tool pprof http://localhost:6060/debug/pprof/mutex
```

#### Race Detection
```bash
# Run with race detector
go run -race cmd/server/main.go

# Test with race detection
go test -race ./...
```

### NATS Diagnostic Commands

#### Server Monitoring
```bash
# Server information
nats server info

# Connection monitoring
nats server report connections

# JetStream monitoring
nats server report jetstream

# Stream information
nats stream info STREAM_NAME

# Consumer monitoring
nats consumer report STREAM_NAME
```

#### Message Tracing
```bash
# Subscribe to all subjects for debugging
nats sub ">"

# Monitor specific message flow
nats sub "service.events.*"

# Check message in stream
nats stream view STREAM_NAME
```

### Logging and Monitoring

#### Structured Logging Format
```go
type LogEntry struct {
    Level       string    `json:"level"`
    Timestamp   time.Time `json:"timestamp"`
    Service     string    `json:"service"`
    Version     string    `json:"version"`
    RequestID   string    `json:"request_id"`
    Message     string    `json:"message"`
    Error       string    `json:"error,omitempty"`
    Duration    string    `json:"duration,omitempty"`
    Component   string    `json:"component"`
    Operation   string    `json:"operation"`
}
```

#### Key Metrics to Monitor
- **Request Rate**: Requests per second
- **Response Time**: p50, p95, p99 latencies  
- **Error Rate**: Percentage of failed requests
- **Memory Usage**: Heap size and GC frequency
- **CPU Usage**: CPU utilization percentage
- **Goroutine Count**: Number of active goroutines
- **Connection Count**: Active NATS connections
- **Message Throughput**: Messages processed per second

### Testing and Reproduction

#### Load Testing
```bash
# HTTP load testing with hey
hey -n 10000 -c 50 http://localhost:8080/api/tickets

# NATS message load testing
for i in {1..1000}; do
    nats pub "test.subject" "test message $i"
done
```

#### Chaos Testing
```go
// Introduce artificial delays
func chaosDelay() {
    if rand.Float64() < 0.1 { // 10% chance
        time.Sleep(time.Duration(rand.Intn(1000)) * time.Millisecond)
    }
}

// Simulate connection failures
func chaosConnectionFailure() error {
    if rand.Float64() < 0.05 { // 5% chance
        return errors.New("simulated connection failure")
    }
    return nil
}
```

## Resolution Strategies

### Immediate Mitigation
- **Scale Horizontally**: Add more service instances
- **Circuit Breaker**: Protect against cascading failures
- **Rate Limiting**: Reduce load on struggling services
- **Fallback Responses**: Provide degraded functionality
- **Traffic Routing**: Route traffic away from problematic instances

### Short-term Fixes
- **Configuration Updates**: Adjust timeouts, pool sizes, or other parameters
- **Hot Fixes**: Deploy minimal code changes to address critical issues  
- **Resource Scaling**: Increase CPU, memory, or connection limits
- **Cache Warming**: Pre-populate caches to improve performance
- **Database Optimization**: Add indexes or optimize queries

### Long-term Solutions
- **Code Refactoring**: Improve algorithms or architecture
- **Infrastructure Improvements**: Upgrade hardware or infrastructure
- **Monitoring Enhancements**: Add better observability and alerting
- **Testing Improvements**: Add chaos testing and load testing
- **Documentation Updates**: Update runbooks and troubleshooting guides

## Prevention Strategies

### Proactive Monitoring
- Set up comprehensive alerts for key metrics
- Implement health checks with appropriate timeouts
- Use distributed tracing for request flow visibility
- Monitor resource utilization trends over time
- Track business metrics alongside technical metrics

### Code Quality Practices
- Regular code reviews with focus on resource management
- Static analysis tools to catch common issues
- Integration tests with realistic load and failure scenarios
- Performance testing as part of CI/CD pipeline
- Regular dependency updates and security scanning

### Operational Excellence
- Regular disaster recovery exercises
- Capacity planning based on growth projections  
- Automated deployment with rollback capabilities
- Configuration management with validation
- Documentation maintenance and knowledge sharing

This framework provides a systematic approach to troubleshooting that can be applied across all Go + NATS microservices while allowing for service-specific customization.
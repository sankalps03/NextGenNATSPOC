# Go + NATS Technical Foundation for Claude Code

This file provides purely technical guidance for Claude Code when implementing Go + NATS patterns. This focuses on infrastructure, performance, and technical architecture decisions only.

## Technical Architecture Philosophy

### Go + NATS Technical Principles
- **High throughput message processing** with minimal memory allocations
- **Connection lifecycle management** with automatic reconnection and health monitoring
- **Concurrent message processing** using goroutine pools and worker patterns
- **Message serialization optimization** for performance-critical paths
- **Resource management** with proper cleanup and graceful shutdown
- **Network resilience** through retry logic and circuit breakers

### Technology Stack Technical Decisions
- **Core NATS**: Sub-millisecond pub-sub, request-reply with fire-and-forget semantics
- **JetStream**: Persistent messaging with acknowledgments, exactly-once delivery
- **Go 1.24+**: Generics, improved GC, structured concurrency primitives
- **Connection pooling**: Multiple connections for throughput optimization
- **Memory pools**: Object reuse for high-frequency allocations
- **Async processing**: Non-blocking message publishing and consumption

---

## Technical Implementation Principles (DO)

### Connection Architecture
- **Connection Pooling**: Maintain multiple connections for different traffic patterns
- **Health Monitoring**: Implement connection health checks with automatic failover
- **Graceful Degradation**: Handle connection failures without dropping messages
- **Resource Isolation**: Separate connection pools for different message types
- **Backpressure Handling**: Implement flow control mechanisms
- **Connection Lifecycle**: Proper startup/shutdown sequence management

### Message Processing Architecture
- **Worker Pool Pattern**: Fixed-size goroutine pools for message processing
- **Queue Management**: Bounded channels to prevent memory exhaustion
- **Batch Processing**: Group operations for improved throughput
- **Memory Management**: Object pooling for frequent allocations/deallocations
- **Async Processing**: Non-blocking message handlers with proper error handling
- **Timeout Management**: Configure realistic timeouts for all operations

### NATS Protocol Optimization
- **Subject Namespace Design**: Hierarchical subject patterns for routing efficiency
- **Message Size Optimization**: Keep payloads minimal, use message compression
- **Queue Group Strategy**: Distribute load across consumer instances
- **JetStream Consumer Configuration**: Durable vs ephemeral based on use case
- **Acknowledgment Patterns**: Manual ack for reliability, auto-ack for performance
- **Stream Retention Policies**: Configure based on storage and replay requirements

### Go-Specific Optimizations
- **Context Propagation**: Proper context.Context usage for cancellation
- **Error Wrapping**: Use fmt.Errorf with %w verb for error chains
- **Channel Patterns**: Buffered vs unbuffered based on communication needs
- **Interface Design**: Small interfaces for testability and modularity
- **Struct Embedding**: Composition over inheritance for extensibility
- **Generics Usage**: Type-safe collections and algorithms where appropriate

---

## Development Anti-Patterns (DON'T)

### Service Design Anti-Patterns
- **Distributed Monolith**: Services that are tightly coupled through synchronous calls
- **Chatty Services**: Multiple round-trips for single business operations
- **God Services**: Services handling multiple unrelated domains
- **Temporal Coupling**: Services that must be deployed in specific order
- **Shared Database**: Multiple services accessing same data store
- **Cascade Failures**: Not implementing circuit breakers for external calls

### Message Design Anti-Patterns  
- **Large Payloads**: Sending massive objects through message bus
- **Synchronous Mindset**: Using request-reply for everything
- **Message Coupling**: Messages containing implementation details
- **Version Breaking**: Changing message structure without versioning
- **Lost Messages**: Not handling acknowledgments properly
- **Infinite Retries**: No max retry limits or dead letter handling

### Implementation Anti-Patterns
- **Global State**: Using package-level variables for connections
- **Blocking Operations**: Long-running operations in message handlers
- **Resource Leaks**: Not closing connections, goroutines properly
- **Silent Failures**: Swallowing errors without logging
- **Hard-coded Configuration**: Not using environment-based config
- **Missing Observability**: No metrics, tracing, or structured logging

---

## AI Workflow for Microservice Development

### Phase 1: Requirements Analysis
1. **Domain Identification**: Identify bounded context and service boundaries
2. **Message Flow Design**: Map events, commands, and queries between services  
3. **Data Ownership**: Determine which service owns which data entities
4. **Integration Patterns**: Choose between event-driven, request-reply, or hybrid
5. **Consistency Requirements**: Identify strong vs eventual consistency needs
6. **Performance Targets**: Define throughput, latency, and availability requirements

### Phase 2: Architecture Planning
1. **Service Topology**: Design service communication patterns
2. **Message Schema Design**: Define event and command structures
3. **Error Handling Strategy**: Plan for failure scenarios and recovery
4. **Scalability Patterns**: Identify bottlenecks and scaling strategies
5. **Security Model**: Authentication, authorization, and message encryption
6. **Observability Strategy**: Logging, metrics, tracing requirements

### Phase 3: Implementation Strategy
1. **Start with Domain Models**: Define core business entities first
2. **Implement Message Contracts**: Create message types with validation
3. **Build Core Business Logic**: Pure functions without infrastructure concerns
4. **Add NATS Integration**: Publishers, subscribers, connection management
5. **Implement Error Handling**: Circuit breakers, retries, dead letter queues
6. **Add Observability**: Structured logging, metrics, health checks

### Phase 4: Testing Strategy
1. **Unit Testing**: Business logic without external dependencies
2. **Integration Testing**: NATS message flows with test containers
3. **Contract Testing**: Message schema compatibility between services
4. **Load Testing**: Performance under expected and peak loads
5. **Chaos Testing**: Failure injection and recovery validation
6. **End-to-End Testing**: Full business workflow validation

### Phase 5: Production Readiness
1. **Configuration Management**: Environment-specific settings
2. **Deployment Automation**: Container images and deployment scripts
3. **Monitoring Setup**: Dashboards, alerts, and SLA monitoring
4. **Documentation**: Service APIs, message schemas, runbooks
5. **Security Hardening**: TLS, authentication, authorization
6. **Performance Optimization**: Profiling and bottleneck resolution

---

## Message Pattern Decision Matrix

### Use Core NATS When:
- **Low Latency Required**: Sub-millisecond response times needed
- **Request-Reply Pattern**: Synchronous-style communication preferred  
- **Ephemeral Messages**: Messages don't need persistence
- **High Frequency, Low Value**: Metrics, heartbeats, status updates
- **Load Balancing**: Simple round-robin message distribution

### Use JetStream When:
- **Guaranteed Delivery**: Messages cannot be lost
- **Message Persistence**: Messages need to survive service restarts
- **Event Sourcing**: Building event-driven architectures
- **Complex Routing**: Advanced subject-based routing needed
- **Replay Capability**: Consumers need to replay message history

### Use Queue Groups When:
- **Load Distribution**: Multiple instances processing messages
- **Horizontal Scaling**: Auto-scaling based on queue depth
- **Work Distribution**: Parallel processing of independent tasks
- **Fault Tolerance**: Service instance failures shouldn't lose messages

---

## Configuration Management Principles

### Environment Variables
- **NATS_URL**: Connection string for NATS cluster
- **SERVICE_NAME**: Used for client identification and logging
- **LOG_LEVEL**: Configurable logging verbosity
- **GRACEFUL_SHUTDOWN_TIMEOUT**: Time to wait for clean shutdown
- **MAX_RECONNECT_ATTEMPTS**: Connection retry configuration
- **JETSTREAM_ENABLED**: Feature flag for JetStream usage

### Configuration Validation
- **Startup Validation**: Fail fast if required config missing
- **Type Safety**: Use struct tags for validation
- **Default Values**: Sensible defaults for optional configuration
- **Hot Reload**: Support runtime configuration updates where safe
- **Secret Management**: Separate secrets from regular configuration

---

## Error Handling Strategy

### Error Classification
- **Transient Errors**: Network issues, temporary unavailability - retry
- **Permanent Errors**: Invalid messages, authorization failures - don't retry
- **Resource Exhaustion**: Memory, connections - apply backpressure
- **Downstream Failures**: External service issues - circuit break

### Recovery Patterns
- **Exponential Backoff**: For transient failures with jitter
- **Circuit Breaker**: For downstream service protection  
- **Dead Letter Queue**: For messages that cannot be processed
- **Compensating Actions**: For distributed transaction failures
- **Graceful Degradation**: Reduced functionality over complete failure

---

## Testing Philosophy

### Test Pyramid Structure
- **Unit Tests (70%)**: Pure business logic, message parsing, validation
- **Integration Tests (20%)**: NATS message flows, database interactions
- **End-to-End Tests (10%)**: Complete business workflows

### Testing Strategies
- **Test Containers**: Use Docker containers for integration tests
- **Message Fixtures**: Predefined messages for consistent testing
- **Mock External Dependencies**: Focus tests on service under test
- **Property-Based Testing**: Generate random test data for edge cases
- **Performance Testing**: Validate under load conditions
- **Chaos Engineering**: Test failure scenarios systematically

---

## Performance Optimization Guidelines

### Message Throughput
- **Batch Operations**: Group database operations where possible
- **Connection Pooling**: Reuse connections for high throughput
- **Async Publishing**: Use async publishers for fire-and-forget messages
- **Message Size**: Keep messages small, reference large data by ID
- **Compression**: Use message compression for large payloads

### Memory Management  
- **Object Pooling**: Reuse frequently allocated objects
- **Garbage Collection Tuning**: Optimize GC for message processing patterns
- **Memory Profiling**: Regular profiling to identify leaks
- **Bounded Buffers**: Limit memory usage under load
- **Resource Monitoring**: Track memory, CPU, and connection usage

### Latency Optimization
- **Minimize Allocations**: Reduce memory allocations in hot paths
- **Efficient Serialization**: Choose fast serialization formats
- **Local Caching**: Cache frequently accessed data
- **Database Connection Pooling**: Reuse database connections
- **CPU Profiling**: Identify and optimize CPU hotspots

---

## Security Best Practices

### Message Security
- **Message Encryption**: Encrypt sensitive message payloads
- **Schema Validation**: Validate all incoming messages
- **Input Sanitization**: Clean user inputs before processing
- **Authentication**: Verify message source identity
- **Authorization**: Check permissions for message operations

### Network Security  
- **TLS Encryption**: Use TLS for all NATS connections
- **Certificate Management**: Proper certificate lifecycle management
- **Network Segmentation**: Isolate NATS traffic appropriately
- **Firewall Rules**: Restrict NATS port access
- **VPN/Private Networks**: Use private networks for service communication

### Operational Security
- **Secrets Management**: Use vault solutions for sensitive data
- **Audit Logging**: Log security-relevant events
- **Regular Updates**: Keep dependencies updated
- **Vulnerability Scanning**: Regular security assessments
- **Incident Response**: Plan for security incident handling

---

## Observability Requirements

### Structured Logging
- **Correlation IDs**: Track requests across service boundaries
- **Contextual Information**: Include service, version, environment
- **Log Levels**: Appropriate levels for different event types
- **Performance Logging**: Track operation durations
- **Error Context**: Rich error information for debugging

### Metrics Collection
- **Message Metrics**: Throughput, latency, error rates
- **System Metrics**: CPU, memory, goroutine count
- **Business Metrics**: Domain-specific KPIs
- **Connection Metrics**: NATS connection health and performance
- **Custom Metrics**: Service-specific measurements

### Distributed Tracing
- **Request Tracing**: Follow requests across service boundaries  
- **Message Tracing**: Track message flow through system
- **Dependency Mapping**: Understand service relationships
- **Performance Analysis**: Identify bottlenecks in request flow
- **Error Attribution**: Track error sources in distributed calls

---

## Common Pitfalls & Solutions

### Message Ordering Issues
**Problem**: Messages processed out of order
**Solution**: Use JetStream with proper partitioning strategy, or design for order-independent processing

### Memory Leaks
**Problem**: Goroutines or connections not properly cleaned up  
**Solution**: Always use context cancellation and defer cleanup, implement proper shutdown procedures

### Cascade Failures
**Problem**: One service failure bringing down entire system
**Solution**: Implement circuit breakers, timeouts, and bulkhead isolation patterns

### Message Schema Evolution
**Problem**: Breaking changes in message format breaking consumers
**Solution**: Use semantic versioning for subjects, maintain backward compatibility

### Performance Degradation
**Problem**: System slows down under load
**Solution**: Implement backpressure, use bounded queues, add load shedding mechanisms

---

## Decision Making Framework

### When to Split Services
- **Team Boundaries**: Different teams own different domains
- **Scaling Requirements**: Different performance characteristics needed
- **Technology Requirements**: Different tech stacks optimal for different domains
- **Data Consistency**: Different consistency requirements
- **Deployment Frequency**: Different release cycles needed

### When to Merge Services  
- **High Coupling**: Frequent communication between services
- **Shared Database**: Services sharing data models
- **Operational Overhead**: Too many services to manage effectively
- **Performance Issues**: Network latency between services problematic
- **Team Size**: Not enough team members to maintain separate services

### Technology Choice Criteria
- **Performance Requirements**: Latency, throughput, scalability needs
- **Consistency Requirements**: Strong vs eventual consistency needs  
- **Durability Requirements**: Message persistence and recovery needs
- **Operational Complexity**: Team's ability to operate chosen technologies
- **Ecosystem Maturity**: Available tools, libraries, community support

---

## Success Metrics

### Technical Metrics
- **Service Availability**: 99.9%+ uptime target
- **Message Processing Latency**: p95 under target thresholds
- **Throughput**: Handle expected peak loads with headroom
- **Error Rates**: Keep error rates under acceptable thresholds
- **Recovery Time**: Fast recovery from failures

### Operational Metrics  
- **Deployment Frequency**: Enable frequent, safe deployments
- **Mean Time to Detection**: Quick identification of issues
- **Mean Time to Resolution**: Fast problem resolution
- **Change Failure Rate**: Low rate of deployment-caused issues
- **Team Productivity**: Measure developer velocity and satisfaction

### Business Metrics
- **Feature Delivery Speed**: Faster time to market for new features
- **System Reliability**: Reduced business impact from technical issues  
- **Cost Efficiency**: Optimal resource utilization
- **Scalability**: Ability to handle business growth
- **Innovation Velocity**: Enable rapid experimentation and iteration

This guidance should inform all architectural and implementation decisions when developing Go + NATS microservices. Always consider the specific context and requirements when applying these principles.
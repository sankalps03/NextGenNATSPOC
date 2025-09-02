# NATS Cluster Setup with Ticket Service

This directory contains a complete Docker setup for a 3-node NATS cluster named `nats-motadata` with JetStream enabled, plus the ticket service configured to connect to the cluster.

## Architecture Overview

### NATS Cluster: `nats-motadata`
- **Node 1**: `nats-motadata-1` (ports: 4222, 8222, 6222, 11222)
- **Node 2**: `nats-motadata-2` (ports: 4223, 8223, 6223, 11223)  
- **Node 3**: `nats-motadata-3` (ports: 4224, 8224, 6224, 11224)

### Services
- **ticket-svc**: Ticket management service connecting to the cluster
- **stream-manager**: Automatically creates required JetStream streams
- **nats-box**: Management container for cluster operations

## Quick Start

### 1. Start the NATS Cluster
```bash
cd docker-cluster
docker-compose up -d
```

### 2. Verify Cluster Status
```bash
# Check all services are running
docker-compose ps

# Check cluster status
docker-compose exec nats-box /scripts/cluster-status.sh
```

### 3. Test Ticket Service
```bash
# Create a ticket
curl -X POST http://localhost:8080/api/v1/tickets \
  -H "Content-Type: application/json" \
  -H "X-Tenant-ID: test-tenant" \
  -d '{"title": "Test ticket", "description": "This is a test", "created_by": "user1"}'

# List tickets
curl -X GET http://localhost:8080/api/v1/tickets \
  -H "X-Tenant-ID: test-tenant"
```

## Detailed Setup

### Port Mappings

#### NATS Node 1 (`nats-motadata-1`)
- **4222**: Client connections
- **8222**: HTTP monitoring/management
- **6222**: Cluster communication
- **11222**: Profiling endpoint

#### NATS Node 2 (`nats-motadata-2`)
- **4223**: Client connections  
- **8223**: HTTP monitoring/management
- **6223**: Cluster communication
- **11223**: Profiling endpoint

#### NATS Node 3 (`nats-motadata-3`)
- **4224**: Client connections
- **8224**: HTTP monitoring/management  
- **6224**: Cluster communication
- **11224**: Profiling endpoint

#### Ticket Service
- **8080**: HTTP API

### JetStream Configuration

Each node is configured with:
- **Storage**: File-based with 3 replicas
- **Memory limit**: 256MB per node
- **File storage**: 2GB per node
- **Message retention**: 7 days
- **Deduplication window**: 10 minutes

### Automatic Stream Creation

The `stream-manager` service automatically creates:

**TICKET_EVENTS Stream**
- **Subjects**: `ticket.*`, `notification.*`, `audit.*`
- **Storage**: File with 3 replicas
- **Retention**: 7 days
- **Max message size**: 1MB
- **Deduplication**: 10 minute window

## Management Commands

### Using NATS Box Container

```bash
# Access the NATS management container
docker-compose exec nats-box sh

# Inside the container, connect to cluster
export NATS_URL="nats://nats-motadata-1:4222,nats://nats-motadata-2:4222,nats://nats-motadata-3:4222"

# Check server info
nats server info

# List streams
nats stream list

# Monitor messages
nats stream info TICKET_EVENTS

# Subscribe to events
nats sub "ticket.*"
```

### Cluster Status Scripts

```bash
# Check cluster health
docker-compose exec nats-box /scripts/cluster-status.sh

# Re-setup streams (if needed)
docker-compose exec nats-box /scripts/setup-streams.sh
```

### HTTP Monitoring Endpoints

Access NATS monitoring via HTTP:

```bash
# Node 1 monitoring
curl http://localhost:8222/varz    # Server variables
curl http://localhost:8222/routez  # Cluster routes
curl http://localhost:8222/subsz   # Subscriptions

# Node 2 monitoring  
curl http://localhost:8223/varz
curl http://localhost:8223/routez
curl http://localhost:8223/subsz

# Node 3 monitoring
curl http://localhost:8224/varz
curl http://localhost:8224/routez  
curl http://localhost:8224/subsz
```

## Service Configuration

### Ticket Service Environment Variables

The ticket service connects to the cluster using:

```bash
NATS_URL=nats://nats-motadata-1:4222,nats://nats-motadata-2:4222,nats://nats-motadata-3:4222
PORT=8080
SERVICE_NAME=ticket-svc
LOG_LEVEL=info
```

### Connection Features

- **Cluster awareness**: Automatically connects to available nodes
- **Failover**: Switches nodes if primary connection fails
- **Reconnection**: Automatic reconnection with exponential backoff
- **Connection pooling**: Efficiently manages cluster connections

## Troubleshooting

### Check Container Status
```bash
docker-compose ps
docker-compose logs nats-motadata-1
docker-compose logs ticket-svc
```

### Verify Cluster Formation
```bash
# Check cluster routes
for port in 8222 8223 8224; do
  echo "=== Node on port $port ==="
  curl -s "http://localhost:$port/routez" | jq '.routes'
done
```

### Test Connectivity
```bash
# Test from nats-box
docker-compose exec nats-box nats server ping
```

### Reset Cluster (if needed)
```bash
# Stop and remove everything
docker-compose down -v

# Remove persistent data
docker volume prune -f

# Restart
docker-compose up -d
```

## Scaling

### Adding More Nodes
To add more nodes to the cluster:

1. Create new configuration file (e.g., `nats-server-4.conf`)
2. Add service to `docker-compose.yml`
3. Update existing nodes' `routes` configuration
4. Restart cluster: `docker-compose up -d`

### Horizontal Service Scaling
```bash
# Scale ticket service to 3 instances
docker-compose up -d --scale ticket-svc=3
```

## Production Considerations

### Security
- Enable TLS for all connections
- Add authentication (JWT/NKeys)
- Use secrets management for credentials
- Restrict network access with firewalls

### Monitoring  
- Set up Prometheus metrics collection
- Configure alerting for node failures
- Monitor JetStream storage usage
- Track message processing rates

### Backup
- Regular backup of JetStream data directories
- Test recovery procedures
- Monitor storage usage and rotation

### Performance
- Tune JetStream settings based on load
- Monitor memory and CPU usage
- Optimize network latency between nodes
- Consider SSD storage for better performance

## Docker Compose Commands Reference

```bash
# Start services
docker-compose up -d

# Stop services
docker-compose stop

# View logs
docker-compose logs -f [service-name]

# Scale services
docker-compose up -d --scale ticket-svc=3

# Restart specific service
docker-compose restart nats-motadata-1

# Remove everything
docker-compose down -v

# Rebuild and start
docker-compose up -d --build
```

This setup provides a robust, production-ready NATS cluster foundation that can be extended and customized based on specific requirements.
# ITSM Microservice Architecture - Final Decision Document

**Date:** October 3, 2025  
**Status:** Approved for Implementation  
**Architecture Type:** Hybrid DuckDB Cache + Analytics on Shared EBS

---

## Executive Summary

The final architecture decision for the ITSM platform is a **hybrid caching and analytics system** that combines:

- **PostgreSQL** as the system of record (source of truth)
- **DuckDB + Parquet** for operational caching and analytics
- **NATS** for event-driven communication between services
- **Shared EBS storage** for Parquet files accessible across all services

This architecture provides the optimal balance of performance, cost efficiency, and operational simplicity.

---

## Core Architecture Components

### 1. Microservice Layer
- Each ITSM module (Ticket, Incident, Change, etc.) runs as an independent microservice
- Each service maintains its own **embedded DuckDB instance** for operational queries
- Services write to PostgreSQL and publish events via NATS
- Hot data kept in-memory in DuckDB for sub-10ms query performance

### 2. Storage Layer
- **PostgreSQL**: Primary database, ACID guarantees, source of truth
- **Parquet Files**: Stored on shared EBS, accessible by all services
- **DuckDB In-Memory**: Hot data cached for fast operational queries

### 3. Analytics Layer
- **Centralized Analytics Service** with dedicated DuckDB instance
- Runs cross-service join queries across all Parquet files
- Supports complex aggregations, reporting, and ML workloads
- Query performance: 200-500ms for federated analytics

### 4. Event Bus
- **NATS** for asynchronous event-driven communication
- Services publish domain events after PostgreSQL commits
- Other services consume events to update their DuckDB caches
- Ensures eventual consistency across the system

---

## Data Flow Architecture

### Write Path
1. Service writes data to **PostgreSQL** (ACID transaction)
2. After successful commit, publish event to **NATS**
3. Other services consume the event
4. Each service updates its local **DuckDB cache**
5. Service writes updated data to **Parquet** on shared EBS

### Read Path (Operational Queries)
1. Service queries **DuckDB in-memory cache** first
2. Cache hit → Return immediately (2-10ms latency)
3. Cache miss → Load from **Parquet file** (50-200ms)
4. Populate in-memory cache for future queries

### Read Path (Analytics Queries)
1. Analytics service receives cross-module query
2. Queries relevant **Parquet files** from shared EBS
3. DuckDB performs federated joins across files
4. Results returned (200-500ms for complex queries)

---

## Key Design Decisions

### DuckDB Placement: Two-Layer Approach

**Layer 1: Per-Service Operational Cache**
- Embedded DuckDB in each microservice
- Purpose: Speed up single-service API queries
- Scope: Only data within service domain
- Performance: 2-20ms for operational queries

**Layer 2: Centralized Analytics Service**
- Dedicated DuckDB instance for cross-service queries
- Purpose: Reporting, dashboards, ML, complex analytics
- Scope: All services' data via shared Parquet files
- Performance: 100ms-5s for analytical queries

**Why Not Combined?**
- Operational and analytical workloads have different requirements
- Operational needs low latency, analytics needs complex joins
- Separation of concerns improves maintainability
- Independent scaling for each layer

### Shared EBS Storage Strategy

**Architecture:**
- Single shared EBS volume mounted by all services (read-only for most)
- Each service has its own Parquet directory structure
- Analytics service has read access to all directories

**Benefits:**
- No data duplication across services
- Simplified cross-service analytics
- Lower storage costs (64% cheaper than pure in-memory)
- Fast crash recovery (load from Parquet vs rebuilding cache)

**File Organization:**
```
/shared-ebs/
├── tickets/
│   ├── hot/          # Last 90 days
│   ├── warm/         # 90-365 days
│   └── cold/         # >365 days
├── incidents/
│   ├── hot/
│   ├── warm/
│   └── cold/
└── changes/
    ├── hot/
    ├── warm/
    └── cold/
```

### Hot/Warm/Cold Data Tiering

**Hot Data (Last 90 days)**
- Kept in-memory in DuckDB
- Highest query frequency
- Target latency: 2-10ms
- 90-95% cache hit rate

**Warm Data (90-365 days)**
- Loaded from Parquet on demand
- Moderate query frequency
- Target latency: 50-200ms
- Cached temporarily after access

**Cold Data (>365 days)**
- Archived in compressed Parquet
- Low query frequency
- Target latency: 200-500ms
- Only for compliance/audit queries

### Multi-Tenant Isolation

**Strategy: Partition-Based Isolation**
- Separate Parquet files per tenant in each service
- Each service maintains tenant-aware in-memory cache
- Row-level security enforced at service level
- Analytics queries filtered by tenant ID

**File Structure:**
```
/shared-ebs/tickets/hot/
├── tenant_001_2025_Q4.parquet
├── tenant_002_2025_Q4.parquet
└── tenant_003_2025_Q4.parquet
```

---

## High Availability & Replication Strategy

### DuckDB HA Architecture via NATS

**Challenge:** DuckDB is embedded and doesn't natively support replication. Traditional database replication doesn't apply.

**Solution:** Event-driven cache synchronization across replicas using NATS.

### Service Instance Architecture

Each microservice runs **multiple replicas** for high availability:

```
Ticket Service
├── Instance 1 (Primary) - us-east-1a
│   ├── DuckDB embedded cache (in-memory + Parquet)
│   ├── Subscribes to NATS ticket.events
│   └── Publishes to NATS on writes
├── Instance 2 (Replica) - us-east-1b
│   ├── DuckDB embedded cache (in-memory + Parquet)
│   ├── Subscribes to NATS ticket.events
│   └── Publishes to NATS on writes
└── Instance 3 (Replica) - us-east-1c
    ├── DuckDB embedded cache (in-memory + Parquet)
    ├── Subscribes to NATS ticket.events
    └── Publishes to NATS on writes
```

### Replication Mechanism

**1. Event-Driven Cache Synchronization**

All service instances maintain **eventually consistent** caches through NATS:

```
Write Flow:
┌─────────────┐
│ Instance 1  │──┐
│ Writes to   │  │
│ PostgreSQL  │  │
└─────────────┘  │
                 ↓
           ┌──────────┐
           │   NATS   │
           │  Event   │
           └──────────┘
                 ↓
    ┌────────────┼────────────┐
    ↓            ↓            ↓
┌─────────┐ ┌─────────┐ ┌─────────┐
│Instance1│ │Instance2│ │Instance3│
│DuckDB   │ │DuckDB   │ │DuckDB   │
│Updates  │ │Updates  │ │Updates  │
└─────────┘ └─────────┘ └─────────┘
```

**Important: In-Memory Cache Updates vs Parquet Writes**

**Problem:** If all 3 instances write to the same Parquet file on shared EBS, it causes write conflicts and data corruption.

**Solution:** Split responsibility between cache updates and Parquet writes:

```
NATS Event Published
    ↓
┌────────────────────────────────────────────────────────┐
│ Broadcast Topic: "ticket.updated.broadcast"            │
│ ALL instances subscribe (for in-memory cache updates)  │
└────────────────────────────────────────────────────────┘
    ↓                    ↓                    ↓
┌───────────┐    ┌───────────┐    ┌───────────┐
│Instance 1 │    │Instance 2 │    │Instance 3 │
│✅ Updates │    │✅ Updates │    │✅ Updates │
│In-Memory  │    │In-Memory  │    │In-Memory  │
│Cache      │    │Cache      │    │Cache      │
└───────────┘    └───────────┘    └───────────┘

┌────────────────────────────────────────────────────────┐
│ Queue Topic: "ticket.updated.parquet"                  │
│ ONLY ONE instance processes (NATS Queue Group)         │
└────────────────────────────────────────────────────────┘
    ↓
NATS Queue Group: "parquet-writers"
    ↓
┌────────────────────────────────────────────────────┐
│ Instance 2 (selected by NATS)                      │
│ ✅ Writes to Parquet                               │
│ /shared-ebs/tickets/tenant_001/delta_001.parquet   │
└────────────────────────────────────────────────────┘

Instance 1: ❌ Not processing this message (queue ensures single consumer)
Instance 3: ❌ Not processing this message (queue ensures single consumer)
```

**Why This Approach:**
- ✅ **No Write Conflicts:** Only ONE instance writes to Parquet at a time
- ✅ **No Coordination Overhead:** NATS queue groups handle distribution automatically
- ✅ **Automatic Failover:** If consumer dies, NATS assigns message to another instance
- ✅ **All Caches Stay Synced:** Broadcast ensures all instances have latest data in-memory
- ✅ **High Performance:** In-memory reads from all instances, coordinated Parquet writes

**NATS Configuration for Dual Topics:**

```yaml
nats_topics:
  # Broadcast topic - ALL instances subscribe
  cache_updates:
    subject: "ticket.updated.broadcast"
    type: "broadcast"
    subscribers: "all_instances"
    purpose: "In-memory cache synchronization"
    
  # Queue topic - ONLY ONE instance processes
  parquet_writes:
    subject: "ticket.updated.parquet"
    type: "queue"
    queue_group: "parquet-writers"
    subscribers: "single_instance_at_a_time"
    purpose: "Coordinated Parquet file writes"
```

**Publisher Behavior:**

When a ticket is updated, the service publishes to BOTH topics:

```yaml
publish_strategy:
  # Step 1: Write to PostgreSQL
  postgresql:
    action: "UPDATE tickets SET ..."
    
  # Step 2: Update local in-memory cache immediately
  local_cache:
    action: "Update DuckDB in-memory table"
    
  # Step 3: Publish to broadcast topic
  nats_broadcast:
    topic: "ticket.updated.broadcast"
    payload: {ticket_id, tenant_id, changes}
    purpose: "Other instances update their in-memory caches"
    
  # Step 4: Publish to queue topic
  nats_queue:
    topic: "ticket.updated.parquet"
    payload: {ticket_id, tenant_id, full_data}
    purpose: "Single consumer writes to Parquet"
```

**Consumer Behavior:**

Each instance subscribes to both topics with different handlers:

```yaml
instance_subscriptions:
  # Subscription 1: Cache updates (all instances)
  cache_subscription:
    topic: "ticket.updated.broadcast"
    handler: "updateInMemoryCache()"
    execution: "All instances process every message"
    latency: "50-100ms"
    
  # Subscription 2: Parquet writes (queue group)
  parquet_subscription:
    topic: "ticket.updated.parquet"
    queue_group: "parquet-writers"
    handler: "writeToParquetBuffer()"
    execution: "Only ONE instance processes each message"
    latency: "5-10 minutes (batched)"
```

**Failover Scenario:**

If the instance currently writing to Parquet fails:

```
Before Failure:
Instance 1: Processing cache updates
Instance 2: Processing cache updates + Parquet writes ✅
Instance 3: Processing cache updates

After Instance 2 Fails:
Instance 1: Processing cache updates + Parquet writes ✅ (NATS reassigned)
Instance 3: Processing cache updates

Result: Zero data loss, automatic failover
```

**Benefits of Queue-Based Parquet Writing:**

| Aspect | Benefit |
|--------|---------|
| **Consistency** | No write conflicts, single writer to Parquet files |
| **Availability** | Automatic failover if writer instance dies |
| **Performance** | All instances serve reads, coordinated writes |
| **Scalability** | Add more instances without coordination complexity |
| **Simplicity** | No need for leader election or distributed locks |

**2. Shared Parquet Storage**

All instances read from the **same shared EBS volume**:

```
┌──────────────────────────────────────┐
│     Shared EBS Volume (Multi-AZ)     │
│  /shared-ebs/tickets/                │
│    ├── hot/ (Parquet files)          │
│    ├── warm/ (Parquet files)         │
│    └── cold/ (Parquet files)         │
└──────────────────────────────────────┘
         ↑           ↑           ↑
         │           │           │
    ┌────────┐  ┌────────┐  ┌────────┐
    │ Inst 1 │  │ Inst 2 │  │ Inst 3 │
    │ us-e-1a│  │ us-e-1b│  │ us-e-1c│
    │(R/W)   │  │(R/W)   │  │(R/W)   │
    └────────┘  └────────┘  └────────┘
    
All instances have R/W access to EBS, but only ONE writes at a time (coordinated via NATS queue)
```

**Read/Write Access Pattern:**

```yaml
ebs_access:
  read_access:
    - All instances can read Parquet files
    - Used for cache misses and cold data access
    - No coordination needed for reads
    
  write_access:
    - All instances have write permission
    - But ONLY queue consumer writes
    - Coordinated via NATS queue group
    - Single writer eliminates conflicts
```

### HA Scenarios and Handling

**Scenario 1: Instance Failure**

When an instance crashes:
1. Kubernetes detects failed pod
2. New instance spins up (30-60 seconds)
3. New instance loads DuckDB cache from Parquet (2-5 minutes)
4. Subscribes to NATS for ongoing updates
5. Begins serving traffic

**Recovery Time:**
- Health check detection: 10-30 seconds
- Pod replacement: 30-60 seconds
- Cache warm-up: 2-5 minutes (hot data only)
- **Total RTO: 3-6 minutes**

**Data Loss:** None - PostgreSQL is source of truth, cache rebuilds from Parquet + NATS events

**Scenario 2: Shared EBS Failure**

If EBS volume fails:
1. All instances lose Parquet access
2. Continue serving from in-memory cache (hot data only)
3. Queries for cold data fail temporarily
4. AWS EBS snapshot restores volume (15-30 minutes)
5. Instances reload Parquet files

**Mitigation:**
- Use EBS Multi-AZ volumes (io2 Block Express)
- Regular automated snapshots (every 6 hours)
- Keep 90 days of hot data in-memory (covers most queries)

### Cache Consistency Model

**Consistency Level: Eventually Consistent**
- All instances receive NATS events within 50-100ms
- Cache updates applied within 100-200ms
- **Max staleness: <500ms** under normal conditions

**Read-Your-Writes Consistency:**

Ensures client reads their own writes immediately while other instances catch up via NATS within 100-500ms.

### NATS Configuration for HA

**NATS Cluster Setup:**

```yaml
# NATS Cluster (3 nodes for quorum)
nats-cluster:
  replicas: 3
  nodes:
    - nats-1.us-east-1a
    - nats-2.us-east-1b
    - nats-3.us-east-1c
  
  # JetStream for persistent events
  jetstream:
    enabled: true
    storage: file
    max_memory: 2GB
    max_file: 10GB
    
  # Message retention
  stream_config:
    name: itsm-events
    subjects: ["ticket.*", "incident.*", "change.*"]
    retention: limits
    max_age: 24h
    max_msgs: 10M
    storage: file
```

**Key Features:**
- ✅ **Durable consumers** - Resume from last position after restart
- ✅ **At-least-once delivery** - Guaranteed event processing
- ✅ **Message persistence** - 24-hour replay window for catch-up
- ✅ **Consumer groups** - Each instance gets all events

### Active-Active Architecture

**Chosen Model: Active-Active**

All instances:
- ✅ Serve read traffic simultaneously
- ✅ Can process write operations
- ✅ Maintain independent DuckDB caches
- ✅ Load balanced by Kubernetes service

**Active-Active Benefits:**
- ✅ Better resource utilization
- ✅ Higher throughput (N instances = N× capacity)
- ✅ Faster failover (replicas already warm)
- ✅ Zero-downtime rolling updates

### Deployment Strategy for Zero-Downtime

**Rolling Update Process:**

```yaml
# Kubernetes Deployment
apiVersion: apps/v1
kind: Deployment
metadata:
  name: ticket-service
spec:
  replicas: 3
  strategy:
    type: RollingUpdate
    rollingUpdate:
      maxSurge: 1
      maxUnavailable: 0
```

**Update Flow:**
1. Start new pod (Instance 4)
2. Wait for health check (Instance 4 warm-up)
3. Add Instance 4 to service pool
4. Remove Instance 1 from pool
5. Graceful shutdown Instance 1
6. Repeat for Instance 2, 3

### Monitoring HA Health

**Key Metrics:**

```
Service Health:
├── instance_up{service="ticket", instance="1"}: 1
├── cache_hit_rate{instance="1"}: 0.94
├── nats_lag_ms{consumer="ticket-cache-sync"}: 87

NATS Cluster:
├── nats_cluster_size: 3
├── nats_messages_per_sec: 2453
├── nats_consumer_lag_ms: 87
```

### Disaster Recovery

**Backup Strategy:**
1. **PostgreSQL**: Continuous WAL archiving + daily snapshots
2. **Parquet Files**: EBS snapshots every 6 hours
3. **NATS State**: JetStream snapshots every hour

**Recovery Scenarios:**

**Complete Region Failure:**
1. Failover to DR region
2. Restore PostgreSQL from latest backup (RPO: <15 min)
3. Mount EBS snapshot (RPO: <6 hours for cold data)
4. Services rebuild hot cache from PostgreSQL + NATS replay
5. **RTO: 15-30 minutes**

### HA Summary

| Component | HA Strategy | RTO | RPO |
|-----------|-------------|-----|-----|
| **PostgreSQL** | Multi-AZ Primary + Sync Standby | <60s | 0 |
| **DuckDB Cache** | Multi-instance + NATS sync | 3-6 min | 0 |
| **Parquet Files** | EBS Multi-AZ + Snapshots | 15-30 min | <6 hours |
| **NATS Cluster** | 3-node cluster + JetStream | <30s | 0 |
| **Service Instances** | 3+ replicas per service | <5 min | 0 |

**Availability Target: 99.95% (4.38 hours downtime/year)**

---

## Handling Frequent Ticket Updates

### Challenge: Update Storms

In ITSM systems, tickets can experience **high-frequency updates**:

**Common Scenarios:**
- Status changes during incident resolution (every 30-60 seconds)
- Comment/note additions by multiple team members
- SLA countdown updates (every minute)
- Automated bot updates (monitoring integrations)
- Mass updates (bulk status changes, reassignments)

**Example Storm Pattern:**
```
Single Ticket (ID: T-12345)
├── 09:00:00 - Status: New → In Progress
├── 09:00:15 - Comment added by Agent A
├── 09:00:30 - Priority: Low → High
├── 09:00:45 - Assigned to: Agent B
├── 09:01:00 - SLA update (auto)
├── 09:01:20 - Comment added by Agent B
├── 09:01:40 - Status: In Progress → Resolved
└── 09:02:00 - Resolution notes added

Result: 8 updates in 2 minutes = 4 updates/minute
```

**System Impact Without Optimization:**
- ❌ 8 NATS events published
- ❌ 8 DuckDB cache updates per instance (8 × 3 = 24 updates)
- ❌ 8 Parquet write operations
- ❌ Cache invalidation overhead
- ❌ Network bandwidth waste
- ❌ EBS IOPS exhaustion

### Solution: Multi-Layer Update Optimization

### 1. Write Coalescing & Batching

**Strategy:** Aggregate multiple updates within a time window before propagating.

**Configuration:**
```yaml
update_buffer:
  flush_interval: 5s
  max_buffer_size: 10000
  flush_on_query: true
```

**Benefits:**
- ✅ 8 updates → 1 coalesced update (87.5% reduction)
- ✅ Reduced NATS messages
- ✅ Fewer DuckDB operations
- ✅ Lower EBS write operations

**Trade-off:**
- ⚠️ Up to 5-second staleness in cache across instances
- ✅ Mitigated: Local instance always has latest

### 2. Change Delta Tracking

**Strategy:** Only propagate actual changes, not full object updates.

**Benefits:**
- ✅ Smaller NATS message payload (50-80% size reduction)
- ✅ Faster cache updates
- ✅ Reduced network transfer

### 3. Smart Cache Update Strategy

**Three-Tier Update Priority:**

| Field Type | Priority | Flush Interval | Use Case |
|------------|----------|----------------|----------|
| status, priority, severity | High | Immediate | Critical for dashboards |
| assignee, due_date, SLA | Medium | 2 seconds | Important for workflows |
| tags, custom_fields | Low | 10 seconds | Nice-to-have |
| audit_log, history | Low | 30 seconds | Background data |

### 4. Parquet Write Optimization

**Problem:** Frequent Parquet rewrites are expensive.

**Solution: Append-Only Log + Periodic Compaction**

```
Write Strategy:
├── Hot Updates → In-memory DuckDB only
├── Every 5 minutes → Flush to append-only delta files
├── Every 1 hour → Compact deltas into main Parquet files
└── Nightly → Full compaction + cleanup

File Structure:
/shared-ebs/tickets/hot/
├── tenant_001_base.parquet
├── tenant_001_delta_001.parquet
├── tenant_001_delta_002.parquet
└── tenant_001_delta_003.parquet
```

**Schedule:**
```yaml
parquet_compaction:
  delta_flush: 5m
  minor_compaction: 1h
  major_compaction: "0 2 * * *"
  max_deltas: 12
```

**Benefits:**
- ✅ In-memory updates are instant
- ✅ Parquet writes are batched
- ✅ Reduced EBS I/O by 95%

### 5. NATS Stream Deduplication

**Solution:** Enable message deduplication in JetStream with 5-minute window.

**Benefits:**
- ✅ Prevents duplicate cache updates
- ✅ Reduces unnecessary processing

### 6. Rate Limiting at Service Level

**Configuration:**
```yaml
rate_limits:
  per_ticket: 10/minute
  per_user: 100/minute
  burst: 5
```

### 7. Monitoring Frequent Updates

**Key Metrics:**
```
Ticket Update Metrics:
├── update_rate{ticket_id="T-12345"}: 4.2/min
├── update_buffer_size: 2847 updates
├── parquet_delta_files: 8
├── compaction_lag_minutes: 12
└── cache_propagation_lag_ms: 87
```

### Performance Impact: Before vs After

**Scenario:** 1000 tickets with 5 updates/minute each = 5000 updates/minute

**Before Optimization:**
```
Per Minute:
├── NATS messages: 5000
├── DuckDB cache updates: 15,000
├── Parquet writes: 5000
├── Network bandwidth: ~500 MB
└── EBS IOPS: 5000 writes
```

**After Optimization:**
```
Per Minute:
├── NATS messages: 1000 (coalesced)
├── DuckDB cache updates: 3,000
├── Parquet writes: 0 (buffered)
├── Network bandwidth: ~100 MB
└── EBS IOPS: ~100

Performance Improvements:
✅ 80% fewer NATS messages
✅ 80% fewer cache updates
✅ 95% fewer Parquet writes
✅ 80% less network bandwidth
✅ 95% reduction in EBS IOPS
```

**Cost Impact:**
```
Before: $1,550/month
After: $310/month
Savings: $1,240/month (80% reduction)
```

### Best Practices Summary

1. ✅ Use write coalescing with 5-second buffer windows
2. ✅ Implement delta tracking to reduce payload size
3. ✅ Prioritize updates based on field importance
4. ✅ Use append-only deltas instead of rewriting Parquet
5. ✅ Enable NATS deduplication
6. ✅ Apply rate limiting to prevent abuse
7. ✅ Monitor update patterns and adjust dynamically
8. ✅ Schedule compaction during off-peak hours
9. ✅ Keep hot data in-memory to absorb storms
10. ✅ Batch operations whenever possible

---

## DuckDB Indexing & Optimization Strategy

### Understanding DuckDB's Indexing Approach

**Important:** DuckDB does NOT use traditional B-tree indexes like PostgreSQL. Instead:

1. **Columnar Storage** - Built-in optimization for column-oriented queries
2. **Zone Maps** - Automatic min/max statistics per row group
3. **Parquet Metadata** - Column statistics stored in file headers
4. **Adaptive Query Execution** - Runtime optimization

**Why No Traditional Indexes?**
- DuckDB is designed for analytical queries (OLAP)
- Columnar format provides excellent scan performance
- Zone maps eliminate need for most indexes
- Memory-mapped files leverage OS page cache

### Optimization Strategies for DuckDB

### 1. File-Level Partitioning (Most Important)

**Strategy:** Partition Parquet files by most frequently filtered columns.

**Example: Partition by Tenant + Time**

```
/shared-ebs/tickets/
├── hot/
│   ├── tenant_001/
│   │   ├── 2025-Q4-W40.parquet
│   │   ├── 2025-Q4-W41.parquet
│   │   └── 2025-Q4-W42.parquet
│   ├── tenant_002/
│   │   ├── 2025-Q4-W40.parquet
│   │   └── 2025-Q4-W41.parquet
│   └── tenant_003/
│       └── 2025-Q4-W42.parquet
├── warm/
│   ├── tenant_001/
│   │   ├── 2025-Q3.parquet
│   │   └── 2025-Q2.parquet
│   └── tenant_002/
│       └── 2025-Q3.parquet
└── cold/
    ├── tenant_001/
    │   └── 2024-full.parquet
    └── tenant_002/
        └── 2024-full.parquet
```

**Query Optimization with Partitioning:**

```sql
-- Without partitioning: Scans all files
SELECT * FROM read_parquet('/shared-ebs/tickets/**/*.parquet')
WHERE tenant_id = 1 AND created_at >= '2025-10-01';
-- Reads: ALL files (slow)

-- With partitioning: Only scans relevant files
SELECT * FROM read_parquet('/shared-ebs/tickets/hot/tenant_001/*.parquet')
WHERE created_at >= '2025-10-01';
-- Reads: Only tenant_001 files from hot tier (fast)
```

**Performance Impact:**
- ✅ Reduces files scanned by 95%+
- ✅ Query time: 500ms → 20ms
- ✅ I/O reduced by 90%+

### 2. Row Group Size Optimization

**Row groups** are the fundamental unit of data storage in Parquet.

```sql
COPY (
    SELECT * FROM tickets 
    ORDER BY tenant_id, status, created_at
) TO '/shared-ebs/tickets/hot/tenant_001_2025-Q4.parquet' (
    FORMAT PARQUET,
    ROW_GROUP_SIZE 50000,
    COMPRESSION 'ZSTD',
    COMPRESSION_LEVEL 6
);
```

**Row Group Size Guidelines:**

| Use Case | Row Group Size | Reason |
|----------|----------------|--------|
| Highly selective queries | 25K-50K | Better filtering, more zone maps |
| Full table scans | 100K-200K | Fewer row groups, less metadata |
| Mixed workload | 50K-75K | Good balance |
| Large text columns | 25K-50K | Manage memory pressure |

**Zone Maps in Action:**

```sql
Row Group 1: tickets 1-50K
  └── Zone Map: 
      ├── tenant_id: min=1, max=5
      ├── status: min='In Progress', max='Resolved'
      └── created_at: min='2025-10-01', max='2025-10-07'

Row Group 2: tickets 50K-100K
  └── Zone Map:
      ├── tenant_id: min=1, max=8
      ├── status: min='Closed', max='New'
      └── created_at: min='2025-10-08', max='2025-10-15'

-- Query: WHERE tenant_id = 10 AND status = 'Open'
-- DuckDB skips BOTH row groups using zone maps!
```

### 3. Physical Data Sorting (Clustering)

**Strategy:** Sort data by most frequently filtered columns.

```sql
CREATE TABLE tickets_sorted AS
SELECT * FROM tickets
ORDER BY tenant_id, status, created_at;

COPY tickets_sorted TO 'tickets_optimized.parquet' (
    FORMAT PARQUET,
    ROW_GROUP_SIZE 50000
);
```

**Why Sorting Matters:**

```
Unsorted Data:
Row Group 1: tenant_id [1,5,2,8,1,3,7,...]  -- Mixed
Query: WHERE tenant_id = 1
Result: Must scan ALL row groups

Sorted Data:
Row Group 1: tenant_id [1,1,1,1,1,1,1,...]  -- Only tenant 1
Row Group 2: tenant_id [2,2,2,2,2,2,2,...]  -- Only tenant 2
Query: WHERE tenant_id = 1
Result: Scans ONLY Row Group 1 (90%+ faster)
```

**Multi-Column Sorting Strategy:**
```sql
-- Priority 1: tenant_id (multi-tenancy)
-- Priority 2: status (common filter)
-- Priority 3: created_at (time-based queries)
ORDER BY tenant_id, status, created_at DESC
```

### 4. Denormalized Data Structures

**Strategy:** Pre-join frequently accessed data.

```sql
CREATE TABLE tickets_denormalized AS
SELECT 
    t.ticket_id,
    t.ticket_number,
    t.tenant_id,
    t.status,
    t.priority,
    t.created_at,
    t.assigned_to,
    u.first_name AS assigned_first_name,
    u.last_name AS assigned_last_name,
    u.email AS assigned_email,
    u.department AS assigned_department
FROM tickets t
LEFT JOIN users u ON t.assigned_to = u.user_id;

COPY tickets_denormalized TO 'tickets_with_users.parquet' (
    FORMAT PARQUET,
    ROW_GROUP_SIZE 50000
);
```

**Denormalization Patterns:**

| Base Table | Common Join | Denormalized Fields |
|------------|-------------|---------------------|
| tickets | users (assigned_to) | first_name, last_name, email, department |
| tickets | groups (assigned_group) | group_name, group_type |
| incidents | tickets (related) | related_ticket_number, related_status |
| changes | users (requested_by) | requester_name, requester_email |

**Trade-offs:**
- ✅ 50-90% faster queries (no joins)
- ✅ Simpler query logic
- ⚠️ Larger file sizes (30-50% increase)
- ⚠️ Need to refresh when user data changes

### 5. Materialized Views for Hot Queries

**Strategy:** Pre-compute frequently used query results.

```sql
-- Dashboard: Open tickets by status
CREATE TABLE mv_tickets_open_by_status AS
SELECT 
    tenant_id,
    status,
    priority,
    COUNT(*) as ticket_count,
    MIN(created_at) as oldest_ticket,
    MAX(created_at) as newest_ticket
FROM tickets
WHERE closed_at IS NULL
GROUP BY tenant_id, status, priority
ORDER BY tenant_id, status, priority;

COPY mv_tickets_open_by_status TO 'mv_open_tickets.parquet';
```

**Materialized View Refresh Strategy:**

```yaml
materialized_views:
  mv_tickets_open_by_status:
    refresh_interval: 5m
    rebuild_time: ~500ms
    
  mv_tickets_by_assignee:
    refresh_interval: 5m
    rebuild_time: ~300ms
    
  mv_incident_trends:
    refresh_interval: 15m
    rebuild_time: ~2s
```

### 6. Column Projection & Pruning

**Strategy:** Only include frequently accessed columns.

```sql
-- Create separate projections for different use cases
CREATE TABLE tickets_list_view AS
SELECT 
    ticket_id, ticket_number, tenant_id, title, 
    status, priority, assigned_to, created_at, updated_at
FROM tickets;  -- Only 9 columns

CREATE TABLE tickets_detail_view AS
SELECT 
    ticket_id, ticket_number, tenant_id, title, description,
    status, priority, category, subcategory, assigned_to, 
    assigned_group, reporter_id, created_at, updated_at,
    due_date, resolved_at, closed_at
FROM tickets;  -- 17 columns
```

**Query Performance Impact:**

```sql
SELECT ticket_id, title, status FROM tickets;

Full table (50 cols): 500ms, 100 MB read
List view (9 cols):    50ms,  10 MB read  (10x faster)
```

### 7. Compression Strategy

```sql
COPY tickets TO 'tickets_optimized.parquet' (
    FORMAT PARQUET,
    ROW_GROUP_SIZE 50000,
    COMPRESSION 'ZSTD',
    COMPRESSION_LEVEL 6
);
```

**Compression Recommendations:**

| Column Type | Codec | Level | Reason |
|-------------|-------|-------|--------|
| Integer IDs | ZSTD | 6 | Excellent compression ratio |
| Status enums | DICTIONARY + ZSTD | 6 | Very repetitive values |
| Text (short) | ZSTD | 6 | Good balance |
| Text (large) | ZSTD | 9 | Maximize compression |
| Timestamps | DELTA_BINARY_PACKED | - | Natural for sequential data |
| Boolean | RLE | - | Run-length encoding perfect |

### 8. Multi-File Queries with Filtering

**Strategy:** Use glob patterns with predicates.

```sql
-- Bad: Reads ALL files, then filters
SELECT * FROM read_parquet('/shared-ebs/tickets/**/*.parquet')
WHERE tenant_id = 1 AND created_at >= '2025-10-01';

-- Good: Only reads relevant partition
SELECT * FROM read_parquet('/shared-ebs/tickets/hot/tenant_001/2025-Q4*.parquet')
WHERE created_at >= '2025-10-01';

-- Best: Combine partitioning + zone maps
-- Result: 99.5% data pruning!
```

### 9. Statistics and Metadata Management

```sql
-- DuckDB automatically collects statistics:
-- - Min/Max values per column per row group
-- - Null count per column
-- - Distinct value count
-- - Data size per column

-- View Parquet file metadata
SELECT * FROM parquet_metadata('/shared-ebs/tickets/hot/tenant_001.parquet');

-- View row group information
SELECT * FROM parquet_schema('/shared-ebs/tickets/hot/tenant_001.parquet');
```

### 10. Query-Specific Indexes (Temporary)

```sql
-- Load data into temporary table
CREATE TEMP TABLE tickets_analysis AS
SELECT * FROM read_parquet('/shared-ebs/tickets/**/*.parquet')
WHERE tenant_id = 1;

-- Analyze to collect statistics
ANALYZE tickets_analysis;

-- Run complex queries
SELECT 
    category,
    status,
    COUNT(*) as count
FROM tickets_analysis
WHERE created_at >= '2025-01-01'
GROUP BY category, status
ORDER BY count DESC;
```

### Performance Benchmarks

**Scenario: Query 100M ticket records**

| Optimization | Query Time | I/O Read | Files Scanned |
|--------------|-----------|----------|---------------|
| No optimization | 25.0s | 50 GB | 1000 files |
| + Partitioning | 5.0s | 10 GB | 200 files |
| + Sorting | 2.0s | 4 GB | 80 files |
| + Projection | 0.8s | 1.5 GB | 30 files |
| + Materialized View | 0.05s | 10 MB | 1 file |

**Cumulative Improvement: 500x faster**

### Optimization Best Practices

**✅ Do:**
1. **Partition by tenant and time** (most important)
2. **Sort data physically** by most common filters
3. **Use 50K row groups** for ITSM workloads
4. **Denormalize** frequently joined tables
5. **Create materialized views** for dashboards
6. **Project only needed columns**
7. **Use ZSTD compression** at level 6
8. **Monitor query patterns**

**❌ Don't:**
1. Store all data in single large file
2. Create unsorted Parquet files
3. Use tiny row groups (<10K rows)
4. Use huge row groups (>200K rows)
5. Over-denormalize (creates massive files)
6. Forget to refresh materialized views
7. Mix hot and cold data in same files
8. Use low compression for large text

### Maintenance Schedule

```yaml
parquet_maintenance:
  daily:
    - Flush in-memory data to delta Parquet files
    - Compact small delta files (< 1 MB)
    
  hourly:
    - Merge delta files into main Parquet files
    - Update materialized views (hot queries)
    
  weekly:
    - Full compaction of hot tier
    - Optimize row group sizes
    - Update column statistics
    
  monthly:
    - Move warm data to cold tier
    - Compress cold tier (higher compression)
    - Archive data beyond retention period
```

### Monitoring DuckDB Performance

**Target Metrics:**
- ✅ Zone map pruning: >90% row groups skipped
- ✅ Partition pruning: >95% files skipped
- ✅ Column pruning: Read <30% of columns
- ✅ Query latency: <50ms operational, <500ms analytics

---

## DuckDB Query Examples

### Setting Up DuckDB Tables and Views

#### 1. Creating Base Tables from Parquet

```sql
-- Create table reference to base Parquet file
CREATE TABLE tickets_base AS
SELECT * FROM read_parquet('/shared-ebs/tickets/hot/tenant_001/base.parquet');

-- Create a view for on-demand reading
CREATE VIEW tickets_hot AS
SELECT * FROM read_parquet('/shared-ebs/tickets/hot/tenant_001/*.parquet');

-- Create table with schema definition
CREATE TABLE tickets (
    ticket_id BIGINT PRIMARY KEY,
    ticket_number VARCHAR(50) NOT NULL,
    tenant_id INTEGER NOT NULL,
    title TEXT NOT NULL,
    description TEXT,
    status VARCHAR(50) NOT NULL,
    priority VARCHAR(20) NOT NULL,
    category VARCHAR(100),
    assigned_to INTEGER,
    created_at TIMESTAMP NOT NULL,
    updated_at TIMESTAMP NOT NULL,
    closed_at TIMESTAMP
);
```

#### 2. Creating Materialized Views

```sql
-- Materialized view for open tickets
CREATE TABLE mv_tickets_open AS
SELECT 
    ticket_id,
    ticket_number,
    tenant_id,
    title,
    status,
    priority,
    assigned_to,
    created_at,
    updated_at
FROM read_parquet('/shared-ebs/tickets/hot/**/*.parquet')
WHERE closed_at IS NULL
ORDER BY tenant_id, status, created_at DESC;

-- Dashboard view: Tickets by status
CREATE TABLE mv_tickets_by_status AS
SELECT 
    tenant_id,
    status,
    priority,
    COUNT(*) as ticket_count,
    COUNT(*) FILTER (WHERE priority = 'High') as high_priority_count,
    MIN(created_at) as oldest_ticket,
    MAX(updated_at) as last_updated
FROM read_parquet('/shared-ebs/tickets/hot/**/*.parquet')
WHERE closed_at IS NULL
GROUP BY tenant_id, status, priority
ORDER BY tenant_id, status, priority;
```

### Reading from Delta and Base Files

#### 3. Query Delta + Base Pattern

```sql
-- Read from base file only
SELECT * FROM read_parquet('/shared-ebs/tickets/hot/tenant_001/base.parquet')
WHERE ticket_id = 'T-12345';

-- Read from all delta files
SELECT * FROM read_parquet('/shared-ebs/tickets/hot/tenant_001/delta_*.parquet')
WHERE ticket_id = 'T-12345';

-- Merge base + all deltas (MOST COMMON PATTERN)
SELECT * FROM read_parquet([
    '/shared-ebs/tickets/hot/tenant_001/base.parquet',
    '/shared-ebs/tickets/hot/tenant_001/delta_*.parquet'
])
WHERE tenant_id = 1 AND status = 'Open'
ORDER BY updated_at DESC;

-- Get latest version of each ticket
SELECT DISTINCT ON (ticket_id) *
FROM read_parquet([
    '/shared-ebs/tickets/hot/tenant_001/base.parquet',
    '/shared-ebs/tickets/hot/tenant_001/delta_*.parquet'
])
ORDER BY ticket_id, updated_at DESC;

-- Using glob pattern
SELECT * FROM read_parquet('/shared-ebs/tickets/hot/tenant_001/*.parquet')
WHERE ticket_id = 'T-12345'
ORDER BY updated_at DESC
LIMIT 1;
```

#### 4. Advanced Delta Reading with Time Travel

```sql
-- Get ticket state at specific point in time
SELECT * FROM read_parquet('/shared-ebs/tickets/hot/tenant_001/*.parquet')
WHERE ticket_id = 'T-12345' 
  AND updated_at <= '2025-10-01 10:00:00'
ORDER BY updated_at DESC
LIMIT 1;

-- Show all updates to a ticket (audit trail)
SELECT 
    ticket_id,
    status,
    priority,
    assigned_to,
    updated_at,
    LAG(status) OVER (PARTITION BY ticket_id ORDER BY updated_at) as prev_status,
    LAG(priority) OVER (PARTITION BY ticket_id ORDER BY updated_at) as prev_priority
FROM read_parquet('/shared-ebs/tickets/hot/tenant_001/*.parquet')
WHERE ticket_id = 'T-12345'
ORDER BY updated_at;

-- Count updates per ticket in last hour
SELECT 
    ticket_id,
    COUNT(*) as update_count,
    MIN(updated_at) as first_update,
    MAX(updated_at) as last_update
FROM read_parquet('/shared-ebs/tickets/hot/tenant_001/delta_*.parquet')
WHERE updated_at >= NOW() - INTERVAL '1 hour'
GROUP BY ticket_id
HAVING COUNT(*) > 5
ORDER BY update_count DESC;
```

#### 5. Efficient Delta Compaction Queries

```sql
-- Compact base + deltas into new base
COPY (
    SELECT DISTINCT ON (ticket_id) *
    FROM read_parquet('/shared-ebs/tickets/hot/tenant_001/*.parquet')
    ORDER BY ticket_id, updated_at DESC
) TO '/shared-ebs/tickets/hot/tenant_001/base_new.parquet' (
    FORMAT PARQUET,
    ROW_GROUP_SIZE 50000,
    COMPRESSION 'ZSTD',
    COMPRESSION_LEVEL 6
);

-- Verify compaction results
SELECT 
    'base_old' as file,
    COUNT(*) as row_count,
    COUNT(DISTINCT ticket_id) as unique_tickets
FROM read_parquet('/shared-ebs/tickets/hot/tenant_001/base.parquet')
UNION ALL
SELECT 
    'base_new' as file,
    COUNT(*) as row_count,
    COUNT(DISTINCT ticket_id) as unique_tickets
FROM read_parquet('/shared-ebs/tickets/hot/tenant_001/base_new.parquet');
```

### Reading from Hot, Warm, and Cold Tiers

#### 6. Hot Tier Queries (Last 90 Days)

```sql
-- Query only hot tier (fastest)
SELECT * FROM read_parquet('/shared-ebs/tickets/hot/**/*.parquet')
WHERE tenant_id = 1 
  AND status = 'Open'
  AND created_at >= NOW() - INTERVAL '90 days'
ORDER BY created_at DESC;

-- Dashboard: Today's activity
SELECT 
    status,
    COUNT(*) as count,
    COUNT(*) FILTER (WHERE priority = 'High') as high_priority
FROM read_parquet('/shared-ebs/tickets/hot/**/*.parquet')
WHERE tenant_id = 1 
  AND DATE(created_at) = CURRENT_DATE
GROUP BY status;

-- Recent updates in hot tier
SELECT 
    ticket_id,
    ticket_number,
    title,
    status,
    updated_at
FROM read_parquet('/shared-ebs/tickets/hot/tenant_001/*.parquet')
WHERE updated_at >= NOW() - INTERVAL '24 hours'
ORDER BY updated_at DESC;
```

#### 7. Warm Tier Queries (90-365 Days)

```sql
-- Query only warm tier
SELECT * FROM read_parquet('/shared-ebs/tickets/warm/**/*.parquet')
WHERE tenant_id = 1 
  AND closed_at BETWEEN '2025-01-01' AND '2025-03-31'
ORDER BY closed_at DESC;

-- Quarterly report
SELECT 
    category,
    status,
    COUNT(*) as ticket_count,
    AVG(EXTRACT(epoch FROM (closed_at - created_at))/3600) as avg_resolution_hours
FROM read_parquet('/shared-ebs/tickets/warm/tenant_001/2025-Q*.parquet')
WHERE closed_at IS NOT NULL
GROUP BY category, status
ORDER BY ticket_count DESC;

-- Monthly trend analysis
SELECT 
    DATE_TRUNC('month', created_at) as month,
    status,
    COUNT(*) as count
FROM read_parquet('/shared-ebs/tickets/warm/**/*.parquet')
WHERE tenant_id = 1
GROUP BY DATE_TRUNC('month', created_at), status
ORDER BY month DESC, status;
```

#### 8. Cold Tier Queries (>365 Days)

```sql
-- Query only cold tier
SELECT * FROM read_parquet('/shared-ebs/tickets/cold/**/*.parquet')
WHERE tenant_id = 1 
  AND ticket_number = 'T-12345';

-- Yearly historical report
SELECT 
    EXTRACT(year FROM created_at) as year,
    status,
    COUNT(*) as ticket_count
FROM read_parquet('/shared-ebs/tickets/cold/tenant_001/*.parquet')
GROUP BY EXTRACT(year FROM created_at), status
ORDER BY year DESC, status;

-- Compliance/audit query
SELECT 
    ticket_id,
    ticket_number,
    title,
    created_at,
    closed_at,
    EXTRACT(epoch FROM (closed_at - created_at))/86400 as days_to_close
FROM read_parquet('/shared-ebs/tickets/cold/**/*.parquet')
WHERE tenant_id = 1 
  AND created_at >= '2023-01-01'
  AND category = 'Security'
ORDER BY created_at;
```

#### 9. Cross-Tier Queries (Hot + Warm + Cold)

```sql
-- Query across all tiers
SELECT * FROM read_parquet('/shared-ebs/tickets/**/*.parquet')
WHERE tenant_id = 1 AND ticket_number = 'T-12345';

-- Last 2 years of data
SELECT 
    ticket_id,
    ticket_number,
    status,
    priority,
    created_at,
    closed_at
FROM read_parquet([
    '/shared-ebs/tickets/hot/**/*.parquet',
    '/shared-ebs/tickets/warm/**/*.parquet',
    '/shared-ebs/tickets/cold/**/*.parquet'
])
WHERE tenant_id = 1 
  AND created_at >= NOW() - INTERVAL '2 years'
ORDER BY created_at DESC;

-- Aggregation across all tiers
SELECT 
    CASE 
        WHEN created_at >= NOW() - INTERVAL '90 days' THEN 'Hot'
        WHEN created_at >= NOW() - INTERVAL '365 days' THEN 'Warm'
        ELSE 'Cold'
    END as tier,
    COUNT(*) as ticket_count,
    AVG(EXTRACT(epoch FROM (closed_at - created_at))/3600) as avg_resolution_hours
FROM read_parquet('/shared-ebs/tickets/**/*.parquet')
WHERE tenant_id = 1 AND closed_at IS NOT NULL
GROUP BY tier
ORDER BY 
    CASE tier
        WHEN 'Hot' THEN 1
        WHEN 'Warm' THEN 2
        WHEN 'Cold' THEN 3
    END;
```

#### 10. Partition-Pruning Queries

```sql
-- Query single tenant's hot data
SELECT * FROM read_parquet('/shared-ebs/tickets/hot/tenant_001/**/*.parquet')
WHERE status = 'Open';
-- Only reads tenant_001 files

-- Query specific time range
SELECT * FROM read_parquet('/shared-ebs/tickets/hot/tenant_001/2025-Q4-W4*.parquet')
WHERE created_at >= '2025-10-01';
-- Only reads week 40+ files

-- Multi-tenant query
SELECT 
    tenant_id,
    status,
    COUNT(*) as count
FROM read_parquet('/shared-ebs/tickets/hot/tenant_{001,002,003}/**/*.parquet')
WHERE status IN ('Open', 'In Progress')
GROUP BY tenant_id, status;
```

#### 11. Cross-Module Analytics

```sql
-- Join tickets, incidents, and changes
SELECT 
    'ticket' as entity_type,
    t.tenant_id,
    t.ticket_number as entity_number,
    t.status,
    t.created_at,
    t.closed_at
FROM read_parquet('/shared-ebs/tickets/hot/**/*.parquet') t
WHERE t.tenant_id = 1

UNION ALL

SELECT 
    'incident' as entity_type,
    i.tenant_id,
    i.incident_number as entity_number,
    i.status,
    i.created_at,
    i.closed_at
FROM read_parquet('/shared-ebs/incidents/hot/**/*.parquet') i
WHERE i.tenant_id = 1

UNION ALL

SELECT 
    'change' as entity_type,
    c.tenant_id,
    c.change_number as entity_number,
    c.status,
    c.created_at,
    c.actual_end as closed_at
FROM read_parquet('/shared-ebs/changes/hot/**/*.parquet') c
WHERE c.tenant_id = 1

ORDER BY created_at DESC;

-- Complex join: Tickets with incidents and users
SELECT 
    t.ticket_id,
    t.ticket_number,
    t.title,
    t.status as ticket_status,
    i.incident_number,
    i.severity as incident_severity,
    u.first_name || ' ' || u.last_name as assigned_to_name,
    u.department
FROM read_parquet('/shared-ebs/tickets/hot/**/*.parquet') t
LEFT JOIN read_parquet('/shared-ebs/incidents/hot/**/*.parquet') i 
    ON i.related_ticket_id = t.ticket_id
LEFT JOIN read_parquet('/shared-ebs/users/*.parquet') u 
    ON t.assigned_to = u.user_id
WHERE t.tenant_id = 1 
  AND t.status = 'Open'
ORDER BY t.created_at DESC;
```

#### 12. Performance Monitoring Queries

```sql
-- Check file sizes by tier
SELECT 
    'hot' as tier,
    COUNT(*) as file_count,
    SUM(file_size) / (1024*1024*1024) as size_gb
FROM parquet_metadata('/shared-ebs/tickets/hot/**/*.parquet')
UNION ALL
SELECT 
    'warm' as tier,
    COUNT(*) as file_count,
    SUM(file_size) / (1024*1024*1024) as size_gb
FROM parquet_metadata('/shared-ebs/tickets/warm/**/*.parquet')
UNION ALL
SELECT 
    'cold' as tier,
    COUNT(*) as file_count,
    SUM(file_size) / (1024*1024*1024) as size_gb
FROM parquet_metadata('/shared-ebs/tickets/cold/**/*.parquet');

-- Check row group statistics
SELECT 
    filename,
    row_group_id,
    num_rows,
    total_byte_size / (1024*1024) as size_mb
FROM parquet_metadata('/shared-ebs/tickets/hot/tenant_001/base.parquet');

-- Verify data distribution across partitions
SELECT 
    regexp_extract(filename, 'tenant_(\d+)', 1) as tenant_id,
    COUNT(*) as row_count,
    MIN(created_at) as min_date,
    MAX(created_at) as max_date
FROM read_parquet('/shared-ebs/tickets/hot/**/*.parquet', filename=true)
GROUP BY tenant_id
ORDER BY tenant_id;

-- Delta file accumulation check
SELECT 
    regexp_extract(filename, '(base|delta_\d+)', 1) as file_type,
    COUNT(*) as file_count,
    SUM(num_rows) as total_rows
FROM parquet_metadata('/shared-ebs/tickets/hot/tenant_001/*.parquet')
GROUP BY file_type;
```

### Best Practices for DuckDB Queries

```sql
-- ✅ GOOD: Partition-aware query
SELECT * FROM read_parquet('/shared-ebs/tickets/hot/tenant_001/*.parquet')
WHERE status = 'Open';

-- ❌ BAD: Full scan across all partitions
SELECT * FROM read_parquet('/shared-ebs/tickets/**/*.parquet')
WHERE tenant_id = 1;

-- ✅ GOOD: Use DISTINCT ON for deduplication
SELECT DISTINCT ON (ticket_id) *
FROM read_parquet('/shared-ebs/tickets/hot/tenant_001/*.parquet')
ORDER BY ticket_id, updated_at DESC;

-- ✅ GOOD: Filter before join
SELECT t.*, u.first_name
FROM (
    SELECT * FROM read_parquet('/shared-ebs/tickets/hot/**/*.parquet')
    WHERE tenant_id = 1 AND status = 'Open'
) t
JOIN read_parquet('/shared-ebs/users/*.parquet') u ON t.assigned_to = u.user_id;
```

### Common Query Patterns Summary

| Query Pattern | File Path | Use Case | Performance |
|---------------|-----------|----------|-------------|
| Single tenant hot | `/hot/tenant_001/*.parquet` | Recent data | <20ms |
| Delta + base | `/hot/tenant_001/{base,delta_*}.parquet` | Latest version | 20-50ms |
| Cross-tier | `/{hot,warm,cold}/**/*.parquet` | Historical | 100-500ms |
| Partition-aware | `/hot/tenant_001/2025-Q4-W42.parquet` | Specific range | <10ms |
| Materialized view | Pre-computed table | Dashboards | <5ms |

---

## Dynamic Widget Architecture (No Materialized Views)

### Challenge: Dynamic Widget Selection

**User Requirement:**
- Users create custom dashboards with any combination of widgets
- Widget types: charts, tables, metrics, filters selected dynamically
- Each widget may need different columns/aggregations
- Materialized views won't work - can't predict all combinations

**Example Dynamic Widgets:**
```
Dashboard 1:
├── Widget 1: Tickets by Status (needs: status, count)
├── Widget 2: Top Assignees (needs: assigned_to, name, count)
└── Widget 3: SLA Breaches (needs: ticket_id, title, sla_breach, due_date)

Dashboard 2:
├── Widget 1: Category Breakdown (needs: category, subcategory, count)
├── Widget 2: Priority Distribution (needs: priority, count, avg_resolution_time)
└── Widget 3: Recent Updates (needs: ticket_id, title, status, updated_at)
```

### Solution: Column-Family Pattern

**Partition Parquet files by access pattern:**

```
/shared-ebs/tickets/hot/tenant_001/
├── core_columns.parquet
│   ├── ticket_id
│   ├── ticket_number
│   ├── tenant_id
│   ├── status
│   ├── priority
│   ├── created_at
│   └── updated_at
│
├── assignment_columns.parquet
│   ├── ticket_id
│   ├── assigned_to
│   ├── assigned_group
│   ├── assigned_at
│   └── assignee_name (denormalized)
│
├── sla_columns.parquet
│   ├── ticket_id
│   ├── sla_breach
│   ├── due_date
│   ├── response_due_at
│   └── resolution_due_at
│
├── details_columns.parquet
│   ├── ticket_id
│   ├── title
│   ├── description
│   └── resolution
│
└── metadata_columns.parquet
    ├── ticket_id
    ├── category
    ├── subcategory
    ├── tags
    └── custom_fields
```

### Query Examples for Dynamic Widgets

#### Widget 1: Status Distribution (Core only)

```sql
-- Widget needs: status, count
-- Reads: core_columns.parquet only

SELECT 
    status,
    COUNT(*) as ticket_count
FROM read_parquet('/shared-ebs/tickets/hot/tenant_001/core_columns.parquet')
WHERE tenant_id = 1
  AND created_at >= NOW() - INTERVAL '30 days'
GROUP BY status
ORDER BY ticket_count DESC;

-- Performance: 5-15ms (reads only 5MB vs 100MB)
```

#### Widget 2: Top Assignees (Core + Assignment)

```sql
-- Widget needs: assignee_name, count, avg_resolution_time
-- Reads: core_columns + assignment_columns

SELECT 
    a.assignee_name,
    COUNT(*) as ticket_count,
    AVG(EXTRACT(epoch FROM (c.updated_at - c.created_at))/3600) as avg_hours
FROM read_parquet('/shared-ebs/tickets/hot/tenant_001/core_columns.parquet') c
JOIN read_parquet('/shared-ebs/tickets/hot/tenant_001/assignment_columns.parquet') a
    ON c.ticket_id = a.ticket_id
WHERE c.tenant_id = 1
  AND c.created_at >= NOW() - INTERVAL '30 days'
GROUP BY a.assignee_name
ORDER BY ticket_count DESC
LIMIT 10;

-- Performance: 10-25ms (reads 7MB vs 100MB)
```

#### Widget 3: SLA Breaches (Core + SLA + Details)

```sql
-- Widget needs: ticket_number, title, due_date, sla_breach
-- Reads: core + sla + details

SELECT 
    c.ticket_number,
    d.title,
    s.due_date,
    s.sla_breach,
    EXTRACT(epoch FROM (NOW() - s.due_date))/3600 as hours_overdue
FROM read_parquet('/shared-ebs/tickets/hot/tenant_001/core_columns.parquet') c
JOIN read_parquet('/shared-ebs/tickets/hot/tenant_001/sla_columns.parquet') s
    ON c.ticket_id = s.ticket_id
JOIN read_parquet('/shared-ebs/tickets/hot/tenant_001/details_columns.parquet') d
    ON c.ticket_id = d.ticket_id
WHERE c.tenant_id = 1
  AND s.sla_breach = TRUE
  AND c.status != 'Closed'
ORDER BY hours_overdue DESC
LIMIT 20;

-- Performance: 15-35ms (reads 22MB vs 100MB)
```

#### Widget 4: Full Ticket Details (All families)

```sql
-- Widget needs: all fields
-- Reads: all column families

SELECT 
    c.ticket_number,
    c.status,
    c.priority,
    d.title,
    d.description,
    a.assignee_name,
    a.assigned_group,
    s.sla_breach,
    s.due_date,
    m.category,
    m.tags,
    c.created_at
FROM read_parquet('/shared-ebs/tickets/hot/tenant_001/core_columns.parquet') c
LEFT JOIN read_parquet('/shared-ebs/tickets/hot/tenant_001/assignment_columns.parquet') a
    ON c.ticket_id = a.ticket_id
LEFT JOIN read_parquet('/shared-ebs/tickets/hot/tenant_001/sla_columns.parquet') s
    ON c.ticket_id = s.ticket_id
LEFT JOIN read_parquet('/shared-ebs/tickets/hot/tenant_001/details_columns.parquet') d
    ON c.ticket_id = d.ticket_id
LEFT JOIN read_parquet('/shared-ebs/tickets/hot/tenant_001/metadata_columns.parquet') m
    ON c.ticket_id = m.ticket_id
WHERE c.tenant_id = 1
  AND c.created_at >= NOW() - INTERVAL '7 days'
ORDER BY c.created_at DESC
LIMIT 50;

-- Performance: 40-80ms (reads 30MB vs 100MB)
```

### Column Family Design Principles

**Grouping Strategy:**

**Core Family:**
- Access frequency: 95%
- Columns: ticket_id, tenant_id, status, priority, created_at, updated_at
- Size: ~5MB per 100K tickets
- Purpose: Essential filtering and sorting

**Assignment Family:**
- Access frequency: 60%
- Columns: ticket_id, assigned_to, assigned_group, assignee_name, assigned_at
- Size: ~2MB per 100K tickets
- Purpose: Assignment tracking

**SLA Family:**
- Access frequency: 30%
- Columns: ticket_id, sla_breach, due_date, response_due_at, resolution_due_at
- Size: ~2MB per 100K tickets
- Purpose: SLA monitoring

**Details Family:**
- Access frequency: 40%
- Columns: ticket_id, title, description, resolution
- Size: ~15MB per 100K tickets
- Purpose: Content display

**Metadata Family:**
- Access frequency: 25%
- Columns: ticket_id, category, subcategory, tags, custom_fields
- Size: ~8MB per 100K tickets
- Purpose: Categorization

### Performance Comparison

**Single File vs Column Families:**

**Widget needs: status + priority + count**

Single File:
- Reads: tickets_full.parquet (100 MB, 50 columns)
- Latency: 80-120ms

Column Families:
- Reads: core_columns.parquet (5 MB, 8 columns)
- Latency: 5-15ms
- Improvement: 95% less I/O, 8x faster

**Complex Widget (4 families):**

Single File:
- Reads: 100 MB
- Latency: 80-120ms

Column Families:
- Reads: 24 MB (core + assignment + sla + details)
- Latency: 20-40ms
- Improvement: 76% less I/O, 3x faster

### Caching Strategy for Dynamic Widgets

**In-Memory Widget Result Cache:**

Configuration:
- Cache Type: In-memory hash map with LRU eviction
- Key: Query hash (columns, filters, aggregations)
- TTL: Dynamic based on widget type
- Max Size: 1000 cached queries per instance

**TTL Strategy:**
- Real-time widgets: 30 seconds
- Aggregated metrics: 5 minutes
- List views: 2 minutes
- Historical reports: 15 minutes

**Cache Invalidation:**
- Automatic: TTL expiration
- Manual: When underlying data changes
- Smart: Invalidate related queries
- Selective: Only affected tenant queries

### Query Optimization for Multi-Family Joins

**DuckDB Automatic Optimization:**

When querying multiple column families:
1. Reads only needed columns from each file
2. Uses hash joins for ticket_id
3. Pushes down filters before join
4. Parallelizes file reads
5. Applies zone map pruning

**Query Performance:**
- Single family: 5-15ms
- Two families: 15-25ms
- Three families: 25-40ms
- All families: 40-80ms

### Column Family Update Strategy

**Update Propagation:**

When ticket updated:
1. Update PostgreSQL
2. Identify changed column families
3. Update only changed families in DuckDB
4. Publish event to NATS with family info
5. Other instances update caches
6. Periodically flush to Parquet deltas

**Change Detection:**
- Core: status, priority, updated_at
- Assignment: assigned_to, assigned_group
- SLA: sla_breach, due_date
- Details: title, description, resolution
- Metadata: category, tags, custom_fields

### Widget Query API Design

**Request Structure:**
- Tenant ID (multi-tenant isolation)
- Widget type (chart, table, metric)
- Columns needed (determines families)
- Filters (WHERE conditions)
- Aggregations (COUNT, SUM, AVG)
- Group by columns
- Time range
- Limit/offset

**Response Structure:**
- Data results (array of records)
- Metadata (query time, cache hit)
- Column families accessed
- Query plan summary

### Performance Characteristics

**Widget Query Performance:**

| Widget Type | Families | Columns | Latency | I/O | Cache Hit |
|-------------|----------|---------|---------|-----|-----------|
| Simple count | 1 (core) | 2-3 | 5-10ms | 5 MB | 90% |
| Status chart | 1 (core) | 3-5 | 8-15ms | 5 MB | 85% |
| Assignee list | 2 | 5-7 | 12-25ms | 7 MB | 75% |
| SLA dashboard | 3 | 8-12 | 20-40ms | 22 MB | 60% |
| Full details | 5 | 20-30 | 40-80ms | 30 MB | 40% |

**Cache Hit Rates:**

Dashboard Initial Load:
- First visit: 0% (cold start)
- Reload: 85-90%
- Widget change: 60-70%
- Filter change: 30-40%

**Combined Performance:**

Average Response Times:
- Cache hit: 2-5ms
- Cache miss, simple: 5-15ms (1 family)
- Cache miss, medium: 15-35ms (2-3 families)
- Cache miss, complex: 40-80ms (4-5 families)
- Overall average: 10-20ms (with 70% cache hit)

### Data Freshness Guarantees

**Real-Time Updates:**
- Local instance: Immediate (read-your-writes)
- Other instances: 100-500ms (via NATS)
- Widget cache: 30 seconds to 15 minutes (TTL)
- Dashboard auto-refresh: Configurable (default 1 minute)

**Consistency Levels:**

Strong Consistency:
- PostgreSQL reads: Always current
- Same instance reads: Immediate after write
- Use case: Critical operations

Eventual Consistency:
- Cross-instance: <500ms lag
- Widget cache: Up to TTL delay
- Use case: Dashboards, reports

### Column Family Maintenance

**Creation and Updates:**

Initial Setup:
1. Export data from PostgreSQL
2. Split into column families
3. Sort by tenant_id, status, created_at
4. Write to Parquet with optimal row groups
5. Create metadata files

Regular Updates:
1. Accumulate changes in delta files
2. Hourly: Merge small deltas
3. Daily: Compact into main files
4. Weekly: Full optimization pass
5. Monthly: Tier data to warm/cold

**File Management:**

Hot Tier (0-90 days):
- Write frequency: High (every 5 minutes)
- File count: 50-100 files per tenant
- Compaction: Hourly
- Access pattern: Random, frequent

Warm Tier (90-365 days):
- Write frequency: Low (migrations only)
- File count: 10-20 files per tenant
- Compaction: Weekly
- Access pattern: Sequential, occasional

Cold Tier (>365 days):
- Write frequency: Rare (archival)
- File count: 1-5 files per tenant
- Compaction: Monthly
- Access pattern: Sequential, rare

### Summary: Column Families vs Materialized Views

**Column Families Approach:**

Advantages:
- ✅ Supports unlimited widget combinations
- ✅ Reads only necessary columns (5-50% of data)
- ✅ Sub-50ms for most queries
- ✅ No pre-computation needed
- ✅ Automatically stays fresh
- ✅ DuckDB handles joins efficiently
- ✅ Scales linearly with data
- ✅ Simple maintenance

Disadvantages:
- ⚠️ Multi-family joins add 10-30ms overhead
- ⚠️ Requires query builder abstraction
- ⚠️ More complex than single file

**Materialized Views Approach:**

Advantages:
- ✅ Very fast for pre-computed queries (1-5ms)
- ✅ Simple to query

Disadvantages:
- ❌ Must pre-compute every possible view
- ❌ Combinatorial explosion
- ❌ High refresh overhead
- ❌ Staleness issues
- ❌ Cannot support ad-hoc queries
- ❌ Storage explosion
- ❌ Maintenance nightmare

**Conclusion:**

For dynamic widget selection, column-family architecture is the only practical solution. It provides:

1. **Flexibility:** Query any field combination without pre-computation
2. **Performance:** 5-80ms by reading only needed families (vs 80-120ms full table)
3. **Freshness:** Always current via event-driven updates
4. **Scalability:** Linear scaling with data volume
5. **Maintainability:** Simple Parquet file management

The overhead of multi-family joins (10-30ms) is negligible compared to the flexibility gained.

---

## Performance Targets

### Operational Queries (Per-Service DuckDB)
- Hot data: **2-10ms** (in-memory)
- Cold data: **50-200ms** (load from Parquet)
- Cache hit rate: **90-95%**
- Throughput: **10K+ queries/sec per service**

### Analytical Queries (Central DuckDB)
- Single table aggregation: **100-500ms**
- Cross-service joins: **200-1000ms**
- Complex reports: **1-5 seconds**
- Concurrent users: **50-100**

### Dynamic Widget Queries
- Simple widgets (1 column family): **5-15ms**
- Medium widgets (2-3 families): **15-35ms**
- Complex widgets (4-5 families): **40-80ms**
- Widget cache hit: **70-80%**
- Cached widget response: **2-5ms**

### System-Wide Metrics
- Write latency: **5-15ms** (PostgreSQL only)
- Event propagation: **<100ms** (NATS delivery)
- Cache consistency: **<1 second** (eventual)
- Recovery time: **<5 minutes** (load from Parquet)

---

## Cost Analysis

### Infrastructure Costs (100K tickets/month, 50 tenants)

**Per-Service Operational Cache:**
- Data volume: 9M records (90 days)
- Storage: ~3GB Parquet per service
- Memory: 200-500MB per instance
- Cost: **$50-100/month** (included in app servers)

**Centralized Analytics:**
- Data volume: 24M records (2 years)
- Storage: ~50GB Parquet (denormalized)
- Memory: 2-4GB dedicated instance
- Cost: **$200-400/month** (dedicated instance)

**Shared EBS Storage:**
- Hot tier: 20GB per service
- Warm tier: 50GB per service
- Cold tier: 100GB per service
- Cost: **$20-40/month** (gp3 storage)

**Total: $270-540/month**

**Cost Comparison:**
- Traditional analytics DB (Snowflake/BigQuery): $2,000-5,000/month
- Pure in-memory solution: $800-1,200/month
- **Savings: 64-75% vs alternatives**

### Cost Breakdown by Component

| Component | Monthly Cost | Annual Cost |
|-----------|-------------|-------------|
| PostgreSQL (RDS Multi-AZ) | $150-200 | $1,800-2,400 |
| Per-Service DuckDB (3 services) | $150-300 | $1,800-3,600 |
| Analytics Service | $200-400 | $2,400-4,800 |
| EBS Storage (gp3) | $60-120 | $720-1,440 |
| NATS Cluster (3 nodes) | $100-150 | $1,200-1,800 |
| Monitoring & Logging | $50-100 | $600-1,200 |
| **Total** | **$710-1,270** | **$8,520-15,240** |

**vs Traditional Stack:**
- PostgreSQL + Redis + ClickHouse: $2,500-4,000/month
- **Savings: 65-72%**

---

## Implementation Phases

### Phase 1: Foundation (Weeks 1-3)
**Goal:** Get core microservices operational with basic caching

**Tasks:**
1. Set up PostgreSQL with proper schemas and partitioning
2. Implement NATS event bus with JetStream
3. Create per-service DuckDB embedded cache
4. Implement basic Parquet write/read functionality
5. Deploy shared EBS volume with proper mount points
6. Set up hot/warm/cold tier structure
7. Implement basic event-driven cache updates

**Deliverables:**
- Ticket, Incident, Change services operational
- Basic CRUD operations with PostgreSQL
- Simple event publishing via NATS
- DuckDB caching for hot data (in-memory)
- Parquet persistence on shared EBS
- Basic monitoring and health checks

**Success Criteria:**
- All services can read/write to PostgreSQL
- NATS events propagate within 100ms
- DuckDB cache hit rate >70%
- Services can load cache from Parquet on restart

### Phase 2: Analytics Layer (Weeks 4-6)
**Goal:** Enable cross-service analytics and reporting

**Tasks:**
1. Deploy centralized Analytics Service with dedicated DuckDB
2. Implement federated Parquet queries across services
3. Build column-family architecture for dynamic widgets
4. Create reporting API endpoints
5. Implement hot/warm/cold tiering with automatic data movement
6. Set up cross-service join queries
7. Implement delta file compaction

**Deliverables:**
- Cross-module analytics queries working
- Dashboard backend operational with widget API
- Historical data accessible across all tiers
- Tiered storage active with automatic lifecycle
- Column families optimized for common access patterns
- Parquet compaction running on schedule

**Success Criteria:**
- Cross-service queries complete in <500ms
- Hot tier queries <20ms
- Warm tier queries <200ms
- Cold tier queries <500ms
- Data automatically moves between tiers

### Phase 3: Optimization (Weeks 7-10)
**Goal:** Optimize performance and add advanced features

**Tasks:**
1. Tune cache hit rates and memory usage per service
2. Implement intelligent prefetching for hot data
3. Add query result caching with smart TTL
4. Optimize Parquet file sizes and row groups
5. Implement write coalescing and batching
6. Set up comprehensive monitoring and alerting
7. Implement rate limiting and backpressure
8. Optimize zone maps and partitioning
9. Configure auto-scaling policies

**Deliverables:**
- 90%+ cache hit rate achieved
- <10ms p95 operational query latency
- <50ms p95 analytical query latency
- Comprehensive monitoring dashboard
- Auto-scaling policies configured
- Write optimization (80% reduction in operations)
- Column-family queries optimized

**Success Criteria:**
- Cache hit rate >90%
- p95 query latency <10ms (operational)
- p95 query latency <50ms (analytics)
- Zero-downtime deployments working
- Auto-scaling responds within 2 minutes
- EBS IOPS reduced by 80%

### Phase 4: Advanced Analytics (Weeks 11+)
**Goal:** Enable ML and predictive analytics

**Tasks:**
1. Build real-time streaming pipelines with NATS
2. Implement ML feature stores in Parquet
3. Add predictive models (ticket escalation, SLA breach prediction)
4. Create recommendation engines for ticket routing
5. Deploy anomaly detection for unusual patterns
6. Implement advanced reporting with custom visualizations
7. Add real-time dashboard updates via WebSocket

**Deliverables:**
- ML pipelines operational
- Predictive analytics in production
- Real-time anomaly detection
- Advanced reporting capabilities
- Custom widget builder for users
- Real-time dashboard updates

**Success Criteria:**
- ML models predict SLA breaches with >85% accuracy
- Anomaly detection catches issues within 5 minutes
- Real-time dashboards update within 1 second
- Users can create custom widgets without developer help

---

## Key Benefits

### Performance
✅ **2-10ms latency** for operational queries (hot data in-memory)  
✅ **90-95% cache hit rate** reduces PostgreSQL load  
✅ **Fast crash recovery** (load from Parquet vs rebuilding)  
✅ **No cache warm-up penalty** on service restart  
✅ **5-80ms dynamic widget queries** based on complexity  
✅ **Automatic query optimization** via DuckDB zone maps

### Cost Efficiency
✅ **64-75% cheaper** than traditional analytics databases  
✅ **Shared EBS eliminates** data duplication  
✅ **Pay only for hot data** in RAM  
✅ **Column families reduce I/O** by 70-95%  
✅ **Tiered storage** optimizes cost vs performance  
✅ **No separate analytics DB** licensing costs

### Operational Simplicity
✅ **Embedded DuckDB** - no external cache servers  
✅ **Automatic cache management** - hot/warm/cold tiering  
✅ **Event-driven consistency** - no complex sync logic  
✅ **Self-healing** - services rebuild cache from Parquet  
✅ **Zero-downtime deployments** with rolling updates  
✅ **Single technology stack** - less operational overhead

### Scalability
✅ **Horizontal service scaling** - add more instances  
✅ **Independent analytics scaling** - dedicated resources  
✅ **Multi-tenant support** - partition-based isolation  
✅ **Supports 100+ tenants** with consistent performance  
✅ **Dynamic widget support** - unlimited combinations  
✅ **Column-family architecture** - scales with data growth

### Flexibility
✅ **Dynamic widget queries** - no pre-computation needed  
✅ **Ad-hoc analytics** - query any data combination  
✅ **Time-travel queries** - historical data analysis  
✅ **Cross-service analytics** - federated queries  
✅ **Extensible architecture** - easy to add new services

---

## Trade-offs and Considerations

### Advantages Over Alternatives

**vs Pure PostgreSQL:**
- ✅ 10-50x faster queries (in-memory cache)
- ✅ Reduced PostgreSQL load by 90%
- ✅ Better analytical query performance
- ✅ No complex indexing strategy needed
- ✅ Handles high read volume without scaling PostgreSQL

**vs Redis/Dragonfly Only:**
- ✅ Complex analytical queries support
- ✅ Lower infrastructure costs (no separate cache cluster)
- ✅ Persistent cache (survives restarts)
- ✅ SQL-based queries (easier development)
- ✅ Automatic query optimization

**vs ClickHouse/Snowflake:**
- ✅ 75% cost savings
- ✅ Simpler architecture (embedded, not separate DB)
- ✅ No separate analytics database to manage
- ✅ No data ETL pipelines needed
- ✅ Real-time data (no batch loading)

**vs Materialized Views:**
- ✅ Supports unlimited dynamic widget combinations
- ✅ No pre-computation overhead
- ✅ Always fresh data
- ✅ No combinatorial explosion of views
- ✅ Simpler maintenance

### Disadvantages and Mitigations

**Eventual Consistency**
- ⚠️ Cache lags behind PostgreSQL (<1 second)
- ✅ **Mitigation:** Read-your-writes consistency per service
- ✅ **Mitigation:** Explicit refresh for critical queries
- ✅ **Mitigation:** NATS ensures <100ms propagation

**Operational Complexity**
- ⚠️ More components to manage vs single database
- ✅ **Mitigation:** Automated cache management
- ✅ **Mitigation:** Comprehensive monitoring and alerting
- ✅ **Mitigation:** Self-healing capabilities
- ✅ **Mitigation:** Kubernetes handles orchestration

**EBS I/O Limits**
- ⚠️ Shared EBS has IOPS limits
- ✅ **Mitigation:** Hot data in-memory (90-95% cache hit)
- ✅ **Mitigation:** gp3 volumes with provisioned IOPS
- ✅ **Mitigation:** Staggered cache loading
- ✅ **Mitigation:** Write coalescing reduces operations by 80%

**Query Complexity**
- ⚠️ Multi-family joins can be complex
- ✅ **Mitigation:** DuckDB optimizes joins automatically
- ✅ **Mitigation:** Query builder abstracts complexity
- ✅ **Mitigation:** Pre-joined denormalized data for common cases

**Learning Curve**
- ⚠️ Team needs to learn DuckDB and Parquet
- ✅ **Mitigation:** DuckDB uses standard SQL
- ✅ **Mitigation:** Extensive documentation and examples
- ✅ **Mitigation:** Training sessions for team
- ✅ **Mitigation:** Similar to existing SQL knowledge

---

## Monitoring Strategy

### Key Metrics

**Per-Service Metrics:**
- Cache hit rate (target: >90%)
- Query latency (p50, p95, p99)
- Memory usage and limits
- Parquet file size and count
- NATS event lag
- DuckDB query execution time
- Row groups scanned vs total
- Column families accessed per query

**Analytics Service Metrics:**
- Query execution time by complexity
- Concurrent query count
- EBS IOPS utilization
- Memory pressure and GC pauses
- Cross-service join performance
- Widget cache hit rate
- Dynamic query compilation time

**System-Wide Metrics:**
- PostgreSQL connection pool utilization
- PostgreSQL query latency
- NATS message throughput
- NATS consumer lag
- Total storage used on EBS (by tier)
- Service-to-service event latency
- Overall system throughput (requests/sec)
- Error rates and types

**Parquet Metrics:**
- File count by tier (hot/warm/cold)
- Delta file accumulation
- Compaction duration
- Row group efficiency
- Compression ratios
- Zone map pruning effectiveness

### Alerting Thresholds

**Critical Alerts:**
- Cache hit rate drops below 85% for 5+ minutes
- p95 latency exceeds 50ms for operational queries
- PostgreSQL connection pool >80% utilized
- EBS IOPS throttling detected
- NATS consumer lag exceeds 1 second
- Service instance down for 1+ minute
- Memory usage >90%
- Disk space >85% on EBS

**Warning Alerts:**
- Cache hit rate below 90% for 10+ minutes
- Memory usage >70% for 10+ minutes
- Parquet files not updated in 5+ minutes
- NATS event lag >500ms
- Analytics queries >5 seconds (p95)
- Delta files >20 per tenant
- Compaction lagging >2 hours
- Error rate >1% of requests

**Informational Alerts:**
- New service deployment started
- Scaling event triggered
- Tier migration completed
- Large query detected (>10 seconds)
- Cache refresh completed
- Backup completed successfully

### Dashboards

**Operations Dashboard:**
- Service health status grid
- Request rate and latency charts
- Error rate trends
- Cache hit rate by service
- NATS message flow visualization
- Resource utilization (CPU, memory, disk)

**Performance Dashboard:**
- Query latency heatmaps
- Slow query analysis
- Cache performance breakdown
- Parquet I/O statistics
- Zone map pruning effectiveness
- Row group scan efficiency

**Business Dashboard:**
- Active users and tenants
- Data volume by tenant
- Query patterns and trends
- Widget usage statistics
- Most expensive queries
- Data growth projections

**Capacity Planning Dashboard:**
- Storage growth trends
- Memory usage projections
- IOPS utilization over time
- Query volume forecasts
- Tenant onboarding impact

---

## Security Considerations

### Data Protection
- ✅ **PostgreSQL:** RBAC and row-level security policies
- ✅ **EBS Volumes:** Encrypted at rest using AWS KMS
- ✅ **NATS:** TLS 1.3 encryption for event transmission
- ✅ **Service-to-Service:** mTLS authentication via Istio
- ✅ **API Gateway:** OAuth 2.0 / JWT token validation
- ✅ **Secrets:** Stored in AWS Secrets Manager, rotated regularly

### Multi-Tenant Isolation
- ✅ **Database Level:** Tenant-scoped queries enforced
- ✅ **Parquet Files:** Tenant-partitioned storage
- ✅ **Row-Level Security:** Enforced in services
- ✅ **API Level:** Tenant ID validation on every request
- ✅ **Cache Pools:** Separate in-memory pools per tenant
- ✅ **Network:** Isolated VPCs per environment

### Access Control
- ✅ **IAM Roles:** Principle of least privilege
- ✅ **Service Accounts:** Kubernetes RBAC
- ✅ **API Authentication:** OAuth 2.0 with scopes
- ✅ **Database Access:** Separate read/write users
- ✅ **SSH Access:** Disabled, use AWS Systems Manager
- ✅ **Audit Logging:** All access logged to CloudWatch

### Compliance
- ✅ **Audit Logs:** All data access logged in PostgreSQL
- ✅ **GDPR:** Data deletion support with cascading deletes
- ✅ **Retention Policies:** Automatic archival and deletion
- ✅ **Data Residency:** Region-specific deployments
- ✅ **Encryption:** At rest and in transit everywhere
- ✅ **SOC 2:** Compliance-ready architecture

### Vulnerability Management
- ✅ **Container Scanning:** Automated with Trivy
- ✅ **Dependency Scanning:** Dependabot alerts
- ✅ **Patch Management:** Regular updates, auto-patching for minor versions
- ✅ **Penetration Testing:** Quarterly external audits
- ✅ **Security Monitoring:** AWS GuardDuty and CloudTrail

---

## Disaster Recovery

### Backup Strategy

**PostgreSQL Backups:**
- Continuous WAL archiving to S3
- Daily full backups at 2 AM UTC
- Point-in-time recovery up to 7 days
- Retention: 30 days for daily, 12 months for monthly
- Automated restore testing weekly

**Parquet Files:**
- EBS snapshots every 6 hours
- Cross-region replication to DR region
- Retention: 7 days for hourly, 30 days for daily
- Lifecycle policy to move to Glacier after 90 days

**NATS State:**
- JetStream snapshots every hour
- Retained for 24 hours
- Replay capability from any point in last 24 hours

**Configuration:**
- Git repository for all infrastructure code
- Kubernetes manifests versioned in Git
- Secrets backed up in AWS Secrets Manager
- Service configurations in etcd, backed up daily

### Recovery Procedures

**Single Service Failure:**
- RTO: 3-6 minutes (Kubernetes auto-restart)
- RPO: 0 (no data loss, cache rebuilds from Parquet)
- Procedure: Kubernetes automatically restarts pod, service loads cache from Parquet

**PostgreSQL Failure:**
- RTO: <60 seconds (automatic failover to standby)
- RPO: 0 (synchronous replication)
- Procedure: RDS Multi-AZ automatic failover

**EBS Volume Failure:**
- RTO: 15-30 minutes (restore from snapshot)
- RPO: <6 hours (latest snapshot)
- Procedure: Mount latest snapshot, services reload cache

**Complete Region Failure:**
- RTO: 15-30 minutes (manual failover to DR region)
- RPO: <15 minutes (PostgreSQL WAL replication)
- Procedure: Update DNS to DR region, restore PostgreSQL from backup, mount EBS snapshots

**Data Corruption:**
- RTO: 30-60 minutes (restore and validate)
- RPO: Up to point of corruption detection
- Procedure: Stop services, restore PostgreSQL to point-in-time before corruption, restore Parquet files, replay NATS events

### Testing Schedule
- Monthly: Single service failure drill
- Quarterly: Regional failover drill
- Semi-annually: Complete DR scenario test
- Annually: Data corruption recovery test

---

## Conclusion

The **Hybrid DuckDB Cache + Analytics Architecture on Shared EBS** represents the optimal solution for the ITSM platform, delivering:

### Key Achievements

1. **Exceptional Performance**
    - Sub-10ms operational queries with 90-95% cache hit rates
    - Dynamic widget queries in 5-80ms without pre-computation
    - Cross-service analytics in 200-500ms
    - Column-family architecture reduces I/O by 70-95%

2. **Cost Efficiency**
    - 64-75% cheaper than traditional analytics databases
    - 80% reduction in EBS IOPS through write optimization
    - No separate cache cluster or analytics database needed
    - Shared EBS eliminates data duplication

3. **Operational Simplicity**
    - Embedded caching with automatic management
    - Self-healing capabilities with Parquet persistence
    - Zero-downtime deployments with rolling updates
    - Event-driven consistency via NATS

4. **Scalability**
    - Supports 100+ tenants with partition-based isolation
    - Horizontal scaling of all components
    - Independent scaling of operational vs analytical workloads
    - Automatic hot/warm/cold tiering

5. **Flexibility**
    - Unlimited dynamic widget combinations
    - Ad-hoc analytics without pre-computation
    - Time-travel queries for historical analysis
    - Easy to add new services and modules

### Production Readiness

This architecture provides:

- ✅ Complete high availability with 99.95% uptime target
- ✅ Comprehensive monitoring and alerting
- ✅ Disaster recovery with RTO <30 minutes
- ✅ Security and compliance ready (GDPR, SOC 2)
- ✅ Clear 10-week implementation roadmap
- ✅ Cost-effective at $270-540/month vs $2,000-5,000 for alternatives

### Path Forward

The architecture supports growth from:
- **MVP:** 10 tenants, 10K tickets/month, 3 services
- **Scale:** 100+ tenants, 1M tickets/month, 10+ services
- **Enterprise:** 1000+ tenants, 10M+ tickets/month, 50+ services

All without fundamental architectural changes, only scaling existing components.

---

## Appendix

### Glossary

**Terms:**
- **DuckDB:** Embedded analytical database optimized for OLAP queries
- **Parquet:** Columnar storage format optimized for analytics
- **Zone Maps:** Min/max statistics per column per row group in Parquet
- **Row Group:** Unit of data organization in Parquet (typically 50K-100K rows)
- **Column Family:** Logical grouping of related columns stored together
- **Hot/Warm/Cold Tiers:** Data lifecycle stages based on access frequency
- **NATS JetStream:** Persistent message streaming layer in NATS
- **EBS gp3:** AWS Elastic Block Store general purpose SSD storage
- **IOPS:** Input/Output Operations Per Second (storage performance metric)
- **Widget:** Configurable dashboard component for data visualization
- **Materialized View:** Pre-computed query result stored as table
- **Zone Map Pruning:** Skipping row groups based on min/max statistics
- **Eventual Consistency:** Data becomes consistent after short delay
- **Read-Your-Writes:** Immediate consistency for same user/session

### References

**Documentation:**
- DuckDB: https://duckdb.org/docs/
- Apache Parquet: https://parquet.apache.org/docs/
- NATS: https://docs.nats.io/
- PostgreSQL: https://www.postgresql.org/docs/
- Kubernetes: https://kubernetes.io/docs/

**Research Papers:**
- "DuckDB: an Embeddable Analytical Database" (SIGMOD 2019)
- "Dremel: Interactive Analysis of Web-Scale Datasets" (VLDB 2010)
- "Parquet: Columnar Storage for the Apache Hadoop Ecosystem"

**Best Practices:**
- "Designing Data-Intensive Applications" by Martin Kleppmann
- "Building Microservices" by Sam Newman
- "Site Reliability Engineering" by Google

**Document Status:** ✅ **COMPLETE - Approved for Implementation**

**Document Prepared By:** Hardik Vala  
**Approved By:** Alpesh Dhamelia

---

**END OF DOCUMENT**

*This document contains the complete architecture specification for the ITSM Microservice Platform with Hybrid DuckDB Cache + Analytics on Shared EBS. All sections are finalized and ready for stakeholder review and implementation.*
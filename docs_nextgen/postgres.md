# NextGen Database Architecture Decisions

**Document Version:** 1.0  
**Date:** October 3, 2025

---

## Executive Summary

This document outlines the key architectural decisions for the NextGen application, focusing on database design, multi-tenancy strategy, indexing approach, microservices communication patterns, and high availability configuration.

---

## 1. Database Technology

**Decision:** PostgreSQL on AWS RDS

**Rationale:**
- Mature, reliable RDBMS with strong ACID compliance
- Excellent performance for transactional workloads
- Rich indexing capabilities (B-tree, GIN, BRIN)
- Strong ecosystem and community support
- AWS RDS provides managed service with Multi-AZ and automated backups
- No custom encoding requirements at this stage

---

## 2. Dynamic Field Management

**Decision:** M1/M2 Sparse Column Pattern

**Implementation:**
- **50 String Columns:** `m1_str_1` through `m1_str_50`
- **50 Numeric Columns:** `m2_num_1` through `m2_num_50`
- **Mapping Table:** Links request types to specific m1/m2 columns with business field names

**Schema Structure:**
```sql
CREATE TABLE tenant_xxx.tickets (
    -- Fixed columns
    id BIGSERIAL PRIMARY KEY,
    ticket_number VARCHAR(50) UNIQUE NOT NULL,
    request_type_id INT NOT NULL,
    title VARCHAR(500) NOT NULL,
    priority VARCHAR(20),
    assigned_to INT,
    created_at TIMESTAMP DEFAULT CURRENT_TIMESTAMP,
    updated_at TIMESTAMP DEFAULT CURRENT_TIMESTAMP,
    
    -- Dynamic string fields
    m1_str_1 VARCHAR(500),
    m1_str_2 VARCHAR(500),
    m1_str_3 VARCHAR(500),
    -- ... up to m1_str_50
    
    -- Dynamic numeric fields
    m2_num_1 NUMERIC(18,4),
    m2_num_2 NUMERIC(18,4),
    m2_num_3 NUMERIC(18,4),
    -- ... up to m2_num_50
);
```

**Advantages:**
- Direct column access for high performance
- No joins required for field retrieval
- Type safety maintained (string vs numeric)
- Simple, standard SQL queries
- Indexable columns when needed

**Considerations:**
- Expected NULL density is acceptable for PostgreSQL
- Metadata mapping table is critical for field management
- Column allocation monitoring required

---

## 3. Multi-Tenancy Strategy

**Decision:** Schema-Level Isolation

**Implementation:**
- Each tenant gets a dedicated PostgreSQL schema
- Identical table structures across all tenant schemas
- Schema naming convention: `tenant_{identifier}`
- Complete data isolation at the schema level

**Example:**
```sql
CREATE SCHEMA tenant_acme;
CREATE SCHEMA tenant_globex;
CREATE SCHEMA tenant_initech;

-- Each with identical structure
CREATE TABLE tenant_acme.tickets (...);
CREATE TABLE tenant_globex.tickets (...);
CREATE TABLE tenant_initech.tickets (...);
```

**Benefits:**
1. **Complete Data Isolation:** Zero risk of cross-tenant data leaks
2. **Performance:** No tenant_id filtering overhead in queries
3. **Compliance:** Simplified GDPR, HIPAA, SOC2 compliance
4. **Backup/Restore:** Tenant-specific operations
5. **Customization:** Schema-level modifications possible per tenant
6. **Resource Management:** Can allocate dedicated resources if needed
7. **Security:** Database-level isolation enforced by PostgreSQL

**Implementation Requirements:**
- Connection pool configuration with `search_path` management
- Schema provisioning automation for new tenants
- Migration scripts applicable across all schemas
- Shared schema for cross-tenant metadata
- Every database connection MUST set correct `search_path` to maintain isolation

**Shared Metadata Schema:**
```sql
CREATE SCHEMA shared;

CREATE TABLE shared.tenants (
    tenant_id UUID PRIMARY KEY,
    schema_name VARCHAR(63) UNIQUE,
    created_at TIMESTAMP,
    is_active BOOLEAN DEFAULT TRUE
);

CREATE TABLE shared.field_mappings (
    tenant_id UUID,
    request_type_id INT,
    field_name VARCHAR(100),
    column_name VARCHAR(20),  -- e.g., 'm1_str_5'
    data_type VARCHAR(10),    -- 'string' or 'numeric'
    is_required BOOLEAN,
    display_order INT,
    validation_rules TEXT
);
```

---

## 4. Indexing Strategy

**Decision:** Composite and Specialized Indexes with Performance-Based Tuning

**Approach:**
- Start with essential indexes
- Add indexes based on actual query patterns and performance data
- Use appropriate index types for different use cases
- Monitor index usage and bloat regularly
- Apply indexes consistently across all tenant schemas

### 4.1 Primary Indexes

```sql
-- Automatic from PRIMARY KEY
-- id column is automatically indexed

CREATE UNIQUE INDEX idx_tickets_number 
ON tenant_xxx.tickets(ticket_number);
```

### 4.2 Core Query Indexes

```sql
-- Priority-based routing (removed status column)
CREATE INDEX idx_tickets_priority_created 
ON tenant_xxx.tickets(priority, created_at DESC);

-- Assignment and workload queries
CREATE INDEX idx_tickets_assigned 
ON tenant_xxx.tickets(assigned_to, created_at DESC) 
WHERE assigned_to IS NOT NULL;

-- Request type filtering
CREATE INDEX idx_tickets_request_type 
ON tenant_xxx.tickets(request_type_id, created_at DESC);
```

### 4.3 Time-Series Indexes (BRIN)

```sql
-- BRIN indexes for efficient range queries on timestamps
CREATE INDEX idx_tickets_created_brin 
ON tenant_xxx.tickets USING BRIN (created_at);

CREATE INDEX idx_tickets_updated_brin 
ON tenant_xxx.tickets USING BRIN (updated_at);

CREATE INDEX idx_tickets_due_date_brin 
ON tenant_xxx.tickets USING BRIN (due_date) 
WHERE due_date IS NOT NULL;
```

**BRIN Benefits:**
- Tiny index footprint (pages vs rows)
- Excellent for sequential/time-series data
- Perfect for created_at, updated_at columns

### 4.4 Dynamic Field Indexes (M1/M2)

```sql
-- Index only frequently queried m1/m2 columns
-- Example: m1_str_1 mapped to 'category'
CREATE INDEX idx_tickets_m1_str_1 
ON tenant_xxx.tickets(m1_str_1) 
WHERE m1_str_1 IS NOT NULL;

-- Composite with request type
CREATE INDEX idx_tickets_reqtype_m1_str_1 
ON tenant_xxx.tickets(request_type_id, m1_str_1) 
WHERE m1_str_1 IS NOT NULL;

-- Numeric range queries (e.g., impact score)
CREATE INDEX idx_tickets_m2_num_1 
ON tenant_xxx.tickets(m2_num_1) 
WHERE m2_num_1 IS NOT NULL;
```

### 4.5 Array/Collection Indexes

```sql
-- GIN indexes for array columns (watchers, tags)
CREATE INDEX idx_tickets_watchers 
ON tenant_xxx.tickets USING GIN (watchers);

CREATE INDEX idx_tickets_tags 
ON tenant_xxx.tickets USING GIN (tags);
```

### 4.6 Full-Text Search (If Required)

```sql
-- Title search
CREATE INDEX idx_tickets_title_fts 
ON tenant_xxx.tickets USING GIN (to_tsvector('english', title));

-- Combined search on title and description
CREATE INDEX idx_tickets_search_fts 
ON tenant_xxx.tickets USING GIN (
    to_tsvector('english', 
        coalesce(title,'') || ' ' || coalesce(description,'')
    )
);
```

### 4.7 Indexing Best Practices

1. **Start Minimal:** Create only essential indexes initially
2. **Monitor Usage:** Use `pg_stat_user_indexes` to track index effectiveness
3. **Partial Indexes:** Use WHERE clauses to reduce index size
4. **Performance-Based:** Add indexes based on slow query analysis
5. **Regular Maintenance:** Monitor and manage index bloat
6. **Per-Tenant Tuning:** Some tenants may need specialized indexes
7. **Schema Consistency:** Apply same indexing strategy across all tenant schemas

---

## 5. Data Compression

**Decision:** Deferred - No Custom Encoding

**Current Status:**
- PostgreSQL's native compression and storage optimization is sufficient
- Custom column-level encoding (RLE, Delta, Bitmap) not supported in PostgreSQL
- Will rely on PostgreSQL's TOAST and internal compression mechanisms

**Future Consideration:**
- If compression becomes critical, evaluate:
    - PostgreSQL columnar extensions (Citus columnar)
    - Hybrid architecture (PostgreSQL + ClickHouse for analytics)
    - TimescaleDB for time-series compression

---

## 6. Microservices Architecture

**Decision:** NATS-Based Service Communication

**Architecture Pattern:**
```
Ticket Service → NATS → Database Service → PostgreSQL
```

### 6.1 Communication Flow

**Request/Reply Pattern:**
- Ticket Service sends create/update requests via NATS
- Database Service receives requests, executes queries
- Response sent back through NATS

**NATS Subject Naming Convention:**
```
db.tickets.create
db.tickets.update
db.tickets.query
db.tickets.delete
db.tickets.bulk_update
```

### 6.2 Database Service Responsibilities

1. **Schema Routing:** Manage `SET search_path = tenant_xxx` for complete schema-level isolation
2. **Connection Pooling:** Efficient connection management per tenant schema
3. **Query Execution:** All database operations with tenant context
4. **Transaction Handling:** ACID compliance within tenant boundaries
5. **Error Management:** Retry logic and failure handling
6. **Schema Validation:** Ensure all operations respect schema-level isolation

### 6.3 Advantages

- Centralized database access control
- Connection pool optimization
- Separation of concerns
- Easier monitoring and logging
- Service-level security boundaries
- Schema-level isolation enforced at service layer

### 6.4 Considerations

**Latency:**
- Additional network hop vs direct connection
- Acceptable tradeoff for architecture benefits

**Reliability:**
- NATS availability critical
- Implement proper timeout and retry mechanisms
- Circuit breaker patterns for resilience

**Connection Management:**
- Database Service handles tenant schema routing with strict isolation
- Pool configuration per tenant schema or dynamic search_path switching
- Connection lifecycle management with schema context
- Prevent cross-schema queries through validation

---

## 7. High Availability & Replication (AWS RDS PostgreSQL)

**Decision:** AWS RDS PostgreSQL with Multi-AZ Deployment

### 7.1 High Availability Configuration

**Multi-AZ Deployment:**
- Synchronous replication to a standby instance in a different Availability Zone
- Automatic failover without manual intervention
- Typical failover time: 60-120 seconds
- Single endpoint - no application changes required during failover
- All tenant schemas replicated synchronously

**Architecture:**
```
Primary AZ (us-east-1a)          Standby AZ (us-east-1b)
┌──────────────────┐            ┌──────────────────┐
│   RDS Primary    │ ══════════>│  RDS Standby     │
│   (Active)       │ Sync Repl  │  (Passive)       │
│  All Schemas     │            │  All Schemas     │
└──────────────────┘            └──────────────────┘
         │                               │
         └───────────────┬───────────────┘
                         │
                 Single DNS Endpoint
              (automatic failover)
```

**Automatic Failover Triggers:**
- Loss of availability in primary AZ
- Loss of network connectivity to primary
- Compute unit failure on primary
- Storage failure on primary
- Manual failover initiated via console/API

**Benefits:**
- **RTO (Recovery Time Objective):** ~60-120 seconds
- **RPO (Recovery Point Objective):** Zero data loss (synchronous replication)
- No data loss during failover
- Automatic DNS endpoint update
- Maintenance with minimal downtime
- All tenant schemas protected equally

### 7.2 Read Replicas

**Configuration:**
- Up to 15 read replicas per primary instance
- Asynchronous replication from primary
- Can be in same region or cross-region
- Independent compute and storage
- All tenant schemas replicated to read replicas

**Use Cases:**
1. **Reporting & Analytics:** Offload read-heavy queries
2. **Geographic Distribution:** Low-latency reads in different regions
3. **Backup Source:** Create snapshots from replica
4. **Disaster Recovery:** Promote replica to standalone instance

**Read Replica Setup:**
```
                Primary Instance (Multi-AZ)
                   (All Tenant Schemas)
                        │
        ┌───────────────┼───────────────┐
        │               │               │
   Read Replica 1  Read Replica 2  Read Replica 3
   (us-east-1)     (us-west-2)     (eu-west-1)
   [Analytics]     [Regional]      [DR/Backup]
```

**Replication Lag Monitoring:**
- CloudWatch metric: `ReplicaLag`
- Typical lag: < 1 second (depends on workload)
- Alert on lag > 30 seconds

### 7.3 Backup Strategy

**Automated Backups:**
- Daily automated snapshots during backup window
- Retention period: 1-35 days (configurable)
- Point-in-time recovery to any second within retention period
- Transaction logs backed up every 5 minutes to S3
- All tenant schemas included in backups

**Manual Snapshots:**
- User-initiated snapshots
- Retained indefinitely until explicitly deleted
- Can be shared across AWS accounts
- Can be copied across regions

**Backup Architecture:**
```
RDS Primary → Transaction Logs → S3 (every 5 mins)
     │
     └─→ Daily Snapshot → S3 (retention: 7-35 days)
```

### 7.4 RDS Configuration for NextGen

**Recommended Setup:**

```yaml
Instance Configuration:
  - Instance Class: db.r6g.xlarge (or larger based on load)
  - Engine: PostgreSQL 15.x or latest
  - Multi-AZ: Enabled
  - Storage: Provisioned IOPS SSD (io2)
  - Allocated Storage: 500 GB (auto-scaling enabled)
  - Max Allocated Storage: 2000 GB
  
High Availability:
  - Multi-AZ Deployment: Yes
  - Automatic Failover: Enabled
  - Failover Priority: Tier-0 (highest)

Backup Configuration:
  - Automated Backups: Enabled
  - Backup Retention: 14 days
  - Backup Window: 03:00-04:00 UTC
  - Copy Tags to Snapshots: Yes
  
Maintenance:
  - Auto Minor Version Upgrade: Yes
  - Maintenance Window: Sunday 04:00-05:00 UTC
  
Performance Insights:
  - Enabled: Yes
  - Retention: 7 days (free tier)

Enhanced Monitoring:
  - Granularity: 60 seconds
  - Monitoring Role: Auto-created
```

### 7.5 Read Replica Strategy for Multi-Tenant

**Schema-Level Isolation on Read Replicas:**
- Read replicas replicate ALL tenant schemas
- Application-level routing maintains schema isolation
- Each read query must specify correct schema context via `search_path`

**Per-Tenant Read Replicas (if needed):**
- Create dedicated read replicas for high-traffic tenants
- Route tenant read queries to dedicated replica with schema context
- Application-level routing based on tenant_id → schema mapping

**Connection Routing with Schema Isolation:**
```javascript
// Pseudo-code for connection routing
function getDbConnection(tenantId, operation) {
  const schemaName = getTenantSchema(tenantId); // e.g., 'tenant_acme'
  
  if (operation === 'READ' && isHighTrafficTenant(tenantId)) {
    const conn = getReadReplicaConnection(tenantId);
    conn.query(`SET search_path = ${schemaName}, public`);
    return conn;
  }
  
  const conn = getPrimaryConnection();
  conn.query(`SET search_path = ${schemaName}, public`);
  return conn;
}
```

**Important:** Every database connection must set the correct `search_path` to maintain schema-level isolation and prevent cross-tenant data access.

### 7.6 Disaster Recovery Plan

**Cross-Region Read Replica:**
- Maintain read replica in different AWS region
- Can be promoted to standalone instance
- RTO: 5-15 minutes (promotion time)
- RPO: Replication lag (typically < 5 seconds)
- All tenant schemas replicated to DR region

**DR Failover Process:**
1. Monitor primary region health
2. If region failure detected:
    - Promote cross-region replica to standalone
    - Update application DNS/endpoints
    - Redirect traffic to new primary
    - Verify schema-level isolation maintained
3. Once primary region recovers:
    - Assess data consistency
    - Re-establish replication
    - Plan failback strategy

### 7.7 Monitoring & Alerts

**Critical CloudWatch Metrics:**
- `DatabaseConnections` - connection pool usage
- `CPUUtilization` - compute capacity
- `FreeableMemory` - memory pressure
- `ReadLatency` / `WriteLatency` - I/O performance
- `ReplicaLag` - replication health
- `DiskQueueDepth` - storage bottleneck indicator

**Alerting Thresholds:**
```yaml
Alerts:
  - CPU > 80% for 5 minutes: Warning
  - CPU > 90% for 2 minutes: Critical
  - DatabaseConnections > 80% of max: Warning
  - ReplicaLag > 30 seconds: Warning
  - ReplicaLag > 60 seconds: Critical
  - FreeStorageSpace < 10 GB: Warning
  - FreeStorageSpace < 5 GB: Critical
```

### 7.8 Cost Optimization

**Strategies:**
- Use Graviton (ARM) instances (db.r6g) for 40% cost savings
- Right-size instances based on CloudWatch metrics
- Use Provisioned IOPS only when necessary
- Archive old snapshots to S3 Glacier
- Delete unnecessary read replicas
- Schedule dev/test instances to stop during off-hours

---

## 8. Exclusions

**What We're NOT Using:**

1. **No JSONB columns** for dynamic fields
    - Better performance with structured columns
    - Type safety maintained
    - Easier to index and query

2. **No custom compression** at this stage
    - PostgreSQL native compression sufficient
    - Premature optimization avoided

3. **No row-level multi-tenancy**
    - Schema-level isolation chosen for security and performance

---

## 9. Future Considerations

### 9.1 Monitoring & Observability
- Query performance tracking per tenant schema
- Index usage statistics per schema
- Connection pool metrics
- NATS message latency
- Schema-level performance analytics

### 9.2 Scaling Strategy
- Vertical scaling per tenant (larger instances for high-traffic tenants)
- Read replicas for reporting workloads (maintaining schema isolation)
- Schema-level sharding: distribute tenant schemas across multiple RDS instances for extreme scale
- Each shard maintains complete schema-level isolation

### 9.3 Optimization Opportunities
- Materialized views for complex reporting queries
- Partition tables by date for high-volume tenants (maintaining schema-level isolation)
- Caching layer (Dragonfly or DuckDB based on performance testing)
    - **Dragonfly**: Drop-in Redis replacement with better performance and memory efficiency
    - **DuckDB**: For analytical query caching and OLAP workloads
    - Cache invalidation strategy per tenant schema
    - Cache key structure to maintain tenant isolation: `tenant_{schema}:{cache_key}`

---

## 10. Success Metrics

**Performance KPIs:**
- Query response time < 100ms for 95th percentile
- Index hit ratio > 95%
- Connection pool utilization < 80%
- NATS message latency < 10ms

**Operational KPIs:**
- Zero cross-tenant data leaks
- Schema provisioning time < 5 minutes
- Index bloat < 20%
- Uptime > 99.9%
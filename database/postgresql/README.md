# PostgreSQL Schema for Ticket Management System

This directory contains the PostgreSQL database schema and related files for the ticket management system, designed to replace the existing DynamoDB implementation.

## Files Overview

- `schema.sql` - Complete PostgreSQL table schema with indexes
- `sample_data.sql` - Sample data insertion and common queries
- `migration.sql` - Migration utilities and data conversion helpers
- `README.md` - This documentation file

## Schema Features

### Auto-Increment Primary Key
- Uses `BIGSERIAL` for the primary key `id` field
- Automatically generates unique IDs for new tickets
- Maintains compatibility with existing ticket numbering

### Data Types
- **Timestamp fields**: `BIGINT` (Unix timestamps in milliseconds)
- **ID fields**: `BIGINT` for foreign keys and references
- **Boolean fields**: `BOOLEAN` with appropriate defaults
- **Text fields**: `VARCHAR` for limited text, `TEXT` for long content
- **Numeric fields**: `INTEGER` for levels and counts, `BIGINT` for durations

### Clustered Indexing Strategy
The schema uses a clustered indexing approach that groups related fields by business domain to reduce write burden and improve query performance:

#### Essential Primary Indexes
- `ticket_id` (unique) - Primary ticket lookup
- `tenant` - Multi-tenant isolation
- `createdtime` - Chronological sorting and range queries

#### Business Domain Clusters (10 clustered indexes)
1. **Request Identity**: `(tenant, requesterid, technicianid, groupid, departmentid, createdbyid)`
2. **SLA Tracking**: `(tenant, dueby, firstresponsetime, responsedue, resolutionescalationtime, lastviolationtime)`
3. **Status Lifecycle**: `(tenant, statusid, statuschangedtime, lastopenedtime, lastresolvedtime, lastclosedtime)`
4. **Priority Impact**: `(tenant, priorityid, urgencyid, impactid, supportlevel, approvalstatus)`
5. **OLA Tracking**: `(tenant, oladueby, oladuelevel, olaescalationtime, lastolaviolationtime)`
6. **UC Tracking**: `(tenant, ucdueby, ucduelevel, ucescalationtime, lastucviolationtime)`
7. **Timing Durations**: `(tenant, totalonholdduration, totalresolutiontime, totalslapausetime, totalworkingtime, reopened)`
8. **Feedback Closure**: `(tenant, closedby, resolvedby, askfeedbackdate, firstfeedbackdate, lastapproveddate)`
9. **Category Templates**: `(tenant, categoryid, templateid, servicecatalogid, requesttype, suggestedcategoryid)`
10. **Integration Misc**: `(tenant, companyid, vendorid, emailreadconfigid, messengerconfigid)`

#### High-Performance Composite Indexes
- `(requesterid, statusid, priorityid)` - User ticket management
- `(technicianid, statusid, createdtime)` - Technician workload tracking
- `(groupid, statusid, dueby)` - Team SLA management
- `(companyid, categoryid, statusid)` - Company service analytics
- `(createdtime, statusid, priorityid)` - Time-based priority queries

#### Benefits of Clustered Indexing
- **Reduced Write Overhead**: ~60% fewer indexes (17 vs 49+)
- **Better Query Performance**: Single index satisfies multi-field queries
- **Lower Storage Overhead**: More efficient index storage
- **Logical Organization**: Indexes grouped by business domain

## Usage Instructions

### 1. New Database Setup
```sql
-- Create database
CREATE DATABASE ticket_management;

-- Connect to database
\c ticket_management;

-- Run schema creation with clustered indexes
\i schema.sql
```

### 2. Migrating Existing Database
```sql
-- For existing databases, use the migration script
\i migrate_to_clustered_indexes.sql

-- The migration script will:
-- 1. Create new clustered indexes alongside existing ones
-- 2. Allow verification of query performance
-- 3. Provide option to drop old individual indexes
```

### 2. Sample Data
```sql
-- Insert sample data and run test queries
\i sample_data.sql
```

### 3. Data Migration (from DynamoDB)
```sql
-- Run migration utilities
\i migration.sql
```

## Key Differences from DynamoDB

### Primary Key Strategy
- **DynamoDB**: Composite key (pk, createdtime)
- **PostgreSQL**: Auto-increment BIGSERIAL primary key
- **Benefit**: Simpler queries, better performance for single-record lookups

### Indexing Strategy
- **DynamoDB**: Global Secondary Indexes (GSI) for searchable fields
- **PostgreSQL**: B-tree indexes on frequently queried columns
- **Benefit**: More flexible query patterns, better join performance

### Data Types
- **DynamoDB**: Everything stored as strings for GSI compatibility
- **PostgreSQL**: Native data types for better performance and validation
- **Benefit**: Type safety, better storage efficiency, native date/time operations

## Common Query Patterns

### 1. Single Ticket Lookup
```sql
SELECT * FROM tickets WHERE id = 12345;
```

### 2. User's Tickets
```sql
SELECT id, name, subject, statusid, createdtime 
FROM tickets 
WHERE requesterid = 7193 
ORDER BY createdtime DESC;
```

### 3. Technician Workload
```sql
SELECT id, name, priorityid, dueby 
FROM tickets 
WHERE technicianid = 6971 AND statusid NOT IN (12, 13)
ORDER BY priorityid DESC, dueby ASC;
```

### 4. SLA Monitoring
```sql
SELECT id, name, dueby, statusid
FROM tickets 
WHERE dueby > 0 AND dueby < EXTRACT(EPOCH FROM NOW()) * 1000
ORDER BY dueby ASC;
```

### 5. Multi-Tenant Queries
```sql
SELECT COUNT(*) as ticket_count, statusid
FROM tickets 
WHERE companyid = 123
GROUP BY statusid;
```

## Performance Considerations

### Index Maintenance
- Indexes are automatically maintained by PostgreSQL
- Use `ANALYZE tickets;` periodically for query optimization
- Monitor index usage with `pg_stat_user_indexes`

### Query Optimization
- Use `EXPLAIN ANALYZE` to analyze query performance
- Consider partial indexes for filtered queries
- Use appropriate `LIMIT` clauses for large result sets

### Connection Pooling
- Implement connection pooling (e.g., PgBouncer) for high-concurrency applications
- Configure appropriate `max_connections` in PostgreSQL

## Migration from DynamoDB

### Data Export
1. Export DynamoDB data to JSON/CSV format
2. Transform timestamp fields (ensure millisecond precision)
3. Convert boolean strings to actual boolean values
4. Handle null/empty values appropriately

### Field Mapping
All CSV fields map directly to PostgreSQL columns with appropriate type conversion:
- String IDs → BIGINT
- Timestamp strings → BIGINT (Unix milliseconds)
- Boolean strings → BOOLEAN
- Text fields → VARCHAR/TEXT

### Validation
- Verify data integrity after migration
- Run sample queries to ensure correct results
- Compare record counts between systems

## Maintenance

### Regular Tasks
- `VACUUM ANALYZE tickets;` - Reclaim space and update statistics
- Monitor index usage and remove unused indexes
- Archive old tickets to separate tables if needed

### Backup Strategy
- Use `pg_dump` for logical backups
- Consider point-in-time recovery with WAL archiving
- Test restore procedures regularly

### Monitoring
- Track query performance with `pg_stat_statements`
- Monitor table and index sizes
- Set up alerts for long-running queries

## Security Considerations

### Access Control
- Create specific database users for applications
- Grant minimal required permissions
- Use connection encryption (SSL/TLS)

### Data Protection
- Implement row-level security for multi-tenant isolation
- Consider column-level encryption for sensitive data
- Regular security updates for PostgreSQL

## Troubleshooting

### Common Issues
1. **Slow queries**: Check index usage with `EXPLAIN ANALYZE`
2. **Lock contention**: Monitor `pg_locks` table
3. **Storage growth**: Regular `VACUUM` and archiving strategy

### Performance Tuning
- Adjust `shared_buffers`, `work_mem`, `maintenance_work_mem`
- Configure appropriate `checkpoint_segments`
- Monitor and tune `random_page_cost` and `seq_page_cost`

This PostgreSQL schema provides a robust, scalable foundation for the ticket management system with improved query flexibility and performance compared to the DynamoDB implementation.

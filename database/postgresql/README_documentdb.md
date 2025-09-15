# PostgreSQL DocumentDB Hybrid Storage with BSON

This implementation provides a hybrid approach combining the best of relational and document database paradigms for ticket storage using PostgreSQL's BSON support.

## Architecture Overview

### Hybrid Schema Design with BSON

The implementation uses a **hybrid approach**:

1. **Fixed Schema**: Common, frequently-queried fields stored as regular PostgreSQL columns
2. **Dynamic BSON Fields**: Less common and custom fields stored as BSON for flexibility and performance
3. **Organized BSON Storage**: Multiple BSON columns for different field categories
4. **Performance Optimized**: Uses appropriate indexes for both fixed and BSON data

### Schema Structure

```sql
CREATE TABLE ticket_hybrid (
    -- DocumentDB style primary key
    _id bson PRIMARY KEY DEFAULT generate_objectid(),
    
    -- Fixed schema (commonly queried fields)
    ticket_id VARCHAR(255) UNIQUE NOT NULL,
    created_at TIMESTAMP DEFAULT CURRENT_TIMESTAMP,
    updated_at TIMESTAMP DEFAULT CURRENT_TIMESTAMP,
    requesterid BIGINT,
    technicianid BIGINT,
    statusid BIGINT,
    priorityid BIGINT,
    subject VARCHAR(500),
    description TEXT,
    -- ... other common fields
    
    -- Dynamic BSON fields (organized by category)
    dynamic_fields bson DEFAULT '{}',     -- General dynamic fields
    user_fields bson DEFAULT '{}',        -- User-related data
    timing_fields bson DEFAULT '{}',      -- Timing/SLA data
    workflow_fields bson DEFAULT '{}',    -- Workflow/approval data
    custom_fields bson DEFAULT '{}',      -- Custom organization fields
    
    schema_version INTEGER DEFAULT 1
);
```

### BSON Field Organization

The BSON fields are organized into logical categories:

#### user_fields BSON:
```json
{
    "updatedbyid": 2001,
    "removedbyid": 2002,
    "closedby": 2003,
    "resolvedby": 2001
}
```

#### timing_fields BSON:
```json
{
    "totalonholdduration": 3600000,
    "totalresolutiontime": 7200000,
    "firstresponsetime": 1703123400000,
    "lastclosedtime": 1703130600000
}
```

#### workflow_fields BSON:
```json
{
    "approvalstatus": 1,
    "approvaltype": 2,
    "supportlevel": 3,
    "resolutionduelevel": 1
}
```

#### dynamic_fields BSON:
```json
{
    "name": "Server Issue",
    "originaldescription": "Original user description",
    "viprequest": true,
    "slaviolated": false,
    "impactid": 3
}
```

#### custom_fields BSON:
```json
{
    "customfield_department": "IT Operations",
    "customfield_severity": 1,
    "customfield_escalated": false
}
```

## Key Features

### 1. Performance Optimization

- **Fixed fields**: Use B-tree indexes for fast queries on common fields
- **Dynamic fields**: Use GIN indexes for efficient JSONB queries
- **Hybrid queries**: Combine both for optimal performance

### 2. Field Mapping

The storage layer automatically maps protobuf fields to the appropriate storage:

```go
// Fixed Schema Fields (fast queries)
FixedFields: map[string]string{
    "requesterid":  "requesterid",
    "statusid":     "statusid",
    "priorityid":   "priorityid",
    "subject":      "subject",
    "description":  "description",
    // ... more common fields
}

// Dynamic Fields (flexible schema)
DynamicFields: map[string]string{
    "name":               "text_fields.name",
    "viprequest":         "boolean_fields.viprequest",
    "totalonholdduration": "number_fields.totalonholdduration",
    // ... less common fields
}
```

### 3. Search Capabilities

#### Fixed Field Searches (Fast)
```go
searchRequest := SearchRequest{
    Conditions: []SearchCondition{
        {Operand: "statusid", Operator: "eq", Value: 2},
        {Operand: "priorityid", Operator: "gte", Value: 1},
    },
}
```

#### Dynamic Field Searches (Flexible)
```go
searchRequest := SearchRequest{
    Conditions: []SearchCondition{
        {Operand: "viprequest", Operator: "eq", Value: true},
        {Operand: "name", Operator: "contains", Value: "Server"},
    },
}
```

#### Hybrid Searches (Best of Both)
```go
searchRequest := SearchRequest{
    Conditions: []SearchCondition{
        {Operand: "statusid", Operator: "ne", Value: 4},        // Fixed field
        {Operand: "viprequest", Operator: "eq", Value: true},   // Dynamic field
    },
}
```

## Usage Examples

### Basic Operations

```go
// Initialize storage
storage, err := NewPostgreSQLDocumentDBStorage(ctx, "ticket_hybrid", connectionString)

// Create ticket
ticketData := &ticketpb.TicketData{
    Fields: map[string]*ticketpb.FieldValue{
        // Fixed schema fields
        "statusid":    {Value: &ticketpb.FieldValue_IntValue{IntValue: 1}},
        "priorityid":  {Value: &ticketpb.FieldValue_IntValue{IntValue: 2}},
        "subject":     {Value: &ticketpb.FieldValue_StringValue{StringValue: "Issue"}},
        
        // Dynamic fields
        "viprequest":  {Value: &ticketpb.FieldValue_BoolValue{BoolValue: true}},
        "customfield": {Value: &ticketpb.FieldValue_StringValue{StringValue: "Custom"}},
    },
}
err, result := storage.CreateTicket(ticketData)

// Search tickets
results, err := storage.SearchTickets(searchRequest)

// Get specific ticket
ticket, found := storage.GetTicket("TKT-123", nil)
```

## Migration

### From Fixed Schema (PostgreSQL)

1. Run the migration script:
```sql
SELECT migrate_fixed_to_hybrid() as migrated_count;
```

2. Verify data integrity:
```sql
SELECT COUNT(*) FROM ticket_hybrid;
```

### From EAV Schema

1. Run the EAV migration:
```sql
SELECT migrate_eav_to_hybrid() as migrated_count;
```

2. Verify field mappings are correct

## Performance Characteristics

### Query Performance

| Operation | Fixed Fields | Dynamic Fields | Hybrid |
|-----------|-------------|----------------|--------|
| Equality | Excellent | Good | Excellent |
| Range | Excellent | Good | Excellent |
| Text Search | Good | Good | Good |
| Complex Queries | Excellent | Good | Excellent |
| Sorting | Excellent | Good | Excellent |

### Storage Efficiency

- **Fixed fields**: Optimal space usage with proper data types
- **Dynamic fields**: Compressed JSONB with minimal overhead
- **Overall**: ~15-25% more efficient than pure EAV

### Index Usage

```sql
-- Fixed field indexes (B-tree)
CREATE INDEX idx_ticket_hybrid_status_priority ON ticket_hybrid(statusid, priorityid);

-- JSONB indexes (GIN)
CREATE INDEX idx_ticket_hybrid_dynamic_fields_gin ON ticket_hybrid USING GIN (dynamic_fields);

-- Specialized JSONB indexes
CREATE INDEX idx_ticket_hybrid_dynamic_text ON ticket_hybrid USING GIN ((dynamic_fields->'text_fields'));
```

## Best Practices

### Field Placement Strategy

**Put in Fixed Schema:**
- Frequently queried fields (statusid, priorityid, requesterid)
- Fields used in JOINs or complex WHERE clauses
- Fields that need range queries or sorting
- Core business fields that rarely change

**Put in Dynamic Fields:**
- Custom fields that vary by organization
- Metadata and audit fields
- Fields that change frequently in structure
- Optional or rarely queried fields

### Query Optimization

1. **Filter on fixed fields first** to reduce result set
2. **Use appropriate operators** for JSONB queries
3. **Create specialized indexes** for common JSONB query patterns
4. **Monitor query performance** and adjust field placement

### Schema Evolution

1. **Use schema_version** field for migrations
2. **Add new fields to dynamic_fields** by default
3. **Promote frequently queried dynamic fields** to fixed schema
4. **Deprecate unused fields** gradually

## Advanced JSONB Queries

### Containment Queries
```sql
-- Check if ticket has specific boolean flags
WHERE dynamic_fields @> '{"boolean_fields": {"viprequest": true}}';

-- Check multiple conditions
WHERE dynamic_fields @> '{"number_fields": {"priorityid": 1}, "boolean_fields": {"slaviolated": false}}';
```

### Path Queries
```sql
-- Get text field value
WHERE dynamic_fields->'text_fields'->>'name' = 'Server Issue';

-- Numeric comparison
WHERE (dynamic_fields->'number_fields'->>'duration')::bigint > 1000;

-- Check field existence
WHERE dynamic_fields->'text_fields' ? 'custom_department';
```

### Full-Text Search
```sql
-- Search across all dynamic content
WHERE to_tsvector('english', dynamic_fields::text) @@ to_tsquery('server & critical');
```

## Monitoring and Maintenance

### Performance Monitoring

```sql
-- Check index usage
SELECT schemaname, tablename, indexname, idx_scan, idx_tup_read, idx_tup_fetch
FROM pg_stat_user_indexes 
WHERE tablename = 'ticket_hybrid';

-- Query performance analysis
EXPLAIN ANALYZE SELECT * FROM ticket_hybrid 
WHERE statusid = 1 AND dynamic_fields->'boolean_fields'->>'viprequest' = 'true';
```

### Maintenance Tasks

```sql
-- Update statistics
ANALYZE ticket_hybrid;

-- Reindex JSONB indexes periodically
REINDEX INDEX idx_ticket_hybrid_dynamic_fields_gin;

-- Check table size
SELECT pg_size_pretty(pg_total_relation_size('ticket_hybrid'));
```

## Comparison with Other Approaches

| Feature | Fixed Schema | EAV | Hybrid | Document DB |
|---------|-------------|-----|--------|-------------|
| Query Performance | Excellent | Poor | Excellent | Good |
| Schema Flexibility | Poor | Excellent | Good | Excellent |
| Storage Efficiency | Good | Poor | Good | Good |
| Complexity | Low | High | Medium | Medium |
| ACID Guarantees | Excellent | Excellent | Excellent | Varies |
| Tooling Support | Excellent | Good | Excellent | Good |

## Conclusion

The PostgreSQL DocumentDB hybrid approach provides:

1. **Performance** of relational databases for common queries
2. **Flexibility** of document databases for evolving schemas  
3. **ACID guarantees** and mature tooling of PostgreSQL
4. **Cost efficiency** by avoiding separate document database infrastructure

This makes it ideal for applications that need both structured and semi-structured data with high performance requirements.
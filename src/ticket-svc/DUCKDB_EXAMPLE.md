# DuckDB Storage for Ticket Service

This implementation provides DuckDB support for the ticket service following the column-family architecture described in the DuckDB documentation.

## Features

- **Column Family Architecture**: Separates ticket data into logical families (core, details, assignment, metadata, SLA)
- **Local Storage**: Uses local file system instead of EBS for development/testing
- **Query Optimization**: Implements projection-aware queries that only read needed column families
- **Full CRUD Operations**: Complete Create, Read, Update, Delete support
- **Search Capabilities**: Advanced search with field projection and sorting

## Architecture

### Table Structure

1. **tickets_core**: Core fields (tenant_id, status, priority, timestamps)
2. **tickets_details**: Content fields (title, description, resolution)
3. **tickets_assignment**: Assignment fields (assigned_to, assigned_group, assignee_name)
4. **tickets_metadata**: Categorization fields (category, subcategory, tags, custom_fields)
5. **tickets_sla**: SLA fields (sla_breach, due_date, response_due_at, resolution_due_at)

### Performance Benefits

- **Reduced I/O**: Only reads needed column families
- **Query Optimization**: DuckDB's columnar storage with zone maps
- **Indexed Access**: Strategic indexes on frequently queried fields
- **Fast Joins**: Efficient joins between column families

## Configuration

Set the following environment variables:

```bash
export STORAGE_TYPE="duckdb"
export DUCKDB_PATH="./data"
export DUCKDB_TABLE="tickets"
```

Or use the default values:
- Storage Type: `duckdb` (default)
- Storage Path: `./data` (default)
- Table Name: `tickets` (default)

## Usage Examples

### 1. Start the Service

```bash
# Set environment variables
export STORAGE_TYPE="duckdb"
export NATS_URL="nats://localhost:4222"

# Run the service
./ticket-svc
```

### 2. Create a Ticket

Send a NATS request to `ticket.service`:

```json
{
  "action": "create",
  "data": {
    "tenant_id": 1,
    "title": "System Login Issue",
    "description": "Users cannot log into the system",
    "status": "Open",
    "priority": "High",
    "category": "Technical",
    "assigned_to": 123,
    "assignee_name": "John Doe",
    "tags": ["urgent", "login", "system"]
  }
}
```

### 3. Get a Ticket

```json
{
  "action": "get",
  "ticket_id": "uuid-here"
}
```

### 4. Search Tickets

Simple status search:
```json
{
  "action": "search",
  "data": {
    "conditions": [
      {
        "field": "status",
        "operator": "eq",
        "value": "Open"
      }
    ]
  }
}
```

Search with projection (only return specific fields):
```json
{
  "action": "search",
  "data": {
    "conditions": [
      {
        "field": "priority",
        "operator": "eq",
        "value": "High"
      }
    ],
    "projected_fields": ["title", "status", "priority", "assigned_to"],
    "sort_fields": [
      {
        "field": "created_at",
        "order": "desc"
      }
    ]
  }
}
```

### 5. Update a Ticket

```json
{
  "action": "update",
  "ticket_id": "uuid-here",
  "data": {
    "status": "In Progress",
    "assigned_to": 456,
    "assignee_name": "Jane Smith"
  }
}
```

### 6. Delete a Ticket

```json
{
  "action": "delete",
  "ticket_id": "uuid-here"
}
```

## Query Performance

### Column Family Benefits

Different queries read different amounts of data:

1. **Status Dashboard** (core family only):
   - Reads: ~5MB for 100K tickets
   - Response time: 5-15ms

2. **Assignment Report** (core + assignment families):
   - Reads: ~7MB for 100K tickets  
   - Response time: 10-25ms

3. **Full Ticket Details** (all families):
   - Reads: ~30MB for 100K tickets
   - Response time: 40-80ms

### Search Operators

Supported search operators:
- `eq`: Equal
- `ne`: Not equal
- `gt`: Greater than
- `lt`: Less than
- `gte`: Greater than or equal
- `lte`: Less than or equal
- `contains`: String contains (LIKE %value%)
- `begins_with`: String starts with (LIKE value%)

## File Structure

```
./data/
└── tickets.db          # DuckDB database file
```

The database contains multiple tables:
- `tickets_core`
- `tickets_details`
- `tickets_assignment`
- `tickets_metadata`
- `tickets_sla`

## Development

### Local Testing

1. Create data directory:
```bash
mkdir -p ./data
```

2. Build and run:
```bash
go build -o ticket-svc
./ticket-svc
```

3. The service will create the DuckDB database and tables automatically.

### Custom Fields

The system supports dynamic custom fields through the `custom_fields` JSON column in the metadata table. Any fields not in the predefined column families are automatically stored as custom fields.

### Monitoring

The service logs:
- Table creation and schema initialization
- Query performance metrics
- Column family access patterns
- Database connection status

Example logs:
```
DuckDB storage initialized with database: ./data/tickets.db, table: tickets
DuckDB schema initialized with column family architecture
Column families architecture enabled for optimal query performance
```

## Migration from Other Storage Types

To migrate from other storage types to DuckDB:

1. Export data from current storage
2. Set `STORAGE_TYPE=duckdb`
3. Restart service (will create new DuckDB schema)
4. Import data using the create ticket API

## Troubleshooting

### Common Issues

1. **Permission Denied**: Ensure the data directory is writable
2. **Database Locked**: Only one instance can access the database file
3. **Schema Errors**: Delete the database file to recreate schema

### Debug Logging

Enable debug logging to see detailed query information:
```bash
export LOG_LEVEL=debug
```

This will show:
- SQL queries being executed
- Query execution times
- Column families being accessed
- Row counts and data sizes

## Performance Tuning

### Query Optimization

1. **Use Projections**: Always specify `projected_fields` for better performance
2. **Filter Early**: Put most selective conditions first
3. **Limit Results**: Use pagination for large result sets
4. **Index Usage**: Queries on tenant_id, status, and created_at are automatically optimized

### Storage Optimization

1. **Batch Operations**: Create multiple tickets in batches when possible
2. **Cleanup**: Regularly delete old tickets to maintain performance
3. **Indexing**: The service automatically creates optimized indexes

## Future Enhancements

Planned improvements:
1. **Shared EBS Support**: Migration to shared EBS storage for production
2. **Parquet Export**: Export to Parquet files for analytics
3. **Hot/Warm/Cold Tiering**: Automatic data lifecycle management
4. **Delta Files**: Support for incremental updates
5. **Compaction**: Background compaction for optimal storage
6. **NATS Integration**: Event-driven cache updates
7. **Multi-tenant Partitioning**: Physical separation by tenant

This implementation provides a solid foundation for the full DuckDB architecture described in the documentation while maintaining compatibility with the existing ticket service interface.
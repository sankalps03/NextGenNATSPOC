# Ticket Simulator Service

A high-performance Go-based simulator service that generates tickets and tests APIs based on configurable EPS (Events Per Second) rates. The simulator reads ticket data from CSV files and performs various API operations including create, search, get, and update operations.

## 🚀 Features

- **EPS-based Generation**: Configurable events per second for different API operations
- **Multi-Tenant Support**: Random tenant selection for realistic multi-tenant testing
- **CSV Data Source**: Reads real ticket data from CSV files with automatic field mapping
- **Multi-API Testing**: Parallel testing of create, search, get, and update APIs
- **Empty Value Handling**: Automatic replacement of empty values with dummy data
- **Comprehensive Metrics**: Real-time performance monitoring and reporting
- **Configurable Operations**: Flexible configuration for different testing scenarios
- **Docker Support**: Containerized deployment with optimized builds
- **Connection Pooling**: Efficient HTTP client with retry logic and connection management

## 📋 API Operations

### Supported Operations
- **Create Tickets**: Generate new tickets from CSV data at configurable EPS
- **Search Tickets**: Execute search queries with various conditions and filters
- **Get Tickets**: Retrieve specific tickets by ID with optional field projection
- **Update Tickets**: Modify existing tickets with randomized or template-based data

### Metrics Collection
- Request counts and success rates per operation
- Response time tracking and averages
- Real-time EPS monitoring
- Error tracking and reporting

## 🔧 Configuration

The service is configured via environment variables:

### Core Configuration
```bash
API_BASE_URL=http://localhost:8082          # Target API base URL

# Multi-Tenant Configuration (choose one)
TENANT_IDS=tenant-1,tenant-2,tenant-3      # Multiple tenants (comma-separated)
# OR
TENANT_ID=simulator-tenant                  # Single tenant (backward compatibility)

CSV_FILE_PATH=./data/request_202502111503.csv # Path to CSV data file
LOG_LEVEL=info                              # Logging level (debug, info, warn, error)
```

### EPS Configuration
```bash
EPS_CREATE=2.0                              # Create requests per second
EPS_SEARCH=1.0                              # Search requests per second
EPS_GET=1.0                                 # Get requests per second
EPS_UPDATE=0.5                              # Update requests per second
```

### HTTP Client Configuration
```bash
HTTP_TIMEOUT=30s                            # Request timeout
HTTP_MAX_IDLE_CONNS=100                     # Maximum idle connections
HTTP_MAX_CONNS_PER_HOST=10                  # Maximum connections per host
HTTP_RETRY_ATTEMPTS=3                       # Number of retry attempts
HTTP_RETRY_DELAY=1s                         # Delay between retries
```

### Operation Configuration
```bash
CREATE_ENABLED=true                         # Enable create operations
SEARCH_ENABLED=true                         # Enable search operations
GET_ENABLED=true                            # Enable get operations
UPDATE_ENABLED=true                         # Enable update operations

CREATE_EXCLUDE_FIELDS=id                    # Fields to exclude from creation
SEARCH_RANDOMIZE_FIELDS=true                # Randomize search field values
GET_USE_CREATED_IDS=true                    # Use IDs from created tickets
UPDATE_RANDOMIZE_VALUES=true                # Randomize update values
```

### Metrics Configuration
```bash
METRICS_ENABLED=true                        # Enable metrics collection
METRICS_REPORT_INTERVAL=10s                 # Metrics reporting interval
METRICS_PORT=8083                           # Metrics server port
```

## 🚀 Quick Start

### Local Development

1. **Clone and navigate to simulator directory:**
   ```bash
   cd src/simulator
   ```

2. **Install dependencies:**
   ```bash
   make deps
   ```

3. **Run with default configuration:**
   ```bash
   make dev-run
   ```

4. **Run with custom configuration:**
   ```bash
   API_BASE_URL=http://localhost:8082 \
   CSV_FILE_PATH=./data/request_202502111503.csv \
   EPS_CREATE=5 \
   EPS_SEARCH=2 \
   LOG_LEVEL=debug \
   make run
   ```

## 🏢 Multi-Tenant Testing

The simulator supports comprehensive multi-tenant testing with random tenant selection:

### Multi-Tenant Configuration
```bash
# Configure multiple tenants for realistic testing
TENANT_IDS=tenant-1,tenant-2,tenant-3,tenant-4,tenant-5

# Each API request randomly selects a tenant
# This tests tenant isolation and performance across tenants
```

### Multi-Tenant Features
- **Random Tenant Selection**: Each API request uses a randomly selected tenant
- **Tenant Isolation Testing**: Verifies proper tenant data separation
- **Cross-Tenant Load Distribution**: Distributes load across multiple tenants
- **Realistic Multi-Tenant Scenarios**: Simulates real-world multi-tenant usage

## 🧪 Testing Scenarios

### Basic Load Testing
```bash
# Light load testing
EPS_CREATE=1 EPS_SEARCH=0.5 EPS_GET=0.5 EPS_UPDATE=0.2 make run

# Medium load testing
EPS_CREATE=5 EPS_SEARCH=2 EPS_GET=2 EPS_UPDATE=1 make run

# Heavy load testing
EPS_CREATE=10 EPS_SEARCH=5 EPS_GET=5 EPS_UPDATE=2 make run
```

### Multi-Tenant Load Testing
```bash
# Test with multiple tenants
TENANT_IDS=tenant-1,tenant-2,tenant-3 \
EPS_CREATE=5 EPS_SEARCH=3 EPS_GET=3 EPS_UPDATE=1 make run

# High-volume multi-tenant testing
TENANT_IDS=tenant-1,tenant-2,tenant-3,tenant-4,tenant-5 \
EPS_CREATE=10 EPS_SEARCH=8 EPS_GET=6 EPS_UPDATE=3 make run
```

### Specific Operation Testing
```bash
# Test only ticket creation across tenants
TENANT_IDS=tenant-1,tenant-2,tenant-3 \
EPS_CREATE=10 EPS_SEARCH=0 EPS_GET=0 EPS_UPDATE=0 make run

# Test only search operations across tenants
TENANT_IDS=tenant-1,tenant-2,tenant-3 \
EPS_CREATE=0 EPS_SEARCH=5 EPS_GET=0 EPS_UPDATE=0 make run
```

## 📁 CSV Data Format

The simulator expects CSV files with ticket data. The first row should contain headers:

```csv
"id","subject","description","priorityid","statusid","categoryid",...
4866,"Extension","Extension request",1,12,0,...
40181,"Article Creation","Create new article",1,12,534,...
```

### Field Handling
- **ID Field**: Automatically excluded from ticket creation
- **Required Fields**: Configurable required fields with default values
- **Field Types**: Automatic type conversion (string, number, boolean)
- **Random Selection**: Random ticket selection from CSV data

## 🔍 Search Conditions

The simulator supports comprehensive search across all ticket fields with intelligent condition generation:

### Supported Searchable Fields
The simulator can search across 70+ ticket fields including:
- **Core Fields**: `id`, `name`, `subject`, `description`
- **Status & Priority**: `statusid`, `priorityid`, `urgencyid`, `impactid`
- **Assignment**: `createdbyid`, `technicianid`, `groupid`, `requesterid`
- **Categories**: `categoryid`, `departmentid`, `locationid`, `companyid`
- **Time Fields**: `dueby`, `firstresponsetime`, `lastclosedtime`, `lastresolvedtime`
- **Escalation**: `resolutionescalationtime`, `responseescalationtime`
- **SLA**: `totalslapausetime`, `totalresolutiontime`, `slaviolated`
- **Approval**: `approvalstatus`, `approvaltype`
- **Flags**: `removed`, `spam`, `viprequest`, `reopened`

### Search Operators
- `eq`: Equal to / `ne`: Not equal to
- `gt`: Greater than / `lt`: Less than
- `gte`: Greater than or equal to / `lte`: Less than or equal to
- `contains`: String contains / `begins_with`: String starts with

### Intelligent Search Generation
The simulator automatically:
- **Selects Random Fields**: Chooses 1-3 random searchable fields per query
- **Uses CSV Values**: Extracts actual values from your CSV data for realistic searches
- **Smart Operators**: Selects appropriate operators based on field types
- **Random Projections**: Generates 3-8 random projection fields per query

## 🛠️ Development

### Building
```bash
make build          # Build binary
make test           # Run tests
make fmt            # Format code
make vet            # Run go vet
make lint           # Run linter
```

### Docker
```bash
make docker-build   # Build Docker image
make docker-run     # Run with Docker
```

## 📊 Monitoring

The simulator provides comprehensive metrics:

### Real-time Metrics
- **Request Rates**: Current EPS for each operation type
- **Success Rates**: Percentage of successful requests per operation
- **Response Times**: Average response times in milliseconds
- **Error Tracking**: Count and types of errors encountered

### Metrics Output
```
METRICS SUMMARY - Total: 1250 req (4.2/s), Success: 98.4%, Errors: 20, Uptime: 297.5s
```

## 🐛 Troubleshooting

### Common Issues

1. **CSV File Not Found**
   ```
   ERROR: Failed to load CSV file: no such file or directory
   ```
   Solution: Verify CSV_FILE_PATH points to a valid file

2. **API Connection Failed**
   ```
   ERROR: Create ticket failed: connection refused
   ```
   Solution: Verify API_BASE_URL and ensure target service is running

3. **No Ticket IDs for Get/Update**
   ```
   WARNING: No ticket IDs available for get operation
   ```
   Solution: Enable create operations first or configure random ticket IDs

### Debug Mode
Enable debug logging for detailed information:
```bash
LOG_LEVEL=debug make run
```

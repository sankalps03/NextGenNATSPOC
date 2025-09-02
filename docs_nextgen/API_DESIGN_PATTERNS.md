# API Design Patterns for SaaS Microservices

This document defines standardized patterns for API design across all microservices to ensure consistency and interoperability.

## RESTful API Standards

### Resource Naming Conventions
- **Collections**: Plural nouns (e.g., `/users`, `/tickets`, `/categories`)
- **Resources**: Singular identifiers (e.g., `/users/{userId}`, `/tickets/{ticketId}`)
- **Nested Resources**: Logical hierarchy (e.g., `/tickets/{ticketId}/comments`)
- **Actions**: Use HTTP verbs, avoid action names in URLs
- **Filtering**: Use query parameters (e.g., `/tickets?status=open&priority=high`)

### URL Structure Patterns
```
Base URL: https://api.{domain}/v{version}/tenants/{tenantId}

Resource Operations:
GET    /resources                 # List resources with pagination
POST   /resources                 # Create new resource  
GET    /resources/{id}            # Get specific resource
PUT    /resources/{id}            # Update entire resource
PATCH  /resources/{id}            # Partial resource update
DELETE /resources/{id}            # Delete resource

Nested Resources:
GET    /resources/{id}/subresources     # List nested resources
POST   /resources/{id}/subresources     # Create nested resource
```

### HTTP Status Code Standards
- **200 OK**: Successful GET, PUT, PATCH operations
- **201 Created**: Successful POST operations
- **204 No Content**: Successful DELETE operations
- **400 Bad Request**: Invalid request format or parameters
- **401 Unauthorized**: Missing or invalid authentication
- **403 Forbidden**: Valid auth but insufficient permissions
- **404 Not Found**: Resource does not exist
- **409 Conflict**: Resource state conflict (e.g., duplicate creation)
- **422 Unprocessable Entity**: Valid format but semantic errors
- **429 Too Many Requests**: Rate limit exceeded
- **500 Internal Server Error**: Server-side errors

## Request and Response Patterns

### Request Format Standards
```json
{
  "data": {
    "type": "resource-type",
    "attributes": {
      "field1": "value1",
      "field2": "value2"
    },
    "relationships": {
      "related-resource": {
        "data": {
          "type": "related-type",
          "id": "related-id"
        }
      }
    }
  },
  "meta": {
    "requestId": "correlation-id",
    "timestamp": "2024-01-01T00:00:00Z"
  }
}
```

### Response Format Standards
```json
{
  "data": {
    "type": "resource-type",
    "id": "resource-id",
    "attributes": {
      "field1": "value1",
      "field2": "value2",
      "createdAt": "2024-01-01T00:00:00Z",
      "updatedAt": "2024-01-01T00:00:00Z"
    },
    "relationships": {
      "related-resource": {
        "data": {
          "type": "related-type", 
          "id": "related-id"
        },
        "links": {
          "self": "/related-resources/related-id"
        }
      }
    }
  },
  "meta": {
    "requestId": "correlation-id",
    "timestamp": "2024-01-01T00:00:00Z"
  }
}
```

### Collection Response Format
```json
{
  "data": [
    {
      "type": "resource-type",
      "id": "resource-id-1",
      "attributes": { ... }
    },
    {
      "type": "resource-type", 
      "id": "resource-id-2",
      "attributes": { ... }
    }
  ],
  "meta": {
    "pagination": {
      "page": 1,
      "perPage": 20,
      "total": 150,
      "totalPages": 8
    },
    "requestId": "correlation-id"
  },
  "links": {
    "self": "/resources?page=1",
    "next": "/resources?page=2",
    "prev": null,
    "first": "/resources?page=1", 
    "last": "/resources?page=8"
  }
}
```

## Error Response Patterns

### Standard Error Format
```json
{
  "errors": [
    {
      "id": "unique-error-id",
      "status": "400",
      "code": "VALIDATION_ERROR",
      "title": "Validation Failed",
      "detail": "The 'email' field must be a valid email address",
      "source": {
        "pointer": "/data/attributes/email"
      },
      "meta": {
        "field": "email",
        "rejectedValue": "invalid-email"
      }
    }
  ],
  "meta": {
    "requestId": "correlation-id",
    "timestamp": "2024-01-01T00:00:00Z"
  }
}
```

### Error Code Categories
- **VALIDATION_ERROR**: Input validation failures
- **AUTHENTICATION_ERROR**: Authentication issues
- **AUTHORIZATION_ERROR**: Permission/access issues  
- **RESOURCE_NOT_FOUND**: Requested resource doesn't exist
- **RESOURCE_CONFLICT**: Resource state conflicts
- **RATE_LIMIT_EXCEEDED**: Too many requests
- **EXTERNAL_SERVICE_ERROR**: External dependency failures
- **INTERNAL_ERROR**: Unexpected server errors

## Pagination Patterns

### Cursor-Based Pagination (Recommended)
```
Request: GET /resources?cursor=eyJ0aW1lc3RhbXAiOiIyMDI0LTAxLTAxVDAwOjAwOjAwWiIsImlkIjoiMTIzIn0&limit=20

Response Headers:
X-Pagination-Cursor-Next: eyJ0aW1lc3RhbXAiOiIyMDI0LTAxLTAxVDAxOjAwOjAwWiIsImlkIjoiNDU2In0
X-Pagination-Has-More: true

Response Body:
{
  "data": [...],
  "meta": {
    "pagination": {
      "cursor": "current-cursor",
      "limit": 20,
      "hasMore": true
    }
  },
  "links": {
    "next": "/resources?cursor=next-cursor&limit=20"
  }
}
```

### Offset-Based Pagination (Simple Cases)
```
Request: GET /resources?page=2&perPage=20

Response:
{
  "data": [...],
  "meta": {
    "pagination": {
      "page": 2,
      "perPage": 20,
      "total": 150,
      "totalPages": 8
    }
  },
  "links": {
    "self": "/resources?page=2&perPage=20",
    "next": "/resources?page=3&perPage=20",
    "prev": "/resources?page=1&perPage=20",
    "first": "/resources?page=1&perPage=20",
    "last": "/resources?page=8&perPage=20"
  }
}
```

## Query Parameter Patterns

### Filtering
```
# Single value filter
GET /tickets?status=open

# Multiple value filter  
GET /tickets?status=open,in-progress

# Range filter
GET /tickets?createdAt[gte]=2024-01-01&createdAt[lt]=2024-02-01

# Text search
GET /tickets?search=database%20issue

# Complex filtering
GET /tickets?status=open&priority=high&assignee.id=user123
```

### Sorting
```
# Single field ascending
GET /tickets?sort=createdAt

# Single field descending
GET /tickets?sort=-createdAt

# Multiple fields
GET /tickets?sort=priority,-createdAt,title
```

### Field Selection
```
# Include specific fields only
GET /tickets?fields=id,title,status,priority

# Include related resource fields
GET /tickets?fields=id,title,assignee.name,assignee.email
```

### Related Resource Inclusion
```
# Include related resources
GET /tickets?include=assignee,category,comments

# Include with field selection
GET /tickets?include=assignee,category&fields=id,title,assignee.name,category.name
```

## Versioning Strategy

### URL Versioning (Recommended)
```
# Version in URL path
GET /v1/tickets
GET /v2/tickets

# Version in subdomain
GET https://v1.api.domain.com/tickets
GET https://v2.api.domain.com/tickets
```

### Header Versioning (Alternative)
```
GET /tickets
Headers:
  Accept: application/vnd.api+json;version=1
  API-Version: 1
```

### Version Lifecycle Management
- **Support Policy**: Support N and N-1 versions simultaneously
- **Deprecation Notice**: 6-month notice before version retirement
- **Migration Guide**: Provide detailed migration documentation
- **Backward Compatibility**: Maintain within major versions

## Authentication and Authorization Patterns

### JWT Bearer Token
```
Authorization: Bearer eyJhbGciOiJIUzI1NiIsInR5cCI6IkpXVCJ9...

Token Claims:
{
  "sub": "user-id",
  "tenant": "tenant-id",
  "roles": ["user", "admin"],
  "permissions": ["tickets:read", "tickets:write"],
  "exp": 1640995200,
  "iat": 1640908800
}
```

### API Key Authentication
```
X-API-Key: api-key-value
X-Tenant-ID: tenant-id

# Or in query parameter
GET /resources?api_key=api-key-value&tenant_id=tenant-id
```

### Rate Limiting Headers
```
Response Headers:
X-RateLimit-Limit: 1000
X-RateLimit-Remaining: 999
X-RateLimit-Reset: 1640995200
X-RateLimit-Window: 3600
```

## Content Negotiation

### Media Types
```
# JSON (default)
Accept: application/json
Content-Type: application/json

# JSON API specification
Accept: application/vnd.api+json
Content-Type: application/vnd.api+json

# CSV export
Accept: text/csv

# XML (if supported)
Accept: application/xml
```

### Compression
```
Accept-Encoding: gzip, deflate, br
Content-Encoding: gzip
```

## CORS and Security Headers

### CORS Headers
```
Access-Control-Allow-Origin: https://app.domain.com
Access-Control-Allow-Methods: GET, POST, PUT, PATCH, DELETE, OPTIONS
Access-Control-Allow-Headers: Content-Type, Authorization, X-API-Key
Access-Control-Max-Age: 86400
```

### Security Headers
```
X-Content-Type-Options: nosniff
X-Frame-Options: DENY
X-XSS-Protection: 1; mode=block
Strict-Transport-Security: max-age=31536000; includeSubDomains
Content-Security-Policy: default-src 'self'
```

## Health Check Patterns

### Health Endpoint
```
GET /health

Response:
{
  "status": "healthy",
  "timestamp": "2024-01-01T00:00:00Z",
  "version": "1.2.3",
  "uptime": 3600,
  "checks": {
    "database": "healthy",
    "nats": "healthy", 
    "external-service": "degraded"
  }
}
```

### Readiness and Liveness
```
GET /health/ready   # Service ready to receive traffic
GET /health/live    # Service is alive and running
```

This API design framework ensures consistency across all microservices while providing flexibility for service-specific needs.
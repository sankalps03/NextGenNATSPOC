# Multi-stage build for Go application
FROM golang:1.24-alpine AS builder

# Set working directory
WORKDIR /app

# Install build dependencies
RUN apk add --no-cache git ca-certificates tzdata

# Copy go mod files
COPY go.mod go.sum ./

# Download dependencies
RUN go mod download

# Copy source code
COPY . .

# Build the application
RUN CGO_ENABLED=0 GOOS=linux GOARCH=amd64 go build \
    -ldflags='-w -s -extldflags "-static"' \
    -a -installsuffix cgo \
    -o simulator .

# Final stage - minimal runtime image
FROM alpine:latest

# Install ca-certificates for HTTPS requests
RUN apk --no-cache add ca-certificates tzdata

# Create non-root user
RUN addgroup -g 1001 -S appgroup && \
    adduser -u 1001 -S appuser -G appgroup

# Set working directory
WORKDIR /app

# Copy the binary from builder stage
COPY --from=builder /app/simulator .

# Copy data directory
COPY --from=builder /app/data ./data

# Create logs directory
RUN mkdir -p /app/logs && \
    chown -R appuser:appgroup /app

# Switch to non-root user
USER appuser

# Expose metrics port
EXPOSE 8083

# Health check
HEALTHCHECK --interval=30s --timeout=10s --start-period=5s --retries=3 \
    CMD pgrep simulator || exit 1

# Set default environment variables
ENV API_BASE_URL=http://localhost:8082 \
    TENANT_IDS=simulator-tenant-1,simulator-tenant-2,simulator-tenant-3 \
    CSV_FILE_PATH=/app/data/request_202502111503.csv \
    EPS_CREATE=1.0 \
    EPS_SEARCH=0.5 \
    EPS_GET=0.5 \
    EPS_UPDATE=0.2 \
    LOG_LEVEL=info \
    METRICS_ENABLED=true \
    METRICS_PORT=8083

# Run the application
CMD ["./simulator"]

# Build stage
FROM golang:1.21-alpine AS builder

# Set working directory
WORKDIR /app

# Install dependencies
RUN apk add --no-cache git ca-certificates tzdata

# Copy go mod and sum files
COPY go.mod go.sum ./

# Download dependencies
RUN go mod download

# Copy source code
COPY . .

# Build the application
RUN CGO_ENABLED=0 GOOS=linux go build -a -installsuffix cgo -o tenant-manager-svc main.go

# Final stage
FROM alpine:latest

# Install ca-certificates for HTTPS calls
RUN apk --no-cache add ca-certificates tzdata

# Create non-root user
RUN adduser -D -s /bin/sh tenant-manager

# Set working directory
WORKDIR /app

# Copy binary from builder stage
COPY --from=builder /app/tenant-manager-svc .

# Change ownership to non-root user
RUN chown tenant-manager:tenant-manager /app/tenant-manager-svc

# Switch to non-root user
USER tenant-manager

# Expose port (if needed for health checks)
EXPOSE 8080

# Health check
HEALTHCHECK --interval=30s --timeout=5s --start-period=10s --retries=3 \
    CMD timeout 5s /app/tenant-manager-svc --health-check || exit 1

# Run the binary
CMD ["./tenant-manager-svc"]
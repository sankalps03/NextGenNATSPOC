# Build stage
FROM golang:1.24-alpine AS builder

# Install git and ca-certificates (needed for fetching dependencies)
RUN apk add --no-cache git ca-certificates tzdata

# Set working directory
WORKDIR /app

# Copy go mod files
COPY ../../go.mod ../../go.sum ./

# Download dependencies
RUN go mod download

# Copy source code
COPY main.go .

# Build the binary
RUN CGO_ENABLED=0 GOOS=linux GOARCH=amd64 go build \
    -ldflags='-w -s -extldflags "-static"' \
    -a -installsuffix cgo \
    -o api-server main.go

# Final stage
FROM scratch

# Copy ca-certificates from builder stage
COPY --from=builder /etc/ssl/certs/ca-certificates.crt /etc/ssl/certs/

# Copy timezone data
COPY --from=builder /usr/share/zoneinfo /usr/share/zoneinfo

# Copy the binary
COPY --from=builder /app/api-server /api-server

# Expose port
EXPOSE 8082

# Health check
HEALTHCHECK --interval=30s --timeout=3s --start-period=5s --retries=3 \
    CMD ["/api-server", "--health-check"] || exit 1

# Set user (security best practice)
USER 65534

# Run the binary
ENTRYPOINT ["/api-server"]
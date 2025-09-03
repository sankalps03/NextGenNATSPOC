# Build stage
FROM golang:1.24-alpine AS builder

# Install protobuf compiler and required tools
RUN apk add --no-cache git ca-certificates tzdata protobuf protobuf-dev

# Install protoc-gen-go
RUN go install google.golang.org/protobuf/cmd/protoc-gen-go@latest

# Set working directory
WORKDIR /app

# Copy go mod files
COPY go.mod go.sum ./

# Download dependencies
RUN go mod download

# Copy proto files and generate protobuf code
COPY proto/ ./proto/
RUN mkdir -p pb
RUN protoc --go_out=pb --go_opt=paths=source_relative proto/ticket.proto

# Copy source code
COPY . .

# Build the binary
RUN CGO_ENABLED=0 GOOS=linux GOARCH=amd64 go build \
    -ldflags='-w -s -extldflags "-static"' \
    -a -installsuffix cgo \
    -o ticket-svc main.go

# Final stage
FROM scratch

# Copy ca-certificates from builder stage
COPY --from=builder /etc/ssl/certs/ca-certificates.crt /etc/ssl/certs/

# Copy timezone data
COPY --from=builder /usr/share/zoneinfo /usr/share/zoneinfo

# Copy the binary
COPY --from=builder /app/ticket-svc /ticket-svc

# Set user (security best practice)
USER 65534

# Run the binary
ENTRYPOINT ["/ticket-svc"]
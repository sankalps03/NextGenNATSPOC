#!/bin/bash

# Generate protobuf files for ticket service

set -e

echo "Generating protobuf files for ticket service..."

# Check if protoc is installed
if ! command -v protoc &> /dev/null; then
    echo "Error: protoc (Protocol Buffer Compiler) is not installed"
    echo "Please install protoc:"
    echo "  Ubuntu/Debian: sudo apt-get install protobuf-compiler"
    echo "  macOS: brew install protobuf"
    echo "  Alpine: apk add protobuf protobuf-dev"
    exit 1
fi

# Install protoc-gen-go if not present
if ! command -v protoc-gen-go &> /dev/null; then
    echo "Installing protoc-gen-go..."
    go install google.golang.org/protobuf/cmd/protoc-gen-go@latest
fi

# Create pb directory if it doesn't exist
mkdir -p pb

# Generate Go code from proto files
echo "Generating Go code from proto/ticket.proto..."
protoc --go_out=pb --go_opt=paths=source_relative proto/ticket.proto

echo "Protobuf generation complete!"
echo "Generated files:"
echo "  - pb/proto/ticket.pb.go"

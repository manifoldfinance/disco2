#!/bin/bash
set -e

# Add GOPATH/bin to PATH to ensure the plugins are accessible
export PATH=$PATH:$(go env GOPATH)/bin

# Install buf if not already installed
if ! command -v buf &> /dev/null; then
    echo "Installing buf..."
    go install github.com/bufbuild/buf/cmd/buf@latest
fi

# Install required plugins 
echo "Installing protoc-gen-go..."
go install google.golang.org/protobuf/cmd/protoc-gen-go@latest

echo "Installing protoc-gen-go-grpc..."
go install google.golang.org/grpc/cmd/protoc-gen-go-grpc@latest

echo "Installing protoc-gen-grpc-gateway..."
go install github.com/grpc-ecosystem/grpc-gateway/v2/protoc-gen-grpc-gateway@latest

echo "Installing protoc-gen-openapiv2..."
go install github.com/grpc-ecosystem/grpc-gateway/v2/protoc-gen-openapiv2@latest

# Verify plugins are installed and accessible
echo "Checking if plugins are in PATH..."
which protoc-gen-go
which protoc-gen-go-grpc
which protoc-gen-grpc-gateway
which protoc-gen-openapiv2

# Clean generated directories before regenerating
echo "Cleaning previous generated files..."
rm -rf pkg/pb/*/{*.pb.go,*.pb.gw.go}

# Run buf generate command
echo "Generating protocol buffer code..."
buf generate

echo "Protocol buffer code generation complete!"

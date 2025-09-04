# Simulator Service Makefile

.PHONY: build run test clean docker-build docker-run deps

# Go parameters
GOCMD=go
GOBUILD=$(GOCMD) build
GOCLEAN=$(GOCMD) clean
GOTEST=$(GOCMD) test
GOGET=$(GOCMD) get
GOMOD=$(GOCMD) mod

# Binary name
BINARY_NAME=simulator
BINARY_UNIX=$(BINARY_NAME)_unix

# Docker parameters
DOCKER_IMAGE=nextgen-simulator
DOCKER_TAG=latest

all: test build

build:
	$(GOBUILD) -o $(BINARY_NAME) -v ./...

test:
	$(GOTEST) -v ./...

clean:
	$(GOCLEAN)
	rm -f $(BINARY_NAME)
	rm -f $(BINARY_UNIX)

run:
	$(GOBUILD) -o $(BINARY_NAME) -v ./...
	./$(BINARY_NAME)

deps:
	$(GOMOD) download
	$(GOMOD) tidy

# Cross compilation
build-linux:
	CGO_ENABLED=0 GOOS=linux GOARCH=amd64 $(GOBUILD) -o $(BINARY_UNIX) -v

docker-build:
	docker build -t $(DOCKER_IMAGE):$(DOCKER_TAG) .

docker-run:
	docker run --rm -p 8083:8083 \
		-e API_BASE_URL=http://host.docker.internal:8082 \
		-e CSV_FILE_PATH=/app/data/request_202502111503.csv \
		-e TENANT_IDS=simulator-tenant-1,simulator-tenant-2,simulator-tenant-3 \
		-e EPS_CREATE=1 \
		-e LOG_LEVEL=info \
		$(DOCKER_IMAGE):$(DOCKER_TAG)

docker-run-local:
	docker run --rm -p 8083:8083 \
		-v $(PWD)/data:/app/data \
		-e API_BASE_URL=http://host.docker.internal:8082 \
		-e CSV_FILE_PATH=/app/data/request_202502111503.csv \
		-e TENANT_IDS=simulator-tenant-1,simulator-tenant-2,simulator-tenant-3 \
		-e EPS_CREATE=2 \
		-e EPS_SEARCH=1 \
		-e EPS_GET=1 \
		-e EPS_UPDATE=0.5 \
		-e LOG_LEVEL=debug \
		$(DOCKER_IMAGE):$(DOCKER_TAG)

docker-run-multitenant:
	docker run --rm -p 8083:8083 \
		-v $(PWD)/data:/app/data \
		-e API_BASE_URL=http://host.docker.internal:8082 \
		-e CSV_FILE_PATH=/app/data/request_202502111503.csv \
		-e TENANT_IDS=tenant-1,tenant-2,tenant-3,tenant-4,tenant-5 \
		-e EPS_CREATE=5 \
		-e EPS_SEARCH=3 \
		-e EPS_GET=3 \
		-e EPS_UPDATE=1 \
		-e LOG_LEVEL=info \
		$(DOCKER_IMAGE):$(DOCKER_TAG)

# Development helpers
dev-run:
	API_BASE_URL=http://localhost:8082 \
	CSV_FILE_PATH=./data/request_202502111503.csv \
	TENANT_IDS=simulator-tenant-1,simulator-tenant-2,simulator-tenant-3 \
	EPS_CREATE=2 \
	EPS_SEARCH=1 \
	EPS_GET=1 \
	EPS_UPDATE=0.5 \
	LOG_LEVEL=debug \
	$(GOBUILD) -o $(BINARY_NAME) && ./$(BINARY_NAME)

# Multi-tenant testing scenarios
dev-run-multitenant:
	API_BASE_URL=http://localhost:8082 \
	CSV_FILE_PATH=./data/request_202502111503.csv \
	TENANT_IDS=tenant-1,tenant-2,tenant-3,tenant-4,tenant-5 \
	EPS_CREATE=5 \
	EPS_SEARCH=3 \
	EPS_GET=3 \
	EPS_UPDATE=1 \
	LOG_LEVEL=info \
	$(GOBUILD) -o $(BINARY_NAME) && ./$(BINARY_NAME)

# Single tenant testing (backward compatibility)
dev-run-single:
	API_BASE_URL=http://localhost:8082 \
	CSV_FILE_PATH=./data/request_202502111503.csv \
	TENANT_ID=simulator-tenant \
	EPS_CREATE=2 \
	EPS_SEARCH=1 \
	EPS_GET=1 \
	EPS_UPDATE=0.5 \
	LOG_LEVEL=debug \
	$(GOBUILD) -o $(BINARY_NAME) && ./$(BINARY_NAME)

fmt:
	$(GOCMD) fmt ./...

vet:
	$(GOCMD) vet ./...

lint:
	golangci-lint run

# Install development dependencies
install-dev-deps:
	$(GOGET) -u golang.org/x/tools/cmd/goimports
	$(GOGET) -u github.com/golangci/golangci-lint/cmd/golangci-lint

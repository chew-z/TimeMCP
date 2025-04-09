.PHONY: build run test clean test-client

# Build the main application
build:
	go build -o bin/timemcp main.go

# Run the application
run:
	go run main.go

# Test using the example client
test-client:
	go run examples/test_client.go

# Run tests
test:
	go test ./...

# Download dependencies
deps:
	go mod download

# Clean build artifacts
clean:
	rm -rf bin/

# Default target
all: deps build

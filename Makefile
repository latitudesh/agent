# Makefile for Latitude.sh Agent

# Variables
BINARY_NAME=lsh-agent
BINARY_PATH=./cmd/agent
BUILD_DIR=./build
VERSION ?= 1.0.0
LDFLAGS=-ldflags "-X main.Version=$(VERSION)"

# Go parameters
GOCMD=go
GOBUILD=$(GOCMD) build
GOCLEAN=$(GOCMD) clean
GOTEST=$(GOCMD) test
GOGET=$(GOCMD) get
GOMOD=$(GOCMD) mod

# Default target
.DEFAULT_GOAL := build

# Build the binary
.PHONY: build
build:
	@echo "Building $(BINARY_NAME)..."
	@mkdir -p $(BUILD_DIR)
	$(GOBUILD) $(LDFLAGS) -o $(BUILD_DIR)/$(BINARY_NAME) $(BINARY_PATH)

# Build for Linux (common deployment target)
.PHONY: build-linux
build-linux:
	@echo "Building $(BINARY_NAME) for Linux..."
	@mkdir -p $(BUILD_DIR)
	GOOS=linux GOARCH=amd64 $(GOBUILD) $(LDFLAGS) -o $(BUILD_DIR)/$(BINARY_NAME)-linux-amd64 $(BINARY_PATH)

# Clean build artifacts
.PHONY: clean
clean:
	@echo "Cleaning..."
	$(GOCLEAN)
	@rm -rf $(BUILD_DIR)

# Download dependencies
.PHONY: deps
deps:
	@echo "Downloading dependencies..."
	$(GOMOD) download
	$(GOMOD) tidy

# Run tests
.PHONY: test
test:
	@echo "Running tests..."
	$(GOTEST) -v ./...

# Check configuration syntax
.PHONY: check-config
check-config: build
	@echo "Checking configuration..."
	$(BUILD_DIR)/$(BINARY_NAME) -check-config -config configs/agent.yaml

# Install the binary to /usr/local/bin
.PHONY: install
install: build
	@echo "Installing $(BINARY_NAME) to /usr/local/bin..."
	sudo cp $(BUILD_DIR)/$(BINARY_NAME) /usr/local/bin/
	sudo chmod +x /usr/local/bin/$(BINARY_NAME)

# Uninstall the binary
.PHONY: uninstall
uninstall:
	@echo "Uninstalling $(BINARY_NAME)..."
	sudo rm -f /usr/local/bin/$(BINARY_NAME)

# Create systemd service (development helper)
.PHONY: create-service
create-service:
	@echo "Creating systemd service..."
	@sudo tee /etc/systemd/system/lsh-agent-go.service > /dev/null <<EOF
[Unit]
Description=Latitude.sh Agent (Go)
After=network.target
Wants=network.target

[Service]
Type=simple
ExecStart=/usr/local/bin/$(BINARY_NAME) -config /etc/lsh-agent/config.yaml
Restart=always
RestartSec=10
User=root
Environment=LATITUDESH_BEARER=""

[Install]
WantedBy=multi-user.target
EOF
	@sudo systemctl daemon-reload
	@echo "Service created. Configure environment variables and enable with:"
	@echo "  sudo systemctl enable lsh-agent-go"
	@echo "  sudo systemctl start lsh-agent-go"

# Remove systemd service
.PHONY: remove-service
remove-service:
	@echo "Removing systemd service..."
	-sudo systemctl stop lsh-agent-go
	-sudo systemctl disable lsh-agent-go
	sudo rm -f /etc/systemd/system/lsh-agent-go.service
	sudo systemctl daemon-reload

# Show version
.PHONY: version
version:
	@echo "Version: $(VERSION)"

# Development: build and run with sample config
.PHONY: dev-run
dev-run: build
	@echo "Running agent in development mode..."
	$(BUILD_DIR)/$(BINARY_NAME) -config configs/agent.yaml

# Show help
.PHONY: help
help:
	@echo "Available targets:"
	@echo "  build         - Build the binary"
	@echo "  build-linux   - Build for Linux x86_64"
	@echo "  clean         - Clean build artifacts"
	@echo "  deps          - Download and tidy dependencies"
	@echo "  test          - Run tests"
	@echo "  check-config  - Validate configuration file"
	@echo "  install       - Install binary to /usr/local/bin"
	@echo "  uninstall     - Remove binary from /usr/local/bin"
	@echo "  create-service - Create systemd service"
	@echo "  remove-service - Remove systemd service"
	@echo "  dev-run       - Build and run with sample config"
	@echo "  version       - Show version"
	@echo "  help          - Show this help"
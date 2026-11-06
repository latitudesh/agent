# Testing Guide for Latitude.sh Go Agent

## Prerequisites

1. **Server Requirements**:
   - Ubuntu/Debian server with UFW firewall
   - Root access or sudo privileges
   - Go 1.22+ installed (for building from source)
   - Active Latitude.sh account with firewall configuration

2. **Required Environment Variables**:
   ```bash
   export LATITUDESH_AUTH_TOKEN="your_bearer_token_here"
   export PROJECT_ID="your_project_id"
   export FIREWALL_ID="your_firewall_id"
   export PUBLIC_IP="auto"  # or specific IP
   ```

## Option 1: Test with Pre-built Binary (Recommended)

### Step 1: Download and Setup
```bash
git clone https://github.com/latitudesh/agent.git
cd agent

# Prebuilt packages (.deb/.rpm, amd64 and arm64) come from the apt and dnf
# repositories: see "Installing from the package repositories" in the README.
# Or build from source, and copy this host's binary to ./lsh-agent for the
# commands below:
ARCH=$(uname -m | sed -e 's/x86_64/amd64/' -e 's/aarch64/arm64/')
make build-linux   # build/lsh-agent-linux-amd64 and build/lsh-agent-linux-arm64
cp "build/lsh-agent-linux-${ARCH}" ./lsh-agent
# (with the package installed, use /usr/bin/lsh-agent instead of ./lsh-agent)
```

### Step 2: Configuration
```bash
# Create configuration directory
sudo mkdir -p /etc/lsh-agent

# Copy configuration template
sudo cp configs/agent.yaml /etc/lsh-agent/config.yaml

# Create legacy environment file (for backward compatibility)
sudo tee /etc/lsh-agent/env > /dev/null << EOF
PROJECT_ID=your_project_id_here
FIREWALL_ID=your_firewall_id_here
PUBLIC_IP=auto
EOF

# Set required environment variables
export LATITUDESH_AUTH_TOKEN="your_bearer_token_here"
```

### Step 3: Basic Testing

#### Test 1: Configuration Validation
```bash
./lsh-agent -check-config -config /etc/lsh-agent/config.yaml
```
Expected output: `Configuration is valid`

#### Test 2: Version Check
```bash
./lsh-agent -version
```
Expected output: `Latitude.sh Agent v1.0.0`

#### Test 3: Dry Run (Single Execution)
```bash
sudo ./lsh-agent -config /etc/lsh-agent/config.yaml
```

This should:
- Connect to Latitude.sh API
- Fetch firewall rules
- Compare with current UFW rules
- Display differences
- Apply changes if needed
- Exit after one cycle

## Option 2: Build from Source

### Step 1: Install Go
```bash
# Install Go 1.23+ for this host's architecture
ARCH=$(uname -m | sed -e 's/x86_64/amd64/' -e 's/aarch64/arm64/')
wget "https://go.dev/dl/go1.23.4.linux-${ARCH}.tar.gz"
sudo tar -C /usr/local -xzf "go1.23.4.linux-${ARCH}.tar.gz"
export PATH=$PATH:/usr/local/go/bin
```

### Step 2: Build and Test
```bash
# Clone and build
git clone https://github.com/latitudesh/agent.git
cd agent

# Download dependencies and build
make deps
make build-linux

# Test the binary
./build/lsh-agent-linux-${ARCH} -version
```

## Testing Scenarios

### Scenario 1: Fresh Installation
1. Start with clean UFW rules: `sudo ufw --force reset`
2. Enable UFW: `sudo ufw --force enable`
3. Run agent: `sudo ./lsh-agent -config /etc/lsh-agent/config.yaml`
4. Verify rules are applied correctly

### Scenario 2: Rule Updates
1. Modify firewall rules in Latitude.sh dashboard
2. Run agent again
3. Verify UFW rules are updated to match

### Scenario 3: Service Installation
```bash
# Install as systemd service
sudo make install
sudo make create-service

# Configure environment
sudo systemctl edit lsh-agent-go
# Add:
# [Service]
# Environment=LATITUDESH_AUTH_TOKEN=your_token_here

# Start service
sudo systemctl enable lsh-agent-go
sudo systemctl start lsh-agent-go

# Monitor logs
sudo journalctl -u lsh-agent-go -f
```

## Expected Behavior

### Successful Run Output:
```
INFO[2024-XX-XX] Agent starting version=1.0.0 config_path=/etc/lsh-agent/config.yaml component=agent
INFO[2024-XX-XX] Starting collection cycle component=agent
INFO[2024-XX-XX] Pinging Latitude.sh API at https://api.latitude.sh/agent/ping
INFO[2024-XX-XX] Successfully retrieved firewall rules from API
INFO[2024-XX-XX] Firewall rules received from the server:
INFO[2024-XX-XX] From: 192.168.1.0/24, To: any, Protocol: tcp, Port: 22
INFO[2024-XX-XX] Starting firewall rule synchronization
INFO[2024-XX-XX] Found 1 API rules
INFO[2024-XX-XX] Found 0 current UFW rules
INFO[2024-XX-XX] Rules to add: 1
INFO[2024-XX-XX] Adding new UFW rules
INFO[2024-XX-XX] Added rule: From: 192.168.1.0/24, Protocol: tcp, Port: 22
INFO[2024-XX-XX] Reloading UFW to apply changes
INFO[2024-XX-XX] Collection cycle completed successfully in 2.3s component=agent
```

## Troubleshooting

### Common Issues:

1. **"UFW binary not found"**:
   ```bash
   sudo apt update && sudo apt install ufw
   ```

2. **"API request failed with status 401"**:
   - Check LATITUDESH_AUTH_TOKEN token
   - Verify token has correct permissions

3. **"Project ID is required"**:
   - Ensure PROJECT_ID is set in environment or config

4. **"UFW command failed"**:
   - Check UFW is enabled: `sudo ufw status`
   - Verify sudo permissions

5. **Service fails to start**:
   ```bash
   sudo journalctl -u lsh-agent-go -n 50
   ```

## Comparison with Shell Version

The Go agent should behave identically to the shell version:
- Same API endpoints
- Same UFW rule format  
- Same environment file compatibility
- Same 30-second polling interval
- Same rule synchronization logic

## Performance Testing

Monitor resource usage:
```bash
# CPU and memory usage
top -p $(pgrep lsh-agent)

# Network connections
sudo netstat -tulpn | grep lsh-agent
```

The Go version should use significantly less memory and CPU than the shell version.

## Next Steps

After successful testing:
1. Create GitHub release with pre-built binaries
2. Update main branch installation script
3. Deploy to production servers
4. Monitor performance and logs
5. Plan next phase (IPMI, hardware monitoring)
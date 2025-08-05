# Telegraf Integration Deployment Checklist

## Pre-Deployment Verification

### 1. GitHub Branch Status ✅
- Branch: `feat/telegraf-device-metrics` 
- Status: Published and up-to-date
- Latest commit: `f130828 Integrate Telegraf metrics collection with agent`

### 2. Files Added/Modified
- ✅ `install.sh` - Added Telegraf installation and configuration
- ✅ `configs/telegraf.conf` - Telegraf configuration template
- ✅ `configs/agent.yaml` - Added metrics endpoint support
- ✅ `internal/config/config.go` - Updated config struct
- ✅ `scripts/test-telegraf.sh` - Testing script

## Installation Command

```bash
# On your test server, run:
curl -L https://raw.githubusercontent.com/latitudesh/agent/feat/telegraf-device-metrics/install.sh | sudo bash -s -- \
  -firewall <FIREWALL_ID> \
  -project <PROJECT_ID> \
  [-public_ip <PUBLIC_IP>] \
  [-extra_parameters "LATITUDESH_METRICS_ENDPOINT=https://your-custom-endpoint.com/metrics"]
```

## Post-Installation Verification Steps

### 1. Service Status Check
```bash
sudo systemctl status lsh-agent.service
sudo systemctl status telegraf.service
```

### 2. Configuration Test
```bash
sudo /path/to/scripts/test-telegraf.sh
```

### 3. Log Inspection
```bash
# Agent logs
sudo journalctl -u lsh-agent -f --since "5 minutes ago"

# Telegraf logs  
sudo journalctl -u telegraf -f --since "5 minutes ago"
```

### 4. Bearer Token Configuration
```bash
# Set bearer token for both services
sudo systemctl edit lsh-agent.service
sudo systemctl edit telegraf.service
# Add: Environment="LATITUDESH_BEARER=your_token_here"

sudo systemctl daemon-reload
sudo systemctl restart lsh-agent.service telegraf.service
```

### 5. Metrics Verification
```bash
# Test Telegraf configuration
sudo telegraf --config /etc/lsh-agent/telegraf.conf --test --input-filter cpu,mem

# Monitor HTTP requests (if endpoint is available)
sudo journalctl -u telegraf -f | grep -i "http\|error\|post"
```

## Expected Behavior

### Services
- **lsh-agent.service**: Should be `active (running)` - handles firewall rules
- **telegraf.service**: Should be `active (running)` - collects and sends metrics

### Metrics Collection
- **Interval**: Every 30 seconds
- **Endpoint**: `https://api.latitude.sh/metrics` (or custom endpoint)
- **Format**: JSON with gzip compression
- **Headers**: Authorization, X-Agent-Version, X-Project-ID, X-Firewall-ID

### System Metrics Collected
- CPU utilization (per-core and total)
- Memory usage and swap
- Disk usage and I/O operations
- Network interface statistics  
- System load and process counts
- Docker container metrics (if available)

## Troubleshooting Common Issues

### Telegraf Not Starting
```bash
# Check configuration syntax
sudo telegraf --config /etc/lsh-agent/telegraf.conf --test

# Check environment variables
cat /etc/lsh-agent/telegraf.env
```

### Network/API Issues
```bash
# Test endpoint connectivity
curl -X POST -H "Authorization: Bearer $TOKEN" https://api.latitude.sh/metrics

# Check Telegraf HTTP output
sudo telegraf --config /etc/lsh-agent/telegraf.conf --test --output-filter http
```

### Permission Issues
```bash
# Verify telegraf user permissions
sudo -u telegraf telegraf --config /etc/lsh-agent/telegraf.conf --test
```

## Rollback Plan
If issues occur, disable Telegraf while keeping the original agent:
```bash
sudo systemctl stop telegraf.service
sudo systemctl disable telegraf.service
# Original lsh-agent continues running firewall management
```
# Metrics Collection with Telegraf

The Latitude.sh Agent now includes system metrics collection via Telegraf, sending comprehensive device metrics to your configured endpoint.

## Features

### Metrics Collected
- **CPU Usage**: Per-core and total utilization statistics
- **Memory**: Usage, available, and swap statistics  
- **Disk**: Filesystem usage and I/O operations
- **Network**: Interface statistics and connection counts
- **System**: Load averages and process counts
- **Docker**: Container metrics (if Docker is available)
- **Processes**: System process information
- **Temperature**: Hardware temperature sensors (if available)

### Data Format
Metrics are sent as JSON objects with the following structure:
```json
{
  "fields": {
    "usage_idle": 99.83,
    "usage_user": 0.17,
    "usage_system": 0.0
  },
  "name": "cpu",
  "tags": {
    "cpu": "cpu-total",
    "host": "server-hostname"
  },
  "timestamp": 1754360686
}
```

## Configuration

### Installation
The install script automatically:
1. Installs Telegraf from InfluxData repositories
2. Creates configuration files in `/etc/lsh-agent/`
3. Sets up systemd services for both the agent and Telegraf
4. Configures environment variables

### Environment Variables
Metrics collection is configured via `/etc/lsh-agent/telegraf.env`:

```bash
PROJECT_ID=proj_xxxxx
FIREWALL_ID=fw_xxxxx  
PUBLIC_IP=x.x.x.x
AGENT_VERSION=1.0.0
LATITUDESH_METRICS_ENDPOINT=https://api.latitude.sh/metrics
```

### Custom Endpoint
To use a custom metrics endpoint, update the `LATITUDESH_METRICS_ENDPOINT` variable:

```bash
# In install script
sudo bash install.sh -firewall fw_xxx -project proj_xxx \
  -extra_parameters "LATITUDESH_METRICS_ENDPOINT=https://your-custom-endpoint.com/metrics"
```

### Authentication
For authenticated endpoints, set the bearer token:
```bash
sudo systemctl edit telegraf.service
# Add: Environment="LATITUDESH_BEARER=your_token_here"
sudo systemctl restart telegraf.service
```

## Service Management

### Status Check
```bash
sudo systemctl status lsh-agent.service telegraf.service
```

### Logs
```bash
# Agent logs
sudo journalctl -u lsh-agent -f

# Telegraf logs  
sudo journalctl -u telegraf -f
```

### Restart Services
```bash
sudo systemctl restart lsh-agent.service telegraf.service
```

## Testing

### Configuration Test
```bash
sudo telegraf --config /etc/lsh-agent/telegraf.conf --test --input-filter cpu
```

### Single Metrics Collection
```bash
sudo telegraf --config /etc/lsh-agent/telegraf.conf --once --quiet
```

### Manual Endpoint Test
```bash
curl -X POST -H "Content-Type: application/json" \
  -d '{"test": "data"}' \
  https://your-endpoint.com/metrics
```

## Troubleshooting

### Common Issues

1. **Telegraf service failing**: Check configuration syntax
   ```bash
   sudo telegraf --config /etc/lsh-agent/telegraf.conf --test
   ```

2. **Metrics not reaching endpoint**: Verify connectivity
   ```bash
   curl -X POST https://your-endpoint.com/metrics
   ```

3. **JSON format errors**: Check endpoint requirements
   - Single objects: `{"name": "cpu", "fields": {...}}`
   - Array format: `[{"name": "cpu", "fields": {...}}]`

### Log Analysis
```bash
# Check for HTTP errors
sudo journalctl -u telegraf | grep -i error

# Monitor real-time metrics sending
sudo journalctl -u telegraf -f | grep -E "(POST|Error|200|400|500)"
```

## Performance

- **Collection Interval**: 30 seconds (configurable)
- **Resource Usage**: ~20MB RAM typical
- **Network**: JSON with gzip compression
- **CPU Overhead**: Minimal (~1-2% additional load)

## Security

- HTTPS endpoints supported
- Bearer token authentication
- Environment variable configuration
- Systemd service isolation
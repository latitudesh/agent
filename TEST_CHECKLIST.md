# Go Agent Testing Checklist

## Pre-Test Setup ✅
- [ ] Server has Ubuntu/Debian with UFW installed
- [ ] Root/sudo access confirmed
- [ ] Git installed and feat/go branch cloned
- [ ] Go 1.22+ installed (for building from source)
- [ ] Environment variables prepared:
  - [ ] `LATITUDESH_AUTH_TOKEN` token
  - [ ] `PROJECT_ID` from dashboard
  - [ ] `FIREWALL_ID` from dashboard

## Build Testing ✅
- [ ] `make deps` - downloads dependencies successfully
- [ ] `make build-linux` - builds binary without errors
- [ ] `./build/lsh-agent-linux-amd64 -version` - shows version
- [ ] `make check-config` - validates configuration

## Configuration Testing ✅
- [ ] Create `/etc/lsh-agent/` directory
- [ ] Copy `configs/agent.yaml` to `/etc/lsh-agent/config.yaml`
- [ ] Create legacy env file `/etc/lsh-agent/env` with PROJECT_ID, FIREWALL_ID, PUBLIC_IP
- [ ] Test config validation: `./lsh-agent -check-config`

## Functional Testing ✅

### Basic Connectivity
- [ ] Test API connectivity (should succeed with valid token)
- [ ] Test with invalid token (should fail gracefully)
- [ ] Check logs are structured and readable

### Firewall Rule Sync
- [ ] **Baseline Test**: Start with clean UFW rules
  ```bash
  sudo ufw --force reset
  sudo ufw --force enable  
  sudo ufw allow ssh
  ```
- [ ] **First Run**: Agent fetches and applies rules from API
- [ ] **Verify**: UFW rules match Latitude.sh dashboard
- [ ] **Second Run**: No changes (idempotent)
- [ ] **Rule Addition**: Add rule in dashboard, verify it's applied
- [ ] **Rule Removal**: Remove rule in dashboard, verify it's removed

### Error Handling
- [ ] Network connectivity loss during execution
- [ ] Invalid API responses
- [ ] UFW command failures
- [ ] Missing permissions

## Service Testing ✅
- [ ] Install binary: `sudo make install`
- [ ] Create service: `sudo make create-service`
- [ ] Configure environment variables in service
- [ ] Start service: `sudo systemctl start lsh-agent-go`
- [ ] Check service status: `sudo systemctl status lsh-agent-go`
- [ ] Monitor logs: `sudo journalctl -u lsh-agent-go -f`
- [ ] Verify 30-second interval execution
- [ ] Test service restart after failure
- [ ] Test graceful shutdown with SIGTERM

## Performance Testing ✅
- [ ] Monitor CPU usage during execution
- [ ] Monitor memory usage over time
- [ ] Check for memory leaks during long runs
- [ ] Verify network requests are reasonable
- [ ] Compare performance with shell version

## Backward Compatibility ✅
- [ ] Legacy `/etc/lsh-agent/env` file is read correctly
- [ ] Environment variables override config file
- [ ] UFW rule format matches shell version exactly
- [ ] API requests match shell version format
- [ ] File outputs (`/tmp/lsh_firewall.json`) are compatible

## Edge Cases ✅
- [ ] Empty firewall rules from API
- [ ] Malformed JSON responses
- [ ] UFW not installed or not found
- [ ] Permission denied for UFW commands
- [ ] Disk space full (temp files)
- [ ] Very large rule sets (performance)

## Production Readiness ✅
- [ ] Logs contain no sensitive information
- [ ] Error messages are helpful for troubleshooting
- [ ] Configuration is documented and clear
- [ ] Service starts automatically after reboot
- [ ] Graceful handling of configuration changes
- [ ] Health check endpoint responds correctly

## Comparison Validation ✅
- [ ] Install both shell and Go versions side by side
- [ ] Run both against same firewall configuration
- [ ] Verify identical UFW rule results
- [ ] Compare API request patterns
- [ ] Validate identical behavior under all scenarios

---

## Quick Test Commands

```bash
# Build and basic test
make build-linux
./build/lsh-agent-linux-amd64 -version
./build/lsh-agent-linux-amd64 -check-config -config configs/agent.yaml

# Single run test (with proper env vars)
sudo -E ./build/lsh-agent-linux-amd64 -config /etc/lsh-agent/config.yaml

# Service test
sudo make install create-service
sudo systemctl edit lsh-agent-go  # Add environment variables
sudo systemctl start lsh-agent-go
sudo journalctl -u lsh-agent-go -f
```

## Expected Results

### ✅ Success Indicators:
- Clean build without warnings
- Configuration validates successfully  
- API connectivity established
- UFW rules synchronized correctly
- Service runs continuously without errors
- Logs are structured and informative
- Performance is better than shell version

### ❌ Failure Indicators:
- Build errors or warnings
- Configuration validation fails
- API authentication failures
- UFW commands fail
- Rules don't match dashboard
- Service crashes or restarts frequently
- Memory/CPU usage issues

## Post-Test Actions

If all tests pass:
- [ ] Create GitHub release with binary
- [ ] Update documentation
- [ ] Plan production rollout

If tests fail:
- [ ] Document specific failures
- [ ] Create GitHub issues for bugs
- [ ] Fix critical issues before release
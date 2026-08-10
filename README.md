# Latitude.sh Agent

The Latitude.sh Agent (`lsh-agent`) is a lightweight daemon that keeps your server's firewall in sync with the [Latitude.sh Firewall](https://www.latitude.sh/docs/networking/firewall). It is required on every server assigned to a firewall: the agent periodically fetches the rules you configure in the dashboard and applies them locally using UFW (Uncomplicated Firewall).

## How it works

1. The agent runs as a systemd service and polls the Latitude.sh API on a configurable interval (default: 30 seconds).
2. On each cycle it fetches the current rules for the firewall assigned to the server.
3. Rules are synchronized with UFW: missing rules are added and stale rules are removed, so the server always matches what is configured in the dashboard.

Notes:

- Only TCP and UDP rules are managed. ICMP traffic is permitted by default and cannot be customized.
- Docker manages its own iptables chains, which take precedence over UFW. Ports published by Docker containers may bypass firewall rules.

## Requirements

- A Linux distribution with systemd:
  - Debian/Ubuntu — UFW ships natively, or
  - RHEL family (Rocky Linux / AlmaLinux 9 and 10) — the installer enables EPEL to provide UFW and disables `firewalld` so UFW owns the firewall
- x86_64 (amd64) architecture — the install script downloads an amd64 Go toolchain
- Root access
- A firewall created in the [Latitude.sh dashboard](https://www.latitude.sh/dashboard) with the server added as an assignment

## Installation

The recommended way to install the agent is through the dashboard: open your firewall, go to the **Overview** tab, expand **Agent Installation**, and run the provided command on the server.

Alternatively, run the install script directly from this repository:

```bash
sudo ./install.sh -firewall <firewall_id> -project <project_id> [-public_ip <public_ip>]
```

The script installs the required dependencies, enables UFW with sane defaults (deny incoming, allow outgoing, allow SSH), builds the agent, and sets up the `lsh-agent` systemd service. On the RHEL family it also enables EPEL (which provides UFW) and disables `firewalld` so UFW owns the firewall.

> **Important:** make sure the server is added to the firewall in the Latitude.sh dashboard, otherwise the agent will have no rules to sync.

### Managing the service

```bash
sudo systemctl status lsh-agent    # check status
sudo journalctl -u lsh-agent -f    # follow logs
sudo systemctl restart lsh-agent   # restart
```

## Configuration

The agent reads its configuration from `/etc/lsh-agent/config.yaml` (see [`configs/agent.yaml`](configs/agent.yaml) for a documented example). Values can be overridden with environment variables:

| Variable | Description | Default |
| --- | --- | --- |
| `PROJECT_ID` | Project ID from the Latitude.sh dashboard (required) | — |
| `FIREWALL_ID` | Firewall ID from the Latitude.sh dashboard (required) | — |
| `PUBLIC_IP` | Public IP of the server | auto-detected |
| `AGENT_INTERVAL` | Sync interval (e.g. `30s`, `1m`) | `30s` |
| `LOG_LEVEL` | Log level (`debug`, `info`, `warn`, `error`) | `info` |
| `UFW_BINARY` | Path to the UFW binary | `/usr/sbin/ufw` |
| `FIREWALL_ENABLED` | Enable/disable rule synchronization | `true` |

The installer also writes `/etc/lsh-agent/env` with `FIREWALL_ID`, `PROJECT_ID`, and `PUBLIC_IP`, which the agent loads automatically.

### Command-line flags

```bash
lsh-agent -config /etc/lsh-agent/config.yaml   # run with a specific config file
lsh-agent -check-config                        # validate configuration and exit
lsh-agent -version                             # print version and exit
```

## Building from source

Requires Go 1.23+.

```bash
make build         # build ./build/lsh-agent
make build-linux   # cross-compile for linux/amd64
make test          # run tests
make help          # list all targets
```

See [TESTING.md](TESTING.md) for detailed testing instructions.

## Uninstalling

```bash
sudo ./uninstall.sh
```

This stops and removes the service, the binary, and the agent files. The script reads `/etc/lsh-agent/env` (created by the installer) and exits if the file is missing — after a partial installation, remove the service and files manually.

> **Warning:** the uninstall script also resets all UFW rules and disables UFW, leaving the server without a local firewall. Remember to remove the server from the firewall in the dashboard as well.

## License

See [LICENSE](LICENSE).

# Latitude.sh Agent

The Latitude.sh Agent (`lsh-agent`) is a lightweight daemon that keeps your server's firewall in sync with the [Latitude.sh Firewall](https://www.latitude.sh/docs/networking/firewall). It is required on every server assigned to a firewall: the agent periodically fetches the rules you configure in the dashboard and applies them locally using UFW (Uncomplicated Firewall).

## How it works

1. The agent runs as a systemd service and polls the Latitude.sh API on a configurable interval (default: 30 seconds).
2. On each cycle it fetches the current rules for the firewall assigned to the server.
3. Before applying anything, it verifies that the firewall returned by the API matches the `firewall_id` (and `project_id`) it was installed with. If the API returns a different firewall — for example a stale assignment left over after the server was released to the pool — the agent refuses to apply it, leaving the existing firewall untouched, rather than enforcing another tenant's rules.
4. Rules are synchronized with UFW: missing rules are added and stale rules are removed, so the server always matches what is configured in the dashboard.

Notes:

- Only TCP and UDP rules are managed. ICMP traffic is permitted by default and cannot be customized.
- Docker manages its own iptables chains, which take precedence over UFW. Ports published by Docker containers may bypass firewall rules.

## Requirements

- A Linux distribution with systemd:
  - Debian/Ubuntu — UFW ships natively, or
  - RHEL family (Rocky Linux / AlmaLinux 9 and 10) — the installer enables EPEL to provide UFW and disables `firewalld` so UFW owns the firewall
- x86_64 (amd64) architecture — the install script downloads an amd64 Go toolchain (the [apt package](#installing-with-apt-debianubuntu) also supports arm64)
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

### Installing with apt (Debian/Ubuntu)

The agent is also published as a `.deb` package in a signed apt repository, for Ubuntu 22.04+ and Debian 12+ on amd64 and arm64:

```bash
sudo install -d -m 0755 /etc/apt/keyrings
curl -fsSL https://packages.lsh.io/apt/lsh-agent.asc | sudo tee /etc/apt/keyrings/lsh-agent.asc > /dev/null
echo "deb [signed-by=/etc/apt/keyrings/lsh-agent.asc] https://packages.lsh.io/apt stable main" | sudo tee /etc/apt/sources.list.d/lsh-agent.list
sudo apt-get update
sudo apt-get install -y lsh-agent             # or lsh-agent=<version> to pin one
```

The package installs the binary, the `lsh-agent` systemd unit and the default configuration, and restarts the agent on every upgrade. It does not configure the host: the service stays inactive until `/etc/lsh-agent/env` exists, and UFW is left as it is. To finish the setup:

```bash
# Bind the agent to your firewall and project
sudo tee /etc/lsh-agent/env > /dev/null << 'EOF'
FIREWALL_ID=<firewall_id>
PROJECT_ID=<project_id>
EOF

# Enable UFW with the same defaults as install.sh (skip if UFW is already active)
sudo ufw default deny incoming
sudo ufw default allow outgoing
sudo ufw allow ssh
sudo ufw --force enable

sudo systemctl restart lsh-agent
```

Hosts previously set up with `install.sh` are migrated when the package is installed: the service switches to the packaged binary, and `/usr/local/bin/lsh-agent` becomes a link to it.

> **Note:** on Debian 12+ and Ubuntu 24.04+, installing UFW removes `iptables-persistent`/`netfilter-persistent`. If the host relies on them to restore `/etc/iptables` rules at boot, use `install.sh` instead, which keeps those rules restored.

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
make package       # build the .deb into ./dist (snapshot)
make help          # list all targets
```

See [TESTING.md](TESTING.md) for detailed testing instructions.

## Releasing

Every merge into `main` updates a draft of the next release ([Release Drafter](https://github.com/release-drafter/release-drafter)): merged PRs grouped by type, and the next version resolved from their labels. The labels come from the conventional-commit PR titles: `feat` bumps the minor, `!` (e.g. `feat!:`) the major, and everything else the patch.

To release, open the draft under **Releases**, review it and publish it. Publishing creates the `vX.Y.Z` tag, and the `release` workflow then builds the binaries and `.deb` packages with GoReleaser, attaches them to the release, and redeploys the apt repository at `https://packages.lsh.io/apt` with the packages of every published release. Pre-release tags (e.g. `v1.2.0-rc.1`) get artifacts but are not added to the repository.

The workflow needs:

- GitHub Pages deploying from GitHub Actions, with the custom domain `packages.lsh.io`;
- a `v*` tag rule in the `github-pages` environment, so tag builds can deploy;
- the `APT_SIGNING_KEY` secret, the ASCII-armored private key that signs the repository (without a passphrase), stored as a secret of the `github-pages` environment rather than of the repository, so only `main` and `v*` tags can read it;
- a tag ruleset restricting who can create `v*` tags, since pushing one publishes a release.

To redeploy the repository without a release (e.g. after rotating the key), run the `release` workflow manually from `main`.

## Uninstalling

```bash
sudo ./uninstall.sh
```

This stops and removes the service, the binary, and the agent files. The script reads `/etc/lsh-agent/env` (created by the installer) and exits if the file is missing — after a partial installation, remove the service and files manually.

> **Warning:** the uninstall script also resets all UFW rules and disables UFW, leaving the server without a local firewall. Remember to remove the server from the firewall in the dashboard as well.

If the agent was installed with apt, remove it with `sudo apt-get purge lsh-agent` instead. This stops the service and removes the agent and its configuration, but leaves UFW and its current rules in place.

## License

See [LICENSE](LICENSE).

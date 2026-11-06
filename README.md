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
  - RHEL family (Rocky Linux / AlmaLinux / Oracle Linux 9 and 10) — the installer enables EPEL to provide UFW when it isn't installed yet (`oracle-epel-release-el<N>` on Oracle Linux) and disables `firewalld` so UFW owns the firewall
- amd64 or arm64 on Debian/Ubuntu; amd64 only on the RHEL family, where the installer builds the agent from source with an amd64 Go toolchain
- Root access
- A firewall created in the [Latitude.sh dashboard](https://www.latitude.sh/dashboard) with the server added as an assignment

## Installation

The recommended way to install the agent is through the dashboard: open your firewall, go to the **Overview** tab, expand **Agent Installation**, and run the provided command on the server.

Alternatively, run the installer yourself:

```bash
curl -fsSL https://packages.lsh.io/install.sh | sudo bash -s -- -firewall <firewall_id> -project <project_id> [-public_ip <public_ip>] [-version <version>]
```

(or `sudo ./install.sh ...` from a clone of this repository). `-version` pins a release; the latest is installed otherwise.

The script installs the required dependencies and enables UFW with sane defaults (deny incoming, allow outgoing, allow SSH). On Debian/Ubuntu it then installs the `lsh-agent` package from the [apt repository](#installing-with-apt-debianubuntu); on the RHEL family it enables EPEL (which provides UFW) unless UFW is already installed, disables `firewalld` so UFW owns the firewall, and builds the agent from source. Either way it writes `/etc/lsh-agent/env` and starts the `lsh-agent` systemd service, checking that it stays up.

> **Important:** make sure the server is added to the firewall in the Latitude.sh dashboard, otherwise the agent will have no rules to sync.

### Installing from the package repositories

The agent is also published as `.deb` and `.rpm` packages in signed apt and dnf repositories at `packages.lsh.io` (same signing key), on amd64 and arm64.

Debian and Ubuntu (Ubuntu 22.04+, Debian 12+):

```bash
# One file sets the repository up, signing key included
sudo curl -fsSLo /etc/apt/sources.list.d/lsh-agent.sources https://packages.lsh.io/apt/lsh-agent.sources
sudo apt-get update
sudo apt-get install -y lsh-agent             # or lsh-agent=<version> to pin one
```

RHEL family (AlmaLinux, Rocky Linux, Oracle Linux 9+; ufw comes from EPEL):

```bash
sudo dnf install -y epel-release              # Oracle Linux: oracle-epel-release-el9 / -el10
sudo curl -fsSLo /etc/yum.repos.d/lsh-agent.repo https://packages.lsh.io/rpm/lsh-agent.repo
sudo dnf install -y ufw lsh-agent             # or lsh-agent-<version> to pin one
```

Install `ufw` by name, as above: dnf removes packages that came in only as a dependency, so `dnf remove lsh-agent` would otherwise remove the firewall too. The dnf repository verifies the signed metadata (`repo_gpgcheck=1`), which pins the checksum of every package, the same model as the apt repository.

Both packages install the binary, the `lsh-agent` systemd unit and the default configuration, and restart the agent on every upgrade. They do not configure the host: the service stays inactive until `/etc/lsh-agent/env` exists, and UFW is left as it is. To finish the setup:

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

On the RHEL family, hand the firewall over from `firewalld` to UFW instead of the plain `ufw --force enable` above, as `install.sh` does: `firewalld` and UFW would both drive netfilter, and EPEL's `ufw enable` does not enable `ufw.service`, so the rules would not come back after a reboot.

```bash
sudo systemctl disable --now firewalld
# If this fails, bring firewalld back before going further: sudo systemctl enable --now firewalld
sudo ufw --force enable
sudo systemctl enable ufw
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

Requires Go 1.23+, and [syft](https://github.com/anchore/syft#installation) for `make package` (it writes the SBOM next to the `.deb`).

```bash
make build         # build ./build/lsh-agent
make build-linux   # cross-compile for linux/amd64
make test          # run the Go tests
make test-shell    # Bats + ShellCheck for install.sh, uninstall.sh and the package scripts
make package       # build the .deb and its SBOM into ./dist (snapshot; needs syft)
make help          # list all targets
```

See [TESTING.md](TESTING.md) for detailed testing instructions.

## Releasing

Every merge into `main` updates a draft of the next release ([Release Drafter](https://github.com/release-drafter/release-drafter)): merged PRs grouped by type, and the next version resolved from their labels. The labels come from the conventional-commit PR titles: `feat` bumps the minor, `!` (e.g. `feat!:`) the major, and everything else the patch.

To release, open the draft under **Releases**, review it and publish it. Publishing creates the `vX.Y.Z` tag, and the `release` workflow then builds the binaries and the `.deb` and `.rpm` packages with GoReleaser, attaches them to the release, and redeploys the apt and dnf repositories at `https://packages.lsh.io/apt` and `https://packages.lsh.io/rpm` with the packages of every published release. Pre-release tags (e.g. `v1.2.0-rc.1`) get artifacts but are not added to the repository.

The workflow needs:

- GitHub Pages deploying from GitHub Actions, with the custom domain `packages.lsh.io`;
- a `v*` tag rule in the `github-pages` environment, so tag builds can deploy;
- the `APT_SIGNING_KEY` secret, the ASCII-armored private key that signs both repositories (without a passphrase), stored as a secret of the `github-pages` environment rather than of the repository, so only `main` and `v*` tags can read it;
- a tag ruleset restricting who can create `v*` tags, since pushing one publishes a release.

To redeploy the repository without a release (e.g. after rotating the key), run the `release` workflow manually from `main`.

### Verifying a release

Each `.deb` and `.rpm` ships with an SPDX SBOM (`<package file>.sbom.json`) and a signed build provenance attestation, both produced by the `release` workflow. To check that a package was built from this repository by that workflow:

```bash
gh attestation verify lsh-agent_<version>_amd64.deb -R latitudesh/agent
```

A weekly workflow (`scheduled-scan`) runs govulncheck on `main` and on the binaries of the latest release, and keeps one tracking issue open while it finds something.

## Uninstalling

```bash
curl -fsSL https://packages.lsh.io/uninstall.sh | sudo bash    # or: sudo ./uninstall.sh
```

This stops and removes the service, the binary, and the agent files; on Debian/Ubuntu it also purges the `lsh-agent` package and removes the apt repository the installer added. The script reads `/etc/lsh-agent/env` (created by the installer) and exits if the file is missing — after a partial installation, remove the service and files manually.

> **Warning:** the uninstall script also resets all UFW rules and disables UFW, leaving the server without a local firewall. Remember to remove the server from the firewall in the dashboard as well.

To remove only the agent and keep UFW and its current rules in place, run `sudo apt-get purge lsh-agent` instead.

## License

See [LICENSE](LICENSE).

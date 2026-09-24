#!/bin/bash
set -e

# Function to display usage
usage() {
    echo "Usage: $0 -firewall <firewall_id> -project <project_id> [-extra_parameters <extra_parameters>] [-public_ip <public_ip>] [-version <version>]"
    exit 1
}

# Parse command line arguments
while [[ $# -gt 0 ]]; do
    key="$1"
    case $key in
        -firewall)
        FIREWALL_ID="$2"
        shift # past argument
        shift # past value
        ;;
        -project)
        PROJECT_ID="$2"
        shift # past argument
        shift # past value
        ;;
        -extra_parameters)
        # Accepted for compatibility with existing install commands; unused.
        # shellcheck disable=SC2034
        EXTRA_PARAMETERS="$2"
        shift # past argument
        shift # past value
        ;;
        -public_ip)
        PUBLIC_IP="$2"
        shift # past argument
        shift # past value
        ;;
        -version)
        # Pin the agent version (e.g. 1.1.0 or v1.1.0); latest when omitted.
        AGENT_VERSION="${2#v}"
        shift # past argument
        shift # past value
        ;;
        *)
        usage
        ;;
    esac
done

# Check if firewall ID and project ID are provided
if [ -z "$FIREWALL_ID" ] || [ -z "$PROJECT_ID" ]; then
    echo "Error: Both Firewall ID and Project ID are required."
    usage
fi

# Check if running as root
if [ "$EUID" -ne 0 ]; then
    echo "Please run as root"
    exit 1
fi

# Detect the OS package family so the installer works on both Debian/Ubuntu
# (apt, the lsh-agent package from the Latitude.sh apt repository, native UFW)
# and the RHEL family (dnf/yum, a source build with gcc/make, UFW from EPEL).
if command -v apt-get &> /dev/null; then
    OS_FAMILY="debian"
elif command -v dnf &> /dev/null; then
    OS_FAMILY="rhel"; RPM_PM="dnf"
elif command -v yum &> /dev/null; then
    OS_FAMILY="rhel"; RPM_PM="yum"
else
    echo "Unsupported OS: need apt-get (Debian/Ubuntu) or dnf/yum (RHEL family)."
    exit 1
fi

# Where Debian/Ubuntu get the lsh-agent package from. Overridable for testing
# against a local repository or for a mirror.
APT_REPO_URL="${LSH_AGENT_APT_REPO:-https://packages.lsh.io/apt}"

# Network retries, the same policy as tinkerbell-packer-images: a transient
# mirror/CDN error (e.g. a distro mirror mid-sync) must not fail an install.
# Passed per call, so nothing is left behind in the host's apt/dnf config.
APT_RETRY_OPTS=(-o Acquire::Retries=5 -o Acquire::Retries::Delay=true -o Acquire::http::Timeout=30 -o Acquire::https::Timeout=30)
DNF_RETRY_OPTS=(--setopt=retries=10 --setopt=timeout=30 --setopt=minrate=1000)
CURL_RETRY_OPTS=(--retry 5 --retry-delay 3 --retry-connrefused)

# Function to install one or more packages
install_package() {
    if [ "$OS_FAMILY" = "debian" ]; then
        apt-get "${APT_RETRY_OPTS[@]}" update && apt-get "${APT_RETRY_OPTS[@]}" install -y "$@"
    else
        "$RPM_PM" "${DNF_RETRY_OPTS[@]}" install -y "$@"
    fi
}

# On the RHEL family, UFW ships in EPEL — enable it before the package loop.
# Skip it when ufw is already installed (every Latitude.sh EL image ships it):
# EPEL is only needed to get ufw. Oracle Linux has no "epel-release" package;
# its EPEL repo comes from oracle-epel-release-el<major>. OL9's happens to
# Provide epel-release, OL10's does not, so installing "epel-release" fails there.
#
# Both functions are covered by tests/install-epel.bats.
OS_RELEASE_FILE=/etc/os-release

# Print the EPEL release package for this host. os-release is read in a
# subshell so its NAME/VERSION/ID don't leak into the installer.
epel_package() {
    local os_id
    # shellcheck source=/dev/null
    os_id="$( . "$OS_RELEASE_FILE" 2> /dev/null && echo "${ID:-}" )"
    if [ "$os_id" = "ol" ]; then
        # shellcheck source=/dev/null
        echo "oracle-epel-release-el$( . "$OS_RELEASE_FILE" && echo "${VERSION_ID%%.*}" )"
    else
        echo "epel-release"
    fi
}

enable_epel_if_needed() {
    local epel_pkg
    [ "$OS_FAMILY" = "rhel" ] || return 0
    command -v ufw &> /dev/null && return 0
    epel_pkg="$(epel_package)"
    rpm -q "$epel_pkg" &> /dev/null && return 0
    echo "Enabling EPEL ($epel_pkg, provides ufw on the RHEL family)..."
    "$RPM_PM" "${DNF_RETRY_OPTS[@]}" install -y "$epel_pkg" || { echo "Failed to enable EPEL; install $epel_pkg and re-run."; exit 1; }
}

enable_epel_if_needed

# On the Debian family, ufw >= 0.36.2 declares "Breaks: iptables-persistent,
# netfilter-persistent" (Debian 12/13 and Ubuntu 24.04+; Ubuntu 22.04 still
# carries 0.36.1 and is unaffected), so apt REMOVES those packages in order to
# install ufw — the two cannot coexist, and reinstalling them afterwards is not
# possible. Remember whether netfilter-persistent was there so the boot-time
# rule restore it provided can be handed over below.
netfilter_persistent_installed() {
    dpkg-query -W -f='${Status}' netfilter-persistent 2>/dev/null | grep -q "ok installed"
}

netfilter_persistent_was_installed=0
if [ "$OS_FAMILY" = "debian" ] && netfilter_persistent_installed; then
    netfilter_persistent_was_installed=1
fi

# Install required packages. git is only needed to build from source (RHEL family).
required_packages=(curl ufw jq)
if [ "$OS_FAMILY" = "rhel" ]; then
    required_packages+=(git)
fi
for pkg in "${required_packages[@]}"; do
    if ! command -v "$pkg" &> /dev/null; then
        echo "Installing $pkg..."
        install_package "$pkg" || exit 1
    fi
done

# If installing ufw did remove netfilter-persistent, take over its one job:
# restoring /etc/iptables/rules.v{4,6} at boot. On a Latitude host that matters —
# the deploy template writes the metadata DNAT (169.254.169.254 -> the metadata
# service) into /etc/iptables/rules.v4 and relies on netfilter-persistent to
# reload it every boot. The rule files survive the package removal; the restorer
# does not, so without this the agent would silently drop the metadata redirect
# on the next reboot.
#
# Before=ufw.service: --noflush still applies the file's chain policies, so a
# policy-only rules.v6 restored after ufw would reset its DROP policy to ACCEPT.
if [ "$netfilter_persistent_was_installed" = 1 ] && ! netfilter_persistent_installed; then
    if [ -f /etc/iptables/rules.v4 ] || [ -f /etc/iptables/rules.v6 ]; then
        echo "ufw replaced netfilter-persistent; preserving the /etc/iptables rules at boot..."
        cat > /etc/systemd/system/lsh-agent-netfilter-restore.service << 'EOF'
[Unit]
Description=Restore /etc/iptables rules (stands in for netfilter-persistent, which ufw replaces)
Documentation=https://github.com/latitudesh/agent
DefaultDependencies=no
Wants=network-pre.target systemd-modules-load.service local-fs.target
Before=network-pre.target shutdown.target ufw.service
After=systemd-modules-load.service local-fs.target
Conflicts=shutdown.target

[Service]
Type=oneshot
RemainAfterExit=yes
ExecStart=/bin/sh -c 'if [ -f /etc/iptables/rules.v4 ]; then iptables-restore --noflush /etc/iptables/rules.v4; fi'
ExecStart=/bin/sh -c 'if [ -f /etc/iptables/rules.v6 ]; then ip6tables-restore --noflush /etc/iptables/rules.v6; fi'

[Install]
WantedBy=multi-user.target
EOF
        systemctl daemon-reload
        systemctl enable lsh-agent-netfilter-restore.service
        # Deliberately not started: those rules are already live on this boot
        # (they were loaded before netfilter-persistent went away), and --noflush
        # would just append duplicates.
    else
        echo "Warning: netfilter-persistent was removed to install ufw, and no /etc/iptables rules were found to preserve." >&2
    fi
fi

# Install the C build toolchain (required by Go's cgo for the net package) on
# the RHEL family, the only one still building the agent from source.
if [ "$OS_FAMILY" = "rhel" ] && { ! command -v gcc &> /dev/null || ! command -v make &> /dev/null; }; then
    echo "Installing gcc/make (build toolchain)..."
    install_package gcc make || exit 1
fi

# UFW is the agent's firewall backend. On the RHEL family firewalld owns
# netfilter by default and would contend with UFW over the same hooks, so it has
# to go — but only once UFW is ready to take over, and with a restore path, so a
# failed switch never leaves the host without a firewall.
FIREWALLD_WAS_ENABLED=0
FIREWALLD_WAS_ACTIVE=0

restore_firewalld() {
    if [ "$FIREWALLD_WAS_ENABLED" = 1 ]; then
        systemctl enable firewalld &> /dev/null || true
    fi
    if [ "$FIREWALLD_WAS_ACTIVE" = 1 ]; then
        systemctl start firewalld &> /dev/null || true
    fi
    if [ "$FIREWALLD_WAS_ENABLED" = 1 ] || [ "$FIREWALLD_WAS_ACTIVE" = 1 ]; then
        echo "UFW setup failed: firewalld restored to its previous state." >&2
    fi
}

disable_firewalld() {
    [ "$OS_FAMILY" = "rhel" ] || return 0

    # Track both halves independently: a host can be enabled-but-stopped (so
    # firewalld would come back on the next boot and fight UFW) or
    # active-but-disabled.
    if systemctl is-enabled --quiet firewalld 2> /dev/null; then
        FIREWALLD_WAS_ENABLED=1
    fi
    if systemctl is-active --quiet firewalld 2> /dev/null; then
        FIREWALLD_WAS_ACTIVE=1
    fi
    if [ "$FIREWALLD_WAS_ENABLED" = 0 ] && [ "$FIREWALLD_WAS_ACTIVE" = 0 ]; then
        return 0
    fi

    echo "Disabling firewalld (conflicts with UFW)..."
    systemctl disable --now firewalld 2> /dev/null || true

    # Verify instead of trusting the exit status: continuing with firewalld
    # still running (or still enabled for the next boot) means two managers
    # writing netfilter rules.
    if systemctl is-active --quiet firewalld 2> /dev/null ||
        systemctl is-enabled --quiet firewalld 2> /dev/null; then
        echo "Error: could not disable firewalld; it would contend with UFW over netfilter." >&2
        echo "Disable it manually ('systemctl disable --now firewalld') and re-run this installer." >&2
        exit 1
    fi
}

# Enable UFW if it's not active
if ufw status | grep -q "Status: active"; then
    echo "Firewall is already active"
    # UFW is already in charge, so dropping firewalld here cannot leave the
    # host unprotected.
    disable_firewalld
else
    echo "Enabling Firewall..."

    # Seed the policy while UFW is still inactive: these only write /etc/ufw
    # config, nothing reaches netfilter yet, so a failure here still leaves the
    # host's current firewall untouched.
    ufw default deny incoming
    ufw default allow outgoing
    ufw allow ssh

    # Now hand netfilter over: firewalld out, UFW in. If enabling UFW fails,
    # the EXIT trap puts firewalld back.
    disable_firewalld
    trap restore_firewalld EXIT
    ufw --force enable
    trap - EXIT

    # EPEL's ufw ships a systemd unit that 'ufw enable' does not enable (it
    # only flips ENABLED= in /etc/ufw/ufw.conf). Without the unit the rules are
    # not reloaded at boot — and firewalld is no longer there to cover for it.
    if [ "$OS_FAMILY" = "rhel" ]; then
        systemctl enable ufw &> /dev/null || true
    fi

    echo "Firewall enabled and configured with default rules"
fi

# Create directory structure
mkdir -p /etc/lsh-agent

if [ "$OS_FAMILY" = "debian" ]; then
    # Install the prebuilt lsh-agent package from the Latitude.sh apt repository:
    # no Go toolchain, git clone or build on the host. The package ships the
    # binary (/usr/bin), the systemd unit and the default config, restarts the
    # agent on upgrades, and migrates a host set up by an older, source-building
    # version of this script.
    echo "Installing Latitude.sh Agent from ${APT_REPO_URL}..."

    # A single file configures the repository: the deb822 entry carries the
    # signing key inline, trusted for this repository only. Drop the one-line
    # .list a manual setup may have left, which apt would reject as a
    # conflicting Signed-By for the same source.
    rm -f /etc/apt/sources.list.d/lsh-agent.list
    curl -fsSL "${CURL_RETRY_OPTS[@]}" "${APT_REPO_URL}/lsh-agent.sources" -o /etc/apt/sources.list.d/lsh-agent.sources
    apt-get "${APT_RETRY_OPTS[@]}" update

    # An explicit -version is installed as asked, even when that is a downgrade.
    # confdef/confold keep a locally changed config.yaml instead of stopping at
    # dpkg's conffile prompt, which fails without a terminal (cloud-init).
    DEBIAN_FRONTEND=noninteractive apt-get "${APT_RETRY_OPTS[@]}" install -y --allow-downgrades \
        -o Dpkg::Options::=--force-confdef -o Dpkg::Options::=--force-confold \
        "lsh-agent${AGENT_VERSION:+=$AGENT_VERSION}"

    # This script used to install the agent to /usr/local/bin. Keep that path
    # working for anything that still calls it or checks for it (e.g. an
    # Ansible `creates:`).
    ln -sf /usr/bin/lsh-agent /usr/local/bin/lsh-agent
else
    # Go resolves GOPATH, GOMODCACHE and GOCACHE from $HOME. cloud-init (and any
    # systemd unit) runs this installer with HOME unset, which leaves GOPATH and
    # GOMODCACHE empty and GOCACHE "off", so the build below dies on
    #   go: module cache not found: neither GOMODCACHE nor GOPATH is set
    # That failure lands AFTER UFW has been switched to default-deny above, leaving
    # the host reachable only over SSH with no agent to write the project's firewall
    # rules into UFW.
    # Use a private, unpredictable workspace instead of a constant path. A fixed
    # /var/tmp/lsh-agent-build could be precreated by a local user (symlink or
    # ownership games against a root Go build) or clobbered by a second installer
    # running at the same time. mktemp -d gives us a fresh directory that is
    # root-owned, mode 0700, and randomly named, so neither of those can happen.
    GO_WORK_DIR="$(mktemp -d "${TMPDIR:-/var/tmp}/lsh-agent-build.XXXXXX")"
    export GOPATH="${GO_WORK_DIR}/gopath"
    export GOMODCACHE="${GO_WORK_DIR}/gopath/pkg/mod"
    export GOCACHE="${GO_WORK_DIR}/gocache"
    mkdir -p "$GOPATH" "$GOMODCACHE" "$GOCACHE"

    # set -e aborts the moment any download/clone/build/copy below fails — before
    # the explicit cleanup near the end ever runs. Remove the build workspace and
    # the source clone from an EXIT trap so a failed install leaves nothing behind
    # for the next attempt to silently reuse a partial workspace.
    cleanup_build() {
        rm -rf "$GO_WORK_DIR" /tmp/agent
    }
    trap cleanup_build EXIT

    # Install Go if not present
    if ! command -v go &>/dev/null; then
      GO_VERSION="1.23.4"
      GO_PACKAGE="go${GO_VERSION}.linux-amd64.tar.gz"

      echo "Installing Go..."
      cd /tmp
      curl -fsSL "${CURL_RETRY_OPTS[@]}" "https://golang.org/dl/${GO_PACKAGE}" -o go.tar.gz
      tar -C /usr/local -xzf go.tar.gz

      echo 'export PATH=$PATH:/usr/local/go/bin' >> /etc/profile
      export PATH=$PATH:/usr/local/go/bin

      rm go.tar.gz

      echo "Go $GO_VERSION installed successfully."
    else
      echo "Go is already installed: $(go version)"
    fi


    # Build and install Go agent from source
    echo "Building Latitude.sh Agent from source..."
    cd /tmp
    # -version builds that release tag instead of the tip of main. git has no
    # retry of its own, so give a transient GitHub error three tries.
    for attempt in 1 2 3; do
        rm -rf agent
        git clone ${AGENT_VERSION:+--branch "v$AGENT_VERSION"} https://github.com/latitudesh/agent.git && break
        if [ "$attempt" = 3 ]; then
            echo "Failed to clone https://github.com/latitudesh/agent.git"
            exit 1
        fi
        sleep 5
    done
    cd agent

    # Remove problematic SDK dependency temporarily
    sed -i '/latitudesh-go-sdk/d' go.mod

    # Build the agent
    export PATH=$PATH:/usr/local/go/bin
    /usr/local/go/bin/go mod tidy
    /usr/local/go/bin/go build -ldflags "-X main.Version=${AGENT_VERSION:-dev}" -o lsh-agent ./cmd/agent

    # Install binary and config. Writing straight onto /usr/local/bin/lsh-agent
    # fails with ETXTBSY ("Text file busy") whenever an agent is already running,
    # which makes every re-install and upgrade on a live host die here. rename(2)
    # has no such restriction: it swaps the directory entry while the running
    # process keeps its own inode, which the kernel frees on the restart below.
    # Stage the new binary in the same directory so the rename stays on one
    # filesystem, where it is atomic — no window with a half-written agent on disk.
    install -m 0755 lsh-agent /usr/local/bin/lsh-agent.new
    mv -f /usr/local/bin/lsh-agent.new /usr/local/bin/lsh-agent
    cp configs/agent.yaml /etc/lsh-agent/config.yaml

    # Cleanup. The EXIT trap already covers every failure path above; run it now on
    # the success path too and clear it so it does not fire again at script exit.
    cd /
    cleanup_build
    trap - EXIT

    # Create systemd service for Go agent
    cat > /etc/systemd/system/lsh-agent.service << 'EOF'
[Unit]
Description=Latitude.sh Agent
After=network.target
Wants=network.target

[Service]
Type=simple
ExecStart=/usr/local/bin/lsh-agent -config /etc/lsh-agent/config.yaml
Restart=always
RestartSec=10
User=root

[Install]
WantedBy=multi-user.target
EOF
fi

# Get public IP address if PUBLIC_IP was not provided
if [ -z "$PUBLIC_IP" ]; then
    PUBLIC_IP=$(hostname -I | awk '{print $1}')
fi

# Create environment file for Go agent (backward compatibility)
echo "FIREWALL_ID=$FIREWALL_ID" > /etc/lsh-agent/env
echo "PROJECT_ID=$PROJECT_ID" >> /etc/lsh-agent/env
echo "PUBLIC_IP=$PUBLIC_IP" >> /etc/lsh-agent/env

# Note: LATITUDESH_AUTH_TOKEN token will be set via systemctl edit command after installation

# Reload systemd, enable and (re)start the service. restart, not start: on a
# re-install `start` is a no-op against the already-running agent, which would
# ignore the env file just written and, on a source build, leave the old binary
# serving from the inode the rename above just detached.
systemctl daemon-reload
systemctl enable lsh-agent.service
systemctl restart lsh-agent.service

# Verify rather than trust the restart. The unit is Type=simple with
# Restart=always, so systemctl returns 0 the moment the fork succeeds —
# an agent that exits immediately still looks like a clean install. Since UFW is
# already default-deny by this point, "installed but not running" is the one
# outcome that must never be reported as success.
sleep 2
if ! systemctl is-active --quiet lsh-agent.service; then
    echo "Error: lsh-agent.service is not running after install." >&2
    echo "UFW is active with default rules only; this host will not receive the project's firewall rules." >&2
    systemctl status lsh-agent.service --no-pager --lines=20 >&2 || true
    exit 1
fi

echo "Installation completed successfully."
echo ""
echo "IMPORTANT: Make sure you added the server to the firewall in the Latitude.sh dashboard."
echo "The agent will start monitoring firewall rules automatically."

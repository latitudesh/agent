#!/bin/bash
set -e

# Function to display usage
usage() {
    echo "Usage: $0 -firewall <firewall_id> -project <project_id> [-extra_parameters <extra_parameters>] [-public_ip <public_ip>] [-version <version>]"
    exit 1
}

# valid_id <value>: a firewall or project ID as the dashboard issues them
# (e.g. fw_..., proj_...): letters, digits, '.', '_' and '-', 1-128 chars.
# These values end up in /etc/lsh-agent/env, so nothing else is allowed in.
valid_id() {
    [[ ${#1} -le 128 && "$1" =~ ^[A-Za-z0-9._-]+$ ]]
}

# valid_ipv4 <value>: an IPv4 address in dotted form, each octet 0-255.
valid_ipv4() {
    local octet
    [[ "$1" =~ ^([0-9]{1,3})\.([0-9]{1,3})\.([0-9]{1,3})\.([0-9]{1,3})$ ]] || return 1
    for octet in "${BASH_REMATCH[@]:1}"; do
        [ "$((10#$octet))" -le 255 ] || return 1
    done
}

# valid_ip <value>: an IPv4 address, or an IPv6 address: up to eight groups
# of 1-4 hex digits, at most one "::" standing for the missing groups, and
# optionally an IPv4 address as the last two groups (::ffff:192.0.2.1).
valid_ip() {
    local ip="$1" max=8 group count=0 groups
    valid_ipv4 "$ip" && return 0
    [[ "$ip" == *:* && ${#ip} -le 45 && "$ip" =~ ^[0-9A-Fa-f:.]+$ ]] || return 1
    [[ "$ip" == *:::* ]] && return 1
    local rest="${ip/::/}"
    [[ "$rest" == *::* ]] && return 1
    if [[ "$ip" == *.* ]]; then
        valid_ipv4 "${ip##*:}" || return 1
        # The IPv4 part fills two groups; count it as one placeholder.
        ip="${ip%:*}:0"
        max=7
    fi
    # A lone ':' may only start or end the address as part of "::".
    [[ "$ip" =~ ^:[^:] || "$ip" =~ [^:]:$ ]] && return 1
    IFS=: read -ra groups <<< "$ip"
    for group in "${groups[@]}"; do
        [ -z "$group" ] && continue
        [[ "$group" =~ ^[0-9A-Fa-f]{1,4}$ ]] || return 1
        count=$((count + 1))
    done
    if [[ "$ip" == *::* ]]; then
        [ "$count" -lt "$max" ]
    else
        [ "$count" -eq "$max" ]
    fi
}

# detect_public_ip: the first global-scope IPv4 that is not private, CGNAT or
# link-local. The API matches a ping against the server's public address, and
# `hostname -I` lists addresses in interface order, so on a host whose private
# VLAN interface comes first it would report an address the API never knows.
# Falls back to the first `hostname -I` address (a host behind NAT has no
# public address on any interface).
detect_public_ip() {
    local addr
    if command -v ip >/dev/null 2>&1; then
        while read -r addr; do
            addr="${addr%/*}"
            case "$addr" in
                10.*|127.*|169.254.*|192.168.*) continue ;;
                172.1[6-9].*|172.2[0-9].*|172.3[01].*) continue ;;
                100.6[4-9].*|100.[7-9][0-9].*|100.1[01][0-9].*|100.12[0-7].*) continue ;;
            esac
            if valid_ipv4 "$addr"; then
                echo "$addr"
                return 0
            fi
        done < <(ip -4 -o addr show scope global | awk '{print $4}')
    fi
    echo "Warning: no public IPv4 address on any interface; using the first address from hostname -I." >&2
    hostname -I | awk '{print $1}'
}

# write_env_file <path>: write the IDs as plain KEY=value lines. The file is
# data: uninstall.sh and the agent read it, nothing executes it. Written to a
# temporary file and renamed, so a reader never sees a partial file.
write_env_file() {
    (
        umask 022
        printf '%s=%s\n' \
            FIREWALL_ID "$FIREWALL_ID" \
            PROJECT_ID "$PROJECT_ID" \
            PUBLIC_IP "$PUBLIC_IP" > "$1.tmp"
    )
    mv -f "$1.tmp" "$1"
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

# Validate the IDs and the public IP before anything is written.
if ! valid_id "$FIREWALL_ID" || ! valid_id "$PROJECT_ID"; then
    echo "Error: Firewall ID and Project ID may only contain letters, digits, '.', '_' and '-'."
    exit 1
fi
if [ -n "$PUBLIC_IP" ] && ! valid_ip "$PUBLIC_IP"; then
    echo "Error: -public_ip must be an IPv4 or IPv6 address."
    exit 1
fi

# Check if running as root
if [ "$EUID" -ne 0 ]; then
    echo "Please run as root"
    exit 1
fi

# Detect the OS package family so the installer works on both Debian/Ubuntu
# (apt, the lsh-agent package from the Latitude.sh apt repository, native UFW)
# and the RHEL family (dnf/yum, the lsh-agent package from the Latitude.sh dnf
# repository, UFW from EPEL).
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
# The same for the RHEL family's dnf repository.
RPM_REPO_URL="${LSH_AGENT_RPM_REPO:-https://packages.lsh.io/rpm}"

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

# Install required packages. ufw is installed by name, never only as the
# agent's dependency: dnf removes dependency-only packages along with the
# package that pulled them, and removing the agent must not remove the firewall.
required_packages=(curl ufw jq)
for pkg in "${required_packages[@]}"; do
    if ! command -v "$pkg" &> /dev/null; then
        echo "Installing $pkg..."
        install_package "$pkg" || exit 1
    fi
done

# Oracle Linux on UEKR8 (OL10's default kernel) moved netfilter matches and
# targets UFW loads (xt_limit, xt_LOG, xt_multiport, xt_recent, ...) out of
# kernel-uek-modules into kernel-uek-modules-extra-netfilter, which nothing
# requires or recommends. Without it 'ufw enable' fails ("Extension limit
# revision 0 not supported") and the rules never load. Install it for the
# running kernel when those modules are missing; once installed by name,
# 'dnf upgrade' keeps it in step with new kernels. Hosts deployed from Latitude
# images built before tinkerbell-packer-images#444 lack it.
#
# Covered by tests/install-uek-netfilter.bats.
ensure_uek_netfilter_modules() {
    local os_id kver pkg
    [ "$OS_FAMILY" = "rhel" ] || return 0
    # shellcheck source=/dev/null
    os_id="$( . "$OS_RELEASE_FILE" 2> /dev/null && echo "${ID:-}" )"
    [ "$os_id" = "ol" ] || return 0
    kver="$(uname -r)"
    case "$kver" in *uek*) ;; *) return 0 ;; esac
    modinfo -k "$kver" xt_limit &> /dev/null && return 0

    pkg="kernel-uek-modules-extra-netfilter-${kver}"
    echo "Installing $pkg (netfilter modules UFW needs on UEK)..."
    if ! "$RPM_PM" "${DNF_RETRY_OPTS[@]}" install -y "$pkg"; then
        echo "Error: UFW cannot run on this kernel without the xt_limit/xt_LOG/xt_multiport modules." >&2
        echo "Install $pkg (or boot a kernel that has them) and re-run this installer." >&2
        exit 1
    fi
}

ensure_uek_netfilter_modules

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
    # The RHEL-family counterpart of the apt path above: the prebuilt lsh-agent
    # package from the Latitude.sh dnf repository, for amd64 and arm64. The
    # package ships the binary (/usr/bin), the systemd unit and the default
    # config, restarts the agent on upgrades, and migrates a host set up by an
    # older version of this script, which built the agent from source.
    echo "Installing Latitude.sh Agent from ${RPM_REPO_URL}..."
    curl -fsSL "${CURL_RETRY_OPTS[@]}" "${RPM_REPO_URL}/lsh-agent.repo" -o /etc/yum.repos.d/lsh-agent.repo
    # Like apt above: the newest version, or an explicit -version as asked,
    # upgrading or downgrading what is installed. A locally changed config.yaml
    # is kept (%config(noreplace)). -y also imports the repository signing key
    # named in lsh-agent.repo on first use.
    if ! "$RPM_PM" "${DNF_RETRY_OPTS[@]}" install -y "lsh-agent${AGENT_VERSION:+-$AGENT_VERSION}"; then
        if [ -n "$AGENT_VERSION" ]; then
            echo "Could not install lsh-agent $AGENT_VERSION from ${RPM_REPO_URL}: releases before the first .rpm package have none." >&2
        fi
        exit 1
    fi
    # Keep /usr/local/bin/lsh-agent working, as on Debian/Ubuntu.
    ln -sf /usr/bin/lsh-agent /usr/local/bin/lsh-agent
fi

# Get public IP address if PUBLIC_IP was not provided
if [ -z "$PUBLIC_IP" ]; then
    PUBLIC_IP=$(detect_public_ip)
    if [ -n "$PUBLIC_IP" ] && ! valid_ip "$PUBLIC_IP"; then
        echo "Warning: could not detect a valid public IP ($PUBLIC_IP); leaving PUBLIC_IP empty."
        PUBLIC_IP=""
    fi
fi

# Create environment file for Go agent (backward compatibility)
write_env_file /etc/lsh-agent/env

# Note: LATITUDESH_AUTH_TOKEN token will be set via systemctl edit command after installation

# Reload systemd, enable and (re)start the service. restart, not start: on a
# re-install `start` is a no-op against the already-running agent, which would
# ignore the env file just written.
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

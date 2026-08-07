#!/bin/bash
set -e

# Function to display usage
usage() {
    echo "Usage: $0 -firewall <firewall_id> -project <project_id> [-extra_parameters <extra_parameters>] [-public_ip <public_ip>]"
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
        EXTRA_PARAMETERS="$2"
        shift # past argument
        shift # past value
        ;;
        -public_ip)
        PUBLIC_IP="$2"
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
# (apt + build-essential + native UFW) and the RHEL family (dnf/yum + gcc/make
# + UFW from EPEL).
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

# Function to install one or more packages
install_package() {
    if [ "$OS_FAMILY" = "debian" ]; then
        apt-get update && apt-get install -y "$@"
    else
        "$RPM_PM" install -y "$@"
    fi
}

# On the RHEL family, UFW ships in EPEL — enable it before the package loop.
if [ "$OS_FAMILY" = "rhel" ] && ! rpm -q epel-release &> /dev/null; then
    echo "Enabling EPEL (provides ufw on the RHEL family)..."
    "$RPM_PM" install -y epel-release || { echo "Failed to enable EPEL; install epel-release and re-run."; exit 1; }
fi

# Install required packages
for pkg in curl ufw jq git; do
    if ! command -v $pkg &> /dev/null; then
        echo "Installing $pkg..."
        install_package $pkg || exit 1
    fi
done

# Install the C build toolchain (required by Go's cgo for the net package):
# build-essential on Debian/Ubuntu, gcc + make on the RHEL family.
if [ "$OS_FAMILY" = "debian" ]; then
    if ! dpkg -s build-essential &> /dev/null; then
        echo "Installing build-essential..."
        install_package build-essential || exit 1
    fi
elif ! command -v gcc &> /dev/null || ! command -v make &> /dev/null; then
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

# Install Go if not present
if ! command -v go &>/dev/null; then
  GO_VERSION="1.23.4"
  GO_PACKAGE="go${GO_VERSION}.linux-amd64.tar.gz"

  echo "Installing Go..."
  cd /tmp
  curl -L -s https://golang.org/dl/${GO_PACKAGE} -o go.tar.gz
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
rm -rf agent
git clone https://github.com/latitudesh/agent.git
cd agent

# Remove problematic SDK dependency temporarily
sed -i '/latitudesh-go-sdk/d' go.mod

# Build the agent
export PATH=$PATH:/usr/local/go/bin
/usr/local/go/bin/go mod tidy
/usr/local/go/bin/go build -o lsh-agent ./cmd/agent

# Install binary and config
cp lsh-agent /usr/local/bin/
chmod +x /usr/local/bin/lsh-agent
cp configs/agent.yaml /etc/lsh-agent/config.yaml

# Cleanup
cd /
rm -rf /tmp/agent

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

# Get public IP address if PUBLIC_IP was not provided
if [ -z "$PUBLIC_IP" ]; then
    PUBLIC_IP=$(hostname -I | awk '{print $1}')
fi

# Create environment file for Go agent (backward compatibility)
echo "FIREWALL_ID=$FIREWALL_ID" > /etc/lsh-agent/env
echo "PROJECT_ID=$PROJECT_ID" >> /etc/lsh-agent/env
echo "PUBLIC_IP=$PUBLIC_IP" >> /etc/lsh-agent/env

# Note: LATITUDESH_AUTH_TOKEN token will be set via systemctl edit command after installation

# Reload systemd, enable and start the service
systemctl daemon-reload
systemctl enable lsh-agent.service
systemctl start lsh-agent.service

echo "Installation completed successfully."
echo ""
echo "IMPORTANT: Make sure you added the server to the firewall in the Latitude.sh dashboard."
echo "The agent will start monitoring firewall rules automatically."

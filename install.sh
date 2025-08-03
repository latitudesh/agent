#!/bin/bash

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

# Function to install a package
install_package() {
    if command -v apt-get &> /dev/null; then
        apt-get update && apt-get install -y "$1"
    elif command -v yum &> /dev/null; then
        yum install -y "$1"
    else
        echo "Unable to install $1. Please install it manually."
        return 1
    fi
}

# Install required packages
for pkg in curl ufw jq git; do
    if ! command -v $pkg &> /dev/null; then
        echo "Installing $pkg..."
        install_package $pkg || exit 1
    fi
done

# Enable UFW if it's not active
if ! ufw status | grep -q "Status: active"; then
    echo "Enabling Firewall..."
    ufw --force enable
    ufw default deny incoming
    ufw default allow outgoing
    ufw allow ssh
    echo "Firewall enabled and configured with default rules"
else
    echo "Firewall is already active"
fi

# Create directory structure
mkdir -p /etc/lsh-agent

# Download and install Go agent binary
echo "Downloading Latitude.sh Agent binary..."
curl -L -s https://github.com/latitudesh/agent/releases/latest/download/lsh-agent-linux-amd64 -o /usr/local/bin/lsh-agent
chmod +x /usr/local/bin/lsh-agent

# Download and install configuration
curl -s https://raw.githubusercontent.com/latitudesh/agent/main/configs/agent.yaml -o /etc/lsh-agent/config.yaml

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

# Set LATITUDESH_BEARER token in service environment
echo "Environment=LATITUDESH_BEARER=" >> /etc/systemd/system/lsh-agent.service

# Reload systemd, enable and start the service
systemctl daemon-reload
systemctl enable lsh-agent.service
systemctl start lsh-agent.service

echo "Installation completed successfully."
echo "
IMPORTANT: Make sure you added the server to the firewall in the Latitude.sh dashboard.
"

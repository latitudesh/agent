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
mkdir -p /etc/lsh-agent/lib

# Download and install rules.sh
curl -s https://raw.githubusercontent.com/latitudesh/agent/main/rules.sh -o /etc/lsh-agent/rules.sh
chmod +x /etc/lsh-agent/rules.sh

# Download and install firewall_diff.sh
curl -s https://raw.githubusercontent.com/latitudesh/agent/main/lib/firewall_diff.sh -o /etc/lsh-agent/lib/firewall_diff.sh
chmod +x /etc/lsh-agent/lib/firewall_diff.sh

# Download and install rule-fetch.service
curl -s https://raw.githubusercontent.com/latitudesh/agent/main/rule-fetch.service -o /etc/systemd/system/rule-fetch.service

# Update the service file to use the new path
sed -i 's|ExecStart=/usr/local/bin/rules.sh|ExecStart=/etc/lsh-agent/rules.sh|' /etc/systemd/system/rule-fetch.service

# Add firewall and project ID to the environment file
echo "FIREWALL_ID=$FIREWALL_ID" > /etc/lsh-agent-env
echo "PROJECT_ID=$PROJECT_ID" >> /etc/lsh-agent-env

# Reload systemd, enable and start the service
systemctl daemon-reload
systemctl enable rule-fetch.service
systemctl start rule-fetch.service

echo "Installation completed successfully."
echo "
IMPORTANT: Make sure you added the server to the firewall in the Latitude.sh dashboard.
"

#!/bin/bash

# Function to display usage
usage() {
    echo "Usage: $0 -firewall <firewall_id> -project <project_id>"
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
    echo "Enabling UFW..."
    ufw --force enable
    ufw default deny incoming
    ufw default allow outgoing
    ufw allow ssh
    echo "UFW enabled and configured with default rules"
else
    echo "UFW is already active"
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

# Get hostname and IP address
HOSTNAME=$(hostname)
IP=$(hostname -I | awk '{print $1}')

# Send POST request
echo "Sending server information to Latitude.sh..."
RESPONSE=$(curl -s -w "\n%{http_code}" -X POST https://maxihost.retool.com/url/register_server \
     -H "Content-Type: application/json" \
     -d "{
         \"hostname\": \"$HOSTNAME\",
         \"ip\": \"$IP\",
         \"firewall\": \"$FIREWALL_ID\",
         \"project\": \"$PROJECT_ID\"
     }")

HTTP_STATUS=$(echo "$RESPONSE" | tail -n1)
RESPONSE_BODY=$(echo "$RESPONSE" | sed '$d')

if [ "$HTTP_STATUS" -eq 201 ]; then
    echo "Installation completed successfully. Server associated with Firewall $FIREWALL_ID and Project $PROJECT_ID"

    echo "
    IMPORTANT: Please approve this server in your Latitude.sh dashboard.
    Visit: https://latitude.sh/dashboard/$PROJECT_ID/networking/firewall/$FIREWALL_ID
    "
    SERVER_ID=$(echo "$RESPONSE_BODY" | jq -r '.server_id // empty')
    if [ -n "$SERVER_ID" ]; then
        echo "SERVER_ID=$SERVER_ID" >> /etc/lsh-agent-env
    else
        echo "Error: Could not extract server ID from the response."
    fi
else
    echo "Error sending server information to Latitude.sh. HTTP Status: $HTTP_STATUS"
fi
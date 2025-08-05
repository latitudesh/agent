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

# Install Telegraf if not present
if ! command -v telegraf &>/dev/null; then
    echo "Installing Telegraf..."
    
    # Add InfluxData repository and install Telegraf
    if command -v apt-get &> /dev/null; then
        # Ubuntu/Debian
        curl -s https://repos.influxdata.com/influxdata-archive_compat.key | gpg --dearmor > /etc/apt/trusted.gpg.d/influxdata-archive_compat.gpg
        echo 'deb [signed-by=/etc/apt/trusted.gpg.d/influxdata-archive_compat.gpg] https://repos.influxdata.com/debian stable main' > /etc/apt/sources.list.d/influxdata.list
        apt-get update && apt-get install -y telegraf
    elif command -v yum &> /dev/null; then
        # RHEL/CentOS
        cat > /etc/yum.repos.d/influxdata.repo << 'EOF'
[influxdata]
name = InfluxData Repository - Stable
baseurl = https://repos.influxdata.com/stable/\$basearch/main
enabled = 1
gpgcheck = 1
gpgkey = https://repos.influxdata.com/influxdata-archive_compat.key
EOF
        yum install -y telegraf
    else
        echo "Unable to install Telegraf. Please install it manually."
        exit 1
    fi
    
    echo "Telegraf installed successfully."
else
    echo "Telegraf is already installed: $(telegraf version)"
fi

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

# Install Go if not present
if ! command -v go &> /dev/null; then
    echo "Installing Go..."
    cd /tmp
    curl -L -s https://golang.org/dl/go1.22.0.linux-amd64.tar.gz -o go.tar.gz
    tar -C /usr/local -xzf go.tar.gz
    export PATH=$PATH:/usr/local/go/bin
    rm go.tar.gz
fi

# Build and install Go agent from source
echo "Building Latitude.sh Agent from source..."
cd /tmp
rm -rf agent
git clone -b feat/go https://github.com/latitudesh/agent.git
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

# Install Telegraf configuration
if [ -f "configs/telegraf.conf" ]; then
    cp configs/telegraf.conf /etc/lsh-agent/telegraf.conf
else
    echo "Warning: configs/telegraf.conf not found, creating basic configuration"
    cat > /etc/lsh-agent/telegraf.conf << 'EOF'
# Basic Telegraf Configuration
[agent]
  interval = "30s"
  
[[inputs.cpu]]
[[inputs.mem]]
[[inputs.disk]]
[[inputs.net]]

[[outputs.http]]
  url = "${LATITUDESH_METRICS_ENDPOINT}"
  method = "POST"
  timeout = "10s"
  [outputs.http.headers]
    Content-Type = "application/json"
    Authorization = "Bearer ${LATITUDESH_BEARER}"
    X-Project-ID = "${PROJECT_ID}"
    X-Firewall-ID = "${FIREWALL_ID}"
  data_format = "json"
  json_timestamp_format = "unix"
EOF
fi

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

# Create environment file for Telegraf
echo "PROJECT_ID=$PROJECT_ID" > /etc/lsh-agent/telegraf.env
echo "FIREWALL_ID=$FIREWALL_ID" >> /etc/lsh-agent/telegraf.env
echo "PUBLIC_IP=$PUBLIC_IP" >> /etc/lsh-agent/telegraf.env
echo "AGENT_VERSION=1.0.0" >> /etc/lsh-agent/telegraf.env
echo "LATITUDESH_METRICS_ENDPOINT=https://api.latitude.sh/metrics" >> /etc/lsh-agent/telegraf.env

# Set additional parameters if provided
if [ -n "$EXTRA_PARAMETERS" ]; then
    echo "$EXTRA_PARAMETERS" >> /etc/lsh-agent/telegraf.env
fi

# Note: LATITUDESH_BEARER token will be set via systemctl edit command after installation

# Create systemd service override for Telegraf
mkdir -p /etc/systemd/system/telegraf.service.d
cat > /etc/systemd/system/telegraf.service.d/override.conf << 'EOF'
[Service]
EnvironmentFile=/etc/lsh-agent/telegraf.env
ExecStart=
ExecStart=/usr/bin/telegraf --config /etc/lsh-agent/telegraf.conf
EOF

# Reload systemd, enable and start the services
systemctl daemon-reload
systemctl enable lsh-agent.service
systemctl enable telegraf.service
systemctl start lsh-agent.service
systemctl start telegraf.service

echo "Installation completed successfully."
echo ""
echo "IMPORTANT: Make sure you added the server to the firewall in the Latitude.sh dashboard."
echo "The agent will start monitoring firewall rules automatically."
echo ""
echo "Services status:"
echo "- Latitude.sh Agent: $(systemctl is-active lsh-agent.service)"
echo "- Telegraf Metrics: $(systemctl is-active telegraf.service)"
echo ""
echo "To set the bearer token for both services:"
echo "  systemctl edit lsh-agent.service"
echo "  systemctl edit telegraf.service"
echo "  Add: Environment=\"LATITUDESH_BEARER=your_token_here\""

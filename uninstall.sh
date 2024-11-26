#!/bin/bash

# Function to display colored text
print_colored() {
    local color=$1
    local text=$2
    case $color in
        "red") echo -e "\033[0;31m$text\033[0m" ;;
        "green") echo -e "\033[0;32m$text\033[0m" ;;
        "yellow") echo -e "\033[0;33m$text\033[0m" ;;
        *) echo "$text" ;;
    esac
}

# Check if /etc/lsh-agent/env exists and source it
if [ -f /etc/lsh-agent/env ]; then
    source /etc/lsh-agent/env
else
    print_colored "red" "Error: /etc/lsh-agent/env file not found."
    exit 1
fi

# Check if PROJECT_ID is set
if [ -z "$PROJECT_ID" ]; then
    print_colored "red" "Error: Project ID is required."
    exit 1
fi

# Check if running as root
if [ "$EUID" -ne 0 ]; then
    print_colored "red" "Please run as root"
    exit 1
fi

# Stop and disable the service
print_colored "yellow" "Stopping and disabling the service..."
systemctl stop rule-fetch.service
systemctl disable rule-fetch.service

# Remove service file
print_colored "yellow" "Removing files..."
rm -f /etc/systemd/system/rule-fetch.service
# Remove agent files
rm -rf /etc/lsh-agent

# Reload systemd
systemctl daemon-reload

# Flush UFW rules and disable firewall
print_colored "yellow" "Flushing Firewall rules and disabling Firewall..."
if command -v ufw &> /dev/null; then
    ufw --force reset
    ufw disable
    print_colored "green" "Firewall rules have been flushed and Firewall has been disabled."
else
    print_colored "red" "Firewall is not installed or not found in the system path."
fi

print_colored "green" "Uninstallation completed successfully."
print_colored "yellow" "Note: If you want to remove other dependencies (curl, jq, git), please do so manually."

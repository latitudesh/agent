#!/bin/bash

# Function to print colored text
print_colored() {
    local color=$1
    local text=$2
    case $color in
        "red") echo -e "\e[31m$text\e[0m" ;;
        "green") echo -e "\e[32m$text\e[0m" ;;
        "yellow") echo -e "\e[33m$text\e[0m" ;;
        *) echo "$text" ;;
    esac
}

# Global error handler
handle_error() {
    local error_message=$1
    print_colored "red" "Error: $error_message"
    exit 1
}

# Load environment variables
if [ -f /etc/lsh-agent/env ]; then
    source /etc/lsh-agent/env
else
    handle_error "Environment file not found. Please run install.sh first."
fi

PING_URL="https://api.latitude.sh/agent/ping"

# Check for extra_parameters
if [ -n "$EXTRA_PARAMETERS" ]; then
    PING_URL="${PING_URL}${EXTRA_PARAMETERS}"
fi

# Import firewall_diff.sh
if [ -f "/etc/lsh-agent/lib/firewall_diff.sh" ]; then
    source /etc/lsh-agent/lib/firewall_diff.sh
else
    handle_error "firewall_diff.sh not found in the lib directory."
fi

# Set the output file path
OUTPUT_FILE="/tmp/lsh_firewall.json"
TEMP_FILE="/tmp/lsh_firewall_temp.json"

# Perform the curl request and save the output to a temporary file
HTTP_STATUS=$(curl -s -w "%{http_code}" -X GET \
  --url "$PING_URL" \
  -H 'Content-Type: application/json' \
  -d "{\"ip_address\": \"$PUBLIC_IP\"}" \
  -o "$TEMP_FILE")

# Check if the curl request was successful (HTTP status 200)
if [ "$HTTP_STATUS" -eq 200 ]; then
    # If successful, move the temporary file to the actual output file
    mv "$TEMP_FILE" "$OUTPUT_FILE"
    echo "Last updated: $(date)" >> "$OUTPUT_FILE"

    # Parse JSON and extract firewall rules
    json_data=$(cat "$OUTPUT_FILE" | sed 's/Last updated.*$//')

    # Check if the JSON data is valid
    if ! jq empty <<< "$json_data" 2>/dev/null; then
        handle_error "Invalid JSON data received from the server."
    fi

    # Check if firewall is an empty array (disabled)
    if jq -e '.firewall | if type == "array" and length == 0 then true else false end' <<< "$json_data" > /dev/null; then
        print_colored "yellow" "Warning: Firewall is disabled or no rules exist for this server."
        exit 0
    fi

    # Check if firewall.rules is an array
    if ! jq -e '.firewall.rules | if type == "array" then true else false end' <<< "$json_data" > /dev/null; then
        handle_error "Unexpected JSON format. Expected an array of rules in firewall.rules."
    fi

    # Check if the array is empty
    if [ "$(jq '.firewall.rules | length' <<< "$json_data")" -eq 0 ]; then
        print_colored "yellow" "Warning: No firewall rules received from the server."
        exit 0
    fi

    # Process and display firewall rules
    echo "Firewall rules received from the server:"
    echo "$json_data" | jq -r '.firewall.rules[] | "From: \(.from // "any"), To: \(.to // "any"), Protocol: \(.protocol // "any"), Port: \(.port // "any")"'

    print_colored "green" "Firewall rules retrieved successfully."

    # Perform diff
    echo -e "\nPerforming diff between existing UFW rules and API rules:"
    firewall_diff "$json_data" sudo_ufw
else
    error_message=$(jq -r '.message // empty' "$TEMP_FILE")
    handle_error "Failed to retrieve firewall rules. HTTP Status: $HTTP_STATUS. Error: $error_message"
fi

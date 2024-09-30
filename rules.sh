#!/bin/bash

# Set the output file path
OUTPUT_FILE="/tmp/retool_data.json"
TEMP_FILE="/tmp/retool_data_temp.json"

# Perform the curl request and save the output to a temporary file
HTTP_STATUS=$(curl -s -w "%{http_code}" -X POST \
  --url "https://api.tryretool.com/v1/workflows/970a836a-d20a-4db8-967f-eee0f04df1bb/startTrigger" \
  -H 'Content-Type: application/json' \
  -H 'X-Workflow-Api-Key: retool_wk_56aee527a9d5429e9c904f3c97e81966' \
  -o "$TEMP_FILE")

# Function to apply a UFW rule
apply_ufw_rule() {
    local action=$1
    local from=$2
    local to=$3

    # Check if 'to' contains a protocol specification
    if [[ $to == *"/"* ]]; then
        local port=$(echo $to | cut -d'/' -f1)
        local proto=$(echo $to | cut -d'/' -f2)
        sudo ufw $action from $from to any proto $proto port $port
    else
        sudo ufw $action from $from to any port $to
    fi
}

# Function to generate a unique identifier for a rule
generate_rule_id() {
    echo "$1 $2 $3" | md5sum | cut -d' ' -f1
}

# Check if the curl request was successful (HTTP status 200)
if [ "$HTTP_STATUS" -eq 200 ]; then
    # If successful, move the temporary file to the actual output file
    mv "$TEMP_FILE" "$OUTPUT_FILE"
    echo "Last updated: $(date)" >> "$OUTPUT_FILE"

    # Parse JSON and apply UFW rules
    json_data=$(cat "$OUTPUT_FILE" | sed 's/Last updated.*$//')

    # Create an associative array to store new rules
    declare -A new_rules

    echo "$json_data" | jq -c '.[]' | while read -r rule; do
        to=$(echo "$rule" | jq -r '.to')
        action=$(echo "$rule" | jq -r '.action')
        from=$(echo "$rule" | jq -r '.from')

        # Generate a unique identifier for this rule
        rule_id=$(generate_rule_id "$action" "$from" "$to")
        new_rules["$rule_id"]=1

        # Convert action to lowercase for UFW
        action_lower=$(echo "$action" | tr '[:upper:]' '[:lower:]')

        # Apply the rule
        apply_ufw_rule $action_lower $from $to

        echo "Applied rule: $action_lower from $from to $to"
    done

    # Get existing UFW rules
    IFS=$'\n'
    existing_rules=($(sudo ufw status | grep -E '^(ALLOW|DENY)' | sed 's/^[ \t]*//;s/[ \t]*$//' | tr '[:upper:]' '[:lower:]'))

    # Check each existing rule
    for rule in "${existing_rules[@]}"; do
        action=$(echo "$rule" | awk '{print $1}')
        from=$(echo "$rule" | awk '{print $3}')
        to_port=$(echo "$rule" | awk '{print $5}')

        # Handle rules with protocol
        if [[ "$rule" == *"/"* ]]; then
            to_port=$(echo "$rule" | awk '{print $5"/"$4}')
        fi

        # Generate a unique identifier for this rule
        rule_id=$(generate_rule_id "$action" "$from" "$to_port")

        # If this rule is not in the new rules, delete it
        if [[ -z "${new_rules[$rule_id]}" ]]; then
            sudo ufw delete $rule
            echo "Deleted rule: $rule"
        fi
    done

    # Reload UFW to apply changes
    sudo ufw reload

    echo "Firewall rules updated successfully."
else
    echo "Failed to fetch data from Retool. HTTP Status: $HTTP_STATUS"
    # Remove the temporary file if the request failed
    rm -f "$TEMP_FILE"
fi
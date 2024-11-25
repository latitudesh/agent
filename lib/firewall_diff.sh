#!/bin/bash

# Function to normalize UFW rule
normalize_ufw_rule() {
    local rule="$1"
    local from proto port

    if [[ $rule =~ ^([0-9]+/[a-z]+) ]]; then
        port="${BASH_REMATCH[1]}"
        proto=$(echo "$port" | cut -d'/' -f2)
        port=$(echo "$port" | cut -d'/' -f1)
        from=$(echo "$rule" | awk '{print $NF}')
    else
        echo "Error: Unexpected rule format - $rule" >&2
        return 1
    fi

    [ "$from" == "Anywhere" ] && from="any"

    echo "From: $from, Protocol: $proto, Port: $port"
}

# Function to get existing UFW rules
get_ufw_rules() {
    sudo ufw status | grep ALLOW | grep -v '(v6)' | while read -r rule; do
        normalize_ufw_rule "$rule"
    done
}

# Function to get API rules
get_api_rules() {
    local json_data="$1"
    echo "$json_data" | jq -r '.firewall.rules[] | "From: \(.from // "any"), Protocol: \(.protocol // "any"), Port: \(.port // "any")"'
}

lowercase() {
    echo "$1" | tr '[:upper:]' '[:lower:]'
}

# Function to add rules to UFW
add_ufw_rules() {
    local rules="$1"
    echo "$rules" | while IFS= read -r rule; do
        if [[ $rule =~ From:\ (.*),\ Protocol:\ (.*),\ Port:\ (.*) ]]; then
            local from="${BASH_REMATCH[1]}"
            local proto=$(lowercase "${BASH_REMATCH[2]}")
            local port="${BASH_REMATCH[3]}"

            if sudo ufw status | grep -q "$port/$proto.*$from"; then
                echo "Rule already exists: $rule"
            else
                local ufw_command="sudo ufw allow proto $proto from $from to any port $port"
                echo "Adding rule: $ufw_command"
                eval "$ufw_command"
            fi
        else
            echo "Error: Invalid rule format - $rule" >&2
        fi
    done
}

# Function to remove rules from UFW
remove_ufw_rules() {
    local rules="$1"

    echo "$rules" | while IFS= read -r rule; do
        echo "Debug: Processing rule: $rule"
        if [[ $rule =~ From:\ (.*),\ Protocol:\ (.*),\ Port:\ (.*) ]]; then
            local from="${BASH_REMATCH[1]}"
            local proto=$(lowercase "${BASH_REMATCH[2]}")
            local port="${BASH_REMATCH[3]}"

            local ufw_command="sudo ufw delete allow from $from to any port $port proto $proto"

            echo "Executing: $ufw_command"
            eval "$ufw_command"
        else
            echo "Error: Invalid rule format - $rule" >&2
        fi
    done
}

# Main diff function
firewall_diff() {
    local json_data="$1"
    local ufw_rules
    local api_rules
    local changes_made=false

    echo "Current UFW rules:"
    ufw_rules=$(get_ufw_rules | sort -u)
    echo "$ufw_rules"

    echo -e "\nAPI rules:"
    api_rules=$(get_api_rules "$json_data" | sort -u)
    echo "$api_rules"

    echo -e "\nPerforming diff between existing UFW rules and API rules:"
    local rules_to_add
    local rules_to_remove
    rules_to_add=$(comm -13 <(echo "$ufw_rules") <(echo "$api_rules"))
    rules_to_remove=$(comm -23 <(echo "$ufw_rules") <(echo "$api_rules"))

    echo -e "\nRules to add:"
    echo "$rules_to_add"

    if [ -n "$rules_to_add" ]; then
        echo -e "\nAdding new rules to UFW:"
        add_ufw_rules "$rules_to_add"
        changes_made=true
    else
        echo "No new rules to add."
    fi

    echo -e "\nRules to remove:"
    echo "$rules_to_remove"

    if [ -n "$rules_to_remove" ]; then
        echo "Removing rules from UFW:"
        remove_ufw_rules "$rules_to_remove"
        changes_made=true
    else
        echo "No rules to remove."
    fi

    # Reload UFW only if changes were made
    if [ "$changes_made" = true ]; then
        echo -e "\nReloading Firewall to apply changes:"
        sudo ufw reload
    else
        echo -e "\nNo changes were made. Skipping Firewall reload."
    fi

    echo -e "\nFinal UFW status:"
    sudo ufw status numbered
}
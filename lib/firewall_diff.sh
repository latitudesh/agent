#!/bin/bash

# Function to normalize UFW rule
normalize_ufw_rule() {
    local rule="$1"
    local from to proto port

    if [[ $rule =~ ^([0-9]+/[a-z]+) ]]; then
        port="${BASH_REMATCH[1]}"
        proto=$(echo "$port" | cut -d'/' -f2)
        port=$(echo "$port" | cut -d'/' -f1)
        from=$(echo "$rule" | awk '{print $NF}')
    else
        echo "Error: Unexpected rule format - $rule" >&2
        return 1
    fi

    [ "$from" == "Anywhere" ] && from="*"

    echo "From: $from, To: *, Protocol: $proto, Port: $port"
}

# Function to get existing UFW rules
get_ufw_rules() {
    sudo ufw status | grep ALLOW | while read -r rule; do
        normalize_ufw_rule "$rule"
    done
}

# Function to get API rules
get_api_rules() {
    local json_data="$1"
    echo "$json_data" | jq -r '.firewall.rules[] | "From: \(.from // "*"), To: \(.to // "*"), Protocol: \(.protocol // "any"), Port: \(.port // "any")"'
}

# Function to add rules to UFW
add_ufw_rules() {
    local rules="$1"
    echo "$rules" | while IFS= read -r rule; do
        if [[ $rule =~ From:\ (.*),\ To:\ (.*),\ Protocol:\ (.*),\ Port:\ (.*) ]]; then
            local from="${BASH_REMATCH[1]}"
            local proto="${BASH_REMATCH[3]}"
            local port="${BASH_REMATCH[4]}"

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
        if [[ $rule =~ From:\ (.*),\ To:\ .*,\ Protocol:\ (.*),\ Port:\ (.*) ]]; then
            local from="${BASH_REMATCH[1]}"
            local proto="${BASH_REMATCH[2]}"
            local port="${BASH_REMATCH[3]}"

            # Construct the UFW delete command
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

    echo "Current UFW rules:"
    ufw_rules=$(get_ufw_rules)
    echo "$ufw_rules"

    echo -e "\nAPI rules:"
    api_rules=$(get_api_rules "$json_data")
    echo "$api_rules"

    echo -e "\nPerforming diff between existing UFW rules and API rules:"
    local rules_to_add
    local rules_to_remove
    rules_to_add=$(comm -13 <(echo "$ufw_rules" | sort) <(echo "$api_rules" | sort))
    rules_to_remove=$(comm -23 <(echo "$ufw_rules" | sort) <(echo "$api_rules" | sort))

    echo -e "\nRules to add:"
    echo "$rules_to_add"

    if [ -n "$rules_to_add" ]; then
        echo -e "\nAdding new rules to UFW:"
        add_ufw_rules "$rules_to_add"
    else
        echo "No new rules to add."
    fi

    echo -e "\nRules to remove:"
    echo "$rules_to_remove"

    if [ -n "$rules_to_remove" ]; then
        echo "Removing rules from UFW:"
        remove_ufw_rules "$rules_to_remove"
    else
        echo "No rules to remove."
    fi

    # Reload UFW to apply changes
    sudo ufw reload
    
    echo -e "\nFinal UFW status:"
    sudo ufw status numbered
}
#!/bin/bash

# Test script for Telegraf configuration
# Run this script to verify Telegraf can parse the configuration and collect metrics

set -e

CONFIG_FILE="${1:-/etc/lsh-agent/telegraf.conf}"
ENV_FILE="${2:-/etc/lsh-agent/telegraf.env}"

echo "Testing Telegraf configuration..."
echo "Config file: $CONFIG_FILE"
echo "Environment file: $ENV_FILE"
echo

# Check if Telegraf is installed
if ! command -v telegraf &>/dev/null; then
    echo "❌ Telegraf is not installed or not in PATH"
    exit 1
fi

echo "✅ Telegraf is installed: $(telegraf version)"

# Check if config file exists
if [ ! -f "$CONFIG_FILE" ]; then
    echo "❌ Configuration file not found: $CONFIG_FILE"
    exit 1
fi

echo "✅ Configuration file exists"

# Check if environment file exists
if [ ! -f "$ENV_FILE" ]; then
    echo "⚠️  Environment file not found: $ENV_FILE"
    echo "   This is expected during development"
else
    echo "✅ Environment file exists"
    echo "   Environment variables:"
    grep -v '^#' "$ENV_FILE" | grep -v '^$' | sed 's/^/     /'
fi

# Test configuration syntax
echo
echo "Testing configuration syntax..."
if source "$ENV_FILE" 2>/dev/null || true; then
    if telegraf --config "$CONFIG_FILE" --test --quiet &>/dev/null; then
        echo "✅ Configuration syntax is valid"
    else
        echo "❌ Configuration syntax error:"
        telegraf --config "$CONFIG_FILE" --test 2>&1 | head -10
        exit 1
    fi
else
    echo "⚠️  Could not source environment file, testing without environment variables"
    if telegraf --config "$CONFIG_FILE" --test --quiet &>/dev/null; then
        echo "✅ Configuration syntax is valid (without env vars)"
    else
        echo "❌ Configuration syntax error:"
        telegraf --config "$CONFIG_FILE" --test 2>&1 | head -10
        exit 1
    fi
fi

# Test input plugins (collect one sample)
echo
echo "Testing input plugins (collecting one sample)..."
if source "$ENV_FILE" 2>/dev/null || true; then
    if timeout 30s telegraf --config "$CONFIG_FILE" --once --quiet 2>/dev/null; then
        echo "✅ Input plugins are working"
    else
        echo "❌ Input plugin error:"
        timeout 30s telegraf --config "$CONFIG_FILE" --once 2>&1 | tail -10
        exit 1
    fi
else
    echo "⚠️  Skipping input plugin test (no environment file)"
fi

echo
echo "🎉 Telegraf configuration test completed successfully!"
echo
echo "To start Telegraf manually:"
echo "  sudo systemctl start telegraf"
echo
echo "To view Telegraf logs:"
echo "  sudo journalctl -u telegraf -f"
echo
echo "To test with live data (10 seconds):"
echo "  sudo telegraf --config $CONFIG_FILE --test --input-filter cpu,mem --output-filter http"
#!/usr/bin/env bash
set -euo pipefail

GATEWAY_IP="${1:?Usage: setup-keepalived.sh <gateway-ip> <primary|secondary>}"
ROLE="${2:?Usage: setup-keepalived.sh <gateway-ip> <primary|secondary>}"
SCRIPT_DIR="$(cd "$(dirname "$0")" && pwd)"

if [ "$ROLE" != "primary" ] && [ "$ROLE" != "secondary" ]; then
    echo "error: ROLE must be 'primary' or 'secondary', got '$ROLE'" >&2
    echo "Usage: setup-keepalived.sh <gateway-ip> <primary|secondary>" >&2
    exit 1
fi

echo "=== Setting up keepalived ($ROLE) on $GATEWAY_IP ==="

# Install keepalived
ssh "$GATEWAY_IP" "apt-get update && apt-get install -y keepalived"

# Copy config
if [ "$ROLE" = "primary" ]; then
    scp "$SCRIPT_DIR/keepalived-primary.conf" "$GATEWAY_IP":/etc/keepalived/keepalived.conf
else
    scp "$SCRIPT_DIR/keepalived-secondary.conf" "$GATEWAY_IP":/etc/keepalived/keepalived.conf
fi

# Enable and start
ssh "$GATEWAY_IP" << 'REMOTE'
systemctl enable keepalived
systemctl restart keepalived
REMOTE

echo "=== Keepalived setup complete ==="

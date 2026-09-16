#!/usr/bin/env bash
set -euo pipefail

GATEWAY_IP="${1:?Usage: setup-gateway.sh <gateway-ip>}"
SCRIPT_DIR="$(cd "$(dirname "$0")" && pwd)"

echo "=== Setting up gateway on: $GATEWAY_IP ==="

# Copy gateway binary
echo "[1/3] Copying gateway binary..."
scp "$SCRIPT_DIR/../cmd/gateway/gateway" "$GATEWAY_IP":/usr/local/bin/dvram-gateway

# Copy config
echo "[2/3] Copying config..."
ssh "$GATEWAY_IP" "mkdir -p /etc/dvram"
scp "$SCRIPT_DIR/../config/gateway.toml" "$GATEWAY_IP":/etc/dvram/gateway.toml

# Install gateway service
echo "[3/3] Installing systemd service..."
ssh "$GATEWAY_IP" << 'REMOTE'
cat > /etc/systemd/system/dvram-gateway.service << EOF
[Unit]
Description=Distributed VRAM Gateway
After=network-online.target
Wants=network-online.target

[Service]
Type=simple
ExecStart=/usr/local/bin/dvram-gateway --config /etc/dvram/gateway.toml
Restart=always
RestartSec=5

[Install]
WantedBy=multi-user.target
EOF

systemctl daemon-reload
systemctl enable dvram-gateway
systemctl start dvram-gateway
REMOTE

echo "=== Gateway setup complete on $GATEWAY_IP ==="

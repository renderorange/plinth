#!/usr/bin/env bash
set -euo pipefail

GATEWAY_IP="${1:?Usage: setup-gateway.sh <gateway-ip>}"
SCRIPT_DIR="$(cd "$(dirname "$0")" && pwd)"
REPO_ROOT="$(cd "$SCRIPT_DIR/.." && pwd)"

echo "=== Setting up gateway on: $GATEWAY_IP ==="

# Copy gateway binary
echo "[1/4] Copying gateway binary..."
if [ ! -x "$REPO_ROOT/gateway" ]; then
    echo "Building gateway..."
    (cd "$REPO_ROOT" && go build -o gateway ./cmd/gateway)
fi
scp "$REPO_ROOT/gateway" "$GATEWAY_IP":/usr/local/bin/dvram-gateway

# Copy config
echo "[2/4] Copying config..."
if [ ! -f "$REPO_ROOT/config/gateway.toml" ]; then
    echo "error: config/gateway.toml not found." >&2
    echo "Copy config/gateway.toml.example to config/gateway.toml and edit it first." >&2
    exit 1
fi
ssh "$GATEWAY_IP" "mkdir -p /etc/dvram"
scp "$REPO_ROOT/config/gateway.toml" "$GATEWAY_IP":/etc/dvram/gateway.toml

# Install gateway service (runs unprivileged as the dvram user)
echo "[3/4] Installing systemd service..."
ssh "$GATEWAY_IP" << 'REMOTE'
id -u dvram &>/dev/null || useradd -r -s /bin/false dvram
chown -R dvram:dvram /etc/dvram
cat > /etc/systemd/system/dvram-gateway.service << EOF
[Unit]
Description=Distributed VRAM Gateway
After=network-online.target
Wants=network-online.target

[Service]
Type=simple
User=dvram
Group=dvram
ExecStart=/usr/local/bin/dvram-gateway --config /etc/dvram/gateway.toml
Restart=always
RestartSec=5

[Install]
WantedBy=multi-user.target
EOF

systemctl daemon-reload
systemctl enable dvram-gateway
systemctl restart dvram-gateway
REMOTE

echo "[4/4] Verifying service..."
ssh "$GATEWAY_IP" "systemctl is-active dvram-gateway"

echo "=== Gateway setup complete on $GATEWAY_IP ==="

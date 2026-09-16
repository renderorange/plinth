#!/usr/bin/env bash
set -euo pipefail

NODE_IP="${1:?Usage: provision-node.sh <node-ip>}"
SCRIPT_DIR="$(cd "$(dirname "$0")" && pwd)"

echo "=== Provisioning node: $NODE_IP ==="

# Install NVIDIA drivers and CUDA
echo "[1/7] Installing NVIDIA drivers and CUDA..."
ssh "$NODE_IP" << 'REMOTE'
apt-get update
apt-get install -y nvidia-driver nvidia-cuda-toolkit
REMOTE

# Install Python and vLLM
echo "[2/7] Installing Python and vLLM..."
ssh "$NODE_IP" << 'REMOTE'
apt-get install -y python3 python3-venv python3-pip
mkdir -p /opt/vllm
python3 -m venv /opt/vllm/.venv
source /opt/vllm/.venv/bin/activate
pip install vllm
REMOTE

# Create vllm user
echo "[3/7] Creating vllm service user..."
ssh "$NODE_IP" << 'REMOTE'
id -u vllm &>/dev/null || useradd -r -s /bin/false vllm
chown -R vllm:vllm /opt/vllm
REMOTE

# Copy model weights
echo "[4/7] Copying model weights..."
ssh "$NODE_IP" "mkdir -p /opt/models"
rsync -avz --progress /opt/models/ "$NODE_IP":/opt/models/

# Install vLLM service
echo "[5/7] Installing vLLM systemd service..."
scp "$SCRIPT_DIR/vllm.service" "$NODE_IP":/etc/systemd/system/vllm.service
ssh "$NODE_IP" << 'REMOTE'
systemctl daemon-reload
systemctl enable vllm
systemctl start vllm
REMOTE

# Install GPU exporter
echo "[6/7] Installing GPU metrics exporter..."
scp "$(dirname "$0")/../cmd/gpu-exporter/gpu-exporter" "$NODE_IP":/usr/local/bin/gpu-exporter
scp "$SCRIPT_DIR/gpu-exporter.service" "$NODE_IP":/etc/systemd/system/gpu-exporter.service
ssh "$NODE_IP" << 'REMOTE'
id -u vram-exporter &>/dev/null || useradd -r -s /bin/false vram-exporter
systemctl daemon-reload
systemctl enable gpu-exporter
systemctl start gpu-exporter
REMOTE

# Verify
echo "[7/7] Verifying services..."
ssh "$NODE_IP" << 'REMOTE'
systemctl is-active vllm
systemctl is-active gpu-exporter
echo "Node provisioned successfully"
REMOTE

echo "=== Done provisioning $NODE_IP ==="

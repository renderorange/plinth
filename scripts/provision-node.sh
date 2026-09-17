#!/usr/bin/env bash
set -euo pipefail

NODE_IP="${1:?Usage: provision-node.sh <node-ip>}"
SCRIPT_DIR="$(cd "$(dirname "$0")" && pwd)"
REPO_ROOT="$(cd "$SCRIPT_DIR/.." && pwd)"

echo "=== Provisioning node: $NODE_IP ==="

# Install NVIDIA drivers and CUDA
echo "[1/7] Installing NVIDIA drivers and CUDA..."
ssh "$NODE_IP" << 'REMOTE'
export DEBIAN_FRONTEND=noninteractive
apt-get update
apt-get install -y nvidia-driver nvidia-cuda-toolkit
REMOTE

# Install Python and vLLM
echo "[2/7] Installing Python and vLLM..."
ssh "$NODE_IP" << 'REMOTE'
export DEBIAN_FRONTEND=noninteractive
apt-get install -y python3 python3-venv python3-pip
mkdir -p /opt/vllm
python3 -m venv /opt/vllm/.venv
. /opt/vllm/.venv/bin/activate
python3 -m pip install --upgrade pip
python3 -m pip install vllm
REMOTE

# Create vllm user (with GPU device access via the video group)
echo "[3/7] Creating vllm service user..."
ssh "$NODE_IP" << 'REMOTE'
groupadd -f video
id -u vllm &>/dev/null || useradd -r -s /bin/false vllm
usermod -aG video vllm
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
systemctl restart vllm
REMOTE

# Install GPU exporter
echo "[6/7] Installing GPU metrics exporter..."
if [ ! -x "$REPO_ROOT/gpu-exporter" ]; then
    echo "Building gpu-exporter..."
    (cd "$REPO_ROOT" && go build -o gpu-exporter ./cmd/gpu-exporter)
fi
scp "$REPO_ROOT/gpu-exporter" "$NODE_IP":/usr/local/bin/gpu-exporter
scp "$SCRIPT_DIR/gpu-exporter.service" "$NODE_IP":/etc/systemd/system/gpu-exporter.service
ssh "$NODE_IP" << 'REMOTE'
groupadd -f video
id -u vram-exporter &>/dev/null || useradd -r -s /bin/false vram-exporter
usermod -aG video vram-exporter
systemctl daemon-reload
systemctl enable gpu-exporter
systemctl restart gpu-exporter
REMOTE

# Verify
echo "[7/7] Verifying services..."
ssh "$NODE_IP" << 'REMOTE'
systemctl is-active vllm
systemctl is-active gpu-exporter
echo "Node provisioned successfully"
REMOTE

echo "=== Done provisioning $NODE_IP ==="

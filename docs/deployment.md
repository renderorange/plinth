# Deployment Guide

This document describes how to deploy plinth in a production environment.

## Prerequisites

- NVIDIA GPU drivers installed on GPU nodes
- CUDA toolkit installed on GPU nodes
- Go 1.22+ for building
- SSH access to all nodes
- rsync for model weight transfer
- Each node's SSH host key fingerprint (for `plinth-provision`): `ssh-keyscan -t ed25519 <node-ip>`

## Deployment Steps

### 1. Build Binaries

```bash
# Build all binaries (gateway, gpu-exporter, plinth-provision)
make all

# Or build individually
go build -o gateway ./cmd/gateway
go build -o gpu-exporter ./cmd/gpu-exporter
go build -o plinth-provision ./cmd/provision
```

### 2. Configure Gateway

```bash
cp config/gateway.toml.example config/gateway.toml
# Edit config/gateway.toml with your cluster settings
```

For `plinth-provision`, set `[provision]` in the same file — at minimum `ssh_host_key` with each node's fingerprint. See [configuration.md](configuration.md) for detailed config reference.

### 3. Provision GPU Nodes

Two complementary options:

**Option A — `plinth-provision` (package installs + model weights):**

```bash
# Download model weights to the controller first
./plinth-provision -config config/gateway.toml weights pull Qwen/Qwen2.5-7B-Instruct

# Provision a specific node (installs drivers, Python, vLLM, the vllm
# service user, and pushes the configured model weights to the node)
./plinth-provision -config config/gateway.toml provision gpu-node-1

# Or provision all nodes
./plinth-provision -config config/gateway.toml provision --all
```

**Option B — `scripts/provision-node.sh` (full node setup including services):**

```bash
./scripts/provision-node.sh <node-ip>
```

This script:
1. Installs NVIDIA drivers and CUDA
2. Installs Python and vLLM in a virtual environment
3. Creates a `vllm` service user
4. Copies model weights from `/opt/models/`
5. Installs and starts the vLLM systemd service
6. Installs and starts the gpu-exporter systemd service

`plinth provision` covers steps 1–3 and pushes weights via rsync but does not install systemd services; use the script for service setup or install the units from `scripts/` manually.

### 4. Set Up Gateway

```bash
./scripts/setup-gateway.sh <gateway-ip>
```

This script:
1. Copies the gateway binary to `/usr/local/bin/dvram-gateway`
2. Copies the config to `/etc/dvram/gateway.toml`
3. Installs and starts the dvram-gateway systemd service

### 5. (Optional) Set Up High Availability

For HA gateway with keepalived:

```bash
# On primary gateway
./scripts/setup-keepalived.sh <primary-ip> primary

# On secondary gateway
./scripts/setup-keepalived.sh <secondary-ip> secondary
```

Edit the keepalived configs to set your virtual IP and authentication.

## Systemd Services

### vllm.service

Runs vLLM inference server on GPU nodes.

```ini
[Unit]
Description=vLLM Inference Server
After=network-online.target
Wants=network-online.target

[Service]
Type=simple
User=vllm
Group=vllm
SupplementaryGroups=video
WorkingDirectory=/opt/vllm
# Replace the model path with the model copied to /opt/models by provision-node.sh.
ExecStart=/opt/vllm/.venv/bin/vllm serve /opt/models/Qwen2.5-32B-Instruct-Q4 --host 0.0.0.0 --port 8000
Restart=always
RestartSec=5
Environment="CUDA_VISIBLE_DEVICES=0"

[Install]
WantedBy=multi-user.target
```

### gpu-exporter.service

Runs GPU metrics exporter on GPU nodes.

```ini
[Unit]
Description=Distributed VRAM GPU Metrics Exporter
After=network-online.target
Wants=network-online.target

[Service]
Type=simple
User=vram-exporter
Group=vram-exporter
SupplementaryGroups=video
ExecStart=/usr/local/bin/gpu-exporter
Restart=always
RestartSec=5

[Install]
WantedBy=multi-user.target
```

### dvram-gateway.service

Runs the plinth gateway.

```ini
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
```

## Verification

After deployment, verify:

1. **Gateway health**: `curl http://<gateway-ip>:8000/health`
2. **Gateway metrics**: `curl http://<gateway-ip>:9090/metrics`
3. **Node health**: `curl http://<node-ip>:9100/health`
4. **Node metrics**: `curl http://<node-ip>:9100/metrics`
5. **Model list**: `curl http://<gateway-ip>:8000/v1/models`

## Troubleshooting

### Node shows as degraded/dead

- Check if vLLM is running: `systemctl status vllm`
- Check if gpu-exporter is running: `systemctl status gpu-exporter`
- Check node health endpoint: `curl http://<node-ip>:8000/health`
- Check gpu-exporter metrics: `curl http://<node-ip>:9100/metrics`

### Gateway not starting

- Check config file exists: `ls -la /etc/dvram/gateway.toml`
- Check config syntax: Review TOML syntax
- Check logs: `journalctl -u dvram-gateway -f`

### High latency

- Check node health states in gateway health endpoint
- Check GPU utilization in node metrics
- Consider adding more GPU nodes

## Upgrading

### Gateway

1. Build new binary: `go build -o gateway ./cmd/gateway`
2. Copy to gateway: `scp gateway <gateway-ip>:/usr/local/bin/dvram-gateway`
3. Restart service: `ssh <gateway-ip> "systemctl restart dvram-gateway"`

### gpu-exporter

1. Build new binary: `go build -o gpu-exporter ./cmd/gpu-exporter`
2. Copy to nodes: `scp gpu-exporter <node-ip>:/usr/local/bin/gpu-exporter`
3. Restart service: `ssh <node-ip> "systemctl restart gpu-exporter"`

### vLLM

1. Update model weights if needed
2. Restart service: `ssh <node-ip> "systemctl restart vllm"`
